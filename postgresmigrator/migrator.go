package postgresmigrator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Migrator implements DatabaseMigrator for Postgres over a pgx Querier.
// Versioned migrations are applied in transactions (Postgres has transactional
// DDL); ensurers run every time afterwards.
type Migrator struct {
	conn             Querier
	logger           Logger
	migrations       []Migration
	ensurers         []SchemaEnsurer
	expectedTables   []string
	migrationTimeout time.Duration
	schema           string // optional schema qualifier (default: search_path/public)
	tablePrefix      string // prefix for the schema_version table
}

var _ DatabaseMigrator = (*Migrator)(nil)

// MigratorOption configures a Migrator.
type MigratorOption func(*Migrator)

// WithMigrations registers migrations (re-sorted by version).
func WithMigrations(migrations []Migration) MigratorOption {
	return func(m *Migrator) {
		m.migrations = append(m.migrations, migrations...)
		m.sortMigrations()
	}
}

// WithEnsurers registers idempotent ensurers that run after migrations.
func WithEnsurers(ensurers []SchemaEnsurer) MigratorOption {
	return func(m *Migrator) {
		m.ensurers = append(m.ensurers, ensurers...)
	}
}

// WithSchema sets an explicit schema qualifier for the migrator's tables.
// If unset, unqualified names are used (resolved via search_path, i.e. public).
func WithSchema(schema string) MigratorOption {
	return func(m *Migrator) {
		m.schema = schema
	}
}

// WithTablePrefix sets a prefix for the schema_version table so multiple
// libraries can share a database. WithTablePrefix("slippy") -> "slippy_schema_version".
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

// WithMigrationTimeout sets the per-migration timeout.
func WithMigrationTimeout(timeout time.Duration) MigratorOption {
	return func(m *Migrator) {
		m.migrationTimeout = timeout
	}
}

// NewMigrator creates a Postgres migrator over the given pgx querier
// (*pgxpool.Pool satisfies Querier).
func NewMigrator(conn Querier, logger Logger, opts ...MigratorOption) (*Migrator, error) {
	if conn == nil {
		return nil, ErrNilConnection
	}
	if logger == nil {
		logger = &NopLogger{}
	}

	m := &Migrator{
		conn:             conn,
		logger:           logger,
		migrations:       []Migration{},
		expectedTables:   []string{},
		migrationTimeout: 5 * time.Minute,
	}
	for _, opt := range opts {
		opt(m)
	}
	if len(m.expectedTables) == 0 {
		m.expectedTables = []string{m.schemaVersionTableName()}
	}
	return m, nil
}

// NewMigratorFromSession creates a migrator from anything exposing a
// *pgxpool.Pool (e.g. goLibMyCarrier/postgres.PostgresSession).
func NewMigratorFromSession(session PoolProvider, logger Logger, opts ...MigratorOption) (*Migrator, error) {
	if session == nil {
		return nil, ErrNilConnection
	}
	return NewMigrator(session.Pool(), logger, opts...)
}

// CreateTables applies pending migrations, then runs the ensurers.
//
// Concurrency: this reads the current schema version and then applies migrations without a
// session-level advisory lock, so it assumes a single migrator runs at a time — the intended
// deployment is one pre-deploy Job (sync-wave/order:0). Because each migration commits inside
// its own transaction and every step is idempotent (CREATE/ALTER ... IF [NOT] EXISTS), a
// double-run is self-correcting rather than corrupting. If concurrent migrators ever become
// possible, wrap this in pg_advisory_lock(<key>) so only one applies at a time.
func (m *Migrator) CreateTables(ctx context.Context) error {
	m.logger.Info(ctx, "Creating database tables", nil)

	if err := m.createSchemaVersionTable(ctx); err != nil {
		return fmt.Errorf("failed to create schema version table: %w", err)
	}

	currentVersion, err := m.GetSchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current schema version: %w", err)
	}
	m.logger.Info(ctx, "Current database schema version", map[string]interface{}{"version": currentVersion})

	applied := 0
	for _, migration := range m.migrations {
		if migration.Version > currentVersion {
			if err := m.applyMigrationWithTimeout(ctx, migration); err != nil {
				return err
			}
			applied++
		}
	}
	if applied == 0 {
		m.logger.Info(ctx, "No migrations needed - database is up to date", nil)
	} else {
		m.logger.Info(ctx, "Database migration completed", map[string]interface{}{"migrations_applied": applied})
	}

	if err := m.runEnsurers(ctx); err != nil {
		return err
	}
	m.logger.Info(ctx, "Database tables created successfully", nil)
	return nil
}

// DropTables drops all expected tables (reverse order) with CASCADE.
func (m *Migrator) DropTables(ctx context.Context) error {
	m.logger.Warning(ctx, "Dropping all database tables", nil)

	tables := make([]string, len(m.expectedTables))
	copy(tables, m.expectedTables)
	for i, j := 0, len(tables)-1; i < j; i, j = i+1, j-1 {
		tables[i], tables[j] = tables[j], tables[i]
	}

	for _, table := range tables {
		qualified := m.qualifiedTableName(table)
		if _, err := m.conn.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", qualified)); err != nil {
			m.logger.Error(ctx, "Failed to drop table", err, map[string]interface{}{"table": qualified})
			return fmt.Errorf("failed to drop table %s: %w", qualified, err)
		}
		m.logger.Info(ctx, "Table dropped", map[string]interface{}{"table": qualified})
	}
	return nil
}

// GetSchemaVersion returns the current schema version (0 if the version table
// does not exist yet or is empty).
func (m *Migrator) GetSchemaVersion(ctx context.Context) (int, error) {
	qualified := m.qualifiedTableName(m.schemaVersionTableName())

	var exists bool
	if err := m.conn.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", qualified).Scan(&exists); err != nil {
		return 0, fmt.Errorf("failed to check if %s exists: %w", qualified, err)
	}
	if !exists {
		return 0, nil
	}

	var version int
	// Order by the monotonic id, not applied_at: applied_at is now() at transaction
	// start, so concurrent-microsecond ties are unordered and a backward clock could
	// surface a stale row; a down-migration also writes the older version with a newer
	// applied_at. id increases strictly with insert order in every case.
	err := m.conn.QueryRow(ctx,
		fmt.Sprintf("SELECT version FROM %s ORDER BY id DESC LIMIT 1", qualified),
	).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get schema version: %w", err)
	}
	return version, nil
}

// SetSchemaVersion records a schema version.
func (m *Migrator) SetSchemaVersion(ctx context.Context, version int) error {
	if _, err := m.conn.Exec(ctx, m.insertVersionSQL(), int32(version)); err != nil {
		return fmt.Errorf("failed to set schema version to %d: %w", version, err)
	}
	return nil
}

// MigrateToVersion migrates up or down to targetVersion.
func (m *Migrator) MigrateToVersion(ctx context.Context, targetVersion int) error {
	currentVersion, err := m.GetSchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current schema version: %w", err)
	}
	if currentVersion == targetVersion {
		m.logger.Info(ctx, "Database is already at target version", map[string]interface{}{"version": targetVersion})
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
		"target_version": targetVersion, "from_version": currentVersion,
	})
	return nil
}

// GetAvailableMigrations returns the registered migrations.
func (m *Migrator) GetAvailableMigrations() []Migration { return m.migrations }

// ValidateSchema checks that all expected tables exist.
func (m *Migrator) ValidateSchema(ctx context.Context) error {
	m.logger.Info(ctx, "Validating database schema", nil)
	for _, table := range m.expectedTables {
		qualified := m.qualifiedTableName(table)
		var exists bool
		if err := m.conn.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", qualified).Scan(&exists); err != nil {
			return &SchemaValidationError{Table: table, Message: "failed to check table existence", Err: err}
		}
		if !exists {
			return &SchemaValidationError{Table: table, Message: "required table does not exist", Err: ErrTableNotFound}
		}
	}
	m.logger.Info(ctx, "Database schema validation completed successfully", nil)
	return nil
}

// AddMigrations registers additional migrations (re-sorted).
func (m *Migrator) AddMigrations(migrations []Migration) {
	m.migrations = append(m.migrations, migrations...)
	m.sortMigrations()
}

// SetExpectedTables sets the tables checked by ValidateSchema.
func (m *Migrator) SetExpectedTables(tables []string) { m.expectedTables = tables }

// GetLatestVersion returns the highest registered migration version (0 if none).
func (m *Migrator) GetLatestVersion() int {
	if len(m.migrations) == 0 {
		return 0
	}
	return m.migrations[len(m.migrations)-1].Version
}

// GetPendingMigrations returns migrations newer than the current version.
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

// GetEnsurers returns the registered ensurers.
func (m *Migrator) GetEnsurers() []SchemaEnsurer { return m.ensurers }

// AddEnsurers registers additional ensurers.
func (m *Migrator) AddEnsurers(ensurers []SchemaEnsurer) {
	m.ensurers = append(m.ensurers, ensurers...)
}

func (m *Migrator) schemaVersionTableName() string {
	if m.tablePrefix != "" {
		return fmt.Sprintf("%s_schema_version", m.tablePrefix)
	}
	return "schema_version"
}

func (m *Migrator) qualifiedTableName(table string) string {
	if m.schema != "" {
		return fmt.Sprintf("%s.%s", m.schema, table)
	}
	return table
}

func (m *Migrator) insertVersionSQL() string {
	return fmt.Sprintf(
		"INSERT INTO %s (version, applied_at) VALUES ($1, now())",
		m.qualifiedTableName(m.schemaVersionTableName()),
	)
}

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

func (m *Migrator) migrateDown(ctx context.Context, currentVersion, targetVersion int) error {
	for i := len(m.migrations) - 1; i >= 0; i-- {
		migration := m.migrations[i]
		if migration.Version > currentVersion || migration.Version <= targetVersion {
			continue
		}
		if strings.TrimSpace(migration.DownSQL) == "" {
			m.logger.Info(ctx, "Skipping migration with no DownSQL (not reversible)", map[string]interface{}{
				"version": migration.Version, "name": migration.Name,
			})
			continue
		}

		previousVersion := targetVersion
		for j := i - 1; j >= 0; j-- {
			if m.migrations[j].Version <= targetVersion {
				previousVersion = m.migrations[j].Version
				break
			}
		}

		if err := m.revertMigration(ctx, migration, previousVersion); err != nil {
			return err
		}
		m.logger.Info(ctx, "Migration reverted successfully", map[string]interface{}{
			"version": migration.Version, "name": migration.Name,
		})
	}
	return nil
}

func (m *Migrator) createSchemaVersionTable(ctx context.Context) error {
	qualified := m.qualifiedTableName(m.schemaVersionTableName())
	createSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id bigserial,
			version integer NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`, qualified)
	// The ALTER backfills the monotonic id on version tables created before it existed, so
	// GetSchemaVersion can order by a strictly increasing key rather than applied_at.
	// Idempotent, and ADD COLUMN populates the sequence for any existing rows in physical
	// (insert) order — matching their applied_at order for this append-only table.
	alterSQL := fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS id bigserial", qualified)
	// CREATE + backfill run in one transaction (Postgres DDL is transactional) so the
	// version table can never linger created-but-without-id.
	return m.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, createSQL); err != nil {
			return fmt.Errorf("failed to create %s table: %w", m.schemaVersionTableName(), err)
		}
		if _, err := tx.Exec(ctx, alterSQL); err != nil {
			return fmt.Errorf("failed to add id column to %s: %w", m.schemaVersionTableName(), err)
		}
		return nil
	})
}

// applyMigrationWithTimeout applies a single migration in a transaction (DDL +
// version bump commit or roll back together).
func (m *Migrator) applyMigrationWithTimeout(ctx context.Context, migration Migration) error {
	if strings.TrimSpace(migration.UpSQL) == "" {
		return &MigrationError{
			Version:     migration.Version,
			Name:        migration.Name,
			Description: migration.Description,
			Operation:   "up",
			Err: fmt.Errorf(
				"UpSQL is empty — migration %d (%s) has no SQL to apply",
				migration.Version,
				migration.Name,
			),
		}
	}

	mctx, cancel := context.WithTimeout(ctx, m.migrationTimeout)
	defer cancel()

	m.logger.Info(ctx, "Applying migration", map[string]interface{}{
		"version": migration.Version, "name": migration.Name, "description": migration.Description,
	})

	if err := m.inTx(mctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(mctx, migration.UpSQL); err != nil {
			return err
		}
		_, err := tx.Exec(mctx, m.insertVersionSQL(), int32(migration.Version))
		return err
	}); err != nil {
		return &MigrationError{
			Version: migration.Version, Name: migration.Name, Description: migration.Description,
			Operation: "up", Err: err,
		}
	}

	m.logger.Info(ctx, "Migration applied successfully", map[string]interface{}{
		"version": migration.Version, "name": migration.Name,
	})
	return nil
}

// revertMigration runs a migration's DownSQL and records previousVersion, in a
// single transaction.
func (m *Migrator) revertMigration(ctx context.Context, migration Migration, previousVersion int) error {
	mctx, cancel := context.WithTimeout(ctx, m.migrationTimeout)
	defer cancel()

	m.logger.Info(ctx, "Reverting migration", map[string]interface{}{
		"version": migration.Version, "name": migration.Name, "description": migration.Description,
	})

	if err := m.inTx(mctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(mctx, migration.DownSQL); err != nil {
			return err
		}
		_, err := tx.Exec(mctx, m.insertVersionSQL(), int32(previousVersion))
		return err
	}); err != nil {
		return &MigrationError{
			Version: migration.Version, Name: migration.Name, Description: migration.Description,
			Operation: "down", Err: err,
		}
	}
	return nil
}

// inTx runs fn inside a transaction, committing on success and rolling back on
// error.
func (m *Migrator) inTx(ctx context.Context, fn func(pgx.Tx) error) (err error) {
	tx, beginErr := m.conn.Begin(ctx)
	if beginErr != nil {
		return fmt.Errorf("begin transaction: %w", beginErr)
	}
	// Roll back on any early exit — including a panic in fn — so the transaction's
	// connection is always released. Skipped after a successful commit; a genuine
	// rollback failure is surfaced only when no earlier error already is.
	committed := false
	defer func() {
		if committed {
			return
		}
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) && err == nil {
			err = fmt.Errorf("rollback: %w", rbErr)
		}
	}()

	if fnErr := fn(tx); fnErr != nil {
		return fnErr
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return fmt.Errorf("commit transaction: %w", commitErr)
	}
	committed = true
	return nil
}

// runEnsurers executes all idempotent ensurers.
func (m *Migrator) runEnsurers(ctx context.Context) error {
	if len(m.ensurers) == 0 {
		return nil
	}
	m.logger.Info(ctx, "Running schema ensurers", map[string]interface{}{"count": len(m.ensurers)})
	for _, ensurer := range m.ensurers {
		ensurerCtx, cancel := context.WithTimeout(ctx, m.migrationTimeout)
		_, err := m.conn.Exec(ensurerCtx, ensurer.SQL)
		cancel()
		if err != nil {
			return fmt.Errorf("ensurer %q failed: %w", ensurer.Name, err)
		}
		m.logger.Debug(ctx, "Ensurer completed", map[string]interface{}{"name": ensurer.Name})
	}
	m.logger.Info(ctx, "Schema ensurers completed", map[string]interface{}{"count": len(m.ensurers)})
	return nil
}

func (m *Migrator) sortMigrations() {
	sort.Slice(m.migrations, func(i, j int) bool {
		return m.migrations[i].Version < m.migrations[j].Version
	})
}
