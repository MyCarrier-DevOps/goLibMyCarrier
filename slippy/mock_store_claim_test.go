package slippy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The internal double mirrors slippytest.MockStore; these pin the same claim-is-a-flag
// contract so the client tests that run against it (push_claim_test.go, client_claim_test.go,
// executor_claim_test.go) prove something.
func TestMockStore_ClaimIsAFlag(t *testing.T) {
	ctx := context.Background()

	t.Run("copies, Update and Create keep claimed_from whatever status they carry", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "c", Status: SlipStatusFailed})
		_, err := store.ClaimSlip(ctx, "c", nil, "cli", "")
		require.NoError(t, err)
		snapshot, err := store.Load(ctx, "c")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, snapshot.ClaimedFrom, "Load copies claimed_from")
		snapshot.ClaimedFrom = ""
		require.NoError(t, store.Update(ctx, snapshot))
		got, _ := store.Load(ctx, "c")
		assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "Update never writes claimed_from")
		require.NoError(t, store.Create(ctx, &Slip{CorrelationID: "c", Status: SlipStatusInProgress}))
		got, _ = store.Load(ctx, "c")
		assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "a redelivered Create keeps the claim, as ON CONFLICT does")
		// Inverted deliberately (PR #87 re-review): a terminal Update used to end the claim.
		// Its status comes from the caller's snapshot, so a claim taken after that read would
		// be cleared by a writer that never saw it; UpdateSlipStatus is the one path that ends
		// a claim.
		snapshot, _ = store.Load(ctx, "c")
		snapshot.Status = SlipStatusPromoted
		require.NoError(t, store.Update(ctx, snapshot))
		got, _ = store.Load(ctx, "c")
		assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "a terminal Update still cannot end the claim")
		require.NoError(t, store.UpdateSlipStatus(ctx, "c", SlipStatusPromoted))
		got, _ = store.Load(ctx, "c")
		assert.Empty(t, got.ClaimedFrom, "the atomic status write is what ends it")
	})

	// Inverted deliberately (PR #87 re-review): the repeat arm used to compare expected
	// against the CURRENT status, which refused the very retry the idempotent arm exists for
	// once the run had moved the row on.
	t.Run("claim never writes status; repeat claim is a no-op checked against the RECORDED prior", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "c", Status: SlipStatusFailed})
		prior, err := store.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusFailed}, "first", "")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, prior)
		got, _ := store.Load(ctx, "c")
		assert.Equal(t, SlipStatusFailed, got.Status)
		require.NoError(t, store.UpdateSlipStatus(ctx, "c", SlipStatusInProgress))
		prior, err = store.ClaimSlip(ctx, "c", nil, "second", "")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, prior, "the recorded prior")
		prior, err = store.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusFailed}, "third", "")
		require.NoError(t, err, "the retry agreed to failed, which is the recorded prior")
		assert.Equal(t, SlipStatusFailed, prior)
		_, err = store.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusCompleted}, "fourth", "")
		require.ErrorIs(t, err, ErrClaimPreconditionFailed, "a claimant that never agreed to failed is refused")
	})

	// A live run nothing has claimed is not adoptable by a caller that named no status.
	t.Run("nil expected refuses an unclaimed in_progress; an explicit one claims it", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "live", Status: SlipStatusInProgress})
		_, err := store.ClaimSlip(ctx, "live", nil, "rerunner", "")
		require.ErrorIs(t, err, ErrClaimPreconditionFailed)
		got, _ := store.Load(ctx, "live")
		assert.Empty(t, got.ClaimedFrom, "nothing written")
		prior, err := store.ClaimSlip(ctx, "live", []SlipStatus{SlipStatusInProgress}, "cli/prejob", "")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusInProgress, prior)
	})

	// Inverted deliberately (PR #87 re-review): the in-flight arm was ErrRunInFlight. It is
	// ReleaseOutcome{Released: false} with nothing written — information, not a failure.
	t.Run("release keeps the claim while in flight, clears when quiescent, never writes status", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "r", Status: SlipStatusFailed, Steps: map[string]Step{"builds": {Status: StepStatusRunning}}})
		_, err := store.ClaimSlip(ctx, "r", nil, "cli", "")
		require.NoError(t, err)
		out, err := store.ReleaseClaim(ctx, "r", "cli", "")
		require.NoError(t, err)
		assert.False(t, out.Released)
		assert.Equal(t, SlipStatusFailed, out.Status)
		require.NoError(t, store.UpdateStep(ctx, "r", "builds", "", StepStatusCompleted))
		out, err = store.ReleaseClaim(ctx, "r", "cli", "")
		require.NoError(t, err)
		assert.True(t, out.Released)
		assert.Equal(t, SlipStatusFailed, out.Status)
		got, _ := store.Load(ctx, "r")
		assert.Equal(t, SlipStatusFailed, got.Status)
		assert.Empty(t, got.ClaimedFrom)
		_, err = store.ReleaseClaim(ctx, "r", "cli", "")
		require.ErrorIs(t, err, ErrNotClaimed)
	})

	t.Run("a terminal status write ends the claim", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "r", Status: SlipStatusFailed})
		_, err := store.ClaimSlip(ctx, "r", nil, "cli", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateSlipStatus(ctx, "r", SlipStatusAbandoned))
		got, _ := store.Load(ctx, "r")
		assert.Empty(t, got.ClaimedFrom)
	})

	t.Run("repave refuses a claimed row", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "old", Repository: "o/r", Branch: "main", CommitSHA: "s", Status: SlipStatusFailed})
		_, err := store.ClaimSlip(ctx, "old", nil, "cli", "")
		require.NoError(t, err)
		successor := &Slip{CorrelationID: "new", Repository: "o/r", Branch: "main", CommitSHA: "s", Status: SlipStatusInProgress}
		require.ErrorIs(t, store.Repave(ctx, "old", successor, nil), ErrSlipWentLive)
		_, err = store.Load(ctx, "old")
		require.NoError(t, err)
	})
}
