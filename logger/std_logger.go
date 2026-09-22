package logger

import (
	"context"
	"log"
	"os"
)

// NopLogger is a logger that does nothing.
// Useful for testing or when logging is not needed.
type NopLogger struct{}

// Ensure NopLogger implements Logger.
var _ Logger = (*NopLogger)(nil)

// Info does nothing.
func (l *NopLogger) Info(ctx context.Context, message string, fields map[string]interface{}) {}

// Debug does nothing.
func (l *NopLogger) Debug(ctx context.Context, message string, fields map[string]interface{}) {}

// Warn does nothing.
func (l *NopLogger) Warn(ctx context.Context, message string, fields map[string]interface{}) {}

// Warning is an alias for Warn.
func (l *NopLogger) Warning(ctx context.Context, message string, fields map[string]interface{}) {}

// Error does nothing.
func (l *NopLogger) Error(ctx context.Context, message string, err error, fields map[string]interface{}) {
}

// WithFields returns the same NopLogger.
func (l *NopLogger) WithFields(fields map[string]interface{}) Logger {
	return l
}

// StdLogger is a simple logger that uses the standard library's log package.
// It provides structured logging with optional debug mode.
type StdLogger struct {
	logger *log.Logger
	fields map[string]interface{}
	debug  bool
}

// Ensure StdLogger implements Logger.
var _ Logger = (*StdLogger)(nil)

// NewStdLogger creates a new StdLogger.
// If debug is true, debug-level messages will be logged.
func NewStdLogger(debug bool) *StdLogger {
	return &StdLogger{
		logger: log.New(os.Stdout, "", log.LstdFlags),
		fields: make(map[string]interface{}),
		debug:  debug,
	}
}

// NewStdLoggerWithPrefix creates a new StdLogger with a custom prefix.
func NewStdLoggerWithPrefix(prefix string, debug bool) *StdLogger {
	return &StdLogger{
		logger: log.New(os.Stdout, prefix, log.LstdFlags),
		fields: make(map[string]interface{}),
		debug:  debug,
	}
}

// Info logs an informational message.
func (l *StdLogger) Info(ctx context.Context, message string, fields map[string]interface{}) {
	l.log("INFO", message, fields)
}

// Debug logs a debug message (only if debug mode is enabled).
func (l *StdLogger) Debug(ctx context.Context, message string, fields map[string]interface{}) {
	if l.debug {
		l.log("DEBUG", message, fields)
	}
}

// Warn logs a warning message.
func (l *StdLogger) Warn(ctx context.Context, message string, fields map[string]interface{}) {
	l.log("WARN", message, fields)
}

// Warning is an alias for Warn.
func (l *StdLogger) Warning(ctx context.Context, message string, fields map[string]interface{}) {
	l.Warn(ctx, message, fields)
}

// Error logs an error message.
func (l *StdLogger) Error(ctx context.Context, message string, err error, fields map[string]interface{}) {
	if err != nil {
		// Copy rather than write into the caller's map: the caller still owns it,
		// and a shared map would also be a concurrent-write hazard. The sibling
		// implementations both copy via mergeFields.
		merged := make(map[string]interface{}, len(fields)+1)
		for k, v := range fields {
			merged[k] = v
		}
		merged["error"] = err.Error()
		fields = merged
	}
	l.log("ERROR", message, fields)
}

// WithFields returns a new StdLogger with the given fields added.
func (l *StdLogger) WithFields(fields map[string]interface{}) Logger {
	newFields := make(map[string]interface{})
	for k, v := range l.fields {
		newFields[k] = v
	}
	for k, v := range fields {
		newFields[k] = v
	}
	return &StdLogger{
		logger: l.logger,
		fields: newFields,
		debug:  l.debug,
	}
}

// IsDebugEnabled returns whether debug logging is enabled.
func (l *StdLogger) IsDebugEnabled() bool {
	return l.debug
}

// SetDebug enables or disables debug logging.
func (l *StdLogger) SetDebug(debug bool) {
	l.debug = debug
}

// log formats and outputs the log message.
func (l *StdLogger) log(level, message string, fields map[string]interface{}) {
	// Merge base fields with provided fields
	allFields := make(map[string]interface{})
	for k, v := range l.fields {
		allFields[k] = v
	}
	for k, v := range fields {
		allFields[k] = v
	}

	// Fields are rendered escaped, quoted where they carry the "key=value"
	// structure, and key-sorted. Interpolating them raw let a caller-supplied
	// newline open a second line that read as a genuine log entry (DEVOPS-284).
	// The message is interpolated as given: Logger's contract puts caller-supplied
	// data in fields and requires message to be a literal, so it is not escaped
	// here.
	fieldsStr := ""
	if len(allFields) > 0 {
		fieldsStr = " " + renderSanitizedFields(allFields)
	}

	l.logger.Printf("[%s] %s%s", level, message, fieldsStr)
}
