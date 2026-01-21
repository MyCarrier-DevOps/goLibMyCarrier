package slippy

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestCorrelationIDToTraceID(t *testing.T) {
	tests := []struct {
		name          string
		correlationID string
		wantOK        bool
		wantHex       string // expected trace ID as hex string
	}{
		{
			name:          "valid UUID with hyphens",
			correlationID: "550e8400-e29b-41d4-a716-446655440000",
			wantOK:        true,
			wantHex:       "550e8400e29b41d4a716446655440000",
		},
		{
			name:          "valid UUID without hyphens",
			correlationID: "550e8400e29b41d4a716446655440000",
			wantOK:        true,
			wantHex:       "550e8400e29b41d4a716446655440000",
		},
		{
			name:          "all zeros UUID",
			correlationID: "00000000-0000-0000-0000-000000000000",
			wantOK:        true,
			wantHex:       "00000000000000000000000000000000",
		},
		{
			name:          "all ones UUID",
			correlationID: "ffffffff-ffff-ffff-ffff-ffffffffffff",
			wantOK:        true,
			wantHex:       "ffffffffffffffffffffffffffffffff",
		},
		{
			name:          "too short",
			correlationID: "550e8400-e29b-41d4",
			wantOK:        false,
		},
		{
			name:          "too long",
			correlationID: "550e8400-e29b-41d4-a716-446655440000-extra",
			wantOK:        false,
		},
		{
			name:          "invalid hex characters",
			correlationID: "550e8400-e29b-41d4-a716-44665544000g",
			wantOK:        false,
		},
		{
			name:          "empty string",
			correlationID: "",
			wantOK:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			traceID, ok := correlationIDToTraceID(tt.correlationID)

			if ok != tt.wantOK {
				t.Errorf("correlationIDToTraceID(%q) ok = %v, want %v", tt.correlationID, ok, tt.wantOK)
				return
			}

			if ok && traceID.String() != tt.wantHex {
				t.Errorf("correlationIDToTraceID(%q) = %s, want %s", tt.correlationID, traceID.String(), tt.wantHex)
			}
		})
	}
}

func TestStartRetrySpan_UsesCorrelationIDAsTraceID(t *testing.T) {
	correlationID := "550e8400-e29b-41d4-a716-446655440000"
	expectedTraceIDHex := "550e8400e29b41d4a716446655440000"

	// Start with a fresh context (no existing trace)
	ctx := context.Background()

	retrySpan := startRetrySpan(ctx, "TestOperation", correlationID)

	// Get the span context from the returned context
	spanCtx := trace.SpanContextFromContext(retrySpan.Context())

	// Verify the trace ID matches the correlation ID
	if spanCtx.TraceID().String() != expectedTraceIDHex {
		t.Errorf("trace ID = %s, want %s", spanCtx.TraceID().String(), expectedTraceIDHex)
	}

	// Clean up
	retrySpan.EndSuccess()
}

func TestStartRetrySpan_PreservesExistingTrace(t *testing.T) {
	// Create a context with an existing trace
	existingTraceID := trace.TraceID{
		0x01,
		0x02,
		0x03,
		0x04,
		0x05,
		0x06,
		0x07,
		0x08,
		0x09,
		0x0a,
		0x0b,
		0x0c,
		0x0d,
		0x0e,
		0x0f,
		0x10,
	}
	existingSpanID := trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18}

	existingSpanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    existingTraceID,
		SpanID:     existingSpanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), existingSpanCtx)

	// Different correlation ID that would produce a different trace ID
	correlationID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	retrySpan := startRetrySpan(ctx, "TestOperation", correlationID)

	// Get the span context from the returned context
	spanCtx := trace.SpanContextFromContext(retrySpan.Context())

	// The trace ID should still be the existing one (not overwritten by correlation ID)
	if spanCtx.TraceID() != existingTraceID {
		t.Errorf(
			"trace ID = %s, want existing trace ID %s (should not override)",
			spanCtx.TraceID().String(),
			existingTraceID.String(),
		)
	}

	// Clean up
	retrySpan.EndSuccess()
}

func TestRetrySpan_RecordAttempt(t *testing.T) {
	ctx := context.Background()
	retrySpan := startRetrySpan(ctx, "TestOperation", "550e8400-e29b-41d4-a716-446655440000")

	// Record a few attempts
	retrySpan.RecordAttempt(100)
	retrySpan.RecordAttempt(200)
	retrySpan.RecordAttempt(400)

	if retrySpan.attempts != 3 {
		t.Errorf("attempts = %d, want 3", retrySpan.attempts)
	}

	if retrySpan.totalBackoff != 700 {
		t.Errorf("totalBackoff = %d, want 700", retrySpan.totalBackoff)
	}

	retrySpan.EndSuccess()
}

func TestRetrySpan_AddAttribute(t *testing.T) {
	ctx := context.Background()
	retrySpan := startRetrySpan(ctx, "TestOperation", "550e8400-e29b-41d4-a716-446655440000")

	// These should not panic
	retrySpan.AddAttribute("string_attr", "value")
	retrySpan.AddAttribute("int_attr", 42)
	retrySpan.AddAttribute("int64_attr", int64(42))
	retrySpan.AddAttribute("bool_attr", true)
	retrySpan.AddAttribute("float64_attr", 3.14)

	retrySpan.EndSuccess()
}
