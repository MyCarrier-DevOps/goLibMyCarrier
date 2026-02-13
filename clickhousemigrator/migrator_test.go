package clickhousemigrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ============================================================================
// NewMigrator Tests
// ============================================================================

func TestNewMigrator_Success(t *testing.T) {
	mockConn := &MockConn{}
	mockLogger := NewMockLogger()

	migrator, err := NewMigrator(mockConn, mockLogger)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if migrator == nil {
		t.Fatal("expected migrator to be non-nil")
	}

	// Verify connection is set (compare as interface values)
	if migrator.conn == nil {
		t.Error("expected connection to be set")
	}

	if migrator.logger != mockLogger {
		t.Error("expected logger to be set")
	}

	// Default timeout should be 5 minutes
	if migrator.migrationTimeout != 5*time.Minute {
		t.Errorf("expected default timeout of 5 minutes, got %v", migrator.migrationTimeout)
	}

	// Default expected tables should include schema_version
	if len(migrator.expectedTables) != 1 || migrator.expectedTables[0] != "schema_version" {
		t.Errorf("expected default expected tables to include schema_version, got %v", migrator.expectedTables)
	}
}

func TestNewMigrator_NilConnection(t *testing.T) {
	_, err := NewMigrator(nil, NewMockLogger())
	if err != ErrNilConnection {
		t.Errorf("expected ErrNilConnection, got %v", err)
	}
}

func TestNewMigrator_NilLogger(t *testing.T) {
	mockConn := &MockConn{}

	migrator, err := NewMigrator(mockConn, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should use NopLogger when nil is provided
	if _, ok := migrator.logger.(*NopLogger); !ok {
		t.Error("expected NopLogger to be used when nil logger is provided")
	}
}

func TestNewMigrator_WithOptions(t *testing.T) {
	mockConn := &MockConn{}
	migrations := []Migration{
		{Version: 1, Name: "test", UpSQL: "CREATE TABLE test", DownSQL: "DROP TABLE test"},
	}

	migrator, err := NewMigrator(
		mockConn,
		nil,
		WithMigrations(migrations),
		WithDatabase("testdb"),
		WithTablePrefix("myapp"),
		WithExpectedTables([]string{"custom_table"}),
		WithMigrationTimeout(10*time.Minute),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(migrator.migrations) != 1 {
		t.Errorf("expected 1 migration, got %d", len(migrator.migrations))
	}

	if migrator.database != "testdb" {
		t.Errorf("expected database 'testdb', got '%s'", migrator.database)
	}

	if migrator.tablePrefix != "myapp" {
		t.Errorf("expected tablePrefix 'myapp', got '%s'", migrator.tablePrefix)
	}

	if migrator.migrationTimeout != 10*time.Minute {
		t.Errorf("expected timeout of 10 minutes, got %v", migrator.migrationTimeout)
	}

	if len(migrator.expectedTables) != 1 || migrator.expectedTables[0] != "custom_table" {
		t.Errorf("expected custom_table, got %v", migrator.expectedTables)
	}
}

func TestNewMigratorFromSession_Success(t *testing.T) {
	mockConn := &MockConn{}
	mockSession := &MockConnection{MockConn: mockConn}

	migrator, err := NewMigratorFromSession(mockSession, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify connection is set
	if migrator.conn == nil {
		t.Error("expected connection from session")
	}
}

func TestNewMigratorFromSession_NilSession(t *testing.T) {
	_, err := NewMigratorFromSession(nil, nil)
	if err != ErrNilConnection {
		t.Errorf("expected ErrNilConnection, got %v", err)
	}
}

// ============================================================================
// Option Tests
// ============================================================================

func TestWithMigrations_SortsByVersion(t *testing.T) {
	mockConn := &MockConn{}
	migrations := []Migration{
		{Version: 3, Name: "third"},
		{Version: 1, Name: "first"},
		{Version: 2, Name: "second"},
	}

	migrator, _ := NewMigrator(mockConn, nil, WithMigrations(migrations))

	if migrator.migrations[0].Version != 1 {
		t.Errorf("expected first migration to be version 1, got %d", migrator.migrations[0].Version)
	}
	if migrator.migrations[1].Version != 2 {
		t.Errorf("expected second migration to be version 2, got %d", migrator.migrations[1].Version)
	}
	if migrator.migrations[2].Version != 3 {
		t.Errorf("expected third migration to be version 3, got %d", migrator.migrations[2].Version)
	}
}

func TestWithTablePrefix(t *testing.T) {
	mockConn := &MockConn{}

	migrator, _ := NewMigrator(mockConn, nil, WithTablePrefix("myapp"))

	tableName := migrator.schemaVersionTableName()
	if tableName != "myapp_schema_version" {
		t.Errorf("expected 'myapp_schema_version', got '%s'", tableName)
	}
}

func TestSchemaVersionTableName_NoPrefix(t *testing.T) {
	mockConn := &MockConn{}
	migrator, _ := NewMigrator(mockConn, nil)

	tableName := migrator.schemaVersionTableName()
	if tableName != "schema_version" {
		t.Errorf("expected 'schema_version', got '%s'", tableName)
	}
}

func TestQualifiedTableName_WithDatabase(t *testing.T) {
	mockConn := &MockConn{}
	migrator, _ := NewMigrator(mockConn, nil, WithDatabase("mydb"))

	qualified := migrator.qualifiedTableName("test_table")
	if qualified != "mydb.test_table" {
		t.Errorf("expected 'mydb.test_table', got '%s'", qualified)
	}
}

func TestQualifiedTableName_WithoutDatabase(t *testing.T) {
	mockConn := &MockConn{}
	migrator, _ := NewMigrator(mockConn, nil)

	qualified := migrator.qualifiedTableName("test_table")
	if qualified != "test_table" {
		t.Errorf("expected 'test_table', got '%s'", qualified)
	}
}

// ============================================================================
// GetSchemaVersion Tests
// ============================================================================

func TestGetSchemaVersion_TableDoesNotExist(t *testing.T) {
	mockConn := &MockConn{
		QueryRowValue: &MockRow{values: []interface{}{uint64(0)}},
	}
	migrator, _ := NewMigrator(mockConn, nil)

	version, err := migrator.GetSchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if version != 0 {
		t.Errorf("expected version 0, got %d", version)
	}
}

func TestGetSchemaVersion_TableExists_HasVersion(t *testing.T) {
	callCount := 0
	mockConn := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			callCount++
			if callCount == 1 {
				// First call: check if table exists
				return &MockRow{values: []interface{}{uint64(1)}}
			}
			// Second call: get version
			return &MockRow{values: []interface{}{uint32(5)}}
		},
	}
	migrator, _ := NewMigrator(mockConn, nil)

	version, err := migrator.GetSchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if version != 5 {
		t.Errorf("expected version 5, got %d", version)
	}
}

func TestGetSchemaVersion_TableExists_Empty(t *testing.T) {
	callCount := 0
	mockConn := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			callCount++
			if callCount == 1 {
				// First call: check if table exists
				return &MockRow{values: []interface{}{uint64(1)}}
			}
			if callCount == 2 {
				// Second call: get version - returns error (no rows)
				return &MockRow{scanErr: errors.New("no rows")}
			}
			// Third call: count rows
			return &MockRow{values: []interface{}{uint64(0)}}
		},
	}
	migrator, _ := NewMigrator(mockConn, nil)

	version, err := migrator.GetSchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if version != 0 {
		t.Errorf("expected version 0, got %d", version)
	}
}

func TestGetSchemaVersion_QueryError(t *testing.T) {
	mockConn := &MockConn{
		QueryRowValue: &MockRow{scanErr: errors.New("connection error")},
	}
	migrator, _ := NewMigrator(mockConn, nil)

	_, err := migrator.GetSchemaVersion(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetSchemaVersion_WithDatabase(t *testing.T) {
	mockConn := &MockConn{
		QueryRowValue: &MockRow{values: []interface{}{uint64(0)}},
	}
	migrator, _ := NewMigrator(mockConn, nil, WithDatabase("customdb"))

	_, _ = migrator.GetSchemaVersion(context.Background())

	// Verify the query uses the custom database
	if len(mockConn.QueryRowCalls) == 0 {
		t.Fatal("expected QueryRow to be called")
	}

	query := mockConn.QueryRowCalls[0].Query
	if !strings.Contains(query, "'customdb'") {
		t.Errorf("expected query to contain 'customdb', got: %s", query)
	}
}

// ============================================================================
// SetSchemaVersion Tests
// ============================================================================

func TestSetSchemaVersion_Success(t *testing.T) {
	mockConn := &MockConn{}
	migrator, _ := NewMigrator(mockConn, nil)

	err := migrator.SetSchemaVersion(context.Background(), 5)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(mockConn.ExecCalls) != 1 {
		t.Fatalf("expected 1 Exec call, got %d", len(mockConn.ExecCalls))
	}

	// Verify the version was passed as argument
	if len(mockConn.ExecCalls[0].Args) != 1 {
		t.Fatalf("expected 1 argument, got %d", len(mockConn.ExecCalls[0].Args))
	}

	if mockConn.ExecCalls[0].Args[0] != uint32(5) {
		t.Errorf("expected version 5, got %v", mockConn.ExecCalls[0].Args[0])
	}
}

func TestSetSchemaVersion_Error(t *testing.T) {
	mockConn := &MockConn{ExecErr: errors.New("insert failed")}
	migrator, _ := NewMigrator(mockConn, nil)

	err := migrator.SetSchemaVersion(context.Background(), 5)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ============================================================================
// CreateTables Tests
// ============================================================================

func TestCreateTables_NoMigrations(t *testing.T) {
	callCount := 0
	mockConn := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			callCount++
			// Return table doesn't exist initially, then exists after creation
			return &MockRow{values: []interface{}{uint64(0)}}
		},
	}
	mockLogger := NewMockLogger()
	migrator, _ := NewMigrator(mockConn, mockLogger)

	err := migrator.CreateTables(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should have created schema_version table
	foundCreate := false
	for _, call := range mockConn.ExecCalls {
		if strings.Contains(call.Query, "CREATE TABLE IF NOT EXISTS") &&
			strings.Contains(call.Query, "schema_version") {
			foundCreate = true
			break
		}
	}
	if !foundCreate {
		t.Error("expected schema_version table creation query")
	}
}

func TestCreateTables_WithMigrations(t *testing.T) {
	callCount := 0
	mockConn := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			callCount++
			// First: table doesn't exist, second: table exists with version 0
			if callCount == 1 {
				return &MockRow{values: []interface{}{uint64(0)}}
			}
			return &MockRow{values: []interface{}{uint64(1), uint32(0)}}
		},
	}

	migrations := []Migration{
		{Version: 1, Name: "create_test", UpSQL: "CREATE TABLE test (id Int32)"},
	}
	migrator, _ := NewMigrator(mockConn, nil, WithMigrations(migrations))

	err := migrator.CreateTables(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should have run the migration
	foundMigration := false
	for _, call := range mockConn.ExecCalls {
		if strings.Contains(call.Query, "CREATE TABLE test") {
			foundMigration = true
			break
		}
	}
	if !foundMigration {
		t.Error("expected migration to be executed")
	}
}

func TestCreateTables_MigrationError(t *testing.T) {
	callCount := 0
	mockConn := &MockConn{
		ExecErrFunc: func(query string) error {
			if strings.Contains(query, "CREATE TABLE test") {
				return errors.New("migration failed")
			}
			return nil
		},
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			callCount++
			if callCount == 1 {
				return &MockRow{values: []interface{}{uint64(0)}}
			}
			return &MockRow{values: []interface{}{uint64(1), uint32(0)}}
		},
	}

	migrations := []Migration{
		{Version: 1, Name: "create_test", UpSQL: "CREATE TABLE test (id Int32)"},
	}
	migrator, _ := NewMigrator(mockConn, nil, WithMigrations(migrations))

	err := migrator.CreateTables(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var migErr *MigrationError
	if !errors.As(err, &migErr) {
		t.Errorf("expected MigrationError, got %T", err)
	}
}

// ============================================================================
// MigrateToVersion Tests
// ============================================================================

func TestMigrateToVersion_AlreadyAtVersion(t *testing.T) {
	callCount := 0
	mockConn := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			callCount++
			if callCount == 1 {
				return &MockRow{values: []interface{}{uint64(1)}}
			}
			return &MockRow{values: []interface{}{uint32(5)}}
		},
	}
	mockLogger := NewMockLogger()
	migrator, _ := NewMigrator(mockConn, mockLogger)

	err := migrator.MigrateToVersion(context.Background(), 5)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should log that we're already at target version
	found := false
	for _, log := range mockLogger.InfoLogs {
		if strings.Contains(log.Message, "already at target version") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected log message about already being at target version")
	}
}

func TestMigrateToVersion_MigrateUp(t *testing.T) {
	callCount := 0
	mockConn := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			callCount++
			if callCount == 1 {
				return &MockRow{values: []interface{}{uint64(1)}}
			}
			return &MockRow{values: []interface{}{uint32(0)}}
		},
	}

	migrations := []Migration{
		{Version: 1, Name: "v1", UpSQL: "SELECT 1"},
		{Version: 2, Name: "v2", UpSQL: "SELECT 2"},
	}
	migrator, _ := NewMigrator(mockConn, nil, WithMigrations(migrations))

	err := migrator.MigrateToVersion(context.Background(), 2)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should have executed both migrations
	execCount := 0
	for _, call := range mockConn.ExecCalls {
		if call.Query == "SELECT 1" || call.Query == "SELECT 2" {
			execCount++
		}
	}
	if execCount != 2 {
		t.Errorf("expected 2 migration executions, got %d", execCount)
	}
}

func TestMigrateToVersion_MigrateDown(t *testing.T) {
	callCount := 0
	mockConn := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			callCount++
			if callCount == 1 {
				return &MockRow{values: []interface{}{uint64(1)}}
			}
			return &MockRow{values: []interface{}{uint32(2)}}
		},
	}

	migrations := []Migration{
		{Version: 1, Name: "v1", UpSQL: "CREATE TABLE t1", DownSQL: "DROP TABLE t1"},
		{Version: 2, Name: "v2", UpSQL: "CREATE TABLE t2", DownSQL: "DROP TABLE t2"},
	}
	migrator, _ := NewMigrator(mockConn, nil, WithMigrations(migrations))

	err := migrator.MigrateToVersion(context.Background(), 0)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should have executed down migrations
	downCount := 0
	for _, call := range mockConn.ExecCalls {
		if strings.HasPrefix(call.Query, "DROP TABLE") {
			downCount++
		}
	}
	if downCount != 2 {
		t.Errorf("expected 2 down migration executions, got %d", downCount)
	}
}

// ============================================================================
// DropTables Tests
// ============================================================================

func TestDropTables_Success(t *testing.T) {
	mockConn := &MockConn{}
	migrator, _ := NewMigrator(mockConn, nil,
		WithExpectedTables([]string{"table1", "table2", "table3"}))

	err := migrator.DropTables(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should drop tables in reverse order
	if len(mockConn.ExecCalls) != 3 {
		t.Fatalf("expected 3 Exec calls, got %d", len(mockConn.ExecCalls))
	}

	// Verify reverse order
	if !strings.Contains(mockConn.ExecCalls[0].Query, "table3") {
		t.Error("expected table3 to be dropped first")
	}
	if !strings.Contains(mockConn.ExecCalls[2].Query, "table1") {
		t.Error("expected table1 to be dropped last")
	}
}

func TestDropTables_Error(t *testing.T) {
	mockConn := &MockConn{
		ExecErr: errors.New("drop failed"),
	}
	migrator, _ := NewMigrator(mockConn, nil,
		WithExpectedTables([]string{"table1"}))

	err := migrator.DropTables(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ============================================================================
// ValidateSchema Tests
// ============================================================================

func TestValidateSchema_AllTablesExist(t *testing.T) {
	mockConn := &MockConn{
		QueryRowValue: &MockRow{values: []interface{}{uint64(1)}},
	}
	migrator, _ := NewMigrator(mockConn, nil,
		WithExpectedTables([]string{"table1", "table2"}))

	err := migrator.ValidateSchema(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidateSchema_TableMissing(t *testing.T) {
	mockConn := &MockConn{
		QueryRowValue: &MockRow{values: []interface{}{uint64(0)}},
	}
	migrator, _ := NewMigrator(mockConn, nil,
		WithExpectedTables([]string{"missing_table"}))

	err := migrator.ValidateSchema(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var validationErr *SchemaValidationError
	if !errors.As(err, &validationErr) {
		t.Errorf("expected SchemaValidationError, got %T", err)
	}

	if validationErr.Table != "missing_table" {
		t.Errorf("expected table 'missing_table', got '%s'", validationErr.Table)
	}
}

func TestValidateSchema_QueryError(t *testing.T) {
	mockConn := &MockConn{
		QueryRowValue: &MockRow{scanErr: errors.New("query error")},
	}
	migrator, _ := NewMigrator(mockConn, nil,
		WithExpectedTables([]string{"table1"}))

	err := migrator.ValidateSchema(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ============================================================================
// AddMigrations Tests
// ============================================================================

func TestAddMigrations(t *testing.T) {
	mockConn := &MockConn{}
	migrator, _ := NewMigrator(mockConn, nil)

	migrator.AddMigrations([]Migration{
		{Version: 2, Name: "second"},
		{Version: 1, Name: "first"},
	})

	if len(migrator.migrations) != 2 {
		t.Fatalf("expected 2 migrations, got %d", len(migrator.migrations))
	}

	// Should be sorted
	if migrator.migrations[0].Version != 1 {
		t.Errorf("expected first migration to be version 1, got %d", migrator.migrations[0].Version)
	}
}

// ============================================================================
// GetAvailableMigrations Tests
// ============================================================================

func TestGetAvailableMigrations(t *testing.T) {
	mockConn := &MockConn{}
	migrations := []Migration{
		{Version: 1, Name: "first"},
		{Version: 2, Name: "second"},
	}
	migrator, _ := NewMigrator(mockConn, nil, WithMigrations(migrations))

	available := migrator.GetAvailableMigrations()
	if len(available) != 2 {
		t.Errorf("expected 2 migrations, got %d", len(available))
	}
}

// ============================================================================
// GetLatestVersion Tests
// ============================================================================

func TestGetLatestVersion_NoMigrations(t *testing.T) {
	mockConn := &MockConn{}
	migrator, _ := NewMigrator(mockConn, nil)

	version := migrator.GetLatestVersion()
	if version != 0 {
		t.Errorf("expected version 0, got %d", version)
	}
}

func TestGetLatestVersion_WithMigrations(t *testing.T) {
	mockConn := &MockConn{}
	migrations := []Migration{
		{Version: 1, Name: "first"},
		{Version: 5, Name: "fifth"},
		{Version: 3, Name: "third"},
	}
	migrator, _ := NewMigrator(mockConn, nil, WithMigrations(migrations))

	version := migrator.GetLatestVersion()
	if version != 5 {
		t.Errorf("expected version 5, got %d", version)
	}
}

// ============================================================================
// GetPendingMigrations Tests
// ============================================================================

func TestGetPendingMigrations(t *testing.T) {
	callCount := 0
	mockConn := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			callCount++
			if callCount == 1 {
				return &MockRow{values: []interface{}{uint64(1)}}
			}
			return &MockRow{values: []interface{}{uint32(2)}}
		},
	}

	migrations := []Migration{
		{Version: 1, Name: "first"},
		{Version: 2, Name: "second"},
		{Version: 3, Name: "third"},
		{Version: 4, Name: "fourth"},
	}
	migrator, _ := NewMigrator(mockConn, nil, WithMigrations(migrations))

	pending, err := migrator.GetPendingMigrations(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(pending) != 2 {
		t.Errorf("expected 2 pending migrations, got %d", len(pending))
	}

	if pending[0].Version != 3 {
		t.Errorf("expected first pending to be version 3, got %d", pending[0].Version)
	}
}

// ============================================================================
// IsMigrationApplied Tests
// ============================================================================

func TestIsMigrationApplied_True(t *testing.T) {
	callCount := 0
	mockConn := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			callCount++
			if callCount == 1 {
				return &MockRow{values: []interface{}{uint64(1)}}
			}
			return &MockRow{values: []interface{}{uint32(5)}}
		},
	}
	migrator, _ := NewMigrator(mockConn, nil)

	applied, err := migrator.IsMigrationApplied(context.Background(), 3)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !applied {
		t.Error("expected migration to be applied")
	}
}

func TestIsMigrationApplied_False(t *testing.T) {
	callCount := 0
	mockConn := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			callCount++
			if callCount == 1 {
				return &MockRow{values: []interface{}{uint64(1)}}
			}
			return &MockRow{values: []interface{}{uint32(2)}}
		},
	}
	migrator, _ := NewMigrator(mockConn, nil)

	applied, err := migrator.IsMigrationApplied(context.Background(), 5)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if applied {
		t.Error("expected migration to not be applied")
	}
}

// ============================================================================
// Conn Tests
// ============================================================================

func TestConn(t *testing.T) {
	mockConn := &MockConn{}
	migrator, _ := NewMigrator(mockConn, nil)

	conn := migrator.Conn()
	if conn != mockConn {
		t.Error("expected Conn to return the underlying connection")
	}
}

// ============================================================================
// SetExpectedTables Tests
// ============================================================================

func TestSetExpectedTables(t *testing.T) {
	mockConn := &MockConn{}
	migrator, _ := NewMigrator(mockConn, nil)

	migrator.SetExpectedTables([]string{"custom1", "custom2"})

	if len(migrator.expectedTables) != 2 {
		t.Errorf("expected 2 tables, got %d", len(migrator.expectedTables))
	}
}

// ============================================================================
// Empty DownSQL / UpSQL Validation Tests
// ============================================================================

func TestMigrateDown_EmptyDownSQL(t *testing.T) {
	callCount := 0
	mockConn := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			callCount++
			if callCount == 1 {
				// Table exists
				return &MockRow{values: []interface{}{uint64(1)}}
			}
			// Current version is 2
			return &MockRow{values: []interface{}{uint32(2)}}
		},
	}

	migrations := []Migration{
		{Version: 1, Name: "v1", UpSQL: "CREATE TABLE t1", DownSQL: "DROP TABLE t1"},
		{Version: 2, Name: "v2_irreversible", UpSQL: "ALTER TABLE t1 ADD COLUMN x", DownSQL: ""},
	}
	migrator, _ := NewMigrator(mockConn, nil, WithMigrations(migrations))

	// Trying to migrate down from version 2 should fail because v2 has no DownSQL
	err := migrator.MigrateToVersion(context.Background(), 0)
	if err == nil {
		t.Fatal("expected error for empty DownSQL, got nil")
	}

	// Should indicate the migration is not reversible
	if !strings.Contains(err.Error(), "DownSQL is empty") {
		t.Errorf("expected error about empty DownSQL, got: %v", err)
	}

	// Should NOT have executed any Exec calls for down migrations
	for _, call := range mockConn.ExecCalls {
		if strings.HasPrefix(call.Query, "DROP TABLE") {
			t.Error("should not have executed any down migration SQL")
		}
	}
}

func TestCreateTables_EmptyUpSQL(t *testing.T) {
	callCount := 0
	mockConn := &MockConn{
		QueryRowFunc: func(ctx context.Context, query string, args ...interface{}) driver.Row {
			callCount++
			if callCount == 1 {
				// Table exists
				return &MockRow{values: []interface{}{uint64(1)}}
			}
			// Current version is 0
			return &MockRow{values: []interface{}{uint32(0)}}
		},
	}

	migrations := []Migration{
		{Version: 1, Name: "v1_empty", UpSQL: "", DownSQL: "DROP TABLE t1"},
	}
	migrator, _ := NewMigrator(mockConn, nil, WithMigrations(migrations))

	err := migrator.CreateTables(context.Background())
	if err == nil {
		t.Fatal("expected error for empty UpSQL, got nil")
	}

	if !strings.Contains(err.Error(), "UpSQL is empty") {
		t.Errorf("expected error about empty UpSQL, got: %v", err)
	}
}
