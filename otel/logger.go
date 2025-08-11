package otel

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	stdlog "log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
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

// OtelLogger implements AppLogger with structured logging and OpenTelemetry logs integration
type OtelLogger struct {
	appName       string
	appVersion    string
	logLevel      LogLevel
	attributes    map[string]interface{}
	fallbackLog   *stdlog.Logger
	useOtel       bool
	tracer        trace.Tracer
	traceProvider *sdktrace.TracerProvider
	logger        otellog.Logger
	logProvider   *sdklog.LoggerProvider
}

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp  string                 `json:"timestamp"`
	Level      string                 `json:"level"`
	Message    string                 `json:"message"`
	AppName    string                 `json:"app_name"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
}

// NewAppLogger returns a new AppLogger instance with structured logging
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
	useOtel := !defined || strings.ToLower(otelSdkDisabled) != "true"

	// Create fallback standard logger
	fallbackLog := stdlog.New(os.Stdout, fmt.Sprintf("[%s] ", appName), stdlog.LstdFlags)

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
			otelLogger.fallbackLog.Printf(
				"Failed to initialize OpenTelemetry, falling back to standard logging: %v",
				err,
			)
			otelLogger.useOtel = false
		}
	}

	return otelLogger
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
		logger:        l.logger,
		logProvider:   l.logProvider,
	}

	// Copy existing attributes
	for k, v := range l.attributes {
		newLogger.attributes[k] = v
	}

	// Add new attribute
	newLogger.attributes[key] = value

	return newLogger
}

// Shutdown gracefully shuts down the OpenTelemetry providers
func (l *OtelLogger) Shutdown(ctx context.Context) error {
	var err error
	if l.traceProvider != nil {
		if shutdownErr := l.traceProvider.Shutdown(ctx); shutdownErr != nil {
			err = shutdownErr
		}
	}
	if l.logProvider != nil {
		if shutdownErr := l.logProvider.Shutdown(ctx); shutdownErr != nil {
			err = shutdownErr
		}
	}
	return err
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

// GinLoggerMiddleware creates a Gin middleware that uses otel logger
func GinLoggerMiddleware(logger AppLogger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Start timer
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// Process request
		c.Next()

		// Log request details
		end := time.Now()
		latency := end.Sub(start)
		clientIP := c.ClientIP()
		method := c.Request.Method
		statusCode := c.Writer.Status()

		if raw != "" {
			path = path + "?" + raw
		}

		logger.Infof("%s - [%s] \"%s %s\" %d %v",
			clientIP,
			end.Format("02/Jan/2006:15:04:05 -0700"),
			method,
			path,
			statusCode,
			latency,
		)
	}
}

// formatOtlpEndpoint formats the OTLP endpoint URL
func (l *OtelLogger) formatOtlpEndpoint(endpoint string) string {
	if endpoint == "" {
		return ""
	}

	// Clean up endpoint URL (remove trailing slashes and ensure proper format)
	endpoint = strings.TrimSuffix(endpoint, "/")
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}
	return endpoint
}

// getOtlpEndpoint determines the OTLP endpoint from environment variables
func (l *OtelLogger) getOtlpEndpoint() string {
	// Use OTEL_HOST_IP if set, otherwise use OTEL_EXPORTER_OTLP_ENDPOINT directly
	hostIP := os.Getenv("OTEL_HOST_IP")
	if hostIP != "" {
		endpoint := hostIP
		// Add port if OTEL_HOST_PORT is set and hostIP doesn't already contain a port
		if port := os.Getenv("OTEL_HOST_PORT"); port != "" && !strings.Contains(hostIP, ":") {
			endpoint = hostIP + ":" + port
		}
		return l.formatOtlpEndpoint(endpoint)
	}

	return l.formatOtlpEndpoint(os.Getenv(OtelEndpointEnv))
}

// handleDisabledOtel handles the case when OpenTelemetry is disabled
func (l *OtelLogger) handleDisabledOtel() error {
	endpoint := l.getOtlpEndpoint()
	if endpoint != "" {
		l.fallbackLog.Printf("Using OTLP endpoint: %s", endpoint)
	}
	l.fallbackLog.Printf("OpenTelemetry SDK is disabled, skipping initialization")
	return nil
}

// initOtel initializes the OpenTelemetry SDK with OTLP trace and log exporters
func (l *OtelLogger) initOtel() error {
	// Check if OpenTelemetry SDK is disabled
	if otelSdkDisabled := os.Getenv("OTEL_SDK_DISABLED"); strings.ToLower(otelSdkDisabled) == "true" {
		return l.handleDisabledOtel()
	}

	// Get and validate OTLP endpoint
	otlpEndpoint := l.getOtlpEndpoint()
	if otlpEndpoint == "" {
		l.fallbackLog.Printf("OpenTelemetry enabled but no OTLP endpoint configured, using noop exporter")
		l.tracer = otel.Tracer(instrumentationName)
		return nil
	}

	l.fallbackLog.Printf("Using OTLP endpoint: %s", otlpEndpoint)
	return l.initOtelProviders(otlpEndpoint)
}

// initOtelProviders initializes the OpenTelemetry providers with the given endpoint
func (l *OtelLogger) initOtelProviders(otlpEndpoint string) error {
	ctx := context.Background()

	// Create resource with service information and Kubernetes attributes
	resourceAttrs := []attribute.KeyValue{
		semconv.ServiceName(l.appName),
		semconv.ServiceVersion(l.appVersion),
		attribute.String("service.instance.id", fmt.Sprintf("%s-%d", l.appName, time.Now().Unix())),
	}

	// Add Kubernetes attributes from environment variables
	l.addKubernetesAttributes(&resourceAttrs)

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

	// Create exporters
	traceExporter, logExporter, err := l.createExporters(ctx, otlpEndpoint)
	if err != nil {
		return err
	}

	// Create and set providers
	l.traceProvider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)

	l.logProvider = sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)

	// Set global providers
	otel.SetTracerProvider(l.traceProvider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Create tracer and logger
	l.tracer = l.traceProvider.Tracer(instrumentationName)
	l.logger = l.logProvider.Logger(instrumentationName)

	l.fallbackLog.Printf("OpenTelemetry initialized with OTLP endpoint: %s", otlpEndpoint)
	return nil
}

// addKubernetesAttributes adds Kubernetes-related attributes to the resource
func (l *OtelLogger) addKubernetesAttributes(resourceAttrs *[]attribute.KeyValue) {
	if nodeName := os.Getenv("OTEL_RESOURCE_ATTRIBUTES_NODE_NAME"); nodeName != "" {
		*resourceAttrs = append(*resourceAttrs, attribute.String("k8s.node.name", nodeName))
	}
	if podName := os.Getenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAME"); podName != "" {
		*resourceAttrs = append(*resourceAttrs, attribute.String("k8s.pod.name", podName))
	}
	if podNamespace := os.Getenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAMESPACE"); podNamespace != "" {
		*resourceAttrs = append(*resourceAttrs, attribute.String("k8s.namespace.name", podNamespace))
	}
	if podUID := os.Getenv("OTEL_RESOURCE_ATTRIBUTES_POD_UID"); podUID != "" {
		*resourceAttrs = append(*resourceAttrs, attribute.String("k8s.pod.uid", podUID))
	}
	if podIP := os.Getenv("OTEL_RESOURCE_ATTRIBUTES_POD_IP"); podIP != "" {
		*resourceAttrs = append(*resourceAttrs, attribute.String("k8s.pod.ip", podIP))
	}
}

// createExporters creates the trace and log exporters
func (l *OtelLogger) createExporters(ctx context.Context, otlpEndpoint string) (
	sdktrace.SpanExporter,
	sdklog.Exporter,
	error,
) {
	// Extract just the host:port from the endpoint URL
	endpointURL := otlpEndpoint
	var isSecure bool
	switch {
	case strings.HasPrefix(otlpEndpoint, "https://"):
		endpointURL = strings.TrimPrefix(otlpEndpoint, "https://")
		isSecure = true
	case strings.HasPrefix(otlpEndpoint, "http://"):
		endpointURL = strings.TrimPrefix(otlpEndpoint, "http://")
		isSecure = false
	default:
		// No scheme, assume HTTP
		isSecure = false
	}

	// Create OTLP HTTP trace exporter
	var traceExporter sdktrace.SpanExporter
	var err error
	if isSecure {
		traceExporter, err = otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(endpointURL),
		)
	} else {
		traceExporter, err = otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(endpointURL),
			otlptracehttp.WithInsecure(),
		)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
	}

	// Create OTLP HTTP log exporter
	var logExporter sdklog.Exporter
	if isSecure {
		logExporter, err = otlploghttp.New(ctx,
			otlploghttp.WithEndpoint(endpointURL),
		)
	} else {
		logExporter, err = otlploghttp.New(ctx,
			otlploghttp.WithEndpoint(endpointURL),
			otlploghttp.WithInsecure(),
		)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create OTLP log exporter: %w", err)
	}

	return traceExporter, logExporter, nil
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

	// Also send to OpenTelemetry logs if enabled
	if l.useOtel && l.logger != nil {
		l.sendOtelLog(level, message)
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
		l.fallbackLog.Println(string(jsonBytes))
	} else {
		// Fallback to simple logging if JSON marshaling fails
		l.logFallback(level, message)
	}
}

// sendOtelLog sends log records to OpenTelemetry logs
func (l *OtelLogger) sendOtelLog(level LogLevel, message string) {
	if l.logger == nil {
		return
	}

	// Convert our log level to OpenTelemetry severity
	var severity otellog.Severity
	switch level {
	case LevelDebug:
		severity = otellog.SeverityDebug
	case LevelInfo:
		severity = otellog.SeverityInfo
	case LevelWarn:
		severity = otellog.SeverityWarn
	case LevelError:
		severity = otellog.SeverityError
	default:
		severity = otellog.SeverityInfo
	}

	// Create log record
	logRecord := otellog.Record{}
	logRecord.SetTimestamp(time.Now())
	logRecord.SetSeverity(severity)
	logRecord.SetSeverityText(level.String())
	logRecord.SetBody(otellog.StringValue(message))

	// Add service attributes
	logRecord.AddAttributes(
		otellog.String("service.name", l.appName),
		otellog.String("service.version", l.appVersion),
	)

	// Add custom attributes
	for k, v := range l.attributes {
		logRecord.AddAttributes(otellog.String(k, fmt.Sprintf("%v", v)))
	}

	// Emit the log record
	l.logger.Emit(context.Background(), logRecord)
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
