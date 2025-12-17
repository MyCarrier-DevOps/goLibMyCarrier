package clickhousemigrator

import (
	"context"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/MyCarrier-DevOps/goLibMyCarrier/logger"
)

// Logger is an alias for the logger.Logger interface from goLibMyCarrier/logger.
// This provides structured logging with context and field support.
type Logger = logger.Logger

// Connection provides an interface for ClickHouse connections.
// This is compatible with the goLibMyCarrier/clickhouse package's ClickhouseSession.
// The interface is designed to work with the native clickhouse-go driver.
type Connection interface {
	// Conn returns the underlying driver.Conn for direct access.
	Conn() driver.Conn

	// Close closes the connection.
	Close() error
}

// DatabaseMigrator provides an interface for database schema migrations.
// This interface defines the contract for migration implementations.
type DatabaseMigrator interface {
	// CreateTables creates all required database tables by running pending migrations.
	CreateTables(ctx context.Context) error

	// DropTables drops all database tables (use with caution).
	DropTables(ctx context.Context) error

	// GetSchemaVersion returns the current schema version.
	GetSchemaVersion(ctx context.Context) (int, error)

	// SetSchemaVersion sets the schema version.
	SetSchemaVersion(ctx context.Context, version int) error

	// MigrateToVersion migrates the database to a specific version.
	// If targetVersion > current version, migrations are applied.
	// If targetVersion < current version, migrations are reverted.
	MigrateToVersion(ctx context.Context, targetVersion int) error

	// GetAvailableMigrations returns a list of available migrations.
	GetAvailableMigrations() []Migration

	// ValidateSchema validates the current database schema.
	ValidateSchema(ctx context.Context) error

	// AddMigrations adds additional migrations to the migrator.
	// Migrations are automatically sorted by version after adding.
	AddMigrations(migrations []Migration)
}

// Migration represents a database migration with up and down SQL.
// Migrations should be designed to be idempotent where possible,
// and the DownSQL should cleanly reverse the UpSQL changes.
type Migration struct {
	// Version is the unique migration version number (must be positive and sequential).
	Version int `json:"version"`

	// Name is a short identifier for the migration (e.g., "create_users_table").
	Name string `json:"name"`

	// Description provides a human-readable description of what the migration does.
	Description string `json:"description"`

	// UpSQL is the SQL to apply this migration.
	UpSQL string `json:"up_sql"`

	// DownSQL is the SQL to revert this migration.
	DownSQL string `json:"down_sql"`
}
