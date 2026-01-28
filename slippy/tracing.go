package slippy

import (
	"context"
	"encoding/hex"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	// tracerName is the instrumentation name for slippy tracing
	tracerName = "github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

// contextKey is a private type for context keys to avoid collisions.
type contextKey string

const (
	// serviceNameKey is the context key for the operational span service name.
	serviceNameKey contextKey = "slippy.operational.service_name"
)

// ========================================================================
// Span Configuration (Functional Options Pattern)
// ========================================================================

// spanConfig holds configuration for creating spans.
type spanConfig struct {
	serviceName string
	spanKind    trace.SpanKind
	attributes  []attribute.KeyValue
}

// SpanOption configures how a span is created.
type SpanOption func(*spanConfig)

// WithServiceName sets the service name attribute on the span.
// This helps identify which service generated the span in APM tools.
// Example: WithServiceName("pushhookparser") or WithServiceName("Slippy")
func WithServiceName(name string) SpanOption {
	return func(c *spanConfig) {
		c.serviceName = name
	}
}

// WithSpanKind sets the span kind (CLIENT, SERVER, PRODUCER, CONSUMER, INTERNAL).
// Use this to specify the span's role in the trace.
func WithSpanKind(kind trace.SpanKind) SpanOption {
	return func(c *spanConfig) {
		c.spanKind = kind
	}
}

// WithAttributes adds custom attributes to the span.
func WithAttributes(attrs ...attribute.KeyValue) SpanOption {
	return func(c *spanConfig) {
		c.attributes = append(c.attributes, attrs...)
	}
}

// applySpanOptions applies options to a spanConfig, returning a configured instance.
func applySpanOptions(opts ...SpanOption) *spanConfig {
	cfg := &spanConfig{
		spanKind: trace.SpanKindInternal, // default
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// ========================================================================
// Context-based Service Name (for automatic propagation)
// ========================================================================

// ContextWithServiceName returns a context with the service name set for operational spans.
// When operational spans are created (via StartOperationalSpan or startRetrySpan),
// they will include this service name as an attribute, making it easier to identify
// which service generated the span in APM tools.
//
// This is useful when you want all spans created with a context to automatically
// inherit the service name without passing it explicitly to each call.
//
// Usage:
//
//	ctx := slippy.ContextWithServiceName(ctx, "pushhookparser")
//	// All operational spans created with this ctx will have service.name = "pushhookparser"
func ContextWithServiceName(ctx context.Context, serviceName string) context.Context {
	return context.WithValue(ctx, serviceNameKey, serviceName)
}

// getServiceNameFromContext retrieves the service name from context, if set.
func getServiceNameFromContext(ctx context.Context) string {
	if v := ctx.Value(serviceNameKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ========================================================================
// Core Tracing Functions
// ========================================================================

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

// SpanKind represents the role of a span in a trace.
type SpanKind = trace.SpanKind

// Span kind constants for external use
const (
	SpanKindInternal = trace.SpanKindInternal // Default, for internal operations
	SpanKindClient   = trace.SpanKindClient   // For outbound calls (API, DB)
	SpanKindServer   = trace.SpanKindServer   // For handling incoming requests
	SpanKindProducer = trace.SpanKindProducer // For sending messages to a broker
	SpanKindConsumer = trace.SpanKindConsumer // For receiving messages from a broker
)

// StartSpan creates a new span with the given name, using the correlation ID as the trace ID
// if no existing trace is present. This is the primary entry point for creating pipeline
// content spans (JobExecution, Held, TestExecution, etc.).
//
// The returned span should be ended with span.End() when the operation completes.
// The returned context should be passed to all subsequent slippy operations.
//
// Options can be used to customize the span:
//   - WithServiceName("myservice"): Set the service.name attribute
//   - WithSpanKind(SpanKindProducer): Set the span kind
//   - WithAttributes(...): Add custom attributes
//
// Usage:
//
//	ctx, span := slippy.StartSpan(ctx, "ProcessPush", correlationID)
//	defer span.End()
//
//	// With options:
//	ctx, span := slippy.StartSpan(ctx, "ProcessPush", correlationID,
//	    slippy.WithServiceName("pushhookparser"),
//	    slippy.WithSpanKind(slippy.SpanKindProducer),
//	)
//	defer span.End()
//
//nolint:spancheck // Caller is responsible for calling span.End() - this is the API contract
func StartSpan(
	ctx context.Context,
	operationName, correlationID string,
	opts ...SpanOption,
) (context.Context, trace.Span) {
	cfg := applySpanOptions(opts...)

	// Ensure we have a trace context rooted in the correlation ID
	ctx = ContextWithCorrelationTrace(ctx, correlationID)

	// Build base attributes
	attrs := []attribute.KeyValue{
		attribute.String("slippy.correlation_id", correlationID),
	}

	// Add service name if provided via option or context
	serviceName := cfg.serviceName
	if serviceName == "" {
		serviceName = getServiceNameFromContext(ctx)
	}
	if serviceName != "" {
		attrs = append(attrs, attribute.String("service.name", serviceName))
	}

	// Add any custom attributes from options
	attrs = append(attrs, cfg.attributes...)

	// Build trace options
	traceOpts := []trace.SpanStartOption{
		trace.WithAttributes(attrs...),
	}
	if cfg.spanKind != trace.SpanKindInternal {
		traceOpts = append(traceOpts, trace.WithSpanKind(cfg.spanKind))
	}

	return tracer().Start(ctx, operationName, traceOpts...)
}

// StartOperationalSpan creates a span that is NOT a child of the pipeline trace.
// This is used for internal operational spans (database operations, retry loops, etc.)
// that should not clutter the pipeline content trace.
//
// The span is created as a new root span, but includes the correlation_id as a
// searchable attribute so it can be correlated with the pipeline trace when debugging.
//
// Options can be used to customize the span:
//   - WithServiceName("Slippy"): Set the service.name attribute (recommended for operational spans)
//   - WithAttributes(...): Add custom attributes
//
// If no service name is provided via option, it will check the context for a service name
// set via ContextWithServiceName.
//
// Use StartSpan for pipeline content spans (JobExecution, Held, TestExecution).
// Use StartOperationalSpan for internal implementation spans (UpdateStep, AppendHistory, hydrateSlip).
//
//nolint:spancheck // Caller is responsible for calling span.End() - this is the API contract
func StartOperationalSpan(
	ctx context.Context,
	operationName, correlationID string,
	opts ...SpanOption,
) (context.Context, trace.Span) {
	cfg := applySpanOptions(opts...)

	// Create a fresh context that preserves deadline/cancellation but strips the trace parent.
	// This ensures the operational span is a new root trace, not a child of the pipeline trace.
	freshCtx := trace.ContextWithSpan(ctx, nil)

	// Build attributes for the span
	attrs := []attribute.KeyValue{
		attribute.String("slippy.correlation_id", correlationID),
		attribute.String("slippy.span_type", "operational"),
	}

	// Add service name - check option first, then context
	serviceName := cfg.serviceName
	if serviceName == "" {
		serviceName = getServiceNameFromContext(ctx)
	}
	if serviceName != "" {
		attrs = append(attrs, attribute.String("service.name", serviceName))
	}

	// Add any custom attributes from options
	attrs = append(attrs, cfg.attributes...)

	// Start a new root span for this operational trace
	newCtx, span := tracer().Start(freshCtx, operationName,
		trace.WithAttributes(attrs...),
	)

	return newCtx, span
}

// RetrySpan represents a traced retry operation with metrics collection.
type RetrySpan struct {
	ctx           context.Context
	span          trace.Span
	operationName string
	attempts      int
	totalBackoff  int64 // milliseconds
}

// startRetrySpan begins a new traced retry operation in an OPERATIONAL trace.
// This creates spans in a separate trace from the pipeline content, keeping
// operational details (retries, version conflicts, etc.) out of the main pipeline trace.
//
// The correlation ID is stored as a searchable attribute (not as the trace ID),
// allowing operational traces to be correlated with pipeline traces when debugging.
//
// The returned RetrySpan wraps the span and caller must call EndSuccess() or EndError().
//
// Span is wrapped in RetrySpan; caller calls EndSuccess/EndError which calls span.End()
func startRetrySpan(ctx context.Context, operationName, correlationID string) *RetrySpan {
	// Create an operational span that is NOT a child of the pipeline trace.
	// This keeps operational spans (UpdateStep, AppendHistory, etc.) separate
	// from pipeline content spans (JobExecution, Held, TestExecution).
	ctx, span := StartOperationalSpan(ctx, operationName, correlationID)

	span.SetAttributes(
		attribute.String("slippy.operation", operationName),
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

// RecordJobExecutionSpan creates a synthetic span representing the job execution duration.
// This is called from PostJob to record the time between PreJob (startedAt) and PostJob (now).
// The span is created and immediately ended with the appropriate timestamps.
//
// Parameters:
//   - ctx: Context with trace information (should have correlation ID trace)
//   - correlationID: The routing slip correlation ID
//   - stepName: The step being executed (e.g., "builds", "unit-test")
//   - componentName: Optional component name for aggregate steps
//   - startedAt: When PreJob marked the step as running
//   - success: Whether the job succeeded
//   - errorMessage: Error message if the job failed
func RecordJobExecutionSpan(
	ctx context.Context,
	correlationID string,
	stepName string,
	componentName string,
	startedAt time.Time,
	success bool,
	errorMessage string,
) {
	// Ensure we have trace context from correlation ID
	ctx = ContextWithCorrelationTrace(ctx, correlationID)

	// Build span name: "JobExecution:step" or "JobExecution:step:component"
	spanName := "JobExecution:" + stepName
	if componentName != "" {
		spanName = spanName + ":" + componentName
	}

	// Create span with the start time from PreJob
	_, span := tracer().Start(ctx, spanName,
		trace.WithTimestamp(startedAt),
		trace.WithAttributes(
			attribute.String("slippy.correlation_id", correlationID),
			attribute.String("slippy.step", stepName),
			attribute.String("slippy.component", componentName),
			attribute.Bool("slippy.job.success", success),
			attribute.String("slippy.job.started_at", startedAt.Format(time.RFC3339)),
		),
	)

	// Set status based on success/failure
	if success {
		span.SetStatus(codes.Ok, "job_completed")
	} else {
		span.SetStatus(codes.Error, "job_failed")
		if errorMessage != "" {
			span.SetAttributes(attribute.String("slippy.job.error_message", errorMessage))
			span.RecordError(&jobError{message: errorMessage})
		}
	}

	// End the span now (this sets the end timestamp to time.Now())
	span.End()
}

// jobError implements error interface for recording job failures
type jobError struct {
	message string
}

func (e *jobError) Error() string {
	return e.message
}

// SerializedSpanContext represents a span context that can be serialized to/from JSON
// for passing between processes (e.g., from pre-job to post-job via Argo artifacts).
type SerializedSpanContext struct {
	TraceID    string `json:"trace_id"`
	SpanID     string `json:"span_id"`
	TraceFlags string `json:"trace_flags"`
}

// SerializeSpanContext extracts the current span context from the context and returns
// a serializable representation. This is used to pass trace context between processes.
//
// Usage in pre-job:
//
//	ctx, span := slippy.StartSpan(ctx, "Held", correlationID)
//	serialized := slippy.SerializeSpanContext(ctx)
//	// Write serialized to artifact file as JSON
func SerializeSpanContext(ctx context.Context) *SerializedSpanContext {
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.IsValid() {
		return nil
	}

	return &SerializedSpanContext{
		TraceID:    spanCtx.TraceID().String(),
		SpanID:     spanCtx.SpanID().String(),
		TraceFlags: spanCtx.TraceFlags().String(),
	}
}

// DeserializeSpanContext creates a context with the span context from a serialized representation.
// This allows post-job to continue the trace started by pre-job.
//
// Usage in post-job:
//
//	// Read serialized from artifact file
//	ctx := slippy.DeserializeSpanContext(context.Background(), serialized)
//	// Now ctx contains the parent trace context from pre-job
func DeserializeSpanContext(ctx context.Context, serialized *SerializedSpanContext) context.Context {
	if serialized == nil || serialized.TraceID == "" || serialized.SpanID == "" {
		return ctx
	}

	// Parse trace ID
	traceIDBytes, err := hex.DecodeString(serialized.TraceID)
	if err != nil || len(traceIDBytes) != 16 {
		return ctx
	}
	var traceID trace.TraceID
	copy(traceID[:], traceIDBytes)

	// Parse span ID
	spanIDBytes, err := hex.DecodeString(serialized.SpanID)
	if err != nil || len(spanIDBytes) != 8 {
		return ctx
	}
	var spanID trace.SpanID
	copy(spanID[:], spanIDBytes)

	// Parse trace flags (default to sampled)
	traceFlags := trace.FlagsSampled
	if serialized.TraceFlags != "" {
		flagByte, err := hex.DecodeString(serialized.TraceFlags)
		if err == nil && len(flagByte) == 1 {
			traceFlags = trace.TraceFlags(flagByte[0])
		}
	}

	// Create span context
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: traceFlags,
		Remote:     true, // Mark as remote since it's from another process
	})

	return trace.ContextWithSpanContext(ctx, spanCtx)
}
