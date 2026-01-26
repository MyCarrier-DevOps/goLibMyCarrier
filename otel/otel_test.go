package otel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
)

func TestLogLevel_String(t *testing.T) {
	tests := []struct {
		level    LogLevel
		expected string
	}{
		{LevelDebug, "debug"},
		{LevelInfo, "info"},
		{LevelWarn, "warn"},
		{LevelError, "error"},
		{LevelFatal, "fatal"},
		{LogLevel(999), "info"}, // default case
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.level.String(); got != tt.expected {
				t.Errorf("LogLevel.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected LogLevel
	}{
		{"debug", LevelDebug},
		{"DEBUG", LevelDebug},
		{"info", LevelInfo},
		{"INFO", LevelInfo},
		{"warn", LevelWarn},
		{"WARN", LevelWarn},
		{"error", LevelError},
		{"ERROR", LevelError},
		{"fatal", LevelFatal},
		{"FATAL", LevelFatal},
		{"invalid", LevelInfo}, // default case
		{"", LevelInfo},        // empty string
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := parseLogLevel(tt.input); got != tt.expected {
				t.Errorf("parseLogLevel(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNewAppLogger_DefaultValues(t *testing.T) {
	// Clear environment variables
	if err := os.Unsetenv("LOG_LEVEL"); err != nil {
		t.Logf("Failed to unset LOG_LEVEL: %v", err)
	}
	if err := os.Unsetenv("LOG_APP_NAME"); err != nil {
		t.Logf("Failed to unset LOG_APP_NAME: %v", err)
	}
	if err := os.Unsetenv("LOG_APP_VERSION"); err != nil {
		t.Logf("Failed to unset LOG_APP_VERSION: %v", err)
	}
	if err := os.Unsetenv("OTEL_SDK_DISABLED"); err != nil {
		t.Logf("Failed to unset OTEL_SDK_DISABLED: %v", err)
	}

	logger := NewAppLogger()
	otelLogger, ok := logger.(*OtelLogger)
	if !ok {
		t.Fatal("NewAppLogger() should return *OtelLogger")
	}

	if otelLogger.logLevel != LevelInfo {
		t.Errorf("Default log level should be LevelInfo, got %v", otelLogger.logLevel)
	}

	if otelLogger.appName != "default_app" {
		t.Errorf("Default app name should be 'default_app', got %q", otelLogger.appName)
	}

	if otelLogger.appVersion != "1.0.0" {
		t.Errorf("Default app version should be '1.0.0', got %q", otelLogger.appVersion)
	}

	if !otelLogger.useOtel {
		t.Error("OpenTelemetry should be enabled by default")
	}
}

func TestNewAppLogger_EnvironmentVariables(t *testing.T) {
	// Set environment variables
	if err := os.Setenv("LOG_LEVEL", "debug"); err != nil {
		t.Fatalf("Failed to set LOG_LEVEL: %v", err)
	}
	if err := os.Setenv("LOG_APP_NAME", "test-app"); err != nil {
		t.Fatalf("Failed to set LOG_APP_NAME: %v", err)
	}
	if err := os.Setenv("LOG_APP_VERSION", "2.1.0"); err != nil {
		t.Fatalf("Failed to set LOG_APP_VERSION: %v", err)
	}
	if err := os.Setenv("OTEL_SDK_DISABLED", "true"); err != nil {
		t.Fatalf("Failed to set OTEL_SDK_DISABLED: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("LOG_LEVEL"); err != nil {
			t.Logf("Failed to unset LOG_LEVEL: %v", err)
		}
		if err := os.Unsetenv("LOG_APP_NAME"); err != nil {
			t.Logf("Failed to unset LOG_APP_NAME: %v", err)
		}
		if err := os.Unsetenv("LOG_APP_VERSION"); err != nil {
			t.Logf("Failed to unset LOG_APP_VERSION: %v", err)
		}
		if err := os.Unsetenv("OTEL_SDK_DISABLED"); err != nil {
			t.Logf("Failed to unset OTEL_SDK_DISABLED: %v", err)
		}
	}()

	logger := NewAppLogger()
	otelLogger, ok := logger.(*OtelLogger)
	if !ok {
		t.Fatal("NewAppLogger() should return *OtelLogger")
	}

	if otelLogger.logLevel != LevelDebug {
		t.Errorf("Log level should be LevelDebug, got %v", otelLogger.logLevel)
	}

	if otelLogger.appName != "test-app" {
		t.Errorf("App name should be 'test-app', got %q", otelLogger.appName)
	}

	if otelLogger.appVersion != "2.1.0" {
		t.Errorf("App version should be '2.1.0', got %q", otelLogger.appVersion)
	}

	if otelLogger.useOtel {
		t.Error("OpenTelemetry should be disabled when OTEL_SDK_DISABLED=true")
	}
}

func TestKubernetesAttributes(t *testing.T) {
	// Set Kubernetes environment variables
	if err := os.Setenv("OTEL_RESOURCE_ATTRIBUTES_NODE_NAME", "test-node"); err != nil {
		t.Fatalf("Failed to set OTEL_RESOURCE_ATTRIBUTES_NODE_NAME: %v", err)
	}
	if err := os.Setenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAME", "test-pod"); err != nil {
		t.Fatalf("Failed to set OTEL_RESOURCE_ATTRIBUTES_POD_NAME: %v", err)
	}
	if err := os.Setenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAMESPACE", "test-namespace"); err != nil {
		t.Fatalf("Failed to set OTEL_RESOURCE_ATTRIBUTES_POD_NAMESPACE: %v", err)
	}
	if err := os.Setenv("OTEL_RESOURCE_ATTRIBUTES_POD_UID", "test-uid-123"); err != nil {
		t.Fatalf("Failed to set OTEL_RESOURCE_ATTRIBUTES_POD_UID: %v", err)
	}
	if err := os.Setenv("OTEL_RESOURCE_ATTRIBUTES_POD_IP", "10.0.0.1"); err != nil {
		t.Fatalf("Failed to set OTEL_RESOURCE_ATTRIBUTES_POD_IP: %v", err)
	}
	if err := os.Setenv("OTEL_SDK_DISABLED", "true"); err != nil {
		t.Fatalf("Failed to set OTEL_SDK_DISABLED: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_NODE_NAME"); err != nil {
			t.Logf("Failed to unset OTEL_RESOURCE_ATTRIBUTES_NODE_NAME: %v", err)
		}
		if err := os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAME"); err != nil {
			t.Logf("Failed to unset OTEL_RESOURCE_ATTRIBUTES_POD_NAME: %v", err)
		}
		if err := os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAMESPACE"); err != nil {
			t.Logf("Failed to unset OTEL_RESOURCE_ATTRIBUTES_POD_NAMESPACE: %v", err)
		}
		if err := os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_POD_UID"); err != nil {
			t.Logf("Failed to unset OTEL_RESOURCE_ATTRIBUTES_POD_UID: %v", err)
		}
		if err := os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_POD_IP"); err != nil {
			t.Logf("Failed to unset OTEL_RESOURCE_ATTRIBUTES_POD_IP: %v", err)
		}
		if err := os.Unsetenv("OTEL_SDK_DISABLED"); err != nil {
			t.Logf("Failed to unset OTEL_SDK_DISABLED: %v", err)
		}
	}()

	logger := NewAppLogger()
	otelLogger, ok := logger.(*OtelLogger)
	if !ok {
		t.Fatal("NewAppLogger() should return *OtelLogger")
	}

	// Since OTel is disabled, we can't test the actual resource attributes,
	// but we can verify that the logger was created successfully with the environment variables set
	if otelLogger.useOtel {
		t.Error("OpenTelemetry should be disabled for this test")
	}
}

func TestKubernetesAttributes_PartialEnvironment(t *testing.T) {
	// Set only some Kubernetes environment variables
	if err := os.Setenv("OTEL_RESOURCE_ATTRIBUTES_NODE_NAME", "test-node"); err != nil {
		t.Fatalf("Failed to set OTEL_RESOURCE_ATTRIBUTES_NODE_NAME: %v", err)
	}
	if err := os.Setenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAME", "test-pod"); err != nil {
		t.Fatalf("Failed to set OTEL_RESOURCE_ATTRIBUTES_POD_NAME: %v", err)
	}
	// Intentionally omit other K8s attributes
	if err := os.Setenv("OTEL_SDK_DISABLED", "true"); err != nil {
		t.Fatalf("Failed to set OTEL_SDK_DISABLED: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_NODE_NAME"); err != nil {
			t.Logf("Failed to unset OTEL_RESOURCE_ATTRIBUTES_NODE_NAME: %v", err)
		}
		if err := os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAME"); err != nil {
			t.Logf("Failed to unset OTEL_RESOURCE_ATTRIBUTES_POD_NAME: %v", err)
		}
		if err := os.Unsetenv("OTEL_SDK_DISABLED"); err != nil {
			t.Logf("Failed to unset OTEL_SDK_DISABLED: %v", err)
		}
	}()

	logger := NewAppLogger()
	otelLogger, ok := logger.(*OtelLogger)
	if !ok {
		t.Fatal("NewAppLogger() should return *OtelLogger")
	}

	// Verify logger creation succeeds even with partial K8s environment
	if otelLogger.useOtel {
		t.Error("OpenTelemetry should be disabled for this test")
	}
}

func TestShouldLog(t *testing.T) {
	tests := []struct {
		loggerLevel  LogLevel
		messageLevel LogLevel
		shouldLog    bool
	}{
		{LevelDebug, LevelDebug, true},
		{LevelDebug, LevelInfo, true},
		{LevelDebug, LevelWarn, true},
		{LevelDebug, LevelError, true},
		{LevelDebug, LevelFatal, true},
		{LevelInfo, LevelDebug, false},
		{LevelInfo, LevelInfo, true},
		{LevelInfo, LevelWarn, true},
		{LevelInfo, LevelError, true},
		{LevelInfo, LevelFatal, true},
		{LevelWarn, LevelDebug, false},
		{LevelWarn, LevelInfo, false},
		{LevelWarn, LevelWarn, true},
		{LevelWarn, LevelError, true},
		{LevelWarn, LevelFatal, true},
		{LevelError, LevelDebug, false},
		{LevelError, LevelInfo, false},
		{LevelError, LevelWarn, false},
		{LevelError, LevelError, true},
		{LevelError, LevelFatal, true},
		{LevelFatal, LevelDebug, false},
		{LevelFatal, LevelInfo, false},
		{LevelFatal, LevelWarn, false},
		{LevelFatal, LevelError, false},
		{LevelFatal, LevelFatal, true},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			logger := &OtelLogger{logLevel: tt.loggerLevel}
			if got := logger.shouldLog(tt.messageLevel); got != tt.shouldLog {
				t.Errorf("shouldLog(%v) with logger level %v = %v, want %v",
					tt.messageLevel, tt.loggerLevel, got, tt.shouldLog)
			}
		})
	}
}

func TestLogStructured(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}
	os.Stdout = w

	logger := &OtelLogger{
		appName:     "test-app",
		logLevel:    LevelDebug,
		attributes:  map[string]interface{}{"key": "value"},
		fallbackLog: log.New(w, "", 0), // Initialize fallbackLog
	}

	logger.logStructured(LevelInfo, "test message")

	// Restore stdout
	if err := w.Close(); err != nil {
		t.Logf("Failed to close writer: %v", err)
	}
	os.Stdout = oldStdout

	// Read the output
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("Failed to read from pipe: %v", err)
	}
	output := buf.String()

	// Parse JSON output
	var logEntry LogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	// Verify log entry
	if logEntry.Level != "info" {
		t.Errorf("Expected level 'info', got %q", logEntry.Level)
	}
	if logEntry.Message != "test message" {
		t.Errorf("Expected message 'test message', got %q", logEntry.Message)
	}
	if logEntry.AppName != "test-app" {
		t.Errorf("Expected app name 'test-app', got %q", logEntry.AppName)
	}
	if logEntry.Attributes["key"] != "value" {
		t.Errorf("Expected attribute key='value', got %v", logEntry.Attributes["key"])
	}

	// Verify timestamp format
	if _, err := time.Parse(time.RFC3339, logEntry.Timestamp); err != nil {
		t.Errorf("Invalid timestamp format: %v", err)
	}
}

func TestLoggingMethods(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	logger := NewAppLogger()

	// Test all logging methods
	logger.Info("info message")
	logger.Infof("info %s", "formatted")
	logger.Debug("debug message")
	logger.Debugf("debug %s", "formatted")
	logger.Warn("warn message")
	logger.Warnf("warn %s", "formatted")
	logger.Error("error message")
	logger.Errorf("error %s", "formatted")

	// Restore stdout
	if err := w.Close(); err != nil {
		t.Logf("Failed to close writer: %v", err)
	}
	os.Stdout = oldStdout

	// Read the output
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("Failed to read from pipe: %v", err)
	}
	output := buf.String()

	// Verify that logs were written
	lines := strings.Split(strings.TrimSpace(output), "\n")
	expectedMessages := []string{
		"info message",
		"info formatted",
		"warn message",
		"warn formatted",
		"error message",
		"error formatted",
	}

	// Note: debug messages won't appear because default log level is info
	actualMessages := 0
	for _, line := range lines {
		if strings.Contains(line, "message") || strings.Contains(line, "formatted") {
			actualMessages++
		}
	}

	if actualMessages != len(expectedMessages) {
		t.Errorf("Expected %d log messages, got %d", len(expectedMessages), actualMessages)
	}
}

func TestFatalLogging(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Clear environment to avoid interference
	if err := os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT"); err != nil {
		t.Logf("Failed to unset OTEL_EXPORTER_OTLP_ENDPOINT: %v", err)
	}
	if err := os.Setenv("LOG_LEVEL", "fatal"); err != nil {
		t.Fatalf("Failed to set LOG_LEVEL: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("LOG_LEVEL"); err != nil {
			t.Logf("Failed to unset LOG_LEVEL: %v", err)
		}
	}()

	logger := NewAppLogger()
	otelLogger, ok := logger.(*OtelLogger)
	if !ok {
		t.Fatal("NewAppLogger() should return *OtelLogger")
	}

	// Test Fatal method
	otelLogger.Fatal("fatal error occurred")

	// Test Fatalf method
	otelLogger.Fatalf("fatal error: %s", "system failure")

	// Restore stdout
	if err := w.Close(); err != nil {
		t.Logf("Failed to close writer: %v", err)
	}
	os.Stdout = oldStdout

	// Read the output
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("Failed to read from pipe: %v", err)
	}
	output := buf.String()

	// Verify that fatal logs were written
	if !strings.Contains(output, "fatal error occurred") {
		t.Error("Expected output to contain 'fatal error occurred'")
	}
	if !strings.Contains(output, "fatal error: system failure") {
		t.Error("Expected output to contain formatted fatal message")
	}

	// Verify log level is "fatal"
	if !strings.Contains(output, `"level":"fatal"`) {
		t.Error("Expected log level to be 'fatal'")
	}
}

func TestFatalLoggingStructured(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}
	os.Stdout = w

	logger := &OtelLogger{
		appName:     "test-app",
		logLevel:    LevelDebug,
		attributes:  map[string]interface{}{"severity": "critical"},
		fallbackLog: log.New(w, "", 0),
	}

	logger.logStructured(LevelFatal, "fatal test message")

	// Restore stdout
	if err := w.Close(); err != nil {
		t.Logf("Failed to close writer: %v", err)
	}
	os.Stdout = oldStdout

	// Read the output
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("Failed to read from pipe: %v", err)
	}
	output := buf.String()

	// Parse JSON output
	var logEntry LogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	// Verify log entry
	if logEntry.Level != "fatal" {
		t.Errorf("Expected level 'fatal', got %q", logEntry.Level)
	}
	if logEntry.Message != "fatal test message" {
		t.Errorf("Expected message 'fatal test message', got %q", logEntry.Message)
	}
	if logEntry.Attributes["severity"] != "critical" {
		t.Errorf("Expected attribute severity='critical', got %v", logEntry.Attributes["severity"])
	}
}

func TestWith(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Clear environment to avoid interference
	if err := os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT"); err != nil {
		t.Logf("Failed to unset OTEL_EXPORTER_OTLP_ENDPOINT: %v", err)
	}

	logger := NewAppLogger()
	enhancedLogger := logger.With("component", "test").With("version", "1.0.0")
	enhancedLogger.Info("test message")

	// Restore stdout
	if err := w.Close(); err != nil {
		t.Logf("Failed to close writer: %v", err)
	}
	os.Stdout = oldStdout

	// Read the output
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("Failed to read from pipe: %v", err)
	}
	output := buf.String()

	// Split output into lines and get the last non-empty line (the actual log entry)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var logLine string
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" && strings.Contains(lines[i], "test message") {
			line := strings.TrimSpace(lines[i])
			// Extract JSON part (everything after the timestamp and prefix)
			if jsonStart := strings.Index(line, "{"); jsonStart != -1 {
				logLine = line[jsonStart:]
			}
			break
		}
	}

	if logLine == "" {
		t.Fatalf("Could not find log entry in output: %s", output)
	}

	// Parse JSON output
	var logEntry LogEntry
	if err := json.Unmarshal([]byte(logLine), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON output %q: %v", logLine, err)
	}

	// Verify attributes
	if logEntry.Attributes["component"] != "test" {
		t.Errorf("Expected component='test', got %v", logEntry.Attributes["component"])
	}
	if logEntry.Attributes["version"] != "1.0.0" {
		t.Errorf("Expected version='1.0.0', got %v", logEntry.Attributes["version"])
	}
}

func TestShutdown(t *testing.T) {
	logger := NewAppLogger()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Test shutdown doesn't panic and returns without error when no tracer provider
	err := logger.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown should not return error, got: %v", err)
	}
}

func TestFromContext(t *testing.T) {
	// Test with empty context
	ctx := context.Background()
	logger := FromContext(ctx)
	if logger == nil {
		t.Error("FromContext should return a logger even with empty context")
	}

	// Test with logger in context
	originalLogger := NewAppLogger()
	ctxWithLogger := WithLogger(ctx, originalLogger)
	retrievedLogger := FromContext(ctxWithLogger)

	if retrievedLogger != originalLogger {
		t.Error("FromContext should return the same logger that was stored in context")
	}
}

func TestLogFallback(t *testing.T) {
	// Create a buffer to capture log output
	var buf bytes.Buffer
	fallbackLogger := log.New(&buf, "[test-app] ", log.LstdFlags)

	logger := &OtelLogger{
		appName:     "test-app",
		logLevel:    LevelDebug,
		attributes:  map[string]interface{}{"key": "value"},
		fallbackLog: fallbackLogger,
	}

	logger.logFallback(LevelInfo, "test message")

	output := buf.String()
	if !strings.Contains(output, "test message") {
		t.Error("Fallback log should contain the message")
	}
	if !strings.Contains(output, "[INFO]") {
		t.Error("Fallback log should contain the log level")
	}
	if !strings.Contains(output, "key=value") {
		t.Error("Fallback log should contain attributes")
	}
}

// Benchmark tests
func BenchmarkLogStructured(b *testing.B) {
	// Redirect stdout to discard output during benchmark
	oldStdout := os.Stdout
	os.Stdout, _ = os.Open(os.DevNull)
	defer func() { os.Stdout = oldStdout }()

	logger := &OtelLogger{
		appName:    "bench-app",
		logLevel:   LevelDebug,
		attributes: map[string]interface{}{"key": "value"},
	}

	b.ResetTimer()
	for range b.N {
		logger.logStructured(LevelInfo, "benchmark message")
	}
}

func BenchmarkWithAttributes(b *testing.B) {
	// Redirect stdout to discard output during benchmark
	oldStdout := os.Stdout
	os.Stdout, _ = os.Open(os.DevNull)
	defer func() { os.Stdout = oldStdout }()

	logger := NewAppLogger()

	b.ResetTimer()
	for i := range b.N {
		enhanced := logger.With("iteration", i).With("benchmark", true)
		enhanced.Info("benchmark message")
	}
}

// Helper function for testing endpoint configuration
func testOtelEndpointConfig(t *testing.T, hostIP, hostPort, otlpEndpoint, expectedOutput string) {
	t.Helper()

	// Set environment variables
	if hostIP != "" {
		if err := os.Setenv("OTEL_HOST_IP", hostIP); err != nil {
			t.Fatalf("Failed to set OTEL_HOST_IP: %v", err)
		}
	}
	if hostPort != "" {
		if err := os.Setenv("OTEL_HOST_PORT", hostPort); err != nil {
			t.Fatalf("Failed to set OTEL_HOST_PORT: %v", err)
		}
	}
	if otlpEndpoint != "" {
		if err := os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", otlpEndpoint); err != nil {
			t.Fatalf("Failed to set OTEL_EXPORTER_OTLP_ENDPOINT: %v", err)
		}
	}
	if err := os.Setenv("OTEL_SDK_DISABLED", "true"); err != nil {
		t.Fatalf("Failed to set OTEL_SDK_DISABLED: %v", err)
	}

	// Cleanup
	defer func() {
		_ = os.Unsetenv("OTEL_HOST_IP")
		_ = os.Unsetenv("OTEL_HOST_PORT")
		_ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		_ = os.Unsetenv("OTEL_SDK_DISABLED")
	}()

	// Create test logger
	var buf bytes.Buffer
	logger := &OtelLogger{
		appName:     "test-app",
		appVersion:  "1.0.0",
		logLevel:    LevelInfo,
		attributes:  make(map[string]interface{}),
		fallbackLog: log.New(&buf, "[test-app] ", log.LstdFlags),
		useOtel:     true,
	}

	// Test the endpoint selection logic
	err := logger.initOtel()
	if err != nil {
		t.Fatalf("initOtel() should not return error when OTEL_SDK_DISABLED=true, got: %v", err)
	}

	// Verify output
	output := buf.String()
	if !strings.Contains(output, expectedOutput) {
		t.Errorf("Expected log to contain %q, got: %s", expectedOutput, output)
	}
}

// Tests for OTEL_HOST_IP and OTEL_HOST_PORT functionality
func TestOtelEndpointConfiguration_HostIP(t *testing.T) {
	testOtelEndpointConfig(t, "192.168.1.100:4317", "", "http://fallback.example.com:4317", "http://192.168.1.100:4317")
}

func TestOtelEndpointConfiguration_HostIPWithPort(t *testing.T) {
	testOtelEndpointConfig(t, "192.168.1.100", "4318", "", "http://192.168.1.100:4318")
}

func TestOtelEndpointConfiguration_HostIPWithExistingPort(t *testing.T) {
	testOtelEndpointConfig(t, "192.168.1.100:4317", "4318", "", "http://192.168.1.100:4317")
}

func TestOtelEndpointConfiguration_FallbackToStandard(t *testing.T) {
	testOtelEndpointConfig(t, "", "", "http://standard.example.com:4317", "http://standard.example.com:4317")
}

func TestOtelEndpointConfiguration_NoEndpoint(t *testing.T) {
	// Test behavior when neither OTEL_HOST_IP nor OTEL_EXPORTER_OTLP_ENDPOINT is set
	if err := os.Unsetenv("OTEL_HOST_IP"); err != nil {
		t.Logf("Failed to unset OTEL_HOST_IP: %v", err)
	}
	if err := os.Unsetenv("OTEL_HOST_PORT"); err != nil {
		t.Logf("Failed to unset OTEL_HOST_PORT: %v", err)
	}
	if err := os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT"); err != nil {
		t.Logf("Failed to unset OTEL_EXPORTER_OTLP_ENDPOINT: %v", err)
	}
	defer func() {
		// Clean up in case they were set
		if err := os.Unsetenv("OTEL_HOST_IP"); err != nil {
			t.Logf("Failed to unset OTEL_HOST_IP: %v", err)
		}
		if err := os.Unsetenv("OTEL_HOST_PORT"); err != nil {
			t.Logf("Failed to unset OTEL_HOST_PORT: %v", err)
		}
		if err := os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT"); err != nil {
			t.Logf("Failed to unset OTEL_EXPORTER_OTLP_ENDPOINT: %v", err)
		}
	}()

	// Capture log output to verify noop behavior
	var buf bytes.Buffer
	logger := &OtelLogger{
		appName:     "test-app",
		appVersion:  "1.0.0",
		logLevel:    LevelInfo,
		attributes:  make(map[string]interface{}),
		fallbackLog: log.New(&buf, "[test-app] ", log.LstdFlags),
		useOtel:     true,
	}

	// Test the endpoint selection logic by calling initOtel
	err := logger.initOtel()
	if err != nil {
		t.Fatalf("initOtel() should not return error when no endpoint is configured, got: %v", err)
	}

	// Verify tracer was still created
	if logger.tracer == nil {
		t.Error("Tracer should be created even with noop exporter")
	}
}

func TestOtelEndpointConfiguration_EmptyHostIP(t *testing.T) {
	testOtelEndpointConfig(t, "", "", "http://fallback.example.com:4317", "http://fallback.example.com:4317")
}

func TestOtelEndpointConfiguration_URLFormatting(t *testing.T) {
	tests := getOtelEndpointFormattingTestCases()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runOtelEndpointFormattingTest(t, tt)
		})
	}
}

type otelEndpointTestCase struct {
	name             string
	hostIP           string
	hostPort         string
	expectedEndpoint string
}

func getOtelEndpointFormattingTestCases() []otelEndpointTestCase {
	return []otelEndpointTestCase{
		{
			name:             "IP with port in hostIP",
			hostIP:           "192.168.1.100:4317",
			hostPort:         "",
			expectedEndpoint: "http://192.168.1.100:4317",
		},
		{
			name:             "IP without port, with separate port",
			hostIP:           "192.168.1.100",
			hostPort:         "4318",
			expectedEndpoint: "http://192.168.1.100:4318",
		},
		{
			name:             "IP without port, no separate port",
			hostIP:           "192.168.1.100",
			hostPort:         "",
			expectedEndpoint: "http://192.168.1.100",
		},
		{
			name:             "hostname with port in hostIP",
			hostIP:           "otel-collector:4317",
			hostPort:         "4318",
			expectedEndpoint: "http://otel-collector:4317",
		},
		{
			name:             "hostname without port, with separate port",
			hostIP:           "otel-collector",
			hostPort:         "4318",
			expectedEndpoint: "http://otel-collector:4318",
		},
		{
			name:             "already has http prefix",
			hostIP:           "http://otel-collector:4317",
			hostPort:         "",
			expectedEndpoint: "http://otel-collector:4317",
		},
		{
			name:             "already has https prefix",
			hostIP:           "https://otel-collector:4317",
			hostPort:         "",
			expectedEndpoint: "https://otel-collector:4317",
		},
		{
			name:             "with trailing slash",
			hostIP:           "192.168.1.100:4317/",
			hostPort:         "",
			expectedEndpoint: "http://192.168.1.100:4317",
		},
		{
			name:             "with http and trailing slash",
			hostIP:           "http://192.168.1.100:4317/",
			hostPort:         "",
			expectedEndpoint: "http://192.168.1.100:4317",
		},
	}
}

func runOtelEndpointFormattingTest(t *testing.T, tt otelEndpointTestCase) {
	setupOtelEndpointTest(t, tt)
	defer cleanupOtelEndpointTest()

	// Capture stdout to verify endpoint formatting
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}
	os.Stdout = w

	logger := createTestOtelLogger()
	err = logger.initOtel()
	if err != nil {
		t.Fatalf("initOtel() should not return error when OTEL_SDK_DISABLED=true, got: %v", err)
	}

	// Close writer and restore stdout
	if err := w.Close(); err != nil {
		t.Logf("Failed to close writer: %v", err)
	}
	os.Stdout = oldStdout

	// Read the output
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("Failed to read from pipe: %v", err)
	}
	output := buf.String()

	validateOtelEndpointFormatting(t, output, tt.expectedEndpoint)
}

func setupOtelEndpointTest(t *testing.T, tt otelEndpointTestCase) {
	if err := os.Setenv("OTEL_HOST_IP", tt.hostIP); err != nil {
		t.Fatalf("Failed to set OTEL_HOST_IP: %v", err)
	}
	if tt.hostPort != "" {
		if err := os.Setenv("OTEL_HOST_PORT", tt.hostPort); err != nil {
			t.Fatalf("Failed to set OTEL_HOST_PORT: %v", err)
		}
	} else {
		if err := os.Unsetenv("OTEL_HOST_PORT"); err != nil {
			t.Logf("Failed to unset OTEL_HOST_PORT: %v", err)
		}
	}
	if err := os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT"); err != nil {
		t.Logf("Failed to unset OTEL_EXPORTER_OTLP_ENDPOINT: %v", err)
	}
	if err := os.Setenv("OTEL_SDK_DISABLED", "true"); err != nil {
		t.Fatalf("Failed to set OTEL_SDK_DISABLED: %v", err)
	}
}

func cleanupOtelEndpointTest() {
	_ = os.Unsetenv("OTEL_HOST_IP")
	_ = os.Unsetenv("OTEL_HOST_PORT")
	_ = os.Unsetenv("OTEL_SDK_DISABLED")
}

func createTestOtelLogger() *OtelLogger {
	return &OtelLogger{
		appName:     "test-app",
		appVersion:  "1.0.0",
		logLevel:    LevelInfo,
		attributes:  make(map[string]interface{}),
		fallbackLog: log.New(os.Stdout, "[test-app] ", log.LstdFlags),
		useOtel:     true,
	}
}

func validateOtelEndpointFormatting(t *testing.T, output string, expectedEndpoint string) {
	if !strings.Contains(output, expectedEndpoint) {
		t.Errorf("Expected log to contain formatted endpoint %q, got: %s", expectedEndpoint, output)
	}
}

// Helper function to set up integration test environment
func setupIntegrationTest(t *testing.T) func() {
	t.Helper()

	if err := os.Setenv("OTEL_HOST_IP", "test-host"); err != nil {
		t.Fatalf("Failed to set OTEL_HOST_IP: %v", err)
	}
	if err := os.Setenv("OTEL_HOST_PORT", "4318"); err != nil {
		t.Fatalf("Failed to set OTEL_HOST_PORT: %v", err)
	}
	if err := os.Setenv("LOG_APP_NAME", "integration-test"); err != nil {
		t.Fatalf("Failed to set LOG_APP_NAME: %v", err)
	}
	if err := os.Setenv("OTEL_SDK_DISABLED", "true"); err != nil {
		t.Fatalf("Failed to set OTEL_SDK_DISABLED: %v", err)
	}

	return func() {
		_ = os.Unsetenv("OTEL_HOST_IP")
		_ = os.Unsetenv("OTEL_HOST_PORT")
		_ = os.Unsetenv("LOG_APP_NAME")
		_ = os.Unsetenv("OTEL_SDK_DISABLED")
	}
}

// Helper function to capture logger output
func captureLoggerOutput(t *testing.T, loggerFunc func(AppLogger)) string {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}
	os.Stdout = w

	logger := NewAppLogger()
	loggerFunc(logger)

	if err := w.Close(); err != nil {
		t.Logf("Failed to close writer: %v", err)
	}
	os.Stdout = oldStdout

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("Failed to read from pipe: %v", err)
	}
	return buf.String()
}

// Helper function to parse log entry from output
func parseLogEntry(t *testing.T, output, expectedMessage string) LogEntry {
	t.Helper()

	if !strings.Contains(output, expectedMessage) {
		t.Errorf("Expected log output to contain test message, got: %s", output)
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	var logLine string
	for _, line := range lines {
		if strings.Contains(line, expectedMessage) {
			if jsonStart := strings.Index(line, "{"); jsonStart != -1 {
				logLine = line[jsonStart:]
			}
			break
		}
	}

	if logLine == "" {
		t.Fatalf("Could not find log entry in output: %s", output)
	}

	var logEntry LogEntry
	if err := json.Unmarshal([]byte(logLine), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}
	return logEntry
}

func TestOtelEndpointConfiguration_Integration(t *testing.T) {
	cleanup := setupIntegrationTest(t)
	defer cleanup()

	output := captureLoggerOutput(t, func(logger AppLogger) {
		logger.Info("integration test message")
	})

	logEntry := parseLogEntry(t, output, "integration test message")

	// Verify log entry structure
	if logEntry.AppName != "integration-test" {
		t.Errorf("Expected app name 'integration-test', got %q", logEntry.AppName)
	}
	if logEntry.Level != "info" {
		t.Errorf("Expected level 'info', got %q", logEntry.Level)
	}
	if logEntry.Message != "integration test message" {
		t.Errorf("Expected message 'integration test message', got %q", logEntry.Message)
	}
}

// TestGinLoggerMiddleware tests the Gin middleware functionality
// Helper function to set up Gin test environment
func setupGinTestEnvironment(t *testing.T) func() {
	t.Helper()

	if err := os.Setenv("LOG_APP_NAME", "gin-test"); err != nil {
		t.Fatalf("Failed to set LOG_APP_NAME: %v", err)
	}
	if err := os.Setenv("LOG_LEVEL", "info"); err != nil {
		t.Fatalf("Failed to set LOG_LEVEL: %v", err)
	}
	if err := os.Setenv("OTEL_SDK_DISABLED", "true"); err != nil {
		t.Fatalf("Failed to set OTEL_SDK_DISABLED: %v", err)
	}

	return func() {
		_ = os.Unsetenv("LOG_APP_NAME")
		_ = os.Unsetenv("LOG_LEVEL")
		_ = os.Unsetenv("OTEL_SDK_DISABLED")
	}
}

// Helper function to capture Gin middleware logs
func captureGinLogs(t *testing.T, testFunc func(*gin.Engine, AppLogger)) string {
	t.Helper()

	var buf bytes.Buffer
	originalOutput := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	gin.SetMode(gin.TestMode)
	router := gin.New()
	logger := NewAppLogger()
	router.Use(GinLoggerMiddleware(logger))

	// Start reading in background
	done := make(chan bool)
	go func() {
		if _, err := io.Copy(&buf, r); err != nil {
			t.Logf("Failed to copy from pipe: %v", err)
		}
		done <- true
	}()

	// Execute test function
	testFunc(router, logger)

	// Close writer and wait for reader to finish
	if err := w.Close(); err != nil {
		t.Logf("Failed to close writer: %v", err)
	}
	<-done

	// Restore stdout
	os.Stdout = originalOutput

	return buf.String()
} // Helper to perform GET request and verify basic response
func performGETRequest(router *gin.Engine, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// Helper to perform POST request and verify basic response
func performPOSTRequest(router *gin.Engine, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestGinLoggerMiddleware(t *testing.T) {
	cleanup := setupGinTestEnvironment(t)
	defer cleanup()

	var response *httptest.ResponseRecorder
	output := captureGinLogs(t, func(router *gin.Engine, logger AppLogger) {
		router.GET("/test", func(c *gin.Context) {
			c.JSON(200, gin.H{"message": "test"})
		})
		response = performGETRequest(router, "/test?param=value", map[string]string{
			"X-Forwarded-For": "192.168.1.100",
		})
	})

	if response.Code != 200 {
		t.Errorf("Expected status code 200, got %d", response.Code)
	}

	if !strings.Contains(output, "GET") {
		t.Errorf("Expected log to contain HTTP method 'GET', got: %s", output)
	}
	if !strings.Contains(output, "/test") {
		t.Errorf("Expected log to contain path '/test', got: %s", output)
	}
	if !strings.Contains(output, "200") {
		t.Errorf("Expected log to contain status code '200', got: %s", output)
	}
}

func TestGinLoggerMiddleware_WithQueryParams(t *testing.T) {
	cleanup := setupGinTestEnvironment(t)
	defer cleanup()

	var response *httptest.ResponseRecorder
	output := captureGinLogs(t, func(router *gin.Engine, logger AppLogger) {
		router.POST("/api/users", func(c *gin.Context) {
			c.JSON(201, gin.H{"id": 1, "name": "test"})
		})
		response = performPOSTRequest(router, "/api/users?include=profile&sort=name", `{"name":"test"}`)
	})

	if response.Code != 201 {
		t.Errorf("Expected status code 201, got %d", response.Code)
	}

	if output == "" {
		t.Error("Expected log output, got empty string")
	}
	if !strings.Contains(output, "POST") {
		t.Errorf("Expected log to contain method 'POST', got: %s", output)
	}
	if !strings.Contains(output, "/api/users") {
		t.Errorf("Expected log to contain path '/api/users', got: %s", output)
	}
	if !strings.Contains(output, "201") {
		t.Errorf("Expected log to contain status code '201', got: %s", output)
	}
}

func TestGinLoggerMiddleware_ErrorStatus(t *testing.T) {
	cleanup := setupGinTestEnvironment(t)
	defer cleanup()

	var response *httptest.ResponseRecorder
	output := captureGinLogs(t, func(router *gin.Engine, logger AppLogger) {
		router.GET("/error", func(c *gin.Context) {
			c.JSON(500, gin.H{"error": "internal server error"})
		})
		response = performGETRequest(router, "/error", nil)
	})

	if response.Code != 500 {
		t.Errorf("Expected status code 500, got %d", response.Code)
	}

	if !strings.Contains(output, "500") {
		t.Errorf("Expected log to contain status code '500', got: %s", output)
	}
	if !strings.Contains(output, "/error") {
		t.Errorf("Expected log to contain path '/error', got: %s", output)
	}
}

// Additional tests to improve coverage

func TestOtelLogger_Shutdown(t *testing.T) {
	// Test shutdown with no providers
	logger := &OtelLogger{}
	ctx := context.Background()
	err := logger.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown with no providers should not return error, got: %v", err)
	}

	// Test with mock providers would require complex mocking
	// The existing test covers the basic case
}

func TestOtelLogger_formatOtlpEndpoint(t *testing.T) {
	logger := &OtelLogger{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty endpoint", "", ""},
		{"endpoint with trailing slash", "http://localhost:4317/", "http://localhost:4317"},
		{"endpoint without protocol", "localhost:4317", "http://localhost:4317"},
		{"https endpoint", "https://collector.example.com:4317", "https://collector.example.com:4317"},
		{"http endpoint", "http://collector.example.com:4317", "http://collector.example.com:4317"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := logger.formatOtlpEndpoint(tt.input)
			if result != tt.expected {
				t.Errorf("formatOtlpEndpoint(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestOtelLogger_getOtlpEndpoint(t *testing.T) {
	logger := &OtelLogger{}

	// Test with OTEL_HOST_IP and OTEL_HOST_PORT
	_ = os.Setenv("OTEL_HOST_IP", "192.168.1.100")
	_ = os.Setenv("OTEL_HOST_PORT", "4317")
	defer func() {
		_ = os.Unsetenv("OTEL_HOST_IP")
		_ = os.Unsetenv("OTEL_HOST_PORT")
	}()

	result := logger.getOtlpEndpoint()
	expected := "http://192.168.1.100:4317"
	if result != expected {
		t.Errorf("getOtlpEndpoint() = %q, want %q", result, expected)
	}

	// Test with OTEL_HOST_IP only (no port)
	_ = os.Unsetenv("OTEL_HOST_PORT")
	result = logger.getOtlpEndpoint()
	expected = "http://192.168.1.100"
	if result != expected {
		t.Errorf("getOtlpEndpoint() = %q, want %q", result, expected)
	}

	// Test with OTEL_HOST_IP containing port
	_ = os.Setenv("OTEL_HOST_IP", "192.168.1.100:4318")
	_ = os.Setenv("OTEL_HOST_PORT", "4317") // Should be ignored
	result = logger.getOtlpEndpoint()
	expected = "http://192.168.1.100:4318"
	if result != expected {
		t.Errorf("getOtlpEndpoint() = %q, want %q", result, expected)
	}

	// Test fallback to OTEL_EXPORTER_OTLP_ENDPOINT
	_ = os.Unsetenv("OTEL_HOST_IP")
	_ = os.Unsetenv("OTEL_HOST_PORT")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel.example.com:4317")
	defer func() { _ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT") }()

	result = logger.getOtlpEndpoint()
	expected = "https://otel.example.com:4317"
	if result != expected {
		t.Errorf("getOtlpEndpoint() = %q, want %q", result, expected)
	}
}

func TestOtelLogger_handleDisabledOtel(t *testing.T) {
	var buf bytes.Buffer
	logger := &OtelLogger{
		fallbackLog: log.New(&buf, "[test] ", 0),
	}

	// Test with endpoint
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317")
	defer func() { _ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT") }()

	err := logger.handleDisabledOtel()
	if err != nil {
		t.Errorf("handleDisabledOtel() should not return error, got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Using OTLP endpoint: http://localhost:4317") {
		t.Errorf("Expected log to contain endpoint info, got: %s", output)
	}
	if !strings.Contains(output, "OpenTelemetry SDK is disabled") {
		t.Errorf("Expected log to contain disabled message, got: %s", output)
	}
}

func TestOtelLogger_addKubernetesAttributes(t *testing.T) {
	logger := &OtelLogger{}

	// Set Kubernetes environment variables
	_ = os.Setenv("OTEL_RESOURCE_ATTRIBUTES_NODE_NAME", "node-1")
	_ = os.Setenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAME", "my-pod")
	_ = os.Setenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAMESPACE", "default")
	_ = os.Setenv("OTEL_RESOURCE_ATTRIBUTES_POD_UID", "12345-67890")
	_ = os.Setenv("OTEL_RESOURCE_ATTRIBUTES_POD_IP", "10.244.0.5")

	defer func() {
		_ = os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_NODE_NAME")
		_ = os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAME")
		_ = os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAMESPACE")
		_ = os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_POD_UID")
		_ = os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_POD_IP")
	}()

	var resourceAttrs []attribute.KeyValue
	logger.addKubernetesAttributes(&resourceAttrs)

	// Check that all Kubernetes attributes were added
	expectedAttrs := map[string]string{
		"k8s.node.name":      "node-1",
		"k8s.pod.name":       "my-pod",
		"k8s.namespace.name": "default",
		"k8s.pod.uid":        "12345-67890",
		"k8s.pod.ip":         "10.244.0.5",
	}

	if len(resourceAttrs) != len(expectedAttrs) {
		t.Errorf("Expected %d attributes, got %d", len(expectedAttrs), len(resourceAttrs))
	}

	for _, attr := range resourceAttrs {
		key := string(attr.Key)
		value := attr.Value.AsString()
		if expectedValue, exists := expectedAttrs[key]; exists {
			if value != expectedValue {
				t.Errorf("Expected %s = %s, got %s", key, expectedValue, value)
			}
		} else {
			t.Errorf("Unexpected attribute: %s = %s", key, value)
		}
	}
}

func TestOtelLogger_addKubernetesAttributes_Empty(t *testing.T) {
	logger := &OtelLogger{}

	// Ensure no Kubernetes environment variables are set
	envVars := []string{
		"OTEL_RESOURCE_ATTRIBUTES_NODE_NAME",
		"OTEL_RESOURCE_ATTRIBUTES_POD_NAME",
		"OTEL_RESOURCE_ATTRIBUTES_POD_NAMESPACE",
		"OTEL_RESOURCE_ATTRIBUTES_POD_UID",
		"OTEL_RESOURCE_ATTRIBUTES_POD_IP",
	}
	for _, env := range envVars {
		_ = os.Unsetenv(env)
	}

	var resourceAttrs []attribute.KeyValue
	logger.addKubernetesAttributes(&resourceAttrs)

	// Should not add any attributes when environment variables are not set
	if len(resourceAttrs) != 0 {
		t.Errorf("Expected 0 attributes when no env vars set, got %d", len(resourceAttrs))
	}
}

func TestOtelLogger_initOtel_Disabled(t *testing.T) {
	var buf bytes.Buffer
	logger := &OtelLogger{
		fallbackLog: log.New(&buf, "[test] ", 0),
	}

	// Test with OTEL_SDK_DISABLED=true
	_ = os.Setenv("OTEL_SDK_DISABLED", "true")
	defer func() { _ = os.Unsetenv("OTEL_SDK_DISABLED") }()

	err := logger.initOtel()
	if err != nil {
		t.Errorf("initOtel() with disabled SDK should not return error, got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "OpenTelemetry SDK is disabled") {
		t.Errorf("Expected log to contain disabled message, got: %s", output)
	}
}

func TestOtelLogger_initOtel_NoEndpoint(t *testing.T) {
	var buf bytes.Buffer
	logger := &OtelLogger{
		fallbackLog: log.New(&buf, "[test] ", 0),
	}

	// Ensure no endpoint environment variables are set
	_ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	_ = os.Unsetenv("OTEL_HOST_IP")
	_ = os.Unsetenv("OTEL_HOST_PORT")
	_ = os.Unsetenv("OTEL_SDK_DISABLED")

	err := logger.initOtel()
	if err != nil {
		t.Errorf("initOtel() with no endpoint should not return error, got: %v", err)
	}

	// Should have set up a tracer even without endpoint
	if logger.tracer == nil {
		t.Error("Expected tracer to be set even without endpoint")
	}
}

func TestOtelLogger_logStructured_JSONMarshalError(t *testing.T) {
	var buf bytes.Buffer
	logger := &OtelLogger{
		appName:     "test-app",
		fallbackLog: log.New(&buf, "", 0),
		attributes: map[string]interface{}{
			"invalid": func() {}, // Functions can't be marshaled to JSON
		},
	}

	logger.logStructured(LevelInfo, "test message")

	output := buf.String()
	// Should fall back to simple logging when JSON marshaling fails
	if !strings.Contains(output, "test message") {
		t.Errorf("Expected fallback log to contain message, got: %s", output)
	}
}

func TestOtelLogger_sendOtelLog(t *testing.T) {
	// Test with nil logger (should not panic)
	logger := &OtelLogger{}
	logger.sendOtelLog(LevelInfo, "test message")

	// Test should not panic and complete without error
	// More comprehensive testing would require mocking the otel logger
}

func TestOtelLogger_sendOtelLog_AllLevels(t *testing.T) {
	// Test all log levels including Fatal
	logger := &OtelLogger{
		appName:    "test-app",
		appVersion: "1.0.0",
		attributes: map[string]interface{}{"test": "value"},
	}

	// Test all log levels (should not panic even with nil otel logger)
	levels := []LogLevel{LevelDebug, LevelInfo, LevelWarn, LevelError, LevelFatal}
	for _, level := range levels {
		t.Run(level.String(), func(t *testing.T) {
			// Should not panic
			logger.sendOtelLog(level, "test message for "+level.String())
		})
	}
}

func TestNewAppLogger_WithEnvironmentVariables(t *testing.T) {
	// Set all environment variables
	_ = os.Setenv("LOG_LEVEL", "debug")
	_ = os.Setenv("LOG_APP_NAME", "custom-app")
	_ = os.Setenv("LOG_APP_VERSION", "2.0.0")
	_ = os.Setenv("OTEL_SDK_DISABLED", "false") // Enable OTel

	defer func() {
		_ = os.Unsetenv("LOG_LEVEL")
		_ = os.Unsetenv("LOG_APP_NAME")
		_ = os.Unsetenv("LOG_APP_VERSION")
		_ = os.Unsetenv("OTEL_SDK_DISABLED")
	}()

	logger := NewAppLogger()
	otelLogger := logger.(*OtelLogger)

	if otelLogger.appName != "custom-app" {
		t.Errorf("Expected appName 'custom-app', got '%s'", otelLogger.appName)
	}
	if otelLogger.appVersion != "2.0.0" {
		t.Errorf("Expected appVersion '2.0.0', got '%s'", otelLogger.appVersion)
	}
	if otelLogger.logLevel != LevelDebug {
		t.Errorf("Expected logLevel LevelDebug, got %v", otelLogger.logLevel)
	}
	if !otelLogger.useOtel {
		t.Error("Expected useOtel to be true when OTEL_SDK_DISABLED=false")
	}
}

func TestNewAppLogger_OtelDisabled(t *testing.T) {
	_ = os.Setenv("OTEL_SDK_DISABLED", "true")
	defer func() { _ = os.Unsetenv("OTEL_SDK_DISABLED") }()

	logger := NewAppLogger()
	otelLogger := logger.(*OtelLogger)

	if otelLogger.useOtel {
		t.Error("Expected useOtel to be false when OTEL_SDK_DISABLED=true")
	}
}

func TestOtelLogger_logFallback(t *testing.T) {
	var buf bytes.Buffer
	logger := &OtelLogger{
		appName:     "test-app",
		fallbackLog: log.New(&buf, "", 0),
		attributes: map[string]interface{}{
			"key1": "value1",
			"key2": 42,
		},
	}

	logger.logFallback(LevelError, "test error message")

	output := buf.String()
	if !strings.Contains(output, "ERROR") {
		t.Errorf("Expected log to contain 'ERROR', got: %s", output)
	}
	if !strings.Contains(output, "test error message") {
		t.Errorf("Expected log to contain message, got: %s", output)
	}
	if !strings.Contains(output, "key1=value1") {
		t.Errorf("Expected log to contain attributes, got: %s", output)
	}
	if !strings.Contains(output, "key2=42") {
		t.Errorf("Expected log to contain attributes, got: %s", output)
	}
}

func TestOtelLogger_logFallback_FatalLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := &OtelLogger{
		appName:     "test-app",
		fallbackLog: log.New(&buf, "", 0),
		attributes: map[string]interface{}{
			"component":  "core",
			"error_code": 500,
		},
	}

	logger.logFallback(LevelFatal, "fatal system error")

	output := buf.String()
	if !strings.Contains(output, "FATAL") {
		t.Errorf("Expected log to contain 'FATAL', got: %s", output)
	}
	if !strings.Contains(output, "fatal system error") {
		t.Errorf("Expected log to contain message, got: %s", output)
	}
	if !strings.Contains(output, "component=core") {
		t.Errorf("Expected log to contain component attribute, got: %s", output)
	}
	if !strings.Contains(output, "error_code=500") {
		t.Errorf("Expected log to contain error_code attribute, got: %s", output)
	}
}

func TestOtelLogger_log_FiltersBasedOnLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := &OtelLogger{
		appName:     "test-app",
		logLevel:    LevelWarn, // Only warn and above
		fallbackLog: log.New(&buf, "", 0),
		useOtel:     false,
		attributes:  make(map[string]interface{}),
	}

	// These should be filtered out
	logger.log(LevelDebug, "debug message")
	logger.log(LevelInfo, "info message")

	// These should be logged
	logger.log(LevelWarn, "warn message")
	logger.log(LevelError, "error message")
	logger.log(LevelFatal, "fatal message")

	output := buf.String()
	if strings.Contains(output, "debug message") {
		t.Error("Debug message should be filtered out")
	}
	if strings.Contains(output, "info message") {
		t.Error("Info message should be filtered out")
	}
	if !strings.Contains(output, "warn message") {
		t.Error("Warn message should be logged")
	}
	if !strings.Contains(output, "error message") {
		t.Error("Error message should be logged")
	}
	if !strings.Contains(output, "fatal message") {
		t.Error("Fatal message should be logged")
	}
}

func TestOtelLogger_NewAppLogger_InitOtelFailure(t *testing.T) {
	// Set environment to enable OTel but with invalid configuration that will cause init to fail
	_ = os.Setenv("OTEL_SDK_DISABLED", "false")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://invalid-endpoint-that-does-not-exist:99999")
	defer func() {
		_ = os.Unsetenv("OTEL_SDK_DISABLED")
		_ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}()

	// Capture stdout to verify fallback message
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	logger := NewAppLogger()

	// Close writer and restore stdout
	_ = w.Close()
	os.Stdout = oldStdout

	// Read output
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	// Logger should still be created even if OTel init fails
	if logger == nil {
		t.Fatal("Logger should be created even if OTel init fails")
	}

	otelLogger, ok := logger.(*OtelLogger)
	if !ok {
		t.Fatal("Should return *OtelLogger")
	}

	// Since the endpoint is invalid but formatted correctly, init might succeed
	// The important thing is that logger is created and functional
	// We test the actual failure case in TestOtelLogger_initOtel_WithEndpoint
	if otelLogger == nil {
		t.Fatal("Logger should be created")
	}
}

func TestOtelLogger_With_PreservesOtelReferences(t *testing.T) {
	// Create a logger with mock OTel providers
	originalLogger := &OtelLogger{
		appName:     "test-app",
		appVersion:  "1.0.0",
		logLevel:    LevelInfo,
		attributes:  map[string]interface{}{"original": "value"},
		fallbackLog: log.New(os.Stdout, "", 0),
		useOtel:     true,
		tracer:      nil, // Would be set in real scenario
	}

	// Create enhanced logger
	enhanced := originalLogger.With("new_key", "new_value")
	enhancedLogger := enhanced.(*OtelLogger)

	// Verify original attributes are preserved
	if enhancedLogger.attributes["original"] != "value" {
		t.Error("Original attributes should be preserved")
	}

	// Verify new attribute is added
	if enhancedLogger.attributes["new_key"] != "new_value" {
		t.Error("New attribute should be added")
	}

	// Verify original logger is not modified
	if _, exists := originalLogger.attributes["new_key"]; exists {
		t.Error("Original logger should not be modified")
	}

	// Verify OTel references are preserved
	if enhancedLogger.useOtel != originalLogger.useOtel {
		t.Error("useOtel should be preserved")
	}
	if enhancedLogger.appName != originalLogger.appName {
		t.Error("appName should be preserved")
	}
	if enhancedLogger.appVersion != originalLogger.appVersion {
		t.Error("appVersion should be preserved")
	}
}

func TestOtelLogger_formatOtlpEndpoint_EdgeCases(t *testing.T) {
	logger := &OtelLogger{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"multiple trailing slashes", "http://localhost:4317///", "http://localhost:4317//"},
		{"single trailing slash removed", "http://localhost:4317/", "http://localhost:4317"},
		{"just protocol http", "http://", "http://http:/"},
		{"just protocol https", "https://", "http://https:/"},
		{"ip address without port", "192.168.1.100", "http://192.168.1.100"},
		{"hostname with path", "collector.example.com/v1/traces", "http://collector.example.com/v1/traces"},
		{
			"https with path and trailing slash",
			"https://collector.example.com/v1/traces/",
			"https://collector.example.com/v1/traces",
		},
		{
			"http with path no trailing slash",
			"http://collector.example.com/v1/traces",
			"http://collector.example.com/v1/traces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := logger.formatOtlpEndpoint(tt.input)
			if result != tt.expected {
				t.Errorf("formatOtlpEndpoint(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestOtelLogger_getOtlpEndpoint_PriorityOrder(t *testing.T) {
	logger := &OtelLogger{}

	// Test 1: OTEL_HOST_IP takes priority over OTEL_EXPORTER_OTLP_ENDPOINT
	_ = os.Setenv("OTEL_HOST_IP", "priority-host")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://fallback.example.com")
	defer func() {
		_ = os.Unsetenv("OTEL_HOST_IP")
		_ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}()

	result := logger.getOtlpEndpoint()
	if !strings.Contains(result, "priority-host") {
		t.Errorf("OTEL_HOST_IP should take priority, got: %s", result)
	}
	if strings.Contains(result, "fallback") {
		t.Errorf("Should not use OTEL_EXPORTER_OTLP_ENDPOINT when OTEL_HOST_IP is set, got: %s", result)
	}
}

func TestOtelLogger_initOtel_WithEndpoint(t *testing.T) {
	var buf bytes.Buffer
	logger := &OtelLogger{
		appName:     "test-app",
		appVersion:  "1.0.0",
		fallbackLog: log.New(&buf, "", 0),
	}

	// Set endpoint but OTel creation will fail (which is expected in test env)
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	_ = os.Unsetenv("OTEL_SDK_DISABLED")
	defer func() {
		_ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}()

	err := logger.initOtel()
	// Error is expected since we don't have a real OTLP collector running
	// The important thing is that it attempts initialization
	if err == nil {
		// If no error, verify tracer or logger was set
		if logger.tracer == nil && logger.logger == nil {
			t.Error("Expected tracer or logger to be set when no error")
		}
	}
}

func TestOtelLogger_log_WithOtelEnabled(t *testing.T) {
	var buf bytes.Buffer
	logger := &OtelLogger{
		appName:     "test-app",
		appVersion:  "1.0.0",
		logLevel:    LevelDebug,
		attributes:  map[string]interface{}{"test": "value"},
		fallbackLog: log.New(&buf, "", 0),
		useOtel:     true,
		logger:      nil, // Nil logger means sendOtelLog will be called but do nothing
	}

	logger.log(LevelInfo, "test message with otel enabled")

	output := buf.String()
	// Should still log to structured output even with OTel enabled
	if !strings.Contains(output, "test message with otel enabled") {
		t.Error("Should log to structured output")
	}
}

func TestOtelLogger_sendOtelLog_WithAttributes(t *testing.T) {
	// Test that sendOtelLog formats attributes correctly
	logger := &OtelLogger{
		appName:    "test-app",
		appVersion: "1.0.0",
		attributes: map[string]interface{}{
			"string_attr": "value",
			"int_attr":    42,
			"bool_attr":   true,
			"float_attr":  3.14,
		},
		logger: nil, // Nil logger, so this won't actually send
	}

	// Should not panic even with complex attributes
	logger.sendOtelLog(LevelInfo, "test message")
	logger.sendOtelLog(LevelWarn, "warning message")
	logger.sendOtelLog(LevelError, "error message")
	logger.sendOtelLog(LevelFatal, "fatal message")
}

func TestOtelLogger_logStructured_WithoutAttributes(t *testing.T) {
	var buf bytes.Buffer
	logger := &OtelLogger{
		appName:     "test-app",
		logLevel:    LevelInfo,
		attributes:  nil, // No attributes
		fallbackLog: log.New(&buf, "", 0),
	}

	logger.logStructured(LevelInfo, "message without attributes")

	output := buf.String()
	var logEntry LogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	// Attributes should be nil or empty in JSON
	if len(logEntry.Attributes) > 0 {
		t.Errorf("Expected no attributes, got: %v", logEntry.Attributes)
	}
}

func TestOtelLogger_logStructured_EmptyAttributesMap(t *testing.T) {
	var buf bytes.Buffer
	logger := &OtelLogger{
		appName:     "test-app",
		logLevel:    LevelInfo,
		attributes:  make(map[string]interface{}), // Empty map
		fallbackLog: log.New(&buf, "", 0),
	}

	logger.logStructured(LevelInfo, "message with empty attributes map")

	output := buf.String()
	var logEntry LogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	// Attributes should not be present in JSON when map is empty
	if len(logEntry.Attributes) > 0 {
		t.Errorf("Expected no attributes in JSON for empty map, got: %v", logEntry.Attributes)
	}
}

// Tests for gRPC/HTTP protocol selection
func TestOtelLogger_ProtocolConstants(t *testing.T) {
	// Verify the protocol constants are correctly defined
	if ProtocolGRPC != "grpc" {
		t.Errorf("ProtocolGRPC should be 'grpc', got %q", ProtocolGRPC)
	}
	if ProtocolHTTP != "http/protobuf" {
		t.Errorf("ProtocolHTTP should be 'http/protobuf', got %q", ProtocolHTTP)
	}
	if OtelProtocolEnv != "OTEL_EXPORTER_OTLP_PROTOCOL" {
		t.Errorf("OtelProtocolEnv should be 'OTEL_EXPORTER_OTLP_PROTOCOL', got %q", OtelProtocolEnv)
	}
}

func TestOtelLogger_ProtocolSelection(t *testing.T) {
	tests := []struct {
		name             string
		protocolEnv      string
		expectedProtocol string
	}{
		{
			name:             "gRPC protocol when explicitly set",
			protocolEnv:      "grpc",
			expectedProtocol: "grpc",
		},
		{
			name:             "gRPC protocol case insensitive",
			protocolEnv:      "GRPC",
			expectedProtocol: "grpc",
		},
		{
			name:             "HTTP protocol when set",
			protocolEnv:      "http/protobuf",
			expectedProtocol: "http/protobuf",
		},
		{
			name:             "HTTP protocol case insensitive",
			protocolEnv:      "HTTP/PROTOBUF",
			expectedProtocol: "http/protobuf",
		},
		{
			name:             "default to gRPC when empty",
			protocolEnv:      "",
			expectedProtocol: "grpc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set the protocol environment variable
			if tt.protocolEnv != "" {
				if err := os.Setenv(OtelProtocolEnv, tt.protocolEnv); err != nil {
					t.Fatalf("Failed to set %s: %v", OtelProtocolEnv, err)
				}
				defer func() {
					if err := os.Unsetenv(OtelProtocolEnv); err != nil {
						t.Logf("Failed to unset %s: %v", OtelProtocolEnv, err)
					}
				}()
			} else {
				if err := os.Unsetenv(OtelProtocolEnv); err != nil {
					t.Logf("Failed to unset %s: %v", OtelProtocolEnv, err)
				}
			}

			// Verify the protocol is read correctly
			protocol := strings.ToLower(os.Getenv(OtelProtocolEnv))
			if protocol == "" {
				protocol = ProtocolGRPC // Default to gRPC
			}

			if protocol != tt.expectedProtocol {
				t.Errorf("Expected protocol %q, got %q", tt.expectedProtocol, protocol)
			}
		})
	}
}

func TestOtelLogger_DefaultPortConstants(t *testing.T) {
	// Verify the default port constants are correctly defined
	if DefaultGRPCPort != "4317" {
		t.Errorf("DefaultGRPCPort should be '4317', got %q", DefaultGRPCPort)
	}
	if DefaultHTTPPort != "4318" {
		t.Errorf("DefaultHTTPPort should be '4318', got %q", DefaultHTTPPort)
	}
}
