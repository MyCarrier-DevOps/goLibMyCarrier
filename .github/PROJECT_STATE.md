# Project State — goLibMyCarrier

> **Last Updated:** January 13, 2026  
> **Status:** Multi-module Go library with CI fixes in progress (PR #21)

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
- **Config error capture** - stores load errors for proper surfacing in validation
- **ClickHouse store** - slip persistence with query builders and scanners
- See `slippy/CLAUDE.md` for detailed patterns

### ClickHouse Package
- **Retry logic** with configurable intervals (default: 2s, 3s, 5s)
- **Optional defaults** - `CLICKHOUSE_PORT` defaults to "9440", `CLICKHOUSE_SKIP_VERIFY` to "false"
- **Viper-based config** - environment variable loading with SetDefault support

---

## Recent Changes

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

1. **PR #21 (fix/slippy)** - Complete CI fixes and config improvements
2. **Validation requirements** - All modules must pass `make fmt`, `make lint`, `make test`

---

## Architectural Decisions

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

---

## Technical Debt / Known Issues

- [ ] `slippytest` package shows 0% coverage (test fixture, expected)
- [ ] Some slippy functions at 0% coverage: `NewClickHouseStoreFromConfig`, `ValidateMinimal`, `PipelineConfig()`
- [ ] `NewClient` in slippy at 18.2% coverage - needs more test scenarios

---

## Next Steps (Not Yet Implemented)

### Immediate
- [ ] Merge PR #21 after CI passes
- [ ] Verify pkg.go.dev indexes new version correctly

### Future
- [ ] Integration tests for slippy with real ClickHouse (currently unit tests with mocks)
- [ ] CLI tooling for slippy operations (manual slip inspection, cleanup)
- [ ] Additional logger adapters beyond Zap
