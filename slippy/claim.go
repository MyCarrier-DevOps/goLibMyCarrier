package slippy

import (
	"context"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"
)

// ClaimMarkerStep is the state_history step name of the adoption marker ClaimSlip appends.
// It is deliberately not a pipeline step name, so no aggregate or phase-duration reader can
// mistake it for a real step event. pushhookparser's stranded-slip cleanup keys on it.
const ClaimMarkerStep = "slip_claimed"

// ReleaseMarkerStep is the state_history step name of the marker ReleaseClaim appends.
// pushhookparser's stranded-slip cleanup treats a release as ending the claim's exemption.
const ReleaseMarkerStep = "slip_released"

// MaxMarkerReasonLen bounds the caller-supplied reason recorded in a claim or release
// marker. slippy-api enforces the same bound on its request bodies; this is the library's
// own defence, so a direct caller cannot grow state_history without limit.
const MaxMarkerReasonLen = 512

// clampReason truncates reason to MaxMarkerReasonLen runes, marking the cut.
func clampReason(reason string) string {
	if utf8.RuneCountInString(reason) <= MaxMarkerReasonLen {
		return reason
	}
	runes := []rune(reason)
	return string(runes[:MaxMarkerReasonLen-1]) + "…"
}

// ClaimMarker builds the adoption history entry every caller writes, so the marker's shape
// is defined once here rather than per client. prior is the slip's status when the claim was
// taken; it goes in the message because an operator reading the history needs to know what
// was adopted. claimedBy is the entry's actor and is audit only.
func ClaimMarker(prior SlipStatus, claimedBy, reason string) StateHistoryEntry {
	msg := fmt.Sprintf("adopted %s slip before dispatching", prior)
	if reason = clampReason(reason); reason != "" {
		msg += ": " + reason
	}
	return StateHistoryEntry{
		Step:      ClaimMarkerStep,
		Status:    StepStatusRunning,
		Timestamp: time.Now(),
		Actor:     claimedBy,
		Message:   msg,
	}
}

// ReleaseMarker builds the release history entry. status is the slip's status at release,
// which the release never changes; it is recorded because the claim's own record
// (claimed_from) is cleared by the same write. releasedBy is the entry's actor and is audit
// only. StepStatusCompleted because a release is the claim's normal end, not a failure; it is
// never read as a pipeline step because the step name is not one.
func ReleaseMarker(status SlipStatus, releasedBy, reason string) StateHistoryEntry {
	msg := fmt.Sprintf("released claim; slip is %s", status)
	if reason = clampReason(reason); reason != "" {
		msg += ": " + reason
	}
	return StateHistoryEntry{
		Step:      ReleaseMarkerStep,
		Status:    StepStatusCompleted,
		Timestamp: time.Now(),
		Actor:     releasedBy,
		Message:   msg,
	}
}

// PushParsedStep is the library's own bookkeeping step: the push path writes it
// (handlePushRetry resets it to running on every deduplicated push) and no post-job ever
// reports it, so it can never mean claimant work is in flight and RunInFlight ignores it.
//
// The exemption is by name, and that is deliberate rather than a shortcut. The shipped
// configs' step 0 is `builds`, an aggregate that build post-jobs report, so this is the one
// step no reporter owns — and the exemption mirrors the one by-name write this library makes,
// handlePushRetry's reset of the same step. A config whose step 0 were instead a
// non-aggregate step that nothing ever reports would hold the claim open the same way; such
// a slip also never completes, which initializeSlipForPush already documents.
const PushParsedStep = "push_parsed"

// RunInFlight reports whether any step, or any component inside an aggregate step, is
// running or held (StepStatus.IsRunning). Components are checked as well as steps because an
// aggregate step's own status can already read failed while a sibling component is still
// building.
//
// Held counts as in flight because a held step is work the run has already committed to: it
// is waiting on prerequisites and will proceed on its own. What holds nothing is a step never
// reported at all, which reads pending. Note that `held` needs no StartStep before it —
// HoldStep writes it directly, and WaitForPrerequisites writes it as its FIRST action — so
// "held implies started" is not a property to lean on; "held implies committed" is.
//
// The operational consequence: WaitForPrerequisites returns on its ctx.Done() arms without
// resolving the step, so a process killed mid-wait leaves the step `held` and the claim held
// with it, exactly as a killed `running` step does. The way out is the recovery route on
// ErrNotClaimed in errors.go — resolve the step, then release. Which helper a caller used
// decides what it sees: this library's WaitForPrerequisites records `held`, while the CLI's
// client-side poll of the read-only prerequisites endpoint records nothing and leaves the
// step `pending`.
//
// PushParsedStep is skipped: it is the library's own bookkeeping, reset to running by
// every deduplicated push and never completed by a post-job, so counting it would make a
// deduped claimed slip unreleasable. This is the one definition of "work in flight" the
// claim protects.
func RunInFlight(slip *Slip) bool {
	for name, step := range slip.Steps {
		if name == PushParsedStep {
			continue
		}
		if step.Status.IsRunning() {
			return true
		}
	}
	for _, components := range slip.Aggregates {
		for _, component := range components {
			if component.Status.IsRunning() {
				return true
			}
		}
	}
	return false
}

// DecideClaim is the claim decision, shared by PostgresStore and both test doubles so the
// three cannot drift. status is the row's current status and claimedFrom its recorded claim
// ("" when unclaimed). prior is the status to report — the current one on a fresh claim, the
// recorded one on a repeat — and write reports whether the store must record the claim
// (claimed_from plus a marker) or this is the idempotent no-op arm.
//
// A slip with no status at all is refused outright: recording it would write
// claimed_from = "", which every reader — the repave guard, the push fast path,
// DecideRelease — treats as unclaimed.
//
// A FRESH claim is a compare-and-set on the CURRENT status. A nil or empty expected admits
// any status EXCEPT an unclaimed in_progress: a row reading in_progress with no claim
// recorded is a live run nothing has adopted, and claiming one silently would let a rerun
// dispatch on top of a pipeline already in flight. A caller that does mean to claim a live
// run says so by listing in_progress in expected — the Slippy CLI pre-job claims out of
// every non-terminal status and does exactly that; pushhookparser's rerunner claims out of
// the ended set and is the caller this refusal protects.
//
// A REPEAT claim on a held claim is an idempotent no-op returning the RECORDED prior, and
// expected is checked against that recorded prior rather than the current status. A retry
// after a lost response therefore passes, because the prior it agreed to is the one on the
// row and the run may have moved the status since; a DIFFERENT claimant whose expected
// excludes the recorded prior is refused rather than handed a claim it did not agree to.
func DecideClaim(status, claimedFrom SlipStatus, expected []SlipStatus) (prior SlipStatus, write bool, err error) {
	if status == "" {
		return "", false, fmt.Errorf("slip has no status: %w", ErrClaimPreconditionFailed)
	}
	if claimedFrom == "" && status == SlipStatusInProgress && !slices.Contains(expected, SlipStatusInProgress) {
		return "", false, fmt.Errorf("in_progress with no claim recorded is a live run: %w",
			ErrClaimPreconditionFailed)
	}
	if claimedFrom != "" {
		if len(expected) > 0 && !slices.Contains(expected, claimedFrom) {
			return "", false, fmt.Errorf("already claimed out of %s, not in %v: %w",
				claimedFrom, expected, ErrClaimPreconditionFailed)
		}
		return claimedFrom, false, nil
	}
	if len(expected) > 0 && !slices.Contains(expected, status) {
		return "", false, fmt.Errorf("status %s not in %v: %w", status, expected, ErrClaimPreconditionFailed)
	}
	return status, true, nil
}

// ReleaseOutcome is what a release decided. Released reports whether the claim was ended —
// claimed_from cleared and a release marker appended — and Released=false means the claim is
// held because work is in flight and NOTHING was written. That is information, not a failure:
// every post-job of a run releases on exit, so all but the last take the false arm and the
// one that finds nothing in flight clears the claim.
//
// Status is the slip's status at decision time in BOTH cases — a release never changes it.
type ReleaseOutcome struct {
	// Released is true when this call cleared the claim, false when it was kept.
	Released bool

	// Status is the slip's status when the decision was made, whichever arm was taken.
	Status SlipStatus
}

// DecideRelease is the release decision, shared the same way. It answers only whether the
// store must clear the claim; the status the caller reports comes from the same read.
//
//   - ErrSlipNotFound when slip is nil, so a store that hands over nothing cannot be read as
//     "nothing to release".
//   - ErrNotClaimed when claimed_from is empty — the normal outcome once a terminal status
//     write already ended the claim.
//   - (false, nil) when the claim is held and the run still has a step or component running
//     or held: releasing then would expose that work to a same-commit repave, so nothing is
//     written and a later release, or the terminal status write, ends the claim instead.
//   - (true, nil) otherwise: the run is quiescent, clear the claim.
func DecideRelease(slip *Slip) (release bool, err error) {
	if slip == nil {
		return false, ErrSlipNotFound
	}
	if slip.ClaimedFrom == "" {
		return false, ErrNotClaimed
	}
	if RunInFlight(slip) {
		return false, nil
	}
	return true, nil
}

// ClaimSlip records that a run is in flight against a slip, so a same-commit push
// deduplicates onto it instead of repaving it (DEVOPS-285, DEVOPS-367). The claim is a flag:
// it never changes the slip's status, and it lives until the run is over — released by a
// post-job once nothing is in flight, or ended by a terminal status write. expected is a
// compare-and-set on the current status; nil admits any status except an unclaimed
// in_progress, which is a live run — a caller that means to claim one lists in_progress in
// expected. A repeat claim on a held claim is an idempotent no-op returning the recorded
// prior, with expected checked against that recorded prior. See SlipStore.ClaimSlip.
func (c *Client) ClaimSlip(
	ctx context.Context, correlationID string, expected []SlipStatus, claimedBy, reason string,
) (SlipStatus, error) {
	ctx, span := StartSpan(ctx, "ClaimSlip", correlationID)
	defer span.End()
	prior, err := c.store.ClaimSlip(ctx, correlationID, expected, claimedBy, reason)
	if err != nil {
		return "", NewSlipError("claim", correlationID, err)
	}
	c.logger.Info(ctx, "Claimed slip", map[string]interface{}{
		"correlation_id": correlationID,
		"prior_status":   string(prior),
		"claimed_by":     claimedBy,
	})
	return prior, nil
}

// ReleaseClaim ends a claim once the claimant's run has nothing left in flight. Every post-job
// calls it on exit, and must have written its own step's terminal status first: quiescence is
// judged from the row, so a post-job that releases before recording its step counts itself as
// in flight and no post-job of the run ever clears the claim. While a sibling step or
// component is still running or held the claim is KEPT — ReleaseOutcome{Released: false} with
// nothing written, not an error — so the last post-job's release is the one that clears it.
// An unclaimed slip is ErrNotClaimed, the normal outcome after a terminal status write
// already ended the claim. The status is never changed; ReleaseOutcome.Status is the status
// at decision time on either arm. See SlipStore.ReleaseClaim.
func (c *Client) ReleaseClaim(
	ctx context.Context, correlationID, releasedBy, reason string,
) (ReleaseOutcome, error) {
	ctx, span := StartSpan(ctx, "ReleaseClaim", correlationID)
	defer span.End()
	out, err := c.store.ReleaseClaim(ctx, correlationID, releasedBy, reason)
	if err != nil {
		return ReleaseOutcome{}, NewSlipError("release claim", correlationID, err)
	}
	msg := "Released slip claim"
	if !out.Released {
		msg = "Slip claim kept: work in flight"
	}
	c.logger.Info(ctx, msg, map[string]interface{}{
		"correlation_id": correlationID,
		"status":         string(out.Status),
		"released":       out.Released,
		"released_by":    releasedBy,
	})
	return out, nil
}

// ProbeSchema is the readiness gate consumers reach through the abstraction they hold: it
// checks the store's SELECT column list against the live schema and reports ErrSchemaBehind
// when the database is behind this library. A store with no schema of its own (ClickHouse)
// returns nil. See SlipStore.ProbeSchema.
func (c *Client) ProbeSchema(ctx context.Context) error {
	return c.store.ProbeSchema(ctx)
}
