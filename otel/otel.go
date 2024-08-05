package otel

import (
	"time"

	"github.com/gin-gonic/gin"
	bridge "github.com/odigos-io/opentelemetry-zap-bridge"
	"go.uber.org/zap"
)

// TODO: Add functions to initialize the OpenTelemetry SDK for tracing and metrics.

func InitZapLogger() (*zap.Logger, error) {
	// Create a logger
	logger, err := zap.NewProduction()
	if err != nil {
		logger.Error("failed to create logger", zap.Error(err))
	}
	logger = bridge.AttachToZapLogger(logger)
	return logger, err
}

// Gin middleware for logging with Zap
func ZapLoggerForGin(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Start time
		start := time.Now()

		// Process request
		c.Next()

		// Log the request details
		logger.Info("request",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.String("client_ip", c.ClientIP()),
		)
	}
}
