package pureotel

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"testing"
	"time"
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
	os.Unsetenv("LOG_LEVEL")
	os.Unsetenv("LOG_APP_NAME")
	os.Unsetenv("LOG_APP_VERSION")
	os.Unsetenv("OTEL_SDK_DISABLED")

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
	os.Setenv("LOG_LEVEL", "debug")
	os.Setenv("LOG_APP_NAME", "test-app")
	os.Setenv("LOG_APP_VERSION", "2.1.0")
	os.Setenv("OTEL_SDK_DISABLED", "true")
	defer func() {
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("LOG_APP_NAME")
		os.Unsetenv("LOG_APP_VERSION")
		os.Unsetenv("OTEL_SDK_DISABLED")
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
	os.Setenv("OTEL_RESOURCE_ATTRIBUTES_NODE_NAME", "test-node")
	os.Setenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAME", "test-pod")
	os.Setenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAMESPACE", "test-namespace")
	os.Setenv("OTEL_RESOURCE_ATTRIBUTES_POD_UID", "test-uid-123")
	os.Setenv("OTEL_RESOURCE_ATTRIBUTES_POD_IP", "10.0.0.1")
	os.Setenv("OTEL_SDK_DISABLED", "true") // Disable OTel to avoid actual initialization
	defer func() {
		os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_NODE_NAME")
		os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAME")
		os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAMESPACE")
		os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_POD_UID")
		os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_POD_IP")
		os.Unsetenv("OTEL_SDK_DISABLED")
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
	os.Setenv("OTEL_RESOURCE_ATTRIBUTES_NODE_NAME", "test-node")
	os.Setenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAME", "test-pod")
	// Intentionally omit other K8s attributes
	os.Setenv("OTEL_SDK_DISABLED", "true") // Disable OTel to avoid actual initialization
	defer func() {
		os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_NODE_NAME")
		os.Unsetenv("OTEL_RESOURCE_ATTRIBUTES_POD_NAME")
		os.Unsetenv("OTEL_SDK_DISABLED")
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
	r, w, _ := os.Pipe()
	os.Stdout = w

	logger := &OtelLogger{
		appName:    "test-app",
		logLevel:   LevelDebug,
		attributes: map[string]interface{}{"key": "value"},
	}

	logger.logStructured(LevelInfo, "test message")

	// Restore stdout
	w.Close()
	os.Stdout = oldStdout

	// Read the output
	var buf bytes.Buffer
	buf.ReadFrom(r)
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
	w.Close()
	os.Stdout = oldStdout

	// Read the output
	var buf bytes.Buffer
	buf.ReadFrom(r)
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
	os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")

	logger := NewAppLogger()
	enhancedLogger := logger.With("component", "test").With("version", "1.0.0")
	enhancedLogger.Info("test message")

	// Restore stdout
	w.Close()
	os.Stdout = oldStdout

	// Read the output
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Split output into lines and get the last non-empty line (the actual log entry)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var logLine string
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" && strings.Contains(lines[i], "test message") {
			logLine = strings.TrimSpace(lines[i])
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
	for i := 0; i < b.N; i++ {
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
	for i := 0; i < b.N; i++ {
		enhanced := logger.With("iteration", i).With("benchmark", true)
		enhanced.Info("benchmark message")
	}
}

// Tests for OTEL_HOST_IP and OTEL_HOST_PORT functionality
func TestOtelEndpointConfiguration_HostIP(t *testing.T) {
	// Test OTEL_HOST_IP takes precedence over OTEL_EXPORTER_OTLP_ENDPOINT
	os.Setenv("OTEL_HOST_IP", "192.168.1.100:4317")
	os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://fallback.example.com:4317")
	os.Setenv("OTEL_SDK_DISABLED", "true") // Disable actual OTel initialization
	defer func() {
		os.Unsetenv("OTEL_HOST_IP")
		os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		os.Unsetenv("OTEL_SDK_DISABLED")
	}()

	// Capture log output to verify endpoint selection
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
		t.Fatalf("initOtel() should not return error when OTEL_SDK_DISABLED=true, got: %v", err)
	}

	// Check that OTEL_HOST_IP was used (it should be in the log output)
	output := buf.String()
	if !strings.Contains(output, "http://192.168.1.100:4317") {
		t.Errorf("Expected log to contain OTEL_HOST_IP endpoint, got: %s", output)
	}
}

func TestOtelEndpointConfiguration_HostIPWithPort(t *testing.T) {
	// Test OTEL_HOST_IP with separate OTEL_HOST_PORT
	os.Setenv("OTEL_HOST_IP", "192.168.1.100")
	os.Setenv("OTEL_HOST_PORT", "4318")
	os.Setenv("OTEL_SDK_DISABLED", "true") // Disable actual OTel initialization
	defer func() {
		os.Unsetenv("OTEL_HOST_IP")
		os.Unsetenv("OTEL_HOST_PORT")
		os.Unsetenv("OTEL_SDK_DISABLED")
	}()

	// Capture log output to verify endpoint selection
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
		t.Fatalf("initOtel() should not return error when OTEL_SDK_DISABLED=true, got: %v", err)
	}

	// Check that OTEL_HOST_IP:OTEL_HOST_PORT was used
	output := buf.String()
	if !strings.Contains(output, "http://192.168.1.100:4318") {
		t.Errorf("Expected log to contain OTEL_HOST_IP:OTEL_HOST_PORT endpoint, got: %s", output)
	}
}

func TestOtelEndpointConfiguration_HostIPWithExistingPort(t *testing.T) {
	// Test OTEL_HOST_IP already contains port, OTEL_HOST_PORT should be ignored
	os.Setenv("OTEL_HOST_IP", "192.168.1.100:4317")
	os.Setenv("OTEL_HOST_PORT", "4318")    // This should be ignored
	os.Setenv("OTEL_SDK_DISABLED", "true") // Disable actual OTel initialization
	defer func() {
		os.Unsetenv("OTEL_HOST_IP")
		os.Unsetenv("OTEL_HOST_PORT")
		os.Unsetenv("OTEL_SDK_DISABLED")
	}()

	// Capture log output to verify endpoint selection
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
		t.Fatalf("initOtel() should not return error when OTEL_SDK_DISABLED=true, got: %v", err)
	}

	// Check that original port from OTEL_HOST_IP was preserved
	output := buf.String()
	if !strings.Contains(output, "http://192.168.1.100:4317") {
		t.Errorf("Expected log to contain original OTEL_HOST_IP port (4317), got: %s", output)
	}
	if strings.Contains(output, ":4318") {
		t.Errorf("OTEL_HOST_PORT should be ignored when OTEL_HOST_IP already contains port, got: %s", output)
	}
}

func TestOtelEndpointConfiguration_FallbackToStandard(t *testing.T) {
	// Test fallback to OTEL_EXPORTER_OTLP_ENDPOINT when OTEL_HOST_IP is not set
	os.Unsetenv("OTEL_HOST_IP")
	os.Unsetenv("OTEL_HOST_PORT")
	os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://standard.example.com:4317")
	os.Setenv("OTEL_SDK_DISABLED", "true") // Disable actual OTel initialization
	defer func() {
		os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		os.Unsetenv("OTEL_SDK_DISABLED")
	}()

	// Capture log output to verify endpoint selection
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
		t.Fatalf("initOtel() should not return error when OTEL_SDK_DISABLED=true, got: %v", err)
	}

	// Check that OTEL_EXPORTER_OTLP_ENDPOINT was used
	output := buf.String()
	if !strings.Contains(output, "http://standard.example.com:4317") {
		t.Errorf("Expected log to contain standard OTLP endpoint, got: %s", output)
	}
}

func TestOtelEndpointConfiguration_NoEndpoint(t *testing.T) {
	// Test behavior when neither OTEL_HOST_IP nor OTEL_EXPORTER_OTLP_ENDPOINT is set
	os.Unsetenv("OTEL_HOST_IP")
	os.Unsetenv("OTEL_HOST_PORT")
	os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	defer func() {
		// Clean up in case they were set
		os.Unsetenv("OTEL_HOST_IP")
		os.Unsetenv("OTEL_HOST_PORT")
		os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
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
	// Test behavior when OTEL_HOST_IP is set but empty
	os.Setenv("OTEL_HOST_IP", "")
	os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://fallback.example.com:4317")
	os.Setenv("OTEL_SDK_DISABLED", "true") // Disable actual OTel initialization
	defer func() {
		os.Unsetenv("OTEL_HOST_IP")
		os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		os.Unsetenv("OTEL_SDK_DISABLED")
	}()

	// Capture log output to verify endpoint selection
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
		t.Fatalf("initOtel() should not return error when OTEL_SDK_DISABLED=true, got: %v", err)
	}

	// Check that fallback endpoint was used
	output := buf.String()
	if !strings.Contains(output, "http://fallback.example.com:4317") {
		t.Errorf("Expected log to contain fallback endpoint when OTEL_HOST_IP is empty, got: %s", output)
	}
}

func TestOtelEndpointConfiguration_URLFormatting(t *testing.T) {
	tests := []struct {
		name             string
		hostIP           string
		hostPort         string
		expectedEndpoint string
	}{
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
			expectedEndpoint: "http://otel-collector:4317", // port ignored when already present
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("OTEL_HOST_IP", tt.hostIP)
			if tt.hostPort != "" {
				os.Setenv("OTEL_HOST_PORT", tt.hostPort)
			} else {
				os.Unsetenv("OTEL_HOST_PORT")
			}
			os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
			os.Setenv("OTEL_SDK_DISABLED", "true") // Disable actual OTel initialization
			defer func() {
				os.Unsetenv("OTEL_HOST_IP")
				os.Unsetenv("OTEL_HOST_PORT")
				os.Unsetenv("OTEL_SDK_DISABLED")
			}()

			// Capture log output to verify endpoint formatting
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
				t.Fatalf("initOtel() should not return error when OTEL_SDK_DISABLED=true, got: %v", err)
			}

			// Check that the endpoint was formatted correctly
			output := buf.String()
			if !strings.Contains(output, tt.expectedEndpoint) {
				t.Errorf("Expected log to contain formatted endpoint %q, got: %s", tt.expectedEndpoint, output)
			}
		})
	}
}

func TestOtelEndpointConfiguration_Integration(t *testing.T) {
	// Test the complete flow with NewAppLogger
	os.Setenv("OTEL_HOST_IP", "test-host")
	os.Setenv("OTEL_HOST_PORT", "4318")
	os.Setenv("LOG_APP_NAME", "integration-test")
	os.Setenv("OTEL_SDK_DISABLED", "true") // Disable actual OTel to avoid external dependencies
	defer func() {
		os.Unsetenv("OTEL_HOST_IP")
		os.Unsetenv("OTEL_HOST_PORT")
		os.Unsetenv("LOG_APP_NAME")
		os.Unsetenv("OTEL_SDK_DISABLED")
	}()

	// Capture stdout to verify the logger works end-to-end
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	logger := NewAppLogger()
	logger.Info("integration test message")

	// Restore stdout
	w.Close()
	os.Stdout = oldStdout

	// Read the output
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify the log was written
	if !strings.Contains(output, "integration test message") {
		t.Errorf("Expected log output to contain test message, got: %s", output)
	}

	// Parse the JSON log entry
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var logLine string
	for _, line := range lines {
		if strings.Contains(line, "integration test message") {
			logLine = line
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
