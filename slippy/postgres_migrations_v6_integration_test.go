//go:build integration

package slippy

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v6ColumnShape reports (exists, data_type, is_nullable) for routing_slips.claimed_from.
func v6ColumnShape(t *testing.T, pool *pgxpool.Pool) (bool, string, string) {
	t.Helper()
	var dataType, nullable string
	err := pool.QueryRow(context.Background(),
		"SELECT data_type, is_nullable FROM information_schema.columns "+
			"WHERE table_schema = current_schema() AND table_name = 'routing_slips' AND column_name = 'claimed_from'",
	).Scan(&dataType, &nullable)
	if err != nil {
		if isNoRows(err) {
			return false, "", ""
		}
		require.NoError(t, err)
	}
	return true, dataType, nullable
}

// TestClaimedFromMigration_V6_Integration exercises v6 against a real Postgres: apply,
// assert shape, revert, assert gone, re-apply (idempotent), and the shape post-condition
// refusing a same-named column of the wrong shape.
func TestClaimedFromMigration_V6_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("applies to latest with the expected shape", func(t *testing.T) {
		pool := newPGMigrationTestPool(t)
		cfg := pgTestPipelineConfig(t)
		res, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
		require.NoError(t, err)
		assert.Equal(t, 6, res.EndVersion, "v6 is the new latest")

		exists, dt, nullable := v6ColumnShape(t, pool)
		require.True(t, exists, "claimed_from must exist after v6")
		assert.Equal(t, "text", dt)
		assert.Equal(t, "YES", nullable)
	})

	t.Run("down removes the column and up re-applies idempotently", func(t *testing.T) {
		pool := newPGMigrationTestPool(t)
		cfg := pgTestPipelineConfig(t)
		_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg, TargetVersion: 5})
		require.NoError(t, err)
		exists, _, _ := v6ColumnShape(t, pool)
		require.False(t, exists, "v6 DownSQL must drop claimed_from")

		_, err = RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
		require.NoError(t, err)
		exists, _, _ = v6ColumnShape(t, pool)
		require.True(t, exists)

		// Re-running UpSQL directly on an already-migrated schema is the IF NOT EXISTS path.
		v6 := NewPostgresDynamicMigrationManager(cfg, nil).GenerateMigrations()[5]
		_, err = pool.Exec(ctx, v6.UpSQL)
		require.NoError(t, err, "v6 UpSQL must be idempotent")
	})

	t.Run("refuses a pre-existing same-named column of the wrong shape", func(t *testing.T) {
		pool := newPGMigrationTestPool(t)
		cfg := pgTestPipelineConfig(t)
		_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg, TargetVersion: 5})
		require.NoError(t, err)
		// Decoy: right name, wrong shape (NOT NULL integer). IF NOT EXISTS will match it by name.
		_, err = pool.Exec(ctx, "ALTER TABLE routing_slips ADD COLUMN claimed_from integer NOT NULL DEFAULT 0")
		require.NoError(t, err)

		v6 := NewPostgresDynamicMigrationManager(cfg, nil).GenerateMigrations()[5]
		_, err = pool.Exec(ctx, v6.UpSQL)
		require.Error(t, err, "the shape post-condition must refuse the decoy")
		assert.Contains(t, err.Error(), "claimed_from")
		assert.Contains(t, err.Error(), "nullable text")
	})
}
