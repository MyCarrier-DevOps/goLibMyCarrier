package slippy

import (
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
