// Package clickhousetest provides test fixtures for the clickhouse package.
// It exports mock implementations that can be used by consumers to test
// code that depends on clickhouse.ClickhouseSessionInterface.
package clickhousetest

import (
	"context"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	ch "github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse"
)

// ConnectCall records arguments passed to Connect
type ConnectCall struct {
	Config *ch.ClickhouseConfig
	Ctx    context.Context
}

// QueryCall records arguments passed to Query or QueryWithArgs
type QueryCall struct {
	Ctx   context.Context
	Query string
	Args  []any
}

// ExecCall records arguments passed to Exec or ExecWithArgs
type ExecCall struct {
	Ctx  context.Context
	Stmt string
	Args []any
}

// QueryRowCall records arguments passed to QueryRow
type QueryRowCall struct {
	Ctx   context.Context
	Query string
	Args  []any
}

// MockSession implements clickhouse.ClickhouseSessionInterface for testing.
// It records all method calls and returns configurable responses.
type MockSession struct {
	// Connect behavior
	ConnectFunc  func(ctx context.Context) error
	ConnectCalls []ConnectCall
	ConnectErr   error

	// Query behavior
	QueryFunc  func(ctx context.Context, query string) (driver.Rows, error)
	QueryCalls []QueryCall
	QueryRows  driver.Rows
	QueryErr   error

	// QueryWithArgs behavior
	QueryWithArgsFunc  func(ctx context.Context, query string, args ...any) (driver.Rows, error)
	QueryWithArgsCalls []QueryCall
	QueryWithArgsRows  driver.Rows
	QueryWithArgsErr   error

	// QueryRow behavior
	QueryRowFunc  func(ctx context.Context, query string, args ...any) driver.Row
	QueryRowCalls []QueryRowCall
	QueryRowRow   driver.Row

	// Exec behavior
	ExecFunc  func(ctx context.Context, stmt string) error
	ExecCalls []ExecCall
	ExecErr   error

	// ExecWithArgs behavior
	ExecWithArgsFunc  func(ctx context.Context, stmt string, args ...any) error
	ExecWithArgsCalls []ExecCall
	ExecWithArgsErr   error

	// Close behavior
	CloseFunc  func() error
	CloseCalls int
	CloseErr   error

	// Conn behavior
	ConnFunc  func() driver.Conn
	ConnCalls int
	ConnConn  driver.Conn
}

// Connect implements ClickhouseSessionInterface.Connect
func (m *MockSession) Connect(cfg *ch.ClickhouseConfig, ctx context.Context) error {
	m.ConnectCalls = append(m.ConnectCalls, ConnectCall{Config: cfg, Ctx: ctx})
	if m.ConnectFunc != nil {
		return m.ConnectFunc(ctx)
	}
	return m.ConnectErr
}

// Query implements ClickhouseSessionInterface.Query
func (m *MockSession) Query(ctx context.Context, query string) (driver.Rows, error) {
	m.QueryCalls = append(m.QueryCalls, QueryCall{Ctx: ctx, Query: query})
	if m.QueryFunc != nil {
		return m.QueryFunc(ctx, query)
	}
	return m.QueryRows, m.QueryErr
}

// QueryWithArgs implements ClickhouseSessionInterface.QueryWithArgs
func (m *MockSession) QueryWithArgs(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	m.QueryWithArgsCalls = append(m.QueryWithArgsCalls, QueryCall{Ctx: ctx, Query: query, Args: args})
	if m.QueryWithArgsFunc != nil {
		return m.QueryWithArgsFunc(ctx, query, args...)
	}
	return m.QueryWithArgsRows, m.QueryWithArgsErr
}

// QueryRow implements ClickhouseSessionInterface.QueryRow
func (m *MockSession) QueryRow(ctx context.Context, query string, args ...any) driver.Row {
	m.QueryRowCalls = append(m.QueryRowCalls, QueryRowCall{Ctx: ctx, Query: query, Args: args})
	if m.QueryRowFunc != nil {
		return m.QueryRowFunc(ctx, query, args...)
	}
	return m.QueryRowRow
}

// Exec implements ClickhouseSessionInterface.Exec
func (m *MockSession) Exec(ctx context.Context, stmt string) error {
	m.ExecCalls = append(m.ExecCalls, ExecCall{Ctx: ctx, Stmt: stmt})
	if m.ExecFunc != nil {
		return m.ExecFunc(ctx, stmt)
	}
	return m.ExecErr
}

// ExecWithArgs implements ClickhouseSessionInterface.ExecWithArgs
func (m *MockSession) ExecWithArgs(ctx context.Context, stmt string, args ...any) error {
	m.ExecWithArgsCalls = append(m.ExecWithArgsCalls, ExecCall{Ctx: ctx, Stmt: stmt, Args: args})
	if m.ExecWithArgsFunc != nil {
		return m.ExecWithArgsFunc(ctx, stmt, args...)
	}
	return m.ExecWithArgsErr
}

// Close implements ClickhouseSessionInterface.Close
func (m *MockSession) Close() error {
	m.CloseCalls++
	if m.CloseFunc != nil {
		return m.CloseFunc()
	}
	return m.CloseErr
}

// Conn implements ClickhouseSessionInterface.Conn
func (m *MockSession) Conn() driver.Conn {
	m.ConnCalls++
	if m.ConnFunc != nil {
		return m.ConnFunc()
	}
	return m.ConnConn
}

// Reset clears all recorded calls for reuse
func (m *MockSession) Reset() {
	m.ConnectCalls = nil
	m.QueryCalls = nil
	m.QueryWithArgsCalls = nil
	m.QueryRowCalls = nil
	m.ExecCalls = nil
	m.ExecWithArgsCalls = nil
	m.CloseCalls = 0
	m.ConnCalls = 0
}

// MockRows implements driver.Rows for testing
type MockRows struct {
	// Columns behavior
	ColumnsData []string

	// Column types behavior
	ColumnTypesData []driver.ColumnType

	// Next behavior
	NextFunc func() bool
	NextData []bool
	nextIdx  int

	// Scan behavior
	ScanFunc func(dest ...any) error
	ScanErr  error
	ScanData [][]any
	scanIdx  int

	// Close behavior
	CloseErr error

	// Err behavior
	ErrErr error

	// Totals behavior
	TotalsFunc func(dest ...any) error
	TotalsErr  error
}

// Columns implements driver.Rows.Columns
func (m *MockRows) Columns() []string {
	return m.ColumnsData
}

// ColumnTypes implements driver.Rows.ColumnTypes
func (m *MockRows) ColumnTypes() []driver.ColumnType {
	return m.ColumnTypesData
}

// Next implements driver.Rows.Next
func (m *MockRows) Next() bool {
	if m.NextFunc != nil {
		return m.NextFunc()
	}
	if m.nextIdx < len(m.NextData) {
		result := m.NextData[m.nextIdx]
		m.nextIdx++
		return result
	}
	return false
}

// Scan implements driver.Rows.Scan
func (m *MockRows) Scan(dest ...any) error {
	if m.ScanFunc != nil {
		return m.ScanFunc(dest...)
	}
	if m.ScanErr != nil {
		return m.ScanErr
	}
	// If we have scan data, copy it to dest
	if m.scanIdx < len(m.ScanData) {
		data := m.ScanData[m.scanIdx]
		for i := 0; i < len(dest) && i < len(data); i++ {
			copyValue(dest[i], data[i])
		}
		m.scanIdx++
	}
	return nil
}

// Close implements driver.Rows.Close
func (m *MockRows) Close() error {
	return m.CloseErr
}

// Err implements driver.Rows.Err
func (m *MockRows) Err() error {
	return m.ErrErr
}

// Totals implements driver.Rows.Totals
func (m *MockRows) Totals(dest ...any) error {
	if m.TotalsFunc != nil {
		return m.TotalsFunc(dest...)
	}
	return m.TotalsErr
}

// ScanStruct implements driver.Rows.ScanStruct
func (m *MockRows) ScanStruct(dest any) error {
	return nil
}

// Reset resets the row iterator state
func (m *MockRows) Reset() {
	m.nextIdx = 0
	m.scanIdx = 0
}

// MockRow implements driver.Row for testing
type MockRow struct {
	// Scan behavior
	ScanFunc func(dest ...any) error
	ScanErr  error
	ScanData []any

	// ScanStruct behavior
	ScanStructFunc func(dest any) error
	ScanStructErr  error
}

// Scan implements driver.Row.Scan
func (m *MockRow) Scan(dest ...any) error {
	if m.ScanFunc != nil {
		return m.ScanFunc(dest...)
	}
	if m.ScanErr != nil {
		return m.ScanErr
	}
	// Copy scan data to dest
	for i := 0; i < len(dest) && i < len(m.ScanData); i++ {
		copyValue(dest[i], m.ScanData[i])
	}
	return nil
}

// ScanStruct implements driver.Row.ScanStruct
func (m *MockRow) ScanStruct(dest any) error {
	if m.ScanStructFunc != nil {
		return m.ScanStructFunc(dest)
	}
	return m.ScanStructErr
}

// Err implements driver.Row.Err
func (m *MockRow) Err() error {
	return m.ScanErr
}

// MockConn implements driver.Conn for testing
type MockConn struct {
	// Query behavior
	QueryFunc func(ctx context.Context, query string, args ...any) (driver.Rows, error)
	QueryRows driver.Rows
	QueryErr  error

	// QueryRow behavior
	QueryRowFunc func(ctx context.Context, query string, args ...any) driver.Row
	QueryRowRow  driver.Row

	// Exec behavior
	ExecFunc func(ctx context.Context, query string, args ...any) error
	ExecErr  error

	// PrepareBatch behavior
	PrepareBatchFunc  func(ctx context.Context, query string, opts ...driver.PrepareBatchOption) (driver.Batch, error)
	PrepareBatchBatch driver.Batch
	PrepareBatchErr   error

	// AsyncInsert behavior
	AsyncInsertFunc func(ctx context.Context, query string, wait bool, args ...any) error
	AsyncInsertErr  error

	// Ping behavior
	PingFunc func(ctx context.Context) error
	PingErr  error

	// Stats behavior
	StatsData driver.Stats

	// Close behavior
	CloseErr error

	// Contributors behavior
	ContributorsFunc func() []string
	ContributorsData []string

	// ServerVersion behavior
	ServerVersionFunc func() (*driver.ServerVersion, error)
	ServerVersionData *driver.ServerVersion
	ServerVersionErr  error

	// Select behavior
	SelectFunc func(ctx context.Context, dest any, query string, args ...any) error
	SelectErr  error
}

// Query implements driver.Conn.Query
func (m *MockConn) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	if m.QueryFunc != nil {
		return m.QueryFunc(ctx, query, args...)
	}
	return m.QueryRows, m.QueryErr
}

// QueryRow implements driver.Conn.QueryRow
func (m *MockConn) QueryRow(ctx context.Context, query string, args ...any) driver.Row {
	if m.QueryRowFunc != nil {
		return m.QueryRowFunc(ctx, query, args...)
	}
	return m.QueryRowRow
}

// Exec implements driver.Conn.Exec
func (m *MockConn) Exec(ctx context.Context, query string, args ...any) error {
	if m.ExecFunc != nil {
		return m.ExecFunc(ctx, query, args...)
	}
	return m.ExecErr
}

// PrepareBatch implements driver.Conn.PrepareBatch
func (m *MockConn) PrepareBatch(
	ctx context.Context,
	query string,
	opts ...driver.PrepareBatchOption,
) (driver.Batch, error) {
	if m.PrepareBatchFunc != nil {
		return m.PrepareBatchFunc(ctx, query, opts...)
	}
	return m.PrepareBatchBatch, m.PrepareBatchErr
}

// AsyncInsert implements driver.Conn.AsyncInsert
func (m *MockConn) AsyncInsert(ctx context.Context, query string, wait bool, args ...any) error {
	if m.AsyncInsertFunc != nil {
		return m.AsyncInsertFunc(ctx, query, wait, args...)
	}
	return m.AsyncInsertErr
}

// Ping implements driver.Conn.Ping
func (m *MockConn) Ping(ctx context.Context) error {
	if m.PingFunc != nil {
		return m.PingFunc(ctx)
	}
	return m.PingErr
}

// Stats implements driver.Conn.Stats
func (m *MockConn) Stats() driver.Stats {
	return m.StatsData
}

// Close implements driver.Conn.Close
func (m *MockConn) Close() error {
	return m.CloseErr
}

// Contributors implements driver.Conn.Contributors
func (m *MockConn) Contributors() []string {
	if m.ContributorsFunc != nil {
		return m.ContributorsFunc()
	}
	return m.ContributorsData
}

// ServerVersion implements driver.Conn.ServerVersion
func (m *MockConn) ServerVersion() (*driver.ServerVersion, error) {
	if m.ServerVersionFunc != nil {
		return m.ServerVersionFunc()
	}
	return m.ServerVersionData, m.ServerVersionErr
}

// Select implements driver.Conn.Select
func (m *MockConn) Select(ctx context.Context, dest any, query string, args ...any) error {
	if m.SelectFunc != nil {
		return m.SelectFunc(ctx, dest, query, args...)
	}
	return m.SelectErr
}

// MockBatch implements driver.Batch for testing
type MockBatch struct {
	// Append behavior
	AppendFunc  func(v ...any) error
	AppendCalls [][]any
	AppendErr   error

	// AppendStruct behavior
	AppendStructFunc  func(v any) error
	AppendStructCalls []any
	AppendStructErr   error

	// Column behavior
	ColumnFunc func(idx int) driver.BatchColumn
	ColumnData map[int]driver.BatchColumn

	// Flush behavior
	FlushFunc  func() error
	FlushCalls int
	FlushErr   error

	// Send behavior
	SendFunc  func() error
	SendCalls int
	SendErr   error

	// Abort behavior
	AbortFunc  func() error
	AbortCalls int
	AbortErr   error

	// IsSent behavior
	IsSentData bool

	// Rows behavior
	RowsData int
}

// Append implements driver.Batch.Append
func (m *MockBatch) Append(v ...any) error {
	m.AppendCalls = append(m.AppendCalls, v)
	if m.AppendFunc != nil {
		return m.AppendFunc(v...)
	}
	return m.AppendErr
}

// AppendStruct implements driver.Batch.AppendStruct
func (m *MockBatch) AppendStruct(v any) error {
	m.AppendStructCalls = append(m.AppendStructCalls, v)
	if m.AppendStructFunc != nil {
		return m.AppendStructFunc(v)
	}
	return m.AppendStructErr
}

// Column implements driver.Batch.Column
func (m *MockBatch) Column(idx int) driver.BatchColumn {
	if m.ColumnFunc != nil {
		return m.ColumnFunc(idx)
	}
	if m.ColumnData != nil {
		return m.ColumnData[idx]
	}
	return nil
}

// Flush implements driver.Batch.Flush
func (m *MockBatch) Flush() error {
	m.FlushCalls++
	if m.FlushFunc != nil {
		return m.FlushFunc()
	}
	return m.FlushErr
}

// Send implements driver.Batch.Send
func (m *MockBatch) Send() error {
	m.SendCalls++
	if m.SendFunc != nil {
		return m.SendFunc()
	}
	return m.SendErr
}

// Abort implements driver.Batch.Abort
func (m *MockBatch) Abort() error {
	m.AbortCalls++
	if m.AbortFunc != nil {
		return m.AbortFunc()
	}
	return m.AbortErr
}

// IsSent implements driver.Batch.IsSent
func (m *MockBatch) IsSent() bool {
	return m.IsSentData
}

// Rows implements driver.Batch.Rows
func (m *MockBatch) Rows() int {
	return m.RowsData
}

// Close implements driver.Batch.Close
func (m *MockBatch) Close() error {
	return nil
}

// Columns implements driver.Batch.Columns
func (m *MockBatch) Columns() []driver.BatchColumn {
	return nil
}

// copyValue is a helper to copy values for Scan operations.
// It handles common types used in ClickHouse queries.
func copyValue(dest, src any) {
	if src == nil {
		return
	}

	// Try to copy using type-specific helpers
	if copyIntTypes(dest, src) {
		return
	}
	if copyUintTypes(dest, src) {
		return
	}
	if copyOtherTypes(dest, src) {
		return
	}
}

// copyIntTypes handles signed integer types.
func copyIntTypes(dest, src any) bool {
	switch d := dest.(type) {
	case *int:
		if i, ok := src.(int); ok {
			*d = i
			return true
		}
	case *int8:
		if i, ok := src.(int8); ok {
			*d = i
			return true
		}
	case *int16:
		if i, ok := src.(int16); ok {
			*d = i
			return true
		}
	case *int32:
		if i, ok := src.(int32); ok {
			*d = i
			return true
		}
	case *int64:
		if i, ok := src.(int64); ok {
			*d = i
			return true
		}
	}
	return false
}

// copyUintTypes handles unsigned integer types.
func copyUintTypes(dest, src any) bool {
	switch d := dest.(type) {
	case *uint:
		if i, ok := src.(uint); ok {
			*d = i
			return true
		}
	case *uint8:
		if i, ok := src.(uint8); ok {
			*d = i
			return true
		}
	case *uint16:
		if i, ok := src.(uint16); ok {
			*d = i
			return true
		}
	case *uint32:
		if i, ok := src.(uint32); ok {
			*d = i
			return true
		}
	case *uint64:
		if i, ok := src.(uint64); ok {
			*d = i
			return true
		}
	}
	return false
}

// copyOtherTypes handles string, float, bool, and other types.
func copyOtherTypes(dest, src any) bool {
	switch d := dest.(type) {
	case *string:
		if s, ok := src.(string); ok {
			*d = s
			return true
		}
	case *float32:
		if f, ok := src.(float32); ok {
			*d = f
			return true
		}
	case *float64:
		if f, ok := src.(float64); ok {
			*d = f
			return true
		}
	case *bool:
		if b, ok := src.(bool); ok {
			*d = b
			return true
		}
	case *[]byte:
		if b, ok := src.([]byte); ok {
			*d = b
			return true
		}
	case *any:
		*d = src
		return true
	}
	return false
}

// Compile-time interface assertions
var _ ch.ClickhouseSessionInterface = (*MockSession)(nil)
var _ driver.Rows = (*MockRows)(nil)
var _ driver.Row = (*MockRow)(nil)
var _ driver.Conn = (*MockConn)(nil)

// Note: MockBatch doesn't fully implement driver.Batch because Columns() requires
// an internal column.Interface type. Use MockBatch for basic batch testing only.
