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

// stripSQLLineComments removes -- comments so an assertion can target what a migration
// EXECUTES rather than what its comments say.
func stripSQLLineComments(sql string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(sql, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// TestUniquenessMigration_V5 pins the shape of Phase B's migration (DEVOPS-231): the two
// cascade FKs on correlation_id and the plain unique index on (lower(repository), commit_sha).
//
// The negative assertions are the load-bearing ones. CONCURRENTLY cannot run inside the
// transaction the migrator wraps each migration in, so its presence would break every deploy,
// not merely waste time. And there is deliberately NO foreign key on
// slip_ancestry.parent_correlation_id — Repave's guarded DELETE runs while descendants still
// point at the old row, so a plain FK there would raise 23503 on every repave with a
// descendant (design spec §3 banner); an ON DELETE CASCADE there would delete a child's
// lineage row when its parent run is repaved (§7). Both FK adds swallow duplicate_object so a
// concurrent or repeated migrator run is a no-op — there is no "partial failure" to re-run
// after, since UpSQL and the version insert share one transaction — and each half of the
// migration ends in a post-condition asserting the shape a name match alone cannot prove.
func TestUniquenessMigration_V5(t *testing.T) {
	cfg := pgTestPipelineConfig(t)
	mgr := NewPostgresDynamicMigrationManager(cfg, nil)

	migs := mgr.GenerateMigrations()
	require.Len(t, migs, 5, "Phase B adds migration v5 on top of v1-v4")
	v5 := migs[4]
	assert.Equal(t, 5, v5.Version)
	assert.Equal(t, "one_slip_per_commit", v5.Name)
	assert.Equal(t, 5, mgr.LatestVersion())
	assert.Equal(t, 5, GetPostgresDynamicMigrationVersion(cfg))

	// Every assertion below runs against the SQL with -- comments stripped, because they are all
	// about what the migration EXECUTES, not how it is documented. No comment supplies any of
	// these literals today, and stripping is what keeps that from mattering: on the raw string a
	// Contains check would false-PASS on a statement commented out rather than deleted, since the
	// literal survives in the prose, and an exact count would false-FAIL on a comment that merely
	// mentions a counted phrase — already the case for CONCURRENTLY, which the index
	// post-condition must name to explain the state it rejects (raw 1, stripped 0).
	upDDL := stripSQLLineComments(v5.UpSQL)
	for _, want := range []string{
		"ALTER TABLE slip_component_states",
		"ADD CONSTRAINT fk_component_states_slip",
		"ALTER TABLE slip_ancestry",
		"ADD CONSTRAINT fk_ancestry_slip",
		"FOREIGN KEY (correlation_id) REFERENCES routing_slips(correlation_id)",
		"ON DELETE CASCADE",
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_routing_slips_repo_sha",
		"ON routing_slips (lower(repository), commit_sha)",
		// Post-conditions: a name match (duplicate_object / IF NOT EXISTS) is not proof of shape.
		"SELECT pg_get_constraintdef(oid), convalidated",
		"IF actual_def IS DISTINCT FROM expected_def OR NOT coalesce(is_valid, false)",
		"WHERE i.indrelid = 'routing_slips'::regclass",
		"AND ic.relname = 'uq_routing_slips_repo_sha'",
		"AND i.indisunique AND i.indisvalid AND i.indisready",
		// The expression, with the underscore escaped so LIKE cannot treat it as a wildcard.
		// Deliberately not pinning how the pattern literal is split across source lines.
		`ON %routing_slips USING btree (lower(repository), commit\_sha)`,
	} {
		assert.Contains(t, upDDL, want)
	}
	assert.Equal(t, 3, strings.Count(upDDL, "ON DELETE CASCADE"),
		"two cascade FK adds on correlation_id plus the post-condition's expected definition")
	assert.Equal(t, 2, strings.Count(upDDL, "duplicate_object"),
		"both FK adds must be no-ops for a repeated migrator run")
	assert.Equal(t, 2, strings.Count(upDDL, "RAISE EXCEPTION"), "one post-condition per half of the migration")
	assert.NotContains(t, upDDL, "CONCURRENTLY",
		"a plain build: CONCURRENTLY cannot run inside the migrator's per-migration transaction")
	assert.NotContains(t, upDDL, "parent_correlation_id",
		"no FK of any kind on slip_ancestry.parent_correlation_id — see the spec §3 banner and §7")
	assert.NotContains(
		t,
		upDDL,
		"public.",
		"schema-agnostic: pg_get_indexdef qualifies the table, so a hardcoded schema would reject a healthy index elsewhere",
	)

	down := stripSQLLineComments(v5.DownSQL)
	for _, want := range []string{
		// The index is dropped via the table, symmetrically with the UpSQL post-condition; a bare
		// name would resolve through search_path and could drop an unrelated index of that name.
		"WHERE i.indrelid = to_regclass('routing_slips')",
		"AND ic.relname = 'uq_routing_slips_repo_sha'",
		"EXECUTE format('DROP INDEX %s', idx)",
		"DROP CONSTRAINT IF EXISTS fk_ancestry_slip",
		"DROP CONSTRAINT IF EXISTS fk_component_states_slip",
	} {
		assert.Contains(t, down, want)
	}
}
