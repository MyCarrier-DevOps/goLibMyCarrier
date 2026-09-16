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

	t.Run("nil expected claims any status, a live in_progress run included", func(t *testing.T) {
		for _, st := range []SlipStatus{SlipStatusCompleted, SlipStatusPending, SlipStatusInProgress} {
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

	t.Run("repeat claim after the run moved status: no-op, expected checked against the current status", func(t *testing.T) {
		claimTestSlip(t, store, "c-moved", "sha-m", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "c-moved", nil, "first", "test")
		require.NoError(t, err)
		require.NoError(t, store.UpdateSlipStatus(ctx, "c-moved", SlipStatusInProgress), "the reconcile branch wrote it")
		prior, err := store.ClaimSlip(ctx, "c-moved", []SlipStatus{SlipStatusInProgress}, "second", "test")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, prior, "the recorded prior, not the current status")
		_, err = store.ClaimSlip(ctx, "c-moved", []SlipStatus{SlipStatusFailed}, "third", "test")
		require.ErrorIs(t, err, ErrClaimPreconditionFailed, "expected is a compare-and-set on the current status")
		got, err := store.Load(ctx, "c-moved")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusInProgress, got.Status, "the no-op arm writes nothing")
		assert.Equal(t, 1, countMarkers(t, store, "c-moved", ClaimMarkerStep))
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

// ReleaseClaim reads the whole row FOR UPDATE and decides under that lock: refused while any
// step or component is running or held, clears the claim otherwise. It never writes status.
func TestPostgresStore_ReleaseClaim_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	t.Run("nothing in flight: claim cleared, status untouched, one marker", func(t *testing.T) {
		claimTestSlip(t, store, "r-ok", "sha-ok", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "r-ok", nil, "rerunner", "test")
		require.NoError(t, err)
		status, err := store.ReleaseClaim(ctx, "r-ok", "post-job", "run over")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, status)
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

	t.Run("a step running or held: ErrRunInFlight, nothing written", func(t *testing.T) {
		for _, st := range []StepStatus{StepStatusRunning, StepStatusHeld} {
			id := "r-flight-" + string(st)
			claimTestSlip(t, store, id, "sha-"+id, SlipStatusFailed)
			_, err := store.ClaimSlip(ctx, id, nil, "rerunner", "")
			require.NoError(t, err)
			require.NoError(t, store.UpdateStep(ctx, id, "unit_tests", "", st))
			_, err = store.ReleaseClaim(ctx, id, "post-job", "")
			require.ErrorIs(t, err, ErrRunInFlight, st)
			got, err := store.Load(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "%s: the claim is kept", st)
			assert.Equal(t, 0, countMarkers(t, store, id, ReleaseMarkerStep))
		}
	})

	t.Run("a running component under an aggregate step: ErrRunInFlight", func(t *testing.T) {
		claimTestSlip(t, store, "r-comp", "sha-comp", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "r-comp", nil, "rerunner", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateComponentStatus(ctx, "r-comp", "web", "builds", StepStatusRunning))
		_, err = store.ReleaseClaim(ctx, "r-comp", "post-job", "")
		require.ErrorIs(t, err, ErrRunInFlight)
		require.NoError(t, store.UpdateComponentStatus(ctx, "r-comp", "web", "builds", StepStatusCompleted))
		_, err = store.ReleaseClaim(ctx, "r-comp", "post-job", "")
		require.NoError(t, err, "the component finished; nothing is in flight")
	})

	t.Run("the last post-job clears: two steps, releases after each", func(t *testing.T) {
		claimTestSlip(t, store, "r-two", "sha-two", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "r-two", nil, "rerunner", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateStep(ctx, "r-two", "builds", "", StepStatusRunning))
		require.NoError(t, store.UpdateStep(ctx, "r-two", "unit_tests", "", StepStatusRunning))
		require.NoError(t, store.UpdateStep(ctx, "r-two", "builds", "", StepStatusCompleted))
		_, err = store.ReleaseClaim(ctx, "r-two", "post-job/builds", "")
		require.ErrorIs(t, err, ErrRunInFlight, "unit_tests still runs")
		require.NoError(t, store.UpdateStep(ctx, "r-two", "unit_tests", "", StepStatusFailed))
		_, err = store.ReleaseClaim(ctx, "r-two", "post-job/unit_tests", "")
		require.NoError(t, err)
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
		_, err = store.ReleaseClaim(ctx, "r-push", "post-job", "")
		require.NoError(t, err)
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
		for i := range relErrs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				startErrs[i] = store.UpdateStep(ctx, "r-racing", "unit_tests", "", StepStatusRunning)
				_, relErrs[i] = store.ReleaseClaim(ctx, "r-racing", "post-job", "")
			}(i)
		}
		wg.Wait()

		for i := range relErrs {
			require.NoError(t, startErrs[i], "racer %d: StartStep", i)
			require.ErrorIs(t, relErrs[i], ErrRunInFlight, "racer %d: released with work in flight", i)
		}
		got, err := store.Load(ctx, "r-racing")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "the claim survives the whole wave")
		assert.Equal(t, 0, countMarkers(t, store, "r-racing", ReleaseMarkerStep))

		require.NoError(t, store.UpdateStep(ctx, "r-racing", "unit_tests", "", StepStatusCompleted))
		_, err = store.ReleaseClaim(ctx, "r-racing", "post-job", "")
		require.NoError(t, err, "nothing in flight now")
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

// The full-row Update never writes claimed_from — except that a terminal status ends the run
// and so ends the claim, the same rule UpdateSlipStatus applies. PromoteSlip is the one
// library path that reaches a terminal status through Update.
func TestPostgresStore_Update_TerminalStatusEndsTheClaim_Integration(t *testing.T) {
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
	assert.Equal(t, SlipStatusPromoted, got.Status)
	assert.Empty(t, got.ClaimedFrom, "a terminal Update ends the claim")
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
}
