package slippy

import (
	"errors"
	"testing"
)

// testPipelineConfigForTests creates a full pipeline config for testing helper methods.
func testPipelineConfigForTests() *PipelineConfig {
	config := &PipelineConfig{
		Version:     "1",
		Name:        "test-pipeline",
		Description: "Test pipeline config",
		Steps: []StepConfig{
			{Name: "push_parsed", Description: "Push parsed"},
			{
				Name:          "builds_completed",
				Description:   "Builds completed",
				Aggregates:    "build",
				Prerequisites: []string{"push_parsed"},
			},
			{
				Name:          "unit_tests_completed",
				Description:   "Unit tests completed",
				Aggregates:    "unit_test",
				Prerequisites: []string{"builds_completed"},
			},
			{
				Name:          "quality_gate",
				Description:   "Quality gate",
				Prerequisites: []string{"unit_tests_completed"},
				IsGate:        true,
			},
			{Name: "dev_deploy", Description: "Dev deploy", Prerequisites: []string{"quality_gate"}},
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

func TestPipelineConfig_GetStepNames(t *testing.T) {
	config := testPipelineConfigForTests()

	names := config.GetStepNames()
	if len(names) != 5 {
		t.Errorf("expected 5 step names, got %d", len(names))
	}
	if names[0] != "push_parsed" {
		t.Errorf("expected first step to be 'push_parsed', got '%s'", names[0])
	}
	if names[4] != "dev_deploy" {
		t.Errorf("expected last step to be 'dev_deploy', got '%s'", names[4])
	}
}

func TestPipelineConfig_GetComponentStep(t *testing.T) {
	config := testPipelineConfigForTests()

	// builds_completed aggregates "build"
	component := config.GetComponentStep("builds_completed")
	if component != "build" {
		t.Errorf("expected component 'build' for builds_completed, got '%s'", component)
	}

	// push_parsed doesn't aggregate anything
	component = config.GetComponentStep("push_parsed")
	if component != "" {
		t.Errorf("expected empty component for push_parsed, got '%s'", component)
	}

	// non-existent step
	component = config.GetComponentStep("non_existent")
	if component != "" {
		t.Errorf("expected empty component for non_existent, got '%s'", component)
	}
}

func TestPipelineConfig_IsAggregateStep(t *testing.T) {
	config := testPipelineConfigForTests()

	if !config.IsAggregateStep("builds_completed") {
		t.Error("expected builds_completed to be an aggregate step")
	}
	if config.IsAggregateStep("push_parsed") {
		t.Error("expected push_parsed to not be an aggregate step")
	}
	if config.IsAggregateStep("non_existent") {
		t.Error("expected non_existent to not be an aggregate step")
	}
}

func TestPipelineConfig_IsGateStep(t *testing.T) {
	config := testPipelineConfigForTests()

	if !config.IsGateStep("quality_gate") {
		t.Error("expected quality_gate to be a gate step")
	}
	if config.IsGateStep("push_parsed") {
		t.Error("expected push_parsed to not be a gate step")
	}
	if config.IsGateStep("non_existent") {
		t.Error("expected non_existent to not be a gate step")
	}
}

func TestPipelineConfig_GetGateSteps(t *testing.T) {
	config := testPipelineConfigForTests()

	gates := config.GetGateSteps()
	if len(gates) != 1 {
		t.Errorf("expected 1 gate step, got %d", len(gates))
	}
	if gates[0] != "quality_gate" {
		t.Errorf("expected gate step 'quality_gate', got '%s'", gates[0])
	}
}

func TestPipelineConfig_GetAggregateSteps(t *testing.T) {
	config := testPipelineConfigForTests()

	aggregates := config.GetAggregateSteps()
	if len(aggregates) != 2 {
		t.Errorf("expected 2 aggregate steps, got %d", len(aggregates))
	}
}

func TestPipelineConfig_ForEachStep(t *testing.T) {
	config := testPipelineConfigForTests()

	var visited []string
	err := config.ForEachStep(func(step *StepConfig) error {
		visited = append(visited, step.Name)
		return nil
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(visited) != 5 {
		t.Errorf("expected 5 visited steps, got %d", len(visited))
	}
}

func TestPipelineConfig_ForEachAggregateStep(t *testing.T) {
	config := testPipelineConfigForTests()

	var visited []string
	err := config.ForEachAggregateStep(func(step *StepConfig) error {
		visited = append(visited, step.Name)
		return nil
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(visited) != 2 {
		t.Errorf("expected 2 visited aggregate steps, got %d", len(visited))
	}
}

func TestIsFilePath(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{"empty string", "", false},
		{"whitespace only", "   ", false},
		{"json object", `{"key": "value"}`, false},
		{"json array", `[{"key": "value"}]`, false},
		{"absolute path", "/etc/config.json", true},
		{"relative current dir", "./config.json", true},
		{"relative parent dir", "../config.json", true},
		{"home relative", "~/config.json", true},
		{"json extension", "config.json", true},
		{"just a word", "config", false},
		{"json with whitespace", "  {}", false},
		{"path with whitespace", "  /etc/config.json  ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isFilePath(tt.input)
			if got != tt.expect {
				t.Errorf("isFilePath(%q) = %v, want %v", tt.input, got, tt.expect)
			}
		})
	}
}

func TestLoadPipelineConfigFromString(t *testing.T) {
	jsonConfig := `{
		"version": "1",
		"name": "test-pipeline",
		"description": "Test pipeline",
		"steps": [
			{"name": "push_parsed", "description": "Push parsed"},
			{"name": "builds", "description": "Builds", "aggregates": "build", "prerequisites": ["push_parsed"]}
		]
	}`

	config, err := LoadPipelineConfigFromString(jsonConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.Name != "test-pipeline" {
		t.Errorf("Name = %q, want 'test-pipeline'", config.Name)
	}
	if len(config.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(config.Steps))
	}
}

func TestLoadPipelineConfigFromString_Invalid(t *testing.T) {
	_, err := LoadPipelineConfigFromString("not json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLoadPipelineConfigFromFile(t *testing.T) {
	// Test with a non-existent file
	_, err := LoadPipelineConfigFromFile("/nonexistent/path/config.json")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestPipelineConfig_GetStep(t *testing.T) {
	config := testPipelineConfigForTests()

	// Test finding existing step
	step := config.GetStep("push_parsed")
	if step == nil {
		t.Fatal("expected to find push_parsed step")
	}
	if step.Name != "push_parsed" {
		t.Errorf("step.Name = %q, want 'push_parsed'", step.Name)
	}

	// Test finding step with aggregates
	step = config.GetStep("builds_completed")
	if step == nil {
		t.Fatal("expected to find builds_completed step")
	}
	if step.Aggregates != "build" {
		t.Errorf("step.Aggregates = %q, want 'build'", step.Aggregates)
	}

	// Test non-existent step
	step = config.GetStep("non_existent")
	if step != nil {
		t.Error("expected nil for non-existent step")
	}
}

func TestPipelineConfig_ForEachStep_Error(t *testing.T) {
	config := testPipelineConfigForTests()

	callCount := 0
	testErr := errors.New("test error")

	err := config.ForEachStep(func(step *StepConfig) error {
		callCount++
		if callCount == 2 {
			return testErr
		}
		return nil
	})

	if err != testErr {
		t.Errorf("expected testErr, got %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}
}

func TestPipelineConfig_ForEachAggregateStep_Error(t *testing.T) {
	config := testPipelineConfigForTests()

	testErr := errors.New("test aggregate error")

	err := config.ForEachAggregateStep(func(step *StepConfig) error {
		return testErr
	})

	if err != testErr {
		t.Errorf("expected testErr, got %v", err)
	}
}

func TestPipelineConfig_GetAggregateStep_Extended(t *testing.T) {
	config := testPipelineConfigForTests()

	// builds_completed uses "build" as aggregates, so aggregateMap["build"] = "builds_completed"
	step := config.GetAggregateStep("build")
	if step != "builds_completed" {
		t.Errorf("GetAggregateStep('build') = %q, want 'builds_completed'", step)
	}

	// Non-existent component
	step = config.GetAggregateStep("non_existent")
	if step != "" {
		t.Errorf("GetAggregateStep('non_existent') = %q, want empty string", step)
	}
}

func TestPipelineConfig_GetAggregateColumnName(t *testing.T) {
	config := testPipelineConfigForTests()

	// For aggregate step, column name is the step name itself
	colName := config.GetAggregateColumnName("builds_completed")
	if colName != "builds_completed" {
		t.Errorf("GetAggregateColumnName('builds_completed') = %q, want 'builds_completed'", colName)
	}

	// Non-aggregate step should return empty
	colName = config.GetAggregateColumnName("push_parsed")
	if colName != "" {
		t.Errorf("GetAggregateColumnName('push_parsed') = %q, want empty string", colName)
	}

	// Non-existent step should return empty
	colName = config.GetAggregateColumnName("non_existent")
	if colName != "" {
		t.Errorf("GetAggregateColumnName('non_existent') = %q, want empty string", colName)
	}
}

func TestPipelineConfig_Validate_Errors(t *testing.T) {
	tests := []struct {
		name        string
		config      string
		errContains string
	}{
		{
			name:        "empty steps",
			config:      `{"version": "1", "name": "test", "steps": []}`,
			errContains: "at least one step",
		},
		{
			name:        "first step has prerequisites",
			config:      `{"version": "1", "name": "test", "steps": [{"name": "step1", "prerequisites": ["other"]}]}`,
			errContains: "first step",
		},
		{
			name:        "empty step name",
			config:      `{"version": "1", "name": "test", "steps": [{"name": ""}]}`,
			errContains: "name cannot be empty",
		},
		{
			name:        "duplicate step names",
			config:      `{"version": "1", "name": "test", "steps": [{"name": "step1"}, {"name": "step1"}]}`,
			errContains: "duplicate step name",
		},
		{
			name:        "unknown prerequisite",
			config:      `{"version": "1", "name": "test", "steps": [{"name": "step1"}, {"name": "step2", "prerequisites": ["unknown"]}]}`,
			errContains: "unknown prerequisite",
		},
		{
			name:        "duplicate aggregate names",
			config:      `{"version": "1", "name": "test", "steps": [{"name": "step1"}, {"name": "step2", "aggregates": "comp", "prerequisites": ["step1"]}, {"name": "step3", "aggregates": "comp", "prerequisites": ["step1"]}]}`,
			errContains: "aggregate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadPipelineConfigFromString(tt.config)
			if err == nil {
				t.Fatal("expected error")
			}
			if tt.errContains != "" && !containsStringHelper(err.Error(), tt.errContains) {
				t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
			}
		})
	}
}

// containsStringHelper checks if s contains substr
func containsStringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestPipelineConfig_ConfigHash(t *testing.T) {
	jsonConfig := `{
		"version": "1",
		"name": "test-pipeline",
		"steps": [{"name": "push_parsed"}]
	}`

	config, err := LoadPipelineConfigFromString(jsonConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hash := config.ConfigHash()
	if hash == "" {
		t.Error("expected non-empty config hash")
	}
	if len(hash) != 64 { // SHA-256 produces 64-char hex string
		t.Errorf("expected 64-char hash, got %d chars", len(hash))
	}
}

func TestParsePipelineConfig_InvalidJSON(t *testing.T) {
	_, err := ParsePipelineConfig([]byte("not valid json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestPipelineConfig_GetEffectivePrerequisites_Extended(t *testing.T) {
	config := testPipelineConfigForTests()

	// Step without component name
	prereqs := config.GetEffectivePrerequisites("builds_completed")
	expected := []string{"push_parsed"}
	if len(prereqs) != len(expected) {
		t.Errorf("GetEffectivePrerequisites() = %v, want %v", prereqs, expected)
	}

	// Non-existent step
	prereqs = config.GetEffectivePrerequisites("non_existent")
	if prereqs != nil {
		t.Errorf("GetEffectivePrerequisites for non-existent step should return nil, got %v", prereqs)
	}
}

func TestPipelineConfig_CircularDependencies(t *testing.T) {
	// Circular dependency: step3 -> step2 -> step3
	circularConfig := `{
		"version": "1",
		"name": "circular-test",
		"steps": [
			{"name": "step1"},
			{"name": "step2", "prerequisites": ["step1", "step3"]},
			{"name": "step3", "prerequisites": ["step2"]}
		]
	}`

	_, err := LoadPipelineConfigFromString(circularConfig)
	if err == nil {
		t.Fatal("expected error for circular dependency")
	}
	if !containsStringHelper(err.Error(), "circular") {
		t.Errorf("error should mention circular dependency: %v", err)
	}
}

func TestPipelineConfig_initialize_Coverage(t *testing.T) {
	// Test initialization with a step that is gate and has aggregates
	jsonConfig := `{
		"version": "1",
		"name": "test-init",
		"steps": [
			{"name": "push_parsed"},
			{"name": "builds_gate", "prerequisites": ["push_parsed"], "aggregates": "build", "is_gate": true}
		]
	}`

	config, err := LoadPipelineConfigFromString(jsonConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify stepsByName is populated
	if config.GetStep("push_parsed") == nil {
		t.Error("push_parsed should be in stepsByName")
	}

	// Verify aggregateMap is populated
	if config.GetAggregateStep("build") != "builds_gate" {
		t.Errorf("aggregateMap should map build to builds_gate")
	}

	// Verify gateSteps is populated
	gates := config.GetGateSteps()
	if len(gates) != 1 || gates[0] != "builds_gate" {
		t.Errorf("gateSteps should contain builds_gate, got %v", gates)
	}
}
