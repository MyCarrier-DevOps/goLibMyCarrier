//go:build integration

package slippy

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pgCountV5(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), q, args...).Scan(&n))
	return n
}

// The exact definition Repave depends on. pg_get_constraintdef renders a NOT VALID constraint
// with a "NOT VALID" suffix, so equality (not Contains) plus convalidated is what pins it.
const v5CascadeFKDef = "FOREIGN KEY (correlation_id) REFERENCES routing_slips(correlation_id) ON DELETE CASCADE"

// v5PoolAtV4 is a fresh database migrated to v4 — the last version without v5's objects — so a
// subtest (or newStoreAtV4) can seed the exact pre-existing state it wants v5 to meet.
//
// Note how it gets there: RunPostgresMigrations always runs CreateTables to LATEST first and only
// then honours a lower TargetVersion (postgres_migrate.go), so this applies v5 and then reverts it
// via v5's DownSQL. The resulting schema is identical to a pristine v4 — but only if DownSQL is
// complete, so that is asserted here rather than left to surface later as a confusing failure in
// whatever the caller was actually testing.
func v5PoolAtV4(t *testing.T) (*pgxpool.Pool, *PipelineConfig) {
	t.Helper()
	pool := newPGMigrationTestPool(t)
	cfg := pgTestPipelineConfig(t)
	_, err := RunPostgresMigrations(context.Background(), pool,
		PostgresMigrateOptions{PipelineConfig: cfg, TargetVersion: 4})
	require.NoError(t, err)
	var leftovers int
	require.NoError(t, pool.QueryRow(context.Background(),
		"SELECT (SELECT count(*) FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid "+
			"WHERE i.indrelid = 'routing_slips'::regclass AND ic.relname = 'uq_routing_slips_repo_sha') + "+
			"(SELECT count(*) FROM pg_constraint WHERE conname IN "+
			"('fk_component_states_slip','fk_ancestry_slip') AND conrelid IN "+
			"('slip_component_states'::regclass,'slip_ancestry'::regclass))").Scan(&leftovers))
	require.Zero(t, leftovers,
		"v5's DownSQL must remove the index and both FKs; a leftover would fail this caller for the wrong reason")
	return pool, cfg
}

// v5MustRefuse runs the migration to latest and asserts it failed, named the offending object
// in its error, and left the schema at v4 with no v5 FK behind (UpSQL and the version insert
// share one transaction, so a failure rolls both back).
func v5MustRefuse(t *testing.T, pool *pgxpool.Pool, cfg *PipelineConfig, wantInError ...string) {
	t.Helper()
	ctx := context.Background()
	_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
	require.Error(t, err, "v5 must refuse this database")
	for _, w := range wantInError {
		assert.Contains(t, err.Error(), w)
	}
	v, err := GetCurrentPostgresSchemaVersion(ctx, pool)
	require.NoError(t, err)
	// Cheap, and pins the migrator contract this migration's whole design rests on rather than
	// anything slippy-side: UpSQL and the version insert share one transaction, so a refusal
	// cannot leave 5 recorded.
	assert.Equal(t, 4, v, "a refused migration must not be recorded")
}

// TestUniquenessMigration_V5_Integration exercises DEVOPS-231 Phase B against a real
// Postgres: the migration installs what it says, refuses an uncleaned database loudly and
// atomically, refuses a pre-existing same-named object of the wrong shape (a name match is not
// proof of definition), is a no-op for a repeated run, and the constraints then behave the way
// the repave path depends on.
func TestUniquenessMigration_V5_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("applies cleanly and installs the unique index and both cascade FKs", func(t *testing.T) {
		pool := newPGMigrationTestPool(t)
		cfg := pgTestPipelineConfig(t)
		res, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
		require.NoError(t, err)
		assert.Equal(t, 5, res.EndVersion)

		assert.Equal(t, 1, pgCountV5(
			t,
			pool,
			"SELECT count(*) FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid "+
				"WHERE i.indrelid = 'routing_slips'::regclass AND ic.relname = 'uq_routing_slips_repo_sha' "+
				"AND i.indisunique AND i.indisvalid AND i.indisready "+
				"AND pg_get_indexdef(i.indexrelid) LIKE 'CREATE UNIQUE INDEX uq_routing_slips_repo_sha "+
				`ON %routing_slips USING btree (lower(repository), commit\_sha)'`,
		),
			"the index must be UNIQUE, valid, ready and on exactly (lower(repository), commit_sha)")
		for _, c := range []string{"fk_component_states_slip", "fk_ancestry_slip"} {
			var def string
			var valid bool
			require.NoError(t, pool.QueryRow(ctx,
				"SELECT pg_get_constraintdef(oid), convalidated FROM pg_constraint WHERE conname = $1", c).
				Scan(&def, &valid), c)
			assert.Equal(t, v5CascadeFKDef, def, c)
			assert.True(t, valid, "%s must be validated, not NOT VALID", c)
		}
		// Deliberately NO FK of any kind on slip_ancestry.parent_correlation_id (spec §3 banner, §7).
		assert.Equal(t, 0, pgCountV5(t, pool,
			"SELECT count(*) FROM pg_constraint WHERE conrelid = 'slip_ancestry'::regclass AND contype = 'f' "+
				"AND pg_get_constraintdef(oid) LIKE '%parent_correlation_id%'"))
	})

	t.Run("refuses a database with duplicate rows and leaves the schema at v4", func(t *testing.T) {
		// This is the contract the cleanup script exists to satisfy. A failure here is the
		// intended signal that the cleanup has not run in an environment; the migration must
		// not be weakened to get past it. The duplicates are seeded childless on purpose: with
		// orphan children present the FK add fails first, and that half of the gate has its own
		// subtest below.
		pool, cfg := v5PoolAtV4(t)
		_, err := pool.Exec(
			ctx,
			"INSERT INTO routing_slips (correlation_id, repository, branch, commit_sha, status) VALUES "+
				"('dup-a','owner/repo','main','sha-dup','abandoned'),('dup-b','OWNER/REPO','main','sha-dup','failed')",
		)
		require.NoError(t, err, "v4 has no uniqueness, so a case-variant duplicate pair inserts")

		v5MustRefuse(t, pool, cfg, "uq_routing_slips_repo_sha")
		// The FK ADDs run in the same transaction as the index build, so they roll back too:
		// nothing is left half-applied for the next attempt to trip over.
		assert.Equal(t, 0, pgCountV5(t, pool,
			"SELECT count(*) FROM pg_constraint WHERE conname IN ('fk_component_states_slip','fk_ancestry_slip')"),
			"a failed v5 must leave no FK behind")
	})

	t.Run("refuses a database with an orphan child row", func(t *testing.T) {
		// The other half of the gate: ADD CONSTRAINT validates existing rows, so a child row
		// whose slip is gone fails the FK add before the index is even attempted.
		pool, cfg := v5PoolAtV4(t)
		_, err := pool.Exec(
			ctx,
			"INSERT INTO slip_component_states (correlation_id, step, component, status) VALUES ('orphan','builds','api','pending')",
		)
		require.NoError(t, err, "v4 has no FK, so an orphan child row inserts")

		v5MustRefuse(t, pool, cfg, "fk_component_states_slip")
	})

	t.Run("refuses a pre-existing same-named FK with the wrong shape", func(t *testing.T) {
		// duplicate_object is swallowed so a repeated migrator run is a no-op, but the swallow
		// only proves a constraint of that NAME exists. Repave deletes the parent row before its
		// children, which is legal only under CASCADE — a NO ACTION FK of the same name would
		// break every repave with child rows. The post-condition must refuse it.
		pool, cfg := v5PoolAtV4(t)
		_, err := pool.Exec(ctx,
			"ALTER TABLE slip_ancestry ADD CONSTRAINT fk_ancestry_slip "+
				"FOREIGN KEY (correlation_id) REFERENCES routing_slips(correlation_id)")
		require.NoError(t, err)

		v5MustRefuse(t, pool, cfg, "fk_ancestry_slip", "expected "+v5CascadeFKDef)
		var def string
		require.NoError(t, pool.QueryRow(ctx,
			"SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'fk_ancestry_slip'").Scan(&def))
		assert.Equal(
			t,
			"FOREIGN KEY (correlation_id) REFERENCES routing_slips(correlation_id)",
			def,
			"the foreign constraint is left exactly as the operator left it, not silently repaired",
		)
	})

	t.Run("refuses a pre-existing same-named FK that is NOT VALID", func(t *testing.T) {
		// pg_get_constraintdef renders this as "... ON DELETE CASCADE NOT VALID", which a
		// Contains check on the cascade text would accept; convalidated is what catches it.
		pool, cfg := v5PoolAtV4(t)
		_, err := pool.Exec(ctx,
			"ALTER TABLE slip_component_states ADD CONSTRAINT fk_component_states_slip "+
				"FOREIGN KEY (correlation_id) REFERENCES routing_slips(correlation_id) ON DELETE CASCADE NOT VALID")
		require.NoError(t, err)

		v5MustRefuse(t, pool, cfg, "fk_component_states_slip", "validated=f")
	})

	t.Run("refuses a pre-existing same-named index with the wrong expression", func(t *testing.T) {
		// IF NOT EXISTS matches the relation NAME only. A unique, valid, ready index on
		// (repository, commit_sha) WITHOUT lower() passes every flag and lets case-variant
		// duplicates through — the exact shape the Create test below exists to reject.
		pool, cfg := v5PoolAtV4(t)
		_, err := pool.Exec(ctx,
			"CREATE UNIQUE INDEX uq_routing_slips_repo_sha ON routing_slips (repository, commit_sha)")
		require.NoError(t, err)

		v5MustRefuse(t, pool, cfg, "is not the expected valid unique index")
	})

	t.Run("refuses a same-named index keyed on a similarly named column", func(t *testing.T) {
		// LIKE reads an unescaped _ as a single-character wildcard, so before commit\_sha was
		// escaped in the pattern, an index on (lower(repository), commitzsha) satisfied it: v5
		// reported success while case-variant duplicates still inserted freely. This is the only
		// state that ever produced a silent success, so it is worth a test of its own.
		pool, cfg := v5PoolAtV4(t)
		_, err := pool.Exec(ctx, `
			ALTER TABLE routing_slips ADD COLUMN commitzsha text NOT NULL DEFAULT '';
			CREATE UNIQUE INDEX uq_routing_slips_repo_sha ON routing_slips (lower(repository), commitzsha)`)
		require.NoError(t, err)

		v5MustRefuse(t, pool, cfg, "is not the expected valid unique index")
	})

	t.Run("succeeds despite an unrelated same-named index in an earlier search_path schema", func(t *testing.T) {
		// CREATE INDEX places the index in the TABLE's schema, so the post-condition has to look it
		// up through the table. A bare 'uq_routing_slips_repo_sha'::regclass resolves through
		// search_path instead — and the default search_path is "$user", public, so a schema named
		// after the connecting role shadows public. That made v5 reject a database it had just
		// migrated correctly, with a message telling the operator to drop the wrong index.
		pool, cfg := v5PoolAtV4(t)
		_, err := pool.Exec(ctx, `
			CREATE SCHEMA slippy_write;
			CREATE TABLE slippy_write.decoy (a text);
			CREATE UNIQUE INDEX uq_routing_slips_repo_sha ON slippy_write.decoy (a)`)
		require.NoError(t, err)

		_, err = RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
		require.NoError(t, err, "an unrelated index of that name in another schema must not fail v5")
		assert.Equal(t, 1, pgCountV5(t, pool,
			"SELECT count(*) FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid "+
				"WHERE i.indrelid = 'routing_slips'::regclass AND ic.relname = 'uq_routing_slips_repo_sha' "+
				"AND i.indisunique AND i.indisvalid"),
			"the canonical index must exist on routing_slips itself")
	})

	t.Run("re-applying the UpSQL on a migrated database is a no-op", func(t *testing.T) {
		// A repeated run must not fail: the FK adds swallow duplicate_object, the index is
		// IF NOT EXISTS, and both post-conditions pass on the objects the first run created.
		// This is the sequential case; the concurrent-migrator case shares the same statements
		// and is what postgresmigrator's "every step is idempotent" contract requires.
		pool := newPGMigrationTestPool(t)
		cfg := pgTestPipelineConfig(t)
		_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
		require.NoError(t, err)

		up := NewPostgresDynamicMigrationManager(cfg, nil).GenerateMigrations()[4].UpSQL
		_, err = pool.Exec(ctx, up)
		require.NoError(t, err, "v5's UpSQL must be idempotent against its own objects")
		assert.Equal(t, 2, pgCountV5(t, pool,
			"SELECT count(*) FROM pg_constraint WHERE conname IN ('fk_component_states_slip','fk_ancestry_slip') "+
				"AND pg_get_constraintdef(oid) = $1 AND convalidated", v5CascadeFKDef))
		assert.Equal(t, 1, pgCountV5(t, pool,
			"SELECT count(*) FROM pg_indexes WHERE indexname = 'uq_routing_slips_repo_sha'"))
	})

	t.Run("deleting a slip cascades to its own child rows and no others", func(t *testing.T) {
		pool := newPGMigrationTestPool(t)
		cfg := pgTestPipelineConfig(t)
		_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `
			INSERT INTO routing_slips (correlation_id, repository, branch, commit_sha) VALUES
				('gone','owner/repo','main','sha-1'), ('kept','owner/repo','main','sha-2');
			INSERT INTO slip_component_states (correlation_id, step, component, status) VALUES
				('gone','builds','api','pending'), ('kept','builds','api','pending');
			INSERT INTO slip_ancestry (repository, branch, correlation_id, parent_correlation_id, parent_commit_sha, parent_status) VALUES
				('owner/repo','main','gone','p','psha','completed'), ('owner/repo','main','kept','p','psha','completed')`)
		require.NoError(t, err)

		_, err = pool.Exec(ctx, "DELETE FROM routing_slips WHERE correlation_id = 'gone'")
		require.NoError(t, err)
		assert.Equal(
			t,
			0,
			pgCountV5(t, pool, "SELECT count(*) FROM slip_component_states WHERE correlation_id = 'gone'"),
		)
		assert.Equal(t, 0, pgCountV5(t, pool, "SELECT count(*) FROM slip_ancestry WHERE correlation_id = 'gone'"))
		assert.Equal(
			t,
			1,
			pgCountV5(t, pool, "SELECT count(*) FROM slip_component_states WHERE correlation_id = 'kept'"),
		)
		assert.Equal(t, 1, pgCountV5(t, pool, "SELECT count(*) FROM slip_ancestry WHERE correlation_id = 'kept'"))
	})

	t.Run("Create maps the index violation to ErrDuplicateSlip", func(t *testing.T) {
		// Before v5 this error was unreachable from a real store, so the duplicate-create
		// backstop in CreateSlipForPush was dormant. This pins the mapping that arms it.
		store, _, _ := newMigratedStore(t)
		require.NoError(
			t,
			store.Create(
				ctx,
				&Slip{
					CorrelationID: "first",
					Repository:    "owner/repo",
					Branch:        "main",
					CommitSHA:     "sha-x",
					Status:        SlipStatusInProgress,
				},
			),
		)
		err := store.Create(
			ctx,
			&Slip{
				CorrelationID: "second",
				Repository:    "OWNER/REPO",
				Branch:        "main",
				CommitSHA:     "sha-x",
				Status:        SlipStatusInProgress,
			},
		)
		require.Error(t, err)
		assert.True(
			t,
			errors.Is(err, ErrDuplicateSlip),
			"a case-variant duplicate must surface as ErrDuplicateSlip, got %v",
			err,
		)
	})
}
