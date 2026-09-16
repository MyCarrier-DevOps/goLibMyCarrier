package slippy

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The claim sentinels must be distinct from each other and from the repave sentinel they
// sit beside: callers branch on errors.Is, so two sentinels that compared equal would make a
// precondition failure indistinguishable from a live-run rejection.
func TestClaimSentinelsAreDistinct(t *testing.T) {
	require.NotErrorIs(t, ErrClaimPreconditionFailed, ErrSlipWentLive)
	require.NotErrorIs(t, ErrNotClaimed, ErrClaimPreconditionFailed)
	require.NotErrorIs(t, ErrNotClaimed, ErrSlipNotFound)
	require.NotErrorIs(t, ErrRunInFlight, ErrNotClaimed)
	assert.Contains(t, ErrClaimPreconditionFailed.Error(), "status")
	assert.Contains(t, ErrNotClaimed.Error(), "claimed")
	assert.Contains(t, ErrRunInFlight.Error(), "in flight")
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
		slip Slip
		want bool
	}{
		{"nothing", Slip{Steps: map[string]Step{"builds": {Status: StepStatusFailed}, "unit_tests": {Status: StepStatusPending}}}, false},
		{"a running step", Slip{Steps: map[string]Step{"builds": {Status: StepStatusRunning}}}, true},
		{"a held step", Slip{Steps: map[string]Step{"dev_deploy": {Status: StepStatusHeld}}}, true},
		{"a running component under a failed aggregate", Slip{
			Steps:      map[string]Step{"builds": {Status: StepStatusFailed}},
			Aggregates: map[string][]ComponentStepData{"builds": {{Component: "api", Status: StepStatusFailed}, {Component: "web", Status: StepStatusRunning}}},
		}, true},
		{"completed and skipped only", Slip{Steps: map[string]Step{"builds": {Status: StepStatusCompleted}, "secretscan": {Status: StepStatusSkipped}}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, RunInFlight(&tc.slip))
		})
	}
}

// DecideClaim is shared by the store and both doubles; this pins the one decision they share.
func TestDecideClaim(t *testing.T) {
	tests := []struct {
		name        string
		status      SlipStatus
		claimedFrom SlipStatus
		expected    []SlipStatus
		wantPrior   SlipStatus
		wantWrite   bool
		wantErr     error
	}{
		{"fresh claim out of failed", SlipStatusFailed, "", []SlipStatus{SlipStatusFailed}, SlipStatusFailed, true, nil},
		{"nil expected admits a live in_progress", SlipStatusInProgress, "", nil, SlipStatusInProgress, true, nil},
		{"nil expected admits promoted", SlipStatusPromoted, "", nil, SlipStatusPromoted, true, nil},
		{"mismatch writes nothing", SlipStatusAbandoned, "", []SlipStatus{SlipStatusFailed}, "", false, ErrClaimPreconditionFailed},
		{"repeat claim returns the recorded prior, no write", SlipStatusFailed, SlipStatusFailed, []SlipStatus{SlipStatusFailed}, SlipStatusFailed, false, nil},
		{"repeat after the run moved status: prior is the recorded one", SlipStatusInProgress, SlipStatusFailed, nil, SlipStatusFailed, false, nil},
		{"repeat checks expected against the current status", SlipStatusInProgress, SlipStatusFailed, []SlipStatus{SlipStatusFailed}, "", false, ErrClaimPreconditionFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prior, write, err := DecideClaim(tc.status, tc.claimedFrom, tc.expected)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.False(t, write)
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
	assert.ErrorIs(t, DecideRelease(&Slip{Steps: quiet}), ErrNotClaimed, "no claim, nothing to release")
	assert.ErrorIs(t, DecideRelease(&Slip{ClaimedFrom: SlipStatusFailed, Steps: running}), ErrRunInFlight)
	assert.ErrorIs(t, DecideRelease(&Slip{ClaimedFrom: SlipStatusFailed, Steps: map[string]Step{"d": {Status: StepStatusHeld}}}), ErrRunInFlight)
	assert.NoError(t, DecideRelease(&Slip{ClaimedFrom: SlipStatusFailed, Steps: quiet}), "quiescent: clear")
	assert.NoError(t, DecideRelease(&Slip{ClaimedFrom: SlipStatusFailed}), "no steps at all is quiescent")
}
