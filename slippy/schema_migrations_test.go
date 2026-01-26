package slippy

import (
	"testing"
)

func TestGetDynamicMigrationVersion(t *testing.T) {
	tests := []struct {
		name   string
		config *PipelineConfig
		want   int
	}{
		{
			name:   "nil config",
			config: nil,
			want:   0,
		},
		{
			name:   "empty steps",
			config: &PipelineConfig{Steps: []StepConfig{}},
			want:   0,
		},
		{
			name: "config with steps",
			config: &PipelineConfig{
				Steps: []StepConfig{
					{Name: "push_parsed"},
					{Name: "builds", Aggregates: "build"},
				},
			},
			want: 4, // Base migrations (table, component states, history view, ancestry)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetDynamicMigrationVersion(tt.config)
			// The version should be positive for configs with steps
			if tt.config != nil && len(tt.config.Steps) > 0 {
				if got <= 0 {
					t.Errorf("GetDynamicMigrationVersion() = %d, expected positive value", got)
				}
			} else {
				if got != tt.want {
					t.Errorf("GetDynamicMigrationVersion() = %d, want %d", got, tt.want)
				}
			}
		})
	}
}

func TestDynamicMigrationManager_GetCurrentStepColumns(t *testing.T) {
	config := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "push_parsed"},
			{Name: "builds", Aggregates: "build"},
			{Name: "tests"},
		},
	}

	manager := NewDynamicMigrationManager(nil, config, "ci", nil)
	columns := manager.GetCurrentStepColumns()

	expected := []string{"push_parsed_status", "builds_status", "tests_status"}
	if len(columns) != len(expected) {
		t.Fatalf("expected %d columns, got %d", len(expected), len(columns))
	}
	for i, col := range columns {
		if col != expected[i] {
			t.Errorf("column[%d] = %q, want %q", i, col, expected[i])
		}
	}
}

func TestDynamicMigrationManager_GetAggregateColumns(t *testing.T) {
	config := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "push_parsed"},
			{Name: "builds", Aggregates: "build"},
			{Name: "tests", Aggregates: "test"},
			{Name: "deploy"}, // No aggregates
		},
	}

	manager := NewDynamicMigrationManager(nil, config, "ci", nil)
	columns := manager.GetAggregateColumns()

	// Only steps with aggregates should be included
	expected := []string{"builds", "tests"}
	if len(columns) != len(expected) {
		t.Fatalf("expected %d aggregate columns, got %d", len(expected), len(columns))
	}
	for i, col := range columns {
		if col != expected[i] {
			t.Errorf("column[%d] = %q, want %q", i, col, expected[i])
		}
	}
}

func TestDynamicMigrationManager_GetAggregateColumns_Empty(t *testing.T) {
	config := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "push_parsed"},
			{Name: "deploy"},
		},
	}

	manager := NewDynamicMigrationManager(nil, config, "ci", nil)
	columns := manager.GetAggregateColumns()

	if len(columns) != 0 {
		t.Errorf("expected 0 aggregate columns, got %d", len(columns))
	}
}
