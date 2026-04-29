# Slippy Pipeline — High-Level State Machine

**Companion to:** `STATE_MACHINE.md` (low-level per-step rules)
**Purpose:** full-pipeline view — how stages progress, what blocks, what recovers, what terminates.
**Source config:** `production.json`

---

## Consistency Invariants

A **discrepancy** is any condition where `slip.status` violates one of these invariants.
Full definition and known violations: see `PROJECT_STATE.md` (Technical Debt — Slippy State Machine Discrepancies).

| # | Invariant |
|---|-----------|
| **I1** | `slip=in_progress` while any step has `status ∈ {failed, error, timeout}` → **violation** |
| **I2** | `slip=failed` while no step has `status ∈ {failed, error, timeout}` AND pipeline not completed → **violation** |
| **I3** | `slip=completed` while any step has `status ∈ {failed, error, timeout}` OR `status = running` → **violation** |
| **I4** | Any status change after `slip=completed` → **violation** |

**Note on `aborted`:** cascade failures (`aborted`) are NOT primary failures — they do not independently drive `slip=failed` and are not counted in I1/I2/I3.
**Note on `pending`/`held`/`skipped`:** these do not block any transition including completion (I3).

**Pipeline termination without completing:**
- `abandoned` — automatic when a newer push supersedes this branch (`AbandonSlip`, `client.go:170`)
- `promoted` — automatic on PR squash-merge to another branch (`PromoteSlip`, `client.go:204`)
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
                          │  package_artifact  —  all running concurrently               │
                          │  slip.status = in_progress                                   │
                          └────┬──────────────────────────────────────────┬─────────────┘
                               │                                          │
                    builds     │                             any CI step  │
                    completes  │                             fails        │
                               │                                          ▼
                               │                               ┌─────────────────────┐
                               │                               │     CI_FAILED        │
                               │                               │  slip.status=failed  │
                               │                               │  downstream blocked  │
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
                                                              │  │ all = aborted    │   │
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
`dev_deploy` unblocks as soon as `builds` completes — does **not** wait for `unit_tests` or `secret_scan`.

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
| `dev_deploy=running` | `in_progress` | `builds=completed` | `failed` — does NOT block preprod |
| `dev_tests=running` | `in_progress` | ArgoCD PostSync → TestEngine ⚠️ | `failed` — does NOT block preprod |
| `dev_deploy=failed` | `failed` | post-job | `dev_tests` aborts if waiting; preprod unaffected |
| `dev_tests=failed` | `failed` | TestEngine RunPostExecution | preprod unaffected (not a prereq) |

> ⚠️ TestEngine starts `dev_tests` via `StartStep` directly (no `WaitForPrerequisites`).
> Tests can start before or during `dev_deploy` in rerun/race scenarios (PROJECT_STATE.md — discrepancy #7).

---

### PREPROD TRACK (concurrent with DEV TRACK)

| Phase | slip.status | Trigger | Failure effect |
|-------|------------|---------|----------------|
| `preprod_deploy=running` | `in_progress` | `builds+unit_tests+secret_scan=completed` | `failed` — blocks `prod_gate` |
| `preprod_tests=running` | `in_progress` | ArgoCD PostSync → TestEngine ⚠️ | `failed` — blocks `prod_gate` |
| `preprod_deploy=failed` | `failed` | post-job | `preprod_tests` aborts if waiting; `prod_gate` blocked |
| `preprod_tests=failed` | `failed` | TestEngine RunPostExecution | `prod_gate` blocked until resolved |

> ⚠️ `preprod_tests` can run against a failed or restarted deployment (PROJECT_STATE.md — discrepancy #9).
> `prod_gate` has no awareness of which deployment the test results belong to.

---

### PROD_GATE

| | |
|---|---|
| **slip.status** | `in_progress` |
| **Prereqs** | `preprod_deploy=completed` AND `preprod_tests=completed` |
| **Running** | `prod_gate` (is_gate=true) |
| **On success** | All downstream prod steps unblocked; pipeline continues to `PROD_RELEASE` |
| **On failure** | `slip=failed`; ALL downstream steps cascade-aborted in one sweep: `prod_release_created`, `prod_deploy`, `prod_tests`, `prod_alert_gate`, `prod_rollback`, `prod_steady_state` |
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
| **Running** | `prod_alert_gate` — watches production health (SLOs, error rates, alerts) |
| **Prereqs in config** | `[]` — only gate injection (`prod_gate=completed`) blocks it |
| **On pass** | `alert-gate.yaml` skips `prod_rollback`, marks `prod_steady_state=completed` → `slip=completed` |
| **On failure** | `alert-gate.yaml` triggers `gitops-rollback.yaml` → `ROLLING_BACK` |
| **⚠️ Gap** | Can start even when `prod_deploy=failed` — gate injection is satisfied but deploy never succeeded (PROJECT_STATE.md — discrepancies #8, #9) |

---

### ROLLING_BACK

| | |
|---|---|
| **slip.status** | `failed` |
| **Running** | `prod_rollback` — automated GitOps + source repo rollback |
| **On rollback complete** | `gitops-rollback.yaml` marks `prod_steady_state=failed` → `PIPELINE_DONE` |
| **prod_steady_state=failed** | Adds to `primaryFailures`; `slip=failed` (already); pipeline effectively closed |

---

### COMPLETED ✅

| | |
|---|---|
| **slip.status** | `completed` — **TERMINAL, IMMUTABLE** |
| **Triggered by** | `prod_steady_state=completed` with `primaryFailures=0` |
| **Triggered from** | `alert-gate.yaml` on pass: marks `prod_steady_state=completed` |
| **checkPipelineCompletion** | Short-circuits immediately: `slip.Status==completed → return` |
| **Further step events** | Recorded in event log but `checkPipelineCompletion` no longer changes `slip.status` |

---

### PIPELINE_DONE (failed terminal)

| | |
|---|---|
| **slip.status** | `failed` — non-terminal but no natural recovery path |
| **Triggered by** | `prod_steady_state=failed` (set by `gitops-rollback.yaml` after rollback) |
| **Technically recoverable?** | Yes — `failed` is non-terminal. But re-running `prod_steady_state` to `completed` after a rollback is semantically wrong |
| **In practice** | Next push to the branch creates a new slip; this one is abandoned |

---

## Failure States Summary

| Failure point | slip.status | What is blocked | Cascade aborts | Recovery trigger |
|---------------|------------|-----------------|----------------|-----------------|
| `builds` failed | `failed` | `dev_deploy`, `preprod_deploy`, all downstream | All steps depending on builds | Re-run `builds` (any component) |
| `unit_tests` failed | `failed` | `preprod_deploy` | `preprod_deploy` + all downstream | Re-run `unit_tests` |
| `secret_scan` failed | `failed` | `preprod_deploy` | `preprod_deploy` + all downstream | Re-run `secret_scan` |
| `dev_deploy` failed | `failed` | `dev_tests` | `dev_tests` (if waiting) | Re-run `dev_deploy` — does NOT block preprod |
| `dev_tests` failed | `failed` | Nothing downstream | None | Re-run `dev_tests` — does NOT block preprod |
| `preprod_deploy` failed | `failed` | `preprod_tests`, `prod_gate` | `preprod_tests` (if waiting) | Re-run `preprod_deploy` |
| `preprod_tests` failed | `failed` | `prod_gate` | None | Re-run `preprod_tests` |
| `prod_gate` failed | `failed` | All production steps | `prod_release_created`, `prod_deploy`, `prod_tests`, `prod_alert_gate`, `prod_rollback`, `prod_steady_state` | Re-run `prod_gate` |
| `prod_release_created` failed | `failed` | `prod_deploy`, `prod_tests` | None (prereqs not met — they stay pending) | Re-run `prod_release_created` |
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
  cascade-aborted (aborted) steps → reset to pending automatically
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
| `DEV_TESTS_RUNNING` | If `dev_tests=failed`: F2/F3 retry — POSTs `/autotriggertests` |
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

  // CHECK 3: recovery — all primary failures resolved
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
| Pure pipeline | `""` | `unit_tests`, `dev_deploy`, `prod_gate` | `appendHistoryWithOverrides` — atomic INSERT SELECT, one column override |
| Aggregate | `""` (rollup) | `builds` | `updateAggregateStatusFromComponentStatesWithHistory` — full Load+hydrateSlip+Update |
| Component | `"mc.x.y"` | individual build | `insertComponentState` + triggers aggregate recalc |

### Step Status Reference

| Status | Terminal? | IsSuccess() | IsFailure() | Category |
|--------|-----------|-------------|-------------|----------|
| `pending` | No | — | — | Initial |
| `held` | No | — | — | Waiting for prereqs |
| `running` | No | — | — | Executing |
| `completed` | Yes | ✅ | — | Success |
| `skipped` | Yes | ✅ | — | Success (treated as completed) |
| `failed` | Yes | — | ✅ primary | Primary failure |
| `error` | Yes | — | ✅ primary | Primary failure |
| `timeout` | Yes | — | ✅ primary | Primary failure |
| `aborted` | Yes | — | ✅ cascade | Cascade — upstream prereq failed |

---

## Code Validation Guide

When reviewing any change to `goLibMyCarrier/slippy/` or any caller (`Slippy/ci/`, `MC.TestEngine/`, `auto-deployer/`, workflow templates), use this checklist. The machine-readable version of these rules is `slippy/state_machine_invariants_test.go` (I1–I4 invariant tests).

### Validation Checklist

1. **`checkPipelineCompletion` call path** — a `checkPipelineCompletion` call MUST fire after every terminal step event on a pure pipeline step (`componentName == ""`). Flag any new caller that calls `CompleteStep`/`FailStep` directly for aggregate/component steps without also calling `RunPostExecution`.

2. **`checkPipelineCompletion` internal order** — the algorithm MUST follow: (a) completed short-circuit, (b) scan primaryFailures and cascadeFailures, (c) primaryFailures check FIRST → failed, (d) prod_steady_state check SECOND → completed, (e) recovery check THIRD. Flag any reordering of steps (c) and (d).

3. **Event log written first** — `insertComponentState` MUST be called before any `routing_slips` write. Flag any change that writes to `routing_slips` before writing to `slip_component_states`.

4. **Slip status at creation** — `initializeSlipForPush` MUST set `Status: SlipStatusInProgress`, not `pending`.

5. **Recovery conditions** — recovery (`failed` → `in_progress`) requires BOTH: `slip.Status == SlipStatusFailed` AND `len(primaryFailures) == 0`. Flag any change that triggers cascade reset without verifying both conditions.

6. **`WaitForPrerequisites` in new callers** — any new integration that calls `StartStep` (pre-job) MUST either call `WaitForPrerequisites` first, or document the explicit assumption about why prereqs are guaranteed at call time.

7. **Pipeline config changes** — for any new step or prerequisite change, trace the cascade abort scope and verify `prod_steady_state` terminal path is still reachable. Verify `dev_deploy` prereqs remain `[builds]` only (adding `unit_tests`/`secret_scan` breaks CI_PARALLEL → DEV independence).

8. **Pipeline phase impact** — identify which pipeline phase(s) the change touches (STATE_MACHINE_V2.md phases) and verify phase transition behaviour is preserved. Flag any change where the high-level phase flow would need to be redrawn but hasn't been updated.

### 4 Most Common Violations

**Violation 1 (I1 — indirect):** `CompleteStep`/`FailStep` called directly for `componentName!=""` without `RunPostExecution` → `slip.status` will not update after build component events. `slip` stays `in_progress` when builds fail (CI_FAILED never reached). Rule: `STATE_MACHINE.md §6`.

**Violation 2 (I3):** `checkPipelineCompletion` order changed — `prod_steady_state` check placed before primary failures scan → pipeline can be marked `completed` despite having failed steps. Rule: `STATE_MACHINE.md §5` — algorithm order.

**Violation 3 (persistence):** `routing_slips` written before `insertComponentState` → if the process crashes between the two writes, `hydrateSlip` will not override the cached status; step is permanently stuck. Rule: `STATE_MACHINE.md §8`.

**Violation 4 (phase independence):** New prerequisite added to a step that breaks phase independence — e.g., adding `unit_tests` to `dev_deploy` prereqs couples DEV TRACK to CI_PARALLEL completion. Rule: `STATE_MACHINE_V2.md` — DEV + PREPROD PARALLEL phase.

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

Run with: `go test -run TestStateMachine ./slippy/...`

Failing tests indicate an invariant violation and MUST be resolved before merging.
