# Slippy AI Development Instructions

This document provides guidance for AI-assisted development of the slippy routing slip library and its integrations.

**State machine specification and validation instructions are in the repo root's `.github/`
(i.e. `<repo-root>/.github/`, not a `slippy/.github/`):**

| File | Purpose |
|------|---------|
| `.github/STATE_MACHINE_V3.md` | Full specification: pipeline phases, consistency invariants (I1–I4), algorithm reference, validation checklist, and automated test coverage |
| `.github/PROJECT_STATE.md` | Project history, known discrepancy table, and architectural decisions |

**Before making any changes to this package, read the repo root's `.github/STATE_MACHINE_V3.md` first.**

---

## Breaking changes

**DEVOPS-231 added `Repave` to the exported `SlipStore` interface:**

```go
Repave(ctx context.Context, oldCorrelationID string, newSlip *Slip, parent *AncestryEntry) error
```

This is a compile-breaking change for any downstream consumer with its own
`SlipStore` implementation — a `var _ slippy.SlipStore = (*fakeStore)(nil)` assertion
now fails with "missing method Repave" until that method is added. This was a
deliberate choice: a reviewer suggested a narrower optional interface asserted at the
call site instead, so existing implementers would not need to change; we rejected
that in favor of compile-time conformance for the operational store, and this repo
already has a documented process for exactly this situation — see the **Slippy Bump
Checklist** in slippy-api's `CLAUDE.md` ("Check if `slippy.SlipStore` interface
gained new methods — update `mockSlipStore`"). Since every module in this repo
releases at one shared version, bumping the dependency in a downstream consumer means
following that checklist, not working around the interface.

**Why `Repave` and not a plain `DeleteSlip`.** An earlier iteration of this work added
`DeleteSlip(ctx, correlationID, successorCorrelationID string) error` and left the push
path to call `Create` afterwards. That shape was never released, and it had a defect that
no amount of ordering could fix: once the delete committed, a failure in the following
`Create` left the commit with **no slip at all**, and the next Kafka redelivery found no
row to repave and failed identically — forever. The producible trigger is ordinary deploy
ordering: `slipColumns()` derives the INSERT column list from the pipeline config, so a
config deployed ahead of the migration that adds its step's `_status` column makes every
insert fail with Postgres 42703. `Repave` makes the replacement atomic, so that failure
rolls back instead of destroying the run. Implementations MUST provide that atomicity.

Folding the create into the store also closed four other defects structurally rather than
by documentation: the successor's row is inserted **before** any descendant is repointed
onto it (so no descendant can name a correlation ID that has no row — necessary but not
sufficient for a foreign key on `slip_ancestry.parent_correlation_id`, and Phase B adds
none; the full argument lives on `SlipStore.Repave` in `interfaces.go`); the superseded
run's own parent link is
carried forward when the caller resolved no ancestry, instead of being deleted and never
replaced; the successor's identity is a `*Slip` the store itself writes rather than a
caller-supplied ID string written into other slips' ancestry rows unvalidated; and the
descendant repoint now rewrites the whole denormalized parent snapshot alongside the id, so
a cross-branch repave no longer truncates `ResolveAncestry` at that hop. The column list is
deliberately not repeated here — it is in `PostgresStore.Repave` (`postgres_store_updates.go`),
beside the `UPDATE` that has to stay in step with it.

**Consumer-visible contract change: `CreateSlipResult.AncestryResolved`.** It used to be
computed as `len(slip.Ancestry) > 0` on the dedup paths, and no store hydrates `Slip.Ancestry`
on load in production — so in practice it was **always false** for every dedup. It now
describes this push's ancestry-resolution attempt, which means it is `true` on the reuse and
empty-run-guard paths (nothing needed resolving) and preserves the computed value everywhere
resolution actually ran. The new value is the correct one; the old formula was a bug. But
slippy-api forwards this field verbatim in its `POST /v1/slips` response and as a span
attribute, so **any dashboard or alert keyed on `ancestry_resolved` changes meaning across
this version bump** and should be checked. Note also that slippy-api computes the same
condemned `len(Ancestry) > 0` formula on one of its own paths, so the two dedup-reporting
sites will disagree until that is updated too.

**One behavioral reversal to be aware of:** a failed repave is now **fatal** to the push.
The pre-`Repave` code logged a failed delete as a warning and created the slip anyway.
That leniency only made sense while delete and create were separate calls; a failed
`Repave` writes nothing, so there is no successor to report. The push fails, Kafka
redelivers, and the redelivery converges because the superseded row is still there.

Callers may also now observe two sentinel errors from this path: `ErrSlipWentLive` (the
repave was rejected because the slip went live between the repave decision and the call —
nothing was written, and the successor was NOT created) and `ErrRepaveUnsupported` (the
store, e.g. `ClickHouseStore`, does not support repave and the caller should fall back to
abandon semantics, then create the successor separately). `Repave` can also return
`ErrDuplicateSlip` once Phase B's unique index exists. See `errors.go` for full contracts.

**DEVOPS-231 also removed the exported field `slippytest.MockStore.CommitIndex`.** The
published double no longer keeps a `"repo:sha" -> correlation_id` map; its four commit
lookups derive their answer from the stored rows instead (`rowsForCommit` plus `loadOrder`
or `findOrder`, depending on which store query the method mirrors). No consumer in this
workspace imports `slippytest` today, so nothing is known to break — but the field was
exported, and any caller that seeded `store.CommitIndex[...]` alongside `AddSlip` fails to
compile after this bump. The fix is to delete the line: seeding the slip is sufficient, and
always was.


### Not breaking: `PushOptions.Dispatch` (DEVOPS-264)

**DEVOPS-264 added `PushOptions.Dispatch` (`DispatchIntent`).** This one is *not* breaking:
the zero value, `DispatchIntentUnspecified`, preserves the previous behavior exactly, so an
un-updated caller is unaffected. Setting it is how a caller fixes the tests-only retrigger
hole — the empty-run guard used to infer "this push dispatches nothing" from
`len(Components) == 0`, which is wrong for a repo running unit tests without builds
(`buildable=false` + `RunUnitTests=true`), because pushhookparser nils out components
whenever builds are skipped while still dispatching unit tests. The guard then returned the
old slip, the caller read `returned != sent` as a duplicate, and suppressed everything
including the unit tests it wanted to re-run. Set `DispatchIntentSomething` when work will
dispatch, `DispatchIntentNothing` when it will not; see `DispatchIntent` for why component
count cannot answer this. The fix is only live once slippy-api and pushhookparser also pass
it through.

---


## Overview

**Slippy** is a Go library that provides **routing slip** functionality for CI/CD pipeline orchestration. It tracks pipeline executions across stages, components, and steps, enabling intelligent hold/proceed decisions based on prerequisite completion.

### Key Characteristics

- **Postgres (`PostgresStore`) is the operational slip store** (since DEVOPS-127). `ClickHouseStore`
  remains in the codebase and implements the same `SlipStore` interface, but is not the write path
  for production slips — e.g. `ClickHouseStore.Repave` (`clickhouse_store.go`) unconditionally
  returns an error wrapping the `ErrRepaveUnsupported` sentinel (see `errors.go`), signaling
  callers to fall back to abandon semantics instead of repave. ClickHouse has neither a
  delete path nor transactions, so it cannot offer `Repave`'s atomicity contract at all.
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

**Postgres is the operational slip store (see Key Characteristics above), but slippy does
NOT read Postgres connection settings itself.** `NewPostgresStore(pool, config, logger)`
takes an already-built `*pgxpool.Pool` — the caller constructs and injects it. The tables
below (`CLICKHOUSE_*`, `SLIPPY_*`) are consumed by `slippy.ConfigFromEnv()`/`NewClient`,
the ClickHouse-backed path; they say nothing about how a deployment provisions Postgres.
For that, see the sibling `goLibMyCarrier/postgres` module: it provides a
`POSTGRES_*`-prefixed env config (`PostgresLoadConfig`: `POSTGRES_HOSTNAME`,
`POSTGRES_USERNAME`, `POSTGRES_PASSWORD`, `POSTGRES_DATABASE`, `POSTGRES_PORT`,
`POSTGRES_SSLMODE`, plus pool/timeout tunables) and a pooled session helper
(`session.go`), mirroring this package's `clickhouse` config shape — the designed
counterpart for building the pool a caller then passes to `NewPostgresStore`.

### Required for Slippy Operation (ClickHouse-backed `NewClient` path only)

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
    // Optional; unset keeps the legacy len(Components) inference. Set it when
    // component count would be misleading — see slippy.DispatchIntent.
    Dispatch: slippy.DispatchIntentSomething, // or DispatchIntentNothing
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
// Update step status using correlation ID (componentName is "" for a pure pipeline step)
err := client.UpdateStepWithStatus(ctx, correlationID, "unit_tests", "", slippy.StepStatusCompleted, "unit tests passed")

// Wrappers around UpdateStepWithStatus for common transitions
err := client.CompleteStep(ctx, correlationID, "unit_tests", "")
err := client.FailStep(ctx, correlationID, "unit_tests", "", "assertion failure in TestFoo")
err := client.StartStep(ctx, correlationID, "unit_tests", "")

// Update component-specific status (componentName is the component, e.g. "api")
err := client.UpdateStepWithStatus(ctx, correlationID, "build", "api", slippy.StepStatusCompleted, "build succeeded")
```

### Prerequisite Checking

```go
result, err := client.CheckPrerequisites(ctx, slip, []string{"unit_tests"}, "")
switch result.Status {
case slippy.PrereqStatusCompleted: // All prereqs complete
case slippy.PrereqStatusRunning:   // Some prereqs still running
case slippy.PrereqStatusFailed:    // A prereq failed
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
├── status.go           # SlipStatus/StepStatus/PrereqStatus enums + predicates (IsTerminal, IsSuccess, IsFailure) only
├── aggregate_status.go # computeAggregateStatus (component -> aggregate rollup shared by both stores)
├── steps.go            # UpdateStepWithStatus + wrappers (CompleteStep, FailStep, StartStep, ...)
├── history.go          # AppendHistoryEntry (state history convenience wrapper)
├── executor.go         # RunPreExecution/RunPostExecution; checkPipelineCompletion (pipeline-status derivation, recovery)
├── prereqs.go          # CheckPrerequisites, holds
├── hold.go             # WaitForPrerequisites
├── migrations.go       # ClickHouse migration options/orchestration
├── dynamic_migrations.go # Pipeline-config-based ClickHouse migrations
├── schema_migrations.go # Versioned core schema migrations (table, materialized views)
├── pipeline_config.go  # Pipeline JSON parsing
├── clickhouse_store.go # ClickHouse SlipStore implementation (not the operational store; see Key Characteristics)
├── postgres_store.go   # PostgresStore type + pgxPool interface (the operational SlipStore, DEVOPS-127)
├── postgres_store_reads.go   # PostgresStore read methods (FindByCommits, LoadByCommit, ResolveAncestry, ...)
├── postgres_store_updates.go # PostgresStore write methods (Update, Repave, ...) + SlipStore conformance assertion
├── postgres_migrate.go   # Postgres schema-migration options and expected-table checks
├── postgres_migrations.go # PostgresDynamicMigrationManager (Postgres counterpart of DynamicMigrationManager)
├── github.go           # GitHub API implementation
├── errors.go           # Custom error types
├── columns.go          # Dynamic column generation
├── query_builder.go    # SQL query building
├── scanner.go          # Row scanning utilities
├── tracing.go          # OpenTelemetry span helpers
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
