//go:build integration

package slippy

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMigratedStore starts a Postgres container, runs the slippy migrations, and returns
// a PostgresStore over the resulting schema.
func newMigratedStore(t *testing.T) (*PostgresStore, *pgxpool.Pool, *PipelineConfig) {
	t.Helper()
	pool := newPGMigrationTestPool(t)
	cfg := pgTestPipelineConfig(t)
	_, err := RunPostgresMigrations(context.Background(), pool, PostgresMigrateOptions{PipelineConfig: cfg})
	require.NoError(t, err)
	store, err := NewPostgresStore(pool, cfg, nil)
	require.NoError(t, err)
	return store, pool, cfg
}

func TestPostgresStore_CRUD_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	slip := &Slip{
		CorrelationID: "c1",
		Repository:    "Owner/Repo",
		Branch:        "main",
		CommitSHA:     "sha1",
		Status:        SlipStatusInProgress,
		Steps: map[string]Step{
			"builds": {Status: StepStatusRunning, StartedAt: &started, Actor: "ci"},
		},
		Aggregates: map[string][]ComponentStepData{
			"builds": {{Component: "api", Status: StepStatusCompleted}},
		},
		StateHistory: []StateHistoryEntry{
			{Step: "builds", Status: StepStatusRunning, Actor: "ci", Timestamp: started},
		},
	}
	require.NoError(t, store.Create(ctx, slip))

	got, err := store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusInProgress, got.Status)
	assert.Equal(t, "Owner/Repo", got.Repository)
	assert.Equal(t, StepStatusRunning, got.Steps["builds"].Status)
	assert.Equal(t, "ci", got.Steps["builds"].Actor)
	require.NotNil(t, got.Steps["builds"].StartedAt)
	assert.Equal(t, StepStatusPending, got.Steps["unit_tests"].Status, "unset steps default pending")
	require.Len(t, got.Aggregates["builds"], 1)
	assert.Equal(t, "api", got.Aggregates["builds"][0].Component)
	require.Len(t, got.StateHistory, 1)

	// Update: promote status and complete the build.
	got.Status = SlipStatusCompleted
	b := got.Steps["builds"]
	b.Status = StepStatusCompleted
	got.Steps["builds"] = b
	require.NoError(t, store.Update(ctx, got))

	reloaded, err := store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusCompleted, reloaded.Status)
	assert.Equal(t, StepStatusCompleted, reloaded.Steps["builds"].Status)

	// Case-insensitive commit lookup.
	byCommit, err := store.LoadByCommit(ctx, "owner/repo", "sha1")
	require.NoError(t, err)
	assert.Equal(t, "c1", byCommit.CorrelationID)

	// Not-found paths.
	_, err = store.Load(ctx, "ghost")
	require.ErrorIs(t, err, ErrSlipNotFound)
	require.ErrorIs(t, store.Update(ctx, &Slip{CorrelationID: "ghost"}), ErrSlipNotFound)
}

func TestPostgresStore_CreateUpsert_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, &Slip{
		CorrelationID: "c1", Repository: "r", Branch: "b", CommitSHA: "sha", Status: SlipStatusPending,
	}))
	// Create again with the same ID overwrites (last-write-wins), no error.
	require.NoError(t, store.Create(ctx, &Slip{
		CorrelationID: "c1", Repository: "r", Branch: "b", CommitSHA: "sha", Status: SlipStatusInProgress,
	}))

	got, err := store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusInProgress, got.Status)
}

func TestPostgresStore_LoadLiveByCommit_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, &Slip{
		CorrelationID: "c1", Repository: "r", Branch: "b", CommitSHA: "sha", Status: SlipStatusInProgress,
	}))

	live, err := store.LoadLiveByCommit(ctx, "r", "sha")
	require.NoError(t, err)
	assert.Equal(t, "c1", live.CorrelationID)

	// Abandon it: LoadLiveByCommit must now exclude it, LoadByCommit must still find it.
	live.Status = SlipStatusAbandoned
	require.NoError(t, store.Update(ctx, live))

	_, err = store.LoadLiveByCommit(ctx, "r", "sha")
	require.ErrorIs(t, err, ErrSlipNotFound)

	still, err := store.LoadByCommit(ctx, "r", "sha")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusAbandoned, still.Status)
}

func TestPostgresStore_Ping_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	require.NoError(t, store.Ping(context.Background()))
}

func TestPostgresStore_DeleteSlip_Cascades_Integration(t *testing.T) {
	store, pool, _ := newMigratedStore(t)
	ctx := context.Background()

	slip := &Slip{
		CorrelationID: "corr-delete-me",
		Repository:    "owner/repo",
		Branch:        "integration",
		CommitSHA:     "sha-delete-cascade",
		Status:        SlipStatusFailed,
		Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
		StateHistory:  []StateHistoryEntry{},
	}
	require.NoError(t, store.Create(ctx, slip))
	require.NoError(t, store.UpdateStep(ctx, "corr-delete-me", "builds", "api", StepStatusFailed))
	require.NoError(t, store.InsertAncestryLink(ctx, slip, AncestryEntry{
		CorrelationID: "corr-parent", CommitSHA: "sha-parent",
		Repository: "owner/repo", Branch: "integration",
		Status: SlipStatusCompleted, CreatedAt: time.Now(),
	}))

	require.NoError(t, store.DeleteSlip(ctx, "corr-delete-me", ""))

	_, err := store.Load(ctx, "corr-delete-me")
	assert.ErrorIs(t, err, ErrSlipNotFound)
	for _, table := range []string{"slip_component_states", "slip_ancestry"} {
		var n int
		require.NoError(t, pool.QueryRow(ctx,
			"SELECT count(*) FROM "+table+" WHERE correlation_id = $1", "corr-delete-me").Scan(&n))
		assert.Zero(t, n, table+" rows must cascade away")
	}
}

// TestPostgresStore_DeleteSlip_RepointsDescendants_Integration pins FIX 3 against a real
// Postgres instance: another slip whose slip_ancestry row points at the repaved slip as
// its parent must be repointed to the successor, not left dangling (a dangling link would
// silently truncate that descendant's ResolveAncestry walk at this hop). It also pins D2.2
// (DEVOPS-231 review): the repoint clears parent_failed_step, since the deleted run's
// failed step is unambiguously wrong once the id beside it names the successor run.
func TestPostgresStore_DeleteSlip_RepointsDescendants_Integration(t *testing.T) {
	store, pool, _ := newMigratedStore(t)
	ctx := context.Background()

	parent := &Slip{
		CorrelationID: "corr-parent-repave",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-parent-repave",
		Status:        SlipStatusFailed,
	}
	require.NoError(t, store.Create(ctx, parent))

	child := &Slip{
		CorrelationID: "corr-child-of-repaved",
		Repository:    "owner/repo",
		Branch:        "feature",
		CommitSHA:     "sha-child-of-repaved",
		Status:        SlipStatusInProgress,
	}
	require.NoError(t, store.Create(ctx, child))
	require.NoError(t, store.InsertAncestryLink(ctx, child, AncestryEntry{
		CorrelationID: parent.CorrelationID, CommitSHA: parent.CommitSHA,
		Repository: parent.Repository, Branch: parent.Branch,
		Status: SlipStatusFailed, FailedStep: "unit_tests", CreatedAt: time.Now(),
	}))

	require.NoError(t, store.DeleteSlip(ctx, parent.CorrelationID, "corr-successor"))

	_, err := store.Load(ctx, parent.CorrelationID)
	assert.ErrorIs(t, err, ErrSlipNotFound, "the repaved slip itself must be gone")

	var newParent, failedStep string
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT parent_correlation_id, parent_failed_step FROM slip_ancestry "+
			"WHERE repository = $1 AND branch = $2 AND correlation_id = $3",
		child.Repository, child.Branch, child.CorrelationID).Scan(&newParent, &failedStep))
	assert.Equal(t, "corr-successor", newParent, "descendant must be repointed to the successor, not dangling")
	assert.Empty(t, failedStep,
		"parent_failed_step must be cleared on repoint: the pre-repave run's failed step is wrong for the successor")
}

// TestPostgresStore_DeleteSlip_WentLive_Integration pins FIX 2's TOCTOU guard against a
// real Postgres instance: a slip that recovers to live between the caller's repave
// decision and the DeleteSlip call must be rejected, not destroyed.
func TestPostgresStore_DeleteSlip_WentLive_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	slip := &Slip{
		CorrelationID: "corr-went-live",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-went-live",
		Status:        SlipStatusFailed,
	}
	require.NoError(t, store.Create(ctx, slip))

	// Simulate executor.go's recovery branch: the failed slip recovers to in_progress
	// after the caller already decided (from an earlier snapshot) to repave it.
	require.NoError(t, store.UpdateSlipStatus(ctx, slip.CorrelationID, SlipStatusInProgress))

	err := store.DeleteSlip(ctx, slip.CorrelationID, "corr-should-not-exist")
	require.ErrorIs(t, err, ErrSlipWentLive)

	got, loadErr := store.Load(ctx, slip.CorrelationID)
	require.NoError(t, loadErr, "the now-live slip must survive the rejected delete")
	assert.Equal(t, SlipStatusInProgress, got.Status)
}
