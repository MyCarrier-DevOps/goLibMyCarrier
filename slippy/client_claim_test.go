package slippy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PromoteSlip reaches a terminal status through UpdateSlipStatus, the one write path that
// ends a claim. It must end it, or a claimed slip promoted mid-run would keep refusing
// repaves forever with no client left to release it (PR #87 review). PromotedTo is asserted
// as the status write rather than a round-trip: no store persists that field (no column), so
// the full-row Update this replaced never carried it to the database either — it only added a
// Load-then-Update snapshot race.
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
	assert.Empty(t, got.ClaimedFrom, "the atomic terminal status write ends the claim")
	assert.Empty(t, store.UpdateCalls, "promote no longer takes the full-row Update path")
	_, err = store.ClaimSlip(ctx, "p", []SlipStatus{SlipStatusFailed}, "rerunner", "")
	require.ErrorIs(t, err, ErrClaimPreconditionFailed, "and a later claim sees the real status, not a stale no-op arm")
}

// The client wrappers open spans and surface the store's decisions unchanged.
func TestClient_ClaimAndRelease_SurfaceStoreDecisions(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{})
	store.AddSlip(&Slip{CorrelationID: "c", Status: SlipStatusFailed, Steps: map[string]Step{"builds": {Status: StepStatusRunning}}})

	claim, err := client.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusFailed}, "cli", "")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusFailed, claim.Prior)
	assert.True(t, claim.Claimed, "this call recorded the claim")
	// The repeat arm is reached only once expected agrees to the CURRENT status, and it
	// reports Claimed=false so a caller can tell its own repeat from a fresh claim.
	claim, err = client.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusFailed}, "cli", "")
	require.NoError(t, err)
	assert.False(t, claim.Claimed, "a claim was already held and nothing was written")
	assert.Equal(t, SlipStatusFailed, claim.Prior, "the RECORDED prior")
	_, err = client.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusCompleted}, "cli", "")
	require.ErrorIs(t, err, ErrClaimPreconditionFailed)

	// Work in flight is an outcome the client surfaces, not an error it wraps.
	out, err := client.ReleaseClaim(ctx, "c", "cli", "")
	require.NoError(t, err)
	assert.False(t, out.Released)
	assert.Equal(t, SlipStatusFailed, out.Status, "the status is reported on the held arm too")
	require.NoError(t, store.UpdateStep(ctx, "c", "builds", "", StepStatusCompleted))
	out, err = client.ReleaseClaim(ctx, "c", "cli", "")
	require.NoError(t, err)
	assert.True(t, out.Released)
	assert.Equal(t, SlipStatusFailed, out.Status)
	_, err = client.ReleaseClaim(ctx, "c", "cli", "")
	require.ErrorIs(t, err, ErrNotClaimed)
}
