package slippy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The internal double mirrors slippytest.MockStore; these pin the same claim-lifetime
// contract so the client tests that run against it (push_claim_test.go) prove something.
func TestMockStore_ClaimLifetime(t *testing.T) {
	ctx := context.Background()

	t.Run("copies and Update carry claimed_from", func(t *testing.T) {
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
	})

	t.Run("repeat claim after a status move: no-op, expected checked against the prior", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "c", Status: SlipStatusFailed})
		_, err := store.ClaimSlip(ctx, "c", nil, "first", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateSlipStatus(ctx, "c", SlipStatusFailed))
		prior, err := store.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusFailed}, "second", "")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, prior)
		got, _ := store.Load(ctx, "c")
		assert.Equal(t, SlipStatusFailed, got.Status, "the no-op arm does not rewrite status")
		_, err = store.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusCompleted}, "third", "")
		require.ErrorIs(t, err, ErrClaimPreconditionFailed)
	})

	t.Run("release after the run wrote a non-terminal status: claim cleared, status kept", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "r", Status: SlipStatusCompleted})
		_, err := store.ClaimSlip(ctx, "r", nil, "cli", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateSlipStatus(ctx, "r", SlipStatusFailed))
		final, err := store.ReleaseClaim(ctx, "r", "cli", "")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, final)
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
		require.NoError(t, store.UpdateSlipStatus(ctx, "old", SlipStatusFailed))
		successor := &Slip{CorrelationID: "new", Repository: "o/r", Branch: "main", CommitSHA: "s", Status: SlipStatusInProgress}
		require.ErrorIs(t, store.Repave(ctx, "old", successor, nil), ErrSlipWentLive)
		_, err = store.Load(ctx, "old")
		require.NoError(t, err)
	})
}
