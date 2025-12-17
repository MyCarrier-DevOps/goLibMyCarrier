package logger

import (
	"context"

	"go.uber.org/zap"
)

// ZapLogger wraps a zap.SugaredLogger to implement the Logger interface.
// This allows using zap loggers with packages that expect the Logger interface.
type ZapLogger struct {
	sugar  *zap.SugaredLogger
	fields map[string]interface{}
}

// Ensure ZapLogger implements Logger.
var _ Logger = (*ZapLogger)(nil)

// NewZapLogger creates a new ZapLogger wrapping the given SugaredLogger.
func NewZapLogger(sugar *zap.SugaredLogger) *ZapLogger {
	return &ZapLogger{
		sugar:  sugar,
		fields: make(map[string]interface{}),
	}
}

// NewZapLoggerFromConfig creates a new ZapLogger using the standard app logger configuration.
// This is a convenience function that combines NewAppLogger and NewZapLogger.
func NewZapLoggerFromConfig() *ZapLogger {
	return NewZapLogger(NewAppLogger())
}

// Info logs an informational message with structured fields.
func (l *ZapLogger) Info(ctx context.Context, message string, fields map[string]interface{}) {
	l.logWithFields(l.sugar.Infow, message, fields)
}

// Debug logs a debug message with structured fields.
func (l *ZapLogger) Debug(ctx context.Context, message string, fields map[string]interface{}) {
	l.logWithFields(l.sugar.Debugw, message, fields)
}

// Warn logs a warning message with structured fields.
func (l *ZapLogger) Warn(ctx context.Context, message string, fields map[string]interface{}) {
	l.logWithFields(l.sugar.Warnw, message, fields)
}

// Warning is an alias for Warn.
func (l *ZapLogger) Warning(ctx context.Context, message string, fields map[string]interface{}) {
	l.Warn(ctx, message, fields)
}

// Error logs an error message with structured fields.
func (l *ZapLogger) Error(ctx context.Context, message string, err error, fields map[string]interface{}) {
	allFields := l.mergeFields(fields)
	if err != nil {
		if allFields == nil {
			allFields = make(map[string]interface{})
		}
		allFields["error"] = err.Error()
	}
	l.logWithFields(l.sugar.Errorw, message, allFields)
}

// WithFields returns a new ZapLogger with the given fields added.
func (l *ZapLogger) WithFields(fields map[string]interface{}) Logger {
	newFields := make(map[string]interface{})
	for k, v := range l.fields {
		newFields[k] = v
	}
	for k, v := range fields {
		newFields[k] = v
	}
	return &ZapLogger{
		sugar:  l.sugar,
		fields: newFields,
	}
}

// Sugar returns the underlying zap.SugaredLogger.
// This allows access to the full zap API when needed.
func (l *ZapLogger) Sugar() *zap.SugaredLogger {
	return l.sugar
}

// logWithFields is a helper that converts map fields to zap's key-value pairs.
func (l *ZapLogger) logWithFields(logFn func(string, ...interface{}), message string, fields map[string]interface{}) {
	allFields := l.mergeFields(fields)
	if len(allFields) == 0 {
		logFn(message)
		return
	}

	// Convert map to alternating key-value pairs for zap
	kvPairs := make([]interface{}, 0, len(allFields)*2)
	for k, v := range allFields {
		kvPairs = append(kvPairs, k, v)
	}
	logFn(message, kvPairs...)
}

// mergeFields merges the logger's base fields with provided fields.
func (l *ZapLogger) mergeFields(fields map[string]interface{}) map[string]interface{} {
	if len(l.fields) == 0 && len(fields) == 0 {
		return nil
	}
	result := make(map[string]interface{})
	for k, v := range l.fields {
		result[k] = v
	}
	for k, v := range fields {
		result[k] = v
	}
	return result
}

// Sync flushes any buffered log entries.
// Applications should take care to call Sync before exiting.
func (l *ZapLogger) Sync() error {
	return l.sugar.Sync()
}
