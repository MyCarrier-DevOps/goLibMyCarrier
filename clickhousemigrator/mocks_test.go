package clickhousemigrator

import (
	"context"
	"errors"
	"sync"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/MyCarrier-DevOps/goLibMyCarrier/logger"
)

// MockRow implements driver.Row for testing.
type MockRow struct {
	values        []interface{}
	scanErr       error
	scanFunc      func(dest ...interface{}) error
	err           error
	scanStructErr error
}

// Err implements driver.Row.
func (r *MockRow) Err() error {
	return r.err
}

// Scan implements driver.Row.
func (r *MockRow) Scan(dest ...interface{}) error {
	if r.scanFunc != nil {
		return r.scanFunc(dest...)
	}
	if r.scanErr != nil {
		return r.scanErr
	}
	// Copy values to destinations
	for i, v := range r.values {
		if i < len(dest) {
			switch d := dest[i].(type) {
			case *uint64:
				if val, ok := v.(uint64); ok {
					*d = val
				}
			case *uint32:
				if val, ok := v.(uint32); ok {
					*d = val
				}
			case *int:
				if val, ok := v.(int); ok {
					*d = val
				}
			case *string:
				if val, ok := v.(string); ok {
					*d = val
				}
			}
		}
	}
	return nil
}

// ScanStruct implements driver.Row.
func (r *MockRow) ScanStruct(dest any) error {
	return r.scanStructErr
}

// MockRows implements driver.Rows for testing.
type MockRows struct {
	columns       []string
	rows          [][]interface{}
	current       int
	closed        bool
	closeErr      error
	nextErr       error
	scanErr       error
	totalsErr     error
	scanStructErr error
}

// Columns implements driver.Rows.
func (r *MockRows) Columns() []string {
	return r.columns
}

// ColumnTypes implements driver.Rows.
func (r *MockRows) ColumnTypes() []driver.ColumnType {
	return nil
}

// Next implements driver.Rows.
func (r *MockRows) Next() bool {
	if r.nextErr != nil || r.closed {
		return false
	}
	r.current++
	return r.current <= len(r.rows)
}

// Scan implements driver.Rows.
func (r *MockRows) Scan(dest ...interface{}) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if r.current < 1 || r.current > len(r.rows) {
		return errors.New("no row to scan")
	}
	row := r.rows[r.current-1]
	for i, v := range row {
		if i < len(dest) {
			switch d := dest[i].(type) {
			case *uint64:
				if val, ok := v.(uint64); ok {
					*d = val
				}
			case *uint32:
				if val, ok := v.(uint32); ok {
					*d = val
				}
			case *int:
				if val, ok := v.(int); ok {
					*d = val
				}
			case *string:
				if val, ok := v.(string); ok {
					*d = val
				}
			}
		}
	}
	return nil
}

// ScanStruct implements driver.Rows.
func (r *MockRows) ScanStruct(dest any) error {
	return r.scanStructErr
}

// Totals implements driver.Rows.
func (r *MockRows) Totals(dest ...any) error {
	return r.totalsErr
}

// Close implements driver.Rows.
func (r *MockRows) Close() error {
	r.closed = true
	return r.closeErr
}

// Err implements driver.Rows.
func (r *MockRows) Err() error {
	return r.nextErr
}

// ExecCall records an Exec call.
type ExecCall struct {
	Query string
	Args  []interface{}
}

// QueryCall records a Query or QueryRow call.
type QueryCall struct {
	Query string
	Args  []interface{}
}

// StatsCall records a Stats call.
type StatsCall struct{}

// AsyncInsertCall records an AsyncInsert call.
type AsyncInsertCall struct {
	Query string
	Args  []interface{}
}

// PrepareBatchCall records a PrepareBatch call.
type PrepareBatchCall struct {
	Query string
}

// MockConn implements driver.Conn for testing.
type MockConn struct {
	mu sync.Mutex

	// Exec tracking
	ExecCalls    []ExecCall
	ExecErr      error
	ExecErrFunc  func(query string) error
	ExecCallback func(ctx context.Context, query string, args ...interface{})

	// Query tracking
	QueryCalls   []QueryCall
	QueryErr     error
	QueryRows    *MockRows
	QueryRowFunc func(ctx context.Context, query string, args ...interface{}) driver.Row

	// QueryRow tracking
	QueryRowCalls []QueryCall
	QueryRowErr   error
	QueryRowValue *MockRow

	// Select tracking
	SelectCalls []QueryCall
	SelectErr   error
	SelectFunc  func(ctx context.Context, dest any, query string, args ...any) error

	// Stats tracking
	StatsCalls []StatsCall
	StatsValue driver.Stats

	// Close tracking
	CloseCalled bool
	CloseErr    error

	// Ping tracking
	PingCalled bool
	PingErr    error

	// Contributors tracking
	ContributorsCalled bool
	ContributorsValue  []string

	// ServerVersion tracking
	ServerVersionCalled bool
	ServerVersionValue  string

	// AsyncInsert tracking
	AsyncInsertCalls []AsyncInsertCall
	AsyncInsertErr   error

	// PrepareBatch tracking
	PrepareBatchCalls []PrepareBatchCall
	PrepareBatchErr   error
	PrepareBatchValue driver.Batch
}

// Exec implements driver.Conn.
func (c *MockConn) Exec(ctx context.Context, query string, args ...interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ExecCalls = append(c.ExecCalls, ExecCall{Query: query, Args: args})
	if c.ExecCallback != nil {
		c.ExecCallback(ctx, query, args...)
	}
	if c.ExecErrFunc != nil {
		return c.ExecErrFunc(query)
	}
	return c.ExecErr
}

// Query implements driver.Conn.
func (c *MockConn) Query(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.QueryCalls = append(c.QueryCalls, QueryCall{Query: query, Args: args})
	if c.QueryErr != nil {
		return nil, c.QueryErr
	}
	if c.QueryRows != nil {
		return c.QueryRows, nil
	}
	return &MockRows{}, nil
}

// QueryRow implements driver.Conn.
func (c *MockConn) QueryRow(ctx context.Context, query string, args ...interface{}) driver.Row {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.QueryRowCalls = append(c.QueryRowCalls, QueryCall{Query: query, Args: args})
	if c.QueryRowFunc != nil {
		return c.QueryRowFunc(ctx, query, args...)
	}
	if c.QueryRowValue != nil {
		return c.QueryRowValue
	}
	// Return a default mock row
	return &MockRow{values: []interface{}{uint64(0)}}
}

// Select implements driver.Conn.
func (c *MockConn) Select(ctx context.Context, dest any, query string, args ...any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.SelectCalls = append(c.SelectCalls, QueryCall{Query: query, Args: args})
	if c.SelectFunc != nil {
		return c.SelectFunc(ctx, dest, query, args...)
	}
	return c.SelectErr
}

// Stats implements driver.Conn.
func (c *MockConn) Stats() driver.Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.StatsCalls = append(c.StatsCalls, StatsCall{})
	return c.StatsValue
}

// Close implements driver.Conn.
func (c *MockConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.CloseCalled = true
	return c.CloseErr
}

// Ping implements driver.Conn.
func (c *MockConn) Ping(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.PingCalled = true
	return c.PingErr
}

// Contributors implements driver.Conn.
func (c *MockConn) Contributors() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ContributorsCalled = true
	return c.ContributorsValue
}

// ServerVersion implements driver.Conn.
func (c *MockConn) ServerVersion() (*driver.ServerVersion, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ServerVersionCalled = true
	return &driver.ServerVersion{}, nil
}

// AsyncInsert implements driver.Conn.
func (c *MockConn) AsyncInsert(ctx context.Context, query string, wait bool, args ...interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.AsyncInsertCalls = append(c.AsyncInsertCalls, AsyncInsertCall{Query: query, Args: args})
	return c.AsyncInsertErr
}

// PrepareBatch implements driver.Conn.
func (c *MockConn) PrepareBatch(ctx context.Context, query string, opts ...driver.PrepareBatchOption) (driver.Batch, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.PrepareBatchCalls = append(c.PrepareBatchCalls, PrepareBatchCall{Query: query})
	if c.PrepareBatchErr != nil {
		return nil, c.PrepareBatchErr
	}
	return c.PrepareBatchValue, nil
}

// Reset clears all call tracking.
func (c *MockConn) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ExecCalls = nil
	c.QueryCalls = nil
	c.QueryRowCalls = nil
	c.SelectCalls = nil
	c.StatsCalls = nil
	c.CloseCalled = false
	c.PingCalled = false
	c.ContributorsCalled = false
	c.ServerVersionCalled = false
	c.AsyncInsertCalls = nil
	c.PrepareBatchCalls = nil
}

// MockConnection implements the Connection interface for testing.
type MockConnection struct {
	MockConn *MockConn
	CloseErr error
}

// Conn implements Connection.
func (c *MockConnection) Conn() driver.Conn {
	return c.MockConn
}

// Close implements Connection.
func (c *MockConnection) Close() error {
	return c.CloseErr
}

// LogEntry represents a log entry.
type LogEntry struct {
	Message string
	Fields  map[string]interface{}
}

// ErrorLogEntry represents an error log entry.
type ErrorLogEntry struct {
	Message string
	Err     error
	Fields  map[string]interface{}
}

// MockLogger implements logger.Logger for testing.
type MockLogger struct {
	mu        sync.Mutex
	InfoLogs  []LogEntry
	DebugLogs []LogEntry
	WarnLogs  []LogEntry
	ErrorLogs []ErrorLogEntry
	Fields    map[string]interface{}
}

// NewMockLogger creates a new MockLogger.
func NewMockLogger() *MockLogger {
	return &MockLogger{
		Fields: make(map[string]interface{}),
	}
}

// Info implements logger.Logger.
func (l *MockLogger) Info(ctx context.Context, message string, fields map[string]interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.InfoLogs = append(l.InfoLogs, LogEntry{Message: message, Fields: fields})
}

// Debug implements logger.Logger.
func (l *MockLogger) Debug(ctx context.Context, message string, fields map[string]interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.DebugLogs = append(l.DebugLogs, LogEntry{Message: message, Fields: fields})
}

// Warn implements logger.Logger.
func (l *MockLogger) Warn(ctx context.Context, message string, fields map[string]interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.WarnLogs = append(l.WarnLogs, LogEntry{Message: message, Fields: fields})
}

// Warning implements logger.Logger (alias for Warn).
func (l *MockLogger) Warning(ctx context.Context, message string, fields map[string]interface{}) {
	l.Warn(ctx, message, fields)
}

// Error implements logger.Logger.
func (l *MockLogger) Error(ctx context.Context, message string, err error, fields map[string]interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ErrorLogs = append(l.ErrorLogs, ErrorLogEntry{Message: message, Err: err, Fields: fields})
}

// WithFields implements logger.Logger.
func (l *MockLogger) WithFields(fields map[string]interface{}) logger.Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	newFields := make(map[string]interface{})
	for k, v := range l.Fields {
		newFields[k] = v
	}
	for k, v := range fields {
		newFields[k] = v
	}
	return &MockLogger{
		Fields: newFields,
	}
}

// Reset clears all log entries.
func (l *MockLogger) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.InfoLogs = nil
	l.DebugLogs = nil
	l.WarnLogs = nil
	l.ErrorLogs = nil
}

// Ensure MockLogger implements logger.Logger.
var _ logger.Logger = (*MockLogger)(nil)
