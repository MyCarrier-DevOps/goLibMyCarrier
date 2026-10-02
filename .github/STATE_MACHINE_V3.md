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
- `aborted`: terminal-cascade (upstream prereq failed). **Reversible** — auto-reset to `pending` by recovery branch in `checkPipelineCompletion` (`executor.go`) when last primary failure resolves.
- Full table: see Step Status Reference below.

**Glossary:**
- *Primary failure* — step in `{failed, error, timeout}`. Drives `slip.status=failed`.
- *Cascade abort* — step in `aborted` because prereq failed. Does NOT drive `slip.status=failed`.
- *Aggregate step* — rollup of N components (e.g. `builds`). Status derived from component states.
- *Pure pipeline step* — `componentName == ""` (e.g. `unit_tests`, `dev_deploy`).
- *Component-state rows* — `slip_component_states` table. One current-state row per (correlation_id, step, component); `component = ''` is the pipeline-level row. The authoritative step status.
- *Materialized step columns* — `routing_slips.<step>_status` columns. Cached projection of the component-state rows. Must always match them (per I5).

## Rules

### Identity
- Slip route unique per correlation id.

### Topology
- Step dependencies defined by pipeline config (`production.json`).
- Initial steps (no prereqs): `builds`, `unit_tests`, `secret_scan`, `package_artifact`.
- Final step: `prod_steady_state`.
- Canary steps (DEVOPS-314): `preprod_rollback_test` (prereq `preprod_tests`; added to `prod_gate` prereqs) and `prod_canary` (prereq `prod_release_created`; added to `prod_deploy` prereqs). Both are marked `skipped` by the release workflow unless the repo's `deployment-strategy` custom property is `canary` (see Canary Steps section).
- All initial steps run in parallel for same correlation id. Build components run in parallel within `builds` aggregate.
- Downstream steps run per config-defined prereqs (see Pipeline Flow diagram).

### Lifecycle
- Slippy CLI `-pre` (`StartStep` / `WaitForPrerequisites`) and `-post` (`RunPostExecution`) drive every step transition. No direct `routing_slips` writes outside this path.
- Step terminal status propagates to `slip.status` via `checkPipelineCompletion`:
  - Any primary failure → `slip=failed`.
  - **FailStep(X) scope:** only `X.status → failed` and `slip.status → failed`. No other step rows modified synchronously. Downstream steps self-abort lazily when each one calls `WaitForPrerequisites` and observes the failed prereq (`hold.go:83-111`).
  - All primary failures resolved AND `slip=failed` → `slip=in_progress`; cascade-aborted steps reset to `pending`.
  - `prod_steady_state=completed` AND zero primary failures → `slip=completed` (terminal, immutable).
- Aggregate `builds`: any single component primary failure → aggregate `failed` → `slip=failed`. Aggregate `completed` only when all components terminal-success.
- Before any component reports, the aggregate step's status is its own last componentless write (DEVOPS-373).
- Recovery path per step: `failed → running → completed`. Slip recovery fires on terminal post-event of last unresolved primary failure.

---

## Consistency Invariants

A **discrepancy** is any condition where `slip.status` violates one of these invariants.
Full definition and known violations: see `PROJECT_STATE.md` (Technical Debt - Slippy State Machine Discrepancies). bd issue `goLibMyCarrier-nl3` covers a concrete I5 violation example (write-path stale column clone). bd issue `goLibMyCarrier-yix` is a related but distinct bug (decision-time staleness in the removed ClickHouse store's read path — separate from the I5 write-path issue). Both predate DEVOPS-343.

| # | Invariant |
|---|-----------|
| **I1** | `slip=in_progress` while any step is a primary failure (`status ∈ {failed, error, timeout}`) → **violation** |
| **I2** | `slip=failed` with zero primary failures → **violation** |
| **I3** | `slip=completed` while any step is a primary failure OR `status = running` → **violation** |
| **I4** | `slip.status` change after `slip=completed` → **violation** (event log writes for further step events ARE allowed; only `slip.status` is immutable — see line 335). **Formerly an exception path:** slippy-api's `POST /v1/slips/{id}/claim` wrote `in_progress` unconditionally under DEVOPS-285; since DEVOPS-367 the claim is a flag and writes no status at all, so it no longer touches `slip.status` — see "An ended row may have work in flight against it" under the repave sharp edges |
| **I5** | `routing_slips.<step>_status` column does not match the status the store derives for that step from its `slip_component_states` rows → **materialization violation** |

**Note on invariant scope:** I1–I4 are semantic correctness invariants (slip.status given step states). I5 is a **materialization consistency** invariant — the cached `routing_slips.<step>_status` columns must match the authoritative rows in `slip_component_states`. PostgresStore keeps I5 for step writes: `updateStepTx` writes the component-state row and the step column in one transaction under the slip's row lock. It does not hold by construction on every path. A full-row `Create` or `Update` writes every `<step>_status` column from the caller's snapshot without touching `slip_component_states`, and `ResetSlipInPlace` rewrites the row but keeps the previous attempt's component rows (`push.go`, "Caveat 1"), so either can leave the two apart. Divergence points at one of those write paths or a write-path bug, not at state-machine logic.

**I5 history — ClickHouse slip store (removed, DEVOPS-343).** Every I5 divergence recorded here
before v1.5.0 came from the ClickHouse store: its async-insert visibility gap under
VersionedCollapsingMergeTree without FINAL, the placeholder-component recompute, and the
sibling-column stale-clone wedge (bd mycarrier-5dv5, prod repro `74cd6676`), with their fixes
(`overlayComponentState`, `filterActiveComponents`, the CLONE_DERIVED 3-tier derive), the
`handlePushRetry` best-effort history trade-off, and the accepted tier-2 inversion residual. None
applies to PostgresStore. The full entries are in this file's history up to goLibMyCarrier v1.4.x
(`git log -p -- .github/STATE_MACHINE_V3.md`).

**Note on `aborted`:** cascade failures (`aborted`) are NOT primary failures — they do not independently drive `slip=failed` and are not counted in I1/I2/I3.
**Note on `pending`/`held`:** non-terminal, do not block any transition including completion (I3).
**Note on `skipped`:** terminal-success, treated as `completed`. Does not block I3.

**Pipeline termination without completing:**
- `abandoned` - automatic when a newer push supersedes this branch (`Client.AbandonSlip`)
- `promoted` - automatic on PR squash-merge to another branch (`Client.PromoteSlip`)
- No operator abort tool exists. Both bypass `checkPipelineCompletion`.

**Same-commit slip identity — repave (DEVOPS-231):**

Slip identity is `(lower(repository), commit_sha)` — one row per commit. `correlation_id`
remains the primary key and the child-row anchor, but identifies the *current run*, not
the slip's identity; `branch` is likewise an attribute of the current run, not part of
identity. A DB unique index (`uq_routing_slips_repo_sha`) enforces this at the storage
layer as of migration v5 — a separately-gated migration (gate described under "No
duplicate detection before migration v5" below); `CreateSlipForPush`
(`push.go`) already implements the contract below ahead of that index landing.

- **Same-commit ended push → repave.** A push for a commit SHA whose existing slip is
  `in_progress`/`pending`/`compensating` is reused (`handlePushRetry`): the returned
  slip keeps the existing `correlation_id`, which differs from the one the caller sent,
  so the caller (pushhookparser) detects a dedup and suppresses re-dispatch. A push for
  a SHA whose slip is `failed` or terminal
  (`completed`/`abandoned`/`promoted`/`compensated`) instead **repaves**: a single
  `SlipStore.Repave` call removes the `routing_slips` row, explicitly deletes its
  `slip_component_states`/`slip_ancestry` children (correct both before and after migration
  v5's cascade FKs exist), inserts the fresh row under the new run's `correlation_id`,
  repoints any descendant onto it, and writes the fresh run's own parent link — **all in
  one transaction** — producing a full re-dispatch (builds, unit tests, secret scan).

  The atomicity is the contract, not an implementation detail. When the delete and the
  create were separate store calls, a create failure after a committed delete left the
  commit with no slip at all and every redelivery reproduced it; the reachable trigger was
  a pipeline config deployed ahead of the migration adding its step's column (Postgres
  42703 on every insert). The statement order inside the transaction is also load-bearing:
  the successor's row is inserted BEFORE any descendant is repointed onto it, which is what
  keeps a descendant from naming a row that does not exist. The repoint rewrites every column
  describing the parent, not just the id, since a stale value beside a fresh id reproduces the
  very inconsistency the repoint removes; the column list is on `SlipStore.Repave` in
  `interfaces.go`, the contract every store implementation owes. That
  ordering is necessary but not sufficient for a foreign key on
  `slip_ancestry.parent_correlation_id`, and migration v5 adds none — both of its FKs are on
  `correlation_id`. The full argument is on `SlipStore.Repave` in `interfaces.go`.

  These two claims are stated here as conclusions with pointers rather than restated in
  full: they drifted across four and three copies respectively in three consecutive review
  rounds, so each now has exactly one home.

  Two further guarantees follow from doing it in one place: if the push resolved no
  ancestry (e.g. a GitHub outage), the superseded run's own parent link is **carried
  forward** to the successor rather than deleted with it; and the descendant repoint
  rewrites `parent_branch` and `parent_status` alongside the id, so a cross-branch repave
  no longer truncates `ResolveAncestry` at that hop (it joins the next hop on the branch
  recorded beside the parent id).

  A failed `Repave` is fatal to the push: nothing was written, so there is no successor to
  report, and failing lets Kafka redeliver against a store that still holds the superseded
  row. A store that cannot repave at all returns `ErrRepaveUnsupported`
  and the push path falls back to pre-DEVOPS-231 abandon-then-create semantics.
- **Empty-run guard.** If the incoming push will dispatch nothing, the existing slip for
  that SHA is ended, and the push does **not** carry that row's own `correlation_id`,
  `CreateSlipForPush` returns the existing slip instead of repaving — nothing would be
  dispatched either way, so repaving would only destroy the prior run's history for no
  benefit.

  "Will dispatch nothing" is `PushOptions.Dispatch` (`DispatchIntent`) when the caller
  states a *recognized* value; `DispatchIntentUnspecified` and any unrecognized value both
  fall back to `len(Components) == 0`. `Validate()` deliberately never rejects an
  unrecognized value — a safe degradation is preferable to a hard push failure — so an
  explicit-but-mis-serialized intent (wrong casing crossing the slippy-api JSON boundary,
  say) is silently ignored rather than honored. Every log line that reports a decision about an
  ended slip for this commit carries the raw value plus `dispatch_intent_honored` and
  `dispatch_intent_recognized`, so that is visible rather than inferred — both guard-applied
  (dedup) lines, both repave lines (the main path's and the duplicate-create backstop's), and the
  create line. The repave ones matter most, because that is where an ignored intent went on to
  destroy a prior run's history.

  The field set and its call sites live in exactly one place, on `addDispatchIntentFields` in
  `push.go`; `grep -n addDispatchIntentFields` is the site list. This paragraph deliberately does
  not state a count — "two fields" and "the one line" were each correct when written and each went
  stale within a commit, and doc/code drift on these counts produced a review finding in six
  consecutive rounds.

  **Alert on `dispatch_intent_recognized = false`**, not on `honored`. `honored=false` beside a
  non-empty `dispatch_intent` looks like the right signature and is not: `DispatchIntent.String()`
  renders the zero value as the literal `unspecified`, so under a Stringer-honouring logger the
  field is never empty and that predicate also matches every unset push — which, until the field
  is forwarded end-to-end, is every real push. It would match every repave and isolate nothing.
  `recognized=false` is true only for a value a caller actually got wrong, and unlike the
  rendering it does not depend on how a consumer's `Logger` serializes the field (`Logger` is an
  interface consumers implement, and `DispatchIntent` has `String()` but no `MarshalJSON`).

  Component count is neither necessary nor sufficient on its own: a tests-only repo
  (`buildable=false` + `RunUnitTests=true`) dispatches unit tests with zero build
  components, and a caller may declare `DispatchIntentNothing` while carrying components.

  Two exclusions sit alongside that, and both are easy to miss because "ended" in this
  document includes `failed`:
  - **`failed` is excluded from the INFERENCE only.** With `Dispatch` unset or
    unrecognized, the guard never claims a `failed` slip, so a componentless re-push can
    still retrigger a stuck run — the carve-out that covers the window before `slippy-api`
    and `pushhookparser` forward the field, during which every real push arrives
    `DispatchIntentUnspecified`. A **recognized** explicit intent is authoritative in both
    directions and is consulted *before* this exclusion, so `DispatchIntentNothing` on a
    `failed` slip DOES dedup — deliberately, since repaving there would destroy the failed
    run's history and seed a successor with no components that nothing could ever advance.
  - **A self-correlation push is excluded**, unconditionally. When the existing row already
    carries this push's `correlation_id`, returning it would mean `returned == sent`, which
    the caller protocol reads as "this is your slip, proceed" rather than as a dedup.

    Being excluded from the guard does **not** mean falling through to a repave. That push is
    diverted to an **in-place reset**: `persistSlipForPush` calls `SlipStore.ResetSlipInPlace`,
    which upserts the row back to live under the SAME correlation ID, keeping its component
    children and replacing its state history, and records a `reset in place after <status>
    attempt` marker so an operator can tell a reset from a first attempt. The reset decides
    under the row lock and refuses when a claimant's run is in flight (see the claim section
    below). This is the ordinary in-delivery retry, not an exotic shape — so "repaved under a
    new correlation ID" describes only pushes carrying a DIFFERENT id.

  See "the guard no longer infers intent from component count" below for the failure this
  fixed, and `DispatchIntent` in `push.go` for what setting it opts a componentless push
  into.

  A consequence that outlives the rollout: a genuinely zero-work repo correctly sends
  `DispatchIntentNothing`, the guard correctly fires, and a failed run there still cannot
  be retriggered by re-pushing the same commit. That is the guard working as designed, not
  the bug below.
- **Cross-commit supersede → abandon (unchanged).** A newer commit still `AbandonSlip`s
  an in-flight older commit on the same branch (see above) — different `(repo, sha)`, so
  this is untouched by the repave change; `abandoned` rows still exist, there is just
  never a second row for the same commit.
- **`branch` is a run attribute, not slip identity.** A fast-forward of an existing SHA
  onto a different branch is not treated specially: it repaves the same `(repo, sha)`
  row, and the fresh row carries the new push's `branch`. The re-dispatch is observably
  the same as before this change, which instead minted a second row for the same SHA
  under the old branch.
- **Note:** rewritten-history force-pushes can strand a live slip on an orphaned SHA
  (pre-existing; ancestry walks cannot see rewritten-away commits) — not addressed by
  this change.

**Known sharp edges of repave:**

- **Repave is a hard delete, and that is a recorded decision.** A repaved run's
  `routing_slips` row — and with it its `state_history` jsonb — plus its
  `slip_component_states` and `slip_ancestry` rows are destroyed. No tombstone, archive
  or retention copy exists anywhere. That was decided in DEVOPS-231 §4, and re-examined
  and confirmed on 2026-09-09 under **DEVOPS-277**, which declined both soft-delete
  (DEVOPS-230's partial-index model) and a tombstone row: nothing consumes a superseded
  run's history, and the 22 dashboard queries that read `routing_slips_pg` with no status
  filter would double-count under soft-delete. The consequence for work still in flight
  against the deleted row — an operator rerun's late step writes hitting
  `ErrSlipNotFound` — is fixed at its cause in pushhookparser (the rerunner claims the
  slip before dispatching), tracked as **DEVOPS-285**; ordinary stragglers from any
  superseded run remain a bare not-found.
- **In-flight peer steps.** `FailStep` (`steps.go`) never modifies peer steps, so a
  slip can be `failed` (e.g. `unit_tests` failed) while another step (e.g. `build`)
  still runs. Repaving deletes the `routing_slips` row those late writers target, so
  their post-job `UpdateStepWithStatus` and any in-progress `WaitForPrerequisites`
  poll (`hold.go`, which reloads the slip via `store.Load` each iteration) now fail
  with `ErrSlipNotFound`, where pre-DEVOPS-231 abandon semantics left the row in
  place to absorb them. This is deliberately NOT fixed by making not-found benign
  across `steps.go`/`hold.go` — that would mask genuine not-found bugs repo-wide —
  and, per the recorded decision in the bullet above, it is not fixed by a tombstone or
  a soft-delete either, so a late writer still cannot tell "repaved, stop quietly" from
  "genuinely missing". The one window worth closing is the operator rerun's, and it is
  being closed at its source rather than in the store — see **DEVOPS-285** below.
  The status-guarded removal (`ErrSlipWentLive`) does remove the worst case: a `failed`
  slip that recovers to `in_progress` before the repave lands is refused, so a
  recovering run is never pulled out from under itself.

  Verified mitigating context: this fails **closed**. A destroyed `secret_scan` verdict
  cannot become a deploy permission, because `AllPrerequisitesMet` requires every
  prerequisite to be `IsSuccess()` and a fresh slip starts `pending`. The cost is
  availability (a false-red post-job), not integrity.
- **Deploy-event attachment.** A same-commit re-push repaves an ended slip whose
  image may have already shipped, so a deploy event for that image now attaches to
  the current (repaved) run for that `(repo, sha)` rather than the run that actually
  built it — see `resolve.go`'s `LoadByCommit` comment for the updated contract.
  This is a deliberate contract change, not a bug: refusing to repave any ended slip
  that recorded an image tag would make re-pushing an already-built commit
  non-repaveable, which is this PR's primary case.
- **Empty-run guard consequences.** ~~In a zero-component repo, a failed run cannot be
  retriggered by re-pushing the same commit.~~ **No longer true — this was the guard's
  worst consequence and it is fixed.** With `Dispatch` unset or unrecognized — which is
  every real push until it is forwarded end-to-end — `emptyRunGuardApplies` never claims a
  `failed` slip, so re-pushing a commit whose run failed DOES repave and re-dispatch, which is
  what makes unit-test retrigger work on a tests-only repo (`buildable=false` +
  `RunUnitTests=true`) before `DispatchIntent` is adopted end-to-end. If you are
  triaging a stuck tests-only repo, re-pushing is expected to help.

  The guard also never claims a push whose correlation ID already matches the existing
  row, because its whole contract is that the caller sees `returned != sent` and
  suppresses its side effects; when they are equal the caller would instead dispatch
  against the slip it was handed.

  What remains true for the statuses the guard still claims (`completed`, `abandoned`,
  `promoted`, `compensated` with zero components): the early return skips
  `resolveAndAbandonAncestors`, so an older in-flight slip on the same branch that a
  fall-through push would otherwise have abandoned stays live. And a superseded-terminal
  slip (`abandoned`/`promoted`/`compensated`) can be returned from `CreateSlipForPush`
  for a componentless push — previously impossible, since that function always either
  reused a live slip or created a fresh one.

  **The guard no longer infers intent from component count alone.** It reads
  `PushOptions.Dispatch` (`DispatchIntent`), because "zero components" legitimately means
  both "branch create at an existing SHA, nothing to do" and "tests-only repo, unit tests
  are about to run" — only the caller can tell them apart. The old inference broke the
  second case concretely: `pushhookparser` nils out components whenever builds are skipped
  while still dispatching the unit-tester event, so the guard fired on a push that DID
  dispatch work, returned the old failed slip, and the caller's `Deduplicated` branch
  short-circuited every side effect — unit tests included. A failed unit-test run on such a
  repo therefore could not be retriggered by re-pushing the commit.

  The affected repo set and the quoted `pushhookparser` source line are deliberately NOT
  repeated here — they are point-in-time facts about another repository, so nothing in this
  repo can notice them going stale. `DispatchIntent`'s godoc in `slippy/push.go` is the
  single place that records them.

  `DispatchIntentUnspecified` is the zero value and keeps the legacy inference, because this
  library releases before `slippy-api` and `pushhookparser` adopt the field. That is exactly
  why the `failed` exclusion above is not redundant with intent: during the whole adoption
  window every real push arrives `Unspecified`, so the exclusion is what carries the
  tests-only retrigger. A *recognized* explicit intent is authoritative in both directions
  and is consulted before the exclusion.

- **An ended row may have work in flight against it (DEVOPS-285).** "Ended" includes
  `failed`, and the operator rerun flow adopts a failed slip's correlation ID and
  dispatches workflows *before* writing anything to slippy — so the row sits `failed`,
  and therefore repave-eligible, for as long as that work takes to report. A same-commit
  push in that window repaves, deletes the row, and the in-flight rerun's step writes
  then fail `ErrSlipNotFound`. This generalises the in-flight-peer bullet above:
  pre-DEVOPS-231 late events from *any* superseded run landed on the abandoned row and
  returned 2xx, and the hard DELETE turns all of them into 404s. Note the `failed`
  carve-out above widens the input surface: a componentless push onto a `failed` row now
  falls through to the guarded DELETE where it previously deduped.

  **A running step does NOT close this window.** An earlier version of this bullet said
  the row is exposed only "for as long as that work takes to report its first step",
  which is wrong and is the most likely thing for a reader to get wrong. The slip-level
  status write in `steps.go` is gated on `status.IsTerminal() && componentName == ""`, so
  a `running` step leaves `slip.status` at its previous value — including the repaveable
  `failed`, `completed` and `promoted` — for the entire time the step runs. For the
  terminal three it is worse: `checkPipelineCompletion` returns early for `completed`,
  `abandoned` and `promoted`, so no later step write ever restores liveness and the
  window never closes at all. When judging whether any adopt-then-dispatch flow is
  exposed, treat "it writes a step event first" as **no protection**.

  **Closed for the rerunner (2026-09-10), by an explicit live-status write.** slippy-api
  gained `POST /v1/slips/{correlationID}/claim`, which appends a `slip_claimed` history
  marker and — at the time (DEVOPS-285) — set `slip.status = in_progress`. `in_progress`
  is absent from `repaveableSlipStatusesSQL`, so a same-commit push arriving after the
  claim took the dedup path instead of the DELETE. Since DEVOPS-367 the claim is the flag
  described below and `status` is untouched; the dedup is on `claimed_from` instead.
  The two callers claim differently, deliberately. pushhookparser's rerunner claims once per
  rerun message, out of the ended set, before dispatching, and treats any claim failure as
  fatal — a rerun that does not own its slip dispatches nothing. The Slippy CLI pre-job
  claims per step, out of every non-terminal status, and proceeds on a 409, logging it: a
  step whose claim was refused still runs, because the claim is protection for the run, not
  a lock the step is required to hold. Verified in prod: a claim at
  `00:02:04` on a `failed` slip, and in dev the differential was observed directly — the
  same commit's push repaved while the slip was `failed` and deduplicated onto the rerun's
  correlation ID six seconds after the claim.

  The claim, as of DEVOPS-367 (goLibMyCarrier ≥ v1.4.0), is a **flag**: `claimed_from`
  set means a run is in flight against the slip — running or held steps and components, which
  is what `RunInFlight` counts. This is what makes a componentless start of an aggregate step
  count as in flight too (DEVOPS-373): before any component has reported, the write lands on
  the step's own status column, the same as a pure pipeline step's, so `RunInFlight` sees it
  through the same Steps loop. It protects running or held steps and components, and nothing
  wider: the gap between one
  step's last post-job and the next step's pre-job is not covered, and closing it is tracked
  as **DEVOPS-371** (a dispatcher-held claim). Four properties:

  - **It never writes `status`.** `SlipStore.ClaimSlip` locks the row, compare-and-sets on
    the *current* status (`if_status` on the API; a mismatch is `ErrClaimPreconditionFailed`
    with nothing written), appends the `slip_claimed` marker and sets `claimed_from` to the
    status the row had — as an audit record, not as something to restore. That
    compare-and-set is on the CURRENT status **whether or not a claim is already held**, and
    the idempotent repeat sits behind it rather than in front of it. On an **unclaimed** row
    `nil` admits any status EXCEPT one whose run has a step or component **in flight**
    (running or held): a caller that means to adopt a running run names the status in
    `if_status` (the Slippy CLI pre-job names every non-terminal status; the rerunner, which
    names only the ended set, is what the refusal protects). A slip with no status at all is
    refused. Once `if_status` agrees, a claim already held is an idempotent no-op —
    `ClaimOutcome{Claimed: false}` carrying the recorded prior, no second marker, nothing
    written — so a caller can tell its own repeat from an existing claim without weakening the
    comparison.

    **The in-flight refusal does not apply to a row that is already claimed** (PR #87, jhicks
    round, finding j-claim), so the arm order is: empty status → compare-and-set → held claim
    → in-flight refusal → fresh claim. The refusal guards *adoption*, and adoption is the
    write: on a claimed row the claim stays where it is and nothing is written on either
    ordering, so refusing there protected nothing and made the documented idempotency false
    for every caller sending a `nil` `if_status` — pre-job 1's `StartStep` puts the run in
    flight, a `nil` `if_status` names nothing, and pre-job 2 of the **same** run was refused on
    a slip its own run held. The arm that RECORDS a claim still sits behind the refusal, so a
    `nil` `if_status` still cannot claim a run that is executing.

    That refusal reads the **step and aggregate columns, not the status name** (PR #87 seventh
    review, finding j3). It used to read `IsLive() && status != pending`, which was wrong in
    both directions: `pending` was carved out as "nothing has been dispatched onto it", but a
    slip KEEPS `pending` for its whole run — `checkPipelineCompletion` only reconciles away
    from `failed` — so a pending slip with three steps running was admitted by a `nil`
    `if_status`; and `in_progress` was refused as "a live run", but between one step's post-job
    and the next step's pre-job an `in_progress` slip has nothing running at all. The evidence
    is read under the same `FOR UPDATE` as the status, by `PostgresStore.ClaimSlip`'s
    `loadClaimStateTx` — the same narrow read `ReleaseClaim` uses.

    The consequence for the rerunner's retry after a lost response, which is what the rule is
    tuned for: if nothing was dispatched the status has not moved, the ended set still
    matches, and the retry claims and dispatches; if the dispatch DID land and a **post-job**
    has reported — a terminal step status, the only write that runs `checkPipelineCompletion`
    — the reconcile branch has written `in_progress`, the ended set misses, and the retry is
    REFUSED, correctly, because the dispatch it is retrying already happened (PR #87 round 6
    reverted an earlier round that compared `if_status` against the recorded prior instead,
    which let a second rerun request dispatch onto a live run).

    **Between those two is the window `ClaimOutcome.InFlight` exists for, and it is not a
    retry at all.** A pre-job's `StartStep` writes `running`, which is NOT terminal, so
    `checkPipelineCompletion` is never reached and the slip still reads `failed` from dispatch
    until the run's first post-job — minutes, for a build. A SECOND rerun message arriving in
    that window passes the same compare-and-set and takes the same idempotent repeat arm as a
    lost-response retry, so `Claimed=false` cannot tell them apart; before DEVOPS-367's seventh
    review the rerunner dispatched a second pipeline onto the first. `ClaimOutcome` now carries
    `InFlight`, read from the same locked row, and pushhookparser's rerunner does not dispatch
    when `claimed=false` and `in_flight=true`. **Known residual, not closed:** between a
    claimant's claim and its pre-job's `StartStep` nothing is running, so two rerun messages
    arriving in *that* window both read `InFlight=false` and both dispatch. Closing it needs a
    per-message claim identity carried end to end — the rerunner sends a constant `claimedBy`,
    and `claimedBy` is audit only — which is a library, API and parser change.

    The status column stays the pipeline's alone, so
    `checkPipelineCompletion`'s terminal bypass keeps protecting `completed` and `promoted`
    even when they are claimed.
  - **While it is held, the row cannot be repaved.** `Repave` refuses a row with
    `claimed_from` set exactly as it refuses a live one (`ErrSlipWentLive`), and the push
    path deduplicates onto a claimed slip before it even resolves ancestry. This holds
    whatever `status` says: a claimed rerun of a `failed` slip still reads `failed`, and a
    step failure mid-run changes nothing about the claim. `Create` and the full-row
    `Update` never write `claimed_from` (SELECT-only), so a stale snapshot cannot clear it.
    Migration v6's down refuses while any claim is held.

    **One exception, added by PR #87's seventh review (finding p1):** a push bearing the
    claimed row's OWN correlation ID is that delivery's in-delivery retry rather than another
    run, and it resets the row **in place** — an upsert that rewrites every step and aggregate
    column and the state history — when the run is **quiescent**. It does NOT when a step or
    component is in flight; then the dedup applies as to any other claimed row, because the
    reset would destroy the state that run is writing under an unchanged correlation ID. Both
    guard paths spell it `claimed && (different id || RunInFlight)`. Because the reset replaces
    `state_history` while `claimed_from` survives it, the reset also re-states the
    `slip_claimed` marker (finding p2): **a `claimed_from` that is SET always has a
    `slip_claimed` marker**, which is what pushhookparser's stranded-cleanup exemption — keyed
    on the marker, not the column — depends on. Stated in that direction deliberately: the
    converse does not hold, because `UpdateSlipStatus` clears the column on a terminal status
    and appends no marker, leaving marker-present/column-absent. That direction is fail-safe —
    `IsTerminal` and the parser's `isLiveSlipStatus` are exact complements over all eight
    statuses, so such a row returns at the live-status gate before the claim gate — whereas
    column-present/marker-absent is the fault this whole path exists to prevent. An abandon or promote is the
    exception: both are terminal statuses written from outside the run, so they end the claim
    even while steps are still running, and both are repaveable — an ancestor abandon or a
    promotion deliberately overrides a live claim.

    **A reset writes only onto an UNCLAIMED row, decided under the row lock the reset itself
    takes** (DEVOPS-367, PR #87). The reset is `SlipStore.ResetSlipInPlace`, one transaction
    that re-reads the target row `FOR UPDATE`, runs the shared `DecideReset`, and either
    performs the upsert or refuses with `ErrSlipClaimed` having written nothing. The refusal
    covers a quiescent claim as well as an in-flight one: a claim records no step until its
    run's first post-job, so a claimed row with nothing running is a dispatched run sitting in
    the queue, not an absent one. Allowing that case let a redelivery wipe a live rerun, because
    the rerunner claims under the ORIGINAL push's correlation ID and so a self-correlated row is
    routinely someone else's. It replaced a bare `Create` — an unlocked upsert — gated on an
    unlocked `LoadByCommit`, with `resolveAndAbandonAncestors`' GitHub round trips in between:
    a row read unclaimed and quiescent could be claimed and start executing in that window, and
    the upsert then kept `claimed_from`, replaced `state_history` (leaving the column set with
    the marker gone, so the row lost its stranded-cleanup exemption) and rewrote the step
    columns of a run that was in flight. The push's snapshot check survives as a fast path that
    decides which route to attempt — a dedup decided there skips ancestor resolution entirely —
    while the locked read decides what actually happens. On a refusal both in-place reset arms
    **deduplicate onto the live row** rather than failing the push: the claimant's run owns the
    slip and the desired end state, one run for this commit, already holds. A store that
    cannot take the lock returns `ErrResetUnsupported` and the push falls back to a plain
    `Create`, which loses nothing there — with no `claimed_from` column there is no claim to protect.
  - **It ends when the run is over, and only then.** Two ways: a **terminal status write**
    through `UpdateSlipStatus` — the ONE write path that ends a claim, which `AbandonSlip`,
    `PromoteSlip` and `checkPipelineCompletion` all take — clears it, because terminal ends
    the run; or a **release**. `SlipStore.ReleaseClaim` (`POST /v1/slips/{id}/release`) reads
    the claim state — the claim, the status and every step and aggregate column, and nothing
    else — `FOR UPDATE` and clears the claim only if no step or component is running or held;
    otherwise it returns `ReleaseOutcome{Released: false}` with nothing written, which is
    information rather than an error (all but the last of a run's N post-job releases take
    that arm). Each post-job must write its own step's terminal status before it releases, or
    it counts itself as in flight and no post-job of the run ever clears the claim.
    `push_parsed`, the library's own bookkeeping step, never counts as in flight. Every
    post-job releases on exit, so the last one — the one that finds nothing in flight —
    clears it. A release never writes `status`. Nothing else ends a claim: not `Create`, not
    the full-row `Update` (whatever status either carries — that status is the caller's own
    snapshot, and a claim may have been taken since it was read), not a `failed` write, not
    the reconcile branch's `in_progress`, not the passage of time. There is no claim owner;
    `claimed_by` and `released_by` are audit strings.
  - **Recovery from a dead run is the workflow exit hook, never a clock.** A workflow that
    dies mid-step leaves its step `running`, which blocks release until Argo's
    workflow-level `hooks.exit` runs `slippy-post-job` and writes the step's result (every
    slip-routed template carries the hook; `prod-gate` and `secretscan` gained theirs under
    DEVOPS-367); the post-job's `slip-post` step retries transient API failures (exit 75).
    No component decides a run is dead by elapsed time: a long build is indistinguishable
    from a wedge by status, so any bound would either repave live runs or be too long to
    matter. When the exit hook cannot run at all — the cluster is gone, the workflow was
    deleted — **the stuck step is what holds the claim**, so the remedy that works in every
    state is to resolve it (`POST /v1/slips/{id}/steps/{step}/complete`, or fail or skip it)
    and then `POST /v1/slips/{id}/release`, which now finds nothing in flight. On a
    **non-terminal** slip `POST /v1/slips/{id}/abandon` also ends the claim, because
    `AbandonSlip` writes the terminal `abandoned` through `UpdateSlipStatus` and `abandoned`
    is in `repaveableSlipStatusesSQL`. On an **already-terminal** slip it does not:
    `checkTerminalStatus` returns early and `AbandonSlip` returns nil without writing (I4), so
    the operator sees success and the claim survives — and terminal-claimed rows are ordinary,
    since the rerunner claims out of the ended set, four of whose statuses are terminal. Use
    the step-then-release route there.
  - **A claim with nothing in flight is reapable, by two routes — one operator, one
    automatic and NARROW.** The state that has no post-job to end it is a claim taken by a
    pre-job whose workflow was then never dispatched: `claimed_from` is set, no step was ever
    reported, `RunInFlight` is false — a release WOULD clear it — but no post-job will ever
    run to call one, and every later same-commit push deduplicates onto the row.
    - **Operator, works in every case:** `POST /v1/slips/{id}/release`. With nothing in
      flight it clears the claim on the first call — there is no stuck step to resolve first,
      because no step was ever reported. (The step-then-release route above is for the other
      shape: a claim held open by a step left `running` or `held`.)
    - **Automatic:** pushhookparser's stranded-slip cleanup, which used to skip a claimed
      slip outright and now exempts one only while the claim is doing something — a step or
      component running or held, the same `RunInFlight` evidence a release decides on. Be
      precise about its reach: its claim gate sits AFTER its live-status gate and its
      `failed` carve-out, so what it actually reaps is a claimed, quiescent slip at
      `pending`, `in_progress` or `compensating`, for a commit a force-push or branch delete
      made unreachable, on the slip's own branch, with `SLIPPY_STRANDED_CLEANUP` armed. That
      flag is **off by default in the deployed parser** (`StrandedCleanupEnabled` is
      `env == "true"`); pushhookparser#56 (DEVOPS-342) inverts the default and is **open and
      unmerged**, so today this route runs only where an operator armed it.
      A claimed quiescent **`failed`** slip — the rerunner's usual adoption —
      returns at the `failed` carve-out, and a claimed **terminal** one at the live-status
      gate; neither is reaped. Those are the operator route's cases.
    The library adds NO time-based sweeper of its own — elapsed time cannot tell a long build
    from a wedge, which is why the in-flight evidence, not a clock, is what the exemption
    reads (DEVOPS-367, PR #87 finding 3).

  What the claim does not cover, stated plainly (tracked as **DEVOPS-371**, a
  dispatcher-held claim): the **gap between two workflows of one run** — after the last
  post-job of one phase releases and before the next phase's pre-job claims (an Argo sensor
  dispatch: seconds to minutes) — is a stretch with no claim held.
  When the row also sits at a repaveable status — the `failed`-rerun case the claim was
  built for, not the healthy run whose row sits at `in_progress` and is protected by
  `IsLive()` — a same-commit push in that gap repaves and starts the commit over, and the
  later workflow's pre-job then fails to resolve its correlation id and exits. That is the
  pre-DEVOPS-285 behaviour for that window and is accepted: the claim protects work that is
  *in flight*, not work that has not started. A step left `running` by a run that never
  reports (an `argo terminate`, a lost cluster) holds the claim until something writes that
  step; a same-commit push deduplicates rather than repaving, and an operator rerun claims
  again as long as the row still reads an ENDED status — its claim is then the idempotent
  no-op. If the stuck run drove the row to `in_progress`, the rerunner's ended-set
  `if_status` no longer matches and its claim is refused; that refusal is the point (a
  dispatch has already run against this slip), and the recovery is the same one the stuck
  step needs — resolve the step, then release. The gap is **per step, not per run**: a step never reported at all is
  `pending` and holds nothing — which is how the CLI's client-side poll of the read-only
  prerequisites endpoint leaves a step waiting on prerequisites, whereas the library's own
  `WaitForPrerequisites` writes `held` as its first action and so does hold the claim — so
  once the concurrent work finishes, the first post-job to exit clears the claim even though
  later steps of the same run are still to be dispatched.

  **A known resting state, pre-existing:** a partial clean rerun (some steps reset, the run
  not carried to an end) leaves the row at `status = in_progress` by way of the reconcile
  branch, unclaimed, with nothing running — which is not repaveable (`IsLive()`) and is also
  refused by the rerunner, whose `if_status` names only the ended set. Nothing in the claim
  work creates or clears that state; only a terminal status write or an operator moves the
  row out of it.

  **Residual, narrowing:** ordinary stragglers from any superseded run still get a
  not-found on write, now with a message that names the likely repave. The ~13
  `slip-routed` templates that adopt a correlation ID route through one shared
  `slippy-pre-job` step; Slippy#28 claims there (out of every non-terminal status, after
  `StartStep`, and releases in every post-job), and is gated on DEVOPS-367 being deployed to
  the same environment first.

- **No duplicate detection before migration v5.** Without the `uq_routing_slips_repo_sha`
  unique index (migration v5, Phase B), an insert for the same `(repository, commit_sha)`
  never conflicts on anything but `correlation_id`, so `ErrDuplicateSlip` — and therefore
  `handleDuplicateSlipBackstop` — is unreachable. A lost Redis-lock race (the dedup lock
  is fail-open) silently inserts a second row for one commit with no detection at all,
  in any environment where the Phase B cleanup has not run and v5 is not yet applied.

  Migration v5 (`one_slip_per_commit`, `postgres_migrations.go`) adds that index plus
  `ON DELETE CASCADE` FKs from `slip_component_states` and `slip_ancestry` on
  `correlation_id`; it adds no FK on `parent_correlation_id` (above). It is gated per
  environment, by construction rather than by a flag: the FK adds validate existing rows
  and the index build fails on duplicates, so v5 fails loudly — and the migrator's
  per-migration transaction rolls it back — until the one-time cleanup script
  (`DEVOPS-231-cleanup-one-row-per-commit.sql`, operator-run, deliberately not a
  migration step) has brought that database to one row per commit and zero orphan child
  rows. That script in turn must run only after the repave-capable slippy-api
  (goLibMyCarrier ≥ v1.3.100) is deployed there: the index must never be live under a
  pre-repave writer, whose failed-path created a second row per commit. Survivor rule:
  non-terminal row first, then `updated_at`, `created_at`, `correlation_id`; losers are
  hard-deleted (DEVOPS-277 — accepted history loss, no archive). v5 is idempotent by name
  (`duplicate_object` swallowed, index `IF NOT EXISTS`) but asserted by shape: each half ends
  in a post-condition that RAISEs unless the object it kept has exactly the expected
  definition, so a pre-existing same-named FK or index of another shape fails the migration
  instead of being recorded as v5.

  What Phase A *does* have, since `Repave` became transactional, is convergence on repave
  failure: nothing is written, the push fails, and the redelivery repaves the still-present
  superseded row. The earlier delete-then-create shape had no such property — a failed
  delete left a stale row beside a fresh one, and a failed create destroyed the run.

**Terminology — two mutually exclusive retrigger mechanisms:**
- **Push-shaped retrigger** ("webhook re-delivery" / "same-commit re-push"): any push
  event for a SHA that already has a slip, handled by `CreateSlipForPush` above (repave,
  retry-reuse, or the empty-run guard). This is the only create/repave path.
- **`retrigger-ci`** (the operator workflow that resolves and re-dispatches an existing
  slip's steps, `action:"rerun"`): reuses the existing `correlation_id` and re-runs
  steps in place — a `failed` slip recovers via `checkPipelineCompletion`'s recovery
  branch (`executor.go`), not via a new push. It never calls `CreateSlipForPush`
  and so never creates or repaves a slip; selective (e.g. unit-tests-only) retrigger must
  never be implemented as a filtered push replay, since repave would delete the build
  state such a retrigger wants to keep.

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

> **Note (DEVOPS-314):** the diagram predates the canary steps. `preprod_rollback_test` sits between `preprod_tests` and `PROD_GATE` (a prereq of `prod_gate`); `prod_canary` sits between `prod_release_created` and `prod_deploy` (a prereq of `prod_deploy`). Both are usually `skipped`, which satisfies prerequisites. See "Canary Steps (DEVOPS-314)".

> **Note:** Prod steps do NOT become `aborted` synchronously when `prod_gate=failed`. Each transitions to `aborted` only when its own `WaitForPrerequisites` runs (`hold.go:83-111`). Steps that never enter pre-job stay `pending`. The recovery branch of `checkPipelineCompletion` (`executor.go`) only resets steps actually in `aborted` — vacuous if none ever transitioned.

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
| `preprod_rollback_test=running` | `in_progress` | after `preprod_tests=completed` (or `skipped` by the release workflow) | `failed` - blocks `prod_gate` |
| `preprod_deploy=failed` | `failed` | post-job | `preprod_tests` aborts if waiting; `prod_gate` blocked |
| `preprod_tests=failed` | `failed` | TestEngine RunPostExecution | `prod_gate` blocked until resolved |
| `preprod_rollback_test=failed` | `failed` | post-job | `prod_gate` blocked until resolved |

> ⚠️ `preprod_tests` can run against a failed or restarted deployment (PROJECT_STATE.md - discrepancy #9).
> `prod_gate` has no awareness of which deployment the test results belong to.

---

### PROD_GATE

| | |
|---|---|
| **slip.status** | `in_progress` |
| **Prereqs** | `preprod_deploy=completed` AND `preprod_tests=completed` AND `preprod_rollback_test` terminal-success (`completed` or `skipped`) |
| **Running** | `prod_gate` (is_gate=true) |
| **On success** | All downstream prod steps unblocked; pipeline continues to `PROD_RELEASE` |
| **On failure** | `slip=failed` (FailStep only flips `prod_gate` + `slip.status`). Downstream steps NOT immediately aborted — each self-aborts lazily when its own `WaitForPrerequisites` observes `prod_gate=failed` (`hold.go:83-111`). Steps whose pre-job never runs stay `pending`. |
| **Recovery** | Re-run `prod_gate` → success → cascade steps reset to `pending` → `prod_gate=completed` must then unblock each downstream step individually as they restart |

---

### PROD_RELEASE

Steps run sequentially within this phase (each unblocks the next):

```
prod_gate=completed
    └─► prod_release_created=running ─► completed
            └─► prod_canary=running ─► completed | skipped (prereq: prod_release_created)
                    └─► prod_deploy=running (prereqs: prod_gate + prod_release_created + prod_canary)
                            └─► prod_tests=running (prereqs: prod_gate + prod_deploy)
```

| | |
|---|---|
| **slip.status** | `in_progress` |
| **Failure at prod_release_created** | `slip=failed`; `prod_canary`, `prod_deploy`, `prod_tests`, `prod_alert_gate`, etc. blocked (prereqs not met) |
| **Failure at prod_canary** | `slip=failed`; `prod_deploy`, `prod_tests`, `prod_steady_state` blocked until a retry makes `prod_canary` `completed` or `skipped` (see Canary Steps for library vs CLI cascade) |
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
| `preprod_tests` failed | `failed` | `preprod_rollback_test`, `prod_gate` | `preprod_rollback_test` (if waiting) | Re-run `preprod_tests` |
| `preprod_rollback_test` failed | `failed` | `prod_gate` and, through it, all production steps | `prod_gate` (library path: hold returns `ErrPrerequisiteFailed`, `prod_gate` -> `aborted`); CLI/Argo path: prod-gate pre-job exits non-zero, `prod_gate` stays `pending` | Re-run `preprod_rollback_test` |
| `prod_gate` failed | `failed` | All production steps | `prod_release_created`, `prod_canary`, `prod_deploy`, `prod_tests`, `prod_alert_gate`, `prod_rollback`, `prod_steady_state` | Re-run `prod_gate` |
| `prod_release_created` failed | `failed` | `prod_canary`, `prod_deploy`, `prod_tests` | None (prereqs not met - they stay pending) | Re-run `prod_release_created` |
| `prod_canary` failed | `failed` | `prod_deploy`, `prod_tests`, `prod_steady_state` | `prod_deploy` (library path, if in WaitForPrerequisites); on the Slippy CLI path nothing is written and `prod_deploy` stays `pending` | Re-run `prod_canary` |
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
  cascade-aborted (`aborted`) steps → reset to `pending` automatically by `checkPipelineCompletion`'s recovery branch (`executor.go`). `aborted` is the ONLY reversible terminal step status; `failed`, `error`, `timeout`, `completed`, `skipped` are not auto-reset. Peer steps in `running`/`held`/`pending` are NEVER modified by FailStep — only the failing step's own row and `slip.status` change synchronously.
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
                    └─► preprod_rollback_test (skipped unless canary repo)
                            └─► prod_gate (after all preprod steps done)
                                    └─► prod_release_created
                                            └─► prod_canary (skipped unless canary repo)
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
| `PROD_RELEASE` | Monitors prod_gate → prod_release_created → prod_deploy → prod_tests sequentially (tracks only these four: `release_stage.go:79-83,120,174`; `prod_canary` and `preprod_rollback_test` are NOT tracked, so their time counts against its 10m prod_gate / prod_release_created / prod_deploy waits, set at `main.go:240-242` from `DefaultDeployTimeout` (`internal/config/config.go:30`); time spent in the step-0 skip jobs also counts against the prod activation wait (`release_stage.go:160-165`, `WaitForActivation`), raised to 8m in auto-deployer#17 (open); follow-up DEVOPS-318/319, bd mycarrier-we5c); does NOT auto-retry |
| Failures | Does NOT auto-retry prod_deploy or prod_gate |

---

## Algorithm Reference

### `checkPipelineCompletion` Pseudocode

**Location:** `executor.go` (`checkPipelineCompletion`)
**Triggered by:** terminal event on a pure pipeline step (guard: `IsTerminal() && componentName == ""`)

```
checkPipelineCompletion(ctx, correlationID):

  slip = store.Load()   // step statuses are the materialized <step>_status columns (I5 scope: see above)

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
| Pure pipeline | `""` | `unit_tests`, `dev_deploy`, `prod_gate` | `updateStepTx`: upsert the pipeline-level component-state row, then `writeStepStatusColumn`, in one transaction |
| Aggregate | `""` (rollup; before any component reports: writes the step column, DEVOPS-373) | `builds` | `updateStepTx`: `recomputeAggregate` over the component rows |
| Component | `"mc.x.y"` | individual build | `updateStepTx`: upsert the component row, then `recomputeAggregate` for its aggregate step |

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
| `aborted` | Yes* | - | ✅ cascade | Cascade - upstream prereq failed. *Reversible: auto-reset to `pending` by `checkPipelineCompletion`'s recovery branch. |

---

## Canary Steps (DEVOPS-314)

Two config-driven steps (Postgres columns `preprod_rollback_test_status`, `prod_canary_status`, added by slippy-migrator before slippy-api restarts; ProbeSchema fails otherwise). The slippy-migrator Job does not re-run on a Vault edit: trigger it explicitly before restarting slippy-api:

| Step | Prereqs | Added to |
|------|---------|----------|
| `preprod_rollback_test` | `preprod_tests` | `prod_gate` prereqs |
| `prod_canary` | `prod_release_created` | `prod_deploy` prereqs |

### Skip-writer contract

- **Status:** introduced by DEVOPS-314 in workflow-dev/workflow-core `create-github-release` step 0 (planned; lands before the config change), not the Slippy library.
- **Ordering invariant:** the skip writer must be live in the workflows before any Slippy config (Vault `#config-dev` / `#config`) gains these steps; otherwise non-canary releases hold `prod_gate`/`prod_deploy` for 60m and fail. The example JSON files carry no skip semantics.
- **Fail-open:** missing/empty custom properties are treated as non-canary, so both steps are skipped (by design, decisions #2/#9: default = rolling). A canary repo whose properties fail to arrive therefore deploys without a canary. A consistency guard between `deployment-strategy` and helm `deploymentType: rollout` is DEVOPS-310's.
- **When / rule:** it marks both steps `skipped` unless the repo custom property `deployment-strategy` equals `canary`. Absent or any other value means skip. Canary repos leave them `pending` for their own workflows to run. No such workflow exists yet (the canary runner is DEVOPS-319, which records `prod_canary`; the rollback-rehearsal driver is DEVOPS-318, which drives `preprod_rollback_test`), so until they land, setting `deployment-strategy=canary` on a repo leaves both steps `pending`, its `prod_gate` holds for 60m, and every release of that repo fails.
- **Why it matters:** `skipped` satisfies prerequisites (`status.go` IsSuccess, `prereqs.go`). A `pending` canary step blocks promotion, so the skip must be written before `prod_gate` on non-canary repos.
- The skip API accepts any state and overwrites terminal statuses, including a `failed` `prod_canary`. `SkipStep` has no status guard, `skipped` counts as success (`IsSuccess`), and retry recovery resets only `aborted` steps. So a re-run release whose custom properties fail to arrive (fail-open) would turn a `failed` `prod_canary` into `skipped` and unblock `prod_deploy`/`prod_gate`, and nothing would revisit the failure. Follow-up: the skip writer or `SkipStep` must refuse to overwrite `failed` on canary steps (ticket: DEVOPS-319, the runner that records `prod_canary`). Flipping the property to non-canary and re-running is the one intended recovery.

### Checklist item 7 trace

- **Cascade scope, `prod_canary` failed:** primary failure, so `slip=failed`. Library path: `prod_deploy`'s hold aborts it; `prod_tests` and `prod_steady_state` stay `pending` until their own holds (blocked until `prod_canary` is `completed` or `skipped`). A retry that completes `prod_canary` resets the aborted steps to `pending` and the slip to `in_progress`.
- **`preprod_rollback_test` failed:** same library/CLI split (see below): `prod_gate` -> `aborted` (library) or stays `pending` (CLI). Blocks `prod_gate` and, through it, everything downstream including `prod_steady_state`.
- **`prod_steady_state` reachability:** `prod_release_created -> prod_canary (skipped|completed) -> prod_deploy -> prod_tests -> prod_steady_state`. The example files list only `[prod_deploy, prod_tests]` as `prod_steady_state` prereqs; the live Vault config also requires `prod_alert_gate` (example files drift), so live reachability additionally depends on the alert-gate branch.
- **Phases:** nothing upstream of the new steps changed (`dev_deploy` is unchanged; its prereqs are `[builds]` in `production.json` and the live config, but `[builds, unit_tests, secret_scan]` in `slippy/default.json`, a pre-existing drift), so DEV and CI_PARALLEL are unaffected (rule 8).

### Library vs CLI cascade

The "aborted automatically" cascade holds only in the library: `WaitForPrerequisites` calls `AbortStep` on a failed prereq (`hold.go:83-111`). The Argo path uses the Slippy CLI, which polls read-only `GET step-prerequisites` and never writes `aborted`. There the downstream step stays `pending` and the pre-job exits non-zero. Retry still works because the recovery reset only touches `aborted` steps. Invariant tests prove the library semantics only (`slippy/state_machine_canary_steps_test.go`).

---

## Code Validation Guide

When reviewing any change to `goLibMyCarrier/slippy/` or any caller (`Slippy/ci/`, `MC.TestEngine/`, `auto-deployer/`, workflow templates), use this checklist. The machine-readable version of these rules is `slippy/state_machine_invariants_test.go` (I1–I4 invariant tests).

### Validation Checklist

1. **`checkPipelineCompletion` call path** - a `checkPipelineCompletion` call MUST fire after every terminal step event on a pure pipeline step (`componentName == ""`). Flag any new caller that calls `CompleteStep`/`FailStep` directly for aggregate/component steps without also calling `RunPostExecution`.

2. **`checkPipelineCompletion` internal order** - the algorithm MUST follow: (a) completed short-circuit, (b) scan primaryFailures and cascadeFailures, (c) primaryFailures check FIRST → failed, (d) prod_steady_state check SECOND → completed, (e) recovery check THIRD. Flag any reordering of steps (c) and (d).

3. **Component-state row and step column in one transaction** - a step write MUST update `slip_component_states` and `routing_slips` in the same transaction (`updateStepTx`). Flag any change that writes one outside the other's transaction.

4. **Slip status at creation** - `initializeSlipForPush` MUST set `Status: SlipStatusInProgress`, not `pending`.

5. **Recovery conditions** - recovery (`failed` → `in_progress`) requires BOTH: `slip.Status == SlipStatusFailed` AND `len(primaryFailures) == 0`. Flag any change that triggers cascade reset without verifying both conditions.

6. **`WaitForPrerequisites` in new callers** - any new integration that calls `StartStep` (pre-job) MUST either call `WaitForPrerequisites` first, or document the explicit assumption about why prereqs are guaranteed at call time.

7. **Pipeline config changes** - for any new step or prerequisite change, trace the cascade abort scope and verify `prod_steady_state` terminal path is still reachable. Verify `dev_deploy` prereqs remain `[builds]` only (adding `unit_tests`/`secret_scan` breaks CI_PARALLEL → DEV independence). Worked example: see "Canary Steps (DEVOPS-314)" below.

8. **Pipeline phase impact** - identify which pipeline phase(s) the change touches (STATE_MACHINE_V3.md phases) and verify phase transition behaviour is preserved. Flag any change where the high-level phase flow would need to be redrawn but hasn't been updated.

9. **(Removed with the ClickHouse store, DEVOPS-343.)** This rule governed the ClickHouse INSERT SELECT writers (`stepStatusOverride`, CLONE_DERIVED). PostgresStore writes step columns with plain UPDATEs inside the slip's row-locked transaction, so there is no clone to override.

10. **Component-state rows are the source of truth** — `Load` returns the materialized `routing_slips.<step>_status` columns, which match the component-state rows only within I5's scope (see the note on invariant scope). A decision that needs the authoritative per-step state reads the step's `slip_component_states` rows. Never query `<step>_status` ad hoc.

### 4 Most Common Violations

**Violation 1 (I1 - indirect):** `CompleteStep`/`FailStep` called directly for `componentName!=""` without `RunPostExecution` → `slip.status` will not update after build component events. `slip` stays `in_progress` when builds fail (CI_FAILED never reached). Rule: `STATE_MACHINE.md §6`.

**Violation 2 (I3):** `checkPipelineCompletion` order changed - `prod_steady_state` check placed before primary failures scan → pipeline can be marked `completed` despite having failed steps. Rule: `STATE_MACHINE.md §5` - algorithm order.

**Violation 3 (persistence):** a step write that updates `routing_slips` and `slip_component_states` in separate transactions → a crash between the two leaves the column and the row disagreeing (I5). Rule: checklist item 3.

**Violation 4 (phase independence):** New prerequisite added to a step that breaks phase independence - e.g., adding `unit_tests` to `dev_deploy` prereqs couples DEV TRACK to CI_PARALLEL completion. Rule: `STATE_MACHINE_V3.md` - DEV + PREPROD PARALLEL phase.

**Violation 5 (I5):** removed with the ClickHouse store (DEVOPS-343). It described the ClickHouse INSERT SELECT stale-clone race, which PostgresStore's transactional writes cannot produce.

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
| `TestStateMachine_I5_AtomicStatusUpdateRespectsStepOverride` | I5 | `FailStep` on a running step: `slip.status=failed` and the failing step column keeps its authoritative value. Mock-based. |
| `TestStateMachine_I5_StaleStepColumnNotPropagated` | I5 | Sequential terminal events (`FailStep` → `CompleteStep` → `FailStep`) do not revert earlier step columns to stale values, and slip.status stays failed while any primary failure remains. Mock-based. |

Run with: `go test -run TestStateMachine ./slippy/...`

Failing tests indicate an invariant violation and MUST be resolved before merging.
