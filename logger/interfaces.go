package logger

import (
	"context"
)

// Logger defines the standard interface for structured logging throughout goLibMyCarrier packages.
// Implementations should provide structured logging with context and field support.
// This interface is designed to be flexible enough for use in libraries while supporting
// context propagation for tracing and structured fields for observability.
//
// message versus fields. Field keys and values are caller-supplied data and are
// escaped before rendering: a newline in a field cannot open a second line that
// reads as a genuine log record, and a value carrying the renderer's own "key=value"
// structure is quoted so it cannot forge a sibling field (DEVOPS-284).
//
// message is NOT escaped. No implementation in this package alters it — StdLogger
// and LogAdapter interpolate it into a formatted line, ZapLogger hands it to zap as
// the record's msg — so whether a newline in message forges a record is decided by
// the sink, not here: zap's JSON encoder escapes it, its console encoder and
// StdLogger's log.Logger do not.
//
// Callers must therefore treat message as a literal chosen by the calling code and
// put anything derived from untrusted input — request bodies, webhook payloads,
// branch names, commit messages, error text from a remote — in fields. Note the
// Error methods already do this for err: it is rendered as an "error" field, not
// folded into message.
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
//
// Fields are rendered by renderSanitizedFields and passed as a single pre-rendered
// %s argument. Do not simplify this back to Infof("%s %v", message, fields):
// folding the map into the message argument let a caller-supplied newline forge a
// log line even when the wrapped logger was zap, whose console encoder appends the
// message verbatim (DEVOPS-284). SimpleLogger exposes no structured path — only
// Printf-style methods — so escaping before interpolation is the available remedy.
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
		a.simple.Infof("%s %s", message, renderSanitizedFields(allFields))
	} else {
		a.simple.Info(message)
	}
}

// Debug implements Logger.
func (a *LogAdapter) Debug(ctx context.Context, message string, fields map[string]interface{}) {
	allFields := a.mergeFields(fields)
	if len(allFields) > 0 {
		a.simple.Debugf("%s %s", message, renderSanitizedFields(allFields))
	} else {
		a.simple.Debug(message)
	}
}

// Warn implements Logger.
func (a *LogAdapter) Warn(ctx context.Context, message string, fields map[string]interface{}) {
	allFields := a.mergeFields(fields)
	if len(allFields) > 0 {
		a.simple.Warnf("%s %s", message, renderSanitizedFields(allFields))
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
		a.simple.Errorf("%s %s", message, renderSanitizedFields(allFields))
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
