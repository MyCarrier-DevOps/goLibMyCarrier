# Project State — goLibMyCarrier

> **Last Updated:** January 13, 2026  
> **Status:** Multi-module Go library with slippy schema ensurer architecture complete (branch: fix/slippy)

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
- See `slippy/CLAUDE.md` for detailed patterns

### ClickHouse Migrator Package
- **Versioned migrations** - one-time schema changes tracked in migrations table
- **Schema ensurers** - idempotent SQL that runs every time (for dynamic columns, indexes)
- **WithEnsurers() option** - pass ensurers to `CreateTables()` for runtime schema updates

### ClickHouse Package
- **Retry logic** with configurable intervals (default: 2s, 3s, 5s)
- **Optional defaults** - `CLICKHOUSE_PORT` defaults to "9440", `CLICKHOUSE_SKIP_VERIFY` to "false"
- **Viper-based config** - environment variable loading with SetDefault support

---

## Recent Changes

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

1. **Branch fix/slippy** - Schema ensurer architecture complete, ready for PR review
2. **Validation requirements** - All modules must pass `make fmt`, `make lint`, `make test`

---

## Architectural Decisions

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
- [ ] Some slippy functions at 0% coverage: `NewClickHouseStoreFromConfig`, `ValidateMinimal`, `PipelineConfig()`
- [ ] `NewClient` in slippy at 18.2% coverage - needs more test scenarios
- [ ] `chcol.JSON` mocking limitations - step_details timestamp parsing cannot be unit tested (requires integration tests)

---

## Next Steps (Not Yet Implemented)

### Immediate
- [ ] Create PR for fix/slippy branch if not already open
- [ ] Merge fix/slippy after CI passes
- [ ] Verify pkg.go.dev indexes new version correctly

### Future
- [ ] Integration tests for slippy with real ClickHouse (currently unit tests with mocks)
- [ ] CLI tooling for slippy operations (manual slip inspection, cleanup)
- [ ] Additional logger adapters beyond Zap
