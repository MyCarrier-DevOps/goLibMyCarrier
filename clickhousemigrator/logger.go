package clickhousemigrator

import (
	"github.com/MyCarrier-DevOps/goLibMyCarrier/logger"
)

// NopLogger is an alias for logger.NopLogger from goLibMyCarrier/logger.
// It is a logger that does nothing, useful for testing or when logging is not needed.
type NopLogger = logger.NopLogger

// StdLogger is an alias for logger.StdLogger from goLibMyCarrier/logger.
// It is a simple logger that uses the standard library's log package.
type StdLogger = logger.StdLogger

// NewStdLogger creates a new StdLogger.
// If debug is true, debug-level messages will be logged.
var NewStdLogger = logger.NewStdLogger

// NewStdLoggerWithPrefix creates a new StdLogger with a custom prefix.
var NewStdLoggerWithPrefix = logger.NewStdLoggerWithPrefix
