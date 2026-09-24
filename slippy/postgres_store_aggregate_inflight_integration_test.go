//go:build integration

package slippy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DEVOPS-373 against real Postgres: a componentless write to an aggregate step with no reported
// component lands on the step's own column, so RunInFlight sees a start; once a component has
// reported, the rollup decides the column and a componentless write changes nothing.
func TestPostgresStore_ComponentlessAggregateWrite_Integration(t *testing.T) {
	tests := []struct {
		name         string
		status       StepStatus
		wantInFlight bool
	}{
		{"running is in flight", StepStatusRunning, true},
		{"held is in flight", StepStatusHeld, true},
		{"completed is quiescent", StepStatusCompleted, false},
		{"skipped is quiescent", StepStatusSkipped, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _, _ := newMigratedStore(t)
			ctx := context.Background()
			mustCreate(t, store, "c1")

			require.NoError(t, store.UpdateStep(ctx, "c1", "builds", "", tt.status))

			got, err := store.Load(ctx, "c1")
			require.NoError(t, err)
			assert.Equal(t, tt.status, got.Steps["builds"].Status)
			assert.Empty(t, got.Aggregates["builds"], "no component reported, so nothing is rolled up")
			assert.Equal(t, tt.wantInFlight, RunInFlight(got))
		})
	}
}

func TestPostgresStore_ComponentlessAggregateWrite_RollupWinsOnceAComponentReports_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()
	mustCreate(t, store, "c1")

	require.NoError(t, store.UpdateStep(ctx, "c1", "builds", "", StepStatusRunning))
	require.NoError(t, store.UpdateComponentStatus(ctx, "c1", "api", "component_builds", StepStatusRunning))
	require.NoError(t, store.UpdateComponentStatus(ctx, "c1", "api", "component_builds", StepStatusCompleted))

	got, err := store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, StepStatusCompleted, got.Steps["builds"].Status, "the rollup over [api completed]")
	assert.False(t, RunInFlight(got))

	// A componentless write after a component has reported does not override the rollup.
	require.NoError(t, store.UpdateStep(ctx, "c1", "builds", "", StepStatusFailed))
	got, err = store.Load(ctx, "c1")
	require.NoError(t, err)
	assert.Equal(t, StepStatusCompleted, got.Steps["builds"].Status)
}
