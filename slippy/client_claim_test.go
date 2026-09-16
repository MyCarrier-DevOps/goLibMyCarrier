package slippy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PromoteSlip reaches a terminal status through the full-row Update, not UpdateSlipStatus.
// Both paths must end the claim, or a claimed slip promoted mid-run would keep refusing
// repaves forever with no client left to release it (PR #87 review).
func TestClient_PromoteSlip_EndsTheClaim(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{})
	store.AddSlip(&Slip{CorrelationID: "p", Repository: "o/r", Branch: "main", CommitSHA: "s", Status: SlipStatusFailed})
	_, err := store.ClaimSlip(ctx, "p", nil, "rerunner", "")
	require.NoError(t, err)

	require.NoError(t, client.PromoteSlip(ctx, "p", "release/1.2"))
	got, err := store.Load(ctx, "p")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusPromoted, got.Status)
	assert.Empty(t, got.ClaimedFrom, "terminal through Update ends the claim like UpdateSlipStatus does")
	_, err = store.ClaimSlip(ctx, "p", []SlipStatus{SlipStatusFailed}, "rerunner", "")
	require.ErrorIs(t, err, ErrClaimPreconditionFailed, "and a later claim sees the real status, not a stale no-op arm")
}

// The client wrappers open spans and surface the store's decisions unchanged.
func TestClient_ClaimAndRelease_SurfaceStoreDecisions(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{})
	store.AddSlip(&Slip{CorrelationID: "c", Status: SlipStatusFailed, Steps: map[string]Step{"builds": {Status: StepStatusRunning}}})

	prior, err := client.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusFailed}, "cli", "")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusFailed, prior)
	_, err = client.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusCompleted}, "cli", "")
	require.ErrorIs(t, err, ErrClaimPreconditionFailed)

	_, err = client.ReleaseClaim(ctx, "c", "cli", "")
	require.ErrorIs(t, err, ErrRunInFlight)
	require.NoError(t, store.UpdateStep(ctx, "c", "builds", "", StepStatusCompleted))
	status, err := client.ReleaseClaim(ctx, "c", "cli", "")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusFailed, status)
	_, err = client.ReleaseClaim(ctx, "c", "cli", "")
	require.ErrorIs(t, err, ErrNotClaimed)
}
