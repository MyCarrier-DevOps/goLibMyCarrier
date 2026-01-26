package slippy

import (
	"strings"
	"testing"
)

func TestNewSlipQueryBuilder(t *testing.T) {
	config := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "push_parsed"},
			{Name: "builds", Aggregates: "component"},
		},
	}

	tests := []struct {
		name         string
		database     string
		wantDatabase string
	}{
		{"with database", "custom_db", "custom_db"},
		{"empty database defaults to ci", "", "ci"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qb := NewSlipQueryBuilder(config, tt.database)
			if qb.Database() != tt.wantDatabase {
				t.Errorf("Database() = %q, want %q", qb.Database(), tt.wantDatabase)
			}
		})
	}
}

func TestSlipQueryBuilder_BuildSelectColumns(t *testing.T) {
	config := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "push_parsed"},
			{Name: "builds", Aggregates: "component"},
			{Name: "tests"},
		},
	}

	qb := NewSlipQueryBuilder(config, "ci")
	columns := qb.BuildSelectColumns()

	// Check core columns are present
	coreColumns := []string{
		ColumnCorrelationID, ColumnRepository, ColumnBranch, ColumnCommitSHA,
		ColumnCreatedAt, ColumnUpdatedAt, ColumnStatus,
		ColumnStepDetails, ColumnStateHistory, ColumnAncestry,
		ColumnSign, ColumnVersion,
	}
	for _, col := range coreColumns {
		found := false
		for _, c := range columns {
			if c == col {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected core column %q not found", col)
		}
	}

	// Check step status columns
	expectedStatusColumns := []string{"push_parsed_status", "builds_status", "tests_status"}
	for _, col := range expectedStatusColumns {
		found := false
		for _, c := range columns {
			if c == col {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected step status column %q not found", col)
		}
	}

	// Check aggregate column (only builds has aggregates)
	if !containsColumn(columns, "builds") {
		t.Error("expected aggregate column 'builds' not found")
	}
}

func TestSlipQueryBuilder_BuildSelectColumnsWithPrefix(t *testing.T) {
	config := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "push_parsed"},
		},
	}

	qb := NewSlipQueryBuilder(config, "ci")
	result := qb.BuildSelectColumnsWithPrefix("s.")

	// Check that all columns have the prefix
	columns := strings.Split(result, ", ")
	for _, col := range columns {
		if !strings.HasPrefix(col, "s.") {
			t.Errorf("column %q does not have prefix 's.'", col)
		}
	}
}

func TestSlipQueryBuilder_BuildSelectQuery(t *testing.T) {
	config := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "push_parsed"},
		},
	}

	qb := NewSlipQueryBuilder(config, "testdb")
	query := qb.BuildSelectQuery("WHERE correlation_id = ?", "LIMIT 1")

	if !strings.Contains(query, "testdb.routing_slips FINAL") {
		t.Error("query should contain 'testdb.routing_slips FINAL'")
	}
	if !strings.Contains(query, "WHERE correlation_id = ?") {
		t.Error("query should contain WHERE clause")
	}
	if !strings.Contains(query, "LIMIT 1") {
		t.Error("query should contain suffix")
	}
}

func TestSlipQueryBuilder_BuildInsertQuery(t *testing.T) {
	config := &PipelineConfig{
		Steps: []StepConfig{},
	}

	qb := NewSlipQueryBuilder(config, "ci")
	columns := []string{"col1", "col2", "col3"}
	placeholders := []string{"?", "?", "?"}
	query := qb.BuildInsertQuery(columns, placeholders)

	if !strings.Contains(query, "INSERT INTO ci.routing_slips") {
		t.Error("query should contain INSERT statement")
	}
	if !strings.Contains(query, "col1, col2, col3") {
		t.Error("query should contain column names")
	}
	if !strings.Contains(query, "?, ?, ?") {
		t.Error("query should contain placeholders")
	}
}

func TestSlipQueryBuilder_StepStatusColumn(t *testing.T) {
	qb := NewSlipQueryBuilder(&PipelineConfig{}, "ci")

	tests := []struct {
		stepName string
		want     string
	}{
		{"push_parsed", "push_parsed_status"},
		{"builds", "builds_status"},
		{"deploy", "deploy_status"},
	}

	for _, tt := range tests {
		t.Run(tt.stepName, func(t *testing.T) {
			got := qb.StepStatusColumn(tt.stepName)
			if got != tt.want {
				t.Errorf("StepStatusColumn(%q) = %q, want %q", tt.stepName, got, tt.want)
			}
		})
	}
}

func TestSlipQueryBuilder_AggregateColumn(t *testing.T) {
	qb := NewSlipQueryBuilder(&PipelineConfig{}, "ci")

	tests := []struct {
		stepName string
		want     string
	}{
		{"builds", "builds"},
		{"tests", "tests"},
		{"deploy", "deploy"},
	}

	for _, tt := range tests {
		t.Run(tt.stepName, func(t *testing.T) {
			got := qb.AggregateColumn(tt.stepName)
			if got != tt.want {
				t.Errorf("AggregateColumn(%q) = %q, want %q", tt.stepName, got, tt.want)
			}
		})
	}
}

func TestSlipQueryBuilder_BuildFindByCommitsQuery(t *testing.T) {
	config := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "push_parsed"},
		},
	}

	qb := NewSlipQueryBuilder(config, "ci")
	query := qb.BuildFindByCommitsQuery()

	if !strings.Contains(query, "WITH commits AS") {
		t.Error("query should contain CTE")
	}
	if !strings.Contains(query, "{commits:Array(String)}") {
		t.Error("query should contain commits placeholder")
	}
	if !strings.Contains(query, "ci.routing_slips s FINAL") {
		t.Error("query should contain table reference")
	}
	if !strings.Contains(query, "LIMIT 1") {
		t.Error("query should have LIMIT 1")
	}
}

func TestSlipQueryBuilder_BuildFindAllByCommitsQuery(t *testing.T) {
	config := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "push_parsed"},
		},
	}

	qb := NewSlipQueryBuilder(config, "ci")
	query := qb.BuildFindAllByCommitsQuery()

	if !strings.Contains(query, "WITH commits AS") {
		t.Error("query should contain CTE")
	}
	if !strings.Contains(query, "ORDER BY c.priority ASC") {
		t.Error("query should have ORDER BY clause")
	}
	// Should NOT have LIMIT 1 (unlike BuildFindByCommitsQuery)
	if strings.Contains(query, "LIMIT 1") {
		t.Error("BuildFindAllByCommitsQuery should NOT have LIMIT 1")
	}
}

func TestSlipQueryBuilder_BuildStepColumnsAndValues(t *testing.T) {
	config := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "push_parsed"},
			{Name: "builds"},
			{Name: "tests"},
		},
	}

	qb := NewSlipQueryBuilder(config, "ci")

	// Test with empty steps map
	columns, placeholders, values := qb.BuildStepColumnsAndValues(nil)

	if len(columns) != 3 {
		t.Errorf("expected 3 columns, got %d", len(columns))
	}
	if len(placeholders) != 3 {
		t.Errorf("expected 3 placeholders, got %d", len(placeholders))
	}
	if len(values) != 3 {
		t.Errorf("expected 3 values, got %d", len(values))
	}

	// All values should default to "pending"
	for i, v := range values {
		if v != string(StepStatusPending) {
			t.Errorf("values[%d] = %v, want 'pending'", i, v)
		}
	}

	// Test with some steps set
	steps := map[string]Step{
		"push_parsed": {Status: StepStatusCompleted},
		"builds":      {Status: StepStatusRunning},
	}
	_, _, values2 := qb.BuildStepColumnsAndValues(steps)

	if values2[0] != string(StepStatusCompleted) {
		t.Errorf("push_parsed status = %v, want 'completed'", values2[0])
	}
	if values2[1] != string(StepStatusRunning) {
		t.Errorf("builds status = %v, want 'running'", values2[1])
	}
	if values2[2] != string(StepStatusPending) {
		t.Errorf("tests status = %v, want 'pending'", values2[2])
	}
}

func TestSlipQueryBuilder_BuildAggregateColumnsAndValues(t *testing.T) {
	config := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "push_parsed"}, // No aggregates
			{Name: "builds", Aggregates: "component"},
			{Name: "tests", Aggregates: "component"},
		},
	}

	qb := NewSlipQueryBuilder(config, "ci")

	// Test with nil aggregates
	columns, placeholders, values := qb.BuildAggregateColumnsAndValues(nil)

	// Only 2 columns (builds and tests have aggregates)
	if len(columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(columns))
	}
	if len(placeholders) != 2 {
		t.Errorf("expected 2 placeholders, got %d", len(placeholders))
	}
	if len(values) != 2 {
		t.Errorf("expected 2 values, got %d", len(values))
	}

	// Values should be empty JSON arrays
	for i, v := range values {
		str := v.(string)
		if !strings.Contains(str, `"items":[]`) {
			t.Errorf("values[%d] = %v, want empty items array", i, v)
		}
	}

	// Test with some aggregate data
	aggregates := map[string][]ComponentStepData{
		"builds": {
			{Component: "svc-a", Status: StepStatusCompleted},
		},
	}
	_, _, values2 := qb.BuildAggregateColumnsAndValues(aggregates)

	buildsVal := values2[0].(string)
	if !strings.Contains(buildsVal, "svc-a") {
		t.Error("builds aggregate should contain svc-a")
	}
}

func TestSlipQueryBuilder_GetStepCount(t *testing.T) {
	tests := []struct {
		name  string
		steps []StepConfig
		want  int
	}{
		{"no steps", []StepConfig{}, 0},
		{"one step", []StepConfig{{Name: "push_parsed"}}, 1},
		{"multiple steps", []StepConfig{{Name: "a"}, {Name: "b"}, {Name: "c"}}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &PipelineConfig{Steps: tt.steps}
			qb := NewSlipQueryBuilder(config, "ci")
			if got := qb.GetStepCount(); got != tt.want {
				t.Errorf("GetStepCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSlipQueryBuilder_GetAggregateSteps(t *testing.T) {
	config := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "push_parsed"},
			{Name: "builds", Aggregates: "component"},
			{Name: "tests"},
			{Name: "deploy", Aggregates: "component"},
		},
	}

	qb := NewSlipQueryBuilder(config, "ci")
	aggregateSteps := qb.GetAggregateSteps()

	if len(aggregateSteps) != 2 {
		t.Errorf("expected 2 aggregate steps, got %d", len(aggregateSteps))
	}

	// Verify the right steps are returned
	names := make(map[string]bool)
	for _, s := range aggregateSteps {
		names[s.Name] = true
	}
	if !names["builds"] {
		t.Error("expected 'builds' to be an aggregate step")
	}
	if !names["deploy"] {
		t.Error("expected 'deploy' to be an aggregate step")
	}
}

// containsColumn checks if a column exists in the slice
func containsColumn(columns []string, target string) bool {
	for _, c := range columns {
		if c == target {
			return true
		}
	}
	return false
}
