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
