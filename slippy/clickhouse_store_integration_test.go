// go:build integration
//go:build integration

package slippy

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/chcol"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	ch "github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse"
)

func init() {
	// Disable ryuk (reaper) for Podman compatibility
	// Ryuk has issues connecting to Docker socket inside Podman containers
	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
}

// integrationTestPipelineConfig returns a pipeline config for integration tests.
// It defines a simple pipeline with builds as an aggregate step.
func integrationTestPipelineConfig() *PipelineConfig {
	config := &PipelineConfig{
		Version:     "1.0",
		Name:        "integration-test-pipeline",
		Description: "Pipeline for integration testing",
		Steps: []StepConfig{
			{Name: "push_parsed", Description: "Push event parsed"},
			{Name: "builds", Description: "Component builds", Aggregates: "builds_completed"},
			{Name: "builds_completed", Description: "All builds completed", Prerequisites: []string{"push_parsed"}},
			{Name: "unit_tests", Description: "Component unit tests", Aggregates: "unit_tests_completed"},
			{
				Name:          "unit_tests_completed",
				Description:   "All unit tests completed",
				Prerequisites: []string{"builds_completed"},
			},
			{Name: "dev_deploy", Description: "Deploy to dev", Prerequisites: []string{"unit_tests_completed"}},
		},
	}
	config.initialize()
	return config
}

// clickhouseContainer wraps a testcontainers ClickHouse instance
type clickhouseContainer struct {
	container testcontainers.Container
	host      string
	port      string
}

// setupClickHouseContainer starts a ClickHouse container for testing
func setupClickHouseContainer(ctx context.Context, t *testing.T) (*clickhouseContainer, error) {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "clickhouse/clickhouse-server:25.8",
		ExposedPorts: []string{"9000/tcp", "8123/tcp"},
		// Wait for both native protocol port and HTTP ping to ensure full readiness
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("9000/tcp"),
			wait.ForHTTP("/ping").WithPort("8123/tcp").WithStatusCodeMatcher(func(status int) bool {
				return status == 200
			}),
		).WithDeadline(120 * time.Second),
		Env: map[string]string{
			"CLICKHOUSE_USER":                      "default",
			"CLICKHOUSE_PASSWORD":                  "",
			"CLICKHOUSE_DB":                        "default",
			"CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1",
		},
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start clickhouse container: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	mappedPort, err := container.MappedPort(ctx, "9000")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("failed to get mapped port: %w", err)
	}

	return &clickhouseContainer{
		container: container,
		host:      host,
		port:      mappedPort.Port(),
	}, nil
}

// terminate stops the container
func (c *clickhouseContainer) terminate(ctx context.Context) error {
	return c.container.Terminate(ctx)
}

// createTestStore creates a ClickHouseStore connected to the test container
// For integration tests, we create a raw connection without TLS since the testcontainer
// doesn't have TLS enabled.
func createTestStore(
	ctx context.Context,
	t *testing.T,
	container *clickhouseContainer,
	pipelineConfig *PipelineConfig,
) (*ClickHouseStore, error) {
	t.Helper()

	// Small delay to ensure ClickHouse is fully initialized
	time.Sleep(2 * time.Second)

	// Connect directly to ClickHouse without TLS for test containers
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{fmt.Sprintf("%s:%s", container.host, container.port)},
		Auth: clickhouse.Auth{
			Database: "default",
			Username: "default",
			Password: "",
		},
		DialTimeout:     10 * time.Second,
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Hour,
		// No TLS for local test container
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open clickhouse connection: %w", err)
	}

	// Retry ping a few times as ClickHouse may need a moment
	var pingErr error
	for i := 0; i < 5; i++ {
		if pingErr = conn.Ping(ctx); pingErr == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if pingErr != nil {
		return nil, fmt.Errorf("failed to ping clickhouse after retries: %w", pingErr)
	}

	// Enable experimental JSON type for ClickHouse 24.8+
	if err := conn.Exec(ctx, "SET allow_experimental_json_type = 1"); err != nil {
		return nil, fmt.Errorf("failed to enable experimental JSON type: %w", err)
	}

	// Wrap the connection in our session wrapper
	session := &testClickhouseSession{conn: conn}

	// Disable optimize after write for now - we'll test it explicitly
	optimizeAfterWrite := false

	store := NewClickHouseStoreFromSession(session, pipelineConfig, "ci_test")
	store.SetOptimizeAfterWrite(optimizeAfterWrite)

	// Run migrations (JSON type setting already applied via conn.Exec above)
	migrateOpts := MigrateOptions{
		Database:       "ci_test",
		PipelineConfig: pipelineConfig,
	}

	if _, err := RunMigrations(ctx, conn, migrateOpts); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return store, nil
}

// testClickhouseSession wraps a ClickHouse connection for testing
type testClickhouseSession struct {
	conn clickhouse.Conn
}

func (s *testClickhouseSession) Connect(ch *ch.ClickhouseConfig, ctx context.Context) error {
	return nil // Already connected
}

func (s *testClickhouseSession) Query(ctx context.Context, query string) (ch.Rows, error) {
	return s.conn.Query(ctx, query)
}

func (s *testClickhouseSession) QueryWithArgs(ctx context.Context, query string, args ...interface{}) (ch.Rows, error) {
	return s.conn.Query(ctx, query, args...)
}

func (s *testClickhouseSession) QueryRow(ctx context.Context, query string, args ...interface{}) ch.Row {
	return s.conn.QueryRow(ctx, query, args...)
}

func (s *testClickhouseSession) Exec(ctx context.Context, stmt string) error {
	return s.conn.Exec(ctx, stmt)
}

func (s *testClickhouseSession) ExecWithArgs(ctx context.Context, stmt string, args ...interface{}) error {
	return s.conn.Exec(ctx, stmt, args...)
}

func (s *testClickhouseSession) Close() error {
	return s.conn.Close()
}

func (s *testClickhouseSession) Conn() ch.Conn {
	return s.conn
}

// createIntegrationTestSlip creates a slip with the given components for integration testing
func createIntegrationTestSlip(correlationID string, components []string, pipelineConfig *PipelineConfig) *Slip {
	slip := &Slip{
		CorrelationID: correlationID,
		Repository:    "myorg/myrepo",
		Branch:        "main",
		CommitSHA:     "abc123def456",
		Status:        SlipStatusPending,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Steps:         make(map[string]Step),
		Aggregates:    make(map[string][]ComponentStepData),
		StateHistory:  []StateHistoryEntry{},
		Sign:          1,
		Version:       1,
	}

	// Initialize steps from pipeline config
	for _, step := range pipelineConfig.Steps {
		slip.Steps[step.Name] = Step{Status: StepStatusPending}
	}

	// Initialize components for builds and unit_tests aggregates
	buildsData := make([]ComponentStepData, 0, len(components))
	unitTestsData := make([]ComponentStepData, 0, len(components))
	for _, comp := range components {
		buildsData = append(buildsData, ComponentStepData{
			Component: comp,
			Status:    StepStatusPending,
		})
		unitTestsData = append(unitTestsData, ComponentStepData{
			Component: comp,
			Status:    StepStatusPending,
		})
	}
	slip.Aggregates["builds"] = buildsData
	slip.Aggregates["unit_tests"] = unitTestsData

	return slip
}

// TestVersionedCollapsingMergeTree_ConcurrentBuildUpdates tests that concurrent
// build completion updates are properly merged without data loss.
//
// This test simulates a real-world scenario where 3 builds complete in random
// order and each update should be preserved in the final record.
func TestVersionedCollapsingMergeTree_ConcurrentBuildUpdates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Start ClickHouse container
	container, err := setupClickHouseContainer(ctx, t)
	if err != nil {
		t.Fatalf("failed to setup clickhouse container: %v", err)
	}
	defer func() {
		if err := container.terminate(ctx); err != nil {
			t.Logf("warning: failed to terminate container: %v", err)
		}
	}()

	pipelineConfig := integrationTestPipelineConfig()
	store, err := createTestStore(ctx, t, container, pipelineConfig)
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	defer store.Close()

	// Create a slip with 3 components
	correlationID := fmt.Sprintf("test-slip-%d", time.Now().UnixNano())
	components := []string{"api", "worker", "frontend"}

	slip := createIntegrationTestSlip(correlationID, components, pipelineConfig)

	// Create the initial slip
	if err := store.Create(ctx, slip); err != nil {
		t.Fatalf("failed to create slip: %v", err)
	}

	// Mark push_parsed as completed using UpdateStep with empty component name
	if err := store.UpdateStep(ctx, correlationID, "push_parsed", "", StepStatusCompleted); err != nil {
		t.Fatalf("failed to update push_parsed step: %v", err)
	}

	t.Logf("Created slip %s with components: %v", correlationID, components)

	// Simulate 3 builds completing in random order
	// Shuffle the components to simulate random completion order
	rand.Shuffle(len(components), func(i, j int) {
		components[i], components[j] = components[j], components[i]
	})

	t.Logf("Build completion order: %v", components)

	// Complete builds one at a time with some delay to simulate real-world timing
	for i, component := range components {
		t.Logf("Completing build for component %d: %s", i+1, component)

		// UpdateComponentStatus signature: (ctx, correlationID, componentName, stepType, status)
		if err := store.UpdateComponentStatus(
			ctx,
			correlationID,
			component,
			"builds",
			StepStatusCompleted,
		); err != nil {
			t.Fatalf("failed to update build for %s: %v", component, err)
		}

		// Query raw data directly from ClickHouse
		session := store.Session()
		rows, err := session.QueryWithArgs(ctx, `
			SELECT version, sign, builds 
			FROM ci_test.routing_slips FINAL 
			WHERE correlation_id = $1
		`, correlationID)
		if err != nil {
			t.Logf("  → Error querying raw data: %v", err)
		} else {
			defer rows.Close()
			for rows.Next() {
				var version uint32
				var sign int8
				builds := chcol.NewJSON()
				if err := rows.Scan(&version, &sign, builds); err != nil {
					t.Logf("  → Error scanning: %v", err)
				} else {
					// Marshal to get the literal JSON representation
					jsonBytes, _ := builds.MarshalJSON()
					t.Logf("  → Raw ClickHouse row after update %d:", i+1)
					t.Logf("      version=%d sign=%d", version, sign)
					t.Logf("      builds=%s", string(jsonBytes))
				}
			}
		}

		// Add a small delay between updates to simulate real-world timing
		time.Sleep(50 * time.Millisecond)
	}

	// After all builds complete, the builds_completed step should auto-update
	// Let's manually set it for now (the aggregate logic is in the client layer)
	if err := store.UpdateStep(ctx, correlationID, "builds_completed", "", StepStatusCompleted); err != nil {
		t.Fatalf("failed to update builds_completed step: %v", err)
	}

	// Run OPTIMIZE TABLE to force collapsing of rows
	if err := store.OptimizeTable(ctx); err != nil {
		t.Fatalf("failed to optimize table: %v", err)
	}

	// Load the final slip and verify no data was lost
	finalSlip, err := store.Load(ctx, correlationID)
	if err != nil {
		t.Fatalf("failed to load final slip: %v", err)
	}

	// Query final raw data directly from ClickHouse
	session := store.Session()
	rows, err := session.QueryWithArgs(ctx, `
		SELECT version, sign, builds 
		FROM ci_test.routing_slips FINAL 
		WHERE correlation_id = $1
	`, correlationID)
	if err != nil {
		t.Logf("Error querying final raw data: %v", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var finalVersion uint32
			var finalSign int8
			builds := chcol.NewJSON()
			if err := rows.Scan(&finalVersion, &finalSign, builds); err != nil {
				t.Logf("Error scanning final row: %v", err)
			} else {
				// Marshal to get the literal JSON representation
				jsonBytes, _ := builds.MarshalJSON()
				t.Log("")
				t.Log("═══════════════════════════════════════════════════════════════")
				t.Log("FINAL RAW CLICKHOUSE ROW")
				t.Log("═══════════════════════════════════════════════════════════════")
				t.Logf("version=%d sign=%d", finalVersion, finalSign)
				t.Logf("builds=%s", string(jsonBytes))
				t.Log("═══════════════════════════════════════════════════════════════")
				t.Log("")
			}
		}
	}

	buildsData := finalSlip.Aggregates["builds"]

	if len(buildsData) != len(components) {
		t.Errorf("expected %d builds, got %d", len(components), len(buildsData))
	}

	completedBuilds := make(map[string]bool)
	for _, build := range buildsData {
		if build.Status != StepStatusCompleted {
			t.Errorf("build %s has status %s, expected completed", build.Component, build.Status)
		}
		completedBuilds[build.Component] = true
	}

	// Verify all expected components are present
	for _, comp := range []string{"api", "worker", "frontend"} {
		if !completedBuilds[comp] {
			t.Errorf("component %s is missing from builds", comp)
		}
	}

	// Verify the builds_completed step is completed
	if finalSlip.Steps["builds_completed"].Status != StepStatusCompleted {
		t.Errorf("builds_completed step has status %s, expected completed", finalSlip.Steps["builds_completed"].Status)
	}

	// Verify version was incremented (initial 1 + 1 push_parsed + 3 builds + 1 builds_completed = 6)
	// Note: Each update increments version
	if finalSlip.Version < 5 {
		t.Errorf("expected version >= 5 after 5 updates, got %d", finalSlip.Version)
	}

	// Verify sign is positive (active row)
	if finalSlip.Sign != 1 {
		t.Errorf("expected sign = 1 for active row, got %d", finalSlip.Sign)
	}

	t.Logf("SUCCESS: All %d builds completed without data loss", len(components))
}

// TestVersionedCollapsingMergeTree_ConcurrentUpdates tests that truly concurrent
// updates from multiple goroutines don't cause data loss.
func TestVersionedCollapsingMergeTree_ConcurrentUpdates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Start ClickHouse container
	container, err := setupClickHouseContainer(ctx, t)
	if err != nil {
		t.Fatalf("failed to setup clickhouse container: %v", err)
	}
	defer func() {
		if err := container.terminate(ctx); err != nil {
			t.Logf("warning: failed to terminate container: %v", err)
		}
	}()

	pipelineConfig := integrationTestPipelineConfig()
	store, err := createTestStore(ctx, t, container, pipelineConfig)
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	defer store.Close()

	// Create a slip with 3 components
	correlationID := fmt.Sprintf("test-concurrent-%d", time.Now().UnixNano())
	components := []string{"api", "worker", "frontend"}

	slip := createIntegrationTestSlip(correlationID, components, pipelineConfig)

	// Create the initial slip
	if err := store.Create(ctx, slip); err != nil {
		t.Fatalf("failed to create slip: %v", err)
	}

	// Mark push_parsed as completed
	if err := store.UpdateStep(ctx, correlationID, "push_parsed", "", StepStatusCompleted); err != nil {
		t.Fatalf("failed to update push_parsed step: %v", err)
	}

	t.Logf("Created slip %s, starting concurrent updates...", correlationID)

	// Launch 3 goroutines to update builds concurrently
	var wg sync.WaitGroup
	errChan := make(chan error, len(components))

	for _, component := range components {
		wg.Add(1)
		go func(comp string) {
			defer wg.Done()

			// Add random delay (0-100ms) to increase contention
			time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)

			t.Logf("Goroutine starting update for component: %s", comp)
			// UpdateComponentStatus signature: (ctx, correlationID, componentName, stepType, status)
			if err := store.UpdateComponentStatus(ctx, correlationID, comp, "builds", StepStatusCompleted); err != nil {
				errChan <- fmt.Errorf("failed to update build for %s: %w", comp, err)
				return
			}
			t.Logf("Goroutine completed update for component: %s", comp)
		}(component)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Fatalf("concurrent update error: %v", err)
	}

	// Run OPTIMIZE TABLE to force collapsing
	if err := store.OptimizeTable(ctx); err != nil {
		t.Fatalf("failed to optimize table: %v", err)
	}

	// Load and verify
	finalSlip, err := store.Load(ctx, correlationID)
	if err != nil {
		t.Fatalf("failed to load final slip: %v", err)
	}

	buildsData := finalSlip.Aggregates["builds"]
	t.Logf("Final slip builds after concurrent updates: %+v", buildsData)
	t.Logf("Final slip version: %d, sign: %d", finalSlip.Version, finalSlip.Sign)

	// Verify all builds are completed
	completedBuilds := make(map[string]bool)
	for _, build := range buildsData {
		completedBuilds[build.Component] = (build.Status == StepStatusCompleted)
		t.Logf("Component %s: status=%s", build.Component, build.Status)
	}

	// Check all components completed
	allCompleted := true
	for _, comp := range components {
		if !completedBuilds[comp] {
			t.Errorf("component %s build not completed", comp)
			allCompleted = false
		}
	}

	if allCompleted {
		t.Logf("SUCCESS: All %d concurrent builds completed without data loss", len(components))
	}
}

// TestVersionedCollapsingMergeTree_QueryWithFinal tests that queries use FINAL
// to get the correct collapsed view even before OPTIMIZE runs.
func TestVersionedCollapsingMergeTree_QueryWithFinal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Start ClickHouse container
	container, err := setupClickHouseContainer(ctx, t)
	if err != nil {
		t.Fatalf("failed to setup clickhouse container: %v", err)
	}
	defer func() {
		if err := container.terminate(ctx); err != nil {
			t.Logf("warning: failed to terminate container: %v", err)
		}
	}()

	pipelineConfig := integrationTestPipelineConfig()
	store, err := createTestStore(ctx, t, container, pipelineConfig)
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	defer store.Close()

	// Create a slip
	correlationID := fmt.Sprintf("test-final-%d", time.Now().UnixNano())
	components := []string{"api"}

	slip := createIntegrationTestSlip(correlationID, components, pipelineConfig)
	slip.CommitSHA = "final123"

	if err := store.Create(ctx, slip); err != nil {
		t.Fatalf("failed to create slip: %v", err)
	}

	// Update the slip multiple times WITHOUT running OPTIMIZE
	if err := store.UpdateStep(ctx, correlationID, "push_parsed", "", StepStatusCompleted); err != nil {
		t.Fatalf("failed to update step push_parsed: %v", err)
	}

	// Update component status
	if err := store.UpdateComponentStatus(ctx, correlationID, "api", "builds", StepStatusCompleted); err != nil {
		t.Fatalf("failed to update build for api: %v", err)
	}

	// DO NOT run OPTIMIZE - we want to test that queries still return correct data

	// Load should still return the latest state thanks to FINAL keyword
	loadedSlip, err := store.Load(ctx, correlationID)
	if err != nil {
		t.Fatalf("failed to load slip: %v", err)
	}

	// Verify we got the latest data
	if loadedSlip.Steps["push_parsed"].Status != StepStatusCompleted {
		t.Errorf("expected push_parsed to be completed, got %s", loadedSlip.Steps["push_parsed"].Status)
	}

	// Find the api build
	buildsData := loadedSlip.Aggregates["builds"]
	var apiBuild *ComponentStepData
	for i := range buildsData {
		if buildsData[i].Component == "api" {
			apiBuild = &buildsData[i]
			break
		}
	}

	if apiBuild == nil {
		t.Fatal("api build not found")
	}

	if apiBuild.Status != StepStatusCompleted {
		t.Errorf("expected api build to be completed, got %s", apiBuild.Status)
	}

	t.Logf("SUCCESS: Query returned correct collapsed data without OPTIMIZE")
	t.Logf("Slip version: %d, sign: %d", loadedSlip.Version, loadedSlip.Sign)
}

// TestVersionedCollapsingMergeTree_DataIntegrity verifies that after multiple
// updates, all data fields are preserved correctly.
func TestVersionedCollapsingMergeTree_DataIntegrity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Start ClickHouse container
	container, err := setupClickHouseContainer(ctx, t)
	if err != nil {
		t.Fatalf("failed to setup clickhouse container: %v", err)
	}
	defer func() {
		if err := container.terminate(ctx); err != nil {
			t.Logf("warning: failed to terminate container: %v", err)
		}
	}()

	pipelineConfig := integrationTestPipelineConfig()
	store, err := createTestStore(ctx, t, container, pipelineConfig)
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	defer store.Close()

	// Create a slip with specific data
	correlationID := fmt.Sprintf("test-integrity-%d", time.Now().UnixNano())
	expectedRepo := "myorg/integrity-test"
	expectedBranch := "feature/data-test"
	expectedCommit := "integrity123456789"

	components := []string{"api", "worker", "frontend"}

	slip := createIntegrationTestSlip(correlationID, components, pipelineConfig)
	slip.Repository = expectedRepo
	slip.Branch = expectedBranch
	slip.CommitSHA = expectedCommit
	slip.Ancestry = []AncestryEntry{{CommitSHA: "parent123", CorrelationID: "parent-corr"}}

	// Add image tags to builds
	for i := range slip.Aggregates["builds"] {
		slip.Aggregates["builds"][i].ImageTag = fmt.Sprintf("%s:v1.0.0", slip.Aggregates["builds"][i].Component)
	}

	if err := store.Create(ctx, slip); err != nil {
		t.Fatalf("failed to create slip: %v", err)
	}

	// Perform multiple updates
	if err := store.UpdateStep(ctx, correlationID, "push_parsed", "", StepStatusCompleted); err != nil {
		t.Fatalf("failed to update push_parsed: %v", err)
	}

	for _, comp := range components {
		if err := store.UpdateComponentStatus(ctx, correlationID, comp, "builds", StepStatusCompleted); err != nil {
			t.Fatalf("failed to update build for %s: %v", comp, err)
		}
	}

	if err := store.UpdateStep(ctx, correlationID, "builds_completed", "", StepStatusCompleted); err != nil {
		t.Fatalf("failed to update builds_completed: %v", err)
	}

	// Optimize and load
	if err := store.OptimizeTable(ctx); err != nil {
		t.Fatalf("failed to optimize table: %v", err)
	}

	finalSlip, err := store.Load(ctx, correlationID)
	if err != nil {
		t.Fatalf("failed to load final slip: %v", err)
	}

	// Verify all data fields are preserved
	verifyField := func(name, expected, actual string) {
		if expected != actual {
			t.Errorf("%s: expected %q, got %q", name, expected, actual)
		}
	}

	verifyField("CorrelationID", correlationID, finalSlip.CorrelationID)
	verifyField("Repository", expectedRepo, finalSlip.Repository)
	verifyField("Branch", expectedBranch, finalSlip.Branch)
	verifyField("CommitSHA", expectedCommit, finalSlip.CommitSHA)

	// Verify ancestry is preserved
	if len(finalSlip.Ancestry) != 1 {
		t.Errorf("expected 1 ancestry entry, got %d", len(finalSlip.Ancestry))
	} else if finalSlip.Ancestry[0].CommitSHA != "parent123" {
		t.Errorf("ancestry commit SHA mismatch: expected parent123, got %s", finalSlip.Ancestry[0].CommitSHA)
	}

	// Verify all builds have their data
	buildsData := finalSlip.Aggregates["builds"]
	buildsByComponent := make(map[string]ComponentStepData)
	for _, build := range buildsData {
		buildsByComponent[build.Component] = build
	}

	for _, comp := range components {
		build, ok := buildsByComponent[comp]
		if !ok {
			t.Errorf("build for component %s not found", comp)
			continue
		}
		if build.Status != StepStatusCompleted {
			t.Errorf("build %s status: expected completed, got %s", comp, build.Status)
		}
		expectedTag := fmt.Sprintf("%s:v1.0.0", comp)
		if build.ImageTag != expectedTag {
			t.Errorf("build %s image tag: expected %s, got %s", comp, expectedTag, build.ImageTag)
		}
	}

	// Verify steps
	if finalSlip.Steps["push_parsed"].Status != StepStatusCompleted {
		t.Errorf("push_parsed: expected completed, got %s", finalSlip.Steps["push_parsed"].Status)
	}
	if finalSlip.Steps["builds_completed"].Status != StepStatusCompleted {
		t.Errorf("builds_completed: expected completed, got %s", finalSlip.Steps["builds_completed"].Status)
	}

	t.Logf("SUCCESS: All data fields preserved after %d updates", 5+len(components))
	t.Logf("Final version: %d", finalSlip.Version)
}
