# DEVOPS-231 — One slip per (repository, commit_sha): repave design

> **⚠️ SUPERSEDED IN PART — review of PR #82 (goLibMyCarrier, DEVOPS-231) changed
> several decisions this spec records as "approved." This document is kept as a
> historical record of what was designed and approved at the time, NOT re-edited to
> match what shipped. Concretely: §6.1's one-arg unguarded `DeleteSlip(ctx,
> correlationID)` shipped instead as `DeleteSlip(ctx, correlationID,
> successorCorrelationID)`, gained an ended-status guard (returns `ErrSlipWentLive` if
> the slip is no longer ended), and repoints descendant `slip_ancestry` links to the
> successor transactionally instead of leaving them dangling. §7's "accept dangling
> `parent_correlation_id`, no repointing" stance was replaced by that repointing. §6.4's
> instruction to drop the `ORDER BY updated_at DESC` tiebreak from `LoadByCommit`/
> `LoadLiveByCommit` was reverted — it is retained (see inline notes below). Treat
> `slippy/interfaces.go`, `slippy/postgres_store_updates.go`, and
> `slippy/postgres_store.go` as the source of truth, not this document.

**Status:** approved design, ready for implementation planning
**Tickets:** [DEVOPS-231](https://linear.app/mycarrier/issue/DEVOPS-231) (this work),
[DEVOPS-230](https://linear.app/mycarrier/issue/DEVOPS-230) (originating analysis, closed as duplicate)
**Decided:** 2026-08-19, after a verified code-trace across goLibMyCarrier, pushhookparser,
Slippy-api, workflow-core, admin, and slippy-find. A condensed operational summary lives in
Slippy-api's `CLAUDE.md` ("Slip Identity Model").

## 1. Problem

`routing_slips` (Postgres, `ci` database) has no DB-level uniqueness on
`(repository, commit_sha)`. Production data (2026-08-10): 12,561 rows, 67 commits with
multiple rows (140 rows involved, 73 surplus; 57 same-branch re-push shapes, 10
cross-branch). Zero commits have two *live* slips — that invariant is enforced only by
app logic (`LoadLiveByCommit` + a fail-open 120s Redis `repo:sha` lock in slippy-api).

The multi-row model forces every commit-keyed reader to answer "which of the N rows is
real" (`ORDER BY updated_at DESC LIMIT 1` tiebreaks, `LoadByCommit` vs
`LoadLiveByCommit`), and produced the stale-row-pick bug class (PR #71, DEVOPS-207).
The storage-level reason a slip could not be reset in place — ClickHouse VCMT version
collapse — is gone with the Postgres migration (DEVOPS-127).

## 2. Decision

Move to **one row per `(lower(repository), commit_sha)`**, enforced by a DB unique
index, with cascade FKs from the child tables. On a push-shaped retrigger of an
**ended** slip, **repave**: delete the row (children cascade) and create a fresh one
with the new run's `correlation_id`.

**Mental model:**

- `(repository, commit_sha)` = **the slip** — stable identity, the unique key.
- `correlation_id` = **the current run** — fresh per repave; stays the PK and the
  cascade anchor for the current run's child rows.
- `branch` = an attribute of the **current run**, not of the slip's identity.

### Rejected alternatives (for the record)

- **Partial unique index on live rows only** (DEVOPS-230 Option A): enforces "one live
  slip" but keeps the multi-row model and the whole which-row-is-real bug class.
  Acceptable as a stopgap; not the end state.
- **Reuse/reset in place**: cannot re-dispatch. The caller's build-vs-suppress
  decision is `Deduplicated = returned_id != sent_id`
  (pushhookparser `pkg/slippy/http_client.go`); keeping the old id means every
  retrigger is seen as a dedup and suppressed — the PR #73 regression. Updating the PK
  in place to fix that is delete+recreate with extra steps.
- **Unique on (repo, branch, sha)**: readers are commit-addressed (image tags encode
  the SHA), so cross-branch duplicates would re-open the ambiguity — and it still
  requires the repave code for the 57 same-branch shapes, while newly permitting two
  live pipelines for one commit.

## 3. Schema changes (goLibMyCarrier migration, Postgres)

> **⚠️ Phase B implementation note (PR #82 review):** the "no cascade on
> `slip_ancestry.parent_correlation_id`" call below still stands, but for a reason
> beyond what was known when this was written. Shipped `DeleteSlip`
> (`slippy/postgres_store_updates.go`) repoints descendant `slip_ancestry` rows'
> `parent_correlation_id` to `successorCorrelationID` *before* the successor's
> `routing_slips` row exists — `Create` is a separate, later call (§6.5). An FK from
> `slip_ancestry.parent_correlation_id` to `routing_slips(correlation_id)` would reject
> that repoint (the referenced row isn't there yet) and break every repave with a
> descendant. If this column ever gains a real FK, the repoint-then-create ordering
> must change first (e.g. defer the FK check, or create-then-repoint-then-delete).

New versioned migration in `slippy/postgres_migrations.go` (current latest: v4 — the
new work is the next version; confirm the number at implementation time). Order within
the migration matters: FKs first, unique index last (see §5 rollout).

```sql
-- children cascade when a slip row is deleted (repave = one DELETE)
ALTER TABLE slip_component_states
  ADD CONSTRAINT fk_component_states_slip
  FOREIGN KEY (correlation_id) REFERENCES routing_slips(correlation_id) ON DELETE CASCADE;

ALTER TABLE slip_ancestry
  ADD CONSTRAINT fk_ancestry_slip
  FOREIGN KEY (correlation_id) REFERENCES routing_slips(correlation_id) ON DELETE CASCADE;

-- one row per commit, case-insensitive to match every reader's lower() comparison
CREATE UNIQUE INDEX uq_routing_slips_repo_sha
  ON routing_slips (lower(repository), commit_sha);
```

- **Plain `CREATE UNIQUE INDEX`, not `CONCURRENTLY`** — the table is ~12.5k rows; a
  plain build is near-instant and runs inside the migrator's normal transactional
  flow. (`CONCURRENTLY` cannot run in a transaction and buys nothing at this size.)
- **No cascade on `slip_ancestry.parent_correlation_id`** — deleting a parent run must
  not delete a child's lineage row. It stays a plain column and may dangle (§7).
- Adding the FKs validates existing data — the cleanup script (§4) must run first in
  any environment that has orphans/dupes.

## 4. One-time data cleanup (standalone script, not the migrator)

Per the DEVOPS-127 convention (migrator Job = schema only), cleanup ships as a
standalone script alongside the existing `DEVOPS-127-*.sql` operational scripts, run
manually per environment before the migration release. Order:

1. **Delete orphaned child rows** — `slip_component_states` / `slip_ancestry` rows
   whose `correlation_id` has no `routing_slips` row (required or the FK add fails).
2. **Dedupe to one row per commit.** Survivor selection: **the non-terminal row if
   any, else latest `updated_at`**. (`failed` is non-terminal/recoverable and must win
   over `abandoned`. Prod data confirms each duplicate set has at most one
   non-terminal row.) The script runs **before** the FKs exist (they arrive with
   release B), so it must delete the losers' child rows explicitly:

```sql
CREATE TEMP TABLE losers AS
SELECT correlation_id FROM (
  SELECT correlation_id,
         row_number() OVER (
           PARTITION BY lower(repository), commit_sha
           ORDER BY (status NOT IN ('abandoned','promoted','compensated','completed')) DESC,
                    updated_at DESC, created_at DESC) AS rn
  FROM routing_slips
) r WHERE rn > 1;

DELETE FROM slip_component_states WHERE correlation_id IN (SELECT correlation_id FROM losers);
DELETE FROM slip_ancestry         WHERE correlation_id IN (SELECT correlation_id FROM losers);
DELETE FROM routing_slips         WHERE correlation_id IN (SELECT correlation_id FROM losers);
```

3. Losers are **hard-deleted** (~73 rows). No archive table; export a CSV first if
   anyone wants a record, but nothing downstream reads those rows.

## 5. Rollout order (hard requirement)

1. **goLib release A — repave code** (§6). Safe against the current schema: repave
   works without the index.
2. **Cleanup script** run in each environment (dev, preprod, prod `ci`).
3. **goLib release B — the migration** (FKs, then unique index) + slippy-api dep bump;
   the slippy-migrator Job applies it on deploy.

**The unique index must never be live while pre-repave code runs.** The old failed
path (`AbandonSlip` + insert) creates a second row for the same `(repo, sha)` and
would 23505-fail every same-commit retrigger. Two releases, in this order, close that
window; the cleanup between them guarantees the index build succeeds.

## 6. Code changes — `goLibMyCarrier/slippy`

### 6.1 Store: `DeleteSlip`

> **⚠️ SUPERSEDED (PR #82 review):** shipped as `DeleteSlip(ctx, correlationID,
> successorCorrelationID string) error` — a third parameter beyond what's described
> below. It also gained an ended-status guard (returns `ErrSlipWentLive` if the row's
> status is no longer ended — the repave decision went stale) and transactionally
> repoints any other slip's `slip_ancestry.parent_correlation_id` from `correlationID`
> to `successorCorrelationID` (or deletes those links if `successorCorrelationID` is
> empty) rather than leaving them dangling. `ClickHouseStore.DeleteSlip` returns an
> error wrapping the `ErrDeleteSlipUnsupported` sentinel unconditionally, not a stub
> delete. See `slippy/interfaces.go`'s `DeleteSlip` doc and
> `slippy/postgres_store_updates.go` for the current contract.

New `SlipStore` method: `DeleteSlip(ctx, correlationID)` — a single
`DELETE FROM routing_slips WHERE correlation_id = $1`; children cascade. (ClickHouse
store gets a stub or equivalent delete; Postgres is the operational store.)
Consumers implementing `SlipStore` mocks (slippy-api `mockSlipStore`) gain the method
on the dep bump — per the existing Slippy Bump Checklist.

### 6.2 `push.go` — same-commit decision table

Keep the status-based branching; change only the same-commit **ended** action:

| Existing slip for the pushed SHA | Action |
|---|---|
| live (`in_progress`/`pending`/`compensating`) | unchanged — `handlePushRetry` reuse; caller sees `Deduplicated: true`, suppresses |
| `failed` | `DeleteSlip` (replaces `AbandonSlip`) → fall through to `Create` with the new run's id |
| terminal (`completed`/`abandoned`/`promoted`/`compensated`) | **also** `DeleteSlip` → fall through to `Create` (today this path silently double-inserts; the index would reject it) |
| ended **and** the incoming create carries **no components** | **empty-run guard**: return the existing slip as a dedup — do NOT repave real history into an empty run |

- The lookup must catch terminal rows too: the terminal branch needs `LoadByCommit`
  semantics (or an equivalent check), since `LoadLiveByCommit` filters
  abandoned/promoted/compensated and the current code falls through blind.
- **Empty-run guard rationale:** branch create/recreate at an existing SHA emits a
  push with no changed files; on `AllowSlipWithNoBuilds` repos that reaches
  CreateSlip with no components. Nothing will be dispatched either way, so returning
  the existing ended slip (dedup → suppress) is harmless and preserves the real run's
  history. This also retires the shape that produced part of the 10 cross-branch
  duplicates.
- **Empty-run guard trade-off (accepted):** tests-only repos
  (`buildable=false` + `RunUnitTests=true` + `AllowSlipWithNoBuilds=true`) always
  create slips with nil components, so a *webhook-redelivery* retrigger of such a repo
  is guard-suppressed instead of repaved. Their retrigger capability is the rerun path
  (retrigger-ci `scope=unit_tests`/`all`), which is unaffected. Regular build repos are
  untouched — their retriggers carry components and repave normally.
- **Cross-branch is not special**: repave uniformly; the fresh row carries the new
  push's branch. A fast-forward of an existing SHA onto another branch re-dispatches,
  exactly matching today's observable behavior (which mints a duplicate row instead).
- **Cross-commit supersede is unchanged**: a newer commit still `AbandonSlip`s an
  in-flight older commit — different `(repo, sha)`, no unique conflict. `abandoned`
  rows still exist; there is just never a second row for the same commit.
- **`23505` backstop**: `store.Create` unique-violation errors route back into the
  delete+recreate path (still behind the `repo:sha` lock). This covers the
  fail-open-Redis concurrency window.

### 6.3 Re-dispatch contract (verified sound — do not change)

`CreateSlipResult`/`CreateSlipOutput` and the consumer contract are untouched. Repave
inserts the caller's new `correlation_id`, so `returned == sent` →
`Deduplicated: false` → pushhookparser re-dispatches. This is airtight because the
push path always mints a fresh `uuid.New()` per Kafka message
(`pushhookparser/pkg/cmd/consumer.go`) and no payload field overrides it.

### 6.4 Readers

> **⚠️ SUPERSEDED (PR #82 review):** the "drop `ORDER BY updated_at DESC LIMIT 1`"
> instruction below was reverted. Shipped `LoadByCommit`/`LoadLiveByCommit`
> (`slippy/postgres_store.go`) keep the tiebreak: release A (Phase A) ships ahead of
> the Phase B cleanup + unique index, so same-commit duplicate rows can still exist in
> production when this code runs, and dropping the tiebreak made the pick among them
> nondeterministic — reopening a stale-duplicate repave bug. `FindByCommits`/
> `FindAllByCommits`'s `ORDER BY c.priority ASC, s.updated_at DESC` is unchanged for
> the same reason.

With ≤1 row per commit, drop `ORDER BY updated_at DESC LIMIT 1` from `LoadByCommit`
and `LoadLiveByCommit`. **Keep** the `LoadByCommit` vs `LoadLiveByCommit` distinction —
a commit's one row can still be `abandoned` (cross-commit supersede), so "the row" vs
"the row only if live" remain different questions. Audit `FindByCommits` /
`FindAllByCommits` ordering assumptions (the `updated_at DESC` tiebreak within a
commit becomes dead weight; cross-commit priority ordering stays).

### 6.5 Concurrency

> **⚠️ Implementation note (PR #82 review):** `DeleteSlip` and `Create` are still two
> separate calls, as described below, and the crash-leaves-no-row self-heal argument
> still holds. But `DeleteSlip` itself is no longer a single bare `DELETE` — it now runs
> its own delete + descendant-repoint sequence inside one transaction (§6.1), which is
> the "single transactional … method" this paragraph anticipated as an optional
> refinement, scoped to the delete side only. The delete-then-create pair across
> `DeleteSlip`/`Create` remains non-transactional as designed here.

The slippy-api `repo:sha` Redis lock (120s TTL, fail-open) remains the serializer for
the read-decide-write sequence; the unique index is the backstop, not the concurrency
mechanism. `DeleteSlip` + `Create` are two calls — a crash between them leaves no row,
which self-heals on Kafka redelivery (the message is not acked). A single
transactional `Repave` store method is a possible refinement, not required for
correctness.

## 7. Retrigger invariants (verified; must hold after this change)

Two mutually exclusive mechanisms exist. This design touches only the second.

- **Rerun** (`action:"rerun"` — sole producer: admin repo's "1e Retrigger Builds and
  Unit Tests" workflow): resolves the existing slip via `GetSlipByCommit`
  (server-side **git**-ancestry walk), reuses its `correlation_id`, dispatches
  `scope=all` (builds + unit tests) or `scope=unit_tests`. Never creates a slip;
  never re-dispatches the secret scan. A `failed` slip recovers **in place** via step
  re-runs (`executor.go` `checkPipelineCompletion`).
- **Push-shaped events** (real pushes, webhook/Kafka redeliveries) are the only
  create/repave triggers and dispatch the full set (builds, unit tests, secret scan,
  auto-deploy, check-runs).
- **Rule: selective retrigger must never be implemented as a filtered push replay** —
  repave would delete the build state a unit-tests-only rerun wants to keep.

> **⚠️ SUPERSEDED (PR #82 review):** the "accept dangling `parent_correlation_id` /
> no repointing" stance below was replaced. Shipped `DeleteSlip`
> (`slippy/postgres_store_updates.go`) performs exactly the repoint this paragraph
> describes as a future contingency — `UPDATE slip_ancestry SET parent_correlation_id
> = $successor, parent_failed_step = '' WHERE parent_correlation_id = $deleted` — every
> time a repave has a successor, transactionally, as part of the same `DeleteSlip`
> call. When there is no successor (`successorCorrelationID == ""`), those descendant
> links are deleted rather than left dangling. See `slippy/interfaces.go`'s
> `DeleteSlip` doc for the current contract.

`slip_ancestry` note: the table is write-only in production — its only reader
(`Client.ResolveAncestry`) has zero callers; all live ancestry resolution is
git-based. Dangling `parent_correlation_id` after repave/dedupe is **accepted**; no
repointing. If the table ever gains a consumer, repave must repoint
(`UPDATE slip_ancestry SET parent_correlation_id = $new WHERE parent_correlation_id = $old`).

## 8. Documentation updates (in scope)

- `.github/STATE_MACHINE_V3.md`: same-commit **repave (delete+recreate)** vs
  cross-commit **abandon**; `branch` = current run's branch; empty-run guard; note
  that rewritten-history force-pushes can strand a live slip (pre-existing); fix
  drifted line refs (recovery branch is `executor.go:365-405`, not `:307-346`).
- `slippy/push.go` comments: retire the stale "retrigger-ci replay" naming — the
  workflow named retrigger-ci sends `action:"rerun"` and never reaches
  `CreateSlipForPush`; the repave path serves re-pushes and webhook re-deliveries.
- `slippy/CLAUDE.md`: fix stale API names (`UpdateStepStatus`/`UpdateComponentStatus`
  → `UpdateStepWithStatus` etc.), the `status.go` file-table description, and the
  ClickHouse-era framing where it misleads.
- admin repo (separate PR): rename the retrigger-ci "Generate Correlation ID" step —
  `slippy-find` resolves an existing id, it does not generate one.
- Re-check `DEVOPS-127-pg-retention.sql` against the new cascade FKs (child deletes
  become automatic; ensure it never deletes children of retained rows).

## 9. Testing (RED-first, per go-tdd)

Library (`goLibMyCarrier/slippy`):

- Retrigger after `failed`: repave — old row + children gone, new row with the new
  run's id, `returned == sent`.
- Re-push after `completed`: repave (today's silent double-insert path).
- In-flight duplicate: still reuses — `returned == existing id` (suppress).
- Empty-run guard: ended slip + no-component create → existing slip returned as
  dedup; history intact.
- Cross-branch repave: ended slip on branch A, push of same SHA on branch B → one row,
  `branch = B`, full re-dispatch signal.
- `DeleteSlip` cascades away all `slip_component_states` + `slip_ancestry` rows for
  the deleted run; no orphans.
- Concurrent same-commit create: one wins; loser path (idempotent-return or `23505`
  backstop) converges to one row.
- Cross-commit supersede unchanged: newer commit abandons in-flight older commit; the
  abandoned row survives as its commit's one row.
- Migration: FKs + unique index apply on a clean schema; index add fails loudly on
  duplicate data (guard for mis-ordered rollout).

Integration (slippy-api, on dep bump): create → repave → `GetSlipByCommit` resolves
the new run (rerun path unaffected by repave).

## 10. Out of scope

- Re-keying the PK to `(repo, sha)` — `correlation_id` stays the PK.
- Abandoning slips on branch-delete / force-push (`before` SHAs) — pre-existing
  stranded-live-slip wart, tracked separately if wanted.
- The slippy-api 120s lock-TTL trap (terminal-slip 409 → silent ack-drop within ~2
  minutes of creation) — pre-existing, unchanged; documented in Slippy-api CLAUDE.md.
- Analytics/dashboard changes — repave erases prior-run history and resets
  `created_at`; accepted with analytics-owner sign-off (DEVOPS-231).

## 11. Acceptance criteria

- [ ] Unique index on `(lower(repository), commit_sha)`; FKs with `ON DELETE CASCADE`
      from both child tables; migration applies cleanly after the cleanup script.
- [ ] Exactly one row per commit; same-commit ended retrigger deletes + recreates;
      children cascade; no orphaned child rows.
- [ ] Retriggers still re-dispatch (`returned == sent`; no PR #73 regression); rerun
      path (`action:"rerun"`) provably unaffected.
- [ ] Empty-run guard: no-component creates never destroy an ended run's history.
- [ ] Readers simplified (`LIMIT 1` tiebreaks dropped) with the live/non-live
      distinction preserved; `FindByCommits`/`FindAllByCommits` audited.
- [ ] `STATE_MACHINE_V3.md`, `push.go` comments, and `slippy/CLAUDE.md` updated.
- [ ] Rollout executed as release A (code) → cleanup → release B (schema).
