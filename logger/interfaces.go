package logger

import (
	"context"
)

// Logger defines the standard interface for structured logging throughout goLibMyCarrier packages.
// Implementations should provide structured logging with context and field support.
// This interface is designed to be flexible enough for use in libraries while supporting
// context propagation for tracing and structured fields for observability.
type Logger interface {
	// Info logs an informational message with optional structured fields.
	Info(ctx context.Context, message string, fields map[string]interface{})

	// Debug logs a debug message with optional structured fields.
	Debug(ctx context.Context, message string, fields map[string]interface{})

	// Warn logs a warning message with optional structured fields.
	Warn(ctx context.Context, message string, fields map[string]interface{})

	// Warning is an alias for Warn for compatibility with different naming conventions.
	Warning(ctx context.Context, message string, fields map[string]interface{})

	// Error logs an error message with the error and optional structured fields.
	Error(ctx context.Context, message string, err error, fields map[string]interface{})

	// WithFields returns a new Logger with the given fields added to all log messages.
	// This is useful for adding contextual information that should appear in all subsequent logs.
	WithFields(fields map[string]interface{}) Logger
}

// SimpleLogger defines a simpler logging interface for cases where context
// and structured fields are not needed. This is compatible with most basic loggers.
type SimpleLogger interface {
	Info(args ...interface{})
	Infof(format string, args ...interface{})
	Debug(args ...interface{})
	Debugf(format string, args ...interface{})
	Warn(args ...interface{})
	Warnf(format string, args ...interface{})
	Error(args ...interface{})
	Errorf(format string, args ...interface{})
}

// LogAdapter wraps a SimpleLogger to implement the full Logger interface.
// This allows using simpler loggers (like zap.SugaredLogger) where the full interface is expected.
type LogAdapter struct {
	simple SimpleLogger
	fields map[string]interface{}
}

// NewLogAdapter creates a new LogAdapter wrapping the given SimpleLogger.
func NewLogAdapter(simple SimpleLogger) *LogAdapter {
	return &LogAdapter{
		simple: simple,
		fields: make(map[string]interface{}),
	}
}

// Info implements Logger.
func (a *LogAdapter) Info(ctx context.Context, message string, fields map[string]interface{}) {
	allFields := a.mergeFields(fields)
	if len(allFields) > 0 {
		a.simple.Infof("%s %v", message, allFields)
	} else {
		a.simple.Info(message)
	}
}

// Debug implements Logger.
func (a *LogAdapter) Debug(ctx context.Context, message string, fields map[string]interface{}) {
	allFields := a.mergeFields(fields)
	if len(allFields) > 0 {
		a.simple.Debugf("%s %v", message, allFields)
	} else {
		a.simple.Debug(message)
	}
}

// Warn implements Logger.
func (a *LogAdapter) Warn(ctx context.Context, message string, fields map[string]interface{}) {
	allFields := a.mergeFields(fields)
	if len(allFields) > 0 {
		a.simple.Warnf("%s %v", message, allFields)
	} else {
		a.simple.Warn(message)
	}
}

// Warning is an alias for Warn.
func (a *LogAdapter) Warning(ctx context.Context, message string, fields map[string]interface{}) {
	a.Warn(ctx, message, fields)
}

// Error implements Logger.
func (a *LogAdapter) Error(ctx context.Context, message string, err error, fields map[string]interface{}) {
	allFields := a.mergeFields(fields)
	if err != nil {
		if allFields == nil {
			allFields = make(map[string]interface{})
		}
		allFields["error"] = err.Error()
	}
	if len(allFields) > 0 {
		a.simple.Errorf("%s %v", message, allFields)
	} else {
		a.simple.Error(message)
	}
}

// WithFields implements Logger.
func (a *LogAdapter) WithFields(fields map[string]interface{}) Logger {
	newFields := make(map[string]interface{})
	for k, v := range a.fields {
		newFields[k] = v
	}
	for k, v := range fields {
		newFields[k] = v
	}
	return &LogAdapter{
		simple: a.simple,
		fields: newFields,
	}
}

// mergeFields merges the adapter's base fields with provided fields.
func (a *LogAdapter) mergeFields(fields map[string]interface{}) map[string]interface{} {
	if len(a.fields) == 0 && len(fields) == 0 {
		return nil
	}
	result := make(map[string]interface{})
	for k, v := range a.fields {
		result[k] = v
	}
	for k, v := range fields {
		result[k] = v
	}
	return result
}
