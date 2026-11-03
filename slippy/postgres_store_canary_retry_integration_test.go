//go:build integration

package slippy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// canaryRetryPipelineConfig carries the release steps scenario H touches.
func canaryRetryPipelineConfig(t *testing.T) *PipelineConfig {
	t.Helper()
	const j = `{
		"version": "1.0",
		"name": "pg-canary-retry",
		"steps": [
			{"name": "push_parsed", "description": "push received"},
			{"name": "prod_canary", "description": "canary", "prerequisites": ["push_parsed"]},
			{"name": "prod_deploy", "description": "prod deploy", "prerequisites": ["prod_canary"]},
			{"name": "prod_tests", "description": "prod tests", "prerequisites": ["prod_deploy"]}
		]
	}`
	cfg, err := ParsePipelineConfig([]byte(j))
	require.NoError(t, err)
	return cfg
}

// Scenario H against real Postgres (DEVOPS-554): the D13 skip reason survives the state_history
// round trip, so a retried canary that completes resets the canary-skipped prod_deploy and
// prod_tests to pending; a non-canary skip reason on the same path stays skipped.
func TestPostgresStore_CanaryRetryResetsCanarySkips_Integration(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		want   StepStatus
	}{
		{"canary reason resets", "canary_abort", StepStatusPending},
		{"non-canary reason stays skipped", "deployment-strategy property is not canary", StepStatusSkipped},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			pool := newPGMigrationTestPool(t)
			cfg := canaryRetryPipelineConfig(t)
			_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
			require.NoError(t, err)
			store, err := NewPostgresStore(pool, cfg, nil)
			require.NoError(t, err)
			mustCreate(t, store, "c1")
			client := NewClientWithDependencies(store, newMockGitHubAPIForE2E(), Config{
				PipelineConfig: cfg,
				Logger:         &e2eTestLogger{t: t},
			})

			require.NoError(t, client.FailStep(ctx, "c1", "prod_canary", "", "canary_abort"))
			for _, s := range []string{"prod_deploy", "prod_tests"} {
				require.NoError(t, client.SkipStep(ctx, "c1", s, "", tt.reason))
			}
			got, err := store.Load(ctx, "c1")
			require.NoError(t, err)
			require.Equal(t, SlipStatusFailed, got.Status)

			// Backdate the step rows past the freshness window (no sleeps): the retry is minutes later
			// in a real release, so its non-terminal writes over terminal steps are re-runs.
			_, err = pool.Exec(ctx,
				"UPDATE slip_component_states SET updated_at = now() - interval '1 hour' WHERE correlation_id = $1",
				"c1")
			require.NoError(t, err)

			require.NoError(t, client.StartStep(ctx, "c1", "prod_canary", ""))
			require.NoError(t, client.CompleteStep(ctx, "c1", "prod_canary", ""))

			got, err = store.Load(ctx, "c1")
			require.NoError(t, err)
			assert.Equal(t, SlipStatusInProgress, got.Status)
			for _, s := range []string{"prod_deploy", "prod_tests"} {
				assert.Equal(t, tt.want, got.Steps[s].Status, "%s after the canary retry", s)
			}
		})
	}
}
