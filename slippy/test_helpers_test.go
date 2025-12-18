package slippy

import (
	"context"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/logger"
)

// testLogger implements Logger for testing. It's a no-op logger.
type testLogger struct {
	fields map[string]interface{}
}

// newTestLogger creates a new test logger.
func newTestLogger() logger.Logger {
	return &testLogger{
		fields: make(map[string]interface{}),
	}
}

// Info logs an informational message (no-op in tests).
func (l *testLogger) Info(ctx context.Context, message string, fields map[string]interface{}) {}

// Debug logs a debug message (no-op in tests).
func (l *testLogger) Debug(ctx context.Context, message string, fields map[string]interface{}) {}

// Warn logs a warning message (no-op in tests).
func (l *testLogger) Warn(ctx context.Context, message string, fields map[string]interface{}) {}

// Warning is an alias for Warn.
func (l *testLogger) Warning(ctx context.Context, message string, fields map[string]interface{}) {}

// Error logs an error message (no-op in tests).
func (l *testLogger) Error(ctx context.Context, message string, err error, fields map[string]interface{}) {}

// WithFields returns a new Logger with the given fields.
func (l *testLogger) WithFields(fields map[string]interface{}) logger.Logger {
	newFields := make(map[string]interface{})
	for k, v := range l.fields {
		newFields[k] = v
	}
	for k, v := range fields {
		newFields[k] = v
	}
	return &testLogger{fields: newFields}
}

// Ensure testLogger implements logger.Logger.
var _ logger.Logger = (*testLogger)(nil)
