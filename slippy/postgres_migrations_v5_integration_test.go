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

// TestUniquenessMigration_V5_Integration exercises DEVOPS-231 Phase B against a real
// Postgres: the migration installs what it says, refuses an uncleaned database loudly and
// atomically, and the constraints then behave the way the repave path depends on.
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
			"SELECT count(*) FROM pg_indexes WHERE indexname = 'uq_routing_slips_repo_sha' AND indexdef LIKE 'CREATE UNIQUE INDEX%'",
		),
			"the (lower(repository), commit_sha) index must exist and be UNIQUE")
		for _, c := range []string{"fk_component_states_slip", "fk_ancestry_slip"} {
			var def string
			require.NoError(t, pool.QueryRow(ctx,
				"SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = $1", c).Scan(&def), c)
			assert.Contains(
				t,
				def,
				"FOREIGN KEY (correlation_id) REFERENCES routing_slips(correlation_id) ON DELETE CASCADE",
				c,
			)
		}
		// Deliberately NO FK of any kind on slip_ancestry.parent_correlation_id (spec §3 banner, §7).
		assert.Equal(t, 0, pgCountV5(t, pool,
			"SELECT count(*) FROM pg_constraint WHERE conrelid = 'slip_ancestry'::regclass AND contype = 'f' "+
				"AND pg_get_constraintdef(oid) LIKE '%parent_correlation_id%'"))
	})

	t.Run("refuses an uncleaned database loudly and leaves the schema at v4", func(t *testing.T) {
		// This is the contract the cleanup script exists to satisfy. A failure here is the
		// intended signal that the cleanup has not run in an environment; the migration must
		// not be weakened to get past it.
		pool := newPGMigrationTestPool(t)
		cfg := pgTestPipelineConfig(t)
		_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg, TargetVersion: 4})
		require.NoError(t, err)
		_, err = pool.Exec(
			ctx,
			"INSERT INTO routing_slips (correlation_id, repository, branch, commit_sha, status) VALUES "+
				"('dup-a','owner/repo','main','sha-dup','abandoned'),('dup-b','OWNER/REPO','main','sha-dup','failed')",
		)
		require.NoError(t, err, "v4 has no uniqueness, so a case-variant duplicate pair inserts")

		_, err = RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
		require.Error(t, err, "v5 must refuse a database that still has duplicate rows")
		assert.Contains(t, err.Error(), "uq_routing_slips_repo_sha")

		v, err := GetCurrentPostgresSchemaVersion(ctx, pool)
		require.NoError(t, err)
		assert.Equal(t, 4, v, "the failed migration must not be recorded")
		// The FK ADDs run in the same transaction as the index build, so they roll back too:
		// nothing is left half-applied for the next attempt to trip over.
		assert.Equal(t, 0, pgCountV5(t, pool,
			"SELECT count(*) FROM pg_constraint WHERE conname IN ('fk_component_states_slip','fk_ancestry_slip')"),
			"a failed v5 must leave no FK behind")
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
