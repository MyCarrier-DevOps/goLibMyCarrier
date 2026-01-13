# Slippy AI Development Instructions

This document provides guidance for AI-assisted development of the slippy routing slip library and its integrations.

---

## Overview

**Slippy** is a Go library that provides **routing slip** functionality for CI/CD pipeline orchestration. It tracks pipeline executions across stages, components, and steps, enabling intelligent hold/proceed decisions based on prerequisite completion.

### Key Characteristics

- **ClickHouse-backed persistence** for high-performance queries
- **Dynamic schema** generated from JSON pipeline configuration
- **Pre-job/Post-job execution model** - bookend operations around existing jobs (does NOT wrap job execution)
- **Correlation ID** is the single canonical identifier for a slip throughout its lifecycle
- **Shadow mode** for gradual rollout without affecting pipelines

---

## Critical Design Patterns

### 1. Shadow Mode Controls Blocking Behavior

**`SLIPPY_SHADOW_MODE` is the single toggle that determines whether errors are blocking or non-blocking.**

```go
// PATTERN: Shadow mode error handling
func handleError(logger Logger, err error) error {
    if IsShadowMode() {
        logger.Errorf("Operation failed (shadow mode - continuing): %v", err)
        return nil  // Non-blocking: log and continue
    }
    return err  // Blocking: return error to caller
}
```

**Rules:**
- **Shadow mode ON (`SLIPPY_SHADOW_MODE=true`)**: Errors are logged but do NOT propagate. Operations return `nil` error.
- **Shadow mode OFF**: Errors are returned to the caller. In production, slippy is critical and SHOULD block if operations fail.

**DO NOT** create separate "WithGracefulFallback" wrapper functions. Shadow mode replaces this pattern entirely.

### 2. Validate Schema Before Migrations

Always check the current schema version before running migrations:

```go
// PATTERN: Validate-first migration logic
currentVersion, err := slippy.GetCurrentSchemaVersion(ctx, conn, database)
if err != nil {
    // Schema version table may not exist yet - expected on first run
    currentVersion = 0
}

targetVersion := slippy.GetDynamicMigrationVersion(pipelineConfig)

if currentVersion < targetVersion {
    // Only run migrations if schema is outdated
    result, err := slippy.RunMigrations(ctx, conn, opts)
    if err != nil {
        return handleError(logger, err)
    }
} else {
    logger.Info("Schema validation passed, no migrations needed")
}
```

### 3. Client Initialization Pattern

Initialize the slippy client early in the application lifecycle, with shadow mode controlling error handling:

```go
// PATTERN: Client initialization with shadow mode
func InitializeSlippy(ctx context.Context, logger Logger) (*slippy.Client, error) {
    if !IsSlippyEnabled() {
        logger.Info("Slippy disabled (SLIPPY_PIPELINE_CONFIG not set)")
        return nil, nil  // Disabled is not an error
    }

    cfg := slippy.ConfigFromEnv()
    
    if err := cfg.Validate(); err != nil {
        return handleInitError(logger, err)  // Shadow mode determines blocking
    }

    // ... validation and migration logic ...

    client, err := slippy.NewClient(cfg)
    if err != nil {
        return handleInitError(logger, err)
    }

    return client, nil
}
```

### 4. Nil Client Safety

All slip operations must handle nil client gracefully:

```go
// PATTERN: Nil client check
func CreateSlipIfNeeded(ctx context.Context, client *slippy.Client, ...) error {
    if client == nil {
        logger.Debug("Slippy client not initialized, skipping")
        return nil  // Not an error - slippy may be disabled
    }
    // ... proceed with operation
}
```

### 5. Avoid Import Cycles

When integrating slippy into other packages, create local data structs rather than importing types that might create cycles:

```go
// PATTERN: Local data struct to avoid import cycles
// In pkg/slippy/slip.go (integration package)
type SlipPushData struct {
    HeadCommitSha  string
    Organization   string
    RepositoryName string
    Branch         string
}

// Instead of importing parser.PushParserData which would create a cycle
```

---

## Environment Variables

### Required for Slippy Operation

| Variable | Description | Example |
|----------|-------------|---------|
| `CLICKHOUSE_HOSTNAME` | ClickHouse host | `clickhouse.example.com` |
| `CLICKHOUSE_PORT` | ClickHouse port | `9440` |
| `CLICKHOUSE_USERNAME` | ClickHouse user | `slippy` |
| `CLICKHOUSE_PASSWORD` | ClickHouse password | `***` |
| `CLICKHOUSE_DATABASE` | ClickHouse database | `ci` |
| `SLIPPY_PIPELINE_CONFIG` | Pipeline JSON (path or raw) | `/config/pipeline.json` |
| `SLIPPY_GITHUB_APP_ID` | GitHub App ID | `12345` |
| `SLIPPY_GITHUB_APP_PRIVATE_KEY` | Private key (PEM or path) | `/secrets/key.pem` |

### Optional

| Variable | Description | Default |
|----------|-------------|---------|
| `SLIPPY_SHADOW_MODE` | Enable shadow mode | `false` |
| `SLIPPY_DATABASE` | Database name | `ci` |
| `SLIPPY_HOLD_TIMEOUT` | Max wait time | `60m` |
| `SLIPPY_POLL_INTERVAL` | Prereq check interval | `60s` |
| `SLIPPY_ANCESTRY_DEPTH` | Commits to check | `20` |
| `CLICKHOUSE_SKIP_VERIFY` | Skip TLS verification | `false` |
| `SLIPPY_GITHUB_ENTERPRISE_URL` | GHE base URL | (github.com) |

### Enabling Slippy

Slippy is enabled when `SLIPPY_PIPELINE_CONFIG` is set. If not set, slippy operations return nil without error.

---

## Core APIs

### Client Creation

```go
// From environment
cfg := slippy.ConfigFromEnv()
client, err := slippy.NewClient(cfg)

// For testing with mocks
client := slippy.NewClientWithDependencies(mockStore, mockGitHub, config)
```

### Slip Creation (Push Events)

```go
slip, err := client.CreateSlipForPush(ctx, slippy.PushOptions{
    CorrelationID: correlationID,  // Links to Kafka events, logs, etc.
    Repository:    "owner/repo",
    Branch:        "main",
    CommitSHA:     "abc123...",
    Components: []slippy.ComponentDefinition{
        {Name: "api", DockerfilePath: "src/MC.Api"},
        {Name: "worker", DockerfilePath: "src/MC.Worker"},
    },
})
```

### Slip Resolution (Pre-Job)

```go
// Resolve slip from context (commit SHA, ancestry, or image tag)
result, err := client.ResolveSlip(ctx, slippy.ResolveOptions{
    Repository: "owner/repo",
    Ref:        commitSHA,
})
correlationID := result.Slip.CorrelationID
```

### Step Updates (Post-Job)

```go
// Update step status using correlation ID
err := client.UpdateStepStatus(ctx, correlationID, "unit_tests", slippy.StepStatusSuccess)

// Update component-specific status
err := client.UpdateComponentStatus(ctx, correlationID, "api", "build", slippy.StepStatusSuccess)
```

### Prerequisite Checking

```go
result, err := client.CheckPrerequisites(ctx, correlationID, "deploy_dev")
switch result.Status {
case slippy.PrereqStatusReady:    // All prereqs complete
case slippy.PrereqStatusWaiting:  // Some prereqs still running
case slippy.PrereqStatusFailed:   // A prereq failed
}
```

---

## Testing Patterns

### Unit Tests

Use the mock implementations in `mock_store_test.go` and `mock_github_test.go`:

```go
func TestMyFunction(t *testing.T) {
    store := NewMockStore()
    github := NewMockGitHub()
    client := slippy.NewClientWithDependencies(store, github, config)
    
    // Test with mocks
}
```

### Shadow Mode Tests

Always test both shadow mode ON and OFF:

```go
func TestOperation_ShadowModeOn(t *testing.T) {
    os.Setenv("SLIPPY_SHADOW_MODE", "true")
    defer os.Unsetenv("SLIPPY_SHADOW_MODE")
    
    err := operationThatMightFail()
    assert.NoError(t, err)  // Shadow mode swallows errors
}

func TestOperation_ShadowModeOff(t *testing.T) {
    os.Unsetenv("SLIPPY_SHADOW_MODE")
    
    err := operationThatMightFail()
    assert.Error(t, err)  // Production mode returns errors
}
```

### Test File Naming

Test files follow the pattern `z_*_test.go` or `*_test.go`. The `z_` prefix is used for integration and complex tests that should run after unit tests.

---

## File Structure

```
slippy/
├── client.go           # Main client entry point
├── config.go           # Configuration and env loading
├── types.go            # Core types (Slip, Step, etc.)
├── interfaces.go       # SlipStore, GitHubAPI interfaces
├── push.go             # CreateSlipForPush
├── resolve.go          # ResolveSlip (ancestry resolution)
├── status.go           # UpdateStepStatus, UpdateComponentStatus
├── prereqs.go          # CheckPrerequisites, holds
├── hold.go             # HoldForPrerequisites
├── migrations.go       # Schema migrations
├── dynamic_migrations.go # Pipeline-config-based migrations
├── pipeline_config.go  # Pipeline JSON parsing
├── clickhouse_store.go # ClickHouse SlipStore implementation
├── github.go           # GitHub API implementation
├── errors.go           # Custom error types
├── columns.go          # Dynamic column generation
├── query_builder.go    # SQL query building
├── scanner.go          # Row scanning utilities
├── utils.go            # Helper functions
├── logger.go           # Logger interface adapter
├── slippytest/         # Test utilities package
└── *_test.go           # Test files
```

---

## Integration Pattern (for consuming packages)

When integrating slippy into a service (like pushhookparser), create a local `pkg/slippy/` package:

```
myservice/
└── pkg/
    └── slippy/
        ├── config.go   # IsSlippyEnabled(), IsShadowMode()
        ├── init.go     # InitializeSlippyDatabase()
        └── slip.go     # CreateSlipIfNeeded() with local data types
```

### Integration Package Structure

**config.go** - Environment checks:
```go
func IsSlippyEnabled() bool {
    return os.Getenv("SLIPPY_PIPELINE_CONFIG") != ""
}

func IsShadowMode() bool {
    return os.Getenv("SLIPPY_SHADOW_MODE") == "true"
}
```

**init.go** - Database/client initialization with shadow mode error handling

**slip.go** - Local data structs and slip creation logic with shadow mode error handling

---

## Common Mistakes to Avoid

1. **❌ Creating "WithGracefulFallback" wrappers** - Use shadow mode instead
2. **❌ Hardcoding blocking/non-blocking behavior** - Let shadow mode control it
3. **❌ Skipping nil client checks** - Client may be nil if slippy is disabled
4. **❌ Running migrations without version check** - Always validate schema first
5. **❌ Importing types that create cycles** - Create local data structs
6. **❌ Treating disabled slippy as an error** - Return nil, nil when disabled
7. **❌ Forgetting to defer client.Close()** - Always clean up resources

---

## Pipeline Configuration

Slippy uses a JSON configuration to define pipeline steps. The schema is dynamic - columns are generated based on the config:

```json
{
  "version": "1.0",
  "name": "MyCarrier CI Pipeline",
  "steps": [
    {
      "name": "push_parsed",
      "description": "Push event received and parsed"
    },
    {
      "name": "build",
      "description": "Container image build",
      "aggregates": "component_builds",
      "prerequisites": ["push_parsed"]
    },
    {
      "name": "unit_tests",
      "description": "Unit test execution",
      "aggregates": "component_unit_tests",
      "prerequisites": ["build"],
      "is_gate": true
    },
    {
      "name": "deploy_dev",
      "description": "Deploy to dev environment",
      "prerequisites": ["unit_tests"]
    }
  ]
}
```

### Step Configuration Fields

- `name`: Unique identifier (becomes column name)
- `description`: Human-readable description
- `prerequisites`: Steps that must complete first
- `aggregates`: Component-level step this aggregates (creates JSON column)
- `is_gate`: If true, failure blocks all subsequent steps

---

## Commit Messages

Follow conventional commits:
- `feat: Add new slippy feature`
- `fix: Resolve shadow mode issue`
- `test: Add coverage for prereq checking`
- `refactor: Simplify error handling`
- `docs: Update README with new API`

---

## Questions to Ask

When implementing new slippy functionality, consider:

1. **Should this operation be blocking in production?** If yes, use the shadow mode pattern.
2. **Does this need to handle nil client?** Almost always yes.
3. **Could this create an import cycle?** If referencing types from other packages.
4. **Is there a schema change?** May need migration updates.
5. **Does this affect the correlation ID flow?** Keep it as the single identifier.
