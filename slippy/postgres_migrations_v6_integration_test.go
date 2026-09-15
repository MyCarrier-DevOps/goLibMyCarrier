//go:build integration

package slippy

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v6ColumnShape reports (exists, type name, nullable) for routing_slips.claimed_from,
// resolved through the TABLE (to_regclass) exactly as the migration's own post-condition
// does, so the two cannot disagree about which routing_slips they are looking at.
func v6ColumnShape(t *testing.T, pool *pgxpool.Pool) (bool, string, bool) {
	t.Helper()
	var typeName string
	var notNull bool
	err := pool.QueryRow(context.Background(),
		"SELECT ty.typname, a.attnotnull FROM pg_attribute a JOIN pg_type ty ON ty.oid = a.atttypid "+
			"WHERE a.attrelid = to_regclass('routing_slips') AND a.attname = 'claimed_from' AND NOT a.attisdropped",
	).Scan(&typeName, &notNull)
	if err != nil {
		if isNoRows(err) {
			return false, "", false
		}
		require.NoError(t, err)
	}
	return true, typeName, !notNull
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
		assert.True(t, nullable)
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

// v6's DownSQL refuses to run while any slip holds a claim. Dropping claimed_from under a
// held claim would leave that slip in_progress with no claim recorded — unclaimable,
// unreleasable and unrepaveable — with nothing left to recover it from once the column is
// gone (PR #87 review). Rolling forward again does not help: the row would come back with a
// NULL claimed_from, which is the same wedge.
func TestClaimedFromMigration_V6_DownRefusesHeldClaims_Integration(t *testing.T) {
	ctx := context.Background()
	store, pool, cfg := newMigratedStore(t)
	claimTestSlip(t, store, "v6-held", "sha-held", SlipStatusFailed)
	_, err := store.ClaimSlip(ctx, "v6-held", nil, "slippy-cli", "")
	require.NoError(t, err)

	_, err = RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg, TargetVersion: 5})
	require.Error(t, err, "down must refuse while a claim is held")
	assert.Contains(t, err.Error(), "hold a claim")
	exists, _, _ := v6ColumnShape(t, pool)
	require.True(t, exists, "the refused down must leave the column in place")
	got, err := store.Load(ctx, "v6-held")
	require.NoError(t, err)
	assert.Equal(t, SlipStatusFailed, got.ClaimedFrom, "and the claim intact")

	_, err = store.ReleaseClaim(ctx, "v6-held", "slippy-cli/postjob", "")
	require.NoError(t, err)
	_, err = RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg, TargetVersion: 5})
	require.NoError(t, err, "with no claim held, down proceeds")
	exists, _, _ = v6ColumnShape(t, pool)
	require.False(t, exists)
}
