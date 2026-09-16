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

**DEVOPS-367 added `ClaimSlip`, `ReleaseClaim` and `ProbeSchema` to the exported
`SlipStore` interface:**

```go
ClaimSlip(ctx context.Context, correlationID string, expected []SlipStatus, claimedBy, reason string) (ClaimOutcome, error)
ReleaseClaim(ctx context.Context, correlationID, releasedBy, reason string) (ReleaseOutcome, error)
ProbeSchema(ctx context.Context) error
```

Same posture as `Repave` below: a downstream `SlipStore` implementation fails to compile
until all three methods exist (the ClickHouse store returns `ErrClaimUnsupported` from the
first two and `nil` from `ProbeSchema`, having no schema of its own; the
`slippytest.MockStore` and slippy-api's `mockSlipStore` implement them). The claim is a
**flag**: `ClaimSlip` never writes `status`, and `ReleaseClaim` clears the claim only when
no step or component is running or held — otherwise it returns
`ReleaseOutcome{Released: false}` with nothing written, which callers treat as information.
`ErrRunInFlight` is **removed**: work in flight is an outcome, not an error, because all but
the last of a run's N post-job releases take that arm.
`ClaimSlip` returns `ClaimOutcome{Claimed, Prior}` for the same reason and with the same
shape: `Claimed=false` means a claim was already held and NOTHING was written — the
idempotent repeat every pre-job after the first takes — and `Prior` is then the recorded
`claimed_from` rather than the current status. Both values mean the slip is claimed on
return; only a caller that must not duplicate work reads `Claimed`.
`expected` is a **compare-and-set on the slip's CURRENT status, whether or not a claim is
already held** — the idempotent repeat sits BEHIND that check, not in front of it. A nil
`expected` admits any status except a live one (`in_progress`, `compensating`; `pending` is
claimable by design), and a claim already recorded on the row is no exemption from that
refusal. This is what stops a second rerun request dispatching on top of a pipeline the
first one already started, while still letting a retry through in the only window where a
retry should dispatch: the one where nothing ran, and the status therefore has not moved
(PR #87, round 6 — an earlier round compared `expected` against the recorded prior instead,
which reopened the double dispatch). `ProbeSchema` is on the interface so
the readiness gate is reachable through the abstraction consumers hold (`Client.ProbeSchema`
wraps it). Implementers should route their decisions through `slippy.DecideClaim` and
`slippy.DecideRelease` so they cannot drift from the store.
`ReleaseMarker(status SlipStatus, releasedBy, reason string)` changed shape in the same
release (the earlier `restored` argument is gone: a release never restores anything). The
contract, including what `nil` means for `expected`, is on `SlipStore.ClaimSlip`,
`SlipStore.ReleaseClaim` and `SlipStore.ProbeSchema` in `interfaces.go`; the model is in
`.github/STATE_MACHINE_V3.md` under DEVOPS-367.

Exactly ONE write path ends a claim: `UpdateSlipStatus` on a terminal status. `Create` and
the full-row `Update` never touch `claimed_from`, whatever status the caller's snapshot
carries, so a Load-then-Update can no longer clear a claim taken after its read;
`PromoteSlip` goes through the atomic status write for that reason. The claim protects work
that is **in flight** — running or held steps and components. The gap between one step's
last post-job and the next step's pre-job is not covered; that is tracked as **DEVOPS-371**
(a dispatcher-held claim).

**A claim with nothing in flight is reapable, by two routes.** The state with no post-job to
end it is a claim taken by a pre-job whose workflow was then never dispatched: `claimed_from`
is set, no step was ever reported, `RunInFlight` is false so a release WOULD clear it — but no
post-job will ever run to call one, and every later same-commit push deduplicates onto the row.

1. **Operator, and it covers every case:** `POST /v1/slips/{id}/release`. Nothing is in flight,
   so the first call clears it; there is no stuck step to resolve first.
2. **Automatic, and it is narrow:** pushhookparser's stranded-slip cleanup, which used to skip
   any claimed slip and now exempts one only while a step or component is running or held.
   Its claim gate sits *after* its live-status gate and its `failed` carve-out, so what it
   actually reaps is a claimed, quiescent slip at `pending`, `in_progress` or `compensating`,
   for a commit a force-push or branch delete made unreachable, on the slip's own branch, with
   `SLIPPY_STRANDED_CLEANUP` armed (off by default). A claimed quiescent `failed` slip — the
   rerunner's usual adoption — and a claimed terminal one both return at earlier gates and are
   never reaped by it; they are route 1's cases.

Do **not** add a time-based sweeper to this library for any of it: a long build is
indistinguishable from a wedge by elapsed time, which is why the exemption reads in-flight
evidence instead (DEVOPS-367, PR #87 finding 3).

When a claim is left behind by a run that will never release it, the **stuck step holds it**:
resolve that step (`POST /v1/slips/{id}/steps/{step}/complete`, or fail or skip it), then
`POST /v1/slips/{id}/release`, which then finds nothing in flight. On a non-terminal slip
`POST /v1/slips/{id}/abandon` also ends it (`AbandonSlip` writes the terminal `abandoned`
through `UpdateSlipStatus`); on an already-terminal slip it is a deliberate no-op (I4) and
clears nothing, so use the step-then-release route there.

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
sufficient for a foreign key on `slip_ancestry.parent_correlation_id`, and migration v5 adds
none; the full argument lives on `SlipStore.Repave` in `interfaces.go`); the superseded
run's own parent link is
carried forward when the caller resolved no ancestry, instead of being deleted and never
replaced; the successor's identity is a `*Slip` the store itself writes rather than a
caller-supplied ID string written into other slips' ancestry rows unvalidated; and the
descendant repoint now rewrites the whole denormalized parent snapshot alongside the id, so
a cross-branch repave no longer truncates `ResolveAncestry` at that hop. The column list is
deliberately not repeated here — it is on `SlipStore.Repave` in `interfaces.go`, the contract
every store implementation owes.

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
`ErrDuplicateSlip` once migration v5's unique index is applied. See `errors.go` for full contracts.

**Migration v5 (`one_slip_per_commit`) has a hard, per-environment precondition.** The
version that carries it adds `uq_routing_slips_repo_sha` on `(lower(repository), commit_sha)`
plus `ON DELETE CASCADE` FKs from `slip_component_states` and `slip_ancestry` on
`correlation_id`. `ADD CONSTRAINT` validates existing rows and a unique index build fails on
duplicates, so on a database that still holds more than one `routing_slips` row for one
commit, or orphan child rows, the migrator fails loudly and its per-migration transaction
rolls v5 back. That is deliberate — do not weaken v5 to get past it. Before bumping a
consumer to that version in ANY environment, both of these must already be true there:

1. The consumer release carrying the repave code (slippy-api on goLibMyCarrier ≥ v1.3.100)
   is deployed. The unique index must never be live while a pre-repave slippy-api runs: its
   old failed-path (`AbandonSlip` + insert) made a second row per commit and would 23505-fail
   every same-commit retrigger.
2. The one-time cleanup script `DEVOPS-231-cleanup-one-row-per-commit.sql` has run against
   that environment's database. It is an operator script kept outside this repo (the
   DEVOPS-127 convention: the migrator Job is schema-only), runs in one transaction, and
   RAISEs — rolling back — unless it ends with exactly the state v5 requires.

slippy-migrator's default `target-version` is latest, so v5 applies automatically on the
first deploy after the bump, and `target-version` cannot hold an environment below it: the
migrator runs `CreateTables` to latest before honouring any lower target
(`postgres_migrate.go`), so a lower value still attempts v5 and still fails. In an environment
whose cleanup has not run, the observable is a crash-looping pre-deploy Job with the recorded
version stuck at 4 — the version INSERT rides in the same transaction as the DDL — and the
config-driven ensurers never reached, so new step columns and indexes do not land either. The
recovery is to run the cleanup, or to pin the consumer to a pre-v5 goLibMyCarrier.

v5 is also idempotent by name but asserted by shape: `duplicate_object` is swallowed and the
index is `IF NOT EXISTS` so a repeated migrator run is a no-op, and each half then RAISEs unless
the object it kept has exactly the expected definition (cascade FKs, `convalidated`; a UNIQUE
valid index on `(lower(repository), commit_sha)`). A pre-existing same-named object of another
shape — including a leftover from a hand-run `CREATE INDEX CONCURRENTLY` — fails v5 loudly; drop
it and re-run. Do not pre-create the index or the FKs by hand.

**DEVOPS-231 also removed the exported field `slippytest.MockStore.CommitIndex`.** The
published double no longer keeps a `"repo:sha" -> correlation_id` map; its four commit
lookups derive their answer from the stored rows instead (`rowsForCommit` plus `loadOrder`
or `findOrder`, depending on which store query the method mirrors). No consumer in this
workspace imports `slippytest` today, so nothing is known to break — but the field was
exported, and any caller that seeded `store.CommitIndex[...]` alongside `AddSlip` fails to
compile after this bump. Usually the fix is to delete the line.

**The lookups also answer differently now, with no compile error to point at it.** A fixture
holding more than one row for one `(repository, commit_sha)` used to get whichever was seeded
last, and now gets the store's own ordering; and a row written straight into the exported
`Slips` map used to be invisible to all four lookups and now participates in every one. So if a
fixture deliberately pointed a commit at a row other than the newest, deleting the line does
**not** preserve its behaviour — seed distinct `UpdatedAt` values instead.


### Not breaking: `PushOptions.Dispatch` (DEVOPS-264)

**DEVOPS-264 added `PushOptions.Dispatch` (`DispatchIntent`).** This one is *not* breaking:
the zero value, `DispatchIntentUnspecified`, preserves the previous behavior exactly, so an
un-updated caller is unaffected. Setting it closes the rest of the tests-only retrigger
hole: the `failed` carve-out already covers a prior run that FAILED, which is the case
"the tests-only retrigger hole" names elsewhere in this repo, so what `Dispatch` adds is
the same repo re-pushing a commit whose prior run ended `completed`, `abandoned`,
`promoted` or `compensated`. The mechanism is the same in both — the empty-run guard used
to infer "this push dispatches nothing" from
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

**Migration v6 (`claimed_from`, DEVOPS-367) rollout order.** Every Postgres read path
selects `claimed_from` (`slipSelectColumns()`), so a library at or past v1.4.0 fails
every `Load` with Postgres 42703 against a database still at v5. The migrator Job must
have applied v6 before any slippy-api pod on that library serves traffic; do not roll
the API image ahead of the migrator. This library supplies the check, but the ordering is
enforced by the consumer that runs it — slippy-api's startup probe, not the library itself:
`PostgresStore.ProbeSchema` compares `slipSelectColumns()` (every column the SELECTs name,
`claimed_from` and each configured step's column alike) against the live schema, and
slippy-api calls it at startup and fails readiness — the pod exits and restarts — for as
long as it returns `ErrSchemaBehind`. An API wired that way crash-loops until v6 is applied
instead of answering every slip request with a 42703; a consumer that never calls the probe
gets no such protection. `ProbeSchema` is on `SlipStore` (and `Client`), so it is reachable
through the abstraction rather than only on the concrete Postgres store.
Rolling back is guarded: v6's DownSQL refuses while any slip holds a claim
(`claimed_from` set), because dropping the column erases the in-flight flag of every held
claim — that run's work becomes repaveable mid-flight — and breaks every `Load` until the
library is rolled back with it. The refusal names up to 20 of the held correlation ids, and
the guard takes an `ACCESS EXCLUSIVE` lock (bounded by a 5s `lock_timeout`, since the
migration transaction otherwise runs with `lock_timeout = 0`) before it counts, so a claim
taken between the count and the drop cannot slip through.
Because every slip-routed pre-job now claims, some slip usually holds a claim in a busy
environment, so plan a rollback as a drain: expect the down to refuse until runs finish or
are released. Let the runs end and `ReleaseClaim` them; where a step is stuck, resolve that
step (`POST /v1/slips/{id}/steps/{step}/complete`) and then release. A NON-terminal slip can
also be ended with `POST /v1/slips/{id}/abandon`; an already-terminal one ignores that call
(I4) and keeps its claim, so use the step-then-release route there. Then re-run the down.

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
    Dispatch: dispatchIntentForThisPush, // MUST be computed per push, never a constant
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
