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
// PushParsedStep is skipped in BOTH loops — the step map and the aggregate map, which is
// keyed by step name as well: it is the library's own bookkeeping, reset to running by every
// deduplicated push and never completed by a post-job, so counting it would make a deduped
// claimed slip unreleasable. This is the one definition of "work in flight" the claim
// protects.
func RunInFlight(slip *Slip) bool {
	// Nil is "no slip, so nothing in flight" rather than a panic: this is exported for
	// third-party stores to route their own release decision through, and a store that hands
	// over nothing must not take the process down. DecideRelease screens nil separately, with
	// ErrSlipNotFound, because for a RELEASE an absent row is an error rather than quiescence.
	if slip == nil {
		return false
	}
	for name, step := range slip.Steps {
		if name == PushParsedStep {
			continue
		}
		if step.Status.IsRunning() {
			return true
		}
	}
	for name, components := range slip.Aggregates {
		// Skipped here for the same reason as above and by the same key: Aggregates is keyed
		// by STEP name, so a config whose push_parsed step aggregates components would
		// otherwise reintroduce the unreleasable claim through the component rollup.
		if name == PushParsedStep {
			continue
		}
		for _, component := range components {
			if component.Status.IsRunning() {
				return true
			}
		}
	}
	return false
}

// ClaimOutcome is what a claim did. Claimed is true when THIS call recorded the claim and
// false when one was already held — the idempotent repeat. Prior is the status the claim was
// taken out of: the current status on a fresh claim, the recorded claimed_from on a repeat.
// InFlight reports whether the slip had a step or component running or held at the moment the
// decision was made, read under the same row lock.
//
// Claimed alone cannot answer the question a rerun caller actually has, which is why InFlight
// exists. A step write does not move the slip's status: StartStep writes running, which is not
// terminal, so checkPipelineCompletion is never reached and a slip dispatched out of failed
// still READS failed until its first post-job. A second rerun message arriving in that window
// passes the same compare-and-set, takes the idempotent repeat arm, and — on Claimed alone —
// looks exactly like a retry whose response was lost before anything dispatched. InFlight is
// what separates them: a caller that finds Claimed=false can tell a claim whose run is
// EXECUTING (do not dispatch; the work is already running) from one whose run never started or
// has finished (a lost response before dispatch, or a stranded claim — dispatching is the
// recovery). DEVOPS-367, PR #87 seventh review.
//
// A caller that only needs the slip protected can ignore both fields, since every non-error
// outcome means the slip is claimed on return.
type ClaimOutcome struct {
	// Claimed is true when this call recorded the claim, false when one was already held.
	Claimed bool

	// Prior is the status the claim was taken out of, recorded or current.
	Prior SlipStatus

	// InFlight is true when a step or component was running or held at decision time.
	InFlight bool
}

// DecideClaim is the claim decision, shared by PostgresStore and both test doubles so the
// three cannot drift. status is the row's current status, claimedFrom its recorded claim (""
// when unclaimed), and inFlight the evidence RunInFlight read from the same locked row. prior
// is the status to report — the current one on a fresh claim, the recorded one on a repeat —
// and write reports whether the store must record the claim (claimed_from plus a marker) or
// this is the idempotent no-op arm.
//
// inFlight is a bool rather than the *Slip it came from deliberately: the store evaluates
// RunInFlight ONCE, hands the same value to this decision and to ClaimOutcome.InFlight, and so
// cannot report a different answer than it decided on. It also keeps TestDecideClaim_Table a
// table of scalars rather than 284 slip literals.
//
// THE RULE, in one sentence: expected is always a compare-and-set on the CURRENT status,
// whether or not a claim is already held — a repeat claim is idempotent only once that
// compare-and-set has agreed to the status the row reads NOW.
//
// The rest follows from it, in this order:
//
//   - A slip with no status at all is refused outright: recording it would write
//     claimed_from = "", which every reader — the repave guard, the push fast path,
//     DecideRelease — treats as unclaimed.
//
//   - A non-empty expected that does not contain the current status is refused, claimed or
//     not. This is the compare-and-set.
//
//   - A run IN FLIGHT — a step or component running or held — that expected did not name is
//     refused, so a nil expected cannot adopt a run that is executing. The refusal does NOT
//     depend on the row being unclaimed, so a run already carrying a claim is protected by it
//     too.
//
//     THIS ARM USED TO READ `status.IsLive() && status != SlipStatusPending`, and that was
//     wrong in both directions (finding j3, PR #87 seventh review). `pending` was carved out
//     as "nothing has been dispatched onto it yet" — but a slip KEEPS `pending` for its whole
//     run, because checkPipelineCompletion only reconciles a status away from `failed`, so a
//     pending slip with three steps running was admitted by a nil expected. And `in_progress`
//     was refused as "a live run" — but between one step's post-job and the next step's
//     pre-job an in_progress slip has nothing running at all, and refusing it made the
//     stranded-claim recovery harder for no protection gained. The status name never carried
//     the fact; the step and aggregate columns do, and they are read under the same FOR UPDATE
//     as the status.
//
//     One behaviour change comes with it, stated rather than left invisible: IsLive() returns
//     true for any status THIS BUILD DOES NOT RECOGNISE (IsTerminal's default arm is false),
//     so an unknown future status used to be refused outright by a nil expected. It is now
//     treated like any other name — admitted when nothing is in flight, refused when something
//     is. The decision no longer reads the status name at all, so there is no longer a class of
//     names it can be wrong about.
//
//     Its `!slices.Contains(expected, status)` clause is only ever REACHABLE for an empty
//     expected: a non-empty one that does not contain the status has already returned at the
//     compare-and-set above, and one that does contain it makes the clause false. It is
//     written in full anyway so this arm states its own precondition and stays correct on its
//     own terms if the compare-and-set above is ever moved, narrowed or reordered — which is
//     how the arms of this decision drifted apart in the first place.
//
//   - Only then, a held claim is the idempotent no-op: prior is the RECORDED claimed_from and
//     nothing is written, so a repeat cannot inflate the audit trail.
//
// THE TWO WORKED CASES the rule exists for, both of them the rerunner retrying a claim whose
// response it lost, with expected = the ended set:
//
//   - The response was lost BEFORE anything dispatched. Nothing ran, so the row still reads
//     failed, the ended set matches, and the retry claims and dispatches. Recovery works —
//     and it is the only window in which a retry SHOULD dispatch.
//
//   - The response was lost AFTER the dispatch and a POST-JOB has reported, so
//     checkPipelineCompletion has run and the reconcile branch has written in_progress. The
//     ended set no longer matches and the retry is refused. That is the desired outcome, not a
//     bug: the dispatch it is retrying already happened, and claiming again would put a second
//     run on top of a live one.
//
//     Read "a post-job has reported" exactly: it is a TERMINAL step status on a pipeline-level
//     step that runs checkPipelineCompletion (steps.go, `status.IsTerminal() && componentName
//     == ""`). A pre-job's StartStep writes `running`, which is not terminal, so it reaches
//     none of that and the status does not move. Between the dispatch and the run's first
//     post-job — minutes, for a build — the row still reads failed and this decision still
//     agrees to the ended set. That window is real, and it is what inFlight covers.
//
// NEITHER worked case is the one A0 was about, and this decision does not answer that one: a
// second rerun MESSAGE, arriving while the first's dispatch is running, sends the same expected
// against a status that has not moved, so the compare-and-set agrees and the repeat arm answers
// it exactly as it answers a lost-response retry. What tells the two apart is
// ClaimOutcome.InFlight, not this function's return — see ClaimOutcome.
//
// A caller that cannot tell "already claimed" from "precondition failed" reads
// ClaimOutcome.Claimed, which is what that distinction is for; it is not a reason to compare
// expected against the recorded prior instead (PR #87, rounds 4 and 6).
func DecideClaim(
	status, claimedFrom SlipStatus, inFlight bool, expected []SlipStatus,
) (prior SlipStatus, write bool, err error) {
	if status == "" {
		return "", false, fmt.Errorf("slip has no status: %w", ErrClaimPreconditionFailed)
	}
	if len(expected) > 0 && !slices.Contains(expected, status) {
		return "", false, fmt.Errorf("status %s not in %v: %w", status, expected, ErrClaimPreconditionFailed)
	}
	if inFlight && !slices.Contains(expected, status) {
		return "", false, fmt.Errorf(
			"a step or component is in flight at status %s; name %s in expected to adopt a run: %w",
			status, status, ErrClaimPreconditionFailed)
	}
	if claimedFrom != "" {
		return claimedFrom, false, nil
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
// compare-and-set on the CURRENT status whether or not a claim is already held; nil admits
// any status EXCEPT one whose run has a step or component in flight, which a caller that means
// to adopt a running run names in expected. Once that compare-and-set agrees, a claim already
// held is an idempotent no-op: ClaimOutcome{Claimed: false} carrying the RECORDED prior, with
// nothing written. Every non-error outcome means the slip is claimed on return; a caller that
// must not dispatch onto work already running reads ClaimOutcome.InFlight, not Claimed. See
// SlipStore.ClaimSlip.
func (c *Client) ClaimSlip(
	ctx context.Context, correlationID string, expected []SlipStatus, claimedBy, reason string,
) (ClaimOutcome, error) {
	ctx, span := StartSpan(ctx, "ClaimSlip", correlationID)
	defer span.End()
	out, err := c.store.ClaimSlip(ctx, correlationID, expected, claimedBy, reason)
	if err != nil {
		return ClaimOutcome{}, NewSlipError("claim", correlationID, err)
	}
	msg := "Claimed slip"
	if !out.Claimed {
		msg = "Slip already claimed: claim kept, nothing written"
	}
	// in_flight is logged on BOTH arms rather than only the repeat: on a fresh claim it is
	// the evidence the caller adopted a run that is executing (only reachable when expected
	// named the status), and on a repeat it is the field that separates a second rerun
	// message from a retry whose response was lost.
	c.logger.Info(ctx, msg, map[string]interface{}{
		"correlation_id": correlationID,
		"prior_status":   string(out.Prior),
		"claimed":        out.Claimed,
		"in_flight":      out.InFlight,
		"claimed_by":     claimedBy,
	})
	return out, nil
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
