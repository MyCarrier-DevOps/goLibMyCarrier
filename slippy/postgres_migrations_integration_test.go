//go:build integration

package slippy

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// newPGMigrationTestPool starts a throwaway Postgres container and returns a connected
// pool. Mirrors the postgresmigrator test helper, including the Rancher-Desktop
// port-forward ping retry.
func newPGMigrationTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: requires Docker")
	}
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("ci_test"),
		tcpostgres.WithUsername("slippy_write"),
		tcpostgres.WithPassword("secret"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	host, err := ctr.Host(ctx)
	require.NoError(t, err)
	port, err := ctr.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err)

	dsn := fmt.Sprintf("postgres://slippy_write:secret@%s:%s/ci_test?sslmode=disable", host, port.Port())
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var pingErr error
	for range 10 {
		if pingErr = pool.Ping(ctx); pingErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NoError(t, pingErr)
	return pool
}

func pgTableExists(t *testing.T, pool *pgxpool.Pool, table string) bool {
	t.Helper()
	var exists bool
	require.NoError(t, pool.QueryRow(context.Background(),
		"SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists))
	return exists
}

func pgColExists(t *testing.T, pool *pgxpool.Pool, table, col string) bool {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		"SELECT count(*) FROM information_schema.columns WHERE table_name=$1 AND column_name=$2",
		table, col).Scan(&n))
	return n > 0
}

func TestRunPostgresMigrations_Integration(t *testing.T) {
	pool := newPGMigrationTestPool(t)
	ctx := context.Background()
	cfg := pgTestPipelineConfig(t)

	// Unmigrated -> version 0.
	v0, err := GetCurrentPostgresSchemaVersion(ctx, pool)
	require.NoError(t, err)
	assert.Equal(t, 0, v0)

	res, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{
		PipelineConfig: cfg,
		Logger:         NewStdLogger(false),
	})
	require.NoError(t, err)
	assert.Equal(t, "up", res.Direction)
	assert.Equal(t, GetPostgresDynamicMigrationVersion(cfg), res.EndVersion)

	// Core tables exist.
	for _, table := range slippyExpectedTables {
		assert.True(t, pgTableExists(t, pool, table), "table %s should exist", table)
	}

	// Every step's status column exists (added by ensurers).
	for _, step := range cfg.Steps {
		assert.True(t, pgColExists(t, pool, "routing_slips", step.Name+"_status"),
			"status column for step %s should exist", step.Name)
	}
	// Aggregate step's jsonb column exists.
	for _, step := range cfg.GetAggregateSteps() {
		assert.True(t, pgColExists(t, pool, "routing_slips", step.Name),
			"aggregate column for step %s should exist", step.Name)
	}

	// Idempotent re-run: no-op, ensurers re-run cleanly.
	res2, err := RunPostgresMigrations(ctx, pool, PostgresMigrateOptions{PipelineConfig: cfg})
	require.NoError(t, err)
	assert.Equal(t, "none", res2.Direction)

	require.NoError(t, ValidatePostgresSchema(ctx, pool, cfg))

	// The slip_status DOMAIN enforces the CHECK constraint.
	_, err = pool.Exec(ctx,
		`INSERT INTO routing_slips (correlation_id, repository, branch, commit_sha, status)
		 VALUES ('c-bad','r','b','sha','not_a_status')`)
	require.Error(t, err, "invalid slip_status must be rejected by the domain CHECK")

	// A valid row inserts fine and defaults land.
	_, err = pool.Exec(ctx,
		`INSERT INTO routing_slips (correlation_id, repository, branch, commit_sha)
		 VALUES ('c-ok','r','b','sha')`)
	require.NoError(t, err)
	var status, buildsStatus string
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT status, builds_status FROM routing_slips WHERE correlation_id='c-ok'").
		Scan(&status, &buildsStatus))
	assert.Equal(t, "pending", status)
	assert.Equal(t, "pending", buildsStatus)
}
