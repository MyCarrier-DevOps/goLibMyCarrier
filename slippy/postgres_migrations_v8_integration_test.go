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

// claimedByColumn reports whether slip_component_states has claimed_by, and whether it is nullable.
func claimedByColumn(t *testing.T, pool *pgxpool.Pool) (present, nullable bool) {
	t.Helper()
	var isNullable string
	err := pool.QueryRow(context.Background(),
		"SELECT is_nullable FROM information_schema.columns "+
			"WHERE table_name = 'slip_component_states' AND column_name = 'claimed_by'").Scan(&isNullable)
	if isNoRows(err) {
		return false, false
	}
	require.NoError(t, err)
	return true, isNullable == "YES"
}

func TestStepClaimantMigration_V8_Integration(t *testing.T) {
	ctx := context.Background()
	cfg := pgTestPipelineConfig(t)

	t.Run("applies to latest with a nullable column", func(t *testing.T) {
		pool := newPGMigrationTestPool(t)
		res, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
		require.NoError(t, err)
		assert.Equal(t, 8, res.EndVersion)
		present, nullable := claimedByColumn(t, pool)
		assert.True(t, present)
		assert.True(t, nullable)
	})

	t.Run("reverts to v7 and re-applies", func(t *testing.T) {
		pool := newPGMigrationTestPool(t)
		_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
		require.NoError(t, err)
		_, err = RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg, TargetVersion: 7})
		require.NoError(t, err)
		present, _ := claimedByColumn(t, pool)
		assert.False(t, present, "the down removes the column")
		_, err = RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
		require.NoError(t, err)
		present, _ = claimedByColumn(t, pool)
		assert.True(t, present)
	})

	t.Run("ProbeSchema refuses a database that has not applied v8", func(t *testing.T) {
		pool := newPGMigrationTestPool(t)
		_, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg, TargetVersion: 7})
		require.NoError(t, err)
		store, err := NewPostgresStore(pool, cfg, nil)
		require.NoError(t, err)
		err = store.ProbeSchema(ctx)
		require.Error(t, err, "Load reads claimed_by, so an API on this library must not serve at v7")
		assert.True(t, errors.Is(err, ErrSchemaBehind), "got %v", err)

		_, err = RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
		require.NoError(t, err)
		assert.NoError(t, store.ProbeSchema(ctx))
	})
}
