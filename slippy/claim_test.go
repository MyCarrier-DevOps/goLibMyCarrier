package slippy

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The claim sentinels must be distinct from each other and from the repave sentinel they
// sit beside: callers branch on errors.Is, so two sentinels that compared equal would make a
// precondition failure indistinguishable from a not-claimed one. Work in flight is
// deliberately NOT a sentinel — it is ReleaseOutcome{Released: false} — so a post-job that
// releases early sees an outcome, not an error.
func TestClaimSentinelsAreDistinct(t *testing.T) {
	require.NotErrorIs(t, ErrClaimPreconditionFailed, ErrSlipWentLive)
	require.NotErrorIs(t, ErrNotClaimed, ErrClaimPreconditionFailed)
	require.NotErrorIs(t, ErrNotClaimed, ErrSlipNotFound)
	assert.Contains(t, ErrClaimPreconditionFailed.Error(), "status")
	assert.Contains(t, ErrNotClaimed.Error(), "claimed")
}

// ClaimedFrom must serialise under the snake_case key the API contract exposes, be omitted
// when the slip is unclaimed, and be excluded from the ClickHouse column mapping (ch:"-")
// because no ClickHouse table has it.
func TestSlip_ClaimedFrom_JSONShape(t *testing.T) {
	claimed, err := json.Marshal(&Slip{CorrelationID: "c1", Status: SlipStatusFailed, ClaimedFrom: SlipStatusFailed})
	require.NoError(t, err)
	assert.Contains(t, string(claimed), `"claimed_from":"failed"`)
	var back Slip
	require.NoError(t, json.Unmarshal(claimed, &back))
	assert.Equal(t, SlipStatusFailed, back.ClaimedFrom, "round-trips under the snake_case key")

	unclaimed, err := json.Marshal(&Slip{CorrelationID: "c2", Status: SlipStatusFailed})
	require.NoError(t, err)
	assert.NotContains(t, string(unclaimed), "claimed_from", "omitempty: an unclaimed slip carries no key")

	field, ok := reflect.TypeOf(Slip{}).FieldByName("ClaimedFrom")
	require.True(t, ok)
	assert.Equal(t, "claimed_from,omitempty", field.Tag.Get("json"))
	assert.Equal(t, "-", field.Tag.Get("ch"), "no ClickHouse column: the ch mapper must skip it")
}

// The marker builders are the single definition of the markers' shape; the status in the
// message is the only in-slip record of what was claimed or released once claimed_from is
// cleared, so the format is load-bearing, not cosmetic.
func TestClaimMarker_RecordsPriorStatusAndReason(t *testing.T) {
	tests := []struct {
		name, reason, wantMsg string
		prior                 SlipStatus
	}{
		{"failed with scope", "retrigger builds", "adopted failed slip before dispatching: retrigger builds", SlipStatusFailed},
		{"reason omitted", "", "adopted failed slip before dispatching", SlipStatusFailed},
		{"completed reads as unusual", "", "adopted completed slip before dispatching", SlipStatusCompleted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := ClaimMarker(tc.prior, "rerunner", tc.reason)
			assert.Equal(t, tc.wantMsg, e.Message)
			assert.Equal(t, "rerunner", e.Actor)
			assert.Equal(t, ClaimMarkerStep, e.Step)
			assert.Equal(t, StepStatusRunning, e.Status)
			assert.False(t, e.Timestamp.IsZero())
		})
	}
}

func TestReleaseMarker_RecordsStatusAndReason(t *testing.T) {
	e := ReleaseMarker(SlipStatusFailed, "post-job", "run over")
	assert.Equal(t, "released claim; slip is failed: run over", e.Message)
	assert.Equal(t, "post-job", e.Actor)
	assert.Equal(t, ReleaseMarkerStep, e.Step)
	assert.Equal(t, StepStatusCompleted, e.Status, "a release is the claim's normal end, not an abort")
	assert.Equal(t, "released claim; slip is in_progress", ReleaseMarker(SlipStatusInProgress, "x", "").Message)
}

// A caller-supplied reason is bounded so state_history cannot grow without limit through the
// markers; slippy-api enforces the same bound on its bodies, this is the library's own.
func TestMarkers_ClampTheReason(t *testing.T) {
	long := strings.Repeat("é", MaxMarkerReasonLen+40)
	claim := ClaimMarker(SlipStatusFailed, "a", long)
	release := ReleaseMarker(SlipStatusFailed, "a", long)
	for _, m := range []string{claim.Message, release.Message} {
		reason := m[strings.Index(m, ": ")+2:]
		assert.Len(t, []rune(reason), MaxMarkerReasonLen, "clamped to MaxMarkerReasonLen runes")
		assert.True(t, strings.HasSuffix(reason, "…"), "the cut is marked")
	}
	exact := strings.Repeat("x", MaxMarkerReasonLen)
	assert.True(t, strings.HasSuffix(ClaimMarker(SlipStatusFailed, "a", exact).Message, exact), "at the bound, untouched")
}

// Neither marker may name a real pipeline step, or an aggregate or phase reader would take
// it for a step event; and neither may reuse push_parsed, which the library's own reset
// marker owns. The API's boot-time collision check keys on these constants.
func TestMarkerSteps_AreNotPipelineSteps(t *testing.T) {
	cfg := pgTestPipelineConfig(t)
	for _, step := range []string{ClaimMarkerStep, ReleaseMarkerStep} {
		assert.Nil(t, cfg.GetStep(step), "%q must not be a configured step", step)
		assert.NotEqual(t, "push_parsed", step)
	}
	assert.NotEqual(t, ClaimMarkerStep, ReleaseMarkerStep)
}

// RunInFlight is the one definition of "work in flight" the claim protects: running or held,
// at step level or inside an aggregate's components.
func TestRunInFlight(t *testing.T) {
	tests := []struct {
		name string
		slip *Slip
		want bool
	}{
		{"nothing", &Slip{Steps: map[string]Step{"builds": {Status: StepStatusFailed}, "unit_tests": {Status: StepStatusPending}}}, false},
		{"a running step", &Slip{Steps: map[string]Step{"builds": {Status: StepStatusRunning}}}, true},
		{"a held step", &Slip{Steps: map[string]Step{"dev_deploy": {Status: StepStatusHeld}}}, true},
		{"a running component under a failed aggregate", &Slip{
			Steps:      map[string]Step{"builds": {Status: StepStatusFailed}},
			Aggregates: map[string][]ComponentStepData{"builds": {{Component: "api", Status: StepStatusFailed}, {Component: "web", Status: StepStatusRunning}}},
		}, true},
		{"completed and skipped only", &Slip{Steps: map[string]Step{"builds": {Status: StepStatusCompleted}, "secretscan": {Status: StepStatusSkipped}}}, false},
		{"push_parsed running is the library's bookkeeping, not in flight", &Slip{Steps: map[string]Step{"push_parsed": {Status: StepStatusRunning}, "builds": {Status: StepStatusFailed}}}, false},
		// Aggregates is keyed by step name too, so the same exemption has to hold there or a
		// config that aggregated push_parsed would hold the claim open through its components.
		{"a running component under push_parsed is that same bookkeeping", &Slip{
			Steps:      map[string]Step{"push_parsed": {Status: StepStatusRunning}, "builds": {Status: StepStatusFailed}},
			Aggregates: map[string][]ComponentStepData{"push_parsed": {{Component: "api", Status: StepStatusRunning}}},
		}, false},
		// Exported for third-party stores to route their own release decision through, so a
		// store that hands over nothing must get an answer rather than a panic.
		{"a nil slip has nothing in flight", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, RunInFlight(tc.slip))
		})
	}
}

// TestDecideClaim_Table is the decision space, cell by cell: every status crossed with a
// sample of claim states, crossed with whether the run is in flight, crossed with the
// if_status sets the real callers send. It exists because the claim decision regressed three
// review rounds running, each time because one arm was fixed in isolation (PR #87, sixth
// review). Cells, not narrative cases, are what stop that: a change to one arm that breaks
// another now fails here rather than in production.
//
// The rule every cell follows, in this order: an empty status is refused outright; a
// non-empty if_status is a compare-and-set on the CURRENT status whether or not a claim is
// held; a held claim then takes the idempotent no-op arm, returning the RECORDED prior with
// nothing written; and only an UNCLAIMED row with a step or component IN FLIGHT is refused
// unless the caller named the status it is running under.
//
// THE LAST TWO SWAPPED PLACES for finding j-claim (PR #87, jhicks round), and the cells that
// moved are exactly {claim held, in flight, if_status empty}: they used to read
// ErrClaimPreconditionFailed and now read the idempotent repeat. Nothing else moved, because
// for a NON-EMPTY if_status the two arms cannot both be live — one that does not contain the
// status returned at the compare-and-set, one that does makes the in-flight arm's own
// `!slices.Contains` clause false. The property the swap preserves is in this table too, as
// the `unclaimed, a step running` cells: the arm that RECORDS a claim still sits behind the
// refusal, so a nil if_status still cannot claim a run that is executing.
//
// WHAT THE inFlight COLUMN CHANGED, and why it is crossed over everything rather than added
// as a few cases. The refusal used to read `status.IsLive() && status != pending`, so it
// answered from the status NAME: `pending` was carved out as "nothing dispatched onto it"
// and `in_progress` was refused as "a live run". Neither reading was true — a slip keeps
// `pending` for its whole run, and `in_progress` between one post-job and the next pre-job
// has nothing running at all — so the two columns that do carry the evidence decide it now
// (A0b, DEVOPS-367). The visible consequences are all in the if_status=anyStatus rows: a
// `pending` slip with a step running is now refused, and an `in_progress` slip with nothing
// running is now claimable.
//
// For every NON-EMPTY if_status the answer is independent of inFlight, and the table carries
// both variants of each such cell to keep it that way: the evidence arm is only ever
// reachable for an empty if_status AND an unclaimed row, since a non-empty if_status that
// does not contain the status already returned at the compare-and-set, one that does contain
// it makes the arm's own `!slices.Contains` clause false, and a claimed row took the repeat
// arm above it. That is the property the doubling buys — drop the clause
// and make the refusal unconditional on in-flight evidence, and every cell where a caller
// NAMED the status of a run in flight flips from claimed (or the idempotent repeat) to
// refused: the Slippy CLI pre-job adopting its own live run, and every rerunner repeat in the
// window A0 is about.
//
// claimedFrom is SAMPLED, not exhaustive — the cross product runs over {unclaimed, failed,
// promoted}, which are the rerunner's two values (it claims out of the ended set, of which
// `failed` and `promoted` are both members), and the rows past the cross product add the
// CLI pre-job's own two (`pending` and `compensating`, since its set is every non-terminal
// status) plus `in_progress`. Every other status is a legal claimed_from that no row here
// carries.
func TestDecideClaim_Table(t *testing.T) {
	var anyStatus []SlipStatus // nil: the caller agreed to claim out of anything
	onlyFailed := []SlipStatus{SlipStatusFailed}
	onlyInProgress := []SlipStatus{SlipStatusInProgress}
	// The Slippy CLI pre-job's set: every non-terminal status (slipStatusTable, app.go).
	nonTerminal := []SlipStatus{
		SlipStatusPending, SlipStatusInProgress, SlipStatusFailed, SlipStatusCompensating,
	}
	// pushhookparser's rerunner set: every ended status (rerunClaimIfStatus, rerunner.go).
	ended := []SlipStatus{
		SlipStatusFailed, SlipStatusCompleted, SlipStatusCompensated, SlipStatusAbandoned, SlipStatusPromoted,
	}

	tests := []struct {
		name        string
		status      SlipStatus
		claimedFrom SlipStatus
		inFlight    bool
		expected    []SlipStatus
		wantPrior   SlipStatus
		wantWrite   bool
		wantErr     error
	}{
		// --- status "" ---
		{"unclaimed, quiescent / if_status=anyStatus: no status at all: refused whatever is asked and whatever is running, an empty status cannot be recorded as a claim", "", "", false, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=anyStatus: no status at all: refused whatever is asked and whatever is running, an empty status cannot be recorded as a claim", "", "", true, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=anyStatus: no status at all: refused whatever is asked and whatever is running, an empty status cannot be recorded as a claim", "", SlipStatusFailed, false, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=anyStatus: no status at all: refused whatever is asked and whatever is running, an empty status cannot be recorded as a claim", "", SlipStatusFailed, true, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=anyStatus: no status at all: refused whatever is asked and whatever is running, an empty status cannot be recorded as a claim", "", SlipStatusPromoted, false, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=anyStatus: no status at all: refused whatever is asked and whatever is running, an empty status cannot be recorded as a claim", "", SlipStatusPromoted, true, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=onlyFailed: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", "", false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyFailed: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", "", true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyFailed: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusFailed, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyFailed: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusFailed, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyFailed: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusPromoted, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyFailed: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusPromoted, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=onlyInProgress: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", "", false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyInProgress: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", "", true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyInProgress: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusFailed, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyInProgress: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusFailed, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyInProgress: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusPromoted, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyInProgress: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusPromoted, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=nonTerminal: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", "", false, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=nonTerminal: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", "", true, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=nonTerminal: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusFailed, false, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=nonTerminal: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusFailed, true, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=nonTerminal: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusPromoted, false, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=nonTerminal: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusPromoted, true, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=ended: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", "", false, ended, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=ended: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", "", true, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=ended: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusFailed, false, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=ended: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusFailed, true, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=ended: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusPromoted, false, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=ended: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusPromoted, true, ended, "", false, ErrClaimPreconditionFailed},
		// --- status pending ---
		{"unclaimed, quiescent / if_status=anyStatus: pending with nothing in flight: claimed — nothing has been dispatched onto it, and it is the step and aggregate columns that say so, not the status name", SlipStatusPending, "", false, anyStatus, SlipStatusPending, true, nil},
		{"unclaimed, a step running / if_status=anyStatus: pending while a step of the run is executing: refused — the run is in flight whatever the status column reads, and nothing named it", SlipStatusPending, "", true, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=anyStatus: pending with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPending, SlipStatusFailed, false, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=anyStatus: pending while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusPending, SlipStatusFailed, true, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=anyStatus: pending with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPending, SlipStatusPromoted, false, anyStatus, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=anyStatus: pending while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusPending, SlipStatusPromoted, true, anyStatus, SlipStatusPromoted, false, nil},
		{"unclaimed, quiescent / if_status=onlyFailed: pending is outside if_status [failed]: refused", SlipStatusPending, "", false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyFailed: pending is outside if_status [failed]: refused", SlipStatusPending, "", true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyFailed: pending is outside if_status [failed]: refused", SlipStatusPending, SlipStatusFailed, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyFailed: pending is outside if_status [failed]: refused", SlipStatusPending, SlipStatusFailed, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyFailed: pending is outside if_status [failed]: refused", SlipStatusPending, SlipStatusPromoted, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyFailed: pending is outside if_status [failed]: refused", SlipStatusPending, SlipStatusPromoted, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=onlyInProgress: pending is outside if_status [in_progress]: refused", SlipStatusPending, "", false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyInProgress: pending is outside if_status [in_progress]: refused", SlipStatusPending, "", true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyInProgress: pending is outside if_status [in_progress]: refused", SlipStatusPending, SlipStatusFailed, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyInProgress: pending is outside if_status [in_progress]: refused", SlipStatusPending, SlipStatusFailed, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyInProgress: pending is outside if_status [in_progress]: refused", SlipStatusPending, SlipStatusPromoted, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyInProgress: pending is outside if_status [in_progress]: refused", SlipStatusPending, SlipStatusPromoted, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=nonTerminal: pending is in the CLI pre-job's set: claimed", SlipStatusPending, "", false, nonTerminal, SlipStatusPending, true, nil},
		{"unclaimed, a step running / if_status=nonTerminal: pending is in the CLI pre-job's set: claimed", SlipStatusPending, "", true, nonTerminal, SlipStatusPending, true, nil},
		{"claim held out of failed, quiescent / if_status=nonTerminal: pending is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPending, SlipStatusFailed, false, nonTerminal, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=nonTerminal: pending is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPending, SlipStatusFailed, true, nonTerminal, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=nonTerminal: pending is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPending, SlipStatusPromoted, false, nonTerminal, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=nonTerminal: pending is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPending, SlipStatusPromoted, true, nonTerminal, SlipStatusPromoted, false, nil},
		{"unclaimed, quiescent / if_status=ended: pending is outside the rerunner's ended set: refused", SlipStatusPending, "", false, ended, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=ended: pending is outside the rerunner's ended set: refused", SlipStatusPending, "", true, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=ended: pending is outside the rerunner's ended set: refused", SlipStatusPending, SlipStatusFailed, false, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=ended: pending is outside the rerunner's ended set: refused", SlipStatusPending, SlipStatusFailed, true, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=ended: pending is outside the rerunner's ended set: refused", SlipStatusPending, SlipStatusPromoted, false, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=ended: pending is outside the rerunner's ended set: refused", SlipStatusPending, SlipStatusPromoted, true, ended, "", false, ErrClaimPreconditionFailed},
		// --- status in_progress ---
		{"unclaimed, quiescent / if_status=anyStatus: in_progress with nothing in flight: claimed — between one step's post-job and the next step's pre-job the status still reads in_progress, and the name alone is no longer the refusal", SlipStatusInProgress, "", false, anyStatus, SlipStatusInProgress, true, nil},
		{"unclaimed, a step running / if_status=anyStatus: in_progress while a step of the run is executing: refused — the run is in flight whatever the status column reads, and nothing named it", SlipStatusInProgress, "", true, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=anyStatus: in_progress with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusInProgress, SlipStatusFailed, false, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=anyStatus: in_progress while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusInProgress, SlipStatusFailed, true, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=anyStatus: in_progress with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusInProgress, SlipStatusPromoted, false, anyStatus, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=anyStatus: in_progress while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusInProgress, SlipStatusPromoted, true, anyStatus, SlipStatusPromoted, false, nil},
		{"unclaimed, quiescent / if_status=onlyFailed: in_progress is outside if_status [failed]: refused, the status moved off what was agreed", SlipStatusInProgress, "", false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyFailed: in_progress is outside if_status [failed]: refused, the status moved off what was agreed", SlipStatusInProgress, "", true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyFailed: in_progress is outside if_status [failed]: refused, the status moved off what was agreed", SlipStatusInProgress, SlipStatusFailed, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyFailed: in_progress is outside if_status [failed]: refused, the status moved off what was agreed", SlipStatusInProgress, SlipStatusFailed, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyFailed: in_progress is outside if_status [failed]: refused, the status moved off what was agreed", SlipStatusInProgress, SlipStatusPromoted, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyFailed: in_progress is outside if_status [failed]: refused, the status moved off what was agreed", SlipStatusInProgress, SlipStatusPromoted, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=onlyInProgress: in_progress is named explicitly: a live run the caller asked for", SlipStatusInProgress, "", false, onlyInProgress, SlipStatusInProgress, true, nil},
		{"unclaimed, a step running / if_status=onlyInProgress: in_progress is named explicitly: a live run the caller asked for", SlipStatusInProgress, "", true, onlyInProgress, SlipStatusInProgress, true, nil},
		{"claim held out of failed, quiescent / if_status=onlyInProgress: in_progress is named explicitly: a live run the caller asked for; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusInProgress, SlipStatusFailed, false, onlyInProgress, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=onlyInProgress: in_progress is named explicitly: a live run the caller asked for; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusInProgress, SlipStatusFailed, true, onlyInProgress, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=onlyInProgress: in_progress is named explicitly: a live run the caller asked for; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusInProgress, SlipStatusPromoted, false, onlyInProgress, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=onlyInProgress: in_progress is named explicitly: a live run the caller asked for; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusInProgress, SlipStatusPromoted, true, onlyInProgress, SlipStatusPromoted, false, nil},
		{"unclaimed, quiescent / if_status=nonTerminal: in_progress is in the CLI pre-job's set: a later phase claims the live run it named", SlipStatusInProgress, "", false, nonTerminal, SlipStatusInProgress, true, nil},
		{"unclaimed, a step running / if_status=nonTerminal: in_progress is in the CLI pre-job's set: a later phase claims the live run it named", SlipStatusInProgress, "", true, nonTerminal, SlipStatusInProgress, true, nil},
		{"claim held out of failed, quiescent / if_status=nonTerminal: in_progress is in the CLI pre-job's set: a later phase claims the live run it named; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusInProgress, SlipStatusFailed, false, nonTerminal, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=nonTerminal: in_progress is in the CLI pre-job's set: a later phase claims the live run it named; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusInProgress, SlipStatusFailed, true, nonTerminal, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=nonTerminal: in_progress is in the CLI pre-job's set: a later phase claims the live run it named; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusInProgress, SlipStatusPromoted, false, nonTerminal, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=nonTerminal: in_progress is in the CLI pre-job's set: a later phase claims the live run it named; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusInProgress, SlipStatusPromoted, true, nonTerminal, SlipStatusPromoted, false, nil},
		{"unclaimed, quiescent / if_status=ended: rerunner retries after its dispatch already started: refused, the work is running", SlipStatusInProgress, "", false, ended, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=ended: rerunner retries after its dispatch already started: refused, the work is running", SlipStatusInProgress, "", true, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=ended: rerunner retries after its dispatch already started: refused, the work is running", SlipStatusInProgress, SlipStatusFailed, false, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=ended: rerunner retries after its dispatch already started: refused, the work is running", SlipStatusInProgress, SlipStatusFailed, true, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=ended: rerunner retries after its dispatch already started: refused, the work is running", SlipStatusInProgress, SlipStatusPromoted, false, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=ended: rerunner retries after its dispatch already started: refused, the work is running", SlipStatusInProgress, SlipStatusPromoted, true, ended, "", false, ErrClaimPreconditionFailed},
		// --- status compensating ---
		{"unclaimed, quiescent / if_status=anyStatus: compensating with nothing in flight: claimed — the status name is not the evidence, the step and aggregate columns are", SlipStatusCompensating, "", false, anyStatus, SlipStatusCompensating, true, nil},
		{"unclaimed, a step running / if_status=anyStatus: compensating while a step of the run is executing: refused — the run is in flight whatever the status column reads, and nothing named it", SlipStatusCompensating, "", true, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=anyStatus: compensating with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensating, SlipStatusFailed, false, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=anyStatus: compensating while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusCompensating, SlipStatusFailed, true, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=anyStatus: compensating with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensating, SlipStatusPromoted, false, anyStatus, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=anyStatus: compensating while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusCompensating, SlipStatusPromoted, true, anyStatus, SlipStatusPromoted, false, nil},
		{"unclaimed, quiescent / if_status=onlyFailed: compensating is outside if_status [failed]: refused", SlipStatusCompensating, "", false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyFailed: compensating is outside if_status [failed]: refused", SlipStatusCompensating, "", true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyFailed: compensating is outside if_status [failed]: refused", SlipStatusCompensating, SlipStatusFailed, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyFailed: compensating is outside if_status [failed]: refused", SlipStatusCompensating, SlipStatusFailed, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyFailed: compensating is outside if_status [failed]: refused", SlipStatusCompensating, SlipStatusPromoted, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyFailed: compensating is outside if_status [failed]: refused", SlipStatusCompensating, SlipStatusPromoted, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=onlyInProgress: compensating is outside if_status [in_progress]: refused", SlipStatusCompensating, "", false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyInProgress: compensating is outside if_status [in_progress]: refused", SlipStatusCompensating, "", true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyInProgress: compensating is outside if_status [in_progress]: refused", SlipStatusCompensating, SlipStatusFailed, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyInProgress: compensating is outside if_status [in_progress]: refused", SlipStatusCompensating, SlipStatusFailed, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyInProgress: compensating is outside if_status [in_progress]: refused", SlipStatusCompensating, SlipStatusPromoted, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyInProgress: compensating is outside if_status [in_progress]: refused", SlipStatusCompensating, SlipStatusPromoted, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=nonTerminal: compensating is in the CLI pre-job's set: claimed", SlipStatusCompensating, "", false, nonTerminal, SlipStatusCompensating, true, nil},
		{"unclaimed, a step running / if_status=nonTerminal: compensating is in the CLI pre-job's set: claimed", SlipStatusCompensating, "", true, nonTerminal, SlipStatusCompensating, true, nil},
		{"claim held out of failed, quiescent / if_status=nonTerminal: compensating is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensating, SlipStatusFailed, false, nonTerminal, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=nonTerminal: compensating is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensating, SlipStatusFailed, true, nonTerminal, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=nonTerminal: compensating is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensating, SlipStatusPromoted, false, nonTerminal, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=nonTerminal: compensating is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensating, SlipStatusPromoted, true, nonTerminal, SlipStatusPromoted, false, nil},
		{"unclaimed, quiescent / if_status=ended: compensating is outside the rerunner's ended set: refused", SlipStatusCompensating, "", false, ended, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=ended: compensating is outside the rerunner's ended set: refused", SlipStatusCompensating, "", true, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=ended: compensating is outside the rerunner's ended set: refused", SlipStatusCompensating, SlipStatusFailed, false, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=ended: compensating is outside the rerunner's ended set: refused", SlipStatusCompensating, SlipStatusFailed, true, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=ended: compensating is outside the rerunner's ended set: refused", SlipStatusCompensating, SlipStatusPromoted, false, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=ended: compensating is outside the rerunner's ended set: refused", SlipStatusCompensating, SlipStatusPromoted, true, ended, "", false, ErrClaimPreconditionFailed},
		// --- status failed ---
		{"unclaimed, quiescent / if_status=anyStatus: failed with nothing in flight: claimed, the rerunner's ordinary adoption", SlipStatusFailed, "", false, anyStatus, SlipStatusFailed, true, nil},
		{"unclaimed, a step running / if_status=anyStatus: failed while a step of the run is executing: refused — the run is in flight whatever the status column reads, and nothing named it", SlipStatusFailed, "", true, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=anyStatus: failed with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusFailed, false, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=anyStatus: failed while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusFailed, SlipStatusFailed, true, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=anyStatus: failed with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusPromoted, false, anyStatus, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=anyStatus: failed while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusFailed, SlipStatusPromoted, true, anyStatus, SlipStatusPromoted, false, nil},
		{"unclaimed, quiescent / if_status=onlyFailed: failed named exactly: the rerunner's fresh claim", SlipStatusFailed, "", false, onlyFailed, SlipStatusFailed, true, nil},
		{"unclaimed, a step running / if_status=onlyFailed: failed named exactly: the rerunner's fresh claim", SlipStatusFailed, "", true, onlyFailed, SlipStatusFailed, true, nil},
		{"claim held out of failed, quiescent / if_status=onlyFailed: failed named exactly: the rerunner's fresh claim; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusFailed, false, onlyFailed, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=onlyFailed: failed named exactly: the rerunner's fresh claim; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusFailed, true, onlyFailed, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=onlyFailed: failed named exactly: the rerunner's fresh claim; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusPromoted, false, onlyFailed, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=onlyFailed: failed named exactly: the rerunner's fresh claim; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusPromoted, true, onlyFailed, SlipStatusPromoted, false, nil},
		{"unclaimed, quiescent / if_status=onlyInProgress: failed is outside if_status [in_progress]: refused", SlipStatusFailed, "", false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyInProgress: failed is outside if_status [in_progress]: refused", SlipStatusFailed, "", true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyInProgress: failed is outside if_status [in_progress]: refused", SlipStatusFailed, SlipStatusFailed, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyInProgress: failed is outside if_status [in_progress]: refused", SlipStatusFailed, SlipStatusFailed, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyInProgress: failed is outside if_status [in_progress]: refused", SlipStatusFailed, SlipStatusPromoted, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyInProgress: failed is outside if_status [in_progress]: refused", SlipStatusFailed, SlipStatusPromoted, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=nonTerminal: failed is in the CLI pre-job's set: claimed", SlipStatusFailed, "", false, nonTerminal, SlipStatusFailed, true, nil},
		{"unclaimed, a step running / if_status=nonTerminal: failed is in the CLI pre-job's set: claimed", SlipStatusFailed, "", true, nonTerminal, SlipStatusFailed, true, nil},
		{"claim held out of failed, quiescent / if_status=nonTerminal: failed is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusFailed, false, nonTerminal, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=nonTerminal: failed is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusFailed, true, nonTerminal, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=nonTerminal: failed is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusPromoted, false, nonTerminal, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=nonTerminal: failed is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusPromoted, true, nonTerminal, SlipStatusPromoted, false, nil},
		{"unclaimed, quiescent / if_status=ended: rerunner retries before anything dispatched: the status never moved, so it claims", SlipStatusFailed, "", false, ended, SlipStatusFailed, true, nil},
		{"unclaimed, a step running / if_status=ended: rerunner retries before anything dispatched: the status never moved, so it claims", SlipStatusFailed, "", true, ended, SlipStatusFailed, true, nil},
		{"claim held out of failed, quiescent / if_status=ended: rerunner retries before anything dispatched: the status never moved, so it claims; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusFailed, false, ended, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=ended: rerunner retries before anything dispatched: the status never moved, so it claims; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusFailed, true, ended, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=ended: rerunner retries before anything dispatched: the status never moved, so it claims; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusPromoted, false, ended, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=ended: rerunner retries before anything dispatched: the status never moved, so it claims; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusPromoted, true, ended, SlipStatusPromoted, false, nil},
		// --- status completed ---
		{"unclaimed, quiescent / if_status=anyStatus: completed with nothing in flight: claimed, an ended slip is not running and the policy is the caller's", SlipStatusCompleted, "", false, anyStatus, SlipStatusCompleted, true, nil},
		{"unclaimed, a step running / if_status=anyStatus: completed while a step of the run is executing: refused — the run is in flight whatever the status column reads, and nothing named it", SlipStatusCompleted, "", true, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=anyStatus: completed with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompleted, SlipStatusFailed, false, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=anyStatus: completed while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusCompleted, SlipStatusFailed, true, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=anyStatus: completed with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompleted, SlipStatusPromoted, false, anyStatus, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=anyStatus: completed while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusCompleted, SlipStatusPromoted, true, anyStatus, SlipStatusPromoted, false, nil},
		{"unclaimed, quiescent / if_status=onlyFailed: completed is outside if_status [failed]: refused", SlipStatusCompleted, "", false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyFailed: completed is outside if_status [failed]: refused", SlipStatusCompleted, "", true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyFailed: completed is outside if_status [failed]: refused", SlipStatusCompleted, SlipStatusFailed, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyFailed: completed is outside if_status [failed]: refused", SlipStatusCompleted, SlipStatusFailed, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyFailed: completed is outside if_status [failed]: refused", SlipStatusCompleted, SlipStatusPromoted, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyFailed: completed is outside if_status [failed]: refused", SlipStatusCompleted, SlipStatusPromoted, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=onlyInProgress: completed is outside if_status [in_progress]: refused", SlipStatusCompleted, "", false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyInProgress: completed is outside if_status [in_progress]: refused", SlipStatusCompleted, "", true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyInProgress: completed is outside if_status [in_progress]: refused", SlipStatusCompleted, SlipStatusFailed, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyInProgress: completed is outside if_status [in_progress]: refused", SlipStatusCompleted, SlipStatusFailed, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyInProgress: completed is outside if_status [in_progress]: refused", SlipStatusCompleted, SlipStatusPromoted, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyInProgress: completed is outside if_status [in_progress]: refused", SlipStatusCompleted, SlipStatusPromoted, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=nonTerminal: completed is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompleted, "", false, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=nonTerminal: completed is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompleted, "", true, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=nonTerminal: completed is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompleted, SlipStatusFailed, false, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=nonTerminal: completed is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompleted, SlipStatusFailed, true, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=nonTerminal: completed is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompleted, SlipStatusPromoted, false, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=nonTerminal: completed is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompleted, SlipStatusPromoted, true, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=ended: completed is in the rerunner's ended set: claimed", SlipStatusCompleted, "", false, ended, SlipStatusCompleted, true, nil},
		{"unclaimed, a step running / if_status=ended: completed is in the rerunner's ended set: claimed", SlipStatusCompleted, "", true, ended, SlipStatusCompleted, true, nil},
		{"claim held out of failed, quiescent / if_status=ended: completed is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompleted, SlipStatusFailed, false, ended, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=ended: completed is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompleted, SlipStatusFailed, true, ended, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=ended: completed is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompleted, SlipStatusPromoted, false, ended, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=ended: completed is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompleted, SlipStatusPromoted, true, ended, SlipStatusPromoted, false, nil},
		// --- status compensated ---
		{"unclaimed, quiescent / if_status=anyStatus: compensated with nothing in flight: claimed, an ended slip is not running and the policy is the caller's", SlipStatusCompensated, "", false, anyStatus, SlipStatusCompensated, true, nil},
		{"unclaimed, a step running / if_status=anyStatus: compensated while a step of the run is executing: refused — the run is in flight whatever the status column reads, and nothing named it", SlipStatusCompensated, "", true, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=anyStatus: compensated with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensated, SlipStatusFailed, false, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=anyStatus: compensated while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusCompensated, SlipStatusFailed, true, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=anyStatus: compensated with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensated, SlipStatusPromoted, false, anyStatus, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=anyStatus: compensated while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusCompensated, SlipStatusPromoted, true, anyStatus, SlipStatusPromoted, false, nil},
		{"unclaimed, quiescent / if_status=onlyFailed: compensated is outside if_status [failed]: refused", SlipStatusCompensated, "", false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyFailed: compensated is outside if_status [failed]: refused", SlipStatusCompensated, "", true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyFailed: compensated is outside if_status [failed]: refused", SlipStatusCompensated, SlipStatusFailed, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyFailed: compensated is outside if_status [failed]: refused", SlipStatusCompensated, SlipStatusFailed, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyFailed: compensated is outside if_status [failed]: refused", SlipStatusCompensated, SlipStatusPromoted, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyFailed: compensated is outside if_status [failed]: refused", SlipStatusCompensated, SlipStatusPromoted, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=onlyInProgress: compensated is outside if_status [in_progress]: refused", SlipStatusCompensated, "", false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyInProgress: compensated is outside if_status [in_progress]: refused", SlipStatusCompensated, "", true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyInProgress: compensated is outside if_status [in_progress]: refused", SlipStatusCompensated, SlipStatusFailed, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyInProgress: compensated is outside if_status [in_progress]: refused", SlipStatusCompensated, SlipStatusFailed, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyInProgress: compensated is outside if_status [in_progress]: refused", SlipStatusCompensated, SlipStatusPromoted, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyInProgress: compensated is outside if_status [in_progress]: refused", SlipStatusCompensated, SlipStatusPromoted, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=nonTerminal: compensated is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompensated, "", false, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=nonTerminal: compensated is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompensated, "", true, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=nonTerminal: compensated is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompensated, SlipStatusFailed, false, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=nonTerminal: compensated is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompensated, SlipStatusFailed, true, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=nonTerminal: compensated is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompensated, SlipStatusPromoted, false, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=nonTerminal: compensated is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompensated, SlipStatusPromoted, true, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=ended: compensated is in the rerunner's ended set: claimed", SlipStatusCompensated, "", false, ended, SlipStatusCompensated, true, nil},
		{"unclaimed, a step running / if_status=ended: compensated is in the rerunner's ended set: claimed", SlipStatusCompensated, "", true, ended, SlipStatusCompensated, true, nil},
		{"claim held out of failed, quiescent / if_status=ended: compensated is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensated, SlipStatusFailed, false, ended, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=ended: compensated is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensated, SlipStatusFailed, true, ended, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=ended: compensated is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensated, SlipStatusPromoted, false, ended, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=ended: compensated is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensated, SlipStatusPromoted, true, ended, SlipStatusPromoted, false, nil},
		// --- status abandoned ---
		{"unclaimed, quiescent / if_status=anyStatus: abandoned with nothing in flight: claimed, an ended slip is not running and the policy is the caller's", SlipStatusAbandoned, "", false, anyStatus, SlipStatusAbandoned, true, nil},
		{"unclaimed, a step running / if_status=anyStatus: abandoned while a step of the run is executing: refused — the run is in flight whatever the status column reads, and nothing named it", SlipStatusAbandoned, "", true, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=anyStatus: abandoned with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusAbandoned, SlipStatusFailed, false, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=anyStatus: abandoned while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusAbandoned, SlipStatusFailed, true, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=anyStatus: abandoned with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusAbandoned, SlipStatusPromoted, false, anyStatus, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=anyStatus: abandoned while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusAbandoned, SlipStatusPromoted, true, anyStatus, SlipStatusPromoted, false, nil},
		{"unclaimed, quiescent / if_status=onlyFailed: abandoned is outside if_status [failed]: refused", SlipStatusAbandoned, "", false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyFailed: abandoned is outside if_status [failed]: refused", SlipStatusAbandoned, "", true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyFailed: abandoned is outside if_status [failed]: refused", SlipStatusAbandoned, SlipStatusFailed, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyFailed: abandoned is outside if_status [failed]: refused", SlipStatusAbandoned, SlipStatusFailed, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyFailed: abandoned is outside if_status [failed]: refused", SlipStatusAbandoned, SlipStatusPromoted, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyFailed: abandoned is outside if_status [failed]: refused", SlipStatusAbandoned, SlipStatusPromoted, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=onlyInProgress: abandoned is outside if_status [in_progress]: refused", SlipStatusAbandoned, "", false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyInProgress: abandoned is outside if_status [in_progress]: refused", SlipStatusAbandoned, "", true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyInProgress: abandoned is outside if_status [in_progress]: refused", SlipStatusAbandoned, SlipStatusFailed, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyInProgress: abandoned is outside if_status [in_progress]: refused", SlipStatusAbandoned, SlipStatusFailed, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyInProgress: abandoned is outside if_status [in_progress]: refused", SlipStatusAbandoned, SlipStatusPromoted, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyInProgress: abandoned is outside if_status [in_progress]: refused", SlipStatusAbandoned, SlipStatusPromoted, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=nonTerminal: abandoned is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusAbandoned, "", false, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=nonTerminal: abandoned is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusAbandoned, "", true, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=nonTerminal: abandoned is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusAbandoned, SlipStatusFailed, false, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=nonTerminal: abandoned is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusAbandoned, SlipStatusFailed, true, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=nonTerminal: abandoned is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusAbandoned, SlipStatusPromoted, false, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=nonTerminal: abandoned is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusAbandoned, SlipStatusPromoted, true, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=ended: abandoned is in the rerunner's ended set: claimed", SlipStatusAbandoned, "", false, ended, SlipStatusAbandoned, true, nil},
		{"unclaimed, a step running / if_status=ended: abandoned is in the rerunner's ended set: claimed", SlipStatusAbandoned, "", true, ended, SlipStatusAbandoned, true, nil},
		{"claim held out of failed, quiescent / if_status=ended: abandoned is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusAbandoned, SlipStatusFailed, false, ended, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=ended: abandoned is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusAbandoned, SlipStatusFailed, true, ended, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=ended: abandoned is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusAbandoned, SlipStatusPromoted, false, ended, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=ended: abandoned is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusAbandoned, SlipStatusPromoted, true, ended, SlipStatusPromoted, false, nil},
		// --- status promoted ---
		{"unclaimed, quiescent / if_status=anyStatus: promoted with nothing in flight: claimed, an ended slip is not running and the policy is the caller's", SlipStatusPromoted, "", false, anyStatus, SlipStatusPromoted, true, nil},
		{"unclaimed, a step running / if_status=anyStatus: promoted while a step of the run is executing: refused — the run is in flight whatever the status column reads, and nothing named it", SlipStatusPromoted, "", true, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=anyStatus: promoted with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPromoted, SlipStatusFailed, false, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=anyStatus: promoted while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusPromoted, SlipStatusFailed, true, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=anyStatus: promoted with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPromoted, SlipStatusPromoted, false, anyStatus, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=anyStatus: promoted while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusPromoted, SlipStatusPromoted, true, anyStatus, SlipStatusPromoted, false, nil},
		{"unclaimed, quiescent / if_status=onlyFailed: promoted is outside if_status [failed]: refused", SlipStatusPromoted, "", false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyFailed: promoted is outside if_status [failed]: refused", SlipStatusPromoted, "", true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyFailed: promoted is outside if_status [failed]: refused", SlipStatusPromoted, SlipStatusFailed, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyFailed: promoted is outside if_status [failed]: refused", SlipStatusPromoted, SlipStatusFailed, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyFailed: promoted is outside if_status [failed]: refused", SlipStatusPromoted, SlipStatusPromoted, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyFailed: promoted is outside if_status [failed]: refused", SlipStatusPromoted, SlipStatusPromoted, true, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=onlyInProgress: promoted is outside if_status [in_progress]: refused", SlipStatusPromoted, "", false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=onlyInProgress: promoted is outside if_status [in_progress]: refused", SlipStatusPromoted, "", true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=onlyInProgress: promoted is outside if_status [in_progress]: refused", SlipStatusPromoted, SlipStatusFailed, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=onlyInProgress: promoted is outside if_status [in_progress]: refused", SlipStatusPromoted, SlipStatusFailed, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=onlyInProgress: promoted is outside if_status [in_progress]: refused", SlipStatusPromoted, SlipStatusPromoted, false, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=onlyInProgress: promoted is outside if_status [in_progress]: refused", SlipStatusPromoted, SlipStatusPromoted, true, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=nonTerminal: promoted is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusPromoted, "", false, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, a step running / if_status=nonTerminal: promoted is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusPromoted, "", true, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, quiescent / if_status=nonTerminal: promoted is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusPromoted, SlipStatusFailed, false, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=nonTerminal: promoted is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusPromoted, SlipStatusFailed, true, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, quiescent / if_status=nonTerminal: promoted is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusPromoted, SlipStatusPromoted, false, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted, a step running / if_status=nonTerminal: promoted is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusPromoted, SlipStatusPromoted, true, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"unclaimed, quiescent / if_status=ended: promoted is in the rerunner's ended set: claimed", SlipStatusPromoted, "", false, ended, SlipStatusPromoted, true, nil},
		{"unclaimed, a step running / if_status=ended: promoted is in the rerunner's ended set: claimed", SlipStatusPromoted, "", true, ended, SlipStatusPromoted, true, nil},
		{"claim held out of failed, quiescent / if_status=ended: promoted is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPromoted, SlipStatusFailed, false, ended, SlipStatusFailed, false, nil},
		{"claim held out of failed, a step running / if_status=ended: promoted is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPromoted, SlipStatusFailed, true, ended, SlipStatusFailed, false, nil},
		{"claim held out of promoted, quiescent / if_status=ended: promoted is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPromoted, SlipStatusPromoted, false, ended, SlipStatusPromoted, false, nil},
		{"claim held out of promoted, a step running / if_status=ended: promoted is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPromoted, SlipStatusPromoted, true, ended, SlipStatusPromoted, false, nil},
		// --- past the cross product: a claim recorded out of in_progress ---
		{"claim held out of in_progress, quiescent / if_status=anyStatus: in_progress with nothing in flight: the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusInProgress, SlipStatusInProgress, false, anyStatus, SlipStatusInProgress, false, nil},
		{"claim held out of in_progress, a step running / if_status=anyStatus: in_progress while a step of the run is executing: the repeat is idempotent — an already-claimed row has nothing left to adopt, so the recorded prior comes back with nothing written and InFlight is what tells the caller not to dispatch", SlipStatusInProgress, SlipStatusInProgress, true, anyStatus, SlipStatusInProgress, false, nil},
		{"claim held out of in_progress, quiescent / if_status=onlyInProgress: the CLI's later pre-job repeats its own claim on the live run it named", SlipStatusInProgress, SlipStatusInProgress, false, onlyInProgress, SlipStatusInProgress, false, nil},
		{"claim held out of in_progress, a step running / if_status=onlyInProgress: the CLI's later pre-job repeats its own claim on the live run it named", SlipStatusInProgress, SlipStatusInProgress, true, onlyInProgress, SlipStatusInProgress, false, nil},
		{"claim held out of failed, quiescent / if_status=[completed]: a second claimant that never agreed to the current status is refused, not handed the claim", SlipStatusFailed, SlipStatusFailed, false, []SlipStatus{SlipStatusCompleted}, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed, a step running / if_status=[completed]: a second claimant that never agreed to the current status is refused, not handed the claim", SlipStatusFailed, SlipStatusFailed, true, []SlipStatus{SlipStatusCompleted}, "", false, ErrClaimPreconditionFailed},
		// --- past the cross product: the claim values the CLI pre-job leaves behind ---
		{"claim held out of pending, quiescent / if_status=nonTerminal: the CLI's later pre-job repeats the claim its first one took at pending", SlipStatusPending, SlipStatusPending, false, nonTerminal, SlipStatusPending, false, nil},
		{"claim held out of pending, a step running / if_status=nonTerminal: the CLI's later pre-job repeats it mid-run: the status it named still matches, so a run in flight is no refusal", SlipStatusPending, SlipStatusPending, true, nonTerminal, SlipStatusPending, false, nil},
		{"claim held out of pending, a step running / if_status=anyStatus: the same row, asked without an if_status: still the idempotent repeat — the in-flight refusal guards ADOPTION, and this row is already claimed", SlipStatusPending, SlipStatusPending, true, anyStatus, SlipStatusPending, false, nil},
		{"claim held out of pending, a step running / if_status=ended: THE CELL A0 WAS ABOUT: the rerunner's second message finds the slip still reading failed while the first dispatch runs; the compare-and-set agrees, the repeat arm answers, and it is ClaimOutcome.InFlight — not this decision — that stops the second dispatch", SlipStatusFailed, SlipStatusPending, true, ended, SlipStatusPending, false, nil},
		{"claim held out of pending, quiescent / if_status=ended: the same retry in the window where nothing has started: the same repeat arm, and InFlight false is what lets the caller dispatch", SlipStatusFailed, SlipStatusPending, false, ended, SlipStatusPending, false, nil},
		{"claim held out of compensating, quiescent / if_status=nonTerminal: a compensation's later pre-job repeats its own claim", SlipStatusCompensating, SlipStatusCompensating, false, nonTerminal, SlipStatusCompensating, false, nil},
		{"claim held out of compensating, a step running / if_status=ended: the compensation ended compensated and a rerun repeats onto it while a step still runs", SlipStatusCompensated, SlipStatusCompensating, true, ended, SlipStatusCompensating, false, nil},
		{"claim held out of compensating, quiescent / if_status=onlyFailed: compensated is outside if_status [failed]: refused, because expected is compared against the CURRENT status and never against the recorded prior", SlipStatusCompensated, SlipStatusCompensating, false, onlyFailed, "", false, ErrClaimPreconditionFailed},
		// --- past the cross product: the arms the in-flight refusal's reorder turns on (finding j-claim) ---
		{"claim held out of failed, a step running / if_status=[] (empty, not nil): an EMPTY if_status reaches the in-flight arm exactly as a nil one does, and an already-claimed row takes the repeat arm in front of it", SlipStatusFailed, SlipStatusFailed, true, []SlipStatus{}, SlipStatusFailed, false, nil},
		{"unclaimed, a step running / if_status=[] (empty, not nil): the same empty set on an UNCLAIMED row is still refused — this is the arm's whole remaining job, refusing a caller that would ADOPT a run it does not hold", SlipStatusFailed, "", true, []SlipStatus{}, "", false, ErrClaimPreconditionFailed},
		{"claim held out of pending, a step running / if_status=anyStatus: THE REPORTED CELL: pre-job 1 claimed with a nil if_status and its StartStep wrote builds=running; pre-job 2 of the SAME run repeats with a nil if_status and gets the documented idempotent no-op, not ErrClaimPreconditionFailed", SlipStatusPending, SlipStatusPending, true, anyStatus, SlipStatusPending, false, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prior, write, err := DecideClaim(tc.status, tc.claimedFrom, tc.inFlight, tc.expected)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Empty(t, prior, "a refused claim reports no prior")
				assert.False(t, write, "a refused claim writes nothing")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantPrior, prior)
			assert.Equal(t, tc.wantWrite, write)
		})
	}
}

func TestDecideRelease(t *testing.T) {
	running := map[string]Step{"builds": {Status: StepStatusRunning}}
	quiet := map[string]Step{"builds": {Status: StepStatusFailed}, "unit_tests": {Status: StepStatusPending}}

	release, err := DecideRelease(nil)
	assert.ErrorIs(t, err, ErrSlipNotFound, "a nil slip is not 'nothing to release'")
	assert.False(t, release)

	release, err = DecideRelease(&Slip{Steps: quiet})
	assert.ErrorIs(t, err, ErrNotClaimed, "no claim, nothing to release")
	assert.False(t, release)

	// Inverted deliberately (PR #87 re-review): work in flight used to be ErrRunInFlight.
	// It is an outcome, not an error — N-1 of N post-job releases take this arm.
	release, err = DecideRelease(&Slip{ClaimedFrom: SlipStatusFailed, Steps: running})
	require.NoError(t, err, "work in flight is not an error")
	assert.False(t, release, "and nothing is written")

	release, err = DecideRelease(&Slip{ClaimedFrom: SlipStatusFailed, Steps: map[string]Step{"d": {Status: StepStatusHeld}}})
	require.NoError(t, err)
	assert.False(t, release, "a held step is work the run committed to")

	release, err = DecideRelease(&Slip{ClaimedFrom: SlipStatusFailed, Steps: quiet})
	require.NoError(t, err)
	assert.True(t, release, "quiescent: clear")

	release, err = DecideRelease(&Slip{ClaimedFrom: SlipStatusFailed})
	require.NoError(t, err)
	assert.True(t, release, "no steps at all is quiescent")
}

// PostgresStore.ReleaseClaim hands DecideRelease a Slip hydrated from claimStateColumns()
// alone — ClaimedFrom, Status, each step's Status and the aggregate components — while both
// test doubles hand it a fully loaded row. The two agree today only because DecideRelease
// reads nothing outside that set; if its read set ever widened, the doubles would keep
// passing while the store silently decided on zero values. This pins the coupling: the same
// answers for a full row and for one stripped to claimStateColumns()' fields.
func TestDecideRelease_ReadsOnlyClaimStateFields(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	hydrate := func(status SlipStatus, claimedFrom SlipStatus, steps map[string]Step,
		aggs map[string][]ComponentStepData,
	) *Slip {
		return &Slip{
			CorrelationID: "corr-full",
			Repository:    "Owner/Repo",
			Branch:        "main",
			CommitSHA:     "0123456789abcdef",
			CreatedAt:     now,
			UpdatedAt:     now.Add(time.Hour),
			Status:        status,
			ClaimedFrom:   claimedFrom,
			PromotedTo:    "corr-successor",
			Steps:         steps,
			Aggregates:    aggs,
			StateHistory: []StateHistoryEntry{
				{Step: ClaimMarkerStep, Status: StepStatusRunning, Timestamp: now, Actor: "rerunner"},
				{Step: "builds", Status: StepStatusRunning, Timestamp: now, Actor: "ci"},
			},
			Ancestry: []AncestryEntry{{CorrelationID: "corr-parent", CommitSHA: "beef", Status: SlipStatusPromoted}},
		}
	}
	// strip zeroes everything outside {Status, ClaimedFrom, Steps[*].Status, Aggregates[*][].Status}.
	strip := func(full *Slip) *Slip {
		bare := &Slip{Status: full.Status, ClaimedFrom: full.ClaimedFrom}
		if full.Steps != nil {
			bare.Steps = make(map[string]Step, len(full.Steps))
			for name, step := range full.Steps {
				bare.Steps[name] = Step{Status: step.Status}
			}
		}
		if full.Aggregates != nil {
			bare.Aggregates = make(map[string][]ComponentStepData, len(full.Aggregates))
			for name, components := range full.Aggregates {
				stripped := make([]ComponentStepData, len(components))
				for i, c := range components {
					stripped[i] = ComponentStepData{Status: c.Status}
				}
				bare.Aggregates[name] = stripped
			}
		}
		return bare
	}

	timed := func(status StepStatus) Step {
		return Step{Status: status, StartedAt: &now, CompletedAt: &now, Actor: "ci", Error: "boom"}
	}
	tests := []struct {
		name string
		full *Slip
	}{
		{"unclaimed", hydrate(SlipStatusFailed, "", map[string]Step{
			"builds": timed(StepStatusFailed), "unit_tests": timed(StepStatusPending),
		}, nil)},
		{"claimed with a component still running", hydrate(SlipStatusFailed, SlipStatusFailed, map[string]Step{
			"builds": timed(StepStatusFailed),
		}, map[string][]ComponentStepData{"builds": {
			{Component: "api", Status: StepStatusFailed, ImageTag: "sha-1"},
			{Component: "web", Status: StepStatusRunning, ImageTag: "sha-2"},
		}})},
		{"claimed and quiescent", hydrate(SlipStatusCompleted, SlipStatusFailed, map[string]Step{
			"builds": timed(StepStatusCompleted), "unit_tests": timed(StepStatusSkipped),
		}, map[string][]ComponentStepData{"builds": {{Component: "api", Status: StepStatusCompleted}}})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bare := strip(tc.full)
			fullRelease, fullErr := DecideRelease(tc.full)
			bareRelease, bareErr := DecideRelease(bare)
			assert.Equal(t, fullRelease, bareRelease, "DecideRelease must read nothing outside claimStateColumns()")
			assert.Equal(t, fullErr, bareErr)
			assert.Equal(t, RunInFlight(tc.full), RunInFlight(bare), "nor may RunInFlight")
		})
	}
}
