package slippy

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/postgresmigrator"
)

// slippyExpectedTables are the core tables the Postgres schema must contain.
var slippyExpectedTables = []string{"routing_slips", "slip_component_states", "slip_ancestry"}

// PostgresMigrateOptions configures a Postgres migration run.
type PostgresMigrateOptions struct {
	// TargetVersion is the version to migrate to. 0 means "latest".
	TargetVersion int

	// Logger for migration output. If nil, a no-op logger is used.
	Logger postgresmigrator.Logger

	// DryRun reports what would change without applying it.
	DryRun bool

	// PipelineConfig defines the pipeline steps for dynamic schema generation. Required.
	PipelineConfig *PipelineConfig
}

// RunPostgresMigrations ensures the slippy Postgres schema is up to date: it creates the
// slippy_schema_version table if needed, applies pending core migrations, and runs the
// config-driven ensurers. A PipelineConfig is required to generate the dynamic schema.
//
// This is the Postgres counterpart of RunMigrations (ClickHouse). It is intended to be
// driven by the dedicated migrator Job, not by slippy-api at startup.
func RunPostgresMigrations(
	ctx context.Context,
	pool *pgxpool.Pool,
	opts PostgresMigrateOptions,
) (*MigrateResult, error) {
	if opts.PipelineConfig == nil {
		return nil, fmt.Errorf("PipelineConfig is required for migrations")
	}
	if opts.Logger == nil {
		opts.Logger = &postgresmigrator.NopLogger{}
	}

	migrator, err := newSlippyPostgresMigrator(pool, opts.PipelineConfig, opts.Logger)
	if err != nil {
		return nil, err
	}

	// Measure the version before applying anything (0 on a fresh database, before the
	// version table exists) so Direction/MigrationsApplied reflect the real work.
	startVersion, err := migrator.GetSchemaVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current schema version: %w", err)
	}

	targetVersion := opts.TargetVersion
	if targetVersion == 0 {
		targetVersion = GetPostgresDynamicMigrationVersion(opts.PipelineConfig)
	}

	if opts.DryRun {
		result := &MigrateResult{
			StartVersion: startVersion,
			EndVersion:   startVersion,
			Direction:    migrateDirection(startVersion, targetVersion),
		}
		opts.Logger.Info(ctx, "[DRY RUN] Would migrate", map[string]interface{}{
			"from_version": startVersion, "to_version": targetVersion,
		})
		return result, nil
	}

	// CreateTables creates the version table, applies all pending up-migrations, and runs
	// the (idempotent) ensurers — ensurers run only here, not in MigrateToVersion.
	if err := migrator.CreateTables(ctx); err != nil {
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	// Honor an explicit lower target (down/partial migration). CreateTables always goes to
	// latest, so this only does work when targetVersion < latest.
	if err := migrator.MigrateToVersion(ctx, targetVersion); err != nil {
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	endVersion, err := migrator.GetSchemaVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to verify schema version: %w", err)
	}

	result := &MigrateResult{
		StartVersion:      startVersion,
		EndVersion:        endVersion,
		Direction:         migrateDirection(startVersion, endVersion),
		MigrationsApplied: absInt(endVersion - startVersion),
	}
	opts.Logger.Info(ctx, "Migration complete", map[string]interface{}{"schema_version": endVersion})
	return result, nil
}

func migrateDirection(from, to int) string {
	switch {
	case to > from:
		return "up"
	case to < from:
		return "down"
	default:
		return "none"
	}
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// ValidatePostgresSchema checks that the core tables exist. A PipelineConfig is required
// to build the migrator, mirroring the ClickHouse ValidateSchema.
func ValidatePostgresSchema(ctx context.Context, pool *pgxpool.Pool, config *PipelineConfig) error {
	if config == nil {
		return fmt.Errorf("PipelineConfig is required for schema validation")
	}
	migrator, err := newSlippyPostgresMigrator(pool, config, nil)
	if err != nil {
		return err
	}
	return migrator.ValidateSchema(ctx)
}

// GetCurrentPostgresSchemaVersion returns the current slippy schema version (0 when
// unmigrated).
func GetCurrentPostgresSchemaVersion(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	migrator, err := postgresmigrator.NewMigrator(
		pool, nil,
		postgresmigrator.WithTablePrefix("slippy"),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create migrator: %w", err)
	}
	return migrator.GetSchemaVersion(ctx)
}

// newSlippyPostgresMigrator builds a postgresmigrator configured with slippy's core
// migrations, config-driven ensurers, the slippy table prefix, and expected tables.
func newSlippyPostgresMigrator(
	pool *pgxpool.Pool,
	config *PipelineConfig,
	logger postgresmigrator.Logger,
) (*postgresmigrator.Migrator, error) {
	migrator, err := postgresmigrator.NewMigrator(
		pool, logger,
		postgresmigrator.WithMigrations(GetPostgresDynamicMigrations(config, logger)),
		postgresmigrator.WithEnsurers(GetPostgresDynamicEnsurers(config, logger)),
		postgresmigrator.WithTablePrefix("slippy"),
		postgresmigrator.WithExpectedTables(slippyExpectedTables),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create migrator: %w", err)
	}
	return migrator, nil
}
