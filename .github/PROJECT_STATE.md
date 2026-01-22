# Project State — goLibMyCarrier

> **Last Updated:** January 22, 2026  
> **Status:** Multi-module Go library with comprehensive E2E integration tests (branch: test/slippy)

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
| `github` | GitHub API client with App authentication | Updated - structured errors |
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
- **Event Sourcing for Component Updates** - uses high-throughput `ReplacingMergeTree` for component states
- See `slippy/CLAUDE.md` for detailed patterns

### Component State Event Sourcing (January 20, 2026)
- **Status:** Complete & Validated
- **Architecture:** Moved component status updates (highly concurrent) to `slip_component_states` table (`ReplacingMergeTree`)
- **Reasoning:** Eliminates lock contention/version conflicts on main `routing_slips` table during parallel build/test execution
- **Pattern:** 
  - Writes: Direct INSERTs (blind writes) - high throughput, no locking
  - Reads: `Load()` performs hydration by querying latest state for each component and overlaying on `Slip` object
- **Migration:** Version 4 `create_slip_component_states`
- **Validation:** Unit tests updated and passing (Mocking blind inserts + hydration query)
To resolve `ErrVersionConflict` during high-concurrency build fan-out (e.g., 50+ concurrent component updates), the data model was refactored:

**Problem:**
- Concurrent updates to the single `routing_slips` row (using `VersionedCollapsingMergeTree`) caused massive contention.
- Optimistic locking retries failed when dozens of components updated simultaneously.

**Solution:**
1.  **Split Storage Model**:
    *   **Main Slip**: Continues using `VersionedCollapsingMergeTree` in `routing_slips` for low-frequency updates (slip creation, pipeline step changes).
    *   **Component States**: New `slip_component_states` table using `ReplacingMergeTree`.
2.  **Lock-Free Writes**:
    *   Component updates (via `UpdateComponentStatus`) are now simple `INSERT`s into `slip_component_states`.
    *   No read-modify-write cycle, no optimistic locking, no retries needed.
3.  **Read-Side Hydration**:
    *   `Load()` fetches the base slip.
    *   `hydrateSlip` fetches the latest state for each component from `slip_component_states`.
    *   In-memory merging updates the `Slip.Aggregates` and recomputes aggregate step status.

### Ancestry Tracking (January 15, 2026)
Slips now track their complete commit ancestry chain, enabling:
- **Lineage preservation**: Each slip stores the full chain of ancestor commits in `Ancestry` JSON field
- **Ancestor resolution**: On new push, searches commit history to find existing slips and mark them abandoned
- **Progressive depth**: Starts with 25 commits, expands to 100 if no ancestor found (configurable)
- **Chain inheritance**: Child slips inherit parent's ancestry + parent's commit, building complete history
- **Single abandonment**: Only the most recent non-terminal ancestor is abandoned (not entire chain)
- **Squash merge promotion**: Feature branch slips are "promoted" (not abandoned) when merged via squash

**Squash Merge Handling:**
Squash merges break git ancestry (the merge commit has no git parent link to the feature branch). Slippy handles this by:
1. Parsing the commit message for PR number (e.g., `Add feature (#42)`)
2. Querying GitHub for the PR's head commit SHA
3. **Walking the PR head's commit ancestry** to find the most recent slip (handles cases where the final PR commit didn't create a slip)
4. Marking the feature branch slip as `promoted` (successful outcome, not abandoned)
5. Recording `PromotedTo` field for bidirectional linking

**Data Model:**
- [Slip struct with Ancestry and PromotedTo fields](slippy/types.go#L15-L50)
- [PushOptions with CommitMessage field](slippy/push.go#L25-L45)

**Slip Statuses:**
- `promoted` - Feature branch slip was successfully merged via PR (terminal, successful)
- `abandoned` - Slip was superseded by a newer push on same branch (terminal, unsuccessful)

**Flow:**
1. Push event arrives with commit list
2. `findAncestorSlipsWithProgressiveDepth()` searches for existing slips in commit history
3. If no ancestor found AND commit message contains PR#: try `findAncestorViaSquashMerge()`
4. If ancestor found via squash merge: **promote** the feature branch slip
5. If ancestor found via git history: **abandon** it (regular push superseding)
6. New slip created with inherited ancestry chain

**Edge Cases & Mitigations:**

| Edge Case | Impact | Mitigation | Status |
|-----------|---------|-----------|--------|
| **Force Push / Rebase** | Git parent links destroyed; ancestry chain breaks | • Logs warning when keywords detected ("force push", "rebase", "amend")<br/>• Progressive depth (100 commits) increases chance of finding ancestor<br/>• Documented limitation: ancestry may break | ⚠️ Partial |
| **Cherry-pick** | New commit with different SHA but same changes; ancestry not preserved | • Detects cherry-pick messages via regex<br/>• Logs warning when detected<br/>• Documented limitation: cherry-picked commits appear as new slips | ⚠️ Partial |
| **Interactive Rebase** | Commits rewritten with new SHAs; old ancestry invisible | • Same as force push (detected via keywords)<br/>• Ancestor resolution may fail completely | ⚠️ Partial |
| **Amended Commit** | Commit rewritten; slip exists for old SHA, not new SHA | • Detected via "amend" keyword in message<br/>• Logs warning<br/>• Old slip remains (may need manual cleanup) | ⚠️ Partial |
| **Forked Repository** | PR may reference fork's repo path (e.g., `user/repo` vs `MyCarrier-DevOps/repo`) | • `normalizeRepository()` stub reserved for future implementation<br/>• Currently returns repo path as-is | 🔴 Not implemented |
| **Multiple Squash Merges** | Nested PRs (feature→dev→main) have multiple PR numbers in message | • `extractAllPRNumbers()` extracts all PR numbers from message<br/>• `findAncestorViaSquashMerge()` tries each PR in order until slip found<br/>• Test coverage for nested merge scenarios | ✅ Implemented |
| **Manual Squash** | Developer manually squashes without GitHub PR; no PR number in message | • Falls back to git ancestry search<br/>• If feature branch not pushed, ancestry breaks<br/>• Documented limitation | 🔴 Unsolvable |
| **Deep History** | Ancestor slip >100 commits back | • `SLIPPY_ANCESTRY_MAX_DEPTH` configurable (default 100)<br/>• Can increase limit if needed<br/>• Documented limitation for very large merges | ⚠️ Configurable |
| **Cross-Repository** | Commits reference different repos (monorepo splits, migrations) | • `normalizeRepository()` stub reserved for future implementation<br/>• Currently ancestry only works within same repository | 🔴 Not implemented |

**New Helper Functions (January 15, 2026):**
- `extractAllPRNumbers(commitMessage)` - Extracts all unique PR numbers from message (supports nested PRs)
- `isCherryPick(commitMessage)` - Regex detection: `(?i)\b(cherry.pick|cherry-pick|picked from|backport)\b`
- `isForceOrRewrite(commitMessage)` - Keyword detection for "force push", "rebase", "amend"
- `normalizeRepository(repo)` - Reserved for future fork handling (currently returns as-is)
- `findAncestorViaSquashMerge()` - Enhanced to loop through all PR numbers until slip found

**Logging:**
When edge cases are detected, warnings are logged with context. See [resolveAndAbandonAncestorsWithWarnings](slippy/push.go#L200-L280) for implementation.

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

### January 16, 2026 — Comprehensive Error Handling (branch: feature/improved-error-handling)

**Problem:**
Ancestry tracking was not working. Root cause: GitHub App was not installed on the MyCarrier-Engineering organization. However, this was difficult to diagnose because:
1. Errors were being silently swallowed with `logger.Error()` calls
2. No actionable information about what was wrong
3. Shadow mode was being checked inside library functions instead of at call sites

**Solution:**
Complete error handling overhaul ensuring ALL errors are returned (never swallowed) with actionable messages. Shadow mode handling moved to call sites.

**New Files:**
- `github/errors.go` - Structured error types for GitHub API operations

**New Error Types (github package):**
- Sentinel errors: [ErrNoInstallation, ErrAuthenticationFailed](github/errors.go#L12-L20)
- [InstallationError](github/errors.go#L41-L80) - includes list of available orgs for actionable debugging
- [GraphQLError](github/errors.go#L85-L130) - detailed API error with query context

**New Error Types (slippy package):**
- Sentinel errors: [ErrAncestryResolutionFailed, ErrAncestorUpdateFailed, ErrHistoryAppendFailed, ErrSlipStatusUpdateFailed](slippy/errors.go#L38-L60)
- [AncestryError](slippy/errors.go#L171-L250) - phase-based error tracking with phases: "github_api", "slip_lookup", "abandon", "promote"

**New Return Type:**
- [CreateSlipResult](slippy/push.go#L117-L130) - replaces direct `*Slip` return, includes `Warnings []error` and `AncestryResolved bool`

**API Changes:**
| Function | Old Signature | New Signature |
|----------|---------------|---------------|
| `CreateSlipForPush` | `(*Slip, error)` | `(*CreateSlipResult, error)` |
| `checkPipelineCompletion` | `(bool, SlipStatus)` | `(bool, SlipStatus, error)` |
| `UpdateStepWithStatus` | Returns single error | Returns `ErrHistoryAppendFailed` + aggregate errors |
| `WaitForPrerequisites` | Swallowed HoldStep errors | Returns HoldStep errors |
| `RunPreExecution` | Swallowed StartStep errors | Returns StartStep errors |
| `ResolveResult` | No warnings | Added `Warnings []error` field |

**Key Principle:**
The slippy library now returns ALL errors. Shadow mode handling happens at the **call site**, not inside the library. See [IsShadowMode](slippy/config.go) for the toggle and the "Never Swallow Errors" architectural decision below for the pattern.

**Files Modified:**
- `github/errors.go` (NEW) - Structured error types
- `github/graphql.go` - `DiscoverInstallationID`, `GetCommitAncestry`, `GetPRHeadCommit` use new errors
- `slippy/errors.go` - Added new sentinel errors and `AncestryError` type
- `slippy/types.go` - Added `CreateSlipResult` struct
- `slippy/push.go` - `CreateSlipForPush` returns `CreateSlipResult`, `resolveAndAbandonAncestorsWithWarnings` collects warnings
- `slippy/executor.go` - `checkPipelineCompletion` returns 3 values, `RunPreExecution` returns StartStep errors
- `slippy/steps.go` - `UpdateStepWithStatus` returns history append and aggregate errors
- `slippy/hold.go` - `WaitForPrerequisites` returns HoldStep errors
- `slippy/resolve.go` - `ResolveResult` includes Warnings field

**Test Updates:**
All tests updated to match new signatures. Tests pass with coverage above 75% threshold.

**Current Status:**

---

### January 22, 2026 — hydrateSlip Bug Fix & E2E Integration Tests (branch: test/slippy)

**Problem:**
E2E integration tests revealed that step status updates were being overwritten. After completing a step (e.g., `unit_tests_completed`), reloading the slip showed the step still as `pending`.

**Root Cause:**
The `AppendHistory` function was calling `Load()` → modify → `Update()` separately from `UpdateStep`. This created a race condition:
1. `UpdateStep` loads slip, updates step to `completed`, saves (version N)
2. `AppendHistory` loads slip (gets version N with step=completed), appends history, saves (version N+1)
3. But with VersionedCollapsingMergeTree, `AppendHistory`'s `Load()` could read stale data, overwriting the step status

**Solution:**
Created `UpdateStepWithHistory` method that combines step update AND history append in a single atomic Load→modify→Update cycle.

**New Interface Method:**
- `SlipStore.UpdateStepWithHistory(ctx, correlationID, stepName, componentName, status, entry)` - atomic step + history update

**Files Modified:**
- `slippy/clickhouse_store.go`: Added `UpdateStepWithHistory` method (~100 lines), `updateAggregateStatusFromComponentStatesWithHistory` method
- `slippy/interfaces.go`: Added `UpdateStepWithHistory` to `SlipStore` interface
- `slippy/steps.go`: Changed `UpdateStepWithStatus` to use combined `UpdateStepWithHistory` instead of separate calls
- `slippy/mock_store_test.go`: Added `UpdateStepWithHistory` implementation
- `slippy/slippytest/mock_store.go`: Added `UpdateStepWithHistory` implementation
- `slippy/steps_test.go`: Updated test for new combined operation behavior

**Secondary Fix — VCMT Orphaned Cancel Rows:**
During testing, discovered that VersionedCollapsingMergeTree `FINAL` modifier can return multiple rows (including orphaned sign=-1 cancel rows) until background merges complete.

**Query Fix:**
All `Load` queries now filter `sign = 1` and `ORDER BY version DESC` to only select active rows:
- `clickhouse_store.go`: Modified `Load` and `LoadByCommit` queries
- `query_builder.go`: Updated `BuildFindByCommitsQuery` and `BuildFindAllByCommitsQuery`

**Additional Fixes:**
- `SetComponentImageTag` function: Fixed to use `c.pipelineConfig.GetAggregateStep("build")` instead of hardcoded `"builds"` key
- `steps_test.go`: Updated test data to use `"builds_completed"` aggregate key

**E2E Integration Tests:**
Comprehensive testcontainers-based E2E tests using real ClickHouse:

| Test | Description | Status |
|------|-------------|--------|
| `TestE2E_FullPipelineFlow` | Complete 12-step pipeline with all phases | ✅ PASS |
| `TestE2E_ConcurrentWriteStressTest` | 20 concurrent component updates | ✅ PASS |
| `TestE2E_PrerequisiteWaiting` | Hold/wait mechanism for prerequisites | ✅ PASS |
| `TestE2E_ReplacingMergeTreeCollapse` | 50 sequential updates verify row collapse | ✅ PASS |
| `TestE2E_ComponentStateEventSourcing` | Component state hydration from event table | ✅ PASS |
| `TestE2E_SlipStatusHistory` | State history recording verification | ✅ PASS |
| `TestE2E_JSONSchemaIntegrity` | JSON serialization roundtrip | ✅ PASS |

**Test Execution:**
```bash
go test -v -run "TestE2E_" -count=1 -timeout 10m -tags=integration
```

**Test Coverage:**
- Unit tests: 278 tests passing
- E2E integration tests: 7 tests passing
- All tests execute every step defined in `default.json` (12 steps including optional `package_artifact`)

**Current Status:**
- ✅ `make test PKG=slippy` - all pass
- ✅ E2E integration tests - all 7 pass
- ✅ Bug fix validated through comprehensive testing

### January 15, 2026 — SOLID/DRY Refactoring (branch: slippy/ancestry-tracking)

**Problem:**
Analysis revealed 12 SOLID and DRY principle violations in the slippy package, creating technical debt:
- Duplicate code patterns (PR extraction, timeout defaulting, terminal status checks)
- Helper functions mixed with business logic
- Inconsistent function ordering

**Solution:**
Applied DRY (Don't Repeat Yourself) principle to eliminate code duplication:

**DRY Improvements:**
1. **Duplicate PR Extraction (#6)** - Rewrote `extractPRNumber()` as thin wrapper around `extractAllPRNumbers()`, eliminating duplicate regex logic
2. **Duplicate Timeout Defaulting (#7)** - Created `applyHoldDefaults()` helper method used in `WaitForPrerequisites` and `RunPreExecution`, replacing 3 instances of identical logic
3. **Duplicate Terminal Checks (#10)** - Created `checkTerminalStatus()` helper method used in `AbandonSlip` and `PromoteSlip`, eliminating duplicate if-check and logging
4. **Error Wrapping (#11)** - Verified all error wrapping uses `fmt.Errorf` with `%w` consistently (already correct)

**Code Organization:**
- Moved unexported helper methods (`applyHoldDefaults`, `checkTerminalStatus`) after all exported methods to satisfy `funcorder` lint rule
- Named return values in `applyHoldDefaults` to satisfy `gocritic` and `nakedret` lint rules
- Added `//nolint:unused` for `extractPRNumber` (used in tests only)


### January 15, 2026 — Edge Case Handling & Multi-PR Support (branch: slippy/ancestry-tracking)

**Problem:**
Identified 9 critical edge cases that could break ancestry chain tracking:
1. Force pushes rewrite history
2. Cherry-picks create duplicate commits with different SHAs
3. Interactive rebases rewrite commit chains
4. Amended commits invalidate previous SHAs
5. Forked repositories have different repo paths
6. Multiple squash merges (nested PRs: feature→dev→main)
7. Manual squash operations lack PR numbers
8. Deep history (>100 commits between slips)
9. Cross-repository references

**Solution:**
Implemented comprehensive detection, mitigation, and fallback strategies for all tractable edge cases.

**New Helper Functions:**
- `extractAllPRNumbers(commitMessage)` - Extracts all unique PR numbers (handles nested merges)
- `isCherryPick(commitMessage)` - Regex detection: `(?i)\b(cherry.pick|cherry-pick|picked from|backport)\b`
- `isForceOrRewrite(commitMessage)` - Keyword detection for "force push", "rebase", "amend"
- `normalizeRepository(repo)` - Reserved for future fork handling (stub)

**Enhanced Logic:**
- `findAncestorViaSquashMerge()` - Now loops through all PR numbers in order, tries each until slip found (solves nested merge scenario)
- `resolveAndAbandonAncestors()` - Logs warnings when cherry-pick or force/rewrite patterns detected
- `findSlipsInPRBranchHistory()` - Walks PR head commit's ancestry (handles non-slip final commits)

**New Regex Patterns:**
- `cherryPickRegex`: `(?i)\b(cherry.pick|cherry-pick|picked from|backport)\b` - Case-insensitive cherry-pick detection

**Test Coverage:**
- `TestExtractAllPRNumbers` - Single PR, multiple PRs, duplicates, nested merges
- `TestIsCherryPick` - Various cherry-pick message formats with case insensitivity
- "tries multiple PR numbers for nested merges" - Validates fallback: PR #100 fails → PR #90 succeeds

**Warning Logging:**
When edge cases detected, warnings are logged. See [resolveAndAbandonAncestorsWithWarnings](slippy/push.go#L200-L280) for implementation.

**Documentation:**
Added comprehensive "Edge Cases & Mitigations" table to PROJECT_STATE.md documenting:
- 9 identified edge cases with impact analysis
- Mitigation strategies for each
- Implementation status (✅ Implemented / ⚠️ Partial / 🔴 Not implemented)
- Known limitations and unsolvable scenarios

**Current Status:**
- ✅ `make lint PKG=slippy` - 0 issues
- ✅ `make test PKG=slippy` - all pass, 77.3% coverage
- ✅ All edge case mitigations implemented where tractable
- ✅ Documentation complete with limitations clearly stated

### January 15, 2026 — Squash Merge Promotion (branch: slippy/ancestry-tracking)

**Problem:** 
Squash merges break git ancestry - the merge commit has no parent link to the feature branch head commit. This meant feature branch slips couldn't be linked to the integration branch slip created after merge.

**Solution:** 
Parse PR number from commit message, query GitHub for PR head commit, find associated slip, and mark it as "promoted" rather than abandoned.

**New Status:**
- `SlipStatusPromoted` - Terminal state indicating feature branch was successfully merged via PR
- Different from `abandoned` (which indicates superseded, unsuccessful)

**New Fields:**
- `Slip.PromotedTo` - Correlation ID of the target slip (enables bidirectional linking)
- `PushOptions.CommitMessage` - Optional field to enable PR-based ancestry resolution

**New Interface Method:**
- `GitHubAPI.GetPRHeadCommit(ctx, owner, repo, prNumber)` - Retrieves PR's head commit SHA

**Changes:**
- `slippy/status.go`: Added `SlipStatusPromoted`, updated `IsTerminal()` to include it
- `slippy/types.go`: Added `PromotedTo` field to `Slip` struct
- `slippy/push.go`: Added `CommitMessage` to `PushOptions`, `extractPRNumber()`, `findAncestorViaSquashMerge()`, `findSlipsInPRBranchHistory()`, updated `resolveAndAbandonAncestors()` to handle promotion vs abandonment
- `slippy/client.go`: Added `PromoteSlip()` method (similar to `AbandonSlip()`)
- `slippy/interfaces.go`: Added `GetPRHeadCommit` to `GitHubAPI` interface
- `github/graphql.go`: Implemented `GetPRHeadCommit()` GraphQL query
- `slippy/mock_github_test.go`: Added `GetPRHeadCommit` support, `PRHeadCommits` map, `SetPRHeadCommit()` helper
- `slippy/slippytest/mock_github.go`: Mirrored `GetPRHeadCommit` support for external test fixtures

**Test Coverage:**
- Added `TestExtractPRNumber` - PR number parsing from various commit message formats
- Added `TestClient_FindAncestorViaSquashMerge` - squash merge lookup scenarios, including walking ancestry when PR head isn't a slip commit
- Added `TestClient_PromoteSlip` - promotion logic and error handling
- Added `TestClient_CreateSlipForPush_SquashMergePromotion` - integration test for full flow

**Current Status:**
- ✅ `make lint PKG=slippy` - 0 issues
- ✅ `make test PKG=slippy` - all pass, 77.1% coverage

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

1. **E2E integration tests complete** - All 7 testcontainers-based tests pass, validating full pipeline flow
2. **hydrateSlip bug fixed** - Combined `UpdateStepWithHistory` prevents step status overwrites
3. **VCMT query fix** - `sign = 1` filter ensures correct row selection
4. **Next: Publish new version** - Tag and publish new goLibMyCarrier version with bug fixes and E2E tests

---

## Architectural Decisions

### Never Swallow Errors
**Decision:** Library functions must ALWAYS return errors. Never use `logger.Error()` and continue silently.
**Rationale:** Callers need full visibility to make informed decisions. Shadow mode handling belongs at call sites, not inside libraries.
**Implementation:**
- All functions return errors they encounter
- Use structured error types with actionable messages
- Collect non-fatal errors as warnings in result structs
- Call sites decide whether to block or continue based on shadow mode
**Anti-pattern:** `logger.Errorf("something failed: %v", err)` without returning the error.

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
**Implementation:** See [FindAllByCommits](slippy/clickhouse_store.go#L273-L320) for the pattern.
**Anti-pattern:** Using `_ = rows.Close()` or `//nolint:errcheck` to suppress lint warnings.

### Two Migration Types (Versioned + Ensurers)
**Decision:** Use versioned migrations for core schema, idempotent ensurers for dynamic columns.
**Rationale:** Adding a step to pipeline config shouldn't require version bump; step columns are order-independent.
**Implementation:**
- Core schema (v1: base table, v2: materialized view) - versioned, tracked in migrations table
- Step columns and indexes - ensurers, run every `CreateTables()` call
- Ensurers use `IF NOT EXISTS` to be idempotent
- Consumers must always call `RunMigrations()` (not just when version mismatches)

### Update Existing Functions, Never Create New Versions
**Decision:** When changing a function's signature or behavior, update the existing function rather than creating a new function with a modified name (e.g., `FooWithWarnings`, `FooV2`).
**Rationale:** Creating new function versions leads to:
- Code duplication and maintenance burden
- Wrapper functions that just call the new version
- Confusion about which function to use
- Deprecated functions that linger in the codebase
**Implementation:**
- Modify the existing function signature directly
- Update all call sites and tests to use the new signature
- Use result structs (e.g., `CreateSlipResult`) to add return values without breaking existing callers
- Never create `FooWithWarnings`, `FooV2`, `FooNew` variants
**Anti-pattern:** Creating `resolveAndAbandonAncestorsWithWarnings` instead of updating `resolveAndAbandonAncestors`.

### Atomic Step Updates with History
**Decision:** Combine step status updates and history appends into a single atomic operation.
**Rationale:** Separate `UpdateStep` and `AppendHistory` calls create race conditions where `AppendHistory`'s `Load()` can read stale data and overwrite the step status change.
**Implementation:**
- `UpdateStepWithHistory` method performs Load→modify step→modify history→Update in one cycle
- Single version increment ensures both changes are applied atomically
- Used by `UpdateStepWithStatus` in `steps.go`
**Anti-pattern:** Calling `UpdateStep` followed by `AppendHistory` separately.

### VCMT Query Pattern with sign=1 Filter
**Decision:** Always filter `sign = 1` and `ORDER BY version DESC` when querying VersionedCollapsingMergeTree tables.
**Rationale:** VCMT's `FINAL` modifier can return multiple rows (including orphaned sign=-1 cancel rows) until background merges complete. The `sign = 1` filter reliably selects only active rows.
**Implementation:**
- `Load`: `WHERE correlation_id = ? AND sign = 1 ORDER BY version DESC LIMIT 1`
- `FindByCommits`: `WHERE ... AND s.sign = 1 ORDER BY c.priority ASC, s.version DESC`
**Anti-pattern:** Relying solely on `FINAL` or `LIMIT 1` without `sign = 1` filter.

---

## Technical Debt / Known Issues

- [ ] `slippytest` package shows 0% coverage (test fixture, expected)
- [ ] `NewClickHouseStoreFromConfig` at 0% coverage - requires real ClickHouse connection
- [ ] `NewClient` in slippy at 18.2% coverage - requires real ClickHouse/GitHub connections
- [ ] `chcol.JSON` mocking limitations - step_details timestamp parsing cannot be unit tested (requires integration tests)

---

## Next Steps (Not Yet Implemented)

### Immediate
- [ ] Publish new goLibMyCarrier version with bug fixes and E2E tests
- [ ] Update pushhookparser to use new `CreateSlipResult` return type
- [ ] Run full repo-wide `make lint` and `make test`

### Future
- [x] ~~Integration tests for slippy with real ClickHouse~~ - **Completed January 22, 2026**
- [ ] CLI tooling for slippy operations (manual slip inspection, cleanup)
- [ ] Additional logger adapters beyond Zap
- [ ] Implement `normalizeRepository()` for fork handling
