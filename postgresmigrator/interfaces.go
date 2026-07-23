package postgresmigrator

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/logger"
)

// Logger is an alias for the logger.Logger interface from goLibMyCarrier/logger.
type Logger = logger.Logger

// Querier is the minimal pgx surface the migrator needs. *pgxpool.Pool
// satisfies it directly, so callers can pass a pool (or the goLibMyCarrier/
// postgres session's Pool()).
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// PoolProvider is anything exposing a *pgxpool.Pool (e.g. the goLibMyCarrier/
// postgres session). Used by NewMigratorFromSession so callers don't reach into
// the pool themselves.
type PoolProvider interface {
	Pool() *pgxpool.Pool
}

// DatabaseMigrator is the contract for schema migration implementations.
type DatabaseMigrator interface {
	// CreateTables applies all pending migrations, then runs the ensurers.
	CreateTables(ctx context.Context) error

	// DropTables drops all expected tables (use with caution).
	DropTables(ctx context.Context) error

	// GetSchemaVersion returns the current schema version (0 if unmigrated).
	GetSchemaVersion(ctx context.Context) (int, error)

	// SetSchemaVersion records a schema version.
	SetSchemaVersion(ctx context.Context, version int) error

	// MigrateToVersion migrates up or down to the target version.
	MigrateToVersion(ctx context.Context, targetVersion int) error

	// GetAvailableMigrations returns the registered migrations.
	GetAvailableMigrations() []Migration

	// ValidateSchema checks that the expected tables exist.
	ValidateSchema(ctx context.Context) error

	// AddMigrations registers additional migrations (re-sorted by version).
	AddMigrations(migrations []Migration)
}

// Migration is a version-gated schema change with up and down SQL. DownSQL may
// be empty for intentionally irreversible migrations (they are skipped on
// down-migration).
type Migration struct {
	// Version is the unique migration version (positive, sequential).
	Version int `json:"version"`
	// Name is a short identifier (e.g. "create_routing_slips").
	Name string `json:"name"`
	// Description is a human-readable summary.
	Description string `json:"description"`
	// UpSQL applies the migration.
	UpSQL string `json:"up_sql"`
	// DownSQL reverts the migration.
	DownSQL string `json:"down_sql"`
}

// SchemaEnsurer is an idempotent schema operation that runs every time, after
// all versioned migrations. Use idempotent SQL (ADD COLUMN IF NOT EXISTS,
// CREATE INDEX IF NOT EXISTS) so it is safe to re-run.
type SchemaEnsurer struct {
	// Name identifies this ensurer for logging.
	Name string `json:"name"`
	// Description explains what it does.
	Description string `json:"description"`
	// SQL is the idempotent statement to execute.
	SQL string `json:"sql"`
}
