package slippy

import (
	"context"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/clickhousemigrator"
)

// GetDynamicMigrations returns migrations generated from the pipeline configuration.
// This is the primary way to get migrations for the slippy schema.
//
// The migrations are:
// 1. Generated from the provided PipelineConfig
// 2. Stored in the slippy_dynamic_migrations table for tracking
// 3. Returned for use with clickhousemigrator
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

// GetDynamicMigrationVersion returns the latest migration version for a config.
// The version is calculated as: 2 (base + history) + len(steps) + 1 (indexes)
func GetDynamicMigrationVersion(config *PipelineConfig) int {
	if config == nil || len(config.Steps) == 0 {
		return 0
	}
	// Version 1: base table
	// Version 2: history view
	// Version 3 to 3+N-1: step columns (N steps)
	// Version 3+N: indexes
	return 3 + len(config.Steps)
}
