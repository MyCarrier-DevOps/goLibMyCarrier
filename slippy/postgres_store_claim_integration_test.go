//go:build integration

package slippy

import (
	"context"
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
