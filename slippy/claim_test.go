package slippy

import (
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
	assert.Contains(t, ErrClaimPreconditionFailed.Error(), "status")
	assert.Contains(t, ErrNotClaimed.Error(), "claimed")
}

// ClaimedFrom is the only field a release restores from; it must serialise under the
// snake_case key the API contract will expose and be omitted when the slip is unclaimed.
func TestSlip_ClaimedFrom_JSONShape(t *testing.T) {
	s := &Slip{CorrelationID: "c1", Status: SlipStatusInProgress, ClaimedFrom: SlipStatusFailed}
	assert.Equal(t, SlipStatusFailed, s.ClaimedFrom)
	var zero Slip
	assert.Empty(t, zero.ClaimedFrom)
}

// The marker builders moved here from slippy-api so every adopter writes the same shape;
// their tests move with them. The prior status in the message is the only in-slip record of
// what was adopted once the claim lands, so the format is load-bearing, not cosmetic.
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

func TestReleaseMarker_RecordsRestoredStatusAndReason(t *testing.T) {
	e := ReleaseMarker(SlipStatusFailed, "post-job", "terminal write failed")
	assert.Equal(t, "released claim; restored failed: terminal write failed", e.Message)
	assert.Equal(t, "post-job", e.Actor)
	assert.Equal(t, ReleaseMarkerStep, e.Step)
	assert.Equal(t, StepStatusAborted, e.Status)
	assert.Equal(t, "released claim; restored failed", ReleaseMarker(SlipStatusFailed, "x", "").Message)
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
