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

// TestDecideClaim_Table is the whole decision space, cell by cell: every status crossed with
// every claim state crossed with the if_status sets the real callers send. It exists because
// the claim decision regressed three review rounds running, each time because one arm was
// fixed in isolation (PR #87, sixth review). Cells, not narrative cases, are what stop that:
// a change to one arm that breaks another now fails here rather than in production.
//
// The rule every cell follows, in this order: an empty status is refused outright; a
// non-empty if_status is a compare-and-set on the CURRENT status whether or not a claim is
// held; a live status (in_progress, compensating — pending is deliberately claimable) is
// refused unless the caller named it; and only then does a held claim take the idempotent
// no-op arm, returning the RECORDED prior with nothing written.
//
// claimedFrom runs over {unclaimed, failed, promoted}: the two claim values a real caller can
// leave behind (the rerunner claims out of an ended status, the CLI pre-job out of a
// non-terminal one). Three cells past the cross product cover claimedFrom = in_progress.
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
		expected    []SlipStatus
		wantPrior   SlipStatus
		wantWrite   bool
		wantErr     error
	}{
		// --- status "" ---
		{"unclaimed / if_status=anyStatus: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", "", anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=anyStatus: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusFailed, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=anyStatus: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusPromoted, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=onlyFailed: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", "", onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyFailed: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusFailed, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyFailed: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusPromoted, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=onlyInProgress: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", "", onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyInProgress: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusFailed, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyInProgress: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusPromoted, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=nonTerminal: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", "", nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=nonTerminal: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusFailed, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=nonTerminal: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusPromoted, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=ended: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", "", ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=ended: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusFailed, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=ended: no status at all: refused whatever is asked, an empty status cannot be recorded as a claim", "", SlipStatusPromoted, ended, "", false, ErrClaimPreconditionFailed},
		// --- status pending ---
		{"unclaimed / if_status=anyStatus: pending, no if_status: claimable by design, a pending slip has no run to dispatch onto", SlipStatusPending, "", anyStatus, SlipStatusPending, true, nil},
		{"claim held out of failed / if_status=anyStatus: pending, no if_status: claimable by design, a pending slip has no run to dispatch onto; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPending, SlipStatusFailed, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=anyStatus: pending, no if_status: claimable by design, a pending slip has no run to dispatch onto; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPending, SlipStatusPromoted, anyStatus, SlipStatusPromoted, false, nil},
		{"unclaimed / if_status=onlyFailed: pending is outside if_status [failed]: refused", SlipStatusPending, "", onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyFailed: pending is outside if_status [failed]: refused", SlipStatusPending, SlipStatusFailed, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyFailed: pending is outside if_status [failed]: refused", SlipStatusPending, SlipStatusPromoted, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=onlyInProgress: pending is outside if_status [in_progress]: refused", SlipStatusPending, "", onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyInProgress: pending is outside if_status [in_progress]: refused", SlipStatusPending, SlipStatusFailed, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyInProgress: pending is outside if_status [in_progress]: refused", SlipStatusPending, SlipStatusPromoted, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=nonTerminal: pending is in the CLI pre-job's set: claimed", SlipStatusPending, "", nonTerminal, SlipStatusPending, true, nil},
		{"claim held out of failed / if_status=nonTerminal: pending is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPending, SlipStatusFailed, nonTerminal, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=nonTerminal: pending is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPending, SlipStatusPromoted, nonTerminal, SlipStatusPromoted, false, nil},
		{"unclaimed / if_status=ended: pending is outside the rerunner's ended set: refused", SlipStatusPending, "", ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=ended: pending is outside the rerunner's ended set: refused", SlipStatusPending, SlipStatusFailed, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=ended: pending is outside the rerunner's ended set: refused", SlipStatusPending, SlipStatusPromoted, ended, "", false, ErrClaimPreconditionFailed},
		// --- status in_progress ---
		{"unclaimed / if_status=anyStatus: in_progress with no if_status is a live run: refused, never adopted silently", SlipStatusInProgress, "", anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=anyStatus: in_progress with no if_status is a live run: refused, never adopted silently", SlipStatusInProgress, SlipStatusFailed, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=anyStatus: in_progress with no if_status is a live run: refused, never adopted silently", SlipStatusInProgress, SlipStatusPromoted, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=onlyFailed: in_progress is outside if_status [failed]: refused, the status moved off what was agreed", SlipStatusInProgress, "", onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyFailed: in_progress is outside if_status [failed]: refused, the status moved off what was agreed", SlipStatusInProgress, SlipStatusFailed, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyFailed: in_progress is outside if_status [failed]: refused, the status moved off what was agreed", SlipStatusInProgress, SlipStatusPromoted, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=onlyInProgress: in_progress is named explicitly: a live run the caller asked for", SlipStatusInProgress, "", onlyInProgress, SlipStatusInProgress, true, nil},
		{"claim held out of failed / if_status=onlyInProgress: in_progress is named explicitly: a live run the caller asked for; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusInProgress, SlipStatusFailed, onlyInProgress, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=onlyInProgress: in_progress is named explicitly: a live run the caller asked for; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusInProgress, SlipStatusPromoted, onlyInProgress, SlipStatusPromoted, false, nil},
		{"unclaimed / if_status=nonTerminal: in_progress is in the CLI pre-job's set: a later phase claims the live run it named", SlipStatusInProgress, "", nonTerminal, SlipStatusInProgress, true, nil},
		{"claim held out of failed / if_status=nonTerminal: in_progress is in the CLI pre-job's set: a later phase claims the live run it named; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusInProgress, SlipStatusFailed, nonTerminal, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=nonTerminal: in_progress is in the CLI pre-job's set: a later phase claims the live run it named; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusInProgress, SlipStatusPromoted, nonTerminal, SlipStatusPromoted, false, nil},
		{"unclaimed / if_status=ended: rerunner retries after its dispatch already started: refused, the work is running", SlipStatusInProgress, "", ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=ended: rerunner retries after its dispatch already started: refused, the work is running", SlipStatusInProgress, SlipStatusFailed, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=ended: rerunner retries after its dispatch already started: refused, the work is running", SlipStatusInProgress, SlipStatusPromoted, ended, "", false, ErrClaimPreconditionFailed},
		// --- status compensating ---
		{"unclaimed / if_status=anyStatus: compensating with no if_status is a live run too: refused", SlipStatusCompensating, "", anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=anyStatus: compensating with no if_status is a live run too: refused", SlipStatusCompensating, SlipStatusFailed, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=anyStatus: compensating with no if_status is a live run too: refused", SlipStatusCompensating, SlipStatusPromoted, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=onlyFailed: compensating is outside if_status [failed]: refused", SlipStatusCompensating, "", onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyFailed: compensating is outside if_status [failed]: refused", SlipStatusCompensating, SlipStatusFailed, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyFailed: compensating is outside if_status [failed]: refused", SlipStatusCompensating, SlipStatusPromoted, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=onlyInProgress: compensating is outside if_status [in_progress]: refused", SlipStatusCompensating, "", onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyInProgress: compensating is outside if_status [in_progress]: refused", SlipStatusCompensating, SlipStatusFailed, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyInProgress: compensating is outside if_status [in_progress]: refused", SlipStatusCompensating, SlipStatusPromoted, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=nonTerminal: compensating is in the CLI pre-job's set: claimed", SlipStatusCompensating, "", nonTerminal, SlipStatusCompensating, true, nil},
		{"claim held out of failed / if_status=nonTerminal: compensating is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensating, SlipStatusFailed, nonTerminal, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=nonTerminal: compensating is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensating, SlipStatusPromoted, nonTerminal, SlipStatusPromoted, false, nil},
		{"unclaimed / if_status=ended: compensating is outside the rerunner's ended set: refused", SlipStatusCompensating, "", ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=ended: compensating is outside the rerunner's ended set: refused", SlipStatusCompensating, SlipStatusFailed, ended, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=ended: compensating is outside the rerunner's ended set: refused", SlipStatusCompensating, SlipStatusPromoted, ended, "", false, ErrClaimPreconditionFailed},
		// --- status failed ---
		{"unclaimed / if_status=anyStatus: failed with no if_status: claimed, a failed run is not live", SlipStatusFailed, "", anyStatus, SlipStatusFailed, true, nil},
		{"claim held out of failed / if_status=anyStatus: failed with no if_status: claimed, a failed run is not live; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusFailed, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=anyStatus: failed with no if_status: claimed, a failed run is not live; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusPromoted, anyStatus, SlipStatusPromoted, false, nil},
		{"unclaimed / if_status=onlyFailed: failed named exactly: the rerunner's fresh claim", SlipStatusFailed, "", onlyFailed, SlipStatusFailed, true, nil},
		{"claim held out of failed / if_status=onlyFailed: failed named exactly: the rerunner's fresh claim; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusFailed, onlyFailed, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=onlyFailed: failed named exactly: the rerunner's fresh claim; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusPromoted, onlyFailed, SlipStatusPromoted, false, nil},
		{"unclaimed / if_status=onlyInProgress: failed is outside if_status [in_progress]: refused", SlipStatusFailed, "", onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyInProgress: failed is outside if_status [in_progress]: refused", SlipStatusFailed, SlipStatusFailed, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyInProgress: failed is outside if_status [in_progress]: refused", SlipStatusFailed, SlipStatusPromoted, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=nonTerminal: failed is in the CLI pre-job's set: claimed", SlipStatusFailed, "", nonTerminal, SlipStatusFailed, true, nil},
		{"claim held out of failed / if_status=nonTerminal: failed is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusFailed, nonTerminal, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=nonTerminal: failed is in the CLI pre-job's set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusPromoted, nonTerminal, SlipStatusPromoted, false, nil},
		{"unclaimed / if_status=ended: rerunner retries before anything dispatched: the status never moved, so it claims", SlipStatusFailed, "", ended, SlipStatusFailed, true, nil},
		{"claim held out of failed / if_status=ended: rerunner retries before anything dispatched: the status never moved, so it claims; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusFailed, ended, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=ended: rerunner retries before anything dispatched: the status never moved, so it claims; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusFailed, SlipStatusPromoted, ended, SlipStatusPromoted, false, nil},
		// --- status completed ---
		{"unclaimed / if_status=anyStatus: completed with no if_status: claimed, an ended slip is not live and the policy is the caller's", SlipStatusCompleted, "", anyStatus, SlipStatusCompleted, true, nil},
		{"claim held out of failed / if_status=anyStatus: completed with no if_status: claimed, an ended slip is not live and the policy is the caller's; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompleted, SlipStatusFailed, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=anyStatus: completed with no if_status: claimed, an ended slip is not live and the policy is the caller's; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompleted, SlipStatusPromoted, anyStatus, SlipStatusPromoted, false, nil},
		{"unclaimed / if_status=onlyFailed: completed is outside if_status [failed]: refused", SlipStatusCompleted, "", onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyFailed: completed is outside if_status [failed]: refused", SlipStatusCompleted, SlipStatusFailed, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyFailed: completed is outside if_status [failed]: refused", SlipStatusCompleted, SlipStatusPromoted, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=onlyInProgress: completed is outside if_status [in_progress]: refused", SlipStatusCompleted, "", onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyInProgress: completed is outside if_status [in_progress]: refused", SlipStatusCompleted, SlipStatusFailed, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyInProgress: completed is outside if_status [in_progress]: refused", SlipStatusCompleted, SlipStatusPromoted, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=nonTerminal: completed is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompleted, "", nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=nonTerminal: completed is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompleted, SlipStatusFailed, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=nonTerminal: completed is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompleted, SlipStatusPromoted, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=ended: completed is in the rerunner's ended set: claimed", SlipStatusCompleted, "", ended, SlipStatusCompleted, true, nil},
		{"claim held out of failed / if_status=ended: completed is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompleted, SlipStatusFailed, ended, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=ended: completed is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompleted, SlipStatusPromoted, ended, SlipStatusPromoted, false, nil},
		// --- status compensated ---
		{"unclaimed / if_status=anyStatus: compensated with no if_status: claimed, an ended slip is not live and the policy is the caller's", SlipStatusCompensated, "", anyStatus, SlipStatusCompensated, true, nil},
		{"claim held out of failed / if_status=anyStatus: compensated with no if_status: claimed, an ended slip is not live and the policy is the caller's; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensated, SlipStatusFailed, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=anyStatus: compensated with no if_status: claimed, an ended slip is not live and the policy is the caller's; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensated, SlipStatusPromoted, anyStatus, SlipStatusPromoted, false, nil},
		{"unclaimed / if_status=onlyFailed: compensated is outside if_status [failed]: refused", SlipStatusCompensated, "", onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyFailed: compensated is outside if_status [failed]: refused", SlipStatusCompensated, SlipStatusFailed, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyFailed: compensated is outside if_status [failed]: refused", SlipStatusCompensated, SlipStatusPromoted, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=onlyInProgress: compensated is outside if_status [in_progress]: refused", SlipStatusCompensated, "", onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyInProgress: compensated is outside if_status [in_progress]: refused", SlipStatusCompensated, SlipStatusFailed, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyInProgress: compensated is outside if_status [in_progress]: refused", SlipStatusCompensated, SlipStatusPromoted, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=nonTerminal: compensated is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompensated, "", nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=nonTerminal: compensated is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompensated, SlipStatusFailed, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=nonTerminal: compensated is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusCompensated, SlipStatusPromoted, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=ended: compensated is in the rerunner's ended set: claimed", SlipStatusCompensated, "", ended, SlipStatusCompensated, true, nil},
		{"claim held out of failed / if_status=ended: compensated is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensated, SlipStatusFailed, ended, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=ended: compensated is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusCompensated, SlipStatusPromoted, ended, SlipStatusPromoted, false, nil},
		// --- status abandoned ---
		{"unclaimed / if_status=anyStatus: abandoned with no if_status: claimed, an ended slip is not live and the policy is the caller's", SlipStatusAbandoned, "", anyStatus, SlipStatusAbandoned, true, nil},
		{"claim held out of failed / if_status=anyStatus: abandoned with no if_status: claimed, an ended slip is not live and the policy is the caller's; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusAbandoned, SlipStatusFailed, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=anyStatus: abandoned with no if_status: claimed, an ended slip is not live and the policy is the caller's; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusAbandoned, SlipStatusPromoted, anyStatus, SlipStatusPromoted, false, nil},
		{"unclaimed / if_status=onlyFailed: abandoned is outside if_status [failed]: refused", SlipStatusAbandoned, "", onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyFailed: abandoned is outside if_status [failed]: refused", SlipStatusAbandoned, SlipStatusFailed, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyFailed: abandoned is outside if_status [failed]: refused", SlipStatusAbandoned, SlipStatusPromoted, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=onlyInProgress: abandoned is outside if_status [in_progress]: refused", SlipStatusAbandoned, "", onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyInProgress: abandoned is outside if_status [in_progress]: refused", SlipStatusAbandoned, SlipStatusFailed, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyInProgress: abandoned is outside if_status [in_progress]: refused", SlipStatusAbandoned, SlipStatusPromoted, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=nonTerminal: abandoned is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusAbandoned, "", nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=nonTerminal: abandoned is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusAbandoned, SlipStatusFailed, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=nonTerminal: abandoned is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusAbandoned, SlipStatusPromoted, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=ended: abandoned is in the rerunner's ended set: claimed", SlipStatusAbandoned, "", ended, SlipStatusAbandoned, true, nil},
		{"claim held out of failed / if_status=ended: abandoned is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusAbandoned, SlipStatusFailed, ended, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=ended: abandoned is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusAbandoned, SlipStatusPromoted, ended, SlipStatusPromoted, false, nil},
		// --- status promoted ---
		{"unclaimed / if_status=anyStatus: promoted with no if_status: claimed, an ended slip is not live and the policy is the caller's", SlipStatusPromoted, "", anyStatus, SlipStatusPromoted, true, nil},
		{"claim held out of failed / if_status=anyStatus: promoted with no if_status: claimed, an ended slip is not live and the policy is the caller's; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPromoted, SlipStatusFailed, anyStatus, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=anyStatus: promoted with no if_status: claimed, an ended slip is not live and the policy is the caller's; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPromoted, SlipStatusPromoted, anyStatus, SlipStatusPromoted, false, nil},
		{"unclaimed / if_status=onlyFailed: promoted is outside if_status [failed]: refused", SlipStatusPromoted, "", onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyFailed: promoted is outside if_status [failed]: refused", SlipStatusPromoted, SlipStatusFailed, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyFailed: promoted is outside if_status [failed]: refused", SlipStatusPromoted, SlipStatusPromoted, onlyFailed, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=onlyInProgress: promoted is outside if_status [in_progress]: refused", SlipStatusPromoted, "", onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=onlyInProgress: promoted is outside if_status [in_progress]: refused", SlipStatusPromoted, SlipStatusFailed, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=onlyInProgress: promoted is outside if_status [in_progress]: refused", SlipStatusPromoted, SlipStatusPromoted, onlyInProgress, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=nonTerminal: promoted is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusPromoted, "", nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of failed / if_status=nonTerminal: promoted is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusPromoted, SlipStatusFailed, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"claim held out of promoted / if_status=nonTerminal: promoted is outside the CLI pre-job's set: refused, a terminal slip is never claimed", SlipStatusPromoted, SlipStatusPromoted, nonTerminal, "", false, ErrClaimPreconditionFailed},
		{"unclaimed / if_status=ended: promoted is in the rerunner's ended set: claimed", SlipStatusPromoted, "", ended, SlipStatusPromoted, true, nil},
		{"claim held out of failed / if_status=ended: promoted is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPromoted, SlipStatusFailed, ended, SlipStatusFailed, false, nil},
		{"claim held out of promoted / if_status=ended: promoted is in the rerunner's ended set: claimed; the repeat is idempotent, the recorded prior comes back, nothing written", SlipStatusPromoted, SlipStatusPromoted, ended, SlipStatusPromoted, false, nil},
		// --- past the cross product: a claim recorded out of in_progress ---
		{"claim held out of in_progress / if_status=anyStatus: a recorded claim is no exemption, a live run is still refused when nothing names it", SlipStatusInProgress, SlipStatusInProgress, anyStatus, "", false, ErrClaimPreconditionFailed},
		{"claim held out of in_progress / if_status=onlyInProgress: the CLI's later pre-job repeats its own claim on the live run it named", SlipStatusInProgress, SlipStatusInProgress, onlyInProgress, SlipStatusInProgress, false, nil},
		{"claim held out of failed / if_status=[completed]: a second claimant that never agreed to the current status is refused, not handed the claim", SlipStatusFailed, SlipStatusFailed, []SlipStatus{SlipStatusCompleted}, "", false, ErrClaimPreconditionFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prior, write, err := DecideClaim(tc.status, tc.claimedFrom, tc.expected)
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
