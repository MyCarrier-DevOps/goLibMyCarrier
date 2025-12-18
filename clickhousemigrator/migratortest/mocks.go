// Package migratortest provides test fixtures and mocks for testing code that uses the clickhousemigrator package.
// This follows the Go standard library pattern (e.g., net/http/httptest).
//
// Example usage:
//
//	func TestMyMigration(t *testing.T) {
//	    conn := migratortest.NewMockConnection()
//	    log := migratortest.NewMockLogger()
//
//	    // Configure expected behavior
//	    conn.MockConn.ExecResults["CREATE TABLE"] = nil
//
//	    // Run your migration
//	    err := runMigration(conn, log)
//	    if err != nil {
//	        t.Fatal(err)
//	    }
//
//	    // Verify queries were executed
//	    if len(conn.MockConn.ExecCalls) != 1 {
//	        t.Error("expected one exec call")
//	    }
//	}
package migratortest

import (
	"context"
	"io"
	"reflect"
	"sync"

	"github.com/ClickHouse/clickhouse-go/v2/lib/column"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// MockConnection implements the Connection interface for testing.
type MockConnection struct {
	mu sync.RWMutex

	// MockConn is the underlying mock driver.Conn
	MockConn *MockConn

	// CloseError is returned by Close if set
	CloseError error

	// CloseCalls tracks calls to Close
	CloseCalls int
}

// NewMockConnection creates a new MockConnection with an initialized MockConn.
func NewMockConnection() *MockConnection {
	return &MockConnection{
		MockConn: NewMockConn(),
	}
}

// Conn returns the underlying mock connection.
func (m *MockConnection) Conn() driver.Conn {
	return m.MockConn
}

// Close closes the connection.
func (m *MockConnection) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CloseCalls++
	return m.CloseError
}

// MockConn implements driver.Conn for testing.
type MockConn struct {
	mu sync.RWMutex

	// ExecCalls tracks Exec calls
	ExecCalls []ExecCall

	// QueryCalls tracks Query calls
	QueryCalls []QueryCall

	// ExecResults maps query prefix to error (nil for success)
	ExecResults map[string]error

	// QueryResults maps query prefix to MockRows
	QueryResults map[string]*MockRows

	// DefaultExecError is returned if no matching ExecResults entry
	DefaultExecError error

	// DefaultQueryError is returned if no matching QueryResults entry
	DefaultQueryError error

	// PingError is returned by Ping
	PingError error

	// AsyncInsertError is returned by AsyncInsert
	AsyncInsertError error

	// PrepareBatchCalls tracks PrepareBatch calls
	PrepareBatchCalls int
}

// ExecCall records an Exec call.
type ExecCall struct {
	Ctx   context.Context
	Query string
	Args  []interface{}
}

// QueryCall records a Query call.
type QueryCall struct {
	Ctx   context.Context
	Query string
	Args  []interface{}
}

// NewMockConn creates a new MockConn with initialized maps.
func NewMockConn() *MockConn {
	return &MockConn{
		ExecResults:  make(map[string]error),
		QueryResults: make(map[string]*MockRows),
	}
}

// Query executes a query and returns rows.
func (m *MockConn) Query(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.QueryCalls = append(m.QueryCalls, QueryCall{
		Ctx:   ctx,
		Query: query,
		Args:  args,
	})

	// Check for matching result
	for prefix, rows := range m.QueryResults {
		if hasPrefix(query, prefix) {
			return rows, nil
		}
	}

	if m.DefaultQueryError != nil {
		return nil, m.DefaultQueryError
	}

	// Return empty rows by default
	return NewMockRows(), nil
}

// QueryRow executes a query and returns a single row.
func (m *MockConn) QueryRow(ctx context.Context, query string, args ...interface{}) driver.Row {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.QueryCalls = append(m.QueryCalls, QueryCall{
		Ctx:   ctx,
		Query: query,
		Args:  args,
	})

	// Check for matching result
	for prefix, rows := range m.QueryResults {
		if hasPrefix(query, prefix) && len(rows.RowData) > 0 {
			return rows.RowData[0]
		}
	}

	return NewMockRow()
}

// Exec executes a query without returning rows.
func (m *MockConn) Exec(ctx context.Context, query string, args ...interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ExecCalls = append(m.ExecCalls, ExecCall{
		Ctx:   ctx,
		Query: query,
		Args:  args,
	})

	// Check for matching error
	for prefix, err := range m.ExecResults {
		if hasPrefix(query, prefix) {
			return err
		}
	}

	return m.DefaultExecError
}

// Ping tests the connection.
func (m *MockConn) Ping(ctx context.Context) error {
	return m.PingError
}

// AsyncInsert performs an async insert.
func (m *MockConn) AsyncInsert(ctx context.Context, query string, wait bool, args ...interface{}) error {
	return m.AsyncInsertError
}

// PrepareBatch prepares a batch insert.
func (m *MockConn) PrepareBatch(ctx context.Context, query string, opts ...driver.PrepareBatchOption) (driver.Batch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.PrepareBatchCalls++
	return NewMockBatch(), nil
}

// Close closes the connection.
func (m *MockConn) Close() error {
	return nil
}

// Stats returns connection stats.
func (m *MockConn) Stats() driver.Stats {
	return driver.Stats{}
}

// Contributors returns contributor info.
func (m *MockConn) Contributors() []string {
	return nil
}

// ServerVersion returns the server version.
func (m *MockConn) ServerVersion() (*driver.ServerVersion, error) {
	return &driver.ServerVersion{}, nil
}

// Select executes a select query and scans into dest.
func (m *MockConn) Select(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	return nil
}

// MockRows implements driver.Rows for testing.
type MockRows struct {
	mu sync.Mutex

	// RowData contains the mock row data
	RowData []*MockRow

	// CurrentIndex tracks the current row position
	CurrentIndex int

	// ColumnNames contains column names
	ColumnNames []string

	// ColumnTypesData contains column type info
	ColumnTypesData []driver.ColumnType

	// CloseError is returned by Close
	CloseError error

	// RowError is returned by Err
	RowError error
}

// NewMockRows creates a new MockRows.
func NewMockRows() *MockRows {
	return &MockRows{
		RowData:         make([]*MockRow, 0),
		ColumnNames:     make([]string, 0),
		ColumnTypesData: make([]driver.ColumnType, 0),
	}
}

// Next advances to the next row.
func (m *MockRows) Next() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.CurrentIndex < len(m.RowData) {
		m.CurrentIndex++
		return true
	}
	return false
}

// Scan scans the current row into dest.
func (m *MockRows) Scan(dest ...interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.CurrentIndex == 0 || m.CurrentIndex > len(m.RowData) {
		return io.EOF
	}

	row := m.RowData[m.CurrentIndex-1]
	return row.Scan(dest...)
}

// ScanStruct scans the current row into a struct.
func (m *MockRows) ScanStruct(dest interface{}) error {
	return nil
}

// ColumnTypes returns column type information.
func (m *MockRows) ColumnTypes() []driver.ColumnType {
	return m.ColumnTypesData
}

// Columns returns column names.
func (m *MockRows) Columns() []string {
	return m.ColumnNames
}

// Totals returns totals row.
func (m *MockRows) Totals(dest ...interface{}) error {
	return nil
}

// Close closes the rows.
func (m *MockRows) Close() error {
	return m.CloseError
}

// Err returns any error from iteration.
func (m *MockRows) Err() error {
	return m.RowError
}

// AddRow adds a row to the mock rows.
func (m *MockRows) AddRow(values ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.RowData = append(m.RowData, &MockRow{Values: values})
}

// MockRow implements driver.Row for testing.
type MockRow struct {
	// Values contains the row data
	Values []interface{}

	// ScanError is returned by Scan
	ScanError error

	// RowError is returned by Err
	RowError error
}

// NewMockRow creates a new MockRow.
func NewMockRow() *MockRow {
	return &MockRow{
		Values: make([]interface{}, 0),
	}
}

// Scan scans the row into dest.
func (m *MockRow) Scan(dest ...interface{}) error {
	if m.ScanError != nil {
		return m.ScanError
	}

	for i, d := range dest {
		if i >= len(m.Values) {
			break
		}

		// Use reflection to set the value
		dv := reflect.ValueOf(d)
		if dv.Kind() != reflect.Ptr {
			continue
		}

		sv := reflect.ValueOf(m.Values[i])
		if sv.IsValid() && dv.Elem().Type().AssignableTo(sv.Type()) {
			dv.Elem().Set(sv)
		}
	}

	return nil
}

// ScanStruct scans the row into a struct.
func (m *MockRow) ScanStruct(dest interface{}) error {
	return nil
}

// Err returns any error associated with the row.
func (m *MockRow) Err() error {
	return m.RowError
}

// MockBatch implements driver.Batch for testing.
type MockBatch struct {
	mu sync.Mutex

	// AppendCalls tracks Append calls
	AppendCalls [][]interface{}

	// SendError is returned by Send
	SendError error

	// AbortError is returned by Abort
	AbortError error

	// CloseError is returned by Close
	CloseError error
}

// NewMockBatch creates a new MockBatch.
func NewMockBatch() *MockBatch {
	return &MockBatch{
		AppendCalls: make([][]interface{}, 0),
	}
}

// Append adds data to the batch.
func (m *MockBatch) Append(v ...interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.AppendCalls = append(m.AppendCalls, v)
	return nil
}

// AppendStruct adds a struct to the batch.
func (m *MockBatch) AppendStruct(v interface{}) error {
	return nil
}

// Column returns a batch column for direct writes.
func (m *MockBatch) Column(int) driver.BatchColumn {
	return nil
}

// Columns returns the column interfaces.
func (m *MockBatch) Columns() []column.Interface {
	return nil
}

// Flush flushes the batch.
func (m *MockBatch) Flush() error {
	return nil
}

// Send sends the batch.
func (m *MockBatch) Send() error {
	return m.SendError
}

// Abort aborts the batch.
func (m *MockBatch) Abort() error {
	return m.AbortError
}

// Close closes the batch.
func (m *MockBatch) Close() error {
	return m.CloseError
}

// IsSent returns whether the batch was sent.
func (m *MockBatch) IsSent() bool {
	return false
}

// Rows returns the number of rows in the batch.
func (m *MockBatch) Rows() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.AppendCalls)
}

// MockLogger implements a simple logger interface for testing.
type MockLogger struct {
	mu sync.RWMutex

	InfoLogs  []string
	DebugLogs []string
	WarnLogs  []string
	ErrorLogs []MockErrorLog
}

// MockErrorLog represents an error log entry.
type MockErrorLog struct {
	Message string
	Error   error
}

// NewMockLogger creates a new MockLogger.
func NewMockLogger() *MockLogger {
	return &MockLogger{
		InfoLogs:  make([]string, 0),
		DebugLogs: make([]string, 0),
		WarnLogs:  make([]string, 0),
		ErrorLogs: make([]MockErrorLog, 0),
	}
}

// Info logs an info message.
func (m *MockLogger) Info(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.InfoLogs = append(m.InfoLogs, msg)
}

// Debug logs a debug message.
func (m *MockLogger) Debug(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DebugLogs = append(m.DebugLogs, msg)
}

// Warn logs a warning message.
func (m *MockLogger) Warn(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WarnLogs = append(m.WarnLogs, msg)
}

// Warning is an alias for Warn.
func (m *MockLogger) Warning(msg string) {
	m.Warn(msg)
}

// Error logs an error message.
func (m *MockLogger) Error(msg string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ErrorLogs = append(m.ErrorLogs, MockErrorLog{Message: msg, Error: err})
}

// WithFields returns the logger (fields are ignored in mock).
func (m *MockLogger) WithFields(fields map[string]interface{}) *MockLogger {
	return m
}

// Reset clears all logged messages.
func (m *MockLogger) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.InfoLogs = make([]string, 0)
	m.DebugLogs = make([]string, 0)
	m.WarnLogs = make([]string, 0)
	m.ErrorLogs = make([]MockErrorLog, 0)
}

// HasLog checks if a log message exists at the specified level.
func (m *MockLogger) HasLog(level, message string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	switch level {
	case "info":
		for _, msg := range m.InfoLogs {
			if hasPrefix(msg, message) || msg == message {
				return true
			}
		}
	case "debug":
		for _, msg := range m.DebugLogs {
			if hasPrefix(msg, message) || msg == message {
				return true
			}
		}
	case "warn", "warning":
		for _, msg := range m.WarnLogs {
			if hasPrefix(msg, message) || msg == message {
				return true
			}
		}
	case "error":
		for _, entry := range m.ErrorLogs {
			if hasPrefix(entry.Message, message) || entry.Message == message {
				return true
			}
		}
	}

	return false
}

// hasPrefix is a simple prefix check.
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// Compile-time interface assertions
var (
	_ driver.Rows  = (*MockRows)(nil)
	_ driver.Row   = (*MockRow)(nil)
	_ driver.Batch = (*MockBatch)(nil)
)
