package postgresmigrator

import (
	"github.com/MyCarrier-DevOps/goLibMyCarrier/logger"
)

// NopLogger is an alias for logger.NopLogger — a logger that discards output.
type NopLogger = logger.NopLogger

// StdLogger is an alias for logger.StdLogger — a simple stdlib-backed logger.
type StdLogger = logger.StdLogger

// NewStdLogger creates a new StdLogger. If debug is true, debug-level messages
// are logged.
var NewStdLogger = logger.NewStdLogger

// NewStdLoggerWithPrefix creates a new StdLogger with a custom prefix.
var NewStdLoggerWithPrefix = logger.NewStdLoggerWithPrefix
