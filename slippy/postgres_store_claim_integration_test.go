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

// ClaimSlip is one transaction: lock, precondition, marker, status, claimed_from. There is
// no half-claimed state, and nothing about the decision is trusted from the caller's read.
func TestPostgresStore_ClaimSlip_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	t.Run("claims out of failed: status, claimed_from, exactly one marker", func(t *testing.T) {
		claimTestSlip(t, store, "c-failed", "sha-f", SlipStatusFailed)
		prior, err := store.ClaimSlip(ctx, "c-failed", []SlipStatus{SlipStatusFailed}, "rerunner", "test")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, prior)
		got, err := store.Load(ctx, "c-failed")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusInProgress, got.Status)
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

	t.Run("nil expected claims any unclaimed status, including a live pending one", func(t *testing.T) {
		claimTestSlip(t, store, "c-completed", "sha-c", SlipStatusCompleted)
		prior, err := store.ClaimSlip(ctx, "c-completed", nil, "rerunner", "test")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusCompleted, prior)
		// pending is live by IsLive() but is not a running claimant, so nil admits it: the one
		// status nil never claims out of is an unclaimed in_progress (the subtest below).
		claimTestSlip(t, store, "c-pending", "sha-pe", SlipStatusPending)
		prior, err = store.ClaimSlip(ctx, "c-pending", nil, "rerunner", "test")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusPending, prior)
	})

	t.Run("repeat claim is an idempotent no-op returning the prior", func(t *testing.T) {
		claimTestSlip(t, store, "c-twice", "sha-t", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "c-twice", nil, "first", "test")
		require.NoError(t, err)
		prior, err := store.ClaimSlip(ctx, "c-twice", []SlipStatus{SlipStatusFailed}, "second", "test")
		require.NoError(t, err, "a repeat claim must not be refused by its own precondition")
		assert.Equal(t, SlipStatusFailed, prior)
		assert.Equal(t, 1, countMarkers(t, store, "c-twice", ClaimMarkerStep), "no second marker")
	})

	t.Run("a claimed slip whose status moved on is still the idempotent no-op", func(t *testing.T) {
		claimTestSlip(t, store, "c-moved", "sha-m", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "c-moved", nil, "first", "test")
		require.NoError(t, err)
		// The run hit a step failure: checkPipelineCompletion wrote failed over in_progress.
		require.NoError(t, store.UpdateSlipStatus(ctx, "c-moved", SlipStatusFailed))
		prior, err := store.ClaimSlip(ctx, "c-moved", []SlipStatus{SlipStatusFailed}, "second", "test")
		require.NoError(t, err, "the claim outlives status writes; a second claimant joins the run")
		assert.Equal(t, SlipStatusFailed, prior)
		got, err := store.Load(ctx, "c-moved")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, got.Status, "the no-op arm must not rewrite the pipeline's status")
		assert.Equal(t, SlipStatusFailed, got.ClaimedFrom)
		assert.Equal(t, 1, countMarkers(t, store, "c-moved", ClaimMarkerStep), "no second marker")
	})

	t.Run("repeat claim checks expected against the recorded prior", func(t *testing.T) {
		claimTestSlip(t, store, "c-prior", "sha-pr", SlipStatusPromoted)
		_, err := store.ClaimSlip(ctx, "c-prior", nil, "first", "test")
		require.NoError(t, err)
		_, err = store.ClaimSlip(ctx, "c-prior", []SlipStatus{SlipStatusFailed}, "second", "test")
		require.ErrorIs(t, err, ErrClaimPreconditionFailed,
			"a caller that agreed to claim only failed must not be told it claimed a promoted slip")
		assert.Equal(t, 1, countMarkers(t, store, "c-prior", ClaimMarkerStep))
	})

	t.Run("a live unclaimed in_progress run is refused", func(t *testing.T) {
		claimTestSlip(t, store, "c-live", "sha-l", SlipStatusInProgress)
		_, err := store.ClaimSlip(ctx, "c-live", nil, "rerunner", "test")
		require.ErrorIs(t, err, ErrClaimPreconditionFailed)
		assert.Equal(t, 0, countMarkers(t, store, "c-live", ClaimMarkerStep))
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

// ReleaseClaim ends the claim whatever the status says: it restores claimed_from only while
// the slip is still in_progress (the run wrote nothing), and otherwise keeps the status the
// run wrote. Either way claimed_from is cleared, so no residue outlives the run.
func TestPostgresStore_ReleaseClaim_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	t.Run("restores the prior status, clears claimed_from, appends one marker", func(t *testing.T) {
		claimTestSlip(t, store, "r-ok", "sha-ok", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "r-ok", nil, "rerunner", "test")
		require.NoError(t, err)
		restored, err := store.ReleaseClaim(ctx, "r-ok", "post-job", "terminal write failed")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, restored)
		got, err := store.Load(ctx, "r-ok")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, got.Status)
		assert.Empty(t, got.ClaimedFrom)
		assert.Equal(t, 1, countMarkers(t, store, "r-ok", ReleaseMarkerStep))
		last := got.StateHistory[len(got.StateHistory)-1]
		assert.Equal(t, "post-job", last.Actor)
		assert.Contains(t, last.Message, "restored failed")
		assert.Contains(t, last.Message, "terminal write failed")
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

	t.Run("run completed: the terminal write already ended the claim, release is ErrNotClaimed", func(t *testing.T) {
		claimTestSlip(t, store, "r-done", "sha-d", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "r-done", nil, "rerunner", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateSlipStatus(ctx, "r-done", SlipStatusCompleted))
		_, err = store.ReleaseClaim(ctx, "r-done", "post-job", "")
		require.ErrorIs(t, err, ErrNotClaimed, "nothing left to release after a terminal write")
		got, err := store.Load(ctx, "r-done")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusCompleted, got.Status, "release must never undo a status the run wrote")
		assert.Empty(t, got.ClaimedFrom)
		assert.Equal(t, 0, countMarkers(t, store, "r-done", ReleaseMarkerStep))
	})

	t.Run("run moved to a non-terminal status: release keeps it and clears the claim", func(t *testing.T) {
		claimTestSlip(t, store, "r-comp", "sha-cp", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "r-comp", nil, "rerunner", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateSlipStatus(ctx, "r-comp", SlipStatusCompensating))
		final, err := store.ReleaseClaim(ctx, "r-comp", "post-job", "")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusCompensating, final)
		got, err := store.Load(ctx, "r-comp")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusCompensating, got.Status)
		assert.Empty(t, got.ClaimedFrom)
		last := got.StateHistory[len(got.StateHistory)-1]
		assert.Contains(t, last.Message, "kept compensating")
	})

	t.Run("step failure then release: failed kept, claim cleared, repeat release is ErrNotClaimed", func(t *testing.T) {
		claimTestSlip(t, store, "r-fail", "sha-rf", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "r-fail", nil, "rerunner", "")
		require.NoError(t, err)
		require.NoError(t, store.UpdateSlipStatus(ctx, "r-fail", SlipStatusFailed))
		final, err := store.ReleaseClaim(ctx, "r-fail", "post-job", "")
		require.NoError(t, err)
		assert.Equal(t, SlipStatusFailed, final)
		_, err = store.ReleaseClaim(ctx, "r-fail", "post-job", "")
		require.ErrorIs(t, err, ErrNotClaimed, "the second release finds no claim")
		assert.Equal(t, 1, countMarkers(t, store, "r-fail", ReleaseMarkerStep))
	})

	t.Run("claim, release, claim again yields two claim markers", func(t *testing.T) {
		claimTestSlip(t, store, "r-again", "sha-g", SlipStatusFailed)
		_, err := store.ClaimSlip(ctx, "r-again", nil, "a", "")
		require.NoError(t, err)
		_, err = store.ReleaseClaim(ctx, "r-again", "a", "")
		require.NoError(t, err)
		prior, err := store.ClaimSlip(ctx, "r-again", []SlipStatus{SlipStatusFailed}, "b", "")
		require.NoError(t, err, "a released slip is claimable again out of its restored status")
		assert.Equal(t, SlipStatusFailed, prior)
		assert.Equal(t, 2, countMarkers(t, store, "r-again", ClaimMarkerStep))
		assert.Equal(t, 1, countMarkers(t, store, "r-again", ReleaseMarkerStep))
	})

	t.Run("not found", func(t *testing.T) {
		_, err := store.ReleaseClaim(ctx, "r-missing", "post-job", "")
		require.ErrorIs(t, err, ErrSlipNotFound)
	})
}

// Repave must not destroy a slip a claimant is running on, whatever its status says: a step
// failure mid-run writes failed over the claim's in_progress, and before this guard that
// reopened the repave window for the rest of the run (PR #87 review, critical). The refusal
// is ErrSlipWentLive because the push path already handles that sentinel by deduplicating
// onto the surviving row.
func TestPostgresStore_Repave_ClaimedSlip_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()
	claimTestSlip(t, store, "rp-claimed", "sha-rp", SlipStatusFailed)
	_, err := store.ClaimSlip(ctx, "rp-claimed", nil, "slippy-cli", "rerun")
	require.NoError(t, err)
	require.NoError(t, store.UpdateSlipStatus(ctx, "rp-claimed", SlipStatusFailed), "a step failed mid-run")

	successor := &Slip{
		CorrelationID: "rp-successor", Repository: "Owner/Repo", Branch: "main", CommitSHA: "sha-rp",
		Status: SlipStatusInProgress,
	}
	err = store.Repave(ctx, "rp-claimed", successor, nil)
	require.ErrorIs(t, err, ErrSlipWentLive, "a claimed row is refused like a live one")
	got, err := store.Load(ctx, "rp-claimed")
	require.NoError(t, err, "the claimed row survives")
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
