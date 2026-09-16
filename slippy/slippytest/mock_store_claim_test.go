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
	src := &slippy.Slip{CorrelationID: "c", Status: slippy.SlipStatusFailed, ClaimedFrom: slippy.SlipStatusFailed}
	assert.Equal(t, slippy.SlipStatusFailed, DeepCopySlip(src).ClaimedFrom)
}

// PostgresStore's full-row Update never writes claimed_from (it is SELECT-only), except that
// a terminal status ends the run and so ends the claim. The double must behave the same way.
func TestMockStore_Update_ClaimSemantics(t *testing.T) {
	ctx := context.Background()
	t.Run("a stale snapshot cannot clear the claim", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "u", Repository: "o/r", CommitSHA: "s", Status: slippy.SlipStatusFailed})
		_, err := store.ClaimSlip(ctx, "u", nil, "cli", "")
		require.NoError(t, err)
		snapshot, err := store.Load(ctx, "u")
		require.NoError(t, err)
		snapshot.ClaimedFrom = ""
		snapshot.Branch = "renamed"
		require.NoError(t, store.Update(ctx, snapshot))
		got, err := store.Load(ctx, "u")
		require.NoError(t, err)
		assert.Equal(t, "renamed", got.Branch, "Update still writes the fields it owns")
		assert.Equal(t, slippy.SlipStatusFailed, got.ClaimedFrom, "but never claimed_from")
	})
	t.Run("a terminal status through Update ends the claim (PromoteSlip's path)", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "u", Status: slippy.SlipStatusFailed})
		_, err := store.ClaimSlip(ctx, "u", nil, "cli", "")
		require.NoError(t, err)
		snapshot, _ := store.Load(ctx, "u")
		snapshot.Status = slippy.SlipStatusPromoted
		require.NoError(t, store.Update(ctx, snapshot))
		got, _ := store.Load(ctx, "u")
		assert.Empty(t, got.ClaimedFrom)
	})
}

// PostgresStore.Create is an ON CONFLICT DO UPDATE whose SET list excludes claimed_from, so a
// redelivered Create for an existing correlation ID resets the row but keeps the claim.
func TestMockStore_Create_KeepsAnExistingClaim(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	require.NoError(t, store.Create(ctx, &slippy.Slip{CorrelationID: "c", Status: slippy.SlipStatusFailed}))
	_, err := store.ClaimSlip(ctx, "c", nil, "cli", "")
	require.NoError(t, err)
	require.NoError(t, store.Create(ctx, &slippy.Slip{CorrelationID: "c", Status: slippy.SlipStatusInProgress}))
	got, err := store.Load(ctx, "c")
	require.NoError(t, err)
	assert.Equal(t, slippy.SlipStatusInProgress, got.Status, "the row was reset")
	assert.Equal(t, slippy.SlipStatusFailed, got.ClaimedFrom, "the claim survived, as in Postgres")
	require.NoError(t, store.Create(ctx, &slippy.Slip{CorrelationID: "fresh", Status: slippy.SlipStatusFailed, ClaimedFrom: slippy.SlipStatusFailed}))
	fresh, _ := store.Load(ctx, "fresh")
	assert.Equal(t, slippy.SlipStatusFailed, fresh.ClaimedFrom, "a new row takes what it is given")
}

// The claim is a flag: it never writes status, and it lives until released with nothing in
// flight or until a terminal write. These pin the store contract on the published double.
func TestMockStore_ClaimSlip_IsAFlag(t *testing.T) {
	ctx := context.Background()

	t.Run("claim sets claimed_from, leaves status alone, appends one marker", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "c", Status: slippy.SlipStatusFailed})
		prior, err := store.ClaimSlip(ctx, "c", []slippy.SlipStatus{slippy.SlipStatusFailed}, "cli", "rerun")
		require.NoError(t, err)
		assert.Equal(t, slippy.SlipStatusFailed, prior)
		got, _ := store.Load(ctx, "c")
		assert.Equal(t, slippy.SlipStatusFailed, got.Status)
		assert.Equal(t, slippy.SlipStatusFailed, got.ClaimedFrom)
		assert.Equal(t, 1, countStep(got, slippy.ClaimMarkerStep))
	})

	t.Run("repeat claim is a no-op that still checks expected against the current status", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "c", Status: slippy.SlipStatusFailed})
		_, err := store.ClaimSlip(ctx, "c", nil, "first", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateSlipStatus(ctx, "c", slippy.SlipStatusInProgress)) // reconcile wrote it
		prior, err := store.ClaimSlip(ctx, "c", []slippy.SlipStatus{slippy.SlipStatusInProgress}, "second", "")
		require.NoError(t, err)
		assert.Equal(t, slippy.SlipStatusFailed, prior, "the recorded prior")
		_, err = store.ClaimSlip(ctx, "c", []slippy.SlipStatus{slippy.SlipStatusFailed}, "third", "")
		require.ErrorIs(t, err, slippy.ErrClaimPreconditionFailed)
		got, _ := store.Load(ctx, "c")
		assert.Equal(t, 1, countStep(got, slippy.ClaimMarkerStep))
	})

	t.Run("nil expected admits a live in_progress run", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "c", Status: slippy.SlipStatusInProgress})
		prior, err := store.ClaimSlip(ctx, "c", nil, "cli", "")
		require.NoError(t, err)
		assert.Equal(t, slippy.SlipStatusInProgress, prior)
	})
}

func TestMockStore_ReleaseClaim_RefusesWhileInFlight(t *testing.T) {
	ctx := context.Background()
	claimed := func(t *testing.T, steps map[string]slippy.Step, aggs map[string][]slippy.ComponentStepData) *MockStore {
		t.Helper()
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "r", Status: slippy.SlipStatusFailed, Steps: steps, Aggregates: aggs})
		_, err := store.ClaimSlip(ctx, "r", nil, "cli", "")
		require.NoError(t, err)
		return store
	}

	t.Run("a running step: ErrRunInFlight, nothing written", func(t *testing.T) {
		store := claimed(t, map[string]slippy.Step{"builds": {Status: slippy.StepStatusRunning}}, nil)
		_, err := store.ReleaseClaim(ctx, "r", "cli", "")
		require.ErrorIs(t, err, slippy.ErrRunInFlight)
		got, _ := store.Load(ctx, "r")
		assert.Equal(t, slippy.SlipStatusFailed, got.ClaimedFrom)
		assert.Equal(t, 0, countStep(got, slippy.ReleaseMarkerStep))
	})
	t.Run("a held step counts as in flight", func(t *testing.T) {
		store := claimed(t, map[string]slippy.Step{"dev_deploy": {Status: slippy.StepStatusHeld}}, nil)
		_, err := store.ReleaseClaim(ctx, "r", "cli", "")
		require.ErrorIs(t, err, slippy.ErrRunInFlight)
	})
	t.Run("a running component under a failed aggregate counts as in flight", func(t *testing.T) {
		store := claimed(t, map[string]slippy.Step{"builds": {Status: slippy.StepStatusFailed}},
			map[string][]slippy.ComponentStepData{"builds": {{Component: "api", Status: slippy.StepStatusFailed}, {Component: "web", Status: slippy.StepStatusRunning}}})
		_, err := store.ReleaseClaim(ctx, "r", "cli", "")
		require.ErrorIs(t, err, slippy.ErrRunInFlight)
	})
	t.Run("nothing in flight: cleared, status untouched, one marker, repeat is ErrNotClaimed", func(t *testing.T) {
		store := claimed(t, map[string]slippy.Step{"builds": {Status: slippy.StepStatusFailed}, "unit_tests": {Status: slippy.StepStatusPending}}, nil)
		status, err := store.ReleaseClaim(ctx, "r", "cli", "run over")
		require.NoError(t, err)
		assert.Equal(t, slippy.SlipStatusFailed, status)
		got, _ := store.Load(ctx, "r")
		assert.Equal(t, slippy.SlipStatusFailed, got.Status, "release never writes status")
		assert.Empty(t, got.ClaimedFrom)
		assert.Equal(t, 1, countStep(got, slippy.ReleaseMarkerStep))
		_, err = store.ReleaseClaim(ctx, "r", "cli", "")
		require.ErrorIs(t, err, slippy.ErrNotClaimed)
	})
	t.Run("a terminal status write already ended the claim", func(t *testing.T) {
		store := claimed(t, nil, nil)
		require.NoError(t, store.UpdateSlipStatus(ctx, "r", slippy.SlipStatusCompleted))
		got, _ := store.Load(ctx, "r")
		assert.Empty(t, got.ClaimedFrom)
		_, err := store.ReleaseClaim(ctx, "r", "cli", "")
		require.ErrorIs(t, err, slippy.ErrNotClaimed)
	})
	t.Run("failed and in_progress writes keep the claim", func(t *testing.T) {
		store := claimed(t, nil, nil)
		for _, st := range []slippy.SlipStatus{slippy.SlipStatusInProgress, slippy.SlipStatusFailed, slippy.SlipStatusCompensating} {
			require.NoError(t, store.UpdateSlipStatus(ctx, "r", st))
			got, _ := store.Load(ctx, "r")
			assert.Equal(t, slippy.SlipStatusFailed, got.ClaimedFrom, st)
		}
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
