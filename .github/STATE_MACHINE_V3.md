# Slippy Pipeline - High-Level State Machine

**Purpose:** full-pipeline view - how stages progress, what blocks, what recovers, what terminates.
**Source config:** `production.json`

---

## Statuses

**Slip statuses:** `in_progress`, `failed`, `completed`, `abandoned`, `promoted`.
- `abandoned` / `promoted`: pipeline-terminal, bypass `checkPipelineCompletion` (see Pipeline termination note below).

**Step statuses:** `pending`, `held`, `running`, `completed`, `skipped`, `failed`, `error`, `timeout`, `aborted`.
- `completed` / `skipped`: terminal-success.
- `failed` / `error` / `timeout`: terminal primary failure.
- `aborted`: terminal-cascade (upstream prereq failed). **Reversible** — auto-reset to `pending` by recovery branch in `checkPipelineCompletion` when last primary failure resolves (`executor.go:307-346`).
- Full table: see Step Status Reference below.

**Glossary:**
- *Primary failure* — step in `{failed, error, timeout}`. Drives `slip.status=failed`.
- *Cascade abort* — step in `aborted` because prereq failed. Does NOT drive `slip.status=failed`.
- *Aggregate step* — rollup of N components (e.g. `builds`). Status derived from component states.
- *Pure pipeline step* — `componentName == ""` (e.g. `unit_tests`, `dev_deploy`).
- *Event log* — `slip_component_states` table. Append-only. Authoritative source of step status history. argMax-derived view is the truth.
- *Materialized step columns* — `routing_slips.<step>_status` columns. Cached projection from event log. Must always match argMax-derived status (per I5).

## Rules

### Identity
- Slip route unique per correlation id.

### Topology
- Step dependencies defined by pipeline config (`production.json`).
- Initial steps (no prereqs): `builds`, `unit_tests`, `secret_scan`, `package_artifact`.
- Final step: `prod_steady_state`.
- All initial steps run in parallel for same correlation id. Build components run in parallel within `builds` aggregate.
- Downstream steps run per config-defined prereqs (see Pipeline Flow diagram).

### Lifecycle
- Slippy CLI `-pre` (`StartStep` / `WaitForPrerequisites`) and `-post` (`RunPostExecution`) drive every step transition. No direct `routing_slips` writes outside this path.
- Step terminal status propagates to `slip.status` via `checkPipelineCompletion`:
  - Any primary failure → `slip=failed`.
  - **FailStep(X) scope:** only `X.status → failed` and `slip.status → failed`. No other step rows modified synchronously. Downstream steps self-abort lazily when each one calls `WaitForPrerequisites` and observes the failed prereq (`hold.go:83-110`).
  - All primary failures resolved AND `slip=failed` → `slip=in_progress`; cascade-aborted steps reset to `pending`.
  - `prod_steady_state=completed` AND zero primary failures → `slip=completed` (terminal, immutable).
- Aggregate `builds`: any single component primary failure → aggregate `failed` → `slip=failed`. Aggregate `completed` only when all components terminal-success.
- Recovery path per step: `failed → running → completed`. Slip recovery fires on terminal post-event of last unresolved primary failure.

---

## Consistency Invariants

A **discrepancy** is any condition where `slip.status` violates one of these invariants.
Full definition and known violations: see `PROJECT_STATE.md` (Technical Debt - Slippy State Machine Discrepancies). bd issue `goLibMyCarrier-nl3` covers a concrete I5 violation example (write-path stale column clone). bd issue `goLibMyCarrier-yix` is a related but distinct bug (decision-time staleness in `hydrateSlip` — separate from the I5 write-path issue).

| # | Invariant |
|---|-----------|
| **I1** | `slip=in_progress` while any step is a primary failure (`status ∈ {failed, error, timeout}`) → **violation** |
| **I2** | `slip=failed` with zero primary failures → **violation** |
| **I3** | `slip=completed` while any step is a primary failure OR `status = running` → **violation** |
| **I4** | `slip.status` change after `slip=completed` → **violation** (event log writes for further step events ARE allowed; only `slip.status` is immutable — see line 335) |
| **I5** | `routing_slips.<step>_status` column does not match event-log-derived status (via `argMax(status, timestamp) FROM slip_component_states GROUP BY step`) → **materialization violation** |

**Note on invariant scope:** I1–I4 are semantic correctness invariants (slip.status given step states). I5 is a **materialization consistency** invariant — the cached `routing_slips.<step>_status` columns must match the authoritative event log in `slip_component_states`. Divergence indicates a write-path bug, not a state-machine logic bug.

**Note on `aborted`:** cascade failures (`aborted`) are NOT primary failures — they do not independently drive `slip=failed` and are not counted in I1/I2/I3.
**Note on `pending`/`held`:** non-terminal, do not block any transition including completion (I3).
**Note on `skipped`:** terminal-success, treated as `completed`. Does not block I3.

**Pipeline termination without completing:**
- `abandoned` - automatic when a newer push supersedes this branch (`AbandonSlip`, `client.go:170`)
- `promoted` - automatic on PR squash-merge to another branch (`PromoteSlip`, `client.go:204`)
- No operator abort tool exists. Both bypass `checkPipelineCompletion`.

---

## Pipeline Flow

```
                          ┌─────────────────────────────────────────────────────────────┐
                          │                         INIT                                 │
                          │  slip created · slip.status=in_progress · builds=running    │
                          │  all others=pending · slip_component_states=empty            │
                          └──────────────────────────┬──────────────────────────────────┘
                                                     │ immediately
                                                     ▼
                          ┌─────────────────────────────────────────────────────────────┐
                          │                      CI_PARALLEL                             │
                          │  builds (×N components) · unit_tests · secret_scan          │
                          │  package_artifact  -  all running concurrently               │
                          │  slip.status = in_progress                                   │
                          └────┬──────────────────────────────────────────┬─────────────┘
                               │                                          │
                    builds     │                             any CI step  │
                    completes  │                             fails        │
                               │                                          ▼
                               │                               ┌─────────────────────┐
                               │                               │     CI_FAILED        │
                               │                               │  slip.status=failed  │
                               │                               │ downstream lazy-abort│
                               │                               └──────────┬──────────┘
                               │                                          │ re-run failed step
                               │                               ┌──────────┴──────────┐
                               │                               │      CI_RECOVERY     │
                               │                               │ (all CI steps pass)  │
                               │                               └──────────┬──────────┘
                               │                                          │
                               ▼                                          │
          ┌────────────────────────────────────────────────────────────────────────────┐
          │                         DEV + PREPROD PARALLEL                               │
          │                                                                              │
          │  builds done             ──►  dev_deploy=running         (prereq: builds)   │
          │  builds+tests+scan done  ──►  preprod_deploy=running     (prereq: builds, unit_tests, secret_scan )    │
          │                                                                              │
          │  Both run independently. dev_deploy failure does NOT block preprod_deploy (according to production.json).   │
          └───────────────────────────────────────────────────────────────────────────┬─┘
                                                                                       │
          ┌──────────────────────────────┐         ┌────────────────────────────────┐ │
          │       DEV TRACK              │         │       PREPROD TRACK            │ │
          │                              │         │                                │ │
          │  dev_deploy ─► dev_tests     │         │  preprod_deploy ─► preprod_    │ │
          │  (TestEngine PostSync ⚠️)    │         │  tests (TestEngine PostSync⚠️) │ │
          │                              │         │                                │ │
          │  Failure: slip=failed        │         │  Failure: slip=failed          │ │
          │  Does NOT block preprod      │         │  BLOCKS prod_gate              │ │
          └──────────────────────────────┘         └────────────────────┬───────────┘ │
                                                                         │             │
                                                   preprod_deploy=completed             │
                                                   preprod_tests=completed              │
                                                                         │             │
                                                                         ▼             │
                                                   ┌─────────────────────────────────┐ │
                                                   │          PROD_GATE               │ │
                                                   │  prod_gate running               │ │
                                                   │  slip.status = in_progress       │ │
                                                   │  is_gate=true: failure cascades  │ │
                                                   │  aborted to ALL prod steps       │ │
                                                   └──────────┬──────────────────────┘ │
                                                              │                         │
                                              gate passes     │    gate fails           │
                                                              │         │               │
                                                              │         ▼               │
                                                              │  ┌─────────────────┐   │
                                                              │  │  GATE_FAILED     │   │
                                                              │  │ slip=failed      │   │
                                                              │  │ prod_release_    │   │
                                                              │  │ created, prod_   │   │
                                                              │  │ deploy, prod_    │   │
                                                              │  │ tests, alert_    │   │
                                                              │  │ gate, rollback,  │   │
                                                              │  │ steady_state     │   │
                                                              │  │ self-abort lazy  │   │
                                                              │  └────────┬────────┘   │
                                                              │           │ re-run gate │
                                                              │           │ (cascade    │
                                                              │           │  resets)    │
                                                              ▼           │             │
                                                   ┌─────────────────────────────────┐ │
                                                   │       PROD_RELEASE               │ │
                                                   │  prod_release_created running    │ │
                                                   │      │                           │ │
                                                   │      ▼                           │ │
                                                   │  prod_deploy + prod_tests        │ │
                                                   │  (prod_tests starts after        │ │
                                                   │   prod_deploy completes)         │ │
                                                   └──────────┬──────────────────────┘ │
                                                              │                         │
                                              all succeed     │    any fails            │
                                                              │         │               │
                                                              │         ▼               │
                                                              │  ┌─────────────────┐   │
                                                              │  │  PROD_FAILED     │   │
                                                              │  │  slip=failed     │   │
                                                              │  │  prod_tests      │   │
                                                              │  │  aborted if      │   │
                                                              │  │  prod_deploy     │   │
                                                              │  │  failed first    │   │
                                                              │  └─────────────────┘   │
                                                              │                         │
                                                              ▼                         │
                                                   ┌─────────────────────────────────┐ │
                                                   │      PROD_MONITORING             │ │
                                                   │  prod_alert_gate running         │ │
                                                   │  (watches prod health/SLOs)      │ │
                                                   └──────────┬──────────────────────┘ │
                                                              │                         │
                                         alert passes         │    alert fires          │
                                                              │         │               │
                                                              ▼         ▼               │
                                             ┌──────────────────┐  ┌────────────────┐  │
                                             │     COMPLETED ✅  │  │  ROLLING_BACK  │  │
                                             │  prod_steady_     │  │  prod_rollback │  │
                                             │  state=completed  │  │  running       │  │
                                             │  slip=completed   │  └───────┬────────┘  │
                                             │  TERMINAL         │          │            │
                                             └──────────────────┘          ▼            │
                                                                   ┌────────────────┐   │
                                                                   │ PIPELINE_DONE  │   │
                                                                   │ prod_steady_   │   │
                                                                   │ state=failed   │   │
                                                                   │ slip=failed    │   │
                                                                   │ (recoverable   │   │
                                                                   │ but no natural │   │
                                                                   │ next step)     │   │
                                                                   └────────────────┘   │
                                                                                        │
└───────────────────────────────────────────────────────────────────────────────────────┘
```

> **Note:** "downstream lazy-abort" means downstream step rows are NOT changed by FailStep. Each downstream step transitions to `aborted` only when its own `WaitForPrerequisites` call observes the failed prereq.

> **Note:** Prod steps do NOT become `aborted` synchronously when `prod_gate=failed`. Each transitions to `aborted` only when its own `WaitForPrerequisites` runs (`hold.go:83-110`). Steps that never enter pre-job stay `pending`. The recovery branch (`executor.go:307-346`) only resets steps actually in `aborted` — vacuous if none ever transitioned.

---

## Pipeline Phases

### INIT

| | |
|---|---|
| **slip.status** | `in_progress` |
| **Steps** | `builds=running` (set at creation), all others `pending` |
| **Transitions out** | Immediately → `CI_PARALLEL` as Argo workflows fire |
| **⚠️ Risk** | If pushhookparser crashes here: `builds` stuck `running` permanently (no event log entry, no watchdog) |

---

### CI_PARALLEL

| | |
|---|---|
| **slip.status** | `in_progress` |
| **Running concurrently** | `builds` (all N components), `unit_tests`, `secret_scan`, `package_artifact` |
| **Transitions out** | `builds=completed` → `dev_deploy` unblocked (parallel with remaining CI) |
| | `builds+unit_tests+secret_scan=completed` → `preprod_deploy` unblocked |
| | Any step `failed/error/timeout` → `CI_FAILED` |

**Key:** `dev_deploy` and `preprod_deploy` have different unblock conditions.
`dev_deploy` unblocks as soon as `builds` completes - does **not** wait for `unit_tests` or `secret_scan`.

---

### CI_FAILED

| | |
|---|---|
| **slip.status** | `failed` |
| **Primary failures** | Whichever of `builds`, `unit_tests`, `secret_scan`, `package_artifact` failed |
| **Cascade aborts** | Steps whose prereqs include the failed step: `dev_deploy` (if builds failed), `preprod_deploy` (if any of builds/unit_tests/secret_scan failed), and all downstream |
| **What is blocked** | Everything downstream of the failing step |
| **What is NOT blocked** | Steps whose prereqs are all still satisfied (e.g. `dev_deploy` is NOT blocked by `unit_tests` failure) |
| **Recovery** | Re-run ALL failed CI steps → when last primary failure resolves → cascade-aborted steps reset to `pending` → `slip=in_progress` |

---

### DEV TRACK (concurrent with PREPROD TRACK)

| Phase | slip.status | Trigger | Failure effect |
|-------|------------|---------|----------------|
| `dev_deploy=running` | `in_progress` | `builds=completed` | `failed` - does NOT block preprod |
| `dev_tests=running` | `in_progress` | ArgoCD PostSync → TestEngine ⚠️ | `failed` - does NOT block preprod |
| `dev_deploy=failed` | `failed` | post-job | `dev_tests` aborts if waiting; preprod unaffected |
| `dev_tests=failed` | `failed` | TestEngine RunPostExecution | preprod unaffected (not a prereq) |

> ⚠️ TestEngine starts `dev_tests` via `StartStep` directly (no `WaitForPrerequisites`).
> Tests can start before or during `dev_deploy` in rerun/race scenarios (PROJECT_STATE.md - discrepancy #7).

---

### PREPROD TRACK (concurrent with DEV TRACK)

| Phase | slip.status | Trigger | Failure effect |
|-------|------------|---------|----------------|
| `preprod_deploy=running` | `in_progress` | `builds+unit_tests+secret_scan=completed` | `failed` - blocks `prod_gate` |
| `preprod_tests=running` | `in_progress` | ArgoCD PostSync → TestEngine ⚠️ | `failed` - blocks `prod_gate` |
| `preprod_deploy=failed` | `failed` | post-job | `preprod_tests` aborts if waiting; `prod_gate` blocked |
| `preprod_tests=failed` | `failed` | TestEngine RunPostExecution | `prod_gate` blocked until resolved |

> ⚠️ `preprod_tests` can run against a failed or restarted deployment (PROJECT_STATE.md - discrepancy #9).
> `prod_gate` has no awareness of which deployment the test results belong to.

---

### PROD_GATE

| | |
|---|---|
| **slip.status** | `in_progress` |
| **Prereqs** | `preprod_deploy=completed` AND `preprod_tests=completed` |
| **Running** | `prod_gate` (is_gate=true) |
| **On success** | All downstream prod steps unblocked; pipeline continues to `PROD_RELEASE` |
| **On failure** | `slip=failed` (FailStep only flips `prod_gate` + `slip.status`). Downstream steps NOT immediately aborted — each self-aborts lazily when its own `WaitForPrerequisites` observes `prod_gate=failed` (`hold.go:83-110`). Steps whose pre-job never runs stay `pending`. |
| **Recovery** | Re-run `prod_gate` → success → cascade steps reset to `pending` → `prod_gate=completed` must then unblock each downstream step individually as they restart |

---

### PROD_RELEASE

Steps run sequentially within this phase (each unblocks the next):

```
prod_gate=completed
    └─► prod_release_created=running ─► completed
            └─► prod_deploy=running (prereqs: prod_gate + prod_release_created)
                    └─► prod_tests=running (prereqs: prod_gate + prod_deploy)
```

| | |
|---|---|
| **slip.status** | `in_progress` |
| **Failure at prod_release_created** | `slip=failed`; `prod_deploy`, `prod_tests`, `prod_alert_gate`, etc. blocked (prereqs not met) |
| **Failure at prod_deploy** | `slip=failed`; `prod_tests` cascade-aborts (detected by WaitForPrerequisites) |
| **Failure at prod_tests** | `slip=failed`; `prod_steady_state` blocked |

---

### PROD_MONITORING

| | |
|---|---|
| **slip.status** | `in_progress` |
| **Running** | `prod_alert_gate` - watches production health (SLOs, error rates, alerts) |
| **Prereqs in config** | `[]` - only gate injection (`prod_gate=completed`) blocks it |
| **On pass** | `alert-gate.yaml` skips `prod_rollback`, marks `prod_steady_state=completed` → `slip=completed` |
| **On failure** | `alert-gate.yaml` triggers `gitops-rollback.yaml` → `ROLLING_BACK` |
| **⚠️ Gap** | Can start even when `prod_deploy=failed` - gate injection is satisfied but deploy never succeeded (PROJECT_STATE.md - discrepancies #8, #9) |

---

### ROLLING_BACK

| | |
|---|---|
| **slip.status** | `failed` |
| **Running** | `prod_rollback` - automated GitOps + source repo rollback |
| **On rollback complete** | `gitops-rollback.yaml` marks `prod_steady_state=failed` → `PIPELINE_DONE` |
| **prod_steady_state=failed** | Adds to `primaryFailures`; `slip=failed` (already); pipeline effectively closed |

---

### COMPLETED ✅

| | |
|---|---|
| **slip.status** | `completed` - **TERMINAL, IMMUTABLE** |
| **Triggered by** | `prod_steady_state=completed` with `primaryFailures=0` |
| **Triggered from** | `alert-gate.yaml` on pass: marks `prod_steady_state=completed` |
| **checkPipelineCompletion** | Short-circuits immediately: `slip.Status==completed → return` |
| **Further step events** | Recorded in event log but `checkPipelineCompletion` no longer changes `slip.status` |

---

### PIPELINE_DONE (failed terminal)

| | |
|---|---|
| **slip.status** | `failed` - non-terminal but no natural recovery path |
| **Triggered by** | `prod_steady_state=failed` (set by `gitops-rollback.yaml` after rollback) |
| **Technically recoverable?** | Yes - `failed` is non-terminal. But re-running `prod_steady_state` to `completed` after a rollback is semantically wrong |
| **In practice** | Next push to the branch creates a new slip; this one is abandoned |

---

## Failure States Summary

> **Lazy cascade note:** "Cascade aborts" column lists steps that WILL transition to `aborted` IF they call `WaitForPrerequisites` after the failure. FailStep itself does not write these rows. Steps that never enter pre-job remain in their current status.

| Failure point | slip.status | What is blocked | Cascade aborts | Recovery trigger |
|---------------|------------|-----------------|----------------|-----------------|
| `builds` failed | `failed` | `dev_deploy`, `preprod_deploy`, all downstream | All steps depending on builds | Re-run `builds` (any component) |
| `unit_tests` failed | `failed` | `preprod_deploy` | `preprod_deploy` + all downstream | Re-run `unit_tests` |
| `secret_scan` failed | `failed` | `preprod_deploy` | `preprod_deploy` + all downstream | Re-run `secret_scan` |
| `package_artifact` failed | `failed` | Nothing downstream | None | Re-run `package_artifact` - no step depends on it |
| `dev_deploy` failed | `failed` | `dev_tests` | `dev_tests` (if waiting) | Re-run `dev_deploy` - does NOT block preprod |
| `dev_tests` failed | `failed` | Nothing downstream | None | Re-run `dev_tests` - does NOT block preprod |
| `preprod_deploy` failed | `failed` | `preprod_tests`, `prod_gate` | `preprod_tests` (if waiting) | Re-run `preprod_deploy` |
| `preprod_tests` failed | `failed` | `prod_gate` | None | Re-run `preprod_tests` |
| `prod_gate` failed | `failed` | All production steps | `prod_release_created`, `prod_deploy`, `prod_tests`, `prod_alert_gate`, `prod_rollback`, `prod_steady_state` | Re-run `prod_gate` |
| `prod_release_created` failed | `failed` | `prod_deploy`, `prod_tests` | None (prereqs not met - they stay pending) | Re-run `prod_release_created` |
| `prod_deploy` failed | `failed` | `prod_tests`, `prod_steady_state` | `prod_tests` (if in WaitForPrerequisites) | Re-run `prod_deploy` |
| `prod_tests` failed | `failed` | `prod_steady_state` | None | Re-run `prod_tests` |
| `prod_alert_gate` failed | `failed` | `prod_steady_state` | None | Triggers rollback instead |

---

## Recovery Rules (applies everywhere)

```
slip recovers from failed → in_progress when:
  ALL primary failures resolved (every failed/error/timeout step is now completed/running/pending)
  AND
  slip.Status == failed at the moment checkPipelineCompletion fires

On recovery:
  cascade-aborted (`aborted`) steps → reset to `pending` automatically by `checkPipelineCompletion` recovery branch (`executor.go:307-346`). `aborted` is the ONLY reversible terminal step status; `failed`, `error`, `timeout`, `completed`, `skipped` are not auto-reset. Peer steps in `running`/`held`/`pending` are NEVER modified by FailStep — only the failing step's own row and `slip.status` change synchronously.
  slip.status → in_progress
  External orchestrators (auto-deployer, Argo) must re-trigger the pending steps
```

**Multiple simultaneous failures:** ALL must be resolved. Resolving only some keeps `slip=failed`.

**Rerunning a failed step:**
```
failed → running  (non-terminal: slip stays failed, no checkPipelineCompletion)
running → completed  (terminal: checkPipelineCompletion fires → may recover)
```

---

## Parallel Execution Model

```
After CI_PARALLEL:

time ──────────────────────────────────────────────────────────────────►

builds completes
    └─► dev_deploy (independent of unit_tests/secret_scan)
            └─► dev_tests (TestEngine PostSync)

builds + unit_tests + secret_scan all complete
    └─► preprod_deploy
            └─► preprod_tests (TestEngine PostSync)
                    └─► prod_gate (after both preprod steps done)
                            └─► prod_release_created
                                    └─► prod_deploy + prod_tests (parallel)
                                              └─► prod_alert_gate
                                                        └─► completed OR rollback
```

**dev track and preprod track are fully independent after CI_PARALLEL.**
A failure in dev track does not block preprod track and vice versa.

---

## What Auto-Deployer Does at Each Phase

Auto-deployer is **read-only** (polls `GetSlip`). It triggers Argo workflows via HTTP webhooks but never writes step events.

| Phase | Auto-deployer action |
|-------|---------------------|
| `CI_PARALLEL` | Waits for CI prereqs to complete before triggering deploys |
| `DEV_RUNNING` | Triggers `dev_deploy` if not already started; watches for completion |
| `DEV_TESTS_RUNNING` | If `dev_tests=failed`: F2/F3 retry - POSTs `/autotriggertests` |
| `PREPROD_RUNNING` | Triggers `preprod_deploy`; watches for completion |
| `PREPROD_TESTS_RUNNING` | If `preprod_tests=failed`: F2/F3 retry |
| `PROD_RELEASE` | Monitors prod_gate → prod_release_created → prod_deploy → prod_tests sequentially; does NOT auto-retry |
| Failures | Does NOT auto-retry prod_deploy or prod_gate |

---

## Algorithm Reference

### `checkPipelineCompletion` Pseudocode

**Location:** `executor.go:249`
**Triggered by:** terminal event on a pure pipeline step (guard: `IsTerminal() && componentName == ""`)

```
checkPipelineCompletion(ctx, correlationID):

  slip = store.Load()  →  hydrateSlip()   // re-derives ALL statuses from slip_component_states

  // GUARD: only completed is immutable (NOT IsTerminal())
  if slip.Status == completed:
    return immediately

  // SCAN: classify all step failures
  primaryFailures  = steps where status ∈ {failed, error, timeout}
  cascadeFailures  = steps where status == aborted

  // CHECK 1: any primary failure → pipeline failed (checked BEFORE prod_steady_state)
  if len(primaryFailures) > 0:
    UpdateSlipStatus(failed)
    return

  // CHECK 2: terminal success condition
  if prod_steady_state.status == completed:
    UpdateSlipStatus(completed)   // TERMINAL
    return

  // CHECK 3: recovery - all primary failures resolved
  if slip.Status == failed AND len(primaryFailures) == 0:
    for each step in cascadeFailures:
      UpdateStepWithStatus(step, pending, "reset: upstream failure resolved")
    UpdateSlipStatus(in_progress)
    return

  // else: no action (pipeline still in progress normally)
```

> **Order matters:** primary failures are checked BEFORE `prod_steady_state`. If both conditions
> are simultaneously true (edge case), the pipeline is set to `failed`, not `completed`.

### Step Categories

| Category | `componentName` | Example | Update path in store |
|----------|-----------------|---------|---------------------|
| Pure pipeline | `""` | `unit_tests`, `dev_deploy`, `prod_gate` | `appendHistoryWithOverrides` - atomic INSERT SELECT, one column override |
| Aggregate | `""` (rollup) | `builds` | `updateAggregateStatusFromComponentStatesWithHistory` - full Load+hydrateSlip+Update |
| Component | `"mc.x.y"` | individual build | `insertComponentState` + triggers aggregate recalc |

### Step Status Reference

| Status | Terminal? | IsSuccess() | IsFailure() | Category |
|--------|-----------|-------------|-------------|----------|
| `pending` | No | - | - | Initial |
| `held` | No | - | - | Waiting for prereqs |
| `running` | No | - | - | Executing |
| `completed` | Yes | ✅ | - | Success |
| `skipped` | Yes | ✅ | - | Success (treated as completed) |
| `failed` | Yes | - | ✅ primary | Primary failure |
| `error` | Yes | - | ✅ primary | Primary failure |
| `timeout` | Yes | - | ✅ primary | Primary failure |
| `aborted` | Yes* | - | ✅ cascade | Cascade - upstream prereq failed. *Reversible: auto-reset to `pending` on recovery (`executor.go:307-346`). |

---

## Code Validation Guide

When reviewing any change to `goLibMyCarrier/slippy/` or any caller (`Slippy/ci/`, `MC.TestEngine/`, `auto-deployer/`, workflow templates), use this checklist. The machine-readable version of these rules is `slippy/state_machine_invariants_test.go` (I1–I4 invariant tests).

### Validation Checklist

1. **`checkPipelineCompletion` call path** - a `checkPipelineCompletion` call MUST fire after every terminal step event on a pure pipeline step (`componentName == ""`). Flag any new caller that calls `CompleteStep`/`FailStep` directly for aggregate/component steps without also calling `RunPostExecution`.

2. **`checkPipelineCompletion` internal order** - the algorithm MUST follow: (a) completed short-circuit, (b) scan primaryFailures and cascadeFailures, (c) primaryFailures check FIRST → failed, (d) prod_steady_state check SECOND → completed, (e) recovery check THIRD. Flag any reordering of steps (c) and (d).

3. **Event log written first** - `insertComponentState` MUST be called before any `routing_slips` write. Flag any change that writes to `routing_slips` before writing to `slip_component_states`.

4. **Slip status at creation** - `initializeSlipForPush` MUST set `Status: SlipStatusInProgress`, not `pending`.

5. **Recovery conditions** - recovery (`failed` → `in_progress`) requires BOTH: `slip.Status == SlipStatusFailed` AND `len(primaryFailures) == 0`. Flag any change that triggers cascade reset without verifying both conditions.

6. **`WaitForPrerequisites` in new callers** - any new integration that calls `StartStep` (pre-job) MUST either call `WaitForPrerequisites` first, or document the explicit assumption about why prereqs are guaranteed at call time.

7. **Pipeline config changes** - for any new step or prerequisite change, trace the cascade abort scope and verify `prod_steady_state` terminal path is still reachable. Verify `dev_deploy` prereqs remain `[builds]` only (adding `unit_tests`/`secret_scan` breaks CI_PARALLEL → DEV independence).

8. **Pipeline phase impact** - identify which pipeline phase(s) the change touches (STATE_MACHINE_V3.md phases) and verify phase transition behaviour is preserved. Flag any change where the high-level phase flow would need to be redrawn but hasn't been updated.

9. **Atomic INSERT SELECT must override step columns being modified** — any new caller of `insertAtomicStatusUpdate`, `appendHistoryWithOverrides`, or `insertAtomicHistoryUpdate` MUST pass `stepStatusOverride` for every step whose status the caller intends to change. Verbatim cloning of step columns is unsafe under VersionedCollapsingMergeTree without FINAL — the just-written row may not be visible to the next SELECT.

10. **Event log is source of truth** — never read raw `routing_slips.<step>_status` for correctness decisions. Always go through `Load` + `hydrateSlip`, OR query `slip_component_states` directly with `argMax(status, timestamp)`.

### 4 Most Common Violations

**Violation 1 (I1 - indirect):** `CompleteStep`/`FailStep` called directly for `componentName!=""` without `RunPostExecution` → `slip.status` will not update after build component events. `slip` stays `in_progress` when builds fail (CI_FAILED never reached). Rule: `STATE_MACHINE.md §6`.

**Violation 2 (I3):** `checkPipelineCompletion` order changed - `prod_steady_state` check placed before primary failures scan → pipeline can be marked `completed` despite having failed steps. Rule: `STATE_MACHINE.md §5` - algorithm order.

**Violation 3 (persistence):** `routing_slips` written before `insertComponentState` → if the process crashes between the two writes, `hydrateSlip` will not override the cached status; step is permanently stuck. Rule: `STATE_MACHINE.md §8`.

**Violation 4 (phase independence):** New prerequisite added to a step that breaks phase independence - e.g., adding `unit_tests` to `dev_deploy` prereqs couples DEV TRACK to CI_PARALLEL completion. Rule: `STATE_MACHINE_V3.md` - DEV + PREPROD PARALLEL phase.

**Violation 5 (I5):** `insertAtomicStatusUpdate` (or any INSERT SELECT in the write path) clones step columns from a stale source row when the just-written row is not yet visible (ClickHouse async insert visibility under VersionedCollapsingMergeTree without FINAL). routing_slips column reverts to stale value while event log shows correct status. Fix: pass `stepStatusOverride` for the modified step into the atomic INSERT SELECT path. Rule: see bd issue `goLibMyCarrier-nl3`.

---

## Slippy simulation (Game) prompt

Alias: Slippy agent validation.

---

### Agent 1 — Workflow Simulator (Haiku)

Drives the slippy pipeline (`production.json`) by issuing requests to Agent 2 turn-by-turn (synchronous). Agent 1 does NOT touch state directly.

**Agent 1 boundary:** Agent 1 emits step-level requests ONLY (start step, complete step with outcome, re-run step, mutation attempts).

**CLI semantics (Agent 1 = Slippy CLI user — each request maps to a CLI verb, not a raw library call):**

| Verb | CLI command | Outcome |
|------|-------------|---------|
| `WaitForPrerequisites(<step>)` | `slippy pre-job <step>` | WFP + StartStep chain → step `running` (prereqs ok), `held` (blocked), or `aborted` (prereq failed). Cite `prejob.go:44`, `app.go:169-235` |
| `CompleteStep(<step>, completed)` | `slippy post-job --success <step>` | step `completed`; triggers `checkPipelineCompletion` |
| `FailStep(<step>, failed)` | `slippy post-job --failed <step>` | step `failed`; triggers `checkPipelineCompletion` (slip.status flips internally — Agent 1 does NOT report it) |
| `RerunStep(<step>, running)` | re-invoke pre-job after `failed → pending` | step `running` |
| `CreateSlip` | push handler creates slip | initial state |

Library-level WFP/StartStep/CompleteStep/FailStep are NOT separately invocable from CLI.

**STEP 0** — ask Agent 2 to create a new pipeline slip route.

**STEP 1** — ask Agent 2 to start 3 parallel initial steps:
- `builds` with N components, N random ∈ [1, 5]
- `unit_tests`
- `secret_scan`

**STEP 2** — report completion of all 3 initial steps to Agent 2 simultaneously. Random F failures, F ∈ [0, 2] (0 allowed). For builds: failure means at least 1 component failed.

**STEP 3** — if STEP 2 had failures: ask Agent 2 to mark all failed steps as re-run (`failed → running`) at the same time, then mark all as `completed` (first re-run always succeeds).

**STEP 4** — for each remaining step in `production.json` (topological order):
- ask Agent 2 to confirm prereqs complete; wait if not.
- ask Agent 2 to start the step.
- ask Agent 2 to complete the step. Outcome: 30% `failed`, 70% `completed`.
- if `failed`: ask Agent 2 to re-run (`failed → running → completed`). Max 1 retry per step (always succeeds on retry).
- `prod_rollback`: run ONLY if `prod_alert_gate=failed`; otherwise stays `pending`, skip directly to `prod_steady_state`.
- proceed to next step.

**STEP 5 — Cascade-abort scenario** — once `prod_gate` is reached, force `prod_gate=failed`.

**STEP 6 — Recovery-cascade-reset scenario** — ask Agent 2 to re-run `prod_gate` to `in_progress` then to `completed`.

**STEP 7** — resume STEP 4 logic from `prod_release_created` to `prod_steady_state=completed`.

**STEP 8 — Terminal/immutable test** — after `slip=completed`, attempt one further step mutation (e.g. ask Agent 2 to set `prod_steady_state=failed`).  Agent 1 just emits the mutation attempt; verification is Agent 2's job.

> **DO NOT EMIT `slip.status` in any event row.** `slip.status` is derived and owned by Agent 2.
> Only emit step transitions (e.g. `prod_gate=failed`, `unit_tests=completed`), never slip outcomes (e.g. `slip=failed`, `slip=in_progress`).

**Output:** `workflow-simulation-report.md` — chronological event log with columns:
`| seq | timestamp | request | claimed step status | scenario tag |`

---

### Agent 2 — Library Robustness Validator (Sonnet)

**Role:** Agent 2 — Library Robustness Validator. Drives virtual slip state per slippy library logic and scores library invariant compliance under adversarial inputs from Agent 1.

**Virtual state shape (per correlation id):**
- `slip.status`
- `steps[name].status`
- `steps[builds].components[name].status`
- `event_log[]`

**Inputs:** Agent 1 events — may be valid CLI verbs, wrong claims, fabricated verbs, `slip.status` events, narration, or other adversarial output.

**Validation rules:**
1. Validate against library source code only. Read files; do not fabricate.
2. For each Agent 1 event:
   - Apply to virtual state per CLI/library logic.
   - Determine library outcome (accepted / rejected / partial).
   - Check all invariants on resulting state.
   - Score event PASS / FAIL / N/A per scoring model below.
3. Agent 1's claimed step/slip status is INPUT, not grading criteria. Library-derived state is the truth.

**Scoring model:**

For each Agent 1 event, Agent 2:
1. Applies the event to virtual state per library logic (read source — do not fake).
2. Determines library response: accepted? rejected? state transition?
3. After applying, checks all invariants on the derived state.
4. Score per event:
   - **PASS** — library handled correctly: valid input applied + invariants hold; OR invalid input gracefully rejected (fabricated verb, post-completed mutation, etc.) without breaking state.
   - **FAIL** — library would produce inconsistent state: an invariant breaks (I1/I2/I3/I4), or aggregate inconsistent with components, or cascade/recovery wrong.
   - **N/A** — Agent 1 emitted a non-event (informational row, slip.status report, narration). Skipped, not counted toward total.

Final correctness = PASS / (PASS + FAIL).

**Specifically check:**
- **I1:** `slip=in_progress` with primary failure → FAIL
- **I2:** `slip=failed` with zero primary failures → FAIL
- **I3:** `slip=completed` with primary failure or running step → FAIL
- **I4:** `slip.status` changed after `slip=completed` → FAIL
- **Aggregate `builds`:** derived value matches `computeAggregateStatus` over current components → else FAIL
- **Lazy cascade:** only steps that called WFP after prereq failure are aborted → else FAIL
- **Recovery:** `aborted → pending` only when `slip.Status==failed` AND `len(primaryFailures)==0` → else FAIL
- **Conditional `prod_rollback`:** should never enter `running` unless `prod_alert_gate=failed` → else FAIL
- **Fabricated CLI verb:** library rejects (no state change) → PASS; if state changed → FAIL
- **Verb/outcome mismatch** (e.g. `CompleteStep(..., failed)`): library rejects → PASS

**Per-turn behavior:**
- Receive Agent 1 event.
- Apply to virtual state per library logic.
- Determine library outcome (accepted / rejected / partial).
- Check all invariants on resulting state.
- Reply to Agent 1 with: library action, derived step status, derived `slip.status`, verdict (PASS/FAIL/N/A), citation.
- Append to `slippy-simulation-report.md`.

**Output:** `slippy-simulation-report.md` — per-event verdict table with columns:
`| seq | request | library_action (accepted/rejected) | derived_step | derived_slip | invariants_held | verdict | citation |`

- `verdict` = PASS / FAIL / N/A
- `invariants_held` = comma-separated list of which invariants checked OK, or `BROKEN: I3` (for example) if failed.

Also includes: **Library Failures** section (4–8 lines per FAIL event only), cross-cutting findings, one-paragraph verdict, final correctness rate = PASS / (PASS + FAIL).

---

### Flow

Synchronous turn-by-turn. Agent 1 emits one request → Agent 2 processes, mutates virtual state, validates, replies → Agent 1 reads reply → emits next request. No batch handoff.

### Cross-run correctness tracking

After each run append one row to `slippy-simulation-history.md`:

```
| run_id | timestamp | git_sha | total_events | pass | fail | n_a | correctness_rate | notes |
```

- `correctness_rate` = PASS / (PASS + FAIL)
- `notes` describes library robustness observations (e.g., invariant violations found, fabricated-verb rejection coverage, recovery correctness).

Rate trend (drop ≥5pp run-over-run) flags regression. No hard threshold gate — purely diagnostic.

**Final deliverables per run:**
- `workflow-simulation-report.md` (Agent 1)
- `slippy-simulation-report.md` (Agent 2)
- `slippy-simulation-history.md` (appended)
```

---

## Automated Test Coverage

The file `slippy/state_machine_invariants_test.go` is the machine-readable enforcement of invariants I1–I4. These tests MUST pass before any change to `slippy/` is merged.

| Test | Invariant | What it verifies |
|------|-----------|-----------------|
| `TestStateMachine_I1_FailedStepSetsPipelineFailed` | I1 | `FailStep` on a running step sets `slip.status=failed` |
| `TestStateMachine_I1_ErrorStepSetsPipelineFailed` | I1 | `UpdateStepWithStatus(error)` sets `slip.status=failed` |
| `TestStateMachine_I1_TimeoutStepSetsPipelineFailed` | I1 | `TimeoutStep` on a held step sets `slip.status=failed` |
| `TestStateMachine_I1_AbortedStepAloneDoesNotSetPipelineFailed` | I1 | Cascade `aborted` alone does NOT set `slip.status=failed` |
| `TestStateMachine_I2_ResolvedFailureRestoresPipelineToInProgress` | I2 | Resolving last primary failure restores `slip.status=in_progress` |
| `TestStateMachine_I2_RecoveryCascadeStepsResetToPending` | I2 | Recovery resets cascade-aborted steps to `pending` |
| `TestStateMachine_I2_PartialRecoveryDoesNotRestorePipeline` | I2 | Resolving only some failures keeps `slip.status=failed` |
| `TestStateMachine_I3_SteadyStateCompletionSetsPipelineCompleted` | I3 | `prod_steady_state=completed` with no failures sets `slip.status=completed` |
| `TestStateMachine_I3_PrimaryFailureBlocksCompletion` | I3 | Primary failure blocks completion even when `prod_steady_state=completed` |
| `TestStateMachine_I4_CompletedSlipIsImmutable` | I4 | `FailStep` on a completed slip does not change `slip.status` |
| `TestStateMachine_I4_CompletedSlipIgnoresRecoveryAttempts` | I4 | `checkPipelineCompletion` on a completed slip changes nothing |
| `TestClient_PromoteSlip_Immutable` | I4 | `FailStep`/`UpdateStepWithStatus` on a promoted slip does not change `slip.status` |
| `TestClient_AbandonSlip_Immutable` | I4 | `FailStep`/`CompleteStep`/`UpdateStepWithStatus` on an abandoned slip does not change `slip.status` |

| `TestStateMachine_I5_AtomicStatusUpdateRespectsStepOverride` | I5 | `FailStep` on a running step wires the step-override through `checkPipelineCompletion`; `slip.status=failed` and the failing step column retains its authoritative value. Mock-based: verifies API contract (override computed and applied), not the ClickHouse async-insert race. See goLibMyCarrier-nl3. |
| `TestStateMachine_I5_StaleStepColumnNotPropagated` | I5 | Sequential terminal events (`FailStep` → `CompleteStep` → `FailStep`) do not revert earlier step columns to stale values; every `checkPipelineCompletion` call preserves all current primary-failure overrides. Mock-based: synchronous store cannot reproduce the visibility race; validates override-wiring contract. See goLibMyCarrier-nl3. |
| `TestE2E_ConcurrentTerminalStepEvents_RoutingSlipsMatchesEventLog` | I5 | Under sequential and concurrent terminal step events, `routing_slips.<step>_status` matches `argMax`-derived status from `slip_component_states`; recovery race leaves no stale columns. File: `slippy/e2e_integration_test.go` (build tag: `integration`). |

Run with: `go test -run TestStateMachine ./slippy/...`

Run I5 e2e test with: `go test -tags integration -run TestE2E_ConcurrentTerminalStepEvents -v ./slippy/...`

Failing tests indicate an invariant violation and MUST be resolved before merging.
