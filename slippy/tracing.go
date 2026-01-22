package slippy

import (
	"context"
	"encoding/hex"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	// tracerName is the instrumentation name for slippy tracing
	tracerName = "github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

// tracer returns the global OpenTelemetry tracer for the slippy package.
// If no tracer provider is configured, this returns a no-op tracer.
func tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// Tracer returns the OpenTelemetry tracer for creating spans.
// This is the public API for external packages (like pushhookparser) to create spans
// that are properly instrumented under the slippy tracer name.
func Tracer() trace.Tracer {
	return tracer()
}

// correlationIDToTraceID converts a UUID correlation ID to an OpenTelemetry trace ID.
// UUIDs are 128-bit, which matches the trace ID size exactly.
// Example: "550e8400-e29b-41d4-a716-446655440000" -> [16]byte trace ID
func correlationIDToTraceID(correlationID string) (trace.TraceID, bool) {
	// Remove hyphens from UUID format
	hexStr := strings.ReplaceAll(correlationID, "-", "")
	if len(hexStr) != 32 {
		return trace.TraceID{}, false
	}

	bytes, err := hex.DecodeString(hexStr)
	if err != nil {
		return trace.TraceID{}, false
	}

	var traceID trace.TraceID
	copy(traceID[:], bytes)
	return traceID, true
}

// ContextWithCorrelationTrace returns a context that uses the correlation ID as the trace ID.
// This allows callers (like pushhookparser) to create a parent span that ties all subsequent
// slippy operations together under a single trace rooted in the correlation ID.
//
// Usage:
//
//	ctx := slippy.ContextWithCorrelationTrace(ctx, correlationID)
//	ctx, span := tracer.Start(ctx, "MyOperation")
//	defer span.End()
//	// All slippy calls with this ctx will be children of this span
//	store.UpdateStep(ctx, correlationID, "build", "", slippy.StatusCompleted)
//
// If the context already has a valid trace, it is returned unchanged.
// If the correlation ID cannot be parsed as a UUID, the original context is returned.
func ContextWithCorrelationTrace(ctx context.Context, correlationID string) context.Context {
	// Don't override an existing valid trace
	if trace.SpanContextFromContext(ctx).IsValid() {
		return ctx
	}

	traceID, ok := correlationIDToTraceID(correlationID)
	if !ok {
		return ctx
	}

	// Create a span context with the correlation ID as trace ID
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		TraceFlags: trace.FlagsSampled,
	})

	return trace.ContextWithSpanContext(ctx, spanCtx)
}

// StartSpan creates a new span with the given name, using the correlation ID as the trace ID
// if no existing trace is present. This is the primary entry point for external callers
// (like pushhookparser) to begin tracing a routing slip operation.
//
// The returned span should be ended with span.End() when the operation completes.
// The returned context should be passed to all subsequent slippy operations.
//
// Usage:
//
//	ctx, span := slippy.StartSpan(ctx, "ProcessPush", correlationID)
//	defer span.End()
//	// All slippy operations using ctx will be children of this span
//
//nolint:spancheck // Caller is responsible for calling span.End() - this is the API contract
func StartSpan(ctx context.Context, operationName, correlationID string) (context.Context, trace.Span) {
	// Ensure we have a trace context rooted in the correlation ID
	ctx = ContextWithCorrelationTrace(ctx, correlationID)

	return tracer().Start(ctx, operationName,
		trace.WithAttributes(
			attribute.String("slippy.correlation_id", correlationID),
		),
	)
}

// RetrySpan represents a traced retry operation with metrics collection.
type RetrySpan struct {
	ctx           context.Context
	span          trace.Span
	operationName string
	attempts      int
	totalBackoff  int64 // milliseconds
}

// startRetrySpan begins a new traced retry operation.
// The correlation ID is used as the trace ID, allowing all operations on the same
// routing slip to be correlated in the tracing backend.
// The span will capture retry attempts, backoff durations, and success/failure.
// The returned RetrySpan wraps the span and caller must call EndSuccess() or EndError().
//
//nolint:spancheck // Span is wrapped in RetrySpan; caller calls EndSuccess/EndError which calls span.End()
func startRetrySpan(ctx context.Context, operationName, correlationID string) *RetrySpan {
	// Try to use the correlation ID as the trace ID for better correlation
	// If the context already has a valid trace, we create a child span instead
	if !trace.SpanContextFromContext(ctx).IsValid() {
		if traceID, ok := correlationIDToTraceID(correlationID); ok {
			// Create a new span context with the correlation ID as trace ID
			spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
				TraceID:    traceID,
				TraceFlags: trace.FlagsSampled, // Ensure the trace is sampled
			})
			ctx = trace.ContextWithSpanContext(ctx, spanCtx)
		}
	}

	ctx, span := tracer().Start(ctx, operationName,
		trace.WithAttributes(
			attribute.String("slippy.operation", operationName),
			attribute.String("slippy.correlation_id", correlationID),
		),
	)

	return &RetrySpan{
		ctx:           ctx,
		span:          span,
		operationName: operationName,
		attempts:      0,
		totalBackoff:  0,
	}
}

// Context returns the context with the span attached.
func (r *RetrySpan) Context() context.Context {
	return r.ctx
}

// RecordAttempt records a retry attempt and the backoff duration that will follow.
func (r *RetrySpan) RecordAttempt(backoffMs int64) {
	r.attempts++
	r.totalBackoff += backoffMs

	// Add an event for each retry attempt
	r.span.AddEvent("retry_attempt",
		trace.WithAttributes(
			attribute.Int("attempt_number", r.attempts),
			attribute.Int64("backoff_ms", backoffMs),
			attribute.Int64("total_backoff_ms", r.totalBackoff),
		),
	)
}

// RecordVersionConflict records a version conflict error.
func (r *RetrySpan) RecordVersionConflict(expectedVersion, actualVersion int) {
	r.span.AddEvent("version_conflict",
		trace.WithAttributes(
			attribute.Int("expected_version", expectedVersion),
			attribute.Int("actual_version", actualVersion),
		),
	)
}

// EndSuccess marks the operation as successful and ends the span.
func (r *RetrySpan) EndSuccess() {
	r.span.SetAttributes(
		attribute.Int("slippy.retry.total_attempts", r.attempts),
		attribute.Int64("slippy.retry.total_backoff_ms", r.totalBackoff),
		attribute.Bool("slippy.retry.succeeded", true),
	)
	r.span.SetStatus(codes.Ok, "operation succeeded")
	r.span.End()
}

// EndError marks the operation as failed and ends the span.
func (r *RetrySpan) EndError(err error) {
	r.span.SetAttributes(
		attribute.Int("slippy.retry.total_attempts", r.attempts),
		attribute.Int64("slippy.retry.total_backoff_ms", r.totalBackoff),
		attribute.Bool("slippy.retry.succeeded", false),
	)
	r.span.RecordError(err)
	r.span.SetStatus(codes.Error, err.Error())
	r.span.End()
}

// EndWithStatus ends the span with a custom status.
func (r *RetrySpan) EndWithStatus(succeeded bool, message string) {
	r.span.SetAttributes(
		attribute.Int("slippy.retry.total_attempts", r.attempts),
		attribute.Int64("slippy.retry.total_backoff_ms", r.totalBackoff),
		attribute.Bool("slippy.retry.succeeded", succeeded),
	)
	if succeeded {
		r.span.SetStatus(codes.Ok, message)
	} else {
		r.span.SetStatus(codes.Error, message)
	}
	r.span.End()
}

// AddAttribute adds a custom attribute to the span.
func (r *RetrySpan) AddAttribute(key string, value interface{}) {
	switch v := value.(type) {
	case string:
		r.span.SetAttributes(attribute.String(key, v))
	case int:
		r.span.SetAttributes(attribute.Int(key, v))
	case int64:
		r.span.SetAttributes(attribute.Int64(key, v))
	case bool:
		r.span.SetAttributes(attribute.Bool(key, v))
	case float64:
		r.span.SetAttributes(attribute.Float64(key, v))
	}
}
