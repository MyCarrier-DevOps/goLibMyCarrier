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
			{"name": "prod_tests", "description": "prod tests", "prerequisites": ["prod_deploy"]},
			{"name": "prod_alert_gate", "description": "alert gate"}
		]
	}`
	cfg, err := ParsePipelineConfig([]byte(j))
	require.NoError(t, err)
	return cfg
}

// Scenario H against real Postgres (DEVOPS-554): the D13 skip reasons survive the state_history
// round trip, so a retried canary that completes resets the canary-skipped prod_deploy, prod_tests
// and prod_alert_gate to pending; non-canary skip reasons on the same path stay skipped.
func TestPostgresStore_CanaryRetryResetsCanarySkips_Integration(t *testing.T) {
	tests := []struct {
		name    string
		reasons map[string]string
		want    StepStatus
	}{
		{"canary reasons reset", map[string]string{
			"prod_deploy":     "canary_abort",
			"prod_tests":      "canary_abort",
			"prod_alert_gate": "canary_not_promoted:Aborted",
		}, StepStatusPending},
		{"non-canary reasons stay skipped", map[string]string{
			"prod_deploy":     "deployment-strategy property is not canary",
			"prod_tests":      "deployment-strategy property is not canary",
			"prod_alert_gate": "auto-rollback-enabled property not set to true — alert-gate skipped",
		}, StepStatusSkipped},
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
			for s, reason := range tt.reasons {
				require.NoError(t, client.SkipStep(ctx, "c1", s, "", reason))
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

			require.NoError(t, client.StartStep(ctx, "c1", "prod_canary", "", ""))
			require.NoError(t, client.CompleteStep(ctx, "c1", "prod_canary", ""))

			got, err = store.Load(ctx, "c1")
			require.NoError(t, err)
			assert.Equal(t, SlipStatusInProgress, got.Status)
			for s := range tt.reasons {
				assert.Equal(t, tt.want, got.Steps[s].Status, "%s after the canary retry", s)
			}
		})
	}
}
