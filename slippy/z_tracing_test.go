package slippy

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func TestStartOperationalSpan_CreatesIndependentTrace(t *testing.T) {
	// Create a context with an existing trace (simulating a pipeline content trace)
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

	correlationID := "550e8400-e29b-41d4-a716-446655440000"

	// Start an operational span - it should NOT inherit from the existing trace
	newCtx, span := StartOperationalSpan(ctx, "TestOperation", correlationID)
	defer span.End()

	// Get the span context from the returned context
	spanCtx := trace.SpanContextFromContext(newCtx)

	// The operational span should NOT be a child of the existing pipeline trace.
	// Since we're using a no-op tracer, the trace ID will be zeros,
	// but critically it should NOT be the existing trace ID.
	if spanCtx.TraceID() == existingTraceID {
		t.Errorf(
			"StartOperationalSpan should create independent trace, but got pipeline trace ID %s",
			spanCtx.TraceID().String(),
		)
	}
}

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

func TestStartRetrySpan_CreatesOperationalTrace(t *testing.T) {
	correlationID := "550e8400-e29b-41d4-a716-446655440000"

	// Start with a fresh context (no existing trace)
	ctx := context.Background()

	retrySpan := startRetrySpan(ctx, "TestOperation", correlationID)

	// Get the span context from the returned context
	spanCtx := trace.SpanContextFromContext(retrySpan.Context())

	// The operational span should NOT use the correlation ID as trace ID
	// (that's only for pipeline content spans, not operational spans).
	// Instead, it creates a new independent trace.
	// The trace ID should be all zeros since we're using a no-op tracer in tests
	// (no exporter configured), but the important thing is that it's NOT the correlation ID.
	correlationIDAsTraceID := "550e8400e29b41d4a716446655440000"
	if spanCtx.TraceID().String() == correlationIDAsTraceID {
		t.Errorf("operational span should NOT use correlation ID as trace ID, got %s", spanCtx.TraceID().String())
	}

	// Clean up
	retrySpan.EndSuccess()
}

func TestStartRetrySpan_DoesNotInheritFromPipelineTrace(t *testing.T) {
	// Create a context with an existing trace (simulating a pipeline content trace)
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

	// Different correlation ID
	correlationID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	retrySpan := startRetrySpan(ctx, "TestOperation", correlationID)

	// Get the span context from the returned context
	spanCtx := trace.SpanContextFromContext(retrySpan.Context())

	// The operational span should NOT inherit from the existing pipeline trace.
	// This keeps operational spans separate from pipeline content spans.
	// Since we're using a no-op tracer, the trace ID will be zeros,
	// but the important thing is it's NOT the existing trace ID.
	if spanCtx.TraceID() == existingTraceID {
		t.Errorf(
			"operational span should NOT inherit from pipeline trace, got trace ID %s",
			spanCtx.TraceID().String(),
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

// ============================================================================
// Context Service Name Tests (Functional Options Pattern)
// ============================================================================

func TestContextWithServiceName(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		wantStored  string
	}{
		{
			name:        "stores service name in context",
			serviceName: "pushhookparser",
			wantStored:  "pushhookparser",
		},
		{
			name:        "stores different service name",
			serviceName: "Slippy",
			wantStored:  "Slippy",
		},
		{
			name:        "stores TestEngine.Worker service name",
			serviceName: "TestEngine.Worker",
			wantStored:  "TestEngine.Worker",
		},
		{
			name:        "handles empty service name",
			serviceName: "",
			wantStored:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = ContextWithServiceName(ctx, tt.serviceName)

			got := getServiceNameFromContext(ctx)
			if got != tt.wantStored {
				t.Errorf("getServiceNameFromContext() = %q, want %q", got, tt.wantStored)
			}
		})
	}
}

func TestGetServiceNameFromContext_EmptyContext(t *testing.T) {
	ctx := context.Background()
	got := getServiceNameFromContext(ctx)
	if got != "" {
		t.Errorf("getServiceNameFromContext() from empty context = %q, want empty string", got)
	}
}

func TestContextWithServiceName_Overwrite(t *testing.T) {
	ctx := context.Background()

	// Set initial service name
	ctx = ContextWithServiceName(ctx, "service1")
	if got := getServiceNameFromContext(ctx); got != "service1" {
		t.Errorf("First set: got %q, want %q", got, "service1")
	}

	// Overwrite with new service name
	ctx = ContextWithServiceName(ctx, "service2")
	if got := getServiceNameFromContext(ctx); got != "service2" {
		t.Errorf("Second set: got %q, want %q", got, "service2")
	}
}

// ============================================================================
// Span Option Tests (Functional Options Pattern)
// ============================================================================

func TestWithServiceName(t *testing.T) {
	cfg := &spanConfig{}
	opt := WithServiceName("test-service")
	opt(cfg)

	if cfg.serviceName != "test-service" {
		t.Errorf("WithServiceName() set serviceName = %q, want %q", cfg.serviceName, "test-service")
	}
}

func TestWithSpanKind(t *testing.T) {
	tests := []struct {
		name string
		kind trace.SpanKind
	}{
		{"client", SpanKindClient},
		{"server", SpanKindServer},
		{"producer", SpanKindProducer},
		{"consumer", SpanKindConsumer},
		{"internal", SpanKindInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &spanConfig{}
			opt := WithSpanKind(tt.kind)
			opt(cfg)

			if cfg.spanKind != tt.kind {
				t.Errorf("WithSpanKind() set spanKind = %v, want %v", cfg.spanKind, tt.kind)
			}
		})
	}
}

func TestWithAttributes(t *testing.T) {
	cfg := &spanConfig{}
	attrs := []attribute.KeyValue{
		attribute.String("key1", "value1"),
		attribute.Int("key2", 42),
		attribute.Bool("key3", true),
	}
	opt := WithAttributes(attrs...)
	opt(cfg)

	if len(cfg.attributes) != 3 {
		t.Errorf("WithAttributes() set %d attributes, want 3", len(cfg.attributes))
	}

	// Verify each attribute
	if cfg.attributes[0].Key != "key1" || cfg.attributes[0].Value.AsString() != "value1" {
		t.Errorf("First attribute mismatch: got %v", cfg.attributes[0])
	}
	if cfg.attributes[1].Key != "key2" || cfg.attributes[1].Value.AsInt64() != 42 {
		t.Errorf("Second attribute mismatch: got %v", cfg.attributes[1])
	}
	if cfg.attributes[2].Key != "key3" || !cfg.attributes[2].Value.AsBool() {
		t.Errorf("Third attribute mismatch: got %v", cfg.attributes[2])
	}
}

func TestWithAttributes_Accumulates(t *testing.T) {
	cfg := &spanConfig{}

	// Add first batch of attributes
	opt1 := WithAttributes(attribute.String("key1", "value1"))
	opt1(cfg)

	// Add second batch of attributes
	opt2 := WithAttributes(attribute.String("key2", "value2"))
	opt2(cfg)

	if len(cfg.attributes) != 2 {
		t.Errorf("WithAttributes() accumulated %d attributes, want 2", len(cfg.attributes))
	}
}

func TestApplySpanOptions_Defaults(t *testing.T) {
	cfg := applySpanOptions()

	if cfg.serviceName != "" {
		t.Errorf("Default serviceName = %q, want empty", cfg.serviceName)
	}
	if cfg.spanKind != trace.SpanKindInternal {
		t.Errorf("Default spanKind = %v, want Internal", cfg.spanKind)
	}
	if len(cfg.attributes) != 0 {
		t.Errorf("Default attributes length = %d, want 0", len(cfg.attributes))
	}
}

func TestApplySpanOptions_MultipleOptions(t *testing.T) {
	cfg := applySpanOptions(
		WithServiceName("my-service"),
		WithSpanKind(SpanKindProducer),
		WithAttributes(attribute.String("env", "test")),
	)

	if cfg.serviceName != "my-service" {
		t.Errorf("serviceName = %q, want %q", cfg.serviceName, "my-service")
	}
	if cfg.spanKind != SpanKindProducer {
		t.Errorf("spanKind = %v, want Producer", cfg.spanKind)
	}
	if len(cfg.attributes) != 1 {
		t.Errorf("attributes length = %d, want 1", len(cfg.attributes))
	}
}

// ============================================================================
// StartSpan with Options Tests
// ============================================================================

func TestStartSpan_WithServiceNameOption(t *testing.T) {
	ctx := context.Background()
	correlationID := "550e8400-e29b-41d4-a716-446655440000"

	_, span := StartSpan(ctx, "TestOperation", correlationID,
		WithServiceName("test-service"),
	)
	defer span.End()

	// The span should be created (we can't easily inspect attributes without a test tracer)
	if span == nil {
		t.Error("StartSpan with options returned nil span")
	}
}

func TestStartSpan_WithSpanKindOption(t *testing.T) {
	ctx := context.Background()
	correlationID := "550e8400-e29b-41d4-a716-446655440000"

	_, span := StartSpan(ctx, "TestOperation", correlationID,
		WithSpanKind(SpanKindProducer),
	)
	defer span.End()

	if span == nil {
		t.Error("StartSpan with SpanKind option returned nil span")
	}
}

func TestStartSpan_WithAllOptions(t *testing.T) {
	ctx := context.Background()
	correlationID := "550e8400-e29b-41d4-a716-446655440000"

	_, span := StartSpan(ctx, "TestOperation", correlationID,
		WithServiceName("my-service"),
		WithSpanKind(SpanKindClient),
		WithAttributes(
			attribute.String("custom.key", "custom-value"),
			attribute.Int("custom.count", 10),
		),
	)
	defer span.End()

	if span == nil {
		t.Error("StartSpan with all options returned nil span")
	}
}

func TestStartSpan_InheritsServiceNameFromContext(t *testing.T) {
	ctx := context.Background()
	ctx = ContextWithServiceName(ctx, "context-service")
	correlationID := "550e8400-e29b-41d4-a716-446655440000"

	// Don't pass WithServiceName - should inherit from context
	_, span := StartSpan(ctx, "TestOperation", correlationID)
	defer span.End()

	if span == nil {
		t.Error("StartSpan should work with service name from context")
	}
}

func TestStartSpan_OptionOverridesContextServiceName(t *testing.T) {
	ctx := context.Background()
	ctx = ContextWithServiceName(ctx, "context-service")
	correlationID := "550e8400-e29b-41d4-a716-446655440000"

	// Pass explicit WithServiceName - should override context
	_, span := StartSpan(ctx, "TestOperation", correlationID,
		WithServiceName("option-service"),
	)
	defer span.End()

	if span == nil {
		t.Error("StartSpan should work with service name from option")
	}
}

// ============================================================================
// StartOperationalSpan with Options Tests
// ============================================================================

func TestStartOperationalSpan_WithServiceNameOption(t *testing.T) {
	ctx := context.Background()
	correlationID := "550e8400-e29b-41d4-a716-446655440000"

	_, span := StartOperationalSpan(ctx, "AppendHistory", correlationID,
		WithServiceName("Slippy"),
	)
	defer span.End()

	if span == nil {
		t.Error("StartOperationalSpan with service name option returned nil span")
	}
}

func TestStartOperationalSpan_WithAttributes(t *testing.T) {
	ctx := context.Background()
	correlationID := "550e8400-e29b-41d4-a716-446655440000"

	_, span := StartOperationalSpan(ctx, "hydrateSlip", correlationID,
		WithAttributes(
			attribute.String("step.name", "build"),
			attribute.Bool("include_history", true),
		),
	)
	defer span.End()

	if span == nil {
		t.Error("StartOperationalSpan with attributes returned nil span")
	}
}

func TestStartOperationalSpan_InheritsServiceNameFromContext(t *testing.T) {
	ctx := context.Background()
	ctx = ContextWithServiceName(ctx, "TestEngine.Worker")
	correlationID := "550e8400-e29b-41d4-a716-446655440000"

	// Don't pass WithServiceName - should inherit from context
	_, span := StartOperationalSpan(ctx, "LoadSlip", correlationID)
	defer span.End()

	if span == nil {
		t.Error("StartOperationalSpan should work with service name from context")
	}
}

func TestStartOperationalSpan_OptionOverridesContextServiceName(t *testing.T) {
	ctx := context.Background()
	ctx = ContextWithServiceName(ctx, "context-service")
	correlationID := "550e8400-e29b-41d4-a716-446655440000"

	// Pass explicit WithServiceName - should override context
	_, span := StartOperationalSpan(ctx, "UpdateStep", correlationID,
		WithServiceName("Slippy"),
	)
	defer span.End()

	if span == nil {
		t.Error("StartOperationalSpan should work with service name from option")
	}
}

// ============================================================================
// Span Kind Constants Tests
// ============================================================================

func TestSpanKindConstants(t *testing.T) {
	// Verify our constants match the underlying trace package constants
	if SpanKindInternal != trace.SpanKindInternal {
		t.Error("SpanKindInternal mismatch")
	}
	if SpanKindClient != trace.SpanKindClient {
		t.Error("SpanKindClient mismatch")
	}
	if SpanKindServer != trace.SpanKindServer {
		t.Error("SpanKindServer mismatch")
	}
	if SpanKindProducer != trace.SpanKindProducer {
		t.Error("SpanKindProducer mismatch")
	}
	if SpanKindConsumer != trace.SpanKindConsumer {
		t.Error("SpanKindConsumer mismatch")
	}
}

// ============================================================================
// StartRetrySpan inherits service name from context
// ============================================================================

func TestStartRetrySpan_InheritsServiceNameFromContext(t *testing.T) {
	ctx := context.Background()
	ctx = ContextWithServiceName(ctx, "TestEngine.Worker")
	correlationID := "550e8400-e29b-41d4-a716-446655440000"

	retrySpan := startRetrySpan(ctx, "UpdateStepWithRetry", correlationID)
	defer retrySpan.EndSuccess()

	if retrySpan == nil {
		t.Fatal("startRetrySpan returned nil")
	}
	if retrySpan.span == nil {
		t.Error("RetrySpan.span is nil")
	}
}
