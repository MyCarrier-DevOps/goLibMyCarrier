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

	if !otelLogger.useOtel {
		t.Error("OpenTelemetry should be enabled by default")
	}
}

func TestNewAppLogger_EnvironmentVariables(t *testing.T) {
	// Set environment variables
	os.Setenv("LOG_LEVEL", "debug")
	os.Setenv("LOG_APP_NAME", "test-app")
	os.Setenv("OTEL_SDK_DISABLED", "true")
	defer func() {
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("LOG_APP_NAME")
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

	if otelLogger.useOtel {
		t.Error("OpenTelemetry should be disabled when OTEL_SDK_DISABLED=true")
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
