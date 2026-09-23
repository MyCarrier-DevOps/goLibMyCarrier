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
		store.AddSlip(
			&slippy.Slip{CorrelationID: "u", Repository: "o/r", CommitSHA: "s", Status: slippy.SlipStatusFailed},
		)
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
//
// A FRESH INSERT CANNOT CARRY A CLAIM EITHER, and that is the half this double used to get
// wrong (PR #87, jhicks review). buildCreateQuery derives its INSERT column list from
// slipColumns(), which excludes claimed_from entirely rather than only excluding it from the
// ON CONFLICT SET list, so a first insert always leaves the column NULL whatever the caller's
// Slip carried. A double that stored the caller's value instead made a consumer test of
// "Repave refuses a claimed row" pass against a row Postgres would have left unclaimed and
// repaved - green here, the opposite outcome there.
func TestMockStore_Create_KeepsAnExistingClaimAndNeverTakesOneFromTheCaller(t *testing.T) {
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
	require.NoError(
		t,
		store.Create(
			ctx,
			&slippy.Slip{CorrelationID: "fresh", Status: slippy.SlipStatusFailed, ClaimedFrom: slippy.SlipStatusFailed},
		),
	)
	fresh, _ := store.Load(ctx, "fresh")
	assert.Empty(t, fresh.ClaimedFrom, "a fresh insert never records a claim: claimed_from is not an INSERT column")
	require.ErrorIs(t, func() error { _, e := store.ReleaseClaim(ctx, "fresh", "cli", ""); return e }(),
		slippy.ErrNotClaimed, "and the row really is unclaimed, not merely reported so")
}

// Repave's successor goes through the same slipColumns() insert (PostgresStore.createTx), so
// the replacement row is always unclaimed too, whatever ClaimedFrom the caller's newSlip
// carried. Same defect, same consequence: a consumer asserting ErrSlipWentLive on the
// successor would pass here and repave against Postgres (PR #87, jhicks review).
func TestMockStore_Repave_SuccessorIsNeverClaimed(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	store.AddSlip(
		&slippy.Slip{
			CorrelationID: "old",
			Repository:    "o/r",
			Branch:        "main",
			CommitSHA:     "s",
			Status:        slippy.SlipStatusFailed,
		},
	)
	successor := repaveSuccessorSlip("new", "o/r", "main", "s")
	successor.ClaimedFrom = slippy.SlipStatusFailed
	require.NoError(t, store.Repave(ctx, "old", successor, nil))
	got, err := store.Load(ctx, "new")
	require.NoError(t, err)
	assert.Empty(t, got.ClaimedFrom, "the successor is inserted unclaimed, as in Postgres")
	require.ErrorIs(t, func() error { _, e := store.ReleaseClaim(ctx, "new", "cli", ""); return e }(),
		slippy.ErrNotClaimed)
}

// The claim is a flag: it never writes status, and it lives until released with nothing in
// flight or until a terminal write. These pin the store contract on the published double.
func TestMockStore_ClaimSlip_IsAFlag(t *testing.T) {
	ctx := context.Background()

	t.Run("claim sets claimed_from, leaves status alone, appends one marker", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "c", Status: slippy.SlipStatusFailed})
		out, err := store.ClaimSlip(ctx, "c", []slippy.SlipStatus{slippy.SlipStatusFailed}, "cli", "rerun")
		require.NoError(t, err)
		assert.Equal(t, slippy.SlipStatusFailed, out.Prior)
		assert.True(t, out.Claimed, "this call is what recorded the claim")
		got, _ := store.Load(ctx, "c")
		assert.Equal(t, slippy.SlipStatusFailed, got.Status)
		assert.Equal(t, slippy.SlipStatusFailed, got.ClaimedFrom)
		assert.Equal(t, 1, countStep(got, slippy.ClaimMarkerStep))
	})

	// Inverted deliberately (PR #87 sixth review): expected used to be compared against the
	// RECORDED prior on a repeat claim, so a second rerun request matched the prior and
	// dispatched on top of the live run the first one had already started. It is a
	// compare-and-set on the CURRENT status whether or not a claim is held; the idempotent
	// arm is reached only after that agrees, and then reports Claimed=false with the RECORDED
	// prior and no second marker.
	t.Run("repeat claim is a no-op, but only once expected agrees to the CURRENT status", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "c", Status: slippy.SlipStatusFailed})
		_, err := store.ClaimSlip(ctx, "c", nil, "first", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateSlipStatus(ctx, "c", slippy.SlipStatusInProgress)) // reconcile wrote it
		_, err = store.ClaimSlip(ctx, "c", []slippy.SlipStatus{slippy.SlipStatusFailed}, "second", "")
		require.ErrorIs(t, err, slippy.ErrClaimPreconditionFailed,
			"the run moved off failed: a retry here would dispatch on top of work already running")
		out, err := store.ClaimSlip(ctx, "c", []slippy.SlipStatus{slippy.SlipStatusInProgress}, "third", "")
		require.NoError(t, err, "a caller that names the current status reaches the idempotent arm")
		assert.False(t, out.Claimed, "nothing written: the claim was already held")
		assert.Equal(t, slippy.SlipStatusFailed, out.Prior, "and the prior is the RECORDED one")
		_, err = store.ClaimSlip(ctx, "c", []slippy.SlipStatus{slippy.SlipStatusCompleted}, "fourth", "")
		require.ErrorIs(
			t,
			err,
			slippy.ErrClaimPreconditionFailed,
			"a claimant that never agreed to in_progress is refused",
		)
		got, _ := store.Load(ctx, "c")
		assert.Equal(t, 1, countStep(got, slippy.ClaimMarkerStep))
	})

	// Inverted deliberately, twice. First (PR #87 re-review): a nil expected used to adopt an
	// unclaimed in_progress run, and a caller that means to adopt one now says so explicitly.
	// Then (PR #87 seventh review): the refusal stopped reading the status NAME and started
	// reading the step and aggregate columns, so what it refuses is a run with work IN FLIGHT
	// — and an in_progress slip sitting between one step's post-job and the next step's
	// pre-job, which has nothing running, is claimable.
	t.Run("nil expected refuses a run in flight; an idle in_progress is claimable", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{CorrelationID: "c", Status: slippy.SlipStatusInProgress,
			Steps: map[string]slippy.Step{"builds": {Status: slippy.StepStatusRunning}}})
		_, err := store.ClaimSlip(ctx, "c", nil, "cli", "")
		require.ErrorIs(t, err, slippy.ErrClaimPreconditionFailed)
		got, _ := store.Load(ctx, "c")
		assert.Empty(t, got.ClaimedFrom, "nothing written")
		assert.Equal(t, 0, countStep(got, slippy.ClaimMarkerStep), "no marker either")

		out, err := store.ClaimSlip(ctx, "c", []slippy.SlipStatus{slippy.SlipStatusInProgress}, "cli", "")
		require.NoError(t, err)
		assert.Equal(t, slippy.SlipStatusInProgress, out.Prior)
		assert.True(t, out.Claimed)
		assert.True(t, out.InFlight, "the adopter is told it took over a run that is executing")

		store.AddSlip(&slippy.Slip{CorrelationID: "idle", Status: slippy.SlipStatusInProgress,
			Steps: map[string]slippy.Step{"builds": {Status: slippy.StepStatusCompleted}}})
		out, err = store.ClaimSlip(ctx, "idle", nil, "cli", "")
		require.NoError(t, err, "in_progress with nothing running is not a run in flight")
		assert.True(t, out.Claimed)
		assert.False(t, out.InFlight)
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
		// Named expected, not nil: these fixtures have work in flight by construction, and a
		// nil expected no longer adopts a run that is executing (PR #87 seventh review).
		_, err := store.ClaimSlip(ctx, "r", []slippy.SlipStatus{slippy.SlipStatusFailed}, "cli", "")
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
		store := claimed(
			t,
			map[string]slippy.Step{"builds": {Status: slippy.StepStatusFailed}},
			map[string][]slippy.ComponentStepData{
				"builds": {
					{Component: "api", Status: slippy.StepStatusFailed},
					{Component: "web", Status: slippy.StepStatusRunning},
				},
			},
		)
		out, err := store.ReleaseClaim(ctx, "r", "cli", "")
		require.NoError(t, err)
		assert.False(t, out.Released)
	})
	t.Run("nothing in flight: cleared, status untouched, one marker, repeat is ErrNotClaimed", func(t *testing.T) {
		store := claimed(
			t,
			map[string]slippy.Step{
				"builds":     {Status: slippy.StepStatusFailed},
				"unit_tests": {Status: slippy.StepStatusPending},
			},
			nil,
		)
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
	store.AddSlip(
		&slippy.Slip{
			CorrelationID: "old",
			Repository:    "o/r",
			Branch:        "main",
			CommitSHA:     "s",
			Status:        slippy.SlipStatusFailed,
		},
	)
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

// The published double's ResetSlipInPlace must decide the way PostgresStore decides — from the
// STORED row at call time, through the shared slippy.DecideReset — or a consumer's test of the
// in-delivery retry passes here and behaves differently in production (DEVOPS-367).
func TestMockStore_ResetSlipInPlace(t *testing.T) {
	ctx := context.Background()

	successor := func(id string) *slippy.Slip {
		return &slippy.Slip{
			CorrelationID: id, Repository: "o/r", CommitSHA: "s-" + id, Status: slippy.SlipStatusPending,
			Steps: map[string]slippy.Step{slippy.PushParsedStep: {Status: slippy.StepStatusRunning}},
		}
	}

	t.Run("claimed with work in flight: refused, nothing written", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{
			CorrelationID: "r1", Repository: "o/r", CommitSHA: "s-r1", Status: slippy.SlipStatusFailed,
			Steps: map[string]slippy.Step{"builds": {Status: slippy.StepStatusRunning}},
		})
		// A run that is EXECUTING is adopted only by naming its status, which is what the
		// rerunner does: a nil expected is refused by DecideClaim's in-flight arm.
		_, err := store.ClaimSlip(ctx, "r1", []slippy.SlipStatus{slippy.SlipStatusFailed},
			"pushhookparser/rerunner", "")
		require.NoError(t, err)

		err = store.ResetSlipInPlace(ctx, successor("r1"))
		require.Error(t, err)
		assert.ErrorIs(t, err, slippy.ErrSlipClaimed)

		got, loadErr := store.Load(ctx, "r1")
		require.NoError(t, loadErr)
		assert.Equal(t, slippy.SlipStatusFailed, got.Status, "the claimant's run is untouched")
		assert.Equal(t, slippy.StepStatusRunning, got.Steps["builds"].Status)
	})

	t.Run("claimed and quiescent: refused, nothing written", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{
			CorrelationID: "r2", Repository: "o/r", CommitSHA: "s-r2", Status: slippy.SlipStatusFailed,
		})
		_, err := store.ClaimSlip(ctx, "r2", nil, "pushhookparser/rerunner", "")
		require.NoError(t, err)

		resetErr := store.ResetSlipInPlace(ctx, successor("r2"))
		require.Error(t, resetErr, "a claim with no step reported yet is a queued run, not an absent one")
		assert.ErrorIs(t, resetErr, slippy.ErrSlipClaimed)

		got, loadErr := store.Load(ctx, "r2")
		require.NoError(t, loadErr)
		assert.Equal(t, slippy.SlipStatusFailed, got.Status, "the refused reset left the row as it was")
		assert.Equal(t, slippy.SlipStatusFailed, got.ClaimedFrom, "claimed_from untouched")
		marker := 0
		actor := ""
		for _, e := range got.StateHistory {
			if e.Step == slippy.ClaimMarkerStep {
				marker++
				actor = e.Actor
			}
		}
		assert.Equal(t, 1, marker, "exactly the marker the claim wrote; nothing added, nothing carried")
		assert.Equal(t, "pushhookparser/rerunner", actor, "still naming the original claimant")
	})

	t.Run("unclaimed: reset however its steps read", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&slippy.Slip{
			CorrelationID: "r3", Repository: "o/r", CommitSHA: "s-r3", Status: slippy.SlipStatusFailed,
			Steps: map[string]slippy.Step{"builds": {Status: slippy.StepStatusRunning}},
		})
		require.NoError(t, store.ResetSlipInPlace(ctx, successor("r3")))
		got, err := store.Load(ctx, "r3")
		require.NoError(t, err)
		assert.Equal(t, slippy.SlipStatusPending, got.Status)
		assert.Empty(t, got.ClaimedFrom)
	})

	t.Run("absent row: inserted, unclaimed, rather than refused", func(t *testing.T) {
		store := NewMockStore()
		require.NoError(t, store.ResetSlipInPlace(ctx, successor("r4")))
		got, err := store.Load(ctx, "r4")
		require.NoError(t, err)
		assert.Equal(t, slippy.SlipStatusPending, got.Status)
		assert.Empty(t, got.ClaimedFrom, "a fresh insert is always unclaimed")
		assert.Equal(t, []string{"r4"}, store.ResetInPlaceCalls)
	})

	t.Run("a nil successor is refused, and the injected error short-circuits", func(t *testing.T) {
		store := NewMockStore()
		assert.ErrorIs(t, store.ResetSlipInPlace(ctx, nil), slippy.ErrInvalidConfiguration)
		store.ResetInPlaceError = slippy.ErrStoreConnection
		assert.ErrorIs(t, store.ResetSlipInPlace(ctx, successor("r5")), slippy.ErrStoreConnection)
	})
}
