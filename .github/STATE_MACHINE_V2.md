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

## Slippy simulation (Game) prompt

Alias: Slippy agent validation.

**Hard rule for both agents: every claim must cite `file_path:line_number` from the live codebase. No speculation, no assumptions, no inferences from spec docs alone. If a fact cannot be verified in code, label it `UNVERIFIED` and explain what is missing.**

```text
VARIABLES (set once at invocation; if not specified, defaults below apply):
  CALLER_PATH    = /Volumes/repos/mycarrier/DevOps/Slippy          [default]
  CALLER_BRANCH  = main                                              [default]
  LIBRARY_SOURCE = go.mod                                            [default: resolve from CALLER_PATH go.mod]
                   | or explicit path, e.g. /Volumes/repos/mycarrier/DevOps/goLibMyCarrier/slippy
                   |   (use for post-PR#56 working tree or any unreleased in-tree version)
  LIBRARY_LABEL  = auto-derived:
                   • if LIBRARY_SOURCE=go.mod → "<version> (linked from <CALLER_PATH> go.mod)"
                   • if LIBRARY_SOURCE=explicit path → "post-PR#56 working tree" (or describe the version)

Both agents lock these values once and use them for ALL file:line citations throughout.
Cross-version contamination (citing one version's code while claiming another) is a hard failure.

----

Run two subagents on the slippy pipeline (production.json):

Agent 1 — Workflow Simulator, Haiku model.

STEP 0 — Lock version (mandatory, before any other step):
  • If LIBRARY_SOURCE=go.mod: read CALLER_PATH go.mod (on CALLER_BRANCH), extract the goLibMyCarrier version tag, locate that exact version in the Go module cache.
    Set LIBRARY_LABEL = "<version> (linked from <CALLER_PATH> go.mod)".
    Set LIBRARY_PATH  = $GOPATH/pkg/mod/github.com/!my!carrier-!dev!ops/go!lib!my!carrier/slippy@<version>
  • If LIBRARY_SOURCE=explicit path: set LIBRARY_PATH = LIBRARY_SOURCE, LIBRARY_LABEL as provided.
  • Write the locked values into the report header (first lines of slippy-simulation-report.md):
      CALLER:  <CALLER_PATH> @ <CALLER_BRANCH>
      LIBRARY: <LIBRARY_LABEL>
      LIBRARY_PATH: <LIBRARY_PATH>
  • Every library_code_ref in every emitted event uses LIBRARY_PATH as the path prefix.
    If you cite a path outside LIBRARY_PATH for a library claim — that is a cross-version contamination error.

Source of truth — read these BEFORE simulating:
  • Caller: CALLER_PATH at CALLER_BRANCH.
    Other valid callers (if CALLER_PATH overridden): slippy-api (slippy-api/internal/infrastructure/slip_writer.go), MC.TestEngine (TestEngine.Worker/pkg/worker/slippy.go), pushhookparser.
  • Library: LIBRARY_PATH (locked in STEP 0 — not the local goLibMyCarrier working tree unless LIBRARY_SOURCE is explicit).
  • Pipeline definition: goLibMyCarrier/slippy/production.json (steps, prerequisites, aggregates, is_gate).

Section 1 — CLI/API surface enumeration (mandatory, FIRST):
  • Enumerate every subcommand the chosen caller exposes by listing every file in its CLI/handler directory (e.g. ci/Slippy/internal/cli/*.go).
    For each: file:line, command name, flags, the slippy library method it ultimately calls.
  • Do NOT invent commands. If a transition you need cannot be driven by any enumerated command, explicitly mark it OUT-OF-CALLER and document who actually drives it
    (e.g. dev_tests / preprod_tests are started by MC.TestEngine via ArgoCD PostSync; prod_alert_gate skip and prod_rollback are driven by alert-gate.yaml / gitops-rollback.yaml workflow templates).

Section 1b — OUT-OF-CALLER driver source read (mandatory, FIRST, before emitting any event):
  • For each OUT-OF-CALLER driver you will reference in the simulation, READ ITS ACTUAL SOURCE before emitting any event tagged with it. Do NOT infer behavior from production.json, the spec doc, or the step's prerequisites — read the driver.
  • For workflow-core YAML drivers: read the full template body. Identify every step that calls a slippy CLI command (slippy pre-job, slippy post-job, slippy skip-step, slippy start-step). Note: prod_steady_state may be completed directly via post-job from an exit handler with NO prior pre-job — verify in alert-gate.yaml before claiming a running phase.
  • For MC.TestEngine: read /Volumes/repos/mycarrier/DevOps/MC.TestEngine/TestEngine.Worker/pkg/worker/slippy.go. TestEngine may call ResolveSlip + StartStep directly, bypassing WaitForPrerequisites — verify before claiming a pre-job phase.
  • For pushhookparser: read its slip-creation code. Confirm the initial step states (pending vs running) before emitting the T+0 snapshot.
  • Document each driver's actual call chain in a "Section 1b — OUT-OF-CALLER driver call chains" table BEFORE Section 2 events. Columns: driver | file:line | what it calls (CLI command + library method) | step lifecycle (pre-job → post-job vs post-job-only vs StartStep+post-job vs skip-step).
  • If you skip Section 1b for a driver, every event you emit for that driver MUST be labeled UNVERIFIED.

Scenario:
  • Simulate one complete slip from creation through prod_steady_state=completed.
  • Start all no-prereq steps concurrently (per production.json).
  • Inject ≥3 failures: one build component, one pure-pipeline step, and one concurrent same-timestamp pair with opposite outcomes (one success, one failure).
  • Restart all failed steps to success and continue to terminal completion.

Per-event format (one JSON object per transition):
{
  "t": "T+45s",
  "caller_command": "<exact CLI command or 'OUT-OF-CALLER: <who>'>",
  "caller_code_ref": "ci/Slippy/internal/cli/postjob.go:NN",
  "library_call": "client.RunPostExecution(...)",
  "library_code_ref": "slippy@vX.Y.Z/executor.go:203",
  "step_transition": {"step": "...", "component": "...", "from": "...", "to": "..."},
  "aggregate_transition": null | {"step": "...", "from": "...", "to": "..."},
  "slip_transition": null | {"from": "...", "to": "..."},
  "trigger_for_slip_change": "<file:line of the call site that fires checkPipelineCompletion, OR 'none — slip status unchanged'>",
  "verification": "VERIFIED" | "UNVERIFIED — <what is missing>"
}

Forbidden claims (auto-fail an event if it appears):
  • "cascade-aborted" without identifying an active WaitForPrerequisites poller for that step. Pending steps without a started runner stay 'pending', not 'aborted' (hold.go is the only abort path).
    Note: in some library versions WaitForPrerequisites returns an error WITHOUT calling AbortStep — verify in the linked hold.go. If it doesn't write 'aborted' to the DB, no cascade-aborts occur.
  • "Aggregate write-back fires checkPipelineCompletion" — the store's aggregate write-back calls s.Update directly; it does NOT re-enter Client.UpdateStepWithStatus.
    Only the chain UpdateStepWithStatus(componentName=="") guard OR RunPostExecution → executor.go:229 fires checkPipelineCompletion. Cite which one for each slip transition.
  • "checkPipelineCompletion fires for component events via UpdateStepWithStatus" — verify this by checking the actual linked-version steps.go guard. In some versions only RunPostExecution drives it.
  • OUT-OF-CALLER step lifecycle claims (e.g. "prod_alert_gate runs pre-job → post-job", "prod_steady_state has a running phase", "TestEngine calls RunPreExecution") that were not verified by reading the driver source per Section 1b. Inference from prerequisites alone is not verification.
  • Any timing claim (e.g. recovery at T+X) that is not derivable from the events you emitted.

After each phase transition, emit a state snapshot:
{ "phase": "...", "t": "...", "slip_status": "...", "primary_failures": [...], "cascade_aborted": [...], "running": [...], "completed": [...], "pending": [...] }

Section "Open assumptions" must list every assumption with VERIFIED (file:line) or UNVERIFIED (what is missing).
The simulation must be self-consistent: dev_tests=completed cannot precede dev_deploy=completed.

Output file: slippy-simulation-report.md.

----

Agent 2 — State Validator, Sonnet model.

STEP 0 — Read locked version from Agent 1's report header (mandatory, first):
  • Open Agent 1's report file. Read the CALLER / LIBRARY / LIBRARY_PATH lines at the top.
  • Set your LIBRARY_PATH and LIBRARY_LABEL to those exact values.
  • Every library file:line you cite uses LIBRARY_PATH as the path prefix.
  • Cross-version contamination is a hard failure: if you cite a path outside LIBRARY_PATH — mark yourself WRONG on that point.
  • Do NOT resolve go.mod yourself — use what Agent 1 locked.

Inputs:
  • Agent 1's report file (path provided).
  • LIBRARY_PATH from Agent 1's report header (the single source of library truth for this round).
  • State machine spec: STATE_MACHINE_V2.md (invariants I1–I4 reference only — NOT a substitute for code).

Validation rules:
  1. Validate against LIBRARY_PATH only. Never switch to the local goLibMyCarrier working tree unless Agent 1's header says LIBRARY_SOURCE is an explicit local path.
     If Agent 1 locked v1.3.75, all your library citations must be v1.3.75 paths — even if you know post-PR#56 behavior from training.
  2. Classify every event as CORRECT / WRONG / PARTIAL with file:line citations.
  3. Specifically check:
     a) Does checkPipelineCompletion actually fire for each claimed trigger?
        - For RunPostExecution: confirm executor.go:229 (or equivalent) in the linked version.
        - For direct UpdateStepWithStatus: read the actual guard in steps.go for the linked version. The guard differs between v1.3.75 (no checkPipelineCompletion in steps.go at all) and post-PR-#56 versions (guard fires for componentName=="").
     b) Cascade-abort claims — only valid if Agent 1 emitted (or implied) an active WaitForPrerequisites poller for that step. Otherwise mark WRONG.
     c) Aggregate transitions — verify computeAggregateStatus and applyComponentStatesToAggregate in the linked clickhouse_store.go produce the claimed aggregate value.
     d) "OUT-OF-CALLER" tags — verify Agent 1 honestly flagged transitions not driven by the chosen caller (TestEngine PostSync, alert-gate.yaml, gitops-rollback.yaml, prod-gate.yaml, etc.).
     e) Recovery branch — confirm both conditions (slip.Status==failed AND len(primaryFailures)==0) hold at the recovery event Agent 1 identified.
     f) Concurrent same-timestamp events — verify they are reachable through independent CLI invocations and the store's conflict-free path (insertComponentState retry loop) handles them.
  4. List Agent 1's "Open assumptions" and re-classify each as VERIFIED / UNVERIFIED / WRONG with file:line.
  5. Compute a correctness rate (correct events / total). Flag any "correct outcome, wrong reason" cases — they count as PARTIAL, not CORRECT.

Output: slippy-simulation-validation.md with a per-event verdict table, a discrepancies section (4–8 lines per WRONG/PARTIAL event), a cross-cutting findings section, and a one-paragraph verdict.

Hard rule: validate against LIBRARY_PATH only (read from Agent 1's report header). Do not apply memorized behavior from a different version — if you know post-PR#56 logic and LIBRARY_LABEL says v1.3.75, those are WRONG citations. Cite the actual code in the locked path.

To switch codebase for next round: override LIBRARY_SOURCE in the VARIABLES block (e.g. set LIBRARY_SOURCE to the goLibMyCarrier working tree path for post-PR#56 testing).

----

Feed Agent 1's output file path to Agent 2. Both agents are read-only. Final deliverable: simulation-report.md + simulation-validation.md.
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

Run with: `go test -run TestStateMachine ./slippy/...`

Failing tests indicate an invariant violation and MUST be resolved before merging.
