package clickhousemigrator

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Migrator implements the DatabaseMigrator interface for ClickHouse.
// It manages database schema migrations in a version-controlled manner.
// It works with the native clickhouse-go driver interface (driver.Conn).
type Migrator struct {
	conn             driver.Conn
	logger           Logger
	migrations       []Migration
	expectedTables   []string
	migrationTimeout time.Duration
	database         string
	tablePrefix      string // Prefix for schema_version table to avoid conflicts between libraries
}

// MigratorOption is a functional option for configuring a Migrator.
type MigratorOption func(*Migrator)

// WithMigrations adds migrations to the migrator.
func WithMigrations(migrations []Migration) MigratorOption {
	return func(m *Migrator) {
		m.migrations = append(m.migrations, migrations...)
		m.sortMigrations()
	}
}

// WithDatabase sets the database name for migrations.
// If not set, uses the connection's default database.
func WithDatabase(database string) MigratorOption {
	return func(m *Migrator) {
		m.database = database
	}
}

// WithTablePrefix sets a prefix for the schema_version table.
// This allows multiple libraries to use the same database without version conflicts.
// For example, WithTablePrefix("slippy") creates "slippy_schema_version" table.
// If not set, uses "schema_version" (not recommended for libraries).
func WithTablePrefix(prefix string) MigratorOption {
	return func(m *Migrator) {
		m.tablePrefix = prefix
	}
}

// WithExpectedTables sets the tables expected to exist after migrations.
func WithExpectedTables(tables []string) MigratorOption {
	return func(m *Migrator) {
		m.expectedTables = tables
	}
}

// WithMigrationTimeout sets the timeout for individual migrations.
func WithMigrationTimeout(timeout time.Duration) MigratorOption {
	return func(m *Migrator) {
		m.migrationTimeout = timeout
	}
}

// NewMigrator creates a new ClickHouse database migrator.
// It accepts either a driver.Conn directly or any type implementing the Connection interface
// (such as goLibMyCarrier/clickhouse.ClickhouseSession).
func NewMigrator(conn driver.Conn, logger Logger, opts ...MigratorOption) (*Migrator, error) {
	if conn == nil {
		return nil, ErrNilConnection
	}

	if logger == nil {
		logger = &NopLogger{}
	}

	migrator := &Migrator{
		conn:             conn,
		logger:           logger,
		migrations:       []Migration{},
		expectedTables:   []string{}, // Will be set after options are applied
		migrationTimeout: 5 * time.Minute,
	}

	// Apply options
	for _, opt := range opts {
		opt(migrator)
	}

	// Set default expected tables including the schema version table
	if len(migrator.expectedTables) == 0 {
		migrator.expectedTables = []string{migrator.schemaVersionTableName()}
	}

	return migrator, nil
}

// NewMigratorFromSession creates a new migrator from a Connection interface
// (e.g., goLibMyCarrier/clickhouse.ClickhouseSession).
func NewMigratorFromSession(session Connection, logger Logger, opts ...MigratorOption) (*Migrator, error) {
	if session == nil {
		return nil, ErrNilConnection
	}
	return NewMigrator(session.Conn(), logger, opts...)
}

// CreateTables creates all required database tables by running pending migrations.
func (m *Migrator) CreateTables(ctx context.Context) error {
	m.logger.Info(ctx, "Creating database tables", nil)

	// First create the schema version table if it doesn't exist
	m.logger.Debug(ctx, "Creating schema version table", nil)
	if err := m.createSchemaVersionTable(ctx); err != nil {
		return fmt.Errorf("failed to create schema version table: %w", err)
	}

	// Get current schema version
	m.logger.Debug(ctx, "Getting current schema version", nil)
	currentVersion, err := m.GetSchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current schema version: %w", err)
	}

	m.logger.Info(ctx, "Current database schema version", map[string]interface{}{
		"version": currentVersion,
	})

	// Apply all migrations newer than current version
	migrationsApplied := 0
	for _, migration := range m.migrations {
		if migration.Version > currentVersion {
			if err := m.applyMigrationWithTimeout(ctx, migration); err != nil {
				return err
			}
			migrationsApplied++
		}
	}

	if migrationsApplied == 0 {
		m.logger.Info(ctx, "No migrations needed - database is up to date", nil)
	} else {
		m.logger.Info(ctx, "Database migration completed", map[string]interface{}{
			"migrations_applied": migrationsApplied,
		})
	}

	m.logger.Info(ctx, "Database tables created successfully", nil)
	return nil
}

// DropTables drops all database tables (use with caution).
func (m *Migrator) DropTables(ctx context.Context) error {
	m.logger.Warning(ctx, "Dropping all database tables", nil)

	// Drop tables in reverse order to handle dependencies
	tables := make([]string, len(m.expectedTables))
	copy(tables, m.expectedTables)

	// Reverse the order
	for i, j := 0, len(tables)-1; i < j; i, j = i+1, j-1 {
		tables[i], tables[j] = tables[j], tables[i]
	}

	for _, table := range tables {
		qualifiedTable := m.qualifiedTableName(table)
		dropQuery := fmt.Sprintf("DROP TABLE IF EXISTS %s", qualifiedTable)
		if err := m.conn.Exec(ctx, dropQuery); err != nil {
			m.logger.Error(ctx, "Failed to drop table", err, map[string]interface{}{
				"table": qualifiedTable,
			})
			return fmt.Errorf("failed to drop table %s: %w", qualifiedTable, err)
		}
		m.logger.Info(ctx, "Table dropped", map[string]interface{}{
			"table": qualifiedTable,
		})
	}

	m.logger.Info(ctx, "All database tables dropped successfully", nil)
	return nil
}

// GetSchemaVersion returns the current schema version.
func (m *Migrator) GetSchemaVersion(ctx context.Context) (int, error) {
	m.logger.Debug(ctx, "Getting current schema version", nil)

	// Determine database to check
	dbCheck := "currentDatabase()"
	if m.database != "" {
		dbCheck = fmt.Sprintf("'%s'", m.database)
	}

	// Check if schema_version table exists
	checkSQL := fmt.Sprintf(`
		SELECT count() 
		FROM system.tables 
		WHERE database = %s AND name = '%s'
	`, dbCheck, m.schemaVersionTableName())

	var tableExists uint64
	row := m.conn.QueryRow(ctx, checkSQL)
	if err := row.Scan(&tableExists); err != nil {
		return 0, fmt.Errorf("failed to check if %s table exists: %w", m.schemaVersionTableName(), err)
	}

	if tableExists == 0 {
		m.logger.Debug(ctx, "Schema version table does not exist, returning version 0", nil)
		return 0, nil
	}

	// Get the latest version
	versionSQL := fmt.Sprintf(`
		SELECT version 
		FROM %s 
		ORDER BY applied_at DESC 
		LIMIT 1
	`, m.qualifiedTableName(m.schemaVersionTableName()))

	var version uint32
	row = m.conn.QueryRow(ctx, versionSQL)
	if err := row.Scan(&version); err != nil {
		// The native driver returns a different error for no rows
		// Check if it's an empty result by querying count
		countSQL := fmt.Sprintf(`SELECT count() FROM %s`, m.qualifiedTableName(m.schemaVersionTableName()))
		var count uint64
		countRow := m.conn.QueryRow(ctx, countSQL)
		if countErr := countRow.Scan(&count); countErr == nil && count == 0 {
			m.logger.Debug(ctx, "Schema version table is empty, returning version 0", nil)
			return 0, nil
		}
		return 0, fmt.Errorf("failed to get schema version: %w", err)
	}

	m.logger.Debug(ctx, "Current schema version retrieved", map[string]interface{}{
		"version": version,
	})
	return int(version), nil
}

// SetSchemaVersion sets the schema version.
func (m *Migrator) SetSchemaVersion(ctx context.Context, version int) error {
	insertSQL := fmt.Sprintf(`INSERT INTO %s (version, applied_at) VALUES (?, now())`, m.qualifiedTableName(m.schemaVersionTableName()))

	if err := m.conn.Exec(ctx, insertSQL, uint32(version)); err != nil {
		return fmt.Errorf("failed to set schema version to %d: %w", version, err)
	}

	return nil
}

// MigrateToVersion migrates the database to a specific version.
func (m *Migrator) MigrateToVersion(ctx context.Context, targetVersion int) error {
	currentVersion, err := m.GetSchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current schema version: %w", err)
	}

	if currentVersion == targetVersion {
		m.logger.Info(ctx, "Database is already at target version", map[string]interface{}{
			"version": targetVersion,
		})
		return nil
	}

	if targetVersion > currentVersion {
		if err := m.migrateUp(ctx, currentVersion, targetVersion); err != nil {
			return err
		}
	} else {
		if err := m.migrateDown(ctx, currentVersion, targetVersion); err != nil {
			return err
		}
	}

	m.logger.Info(ctx, "Database migration completed", map[string]interface{}{
		"target_version": targetVersion,
		"from_version":   currentVersion,
	})

	return nil
}

// GetAvailableMigrations returns a list of available migrations.
func (m *Migrator) GetAvailableMigrations() []Migration {
	return m.migrations
}

// ValidateSchema validates the current database schema.
func (m *Migrator) ValidateSchema(ctx context.Context) error {
	m.logger.Info(ctx, "Validating database schema", nil)

	// Determine database to check
	dbCheck := "currentDatabase()"
	if m.database != "" {
		dbCheck = fmt.Sprintf("'%s'", m.database)
	}

	for _, table := range m.expectedTables {
		checkSQL := fmt.Sprintf(`
			SELECT count() 
			FROM system.tables 
			WHERE database = %s AND name = ?
		`, dbCheck)

		var tableExists uint64
		row := m.conn.QueryRow(ctx, checkSQL, table)
		if err := row.Scan(&tableExists); err != nil {
			return &SchemaValidationError{
				Table:   table,
				Message: "failed to check table existence",
				Err:     err,
			}
		}

		if tableExists == 0 {
			return &SchemaValidationError{
				Table:   table,
				Message: "required table does not exist",
				Err:     ErrTableNotFound,
			}
		}
	}

	m.logger.Info(ctx, "Database schema validation completed successfully", nil)
	return nil
}

// AddMigrations adds additional migrations to the migrator.
func (m *Migrator) AddMigrations(migrations []Migration) {
	m.migrations = append(m.migrations, migrations...)
	m.sortMigrations()
}

// SetExpectedTables sets the expected tables for schema validation.
func (m *Migrator) SetExpectedTables(tables []string) {
	m.expectedTables = tables
}

// migrateUp applies migrations from currentVersion to targetVersion.
func (m *Migrator) migrateUp(ctx context.Context, currentVersion, targetVersion int) error {
	for _, migration := range m.migrations {
		if migration.Version <= currentVersion || migration.Version > targetVersion {
			continue
		}

		if err := m.applyMigrationWithTimeout(ctx, migration); err != nil {
			return err
		}
	}

	return nil
}

// migrateDown reverts migrations from currentVersion to targetVersion.
func (m *Migrator) migrateDown(ctx context.Context, currentVersion, targetVersion int) error {
	for i := len(m.migrations) - 1; i >= 0; i-- {
		migration := m.migrations[i]
		if migration.Version > currentVersion || migration.Version <= targetVersion {
			continue
		}

		m.logger.Info(ctx, "Reverting migration", map[string]interface{}{
			"version":     migration.Version,
			"name":        migration.Name,
			"description": migration.Description,
		})

		if err := m.conn.Exec(ctx, migration.DownSQL); err != nil {
			return &MigrationError{
				Version:     migration.Version,
				Name:        migration.Name,
				Description: migration.Description,
				Operation:   "down",
				Err:         err,
			}
		}

		// Find the previous version
		previousVersion := targetVersion
		for j := i - 1; j >= 0; j-- {
			if m.migrations[j].Version <= targetVersion {
				previousVersion = m.migrations[j].Version
				break
			}
		}

		if err := m.SetSchemaVersion(ctx, previousVersion); err != nil {
			return fmt.Errorf("failed to update schema version to %d: %w", previousVersion, err)
		}

		m.logger.Info(ctx, "Migration reverted successfully", map[string]interface{}{
			"version": migration.Version,
			"name":    migration.Name,
		})
	}

	return nil
}

// createSchemaVersionTable creates the schema version table if it doesn't exist.
func (m *Migrator) createSchemaVersionTable(ctx context.Context) error {
	// Build the CREATE TABLE statement with the proper table name
	createSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			version UInt32,
			applied_at DateTime DEFAULT now64()
		) ENGINE = MergeTree()
		ORDER BY applied_at
		SETTINGS index_granularity = 8192
	`, m.qualifiedTableName(m.schemaVersionTableName()))

	if err := m.conn.Exec(ctx, createSQL); err != nil {
		return fmt.Errorf("failed to create %s table: %w", m.schemaVersionTableName(), err)
	}

	return nil
}

// applyMigrationWithTimeout applies a single migration with extended timeout.
func (m *Migrator) applyMigrationWithTimeout(ctx context.Context, migration Migration) error {
	// Create a child context with extended timeout for complex table creation operations
	migrationCtx, cancel := context.WithTimeout(ctx, m.migrationTimeout)
	defer cancel()

	m.logger.Info(ctx, "Applying migration with extended timeout", map[string]interface{}{
		"version":     migration.Version,
		"name":        migration.Name,
		"description": migration.Description,
		"timeout":     m.migrationTimeout.String(),
	})

	if err := m.conn.Exec(migrationCtx, migration.UpSQL); err != nil {
		return &MigrationError{
			Version:     migration.Version,
			Name:        migration.Name,
			Description: migration.Description,
			Operation:   "up",
			Err:         err,
		}
	}

	if err := m.SetSchemaVersion(migrationCtx, migration.Version); err != nil {
		return fmt.Errorf("failed to update schema version to %d: %w", migration.Version, err)
	}

	m.logger.Info(ctx, "Migration applied successfully", map[string]interface{}{
		"version": migration.Version,
		"name":    migration.Name,
	})

	return nil
}

// sortMigrations sorts migrations by version.
func (m *Migrator) sortMigrations() {
	sort.Slice(m.migrations, func(i, j int) bool {
		return m.migrations[i].Version < m.migrations[j].Version
	})
}

// GetLatestVersion returns the highest migration version available.
func (m *Migrator) GetLatestVersion() int {
	if len(m.migrations) == 0 {
		return 0
	}
	return m.migrations[len(m.migrations)-1].Version
}

// GetPendingMigrations returns migrations that haven't been applied yet.
func (m *Migrator) GetPendingMigrations(ctx context.Context) ([]Migration, error) {
	currentVersion, err := m.GetSchemaVersion(ctx)
	if err != nil {
		return nil, err
	}

	var pending []Migration
	for _, migration := range m.migrations {
		if migration.Version > currentVersion {
			pending = append(pending, migration)
		}
	}

	return pending, nil
}

// IsMigrationApplied checks if a specific migration version has been applied.
func (m *Migrator) IsMigrationApplied(ctx context.Context, version int) (bool, error) {
	currentVersion, err := m.GetSchemaVersion(ctx)
	if err != nil {
		return false, err
	}
	return version <= currentVersion, nil
}

// Conn returns the underlying driver.Conn.
func (m *Migrator) Conn() driver.Conn {
	return m.conn
}

// schemaVersionTableName returns the name of the schema version table.
// If a table prefix is set, returns "{prefix}_schema_version", otherwise "schema_version".
func (m *Migrator) schemaVersionTableName() string {
	if m.tablePrefix != "" {
		return fmt.Sprintf("%s_schema_version", m.tablePrefix)
	}
	return "schema_version"
}

// qualifiedTableName returns the fully qualified table name with database prefix if set.
func (m *Migrator) qualifiedTableName(table string) string {
	if m.database != "" {
		return fmt.Sprintf("%s.%s", m.database, table)
	}
	return table
}
