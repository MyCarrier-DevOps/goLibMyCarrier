package slippy

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pgTestPipelineConfig builds a small pipeline config exercising the generator:
// a plain first step, one aggregate step, a gate step, and a deploy step.
func pgTestPipelineConfig(t *testing.T) *PipelineConfig {
	t.Helper()
	const j = `{
		"version": "1.0",
		"name": "pg-test",
		"steps": [
			{"name": "push_parsed", "description": "push received"},
			{"name": "builds", "description": "container builds", "aggregates": "component_builds", "prerequisites": ["push_parsed"]},
			{"name": "unit_tests", "description": "unit tests", "prerequisites": ["builds"], "is_gate": true},
			{"name": "dev_deploy", "description": "deploy to dev", "prerequisites": ["unit_tests"]}
		]
	}`
	cfg, err := ParsePipelineConfig([]byte(j))
	require.NoError(t, err)
	return cfg
}

func TestPostgresDynamicMigrations_Generate(t *testing.T) {
	cfg := pgTestPipelineConfig(t)
	mgr := NewPostgresDynamicMigrationManager(cfg, nil)

	migs := mgr.GenerateMigrations()
	require.NotEmpty(t, migs)

	// Versions are sequential starting at 1.
	for i, mig := range migs {
		assert.Equal(t, i+1, mig.Version, "migration %d out of order", i)
		assert.NotEmpty(t, mig.UpSQL)
		assert.NotEmpty(t, mig.Name)
	}
	assert.Equal(t, len(migs), mgr.LatestVersion())
	assert.Equal(t, mgr.LatestVersion(), GetPostgresDynamicMigrationVersion(cfg))

	var allUp strings.Builder
	for _, m := range migs {
		allUp.WriteString(m.UpSQL)
		allUp.WriteString("\n")
	}
	up := allUp.String()

	// No ClickHouse-isms survive the port.
	for _, banned := range []string{"MergeTree", "Enum8", "DateTime64", " sign ", "UInt64", "PARTITION BY toYYYYMM"} {
		assert.NotContains(t, up, banned, "generated Postgres DDL must not contain ClickHouse construct %q", banned)
	}

	// Postgres constructs are present.
	assert.Contains(t, up, "CREATE DOMAIN")
	assert.Contains(t, up, "slip_status")
	assert.Contains(t, up, "step_status")
	assert.Contains(t, up, "jsonb")
	assert.Contains(t, up, "timestamptz")
	assert.Contains(t, up, "correlation_id text PRIMARY KEY")
	assert.Contains(t, up, "PRIMARY KEY (correlation_id, step, component)")
	assert.Contains(t, up, "PRIMARY KEY (repository, branch, correlation_id)")
}

func TestPostgresDynamicEnsurers_PerStep(t *testing.T) {
	cfg := pgTestPipelineConfig(t)
	ensurers := NewPostgresDynamicMigrationManager(cfg, nil).GenerateEnsurers()
	require.NotEmpty(t, ensurers)

	var all strings.Builder
	for _, e := range ensurers {
		assert.NotEmpty(t, e.Name)
		assert.NotEmpty(t, e.SQL)
		all.WriteString(e.SQL)
		all.WriteString("\n")
	}
	sql := all.String()

	// Every step gets an idempotent status column.
	for _, s := range cfg.Steps {
		want := fmt.Sprintf("ADD COLUMN IF NOT EXISTS %s_status step_status", s.Name)
		assert.Contains(t, sql, want, "missing status-column ensurer for step %q", s.Name)
	}

	// The aggregate step additionally gets a jsonb component column named after the step.
	aggs := cfg.GetAggregateSteps()
	require.NotEmpty(t, aggs)
	for _, s := range aggs {
		want := fmt.Sprintf("ADD COLUMN IF NOT EXISTS %s jsonb", s.Name)
		assert.Contains(t, sql, want, "missing aggregate jsonb ensurer for step %q", s.Name)
	}

	// Ensurers must be idempotent (safe to re-run every deploy).
	assert.Contains(t, sql, "IF NOT EXISTS")
	assert.NotContains(t, sql, "Enum8")
}

func TestGetPostgresDynamicMigrationVersion_EmptyConfig(t *testing.T) {
	assert.Equal(t, 0, GetPostgresDynamicMigrationVersion(nil))
	assert.Equal(t, 0, GetPostgresDynamicMigrationVersion(&PipelineConfig{}))
}

func TestGetPostgresDynamic_FreeFuncs(t *testing.T) {
	cfg := pgTestPipelineConfig(t)
	assert.NotEmpty(t, GetPostgresDynamicMigrations(cfg, nil))
	assert.Len(t, GetPostgresDynamicEnsurers(cfg, nil), len(cfg.Steps)+1)
}

func TestMigrateDirection(t *testing.T) {
	assert.Equal(t, "up", migrateDirection(0, 4))
	assert.Equal(t, "down", migrateDirection(4, 1))
	assert.Equal(t, "none", migrateDirection(2, 2))
}

func TestAbsInt(t *testing.T) {
	assert.Equal(t, 3, absInt(-3))
	assert.Equal(t, 3, absInt(3))
	assert.Equal(t, 0, absInt(0))
}

// TestIndexEnsurer_EmitsEveryIndex pins the index names the ensurer emits.
//
// Nothing asserted these before: TestPostgresDynamicEnsurers_PerStep checks the per-step column
// DDL and a generic "IF NOT EXISTS", and TestGetPostgresDynamic_FreeFuncs counts ensurers —
// GenerateEnsurers appends exactly one index ensurer, so the statement count inside it cannot
// move that number. Deleting any CREATE INDEX line was green.
//
// That mattered most for the two slip_ancestry indexes, which the ensurer's own comment argues
// are load-bearing: Repave runs three slip_ancestry statements inside the transaction already
// holding the superseded row's delete lock, under a 30s statement timeout, with a repave failure
// now fatal to the push. Before them, correlation_id was reachable only as a full index scan and
// parent_correlation_id had no index at all.
//
// This is a change-detector by construction — it proves the string is emitted, not that an index
// exists. TestPostgresMigrations_IndexesExist_Integration is the behavioural half.
func TestIndexEnsurer_EmitsEveryIndex(t *testing.T) {
	m := &PostgresDynamicMigrationManager{config: testPipelineConfig()}
	sql := m.indexEnsurer().SQL

	// The five unconditional indexes.
	for _, name := range []string{
		"idx_routing_slips_repo",
		"idx_routing_slips_commit",
		"idx_routing_slips_status",
		"idx_slip_ancestry_correlation",
		"idx_slip_ancestry_parent",
	} {
		assert.Contains(t, sql, "CREATE INDEX IF NOT EXISTS "+name+" ",
			"indexEnsurer must emit %s", name)
	}

	// The deploy-status indexes are conditional on the step existing, because their columns are
	// created by the step ensurers. Derived from the config rather than hardcoded, so this states
	// the rule instead of restating whatever testPipelineConfig happens to carry.
	for _, stepName := range []string{"dev_deploy", "preprod_deploy", "prod_deploy"} {
		stmt := "CREATE INDEX IF NOT EXISTS idx_" + stepName + "_status ON routing_slips (" +
			stepName + "_status)"
		if m.config.GetStep(stepName) != nil {
			assert.Contains(t, sql, stmt, "%s is configured, so its index must be emitted", stepName)
		} else {
			assert.NotContains(t, sql, stmt,
				"%s is not configured, so no step ensurer creates the column this would index", stepName)
		}
	}

	// The two repave indexes must be on the columns Repave actually filters, not merely present
	// under the right name — an index on the wrong column is the failure this guards.
	assert.Contains(t, sql, "idx_slip_ancestry_correlation ON slip_ancestry (correlation_id)")
	assert.Contains(t, sql, "idx_slip_ancestry_parent ON slip_ancestry (parent_correlation_id)")
}

// TestIndexEnsurer_OmitsDeployIndexesForAbsentSteps is the complement: the deploy-status indexes
// are conditional on the step existing, because their columns are created by the step ensurers.
// Without this, emitting them unconditionally would satisfy the table above.
func TestIndexEnsurer_OmitsDeployIndexesForAbsentSteps(t *testing.T) {
	m := &PostgresDynamicMigrationManager{config: &PipelineConfig{Steps: []StepConfig{{Name: "build"}}}}
	sql := m.indexEnsurer().SQL

	assert.Contains(t, sql, "idx_slip_ancestry_correlation",
		"the unconditional indexes are still emitted")
	for _, name := range []string{"idx_dev_deploy_status", "idx_preprod_deploy_status", "idx_prod_deploy_status"} {
		assert.NotContains(t, sql, name,
			"%s indexes a column no step ensurer creates for this config", name)
	}
}
