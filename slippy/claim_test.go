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
