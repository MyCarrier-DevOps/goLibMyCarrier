package slippy

// This file provides logger utilities for the slippy package.
// The Logger interface and implementations are provided by the
// goLibMyCarrier/logger package and re-exported via interfaces.go.
//
// Usage:
//   - NopLogger() returns a no-op logger
//   - NewStdLogger(debug) returns a simple stdout logger
//   - For production, wrap a zap.SugaredLogger with logger.NewZapLogger()
