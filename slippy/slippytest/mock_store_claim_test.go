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

// PostgresStore's full-row Update never writes claimed_from (it is SELECT-only), whatever
// status the snapshot carries. The double must behave the same way.
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
	// Inverted deliberately (PR #87 re-review): a terminal Update used to end the claim. The
	// status in a full-row write is the caller's own snapshot, so a claim taken after that
	// read would be cleared by a writer that never saw it. PromoteSlip now goes through
	// UpdateSlipStatus, the one write path that ends a claim.
	t.Run("a terminal status through Update still cannot end the claim", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "u", Status: slippy.SlipStatusFailed})
		_, err := store.ClaimSlip(ctx, "u", nil, "cli", "")
		require.NoError(t, err)
		snapshot, _ := store.Load(ctx, "u")
		snapshot.Status = slippy.SlipStatusPromoted
		require.NoError(t, store.Update(ctx, snapshot))
		got, _ := store.Load(ctx, "u")
		assert.Equal(t, slippy.SlipStatusPromoted, got.Status, "Update still writes the status column")
		assert.Equal(t, slippy.SlipStatusFailed, got.ClaimedFrom, "but never claimed_from")
		require.NoError(t, store.UpdateSlipStatus(ctx, "u", slippy.SlipStatusPromoted))
		got, _ = store.Load(ctx, "u")
		assert.Empty(t, got.ClaimedFrom, "the atomic status write is what ends it")
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

	// Inverted deliberately (PR #87 re-review): expected used to be compared against the
	// CURRENT status on a repeat claim, which refused the retry-after-a-lost-response the
	// idempotent arm exists for once the run had moved the row on.
	t.Run("repeat claim is a no-op that checks expected against the RECORDED prior", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "c", Status: slippy.SlipStatusFailed})
		_, err := store.ClaimSlip(ctx, "c", nil, "first", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateSlipStatus(ctx, "c", slippy.SlipStatusInProgress)) // reconcile wrote it
		prior, err := store.ClaimSlip(ctx, "c", []slippy.SlipStatus{slippy.SlipStatusFailed}, "second", "")
		require.NoError(t, err, "the retry agreed to failed, which is the recorded prior")
		assert.Equal(t, slippy.SlipStatusFailed, prior)
		_, err = store.ClaimSlip(ctx, "c", []slippy.SlipStatus{slippy.SlipStatusCompleted}, "third", "")
		require.ErrorIs(t, err, slippy.ErrClaimPreconditionFailed, "a claimant that never agreed to failed is refused")
		got, _ := store.Load(ctx, "c")
		assert.Equal(t, 1, countStep(got, slippy.ClaimMarkerStep))
	})

	// Inverted deliberately (PR #87 re-review): a nil expected used to adopt an unclaimed
	// in_progress run. That is a live run nothing has claimed; a caller that means to claim
	// one now says so explicitly.
	t.Run("nil expected refuses a live in_progress run; an explicit one claims it", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "c", Status: slippy.SlipStatusInProgress})
		_, err := store.ClaimSlip(ctx, "c", nil, "cli", "")
		require.ErrorIs(t, err, slippy.ErrClaimPreconditionFailed)
		got, _ := store.Load(ctx, "c")
		assert.Empty(t, got.ClaimedFrom, "nothing written")
		assert.Equal(t, 0, countStep(got, slippy.ClaimMarkerStep), "no marker either")

		prior, err := store.ClaimSlip(ctx, "c", []slippy.SlipStatus{slippy.SlipStatusInProgress}, "cli", "")
		require.NoError(t, err)
		assert.Equal(t, slippy.SlipStatusInProgress, prior)
	})
}

// Work in flight KEEPS the claim and writes nothing — ReleaseOutcome{Released: false}, not an
// error. Inverted deliberately (PR #87 re-review): this was ErrRunInFlight, which made N-1 of
// N post-job releases look like failures to every caller.
func TestMockStore_ReleaseClaim_KeepsTheClaimWhileInFlight(t *testing.T) {
	ctx := context.Background()
	claimed := func(t *testing.T, steps map[string]slippy.Step, aggs map[string][]slippy.ComponentStepData) *MockStore {
		t.Helper()
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "r", Status: slippy.SlipStatusFailed, Steps: steps, Aggregates: aggs})
		_, err := store.ClaimSlip(ctx, "r", nil, "cli", "")
		require.NoError(t, err)
		return store
	}

	t.Run("a running step: Released=false, nothing written", func(t *testing.T) {
		store := claimed(t, map[string]slippy.Step{"builds": {Status: slippy.StepStatusRunning}}, nil)
		out, err := store.ReleaseClaim(ctx, "r", "cli", "")
		require.NoError(t, err, "work in flight is an outcome, not an error")
		assert.False(t, out.Released)
		assert.Equal(t, slippy.SlipStatusFailed, out.Status, "the status is known on the held arm too")
		got, _ := store.Load(ctx, "r")
		assert.Equal(t, slippy.SlipStatusFailed, got.ClaimedFrom)
		assert.Equal(t, 0, countStep(got, slippy.ReleaseMarkerStep))
	})
	t.Run("a held step counts as in flight", func(t *testing.T) {
		store := claimed(t, map[string]slippy.Step{"dev_deploy": {Status: slippy.StepStatusHeld}}, nil)
		out, err := store.ReleaseClaim(ctx, "r", "cli", "")
		require.NoError(t, err)
		assert.False(t, out.Released)
	})
	t.Run("a running component under a failed aggregate counts as in flight", func(t *testing.T) {
		store := claimed(t, map[string]slippy.Step{"builds": {Status: slippy.StepStatusFailed}},
			map[string][]slippy.ComponentStepData{"builds": {{Component: "api", Status: slippy.StepStatusFailed}, {Component: "web", Status: slippy.StepStatusRunning}}})
		out, err := store.ReleaseClaim(ctx, "r", "cli", "")
		require.NoError(t, err)
		assert.False(t, out.Released)
	})
	t.Run("nothing in flight: cleared, status untouched, one marker, repeat is ErrNotClaimed", func(t *testing.T) {
		store := claimed(t, map[string]slippy.Step{"builds": {Status: slippy.StepStatusFailed}, "unit_tests": {Status: slippy.StepStatusPending}}, nil)
		out, err := store.ReleaseClaim(ctx, "r", "cli", "run over")
		require.NoError(t, err)
		assert.True(t, out.Released)
		assert.Equal(t, slippy.SlipStatusFailed, out.Status)
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
