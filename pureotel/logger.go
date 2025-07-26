package pureotel

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.20.0"
	"go.opentelemetry.io/otel/trace"
	klog "k8s.io/klog/v2"
)

// Logger constants
const (
	TimestampFormat = "2006-01-02 15:04:05"
	InfoLevel       = "info"
	DebugLevel      = "debug"
	ErrorLevel      = "error"
	WarnLevel       = "warn"

	// OTel environment variables for configuration
	OtelEndpointEnv     = "OTEL_EXPORTER_OTLP_ENDPOINT"
	OtelHeadersEnv      = "OTEL_EXPORTER_OTLP_HEADERS"
	otelSdkDisabled     = "OTEL_SDK_DISABLED"
	instrumentationName = "github.com/MyCarrier-DevOps/goLibMyCarrier/test/logger"
)

// LogLevel represents the severity of a log entry
type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

// String returns the string representation of the log level
func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return DebugLevel
	case LevelInfo:
		return InfoLevel
	case LevelWarn:
		return WarnLevel
	case LevelError:
		return ErrorLevel
	default:
		return InfoLevel
	}
}

// AppLogger interface for structured logging
type AppLogger interface {
	Info(args ...interface{})
	Infof(template string, args ...interface{})
	Debug(args ...interface{})
	Debugf(template string, args ...interface{})
	Error(args ...interface{})
	Errorf(template string, args ...interface{})
	Warn(args ...interface{})
	Warnf(template string, args ...interface{})
	With(key string, value interface{}) AppLogger
	Shutdown(ctx context.Context) error
}

// OtelLogger implements AppLogger with OpenTelemetry tracing integration
type OtelLogger struct {
	appName       string
	appVersion    string
	logLevel      LogLevel
	attributes    map[string]interface{}
	fallbackLog   *log.Logger
	useOtel       bool
	tracer        trace.Tracer
	traceProvider *sdktrace.TracerProvider
}

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp  string                 `json:"timestamp"`
	Level      string                 `json:"level"`
	Message    string                 `json:"message"`
	AppName    string                 `json:"app_name"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
}

// NewAppLogger returns a new AppLogger instance with OpenTelemetry integration
func NewAppLogger() AppLogger {
	// Set default log level and app name
	logLevelStr, _ := os.LookupEnv("LOG_LEVEL")
	if logLevelStr == "" {
		logLevelStr = InfoLevel
	}
	appName, _ := os.LookupEnv("LOG_APP_NAME")
	if appName == "" {
		appName = "test_application_abcd"
	}
	appVersion, _ := os.LookupEnv("LOG_APP_VERSION")
	if appVersion == "" {
		appVersion = "1.0.0"
	}

	logLevel := parseLogLevel(logLevelStr)

	// Check if OpenTelemetry is disabled
	otelSdkDisabled, defined := os.LookupEnv(otelSdkDisabled)
	useOtel := !(defined && strings.ToLower(otelSdkDisabled) == "true")

	// Create fallback standard logger
	fallbackLog := log.New(os.Stdout, fmt.Sprintf("[%s] ", appName), log.LstdFlags)

	otelLogger := &OtelLogger{
		appName:     appName,
		appVersion:  appVersion,
		logLevel:    logLevel,
		attributes:  make(map[string]interface{}),
		fallbackLog: fallbackLog,
		useOtel:     useOtel,
	}

	// Initialize OpenTelemetry if enabled
	if useOtel {
		if err := otelLogger.initOtel(); err != nil {
			otelLogger.fallbackLog.Printf("Failed to initialize OpenTelemetry, falling back to standard logging: %v", err)
			otelLogger.useOtel = false
		}
	}

	return otelLogger
}

// initOtel initializes the OpenTelemetry SDK with OTLP exporter
func (l *OtelLogger) initOtel() error {
	ctx := context.Background()

	// Check if OTLP endpoint is configured
	var otlpEndpoint string

	// Use OTEL_HOST_IP if set, otherwise use OTEL_EXPORTER_OTLP_ENDPOINT directly
	if hostIP := os.Getenv("OTEL_HOST_IP"); hostIP != "" {
		otlpEndpoint = hostIP
		// Add port if OTEL_HOST_PORT is set and hostIP doesn't already contain a port
		if port := os.Getenv("OTEL_HOST_PORT"); port != "" && !strings.Contains(hostIP, ":") {
			otlpEndpoint = hostIP + ":" + port
		}
	} else {
		otlpEndpoint = os.Getenv(OtelEndpointEnv)
	}

	if otlpEndpoint == "" {
		l.fallbackLog.Printf("OpenTelemetry enabled but no OTLP endpoint configured, using noop exporter")
		l.tracer = otel.Tracer(instrumentationName)
		return nil
	}

	// Clean up endpoint URL (remove trailing slashes and ensure proper format)
	otlpEndpoint = strings.TrimSuffix(otlpEndpoint, "/")
	if !strings.HasPrefix(otlpEndpoint, "http://") && !strings.HasPrefix(otlpEndpoint, "https://") {
		otlpEndpoint = "http://" + otlpEndpoint
	}

	l.fallbackLog.Printf("Using OTLP endpoint: %s", otlpEndpoint)

	// Create resource with service information and Kubernetes attributes
	resourceAttrs := []attribute.KeyValue{
		semconv.ServiceName(l.appName),
		semconv.ServiceVersion(l.appVersion),
		attribute.String("service.instance.id", fmt.Sprintf("%s-%d", l.appName, time.Now().Unix())),
	}

	// Add Kubernetes attributes from environment variables
	if nodeName := os.Getenv("OTEL_RESOURCE_ATTRIBUTES_NODE_NAME"); nodeName != "" {
		resourceAttrs = append(resourceAttrs, attribute.String("k8s.node.name", nodeName))
	}
	if podName := os.Getenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAME"); podName != "" {
		resourceAttrs = append(resourceAttrs, attribute.String("k8s.pod.name", podName))
	}
	if podNamespace := os.Getenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAMESPACE"); podNamespace != "" {
		resourceAttrs = append(resourceAttrs, attribute.String("k8s.namespace.name", podNamespace))
	}
	if podUID := os.Getenv("OTEL_RESOURCE_ATTRIBUTES_POD_UID"); podUID != "" {
		resourceAttrs = append(resourceAttrs, attribute.String("k8s.pod.uid", podUID))
	}
	if podIP := os.Getenv("OTEL_RESOURCE_ATTRIBUTES_POD_IP"); podIP != "" {
		resourceAttrs = append(resourceAttrs, attribute.String("k8s.pod.ip", podIP))
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			resourceAttrs...,
		),
	)
	if err != nil {
		// If merge fails due to schema conflicts, create a new resource with the same attributes
		res = resource.NewWithAttributes(
			semconv.SchemaURL,
			resourceAttrs...,
		)
	}

	// Create OTLP HTTP trace exporter with explicit endpoint configuration
	var traceExporter sdktrace.SpanExporter
	if strings.HasPrefix(otlpEndpoint, "https://") {
		// Use HTTPS
		traceExporter, err = otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(otlpEndpoint),
		)
	} else {
		// Use HTTP (insecure)
		traceExporter, err = otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(otlpEndpoint),
			otlptracehttp.WithInsecure(),
		)
	}
	if err != nil {
		return fmt.Errorf("failed to create OTLP trace exporter: %w", err)
	}

	// Create trace provider
	l.traceProvider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)

	// Set global trace provider
	otel.SetTracerProvider(l.traceProvider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Create tracer
	l.tracer = l.traceProvider.Tracer(instrumentationName)

	l.fallbackLog.Printf("OpenTelemetry initialized with OTLP endpoint: %s", otlpEndpoint)
	return nil
}

// parseLogLevel converts string to LogLevel
func parseLogLevel(level string) LogLevel {
	switch strings.ToLower(level) {
	case DebugLevel:
		return LevelDebug
	case InfoLevel:
		return LevelInfo
	case WarnLevel:
		return LevelWarn
	case ErrorLevel:
		return LevelError
	default:
		return LevelInfo
	}
}

// shouldLog checks if the message should be logged based on the log level
func (l *OtelLogger) shouldLog(level LogLevel) bool {
	return level >= l.logLevel
}

// log is the core logging function
func (l *OtelLogger) log(level LogLevel, message string) {
	if !l.shouldLog(level) {
		return
	}

	// Always log to structured output
	l.logStructured(level, message)

	// Also create OpenTelemetry span if enabled
	if l.useOtel && l.tracer != nil {
		l.createOtelSpan(level, message)
	}
}

// logStructured outputs structured JSON logs
func (l *OtelLogger) logStructured(level LogLevel, message string) {
	logEntry := LogEntry{
		Timestamp: time.Now().Format(time.RFC3339),
		Level:     level.String(),
		Message:   message,
		AppName:   l.appName,
	}

	// Add attributes if any
	if len(l.attributes) > 0 {
		logEntry.Attributes = make(map[string]interface{})
		for k, v := range l.attributes {
			logEntry.Attributes[k] = v
		}
	}

	// Output as JSON for structured logging
	if jsonBytes, err := json.Marshal(logEntry); err == nil {
		fmt.Println(string(jsonBytes))
	} else {
		// Fallback to simple logging if JSON marshaling fails
		l.logFallback(level, message)
	}
}

// createOtelSpan creates an OpenTelemetry span that contains log data
// This sends structured log data to the OTLP collector as trace events
func (l *OtelLogger) createOtelSpan(level LogLevel, message string) {
	if l.tracer == nil {
		return
	}

	ctx := context.Background()
	spanName := fmt.Sprintf("log.%s", level.String())

	_, span := l.tracer.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()

	// Set span attributes with log data
	attrs := []attribute.KeyValue{
		attribute.String("log.severity", level.String()),
		attribute.String("log.body", message),
		attribute.String("service.name", l.appName),
		attribute.String("log.timestamp", time.Now().Format(time.RFC3339)),
		attribute.String("instrumentation.name", instrumentationName),
	}

	// Add custom attributes
	for k, v := range l.attributes {
		attrs = append(attrs, attribute.String(fmt.Sprintf("log.%s", k), fmt.Sprintf("%v", v)))
	}

	span.SetAttributes(attrs...)

	// Add the log message as an event (this will appear as structured data in the collector)
	span.AddEvent("log", trace.WithAttributes(
		attribute.String("level", level.String()),
		attribute.String("message", message),
		attribute.String("timestamp", time.Now().Format(time.RFC3339)),
	))

	// Set span status based on log level
	if level == LevelError {
		span.RecordError(fmt.Errorf("log error: %s", message))
	}
}

// logFallback uses standard library logging as fallback
func (l *OtelLogger) logFallback(level LogLevel, message string) {
	timestamp := time.Now().Format(TimestampFormat)
	attrs := ""
	if len(l.attributes) > 0 {
		var attrPairs []string
		for k, v := range l.attributes {
			attrPairs = append(attrPairs, fmt.Sprintf("%s=%v", k, v))
		}
		attrs = fmt.Sprintf(" [%s]", strings.Join(attrPairs, ", "))
	}

	l.fallbackLog.Printf("%s [%s]%s %s", timestamp, strings.ToUpper(level.String()), attrs, message)
}

// Info logs an info level message
func (l *OtelLogger) Info(args ...interface{}) {
	l.log(LevelInfo, fmt.Sprint(args...))
}

// Infof logs an info level message with formatting
func (l *OtelLogger) Infof(template string, args ...interface{}) {
	l.log(LevelInfo, fmt.Sprintf(template, args...))
}

// Debug logs a debug level message
func (l *OtelLogger) Debug(args ...interface{}) {
	l.log(LevelDebug, fmt.Sprint(args...))
}

// Debugf logs a debug level message with formatting
func (l *OtelLogger) Debugf(template string, args ...interface{}) {
	l.log(LevelDebug, fmt.Sprintf(template, args...))
}

// Error logs an error level message
func (l *OtelLogger) Error(args ...interface{}) {
	l.log(LevelError, fmt.Sprint(args...))
}

// Errorf logs an error level message with formatting
func (l *OtelLogger) Errorf(template string, args ...interface{}) {
	l.log(LevelError, fmt.Sprintf(template, args...))
}

// Warn logs a warning level message
func (l *OtelLogger) Warn(args ...interface{}) {
	l.log(LevelWarn, fmt.Sprint(args...))
}

// Warnf logs a warning level message with formatting
func (l *OtelLogger) Warnf(template string, args ...interface{}) {
	l.log(LevelWarn, fmt.Sprintf(template, args...))
}

// With adds a key-value pair to the logger context
func (l *OtelLogger) With(key string, value interface{}) AppLogger {
	newLogger := &OtelLogger{
		appName:       l.appName,
		appVersion:    l.appVersion,
		logLevel:      l.logLevel,
		attributes:    make(map[string]interface{}),
		fallbackLog:   l.fallbackLog,
		useOtel:       l.useOtel,
		tracer:        l.tracer,
		traceProvider: l.traceProvider,
	}

	// Copy existing attributes
	for k, v := range l.attributes {
		newLogger.attributes[k] = v
	}

	// Add new attribute
	newLogger.attributes[key] = value

	return newLogger
}

// Shutdown gracefully shuts down the OpenTelemetry tracer provider
func (l *OtelLogger) Shutdown(ctx context.Context) error {
	if l.traceProvider != nil {
		return l.traceProvider.Shutdown(ctx)
	}
	return nil
}

func SetKlogLevel(level int) {
	klog.InitFlags(nil)
	_ = flag.Set("v", strconv.Itoa(level))
}

type loggerKey struct{}

// WithLogger returns a copy of parent context in which the
// value associated with logger key is the supplied logger.
func WithLogger(ctx context.Context, logger AppLogger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

// FromContext returns the logger in the context.
func FromContext(ctx context.Context) AppLogger {
	if logger, ok := ctx.Value(loggerKey{}).(AppLogger); ok {
		return logger
	}
	return NewAppLogger()
}
