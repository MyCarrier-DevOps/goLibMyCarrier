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

	if otelLogger.appName != "test_application_abcd" {
		t.Errorf("Default app name should be 'test_application_abcd', got %q", otelLogger.appName)
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
		{LevelInfo, LevelDebug, false},
		{LevelInfo, LevelInfo, true},
		{LevelInfo, LevelWarn, true},
		{LevelInfo, LevelError, true},
		{LevelWarn, LevelDebug, false},
		{LevelWarn, LevelInfo, false},
		{LevelWarn, LevelWarn, true},
		{LevelWarn, LevelError, true},
		{LevelError, LevelDebug, false},
		{LevelError, LevelInfo, false},
		{LevelError, LevelWarn, false},
		{LevelError, LevelError, true},
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

func TestSetKlogLevel(t *testing.T) {
	// Test that SetKlogLevel doesn't panic
	SetKlogLevel(2)
	// We can't easily test the actual effect, but we can ensure it doesn't crash
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

	// Check that noop exporter message was logged
	output := buf.String()
	if !strings.Contains(output, "no OTLP endpoint configured, using noop exporter") {
		t.Errorf("Expected log to contain noop exporter message, got: %s", output)
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
	defer func() {
		if err := w.Close(); err != nil {
			t.Logf("Failed to close writer: %v", err)
		}
		os.Stdout = originalOutput
	}()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	logger := NewAppLogger()
	router.Use(GinLoggerMiddleware(logger))

	testFunc(router, logger)

	// Read the captured output
	go func() {
		if _, err := io.Copy(&buf, r); err != nil {
			t.Logf("Failed to copy from pipe: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)

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
