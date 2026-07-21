package postgresmigrator

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// --- unit-level fakes (no database) ---

type fakeRow struct{}

func (fakeRow) Scan(...any) error { return errors.New("fake: no rows") }

type fakeQuerier struct{}

func (fakeQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (fakeQuerier) QueryRow(context.Context, string, ...any) pgx.Row { return fakeRow{} }
func (fakeQuerier) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("fake: begin unsupported")
}

func TestNewMigrator_NilConn(t *testing.T) {
	_, err := NewMigrator(nil, nil)
	require.ErrorIs(t, err, ErrNilConnection)
}

func TestMigrator_MigrationsSortedAndLatest(t *testing.T) {
	m, err := NewMigrator(fakeQuerier{}, nil, WithMigrations([]Migration{
		{Version: 3, Name: "c", UpSQL: "x"},
		{Version: 1, Name: "a", UpSQL: "x"},
		{Version: 2, Name: "b", UpSQL: "x"},
	}))
	require.NoError(t, err)

	got := m.GetAvailableMigrations()
	require.Len(t, got, 3)
	assert.Equal(t, []int{1, 2, 3}, []int{got[0].Version, got[1].Version, got[2].Version})
	assert.Equal(t, 3, m.GetLatestVersion())

	m.AddMigrations([]Migration{{Version: 0, Name: "zero", UpSQL: "x"}})
	assert.Equal(t, 0, m.GetAvailableMigrations()[0].Version, "AddMigrations must re-sort")
}

func TestApplyMigration_EmptyUpSQL(t *testing.T) {
	m, err := NewMigrator(fakeQuerier{}, nil)
	require.NoError(t, err)

	// Empty UpSQL is rejected before any DB call (fake Begin would error otherwise).
	err = m.applyMigrationWithTimeout(context.Background(), Migration{Version: 1, Name: "empty"})
	require.Error(t, err)
	var me *MigrationError
	require.ErrorAs(t, err, &me)
	assert.Equal(t, "up", me.Operation)
}

func TestMigrationError(t *testing.T) {
	base := errors.New("boom")
	me := &MigrationError{Version: 3, Name: "add_col", Operation: "up", Err: base}
	assert.Contains(t, me.Error(), "version 3")
	assert.Contains(t, me.Error(), "add_col")
	assert.ErrorIs(t, me, base)
}

func TestSchemaValidationError(t *testing.T) {
	base := errors.New("missing")
	withTable := &SchemaValidationError{Table: "things", Message: "not found", Err: base}
	assert.Contains(t, withTable.Error(), "things")
	assert.ErrorIs(t, withTable, base)

	noTable := &SchemaValidationError{Message: "generic"}
	assert.Contains(t, noTable.Error(), "generic")
	assert.NotContains(t, noTable.Error(), "for table")
}

func TestMigrator_UnitAccessors(t *testing.T) {
	m, err := NewMigrator(fakeQuerier{}, nil,
		WithSchema("app"),
		WithTablePrefix("slippy"),
		WithMigrationTimeout(90*time.Second),
		WithEnsurers([]SchemaEnsurer{{Name: "e1"}}),
	)
	require.NoError(t, err)

	// Option setters landed on the struct.
	assert.Equal(t, "app", m.schema)
	assert.Equal(t, 90*time.Second, m.migrationTimeout)

	// Qualified names honor schema + prefix.
	assert.Equal(t, "app.things", m.qualifiedTableName("things"))
	assert.Equal(t, "app.slippy_schema_version", m.qualifiedTableName(m.schemaVersionTableName()))

	// Ensurer accessors.
	assert.Len(t, m.GetEnsurers(), 1)
	m.AddEnsurers([]SchemaEnsurer{{Name: "e2"}})
	assert.Len(t, m.GetEnsurers(), 2)

	// Expected-tables setter.
	m.SetExpectedTables([]string{"a", "b"})

	// No migrations registered -> latest version 0.
	assert.Equal(t, 0, m.GetLatestVersion())

	// Unqualified name when no schema is set.
	plain, err := NewMigrator(fakeQuerier{}, nil)
	require.NoError(t, err)
	assert.Equal(t, "things", plain.qualifiedTableName("things"))
	assert.Equal(t, "schema_version", plain.schemaVersionTableName())
}

func TestNewMigratorFromSession_NilSession(t *testing.T) {
	_, err := NewMigratorFromSession(nil, nil)
	require.ErrorIs(t, err, ErrNilConnection)
}

// --- integration (testcontainer) ---

// poolProvider adapts a *pgxpool.Pool to the PoolProvider interface so the
// integration test can exercise NewMigratorFromSession.
type poolProvider struct{ pool *pgxpool.Pool }

func (p poolProvider) Pool() *pgxpool.Pool { return p.pool }

func newTestPool(t *testing.T) *pgxpool.Pool {
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

	// Rancher Desktop's port-forward can lag the "ready" log, so retry the ping.
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

func colExists(t *testing.T, pool *pgxpool.Pool, table, col string) bool {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		"SELECT count(*) FROM information_schema.columns WHERE table_name=$1 AND column_name=$2",
		table, col).Scan(&n))
	return n > 0
}

func TestMigrator_Integration(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	migrations := []Migration{
		{
			Version: 1, Name: "create_things",
			UpSQL:   "CREATE TABLE IF NOT EXISTS things (id text PRIMARY KEY)",
			DownSQL: "DROP TABLE IF EXISTS things",
		},
		{
			Version: 2, Name: "add_name",
			UpSQL:   "ALTER TABLE things ADD COLUMN IF NOT EXISTS name text",
			DownSQL: "ALTER TABLE things DROP COLUMN IF EXISTS name",
		},
	}
	ensurers := []SchemaEnsurer{
		{
			Name: "ensure_created_at",
			SQL:  "ALTER TABLE things ADD COLUMN IF NOT EXISTS created_at timestamptz DEFAULT now()",
		},
	}

	m, err := NewMigrator(pool, NewStdLogger(false),
		WithMigrations(migrations),
		WithEnsurers(ensurers),
		WithTablePrefix("test"),
		WithExpectedTables([]string{"things"}),
	)
	require.NoError(t, err)

	// Fresh database: no version table yet -> version 0.
	v0, err := m.GetSchemaVersion(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, v0)

	require.NoError(t, m.CreateTables(ctx))

	v, err := m.GetSchemaVersion(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, v)
	require.NoError(t, m.ValidateSchema(ctx))
	assert.True(t, colExists(t, pool, "things", "name"), "v2 column present")
	assert.True(t, colExists(t, pool, "things", "created_at"), "ensurer column present")

	// Idempotent re-run: no pending migrations, ensurer re-runs cleanly.
	require.NoError(t, m.CreateTables(ctx))
	v, _ = m.GetSchemaVersion(ctx)
	assert.Equal(t, 2, v)

	// Down to 1: v2 reverted.
	require.NoError(t, m.MigrateToVersion(ctx, 1))
	v, _ = m.GetSchemaVersion(ctx)
	assert.Equal(t, 1, v)
	assert.False(t, colExists(t, pool, "things", "name"), "v2 column dropped on down-migration")

	// Back up to 2.
	require.NoError(t, m.MigrateToVersion(ctx, 2))
	v, _ = m.GetSchemaVersion(ctx)
	assert.Equal(t, 2, v)
	assert.True(t, colExists(t, pool, "things", "name"), "v2 column restored")

	// Fully migrated -> nothing pending.
	pending, err := m.GetPendingMigrations(ctx)
	require.NoError(t, err)
	assert.Empty(t, pending)

	// SetSchemaVersion round-trips.
	require.NoError(t, m.SetSchemaVersion(ctx, 5))
	v, _ = m.GetSchemaVersion(ctx)
	assert.Equal(t, 5, v)

	// NewMigratorFromSession over a PoolProvider adapter applies cleanly.
	fromSess, err := NewMigratorFromSession(poolProvider{pool}, nil,
		WithMigrations(migrations),
		WithTablePrefix("sess"),
		WithExpectedTables([]string{"things"}),
	)
	require.NoError(t, err)
	require.NoError(t, fromSess.CreateTables(ctx))

	// DropTables removes the expected table.
	require.NoError(t, m.DropTables(ctx))
	assert.False(t, colExists(t, pool, "things", "id"), "things dropped by DropTables")
}
