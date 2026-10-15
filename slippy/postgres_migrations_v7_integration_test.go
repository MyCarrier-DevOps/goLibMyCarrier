//go:build integration

package slippy

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v7ConstraintDef returns pg_get_constraintdef for a named slip_rollbacks constraint, or ""
// when it does not exist.
func v7ConstraintDef(t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	var def string
	err := pool.QueryRow(context.Background(),
		"SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c "+
			"WHERE c.conrelid = to_regclass('slip_rollbacks') AND c.conname = $1", name).Scan(&def)
	if isNoRows(err) {
		return ""
	}
	require.NoError(t, err)
	return def
}

// TestSlipRollbacksMigration_V7_Integration exercises v7 against a real Postgres: apply and
// assert shape, revert and re-apply, re-run directly, refuse a wrong-shaped decoy, refuse an
// unknown status at the CHECK, and cascade a slip delete.
func TestSlipRollbacksMigration_V7_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("applies to latest with the expected shape", func(t *testing.T) {
		pool := newPGMigrationTestPool(t)
		cfg := pgTestPipelineConfig(t)
		res, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
		require.NoError(t, err)
		assert.Equal(t, 7, res.EndVersion, "v7 is the new latest")

		require.True(t, pgTableExists(t, pool, "slip_rollbacks"))
		assert.Equal(t,
			"FOREIGN KEY (correlation_id) REFERENCES routing_slips(correlation_id) ON DELETE CASCADE",
			v7ConstraintDef(t, pool, "fk_slip_rollbacks_slip"))
		assert.Equal(
			t,
			"UNIQUE (correlation_id, workflow)",
			v7ConstraintDef(t, pool, "uq_slip_rollbacks_slip_workflow"),
		)
		assert.Contains(t, v7ConstraintDef(t, pool, "ck_slip_rollbacks_status"), "dry_run")
	})

	// ValidatePostgresSchema and ProbeSchema read one list of child tables, so they agree on v7.
	t.Run("ValidatePostgresSchema refuses a database without slip_rollbacks", func(t *testing.T) {
		pool := newPGMigrationTestPool(t)
		cfg := pgTestPipelineConfig(t)
		_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg, TargetVersion: 6})
		require.NoError(t, err)

		err = ValidatePostgresSchema(ctx, pool, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "slip_rollbacks")
	})

	t.Run("down drops the table and up re-applies idempotently", func(t *testing.T) {
		pool := newPGMigrationTestPool(t)
		cfg := pgTestPipelineConfig(t)
		_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
		require.NoError(t, err)

		_, err = RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg, TargetVersion: 6})
		require.NoError(t, err)
		require.False(t, pgTableExists(t, pool, "slip_rollbacks"), "v7 DownSQL must drop slip_rollbacks")

		_, err = RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
		require.NoError(t, err)
		require.True(t, pgTableExists(t, pool, "slip_rollbacks"))

		// Re-running UpSQL on an already-migrated schema is the IF NOT EXISTS path.
		v7 := NewPostgresDynamicMigrationManager(cfg, nil).GenerateMigrations()[6]
		_, err = pool.Exec(ctx, v7.UpSQL)
		require.NoError(t, err, "v7 UpSQL must be idempotent")
	})

	t.Run("refuses a pre-existing same-named table of the wrong shape", func(t *testing.T) {
		pool := newPGMigrationTestPool(t)
		cfg := pgTestPipelineConfig(t)
		_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg, TargetVersion: 6})
		require.NoError(t, err)
		// Decoy: right name and columns for the index, no cascade FK, no uniqueness.
		_, err = pool.Exec(
			ctx,
			"CREATE TABLE slip_rollbacks (id bigserial PRIMARY KEY, correlation_id text NOT NULL, workflow text NOT NULL)",
		)
		require.NoError(t, err)

		v7 := NewPostgresDynamicMigrationManager(cfg, nil).GenerateMigrations()[6]
		_, err = pool.Exec(ctx, v7.UpSQL)
		require.Error(t, err, "the shape post-condition must refuse the decoy")
		assert.Contains(t, err.Error(), "fk_slip_rollbacks_slip")
	})

	// The .14 prerelease shape: the cascade FK is right, the key is the old UNIQUE (workflow).
	t.Run("refuses a same-named table keyed on the workflow alone", func(t *testing.T) {
		pool := newPGMigrationTestPool(t)
		cfg := pgTestPipelineConfig(t)
		_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg, TargetVersion: 6})
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `CREATE TABLE slip_rollbacks (
			id bigserial PRIMARY KEY,
			correlation_id text NOT NULL,
			workflow text NOT NULL,
			CONSTRAINT fk_slip_rollbacks_slip FOREIGN KEY (correlation_id)
				REFERENCES routing_slips(correlation_id) ON DELETE CASCADE,
			CONSTRAINT uq_slip_rollbacks_workflow UNIQUE (workflow))`)
		require.NoError(t, err)

		v7 := NewPostgresDynamicMigrationManager(cfg, nil).GenerateMigrations()[6]
		_, err = pool.Exec(ctx, v7.UpSQL)
		require.Error(t, err, "the post-condition must refuse the old key")
		assert.Contains(t, err.Error(), "uq_slip_rollbacks_slip_workflow")
	})

	t.Run("the CHECK refuses a status the library does not define", func(t *testing.T) {
		store, pool, _ := newMigratedStore(t)
		claimTestSlip(t, store, "v7-check", "sha-v7-check", SlipStatusFailed)

		_, err := pool.Exec(ctx,
			"INSERT INTO slip_rollbacks (correlation_id, workflow, environment, status) VALUES ($1, $2, $3, $4)",
			"v7-check", "wf-v7-check", "prod", "error")
		require.Error(t, err)
		var pgErr *pgconn.PgError
		require.True(t, errors.As(err, &pgErr))
		assert.Equal(t, "23514", pgErr.Code)
		assert.Equal(t, "ck_slip_rollbacks_status", pgErr.ConstraintName)
	})

	t.Run("deleting the slip deletes its rollback rows", func(t *testing.T) {
		store, pool, _ := newMigratedStore(t)
		claimTestSlip(t, store, "v7-cascade", "sha-v7-cascade", SlipStatusFailed)
		_, err := pool.Exec(ctx,
			"INSERT INTO slip_rollbacks (correlation_id, workflow, environment, status) VALUES ($1, $2, $3, $4)",
			"v7-cascade", "wf-v7-cascade", "prod", "success")
		require.NoError(t, err)

		_, err = pool.Exec(ctx, "DELETE FROM routing_slips WHERE correlation_id = $1", "v7-cascade")
		require.NoError(t, err)

		var n int
		require.NoError(t, pool.QueryRow(ctx,
			"SELECT count(*) FROM slip_rollbacks WHERE correlation_id = $1", "v7-cascade").Scan(&n))
		assert.Zero(t, n, "ON DELETE CASCADE removes the run's rollback rows with the slip")
	})
}
