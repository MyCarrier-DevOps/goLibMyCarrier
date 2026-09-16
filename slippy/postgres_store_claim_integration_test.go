//go:build integration

package slippy

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claimedFromSlip creates a plain slip and then sets claimed_from directly in SQL, so the
// scan paths can be tested before ClaimSlip exists and independently of its semantics.
func claimedFromSlip(t *testing.T, store *PostgresStore, pool *pgxpool.Pool, corr, repo, sha string, from SlipStatus) {
	t.Helper()
	require.NoError(t, store.Create(context.Background(), &Slip{
		CorrelationID: corr, Repository: repo, Branch: "main", CommitSHA: sha, Status: SlipStatusInProgress,
	}))
	tag, err := pool.Exec(context.Background(),
		"UPDATE routing_slips SET claimed_from = $1 WHERE correlation_id = $2", string(from), corr)
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected())
}

// Every SELECT path that hydrates a Slip must carry claimed_from, or a release decided on a
// Load would see an unclaimed slip and refuse. claimed_from is SELECT-only: Create and the
// full-row Update must never write it, since a caller's snapshot Update could otherwise
// clear a claim it never knew about.
func TestPostgresStore_ClaimedFrom_IsScannedOnEveryReadPath_Integration(t *testing.T) {
	store, pool, _ := newMigratedStore(t)
	ctx := context.Background()
	claimedFromSlip(t, store, pool, "c-claimed", "Owner/Repo", "sha-claimed", SlipStatusFailed)

	t.Run("Load", func(t *testing.T) {
		got, err := store.Load(ctx, "c-claimed")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, got.ClaimedFrom)
	})
	t.Run("LoadByCommit", func(t *testing.T) {
		got, err := store.LoadByCommit(ctx, "owner/repo", "sha-claimed")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, got.ClaimedFrom)
	})
	t.Run("FindByCommits", func(t *testing.T) {
		got, matched, err := store.FindByCommits(ctx, "Owner/Repo", []string{"sha-claimed"})
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, SlipStatusFailed, got.ClaimedFrom)
		assert.Equal(t, "sha-claimed", matched, "the extra matched_commit destination must still line up")
	})
	t.Run("unclaimed slip reads empty", func(t *testing.T) {
		require.NoError(t, store.Create(ctx, &Slip{
			CorrelationID: "c-plain", Repository: "Owner/Repo", Branch: "main", CommitSHA: "sha-plain", Status: SlipStatusInProgress,
		}))
		got, err := store.Load(ctx, "c-plain")
		require.NoError(t, err)
		assert.Empty(t, got.ClaimedFrom)
	})
	t.Run("full-row Update does not clear claimed_from", func(t *testing.T) {
		got, err := store.Load(ctx, "c-claimed")
		require.NoError(t, err)
		got.ClaimedFrom = "" // a stale or careless caller snapshot
		got.Branch = "renamed"
		require.NoError(t, store.Update(ctx, got))
		again, err := store.Load(ctx, "c-claimed")
		require.NoError(t, err)
		assert.Equal(t, "renamed", again.Branch, "Update still writes the columns it owns")
		assert.Equal(t, SlipStatusFailed, again.ClaimedFrom, "but never claimed_from")
	})
}

// claimTestSlip creates a slip in the given status and returns it.
func claimTestSlip(t *testing.T, store *PostgresStore, corr, sha string, status SlipStatus) *Slip {
	t.Helper()
	slip := &Slip{CorrelationID: corr, Repository: "Owner/Repo", Branch: "main", CommitSHA: sha, Status: status}
	require.NoError(t, store.Create(context.Background(), slip))
	return slip
}

func countMarkers(t *testing.T, store *PostgresStore, corr, step string) int {
	t.Helper()
	slip, err := store.Load(context.Background(), corr)
	require.NoError(t, err)
	n := 0
	for _, e := range slip.StateHistory {
		if e.Step == step {
			n++
		}
	}
	return n
}

// ClaimSlip is one transaction: lock, compare-and-set on status, marker, claimed_from. It
// never writes status. There is no half-claimed state, and nothing about the decision is
// trusted from the caller's read.
func TestPostgresStore_ClaimSlip_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	t.Run("claims out of failed: claimed_from set, status unchanged, exactly one marker", func(t *testing.T) {
		claimTestSlip(t, store, "c-failed", "sha-f", SlipStatusFailed)
		prior, err := store.ClaimSlip(ctx, "c-failed", []SlipStatus{SlipStatusFailed}, "rerunner", "test")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, prior)
		got, err := store.Load(ctx, "c-failed")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, got.Status, "the claim never writes status")
		assert.Equal(t, SlipStatusFailed, got.ClaimedFrom)
		assert.Equal(t, 1, countMarkers(t, store, "c-failed", ClaimMarkerStep))
		last := got.StateHistory[len(got.StateHistory)-1]
		assert.Equal(t, "rerunner", last.Actor)
		assert.Contains(t, last.Message, "adopted failed slip", "the store names the true prior, read under lock")
	})

	t.Run("precondition mismatch writes nothing", func(t *testing.T) {
		claimTestSlip(t, store, "c-abandoned", "sha-a", SlipStatusAbandoned)
		_, err := store.ClaimSlip(ctx, "c-abandoned", []SlipStatus{SlipStatusFailed}, "rerunner", "test")
		require.ErrorIs(t, err, ErrClaimPreconditionFailed)
		got, err := store.Load(ctx, "c-abandoned")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusAbandoned, got.Status, "status untouched")
		assert.Empty(t, got.ClaimedFrom)
		assert.Equal(t, 0, countMarkers(t, store, "c-abandoned", ClaimMarkerStep), "no marker on a refused claim")
	})

	// Inverted deliberately (PR #87 re-review): a nil expected used to claim an unclaimed
	// in_progress row too. That row is a live run nothing has adopted, and claiming it
	// silently is how a rerun dispatches on top of a pipeline already in flight.
	t.Run("nil expected claims any status except a live unclaimed run", func(t *testing.T) {
		for _, st := range []SlipStatus{SlipStatusCompleted, SlipStatusPending} {
			id := "c-any-" + string(st)
			claimTestSlip(t, store, id, "sha-"+string(st), st)
			prior, err := store.ClaimSlip(ctx, id, nil, "rerunner", "test")
			require.NoError(t, err, st)
			assert.Equal(t, st, prior)
			got, err := store.Load(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, st, got.Status, "status untouched")
			assert.Equal(t, st, got.ClaimedFrom)
		}

		claimTestSlip(t, store, "c-live", "sha-live", SlipStatusInProgress)
		_, err := store.ClaimSlip(ctx, "c-live", nil, "rerunner", "test")
		require.ErrorIs(t, err, ErrClaimPreconditionFailed)
		got, err := store.Load(ctx, "c-live")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusInProgress, got.Status, "status untouched")
		assert.Empty(t, got.ClaimedFrom, "nothing written")
		assert.Equal(t, 0, countMarkers(t, store, "c-live", ClaimMarkerStep), "and no marker")
	})

	t.Run("an explicit in_progress in expected claims a live run", func(t *testing.T) {
		claimTestSlip(t, store, "c-live-explicit", "sha-live-explicit", SlipStatusInProgress)
		prior, err := store.ClaimSlip(ctx, "c-live-explicit",
			[]SlipStatus{SlipStatusInProgress}, "slippy-cli/prejob", "test")
		require.NoError(t, err, "the CLI pre-job claims out of every non-terminal status")
		assert.Equal(t, SlipStatusInProgress, prior)
		got, err := store.Load(ctx, "c-live-explicit")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusInProgress, got.Status, "status untouched")
		assert.Equal(t, SlipStatusInProgress, got.ClaimedFrom)
		assert.Equal(t, 1, countMarkers(t, store, "c-live-explicit", ClaimMarkerStep))
	})

	t.Run("repeat claim is an idempotent no-op returning the recorded prior", func(t *testing.T) {
		claimTestSlip(t, store, "c-twice", "sha-t", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "c-twice", nil, "first", "test")
		require.NoError(t, err)
		prior, err := store.ClaimSlip(ctx, "c-twice", []SlipStatus{SlipStatusFailed}, "second", "test")
		require.NoError(t, err, "a repeat claim must not be refused by its own precondition")
		assert.Equal(t, SlipStatusFailed, prior)
		assert.Equal(t, 1, countMarkers(t, store, "c-twice", ClaimMarkerStep), "no second marker")
	})

	// Inverted deliberately (PR #87 re-review): expected used to be compared against the
	// CURRENT status on a repeat claim, which refused the retry-after-a-lost-response the
	// idempotent arm exists for as soon as the run moved the row on.
	t.Run("repeat claim after the run moved status: expected checked against the RECORDED prior", func(t *testing.T) {
		claimTestSlip(t, store, "c-moved", "sha-m", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "c-moved", nil, "first", "test")
		require.NoError(t, err)
		require.NoError(t, store.UpdateSlipStatus(ctx, "c-moved", SlipStatusInProgress), "the reconcile branch wrote it")
		prior, err := store.ClaimSlip(ctx, "c-moved", []SlipStatus{SlipStatusFailed}, "second", "test")
		require.NoError(t, err, "the retry agreed to failed, which is what the row records")
		assert.Equal(t, SlipStatusFailed, prior, "the recorded prior, not the current status")
		_, err = store.ClaimSlip(ctx, "c-moved", []SlipStatus{SlipStatusCompleted}, "third", "test")
		require.ErrorIs(t, err, ErrClaimPreconditionFailed, "a claimant that never agreed to failed is refused")
		got, err := store.Load(ctx, "c-moved")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusInProgress, got.Status, "the no-op arm writes nothing")
		assert.Equal(t, 1, countMarkers(t, store, "c-moved", ClaimMarkerStep), "still exactly one marker")
	})

	t.Run("not found", func(t *testing.T) {
		_, err := store.ClaimSlip(ctx, "c-missing", nil, "rerunner", "test")
		require.ErrorIs(t, err, ErrSlipNotFound)
	})

	t.Run("concurrent claims produce exactly one marker", func(t *testing.T) {
		claimTestSlip(t, store, "c-race", "sha-r", SlipStatusFailed)
		var wg sync.WaitGroup
		errs := make([]error, 8)
		for i := range errs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, errs[i] = store.ClaimSlip(ctx, "c-race", nil, "racer", "test")
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			require.NoError(t, err, "racer %d", i)
		}
		assert.Equal(t, 1, countMarkers(t, store, "c-race", ClaimMarkerStep))
	})
}

// ReleaseClaim reads the claim state FOR UPDATE and decides under that lock: refused while any
// step or component is running or held, clears the claim otherwise. It never writes status.
// These subtests are the behaviour pin for that read: they cover a step running, a component
// running inside an aggregate, and push_parsed being ignored, so a read that dropped any of
// those columns would fail here rather than silently release a run still in flight.
func TestPostgresStore_ReleaseClaim_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	t.Run("nothing in flight: claim cleared, status untouched, one marker", func(t *testing.T) {
		claimTestSlip(t, store, "r-ok", "sha-ok", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "r-ok", nil, "rerunner", "test")
		require.NoError(t, err)
		out, err := store.ReleaseClaim(ctx, "r-ok", "post-job", "run over")
		require.NoError(t, err)
		assert.True(t, out.Released)
		assert.Equal(t, SlipStatusFailed, out.Status)
		got, err := store.Load(ctx, "r-ok")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, got.Status, "release never writes status")
		assert.Empty(t, got.ClaimedFrom)
		assert.Equal(t, 1, countMarkers(t, store, "r-ok", ReleaseMarkerStep))
		last := got.StateHistory[len(got.StateHistory)-1]
		assert.Equal(t, "post-job", last.Actor)
		assert.Contains(t, last.Message, "slip is failed")
		assert.Contains(t, last.Message, "run over")
	})

	// Inverted deliberately (PR #87 re-review): these arms were ErrRunInFlight. Work in
	// flight is ReleaseOutcome{Released: false} with nothing written — information, not a
	// failure, since N-1 of a run's N post-job releases take it.
	t.Run("a step running or held: the claim is kept, nothing written", func(t *testing.T) {
		for _, st := range []StepStatus{StepStatusRunning, StepStatusHeld} {
			id := "r-flight-" + string(st)
			claimTestSlip(t, store, id, "sha-"+id, SlipStatusFailed)
			_, err := store.ClaimSlip(ctx, id, nil, "rerunner", "")
			require.NoError(t, err)
			require.NoError(t, store.UpdateStep(ctx, id, "unit_tests", "", st))
			out, err := store.ReleaseClaim(ctx, id, "post-job", "")
			require.NoError(t, err, st)
			assert.False(t, out.Released, st)
			assert.Equal(t, SlipStatusFailed, out.Status, "%s: the status is known on the held arm too", st)
			got, err := store.Load(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "%s: the claim is kept", st)
			assert.Equal(t, 0, countMarkers(t, store, id, ReleaseMarkerStep))
		}
	})

	t.Run("a running component under an aggregate step keeps the claim", func(t *testing.T) {
		claimTestSlip(t, store, "r-comp", "sha-comp", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "r-comp", nil, "rerunner", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateComponentStatus(ctx, "r-comp", "web", "builds", StepStatusRunning))
		out, err := store.ReleaseClaim(ctx, "r-comp", "post-job", "")
		require.NoError(t, err)
		assert.False(t, out.Released)
		require.NoError(t, store.UpdateComponentStatus(ctx, "r-comp", "web", "builds", StepStatusCompleted))
		out, err = store.ReleaseClaim(ctx, "r-comp", "post-job", "")
		require.NoError(t, err)
		assert.True(t, out.Released, "the component finished; nothing is in flight")
	})

	// The documented recovery for a claim held by a dead run: the stuck STEP is what holds
	// it, so resolving that step and releasing again is the route that works in every state —
	// including on a terminal slip, where AbandonSlip is a deliberate no-op (I4) and clears
	// nothing (PR #87 re-review).
	t.Run("a stuck step resolved then released clears the claim", func(t *testing.T) {
		claimTestSlip(t, store, "r-stuck", "sha-stuck", SlipStatusCompleted)
		_, err := store.ClaimSlip(ctx, "r-stuck", nil, "rerunner", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateStep(ctx, "r-stuck", "dev_deploy", "", StepStatusRunning))

		out, err := store.ReleaseClaim(ctx, "r-stuck", "operator", "")
		require.NoError(t, err)
		assert.False(t, out.Released, "the stuck step holds the claim")

		require.NoError(t, store.UpdateStep(ctx, "r-stuck", "dev_deploy", "", StepStatusCompleted))
		out, err = store.ReleaseClaim(ctx, "r-stuck", "operator", "resolved a wedged step")
		require.NoError(t, err)
		assert.True(t, out.Released)
		assert.Equal(t, SlipStatusCompleted, out.Status)
		got, err := store.Load(ctx, "r-stuck")
		require.NoError(t, err)
		assert.Empty(t, got.ClaimedFrom, "the claim is gone")
		assert.Equal(t, SlipStatusCompleted, got.Status, "and the release never wrote status")
	})

	t.Run("the last post-job clears: two steps, releases after each", func(t *testing.T) {
		claimTestSlip(t, store, "r-two", "sha-two", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "r-two", nil, "rerunner", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateStep(ctx, "r-two", "builds", "", StepStatusRunning))
		require.NoError(t, store.UpdateStep(ctx, "r-two", "unit_tests", "", StepStatusRunning))
		require.NoError(t, store.UpdateStep(ctx, "r-two", "builds", "", StepStatusCompleted))
		out, err := store.ReleaseClaim(ctx, "r-two", "post-job/builds", "")
		require.NoError(t, err)
		assert.False(t, out.Released, "unit_tests still runs")
		require.NoError(t, store.UpdateStep(ctx, "r-two", "unit_tests", "", StepStatusFailed))
		out, err = store.ReleaseClaim(ctx, "r-two", "post-job/unit_tests", "")
		require.NoError(t, err)
		assert.True(t, out.Released)
		got, err := store.Load(ctx, "r-two")
		require.NoError(t, err)
		assert.Empty(t, got.ClaimedFrom)
		assert.Equal(t, 1, countMarkers(t, store, "r-two", ReleaseMarkerStep), "one release marker, from the one that cleared")
	})

	t.Run("push_parsed running does not count as in flight", func(t *testing.T) {
		claimTestSlip(t, store, "r-push", "sha-push", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "r-push", nil, "rerunner", "")
		require.NoError(t, err)
		// Every deduplicated same-commit push resets push_parsed to running and nothing ever
		// completes it; counting it would make the claimed slip unreleasable forever.
		require.NoError(t, store.UpdateStep(ctx, "r-push", "push_parsed", "", StepStatusRunning))
		out, err := store.ReleaseClaim(ctx, "r-push", "post-job", "")
		require.NoError(t, err)
		assert.True(t, out.Released)
		got, err := store.Load(ctx, "r-push")
		require.NoError(t, err)
		assert.Empty(t, got.ClaimedFrom)
	})

	t.Run("unclaimed slip is ErrNotClaimed and untouched", func(t *testing.T) {
		claimTestSlip(t, store, "r-plain", "sha-p", SlipStatusFailed)
		_, err := store.ReleaseClaim(ctx, "r-plain", "post-job", "")
		require.ErrorIs(t, err, ErrNotClaimed)
		got, err := store.Load(ctx, "r-plain")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, got.Status)
		assert.Equal(t, 0, countMarkers(t, store, "r-plain", ReleaseMarkerStep))
	})

	t.Run("a terminal write already ended the claim: ErrNotClaimed, status kept", func(t *testing.T) {
		claimTestSlip(t, store, "r-done", "sha-d", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "r-done", nil, "rerunner", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateSlipStatus(ctx, "r-done", SlipStatusCompleted))
		_, err = store.ReleaseClaim(ctx, "r-done", "post-job", "")
		require.ErrorIs(t, err, ErrNotClaimed)
		got, err := store.Load(ctx, "r-done")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusCompleted, got.Status)
		assert.Empty(t, got.ClaimedFrom)
	})

	t.Run("claim, release, claim again yields two claim markers", func(t *testing.T) {
		claimTestSlip(t, store, "r-again", "sha-g", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "r-again", nil, "a", "")
		require.NoError(t, err)
		_, err = store.ReleaseClaim(ctx, "r-again", "a", "")
		require.NoError(t, err)
		prior, err := store.ClaimSlip(ctx, "r-again", []SlipStatus{SlipStatusFailed}, "b", "")
		require.NoError(t, err, "a released slip is claimable again")
		assert.Equal(t, SlipStatusFailed, prior)
		assert.Equal(t, 2, countMarkers(t, store, "r-again", ClaimMarkerStep))
		assert.Equal(t, 1, countMarkers(t, store, "r-again", ReleaseMarkerStep))
	})

	// The release reads the row FOR UPDATE and decides under that lock, so a release can never
	// interleave with a StartStep and observe a row that is neither quiescent nor in flight.
	// Each goroutine here writes its own step event before its own release, so by the time any
	// release takes the lock at least that goroutine's running step is visible: every release
	// must be refused. Nothing completes the step during the wave, so the claim survives it.
	t.Run("release racing StartStep never clears a claim with work in flight", func(t *testing.T) {
		claimTestSlip(t, store, "r-racing", "sha-racing", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "r-racing", nil, "rerunner", "")
		require.NoError(t, err)

		var wg sync.WaitGroup
		startErrs := make([]error, 8)
		relErrs := make([]error, 8)
		relOuts := make([]ReleaseOutcome, 8)
		for i := range relErrs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				startErrs[i] = store.UpdateStep(ctx, "r-racing", "unit_tests", "", StepStatusRunning)
				relOuts[i], relErrs[i] = store.ReleaseClaim(ctx, "r-racing", "post-job", "")
			}(i)
		}
		wg.Wait()

		for i := range relErrs {
			require.NoError(t, startErrs[i], "racer %d: StartStep", i)
			require.NoError(t, relErrs[i], "racer %d: work in flight is not an error", i)
			assert.False(t, relOuts[i].Released, "racer %d: released with work in flight", i)
		}
		got, err := store.Load(ctx, "r-racing")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "the claim survives the whole wave")
		assert.Equal(t, 0, countMarkers(t, store, "r-racing", ReleaseMarkerStep))

		require.NoError(t, store.UpdateStep(ctx, "r-racing", "unit_tests", "", StepStatusCompleted))
		out, err := store.ReleaseClaim(ctx, "r-racing", "post-job", "")
		require.NoError(t, err)
		assert.True(t, out.Released, "nothing in flight now")
		got, err = store.Load(ctx, "r-racing")
		require.NoError(t, err)
		assert.Empty(t, got.ClaimedFrom)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := store.ReleaseClaim(ctx, "r-missing", "post-job", "")
		require.ErrorIs(t, err, ErrSlipNotFound)
	})
}

// Repave must not destroy a slip a claimant is running on, whatever its status says: the
// claim never writes status, so a claimed rerun of a failed slip still reads failed. The
// refusal is ErrSlipWentLive because the push path already handles that sentinel by
// deduplicating onto the surviving row.
func TestPostgresStore_Repave_ClaimedSlip_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()
	claimTestSlip(t, store, "rp-claimed", "sha-rp", SlipStatusFailed)
	_, err := store.ClaimSlip(ctx, "rp-claimed", nil, "slippy-cli", "rerun")
	require.NoError(t, err)

	successor := &Slip{
		CorrelationID: "rp-successor", Repository: "Owner/Repo", Branch: "main", CommitSHA: "sha-rp",
		Status: SlipStatusInProgress,
	}
	err = store.Repave(ctx, "rp-claimed", successor, nil)
	require.ErrorIs(t, err, ErrSlipWentLive, "a claimed row is refused like a live one")
	got, err := store.Load(ctx, "rp-claimed")
	require.NoError(t, err, "the claimed row survives")
	assert.Equal(t, SlipStatusFailed, got.Status)
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom)
	_, err = store.Load(ctx, "rp-successor")
	require.ErrorIs(t, err, ErrSlipNotFound, "no successor on a refused repave")

	_, err = store.ReleaseClaim(ctx, "rp-claimed", "slippy-cli/postjob", "")
	require.NoError(t, err)
	require.NoError(t, store.Repave(ctx, "rp-claimed", successor, nil), "released, the failed slip is repaveable again")
	_, err = store.Load(ctx, "rp-claimed")
	require.ErrorIs(t, err, ErrSlipNotFound)
}

// A terminal status ends the run by definition (I4: terminal is monotonic), so it ends the
// claim too: nothing is left to protect, and a claim that outlived its run would refuse every
// later repave of that commit with no client left to release it. failed is not terminal —
// other components of the run may still be executing — so it keeps the claim; the same goes
// for the reconcile path's in_progress. ReleaseClaim ends those when the run is over.
func TestPostgresStore_UpdateSlipStatus_TerminalWriteEndsTheClaim_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()
	claimTestSlip(t, store, "t-claim", "sha-t", SlipStatusFailed)
	_, err := store.ClaimSlip(ctx, "t-claim", nil, "slippy-cli", "")
	require.NoError(t, err)

	for _, kept := range []SlipStatus{SlipStatusFailed, SlipStatusInProgress, SlipStatusCompensating} {
		require.NoError(t, store.UpdateSlipStatus(ctx, "t-claim", kept))
		got, err := store.Load(ctx, "t-claim")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "%s is not terminal: the claim is kept", kept)
	}

	require.NoError(t, store.UpdateSlipStatus(ctx, "t-claim", SlipStatusCompleted))
	got, err := store.Load(ctx, "t-claim")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusCompleted, got.Status)
	assert.Empty(t, got.ClaimedFrom, "a terminal write ends the claim")

	successor := &Slip{
		CorrelationID: "t-successor", Repository: "Owner/Repo", Branch: "main", CommitSHA: "sha-t",
		Status: SlipStatusInProgress,
	}
	require.NoError(t, store.Repave(ctx, "t-claim", successor, nil), "the completed slip is repaveable with no release")
}

// The full-row Update never writes claimed_from, WHATEVER status it carries. Inverted
// deliberately (PR #87 re-review): a terminal Update used to clear the claim, keyed on the
// status in the caller's own snapshot — so a Load-then-Update could end a claim taken after
// its read, and rewrite that run's history with a stale row. UpdateSlipStatus is the one
// write path that ends a claim, and PromoteSlip now takes it.
func TestPostgresStore_Update_NeverEndsTheClaim_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()
	claimTestSlip(t, store, "u-claim", "sha-u", SlipStatusFailed)
	_, err := store.ClaimSlip(ctx, "u-claim", nil, "rerunner", "")
	require.NoError(t, err)

	snapshot, err := store.Load(ctx, "u-claim")
	require.NoError(t, err)
	snapshot.Status = SlipStatusInProgress
	snapshot.ClaimedFrom = ""
	require.NoError(t, store.Update(ctx, snapshot))
	got, err := store.Load(ctx, "u-claim")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "a non-terminal Update cannot clear the claim, whatever the snapshot says")

	snapshot.Status = SlipStatusPromoted
	require.NoError(t, store.Update(ctx, snapshot))
	got, err = store.Load(ctx, "u-claim")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusPromoted, got.Status, "Update still writes the columns it owns")
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "a terminal snapshot does not end the claim either")

	require.NoError(t, store.UpdateSlipStatus(ctx, "u-claim", SlipStatusPromoted))
	got, err = store.Load(ctx, "u-claim")
	require.NoError(t, err)
	assert.Empty(t, got.ClaimedFrom, "the atomic terminal status write is what ends it")
}

// Create's ON CONFLICT arm re-creates a slip that already exists — the same-correlation-ID
// redelivery path. claimed_from is not one of the columns it writes, so a claim held by a
// run in flight survives a redelivery that resets the row's status underneath it. If it did
// not, a redelivery would silently drop the in-flight flag and expose the run to a repave.
func TestPostgresStore_Create_KeepsAnExistingClaim_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()
	claimTestSlip(t, store, "cr-claim", "sha-cr", SlipStatusFailed)
	_, err := store.ClaimSlip(ctx, "cr-claim", nil, "rerunner", "")
	require.NoError(t, err)

	require.NoError(t, store.Create(ctx, &Slip{
		CorrelationID: "cr-claim", Repository: "Owner/Repo", Branch: "main", CommitSHA: "sha-cr",
		Status: SlipStatusInProgress,
	}), "the ON CONFLICT arm")

	got, err := store.Load(ctx, "cr-claim")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusInProgress, got.Status, "Create still writes the columns it owns")
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "but never claimed_from: the claim survives")

	// A redelivery carrying a TERMINAL status is the same rule, and worth pinning separately:
	// the SET list is slipColumns(), which has no claimed_from in it, so a terminal status in
	// a caller's snapshot ends nothing. Only UpdateSlipStatus does — and that is the remedy
	// when a redelivery leaves a claim nobody will release.
	require.NoError(t, store.Create(ctx, &Slip{
		CorrelationID: "cr-claim", Repository: "Owner/Repo", Branch: "main", CommitSHA: "sha-cr",
		Status: SlipStatusCompleted,
	}), "a redelivered Create carrying a terminal status")

	got, err = store.Load(ctx, "cr-claim")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusCompleted, got.Status)
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "a terminal Create leaves the claim standing")

	require.NoError(t, store.UpdateSlipStatus(ctx, "cr-claim", SlipStatusAbandoned))
	got, err = store.Load(ctx, "cr-claim")
	require.NoError(t, err)
	assert.Empty(t, got.ClaimedFrom, "the atomic terminal status write is what clears it")
}

// ProbeSchema is the startup gate that keeps an API pod ahead of its database from serving
// at all. slipSelectColumns() appends claimed_from unconditionally, so against a schema
// still at v5 every read path fails with Postgres 42703 (undefined_column) — not
// ErrSlipNotFound, which is why a caller cannot discover the problem from a Load's sentinel.
// The probe answers the question directly, and the second subtest pins the 42703 it exists
// for: against a v5 database Load fails, and fails with something other than "not found".
func TestPostgresStore_ProbeSchema_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("passes against a fully migrated schema", func(t *testing.T) {
		store, _, _ := newMigratedStore(t)
		require.NoError(t, store.ProbeSchema(ctx), "v6 applied: the probe must pass")
	})

	t.Run("a missing step column is caught too, not just claimed_from", func(t *testing.T) {
		store, pool, _ := newMigratedStore(t)
		_, err := pool.Exec(ctx, "ALTER TABLE routing_slips DROP COLUMN dev_deploy_status")
		require.NoError(t, err)

		err = store.ProbeSchema(ctx)
		require.Error(t, err, "the probe covers the whole select list, not one column of it")
		assert.ErrorIs(t, err, ErrSchemaBehind)
		assert.Contains(t, err.Error(), "dev_deploy_status", "and names the column that is missing")
	})

	t.Run("reports ErrSchemaBehind at v5, where every read fails", func(t *testing.T) {
		pool := newPGMigrationTestPool(t)
		cfg := pgTestPipelineConfig(t)
		_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg, TargetVersion: 5})
		require.NoError(t, err)
		store, err := NewPostgresStore(pool, cfg, nil)
		require.NoError(t, err)

		err = store.ProbeSchema(ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSchemaBehind)
		assert.Contains(t, err.Error(), ColumnClaimedFrom, "the probe names the missing column")

		_, loadErr := store.Load(ctx, "anything")
		require.Error(t, loadErr, "every read selects claimed_from, so it cannot succeed at v5")
		assert.NotErrorIs(t, loadErr, ErrSlipNotFound,
			"and it fails with 42703, not a sentinel the caller could mistake for an empty database")
	})
}
