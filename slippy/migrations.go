package slippy

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/clickhousemigrator"
)

// MigrateOptions configures migration behavior.
type MigrateOptions struct {
	// TargetVersion specifies the version to migrate to.
	// If 0, migrates to the latest version.
	TargetVersion int

	// Logger for migration output. If nil, a no-op logger is used.
	Logger clickhousemigrator.Logger

	// DryRun if true, only shows what would be done without making changes.
	DryRun bool

	// Database to use for migrations. Defaults to "ci".
	Database string

	// PipelineConfig defines the pipeline steps for dynamic schema generation.
	// Required for dynamic migrations.
	PipelineConfig *PipelineConfig
}

// MigrateResult contains information about the migration run.
type MigrateResult struct {
	// StartVersion is the schema version before migration.
	StartVersion int

	// EndVersion is the schema version after migration.
	EndVersion int

	// MigrationsApplied is the number of migrations that were applied.
	MigrationsApplied int

	// Direction indicates whether migrations went "up" or "down".
	Direction string
}

// RunMigrations ensures the slippy schema is up to date.
// It creates the schema_version table if needed and applies any pending migrations.
// A PipelineConfig is required to generate the dynamic schema.
func RunMigrations(ctx context.Context, conn driver.Conn, opts MigrateOptions) (*MigrateResult, error) {
	// Validate required config
	if opts.PipelineConfig == nil {
		return nil, fmt.Errorf("PipelineConfig is required for migrations")
	}

	// Set defaults
	if opts.Database == "" {
		opts.Database = "ci"
	}
	if opts.Logger == nil {
		opts.Logger = &clickhousemigrator.NopLogger{}
	}

	// Create the database if it doesn't exist
	if err := ensureDatabase(ctx, conn, opts.Database); err != nil {
		return nil, fmt.Errorf("failed to ensure database exists: %w", err)
	}

	// Get dynamic migrations from pipeline config
	migrations, err := GetDynamicMigrations(ctx, conn, opts.PipelineConfig, opts.Database, opts.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to get dynamic migrations: %w", err)
	}

	// Create the migrator with slippy-specific table prefix
	migrator, err := clickhousemigrator.NewMigrator(
		conn,
		opts.Logger,
		clickhousemigrator.WithMigrations(migrations),
		clickhousemigrator.WithDatabase(opts.Database),
		clickhousemigrator.WithTablePrefix("slippy"), // Creates slippy_schema_version table
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create migrator: %w", err)
	}

	// Create the slippy_schema_version table if needed
	if err := migrator.CreateTables(ctx); err != nil {
		return nil, fmt.Errorf("failed to create slippy_schema_version table: %w", err)
	}

	// Get current version
	startVersion, err := migrator.GetSchemaVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current schema version: %w", err)
	}

	// Determine target version
	targetVersion := opts.TargetVersion
	if targetVersion == 0 {
		targetVersion = GetDynamicMigrationVersion(opts.PipelineConfig)
	}

	result := &MigrateResult{
		StartVersion: startVersion,
		EndVersion:   startVersion, // Will be updated after migration
	}

	// Determine direction
	switch {
	case targetVersion > startVersion:
		result.Direction = "up"
	case targetVersion < startVersion:
		result.Direction = "down"
	default:
		result.Direction = "none"
		opts.Logger.Info(ctx, "Schema is already at target version", map[string]interface{}{
			"target_version": targetVersion,
		})
		return result, nil
	}

	if opts.DryRun {
		opts.Logger.Info(ctx, "[DRY RUN] Would migrate", map[string]interface{}{
			"from_version": startVersion,
			"to_version":   targetVersion,
		})
		return result, nil
	}

	// Run the migration
	opts.Logger.Info(ctx, "Migrating schema", map[string]interface{}{
		"from_version": startVersion,
		"to_version":   targetVersion,
	})
	if err := migrator.MigrateToVersion(ctx, targetVersion); err != nil {
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	// Verify final version
	endVersion, err := migrator.GetSchemaVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to verify schema version: %w", err)
	}

	result.EndVersion = endVersion
	if startVersion < endVersion {
		result.MigrationsApplied = endVersion - startVersion
	} else {
		result.MigrationsApplied = startVersion - endVersion
	}

	opts.Logger.Info(ctx, "Migration complete", map[string]interface{}{
		"schema_version": endVersion,
	})
	return result, nil
}

// ValidateSchema checks that all required tables and views exist.
// A PipelineConfig is required to know which schema elements to validate.
func ValidateSchema(ctx context.Context, conn driver.Conn, config *PipelineConfig, database string) error {
	if config == nil {
		return fmt.Errorf("PipelineConfig is required for schema validation")
	}
	if database == "" {
		database = "ci"
	}

	// Get dynamic migrations from pipeline config
	migrations, err := GetDynamicMigrations(ctx, conn, config, database, nil)
	if err != nil {
		return fmt.Errorf("failed to get dynamic migrations: %w", err)
	}

	migrator, err := clickhousemigrator.NewMigrator(
		conn,
		nil, // Use NopLogger
		clickhousemigrator.WithMigrations(migrations),
		clickhousemigrator.WithDatabase(database),
		clickhousemigrator.WithTablePrefix("slippy"),
	)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}

	return migrator.ValidateSchema(ctx)
}

// GetCurrentSchemaVersion returns the current schema version.
func GetCurrentSchemaVersion(ctx context.Context, conn driver.Conn, database string) (int, error) {
	if database == "" {
		database = "ci"
	}

	// Use empty migrations - we just need to query the schema version table
	migrator, err := clickhousemigrator.NewMigrator(
		conn,
		nil, // Use NopLogger
		clickhousemigrator.WithMigrations([]clickhousemigrator.Migration{}),
		clickhousemigrator.WithDatabase(database),
		clickhousemigrator.WithTablePrefix("slippy"),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create migrator: %w", err)
	}

	return migrator.GetSchemaVersion(ctx)
}

// GetPendingMigrations returns the list of migrations that have not yet been applied.
// A PipelineConfig is required to know which migrations should exist.
func GetPendingMigrations(
	ctx context.Context,
	conn driver.Conn,
	config *PipelineConfig,
	database string,
) ([]clickhousemigrator.Migration, error) {
	if config == nil {
		return nil, fmt.Errorf("PipelineConfig is required for pending migrations")
	}
	if database == "" {
		database = "ci"
	}

	// Get dynamic migrations from pipeline config
	migrations, err := GetDynamicMigrations(ctx, conn, config, database, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get dynamic migrations: %w", err)
	}

	migrator, err := clickhousemigrator.NewMigrator(
		conn,
		nil, // Use NopLogger
		clickhousemigrator.WithMigrations(migrations),
		clickhousemigrator.WithDatabase(database),
		clickhousemigrator.WithTablePrefix("slippy"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create migrator: %w", err)
	}

	return migrator.GetPendingMigrations(ctx)
}

// ensureDatabase creates the database if it doesn't exist.
func ensureDatabase(ctx context.Context, conn driver.Conn, database string) error {
	query := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", database)
	return conn.Exec(ctx, query)
}
