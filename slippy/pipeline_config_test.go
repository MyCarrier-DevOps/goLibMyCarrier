package slippy

import (
	"errors"
	"strings"
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

// Two step names that differ only in case are ONE Postgres column, because unquoted
// identifiers fold to lower case. Exact-case uniqueness admits both, and then every write
// builds `SET Deploy_status = $n, deploy_status = $m` — the same column twice in one SET list,
// which is 42701 on every update the slip ever takes. The config is where that is caught, not
// ProbeSchema: the probe folds case deliberately (a configured `Deploy_Dev` legitimately lands
// as `deploy_dev`), and that folding is exactly what hides this (PR #87 finding j6).
func TestPipelineConfig_Validate_RejectsStepNamesThatFoldTogether(t *testing.T) {
	config := &PipelineConfig{
		Version: "1",
		Name:    "case-collision",
		Steps: []StepConfig{
			{Name: "push_parsed"},
			{Name: "Deploy", Prerequisites: []string{"push_parsed"}},
			{Name: "deploy", Prerequisites: []string{"push_parsed"}},
		},
	}
	config.initialize()
	err := config.Validate()
	if err == nil {
		t.Fatal("expected a case-folding collision to be rejected")
	}
	for _, want := range []string{"Deploy", "deploy", "deploy_status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name both steps and the column they share; got %q, want %q in it", err, want)
		}
	}

	// The same two names, one of them quoted-distinct in spelling only, still differ as
	// identifiers once folded — but names that differ outside case are fine.
	ok := &PipelineConfig{
		Version: "1",
		Name:    "no-collision",
		Steps:   []StepConfig{{Name: "push_parsed"}, {Name: "Deploy_Dev", Prerequisites: []string{"push_parsed"}}},
	}
	ok.initialize()
	if err := ok.Validate(); err != nil {
		t.Errorf("a single mixed-case name is legal (Postgres folds it consistently): %v", err)
	}
}

// Postgres truncates an unquoted identifier at 63 bytes SILENTLY, so a step name long enough to
// push {name}_status past that produces a column whose real name is not the one the SELECT and
// SET lists are built from: every read of that step then fails 42703 against a schema the
// migrator reported as applied.
func TestPipelineConfig_Validate_RejectsStepNamesThatTruncate(t *testing.T) {
	longest := repeatRune('a', MaxStepNameLen)
	ok := &PipelineConfig{
		Version: "1",
		Name:    "at-the-limit",
		Steps:   []StepConfig{{Name: "push_parsed"}, {Name: longest, Prerequisites: []string{"push_parsed"}}},
	}
	ok.initialize()
	if err := ok.Validate(); err != nil {
		t.Errorf("a name whose column is exactly 63 bytes is legal: %v", err)
	}
	if len(longest+"_status") != 63 {
		t.Fatalf("MaxStepNameLen must be 63 minus len(\"_status\"); got a %d-byte column", len(longest+"_status"))
	}

	tooLong := repeatRune('a', MaxStepNameLen+1)
	over := &PipelineConfig{
		Version: "1",
		Name:    "over-the-limit",
		Steps:   []StepConfig{{Name: "push_parsed"}, {Name: tooLong, Prerequisites: []string{"push_parsed"}}},
	}
	over.initialize()
	err := over.Validate()
	if err == nil {
		t.Fatal("expected a step name that truncates at 63 bytes to be rejected")
	}
	if !strings.Contains(err.Error(), "63 bytes") {
		t.Errorf("the error must say what the limit is; got %q", err)
	}
}

// repeatRune builds an n-byte step name, so the length boundary is expressed as a length.
func repeatRune(r byte, n int) string {
	return strings.Repeat(string(r), n)
}

// A step name reaches identifier position UNQUOTED: stepColumnEnsurer
// (postgres_migrations.go) emits `ADD COLUMN IF NOT EXISTS {name}_status step_status ...`,
// and slipColumns/slipSelectColumns (postgres_store.go) rebuild the same identifier into
// every SELECT and every SET list. Postgres accepts an unquoted identifier only when it
// BEGINS with a letter or an underscore, and ClickHouse's non-quoted identifier rule is
// ^[a-zA-Z_][0-9a-zA-Z_]*$ — so `prod-deploy` emits `prod-deploy_status` and `1deploy`
// emits `1deploy_status`, both 42601 on the migration every consumer runs at startup, with
// nothing to recover them because the same identifier is rebuilt on every read and write
// (PR #87, pkuzmenko finding 1 arm A).
func TestValidateStepIdentifier_RejectsNamesThatAreNotBareIdentifiers(t *testing.T) {
	tests := []struct {
		name     string
		stepName string
		rejected bool
	}{
		{"a hyphen is a minus sign once spliced: prod-deploy_status is 42601", "prod-deploy", true},
		{"a leading digit lexes as a numeric literal, not an identifier", "1deploy", true},
		{"a space splits one identifier into two", "prod deploy", true},
		{"a single quote opens a string literal", "o'brien_check", true},
		{"a dot reads as a schema qualifier", "public.deploy", true},
		{"a dollar sign is legal inside a Postgres identifier but not a ClickHouse one", "deploy$dev", true},
		{"a quote character would let the name close and reopen the identifier", `deploy"`, true},
		{"empty is not an identifier either (Validate's own empty check reaches it first)", "", true},
		{"a leading underscore is a legal identifier start", "_deploy", false},
		{"letters, digits and underscores after the first byte", "deploy_dev_2", false},
		{"mixed case is legal: Postgres folds it consistently", "Deploy_Dev", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateStepIdentifier(tc.stepName, map[string]string{})
			if tc.rejected && err == nil {
				t.Fatalf("step name %q generates a broken identifier and must be rejected", tc.stepName)
			}
			if !tc.rejected && err != nil {
				t.Fatalf("step name %q is a bare identifier and must be admitted: %v", tc.stepName, err)
			}
		})
	}
}

// The same rejection through the real entry point, so a config carrying such a name never
// reaches the migrator at all: it is refused at parse time, once, before any DDL runs.
func TestParsePipelineConfig_RejectsStepNameThatIsNotABareIdentifier(t *testing.T) {
	const j = `{
		"version": "1",
		"name": "hyphenated-step",
		"steps": [
			{"name": "push_parsed"},
			{"name": "prod-deploy", "prerequisites": ["push_parsed"]}
		]
	}`
	_, err := ParsePipelineConfig([]byte(j))
	if err == nil {
		t.Fatal("a step name that is not a bare identifier must be rejected at parse time")
	}
	if !strings.Contains(err.Error(), "prod-deploy") {
		t.Errorf("the error must name the offending step; got %q", err)
	}
}

// An AGGREGATE step's jsonb column is its BARE name — slipColumns() appends step.Name and
// stepColumnEnsurer emits `ADD COLUMN IF NOT EXISTS <name> jsonb` — so a name that is
// already a fixed routing_slips column silently does nothing at migration time, ProbeSchema
// reports the column present because it IS present, and every later write then names that
// column twice in one SET list: 42701 on every update the slip ever takes. That is the same
// fault the case-folding arm prevents, reached by another route (PR #87, pkuzmenko
// finding 1 arm B).
func TestValidateStepIdentifier_RejectsNamesThatCollideWithAFixedColumn(t *testing.T) {
	tests := []struct {
		name     string
		stepName string
		rejected bool
	}{
		{"status is the slip's own status column", ColumnStatus, true},
		{"correlation_id is the primary key", ColumnCorrelationID, true},
		{"repository", ColumnRepository, true},
		{"branch", ColumnBranch, true},
		{"commit_sha", ColumnCommitSHA, true},
		{"created_at", ColumnCreatedAt, true},
		{"updated_at", ColumnUpdatedAt, true},
		{"step_details", ColumnStepDetails, true},
		{"state_history", ColumnStateHistory, true},
		{"claimed_from is SELECT-only but it is still a column of the table", ColumnClaimedFrom, true},
		{"the reservation folds, because Postgres folds the DDL that would collide", "Status", true},
		{"a name that merely contains a reserved one is fine", "status_check", false},
		{"a name suffixed past a reserved one is fine", "branch_protection", false},
		{"an ordinary step name", "dev_deploy", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateStepIdentifier(tc.stepName, map[string]string{})
			if tc.rejected && err == nil {
				t.Fatalf("step name %q collides with a fixed routing_slips column and must be rejected",
					tc.stepName)
			}
			if !tc.rejected && err != nil {
				t.Fatalf("step name %q collides with nothing and must be admitted: %v", tc.stepName, err)
			}
		})
	}
}

// The reservation covers EVERY step, not only the aggregate ones whose bare column actually
// collides. A non-aggregate step's {name}_status column cannot hit a fixed column — no fixed
// column ends in _status — so the rule is stricter than the fault requires, on purpose:
// adding "aggregates" to an existing step is a one-word config edit, and it is the only thing
// standing between a merely confusing name and a silent 42701.
func TestParsePipelineConfig_ReservesFixedColumnNamesForEveryStepKind(t *testing.T) {
	for _, shape := range []struct {
		name string
		step string
	}{
		{
			"aggregate step: the bare column is the collision",
			`{"name": "status", "prerequisites": ["push_parsed"], "aggregates": "component_status"}`,
		},
		{
			"plain step: reserved anyway, one config edit away from the collision",
			`{"name": "status", "prerequisites": ["push_parsed"]}`,
		},
	} {
		t.Run(shape.name, func(t *testing.T) {
			j := `{"version": "1", "name": "reserved", "steps": [{"name": "push_parsed"}, ` + shape.step + `]}`
			_, err := ParsePipelineConfig([]byte(j))
			if err == nil {
				t.Fatal("a step named after a fixed routing_slips column must be rejected at parse time")
			}
			if !strings.Contains(err.Error(), "status") {
				t.Errorf("the error must name the column; got %q", err)
			}
		})
	}
}

// The reserved set is DERIVED from the column list the store actually builds, not restated
// beside it: a column added to slipColumns()/slipSelectColumns() must become reserved in the
// same edit. This test fails if the two ever drift, which is the whole reason the reservation
// reads fixedSlipColumns() rather than a hand-typed list.
func TestValidateStepIdentifier_ReservationTracksSlipSelectColumns(t *testing.T) {
	store, _ := newMockStore(t)

	fromConfig := make(map[string]bool)
	for _, step := range store.config.Steps {
		fromConfig[step.Name+"_status"] = true
		if step.Aggregates != "" {
			fromConfig[step.Name] = true
		}
	}

	checked := 0
	for _, col := range store.slipSelectColumns() {
		if fromConfig[col] {
			continue // config-derived, not a fixed column
		}
		checked++
		if err := validateStepIdentifier(col, map[string]string{}); err == nil {
			t.Errorf("%q is a fixed routing_slips column that slipSelectColumns() emits, "+
				"but it is admitted as a step name", col)
		}
	}
	if checked != len(fixedSlipColumns())+1 {
		t.Fatalf("expected every fixed column plus claimed_from to be checked; checked %d", checked)
	}
}
