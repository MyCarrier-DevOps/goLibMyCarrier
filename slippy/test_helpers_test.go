package slippy

import (
	"context"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/logger"
)

// testLogger implements Logger for testing. It's a no-op logger.
type testLogger struct {
	fields map[string]interface{}
}

// newTestLogger creates a new test logger.
func newTestLogger() logger.Logger {
	return &testLogger{
		fields: make(map[string]interface{}),
	}
}

// Info logs an informational message (no-op in tests).
func (l *testLogger) Info(ctx context.Context, message string, fields map[string]interface{}) {}

// Debug logs a debug message (no-op in tests).
func (l *testLogger) Debug(ctx context.Context, message string, fields map[string]interface{}) {}

// Warn logs a warning message (no-op in tests).
func (l *testLogger) Warn(ctx context.Context, message string, fields map[string]interface{}) {}

// Warning is an alias for Warn.
func (l *testLogger) Warning(ctx context.Context, message string, fields map[string]interface{}) {}

// Error logs an error message (no-op in tests).
func (l *testLogger) Error(ctx context.Context, message string, err error, fields map[string]interface{}) {
}

// WithFields returns a new Logger with the given fields.
func (l *testLogger) WithFields(fields map[string]interface{}) logger.Logger {
	newFields := make(map[string]interface{})
	for k, v := range l.fields {
		newFields[k] = v
	}
	for k, v := range fields {
		newFields[k] = v
	}
	return &testLogger{fields: newFields}
}

// Ensure testLogger implements logger.Logger.
var _ logger.Logger = (*testLogger)(nil)

// testPipelineConfig returns a minimal pipeline config for testing.
// The config is properly initialized with internal lookup maps.
func testPipelineConfig() *PipelineConfig {
	config := &PipelineConfig{
		Version:     "1",
		Name:        "test-pipeline",
		Description: "Test pipeline config",
		Steps: []StepConfig{
			{Name: "push_parsed", Description: "Push parsed"},
			{
				Name:          "builds",
				Description:   "Builds completed",
				Aggregates:    "build",
				Prerequisites: []string{"push_parsed"},
			},
			{
				Name:          "unit_tests",
				Description:   "Unit tests completed",
				Aggregates:    "unit_test",
				Prerequisites: []string{"builds"},
			},
			{Name: "dev_deploy", Description: "Dev deploy", Prerequisites: []string{"unit_tests"}},
		},
	}
	// Initialize internal lookup maps (same as what LoadPipelineConfig does)
	config.stepsByName = make(map[string]*StepConfig)
	config.aggregateMap = make(map[string]string)
	config.gateSteps = make([]string, 0)
	for i := range config.Steps {
		step := &config.Steps[i]
		step.order = i
		config.stepsByName[step.Name] = step
		if step.Aggregates != "" {
			config.aggregateMap[step.Aggregates] = step.Name
		}
		if step.IsGate {
			config.gateSteps = append(config.gateSteps, step.Name)
		}
	}
	return config
}

// capturedLogCall records a single logger invocation, for tests that assert on what was logged.
type capturedLogCall struct {
	level   string
	message string
	fields  map[string]interface{}
}

// capturingLogger is a minimal Logger implementation that records every call
// instead of discarding it, so tests can assert on the exact message/fields
// pair a code path emitted.
type capturingLogger struct {
	calls []capturedLogCall
}

func (l *capturingLogger) Info(_ context.Context, message string, fields map[string]interface{}) {
	l.calls = append(l.calls, capturedLogCall{level: "info", message: message, fields: fields})
}

func (l *capturingLogger) Debug(_ context.Context, message string, fields map[string]interface{}) {
	l.calls = append(l.calls, capturedLogCall{level: "debug", message: message, fields: fields})
}

func (l *capturingLogger) Warn(_ context.Context, message string, fields map[string]interface{}) {
	l.calls = append(l.calls, capturedLogCall{level: "warn", message: message, fields: fields})
}

func (l *capturingLogger) Warning(ctx context.Context, message string, fields map[string]interface{}) {
	l.Warn(ctx, message, fields)
}

func (l *capturingLogger) Error(
	_ context.Context, message string, err error, fields map[string]interface{},
) {
	if fields == nil {
		fields = map[string]interface{}{}
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	l.calls = append(l.calls, capturedLogCall{level: "error", message: message, fields: fields})
}

func (l *capturingLogger) WithFields(map[string]interface{}) logger.Logger {
	return l
}

var _ logger.Logger = (*capturingLogger)(nil)

// callsWithField returns the subset of captured calls whose fields map has
// the given key set to true.
func (l *capturingLogger) callsWithField(key string) []capturedLogCall {
	var out []capturedLogCall
	for _, c := range l.calls {
		if v, ok := c.fields[key]; ok {
			if b, ok := v.(bool); ok && b {
				out = append(out, c)
			}
		}
	}
	return out
}

// callsWithLevel returns the subset of captured calls at the given log level
// (e.g. "warn", "info", "error").
func (l *capturingLogger) callsWithLevel(level string) []capturedLogCall {
	var out []capturedLogCall
	for _, c := range l.calls {
		if c.level == level {
			out = append(out, c)
		}
	}
	return out
}
