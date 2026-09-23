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

	// Inverted deliberately (PR #87 sixth review): the repeat arm used to compare expected
	// against the RECORDED prior, so a second rerun request for a commit whose pipeline was
	// already live matched the prior and dispatched on top of it. expected is a
	// compare-and-set on the CURRENT status whether or not a claim is held, and the
	// idempotent arm sits behind it.
	//
	// The row carries a RUNNING step throughout (PR #87 seventh review): the refusal of the
	// nil-expected claim below is now evidence-based, so a slip with no step ever reported
	// would be claimable at in_progress and would prove the opposite of what this asserts.
	t.Run("claim never writes status; a repeat is idempotent once expected agrees to the CURRENT status", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "c", Status: SlipStatusFailed,
			Steps: map[string]Step{"builds": {Status: StepStatusRunning}}})
		out, err := store.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusFailed}, "first", "")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, out.Prior)
		assert.True(t, out.Claimed)
		got, _ := store.Load(ctx, "c")
		assert.Equal(t, SlipStatusFailed, got.Status)
		require.NoError(t, store.UpdateSlipStatus(ctx, "c", SlipStatusInProgress))
		// Inverted again for finding j-claim (PR #87, jhicks round): the in-flight refusal
		// guards ADOPTION, and this row is already claimed, so a nil expected reaches the
		// idempotent repeat rather than ErrClaimPreconditionFailed. The compare-and-set above
		// it is unaffected, which the `third` and `fifth` claims below still pin.
		out, err = store.ClaimSlip(ctx, "c", nil, "second", "")
		require.NoError(t, err, "a claimed row has nothing left to adopt, so the repeat arm answers")
		assert.False(t, out.Claimed, "nothing written")
		assert.Equal(t, SlipStatusFailed, out.Prior, "the recorded prior, not the current status")
		assert.True(t, out.InFlight, "and the caller is told the claim's run is executing")
		_, err = store.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusFailed}, "third", "")
		require.ErrorIs(t, err, ErrClaimPreconditionFailed, "the rerunner's retry after its dispatch started is refused")
		out, err = store.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusInProgress}, "fourth", "")
		require.NoError(t, err, "a caller that names the current status reaches the idempotent arm")
		assert.False(t, out.Claimed, "nothing written")
		assert.Equal(t, SlipStatusFailed, out.Prior, "the recorded prior")
		_, err = store.ClaimSlip(ctx, "c", []SlipStatus{SlipStatusCompleted}, "fifth", "")
		require.ErrorIs(t, err, ErrClaimPreconditionFailed, "a claimant that never agreed to in_progress is refused")
	})

	// A run IN FLIGHT is not adoptable by a caller that named no status — claimed or not; the
	// refusal does not depend on the row being unclaimed (see the repeat subtest above).
	//
	// Inverted deliberately (PR #87 seventh review): the refusal used to read the status NAME,
	// so `in_progress` was refused whether or not anything was running. The second half pins
	// the other side of that inversion — an in_progress slip between one step's post-job and
	// the next step's pre-job has nothing in flight and IS claimable — and the outcome now
	// carries the evidence it decided on.
	t.Run("nil expected refuses a run in flight; an idle in_progress is claimable", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "live", Status: SlipStatusInProgress,
			Steps: map[string]Step{"builds": {Status: StepStatusRunning}}})
		_, err := store.ClaimSlip(ctx, "live", nil, "rerunner", "")
		require.ErrorIs(t, err, ErrClaimPreconditionFailed)
		got, _ := store.Load(ctx, "live")
		assert.Empty(t, got.ClaimedFrom, "nothing written")
		out, err := store.ClaimSlip(ctx, "live", []SlipStatus{SlipStatusInProgress}, "cli/prejob", "")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusInProgress, out.Prior)
		assert.True(t, out.Claimed)
		assert.True(t, out.InFlight, "the adopter is told it took over a run that is executing")

		store.AddSlip(&Slip{CorrelationID: "idle", Status: SlipStatusInProgress,
			Steps: map[string]Step{"builds": {Status: StepStatusCompleted}, "unit_tests": {Status: StepStatusPending}}})
		out, err = store.ClaimSlip(ctx, "idle", nil, "rerunner", "")
		require.NoError(t, err, "in_progress with nothing running is not a run in flight")
		assert.Equal(t, SlipStatusInProgress, out.Prior)
		assert.True(t, out.Claimed)
		assert.False(t, out.InFlight)
	})

	// Inverted deliberately (PR #87 re-review): the in-flight arm was ErrRunInFlight. It is
	// ReleaseOutcome{Released: false} with nothing written — information, not a failure.
	t.Run("release keeps the claim while in flight, clears when quiescent, never writes status", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "r", Status: SlipStatusFailed, Steps: map[string]Step{"builds": {Status: StepStatusRunning}}})
		// Named expected, not nil: the row has a step running, and a nil expected no longer
		// adopts a run in flight. This is the shape a real adopter of a running run sends.
		claim, err := store.ClaimSlip(ctx, "r", []SlipStatus{SlipStatusFailed}, "cli", "")
		require.NoError(t, err)
		assert.True(t, claim.InFlight, "the claim reports the evidence it was decided on")
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

// The internal double's ResetSlipInPlace, held to the same contract as the published one and
// as PostgresStore: the decision comes from the STORED row through the shared DecideReset, so
// the client tests that drive the race against this double prove something (DEVOPS-367).
func TestMockStore_ResetSlipInPlace(t *testing.T) {
	ctx := context.Background()
	successor := func(id string) *Slip {
		return &Slip{
			CorrelationID: id, Repository: "o/r", CommitSHA: "s-" + id, Status: SlipStatusPending,
			Steps: map[string]Step{PushParsedStep: {Status: StepStatusRunning}},
		}
	}

	t.Run("claimed with work in flight: refused, nothing written", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{
			CorrelationID: "m1", Repository: "o/r", CommitSHA: "s-m1", Status: SlipStatusFailed,
			Steps: map[string]Step{"builds": {Status: StepStatusRunning}},
		})
		// A run that is EXECUTING is adopted only by naming its status, which is what the
		// rerunner does: a nil expected is refused by DecideClaim's in-flight arm.
		_, err := store.ClaimSlip(ctx, "m1", []SlipStatus{SlipStatusFailed}, "pushhookparser/rerunner", "")
		require.NoError(t, err)

		err = store.ResetSlipInPlace(ctx, successor("m1"))
		assert.ErrorIs(t, err, ErrSlipClaimed)
		got, loadErr := store.Load(ctx, "m1")
		require.NoError(t, loadErr)
		assert.Equal(t, SlipStatusFailed, got.Status)
		assert.Equal(t, StepStatusRunning, got.Steps["builds"].Status)
	})

	t.Run("claimed and quiescent: refused, nothing written", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{CorrelationID: "m2", Repository: "o/r", CommitSHA: "s-m2", Status: SlipStatusFailed})
		_, err := store.ClaimSlip(ctx, "m2", nil, "slippy-cli/prejob", "")
		require.NoError(t, err)

		err = store.ResetSlipInPlace(ctx, successor("m2"))
		require.Error(t, err, "a claim with no step reported yet is a queued run, not an absent one")
		assert.ErrorIs(t, err, ErrSlipClaimed)

		got, loadErr := store.Load(ctx, "m2")
		require.NoError(t, loadErr)
		assert.Equal(t, SlipStatusFailed, got.Status, "the refused reset left the row exactly as it was")
		assert.Equal(t, SlipStatusFailed, got.ClaimedFrom)
		assert.Equal(t, 1, countHistoryStep(got, ClaimMarkerStep))
		assert.Equal(t, "slippy-cli/prejob", lastHistoryActor(got, ClaimMarkerStep))
	})

	t.Run("unclaimed, and absent", func(t *testing.T) {
		store := NewMockStore()
		store.AddSlip(&Slip{
			CorrelationID: "m3", Repository: "o/r", CommitSHA: "s-m3", Status: SlipStatusFailed,
			Steps: map[string]Step{"builds": {Status: StepStatusRunning}},
		})
		require.NoError(t, store.ResetSlipInPlace(ctx, successor("m3")))
		got, err := store.Load(ctx, "m3")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusPending, got.Status, "an unclaimed row is reset however its steps read")
		assert.Empty(t, got.ClaimedFrom)

		require.NoError(t, store.ResetSlipInPlace(ctx, successor("m4")))
		got, err = store.Load(ctx, "m4")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusPending, got.Status, "an absent row is inserted, not refused")
		assert.Empty(t, got.ClaimedFrom)
	})

	t.Run("a nil successor is refused, and the injected error short-circuits", func(t *testing.T) {
		store := NewMockStore()
		assert.ErrorIs(t, store.ResetSlipInPlace(ctx, nil), ErrInvalidConfiguration)
		store.ResetInPlaceError = ErrStoreConnection
		assert.ErrorIs(t, store.ResetSlipInPlace(ctx, successor("m5")), ErrStoreConnection)
	})
}
