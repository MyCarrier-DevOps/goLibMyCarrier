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
		m.claimedFromMigration(),
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
// added in the same transaction as the index, so nothing is left half-applied. A failure names
// what broke — uq_routing_slips_repo_sha for a duplicate commit, fk_component_states_slip or
// fk_ancestry_slip for an orphan child row. Usually the cleanup has not run; it can also have
// been re-broken since, as a lost dedup-lock race is undetectable until this index exists (see
// CreateSlipForPush in push.go). Either way, do NOT weaken this migration to get past it.
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
// IF NOT EXISTS so a repeated migrator run is a no-op (postgresmigrator relies on that; it
// takes no advisory lock and assumes one migrator at a time). Two concurrent runs from v4 also
// converge, but incidentally: ADD FOREIGN KEY below takes SHARE ROW EXCLUSIVE held to commit,
// so the loser blocks there and finds the index already built. Keep the index AFTER the FK
// ADDs. A name match is still not proof of definition: a pre-existing same-named object with
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
				-- Post-condition. IF NOT EXISTS matches the relation NAME only: an invalid leftover
				-- from a hand-run CREATE INDEX CONCURRENTLY, a non-unique or differently-keyed index,
				-- or a table of that name would all be "skipped" and v5 recorded with no uniqueness
				-- at all. Pin what the one-slip invariant needs — UNIQUE, valid, ready, on exactly
				-- this expression — and fail loudly otherwise.
				--
				-- Three details are load-bearing. The index is found via the TABLE (indrelid plus
				-- relname), not by casting its bare name to regclass: CREATE INDEX puts the index in
				-- the table's schema, while a bare name resolves through search_path and could find
				-- an unrelated index of that name in an earlier schema — which would fail a database
				-- this migration had just correctly migrated. The flags are not redundant with the
				-- text, because pg_get_indexdef renders an invalid index identically to a valid one.
				-- And the pattern is anchored at both ends, with one wildcard for the schema
				-- qualifier pg_get_indexdef adds (a hardcoded schema would reject a healthy index in
				-- any other schema) and commit\_sha escaped, since LIKE would otherwise read the
				-- underscore as a single-character wildcard and accept an index on a similarly named
				-- column.
				IF NOT EXISTS (
					SELECT 1
					FROM pg_index i
					JOIN pg_class ic ON ic.oid = i.indexrelid
					WHERE i.indrelid = 'routing_slips'::regclass
					  AND ic.relname = 'uq_routing_slips_repo_sha'
					  AND i.indisunique AND i.indisvalid AND i.indisready
					  AND pg_get_indexdef(i.indexrelid) LIKE 'CREATE UNIQUE INDEX uq_routing_slips_repo_sha '
					      || 'ON %routing_slips USING btree (lower(repository), commit\_sha)'
				) THEN
					RAISE EXCEPTION 'uq_routing_slips_repo_sha on routing_slips is not the expected valid unique index; DROP it and re-run v5';
				END IF;
			END $$;
		`,
		DownSQL: `
			DO $$
			DECLARE
				idx regclass;
			BEGIN
				-- Find the index through the TABLE, mirroring the UpSQL post-condition. A bare
				-- name resolves through search_path, so it could drop an unrelated index of this
				-- name from an earlier schema and leave the real one live under a reverted
				-- version — the one state a pre-repave writer must never meet, since it
				-- 23505-fails every same-commit retrigger. regclass renders schema-qualified
				-- when the relation is not visible, so this drops the object it found.
				--
				-- Deliberate behaviour change from a bare IF EXISTS drop: to_regclass returns
				-- NULL instead of raising 42P01, so a missing routing_slips makes this a no-op
				-- rather than an error. That is the right outcome on a down path.
				SELECT i.indexrelid INTO idx
					FROM pg_index i
					JOIN pg_class ic ON ic.oid = i.indexrelid
					WHERE i.indrelid = to_regclass('routing_slips')
					  AND ic.relname = 'uq_routing_slips_repo_sha';
				IF idx IS NOT NULL THEN
					EXECUTE format('DROP INDEX %s', idx);
				END IF;
			END $$;
			-- IF EXISTS on the tables too, for the reason v6's down carries it: a bare ALTER
			-- TABLE raises 42P01 on a missing relation, which would make this down fail on a
			-- database where the tables were never created rather than do nothing.
			ALTER TABLE IF EXISTS slip_ancestry DROP CONSTRAINT IF EXISTS fk_ancestry_slip;
			ALTER TABLE IF EXISTS slip_component_states DROP CONSTRAINT IF EXISTS fk_component_states_slip;
		`,
	}
}

// claimedFromMigration (v6) adds the nullable column ClaimSlip records a claim in: the
// status the row had at claim time, set for as long as a run holds the slip (DEVOPS-367).
//
// Shape decisions, each deliberate:
//   - text, not the slip_status DOMAIN: the value is only ever written by ClaimSlip from a
//     status the row already held, so it cannot be out of range, and a DOMAIN would make the
//     v6 → v5 DownSQL depend on nothing else referencing the domain. Plain text keeps the
//     column self-contained.
//   - NULL means "not currently claimed". No DEFAULT: an empty string would be a second
//     encoding of the same fact and every reader would have to treat both as unclaimed.
//   - No index: the column is read by correlation_id (ReleaseClaim) on a row already locked
//     FOR UPDATE, never scanned.
//
// Idempotent by NAME (IF NOT EXISTS), asserted by SHAPE below: the swallowed re-run proves
// only that a column called claimed_from exists, so the post-condition checks type and
// nullability and RAISEs, so a pre-existing same-named column of another shape fails v6
// loudly instead of being recorded as applied — the same posture as v5's constraint checks.
func (m *PostgresDynamicMigrationManager) claimedFromMigration() postgresmigrator.Migration {
	return postgresmigrator.Migration{
		Version:     6,
		Name:        "claimed_from",
		Description: "routing_slips.claimed_from: status at claim time recorded by ClaimSlip; set while a run holds the slip, cleared by ReleaseClaim or by UpdateSlipStatus on a terminal status (DEVOPS-367)",
		UpSQL: `
			-- The lock_timeout is the FIRST statement of the up, for the same reason it is the
			-- first statement of the down: postgresmigrator runs the migration transaction with
			-- SET LOCAL lock_timeout = 0, and ADD COLUMN takes ACCESS EXCLUSIVE. An unbounded
			-- request for it queues AHEAD of every subsequent reader, so on a busy database one
			-- open transaction holding a read lock stalls all slip traffic behind this migration
			-- until it is granted or cancelled by hand. This is the likelier of the two paths to
			-- meet that, not the rarer: the up runs on every consumer startup while the down runs
			-- only on a deliberate rollback. Five seconds fails the migration instead; re-run it.
			-- SET LOCAL is scoped to the migration transaction, so it restores itself on commit
			-- or rollback.
			SET LOCAL lock_timeout = '5s';

			ALTER TABLE routing_slips ADD COLUMN IF NOT EXISTS claimed_from text NULL;

			-- Post-condition: IF NOT EXISTS matched a NAME; assert the SHAPE ReleaseClaim relies on.
			-- Resolved through the TABLE (to_regclass), as v5's post-conditions are: a query on
			-- information_schema filtered by current_schema() answers for the first schema on
			-- search_path, which is not necessarily the routing_slips the ALTER above touched.
			DO $$
			BEGIN
				IF NOT EXISTS (
					SELECT 1
					FROM pg_attribute a
					JOIN pg_type ty ON ty.oid = a.atttypid
					WHERE a.attrelid = to_regclass('routing_slips')
					  AND a.attname  = 'claimed_from'
					  AND NOT a.attisdropped
					  AND ty.typname = 'text'
					  AND NOT a.attnotnull
				) THEN
					RAISE EXCEPTION 'migration v6: routing_slips.claimed_from exists but is not a nullable text column; fix or drop it and re-run v6';
				END IF;
			END $$;
		`,
		DownSQL: `
			-- Refuse while any claim is held. Dropping claimed_from under a held claim silently
			-- ends that claim: the run's in-flight work is exposed to a same-commit repave, and
			-- there is no way back — rolling forward again brings the column back NULL, so the
			-- claim is gone for good. It also breaks every Load on a library that selects the
			-- column. Let the runs end or release the claims, then re-run the down.
			-- The message names up to 20 held correlation ids, so the operator can act on the
			-- refusal without a second query.
			-- to_regclass makes a missing table or column a no-op rather than an error.
			--
			-- The LOCK is what closes the drain race: the count below takes ACCESS SHARE, which
			-- does NOT conflict with ClaimSlip's ROW EXCLUSIVE, so a claim taken between the
			-- count and the DROP COLUMN would be erased without the RAISE ever firing. Taking
			-- ACCESS EXCLUSIVE first — the same lock the DROP will take — holds claimants out
			-- for the rest of the transaction, so the count is the state the drop acts on.
			--
			-- The lock_timeout is the FIRST statement of the down, outside the DO block, so it
			-- bounds EVERY lock request the transaction makes — the guard's LOCK TABLE and the
			-- trailing DROP COLUMN's own ACCESS EXCLUSIVE alike. It used to sit inside the
			-- IF EXISTS branch, which left the DROP unbounded on exactly the path where the
			-- guard does not run (the column already absent, so no LOCK was taken either). It
			-- is required rather than defensive: postgresmigrator runs the migration
			-- transaction with SET LOCAL lock_timeout = 0, and an unbounded ACCESS EXCLUSIVE
			-- request queues AHEAD of every subsequent reader, so on a busy database it would
			-- stall all slip traffic until it were granted or the rollback were cancelled by
			-- hand. Five seconds fails the down instead; re-run it. SET LOCAL is scoped to the
			-- migration transaction, so it restores itself on commit or rollback.
			SET LOCAL lock_timeout = '5s';

			DO $$
			DECLARE
				held bigint;
				held_ids text;
			BEGIN
				IF EXISTS (
					SELECT 1 FROM pg_attribute
					WHERE attrelid = to_regclass('routing_slips')
					  AND attname = 'claimed_from'
					  AND NOT attisdropped
				) THEN
					LOCK TABLE routing_slips IN ACCESS EXCLUSIVE MODE;
					SELECT count(*) INTO held FROM routing_slips
					WHERE claimed_from IS NOT NULL AND claimed_from <> '';
					IF held > 0 THEN
						SELECT string_agg(correlation_id, ', ' ORDER BY correlation_id) INTO held_ids
						FROM (SELECT correlation_id FROM routing_slips
						      WHERE claimed_from IS NOT NULL AND claimed_from <> ''
						      ORDER BY correlation_id LIMIT 20) h;
						RAISE EXCEPTION 'migration v6 down: % slip(s) hold a claim (claimed_from set): % — resolve the step holding each claim (POST /v1/slips/{id}/steps/{step}/complete) then POST /v1/slips/{id}/release, or let the runs end; a NON-terminal slip can also be ended with POST /v1/slips/{id}/abandon, which an already-terminal one ignores', held, held_ids;
					END IF;
				END IF;
			END $$;
			-- IF EXISTS on the TABLE as well as the column: without it a missing routing_slips
			-- raises 42P01 and the down is not the no-op the guard above already is (its
			-- to_regclass returns NULL and skips), nor the no-op this migration's own tests
			-- assert it to be.
			ALTER TABLE IF EXISTS routing_slips DROP COLUMN IF EXISTS claimed_from;
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
