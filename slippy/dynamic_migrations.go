package slippy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/clickhousemigrator"
)

// DynamicMigrationManager generates and manages migrations based on pipeline configuration.
// It stores generated migrations in a database table, allowing the schema to evolve
// as the pipeline configuration changes.
type DynamicMigrationManager struct {
	conn     driver.Conn
	config   *PipelineConfig
	database string
	logger   clickhousemigrator.Logger
}

// DynamicMigration represents a migration stored in the database.
type DynamicMigration struct {
	Version     uint32    `ch:"version"` // UInt32 in ClickHouse
	Name        string    `ch:"name"`
	Description string    `ch:"description"`
	UpSQL       string    `ch:"up_sql"`
	DownSQL     string    `ch:"down_sql"`
	AppliedAt   time.Time `ch:"applied_at"`
	ConfigHash  string    `ch:"config_hash"`
}

// NewDynamicMigrationManager creates a new migration manager.
func NewDynamicMigrationManager(
	conn driver.Conn,
	config *PipelineConfig,
	database string,
	logger clickhousemigrator.Logger,
) *DynamicMigrationManager {
	if database == "" {
		database = "ci"
	}
	if logger == nil {
		logger = &clickhousemigrator.NopLogger{}
	}
	return &DynamicMigrationManager{
		conn:     conn,
		config:   config,
		database: database,
		logger:   logger,
	}
}

// EnsureMigrationTable creates the dynamic migrations table if it doesn't exist.
func (m *DynamicMigrationManager) EnsureMigrationTable(ctx context.Context) error {
	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.slippy_dynamic_migrations (
			version UInt32,
			name String,
			description String,
			up_sql String,
			down_sql String,
			applied_at DateTime64(3) DEFAULT now64(3),
			config_hash String
		)
		ENGINE = MergeTree()
		ORDER BY (version)
	`, m.database)

	return m.conn.Exec(ctx, query)
}

// GetStoredMigrations retrieves all migrations from the database.
func (m *DynamicMigrationManager) GetStoredMigrations(ctx context.Context) (migrations []DynamicMigration, err error) {
	query := fmt.Sprintf(`
		SELECT version, name, description, up_sql, down_sql, applied_at, config_hash
		FROM %s.slippy_dynamic_migrations
		ORDER BY version ASC
	`, m.database)

	rows, err := m.conn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query migrations: %w", err)
	}
	defer func() {
		closeErr := rows.Close()
		if err == nil && closeErr != nil {
			err = fmt.Errorf("failed to close rows: %w", closeErr)
		}
	}()

	for rows.Next() {
		var mig DynamicMigration
		if err := rows.Scan(&mig.Version, &mig.Name, &mig.Description, &mig.UpSQL, &mig.DownSQL, &mig.AppliedAt, &mig.ConfigHash); err != nil {
			return nil, fmt.Errorf("failed to scan migration: %w", err)
		}
		migrations = append(migrations, mig)
	}

	return migrations, nil
}

// StoreMigration persists a migration to the database.
func (m *DynamicMigrationManager) StoreMigration(ctx context.Context, mig DynamicMigration) error {
	query := fmt.Sprintf(`
		INSERT INTO %s.slippy_dynamic_migrations (version, name, description, up_sql, down_sql, config_hash)
		VALUES (?, ?, ?, ?, ?, ?)
	`, m.database)

	return m.conn.Exec(ctx, query, mig.Version, mig.Name, mig.Description, mig.UpSQL, mig.DownSQL, mig.ConfigHash)
}

// GenerateAndStoreMigrations generates migrations based on config and stores any new ones.
// Returns the complete list of migrations (stored + newly generated) for use with clickhousemigrator.
func (m *DynamicMigrationManager) GenerateAndStoreMigrations(
	ctx context.Context,
) ([]clickhousemigrator.Migration, error) {
	if err := m.EnsureMigrationTable(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure migration table: %w", err)
	}

	// Get existing migrations from database
	storedMigrations, err := m.GetStoredMigrations(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get stored migrations: %w", err)
	}

	// Generate migrations from current config
	generatedMigrations := m.generateMigrationsFromConfig()

	// Determine which migrations need to be stored
	storedVersions := make(map[uint32]bool)
	for _, mig := range storedMigrations {
		storedVersions[mig.Version] = true
	}

	// Store any new migrations
	for _, mig := range generatedMigrations {
		version := uint32(mig.Version) // clickhousemigrator.Migration uses int
		if !storedVersions[version] {
			dynMig := DynamicMigration{
				Version:     version,
				Name:        mig.Name,
				Description: mig.Description,
				UpSQL:       mig.UpSQL,
				DownSQL:     mig.DownSQL,
				ConfigHash:  m.config.ConfigHash(),
			}
			if err := m.StoreMigration(ctx, dynMig); err != nil {
				return nil, fmt.Errorf("failed to store migration %d: %w", mig.Version, err)
			}
			m.logger.Info(ctx, "Stored new migration", map[string]interface{}{
				"version": mig.Version,
				"name":    mig.Name,
			})
		}
	}

	return generatedMigrations, nil
}

// GetMigrationsForClickhouseMigrator returns migrations in clickhousemigrator format.
// This first checks stored migrations, then generates any missing ones from config.
func (m *DynamicMigrationManager) GetMigrationsForClickhouseMigrator(
	ctx context.Context,
) ([]clickhousemigrator.Migration, error) {
	return m.GenerateAndStoreMigrations(ctx)
}

// GetCurrentStepColumns returns the list of step status column names from config.
func (m *DynamicMigrationManager) GetCurrentStepColumns() []string {
	columns := make([]string, len(m.config.Steps))
	for i, step := range m.config.Steps {
		columns[i] = fmt.Sprintf("%s_status", step.Name)
	}
	return columns
}

// GetAggregateColumns returns the list of aggregate JSON column names from config.
func (m *DynamicMigrationManager) GetAggregateColumns() []string {
	columns := make([]string, 0)
	for _, step := range m.config.Steps {
		if step.Aggregates != "" {
			columns = append(columns, pluralize(step.Aggregates))
		}
	}
	return columns
}

// Core schema uses versioned migrations (run once):
// - v1: Base routing_slips table
// - v2: History materialized view
//
// Dynamic schema uses ensurers (run every time, must be idempotent):
// - Step columns (ADD COLUMN IF NOT EXISTS)
// - Secondary indexes (ADD INDEX IF NOT EXISTS)

// GenerateEnsurers creates idempotent schema operations for dynamic columns.
// These run every time and handle step columns based on configuration.
func (m *DynamicMigrationManager) GenerateEnsurers() []clickhousemigrator.SchemaEnsurer {
	ensurers := make([]clickhousemigrator.SchemaEnsurer, 0)

	// Add ensurers for each step column
	for _, step := range m.config.Steps {
		ensurers = append(ensurers, m.generateStepColumnEnsurer(step))
	}

	// Add ensurer for secondary indexes
	ensurers = append(ensurers, m.generateIndexEnsurer())

	return ensurers
}

// generateMigrationsFromConfig creates the core versioned migrations.
// These define the table structure and run once per version.
func (m *DynamicMigrationManager) generateMigrationsFromConfig() []clickhousemigrator.Migration {
	return []clickhousemigrator.Migration{
		m.generateBaseTableMigration(),
		m.generateHistoryViewMigration(),
	}
}

// generateBaseTableMigration creates the core routing_slips table.
func (m *DynamicMigrationManager) generateBaseTableMigration() clickhousemigrator.Migration {
	return clickhousemigrator.Migration{
		Version:     1,
		Name:        "create_routing_slips_base",
		Description: "Creates the base routing_slips table with core columns",
		UpSQL: fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.routing_slips (
				-- Primary identifier
				correlation_id String,

				-- Repository metadata
				repository String,
				branch String,
				commit_sha String,

				-- Timestamps
				created_at DateTime64(3) DEFAULT now64(3),
				updated_at DateTime64(3) DEFAULT now64(3),

				-- Overall slip status
				status Enum8(
					'pending' = 1,
					'in_progress' = 2,
					'completed' = 3,
					'failed' = 4,
					'compensating' = 5,
					'compensated' = 6
				) DEFAULT 'pending',

				-- Step execution details (timing, actor, errors for all steps)
				step_details JSON DEFAULT '{}',

				-- Complete audit trail (array wrapped in object for ClickHouse JSON compatibility)
				state_history JSON DEFAULT '{"entries":[]}',

				-- Bloom filter indexes
				INDEX idx_repository repository TYPE bloom_filter GRANULARITY 1,
				INDEX idx_commit commit_sha TYPE bloom_filter GRANULARITY 1,
				INDEX idx_branch branch TYPE bloom_filter GRANULARITY 1
			)
			ENGINE = ReplacingMergeTree(updated_at)
			ORDER BY (correlation_id)
			PARTITION BY toYYYYMM(created_at)
			SETTINGS index_granularity = 8192
		`, m.database),
		DownSQL: fmt.Sprintf(`DROP TABLE IF EXISTS %s.routing_slips`, m.database),
	}
}

// generateHistoryViewMigration creates the materialized view for historical tracking.
func (m *DynamicMigrationManager) generateHistoryViewMigration() clickhousemigrator.Migration {
	// NOTE: state_history.entries is a Dynamic type in ClickHouse's JSON column.
	// We use dynamicElement() to extract the Array from the Dynamic type,
	// which allows ARRAY JOIN to work properly without converting to String.
	return clickhousemigrator.Migration{
		Version:     2,
		Name:        "create_routing_slip_history_mv",
		Description: "Creates materialized view for historical tracking of all slip state changes",
		UpSQL: fmt.Sprintf(`
			CREATE MATERIALIZED VIEW IF NOT EXISTS %s.routing_slip_history_mv
			ENGINE = MergeTree()
			ORDER BY (correlation_id, timestamp)
			PARTITION BY toYYYYMM(timestamp)
			AS SELECT
				correlation_id,
				repository,
				entry.step::String AS step,
				entry.component::String AS component,
				entry.status::String AS status,
				parseDateTimeBestEffort(entry.timestamp::String) AS timestamp,
				entry.actor::String AS actor,
				entry.message::String AS message
			FROM %s.routing_slips
			ARRAY JOIN dynamicElement(state_history.entries, 'Array(JSON)') AS entry
		`, m.database, m.database),
		DownSQL: fmt.Sprintf(`DROP VIEW IF EXISTS %s.routing_slip_history_mv`, m.database),
	}
}

// generateStepColumnEnsurer creates an idempotent ensurer for a step's columns.
// Uses ADD COLUMN IF NOT EXISTS so it's safe to run every time.
func (m *DynamicMigrationManager) generateStepColumnEnsurer(step StepConfig) clickhousemigrator.SchemaEnsurer {
	statusColumn := fmt.Sprintf("%s_status", step.Name)

	var sql strings.Builder
	sql.WriteString(fmt.Sprintf(`
		ALTER TABLE %s.routing_slips
		ADD COLUMN IF NOT EXISTS %s Enum8(
			'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
			'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
		) DEFAULT 'pending'`, m.database, statusColumn))

	// If this is an aggregate step, add a JSON column for component data
	// Array wrapped in object for ClickHouse JSON compatibility
	if step.Aggregates != "" {
		aggregateColumn := pluralize(step.Aggregates)
		sql.WriteString(fmt.Sprintf(`,
		ADD COLUMN IF NOT EXISTS %s JSON DEFAULT '{"items":[]}'`, aggregateColumn))
	}

	description := fmt.Sprintf("Ensures %s column exists for step '%s'", statusColumn, step.Name)
	if step.Aggregates != "" {
		description += fmt.Sprintf(" and %s column for component data", pluralize(step.Aggregates))
	}

	return clickhousemigrator.SchemaEnsurer{
		Name:        fmt.Sprintf("ensure_step_%s", step.Name),
		Description: description,
		SQL:         sql.String(),
	}
}

// generateIndexEnsurer creates an idempotent ensurer for secondary indexes.
func (m *DynamicMigrationManager) generateIndexEnsurer() clickhousemigrator.SchemaEnsurer {
	var sql strings.Builder

	sql.WriteString(fmt.Sprintf(`
		ALTER TABLE %s.routing_slips
		ADD INDEX IF NOT EXISTS idx_status status TYPE set(10) GRANULARITY 1
	`, m.database))

	// Add indexes for deploy steps (commonly queried for held status)
	deploySteps := []string{"dev_deploy", "preprod_deploy", "prod_deploy"}
	for _, stepName := range deploySteps {
		if m.config.GetStep(stepName) != nil {
			statusColumn := fmt.Sprintf("%s_status", stepName)
			indexName := fmt.Sprintf("idx_%s_held", stepName)

			sql.WriteString(fmt.Sprintf(`,
		ADD INDEX IF NOT EXISTS %s %s TYPE set(10) GRANULARITY 1
			`, indexName, statusColumn))
		}
	}

	return clickhousemigrator.SchemaEnsurer{
		Name:        "ensure_secondary_indexes",
		Description: "Ensures secondary indexes exist for status columns",
		SQL:         sql.String(),
	}
}
