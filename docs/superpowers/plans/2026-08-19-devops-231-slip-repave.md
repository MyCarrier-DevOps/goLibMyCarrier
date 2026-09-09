# DEVOPS-231 Slip Repave Implementation Plan (goLibMyCarrier)

> **⚠️ SUPERSEDED IN PART — review of PR #82 (goLibMyCarrier, DEVOPS-231) changed
> decisions this plan still records as-written. This document is kept as a historical
> record of what was planned, NOT re-edited to match what shipped. Concretely:
> Task 7's instruction to drop the `ORDER BY updated_at DESC` tiebreaks was reverted —
> they are retained in `LoadByCommit`/`LoadLiveByCommit` because removing them made the
> lookup nondeterministic against pre-cleanup duplicate rows.
>
> **`DeleteSlip` no longer exists at all.** It was superseded, before release, by
> `Repave(ctx, oldCorrelationID string, newSlip *Slip, parent *AncestryEntry) error`,
> which performs the guarded removal, the child cleanup, the successor's insert, the
> descendant repoint and the successor's ancestry link as ONE transaction. The
> delete-then-`Create` sequence this plan describes could leave a commit with no slip at
> all whenever the create failed after the delete committed — unrecoverably, since the
> next redelivery found no row to repave. Everything this plan says about `DeleteSlip`
> (Task 1's signature, the two-call ordering, the "phantom successor" and
> "no convergence backstop" caveats, and the sentinel name
> `ErrDeleteSlipUnsupported`, now `ErrRepaveUnsupported`) is historical.
>
> Treat the current code (`slippy/interfaces.go`, `slippy/postgres_store_updates.go`,
> `slippy/postgres_store.go`, `slippy/push.go`) as the source of truth, not this plan.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One `routing_slips` row per `(lower(repository), commit_sha)` — repave (delete + recreate) on same-commit ended retriggers, enforced by cascade FKs and a DB unique index.

**Architecture:** Two-phase delivery in `goLibMyCarrier/slippy`. **Phase A** (release A): new `SlipStore.DeleteSlip`, `push.go` same-commit repave with an empty-run guard, an `ErrDuplicateSlip` backstop, reader simplifications, and doc updates. **Phase B** (release B, HARD-GATED on the ops cleanup script having run in every environment): Postgres migration v5 adding the cascade FKs and the unique index. The `CreateSlipResult` consumer contract does not change.

**Tech Stack:** Go, pgx/v5 (Postgres store), testcontainers integration tests (`//go:build integration`), table-driven unit tests with `MockStore`/`MockGitHubAPI`.

**Spec:** `docs/superpowers/specs/2026-08-19-devops-231-one-slip-per-commit-design.md`

## Global Constraints

- Work only in the `slippy` module; scope make targets: `make test PKG=slippy`, `make lint PKG=slippy`.
- RED-first: every code task writes its failing test before the implementation (go-tdd).
- `go` directive stays at the minor (`go 1.26`) — library repo, do not pin patch.
- Conventional commits (`feat:`/`fix:`/`test:`/`docs:`).
- **Phase B tasks (10–11) must NOT merge into the same release as Phase A.** They ship as a separate PR after the one-time cleanup script has run in dev, preprod, and prod (see the consolidated plan at the workspace root).
- Do not change `CreateSlipResult`, `CreateSlipOutput`, or any slippy-api HTTP schema.

---

## Phase A — repave code (release A)

### Task 1: `SlipStore.DeleteSlip` — interface, mock, Postgres, ClickHouse

**Files:**
- Modify: `slippy/interfaces.go` (after `UpdateSlipStatus`, ~line 71)
- Modify: `slippy/mock_store_test.go` (struct ~line 79, constructor ~line 204, methods after `Create`)
- Modify: `slippy/postgres_store_updates.go` (new method at end)
- Modify: `slippy/clickhouse_store.go` (new method near `InsertAncestryLink`, ~line 888)
- Test: `slippy/postgres_store_integration_test.go`, `slippy/mock_store_impl_test.go`

**Interfaces:**
- Consumes: existing `SlipStore`, `PostgresStore.pool` (`Exec(ctx, sql, args...) (pgconn.CommandTag, error)`).
- Produces: `DeleteSlip(ctx context.Context, correlationID string) error` on `SlipStore`, `MockStore.DeleteSlipCalls []string`, `MockStore.DeleteSlipError error`. Task 2+ depends on these exact names.

- [ ] **Step 1: Write the failing integration test** (append to `postgres_store_integration_test.go`):

```go
func TestPostgresStore_DeleteSlip_Cascades_Integration(t *testing.T) {
	store, pool, _ := newMigratedStore(t)
	ctx := context.Background()

	slip := &Slip{
		CorrelationID: "corr-delete-me",
		Repository:    "owner/repo",
		Branch:        "integration",
		CommitSHA:     "sha-delete-cascade",
		Status:        SlipStatusFailed,
		Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
		StateHistory:  []StateHistoryEntry{},
	}
	require.NoError(t, store.Create(ctx, slip))
	require.NoError(t, store.UpdateStep(ctx, "corr-delete-me", "builds", "api", StepStatusFailed))
	require.NoError(t, store.InsertAncestryLink(ctx, slip, AncestryEntry{
		CorrelationID: "corr-parent", CommitSHA: "sha-parent",
		Repository: "owner/repo", Branch: "integration",
		Status: SlipStatusCompleted, CreatedAt: time.Now(),
	}))

	require.NoError(t, store.DeleteSlip(ctx, "corr-delete-me"))

	_, err := store.Load(ctx, "corr-delete-me")
	assert.ErrorIs(t, err, ErrSlipNotFound)
	for _, table := range []string{"slip_component_states", "slip_ancestry"} {
		var n int
		require.NoError(t, pool.QueryRow(ctx,
			"SELECT count(*) FROM "+table+" WHERE correlation_id = $1", "corr-delete-me").Scan(&n))
		assert.Zero(t, n, table+" rows must cascade away")
	}
}
```

Note: until Task 10's migration exists, the FKs are absent — so the Postgres
implementation must delete children explicitly (three statements in one
transaction), NOT rely on cascades. The cascade FKs make it redundant later, not
wrong. This is what keeps release A correct on the pre-migration schema.

- [ ] **Step 2: Run to verify it fails to compile** (`DeleteSlip` undefined):

Run: `cd slippy && go test -tags integration -run TestPostgresStore_DeleteSlip_Cascades_Integration ./... ; cd ..`
Expected: compile error `store.DeleteSlip undefined`

- [ ] **Step 3: Add to the interface** (`interfaces.go`, after `UpdateSlipStatus`):

```go
	// DeleteSlip removes a routing slip row and its child rows
	// (slip_component_states, slip_ancestry) for the given run. Used by the
	// same-commit repave path (DEVOPS-231): a retrigger of an ended slip deletes
	// the prior run and creates a fresh one under the new correlation_id.
	// Deleting a missing slip is not an error (idempotent).
	DeleteSlip(ctx context.Context, correlationID string) error
```

- [ ] **Step 4: Postgres implementation** (`postgres_store_updates.go`, end of file):

```go
// DeleteSlip removes the slip row and its children in one transaction. Children are
// deleted explicitly so the method is correct both before and after the cascade FKs
// of migration v5 exist (release A runs against the pre-FK schema).
func (s *PostgresStore) DeleteSlip(ctx context.Context, correlationID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete slip %s: %w", correlationID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, stmt := range []string{
		"DELETE FROM slip_component_states WHERE correlation_id = $1",
		"DELETE FROM slip_ancestry WHERE correlation_id = $1",
		"DELETE FROM routing_slips WHERE correlation_id = $1",
	} {
		if _, err := tx.Exec(ctx, stmt, correlationID); err != nil {
			return fmt.Errorf("delete slip %s: %w", correlationID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete slip %s: %w", correlationID, err)
	}
	return nil
}
```

If `s.pool` (the store's pool interface) lacks `Begin`, extend the interface the
same way `Exec`/`QueryRow` are declared (`postgres_store.go` ~line 20) — pgxpool
provides `Begin(ctx) (pgx.Tx, error)` natively; the mock pool in
`postgres_store_test.go` gains a trivial fake `Tx`.

- [ ] **Step 5: MockStore implementation** (`mock_store_test.go`):

Add to the struct (call-tracking block, ~line 100): `DeleteSlipCalls []string` and
(error-injection block) `DeleteSlipError error`. Add the method after `Create`:

```go
// DeleteSlip removes the slip and its commit index entry (children live on the
// Slip struct in the mock, so removing the slip removes everything).
func (m *MockStore) DeleteSlip(ctx context.Context, correlationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DeleteSlipCalls = append(m.DeleteSlipCalls, correlationID)
	if m.DeleteSlipError != nil {
		return m.DeleteSlipError
	}
	if slip, ok := m.Slips[correlationID]; ok {
		delete(m.CommitIndex, slip.Repository+":"+slip.CommitSHA)
		delete(m.Slips, correlationID)
	}
	return nil
}
```

- [ ] **Step 6: ClickHouse stub** (`clickhouse_store.go`): the ClickHouse store is
retained for non-slip readers only (post-DEVOPS-127); repave is Postgres-only:

```go
// DeleteSlip is not supported on the ClickHouse store. Postgres is the operational
// slip store (DEVOPS-127); the repave path (DEVOPS-231) must never run against
// ClickHouse. Returning an error (rather than a silent no-op) makes a
// misconfiguration loud.
func (s *ClickHouseStore) DeleteSlip(_ context.Context, correlationID string) error {
	return fmt.Errorf("DeleteSlip(%s): not supported on the ClickHouse store; Postgres is the operational slip store", correlationID)
}
```

- [ ] **Step 7: Compile + unit tests pass**

Run: `cd slippy && go build ./... && go test -run 'TestMockStore|TestClickHouse.*DeleteSlip' ./... ; cd ..`
Expected: PASS (add a two-line unit test in `mock_store_impl_test.go` asserting
`DeleteSlip` removes the slip and records the call, mirroring its `Create` test style)

- [ ] **Step 8: Run the integration test** (requires Docker):

Run: `cd slippy && go test -tags integration -run TestPostgresStore_DeleteSlip_Cascades_Integration ./... ; cd ..`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add slippy/interfaces.go slippy/mock_store_test.go slippy/mock_store_impl_test.go slippy/postgres_store_updates.go slippy/postgres_store_test.go slippy/clickhouse_store.go slippy/postgres_store_integration_test.go
git commit -m "feat(slippy): add SlipStore.DeleteSlip for same-commit repave (DEVOPS-231)"
```

### Task 2: Repave the `failed` same-commit path (`AbandonSlip` → `DeleteSlip`)

**Files:**
- Modify: `slippy/push.go:184-233` (`CreateSlipForPush` same-commit block)
- Test: `slippy/push_test.go:248-350` (two existing subtests, modified RED-first)

**Interfaces:**
- Consumes: `store.DeleteSlip` (Task 1), existing `handlePushRetry`, `initializeSlipForPush`, `store.Create`.
- Produces: the repave behavior later tasks extend. No signature changes.

- [ ] **Step 1: Modify the existing test to demand deletion (RED).** In
`push_test.go`, subtest `"failed existing slip - supersedes and creates fresh"`
(~line 248): rename to `"failed existing slip - repaves (delete + create fresh)"`
and replace the abandoned-status assertions (~lines 293-300) with:

```go
	// The old failed slip must be DELETED (repave), not abandoned — one row per commit.
	if _, ok := store.Slips["corr-old-failed"]; ok {
		t.Error("old failed slip must be deleted on repave, still present")
	}
	if len(store.DeleteSlipCalls) != 1 || store.DeleteSlipCalls[0] != "corr-old-failed" {
		t.Errorf("expected DeleteSlip(corr-old-failed), got %v", store.DeleteSlipCalls)
	}
```

Update the subtest's doc comment to describe repave (delete + recreate) and cite
the spec instead of "abandon". Keep the assertions for the returned new id, the
single `Create` call, and no `handlePushRetry`.

- [ ] **Step 2: Modify the delete-error subtest (RED).** Subtest
`"failed existing slip - abandon error is non-fatal, still creates fresh"`
(~line 309): rename to `"failed existing slip - delete error is non-fatal, still creates fresh"`;
replace `store.UpdateSlipStatusError = errors.New("clickhouse unavailable")` with
`store.DeleteSlipError = errors.New("postgres unavailable")`. Keep the
fresh-slip + warning assertions unchanged.

- [ ] **Step 3: Run to verify both fail**

Run: `cd slippy && go test -run 'TestClient_CreateSlipForPush' ./... ; cd ..`
Expected: FAIL — old slip still present / no DeleteSlip calls (code still abandons)

- [ ] **Step 4: Implement.** In `push.go`, replace the `AbandonSlip` call block
(~lines 218-231) with:

```go
		c.logger.Info(ctx, "Repaving ended slip for same commit (delete + recreate)", map[string]interface{}{
			"repaved_id":      existingSlip.CorrelationID,
			"repaved_commit":  shortSHA(existingSlip.CommitSHA),
			"repaved_status":  string(existingSlip.Status),
			"superseding_id":  opts.CorrelationID,
		})
		if delErr := c.store.DeleteSlip(ctx, existingSlip.CorrelationID); delErr != nil {
			// Non-fatal: record a warning and still create the fresh slip. Blocking
			// creation here would re-introduce the "retrigger never builds" bug; if the
			// stale row survives, the ErrDuplicateSlip backstop (and, post-migration,
			// the unique index) converges on the next attempt.
			result.Warnings = append(result.Warnings,
				fmt.Errorf("failed to delete repaved slip %s: %w", existingSlip.CorrelationID, delErr))
		}
		// fall through to fresh-slip creation with opts.CorrelationID
```

Also update the function doc comment (~lines 145-155): the failed bullet now says
the stuck slip is *deleted (repaved)* and a fresh slip created; drop the
"retrigger-ci replay" phrasing — say "webhook re-delivery or same-commit re-push"
(retrigger-ci is the rerun path and never reaches this function).

- [ ] **Step 5: Run tests**

Run: `cd slippy && go test -run 'TestClient_CreateSlipForPush' ./... ; cd ..`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add slippy/push.go slippy/push_test.go
git commit -m "feat(slippy): repave (delete+recreate) failed same-commit slips on push retrigger (DEVOPS-231)"
```

### Task 3: Repave terminal same-commit slips (lookup switch + terminal branch)

**Files:**
- Modify: `slippy/push.go:175-233` (lookup + branch structure)
- Test: `slippy/push_test.go:352+` (terminal-statuses subtest, modified RED-first)

**Interfaces:**
- Consumes: `store.LoadByCommit` (unfiltered), `store.DeleteSlip`, `Status.IsTerminal()`.
- Produces: final same-commit decision structure that Task 4 guards.

- [ ] **Step 1: Modify the terminal test (RED).** Subtest
`"new slip for terminal existing slip"` (~line 352) iterates
`abandoned/promoted/compensated/completed`. After the existing assertions that a
fresh slip is created with the new id, add per-status:

```go
				if _, ok := store.Slips["corr-terminal-old"]; ok {
					t.Errorf("[%s] old terminal slip must be deleted on repave (one row per commit)", termStatus)
				}
				if len(store.DeleteSlipCalls) != 1 {
					t.Errorf("[%s] expected 1 DeleteSlip call, got %d", termStatus, len(store.DeleteSlipCalls))
				}
```

Rename the subtest to `"terminal existing slip - repaves (delete + create fresh)"`.
IMPORTANT: the pre-created slip in this test must carry `Components` (or the
`opts` must — check the block at ~line 361-380 and ensure `opts.Components` is
non-empty) so Task 4's guard does not shortcut it.

- [ ] **Step 2: Run to verify it fails**

Run: `cd slippy && go test -run 'TestClient_CreateSlipForPush' ./... ; cd ..`
Expected: FAIL for `abandoned`/`promoted`/`compensated` (LoadLiveByCommit filters
them → code never sees them → old rows survive) and for `completed` (falls through
without deleting)

- [ ] **Step 3: Implement.** In `push.go` (~line 184), switch the lookup and
restructure the branch:

```go
	existingSlip, err := c.store.LoadByCommit(ctx, opts.Repository, opts.CommitSHA)
	if err == nil && existingSlip != nil {
		if !existingSlip.Status.IsTerminal() && existingSlip.Status != SlipStatusFailed {
			// live in-flight (in_progress/pending/compensating): reuse — unchanged
			slip, retryErr := c.handlePushRetry(ctx, existingSlip)
			...
		}
		// failed OR terminal: repave (Task 2's delete block), then fall through to Create
	}
```

Preserve the existing in-flight comment block. Update the lookup comment
(~lines 175-183): the lookup is now `LoadByCommit` because under one-row-per-commit
ANY existing row for this `(repo, sha)` — including `abandoned` from a cross-commit
supersede — must be repaved before `Create`, or the unique index rejects the insert.
`LoadByCommit` returns `ErrSlipNotFound` for a missing row exactly like
`LoadLiveByCommit` — keep the `err == nil` guard shape.

- [ ] **Step 4: Run the full push test file**

Run: `cd slippy && go test -run 'TestClient_CreateSlipForPush|TestClient_resolveAndAbandonAncestors' ./... ; cd ..`
Expected: PASS (cross-commit supersede tests untouched — different SHA, still `AbandonSlip`)

- [ ] **Step 5: Commit**

```bash
git add slippy/push.go slippy/push_test.go
git commit -m "feat(slippy): repave terminal same-commit slips; lookup by commit unfiltered (DEVOPS-231)"
```

### Task 4: Empty-run guard

**Files:**
- Modify: `slippy/push.go` (inside the failed/terminal branch, before the delete)
- Test: `slippy/push_test.go` (new subtest in `TestClient_CreateSlipForPush`)

**Interfaces:**
- Consumes: Task 3's branch structure; `opts.Components`.
- Produces: guard behavior; result carries the EXISTING slip (dedup semantics).

- [ ] **Step 1: Write the failing test** (new subtest):

```go
	t.Run("ended slip + no components - returns existing slip, no repave (empty-run guard)", func(t *testing.T) {
		// Branch create/recreate at an existing SHA reaches CreateSlip with no
		// components (AllowSlipWithNoBuilds repos). Nothing would be dispatched, so
		// repaving would only destroy the real run's history. Return the existing
		// ended slip as a dedup instead (caller sees returned != sent → suppress).
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		store.AddSlip(&Slip{
			CorrelationID: "corr-real-run",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-branch-create",
			Status:        SlipStatusCompleted,
			Steps:         map[string]Step{"builds": {Status: StepStatusCompleted}},
			StateHistory:  []StateHistoryEntry{},
		})

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-branch-create",
			Repository:    "owner/repo",
			Branch:        "feature/new-branch",
			CommitSHA:     "sha-branch-create",
			Components:    nil, // no work to dispatch
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Slip.CorrelationID != "corr-real-run" {
			t.Errorf("guard must return the existing run, got %q", result.Slip.CorrelationID)
		}
		if len(store.DeleteSlipCalls) != 0 {
			t.Errorf("guard must not repave, got DeleteSlip calls: %v", store.DeleteSlipCalls)
		}
		if len(store.CreateCalls) != 0 {
			t.Errorf("guard must not create, got %d Create calls", len(store.CreateCalls))
		}
	})
```

- [ ] **Step 2: Run to verify it fails** (current code repaves)

Run: `cd slippy && go test -run 'TestClient_CreateSlipForPush' ./... ; cd ..`
Expected: FAIL — DeleteSlip called / new slip returned

- [ ] **Step 3: Implement.** At the top of the failed/terminal branch (before the
delete block):

```go
		if len(opts.Components) == 0 {
			// Empty-run guard: nothing will be dispatched for this push (branch
			// create/recreate at an existing SHA, or a components-less repo).
			// Repaving would destroy the prior run's history for zero benefit.
			// Return the existing slip; the caller sees returned != sent and
			// suppresses side effects. Trade-off for tests-only repos: see spec §6.2.
			c.logger.Info(ctx, "Empty-run guard: reusing ended slip for componentless push", map[string]interface{}{
				"existing_id": existingSlip.CorrelationID,
				"commit":      shortSHA(existingSlip.CommitSHA),
			})
			result.Slip = existingSlip
			result.AncestryResolved = len(existingSlip.Ancestry) > 0
			return result, nil
		}
```

- [ ] **Step 4: Run tests** — the zero-component NEW-slip tests
(`TestClient_InitializeSlipForPush_EmptyComponents`, `_MobileApp`) must still pass:
the guard only fires when an ended slip already exists for the SHA.

Run: `cd slippy && go test -run 'TestClient_CreateSlipForPush|TestClient_InitializeSlipForPush' ./... ; cd ..`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add slippy/push.go slippy/push_test.go
git commit -m "feat(slippy): empty-run guard - componentless push reuses ended slip instead of repave (DEVOPS-231)"
```

### Task 5: Cross-branch repave behavior tests (pin the semantics)

**Files:**
- Test: `slippy/push_test.go` (two new subtests; no production code expected)

**Interfaces:**
- Consumes: Tasks 2–4 behavior.
- Produces: pinned cross-branch semantics (`branch` = current run's branch).

- [ ] **Step 1: Write both tests** (they should PASS immediately — they pin
behavior; if either fails, the earlier tasks are wrong):

```go
	t.Run("cross-branch ended slip - repaves onto the new branch", func(t *testing.T) {
		// FF-merge shape: SHA built on feature branch, same SHA pushed to integration
		// after the slip ended. One row per commit: the slip repaves onto the new
		// branch and the caller re-dispatches (returned == sent).
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		store.AddSlip(&Slip{
			CorrelationID: "corr-feature-run",
			Repository:    "owner/repo",
			Branch:        "feature/thing",
			CommitSHA:     "sha-ff-merge",
			Status:        SlipStatusCompleted,
			Steps:         map[string]Step{"builds": {Status: StepStatusCompleted}},
			StateHistory:  []StateHistoryEntry{},
		})

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-integration-run",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-ff-merge",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Slip.CorrelationID != "corr-integration-run" {
			t.Errorf("expected repave with new id, got %q", result.Slip.CorrelationID)
		}
		if result.Slip.Branch != "integration" {
			t.Errorf("branch must follow the current run, got %q", result.Slip.Branch)
		}
		if _, ok := store.Slips["corr-feature-run"]; ok {
			t.Error("feature-branch run must be deleted (one row per commit)")
		}
	})

	t.Run("cross-branch in-flight slip - reuses and keeps original branch", func(t *testing.T) {
		// Same SHA pushed to a second branch while the first branch's slip is live:
		// reuse (suppress), slip keeps the original branch. Pre-existing behavior.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		store.AddSlip(&Slip{
			CorrelationID: "corr-inflight",
			Repository:    "owner/repo",
			Branch:        "feature/thing",
			CommitSHA:     "sha-inflight",
			Status:        SlipStatusInProgress,
			Steps:         map[string]Step{"builds": {Status: StepStatusRunning}},
			StateHistory:  []StateHistoryEntry{},
		})

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-second-branch",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-inflight",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Slip.CorrelationID != "corr-inflight" {
			t.Errorf("expected reuse of in-flight slip, got %q", result.Slip.CorrelationID)
		}
		if result.Slip.Branch != "feature/thing" {
			t.Errorf("in-flight reuse must keep the original branch, got %q", result.Slip.Branch)
		}
		if len(store.DeleteSlipCalls) != 0 {
			t.Error("in-flight slip must never be repaved")
		}
	})
```

- [ ] **Step 2: Run and confirm both pass**

Run: `cd slippy && go test -run 'TestClient_CreateSlipForPush' ./... ; cd ..`
Expected: PASS (if the FF test fails on `Branch`, check `initializeSlipForPush`
uses `opts.Branch` — it does — and that the mock's `deepCopySlip` copies `Branch`)

- [ ] **Step 3: Commit**

```bash
git add slippy/push_test.go
git commit -m "test(slippy): pin cross-branch repave and in-flight reuse semantics (DEVOPS-231)"
```

### Task 6: `ErrDuplicateSlip` backstop (unique-violation recovery)

**Files:**
- Modify: `slippy/errors.go` (new sentinel)
- Modify: `slippy/postgres_store.go` (`Create` maps 23505 → sentinel)
- Modify: `slippy/push.go` (`CreateSlipForPush` catches sentinel, repaves once)
- Test: `slippy/push_test.go`, `slippy/postgres_store_test.go`

**Interfaces:**
- Consumes: `store.Create`, `store.LoadByCommit`, `store.DeleteSlip`.
- Produces: `var ErrDuplicateSlip = errors.New("a slip already exists for this repository and commit")` in `errors.go`. slippy-api does not need to handle it (push.go resolves it internally).

- [ ] **Step 1: Write the failing client test:**

```go
	t.Run("duplicate-create backstop - repaves and retries once", func(t *testing.T) {
		// Redis-lock fail-open race: two creates for the same new commit; the loser's
		// INSERT hits the unique index (ErrDuplicateSlip). The backstop loads the
		// winner row, repaves it, and retries the create once.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		// Winner row exists but is NOT indexed under the loser's lookup until Create
		// is attempted: simulate by injecting the error on first Create only.
		store.AddSlip(&Slip{
			CorrelationID: "corr-race-winner",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-race",
			Status:        SlipStatusCompleted, // ended by the time the retry lands
			Steps:         map[string]Step{"builds": {Status: StepStatusCompleted}},
			StateHistory:  []StateHistoryEntry{},
		})
		store.CreateErrorFor["corr-race-loser"] = ErrDuplicateSlip

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-race-loser",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-race",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		})
		if err != nil {
			t.Fatalf("backstop must recover, got error: %v", err)
		}
		_ = result
		if len(store.DeleteSlipCalls) != 1 {
			t.Errorf("expected backstop DeleteSlip of the conflicting row, got %v", store.DeleteSlipCalls)
		}
	})
```

Mock caveat: `CreateErrorFor` fires on every attempt for that id. Make the
backstop's retry succeed by having the implementation CLEAR-AND-RETRY through
`store.Create` exactly once and the test inject via a one-shot error: add
`CreateErrorOnce map[string]error` to `MockStore` (consumed on first use) if
`CreateErrorFor` proves too sticky — implement whichever is smaller, but the test
must end with a successful create after exactly one DeleteSlip.

NOTE: the sequence above will FIRST hit Task 3's normal repave (an ended slip for
`sha-race` exists) — that is fine: the injected `ErrDuplicateSlip` then simulates
"someone re-inserted between my delete and my insert". What the test pins is:
`ErrDuplicateSlip` from `Create` → one more `LoadByCommit` + `DeleteSlip` + retry,
no error surfaced. Adjust the expected `DeleteSlipCalls` count to 2 if the normal
repave path also fires in this arrangement — assert the FINAL state instead:
create succeeded, no error, at least one backstop delete.

- [ ] **Step 2: Run to verify it fails** (`ErrDuplicateSlip` undefined)

Run: `cd slippy && go test -run 'TestClient_CreateSlipForPush' ./... ; cd ..`
Expected: compile FAIL

- [ ] **Step 3: Implement — errors.go sentinel** (match existing var-block style):

```go
	// ErrDuplicateSlip indicates an insert conflicted with the one-row-per-commit
	// unique index (uq_routing_slips_repo_sha). The push path treats it as "someone
	// else holds the row" and routes to the repave/dedup backstop (DEVOPS-231).
	ErrDuplicateSlip = errors.New("a slip already exists for this repository and commit")
```

- [ ] **Step 4: Implement — Postgres mapping.** In `PostgresStore.Create`
(`postgres_store.go`), wrap the INSERT error:

```go
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uq_routing_slips_repo_sha" {
		return fmt.Errorf("create slip %s: %w", slip.CorrelationID, ErrDuplicateSlip)
	}
```

(Until migration v5 exists no code path produces it — the mapping is inert but
tested via a fake-pool unit test in `postgres_store_test.go` following that file's
existing fake-pool pattern; if no fake-pool pattern exists for Create errors, defer
the mapping test to Task 11's integration test and note it in the commit.)

- [ ] **Step 5: Implement — push.go backstop.** Replace the plain create-error
return (~line 243-245):

```go
	if err := c.store.Create(ctx, slip); err != nil {
		if errors.Is(err, ErrDuplicateSlip) {
			// Unique-index backstop (fail-open Redis race): another run holds the
			// row. Load it, repave it, retry once. Still serialized by the repo:sha
			// lock in the normal case; this is the last-resort convergence path.
			if conflicting, loadErr := c.store.LoadByCommit(ctx, opts.Repository, opts.CommitSHA); loadErr == nil && conflicting != nil {
				if delErr := c.store.DeleteSlip(ctx, conflicting.CorrelationID); delErr != nil {
					return nil, fmt.Errorf("failed to repave conflicting slip %s: %w", conflicting.CorrelationID, delErr)
				}
			}
			if retryErr := c.store.Create(ctx, slip); retryErr != nil {
				return nil, fmt.Errorf("failed to create slip after duplicate backstop: %w", retryErr)
			}
		} else {
			return nil, fmt.Errorf("failed to create slip: %w", err)
		}
	}
```

- [ ] **Step 6: Run tests**

Run: `cd slippy && go test -run 'TestClient_CreateSlipForPush' ./... ; cd ..`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add slippy/errors.go slippy/postgres_store.go slippy/postgres_store_test.go slippy/push.go slippy/push_test.go slippy/mock_store_test.go
git commit -m "feat(slippy): ErrDuplicateSlip backstop routes unique-index conflicts to repave (DEVOPS-231)"
```

### Task 7: Reader simplification

> **⚠️ SUPERSEDED (PR #82 review):** Step 1 below — dropping the `ORDER BY
> updated_at DESC` tiebreak — was reverted after this plan was written. The shipped
> `LoadByCommit`/`LoadLiveByCommit` (`slippy/postgres_store.go`) keep the tiebreak: the
> Phase A / Phase B split means release A still runs against pre-cleanup data where
> same-commit duplicates exist, and an unordered pick among duplicates reopened a
> stale-duplicate repave bug. Do not remove the `ORDER BY` clause; see the current code
> for the up-to-date rationale comment.

**Files:**
- Modify: `slippy/postgres_store.go:152-169` (`LoadByCommit`, `LoadLiveByCommit`)
- Modify: `slippy/postgres_store_reads.go:13-60` (`FindByCommits`/`FindAllByCommits` — comment only)
- Test: `slippy/postgres_store_reads_test.go` (existing tests must keep passing)

**Interfaces:**
- Consumes: nothing new. Produces: identical signatures, simpler SQL.

- [ ] **Step 1: Drop the tiebreak.** In both `LoadByCommit` and `LoadLiveByCommit`
remove `ORDER BY updated_at DESC` — keep `LIMIT 1` (harmless, and release A still
runs against pre-cleanup data where duplicates exist; with dupes present the
now-unordered pick is acceptable because every same-commit path immediately
repaves). Add one comment line to each: `// one row per (repo, sha) — DEVOPS-231; LIMIT 1 is belt-and-braces for pre-cleanup data`.
KEEP the status filter in `LoadLiveByCommit` and both methods' distinction (an
`abandoned` row from a cross-commit supersede is "the row" but not "the live row").

- [ ] **Step 2: Audit `FindByCommits`/`FindAllByCommits`.** The `ORDER BY
c.priority ASC, s.updated_at DESC` cross-commit ordering STAYS (it orders across
different commits); add a comment that the `updated_at` component no longer breaks
same-commit ties (there are none) and only stabilizes cross-commit output.

- [ ] **Step 3: Run the read tests**

Run: `cd slippy && go test -run 'TestPostgres.*Read|TestPostgres.*Load|TestPostgres.*Find' ./... ; cd ..`
Expected: PASS (if any unit test pinned the ORDER BY string verbatim, update the
expected SQL in that test)

- [ ] **Step 4: Commit**

```bash
git add slippy/postgres_store.go slippy/postgres_store_reads.go slippy/postgres_store_reads_test.go
git commit -m "refactor(slippy): drop same-commit ORDER BY tiebreaks from commit readers (DEVOPS-231)"
```

### Task 8: Documentation updates

**Files:**
- Modify: `.github/STATE_MACHINE_V3.md`
- Modify: `slippy/CLAUDE.md`
- Modify: `slippy/push.go` (comments only, where Tasks 2-3 didn't already)

**Interfaces:** none (docs).

- [ ] **Step 1: `STATE_MACHINE_V3.md`.** Add/replace the same-commit section:
  - Same-commit ended (failed or terminal) push → **repave** (delete + recreate,
    new correlation_id, children cascade); cross-commit supersede → **abandon**
    (unchanged). One row per `(lower(repository), commit_sha)` enforced by
    `uq_routing_slips_repo_sha` (migration v5).
  - `branch` is an attribute of the current run; a fast-forward of an existing SHA
    onto another branch repaves onto that branch.
  - Empty-run guard: a componentless push against an ended slip reuses it
    (dedup) instead of repaving.
  - Note: rewritten-history force-pushes can strand a live slip on an orphaned
    SHA (pre-existing; ancestry walks cannot see rewritten-away commits).
  - Fix drifted line refs: the recovery branch is `executor.go:365-405` (spec cites
    `:307-346`); re-grep `hold.go` refs while there.
  - Terminology: the push-path retrigger is "webhook re-delivery / same-commit
    re-push"; the operator workflow "retrigger-ci" is the RERUN path
    (`action:"rerun"`) and never creates or repaves slips.
- [ ] **Step 2: `slippy/CLAUDE.md`.** Fix stale API names: `UpdateStepStatus` /
  `UpdateComponentStatus` / `StepStatusSuccess` → `UpdateStepWithStatus` (+ wrappers
  `CompleteStep`/`FailStep`/`StartStep`) / `StepStatusCompleted`; fix the file
  table's `status.go` description (enums + predicates only; update logic lives in
  `steps.go`/`executor.go`); note Postgres is the operational store and the spec
  path is `.github/STATE_MACHINE_V3.md` at the repo root.
- [ ] **Step 3: `push.go`** — re-read the full `CreateSlipForPush` doc comment and
  the same-commit block comments for leftover "abandon"/"retrigger-ci replay"
  phrasing; align with repave terminology.
- [ ] **Step 4: Commit**

```bash
git add .github/STATE_MACHINE_V3.md slippy/CLAUDE.md slippy/push.go
git commit -m "docs(slippy): repave semantics in STATE_MACHINE_V3, fix stale API names and retrigger naming (DEVOPS-231)"
```

### Task 9: Phase A verification gate

- [ ] **Step 1:** Run `make fmt && make lint PKG=slippy && make test PKG=slippy`
Expected: 0 lint issues; all tests pass; coverage gate satisfied.
- [ ] **Step 2:** Run integration tests: `cd slippy && go test -tags integration ./... ; cd ..` (Docker required)
Expected: PASS.
- [ ] **Step 3:** `/go-verify` equivalent complete → Phase A PR ready.
**STOP: Phase A merges and RELEASES before any Phase B work. The one-time cleanup
script (consolidated plan, workspace root) must run in dev/preprod/prod before
Phase B merges.**

---

## Phase B — schema migration (release B; GATED on cleanup)

### Task 10: Migration v5 — cascade FKs + unique index

**Files:**
- Modify: `slippy/postgres_migrations.go` (new `uniquenessMigration()`, added to `GenerateMigrations`)
- Test: `slippy/postgres_migrations_test.go` (version expectations), `slippy/postgres_migrations_integration_test.go`

**Interfaces:**
- Consumes: `postgresmigrator.Migration{Version, Name, Description, UpSQL, DownSQL}`.
- Produces: migration Version 5; `LatestVersion()` returns 5.

- [ ] **Step 1: Write the failing version test.** In
`postgres_migrations_test.go`, find the test asserting `LatestVersion()` /
migration count and bump the expectation to 5 (and count 5). If none pins it, add:

```go
func TestPostgresMigrations_LatestVersionIsFive(t *testing.T) {
	m := NewPostgresDynamicMigrationManager(pgTestPipelineConfig(t), nil)
	if got := m.LatestVersion(); got != 5 {
		t.Fatalf("LatestVersion = %d, want 5", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails** (`LatestVersion` = 4)

Run: `cd slippy && go test -run 'TestPostgresMigrations' ./... ; cd ..`
Expected: FAIL

- [ ] **Step 3: Implement** (`postgres_migrations.go`; add
`m.uniquenessMigration(),` to `GenerateMigrations()` after `m.ancestryMigration()`):

```go
// uniquenessMigration enforces one row per (lower(repository), commit_sha) and adds
// the repave cascade FKs (DEVOPS-231). PRECONDITION: orphaned child rows and
// duplicate (repo, sha) rows must be cleaned first (one-time ops script) — the FK
// ADDs validate existing data and the unique index build fails on duplicates. A
// loud failure here means the cleanup has not run in this environment; do NOT
// weaken this migration to work around it.
// Plain CREATE UNIQUE INDEX (not CONCURRENTLY): the table is ~12.5k rows, the build
// is near-instant, and it must run inside the migrator's transaction.
func (m *PostgresDynamicMigrationManager) uniquenessMigration() postgresmigrator.Migration {
	return postgresmigrator.Migration{
		Version:     5,
		Name:        "one_slip_per_commit",
		Description: "Cascade FKs from child tables and unique (lower(repository), commit_sha) index (DEVOPS-231)",
		UpSQL: `
			DO $$
			BEGIN
				BEGIN
					ALTER TABLE slip_component_states
						ADD CONSTRAINT fk_component_states_slip
						FOREIGN KEY (correlation_id) REFERENCES routing_slips(correlation_id)
						ON DELETE CASCADE;
				EXCEPTION WHEN duplicate_object THEN NULL;
				END;
				BEGIN
					ALTER TABLE slip_ancestry
						ADD CONSTRAINT fk_ancestry_slip
						FOREIGN KEY (correlation_id) REFERENCES routing_slips(correlation_id)
						ON DELETE CASCADE;
				EXCEPTION WHEN duplicate_object THEN NULL;
				END;
			END $$;
			CREATE UNIQUE INDEX IF NOT EXISTS uq_routing_slips_repo_sha
				ON routing_slips (lower(repository), commit_sha);
		`,
		DownSQL: `
			DROP INDEX IF EXISTS uq_routing_slips_repo_sha;
			ALTER TABLE slip_ancestry DROP CONSTRAINT IF EXISTS fk_ancestry_slip;
			ALTER TABLE slip_component_states DROP CONSTRAINT IF EXISTS fk_component_states_slip;
		`,
	}
}
```

Deliberately NO cascade FK on `slip_ancestry.parent_correlation_id` — deleting a
parent run must not delete a child's lineage row (spec §7).

- [ ] **Step 4: Run unit tests**

Run: `cd slippy && go test -run 'TestPostgresMigrations' ./... ; cd ..`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add slippy/postgres_migrations.go slippy/postgres_migrations_test.go
git commit -m "feat(slippy): migration v5 - cascade FKs + unique (repo,sha) index (DEVOPS-231)"
```

### Task 11: Migration v5 integration tests

**Files:**
- Test: `slippy/postgres_migrations_integration_test.go` (append)

**Interfaces:**
- Consumes: `newMigratedStore(t)` helper (now migrates through v5).

- [ ] **Step 1: Write the tests:**

```go
func TestMigrationV5_UniqueIndexRejectsDuplicateCommit_Integration(t *testing.T) {
	store, _, _ := newMigratedStore(t)
	ctx := context.Background()

	first := &Slip{
		CorrelationID: "corr-uq-1", Repository: "Owner/Repo", Branch: "integration",
		CommitSHA: "sha-uq", Status: SlipStatusCompleted,
		Steps: map[string]Step{}, StateHistory: []StateHistoryEntry{},
	}
	require.NoError(t, store.Create(ctx, first))

	dupe := &Slip{
		CorrelationID: "corr-uq-2", Repository: "owner/repo", // case-variant: index is on lower()
		Branch: "integration", CommitSHA: "sha-uq", Status: SlipStatusInProgress,
		Steps: map[string]Step{}, StateHistory: []StateHistoryEntry{},
	}
	err := store.Create(ctx, dupe)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDuplicateSlip, "23505 on uq_routing_slips_repo_sha must map to ErrDuplicateSlip")
}

func TestMigrationV5_CascadeDeleteChildren_Integration(t *testing.T) {
	store, pool, _ := newMigratedStore(t)
	ctx := context.Background()

	slip := &Slip{
		CorrelationID: "corr-cascade", Repository: "owner/repo", Branch: "integration",
		CommitSHA: "sha-cascade", Status: SlipStatusFailed,
		Steps: map[string]Step{}, StateHistory: []StateHistoryEntry{},
	}
	require.NoError(t, store.Create(ctx, slip))
	require.NoError(t, store.UpdateStep(ctx, "corr-cascade", "builds", "api", StepStatusFailed))

	// Raw row delete (NOT store.DeleteSlip) — proves the FK cascade itself works.
	_, err := pool.Exec(ctx, "DELETE FROM routing_slips WHERE correlation_id = $1", "corr-cascade")
	require.NoError(t, err)

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT count(*) FROM slip_component_states WHERE correlation_id = $1", "corr-cascade").Scan(&n))
	assert.Zero(t, n, "component states must cascade on raw slip delete")
}
```

- [ ] **Step 2: Run** — `cd slippy && go test -tags integration -run 'TestMigrationV5' ./... ; cd ..`
Expected: PASS (this also completes Task 6's deferred `ErrDuplicateSlip` mapping
verification against a real 23505)

- [ ] **Step 3: Full verification** — `make fmt && make lint PKG=slippy && make test PKG=slippy`, then `cd slippy && go test -tags integration ./...`
Expected: all green → Phase B PR ready.

- [ ] **Step 4: Commit**

```bash
git add slippy/postgres_migrations_integration_test.go
git commit -m "test(slippy): migration v5 integration - unique index rejection + FK cascade (DEVOPS-231)"
```
