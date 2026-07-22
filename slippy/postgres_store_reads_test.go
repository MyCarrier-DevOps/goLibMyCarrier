package slippy

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slipRowValues returns a minimal routing_slips row in slipColumns() order for the
// pgTestPipelineConfig schema (9 core + 4 step-status + 1 aggregate = 14 values).
func slipRowValues(id, commit string) []any {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return []any{
		id, "owner/repo", "main", commit, ts, ts,
		"in_progress", []byte("{}"), []byte(`{"entries":[]}`),
		"pending", "pending", "pending", "pending",
		[]byte(`{"items":[]}`),
	}
}

func TestPostgresStore_FindByCommits_Empty(t *testing.T) {
	store, _ := newMockStore(t)
	_, _, err := store.FindByCommits(context.Background(), "owner/repo", nil)
	require.Error(t, err, "empty commit list is an error")
}

func TestPostgresStore_FindByCommits_NotFound(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectQuery("FROM routing_slips s").
		WithArgs(anyArgs(2)...).
		WillReturnError(pgx.ErrNoRows)

	_, _, err := store.FindByCommits(context.Background(), "owner/repo", []string{"sha1"})
	require.ErrorIs(t, err, ErrSlipNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_FindByCommits_OK(t *testing.T) {
	store, mock := newMockStore(t)
	cols := append(store.slipColumns(), "matched_commit")
	rows := pgxmock.NewRows(cols).AddRow(append(slipRowValues("c1", "sha1"), "sha1")...)
	mock.ExpectQuery("JOIN unnest").
		WithArgs(anyArgs(2)...).
		WillReturnRows(rows)

	slip, matched, err := store.FindByCommits(context.Background(), "owner/repo", []string{"sha1", "sha2"})
	require.NoError(t, err)
	assert.Equal(t, "c1", slip.CorrelationID)
	assert.Equal(t, "sha1", matched)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_FindAllByCommits_Empty(t *testing.T) {
	store, _ := newMockStore(t)
	results, err := store.FindAllByCommits(context.Background(), "owner/repo", nil)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestPostgresStore_FindAllByCommits_OK(t *testing.T) {
	store, mock := newMockStore(t)
	cols := append(store.slipColumns(), "matched_commit")
	rows := pgxmock.NewRows(cols).
		AddRow(append(slipRowValues("c1", "sha1"), "sha1")...).
		AddRow(append(slipRowValues("c2", "sha2"), "sha2")...)
	mock.ExpectQuery("JOIN unnest").
		WithArgs(anyArgs(2)...).
		WillReturnRows(rows)

	results, err := store.FindAllByCommits(context.Background(), "owner/repo", []string{"sha1", "sha2"})
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "c1", results[0].Slip.CorrelationID)
	assert.Equal(t, "sha1", results[0].MatchedCommit)
	assert.Equal(t, "c2", results[1].Slip.CorrelationID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_InsertAncestryLink(t *testing.T) {
	store, mock := newMockStore(t)
	mock.ExpectExec("INSERT INTO slip_ancestry .* ON CONFLICT").
		WithArgs(anyArgs(10)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	child := &Slip{CorrelationID: "child", Repository: "owner/repo", Branch: "feature"}
	parent := AncestryEntry{
		CorrelationID: "parent", CommitSHA: "psha", Status: SlipStatusCompleted,
		Repository: "owner/repo", Branch: "main", CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, store.InsertAncestryLink(context.Background(), child, parent))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_ResolveAncestry_Walk(t *testing.T) {
	store, mock := newMockStore(t)
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ancestryCols := []string{
		"parent_correlation_id", "parent_commit_sha", "parent_status",
		"parent_failed_step", "parent_repository", "parent_branch", "created_at",
	}

	// hop 1: child -> parent (p1)
	mock.ExpectQuery("FROM slip_ancestry").
		WithArgs("owner/repo", "feature", "child").
		WillReturnRows(pgxmock.NewRows(ancestryCols).AddRow("p1", "sha1", "completed", "", "owner/repo", "main", ts))
	// hop 2: p1 -> grandparent (p2)
	mock.ExpectQuery("FROM slip_ancestry").
		WithArgs("owner/repo", "main", "p1").
		WillReturnRows(pgxmock.NewRows(ancestryCols).AddRow("p2", "sha0", "failed", "build", "owner/repo", "main", ts))
	// hop 3: p2 -> none
	mock.ExpectQuery("FROM slip_ancestry").
		WithArgs("owner/repo", "main", "p2").
		WillReturnError(pgx.ErrNoRows)

	chain, err := store.ResolveAncestry(context.Background(), "owner/repo", "feature", "child", 10)
	require.NoError(t, err)
	require.Len(t, chain, 2)
	assert.Equal(t, "p1", chain[0].CorrelationID)
	assert.Equal(t, SlipStatusCompleted, chain[0].Status)
	assert.Equal(t, "p2", chain[1].CorrelationID)
	assert.Equal(t, "build", chain[1].FailedStep)
	require.NoError(t, mock.ExpectationsWereMet())
}
