package logger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// ============================================================================
// NopLogger Tests
// ============================================================================

func TestNopLogger_ImplementsInterface(t *testing.T) {
	var _ Logger = (*NopLogger)(nil)
}

func TestNopLogger_Info(t *testing.T) {
	nop := &NopLogger{}
	// Should not panic
	nop.Info(context.Background(), "test message", map[string]interface{}{"key": "value"})
	nop.Info(context.Background(), "test message", nil)
}

func TestNopLogger_Debug(t *testing.T) {
	nop := &NopLogger{}
	// Should not panic
	nop.Debug(context.Background(), "test message", map[string]interface{}{"key": "value"})
	nop.Debug(context.Background(), "test message", nil)
}

func TestNopLogger_Warn(t *testing.T) {
	nop := &NopLogger{}
	// Should not panic
	nop.Warn(context.Background(), "test message", map[string]interface{}{"key": "value"})
	nop.Warn(context.Background(), "test message", nil)
}

func TestNopLogger_Warning(t *testing.T) {
	nop := &NopLogger{}
	// Should not panic
	nop.Warning(context.Background(), "test message", map[string]interface{}{"key": "value"})
	nop.Warning(context.Background(), "test message", nil)
}

func TestNopLogger_Error(t *testing.T) {
	nop := &NopLogger{}
	// Should not panic
	nop.Error(context.Background(), "test message", errors.New("test error"), map[string]interface{}{"key": "value"})
	nop.Error(context.Background(), "test message", nil, nil)
}

func TestNopLogger_WithFields(t *testing.T) {
	nop := &NopLogger{}
	result := nop.WithFields(map[string]interface{}{"key": "value"})

	// Should return the same NopLogger
	assert.Same(t, nop, result)
}

func TestNopLogger_Concurrent(t *testing.T) {
	nop := &NopLogger{}
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			nop.Info(ctx, fmt.Sprintf("message %d", i), map[string]interface{}{"i": i})
			nop.Debug(ctx, fmt.Sprintf("message %d", i), nil)
			nop.Warn(ctx, fmt.Sprintf("message %d", i), nil)
			nop.Error(ctx, fmt.Sprintf("message %d", i), nil, nil)
			_ = nop.WithFields(map[string]interface{}{"i": i})
		}(i)
	}
	wg.Wait()
}

// ============================================================================
// StdLogger Tests
// ============================================================================

func TestStdLogger_ImplementsInterface(t *testing.T) {
	var _ Logger = (*StdLogger)(nil)
}

func TestNewStdLogger(t *testing.T) {
	tests := []struct {
		name  string
		debug bool
	}{
		{"debug enabled", true},
		{"debug disabled", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := NewStdLogger(tt.debug)
			require.NotNil(t, logger)
			assert.Equal(t, tt.debug, logger.IsDebugEnabled())
		})
	}
}

func TestNewStdLoggerWithPrefix(t *testing.T) {
	logger := NewStdLoggerWithPrefix("[TEST] ", true)
	require.NotNil(t, logger)
	assert.True(t, logger.IsDebugEnabled())
}

func TestStdLogger_Info(t *testing.T) {
	var buf bytes.Buffer
	logger := &StdLogger{
		logger: log.New(&buf, "", 0),
		fields: make(map[string]interface{}),
		debug:  false,
	}

	ctx := context.Background()
	logger.Info(ctx, "test message", map[string]interface{}{"key": "value"})

	output := buf.String()
	assert.Contains(t, output, "[INFO]")
	assert.Contains(t, output, "test message")
	assert.Contains(t, output, "key=value")
}

func TestStdLogger_Info_NoFields(t *testing.T) {
	var buf bytes.Buffer
	logger := &StdLogger{
		logger: log.New(&buf, "", 0),
		fields: make(map[string]interface{}),
		debug:  false,
	}

	ctx := context.Background()
	logger.Info(ctx, "test message", nil)

	output := buf.String()
	assert.Contains(t, output, "[INFO]")
	assert.Contains(t, output, "test message")
}

func TestStdLogger_Debug_Enabled(t *testing.T) {
	var buf bytes.Buffer
	logger := &StdLogger{
		logger: log.New(&buf, "", 0),
		fields: make(map[string]interface{}),
		debug:  true,
	}

	ctx := context.Background()
	logger.Debug(ctx, "debug message", map[string]interface{}{"debug": true})

	output := buf.String()
	assert.Contains(t, output, "[DEBUG]")
	assert.Contains(t, output, "debug message")
}

func TestStdLogger_Debug_Disabled(t *testing.T) {
	var buf bytes.Buffer
	logger := &StdLogger{
		logger: log.New(&buf, "", 0),
		fields: make(map[string]interface{}),
		debug:  false,
	}

	ctx := context.Background()
	logger.Debug(ctx, "debug message", nil)

	output := buf.String()
	assert.Empty(t, output, "debug message should not be logged when debug is disabled")
}

func TestStdLogger_Warn(t *testing.T) {
	var buf bytes.Buffer
	logger := &StdLogger{
		logger: log.New(&buf, "", 0),
		fields: make(map[string]interface{}),
		debug:  false,
	}

	ctx := context.Background()
	logger.Warn(ctx, "warning message", map[string]interface{}{"severity": "high"})

	output := buf.String()
	assert.Contains(t, output, "[WARN]")
	assert.Contains(t, output, "warning message")
	assert.Contains(t, output, "severity=high")
}

func TestStdLogger_Warning_IsAliasForWarn(t *testing.T) {
	var buf bytes.Buffer
	logger := &StdLogger{
		logger: log.New(&buf, "", 0),
		fields: make(map[string]interface{}),
		debug:  false,
	}

	ctx := context.Background()
	logger.Warning(ctx, "warning message", nil)

	output := buf.String()
	assert.Contains(t, output, "[WARN]")
	assert.Contains(t, output, "warning message")
}

func TestStdLogger_Error(t *testing.T) {
	var buf bytes.Buffer
	logger := &StdLogger{
		logger: log.New(&buf, "", 0),
		fields: make(map[string]interface{}),
		debug:  false,
	}

	ctx := context.Background()
	testErr := errors.New("something went wrong")
	logger.Error(ctx, "error message", testErr, map[string]interface{}{"code": 500})

	output := buf.String()
	assert.Contains(t, output, "[ERROR]")
	assert.Contains(t, output, "error message")
	assert.Contains(t, output, "error=something went wrong")
	assert.Contains(t, output, "code=500")
}

func TestStdLogger_Error_NilError(t *testing.T) {
	var buf bytes.Buffer
	logger := &StdLogger{
		logger: log.New(&buf, "", 0),
		fields: make(map[string]interface{}),
		debug:  false,
	}

	ctx := context.Background()
	logger.Error(ctx, "error message", nil, nil)

	output := buf.String()
	assert.Contains(t, output, "[ERROR]")
	assert.Contains(t, output, "error message")
	assert.NotContains(t, output, "error=")
}

func TestStdLogger_Error_NilFields(t *testing.T) {
	var buf bytes.Buffer
	logger := &StdLogger{
		logger: log.New(&buf, "", 0),
		fields: make(map[string]interface{}),
		debug:  false,
	}

	ctx := context.Background()
	testErr := errors.New("test error")
	logger.Error(ctx, "error message", testErr, nil)

	output := buf.String()
	assert.Contains(t, output, "error=test error")
}

func TestStdLogger_WithFields(t *testing.T) {
	var buf bytes.Buffer
	baseLogger := &StdLogger{
		logger: log.New(&buf, "", 0),
		fields: map[string]interface{}{"base": "field"},
		debug:  true,
	}

	childLogger := baseLogger.WithFields(map[string]interface{}{"child": "field"})

	require.NotNil(t, childLogger)
	require.IsType(t, &StdLogger{}, childLogger)

	stdChild := childLogger.(*StdLogger)

	// Verify both fields are present
	assert.Equal(t, "field", stdChild.fields["base"])
	assert.Equal(t, "field", stdChild.fields["child"])

	// Verify debug setting is inherited
	assert.True(t, stdChild.debug)
}

func TestStdLogger_WithFields_OverwritesExisting(t *testing.T) {
	baseLogger := &StdLogger{
		logger: log.New(&bytes.Buffer{}, "", 0),
		fields: map[string]interface{}{"key": "original"},
		debug:  false,
	}

	childLogger := baseLogger.WithFields(map[string]interface{}{"key": "overwritten"})
	stdChild := childLogger.(*StdLogger)

	assert.Equal(t, "overwritten", stdChild.fields["key"])
}

func TestStdLogger_WithFields_DoesNotModifyParent(t *testing.T) {
	baseLogger := &StdLogger{
		logger: log.New(&bytes.Buffer{}, "", 0),
		fields: map[string]interface{}{"base": "field"},
		debug:  false,
	}

	_ = baseLogger.WithFields(map[string]interface{}{"child": "field"})

	// Parent should not have child's field
	_, exists := baseLogger.fields["child"]
	assert.False(t, exists)
}

func TestStdLogger_WithFields_LogsAllFields(t *testing.T) {
	var buf bytes.Buffer
	baseLogger := &StdLogger{
		logger: log.New(&buf, "", 0),
		fields: map[string]interface{}{"base": "value1"},
		debug:  false,
	}

	childLogger := baseLogger.WithFields(map[string]interface{}{"child": "value2"})
	childLogger.Info(context.Background(), "test", map[string]interface{}{"call": "value3"})

	output := buf.String()
	assert.Contains(t, output, "base=value1")
	assert.Contains(t, output, "child=value2")
	assert.Contains(t, output, "call=value3")
}

func TestStdLogger_SetDebug(t *testing.T) {
	logger := NewStdLogger(false)
	assert.False(t, logger.IsDebugEnabled())

	logger.SetDebug(true)
	assert.True(t, logger.IsDebugEnabled())

	logger.SetDebug(false)
	assert.False(t, logger.IsDebugEnabled())
}

// ============================================================================
// ZapLogger Tests
// ============================================================================

func TestZapLogger_ImplementsInterface(t *testing.T) {
	var _ Logger = (*ZapLogger)(nil)
}

func TestNewZapLogger(t *testing.T) {
	sugar := zap.NewNop().Sugar()
	logger := NewZapLogger(sugar)

	require.NotNil(t, logger)
	assert.Same(t, sugar, logger.Sugar())
}

func TestNewZapLoggerFromConfig(t *testing.T) {
	logger := NewZapLoggerFromConfig()
	require.NotNil(t, logger)
	require.NotNil(t, logger.Sugar())
}

// createTestZapLogger creates a ZapLogger with an observable core for testing
func createTestZapLogger() (*ZapLogger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	sugar := zap.New(core).Sugar()
	return NewZapLogger(sugar), logs
}

func TestZapLogger_Info(t *testing.T) {
	logger, logs := createTestZapLogger()
	ctx := context.Background()

	logger.Info(ctx, "test message", map[string]interface{}{"key": "value"})

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, zapcore.InfoLevel, entries[0].Level)
	assert.Equal(t, "test message", entries[0].Message)
	assert.Contains(t, entries[0].ContextMap(), "key")
}

func TestZapLogger_Info_NoFields(t *testing.T) {
	logger, logs := createTestZapLogger()
	ctx := context.Background()

	logger.Info(ctx, "test message", nil)

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, "test message", entries[0].Message)
}

func TestZapLogger_Debug(t *testing.T) {
	logger, logs := createTestZapLogger()
	ctx := context.Background()

	logger.Debug(ctx, "debug message", map[string]interface{}{"debug": true})

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, zapcore.DebugLevel, entries[0].Level)
	assert.Equal(t, "debug message", entries[0].Message)
}

func TestZapLogger_Warn(t *testing.T) {
	logger, logs := createTestZapLogger()
	ctx := context.Background()

	logger.Warn(ctx, "warning message", map[string]interface{}{"severity": "high"})

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, zapcore.WarnLevel, entries[0].Level)
	assert.Equal(t, "warning message", entries[0].Message)
}

func TestZapLogger_Warning_IsAliasForWarn(t *testing.T) {
	logger, logs := createTestZapLogger()
	ctx := context.Background()

	logger.Warning(ctx, "warning message", nil)

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, zapcore.WarnLevel, entries[0].Level)
}

func TestZapLogger_Error(t *testing.T) {
	logger, logs := createTestZapLogger()
	ctx := context.Background()

	testErr := errors.New("something went wrong")
	logger.Error(ctx, "error message", testErr, map[string]interface{}{"code": 500})

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, zapcore.ErrorLevel, entries[0].Level)
	assert.Equal(t, "error message", entries[0].Message)

	contextMap := entries[0].ContextMap()
	assert.Equal(t, "something went wrong", contextMap["error"])
	assert.EqualValues(t, 500, contextMap["code"])
}

func TestZapLogger_Error_NilError(t *testing.T) {
	logger, logs := createTestZapLogger()
	ctx := context.Background()

	logger.Error(ctx, "error message", nil, nil)

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.NotContains(t, entries[0].ContextMap(), "error")
}

func TestZapLogger_WithFields(t *testing.T) {
	logger, logs := createTestZapLogger()
	ctx := context.Background()

	childLogger := logger.WithFields(map[string]interface{}{"base": "value"})
	childLogger.Info(ctx, "test", nil)

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, "value", entries[0].ContextMap()["base"])
}

func TestZapLogger_WithFields_Chained(t *testing.T) {
	logger, logs := createTestZapLogger()
	ctx := context.Background()

	childLogger := logger.WithFields(map[string]interface{}{"field1": "value1"})
	grandchildLogger := childLogger.WithFields(map[string]interface{}{"field2": "value2"})
	grandchildLogger.Info(ctx, "test", map[string]interface{}{"field3": "value3"})

	entries := logs.All()
	require.Len(t, entries, 1)

	contextMap := entries[0].ContextMap()
	assert.Equal(t, "value1", contextMap["field1"])
	assert.Equal(t, "value2", contextMap["field2"])
	assert.Equal(t, "value3", contextMap["field3"])
}

func TestZapLogger_WithFields_DoesNotModifyParent(t *testing.T) {
	logger, logs := createTestZapLogger()
	ctx := context.Background()

	_ = logger.WithFields(map[string]interface{}{"child": "value"})
	logger.Info(ctx, "parent log", nil)

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.NotContains(t, entries[0].ContextMap(), "child")
}

func TestZapLogger_Sync(t *testing.T) {
	logger := NewZapLogger(zap.NewNop().Sugar())
	err := logger.Sync()
	assert.NoError(t, err)
}

// ============================================================================
// LogAdapter Tests
// ============================================================================

// mockSimpleLogger captures log calls for testing
type mockSimpleLogger struct {
	infoCalls   []string
	infofCalls  []string
	debugCalls  []string
	debugfCalls []string
	warnCalls   []string
	warnfCalls  []string
	errorCalls  []string
	errorfCalls []string
	mu          sync.Mutex
}

func (m *mockSimpleLogger) Info(args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.infoCalls = append(m.infoCalls, fmt.Sprint(args...))
}

func (m *mockSimpleLogger) Infof(format string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.infofCalls = append(m.infofCalls, fmt.Sprintf(format, args...))
}

func (m *mockSimpleLogger) Debug(args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.debugCalls = append(m.debugCalls, fmt.Sprint(args...))
}

func (m *mockSimpleLogger) Debugf(format string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.debugfCalls = append(m.debugfCalls, fmt.Sprintf(format, args...))
}

func (m *mockSimpleLogger) Warn(args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.warnCalls = append(m.warnCalls, fmt.Sprint(args...))
}

func (m *mockSimpleLogger) Warnf(format string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.warnfCalls = append(m.warnfCalls, fmt.Sprintf(format, args...))
}

func (m *mockSimpleLogger) Error(args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errorCalls = append(m.errorCalls, fmt.Sprint(args...))
}

func (m *mockSimpleLogger) Errorf(format string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errorfCalls = append(m.errorfCalls, fmt.Sprintf(format, args...))
}

func TestLogAdapter_ImplementsInterface(t *testing.T) {
	var _ Logger = (*LogAdapter)(nil)
}

func TestNewLogAdapter(t *testing.T) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)

	require.NotNil(t, adapter)
	assert.Same(t, mock, adapter.simple)
	assert.NotNil(t, adapter.fields)
}

func TestLogAdapter_Info_WithFields(t *testing.T) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)
	ctx := context.Background()

	adapter.Info(ctx, "test message", map[string]interface{}{"key": "value"})

	require.Len(t, mock.infofCalls, 1)
	assert.Contains(t, mock.infofCalls[0], "test message")
	assert.Contains(t, mock.infofCalls[0], "key")
}

func TestLogAdapter_Info_NoFields(t *testing.T) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)
	ctx := context.Background()

	adapter.Info(ctx, "test message", nil)

	require.Len(t, mock.infoCalls, 1)
	assert.Equal(t, "test message", mock.infoCalls[0])
}

func TestLogAdapter_Debug_WithFields(t *testing.T) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)
	ctx := context.Background()

	adapter.Debug(ctx, "debug message", map[string]interface{}{"debug": true})

	require.Len(t, mock.debugfCalls, 1)
	assert.Contains(t, mock.debugfCalls[0], "debug message")
}

func TestLogAdapter_Debug_NoFields(t *testing.T) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)
	ctx := context.Background()

	adapter.Debug(ctx, "debug message", nil)

	require.Len(t, mock.debugCalls, 1)
	assert.Equal(t, "debug message", mock.debugCalls[0])
}

func TestLogAdapter_Warn_WithFields(t *testing.T) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)
	ctx := context.Background()

	adapter.Warn(ctx, "warning message", map[string]interface{}{"severity": "high"})

	require.Len(t, mock.warnfCalls, 1)
	assert.Contains(t, mock.warnfCalls[0], "warning message")
}

func TestLogAdapter_Warn_NoFields(t *testing.T) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)
	ctx := context.Background()

	adapter.Warn(ctx, "warning message", nil)

	require.Len(t, mock.warnCalls, 1)
	assert.Equal(t, "warning message", mock.warnCalls[0])
}

func TestLogAdapter_Warning_IsAliasForWarn(t *testing.T) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)
	ctx := context.Background()

	adapter.Warning(ctx, "warning message", nil)

	require.Len(t, mock.warnCalls, 1)
	assert.Equal(t, "warning message", mock.warnCalls[0])
}

func TestLogAdapter_Error_WithErrorAndFields(t *testing.T) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)
	ctx := context.Background()

	testErr := errors.New("test error")
	adapter.Error(ctx, "error message", testErr, map[string]interface{}{"code": 500})

	require.Len(t, mock.errorfCalls, 1)
	assert.Contains(t, mock.errorfCalls[0], "error message")
	assert.Contains(t, mock.errorfCalls[0], "error")
	assert.Contains(t, mock.errorfCalls[0], "test error")
}

func TestLogAdapter_Error_NilError(t *testing.T) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)
	ctx := context.Background()

	adapter.Error(ctx, "error message", nil, nil)

	require.Len(t, mock.errorCalls, 1)
	assert.Equal(t, "error message", mock.errorCalls[0])
}

func TestLogAdapter_Error_OnlyError(t *testing.T) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)
	ctx := context.Background()

	testErr := errors.New("test error")
	adapter.Error(ctx, "error message", testErr, nil)

	require.Len(t, mock.errorfCalls, 1)
	assert.Contains(t, mock.errorfCalls[0], "test error")
}

func TestLogAdapter_WithFields(t *testing.T) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)

	childAdapter := adapter.WithFields(map[string]interface{}{"base": "value"})

	require.NotNil(t, childAdapter)
	require.IsType(t, &LogAdapter{}, childAdapter)

	logAdapter := childAdapter.(*LogAdapter)
	assert.Equal(t, "value", logAdapter.fields["base"])
}

func TestLogAdapter_WithFields_Chained(t *testing.T) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)
	ctx := context.Background()

	childAdapter := adapter.WithFields(map[string]interface{}{"field1": "value1"})
	grandchildAdapter := childAdapter.WithFields(map[string]interface{}{"field2": "value2"})
	grandchildAdapter.Info(ctx, "test", nil)

	require.Len(t, mock.infofCalls, 1)
	assert.Contains(t, mock.infofCalls[0], "field1")
	assert.Contains(t, mock.infofCalls[0], "field2")
}

func TestLogAdapter_WithFields_LogsBaseFields(t *testing.T) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)
	ctx := context.Background()

	childAdapter := adapter.WithFields(map[string]interface{}{"base": "value"})
	childAdapter.Info(ctx, "test", map[string]interface{}{"call": "param"})

	require.Len(t, mock.infofCalls, 1)
	assert.Contains(t, mock.infofCalls[0], "base")
	assert.Contains(t, mock.infofCalls[0], "call")
}

func TestLogAdapter_mergeFields_BothEmpty(t *testing.T) {
	adapter := NewLogAdapter(&mockSimpleLogger{})

	result := adapter.mergeFields(nil)
	assert.Nil(t, result)
}

func TestLogAdapter_mergeFields_OnlyBase(t *testing.T) {
	adapter := &LogAdapter{
		simple: &mockSimpleLogger{},
		fields: map[string]interface{}{"base": "value"},
	}

	result := adapter.mergeFields(nil)
	assert.Equal(t, "value", result["base"])
}

func TestLogAdapter_mergeFields_OnlyProvided(t *testing.T) {
	adapter := NewLogAdapter(&mockSimpleLogger{})

	result := adapter.mergeFields(map[string]interface{}{"provided": "value"})
	assert.Equal(t, "value", result["provided"])
}

func TestLogAdapter_mergeFields_Both(t *testing.T) {
	adapter := &LogAdapter{
		simple: &mockSimpleLogger{},
		fields: map[string]interface{}{"base": "base_value"},
	}

	result := adapter.mergeFields(map[string]interface{}{"provided": "provided_value"})
	assert.Equal(t, "base_value", result["base"])
	assert.Equal(t, "provided_value", result["provided"])
}

func TestLogAdapter_mergeFields_OverwritesBase(t *testing.T) {
	adapter := &LogAdapter{
		simple: &mockSimpleLogger{},
		fields: map[string]interface{}{"key": "original"},
	}

	result := adapter.mergeFields(map[string]interface{}{"key": "overwritten"})
	assert.Equal(t, "overwritten", result["key"])
}

// ============================================================================
// Interface Compliance Tests
// ============================================================================

func TestLogger_InterfaceCompliance(t *testing.T) {
	// Create all logger implementations and verify they can be used interchangeably
	loggers := []Logger{
		&NopLogger{},
		NewStdLogger(true),
		NewZapLogger(zap.NewNop().Sugar()),
		NewLogAdapter(&mockSimpleLogger{}),
	}

	ctx := context.Background()
	testErr := errors.New("test error")
	fields := map[string]interface{}{"key": "value"}

	for i, logger := range loggers {
		t.Run(fmt.Sprintf("logger_%d", i), func(t *testing.T) {
			// All methods should work without panic
			logger.Info(ctx, "info", fields)
			logger.Debug(ctx, "debug", fields)
			logger.Warn(ctx, "warn", fields)
			logger.Warning(ctx, "warning", fields)
			logger.Error(ctx, "error", testErr, fields)

			child := logger.WithFields(fields)
			assert.NotNil(t, child)
			child.Info(ctx, "child info", nil)
		})
	}
}

// ============================================================================
// Concurrent Safety Tests
// ============================================================================

func TestStdLogger_Concurrent(t *testing.T) {
	var buf bytes.Buffer
	logger := &StdLogger{
		logger: log.New(&buf, "", 0),
		fields: make(map[string]interface{}),
		debug:  true,
	}
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			logger.Info(ctx, fmt.Sprintf("info %d", i), map[string]interface{}{"i": i})
			logger.Debug(ctx, fmt.Sprintf("debug %d", i), nil)
			logger.Warn(ctx, fmt.Sprintf("warn %d", i), nil)
			logger.Error(ctx, fmt.Sprintf("error %d", i), nil, nil)
		}(i)
	}
	wg.Wait()

	// All messages should be logged (400 total)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Len(t, lines, 400)
}

func TestZapLogger_Concurrent(t *testing.T) {
	logger, logs := createTestZapLogger()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			logger.Info(ctx, fmt.Sprintf("info %d", i), map[string]interface{}{"i": i})
			logger.Debug(ctx, fmt.Sprintf("debug %d", i), nil)
			logger.Warn(ctx, fmt.Sprintf("warn %d", i), nil)
			logger.Error(ctx, fmt.Sprintf("error %d", i), nil, nil)
		}(i)
	}
	wg.Wait()

	// All messages should be logged (400 total)
	assert.Len(t, logs.All(), 400)
}

func TestLogAdapter_Concurrent(t *testing.T) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			adapter.Info(ctx, fmt.Sprintf("info %d", i), map[string]interface{}{"i": i})
			adapter.Debug(ctx, fmt.Sprintf("debug %d", i), nil)
			adapter.Warn(ctx, fmt.Sprintf("warn %d", i), nil)
			adapter.Error(ctx, fmt.Sprintf("error %d", i), nil, nil)
		}(i)
	}
	wg.Wait()

	// Check that all calls were made (100 of each type, but some use format versions)
	totalCalls := len(mock.infoCalls) + len(mock.infofCalls) +
		len(mock.debugCalls) + len(mock.debugfCalls) +
		len(mock.warnCalls) + len(mock.warnfCalls) +
		len(mock.errorCalls) + len(mock.errorfCalls)
	assert.Equal(t, 400, totalCalls)
}

// ============================================================================
// Edge Cases Tests
// ============================================================================

func TestLogger_NilFields(t *testing.T) {
	loggers := []Logger{
		&NopLogger{},
		NewStdLogger(true),
		NewZapLogger(zap.NewNop().Sugar()),
		NewLogAdapter(&mockSimpleLogger{}),
	}

	ctx := context.Background()

	for _, logger := range loggers {
		// Should not panic with nil fields
		logger.Info(ctx, "test", nil)
		logger.Debug(ctx, "test", nil)
		logger.Warn(ctx, "test", nil)
		logger.Warning(ctx, "test", nil)
		logger.Error(ctx, "test", nil, nil)
		logger.WithFields(nil)
	}
}

func TestLogger_EmptyMessage(t *testing.T) {
	loggers := []Logger{
		&NopLogger{},
		NewStdLogger(true),
		NewZapLogger(zap.NewNop().Sugar()),
		NewLogAdapter(&mockSimpleLogger{}),
	}

	ctx := context.Background()

	for _, logger := range loggers {
		// Should not panic with empty message
		logger.Info(ctx, "", nil)
		logger.Debug(ctx, "", nil)
		logger.Warn(ctx, "", nil)
		logger.Warning(ctx, "", nil)
		logger.Error(ctx, "", nil, nil)
	}
}

func TestLogger_SpecialCharactersInFields(t *testing.T) {
	var buf bytes.Buffer
	logger := &StdLogger{
		logger: log.New(&buf, "", 0),
		fields: make(map[string]interface{}),
		debug:  false,
	}

	ctx := context.Background()
	specialFields := map[string]interface{}{
		"with spaces":   "value with spaces",
		"with=equals":   "value=with=equals",
		"with\nnewline": "value\nwith\nnewline",
		"unicode":       "日本語",
	}

	logger.Info(ctx, "test message", specialFields)

	output := buf.String()
	assert.Contains(t, output, "[INFO]")
	assert.Contains(t, output, "test message")
}

func TestLogger_LargeFields(t *testing.T) {
	logger := NewStdLogger(false)
	ctx := context.Background()

	largeFields := make(map[string]interface{})
	for i := 0; i < 1000; i++ {
		largeFields[fmt.Sprintf("field_%d", i)] = fmt.Sprintf("value_%d", i)
	}

	// Should not panic with large number of fields
	logger.Info(ctx, "test message", largeFields)
}

func TestLogger_NestedFieldValues(t *testing.T) {
	logger, logs := createTestZapLogger()
	ctx := context.Background()

	nestedFields := map[string]interface{}{
		"nested": map[string]interface{}{
			"inner": "value",
		},
		"slice": []string{"a", "b", "c"},
	}

	logger.Info(ctx, "test message", nestedFields)

	entries := logs.All()
	require.Len(t, entries, 1)
}

// ============================================================================
// Benchmark Tests
// ============================================================================

func BenchmarkNopLogger_Info(b *testing.B) {
	logger := &NopLogger{}
	ctx := context.Background()
	fields := map[string]interface{}{"key": "value"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info(ctx, "test message", fields)
	}
}

func BenchmarkStdLogger_Info(b *testing.B) {
	logger := &StdLogger{
		logger: log.New(&bytes.Buffer{}, "", 0),
		fields: make(map[string]interface{}),
		debug:  false,
	}
	ctx := context.Background()
	fields := map[string]interface{}{"key": "value"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info(ctx, "test message", fields)
	}
}

func BenchmarkZapLogger_Info(b *testing.B) {
	logger := NewZapLogger(zap.NewNop().Sugar())
	ctx := context.Background()
	fields := map[string]interface{}{"key": "value"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info(ctx, "test message", fields)
	}
}

func BenchmarkLogAdapter_Info(b *testing.B) {
	mock := &mockSimpleLogger{}
	adapter := NewLogAdapter(mock)
	ctx := context.Background()
	fields := map[string]interface{}{"key": "value"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		adapter.Info(ctx, "test message", fields)
	}
}

func BenchmarkStdLogger_WithFields(b *testing.B) {
	logger := NewStdLogger(false)
	fields := map[string]interface{}{"key": "value"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = logger.WithFields(fields)
	}
}

func BenchmarkZapLogger_WithFields(b *testing.B) {
	logger := NewZapLogger(zap.NewNop().Sugar())
	fields := map[string]interface{}{"key": "value"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = logger.WithFields(fields)
	}
}
