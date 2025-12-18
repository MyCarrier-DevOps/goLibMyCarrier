// Package loggertest provides test fixtures and mocks for testing code that uses the logger package.
// This follows the Go standard library pattern (e.g., net/http/httptest).
//
// Example usage:
//
//	func TestMyFunction(t *testing.T) {
//	    log := loggertest.NewMockLogger()
//
//	    // Run your test with the mock logger
//	    myFunction(ctx, log)
//
//	    // Verify logging behavior
//	    if !log.HasLog("info", "Processing started") {
//	        t.Error("expected 'Processing started' info log")
//	    }
//
//	    if log.HasError("failed") {
//	        t.Error("unexpected error logged")
//	    }
//	}
package loggertest

import (
	"context"
	"sync"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/logger"
)

// LogEntry represents a single log entry captured by MockLogger.
type LogEntry struct {
	Context context.Context
	Message string
	Fields  map[string]interface{}
}

// ErrorLogEntry represents an error log entry captured by MockLogger.
type ErrorLogEntry struct {
	Context context.Context
	Message string
	Error   error
	Fields  map[string]interface{}
}

// MockLogger is a mock implementation of logger.Logger for testing.
// It captures all log calls for later verification.
//
// Features:
//   - Thread-safe log capture
//   - Separate tracking for each log level
//   - Helper methods for common assertions
//   - Field capture for structured logging
//   - Context capture for tracing validation
type MockLogger struct {
	mu sync.RWMutex

	// Captured logs by level
	InfoLogs  []LogEntry
	DebugLogs []LogEntry
	WarnLogs  []LogEntry
	ErrorLogs []ErrorLogEntry

	// Fields set via WithFields
	currentFields map[string]interface{}
}

// NewMockLogger creates a new MockLogger with initialized slices.
func NewMockLogger() *MockLogger {
	return &MockLogger{
		InfoLogs:      make([]LogEntry, 0),
		DebugLogs:     make([]LogEntry, 0),
		WarnLogs:      make([]LogEntry, 0),
		ErrorLogs:     make([]ErrorLogEntry, 0),
		currentFields: make(map[string]interface{}),
	}
}

// Info logs an informational message with optional structured fields.
func (m *MockLogger) Info(ctx context.Context, message string, fields map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.InfoLogs = append(m.InfoLogs, LogEntry{
		Context: ctx,
		Message: message,
		Fields:  mergeFields(m.currentFields, fields),
	})
}

// Debug logs a debug message with optional structured fields.
func (m *MockLogger) Debug(ctx context.Context, message string, fields map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.DebugLogs = append(m.DebugLogs, LogEntry{
		Context: ctx,
		Message: message,
		Fields:  mergeFields(m.currentFields, fields),
	})
}

// Warn logs a warning message with optional structured fields.
func (m *MockLogger) Warn(ctx context.Context, message string, fields map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.WarnLogs = append(m.WarnLogs, LogEntry{
		Context: ctx,
		Message: message,
		Fields:  mergeFields(m.currentFields, fields),
	})
}

// Warning is an alias for Warn for compatibility with different naming conventions.
func (m *MockLogger) Warning(ctx context.Context, message string, fields map[string]interface{}) {
	m.Warn(ctx, message, fields)
}

// Error logs an error message with the error and optional structured fields.
func (m *MockLogger) Error(ctx context.Context, message string, err error, fields map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ErrorLogs = append(m.ErrorLogs, ErrorLogEntry{
		Context: ctx,
		Message: message,
		Error:   err,
		Fields:  mergeFields(m.currentFields, fields),
	})
}

// WithFields returns a new Logger with the given fields added to all log messages.
func (m *MockLogger) WithFields(fields map[string]interface{}) logger.Logger {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Create a new logger that shares the same log slices but has additional fields
	newLogger := &MockLogger{
		InfoLogs:      m.InfoLogs,
		DebugLogs:     m.DebugLogs,
		WarnLogs:      m.WarnLogs,
		ErrorLogs:     m.ErrorLogs,
		currentFields: make(map[string]interface{}),
	}

	// Copy existing fields
	for k, v := range m.currentFields {
		newLogger.currentFields[k] = v
	}

	// Add new fields
	for k, v := range fields {
		newLogger.currentFields[k] = v
	}

	return newLogger
}

// Reset clears all captured logs.
func (m *MockLogger) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.InfoLogs = make([]LogEntry, 0)
	m.DebugLogs = make([]LogEntry, 0)
	m.WarnLogs = make([]LogEntry, 0)
	m.ErrorLogs = make([]ErrorLogEntry, 0)
	m.currentFields = make(map[string]interface{})
}

// HasLog checks if a log message exists at the specified level.
// The level should be "info", "debug", "warn", or "error".
// The message parameter is checked for substring match.
func (m *MockLogger) HasLog(level, message string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	switch level {
	case "info":
		for _, entry := range m.InfoLogs {
			if containsString(entry.Message, message) {
				return true
			}
		}
	case "debug":
		for _, entry := range m.DebugLogs {
			if containsString(entry.Message, message) {
				return true
			}
		}
	case "warn", "warning":
		for _, entry := range m.WarnLogs {
			if containsString(entry.Message, message) {
				return true
			}
		}
	case "error":
		for _, entry := range m.ErrorLogs {
			if containsString(entry.Message, message) {
				return true
			}
		}
	}

	return false
}

// HasError checks if any error log contains the given message substring.
func (m *MockLogger) HasError(message string) bool {
	return m.HasLog("error", message)
}

// LogCount returns the total number of logs captured at all levels.
func (m *MockLogger) LogCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.InfoLogs) + len(m.DebugLogs) + len(m.WarnLogs) + len(m.ErrorLogs)
}

// GetInfoMessages returns all info log messages.
func (m *MockLogger) GetInfoMessages() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	messages := make([]string, len(m.InfoLogs))
	for i, entry := range m.InfoLogs {
		messages[i] = entry.Message
	}
	return messages
}

// GetErrorMessages returns all error log messages.
func (m *MockLogger) GetErrorMessages() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	messages := make([]string, len(m.ErrorLogs))
	for i, entry := range m.ErrorLogs {
		messages[i] = entry.Message
	}
	return messages
}

// GetErrors returns all errors that were logged.
func (m *MockLogger) GetErrors() []error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	errors := make([]error, len(m.ErrorLogs))
	for i, entry := range m.ErrorLogs {
		errors[i] = entry.Error
	}
	return errors
}

// mergeFields creates a new map combining base fields with additional fields.
func mergeFields(base, additional map[string]interface{}) map[string]interface{} {
	if base == nil && additional == nil {
		return nil
	}

	result := make(map[string]interface{})

	for k, v := range base {
		result[k] = v
	}

	for k, v := range additional {
		result[k] = v
	}

	return result
}

// containsString checks if s contains substr.
func containsString(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}

	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Ensure MockLogger implements logger.Logger at compile time.
var _ logger.Logger = (*MockLogger)(nil)
