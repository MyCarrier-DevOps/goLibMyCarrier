package slippy

import (
	"context"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/clickhousemigrator"
)

// GetDynamicMigrations returns versioned migrations for core schema (table, MV).
// These run once per version and establish the base table structure.
//
// Parameters:
//   - ctx: Context for database operations
//   - conn: ClickHouse driver connection
//   - config: Pipeline configuration defining steps
//   - database: Database name (defaults to "ci" if empty)
//   - logger: Optional logger for migration output
func GetDynamicMigrations(
	ctx context.Context,
	conn driver.Conn,
	config *PipelineConfig,
	database string,
	logger clickhousemigrator.Logger,
) ([]clickhousemigrator.Migration, error) {
	manager := NewDynamicMigrationManager(conn, config, database, logger)
	return manager.GetMigrationsForClickhouseMigrator(ctx)
}

// GetDynamicEnsurers returns schema ensurers for dynamic columns (step columns, indexes).
// These run every time and use idempotent SQL (ADD COLUMN IF NOT EXISTS).
// Ensurers handle configuration-driven schema that can change between deployments.
//
// Parameters:
//   - conn: ClickHouse driver connection
//   - config: Pipeline configuration defining steps
//   - database: Database name (defaults to "ci" if empty)
//   - logger: Optional logger for output
func GetDynamicEnsurers(
	conn driver.Conn,
	config *PipelineConfig,
	database string,
	logger clickhousemigrator.Logger,
) []clickhousemigrator.SchemaEnsurer {
	manager := NewDynamicMigrationManager(conn, config, database, logger)
	return manager.GenerateEnsurers()
}

// GetDynamicMigrationVersion returns the latest migration version for core schema.
// Since step columns are now ensurers (not versioned), this only counts core migrations.
func GetDynamicMigrationVersion(config *PipelineConfig) int {
	if config == nil || len(config.Steps) == 0 {
		return 0
	}
	// Version 1: base table
	// Version 2: history view
	// Step columns and indexes are now ensurers, not versioned migrations
	return 2
}
