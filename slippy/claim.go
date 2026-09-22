package slippy

import (
	"context"
	"fmt"
	"slices"
	"strings"
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

// MaxMarkerActorLen bounds the caller-supplied actor recorded in a claim or release marker.
//
// It is deliberately NOT MaxMarkerReasonLen. slippy-api constrains ClaimedBy and ReleasedBy to
// maxLength 128 with pattern ^[A-Za-z0-9._:/-]+$, so reusing the 512-rune reason bound would
// make the library's own backstop four times looser than the only caller it backstops. 128
// RUNES is the looser-charset equivalent of that caller's 128 ASCII characters, which is the
// right relationship for a defence that sits behind a stricter boundary rather than beside it.
const MaxMarkerActorLen = 128

// clampReason truncates reason to MaxMarkerReasonLen runes, marking the cut.
func clampReason(reason string) string {
	return clampRunes(reason, MaxMarkerReasonLen)
}

// clampActor truncates a marker's caller-supplied actor to MaxMarkerActorLen runes.
//
// Separate from clampReason because the two bounds differ and must keep differing; both route
// through clampRunes so neither can acquire a byte-based truncation by drift.
func clampActor(actor string) string {
	return clampRunes(actor, MaxMarkerActorLen)
}

// maxMarkerFieldBytes is the byte ceiling a single clamped marker field may contribute to
// state_history, independent of its rune bound.
//
// Both bounds are needed and neither implies the other (PR #87 review, jhicks). The rune bound
// is what makes the cut safe — state_history is jsonb, so a cut landing mid-rune produces
// invalid UTF-8 and fails at marshal rather than truncating gracefully. But runes are not the
// metric the column is sized in: 512 runes of 4-byte UTF-8 is ~2 KB, so a rune bound alone does
// not deliver the "cannot grow state_history without limit" guarantee MaxMarkerReasonLen's doc
// states. clampRunes therefore cuts on a rune boundary and keeps cutting until the result fits
// this many bytes.
//
// 1 KiB is chosen to sit above the worst case for the bounds actually in use (512 ASCII runes
// of reason plus 128 of actor) so no realistic caller is truncated by it, while still capping
// the pathological all-4-byte case at something the column can absorb.
const maxMarkerFieldBytes = 1024

// clampRunes truncates s to limit runes AND to maxMarkerFieldBytes bytes, marking the cut with
// an ellipsis. It always cuts on a rune boundary.
//
// Rune boundaries rather than byte offsets on purpose: state_history is a jsonb column, so a
// cut that lands mid-rune produces invalid UTF-8 and fails at marshal rather than truncating
// gracefully. The byte ceiling is applied by dropping whole runes, never by slicing bytes.
func clampRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit && len(s) <= maxMarkerFieldBytes {
		return s
	}
	runes := []rune(s)
	if len(runes) > limit {
		runes = runes[:limit-1]
	}
	// The ellipsis is itself 3 bytes, so the budget it has to fit inside is that much smaller.
	for len(string(runes)) > maxMarkerFieldBytes-len("…") {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

// ClaimMarker builds the adoption history entry every caller writes, so the marker's shape
// is defined once here rather than per client. prior is the slip's status when the claim was
// taken; it goes in the message because an operator reading the history needs to know what
// was adopted. claimedBy is the entry's actor and is audit only.
//
// The actor is clamped and then defaulted, and that order is load-bearing: clamping a
// non-empty string can never yield "", so clamp-then-default is the only order in which both
// guarantees hold. It is markerActor's job, shared with ReleaseMarker so the pair cannot
// acquire different rules.
func ClaimMarker(prior SlipStatus, claimedBy, reason string) StateHistoryEntry {
	msg := fmt.Sprintf("adopted %s slip before dispatching", prior)
	if reason = clampReason(reason); reason != "" {
		msg += ": " + reason
	}
	return StateHistoryEntry{
		Step:      ClaimMarkerStep,
		Status:    StepStatusRunning,
		Timestamp: time.Now(),
		Actor:     markerActor(claimedBy),
		Message:   msg,
	}
}

// markerActor bounds and defaults a marker's caller-supplied actor.
//
// Both halves defend the same field for the same reason MaxMarkerReasonLen exists: the marker
// path writes state_history WITHOUT going through Client.AppendHistoryEntry, which is the only
// other place that defaults an empty actor, so a direct Go consumer reaches this field with
// neither bound nor default applied. slippy-api's minLength:"1" maxLength:"128" means neither
// half is reachable through the deployed API — this is the library holding the line
// MaxMarkerReasonLen's own doc says it holds, for the direct caller it names.
//
// The empty case matters more than the long one. Actor is overloaded as the claim's PRESENCE
// signal: pushhookparser derives its ClaimedBy by scanning state_history backwards and taking
// the newest claim marker's Actor, answering "" both for "no claim marker" and for "a release
// follows it". So an empty actor would set claimed_from while reading UNCLAIMED to the
// marker reader, which is the column-present/marker-absent direction the reset path goes to
// some length to avoid. LibraryActor is honest rather than a placeholder: a caller that named
// nobody really was the library, as far as any later reader can tell.
func markerActor(actor string) string {
	if actor = clampActor(actor); actor == "" {
		return LibraryActor
	}
	return actor
}

// ReleaseMarker builds the release history entry. status is the slip's status at release,
// which the release never changes; it is recorded because the claim's own record
// (claimed_from) is cleared by the same write. releasedBy is the entry's actor and is audit
// only. StepStatusCompleted because a release is the claim's normal end, not a failure; it is
// never read as a pipeline step because the step name is not one.
//
// The actor goes through markerActor for ClaimMarker's reasons; the pair is deliberately
// symmetric, because a bound or default applied to one and not the other is a latent bug
// rather than a smaller version of the same defence.
func ReleaseMarker(status SlipStatus, releasedBy, reason string) StateHistoryEntry {
	msg := fmt.Sprintf("released claim; slip is %s", status)
	if reason = clampReason(reason); reason != "" {
		msg += ": " + reason
	}
	return StateHistoryEntry{
		Step:      ReleaseMarkerStep,
		Status:    StepStatusCompleted,
		Timestamp: time.Now(),
		Actor:     markerActor(releasedBy),
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

// reservedMarkerSteps is every state_history step name the library OWNS, folded to lower case
// the way a step name is folded everywhere else it is compared.
//
// DERIVED from the constants rather than restated, so renaming one of them reserves the new
// name by the same edit. TestReservedMarkerSteps_MatchesTheMarkerConstants pins that.
//
// PushParsedStep is deliberately NOT in this set, and the distinction is the whole reason the
// set exists as its own list rather than as "the three constants at the top of this file".
// `push_parsed` is a REAL pipeline step: a config may declare it, the library's own fixtures
// do, and a post-job may legitimately report it. RunInFlight skips it by name because nothing
// ever completes it, not because a caller may not write it — reserving it would reject a valid
// config at parse time and refuse a legitimate step update. ClaimMarkerStep and
// ReleaseMarkerStep are different in kind: they are not pipeline steps at all, they are the
// claim's audit record, and a caller writing one forges or suppresses the signal a different
// repository gates an irreversible write on.
//
// This is also a different family from reservedStepNames in pipeline_config.go, which reserves
// the fixed routing_slips COLUMNS: that set prevents a generated identifier from colliding with
// an existing column, while this one prevents a caller from entering the marker NAMESPACE.
// validateStepIdentifier checks both, with its own message for each, because the two faults
// look nothing alike to whoever has to fix the config.
var reservedMarkerSteps = func() map[string]struct{} {
	names := []string{ClaimMarkerStep, ReleaseMarkerStep}
	reserved := make(map[string]struct{}, len(names))
	for _, name := range names {
		reserved[strings.ToLower(name)] = struct{}{}
	}
	return reserved
}()

// reservedMarkerStep reports whether name is one of the library's own marker steps.
//
// It exists as one predicate so the config check and every write-path check cannot disagree
// about what is reserved — the drift that let the namespace go undefended in the first place,
// when this file asserted ClaimMarkerStep "is deliberately not a pipeline step name" and
// nothing enforced it.
func reservedMarkerStep(name string) bool {
	_, ok := reservedMarkerSteps[strings.ToLower(name)]
	return ok
}

// GuardReservedStepWrite refuses a CALLER-SUPPLIED write into the library's marker namespace,
// checking both the step name the caller addressed and the Step of any entry it supplied.
// It returns an error wrapping ErrReservedStepName, or nil when the write may proceed.
//
// Exported for the reason DecideClaim, DecideRelease and DecideReset are: PostgresStore,
// slippytest.MockStore and the in-package double must all refuse the same names, or a
// consumer's assertion about a rejected step would pass against a double and fail against
// Postgres. This is the one definition of "a caller may not write this step".
//
// It guards the ENTRY POINTS — UpdateStep, UpdateComponentStatus, UpdateStepWithHistory and
// AppendHistory — and deliberately not appendHistoryTx, the shared plumbing beneath them:
// ClaimSlip appends a ClaimMarker through that plumbing, ReleaseClaim a ReleaseMarker, and
// Repave its push_parsed bookkeeping entry. Guarding the plumbing would refuse the library's
// own writes; guarding the entry points refuses exactly the input that should never have
// named a marker in the first place.
//
// An empty stepName means the caller addressed no step (the AppendHistory shape), not that the
// check is skipped — the entry's own Step is checked either way.
func GuardReservedStepWrite(stepName string, entry *StateHistoryEntry) error {
	if stepName != "" && reservedMarkerStep(stepName) {
		return fmt.Errorf(
			"step %q is a state_history marker the library owns: %w", stepName, ErrReservedStepName)
	}
	if entry != nil && entry.Step != "" && reservedMarkerStep(entry.Step) {
		return fmt.Errorf(
			"history entry step %q is a state_history marker the library owns: %w",
			entry.Step, ErrReservedStepName)
	}
	return nil
}

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
// WHICH FIELDS A CALLER MAY IGNORE DEPENDS ENTIRELY ON WHAT IT DOES NEXT, and an earlier
// version of this paragraph said only the permissive half (PR #87 review, pkuzmenko):
//
//   - A caller that only needs the slip PROTECTED can ignore both fields, since every
//     non-error outcome means the slip is claimed on return. A pre-job that claims and then
//     runs its own step is this caller.
//   - A caller that may DISPATCH work can ignore neither, and must branch on InFlight rather
//     than on Claimed. Claimed=false with InFlight=true means another run is already
//     executing against this slip and dispatching would double it; Claimed=false with
//     InFlight=false is the lost-response or stranded-claim window where dispatching IS the
//     recovery. The two are indistinguishable on Claimed alone, for the reason above. This is
//     the rule slippy-api#59 states on its own endpoint ("BRANCH ON in_flight, NOT ON
//     claimed") and the one pushhookparser#55's rerunner implements.
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
//   - Then a held claim is the idempotent no-op: prior is the RECORDED claimed_from and
//     nothing is written, so a repeat cannot inflate the audit trail.
//
//     IT SITS AHEAD OF THE IN-FLIGHT REFUSAL BELOW, and that order is the fix for finding
//     j-claim (PR #87, jhicks round). The refusal exists to stop a caller ADOPTING a run it
//     does not hold — and adopting is the WRITE, which only the last arm reaches. On an
//     already-claimed row there is nothing left to adopt: the claim stays exactly where it
//     was, nothing is written either way, and the only thing the ordering decides is whether
//     the caller is handed the recorded prior or ErrClaimPreconditionFailed. With the refusal
//     in front, `all but the first pre-job of a run takes the idempotent repeat` — the
//     idempotency SlipStore.ClaimSlip documents — was false for every caller sending a nil
//     expected, because pre-job 1's StartStep makes the run in-flight and
//     slices.Contains(nil, status) is always false. It is true of the code now.
//
//     Protection is not weakened by the move, because the only arm that records a claim is
//     the last one and it is still BEHIND the refusal: a fresh claim onto a run that is
//     executing still requires the caller to name that run's status in expected. What the
//     repeat arm hands back instead of an error is strictly more information — the recorded
//     prior, plus ClaimOutcome.InFlight read off the same locked row — and InFlight, not the
//     error, is what a caller that must not double-dispatch branches on (see ClaimOutcome).
//
//     THAT PROTECTION IS ONLY REAL IF InFlight REACHES THE CALLER, which for an out-of-process
//     adopter means it has to be on the wire (PR #87 review, pkuzmenko). It is, in the same
//     release train as this change and not after it: slippy-api#59 answers the claim 200 with
//     a body carrying {claimed, prior, in_flight} — in_flight is a REQUIRED field of that
//     response, not an optional one — and takes if_status in, which is this expected. Its two
//     adopters read it: pushhookparser#55's client returns (claimed, inFlight, err) and its
//     rerunner dispatches only when both are false, and Slippy#28's pre-job sends if_status
//     from prejobClaimIfStatus(). Until that train lands, the consumers' main branches carry
//     a 3-argument ClaimSlip and a 204, where a nil expected would be the flipped class with
//     nothing to read — which is why this library must not be bumped into a consumer ahead of
//     its own claim PR.
//
//   - A run IN FLIGHT — a step or component running or held — that expected did not name is
//     refused, so a nil expected cannot record a claim on a run that is executing. Reached
//     only for an UNCLAIMED row, per the arm above.
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
//   - Only then, an unclaimed row whose current status the caller agreed to is claimed:
//     prior is that status, and the store records it.
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
	if claimedFrom != "" {
		return claimedFrom, false, nil
	}
	if inFlight && !slices.Contains(expected, status) {
		return "", false, fmt.Errorf(
			"a step or component is in flight at status %s; name %s in expected to adopt a run: %w",
			status, status, ErrClaimPreconditionFailed)
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

// DecideReset is the in-place reset decision, shared by PostgresStore.ResetSlipInPlace and
// both test doubles so the three cannot drift, exactly as DecideClaim and DecideRelease are.
// claimedFrom is the target row's recorded claim ("" when unclaimed) and inFlight the evidence
// RunInFlight read from the SAME locked row — both read under the FOR UPDATE that the upsert
// then lands beneath, which is the whole reason this decision exists as a store operation
// rather than as a push-side branch on an unlocked snapshot (DEVOPS-367).
//
// THE RULE, in one sentence: an in-place reset is allowed only onto an UNCLAIMED row.
//
//   - ALLOWED when claimedFrom is empty. Nobody holds the row, so there is no other run's
//     state for the upsert to destroy.
//
//   - REFUSED, ErrSlipClaimed, whenever the row is claimed — whether or not anything of that
//     claim's run is running yet. The reset is Create's ON CONFLICT arm, which rewrites every
//     step and aggregate column and the whole state history; performing it would destroy the
//     state that run is about to write, under an unchanged correlation ID, leaving an operator
//     no way to tell which attempt wrote what. The caller deduplicates onto the live row.
//
// THE QUIESCENT CLAIM USED TO BE ALLOWED, and that was the bug (PR #87 review, pkuzmenko).
// The carve-out's premise was that a row carrying THIS push's correlation ID must be this
// delivery's own retry, so there was no other run's work to protect. That does not hold: the
// rerunner adopts the slip it looked up and claims under the ORIGINAL push's correlation ID
// (pushhookparser#55 sets correlationID = lookup.CorrelationID), and the Slippy CLI pre-job
// claims the slip its workflow was dispatched for. So a self-correlated row is routinely
// claimed by SOMEONE ELSE'S run.
//
// The window was not a race either. A claim records no step: the claimant's status stays where
// it was until its first post-job, and RunInFlight stays false for the whole time its workflow
// sits in the queue. A redelivery of the original push landing in that window found
// claimed && !inFlight, was allowed, and reset every step, aggregate and the history of a run
// that was already dispatched.
//
// inFlight is therefore no longer part of the DECISION, and is kept only so the refusal can
// tell an operator which shape it refused. Keeping the parameter also means a third-party
// store that already routes through this function does not have to change its call.
//
// This aligns the three encodings of "claimed means protected" that used to disagree:
// slipUnclaimedSQL refuses on a set claimed_from full stop, Repave's guard does the same, and
// this now matches them. The remaining difference is CreateSlipForPush's dedup gate, which
// lets a self-correlated claimed row fall through to here rather than deduping at the push
// layer — that is a round trip, not a divergence, because this refusal sends it to the same
// dedup.
func DecideReset(claimedFrom SlipStatus, inFlight bool) error {
	if claimedFrom == "" {
		return nil
	}
	if inFlight {
		return fmt.Errorf(
			"a step or component is in flight under the claim taken out of %s: %w",
			claimedFrom, ErrSlipClaimed)
	}
	return fmt.Errorf(
		"the row is claimed out of %s; its run has not reported a step yet, which is not "+
			"evidence that nothing was dispatched: %w", claimedFrom, ErrSlipClaimed)
}

// ClaimSlip records that a run is in flight against a slip, so a same-commit push
// deduplicates onto it instead of repaving it (DEVOPS-285, DEVOPS-367). The claim is a flag:
// it never changes the slip's status, and it lives until the run is over — released by a
// post-job once nothing is in flight, or ended by a terminal status write. expected is a
// compare-and-set on the CURRENT status whether or not a claim is already held; on an
// UNCLAIMED row nil admits any status EXCEPT one whose run has a step or component in flight,
// which a caller that means to adopt a running run names in expected. Once that
// compare-and-set agrees, a claim already held is an idempotent no-op: ClaimOutcome{Claimed:
// false} carrying the RECORDED prior, with nothing written — including while that claim's own
// run is executing, which is the arm every pre-job after the first takes. Every non-error outcome means the slip is claimed on return; a caller that
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
