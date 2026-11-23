//go:build integration

package slippy

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// retiredStepConfigs returns the example pipeline config twice: with the retired rehearsal step
// as the live config carries it before the cutover (a prerequisite of prod_gate, after
// preprod_tests), and without it. Both come from production.json, so the shapes track the shipped
// example instead of a hand-written copy of it.
func retiredStepConfigs(t *testing.T) (with, without *PipelineConfig) {
	t.Helper()
	base, err := LoadPipelineConfigFromFile("production.json")
	require.NoError(t, err)

	build := func(include bool) *PipelineConfig {
		var steps []StepConfig
		for _, step := range base.Steps {
			if step.Name == retiredRehearsalStep {
				continue
			}
			step.Prerequisites = slices.DeleteFunc(slices.Clone(step.Prerequisites), func(p string) bool {
				return p == retiredRehearsalStep
			})
			if step.Name == "prod_gate" && include {
				step.Prerequisites = append(step.Prerequisites, retiredRehearsalStep)
			}
			steps = append(steps, step)
			if step.Name == "preprod_tests" && include {
				steps = append(steps, StepConfig{
					Name:          retiredRehearsalStep,
					Description:   "rehearsal",
					Prerequisites: []string{"preprod_tests"},
				})
			}
		}
		raw, err := json.Marshal(PipelineConfig{Version: base.Version, Name: base.Name, Steps: steps})
		require.NoError(t, err)
		cfg, err := ParsePipelineConfig(raw)
		require.NoError(t, err)
		return cfg
	}
	return build(true), build(false)
}

func retiredStepClient(t *testing.T, store *PostgresStore, cfg *PipelineConfig) *Client {
	t.Helper()
	return NewClientWithDependencies(store, newMockGitHubAPIForE2E(), Config{
		PipelineConfig: cfg,
		Logger:         &e2eTestLogger{t: t},
		HoldTimeout:    2 * time.Second,
		PollInterval:   50 * time.Millisecond,
	})
}

func pgColumnExists(t *testing.T, pool *pgxpool.Pool, table, column string) bool {
	t.Helper()
	var exists bool
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = $1 AND column_name = $2)`,
		table, column).Scan(&exists))
	return exists
}

// R3 against real Postgres. The live config no longer has the step, and a workflow that started
// before the cutover (create-github-release step 0, the rehearsal's own pre/post-job, TestEngine)
// still writes it. Every write returns nil. The store records the event in slip_component_states
// and state_history and writes no status column (there is none to write); the step never appears
// in a loaded slip and never moves the slip to failed.
func TestRetiredStep_WritesAreAccepted_Integration(t *testing.T) {
	_, without := retiredStepConfigs(t)
	column := stepStatusColumn(retiredRehearsalStep)

	writes := map[string]func(context.Context, *Client, string) error{
		"start": func(ctx context.Context, c *Client, id string) error {
			return c.StartStep(ctx, id, retiredRehearsalStep, "", "run-1")
		},
		"complete": func(ctx context.Context, c *Client, id string) error {
			return c.CompleteStep(ctx, id, retiredRehearsalStep, "")
		},
		"fail": func(ctx context.Context, c *Client, id string) error {
			return c.FailStep(ctx, id, retiredRehearsalStep, "", "rehearsal failed")
		},
		"skip": func(ctx context.Context, c *Client, id string) error {
			return c.SkipStep(ctx, id, retiredRehearsalStep, "", "deployment-strategy is not canary")
		},
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			pool := newPGMigrationTestPool(t)
			_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: without})
			require.NoError(t, err)
			store, err := NewPostgresStore(pool, without, nil)
			require.NoError(t, err)
			mustCreate(t, store, "c1")
			client := retiredStepClient(t, store, without)

			require.NoError(t, write(ctx, client, "c1"))

			require.False(t, pgColumnExists(t, pool, "routing_slips", column),
				"the fresh schema has no column for the retired step, so a column write would have failed")
			got, err := store.Load(ctx, "c1")
			require.NoError(t, err)
			assert.NotContains(t, got.Steps, retiredRehearsalStep)
			assert.Equal(t, SlipStatusInProgress, got.Status, "the retired step must not fail the slip")

			var events int
			require.NoError(t, pool.QueryRow(ctx,
				"SELECT count(*) FROM slip_component_states WHERE correlation_id = $1 AND step = $2",
				"c1", retiredRehearsalStep).Scan(&events))
			assert.Equal(t, 1, events, "the event is recorded in slip_component_states")
			assert.True(t, slices.ContainsFunc(got.StateHistory, func(e StateHistoryEntry) bool {
				return e.Step == retiredRehearsalStep
			}), "the event is recorded in state_history")
		})
	}
}

// R4 and R5 against real Postgres. A slip is created while the config still has the step, with
// the step pending or failed. The config then drops the step and the migrator runs again. The
// slip passes prod_gate once preprod_deploy and preprod_tests succeed, the step never shows in
// the slip, and the migration keeps the column.
func TestRetiredStep_SlipCreatedWithStep_PassesProdGateWithoutIt_Integration(t *testing.T) {
	with, without := retiredStepConfigs(t)
	column := stepStatusColumn(retiredRehearsalStep)

	for _, legacy := range []StepStatus{StepStatusPending, StepStatusFailed} {
		t.Run(string(legacy), func(t *testing.T) {
			ctx := context.Background()
			pool := newPGMigrationTestPool(t)

			// Before the cutover: the live config has the step.
			_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: with})
			require.NoError(t, err)
			oldStore, err := NewPostgresStore(pool, with, nil)
			require.NoError(t, err)
			mustCreate(t, oldStore, "c1")
			oldClient := retiredStepClient(t, oldStore, with)
			require.NoError(t, oldClient.CompleteStep(ctx, "c1", "preprod_deploy", ""))
			require.NoError(t, oldClient.CompleteStep(ctx, "c1", "preprod_tests", ""))
			if legacy == StepStatusFailed {
				require.NoError(t, oldClient.FailStep(ctx, "c1", retiredRehearsalStep, "", "rehearsal failed"))
			}
			before, err := oldStore.Load(ctx, "c1")
			require.NoError(t, err)
			require.Equal(t, legacy, before.Steps[retiredRehearsalStep].Status)

			// The cutover: the config drops the step and the migrator runs against the same database.
			_, err = RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: without})
			require.NoError(t, err)
			require.NoError(t, ValidatePostgresSchema(ctx, pool, without))
			assert.True(t, pgColumnExists(t, pool, "routing_slips", column), "R5: the migration keeps the column")

			newStore, err := NewPostgresStore(pool, without, nil)
			require.NoError(t, err)
			require.NoError(t, newStore.ProbeSchema(ctx))
			newClient := retiredStepClient(t, newStore, without)

			got, err := newStore.Load(ctx, "c1")
			require.NoError(t, err)
			assert.NotContains(t, got.Steps, retiredRehearsalStep, "the slip no longer carries the step")
			if legacy == StepStatusFailed {
				// The stored slip status is only recomputed by a terminal pipeline-level write, so
				// it still reads failed here.
				assert.Equal(t, SlipStatusFailed, got.Status)
			}

			// prod_gate's prerequisites are the live config's, read at evaluation time.
			gate := without.GetStep("prod_gate")
			require.NotNil(t, gate)
			require.Equal(t, []string{"preprod_deploy", "preprod_tests"}, gate.Prerequisites)
			res, err := newClient.CheckPrerequisites(ctx, got, gate.Prerequisites, "")
			require.NoError(t, err)
			assert.Equal(t, PrereqStatusCompleted, res.Status)
			require.NoError(t, newClient.WaitForPrerequisites(ctx, HoldOptions{
				CorrelationID: "c1", Prerequisites: gate.Prerequisites, StepName: "prod_gate",
			}))

			// The next terminal pipeline-level write reconciles the slip: the retired step is not a
			// primary failure, so nothing keeps it failed.
			require.NoError(t, newClient.StartStep(ctx, "c1", "prod_gate", "", "run-2"))
			require.NoError(t, newClient.CompleteStep(ctx, "c1", "prod_gate", ""))
			after, err := newStore.Load(ctx, "c1")
			require.NoError(t, err)
			assert.Equal(t, StepStatusCompleted, after.Steps["prod_gate"].Status)
			assert.Equal(t, SlipStatusInProgress, after.Status, "R4: the retired step must not fail the slip")
			assert.NotContains(t, after.Steps, retiredRehearsalStep)
		})
	}
}

// R5: a slip created after the cutover still gets a value in the kept column. The migration
// added it as NOT NULL DEFAULT 'pending', and no write names it any more, so it reads pending
// for every new slip. Anything that mirrors routing_slips column by column sees that constant.
func TestRetiredStep_KeptColumnReadsPendingForNewSlips_Integration(t *testing.T) {
	ctx := context.Background()
	with, without := retiredStepConfigs(t)
	pool := newPGMigrationTestPool(t)
	_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: with})
	require.NoError(t, err)
	_, err = RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: without})
	require.NoError(t, err)
	store, err := NewPostgresStore(pool, without, nil)
	require.NoError(t, err)
	mustCreate(t, store, "c2")

	var kept string
	require.NoError(t, pool.QueryRow(
		ctx,
		"SELECT "+stepStatusColumn(
			retiredRehearsalStep,
		)+" FROM routing_slips WHERE correlation_id = $1",
		"c2",
	).Scan(&kept))
	assert.Equal(t, "pending", kept)
}
