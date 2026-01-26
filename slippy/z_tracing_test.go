package slippy

import (
	"context"
	"testing"
	"time"

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

func TestContextWithCorrelationTrace(t *testing.T) {
	correlationID := "550e8400-e29b-41d4-a716-446655440000"
	expectedTraceIDHex := "550e8400e29b41d4a716446655440000"

	// Start with a fresh context
	ctx := context.Background()

	// Apply correlation trace
	ctx = ContextWithCorrelationTrace(ctx, correlationID)

	// Verify the trace ID was set
	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.TraceID().String() != expectedTraceIDHex {
		t.Errorf("trace ID = %s, want %s", spanCtx.TraceID().String(), expectedTraceIDHex)
	}
}

func TestContextWithCorrelationTrace_PreservesExisting(t *testing.T) {
	existingTraceID := trace.TraceID{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	}
	existingSpanID := trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18}

	existingSpanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    existingTraceID,
		SpanID:     existingSpanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), existingSpanCtx)

	// Try to apply a different correlation ID
	correlationID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	ctx = ContextWithCorrelationTrace(ctx, correlationID)

	// Should still have the existing trace ID
	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.TraceID() != existingTraceID {
		t.Errorf("trace ID = %s, want existing %s", spanCtx.TraceID().String(), existingTraceID.String())
	}
}

func TestContextWithCorrelationTrace_InvalidUUID(t *testing.T) {
	ctx := context.Background()

	// Invalid correlation ID should return context unchanged
	ctx = ContextWithCorrelationTrace(ctx, "not-a-uuid")

	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.IsValid() {
		t.Error("expected invalid span context for invalid UUID")
	}
}

func TestStartSpan(t *testing.T) {
	correlationID := "550e8400-e29b-41d4-a716-446655440000"
	expectedTraceIDHex := "550e8400e29b41d4a716446655440000"

	ctx := context.Background()

	// Start a span using the public API
	ctx, span := StartSpan(ctx, "ProcessPush", correlationID)
	defer span.End()

	// Verify the trace ID matches the correlation ID
	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.TraceID().String() != expectedTraceIDHex {
		t.Errorf("trace ID = %s, want %s", spanCtx.TraceID().String(), expectedTraceIDHex)
	}

	// Note: span.IsRecording() returns false with no-op tracer, which is expected in tests
	// In production with a configured OTel provider, spans will be recording
}

func TestStartSpan_ChildOfExistingTrace(t *testing.T) {
	existingTraceID := trace.TraceID{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	}
	existingSpanID := trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18}

	existingSpanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    existingTraceID,
		SpanID:     existingSpanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), existingSpanCtx)

	// Start a span - should be child of existing trace
	correlationID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	ctx, span := StartSpan(ctx, "ProcessPush", correlationID)
	defer span.End()

	// Should preserve existing trace ID
	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.TraceID() != existingTraceID {
		t.Errorf("trace ID = %s, want existing %s", spanCtx.TraceID().String(), existingTraceID.String())
	}
}

func TestTracer(t *testing.T) {
	// Tracer() should return a non-nil tracer
	tr := Tracer()
	if tr == nil {
		t.Error("Tracer() should not return nil")
	}
	// Should be the same as the internal tracer()
	if tr != tracer() {
		t.Error("Tracer() should return the same tracer as internal tracer()")
	}
}

func TestRetrySpan_RecordVersionConflict(t *testing.T) {
	ctx := context.Background()
	correlationID := "550e8400-e29b-41d4-a716-446655440000"

	retrySpan := startRetrySpan(ctx, "TestOperation", correlationID)

	// Should not panic
	retrySpan.RecordVersionConflict(1, 2)
	retrySpan.RecordVersionConflict(5, 10)

	retrySpan.EndSuccess()
}

func TestRetrySpan_EndWithStatus(t *testing.T) {
	ctx := context.Background()
	correlationID := "550e8400-e29b-41d4-a716-446655440000"

	tests := []struct {
		name    string
		success bool
		message string
	}{
		{"success", true, "operation completed"},
		{"failure", false, "operation failed: timeout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retrySpan := startRetrySpan(ctx, "TestOperation", correlationID)

			// Should not panic
			retrySpan.EndWithStatus(tt.success, tt.message)
		})
	}
}

func TestRecordJobExecutionSpan(t *testing.T) {
	ctx := context.Background()
	correlationID := "550e8400-e29b-41d4-a716-446655440000"
	startTime := time.Now().Add(-5 * time.Second)

	tests := []struct {
		name          string
		stepName      string
		componentName string
		success       bool
		errorMessage  string
	}{
		{"success without component", "builds", "", true, ""},
		{"success with component", "builds", "svc-a", true, ""},
		{"failure without error message", "builds", "", false, ""},
		{"failure with error message", "builds", "svc-a", false, "build failed: exit code 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			RecordJobExecutionSpan(
				ctx,
				correlationID,
				tt.stepName,
				tt.componentName,
				startTime,
				tt.success,
				tt.errorMessage,
			)
		})
	}
}

func TestJobError(t *testing.T) {
	err := &jobError{message: "build failed"}
	if err.Error() != "build failed" {
		t.Errorf("Error() = %q, want 'build failed'", err.Error())
	}
}
