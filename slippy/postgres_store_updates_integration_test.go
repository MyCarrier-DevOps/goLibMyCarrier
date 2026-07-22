//go:build integration

package slippy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findComponent returns the component entry for name in slip.Aggregates[agg], or nil.
func findComponent(slip *Slip, agg, name string) *ComponentStepData {
	for i := range slip.Aggregates[agg] {
		if slip.Aggregates[agg][i].Component == name {
			return &slip.Aggregates[agg][i]
		}
	}
	return nil
}

func mustCreate(t *testing.T, store *PostgresStore, id string) {
	t.Helper()
	require.NoError(t, store.Create(context.Background(), &Slip{
		CorrelationID: id, Repository: "owner/repo", Branch: "main",
		CommitSHA: "sha-" + id, Status: SlipStatusInProgress,
	}))
}

func TestPostgresStore_UpdateStep_Pipeline_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()
	mustCreate(t, store, "c1")

	require.NoError(t, store.UpdateStep(ctx, "c1", "unit_tests", "", StepStatusRunning))
	got, err := store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, StepStatusRunning, got.Steps["unit_tests"].Status)

	require.NoError(t, store.UpdateStep(ctx, "c1", "unit_tests", "", StepStatusCompleted))
	got, err = store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, StepStatusCompleted, got.Steps["unit_tests"].Status)

	require.ErrorIs(t, store.UpdateStep(ctx, "ghost", "unit_tests", "", StepStatusRunning), ErrSlipNotFound)
}

func TestPostgresStore_ComponentAggregate_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()
	mustCreate(t, store, "c1")

	// One component running -> aggregate running.
	require.NoError(t, store.UpdateComponentStatus(ctx, "c1", "api", "component_builds", StepStatusRunning))
	got, err := store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, StepStatusRunning, got.Steps["builds"].Status)
	api := findComponent(got, "builds", "api")
	require.NotNil(t, api)
	require.NotNil(t, api.StartedAt, "running component records StartedAt")

	// Second component still running -> aggregate running.
	require.NoError(t, store.UpdateComponentStatus(ctx, "c1", "web", "component_builds", StepStatusRunning))

	// api completes -> aggregate still running (web running).
	require.NoError(t, store.UpdateComponentStatus(ctx, "c1", "api", "component_builds", StepStatusCompleted))
	got, err = store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, StepStatusRunning, got.Steps["builds"].Status)
	api = findComponent(got, "builds", "api")
	require.NotNil(t, api)
	assert.Equal(t, StepStatusCompleted, api.Status)
	assert.NotNil(t, api.StartedAt, "StartedAt preserved across running->completed")
	assert.NotNil(t, api.CompletedAt)

	// web completes -> all completed -> aggregate completed.
	require.NoError(t, store.UpdateComponentStatus(ctx, "c1", "web", "component_builds", StepStatusCompleted))
	got, err = store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, StepStatusCompleted, got.Steps["builds"].Status)
	assert.Len(t, got.Aggregates["builds"], 2)
}

func TestPostgresStore_TerminalGuard_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()
	mustCreate(t, store, "c1")

	require.NoError(t, store.UpdateComponentStatus(ctx, "c1", "api", "component_builds", StepStatusCompleted))

	// completed (terminal) -> running (non-terminal) is rejected.
	err := store.UpdateComponentStatus(ctx, "c1", "api", "component_builds", StepStatusRunning)
	require.ErrorIs(t, err, ErrTerminalAlreadyExists)
	got, err := store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, StepStatusCompleted, findComponent(got, "builds", "api").Status, "stale demotion rejected")

	// completed -> aborted (terminal -> terminal) is allowed.
	require.NoError(t, store.UpdateComponentStatus(ctx, "c1", "api", "component_builds", StepStatusAborted))
	// aborted -> pending (retry reset) is the one allowed terminal->non-terminal transition.
	require.NoError(t, store.UpdateComponentStatus(ctx, "c1", "api", "component_builds", StepStatusPending))
	got, err = store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, StepStatusPending, findComponent(got, "builds", "api").Status)
}

func TestPostgresStore_History_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()
	mustCreate(t, store, "c1")

	require.NoError(t, store.UpdateStepWithHistory(ctx, "c1", "unit_tests", "", StepStatusCompleted,
		StateHistoryEntry{Step: "unit_tests", Status: StepStatusCompleted, Actor: "ci", Message: "green"}))
	require.NoError(t, store.AppendHistory(ctx, "c1",
		StateHistoryEntry{Step: "dev_deploy", Status: StepStatusRunning, Actor: "cd"}))

	got, err := store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, StepStatusCompleted, got.Steps["unit_tests"].Status)
	require.Len(t, got.StateHistory, 2)
	assert.Equal(t, "unit_tests", got.StateHistory[0].Step)
	assert.Equal(t, "dev_deploy", got.StateHistory[1].Step)

	require.ErrorIs(t, store.AppendHistory(ctx, "ghost", StateHistoryEntry{Step: "x"}), ErrSlipNotFound)
}

func TestPostgresStore_UpdateSlipStatus_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()
	mustCreate(t, store, "c1")

	require.NoError(t, store.UpdateSlipStatus(ctx, "c1", SlipStatusCompleted))
	got, err := store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusCompleted, got.Status)

	require.ErrorIs(t, store.UpdateSlipStatus(ctx, "ghost", SlipStatusCompleted), ErrSlipNotFound)
}

func TestPostgresStore_SetComponentImageTag_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()
	mustCreate(t, store, "c1")

	require.NoError(t, store.UpdateComponentStatus(ctx, "c1", "api", "component_builds", StepStatusCompleted))
	// Aggregate-name step gets normalized to the component step type.
	require.NoError(t, store.SetComponentImageTag(ctx, "c1", "builds", "api", "registry/api:sha1"))

	got, err := store.Load(ctx, "c1")
	require.NoError(t, err)
	api := findComponent(got, "builds", "api")
	require.NotNil(t, api)
	assert.Equal(t, "registry/api:sha1", api.ImageTag)
	assert.Equal(t, StepStatusCompleted, api.Status, "status preserved when setting image tag")

	// Unknown component -> error.
	err = store.SetComponentImageTag(ctx, "c1", "component_builds", "ghost", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
