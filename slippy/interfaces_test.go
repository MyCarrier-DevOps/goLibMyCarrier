package slippy

import (
	"context"
	"testing"
)

func TestNopLogger(t *testing.T) {
	ctx := context.Background()
	
	t.Run("returns non-nil logger", func(t *testing.T) {
		log := NopLogger()
		if log == nil {
			t.Error("NopLogger returned nil")
		}
	})

	t.Run("does not panic on method calls", func(t *testing.T) {
		log := NopLogger()
		// Verify these don't panic
		log.Info(ctx, "test message", nil)
		log.Debug(ctx, "debug message", nil)
		log.Warn(ctx, "warn message", nil)
		log.Error(ctx, "error message", nil, nil)
	})
}

func TestNewStdLogger(t *testing.T) {
	t.Run("creates logger without debug", func(t *testing.T) {
		log := NewStdLogger(false)
		if log == nil {
			t.Error("NewStdLogger returned nil")
		}
	})

	t.Run("creates logger with debug", func(t *testing.T) {
		log := NewStdLogger(true)
		if log == nil {
			t.Error("NewStdLogger returned nil")
		}
	})
}
