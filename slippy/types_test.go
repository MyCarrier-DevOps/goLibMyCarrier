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

func TestSlip_WithComponents(t *testing.T) {
	slip := &Slip{
		CorrelationID: "test-corr-002",
		Components: []Component{
			{
				Name:           "api",
				DockerfilePath: "services/api/Dockerfile",
				BuildStatus:    StepStatusPending,
				UnitTestStatus: StepStatusPending,
			},
			{
				Name:           "worker",
				DockerfilePath: "services/worker/Dockerfile",
				BuildStatus:    StepStatusPending,
				UnitTestStatus: StepStatusPending,
			},
		},
	}

	if len(slip.Components) != 2 {
		t.Errorf("len(Components) = %d, want 2", len(slip.Components))
	}
	if slip.Components[0].Name != "api" {
		t.Errorf("Components[0].Name = %q, want 'api'", slip.Components[0].Name)
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

func TestComponent_Fields(t *testing.T) {
	comp := Component{
		Name:           "api",
		DockerfilePath: "services/api/Dockerfile",
		BuildStatus:    StepStatusCompleted,
		UnitTestStatus: StepStatusRunning,
		ImageTag:       "v1.2.3",
	}

	if comp.Name != "api" {
		t.Errorf("Name = %q, want 'api'", comp.Name)
	}
	if comp.BuildStatus != StepStatusCompleted {
		t.Errorf("BuildStatus = %v, want %v", comp.BuildStatus, StepStatusCompleted)
	}
	if comp.ImageTag != "v1.2.3" {
		t.Errorf("ImageTag = %q, want 'v1.2.3'", comp.ImageTag)
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

func TestPipelineSteps(t *testing.T) {
	expectedSteps := []string{
		"push_parsed",
		"builds_completed",
		"unit_tests_completed",
		"secret_scan_completed",
		"dev_deploy",
		"dev_tests",
		"preprod_deploy",
		"preprod_tests",
		"prod_release_created",
		"prod_deploy",
		"prod_tests",
		"alert_gate",
		"prod_steady_state",
	}

	if len(PipelineSteps) != len(expectedSteps) {
		t.Errorf("len(PipelineSteps) = %d, want %d", len(PipelineSteps), len(expectedSteps))
	}

	for i, step := range expectedSteps {
		if PipelineSteps[i] != step {
			t.Errorf("PipelineSteps[%d] = %q, want %q", i, PipelineSteps[i], step)
		}
	}
}

func TestAggregateStepMap(t *testing.T) {
	if AggregateStepMap["build"] != "builds_completed" {
		t.Errorf("AggregateStepMap['build'] = %q, want 'builds_completed'", AggregateStepMap["build"])
	}
	if AggregateStepMap["unit_test"] != "unit_tests_completed" {
		t.Errorf("AggregateStepMap['unit_test'] = %q, want 'unit_tests_completed'", AggregateStepMap["unit_test"])
	}
}
