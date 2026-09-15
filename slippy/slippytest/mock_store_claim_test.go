package slippytest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

// The published double must carry claimed_from through every copy, or a consumer's test of
// a release decided on a Load sees an unclaimed slip and passes for the wrong reason.
func TestDeepCopySlip_CarriesClaimedFrom(t *testing.T) {
	src := &slippy.Slip{CorrelationID: "c", Status: slippy.SlipStatusInProgress, ClaimedFrom: slippy.SlipStatusFailed}
	cpy := DeepCopySlip(src)
	assert.Equal(t, slippy.SlipStatusFailed, cpy.ClaimedFrom)
}

// PostgresStore's full-row Update never writes claimed_from (it is SELECT-only), so a stale
// snapshot cannot clear a claim it never loaded. The double must behave the same way.
func TestMockStore_Update_PreservesClaimedFrom(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()
	store.AddSlip(&slippy.Slip{CorrelationID: "u", Repository: "o/r", CommitSHA: "s", Status: slippy.SlipStatusFailed})
	_, err := store.ClaimSlip(ctx, "u", nil, "cli", "")
	require.NoError(t, err)

	snapshot, err := store.Load(ctx, "u")
	require.NoError(t, err)
	snapshot.ClaimedFrom = "" // a careless or stale caller
	snapshot.Branch = "renamed"
	require.NoError(t, store.Update(ctx, snapshot))

	got, err := store.Load(ctx, "u")
	require.NoError(t, err)
	assert.Equal(t, "renamed", got.Branch, "Update still writes the fields it owns")
	assert.Equal(t, slippy.SlipStatusFailed, got.ClaimedFrom, "but never claimed_from")
}

// The claim lives from ClaimSlip to ReleaseClaim regardless of what the pipeline writes to
// status in between; these pin the store contract on the published double.
func TestMockStore_ClaimSlip_ClaimLifetime(t *testing.T) {
	ctx := context.Background()

	t.Run("repeat claim after a status move is a no-op that keeps the pipeline's status", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "c", Status: slippy.SlipStatusFailed})
		_, err := store.ClaimSlip(ctx, "c", nil, "first", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateSlipStatus(ctx, "c", slippy.SlipStatusFailed))

		prior, err := store.ClaimSlip(ctx, "c", []slippy.SlipStatus{slippy.SlipStatusFailed}, "second", "")
		require.NoError(t, err)
		assert.Equal(t, slippy.SlipStatusFailed, prior)
		got, _ := store.Load(ctx, "c")
		assert.Equal(t, slippy.SlipStatusFailed, got.Status)
		assert.Equal(t, slippy.SlipStatusFailed, got.ClaimedFrom)
		assert.Equal(t, 1, countStep(got, slippy.ClaimMarkerStep))
	})

	t.Run("repeat claim checks expected against the recorded prior", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "c", Status: slippy.SlipStatusPromoted})
		_, err := store.ClaimSlip(ctx, "c", nil, "first", "")
		require.NoError(t, err)
		_, err = store.ClaimSlip(ctx, "c", []slippy.SlipStatus{slippy.SlipStatusFailed}, "second", "")
		require.ErrorIs(t, err, slippy.ErrClaimPreconditionFailed)
	})

	t.Run("a live unclaimed in_progress run is refused", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "c", Status: slippy.SlipStatusInProgress})
		_, err := store.ClaimSlip(ctx, "c", nil, "first", "")
		require.ErrorIs(t, err, slippy.ErrClaimPreconditionFailed)
	})
}

func TestMockStore_ReleaseClaim_ClaimLifetime(t *testing.T) {
	ctx := context.Background()

	t.Run("run wrote nothing: prior status restored", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "r", Status: slippy.SlipStatusFailed})
		_, err := store.ClaimSlip(ctx, "r", nil, "cli", "")
		require.NoError(t, err)
		final, err := store.ReleaseClaim(ctx, "r", "cli", "")
		require.NoError(t, err)
		assert.Equal(t, slippy.SlipStatusFailed, final)
		got, _ := store.Load(ctx, "r")
		assert.Equal(t, slippy.SlipStatusFailed, got.Status)
		assert.Empty(t, got.ClaimedFrom)
	})

	t.Run("run wrote a non-terminal status: claim cleared, status kept", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "r", Status: slippy.SlipStatusCompleted})
		_, err := store.ClaimSlip(ctx, "r", nil, "cli", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateSlipStatus(ctx, "r", slippy.SlipStatusFailed))
		final, err := store.ReleaseClaim(ctx, "r", "cli", "")
		require.NoError(t, err)
		assert.Equal(t, slippy.SlipStatusFailed, final)
		got, _ := store.Load(ctx, "r")
		assert.Equal(t, slippy.SlipStatusFailed, got.Status)
		assert.Empty(t, got.ClaimedFrom)
		assert.Equal(t, 1, countStep(got, slippy.ReleaseMarkerStep))
		_, err = store.ReleaseClaim(ctx, "r", "cli", "")
		require.ErrorIs(t, err, slippy.ErrNotClaimed)
	})

	t.Run("a terminal status write ends the claim on its own", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "r", Status: slippy.SlipStatusFailed})
		_, err := store.ClaimSlip(ctx, "r", nil, "cli", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateSlipStatus(ctx, "r", slippy.SlipStatusFailed))
		got, _ := store.Load(ctx, "r")
		assert.Equal(t, slippy.SlipStatusFailed, got.ClaimedFrom, "failed is not terminal")
		require.NoError(t, store.UpdateSlipStatus(ctx, "r", slippy.SlipStatusCompleted))
		got, _ = store.Load(ctx, "r")
		assert.Empty(t, got.ClaimedFrom, "completed is")
		_, err = store.ReleaseClaim(ctx, "r", "cli", "")
		require.ErrorIs(t, err, slippy.ErrNotClaimed)
	})
}

// Repave refuses a claimed row exactly as it refuses a live one, so a consumer's test of the
// push path's went-live fallback exercises the claim case against this double too.
func TestMockStore_Repave_ClaimedSlip_ReturnsErrSlipWentLive(t *testing.T) {
	store := NewMockStore()
	ctx := context.Background()
	store.AddSlip(&slippy.Slip{CorrelationID: "old", Repository: "o/r", Branch: "main", CommitSHA: "s", Status: slippy.SlipStatusFailed})
	_, err := store.ClaimSlip(ctx, "old", nil, "cli", "")
	require.NoError(t, err)
	require.NoError(t, store.UpdateSlipStatus(ctx, "old", slippy.SlipStatusFailed), "a step failed mid-run")

	successor := repaveSuccessorSlip("new", "o/r", "main", "s")
	require.ErrorIs(t, store.Repave(ctx, "old", successor, nil), slippy.ErrSlipWentLive)
	_, err = store.Load(ctx, "old")
	require.NoError(t, err, "the claimed row survives")
	_, err = store.Load(ctx, "new")
	require.ErrorIs(t, err, slippy.ErrSlipNotFound)

	_, err = store.ReleaseClaim(ctx, "old", "cli", "")
	require.NoError(t, err)
	require.NoError(t, store.Repave(ctx, "old", successor, nil), "released, the ended slip is repaveable")
}

func countStep(slip *slippy.Slip, step string) int {
	n := 0
	for _, e := range slip.StateHistory {
		if e.Step == step {
			n++
		}
	}
	return n
}
