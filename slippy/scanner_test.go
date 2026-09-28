package slippy

import (
	"testing"
)

func TestSlipScanner_BuildScanContext(t *testing.T) {
	config := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "push_parsed"},
			{Name: "builds", Aggregates: "build"},
			{Name: "tests"},
		},
	}
	config.stepsByName = make(map[string]*StepConfig)
	for i := range config.Steps {
		config.stepsByName[config.Steps[i].Name] = &config.Steps[i]
	}

	scanner := NewSlipScanner(config)

	t.Run("creates context with correct number of destinations", func(t *testing.T) {
		ctx := scanner.BuildScanContext()

		// Core columns: 12 (correlation_id, repository, branch, commit_sha,
		// created_at, updated_at, status, step_details, state_history, ancestry, sign, version)
		// + 3 step status columns (push_parsed_status, builds_status, tests_status)
		// + 1 aggregate column (builds)
		// = 16 total
		if ctx.slip == nil {
			t.Fatal("slip should not be nil")
		}
		if len(ctx.stepStatuses) != 3 {
			t.Errorf("expected 3 step statuses, got %d", len(ctx.stepStatuses))
		}
		if len(ctx.aggregateJSONs) != 1 {
			t.Errorf("expected 1 aggregate JSON, got %d", len(ctx.aggregateJSONs))
		}
	})

	t.Run("creates context with extra destinations", func(t *testing.T) {
		var extraDest string
		ctx := scanner.BuildScanContext(&extraDest)

		// The extra destination should be included
		expectedLen := len(scanner.BuildScanContext().scanDest) + 1
		if len(ctx.scanDest) != expectedLen {
			t.Errorf("expected %d scan destinations, got %d", expectedLen, len(ctx.scanDest))
		}
	})
}

func TestNewSlipScanner(t *testing.T) {
	config := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "push_parsed"},
		},
	}

	scanner := NewSlipScanner(config)

	if scanner == nil {
		t.Fatal("NewSlipScanner should not return nil")
	}
	if scanner.config != config {
		t.Error("scanner should reference the provided config")
	}
}
