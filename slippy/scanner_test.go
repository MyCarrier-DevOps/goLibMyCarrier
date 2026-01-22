package slippy

import (
	"testing"
	"time"
)

func TestReconstructStepTimingFromHistory(t *testing.T) {
	// Create a minimal config for the scanner
	config := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "unit_tests_completed", Description: "Unit tests"},
			{Name: "builds_completed", Description: "Builds", Aggregates: "build"},
			{Name: "secret_scan", Description: "Secret scan"},
		},
	}
	config.stepsByName = make(map[string]*StepConfig)
	for i := range config.Steps {
		config.stepsByName[config.Steps[i].Name] = &config.Steps[i]
	}

	scanner := NewSlipScanner(config)

	t.Run("fills missing StartedAt from history", func(t *testing.T) {
		startTime := time.Date(2026, 1, 22, 16, 16, 13, 0, time.UTC)

		slip := &Slip{
			Steps: map[string]Step{
				"unit_tests_completed": {Status: StepStatusRunning, StartedAt: nil},
			},
			StateHistory: []StateHistoryEntry{
				{
					Step:      "unit_tests_completed",
					Status:    StepStatusRunning,
					Timestamp: startTime,
					Message:   "step started",
				},
			},
		}

		scanner.reconstructStepTimingFromHistory(slip)

		step := slip.Steps["unit_tests_completed"]
		if step.StartedAt == nil {
			t.Fatal("StartedAt should not be nil after reconstruction")
		}
		if !step.StartedAt.Equal(startTime) {
			t.Errorf("StartedAt = %v, want %v", step.StartedAt, startTime)
		}
	})

	t.Run("fills missing CompletedAt from history", func(t *testing.T) {
		startTime := time.Date(2026, 1, 22, 16, 16, 13, 0, time.UTC)
		endTime := time.Date(2026, 1, 22, 16, 19, 10, 0, time.UTC)

		slip := &Slip{
			Steps: map[string]Step{
				"unit_tests_completed": {Status: StepStatusCompleted, StartedAt: nil, CompletedAt: nil},
			},
			StateHistory: []StateHistoryEntry{
				{
					Step:      "unit_tests_completed",
					Status:    StepStatusRunning,
					Timestamp: startTime,
					Message:   "step started",
				},
				{
					Step:      "unit_tests_completed",
					Status:    StepStatusCompleted,
					Timestamp: endTime,
					Message:   "step completed",
				},
			},
		}

		scanner.reconstructStepTimingFromHistory(slip)

		step := slip.Steps["unit_tests_completed"]
		if step.StartedAt == nil {
			t.Fatal("StartedAt should not be nil after reconstruction")
		}
		if step.CompletedAt == nil {
			t.Fatal("CompletedAt should not be nil after reconstruction")
		}
		if !step.StartedAt.Equal(startTime) {
			t.Errorf("StartedAt = %v, want %v", step.StartedAt, startTime)
		}
		if !step.CompletedAt.Equal(endTime) {
			t.Errorf("CompletedAt = %v, want %v", step.CompletedAt, endTime)
		}
	})

	t.Run("does not overwrite existing timing from step_details", func(t *testing.T) {
		// Existing timing from step_details (more precise)
		existingStartTime := time.Date(2026, 1, 22, 16, 16, 13, 500000000, time.UTC)

		// History has a different (less precise) timestamp
		historyStartTime := time.Date(2026, 1, 22, 16, 16, 14, 0, time.UTC)

		slip := &Slip{
			Steps: map[string]Step{
				"unit_tests_completed": {Status: StepStatusRunning, StartedAt: &existingStartTime},
			},
			StateHistory: []StateHistoryEntry{
				{
					Step:      "unit_tests_completed",
					Status:    StepStatusRunning,
					Timestamp: historyStartTime,
					Message:   "step started",
				},
			},
		}

		scanner.reconstructStepTimingFromHistory(slip)

		step := slip.Steps["unit_tests_completed"]
		if !step.StartedAt.Equal(existingStartTime) {
			t.Errorf("StartedAt was overwritten: got %v, want %v", step.StartedAt, existingStartTime)
		}
	})

	t.Run("ignores component-level history entries", func(t *testing.T) {
		componentStartTime := time.Date(2026, 1, 22, 16, 16, 15, 0, time.UTC)

		slip := &Slip{
			Steps: map[string]Step{
				"builds_completed": {Status: StepStatusRunning, StartedAt: nil},
			},
			StateHistory: []StateHistoryEntry{
				{
					Step:      "builds_completed",
					Component: "mc.example.api", // This is a component entry
					Status:    StepStatusRunning,
					Timestamp: componentStartTime,
					Message:   "component started",
				},
			},
		}

		scanner.reconstructStepTimingFromHistory(slip)

		step := slip.Steps["builds_completed"]
		// StartedAt should still be nil because we only had component entries
		if step.StartedAt != nil {
			t.Errorf("StartedAt should be nil for component-only history, got %v", step.StartedAt)
		}
	})

	t.Run("handles multiple steps with mixed history", func(t *testing.T) {
		unitTestStart := time.Date(2026, 1, 22, 16, 16, 13, 0, time.UTC)
		unitTestEnd := time.Date(2026, 1, 22, 16, 19, 10, 0, time.UTC)
		secretScanStart := time.Date(2026, 1, 22, 16, 31, 0, 0, time.UTC)
		secretScanEnd := time.Date(2026, 1, 22, 16, 31, 41, 0, time.UTC)

		slip := &Slip{
			Steps: map[string]Step{
				"unit_tests_completed": {Status: StepStatusCompleted},
				"secret_scan":          {Status: StepStatusCompleted},
				"builds_completed":     {Status: StepStatusCompleted},
			},
			StateHistory: []StateHistoryEntry{
				// unit_tests entries
				{Step: "unit_tests_completed", Status: StepStatusRunning, Timestamp: unitTestStart},
				{Step: "unit_tests_completed", Status: StepStatusCompleted, Timestamp: unitTestEnd},
				// builds_completed component entries (should be ignored)
				{Step: "builds_completed", Component: "api", Status: StepStatusRunning, Timestamp: time.Now()},
				{Step: "builds_completed", Component: "api", Status: StepStatusCompleted, Timestamp: time.Now()},
				// secret_scan entries
				{Step: "secret_scan", Status: StepStatusRunning, Timestamp: secretScanStart},
				{Step: "secret_scan", Status: StepStatusCompleted, Timestamp: secretScanEnd},
			},
		}

		scanner.reconstructStepTimingFromHistory(slip)

		// Check unit_tests_completed
		unitTest := slip.Steps["unit_tests_completed"]
		if unitTest.StartedAt == nil || !unitTest.StartedAt.Equal(unitTestStart) {
			t.Errorf("unit_tests_completed StartedAt = %v, want %v", unitTest.StartedAt, unitTestStart)
		}
		if unitTest.CompletedAt == nil || !unitTest.CompletedAt.Equal(unitTestEnd) {
			t.Errorf("unit_tests_completed CompletedAt = %v, want %v", unitTest.CompletedAt, unitTestEnd)
		}

		// Check secret_scan
		secretScan := slip.Steps["secret_scan"]
		if secretScan.StartedAt == nil || !secretScan.StartedAt.Equal(secretScanStart) {
			t.Errorf("secret_scan StartedAt = %v, want %v", secretScan.StartedAt, secretScanStart)
		}
		if secretScan.CompletedAt == nil || !secretScan.CompletedAt.Equal(secretScanEnd) {
			t.Errorf("secret_scan CompletedAt = %v, want %v", secretScan.CompletedAt, secretScanEnd)
		}

		// Check builds_completed (should have no timing - only component entries)
		builds := slip.Steps["builds_completed"]
		if builds.StartedAt != nil {
			t.Errorf("builds_completed StartedAt should be nil (only component entries), got %v", builds.StartedAt)
		}
	})

	t.Run("handles empty history", func(t *testing.T) {
		slip := &Slip{
			Steps: map[string]Step{
				"unit_tests_completed": {Status: StepStatusPending},
			},
			StateHistory: []StateHistoryEntry{},
		}

		// Should not panic
		scanner.reconstructStepTimingFromHistory(slip)

		step := slip.Steps["unit_tests_completed"]
		if step.StartedAt != nil {
			t.Errorf("StartedAt should be nil with empty history, got %v", step.StartedAt)
		}
	})

	t.Run("uses first running transition for StartedAt", func(t *testing.T) {
		firstRunning := time.Date(2026, 1, 22, 16, 16, 13, 0, time.UTC)
		secondRunning := time.Date(2026, 1, 22, 16, 20, 0, 0, time.UTC) // Retry after held

		slip := &Slip{
			Steps: map[string]Step{
				"unit_tests_completed": {Status: StepStatusRunning},
			},
			StateHistory: []StateHistoryEntry{
				{Step: "unit_tests_completed", Status: StepStatusRunning, Timestamp: firstRunning},
				{Step: "unit_tests_completed", Status: StepStatusHeld, Timestamp: time.Now()},
				{Step: "unit_tests_completed", Status: StepStatusRunning, Timestamp: secondRunning},
			},
		}

		scanner.reconstructStepTimingFromHistory(slip)

		step := slip.Steps["unit_tests_completed"]
		if step.StartedAt == nil || !step.StartedAt.Equal(firstRunning) {
			t.Errorf("StartedAt should be first running time %v, got %v", firstRunning, step.StartedAt)
		}
	})
}
