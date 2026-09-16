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

	t.Run("copies, Update and Create keep claimed_from; a terminal Update clears it", func(t *testing.T) {
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
		snapshot, _ = store.Load(ctx, "c")
		snapshot.Status = SlipStatusPromoted
		require.NoError(t, store.Update(ctx, snapshot))
		got, _ = store.Load(ctx, "c")
		assert.Empty(t, got.ClaimedFrom, "a terminal Update ends the claim")
	})

	t.Run("claim never writes status; repeat claim is a no-op checked against the current status", func(t *testing.T) {
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
		_, err = store.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusFailed}, "third", "")
		require.ErrorIs(t, err, ErrClaimPreconditionFailed)
	})

	t.Run("release refused while in flight, clears when quiescent, never writes status", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "r", Status: SlipStatusFailed, Steps: map[string]Step{"builds": {Status: StepStatusRunning}}})
		_, err := store.ClaimSlip(ctx, "r", nil, "cli", "")
		require.NoError(t, err)
		_, err = store.ReleaseClaim(ctx, "r", "cli", "")
		require.ErrorIs(t, err, ErrRunInFlight)
		require.NoError(t, store.UpdateStep(ctx, "r", "builds", "", StepStatusCompleted))
		status, err := store.ReleaseClaim(ctx, "r", "cli", "")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, status)
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
