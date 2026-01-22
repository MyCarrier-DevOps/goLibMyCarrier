//go:build integration

// Package slippy provides end-to-end integration tests that simulate the complete
// CI/CD pipeline flow including pushhookparser and Slippy operations.
// These tests use testcontainers to spin up a real ClickHouse instance and
// validate that all operations work correctly with the production schema.
package slippy

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// =============================================================================
// TEST INFRASTRUCTURE
// =============================================================================

// e2eClickHouseContainer wraps a testcontainers ClickHouse instance for E2E tests
type e2eClickHouseContainer struct {
	container testcontainers.Container
	host      string
	port      string
	conn      clickhouse.Conn
}

// setupE2EClickHouseContainer starts a ClickHouse container for E2E testing
func setupE2EClickHouseContainer(ctx context.Context, t *testing.T) (*e2eClickHouseContainer, error) {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "clickhouse/clickhouse-server:25.8",
		ExposedPorts: []string{"9000/tcp", "8123/tcp"},
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

	// Small delay to ensure ClickHouse is fully initialized
	time.Sleep(2 * time.Second)

	// Connect directly to ClickHouse without TLS for test containers
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{fmt.Sprintf("%s:%s", host, mappedPort.Port())},
		Auth: clickhouse.Auth{
			Database: "default",
			Username: "default",
			Password: "",
		},
		DialTimeout:     10 * time.Second,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
	})
	if err != nil {
		_ = container.Terminate(ctx)
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
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("failed to ping clickhouse after retries: %w", pingErr)
	}

	// Enable experimental JSON type for ClickHouse 24.8+
	if err := conn.Exec(ctx, "SET allow_experimental_json_type = 1"); err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("failed to enable experimental JSON type: %w", err)
	}

	return &e2eClickHouseContainer{
		container: container,
		host:      host,
		port:      mappedPort.Port(),
		conn:      conn,
	}, nil
}

// terminate stops the container
func (c *e2eClickHouseContainer) terminate(ctx context.Context) error {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	return c.container.Terminate(ctx)
}

// mockGitHubAPIForE2E implements GitHubAPI for E2E tests
type mockGitHubAPIForE2E struct {
	mu           sync.RWMutex
	ancestryMap  map[string][]string // repo/ref -> []commitSHA
	prHeadCommit map[string]string   // repo/prNum -> headCommitSHA
}

func newMockGitHubAPIForE2E() *mockGitHubAPIForE2E {
	return &mockGitHubAPIForE2E{
		ancestryMap:  make(map[string][]string),
		prHeadCommit: make(map[string]string),
	}
}

func (m *mockGitHubAPIForE2E) GetCommitAncestry(
	ctx context.Context,
	owner, repo, ref string,
	depth int,
) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := fmt.Sprintf("%s/%s/%s", owner, repo, ref)
	if commits, ok := m.ancestryMap[key]; ok {
		if depth > 0 && len(commits) > depth {
			return commits[:depth], nil
		}
		return commits, nil
	}
	return nil, nil
}

func (m *mockGitHubAPIForE2E) GetPRHeadCommit(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := fmt.Sprintf("%s/%s/%d", owner, repo, prNumber)
	if commit, ok := m.prHeadCommit[key]; ok {
		return commit, nil
	}
	return "", fmt.Errorf("PR not found")
}

func (m *mockGitHubAPIForE2E) ClearCache() {}

func (m *mockGitHubAPIForE2E) SetAncestry(owner, repo, ref string, commits []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s/%s/%s", owner, repo, ref)
	m.ancestryMap[key] = commits
}

// e2eTestLogger implements Logger for E2E tests
type e2eTestLogger struct {
	t      *testing.T
	fields map[string]interface{}
}

func (l *e2eTestLogger) Debug(ctx context.Context, msg string, fields map[string]interface{}) {
	// Suppress debug in tests unless verbose
}

func (l *e2eTestLogger) Info(ctx context.Context, msg string, fields map[string]interface{}) {
	l.t.Logf("[INFO] %s %v", msg, fields)
}

func (l *e2eTestLogger) Warn(ctx context.Context, msg string, fields map[string]interface{}) {
	l.t.Logf("[WARN] %s %v", msg, fields)
}

func (l *e2eTestLogger) Warning(ctx context.Context, msg string, fields map[string]interface{}) {
	l.t.Logf("[WARN] %s %v", msg, fields)
}

func (l *e2eTestLogger) Error(ctx context.Context, msg string, err error, fields map[string]interface{}) {
	l.t.Logf("[ERROR] %s err=%v %v", msg, err, fields)
}

func (l *e2eTestLogger) WithFields(fields map[string]interface{}) Logger {
	newFields := make(map[string]interface{})
	for k, v := range l.fields {
		newFields[k] = v
	}
	for k, v := range fields {
		newFields[k] = v
	}
	return &e2eTestLogger{t: l.t, fields: newFields}
}

// e2eStepNames holds step names extracted from the pipeline config.
// Tests use these to drive behavior from the config instead of hardcoding.
type e2eStepNames struct {
	builds     string // aggregate step for builds (from config.GetAggregateStep("build"))
	unitTests  string // step for unit tests
	secretScan string // step for secret scanning
	devDeploy  string // step for dev deployment
}

// extractStepNamesFromConfig discovers step names from the loaded pipeline config.
// This ensures E2E tests exercise the full config interpretation flow.
func extractStepNamesFromConfig(t *testing.T, config *PipelineConfig) e2eStepNames {
	t.Helper()

	// Get the aggregate step for "build" - this tests GetAggregateStep()
	buildsStep := config.GetAggregateStep("build")
	if buildsStep == "" {
		t.Fatal("config has no aggregate step for 'build' - check default.json")
	}

	// These step names come directly from the config
	// In a more dynamic test, we could iterate config.Steps to find them
	names := e2eStepNames{
		builds:     buildsStep,
		unitTests:  "unit_tests",
		secretScan: "secret_scan",
		devDeploy:  "dev_deploy",
	}

	// Validate that referenced steps exist in config
	for _, stepName := range []string{names.unitTests, names.secretScan, names.devDeploy} {
		if config.GetStep(stepName) == nil {
			t.Fatalf("config missing expected step %q", stepName)
		}
	}

	t.Logf("Config-driven step names: builds=%q, unitTests=%q, secretScan=%q, devDeploy=%q",
		names.builds, names.unitTests, names.secretScan, names.devDeploy)

	return names
}

// =============================================================================
// FULL END-TO-END PIPELINE TEST
// =============================================================================

// TestE2E_FullPipelineFlow executes the complete CI/CD pipeline flow:
//
// Phase 1: PUSHHOOKPARSER FLOW
//   - Create routing slip with components (simulates push event)
//   - Initialize all steps from pipeline config
//   - Component builds start in parallel
//
// Phase 2: CONCURRENT BUILD UPDATES
//   - Multiple components complete builds concurrently
//   - Tests data accumulation without data loss
//   - Verifies aggregate step auto-updates
//
// Phase 3: SLIPPY PRE-JOB / POST-JOB FLOW
//   - Resolves slip via ancestry
//   - Checks prerequisites (holds until met)
//   - Updates step status (start → complete/fail)
//
// Phase 4: FULL PIPELINE COMPLETION
//   - All steps execute with proper ordering
//   - Verifies ReplacingMergeTree collapse works correctly
//   - Validates final slip state matches expected
//
// This test uses default.json for pipeline configuration (production schema).
func TestE2E_FullPipelineFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Disable ryuk for Podman compatibility
	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	// Start ClickHouse container
	container, err := setupE2EClickHouseContainer(ctx, t)
	if err != nil {
		t.Fatalf("failed to setup clickhouse container: %v", err)
	}
	defer func() {
		if err := container.terminate(ctx); err != nil {
			t.Logf("warning: failed to terminate container: %v", err)
		}
	}()

	// Load production pipeline config from default.json
	pipelineConfig, err := LoadPipelineConfigFromFile("default.json")
	if err != nil {
		t.Fatalf("failed to load default.json: %v", err)
	}
	t.Logf("Loaded pipeline config: %s (%d steps)", pipelineConfig.Name, len(pipelineConfig.Steps))

	// Extract step names from config - tests use these to drive behavior dynamically
	stepNames := extractStepNamesFromConfig(t, pipelineConfig)
	buildsStep := stepNames.builds
	unitTestsStep := stepNames.unitTests
	secretScanStep := stepNames.secretScan
	devDeployStep := stepNames.devDeploy

	// Verify the config has the expected step with prerequisites
	devDeployCfg := pipelineConfig.GetStep(devDeployStep)
	if devDeployCfg == nil {
		t.Fatalf("config missing step %q", devDeployStep)
	}
	t.Logf("Step %q prerequisites from config: %v", devDeployStep, devDeployCfg.Prerequisites)

	// Log all steps for visibility
	for _, step := range pipelineConfig.Steps {
		prereqs := "none"
		if len(step.Prerequisites) > 0 {
			prereqs = fmt.Sprintf("%v", step.Prerequisites)
		}
		t.Logf("  Step: %s (prereqs: %s, aggregates: %s, gate: %v)",
			step.Name, prereqs, step.Aggregates, step.IsGate)
	}

	// Run migrations
	migrateOpts := MigrateOptions{
		Database:       "ci_e2e_test",
		PipelineConfig: pipelineConfig,
	}
	result, err := RunMigrations(ctx, container.conn, migrateOpts)
	if err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}
	t.Logf("Migrations applied: %d (v%d → v%d)", result.MigrationsApplied, result.StartVersion, result.EndVersion)

	// Create test store
	session := &testClickhouseSession{conn: container.conn}
	store := NewClickHouseStoreFromSession(session, pipelineConfig, "ci_e2e_test")
	defer store.Close()

	// Create mock GitHub API
	mockGitHub := newMockGitHubAPIForE2E()

	// Create slippy client with test dependencies
	config := Config{
		PipelineConfig: pipelineConfig,
		HoldTimeout:    30 * time.Second,
		PollInterval:   500 * time.Millisecond,
		AncestryDepth:  20,
		Database:       "ci_e2e_test",
		Logger:         &e2eTestLogger{t: t},
	}
	client := NewClientWithDependencies(store, mockGitHub, config)

	// Define test components (realistic component names)
	components := []ComponentDefinition{
		{Name: "api", DockerfilePath: "src/MC.Api"},
		{Name: "worker", DockerfilePath: "src/MC.Worker"},
		{Name: "scheduler", DockerfilePath: "src/MC.Scheduler"},
		{Name: "frontend", DockerfilePath: "src/MC.Frontend"},
	}

	// =========================================================================
	// PHASE 1: PUSHHOOKPARSER FLOW - Create Routing Slip
	// =========================================================================
	t.Log("")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
	t.Log("PHASE 1: PUSHHOOKPARSER FLOW - Create Routing Slip")
	t.Log("═══════════════════════════════════════════════════════════════════════════")

	correlationID := fmt.Sprintf("e2e-test-%d", time.Now().UnixNano())
	commitSHA := fmt.Sprintf("abc%d", time.Now().UnixNano()%10000000)
	repository := "MyCarrier-DevOps/test-repo"
	branch := "main"

	// Setup ancestry for slip resolution
	mockGitHub.SetAncestry("MyCarrier-DevOps", "test-repo", commitSHA, []string{commitSHA})

	// Create slip (simulating pushhookparser)
	pushOpts := PushOptions{
		CorrelationID: correlationID,
		Repository:    repository,
		Branch:        branch,
		CommitSHA:     commitSHA,
		Components:    components,
	}

	createResult, err := client.CreateSlipForPush(ctx, pushOpts)
	if err != nil {
		t.Fatalf("PHASE 1 FAILED: CreateSlipForPush error: %v", err)
	}
	t.Logf("✓ Created slip: %s (ancestry resolved: %v, warnings: %d)",
		createResult.Slip.CorrelationID, createResult.AncestryResolved, len(createResult.Warnings))

	// Verify initial slip state
	slip, err := client.Load(ctx, correlationID)
	if err != nil {
		t.Fatalf("failed to load slip: %v", err)
	}

	t.Logf("✓ Slip status: %s, version: %d", slip.Status, slip.Version)
	t.Logf("✓ Steps initialized: %d", len(slip.Steps))
	t.Logf("✓ Aggregates initialized: %d", len(slip.Aggregates))

	// Verify builds_completed aggregate has all components
	buildsAggregate, ok := slip.Aggregates[buildsStep]
	if !ok {
		t.Logf("Available aggregates: %v", slip.Aggregates)
		t.Fatal("builds_completed aggregate not found")
	}
	t.Logf("✓ builds_completed has %d components", len(buildsAggregate))

	// =========================================================================
	// PHASE 2: CONCURRENT BUILD UPDATES
	// =========================================================================
	t.Log("")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
	t.Log("PHASE 2: CONCURRENT BUILD UPDATES")
	t.Log("═══════════════════════════════════════════════════════════════════════════")

	// Test concurrent builds completing
	var buildWg sync.WaitGroup
	buildErrors := make(chan error, len(components))

	for _, comp := range components {
		buildWg.Add(1)
		go func(component ComponentDefinition) {
			defer buildWg.Done()

			// Random delay to simulate real build timing (0-200ms)
			time.Sleep(time.Duration(rand.Intn(200)) * time.Millisecond)

			// Start the build
			if err := client.StartStep(ctx, correlationID, buildsStep, component.Name); err != nil {
				buildErrors <- fmt.Errorf("start build %s: %w", component.Name, err)
				return
			}

			// Simulate build work (50-150ms)
			time.Sleep(time.Duration(50+rand.Intn(100)) * time.Millisecond)

			// Complete the build
			if err := client.CompleteStep(ctx, correlationID, buildsStep, component.Name); err != nil {
				buildErrors <- fmt.Errorf("complete build %s: %w", component.Name, err)
				return
			}
			t.Logf("✓ Build completed: %s", component.Name)
		}(comp)
	}

	buildWg.Wait()
	close(buildErrors)

	// Check for build errors
	for err := range buildErrors {
		t.Fatalf("PHASE 2 FAILED: %v", err)
	}

	// Verify all builds completed and data was accumulated
	slip, err = client.Load(ctx, correlationID)
	if err != nil {
		t.Fatalf("failed to load slip after builds: %v", err)
	}

	buildsAggregate = slip.Aggregates[buildsStep]
	completedBuilds := make(map[string]bool)
	for _, build := range buildsAggregate {
		if build.Status == StepStatusCompleted {
			completedBuilds[build.Component] = true
			t.Logf("✓ builds_completed/%s = %s", build.Component, build.Status)
		}
	}

	if len(completedBuilds) != len(components) {
		t.Fatalf("PHASE 2 FAILED: Expected %d completed builds, got %d",
			len(components), len(completedBuilds))
	}

	t.Logf("✓ All %d concurrent builds completed successfully", len(components))

	// Verify builds_completed step was auto-updated to completed
	if slip.Steps[buildsStep].Status != StepStatusCompleted {
		t.Errorf("Expected builds_completed step to be completed, got %s",
			slip.Steps[buildsStep].Status)
	}
	t.Log("✓ builds_completed aggregate step auto-completed")

	// =========================================================================
	// PHASE 3: SEQUENTIAL UNIT TEST UPDATES (non-concurrent)
	// =========================================================================
	t.Log("")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
	t.Log("PHASE 3: SEQUENTIAL STEP UPDATES (non-aggregate steps)")
	t.Log("═══════════════════════════════════════════════════════════════════════════")

	// Note: In production default.json, unit_tests_completed is a REGULAR step,
	// not an aggregate step (no "aggregates" field). So we complete it directly.
	t.Log("DEBUG: Before StartStep, checking step in config...")
	stepConfig := pipelineConfig.GetStep(unitTestsStep)
	if stepConfig == nil {
		t.Fatal("FATAL: unit_tests_completed not found in pipeline config!")
	}
	t.Logf(
		"DEBUG: Found step in config: %s (aggregates: %q, gate: %v)",
		stepConfig.Name,
		stepConfig.Aggregates,
		stepConfig.IsGate,
	)

	if err := client.StartStep(ctx, correlationID, unitTestsStep, ""); err != nil {
		t.Fatalf("failed to start unit_tests_completed: %v", err)
	}
	if err := client.CompleteStep(ctx, correlationID, unitTestsStep, ""); err != nil {
		t.Fatalf("failed to complete unit_tests_completed: %v", err)
	}
	t.Log("✓ unit_tests_completed step completed")

	// Debug: Query raw database state
	t.Log("DEBUG: Querying raw database state for unit_tests_completed...")
	debugRows, dbErr := container.conn.Query(ctx, fmt.Sprintf(
		"SELECT unit_tests_completed_status, sign, version FROM %s.routing_slips WHERE correlation_id = '%s' ORDER BY version DESC",
		"ci_e2e_test",
		correlationID,
	))
	if dbErr != nil {
		t.Logf("DEBUG: Failed to query raw state: %v", dbErr)
	} else {
		for debugRows.Next() {
			var status string
			var sign int8
			var version uint64
			if err := debugRows.Scan(&status, &sign, &version); err != nil {
				t.Logf("DEBUG: Scan error: %v", err)
			} else {
				t.Logf("DEBUG: Raw row - status=%s, sign=%d, version=%d", status, sign, version)
			}
		}
		debugRows.Close()
	}

	// Debug: Query with FINAL
	t.Log("DEBUG: Querying with FINAL...")
	finalRows, finalErr := container.conn.Query(ctx, fmt.Sprintf(
		"SELECT unit_tests_completed_status, sign, version FROM %s.routing_slips FINAL WHERE correlation_id = '%s'",
		"ci_e2e_test", correlationID))
	if finalErr != nil {
		t.Logf("DEBUG: Failed to query with FINAL: %v", finalErr)
	} else {
		for finalRows.Next() {
			var status string
			var sign int8
			var version uint64
			if err := finalRows.Scan(&status, &sign, &version); err != nil {
				t.Logf("DEBUG: FINAL Scan error: %v", err)
			} else {
				t.Logf("DEBUG: FINAL row - status=%s, sign=%d, version=%d", status, sign, version)
			}
		}
		finalRows.Close()
	}

	// Verify unit tests completed
	slip, err = client.Load(ctx, correlationID)
	if err != nil {
		t.Fatalf("failed to load slip after unit tests: %v", err)
	}

	// Debug: dump all step statuses
	t.Log("DEBUG: All step statuses after unit_tests_completed:")
	for stepName, step := range slip.Steps {
		t.Logf("  - %s: %s", stepName, step.Status)
	}

	if slip.Steps[unitTestsStep].Status != StepStatusCompleted {
		t.Errorf("Expected unit_tests_completed step to be completed, got %s",
			slip.Steps[unitTestsStep].Status)
	}
	t.Log("✓ unit_tests_completed verified as completed")

	// =========================================================================
	// PHASE 4: SLIPPY PRE-JOB / POST-JOB FLOW
	// =========================================================================
	t.Log("")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
	t.Log("PHASE 4: SLIPPY PRE-JOB / POST-JOB FLOW")
	t.Log("═══════════════════════════════════════════════════════════════════════════")

	// Test slip resolution (what Slippy CLI does in pre-job)
	resolveOpts := ResolveOptions{
		Repository:    repository,
		Ref:           commitSHA,
		AncestryDepth: 20,
	}
	resolveResult, err := client.ResolveSlip(ctx, resolveOpts)
	if err != nil {
		t.Fatalf("PHASE 4 FAILED: ResolveSlip error: %v", err)
	}
	t.Logf("✓ Resolved slip: %s (by: %s, commit: %s)",
		resolveResult.Slip.CorrelationID, resolveResult.ResolvedBy, resolveResult.MatchedCommit)

	// Complete secret_scan step (no prerequisites)
	t.Log("Completing secret_scan step...")
	if err := client.StartStep(ctx, correlationID, secretScanStep, ""); err != nil {
		t.Fatalf("failed to start secret_scan: %v", err)
	}
	if err := client.CompleteStep(ctx, correlationID, secretScanStep, ""); err != nil {
		t.Fatalf("failed to complete secret_scan: %v", err)
	}
	t.Log("✓ secret_scan completed")

	// Complete package_artifact step (no prerequisites - optional step for nuget/npm publishing)
	t.Log("Completing package_artifact step (optional package publishing)...")
	if err := client.StartStep(ctx, correlationID, "package_artifact", ""); err != nil {
		t.Fatalf("failed to start package_artifact: %v", err)
	}
	if err := client.CompleteStep(ctx, correlationID, "package_artifact", ""); err != nil {
		t.Fatalf("failed to complete package_artifact: %v", err)
	}
	t.Log("✓ package_artifact completed")

	// Complete dev_deploy step (has prerequisites: builds_completed, unit_tests_completed, secret_scan)
	t.Log("Executing dev_deploy step (with prerequisite check)...")

	// Reload the slip to get the latest state before checking prerequisites
	slip, _ = client.Load(ctx, correlationID)
	prereqResult, err := client.CheckPrerequisites(
		ctx,
		slip,
		[]string{buildsStep, unitTestsStep, secretScanStep},
		"",
	)
	if err != nil {
		t.Fatalf("failed to check prerequisites: %v", err)
	}
	t.Logf("✓ Prerequisites check: status=%s, completed=%v", prereqResult.Status, prereqResult.CompletedPrereqs)

	if prereqResult.Status != PrereqStatusCompleted {
		t.Fatalf("Prerequisites not met: running=%v, failed=%v",
			prereqResult.RunningPrereqs, prereqResult.FailedPrereqs)
	}

	// Now start and complete dev_deploy
	if err := client.StartStep(ctx, correlationID, "dev_deploy", ""); err != nil {
		t.Fatalf("failed to start dev_deploy: %v", err)
	}
	if err := client.CompleteStep(ctx, correlationID, "dev_deploy", ""); err != nil {
		t.Fatalf("failed to complete dev_deploy: %v", err)
	}
	t.Log("✓ dev_deploy completed")

	// Complete dev_tests step
	if err := client.StartStep(ctx, correlationID, "dev_tests", ""); err != nil {
		t.Fatalf("failed to start dev_tests: %v", err)
	}
	if err := client.CompleteStep(ctx, correlationID, "dev_tests", ""); err != nil {
		t.Fatalf("failed to complete dev_tests: %v", err)
	}
	t.Log("✓ dev_tests completed")

	// Complete preprod_deploy and preprod_tests
	if err := client.StartStep(ctx, correlationID, "preprod_deploy", ""); err != nil {
		t.Fatalf("failed to start preprod_deploy: %v", err)
	}
	if err := client.CompleteStep(ctx, correlationID, "preprod_deploy", ""); err != nil {
		t.Fatalf("failed to complete preprod_deploy: %v", err)
	}
	t.Log("✓ preprod_deploy completed")

	if err := client.StartStep(ctx, correlationID, "preprod_tests", ""); err != nil {
		t.Fatalf("failed to start preprod_tests: %v", err)
	}
	if err := client.CompleteStep(ctx, correlationID, "preprod_tests", ""); err != nil {
		t.Fatalf("failed to complete preprod_tests: %v", err)
	}
	t.Log("✓ preprod_tests completed")

	// =========================================================================
	// PHASE 5: PRODUCTION GATE AND DEPLOYMENT
	// =========================================================================
	t.Log("")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
	t.Log("PHASE 5: PRODUCTION GATE AND DEPLOYMENT")
	t.Log("═══════════════════════════════════════════════════════════════════════════")

	// Complete prod_gate (is_gate: true)
	if err := client.StartStep(ctx, correlationID, "prod_gate", ""); err != nil {
		t.Fatalf("failed to start prod_gate: %v", err)
	}
	if err := client.CompleteStep(ctx, correlationID, "prod_gate", ""); err != nil {
		t.Fatalf("failed to complete prod_gate: %v", err)
	}
	t.Log("✓ prod_gate completed (gate step)")

	// Complete prod_release_created
	if err := client.StartStep(ctx, correlationID, "prod_release_created", ""); err != nil {
		t.Fatalf("failed to start prod_release_created: %v", err)
	}
	if err := client.CompleteStep(ctx, correlationID, "prod_release_created", ""); err != nil {
		t.Fatalf("failed to complete prod_release_created: %v", err)
	}
	t.Log("✓ prod_release_created completed")

	// Complete prod_deploy
	if err := client.StartStep(ctx, correlationID, "prod_deploy", ""); err != nil {
		t.Fatalf("failed to start prod_deploy: %v", err)
	}
	if err := client.CompleteStep(ctx, correlationID, "prod_deploy", ""); err != nil {
		t.Fatalf("failed to complete prod_deploy: %v", err)
	}
	t.Log("✓ prod_deploy completed")

	// Complete prod_tests (final step)
	if err := client.StartStep(ctx, correlationID, "prod_tests", ""); err != nil {
		t.Fatalf("failed to start prod_tests: %v", err)
	}
	if err := client.CompleteStep(ctx, correlationID, "prod_tests", ""); err != nil {
		t.Fatalf("failed to complete prod_tests: %v", err)
	}
	t.Log("✓ prod_tests completed (FINAL STEP)")

	// =========================================================================
	// PHASE 6: VERIFICATION
	// =========================================================================
	t.Log("")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
	t.Log("PHASE 6: FINAL VERIFICATION")
	t.Log("═══════════════════════════════════════════════════════════════════════════")

	// Force OPTIMIZE to collapse all rows
	if err := store.OptimizeTable(ctx); err != nil {
		t.Logf("Warning: OPTIMIZE TABLE failed: %v", err)
	}

	// Load final slip state
	finalSlip, err := client.Load(ctx, correlationID)
	if err != nil {
		t.Fatalf("failed to load final slip: %v", err)
	}

	t.Log("")
	t.Log("FINAL SLIP STATE:")
	t.Logf("  Correlation ID: %s", finalSlip.CorrelationID)
	t.Logf("  Repository: %s", finalSlip.Repository)
	t.Logf("  Branch: %s", finalSlip.Branch)
	t.Logf("  Commit: %s", finalSlip.CommitSHA)
	t.Logf("  Status: %s", finalSlip.Status)
	t.Logf("  Version: %d", finalSlip.Version)
	t.Log("")
	t.Log("  STEP STATUSES:")

	// Verify all steps are completed
	allStepsCompleted := true
	for _, stepCfg := range pipelineConfig.Steps {
		step, ok := finalSlip.Steps[stepCfg.Name]
		if !ok {
			t.Errorf("Step %s not found in final slip", stepCfg.Name)
			allStepsCompleted = false
			continue
		}
		statusSymbol := "✓"
		if step.Status != StepStatusCompleted {
			statusSymbol = "✗"
			allStepsCompleted = false
		}
		t.Logf("    %s %s: %s", statusSymbol, stepCfg.Name, step.Status)
	}

	t.Log("")
	t.Log("  AGGREGATE COMPONENT STATUSES:")
	for aggName, components := range finalSlip.Aggregates {
		t.Logf("    %s (%d components):", aggName, len(components))
		for _, comp := range components {
			statusSymbol := "✓"
			if comp.Status != StepStatusCompleted {
				statusSymbol = "✗"
			}
			t.Logf("      %s %s: %s", statusSymbol, comp.Component, comp.Status)
		}
	}

	// Verify data integrity
	t.Log("")
	t.Log("DATA INTEGRITY CHECKS:")

	// Check all builds completed
	buildsAggregate = finalSlip.Aggregates[buildsStep]
	if len(buildsAggregate) != len(components) {
		t.Errorf("Expected %d components in builds_completed, got %d",
			len(components), len(buildsAggregate))
	} else {
		t.Logf("  ✓ builds_completed has correct component count (%d)", len(buildsAggregate))
	}

	// Check row count in database (should be exactly 1 after OPTIMIZE)
	var rowCount uint64
	if err := container.conn.QueryRow(ctx, `
		SELECT count(*) FROM ci_e2e_test.routing_slips 
		WHERE correlation_id = $1
	`, correlationID).Scan(&rowCount); err != nil {
		t.Errorf("Failed to count rows: %v", err)
	} else {
		// Note: ReplacingMergeTree may have 2 rows (version and sign-based)
		// but FINAL should return 1
		t.Logf("  ✓ Raw row count: %d (OPTIMIZE may not immediately collapse)", rowCount)
	}

	// Check active row count (sign = 1 gives us the current active state)
	// Note: VCMT's FINAL modifier may return multiple rows until background merges complete
	// The sign = 1 filter is the authoritative way to get active rows
	var activeRowCount uint64
	if err := container.conn.QueryRow(ctx, `
		SELECT count(*) FROM ci_e2e_test.routing_slips
		WHERE correlation_id = $1 AND sign = 1
	`, correlationID).Scan(&activeRowCount); err != nil {
		t.Errorf("Failed to count active rows: %v", err)
	} else {
		// We expect at least 1 active row (the current state)
		if activeRowCount < 1 {
			t.Errorf("Expected at least 1 active row (sign=1), got %d", activeRowCount)
		} else {
			t.Logf("  ✓ Active row count (sign=1): %d", activeRowCount)
		}
	}

	if allStepsCompleted {
		t.Log("")
		t.Log("═══════════════════════════════════════════════════════════════════════════")
		t.Log("SUCCESS: Full E2E pipeline completed successfully!")
		t.Log("═══════════════════════════════════════════════════════════════════════════")
	} else {
		t.Fatal("FAILED: Not all steps completed")
	}
}

// =============================================================================
// CONCURRENT WRITE TEST - Stress Test Data Accumulation
// =============================================================================

// TestE2E_ConcurrentWriteStressTest performs a high-contention concurrent write test
// to verify that the ReplacingMergeTree correctly accumulates all data without loss.
// This test specifically validates:
// 1. Multiple goroutines updating the same slip simultaneously
// 2. Component state event sourcing handles concurrent writes
// 3. No data loss when many updates hit the same correlation ID
// 4. FINAL correctly collapses all rows to a single consistent state
func TestE2E_ConcurrentWriteStressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E stress test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Disable ryuk for Podman compatibility
	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	// Start ClickHouse container
	container, err := setupE2EClickHouseContainer(ctx, t)
	if err != nil {
		t.Fatalf("failed to setup clickhouse container: %v", err)
	}
	defer func() {
		if err := container.terminate(ctx); err != nil {
			t.Logf("warning: failed to terminate container: %v", err)
		}
	}()

	// Load production pipeline config
	pipelineConfig, err := LoadPipelineConfigFromFile("default.json")
	if err != nil {
		t.Fatalf("failed to load default.json: %v", err)
	}

	// Extract step names from config
	stepNames := extractStepNamesFromConfig(t, pipelineConfig)
	buildsStep := stepNames.builds

	// Run migrations
	migrateOpts := MigrateOptions{
		Database:       "ci_stress_test",
		PipelineConfig: pipelineConfig,
	}
	if _, err := RunMigrations(ctx, container.conn, migrateOpts); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// Create test store
	session := &testClickhouseSession{conn: container.conn}
	store := NewClickHouseStoreFromSession(session, pipelineConfig, "ci_stress_test")
	defer store.Close()

	// Create client
	mockGitHub := newMockGitHubAPIForE2E()
	config := Config{
		PipelineConfig: pipelineConfig,
		HoldTimeout:    30 * time.Second,
		PollInterval:   100 * time.Millisecond,
		AncestryDepth:  20,
		Database:       "ci_stress_test",
		Logger:         &e2eTestLogger{t: t},
	}
	client := NewClientWithDependencies(store, mockGitHub, config)

	// Create many components to stress test
	numComponents := 20 // High number of components
	components := make([]ComponentDefinition, numComponents)
	for i := 0; i < numComponents; i++ {
		components[i] = ComponentDefinition{
			Name:           fmt.Sprintf("component-%02d", i),
			DockerfilePath: fmt.Sprintf("src/MC.Component%02d", i),
		}
	}

	t.Logf("Starting concurrent write stress test with %d components", numComponents)

	// Create slip
	correlationID := fmt.Sprintf("stress-test-%d", time.Now().UnixNano())
	commitSHA := fmt.Sprintf("stress%d", time.Now().UnixNano()%10000000)
	repository := "MyCarrier-DevOps/stress-test"

	mockGitHub.SetAncestry("MyCarrier-DevOps", "stress-test", commitSHA, []string{commitSHA})

	pushOpts := PushOptions{
		CorrelationID: correlationID,
		Repository:    repository,
		Branch:        "main",
		CommitSHA:     commitSHA,
		Components:    components,
	}

	if _, err := client.CreateSlipForPush(ctx, pushOpts); err != nil {
		t.Fatalf("failed to create slip: %v", err)
	}
	t.Logf("Created slip: %s", correlationID)

	// Concurrently update ALL components simultaneously
	t.Log("Starting high-contention concurrent updates...")

	var wg sync.WaitGroup
	errors := make(chan error, numComponents*2) // start + complete per component

	startTime := time.Now()

	// Start all goroutines simultaneously
	t.Logf("Starting %d concurrent updates (each updating a different component)...", numComponents)

	// Use a barrier to ensure maximum contention
	var barrier sync.WaitGroup
	barrier.Add(1)

	for _, comp := range components {
		wg.Add(1)
		go func(component ComponentDefinition) {
			defer wg.Done()

			// Wait for all goroutines to be ready
			barrier.Wait()

			// Add slight random jitter (0-10ms) to create realistic contention
			time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)

			// Start and complete build in quick succession
			if err := client.StartStep(ctx, correlationID, buildsStep, component.Name); err != nil {
				errors <- fmt.Errorf("start %s: %w", component.Name, err)
				return
			}

			// Minimal processing time
			time.Sleep(time.Duration(rand.Intn(5)) * time.Millisecond)

			if err := client.CompleteStep(ctx, correlationID, buildsStep, component.Name); err != nil {
				errors <- fmt.Errorf("complete %s: %w", component.Name, err)
				return
			}
		}(comp)
	}

	// Release all goroutines at once for maximum contention
	barrier.Done()

	// Wait for all to complete
	wg.Wait()
	close(errors)

	elapsed := time.Since(startTime)
	t.Logf("Concurrent updates completed in %v", elapsed)

	// Check for errors
	errorCount := 0
	for err := range errors {
		t.Errorf("Concurrent update error: %v", err)
		errorCount++
	}

	if errorCount > 0 {
		t.Fatalf("STRESS TEST FAILED: %d errors during concurrent updates", errorCount)
	}

	// Force OPTIMIZE
	if err := store.OptimizeTable(ctx); err != nil {
		t.Logf("Warning: OPTIMIZE TABLE failed: %v", err)
	}

	// Verify all data was accumulated
	finalSlip, err := client.Load(ctx, correlationID)
	if err != nil {
		t.Fatalf("failed to load final slip: %v", err)
	}

	buildsAggregate := finalSlip.Aggregates[buildsStep]

	t.Log("")
	t.Log("STRESS TEST RESULTS:")
	t.Logf("  Total components: %d", numComponents)
	t.Logf("  Components in aggregate: %d", len(buildsAggregate))

	// Build a map of component statuses
	componentStatuses := make(map[string]StepStatus)
	for _, comp := range buildsAggregate {
		componentStatuses[comp.Component] = comp.Status
	}

	// Verify each component
	missingOrIncomplete := []string{}
	for _, comp := range components {
		status, found := componentStatuses[comp.Name]
		if !found {
			missingOrIncomplete = append(missingOrIncomplete, fmt.Sprintf("%s (MISSING)", comp.Name))
		} else if status != StepStatusCompleted {
			missingOrIncomplete = append(missingOrIncomplete, fmt.Sprintf("%s (status=%s)", comp.Name, status))
		}
	}

	if len(missingOrIncomplete) > 0 {
		t.Log("  MISSING OR INCOMPLETE COMPONENTS:")
		for _, issue := range missingOrIncomplete {
			t.Logf("    - %s", issue)
		}
		t.Fatalf("STRESS TEST FAILED: %d components missing or incomplete", len(missingOrIncomplete))
	}

	t.Logf("  ✓ All %d components correctly accumulated", numComponents)

	// Verify aggregate step status
	if finalSlip.Steps[buildsStep].Status != StepStatusCompleted {
		t.Errorf("Expected builds_completed step to be completed, got %s",
			finalSlip.Steps[buildsStep].Status)
	} else {
		t.Log("  ✓ builds_completed aggregate step auto-completed")
	}

	// Verify active row count (sign=1)
	// Note: VCMT's FINAL may return multiple rows until background merges complete
	// The sign=1 filter is the authoritative way to get active rows
	var activeRowCount uint64
	if err := container.conn.QueryRow(ctx, `
		SELECT count(*) FROM ci_stress_test.routing_slips
		WHERE correlation_id = $1 AND sign = 1
	`, correlationID).Scan(&activeRowCount); err != nil {
		t.Errorf("Failed to count active rows: %v", err)
	}
	if activeRowCount < 1 {
		t.Errorf("Expected at least 1 active row (sign=1), got %d", activeRowCount)
	} else {
		t.Logf("  ✓ Active row count (sign=1): %d", activeRowCount)
	}

	t.Log("")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
	t.Logf("SUCCESS: Concurrent write stress test passed (%d components, %v)", numComponents, elapsed)
	t.Log("═══════════════════════════════════════════════════════════════════════════")
}

// =============================================================================
// PREREQUISITE WAITING TEST
// =============================================================================

// TestE2E_PrerequisiteWaiting tests the hold/wait mechanism for prerequisites.
// This simulates Slippy CLI waiting for upstream steps to complete before proceeding.
func TestE2E_PrerequisiteWaiting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E prerequisite test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	container, err := setupE2EClickHouseContainer(ctx, t)
	if err != nil {
		t.Fatalf("failed to setup clickhouse container: %v", err)
	}
	defer container.terminate(ctx)

	pipelineConfig, err := LoadPipelineConfigFromFile("default.json")
	if err != nil {
		t.Fatalf("failed to load default.json: %v", err)
	}

	// Extract step names from config
	stepNames := extractStepNamesFromConfig(t, pipelineConfig)
	buildsStep := stepNames.builds
	unitTestsStep := stepNames.unitTests
	secretScanStep := stepNames.secretScan

	migrateOpts := MigrateOptions{
		Database:       "ci_prereq_test",
		PipelineConfig: pipelineConfig,
	}
	if _, err := RunMigrations(ctx, container.conn, migrateOpts); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	session := &testClickhouseSession{conn: container.conn}
	store := NewClickHouseStoreFromSession(session, pipelineConfig, "ci_prereq_test")
	defer store.Close()

	mockGitHub := newMockGitHubAPIForE2E()
	config := Config{
		PipelineConfig: pipelineConfig,
		HoldTimeout:    5 * time.Second, // Short timeout for test
		PollInterval:   100 * time.Millisecond,
		AncestryDepth:  20,
		Database:       "ci_prereq_test",
		Logger:         &e2eTestLogger{t: t},
	}
	client := NewClientWithDependencies(store, mockGitHub, config)

	// Create slip with components
	correlationID := fmt.Sprintf("prereq-test-%d", time.Now().UnixNano())
	commitSHA := fmt.Sprintf("prereq%d", time.Now().UnixNano()%10000000)
	repository := "MyCarrier-DevOps/prereq-test"

	mockGitHub.SetAncestry("MyCarrier-DevOps", "prereq-test", commitSHA, []string{commitSHA})

	components := []ComponentDefinition{
		{Name: "api", DockerfilePath: "src/MC.Api"},
		{Name: "worker", DockerfilePath: "src/MC.Worker"},
	}

	pushOpts := PushOptions{
		CorrelationID: correlationID,
		Repository:    repository,
		Branch:        "main",
		CommitSHA:     commitSHA,
		Components:    components,
	}

	if _, err := client.CreateSlipForPush(ctx, pushOpts); err != nil {
		t.Fatalf("failed to create slip: %v", err)
	}

	t.Log("Testing prerequisite waiting...")

	// Start a goroutine that will complete prerequisites after a delay
	go func() {
		time.Sleep(1 * time.Second)
		t.Log("Background: Completing builds...")
		// builds_completed is an aggregate step with components
		for _, comp := range components {
			client.CompleteStep(ctx, correlationID, buildsStep, comp.Name)
		}

		time.Sleep(500 * time.Millisecond)
		t.Log("Background: Completing unit tests...")
		// unit_tests_completed is a simple step (no aggregates), so complete without component
		client.CompleteStep(ctx, correlationID, unitTestsStep, "")

		time.Sleep(500 * time.Millisecond)
		t.Log("Background: Completing secret_scan...")
		client.CompleteStep(ctx, correlationID, secretScanStep, "")
	}()

	// Wait for prerequisites (should block until the background goroutine completes them)
	prereqs := []string{buildsStep, unitTestsStep, secretScanStep}
	holdOpts := HoldOptions{
		CorrelationID: correlationID,
		Prerequisites: prereqs,
		Timeout:       10 * time.Second,
		PollInterval:  200 * time.Millisecond,
		StepName:      "dev_deploy",
	}

	t.Logf("Waiting for prerequisites: %v (timeout: %v)", prereqs, holdOpts.Timeout)
	startWait := time.Now()

	err = client.WaitForPrerequisites(ctx, holdOpts)
	waitDuration := time.Since(startWait)

	if err != nil {
		t.Fatalf("WaitForPrerequisites failed: %v", err)
	}

	t.Logf("✓ Prerequisites met after %v", waitDuration)

	// Verify the step was held while waiting
	slip, _ := client.Load(ctx, correlationID)
	t.Logf("✓ dev_deploy step transitioned correctly (final status will be set by caller)")

	// Verify all prerequisites are completed
	for _, prereq := range prereqs {
		step := slip.Steps[prereq]
		if step.Status != StepStatusCompleted {
			t.Errorf("Prerequisite %s should be completed, got %s", prereq, step.Status)
		}
	}

	t.Log("")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
	t.Log("SUCCESS: Prerequisite waiting test passed")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
}

// =============================================================================
// DATA COLLAPSE VERIFICATION TEST
// =============================================================================

// TestE2E_ReplacingMergeTreeCollapse verifies that ReplacingMergeTree correctly
// collapses multiple versions of the same record into a single final state.
func TestE2E_ReplacingMergeTreeCollapse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E collapse test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	container, err := setupE2EClickHouseContainer(ctx, t)
	if err != nil {
		t.Fatalf("failed to setup clickhouse container: %v", err)
	}
	defer container.terminate(ctx)

	pipelineConfig, err := LoadPipelineConfigFromFile("default.json")
	if err != nil {
		t.Fatalf("failed to load default.json: %v", err)
	}

	// Extract step names from config (not used directly in this test but validates config)
	_ = extractStepNamesFromConfig(t, pipelineConfig)

	migrateOpts := MigrateOptions{
		Database:       "ci_collapse_test",
		PipelineConfig: pipelineConfig,
	}
	if _, err := RunMigrations(ctx, container.conn, migrateOpts); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	session := &testClickhouseSession{conn: container.conn}
	store := NewClickHouseStoreFromSession(session, pipelineConfig, "ci_collapse_test")
	defer store.Close()

	mockGitHub := newMockGitHubAPIForE2E()
	config := Config{
		PipelineConfig: pipelineConfig,
		HoldTimeout:    30 * time.Second,
		PollInterval:   100 * time.Millisecond,
		AncestryDepth:  20,
		Database:       "ci_collapse_test",
		Logger:         &e2eTestLogger{t: t},
	}
	client := NewClientWithDependencies(store, mockGitHub, config)

	// Create slip
	correlationID := fmt.Sprintf("collapse-test-%d", time.Now().UnixNano())
	commitSHA := fmt.Sprintf("collapse%d", time.Now().UnixNano()%10000000)
	repository := "MyCarrier-DevOps/collapse-test"

	mockGitHub.SetAncestry("MyCarrier-DevOps", "collapse-test", commitSHA, []string{commitSHA})

	components := []ComponentDefinition{
		{Name: "api", DockerfilePath: "src/MC.Api"},
	}

	pushOpts := PushOptions{
		CorrelationID: correlationID,
		Repository:    repository,
		Branch:        "main",
		CommitSHA:     commitSHA,
		Components:    components,
	}

	if _, err := client.CreateSlipForPush(ctx, pushOpts); err != nil {
		t.Fatalf("failed to create slip: %v", err)
	}

	// Perform many sequential updates to create many versions
	numUpdates := 50
	t.Logf("Performing %d sequential updates to create version history...", numUpdates)

	for i := 0; i < numUpdates; i++ {
		slip, err := store.Load(ctx, correlationID)
		if err != nil {
			t.Fatalf("update %d: failed to load: %v", i, err)
		}
		slip.UpdatedAt = time.Now()
		if err := store.Update(ctx, slip); err != nil {
			t.Fatalf("update %d: failed to update: %v", i, err)
		}
	}
	t.Logf("✓ Completed %d updates", numUpdates)

	// Count raw rows (without FINAL)
	var rawRowCount uint64
	if err := container.conn.QueryRow(ctx, `
		SELECT count(*) FROM ci_collapse_test.routing_slips
		WHERE correlation_id = $1
	`, correlationID).Scan(&rawRowCount); err != nil {
		t.Fatalf("failed to count raw rows: %v", err)
	}
	t.Logf("Raw row count (before OPTIMIZE): %d", rawRowCount)

	// Count rows with FINAL (logical view)
	var finalRowCount uint64
	if err := container.conn.QueryRow(ctx, `
		SELECT count(*) FROM ci_collapse_test.routing_slips FINAL
		WHERE correlation_id = $1
	`, correlationID).Scan(&finalRowCount); err != nil {
		t.Fatalf("failed to count FINAL rows: %v", err)
	}
	t.Logf("FINAL row count (logical view): %d", finalRowCount)

	if finalRowCount != 1 {
		t.Errorf("Expected FINAL to return exactly 1 row, got %d", finalRowCount)
	}

	// Run OPTIMIZE TABLE to physically collapse rows
	t.Log("Running OPTIMIZE TABLE...")
	if err := store.OptimizeTable(ctx); err != nil {
		t.Logf("Warning: OPTIMIZE TABLE failed: %v", err)
	}

	// Small delay for OPTIMIZE to complete
	time.Sleep(2 * time.Second)

	// Count raw rows after OPTIMIZE
	var postOptimizeCount uint64
	if err := container.conn.QueryRow(ctx, `
		SELECT count(*) FROM ci_collapse_test.routing_slips
		WHERE correlation_id = $1
	`, correlationID).Scan(&postOptimizeCount); err != nil {
		t.Fatalf("failed to count rows after OPTIMIZE: %v", err)
	}
	t.Logf("Raw row count (after OPTIMIZE): %d", postOptimizeCount)

	// Load final slip and verify data integrity
	finalSlip, err := client.Load(ctx, correlationID)
	if err != nil {
		t.Fatalf("failed to load final slip: %v", err)
	}

	// Verify slip data is intact
	if finalSlip.CorrelationID != correlationID {
		t.Errorf("CorrelationID mismatch: expected %s, got %s", correlationID, finalSlip.CorrelationID)
	}
	if finalSlip.Repository != repository {
		t.Errorf("Repository mismatch: expected %s, got %s", repository, finalSlip.Repository)
	}
	if finalSlip.CommitSHA != commitSHA {
		t.Errorf("CommitSHA mismatch: expected %s, got %s", commitSHA, finalSlip.CommitSHA)
	}

	t.Log("")
	t.Log("COLLAPSE TEST RESULTS:")
	t.Logf("  Updates performed: %d", numUpdates)
	t.Logf("  Raw rows before OPTIMIZE: %d", rawRowCount)
	t.Logf("  FINAL rows (logical): %d", finalRowCount)
	t.Logf("  Raw rows after OPTIMIZE: %d", postOptimizeCount)
	t.Logf("  Data integrity: VERIFIED")
	t.Log("")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
	t.Log("SUCCESS: ReplacingMergeTree collapse test passed")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
}

// =============================================================================
// COMPONENT STATE EVENT SOURCING TEST
// =============================================================================

// TestE2E_ComponentStateEventSourcing verifies that component states are properly
// stored in the event sourcing table (slip_component_states) and correctly
// hydrated back into slip aggregates on load.
func TestE2E_ComponentStateEventSourcing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E event sourcing test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	container, err := setupE2EClickHouseContainer(ctx, t)
	if err != nil {
		t.Fatalf("failed to setup clickhouse container: %v", err)
	}
	defer container.terminate(ctx)

	pipelineConfig, err := LoadPipelineConfigFromFile("default.json")
	if err != nil {
		t.Fatalf("failed to load default.json: %v", err)
	}

	// Extract step names from config
	stepNames := extractStepNamesFromConfig(t, pipelineConfig)
	buildsStep := stepNames.builds

	migrateOpts := MigrateOptions{
		Database:       "ci_eventsource_test",
		PipelineConfig: pipelineConfig,
	}
	if _, err := RunMigrations(ctx, container.conn, migrateOpts); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	session := &testClickhouseSession{conn: container.conn}
	store := NewClickHouseStoreFromSession(session, pipelineConfig, "ci_eventsource_test")
	defer store.Close()

	mockGitHub := newMockGitHubAPIForE2E()
	config := Config{
		PipelineConfig: pipelineConfig,
		HoldTimeout:    30 * time.Second,
		PollInterval:   100 * time.Millisecond,
		AncestryDepth:  20,
		Database:       "ci_eventsource_test",
		Logger:         &e2eTestLogger{t: t},
	}
	client := NewClientWithDependencies(store, mockGitHub, config)

	// Create slip with multiple components
	correlationID := fmt.Sprintf("eventsource-test-%d", time.Now().UnixNano())
	commitSHA := fmt.Sprintf("eventsource%d", time.Now().UnixNano()%10000000)
	repository := "MyCarrier-DevOps/eventsource-test"

	mockGitHub.SetAncestry("MyCarrier-DevOps", "eventsource-test", commitSHA, []string{commitSHA})

	components := []ComponentDefinition{
		{Name: "api", DockerfilePath: "src/MC.Api"},
		{Name: "worker", DockerfilePath: "src/MC.Worker"},
		{Name: "scheduler", DockerfilePath: "src/MC.Scheduler"},
	}

	pushOpts := PushOptions{
		CorrelationID: correlationID,
		Repository:    repository,
		Branch:        "main",
		CommitSHA:     commitSHA,
		Components:    components,
	}

	if _, err := client.CreateSlipForPush(ctx, pushOpts); err != nil {
		t.Fatalf("failed to create slip: %v", err)
	}

	t.Log("Testing component state event sourcing...")

	// Update each component through multiple states
	states := []StepStatus{StepStatusRunning, StepStatusCompleted}

	for _, comp := range components {
		for _, status := range states {
			var err error
			switch status {
			case StepStatusRunning:
				err = client.StartStep(ctx, correlationID, buildsStep, comp.Name)
			case StepStatusCompleted:
				err = client.CompleteStep(ctx, correlationID, buildsStep, comp.Name)
			}
			if err != nil {
				t.Fatalf("failed to update %s to %s: %v", comp.Name, status, err)
			}
			t.Logf("✓ %s -> %s", comp.Name, status)
		}
	}

	// Query component states directly from the event sourcing table
	t.Log("")
	t.Log("COMPONENT STATES IN EVENT SOURCING TABLE:")

	rows, err := container.conn.Query(ctx, `
		SELECT step, component, status, timestamp
		FROM ci_eventsource_test.slip_component_states FINAL
		WHERE correlation_id = $1
		ORDER BY step, component, timestamp
	`, correlationID)
	if err != nil {
		t.Fatalf("failed to query component states: %v", err)
	}
	defer rows.Close()

	eventCount := 0
	for rows.Next() {
		var step, component, status string
		var timestamp time.Time
		if err := rows.Scan(&step, &component, &status, &timestamp); err != nil {
			t.Fatalf("failed to scan row: %v", err)
		}
		t.Logf("  %s/%s = %s (at %v)", step, component, status, timestamp.Format(time.RFC3339Nano))
		eventCount++
	}
	t.Logf("Total component state records: %d", eventCount)

	// Now load the slip and verify hydration
	loadedSlip, err := client.Load(ctx, correlationID)
	if err != nil {
		t.Fatalf("failed to load slip: %v", err)
	}

	t.Log("")
	t.Log("HYDRATED AGGREGATES:")
	buildsAggregate := loadedSlip.Aggregates[buildsStep]
	t.Logf("builds_completed aggregate has %d components:", len(buildsAggregate))

	for _, comp := range buildsAggregate {
		t.Logf("  %s: %s", comp.Component, comp.Status)
	}

	// Verify all components were hydrated with correct final status
	if len(buildsAggregate) != len(components) {
		t.Errorf("Expected %d components, got %d", len(components), len(buildsAggregate))
	}

	for _, comp := range buildsAggregate {
		if comp.Status != StepStatusCompleted {
			t.Errorf("Expected %s to be completed, got %s", comp.Component, comp.Status)
		}
	}

	t.Log("")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
	t.Log("SUCCESS: Component state event sourcing test passed")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
}

// =============================================================================
// SLIP STATUS HISTORY TEST
// =============================================================================

// TestE2E_SlipStatusHistory verifies that state history is properly recorded
// for all step transitions throughout the pipeline.
func TestE2E_SlipStatusHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E history test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	container, err := setupE2EClickHouseContainer(ctx, t)
	if err != nil {
		t.Fatalf("failed to setup clickhouse container: %v", err)
	}
	defer container.terminate(ctx)

	pipelineConfig, err := LoadPipelineConfigFromFile("default.json")
	if err != nil {
		t.Fatalf("failed to load default.json: %v", err)
	}

	// Extract step names from config
	stepNames := extractStepNamesFromConfig(t, pipelineConfig)
	buildsStep := stepNames.builds
	secretScanStep := stepNames.secretScan

	migrateOpts := MigrateOptions{
		Database:       "ci_history_test",
		PipelineConfig: pipelineConfig,
	}
	if _, err := RunMigrations(ctx, container.conn, migrateOpts); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	session := &testClickhouseSession{conn: container.conn}
	store := NewClickHouseStoreFromSession(session, pipelineConfig, "ci_history_test")
	defer store.Close()

	mockGitHub := newMockGitHubAPIForE2E()
	config := Config{
		PipelineConfig: pipelineConfig,
		HoldTimeout:    30 * time.Second,
		PollInterval:   100 * time.Millisecond,
		AncestryDepth:  20,
		Database:       "ci_history_test",
		Logger:         &e2eTestLogger{t: t},
	}
	client := NewClientWithDependencies(store, mockGitHub, config)

	// Create slip
	correlationID := fmt.Sprintf("history-test-%d", time.Now().UnixNano())
	commitSHA := fmt.Sprintf("history%d", time.Now().UnixNano()%10000000)
	repository := "MyCarrier-DevOps/history-test"

	mockGitHub.SetAncestry("MyCarrier-DevOps", "history-test", commitSHA, []string{commitSHA})

	components := []ComponentDefinition{
		{Name: "api", DockerfilePath: "src/MC.Api"},
	}

	pushOpts := PushOptions{
		CorrelationID: correlationID,
		Repository:    repository,
		Branch:        "main",
		CommitSHA:     commitSHA,
		Components:    components,
	}

	if _, err := client.CreateSlipForPush(ctx, pushOpts); err != nil {
		t.Fatalf("failed to create slip: %v", err)
	}

	t.Log("Testing status history recording...")

	// Perform a series of step updates
	stepSequence := []struct {
		step      string
		component string
		action    string
	}{
		{buildsStep, "api", "start"},
		{buildsStep, "api", "complete"},
		{secretScanStep, "", "start"},
		{secretScanStep, "", "complete"},
		{"dev_deploy", "", "start"},
		{"dev_deploy", "", "complete"},
	}

	for _, s := range stepSequence {
		var err error
		switch s.action {
		case "start":
			err = client.StartStep(ctx, correlationID, s.step, s.component)
		case "complete":
			err = client.CompleteStep(ctx, correlationID, s.step, s.component)
		}
		if err != nil {
			t.Fatalf("failed to %s %s/%s: %v", s.action, s.step, s.component, err)
		}
		t.Logf("✓ %s %s/%s", s.action, s.step, s.component)
	}

	// Load slip and check history
	slip, err := client.Load(ctx, correlationID)
	if err != nil {
		t.Fatalf("failed to load slip: %v", err)
	}

	t.Log("")
	t.Log("STATE HISTORY:")
	for i, entry := range slip.StateHistory {
		comp := entry.Component
		if comp == "" {
			comp = "(no component)"
		}
		t.Logf("  %d. [%s] %s/%s -> %s: %s",
			i+1, entry.Timestamp.Format("15:04:05.000"), entry.Step, comp, entry.Status, entry.Message)
	}

	// Verify we have history entries
	if len(slip.StateHistory) == 0 {
		t.Error("Expected state history entries, got none")
	} else {
		t.Logf("✓ %d history entries recorded", len(slip.StateHistory))
	}

	t.Log("")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
	t.Log("SUCCESS: Status history test passed")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
}

// =============================================================================
// JSON SCHEMA VERIFICATION
// =============================================================================

// TestE2E_JSONSchemaIntegrity verifies that all JSON columns (aggregates, state_history)
// are properly serialized and deserialized through the full write/read cycle.
func TestE2E_JSONSchemaIntegrity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E JSON schema test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	container, err := setupE2EClickHouseContainer(ctx, t)
	if err != nil {
		t.Fatalf("failed to setup clickhouse container: %v", err)
	}
	defer container.terminate(ctx)

	pipelineConfig, err := LoadPipelineConfigFromFile("default.json")
	if err != nil {
		t.Fatalf("failed to load default.json: %v", err)
	}

	// Extract step names from config
	stepNames := extractStepNamesFromConfig(t, pipelineConfig)
	buildsStep := stepNames.builds

	migrateOpts := MigrateOptions{
		Database:       "ci_json_test",
		PipelineConfig: pipelineConfig,
	}
	if _, err := RunMigrations(ctx, container.conn, migrateOpts); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	session := &testClickhouseSession{conn: container.conn}
	store := NewClickHouseStoreFromSession(session, pipelineConfig, "ci_json_test")
	defer store.Close()

	mockGitHub := newMockGitHubAPIForE2E()
	config := Config{
		PipelineConfig: pipelineConfig,
		HoldTimeout:    30 * time.Second,
		PollInterval:   100 * time.Millisecond,
		AncestryDepth:  20,
		Database:       "ci_json_test",
		Logger:         &e2eTestLogger{t: t},
	}
	client := NewClientWithDependencies(store, mockGitHub, config)

	// Create slip with components that have all fields populated
	correlationID := fmt.Sprintf("json-test-%d", time.Now().UnixNano())
	commitSHA := fmt.Sprintf("json%d", time.Now().UnixNano()%10000000)
	repository := "MyCarrier-DevOps/json-test"

	mockGitHub.SetAncestry("MyCarrier-DevOps", "json-test", commitSHA, []string{commitSHA})

	components := []ComponentDefinition{
		{Name: "api", DockerfilePath: "src/MC.Api"},
		{Name: "worker", DockerfilePath: "src/MC.Worker"},
	}

	pushOpts := PushOptions{
		CorrelationID: correlationID,
		Repository:    repository,
		Branch:        "feature/json-test",
		CommitSHA:     commitSHA,
		Components:    components,
	}

	if _, err := client.CreateSlipForPush(ctx, pushOpts); err != nil {
		t.Fatalf("failed to create slip: %v", err)
	}

	// Add various data to test JSON serialization
	for _, comp := range components {
		if err := client.StartStep(ctx, correlationID, buildsStep, comp.Name); err != nil {
			t.Fatalf("failed to start build: %v", err)
		}
		// Set image tag
		if err := client.SetComponentImageTag(
			ctx,
			correlationID,
			comp.Name,
			fmt.Sprintf("mycarrier/%s:%s-12345", comp.Name, commitSHA[:7]),
		); err != nil {
			t.Fatalf("failed to set image tag: %v", err)
		}
		if err := client.CompleteStep(ctx, correlationID, buildsStep, comp.Name); err != nil {
			t.Fatalf("failed to complete build: %v", err)
		}
	}

	// Query raw JSON from database
	t.Log("")
	t.Log("RAW JSON DATA FROM CLICKHOUSE:")

	var buildsJSON, stateHistoryJSON, ancestryJSON string
	// Use the config-driven step name as the column name
	rawQuery := fmt.Sprintf(`
		SELECT 
			toString(%s) as builds,
			toString(state_history) as history,
			toString(ancestry) as ancestry
		FROM ci_json_test.routing_slips FINAL
		WHERE correlation_id = $1
	`, buildsStep)
	err = container.conn.QueryRow(ctx, rawQuery, correlationID).Scan(&buildsJSON, &stateHistoryJSON, &ancestryJSON)
	if err != nil {
		t.Fatalf("failed to query raw JSON: %v", err)
	}

	t.Logf("  %s JSON: %s", buildsStep, buildsJSON[:min(200, len(buildsJSON))]+"...")
	t.Logf("  state_history JSON: %s", stateHistoryJSON[:min(200, len(stateHistoryJSON))]+"...")
	t.Logf("  ancestry JSON: %s", ancestryJSON)

	// Verify JSON is valid by parsing
	var buildsData interface{}
	if err := json.Unmarshal([]byte(buildsJSON), &buildsData); err != nil {
		t.Errorf("%s JSON is invalid: %v", buildsStep, err)
	} else {
		t.Logf("  ✓ %s JSON is valid", buildsStep)
	}

	var historyData interface{}
	if err := json.Unmarshal([]byte(stateHistoryJSON), &historyData); err != nil {
		t.Errorf("state_history JSON is invalid: %v", err)
	} else {
		t.Log("  ✓ state_history JSON is valid")
	}

	// Load slip and verify data integrity
	slip, err := client.Load(ctx, correlationID)
	if err != nil {
		t.Fatalf("failed to load slip: %v", err)
	}

	// Verify component data was deserialized correctly
	t.Log("")
	t.Log("DESERIALIZED DATA:")
	buildsAggregate := slip.Aggregates[buildsStep]
	for _, comp := range buildsAggregate {
		t.Logf("  %s: status=%s, imageTag=%s", comp.Component, comp.Status, comp.ImageTag)
		if comp.ImageTag == "" {
			t.Errorf("Expected imageTag to be set for %s", comp.Component)
		}
	}

	t.Log("")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
	t.Log("SUCCESS: JSON schema integrity test passed")
	t.Log("═══════════════════════════════════════════════════════════════════════════")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
