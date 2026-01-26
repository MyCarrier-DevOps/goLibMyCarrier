package slippy

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestColumnNameConsistency proves that the step name from config is used consistently
// throughout the entire codebase. By running the same tests against different config
// variations, we verify that changing the config changes behavior everywhere.
func TestColumnNameConsistency(t *testing.T) {
	// Define multiple config variations with different step names
	// Each variation uses a different name for the "build" aggregate step
	configVariations := []struct {
		name           string // test case name
		buildStepName  string // the step name for builds aggregate
		buildAggregate string // the aggregate type (always "build")
		testStepName   string // the step name for unit_tests aggregate
		testAggregate  string // the aggregate type (always "unit_test")
	}{
		{
			name:           "short_names",
			buildStepName:  "builds",
			buildAggregate: "build",
			testStepName:   "unit_tests",
			testAggregate:  "unit_test",
		},
		{
			name:           "verbose_names",
			buildStepName:  "builds_completed",
			buildAggregate: "build",
			testStepName:   "unit_tests_completed",
			testAggregate:  "unit_test",
		},
		{
			name:           "custom_names",
			buildStepName:  "container_images",
			buildAggregate: "build",
			testStepName:   "test_results",
			testAggregate:  "unit_test",
		},
		{
			name:           "minimal_names",
			buildStepName:  "b",
			buildAggregate: "build",
			testStepName:   "t",
			testAggregate:  "unit_test",
		},
	}

	for _, variation := range configVariations {
		t.Run(variation.name, func(t *testing.T) {
			// Create config dynamically based on variation
			config := createTestConfigWithNames(
				variation.buildStepName, variation.buildAggregate,
				variation.testStepName, variation.testAggregate,
			)

			// Run all consistency checks with this config
			t.Run("GetAggregateStep", func(t *testing.T) {
				testGetAggregateStepConsistency(t, config, variation.buildStepName, variation.buildAggregate)
				testGetAggregateStepConsistency(t, config, variation.testStepName, variation.testAggregate)
			})

			t.Run("GetAggregateColumnName", func(t *testing.T) {
				testGetAggregateColumnNameConsistency(t, config, variation.buildStepName)
				testGetAggregateColumnNameConsistency(t, config, variation.testStepName)
			})

			t.Run("PushInitialization", func(t *testing.T) {
				testPushInitializationConsistency(t, config, variation.buildStepName, variation.testStepName)
			})

			t.Run("PrerequisiteResolution", func(t *testing.T) {
				testPrerequisiteResolutionConsistency(t, config,
					variation.buildStepName, variation.buildAggregate,
					variation.testStepName, variation.testAggregate)
			})

			t.Run("AggregateUpdate", func(t *testing.T) {
				testAggregateUpdateConsistency(t, config,
					variation.buildStepName, variation.buildAggregate)
			})

			t.Run("SetImageTag", func(t *testing.T) {
				testSetImageTagConsistency(t, config, variation.buildStepName)
			})
		})
	}
}

// createTestConfigWithNames creates a pipeline config with the specified step names.
func createTestConfigWithNames(buildStepName, buildAggregate, testStepName, testAggregate string) *PipelineConfig {
	configJSON := `{
		"version": "1.0",
		"name": "test-pipeline",
		"steps": [
			{"name": "init", "description": "Initial step"},
			{"name": "` + buildStepName + `", "description": "Builds", "aggregates": "` + buildAggregate + `", "prerequisites": ["init"]},
			{"name": "` + testStepName + `", "description": "Tests", "aggregates": "` + testAggregate + `", "prerequisites": ["` + buildStepName + `"]},
			{"name": "deploy", "description": "Deploy", "prerequisites": ["` + testStepName + `"]}
		]
	}`

	var config PipelineConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		panic("failed to create test config: " + err.Error())
	}

	// Initialize internal lookup maps
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

	return &config
}

// testGetAggregateStepConsistency verifies GetAggregateStep returns the step name from config.
func testGetAggregateStepConsistency(t *testing.T, config *PipelineConfig, expectedStepName, aggregateType string) {
	result := config.GetAggregateStep(aggregateType)
	if result != expectedStepName {
		t.Errorf("GetAggregateStep(%q) = %q, want %q", aggregateType, result, expectedStepName)
	}
}

// testGetAggregateColumnNameConsistency verifies GetAggregateColumnName returns the step name.
func testGetAggregateColumnNameConsistency(t *testing.T, config *PipelineConfig, stepName string) {
	result := config.GetAggregateColumnName(stepName)
	if result != stepName {
		t.Errorf("GetAggregateColumnName(%q) = %q, want %q (step name itself)", stepName, result, stepName)
	}
}

// testPushInitializationConsistency verifies that slip initialization uses config step names for aggregates.
func testPushInitializationConsistency(t *testing.T, config *PipelineConfig, buildStepName, testStepName string) {
	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

	opts := PushOptions{
		CorrelationID: "test-consistency-push",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "abc123",
		Components: []ComponentDefinition{
			{Name: "service-a"},
			{Name: "service-b"},
		},
	}

	slip := client.initializeSlipForPush(opts, nil)

	// Verify build aggregate column uses the step name from config
	if _, ok := slip.Aggregates[buildStepName]; !ok {
		t.Errorf("slip.Aggregates[%q] not found - expected aggregate column to match step name", buildStepName)
		t.Logf("Available aggregate keys: %v", getMapKeys(slip.Aggregates))
	} else {
		if len(slip.Aggregates[buildStepName]) != 2 {
			t.Errorf(
				"slip.Aggregates[%q] has %d components, want 2",
				buildStepName,
				len(slip.Aggregates[buildStepName]),
			)
		}
	}

	// Verify all steps from config exist
	for _, step := range config.Steps {
		if _, ok := slip.Steps[step.Name]; !ok {
			t.Errorf("slip.Steps[%q] not found - expected step from config", step.Name)
		}
	}
}

// testPrerequisiteResolutionConsistency verifies prerequisite checking uses config step names.
func testPrerequisiteResolutionConsistency(t *testing.T, config *PipelineConfig,
	buildStepName, buildAggregate, testStepName, testAggregate string) {
	ctx := context.Background()
	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

	// Create a slip with component data using the config step name
	slip := &Slip{
		CorrelationID: "test-prereq-consistency",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "prereq123",
		Status:        SlipStatusInProgress,
		Aggregates: map[string][]ComponentStepData{
			buildStepName: {
				{Component: "service-a", Status: StepStatusCompleted},
			},
			testStepName: {
				{Component: "service-a", Status: StepStatusCompleted},
			},
		},
		Steps: make(map[string]Step),
	}
	store.AddSlip(slip)

	// Check build prerequisite for component - should find it in the aggregate column
	result, err := client.CheckPrerequisites(ctx, slip, []string{buildAggregate}, "service-a")
	if err != nil {
		t.Fatalf("CheckPrerequisites failed: %v", err)
	}
	if result.Status != PrereqStatusCompleted {
		t.Errorf(
			"CheckPrerequisites for %q component returned status %q, want completed",
			buildAggregate,
			result.Status,
		)
	}

	// Check test prerequisite for component
	result, err = client.CheckPrerequisites(ctx, slip, []string{testAggregate}, "service-a")
	if err != nil {
		t.Fatalf("CheckPrerequisites failed: %v", err)
	}
	if result.Status != PrereqStatusCompleted {
		t.Errorf("CheckPrerequisites for %q component returned status %q, want completed", testAggregate, result.Status)
	}
}

// testAggregateUpdateConsistency verifies aggregate status updates use config step names.
func testAggregateUpdateConsistency(t *testing.T, config *PipelineConfig, buildStepName, buildAggregate string) {
	ctx := context.Background()
	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

	// Create a slip with all components completed
	slip := &Slip{
		CorrelationID: "test-agg-update-consistency",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "agg123",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        SlipStatusInProgress,
		Aggregates: map[string][]ComponentStepData{
			buildStepName: {
				{Component: "service-a", Status: StepStatusCompleted},
				{Component: "service-b", Status: StepStatusCompleted},
			},
		},
		Steps: make(map[string]Step),
	}
	store.AddSlip(slip)

	// Update a component step - should trigger aggregate update
	err := client.UpdateStepWithStatus(
		ctx,
		slip.CorrelationID,
		buildAggregate,
		"service-a",
		StepStatusCompleted,
		"done",
	)
	if err != nil {
		t.Fatalf("UpdateStepWithStatus failed: %v", err)
	}

	// Verify the aggregate step (with config name) was updated
	var foundAggregateUpdate bool
	for _, call := range store.UpdateStepCalls {
		if call.StepName == buildStepName {
			foundAggregateUpdate = true
			if call.Status != StepStatusCompleted {
				t.Errorf("Aggregate step %q updated with status %q, want completed", buildStepName, call.Status)
			}
			break
		}
	}
	if !foundAggregateUpdate {
		t.Errorf("Expected aggregate step %q to be updated, but it wasn't", buildStepName)
		t.Logf("UpdateStepCalls: %+v", store.UpdateStepCalls)
	}
}

// testSetImageTagConsistency verifies SetComponentImageTag uses config step names.
func testSetImageTagConsistency(t *testing.T, config *PipelineConfig, buildStepName string) {
	ctx := context.Background()
	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

	// Create a slip with component data using the config step name
	slip := &Slip{
		CorrelationID: "test-image-tag-consistency",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "img123",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        SlipStatusInProgress,
		Aggregates: map[string][]ComponentStepData{
			buildStepName: {
				{Component: "service-a"},
			},
		},
		Steps: make(map[string]Step),
	}
	store.AddSlip(slip)

	// Set image tag
	err := client.SetComponentImageTag(ctx, slip.CorrelationID, "service-a", "myregistry/service-a:v1.0.0")
	if err != nil {
		t.Fatalf("SetComponentImageTag failed: %v", err)
	}

	// Verify the update was made to the correct aggregate column
	if len(store.UpdateCalls) != 1 {
		t.Fatalf("Expected 1 Update call, got %d", len(store.UpdateCalls))
	}

	updatedSlip := store.UpdateCalls[0].Slip
	components, ok := updatedSlip.Aggregates[buildStepName]
	if !ok {
		t.Fatalf("Updated slip missing aggregate column %q", buildStepName)
	}
	if len(components) != 1 || components[0].ImageTag != "myregistry/service-a:v1.0.0" {
		t.Errorf("Image tag not set correctly in aggregate %q", buildStepName)
	}
}

// Helper function to get map keys for debugging
func getMapKeys[K comparable, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestAggregateStepsFromConfig verifies GetAggregateSteps returns steps with names from config.
func TestAggregateStepsFromConfig(t *testing.T) {
	configs := []struct {
		name             string
		buildName        string
		testName         string
		expectedAggSteps []string
	}{
		{"standard", "builds", "unit_tests", []string{"builds", "unit_tests"}},
		{"verbose", "builds_completed", "tests_completed", []string{"builds_completed", "tests_completed"}},
		{"custom", "images", "checks", []string{"images", "checks"}},
	}

	for _, tc := range configs {
		t.Run(tc.name, func(t *testing.T) {
			config := createTestConfigWithNames(tc.buildName, "build", tc.testName, "unit_test")
			aggSteps := config.GetAggregateSteps()

			if len(aggSteps) != len(tc.expectedAggSteps) {
				t.Fatalf("GetAggregateSteps returned %d steps, want %d", len(aggSteps), len(tc.expectedAggSteps))
			}

			for i, step := range aggSteps {
				if step.Name != tc.expectedAggSteps[i] {
					t.Errorf("GetAggregateSteps()[%d].Name = %q, want %q", i, step.Name, tc.expectedAggSteps[i])
				}
			}
		})
	}
}
