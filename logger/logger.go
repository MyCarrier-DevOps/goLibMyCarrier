package logger

import (
	"context"
	"os"

	zap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger constants
const (
	TimestampFormat = "2006-01-02 15:04:05"
	InfoLevel       = "info"
	DebugLevel      = "debug"
	ErrorLevel      = "error"
)

// AppLogger returns a new AppLogger instance
func NewAppLogger() *zap.SugaredLogger {
	// Set default log level and app name
	logLevel, _ := os.LookupEnv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = InfoLevel
	}
	appName, _ := os.LookupEnv("LOG_APP_NAME")
	if appName == "" {
		appName = "default_app"
	}

	config := ConfigureLogLevelLogger(logLevel)
	// Config customization goes here if any
	config.EncoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	config.OutputPaths = []string{"stdout"}
	logger, err := config.Build()
	if err != nil {
		panic(err)
	}
	return logger.Named(appName).Sugar()
}

type loggerKey struct{}

// WithLogger returns a copy of parent context in which the
// value associated with logger key is the supplied logger.
func WithLogger(ctx context.Context, logger *zap.SugaredLogger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

// FromContext returns the logger in the context.
func FromContext(ctx context.Context) *zap.SugaredLogger {
	if logger, ok := ctx.Value(loggerKey{}).(*zap.SugaredLogger); ok {
		return logger
	}
	return NewAppLogger()
}

// Returns logger conifg depending on the log level
func ConfigureLogLevelLogger(logLevel string) zap.Config {
	logConfig := zap.NewProductionConfig()
	switch logLevel {
	case InfoLevel:
		logConfig.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	case ErrorLevel:
		logConfig.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	case DebugLevel:
		logConfig.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	default:
		logConfig.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}
	return logConfig
}
