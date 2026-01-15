# Project State — goLibMyCarrier

> **Last Updated:** January 15, 2026  
> **Status:** Multi-module Go library with ancestry tracking (branch: slippy/ancestry-tracking)

---

## Overview

goLibMyCarrier is a **multi-module Go monorepo** providing reusable infrastructure libraries for MyCarrier DevOps. Each subdirectory is an independent Go module with unified versioning via CI.

### Key Characteristics
- **Multi-module architecture** - each package has its own `go.mod`
- **Unified versioning** - all modules share same version (e.g., `v1.3.43`)
- **75% test coverage threshold** enforced per module
- **ClickHouse-backed persistence** for slippy routing slips

---

## Implemented Systems

### Core Packages
| Package | Purpose | Status |
|---------|---------|--------|
| `clickhouse` | ClickHouse client with retry logic, session management | Stable |
| `clickhousemigrator` | Schema migration framework | Stable |
| `slippy` | Routing slip orchestration for CI/CD pipelines | Active development |
| `logger` | Zap-based structured logging | Stable |
| `kafka` | Kafka producer/consumer utilities | Stable |
| `github` | GitHub API client with App authentication | Stable |
| `vault` | HashiCorp Vault integration | Stable |
| `auth` | Gin authentication middleware | Stable |
| `otel` | OpenTelemetry instrumentation | Stable |
| `argocdclient` | ArgoCD application/manifest client | Stable |
| `yaml` | YAML utilities | Stable |

### Slippy Package (Most Complex)
- **Shadow mode** (`SLIPPY_SHADOW_MODE`) - controls blocking vs non-blocking errors
- **Dynamic schema** - generated from JSON pipeline configuration
- **Two migration types** - versioned migrations for core schema + idempotent ensurers for dynamic columns
- **Config error capture** - stores load errors for proper surfacing in validation
- **ClickHouse store** - slip persistence with query builders and scanners
- **Native JSON handling** - uses `chcol.JSON` for ClickHouse JSON columns
- **Ancestry tracking** - maintains full commit lineage chain in `Ancestry` JSON field
- **Progressive depth ancestry search** - starts at 25 commits, expands to 100 if no ancestor found
- **Ancestry inheritance** - child slips inherit parent's ancestry chain for complete lineage
- See `slippy/CLAUDE.md` for detailed patterns

### Ancestry Tracking (January 15, 2026)
Slips now track their complete commit ancestry chain, enabling:
- **Lineage preservation**: Each slip stores the full chain of ancestor commits in `Ancestry` JSON field
- **Ancestor resolution**: On new push, searches commit history to find existing slips and mark them abandoned
- **Progressive depth**: Starts with 25 commits, expands to 100 if no ancestor found (configurable)
- **Chain inheritance**: Child slips inherit parent's ancestry + parent's commit, building complete history
- **Single abandonment**: Only the most recent non-terminal ancestor is abandoned (not entire chain)

**Data Model:**
```go
type Slip struct {
    // ... other fields
    Ancestry chcol.JSON[[]string] `ch:"ancestry"` // JSON array of commit SHAs
}
```

**Flow:**
1. Push event arrives with commit list
2. `findAncestorSlipsWithProgressiveDepth()` searches for existing slips in commit history
3. If ancestor found: inherit its `Ancestry` chain + add ancestor's commit SHA
4. Abandon the most recent non-terminal ancestor slip
5. New slip created with inherited ancestry chain

### ClickHouse Migrator Package
- **Versioned migrations** - one-time schema changes tracked in migrations table
- **Schema ensurers** - idempotent SQL that runs every time (for dynamic columns, indexes)
- **WithEnsurers() option** - pass ensurers to `CreateTables()` for runtime schema updates

### ClickHouse Package
- **Retry logic** with configurable intervals (default: 2s, 3s, 5s)
- **Optional defaults** - `CLICKHOUSE_PORT` defaults to "9440", `CLICKHOUSE_SKIP_VERIFY` to "false"
- **Isolated Viper instance** - uses `viper.New()` to avoid global state pollution

---

## Recent Changes

### January 15, 2026 — Ancestry Tracking & Progressive Depth Search (branch: slippy/ancestry-tracking)

**Problem:** 
1. No way to track full commit lineage across slip generations
2. Large pushes with many commits between slips (>25) caused ancestor resolution to fail

**Solution:** 
1. Added `Ancestry` JSON field to store complete commit chain; child slips inherit parent's ancestry
2. Implemented progressive depth search - starts at `AncestryDepth` (default 25), expands to `AncestryMaxDepth` (default 100) if no ancestor found

**New Configuration:**
- `SLIPPY_ANCESTRY_DEPTH`: Initial search depth (default: 25)
- `SLIPPY_ANCESTRY_MAX_DEPTH`: Maximum depth for progressive expansion (default: 100)

**Changes:**
- `slippy/config.go`: Added `AncestryMaxDepth` field with env var loading and validation
- `slippy/push.go`: New `findAncestorSlipsWithProgressiveDepth()` function, refactored `resolveAndAbandonAncestors()`
- `slippy/clickhouse_store.go`: Fixed `FindAllByCommits` to use named returns for proper `rows.Close()` error handling
- `slippy/client.go`: Combined parameter types in `AbandonSlip` per lint requirements
- `slippy/mock_store_test.go`: Added `FindAllByCommitsError`, `FindAllByCommitsCalls`, ancestry deep copy
- `slippy/slippytest/mock_store.go`: Mirrored `FindAllByCommits` support for external test fixtures

**New Features:**
- **Ancestry inheritance**: Child slips inherit parent's ancestry chain for full lineage preservation
- **Only most recent abandoned**: Only the first (most recent) non-terminal ancestor is abandoned
- **Failed step tracking**: Records which step failed in ancestor slip for debugging

**Test Coverage:**
- Added comprehensive tests for `resolveAndAbandonAncestors` (was 24.1%)
- Added comprehensive tests for `findAncestorSlipsWithProgressiveDepth` (was 47.8%)
- Added tests for `AbandonSlip`, `ValidateMinimal`, `WithPipelineConfig`, `WithDatabase`
- Coverage improved from 63% → **76.6%** (exceeds 75% threshold)

**Current Status:**
- ✅ `make lint PKG=slippy` - 0 issues
- ✅ `make test PKG=slippy` - all pass, 76.6% coverage

### January 14, 2026 — Isolated Viper Instances (branch: fix/slippy)

**Problem:** Global viper state (`SetEnvPrefix`) caused cross-package interference. When `pushhookparser` loaded slippy config (which loaded ClickHouse config with `CLICKHOUSE` prefix), later calls to `viper.GetString("PAYLOAD")` looked for `CLICKHOUSE_PAYLOAD` instead of `PAYLOAD`.

**Solution:** Refactored config loading to use isolated viper instances (`viper.New()`) instead of the global viper singleton.

**Changes:**
- `clickhouse/clickhouse.go`: `ClickhouseLoadConfig()` now uses `v := viper.New()` instead of global `viper.SetEnvPrefix()`
- Consumer applications (pushhookparser) can safely use `os.Getenv()` for simple env vars without prefix concerns

**Architectural Decision:** See "Isolated Viper Instances" in Architectural Decisions section below.

### January 13, 2026 — Schema Ensurer Architecture (branch: fix/slippy)

**Two Migration Types Pattern:**
- `clickhousemigrator/interfaces.go`: Added `SchemaEnsurer` struct (Name, Description, SQL)
- `clickhousemigrator/migrator.go`: Added `ensurers` field, `WithEnsurers()` option, `runEnsurers()` method
- Versioned migrations run once (tracked in migrations table), ensurers run every time
- **Rationale:** Adding a new step to pipeline config shouldn't require manual version bumps

**Slippy Ensurer Refactoring:**
- `slippy/dynamic_migrations.go`: Replaced step migrations with `GenerateEnsurers()`, `generateStepColumnEnsurer()`, `generateIndexEnsurer()`
- `slippy/schema_migrations.go`: Added `GetDynamicEnsurers()`, `GetDynamicMigrationVersion()` now returns 2 (core schema only)
- `slippy/migrations.go`: `RunMigrations()` and `ValidateSchema()` use `WithEnsurers(ensurers)`
- Core schema: v1 (base table), v2 (materialized view) - versioned migrations
- Dynamic columns: step status columns, indexes - idempotent ensurers

**Scanner Updates:**
- `slippy/scanner.go`: Added `mergeStepDetailsFromJSON()` using `chcol.JSON.NestedMap()`
- Removed old `mergeStepDetails()` string-based function
- Uses `chcol.ExtractJSONPathAs[T]()` for state history and aggregates

**Test Fixes:**
- `slippy/clickhouse_store_unit_test.go`: Updated mocks to use `chcol.JSON.Scan()` 
- Added `chcol` import and proper data wrapper structures
- Fixed test expectations for graceful JSON error handling
- `slippy/config_test.go`: Fixed errcheck violations (added `_ =` to `os.Setenv` calls)

**Consumer Updates:**
- `pushhookparser/pkg/slippy/init.go`: Always calls `RunMigrations()` so ensurers run
- `slippytest/main.go`: Always calls `RunMigrations()` for consistent behavior

**Current Status:**
- ✅ `go fmt` passes
- ✅ `golangci-lint` reports 0 issues  
- ✅ All tests pass
- ✅ 75.7% statement coverage

### January 13, 2026 — ClickHouse JSON Type Fixes (branch: fix/slippy)

**ClickHouse JSON Column Handling:**
- `slippy/dynamic_migrations.go`: Fixed JSON column types for ClickHouse compatibility
  - Arrays must be wrapped in objects (`{"entries":[]}`, `{"items":[]}`)
  - ClickHouse JSON type only supports objects at root level
  - Multi-statement ALTER TABLE not supported - use comma-separated ADD COLUMN
  - Materialized view uses `dynamicElement(state_history.entries, 'Array(JSON)')` to extract array from Dynamic type for ARRAY JOIN

**Native JSON Scanning:**
- `slippy/scanner.go`: Use `chcol.JSON` type from clickhouse-go driver
  - Import `github.com/ClickHouse/clickhouse-go/v2/lib/chcol`
  - Use `chcol.NewJSON()` for scan destinations
  - Use `chcol.ExtractJSONPathAs[T]()` to extract nested data

**Data Wrapping:**
- `slippy/clickhouse_store.go`: Wrap `StateHistory` array in `{"entries": [...]}` on insert
- `slippy/query_builder.go`: Wrap aggregate arrays in `{"items": [...]}` on insert

**Type Fixes:**
- `slippy/dynamic_migrations.go`: Changed `DynamicMigration.Version` from `int` to `uint32`
  - ClickHouse `UInt32` column requires exact Go type match
  - Updated `storedVersions` map to use `uint32` keys

### January 13, 2026 — Config Error Handling & CI Fixes (PR #21)

**Config Error Capture Pattern:**
- `slippy/config.go`: Added `clickhouseLoadErr` and `pipelineLoadErr` fields to Config
- Errors captured on first load attempt, surfaced in `Validate()` with context
- Removed "retry on validation" anti-pattern

**ClickHouse Optional Defaults:**
- `clickhouse/clickhouse.go`: Added `viper.SetDefault()` for port and skip_verify
- Tests verified in `clickhouse_test.go`

**CI Workflow Fixes:**
- `.github/workflows/ci.yml`: Main library tag now created before submodule tags
- Added pkg.go.dev automatic refresh after release
- Root `go.mod` and `goLibMyCarrier.go` added to make root module valid

**Documentation:**
- `.github/copilot-instructions.md`: Created with project-specific patterns
- `.github/instructions/go.instructions.md`: Referenced for Go coding standards

---

## Current Focus

1. **Branch slippy/ancestry-tracking** - Ancestry tracking with progressive depth search complete, ready for testing
2. **Validation requirements** - All modules must pass `make fmt`, `make lint`, `make test`
3. **Next: Extend pushhookparser** - Update pushhookparser slippy integration with new functionality

---

## Architectural Decisions

### Never Bypass Linting Rules
**Decision:** Never use `//nolint` directives, `_ =` to ignore return values, or any mechanism to circumvent lint errors without explicit user permission.
**Rationale:** Lint rules exist for code quality. Bypassing them hides real issues and creates technical debt. The proper fix is always to resolve the underlying problem.
**Implementation:** When a lint error is encountered, fix the code properly. If a rule seems genuinely wrong for the situation, discuss with user before bypassing.
**Anti-pattern:** Adding `//nolint:errcheck` or `_ = someFunc()` to silence warnings.

### Isolated Viper Instances
**Decision:** Use `viper.New()` for isolated instances instead of the global viper singleton when loading package-specific configuration.
**Rationale:** Viper's global functions (`SetEnvPrefix`, `AutomaticEnv`, etc.) modify shared state. When multiple packages set different prefixes, the last one wins, causing other packages to fail to read their environment variables. For example, `CLICKHOUSE` prefix causes `viper.GetString("PAYLOAD")` to look for `CLICKHOUSE_PAYLOAD`.
**Implementation:**
- Library packages (e.g., `clickhouse`, `slippy`) use `v := viper.New()` and call all methods on `v`
- Consumer applications can use either isolated instances or `os.Getenv()` for simple unprefixed vars
- Never call `viper.SetEnvPrefix()` on the global instance in library code
**Anti-pattern:** Using global `viper.SetEnvPrefix()` in library packages that may be imported by applications using viper for other purposes.

### ClickHouse JSON Column Pattern
**Decision:** Use native JSON type with arrays wrapped in objects. Never use String type to store complex data.
**Rationale:** ClickHouse JSON type only supports objects at root; arrays need wrapper. String storage loses type safety, query optimization, and ClickHouse's native JSON functions.
**Implementation:**
- Store: `{"entries": [...]}` for state_history, `{"items": [...]}` for aggregates
- Read: Use `chcol.ExtractJSONPathAs[T](jsonCol, "entries")` to unwrap
- Queries: Use dot notation (`state_history.entries`) with `ARRAY JOIN` for materialized views
- Cast Dynamic to String: Use `entry.field::String` syntax when accessing nested fields
- Extract Array from Dynamic: Use `dynamicElement(column, 'Array(JSON)')` when ARRAY JOIN needs an array from JSON column
- **Anti-pattern:** Never use `toString(column)` or `String DEFAULT '[]'` for JSON data

### Error Handling Pattern
**Decision:** Capture config load errors on first attempt, don't retry in validation.
**Rationale:** Retrying is wasteful; errors should be surfaced with full context immediately.
**Implementation:** Private error fields in Config struct, checked in Validate().

### Shadow Mode Pattern
**Decision:** Single toggle (`SLIPPY_SHADOW_MODE`) controls all blocking behavior.
**Rationale:** Cleaner than "WithGracefulFallback" wrappers; single source of truth.
**Implementation:** `handleError()` checks `IsShadowMode()` - returns nil (log only) or propagates error.

### Multi-Module Versioning
**Decision:** All modules share same version number (root + submodules).
**Rationale:** Simplifies dependency management for consumers; ensures compatibility.
**Implementation:** CI creates root tag first, then submodule tags with same version.

### Proper Error Handling for rows.Close()
**Decision:** Use named return values with deferred close to properly handle `rows.Close()` errors.
**Rationale:** Never use `_ = rows.Close()` to bypass errcheck. Named returns allow capturing close errors properly.
**Implementation:**
```go
func FindAllByCommits(...) (results []SlipWithCommit, err error) {
    rows, err := db.Query(...)
    if err != nil { return nil, err }
    defer func() {
        closeErr := rows.Close()
        if err == nil && closeErr != nil {
            err = fmt.Errorf("failed to close rows: %w", closeErr)
        }
    }()
    // ... scan rows ...
    return results, nil
}
```
**Anti-pattern:** Using `_ = rows.Close()` or `//nolint:errcheck` to suppress lint warnings.

### Two Migration Types (Versioned + Ensurers)
**Decision:** Use versioned migrations for core schema, idempotent ensurers for dynamic columns.
**Rationale:** Adding a step to pipeline config shouldn't require version bump; step columns are order-independent.
**Implementation:**
- Core schema (v1: base table, v2: materialized view) - versioned, tracked in migrations table
- Step columns and indexes - ensurers, run every `CreateTables()` call
- Ensurers use `IF NOT EXISTS` to be idempotent
- Consumers must always call `RunMigrations()` (not just when version mismatches)

---

## Technical Debt / Known Issues

- [ ] `slippytest` package shows 0% coverage (test fixture, expected)
- [ ] `NewClickHouseStoreFromConfig` at 0% coverage - requires real ClickHouse connection
- [ ] `NewClient` in slippy at 18.2% coverage - requires real ClickHouse/GitHub connections
- [ ] `chcol.JSON` mocking limitations - step_details timestamp parsing cannot be unit tested (requires integration tests)

---

## Next Steps (Not Yet Implemented)

### Immediate
- [ ] Extend pushhookparser with new progressive depth ancestry support
- [ ] Run full repo-wide `make lint` and `make test` 
- [ ] Create PR for slippy/ancestry-tracking branch
- [ ] Merge after CI passes

### Future
- [ ] Integration tests for slippy with real ClickHouse (currently unit tests with mocks)
- [ ] CLI tooling for slippy operations (manual slip inspection, cleanup)
- [ ] Additional logger adapters beyond Zap
