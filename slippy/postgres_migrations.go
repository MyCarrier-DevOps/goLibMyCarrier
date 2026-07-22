package slippy

import (
	"fmt"
	"strings"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/postgresmigrator"
)

// PostgresDynamicMigrationManager generates Postgres schema migrations and ensurers
// from a pipeline configuration. It is the Postgres counterpart of
// DynamicMigrationManager (ClickHouse).
//
// Unlike the ClickHouse schema, the Postgres schema is greenfield (a hard cutover,
// with no pre-existing Postgres version history to preserve). Core migrations therefore
// start at version 1 with the final table shapes directly — there is no need to replay
// ClickHouse's version evolution (materialized-view create-then-drop, inline-ancestry
// add-then-drop). The Postgres port also collapses ClickHouse's async-merge machinery:
//   - no sign / version columns and no VersionedCollapsingMergeTree — Postgres updates
//     rows in place under MVCC;
//   - Enum8 becomes a text DOMAIN + CHECK (slip_status, step_status);
//   - JSON becomes jsonb, DateTime64 becomes timestamptz;
//   - slip_component_states is a current-state table keyed by
//     (correlation_id, step, component), not an append-only ReplacingMergeTree log.
//
// Per-step status columns and aggregate jsonb columns are emitted as idempotent
// ensurers (ADD COLUMN IF NOT EXISTS), exactly as in the ClickHouse manager, so the
// schema tracks the pipeline config without new versioned migrations.
type PostgresDynamicMigrationManager struct {
	config *PipelineConfig
	logger postgresmigrator.Logger
}

// NewPostgresDynamicMigrationManager creates a manager for the given pipeline config.
// A nil logger is replaced with a no-op logger.
func NewPostgresDynamicMigrationManager(
	config *PipelineConfig,
	logger postgresmigrator.Logger,
) *PostgresDynamicMigrationManager {
	if logger == nil {
		logger = &postgresmigrator.NopLogger{}
	}
	return &PostgresDynamicMigrationManager{config: config, logger: logger}
}

// GenerateMigrations returns the core versioned migrations (enums + base tables).
// These run once and define the base schema; per-step columns are ensurers.
func (m *PostgresDynamicMigrationManager) GenerateMigrations() []postgresmigrator.Migration {
	return []postgresmigrator.Migration{
		m.enumsMigration(),
		m.routingSlipsMigration(),
		m.componentStatesMigration(),
		m.ancestryMigration(),
	}
}

// LatestVersion returns the highest core migration version.
func (m *PostgresDynamicMigrationManager) LatestVersion() int {
	migs := m.GenerateMigrations()
	latest := 0
	for _, mig := range migs {
		if mig.Version > latest {
			latest = mig.Version
		}
	}
	return latest
}

// GenerateEnsurers returns idempotent schema operations for the dynamic (config-driven)
// columns: one status column per step, an extra jsonb column per aggregate step, plus
// the secondary indexes. They use ADD COLUMN / CREATE INDEX IF NOT EXISTS so they are
// safe to run on every deploy.
func (m *PostgresDynamicMigrationManager) GenerateEnsurers() []postgresmigrator.SchemaEnsurer {
	ensurers := make([]postgresmigrator.SchemaEnsurer, 0, len(m.config.Steps)+1)
	for _, step := range m.config.Steps {
		ensurers = append(ensurers, m.stepColumnEnsurer(step))
	}
	ensurers = append(ensurers, m.indexEnsurer())
	return ensurers
}

// enumsMigration creates the slip_status and step_status text DOMAINs. Each CREATE is
// wrapped in a sub-block that swallows duplicate_object so the migration is safe even
// if a DOMAIN already exists (e.g. left over from an earlier schema).
func (m *PostgresDynamicMigrationManager) enumsMigration() postgresmigrator.Migration {
	return postgresmigrator.Migration{
		Version:     1,
		Name:        "create_slip_enums",
		Description: "Creates slip_status and step_status text DOMAINs (Enum8 replacement)",
		UpSQL: `
			DO $$
			BEGIN
				BEGIN
					CREATE DOMAIN slip_status AS text
						CHECK (VALUE IN ('pending','in_progress','completed','failed',
						                 'compensating','compensated','abandoned','promoted'));
				EXCEPTION WHEN duplicate_object THEN NULL;
				END;
				BEGIN
					CREATE DOMAIN step_status AS text
						CHECK (VALUE IN ('pending','held','running','completed',
						                 'failed','error','aborted','timeout','skipped'));
				EXCEPTION WHEN duplicate_object THEN NULL;
				END;
			END $$;
		`,
		DownSQL: `DROP DOMAIN IF EXISTS slip_status, step_status CASCADE`,
	}
}

// routingSlipsMigration creates the core routing_slips table (core columns only; the
// per-step status/aggregate columns are added by ensurers). No sign/version columns.
func (m *PostgresDynamicMigrationManager) routingSlipsMigration() postgresmigrator.Migration {
	return postgresmigrator.Migration{
		Version:     2,
		Name:        "create_routing_slips",
		Description: "Creates the core routing_slips table (correlation_id PK, no sign/version)",
		UpSQL: `
			CREATE TABLE IF NOT EXISTS routing_slips (
				correlation_id text PRIMARY KEY,
				repository     text NOT NULL,
				branch         text NOT NULL,
				commit_sha     text NOT NULL,
				created_at     timestamptz NOT NULL DEFAULT now(),
				updated_at     timestamptz NOT NULL DEFAULT now(),
				status         slip_status NOT NULL DEFAULT 'pending',
				step_details   jsonb NOT NULL DEFAULT '{}',
				state_history  jsonb NOT NULL DEFAULT '{"entries":[]}'
			)
		`,
		DownSQL: `DROP TABLE IF EXISTS routing_slips`,
	}
}

// componentStatesMigration creates the current-state component table. One row per
// (correlation_id, step, component); component=” is the pipeline-level sentinel.
// Updates are ON CONFLICT upserts, so there is no append-only event log to dedup.
func (m *PostgresDynamicMigrationManager) componentStatesMigration() postgresmigrator.Migration {
	return postgresmigrator.Migration{
		Version:     3,
		Name:        "create_slip_component_states",
		Description: "Creates the current-state slip_component_states table (PK correlation_id,step,component)",
		UpSQL: `
			CREATE TABLE IF NOT EXISTS slip_component_states (
				correlation_id text NOT NULL,
				step           text NOT NULL,
				component      text NOT NULL DEFAULT '',
				status         step_status NOT NULL,
				message        text NOT NULL DEFAULT '',
				image_tag      text NOT NULL DEFAULT '',
				updated_at     timestamptz NOT NULL DEFAULT now(),
				PRIMARY KEY (correlation_id, step, component)
			)
		`,
		DownSQL: `DROP TABLE IF EXISTS slip_component_states`,
	}
}

// ancestryMigration creates the slip_ancestry table of direct parent links.
func (m *PostgresDynamicMigrationManager) ancestryMigration() postgresmigrator.Migration {
	return postgresmigrator.Migration{
		Version:     4,
		Name:        "create_slip_ancestry",
		Description: "Creates the slip_ancestry table of direct parent links (PK repository,branch,correlation_id)",
		UpSQL: `
			CREATE TABLE IF NOT EXISTS slip_ancestry (
				repository            text NOT NULL,
				branch                text NOT NULL,
				correlation_id        text NOT NULL,
				parent_correlation_id text NOT NULL,
				parent_commit_sha     text NOT NULL,
				parent_status         slip_status NOT NULL,
				parent_failed_step    text NOT NULL DEFAULT '',
				parent_repository     text NOT NULL DEFAULT '',
				parent_branch         text NOT NULL DEFAULT '',
				created_at            timestamptz NOT NULL DEFAULT now(),
				PRIMARY KEY (repository, branch, correlation_id)
			)
		`,
		DownSQL: `DROP TABLE IF EXISTS slip_ancestry`,
	}
}

// stepColumnEnsurer builds the idempotent ALTER TABLE for a step's columns: always a
// {step}_status column, plus a {step} jsonb column when the step aggregates components.
func (m *PostgresDynamicMigrationManager) stepColumnEnsurer(step StepConfig) postgresmigrator.SchemaEnsurer {
	statusColumn := fmt.Sprintf("%s_status", step.Name)

	var sql strings.Builder
	fmt.Fprintf(&sql,
		"ALTER TABLE routing_slips\n\tADD COLUMN IF NOT EXISTS %s step_status NOT NULL DEFAULT 'pending'",
		statusColumn)

	description := fmt.Sprintf("Ensures %s column exists for step '%s'", statusColumn, step.Name)
	if step.Aggregates != "" {
		// Aggregate column name is the step name (e.g. "builds").
		fmt.Fprintf(&sql,
			",\n\tADD COLUMN IF NOT EXISTS %s jsonb NOT NULL DEFAULT '{\"items\":[]}'",
			step.Name)
		description += fmt.Sprintf(" and %s jsonb column for component data", step.Name)
	}

	return postgresmigrator.SchemaEnsurer{
		Name:        fmt.Sprintf("ensure_step_%s", step.Name),
		Description: description,
		SQL:         sql.String(),
	}
}

// indexEnsurer builds idempotent secondary indexes on routing_slips. Each CREATE INDEX
// IF NOT EXISTS is its own statement; pgx sends a parameterless multi-statement Exec via
// the simple protocol.
func (m *PostgresDynamicMigrationManager) indexEnsurer() postgresmigrator.SchemaEnsurer {
	var sql strings.Builder
	sql.WriteString(
		"CREATE INDEX IF NOT EXISTS idx_routing_slips_repo ON routing_slips (lower(repository));\n")
	sql.WriteString(
		"CREATE INDEX IF NOT EXISTS idx_routing_slips_commit ON routing_slips (commit_sha);\n")
	sql.WriteString(
		"CREATE INDEX IF NOT EXISTS idx_routing_slips_status ON routing_slips (status);\n")

	// Indexes on deploy-step status columns commonly filtered for held slips. Only emitted
	// for steps that exist in the config (the columns are created by their step ensurers).
	for _, stepName := range []string{"dev_deploy", "preprod_deploy", "prod_deploy"} {
		if m.config.GetStep(stepName) != nil {
			fmt.Fprintf(&sql,
				"CREATE INDEX IF NOT EXISTS idx_%s_status ON routing_slips (%s_status);\n",
				stepName, stepName)
		}
	}

	return postgresmigrator.SchemaEnsurer{
		Name:        "ensure_secondary_indexes",
		Description: "Ensures secondary indexes exist on routing_slips",
		SQL:         sql.String(),
	}
}

// GetPostgresDynamicMigrations returns the core versioned migrations for the config.
func GetPostgresDynamicMigrations(
	config *PipelineConfig,
	logger postgresmigrator.Logger,
) []postgresmigrator.Migration {
	return NewPostgresDynamicMigrationManager(config, logger).GenerateMigrations()
}

// GetPostgresDynamicEnsurers returns the idempotent ensurers for the config.
func GetPostgresDynamicEnsurers(
	config *PipelineConfig,
	logger postgresmigrator.Logger,
) []postgresmigrator.SchemaEnsurer {
	return NewPostgresDynamicMigrationManager(config, logger).GenerateEnsurers()
}

// GetPostgresDynamicMigrationVersion returns the latest core migration version for the
// config, or 0 when the config is nil or has no steps.
func GetPostgresDynamicMigrationVersion(config *PipelineConfig) int {
	if config == nil || len(config.Steps) == 0 {
		return 0
	}
	return NewPostgresDynamicMigrationManager(config, nil).LatestVersion()
}
