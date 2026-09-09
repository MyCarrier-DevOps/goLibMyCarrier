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
		m.uniquenessMigration(),
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

// uniquenessMigration is DEVOPS-231 Phase B: one routing_slips row per
// (lower(repository), commit_sha), enforced by the database, plus the cascade FKs that let
// Repave's single guarded DELETE take the superseded run's child rows with it.
//
// PRECONDITION — a one-time cleanup script must have run in this environment first, in this
// order: delete orphaned child rows, dedupe to one row per commit (non-terminal survivor, else
// newest), delete the losers' children explicitly (the cascade does not exist yet). The FK
// ADDs validate existing data and the unique index build fails on duplicates, so on an
// uncleaned database this migration fails LOUDLY and the migrator rolls it back — the FKs are
// added in the same transaction as the index, so nothing is left half-applied. A failure here
// means the cleanup has not run; do NOT weaken this migration to get past it.
//
// Sequencing — the index must never be live while a pre-repave slippy-api runs: the old
// failed-path (AbandonSlip + insert) creates a second row for the same commit and would
// 23505-fail every same-commit retrigger. Release order is repave code (v1.3.100) deployed →
// cleanup per environment → this migration. See the design spec §5.
//
// Plain CREATE UNIQUE INDEX, not CONCURRENTLY: the table is ~12.5k rows so the build is
// near-instant, and CONCURRENTLY cannot run inside the transaction the migrator wraps each
// migration in — it would break the migrator, not merely waste time.
//
// Idempotent by NAME, asserted by SHAPE. `duplicate_object` is swallowed and the index is
// IF NOT EXISTS so a concurrent or repeated migrator run is a no-op (postgresmigrator relies on
// that), but a name match is not proof of definition: a pre-existing same-named object with
// another shape — a NO ACTION or NOT VALID FK, an index built without lower(), a leftover from
// a failed CONCURRENTLY build, even a table of that name — would otherwise be kept silently and
// v5 recorded with the guarantee absent. Each half therefore ends in a post-condition that
// asserts exactly what the code depends on (the FKs' pg_get_constraintdef text plus
// convalidated; a UNIQUE, valid, ready btree index on exactly (lower(repository), commit_sha))
// and RAISEs otherwise. The fix for that failure is to drop the foreign object and re-run,
// never to weaken the check.
//
// Deliberately NO foreign key on slip_ancestry.parent_correlation_id. Repave's first statement
// is the guarded DELETE of the old row, which runs while descendants still carry
// parent_correlation_id = old, so a plain FK would raise 23503 on every repave that has a
// descendant; ON DELETE CASCADE there would delete a child's lineage row when its parent run
// is repaved. It stays a plain column and may dangle (spec §3 banner, §7).
func (m *PostgresDynamicMigrationManager) uniquenessMigration() postgresmigrator.Migration {
	return postgresmigrator.Migration{
		Version:     5,
		Name:        "one_slip_per_commit",
		Description: "Cascade FKs from child tables on correlation_id and a unique (lower(repository), commit_sha) index (DEVOPS-231 Phase B)",
		UpSQL: `
			DO $$
			DECLARE
				expected_def constant text :=
					'FOREIGN KEY (correlation_id) REFERENCES routing_slips(correlation_id) ON DELETE CASCADE';
				fk record;
				actual_def text;
				is_valid boolean;
			BEGIN
				BEGIN
					ALTER TABLE slip_component_states
						ADD CONSTRAINT fk_component_states_slip
						FOREIGN KEY (correlation_id) REFERENCES routing_slips(correlation_id)
						ON DELETE CASCADE;
				EXCEPTION WHEN duplicate_object THEN NULL;
				END;
				BEGIN
					ALTER TABLE slip_ancestry
						ADD CONSTRAINT fk_ancestry_slip
						FOREIGN KEY (correlation_id) REFERENCES routing_slips(correlation_id)
						ON DELETE CASCADE;
				EXCEPTION WHEN duplicate_object THEN NULL;
				END;
				-- Post-condition. The swallowed error above only proves a constraint of that NAME exists;
				-- assert the definition and validity Repave depends on, so a pre-existing same-named FK
				-- with another shape (NO ACTION / RESTRICT / DEFERRABLE / NOT VALID) fails this
				-- migration loudly instead of being recorded as v5. The expected text spells
				-- routing_slips unqualified, which is how pg_get_constraintdef renders it whenever the
				-- table is visible in the migrator's search_path; where it is not, the ALTER above has
				-- already failed with 42P01, so a false mismatch here is unreachable.
				FOR fk IN
					SELECT * FROM (VALUES
						('slip_component_states', 'fk_component_states_slip'),
						('slip_ancestry',         'fk_ancestry_slip')
					) AS t(tbl, con)
				LOOP
					SELECT pg_get_constraintdef(oid), convalidated
						INTO actual_def, is_valid
						FROM pg_constraint
						WHERE conrelid = fk.tbl::regclass AND conname = fk.con AND contype = 'f';
					IF actual_def IS DISTINCT FROM expected_def OR NOT coalesce(is_valid, false) THEN
						RAISE EXCEPTION 'migration v5: % on % is % (validated=%); expected %',
							fk.con, fk.tbl, coalesce(actual_def, '<missing>'), is_valid, expected_def;
					END IF;
				END LOOP;
			END $$;
			CREATE UNIQUE INDEX IF NOT EXISTS uq_routing_slips_repo_sha
				ON routing_slips (lower(repository), commit_sha);
			DO $$
			BEGIN
				-- Post-condition. IF NOT EXISTS matches the relation NAME only: an invalid leftover from a
				-- failed hand-run online build, a non-unique or differently-keyed index, or a table of that name
				-- would all be "skipped" and v5 recorded with no uniqueness at all. Pin what the
				-- one-slip invariant needs — UNIQUE, valid, ready, on exactly this expression — and
				-- fail loudly otherwise. The flags are not redundant with the text: pg_get_indexdef
				-- renders an INVALID index identically to a valid one. The pattern is anchored at both
				-- ends with a single wildcard for the schema qualifier, because pg_get_indexdef
				-- schema-qualifies the table and a hardcoded schema would reject a healthy index in
				-- any other schema; the two regclass pins are what make that wildcard safe.
				IF NOT EXISTS (
					SELECT 1 FROM pg_index
					WHERE indexrelid = 'uq_routing_slips_repo_sha'::regclass
					  AND indrelid = 'routing_slips'::regclass
					  AND indisunique AND indisvalid AND indisready
					  AND pg_get_indexdef(indexrelid) LIKE 'CREATE UNIQUE INDEX uq_routing_slips_repo_sha '
					      || 'ON %routing_slips USING btree (lower(repository), commit_sha)'
				) THEN
					RAISE EXCEPTION 'uq_routing_slips_repo_sha is not the expected valid unique index; DROP it and re-run v5';
				END IF;
			END $$;
		`,
		DownSQL: `
			DROP INDEX IF EXISTS uq_routing_slips_repo_sha;
			ALTER TABLE slip_ancestry DROP CONSTRAINT IF EXISTS fk_ancestry_slip;
			ALTER TABLE slip_component_states DROP CONSTRAINT IF EXISTS fk_component_states_slip;
		`,
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

	// slip_ancestry indexes for the repave path (DEVOPS-231). Repave runs three statements
	// against this table — a child cleanup and a carry-forward read on correlation_id, and
	// the descendant repoint on parent_correlation_id — all INSIDE the transaction that
	// already holds the superseded row's delete lock. The table's only index is its
	// (repository, branch, correlation_id) primary key, so correlation_id was reachable
	// only as a full index scan and parent_correlation_id had no index at all. With a 30s
	// statement timeout (postgres.DefaultStatementTimeout) and a repave failure now fatal
	// to the push, a slow scan there does not degrade — it fails the message and every
	// redelivery reproduces it.
	sql.WriteString(
		"CREATE INDEX IF NOT EXISTS idx_slip_ancestry_correlation ON slip_ancestry (correlation_id);\n")
	sql.WriteString(
		"CREATE INDEX IF NOT EXISTS idx_slip_ancestry_parent ON slip_ancestry (parent_correlation_id);\n")

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
		Description: "Ensures secondary indexes exist on routing_slips and slip_ancestry",
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
