package slippy

import (
	"testing"
	"time"
)

func TestSlip_Initialization(t *testing.T) {
	slip := &Slip{
		CorrelationID: "test-corr-001",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "abc123",
		Status:        SlipStatusPending,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if slip.CorrelationID != "test-corr-001" {
		t.Errorf("CorrelationID = %q, want 'test-corr-001'", slip.CorrelationID)
	}
	if slip.Status != SlipStatusPending {
		t.Errorf("Status = %v, want %v", slip.Status, SlipStatusPending)
	}
}

func TestSlip_WithAggregates(t *testing.T) {
	slip := &Slip{
		CorrelationID: "test-corr-002",
		Aggregates: map[string][]ComponentStepData{
			"builds": {
				{
					Component: "api",
					Status:    StepStatusPending,
				},
				{
					Component: "worker",
					Status:    StepStatusPending,
				},
			},
			"unit_tests": {
				{
					Component: "api",
					Status:    StepStatusPending,
				},
				{
					Component: "worker",
					Status:    StepStatusPending,
				},
			},
		},
	}

	if len(slip.Aggregates["builds"]) != 2 {
		t.Errorf("len(Aggregates[builds]) = %d, want 2", len(slip.Aggregates["builds"]))
	}
	if slip.Aggregates["builds"][0].Component != "api" {
		t.Errorf("Aggregates[builds][0].Component = %q, want 'api'", slip.Aggregates["builds"][0].Component)
	}
}

func TestSlip_WithSteps(t *testing.T) {
	now := time.Now()
	slip := &Slip{
		CorrelationID: "test-corr-003",
		Steps: map[string]Step{
			"push_parsed": {
				Status:      StepStatusCompleted,
				StartedAt:   &now,
				CompletedAt: &now,
				Actor:       "parser",
			},
			"builds_completed": {
				Status: StepStatusPending,
			},
		},
	}

	if len(slip.Steps) != 2 {
		t.Errorf("len(Steps) = %d, want 2", len(slip.Steps))
	}
	if slip.Steps["push_parsed"].Status != StepStatusCompleted {
		t.Errorf("Steps['push_parsed'].Status = %v, want %v", slip.Steps["push_parsed"].Status, StepStatusCompleted)
	}
}

func TestSlip_WithStateHistory(t *testing.T) {
	now := time.Now()
	slip := &Slip{
		CorrelationID: "test-corr-004",
		StateHistory: []StateHistoryEntry{
			{
				Step:      "push_parsed",
				Status:    StepStatusCompleted,
				Timestamp: now,
				Actor:     "parser",
				Message:   "Push parsed successfully",
			},
		},
	}

	if len(slip.StateHistory) != 1 {
		t.Errorf("len(StateHistory) = %d, want 1", len(slip.StateHistory))
	}
	if slip.StateHistory[0].Step != "push_parsed" {
		t.Errorf("StateHistory[0].Step = %q, want 'push_parsed'", slip.StateHistory[0].Step)
	}
}

func TestStep_Fields(t *testing.T) {
	now := time.Now()
	step := Step{
		Status:      StepStatusFailed,
		StartedAt:   &now,
		CompletedAt: &now,
		Actor:       "builder",
		Error:       "build failed",
	}

	if step.Status != StepStatusFailed {
		t.Errorf("Status = %v, want %v", step.Status, StepStatusFailed)
	}
	if step.Error != "build failed" {
		t.Errorf("Error = %q, want 'build failed'", step.Error)
	}
}

func TestStateHistoryEntry_Fields(t *testing.T) {
	now := time.Now()
	entry := StateHistoryEntry{
		Step:      "dev_deploy",
		Component: "api",
		Status:    StepStatusCompleted,
		Timestamp: now,
		Actor:     "deployer",
		Message:   "Deployed to dev environment",
	}

	if entry.Step != "dev_deploy" {
		t.Errorf("Step = %q, want 'dev_deploy'", entry.Step)
	}
	if entry.Component != "api" {
		t.Errorf("Component = %q, want 'api'", entry.Component)
	}
}

func TestSlipOptions_Fields(t *testing.T) {
	opts := SlipOptions{
		CorrelationID: "push-123",
		Repository:    "owner/repo",
		Branch:        "feature-branch",
		CommitSHA:     "abc123def456",
		Components: []ComponentDefinition{
			{Name: "api", DockerfilePath: "api/Dockerfile"},
		},
	}

	if opts.CorrelationID != "push-123" {
		t.Errorf("CorrelationID = %q, want 'push-123'", opts.CorrelationID)
	}
	if len(opts.Components) != 1 {
		t.Errorf("len(Components) = %d, want 1", len(opts.Components))
	}
}

func TestComponentDefinition_Fields(t *testing.T) {
	def := ComponentDefinition{
		Name:           "worker",
		DockerfilePath: "services/worker/Dockerfile",
	}

	if def.Name != "worker" {
		t.Errorf("Name = %q, want 'worker'", def.Name)
	}
}

func TestPipelineConfig_GetAggregateStep(t *testing.T) {
	config := &PipelineConfig{
		Version:     "1",
		Name:        "test-pipeline",
		Description: "Test pipeline",
		Steps: []StepConfig{
			{Name: "push_parsed", Description: "Push parsed"},
			{Name: "builds_completed", Description: "Builds completed", Aggregates: "build"},
			{Name: "unit_tests_completed", Description: "Unit tests completed", Aggregates: "unit_test"},
			{Name: "dev_deploy", Description: "Dev deploy"},
		},
	}
	// Initialize internal maps
	config.stepsByName = make(map[string]*StepConfig)
	config.aggregateMap = make(map[string]string)
	for i := range config.Steps {
		step := &config.Steps[i]
		config.stepsByName[step.Name] = step
		if step.Aggregates != "" {
			config.aggregateMap[step.Aggregates] = step.Name
		}
	}

	// Test finding aggregate steps - GetAggregateStep returns the step name as a string
	if step := config.GetAggregateStep("build"); step != "builds_completed" {
		t.Errorf("GetAggregateStep('build') should return 'builds_completed', got '%s'", step)
	}
	if step := config.GetAggregateStep("unit_test"); step != "unit_tests_completed" {
		t.Errorf("GetAggregateStep('unit_test') should return 'unit_tests_completed', got '%s'", step)
	}
	if step := config.GetAggregateStep("nonexistent"); step != "" {
		t.Errorf("GetAggregateStep('nonexistent') should return empty string, got '%s'", step)
	}
}

func TestPipelineConfig_GetEffectivePrerequisites(t *testing.T) {
	config := &PipelineConfig{
		Version:     "1",
		Name:        "test-pipeline",
		Description: "Test pipeline",
		Steps: []StepConfig{
			{Name: "push_parsed", Description: "Push parsed"},
			{
				Name:          "builds_completed",
				Description:   "Builds completed",
				Aggregates:    "build",
				Prerequisites: []string{"push_parsed"},
			},
			{Name: "alert_gate", Description: "Alert gate", IsGate: true, Prerequisites: []string{"builds_completed"}},
			{Name: "dev_deploy", Description: "Dev deploy", Prerequisites: []string{"alert_gate"}},
		},
	}
	// Initialize internal maps
	config.stepsByName = make(map[string]*StepConfig)
	config.gateSteps = make([]string, 0)
	for i := range config.Steps {
		step := &config.Steps[i]
		config.stepsByName[step.Name] = step
		if step.IsGate {
			config.gateSteps = append(config.gateSteps, step.Name)
		}
	}

	// Test effective prerequisites
	prereqs := config.GetEffectivePrerequisites("dev_deploy")
	if len(prereqs) != 1 || prereqs[0] != "alert_gate" {
		t.Errorf("GetEffectivePrerequisites('dev_deploy') = %v, want ['alert_gate']", prereqs)
	}

	// First step has no prerequisites
	prereqs = config.GetEffectivePrerequisites("push_parsed")
	if len(prereqs) != 0 {
		t.Errorf("GetEffectivePrerequisites('push_parsed') = %v, want []", prereqs)
	}
}
