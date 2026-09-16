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

// pushParsedStep is the library's own bookkeeping step: the push path writes it
// (handlePushRetry resets it to running on every deduplicated push) and no post-job ever
// reports it, so it can never mean claimant work is in flight and RunInFlight ignores it.
const pushParsedStep = "push_parsed"

// RunInFlight reports whether any step, or any component inside an aggregate step, is
// running or held (StepStatus.IsRunning). Components are checked as well as steps because an
// aggregate step's own status can already read failed while a sibling component is still
// building, and held counts because a held step's pre-job has already run and will not claim
// again. pushParsedStep is skipped: it is the library's own bookkeeping, reset to running by
// every deduplicated push and never completed by a post-job, so counting it would make a
// deduped claimed slip unreleasable. This is the one definition of "work in flight" the
// claim protects.
func RunInFlight(slip *Slip) bool {
	for name, step := range slip.Steps {
		if name == pushParsedStep {
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
// ("" when unclaimed). A non-empty expected is a compare-and-set on status: a mismatch is
// ErrClaimPreconditionFailed and the store writes nothing. Otherwise the claim is granted:
// write reports whether the store must record it (claimed_from plus a marker) or the slip is
// already claimed and this is the idempotent no-op arm. prior is the status to report — the
// current one on a fresh claim, the recorded one on a repeat. A slip with no status at all is
// refused outright: recording it would write claimed_from = "", which every reader — the
// repave guard, the push fast path, DecideRelease — treats as unclaimed.
func DecideClaim(status, claimedFrom SlipStatus, expected []SlipStatus) (prior SlipStatus, write bool, err error) {
	if status == "" {
		return "", false, fmt.Errorf("slip has no status: %w", ErrClaimPreconditionFailed)
	}
	if len(expected) > 0 && !slices.Contains(expected, status) {
		return "", false, fmt.Errorf("status %s not in %v: %w", status, expected, ErrClaimPreconditionFailed)
	}
	if claimedFrom != "" {
		return claimedFrom, false, nil
	}
	return status, true, nil
}

// DecideRelease is the release decision, shared the same way. nil means clear the claim.
// ErrNotClaimed when there is none; ErrRunInFlight when the claim is held and the run still
// has a step or component running or held — releasing then would expose that work to a
// same-commit repave, so the release is refused and the caller's later release, or the
// terminal status write, ends the claim instead.
func DecideRelease(slip *Slip) error {
	if slip.ClaimedFrom == "" {
		return ErrNotClaimed
	}
	if RunInFlight(slip) {
		return ErrRunInFlight
	}
	return nil
}

// ClaimSlip records that a run is in flight against a slip, so a same-commit push
// deduplicates onto it instead of repaving it (DEVOPS-285, DEVOPS-367). The claim is a flag:
// it never changes the slip's status, and it lives until the run is over — released by a
// post-job once nothing is in flight, or ended by a terminal status write. expected is a
// compare-and-set on the current status; nil admits any status. A repeat claim on a held
// claim is an idempotent no-op returning the recorded prior. See SlipStore.ClaimSlip.
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
// calls it on exit: while a sibling step or component is still running or held the release is
// refused with ErrRunInFlight and nothing is written, so the last post-job's release is the
// one that clears it. An unclaimed slip is ErrNotClaimed — the normal outcome after a terminal
// status write already ended the claim. The status is never changed; the returned value is
// the status at release. See SlipStore.ReleaseClaim.
func (c *Client) ReleaseClaim(
	ctx context.Context, correlationID, releasedBy, reason string,
) (SlipStatus, error) {
	ctx, span := StartSpan(ctx, "ReleaseClaim", correlationID)
	defer span.End()
	status, err := c.store.ReleaseClaim(ctx, correlationID, releasedBy, reason)
	if err != nil {
		return "", NewSlipError("release claim", correlationID, err)
	}
	c.logger.Info(ctx, "Released slip claim", map[string]interface{}{
		"correlation_id": correlationID,
		"status":         string(status),
		"released_by":    releasedBy,
	})
	return status, nil
}
