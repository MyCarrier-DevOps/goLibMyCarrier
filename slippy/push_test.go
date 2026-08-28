package slippy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPushOptions_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    PushOptions
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid options",
			opts: PushOptions{
				CorrelationID: "corr-123",
				Repository:    "owner/repo",
				Branch:        "main",
				CommitSHA:     "abc123",
				Components: []ComponentDefinition{
					{Name: "svc", DockerfilePath: "Dockerfile"},
				},
			},
			wantErr: false,
		},
		{
			name: "missing correlation_id",
			opts: PushOptions{
				Repository: "owner/repo",
				CommitSHA:  "abc123",
			},
			wantErr: true,
			errMsg:  "correlation_id is required",
		},
		{
			name: "missing repository",
			opts: PushOptions{
				CorrelationID: "corr-123",
				CommitSHA:     "abc123",
			},
			wantErr: true,
			errMsg:  "repository is required",
		},
		{
			name: "missing commit_sha",
			opts: PushOptions{
				CorrelationID: "corr-123",
				Repository:    "owner/repo",
			},
			wantErr: true,
			errMsg:  "commit_sha is required",
		},
		{
			name: "empty components is valid",
			opts: PushOptions{
				CorrelationID: "corr-123",
				Repository:    "owner/repo",
				CommitSHA:     "abc123",
				Components:    []ComponentDefinition{},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if err.Error() != tt.errMsg {
					t.Errorf("Validate() error = %q, want %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestClient_CreateSlipForPush(t *testing.T) {
	ctx := context.Background()

	t.Run("success - new slip", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		opts := PushOptions{
			CorrelationID: "corr-push-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123def456",
			Components: []ComponentDefinition{
				{Name: "svc-a", DockerfilePath: "services/a/Dockerfile"},
				{Name: "svc-b", DockerfilePath: "services/b/Dockerfile"},
			},
		}

		result, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		slip := result.Slip

		// Verify the returned slip
		if slip.CorrelationID != "corr-push-1" {
			t.Errorf("expected CorrelationID 'corr-push-1', got '%s'", slip.CorrelationID)
		}
		if slip.Repository != "owner/repo" {
			t.Errorf("expected Repository 'owner/repo', got '%s'", slip.Repository)
		}
		if slip.Branch != "main" {
			t.Errorf("expected Branch 'main', got '%s'", slip.Branch)
		}
		if slip.CommitSHA != "abc123def456" {
			t.Errorf("expected CommitSHA 'abc123def456', got '%s'", slip.CommitSHA)
		}
		if slip.Status != SlipStatusInProgress {
			t.Errorf("expected Status 'in_progress', got '%s'", slip.Status)
		}

		// Verify aggregates have component data - use config to get the aggregate step name
		aggregateSteps := config.GetAggregateSteps()
		if len(aggregateSteps) == 0 {
			t.Fatal("expected at least one aggregate step in config")
		}
		aggregateColumnName := aggregateSteps[0].Name
		if len(slip.Aggregates[aggregateColumnName]) != 2 {
			t.Fatalf(
				"expected 2 components in %s aggregate, got %d",
				aggregateColumnName,
				len(slip.Aggregates[aggregateColumnName]),
			)
		}
		if slip.Aggregates[aggregateColumnName][0].Component != "svc-a" {
			t.Errorf("expected first component 'svc-a', got '%s'", slip.Aggregates[aggregateColumnName][0].Component)
		}
		if slip.Aggregates[aggregateColumnName][0].Status != StepStatusPending {
			t.Errorf("expected build status 'pending', got '%s'", slip.Aggregates[aggregateColumnName][0].Status)
		}

		// Verify steps were initialized - use config step names
		firstStepName := config.Steps[0].Name
		if slip.Steps[firstStepName].Status != StepStatusRunning {
			t.Errorf("expected %s status 'running', got '%s'", firstStepName, slip.Steps[firstStepName].Status)
		}
		lastStepName := config.Steps[len(config.Steps)-1].Name
		if slip.Steps[lastStepName].Status != StepStatusPending {
			t.Errorf("expected %s status 'pending', got '%s'", lastStepName, slip.Steps[lastStepName].Status)
		}

		// Verify history was created
		if len(slip.StateHistory) == 0 {
			t.Error("expected state history to be initialized")
		}
		if slip.StateHistory[0].Step != firstStepName {
			t.Errorf("expected first history entry for '%s', got '%s'", firstStepName, slip.StateHistory[0].Step)
		}

		// Verify store was called
		if len(store.CreateCalls) != 1 {
			t.Errorf("expected 1 Create call, got %d", len(store.CreateCalls))
		}
	})

	t.Run("retry - existing slip", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		// Pre-create an existing slip
		existingSlip := &Slip{
			CorrelationID: "corr-push-retry",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "retry123",
			CreatedAt:     time.Now().Add(-5 * time.Minute),
			UpdatedAt:     time.Now().Add(-5 * time.Minute),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusFailed}, // Previously failed
			},
			StateHistory: []StateHistoryEntry{},
		}
		store.AddSlip(existingSlip)

		opts := PushOptions{
			CorrelationID: "corr-push-retry-new", // Different correlation ID
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "retry123", // Same commit
			Components:    []ComponentDefinition{},
		}

		result, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		slip := result.Slip

		// Should return the existing slip (not create new)
		if slip.CorrelationID != "corr-push-retry" {
			t.Errorf("expected existing slip ID 'corr-push-retry', got '%s'", slip.CorrelationID)
		}

		// Verify no Create call (retry should update, not create)
		if len(store.CreateCalls) != 0 {
			t.Errorf("expected 0 Create calls (retry), got %d", len(store.CreateCalls))
		}

		// Verify UpdateStep was called to reset push_parsed
		var foundUpdateStep bool
		for _, call := range store.UpdateStepCalls {
			if call.StepName == "push_parsed" && call.Status == StepStatusRunning {
				foundUpdateStep = true
				break
			}
		}
		if !foundUpdateStep {
			t.Error("expected push_parsed to be reset to running")
		}

		// bd mycarrier-5dv5 (F1): handlePushRetry must reset push_parsed and append the
		// state_history entry via a single atomic UpdateStepWithHistory call, not two
		// separate UpdateStep + AppendHistory calls. Two separate calls would let
		// AppendHistory's CLONE_DERIVED derive CTE race the just-written push_parsed
		// event under ClickHouse async-insert visibility lag, falling back to a stale
		// clone of push_parsed_status instead of the explicit stepStatusOverride that
		// UpdateStepWithHistory's pure-step branch passes to appendHistoryWithOverrides.
		if store.UpdateStepWithHistoryCallCount != 1 {
			t.Errorf("expected exactly 1 atomic UpdateStepWithHistory call for the push_parsed "+
				"retry reset (override must be passed atomically, not via separate "+
				"UpdateStep+AppendHistory calls), got %d", store.UpdateStepWithHistoryCallCount)
		}
		if len(store.UpdateStepCalls) != 1 {
			t.Errorf("expected exactly 1 UpdateStepCalls entry, got %d", len(store.UpdateStepCalls))
		}
		if len(store.AppendHistoryCalls) != 1 {
			t.Errorf("expected exactly 1 AppendHistoryCalls entry, got %d", len(store.AppendHistoryCalls))
		}
		// B6 (review fix): the in-flight reuse path dedups onto a pre-existing slip
		// (existingSlip has no Ancestry set here), so AncestryResolved must be true -
		// "no resolution was attempted or needed" - not `len(slip.Ancestry) > 0`,
		// which is unconditionally false in production (no store hydrates Ancestry on
		// load).
		if !result.AncestryResolved {
			t.Error("expected AncestryResolved=true for the in-flight reuse dedup")
		}
	})

	t.Run("failed existing slip - repaves (delete + create fresh)", func(t *testing.T) {
		// A prior pipeline for this EXACT commit ran and FAILED (non-terminal, stuck).
		// A new push for the same commit (webhook re-delivery or same-commit re-push)
		// must repave: DELETE the failed slip and create a FRESH slip with the caller's
		// correlation ID — NOT reuse it via handlePushRetry, and not merely abandon it,
		// per DEVOPS-231's one-row-per-(repository, commit_sha) repave model
		// (STATE_MACHINE_V3.md). Reusing it via retry returns a different correlation ID
		// to the caller (slippy-api → pushhookparser), which reads that as a dedup and
		// suppresses builds + unit tests (the retrigger bug).
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		store.AddSlip(&Slip{
			CorrelationID: "corr-old-failed",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "failed-commit-xyz",
			Status:        SlipStatusFailed,
			Steps: map[string]Step{
				"builds": {Status: StepStatusFailed},
			},
			StateHistory: []StateHistoryEntry{},
		})

		opts := PushOptions{
			CorrelationID: "corr-retrigger-new",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "failed-commit-xyz", // same commit — a retrigger replay
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		}

		result, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Must return the NEW slip so the caller does not see a dedup.
		if result.Slip.CorrelationID != "corr-retrigger-new" {
			t.Errorf("expected new slip ID 'corr-retrigger-new', got '%s'", result.Slip.CorrelationID)
		}
		// The successor is written by Repave itself, inside the same transaction that
		// removed the superseded row, so there is no separate Create call to count any
		// more. What matters is that the successor really is persisted.
		if len(store.CreateCalls) != 0 {
			t.Errorf("a repave must not issue a separate Create, got %d", len(store.CreateCalls))
		}
		if _, ok := store.Slips["corr-retrigger-new"]; !ok {
			t.Error("successor must be persisted by the repave")
		}
		// The old failed slip must be REPLACED (repaved), not abandoned — one row per commit.
		if _, ok := store.Slips["corr-old-failed"]; ok {
			t.Error("old failed slip must be removed on repave, still present")
		}
		if len(store.RepaveCalls) != 1 || store.RepaveCalls[0] != "corr-old-failed" {
			t.Errorf("expected Repave(corr-old-failed), got %v", store.RepaveCalls)
		}
		if len(store.RepaveSuccessorCalls) != 1 || store.RepaveSuccessorCalls[0] != "corr-retrigger-new" {
			t.Errorf("expected the fresh slip to be the repave successor, got %v", store.RepaveSuccessorCalls)
		}
		// handlePushRetry (reset push_parsed -> running) must NOT run for a failed slip.
		for _, call := range store.UpdateStepCalls {
			if call.StepName == "push_parsed" && call.Status == StepStatusRunning {
				t.Error("handlePushRetry must not run for a failed existing slip (should supersede)")
			}
		}
	})

	t.Run("failed existing slip - repave failure is fatal, leaving the store untouched", func(t *testing.T) {
		// A repave failure is now FATAL, a deliberate reversal of the pre-Repave code
		// (which warned and created the slip anyway). That leniency only made sense while
		// delete and create were separate calls: the create could still succeed on its own,
		// at the cost of leaving a stale row behind. Repave writes nothing when it fails,
		// so there is no successor to fall through to — reporting success here would
		// return a slip that does not exist. Failing the push lets Kafka redeliver, which
		// converges because the superseded row is still there to repave next time.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		store.AddSlip(&Slip{
			CorrelationID: "corr-old-failed-2",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "failed-commit-abc",
			Status:        SlipStatusFailed,
			Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
			StateHistory:  []StateHistoryEntry{},
		})
		store.RepaveError = errors.New("postgres unavailable")

		opts := PushOptions{
			CorrelationID: "corr-retrigger-2",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "failed-commit-abc",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		}

		result, err := client.CreateSlipForPush(ctx, opts)
		if err == nil {
			t.Fatal("expected a repave failure to fail the push so Kafka redelivers")
		}
		if !strings.Contains(err.Error(), "postgres unavailable") {
			t.Errorf("expected the underlying store error to be wrapped, got %v", err)
		}
		if result != nil {
			t.Errorf("expected no result alongside the error, got %+v", result)
		}
		// Convergence depends on this: the superseded row must still be there for the
		// redelivery to repave, and no half-written successor may exist.
		if _, ok := store.Slips["corr-old-failed-2"]; !ok {
			t.Error("the superseded row must survive a failed repave so a redelivery can converge")
		}
		if _, ok := store.Slips["corr-retrigger-2"]; ok {
			t.Error("a failed repave must not leave a successor behind")
		}
		if len(store.CreateCalls) != 0 {
			t.Errorf("a failed repave must not fall through to Create, got %d calls", len(store.CreateCalls))
		}
	})

	t.Run("same-correlation retry on an ended slip converges instead of dead-lettering", func(t *testing.T) {
		// A caller retrying WITHIN one delivery reuses its correlation ID, so the id it
		// presents as the successor can be the id already on the row. pushhookparser's
		// bounded retry does exactly this.
		//
		// The sequence that reaches it: attempt 1 creates the slip and dispatches, then fails
		// later in the handler (a check-run write, say); the dispatched build fails fast, so
		// the slip is `failed` by the time attempt 2 runs. Attempt 2's LoadByCommit finds an
		// ENDED row whose correlation_id equals opts.CorrelationID.
		//
		// Routing that to Repave cannot work — the store rejects a self-repave, correctly,
		// because it can only destroy history under an unchanged id. But the rejection is
		// non-converging: every retry presents the identical input, so the push fails
		// identically and the message dead-letters. Before this branch existed, the same
		// retry SUCCEEDED, because Create is an upsert on correlation_id and abandon+create
		// simply rewrote the row.
		//
		// So a same-correlation push takes the plain create path: the upsert rewrites the row
		// in place, resetting it to a live status, and the caller sees returned == sent and
		// re-dispatches. That restores the pre-Repave outcome exactly rather than inventing
		// new semantics for it.
		//
		// Note what this deliberately does NOT do: it is not routed to handlePushRetry, which
		// would return the ended row with only push_parsed reset — the caller would then see
		// returned == sent, dispatch anyway, and report against a slip whose top-level status
		// is still `failed`. The upsert is what makes the returned slip genuinely live.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		store.AddSlip(&Slip{
			CorrelationID: "corr-same-delivery",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-retry",
			Status:        SlipStatusFailed,
			Steps:         map[string]Step{"unit_tests": {Status: StepStatusFailed}},
			StateHistory:  []StateHistoryEntry{},
		})

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-same-delivery", // SAME id as the row: an in-delivery retry
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-retry",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		})
		if err != nil {
			t.Fatalf("a same-correlation retry must converge, not fail: %v", err)
		}
		if result.Slip == nil || result.Slip.CorrelationID != "corr-same-delivery" {
			t.Fatalf("expected the slip under the caller's own id, got %+v", result.Slip)
		}
		if !result.Slip.Status.IsLive() {
			t.Errorf("the row must be reset to a LIVE status so the re-dispatched run can "+
				"report against it — a returned slip still carrying the failed status is "+
				"the outcome routing this to handlePushRetry would have produced (got %q)",
				result.Slip.Status)
		}
		if len(store.RepaveCalls) != 0 {
			t.Errorf("no repave may be attempted for a self-referential id, got %v", store.RepaveCalls)
		}
		if len(store.CreateCalls) != 1 {
			t.Errorf("expected exactly one Create (the upsert), got %d", len(store.CreateCalls))
		}
		stored, loadErr := store.Load(ctx, "corr-same-delivery")
		if loadErr != nil {
			t.Fatalf("the row must still exist after the reset: %v", loadErr)
		}
		if !stored.Status.IsLive() {
			t.Errorf("the persisted row must be reset too, not just the returned copy, "+
				"got %q", stored.Status)
		}
	})

	// The empty-run guard must never claim a push whose correlation ID already matches the
	// existing row. The guard's contract is that the caller sees returned != sent and
	// suppresses its side effects — that is the only thing making it safe to return a slip
	// the caller did not create. When returned == sent, the caller reads the result as its
	// own freshly created slip and dispatches against it, so for an ended row it would report
	// against a terminal slip.
	//
	// Tabled over all five ended statuses because the earlier fix covered `failed` only, and
	// `completed` is the shape that exposed the hole: a run that finished between two
	// attempts of one delivery is not `failed`, so the carve-out never fired.
	for _, endedStatus := range []SlipStatus{
		SlipStatusCompleted,
		SlipStatusFailed,
		SlipStatusAbandoned,
		SlipStatusPromoted,
		SlipStatusCompensated,
	} {
		t.Run("same-correlation zero-component push onto "+string(endedStatus)+
			" resets in place, guard must not claim it", func(t *testing.T) {
			store := NewMockStore()
			github := NewMockGitHubAPI()
			client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

			store.AddSlip(&Slip{
				CorrelationID: "corr-self",
				Repository:    "owner/repo",
				Branch:        "integration",
				CommitSHA:     "sha-self",
				Status:        endedStatus,
				Steps:         map[string]Step{"builds": {Status: StepStatusCompleted}},
				StateHistory:  []StateHistoryEntry{},
			})

			result, err := client.CreateSlipForPush(ctx, PushOptions{
				CorrelationID: "corr-self", // an in-delivery retry reuses its ID
				Repository:    "owner/repo",
				Branch:        "integration",
				CommitSHA:     "sha-self",
				Components:    nil, // zero components: what the guard keys on
			})
			if err != nil {
				t.Fatalf("a same-correlation retry must converge, got %v", err)
			}
			if result.Slip == nil {
				t.Fatal("expected a slip")
			}
			if !result.Slip.Status.IsLive() {
				t.Errorf("the returned slip must be LIVE: the caller sees returned == sent and "+
					"will dispatch against it, so handing back a %s slip means reporting "+
					"against a terminal run", endedStatus)
			}
			if len(store.RepaveCalls) != 0 {
				t.Errorf("no repave may be attempted for a self-referential id, got %v",
					store.RepaveCalls)
			}
			if len(store.CreateCalls) != 1 {
				t.Errorf("expected exactly one Create (the in-place upsert), got %d",
					len(store.CreateCalls))
			}
		})
	}

	t.Run("repave rejected on its inputs - fatal, and named as non-converging", func(t *testing.T) {
		// ErrInvalidConfiguration gets its own arm rather than landing in the fatal
		// default, because the default's justification for being fatal inverts for it.
		// The default reasons that failing the push "lets Kafka redeliver against a store
		// that still holds the superseded row" — i.e. that redelivery converges. This
		// sentinel cannot converge: the store refused the INPUTS (today, a self-repave
		// where oldCorrelationID == newSlip.CorrelationID), the correlation ID is stable
		// within a delivery, so every redelivery is rejected identically.
		//
		// Fatal is still right — a caller in this state has a bug worth surfacing, and
		// degrading would either destroy history or return a slip whose status contradicts
		// what the caller thinks it created. What the arm buys is that the sentinel is
		// wrapped and logged as its own class instead of looking like a transient store
		// failure that a retry might clear.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		store.AddSlip(&Slip{
			CorrelationID: "corr-badinput",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-badinput",
			Status:        SlipStatusFailed,
			Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
			StateHistory:  []StateHistoryEntry{},
		})
		store.RepaveError = fmt.Errorf("%w: Repave successor corr-badinput is the slip being repaved",
			ErrInvalidConfiguration)

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-badinput-push",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-badinput",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		})
		if err == nil {
			t.Fatal("a repave rejected on its inputs must fail the push")
		}
		if !errors.Is(err, ErrInvalidConfiguration) {
			t.Errorf("the sentinel must survive wrapping so callers can classify it, got %v", err)
		}
		if result != nil {
			t.Errorf("expected no result alongside the error, got %+v", result)
		}
		if len(store.CreateCalls) != 0 {
			t.Errorf("no successor may be created, got %d Create calls", len(store.CreateCalls))
		}
		if _, ok := store.Slips["corr-badinput"]; !ok {
			t.Error("the superseded row must be left untouched")
		}
	})

	t.Run("terminal existing slip - repaves (delete + create fresh)", func(t *testing.T) {
		// When a terminal slip (abandoned, promoted, compensated, completed) already
		// exists for the commit SHA, CreateSlipForPush must NOT call handlePushRetry.
		// Instead it repaves: delete the old row, then fall through and create a fresh
		// slip with the new correlation ID. Under one-row-per-commit ANY existing row
		// for the pushed SHA must be repaved before Create — this prevents webhook
		// re-delivery or bot-commit races from resurrecting stale slips, and keeps the
		// unique (repository, commit_sha) index from rejecting the insert.
		for _, termStatus := range []SlipStatus{
			SlipStatusAbandoned, SlipStatusPromoted, SlipStatusCompensated, SlipStatusCompleted,
		} {
			termStatus := termStatus
			t.Run(string(termStatus), func(t *testing.T) {
				store := NewMockStore()
				github := NewMockGitHubAPI()
				config := testPipelineConfig()
				client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

				// Pre-create terminal slip for the same commit
				store.AddSlip(&Slip{
					CorrelationID: "corr-terminal-old",
					Repository:    "owner/repo",
					Branch:        "main",
					CommitSHA:     "terminal-commit-abc",
					Status:        termStatus,
					Steps:         map[string]Step{},
					StateHistory:  []StateHistoryEntry{},
				})

				opts := PushOptions{
					CorrelationID: "corr-new-after-terminal",
					Repository:    "owner/repo",
					Branch:        "main",
					CommitSHA:     "terminal-commit-abc",
					Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
				}

				result, err := client.CreateSlipForPush(ctx, opts)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				// Must return the NEW slip, not the terminal one
				if result.Slip.CorrelationID != "corr-new-after-terminal" {
					t.Errorf("expected new slip ID 'corr-new-after-terminal', got '%s'", result.Slip.CorrelationID)
				}

				// Repave writes the successor itself, so no separate Create is issued —
				// the invariant to check is that the successor is persisted.
				if len(store.CreateCalls) != 0 {
					t.Errorf("[%s] a repave must not issue a separate Create, got %d",
						termStatus, len(store.CreateCalls))
				}
				if _, ok := store.Slips["corr-new-after-terminal"]; !ok {
					t.Errorf("[%s] successor must be persisted by the repave", termStatus)
				}

				// handlePushRetry resets push_parsed to running — must NOT have happened
				for _, call := range store.UpdateStepCalls {
					if call.StepName == "push_parsed" && call.Status == StepStatusRunning {
						t.Error("handlePushRetry must not be called for a terminal existing slip")
					}
				}

				// One row per (repository, commit_sha): the old terminal slip must be
				// repaved (replaced), not left behind.
				if _, ok := store.Slips["corr-terminal-old"]; ok {
					t.Errorf("[%s] old terminal slip must be removed on repave (one row per commit)", termStatus)
				}
				if len(store.RepaveCalls) != 1 {
					t.Errorf("[%s] expected 1 Repave call, got %d", termStatus, len(store.RepaveCalls))
				}
			})
		}
	})

	t.Run("retry detection uses unfiltered LoadByCommit (one row per commit)", func(t *testing.T) {
		// DEVOPS-231 (one row per commit) reversed the F1-era invariant this guard
		// used to protect: the retry-detection lookup must now route through
		// unfiltered LoadByCommit, not LoadLiveByCommit. Under one-row-per-commit,
		// ANY existing row for the pushed (repo, sha) — including an
		// abandoned/promoted/compensated row left behind by a cross-commit
		// supersede — must be found and repaved before Create, or the unique
		// (repository, commit_sha) index rejects the insert. Live-only filtering
		// (the old LoadLiveByCommit routing) would hide exactly those rows from the
		// repave path, leaving them behind.
		//
		// This also re-proves the property the old guard actually protected: a
		// superseded/abandoned same-SHA row is never resurrected via
		// handlePushRetry. Under the new routing it's still never reused — it's
		// repaved (deleted) instead of reset.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		store.AddSlip(&Slip{
			CorrelationID: "corr-abandoned-old",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "routing-check-sha",
			Status:        SlipStatusAbandoned,
			Steps:         map[string]Step{},
			StateHistory:  []StateHistoryEntry{},
		})

		opts := PushOptions{
			CorrelationID: "corr-routing-check",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "routing-check-sha",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		}

		result, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// (a) LoadByCommit is the retained routing: exactly one lookup for the
		// pushed (repo, sha).
		if len(store.LoadByCommitCalls) != 1 {
			t.Errorf("expected exactly 1 LoadByCommit call, got %d", len(store.LoadByCommitCalls))
		} else if call := store.LoadByCommitCalls[0]; call.Repository != opts.Repository || call.CommitSHA != opts.CommitSHA {
			t.Errorf("expected LoadByCommit(%q, %q), got LoadByCommit(%q, %q)",
				opts.Repository, opts.CommitSHA, call.Repository, call.CommitSHA)
		}

		// (b) LoadLiveByCommit must no longer be consulted on this path.
		if len(store.LoadLiveByCommitCalls) != 0 {
			t.Errorf(
				"expected 0 LoadLiveByCommit calls (retry detection must use unfiltered LoadByCommit), got %d",
				len(store.LoadLiveByCommitCalls),
			)
		}

		// (c) The abandoned same-SHA row must be repaved, not resurrected: the
		// caller sees its NEW correlation_id, and the old row was replaced (one
		// Repave call, gone from store.Slips) rather than reset via
		// handlePushRetry (no push_parsed -> running UpdateStep call on the old id).
		if result.Slip.CorrelationID != opts.CorrelationID {
			t.Errorf("expected fresh slip %q, got %q", opts.CorrelationID, result.Slip.CorrelationID)
		}
		if _, ok := store.Slips["corr-abandoned-old"]; ok {
			t.Error("abandoned same-SHA row must be repaved (removed), not left behind")
		}
		if len(store.RepaveCalls) != 1 {
			t.Errorf("expected 1 Repave call for the abandoned row, got %d", len(store.RepaveCalls))
		}
		for _, call := range store.UpdateStepCalls {
			if call.CorrelationID == "corr-abandoned-old" && call.StepName == "push_parsed" &&
				call.Status == StepStatusRunning {
				t.Error("abandoned row must not be reused via handlePushRetry (no push_parsed reset on the old id)")
			}
		}
	})

	t.Run("ended slip + no components - returns existing slip, no repave (empty-run guard)", func(t *testing.T) {
		// Branch create/recreate at an existing SHA reaches CreateSlip with no
		// components (AllowSlipWithNoBuilds repos). Nothing would be dispatched, so
		// repaving would only destroy the real run's history. Return the existing
		// ended slip as a dedup instead (caller sees returned != sent → suppress).
		//
		// This also pins the back-compat property the rollout depends on: Dispatch is
		// left at its zero value here, so it doubles as the DispatchIntentUnspecified
		// case. goLib releases before slippy-api and pushhookparser adopt the field, and
		// a caller that sets nothing must behave exactly as it does today. The predicate
		// itself is covered directly by TestPushOptions_dispatchesNothing.
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
		if len(store.RepaveCalls) != 0 {
			t.Errorf("guard must not repave, got Repave calls: %v", store.RepaveCalls)
		}
		if len(store.CreateCalls) != 0 {
			t.Errorf("guard must not create, got %d Create calls", len(store.CreateCalls))
		}
		// B6 (review fix): AncestryResolved=true here means "no resolution was
		// attempted or needed - the returned slip is pre-existing", NOT "the loaded
		// slip happens to carry a populated Ancestry field". The reused slip here has
		// no ancestry, and AncestryResolved must still be true: no store hydrates
		// Slip.Ancestry on load in production, so `len(existingSlip.Ancestry) > 0`
		// was unconditionally false in prod and misfired dedup-alert consumers on
		// every guard hit.
		if !result.AncestryResolved {
			t.Error("expected AncestryResolved=true (dedup: no resolution needed for a pre-existing slip)")
		}
	})

	// TestClient_CreateSlipForPush's dispatch-intent subtests pin the fix for the
	// tests-only-repo retrigger hole (DEVOPS-264): the empty-run guard used to infer "this
	// push dispatches nothing" from len(Components) == 0, which is wrong for a repo that
	// runs unit tests without builds, so a failed run there could not be retriggered by
	// re-pushing the commit.
	//
	// The mechanism, the affected repo set, and the quoted pushhookparser line all live in
	// one place — DispatchIntent's godoc in push.go. They are facts about ANOTHER
	// repository's config and source, so nothing here can observe them going stale;
	// duplicating them would just rot in two places at once.
	//
	// One fact that IS local and does belong here: DEVOPS-231's guard also excludes `failed`
	// outright, independently of intent. That is what covers the rollout window, during which
	// every real push from those repos still arrives with Dispatch unset. The two terms are
	// complementary — intent covers every status once adopted, the carve-out covers `failed`
	// regardless of adoption — so both are exercised below.

	t.Run("failed slip + no components, dispatch unset - retriggers via the failed carve-out",
		func(t *testing.T) {
			// The PRE-adoption shape: a real tests-only-repo push during the rollout window.
			// On the merge base a same-commit push onto a FAILED slip always abandoned it and
			// created a fresh slip under the caller's correlation ID, and the code said why —
			// "blocking fresh-slip creation here would re-introduce the 'retrigger never
			// builds' bug". A failed slip never advances on its own, so a new push for the
			// same commit is a deliberate request to run CI again.
			//
			// This must hold with Dispatch at its zero value, which is the whole point: it is
			// what makes the fix independent of the adoption order.
			store := NewMockStore()
			github := NewMockGitHubAPI()
			client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

			store.AddSlip(&Slip{
				CorrelationID: "corr-failed-run",
				Repository:    "owner/repo",
				Branch:        "integration",
				CommitSHA:     "sha-retrigger",
				Status:        SlipStatusFailed,
				Steps:         map[string]Step{"unit_tests": {Status: StepStatusFailed}},
				StateHistory:  []StateHistoryEntry{},
			})

			result, err := client.CreateSlipForPush(ctx, PushOptions{
				CorrelationID: "corr-retrigger",
				Repository:    "owner/repo",
				Branch:        "integration",
				CommitSHA:     "sha-retrigger",
				Components:    nil, // tests-only repo: unit tests dispatch, no build components
				// Dispatch deliberately left unset — the pre-adoption reality.
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Slip.CorrelationID != "corr-retrigger" {
				t.Errorf("a failed slip must be replaced by a fresh slip under the caller's id "+
					"(so the caller sees returned == sent and re-dispatches), got %q",
					result.Slip.CorrelationID)
			}
			if len(store.RepaveCalls) != 1 {
				t.Errorf("expected exactly 1 Repave call for the failed slip, got %v", store.RepaveCalls)
			} else if store.RepaveCalls[0] != "corr-failed-run" {
				t.Errorf("expected the failed slip to be repaved, got %q", store.RepaveCalls[0])
			}
			if _, ok := store.Slips["corr-failed-run"]; ok {
				t.Error("the failed run must be replaced, not left behind")
			}
		})

	t.Run("ended slip + no components but dispatch intent says work WILL run - repaves", func(t *testing.T) {
		// The POST-adoption shape: once callers forward the field, intent decides and the
		// carve-out above is no longer what carries this case.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		store.AddSlip(&Slip{
			CorrelationID: "corr-failed-tests-only",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-tests-only",
			Status:        SlipStatusFailed,
			Steps:         map[string]Step{"unit_tests": {Status: StepStatusFailed}},
			StateHistory:  []StateHistoryEntry{},
		})

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-retrigger-tests-only",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-tests-only",
			// A tests-only repo: no build components, but unit tests will be dispatched.
			Components: nil,
			Dispatch:   DispatchIntentSomething,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Slip.CorrelationID != "corr-retrigger-tests-only" {
			t.Errorf("expected the fresh slip so the caller re-dispatches, got %q", result.Slip.CorrelationID)
		}
		if len(store.RepaveCalls) != 1 || store.RepaveCalls[0] != "corr-failed-tests-only" {
			t.Errorf("expected the failed run to be repaved, got %v", store.RepaveCalls)
		}
		if _, ok := store.Slips["corr-failed-tests-only"]; ok {
			t.Error("the failed run must be replaced, not left behind")
		}
	})

	t.Run("ended slip + components but dispatch intent says nothing will run - dedups", func(t *testing.T) {
		// The converse: intent is authoritative in BOTH directions. A caller that knows
		// nothing will dispatch gets the guard even when components are present, so the
		// guard stops depending on component count entirely. Note the seeded status is
		// `completed`, not `failed` — the failed carve-out would otherwise decide this, and
		// what is under test here is the intent term.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		store.AddSlip(&Slip{
			CorrelationID: "corr-real-run-2",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-no-dispatch",
			Status:        SlipStatusCompleted,
			Steps:         map[string]Step{"builds": {Status: StepStatusCompleted}},
			StateHistory:  []StateHistoryEntry{},
		})

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-would-be-fresh-2",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-no-dispatch",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
			Dispatch:      DispatchIntentNothing,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Slip.CorrelationID != "corr-real-run-2" {
			t.Errorf("guard must return the existing run, got %q", result.Slip.CorrelationID)
		}
		if len(store.RepaveCalls) != 0 {
			t.Errorf("guard must not repave, got Repave calls: %v", store.RepaveCalls)
		}
	})

	t.Run("dispatch intent Nothing seeds no components on the fresh-create path", func(t *testing.T) {
		// The guards only run when an ended row already exists. On the fresh-create path
		// there is no guard, so a Nothing push carrying components used to seed pending
		// aggregate rows nobody would ever advance: computeAggregateStatus keeps an
		// all-pending aggregate pending forever (an EMPTY one resolves to completed), the
		// slip stays IsLive(), and every later same-commit push takes handlePushRetry
		// instead of repaving — reintroducing the unretriggerable hole DispatchIntent
		// exists to close, through a different door.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-nothing-with-components",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-nothing-fresh",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
			Dispatch:      DispatchIntentNothing,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for name, comps := range result.Slip.Aggregates {
			if len(comps) != 0 {
				t.Errorf("aggregate %q seeded %d components for a push that dispatches nothing: %+v",
					name, len(comps), comps)
			}
		}

		// Pin the mechanism rather than the downstream symptom: an empty aggregate is a
		// vacuous all-completed, so the pipeline can reach a terminal state and the slip
		// stops being live. An all-pending aggregate resolves to pending and never
		// advances, which is what wedged the slip. (The wedge itself needs a running
		// executor to observe, so it is not assertable from here.)
		for name, comps := range result.Slip.Aggregates {
			if got := computeAggregateStatus(comps); got != StepStatusCompleted {
				t.Errorf("aggregate %q resolves to %q; a no-dispatch push must leave it vacuously completed",
					name, got)
			}
		}
	})

	t.Run(
		"ended slip + no components + ancestry - guard reuse still reports AncestryResolved=true",
		func(t *testing.T) {
			// Same empty-run guard as above, but the reused ended slip HAS ancestry. This
			// pins that AncestryResolved=true does NOT depend on the loaded slip's own
			// Ancestry field one way or the other (see B6 above) - true in both the
			// no-ancestry and has-ancestry cases, for the same reason (dedup onto a
			// pre-existing slip, nothing was resolved).
			store := NewMockStore()
			github := NewMockGitHubAPI()
			client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

			store.AddSlip(&Slip{
				CorrelationID: "corr-real-run-ancestry",
				Repository:    "owner/repo",
				Branch:        "integration",
				CommitSHA:     "sha-branch-create-ancestry",
				Status:        SlipStatusCompleted,
				Steps:         map[string]Step{"builds": {Status: StepStatusCompleted}},
				StateHistory:  []StateHistoryEntry{},
				Ancestry: []AncestryEntry{
					{CorrelationID: "corr-ancestor", CommitSHA: "sha-ancestor", Status: SlipStatusCompleted},
				},
			})

			result, err := client.CreateSlipForPush(ctx, PushOptions{
				CorrelationID: "corr-branch-create-ancestry",
				Repository:    "owner/repo",
				Branch:        "feature/new-branch",
				CommitSHA:     "sha-branch-create-ancestry",
				Components:    nil, // no work to dispatch
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Slip.CorrelationID != "corr-real-run-ancestry" {
				t.Errorf("guard must return the existing run, got %q", result.Slip.CorrelationID)
			}
			if !result.AncestryResolved {
				t.Error("expected AncestryResolved=true for a reused slip that has ancestry")
			}
		},
	)

	t.Run(
		"empty-run guard with superseded-terminal row - abandoned/promoted can now escape as result.Slip",
		func(t *testing.T) {
			// B8 (review fix): the empty-run guard was only ever exercised with a
			// `completed` existing row. Before B5's fix, the backstop's live-vs-ended
			// check was NOT applied consistently, but the MAIN path's guard already
			// covered abandoned/promoted, they just were not asserted. Pin this
			// explicitly for supersede-terminal statuses (abandoned - superseded by a
			// direct push - and promoted - superseded via squash-merge PR): such a slip
			// can now be returned as result.Slip from CreateSlipForPush, which could not
			// happen before the guard existed (any terminal row used to always be
			// repaved, never returned to the caller).
			for _, termStatus := range []SlipStatus{SlipStatusAbandoned, SlipStatusPromoted} {
				termStatus := termStatus
				t.Run(string(termStatus), func(t *testing.T) {
					store := NewMockStore()
					github := NewMockGitHubAPI()
					client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

					commitSHA := "sha-superseded-terminal-" + string(termStatus)
					store.AddSlip(&Slip{
						CorrelationID: "corr-superseded-terminal-" + string(termStatus),
						Repository:    "owner/repo",
						Branch:        "integration",
						CommitSHA:     commitSHA,
						Status:        termStatus,
						Steps:         map[string]Step{"builds": {Status: StepStatusCompleted}},
						StateHistory:  []StateHistoryEntry{},
					})

					result, err := client.CreateSlipForPush(ctx, PushOptions{
						CorrelationID: "corr-branch-create-" + string(termStatus),
						Repository:    "owner/repo",
						Branch:        "feature/new-branch-" + string(termStatus),
						CommitSHA:     commitSHA,
						Components:    nil, // no work to dispatch
					})
					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
					if result.Slip == nil ||
						result.Slip.CorrelationID != "corr-superseded-terminal-"+string(termStatus) {
						t.Errorf("expected guard to return the existing %s slip, got %+v", termStatus, result.Slip)
					}
					if result.Slip.Status != termStatus {
						t.Errorf("expected returned slip to keep status %s, got %s", termStatus, result.Slip.Status)
					}
					if len(store.RepaveCalls) != 0 {
						t.Errorf("[%s] guard must not repave, got Repave calls: %v", termStatus, store.RepaveCalls)
					}
					if len(store.CreateCalls) != 0 {
						t.Errorf("[%s] guard must not create, got %d Create calls", termStatus, len(store.CreateCalls))
					}
					if !result.AncestryResolved {
						t.Errorf("[%s] expected AncestryResolved=true (dedup onto a pre-existing slip)", termStatus)
					}
				})
			}
		},
	)

	t.Run("validation error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		opts := PushOptions{
			// Missing required fields
		}

		_, err := client.CreateSlipForPush(ctx, opts)
		if err == nil {
			t.Fatal("expected validation error")
		}
		// Error occurred as expected - validation failure
	})

	t.Run("store create error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		store.CreateError = errors.New("database unavailable")

		opts := PushOptions{
			CorrelationID: "corr-push-err",
			Repository:    "owner/repo",
			CommitSHA:     "errabc",
		}

		_, err := client.CreateSlipForPush(ctx, opts)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("retry - UpdateStep error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		// Pre-create an existing slip
		existingSlip := &Slip{
			CorrelationID: "corr-push-retry-err",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "retryerr123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusFailed},
			},
		}
		store.AddSlip(existingSlip)
		store.UpdateStepError = errors.New("update step failed")

		opts := PushOptions{
			CorrelationID: "new-corr",
			Repository:    "owner/repo",
			CommitSHA:     "retryerr123", // Same commit
		}

		_, err := client.CreateSlipForPush(ctx, opts)
		if err == nil {
			t.Fatal("expected error from UpdateStep failure")
		}
	})

	t.Run("retry - history write-back error is non-fatal", func(t *testing.T) {
		// handlePushRetry routes through UpdateStepWithHistory, which adopts the real
		// store's best-effort history write-back semantics (#75): the event/step-status
		// write is already durable, so a history write-back failure is Warn-logged and
		// swallowed, not propagated. The state_history audit entry for this transition is
		// lost, but retry processing (and CreateSlipForPush) must still succeed. Event
		// insert / gate-check failures (simulated by UpdateStepError, see the
		// "retry - UpdateStep error" case above) still hard-fail.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		existingSlip := &Slip{
			CorrelationID: "corr-push-hist-err",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "histerr123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusFailed},
			},
		}
		store.AddSlip(existingSlip)
		store.AppendHistoryError = errors.New("history append failed")

		opts := PushOptions{
			CorrelationID: "new-corr",
			Repository:    "owner/repo",
			CommitSHA:     "histerr123",
		}

		result, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("expected no error (history write-back failures are best-effort), got: %v", err)
		}
		if result == nil {
			t.Fatal("expected a slip to be returned")
		}
		if len(store.SwallowedHistoryErrors) != 1 {
			t.Errorf(
				"expected the history write-back failure to be recorded as swallowed, got %d",
				len(store.SwallowedHistoryErrors),
			)
		}
	})

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
		if len(store.RepaveCalls) != 0 {
			t.Error("in-flight slip must never be repaved")
		}
	})

	t.Run("duplicate-create backstop - repaves and retries once", func(t *testing.T) {
		// Redis-lock fail-open race: two creates for the same new commit; the loser's
		// INSERT hits the unique index (ErrDuplicateSlip). The backstop loads the
		// winner row, repaves it, and retries the create once.
		//
		// NOTE: this arrangement FIRST hits Task 3's normal repave (an ended slip for
		// sha-race already exists), which deletes the winner row and clears its commit
		// index entry before Create is even attempted. That leaves nothing for the
		// backstop's own LoadByCommit to find once the injected ErrDuplicateSlip fires,
		// so the backstop retries Create directly without a second delete. What matters
		// is the FINAL state: create succeeds, no error surfaces, and at least one
		// repave/backstop delete happened along the way.
		//
		// CreateErrorFor fires on every attempt for an id, which can't express "fail
		// first, succeed on retry" - use the one-shot CreateErrorOnce instead.
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
		store.CreateErrorOnce["corr-race-loser"] = ErrDuplicateSlip

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
		if result.Slip == nil || result.Slip.CorrelationID != "corr-race-loser" {
			t.Errorf("expected corr-race-loser to be created after backstop retry, got %+v", result.Slip)
		}
		if _, ok := store.Slips["corr-race-loser"]; !ok {
			t.Error("expected corr-race-loser to be persisted after backstop retry")
		}
		if len(store.RepaveCalls) < 1 {
			t.Errorf("expected at least one repave/backstop delete, got %v", store.RepaveCalls)
		}
	})

	t.Run("duplicate-create backstop - live conflicting row is not deleted (dedup)", func(t *testing.T) {
		// The real race this backstop exists for: no existing row for this commit
		// (both pushes see not-found on the initial LoadByCommit), both call Create.
		// The winner's insert lands WHILE the loser's Create is in flight - a live
		// (in_progress) row now exists that did not exist a moment ago - and the
		// loser's insert hits the unique index (ErrDuplicateSlip).
		//
		// Unlike the subtest above (whose fixture seeds a TERMINAL winner that the
		// normal repave block deletes before Create is ever attempted, so the
		// backstop's LoadByCommit finds nothing), this fixture must make the winner
		// row appear strictly BETWEEN the loser's initial LoadByCommit (must find
		// nothing) and the backstop's LoadByCommit (must find the live winner).
		// SeedOnCreate injects the winner at the moment Create is called, achieving
		// exactly that ordering.
		//
		// The live winner's pipeline may already be dispatched under its
		// correlation_id, so the backstop must treat it like the reuse branch does:
		// dedup onto the winner, do NOT delete it, and do NOT retry Create.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		winner := &Slip{
			CorrelationID: "corr-race-winner-live",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-race-live",
			Status:        SlipStatusInProgress, // still live - pipeline may be dispatched
			Steps:         map[string]Step{"builds": {Status: StepStatusRunning}},
			StateHistory:  []StateHistoryEntry{},
		}
		store.SeedOnCreate["corr-race-loser-live"] = winner
		store.CreateErrorOnce["corr-race-loser-live"] = ErrDuplicateSlip

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-race-loser-live",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-race-live",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Slip == nil || result.Slip.CorrelationID != "corr-race-winner-live" {
			t.Errorf("expected dedup onto the live winner corr-race-winner-live, got %+v", result.Slip)
		}
		for _, deleted := range store.RepaveCalls {
			if deleted == "corr-race-winner-live" {
				t.Errorf("live conflicting slip must never be repaved, got Repave calls: %v", store.RepaveCalls)
			}
		}
		if len(store.CreateCalls) != 1 {
			t.Errorf("expected exactly one Create attempt (no retry after dedup), got %d", len(store.CreateCalls))
		}
		// B6 (review fix): the backstop dedup path also dedups onto a pre-existing
		// slip (winner has no Ancestry set), so AncestryResolved must be true here too.
		// Note this holds because resolveAndAbandonAncestors SUCCEEDED (the GitHub mock is
		// healthy) and set it true on its own — not because the backstop forces it. The
		// subtest below covers the case where resolution failed.
		if !result.AncestryResolved {
			t.Error("expected AncestryResolved=true for the backstop live-conflict dedup")
		}
	})

	t.Run("duplicate-create backstop - dedup must not force AncestryResolved over a real failure",
		func(t *testing.T) {
			// AncestryResolved describes THIS push's resolution attempt, wherever an attempt
			// happened — not the provenance of the slip being returned. The backstop runs
			// AFTER resolveAndAbandonAncestors, so by the time it dedups, the field already
			// holds that attempt's real outcome.
			//
			// Forcing true here would clobber a legitimate false whose failure is already
			// recorded in result.Warnings, producing AncestryResolved=true sitting next to an
			// ancestry error during a GitHub outage — a self-contradictory result that would
			// misfire any alerting keyed on the field. That is the same reasoning the two
			// went-live abort paths already state (D3.2); it applies identically here.
			//
			// The "returns someone else's slip, so nothing needed resolving" reading does not
			// discriminate: the ended-conflict repave branch below also returns a reloaded
			// conflicting row and deliberately preserves the computed value.
			store := NewMockStore()
			github := NewMockGitHubAPI()
			github.GetCommitAncestryError = errors.New("github is down")
			client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

			winner := &Slip{
				CorrelationID: "corr-outage-winner",
				Repository:    "owner/repo",
				Branch:        "integration",
				CommitSHA:     "sha-outage",
				Status:        SlipStatusInProgress,
				Steps:         map[string]Step{"builds": {Status: StepStatusRunning}},
				StateHistory:  []StateHistoryEntry{},
			}
			store.SeedOnCreate["corr-outage-loser"] = winner
			store.CreateErrorOnce["corr-outage-loser"] = ErrDuplicateSlip

			result, err := client.CreateSlipForPush(ctx, PushOptions{
				CorrelationID: "corr-outage-loser",
				Repository:    "owner/repo",
				Branch:        "integration",
				CommitSHA:     "sha-outage",
				Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Slip == nil || result.Slip.CorrelationID != "corr-outage-winner" {
				t.Fatalf("expected dedup onto the live winner, got %+v", result.Slip)
			}
			if len(result.Warnings) == 0 {
				t.Fatal("expected the ancestry failure to be recorded as a warning")
			}
			if result.AncestryResolved {
				t.Errorf("AncestryResolved must stay false when resolution failed for this push; "+
					"true alongside %d warning(s) contradicts them", len(result.Warnings))
			}
		})

	t.Run("duplicate-create backstop - ended conflicting row (completed) is repaved and retried", func(t *testing.T) {
		// The ENDED counterpart to "duplicate-create backstop - live conflicting row is
		// not deleted (dedup)" above: the conflicting row the BACKSTOP ITSELF finds (via
		// its own LoadByCommit, not the caller's initial one) is terminal, so it must be
		// repaved and Create retried - unlike the live case, which must dedup without
		// deleting.
		//
		// SeedOnCreate (not AddSlip) is required here: AddSlip would make the row
		// visible to the caller's INITIAL LoadByCommit, which would route through the
		// normal repave block in CreateSlipForPush and delete it before Create is ever
		// attempted - never exercising handleDuplicateSlipBackstop's own ended-row
		// branch at all (see the NOTE on "repaves and retries once" above, which hits
		// exactly that shape). SeedOnCreate instead makes the row appear at the moment
		// Create is called, so only the backstop's LoadByCommit ever sees it.
		//
		// Kills mutants that survive today: replacing the live-check
		// (`!conflicting.Status.IsTerminal() && conflicting.Status != SlipStatusFailed`)
		// with `if true` (would treat this ended row as live: dedup onto the conflicting
		// id, never delete, never retry), and dropping the `!conflicting.Status.IsTerminal()`
		// half of the conjunction.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		conflicting := &Slip{
			CorrelationID: "corr-conflict-completed",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-backstop-ended",
			Status:        SlipStatusCompleted,
			Steps:         map[string]Step{"builds": {Status: StepStatusCompleted}},
			StateHistory:  []StateHistoryEntry{},
		}
		store.SeedOnCreate["corr-caller-completed"] = conflicting
		store.CreateErrorOnce["corr-caller-completed"] = ErrDuplicateSlip

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-caller-completed",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-backstop-ended",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Slip == nil || result.Slip.CorrelationID != "corr-caller-completed" {
			t.Errorf("expected the caller's own new slip (not the conflicting one), got %+v", result.Slip)
		}
		foundRepave := false
		for _, superseded := range store.RepaveCalls {
			if superseded == "corr-conflict-completed" {
				foundRepave = true
			}
		}
		if !foundRepave {
			t.Errorf(
				"expected the ended conflicting row to be repaved, got Repave calls: %v",
				store.RepaveCalls,
			)
		}
		// The backstop's repave replaces the conflicting row WITH the caller's successor in
		// one transaction, so there is no second Create attempt to make: the first (failed)
		// attempt is the only one, and the successor exists because the repave wrote it.
		callerCreates := 0
		for _, call := range store.CreateCalls {
			if call.Slip.CorrelationID == "corr-caller-completed" {
				callerCreates++
			}
		}
		if callerCreates != 1 {
			t.Errorf(
				"expected one Create attempt (the failed one); the repave supplies the successor, got %d",
				callerCreates,
			)
		}
		if len(store.RepaveSuccessorCalls) != 1 || store.RepaveSuccessorCalls[0] != "corr-caller-completed" {
			t.Errorf("expected the caller's slip to be the repave successor, got %v", store.RepaveSuccessorCalls)
		}
		if _, ok := store.Slips["corr-caller-completed"]; !ok {
			t.Error("the caller's successor must be persisted by the backstop's repave")
		}
	})

	t.Run("duplicate-create backstop - ended conflicting row (failed) is repaved and retried", func(t *testing.T) {
		// Same shape as the completed case above, but the conflicting row is
		// SlipStatusFailed. Failed is non-terminal (IsTerminal()==false - a pipeline may
		// still recover), so this specifically pins the `conflicting.Status !=
		// SlipStatusFailed` half of the live-check conjunction: a mutant dropping just
		// that half would see `!conflicting.Status.IsTerminal()` alone (true for
		// failed) and wrongly treat this row as live, deduping onto it instead of
		// repaving and retrying.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		conflicting := &Slip{
			CorrelationID: "corr-conflict-failed",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-backstop-ended-failed",
			Status:        SlipStatusFailed,
			Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
			StateHistory:  []StateHistoryEntry{},
		}
		store.SeedOnCreate["corr-caller-failed"] = conflicting
		store.CreateErrorOnce["corr-caller-failed"] = ErrDuplicateSlip

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-caller-failed",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-backstop-ended-failed",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Slip == nil || result.Slip.CorrelationID != "corr-caller-failed" {
			t.Errorf("expected the caller's own new slip (not the conflicting one), got %+v", result.Slip)
		}
		foundRepave := false
		for _, superseded := range store.RepaveCalls {
			if superseded == "corr-conflict-failed" {
				foundRepave = true
			}
		}
		if !foundRepave {
			t.Errorf(
				"expected the failed conflicting row to be repaved, got Repave calls: %v",
				store.RepaveCalls,
			)
		}
		callerCreates := 0
		for _, call := range store.CreateCalls {
			if call.Slip.CorrelationID == "corr-caller-failed" {
				callerCreates++
			}
		}
		if callerCreates != 1 {
			t.Errorf(
				"expected one Create attempt (the failed one); the repave supplies the successor, got %d",
				callerCreates,
			)
		}
		if _, ok := store.Slips["corr-caller-failed"]; !ok {
			t.Error("the caller's successor must be persisted by the backstop's repave")
		}
	})

	t.Run("duplicate-create backstop - LoadByCommit returns (nil, nil) falls through to retry", func(t *testing.T) {
		// Defensive-path coverage for handleDuplicateSlipBackstop's
		// `loadErr != nil || conflicting == nil` guard. No known real store returns
		// (nil, nil) from LoadByCommit - a miss always carries ErrSlipNotFound - but the
		// guard is written to be safe against it anyway (DEVOPS-231). A mutant flipping
		// `||` to `&&` would fail this exact input: (nil error, nil slip) makes the
		// `&&` false, so the code would fall through to `conflicting.Status` and panic
		// on the nil pointer instead of falling through to the caller's single retry.
		//
		// The hook is call-indexed (LoadByCommitNilOnCall = 2) because CreateSlipForPush's
		// own initial lookup is LoadByCommit call 1. Call 1 is left to miss normally, so
		// there is no row to repave and the push goes down the plain-create path; the
		// duplicate on that Create is what invokes the backstop, whose own lookup is call 2.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		store.CreateErrorOnce["corr-caller-nilnil"] = ErrDuplicateSlip
		store.LoadByCommitNilOnCall = 2

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-caller-nilnil",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-nilnil",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Slip == nil || result.Slip.CorrelationID != "corr-caller-nilnil" {
			t.Errorf("expected the caller's own new slip after the fall-through retry, got %+v", result.Slip)
		}
		// The guard must have sent the backstop down the fall-through path rather than the
		// repave path: with no conflicting slip to name, nothing may be repaved.
		if len(store.RepaveCalls) != 0 {
			t.Errorf("a (nil, nil) lookup gives the backstop nothing to repave, got %v", store.RepaveCalls)
		}
		if len(store.LoadByCommitCalls) < 2 {
			t.Errorf("expected the backstop to make its own LoadByCommit call, got %d total",
				len(store.LoadByCommitCalls))
		}
		callerCreates := 0
		for _, call := range store.CreateCalls {
			if call.Slip.CorrelationID == "corr-caller-nilnil" {
				callerCreates++
			}
		}
		if callerCreates != 2 {
			t.Errorf("expected the failed Create plus the fall-through retry, got %d", callerCreates)
		}
	})

	t.Run("B1: repave returns ErrSlipWentLive - dedups onto the (reloaded) live slip, no create", func(t *testing.T) {
		// The slip went live between the repave decision (existingSlip.Status was
		// ended) and the Repave call (e.g. executor.go's recovery branch flipped
		// it back to in_progress). Repave's status guard rejects the replacement and
		// returns ErrSlipWentLive, having written nothing. Creating a fresh slip here
		// would produce two competing live runs for the same commit (nothing at the DB
		// level stops that pre-index). The repave must be abandoned and treated exactly
		// like the live-reuse case: dedup onto the slip, reloaded so the returned copy
		// reflects its current (live) state.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		store.AddSlip(&Slip{
			CorrelationID: "corr-went-live",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-went-live",
			Status:        SlipStatusFailed, // ended at decision time
			Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
			StateHistory:  []StateHistoryEntry{},
		})
		// Simulate the slip transitioning to live in the window between the repave
		// decision (the LoadByCommit above sees it as Failed/ended) and the
		// Repave call: RepaveWentLiveStatus mutates the stored row's status
		// at the moment Repave is invoked, so the code's reload-after-error
		// observes the new state rather than the stale decision-time snapshot.
		store.RepaveError = ErrSlipWentLive
		store.RepaveWentLiveStatus = map[string]SlipStatus{"corr-went-live": SlipStatusInProgress}

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-would-be-fresh",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-went-live",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Slip == nil || result.Slip.CorrelationID != "corr-went-live" {
			t.Errorf("expected dedup onto the now-live slip corr-went-live, got %+v", result.Slip)
		}
		if result.Slip.Status != SlipStatusInProgress {
			t.Errorf("expected the returned slip to reflect its reloaded live status, got %s", result.Slip.Status)
		}
		if len(store.CreateCalls) != 0 {
			t.Errorf("must not create a fresh slip when the existing one went live, got %d Create calls",
				len(store.CreateCalls))
		}
		if !result.AncestryResolved {
			t.Error("expected AncestryResolved=true (dedup onto a pre-existing slip)")
		}
	})

	t.Run(
		"B2: repave delete returns ErrRepaveUnsupported - falls back to AbandonSlip, fresh slip still created",
		func(t *testing.T) {
			// ClickHouse-backed stores implement no delete path (DEVOPS-127: Postgres is
			// the operational slip store). NewClient still builds a ClickHouseStore
			// unconditionally, so a CH-backed client's repave attempts hit this on every
			// same-commit push. The fallback restores the pre-DEVOPS-231 behavior:
			// abandon the superseded slip instead of repaving it, then still create the
			// fresh slip so the caller sees a new correlation_id and re-dispatches.
			store := NewMockStore()
			github := NewMockGitHubAPI()
			config := testPipelineConfig()
			client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

			store.AddSlip(&Slip{
				CorrelationID: "corr-ch-old",
				Repository:    "owner/repo",
				Branch:        "main",
				CommitSHA:     "ch-commit",
				Status:        SlipStatusFailed,
				Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
				StateHistory:  []StateHistoryEntry{},
			})
			store.RepaveError = ErrRepaveUnsupported

			result, err := client.CreateSlipForPush(ctx, PushOptions{
				CorrelationID: "corr-ch-new",
				Repository:    "owner/repo",
				Branch:        "main",
				CommitSHA:     "ch-commit",
				Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Slip == nil || result.Slip.CorrelationID != "corr-ch-new" {
				t.Errorf("expected the fresh slip to still be created, got %+v", result.Slip)
			}
			if len(store.CreateCalls) != 1 {
				t.Errorf("expected 1 Create call for the fresh slip, got %d", len(store.CreateCalls))
			}
			foundAbandon := false
			for _, call := range store.UpdateSlipStatusCalls {
				if call.CorrelationID == "corr-ch-old" && call.Status == SlipStatusAbandoned {
					foundAbandon = true
				}
			}
			if !foundAbandon {
				t.Error("expected AbandonSlip fallback to mark the old slip abandoned")
			}
			// D3.3 (DEVOPS-231 review): zero warnings are expected on this successful-create
			// path. AbandonSlip succeeds here (the old slip is Failed, not terminal), and this
			// fallback fires on EVERY same-commit push against a ClickHouse-backed client -
			// treating that as a Warning would misfire any consumer that alerts on
			// len(result.Warnings) > 0 for what is a routine webhook redelivery. A Warning is
			// only added when AbandonSlip itself fails (see the sibling "already terminal"
			// and "abandon fails" subtests below).
			if len(result.Warnings) != 0 {
				t.Fatalf("expected 0 warnings for a successful create via the unsupported-repave fallback, got %d: %v",
					len(result.Warnings), result.Warnings)
			}
		},
	)

	t.Run("D3.3: repave fallback on unsupported store - already-terminal slip is not falsely claimed abandoned",
		func(t *testing.T) {
			// AbandonSlip's checkTerminalStatus (client.go) silently no-ops for an
			// already-terminal slip. LoadByCommit's unfiltered lookup means exactly these
			// rows reach this fallback (a terminal existing slip is repaved just like a
			// failed one). Before D3.3, the fallback unconditionally claimed "abandoned
			// instead" and always added a warning, even though nothing changed here.
			store := NewMockStore()
			github := NewMockGitHubAPI()
			client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

			store.AddSlip(&Slip{
				CorrelationID: "corr-ch-terminal",
				Repository:    "owner/repo",
				Branch:        "main",
				CommitSHA:     "ch-commit-terminal",
				Status:        SlipStatusCompleted, // already terminal: AbandonSlip would no-op
				Steps:         map[string]Step{"builds": {Status: StepStatusCompleted}},
				StateHistory:  []StateHistoryEntry{},
			})
			store.RepaveError = ErrRepaveUnsupported

			result, err := client.CreateSlipForPush(ctx, PushOptions{
				CorrelationID: "corr-ch-new-terminal",
				Repository:    "owner/repo",
				Branch:        "main",
				CommitSHA:     "ch-commit-terminal",
				Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Slip == nil || result.Slip.CorrelationID != "corr-ch-new-terminal" {
				t.Errorf("expected the fresh slip to still be created, got %+v", result.Slip)
			}
			for _, call := range store.UpdateSlipStatusCalls {
				if call.CorrelationID == "corr-ch-terminal" {
					t.Errorf("AbandonSlip must not be invoked on an already-terminal slip "+
						"(it would silently no-op); got status update call %+v", call)
				}
			}
			if len(result.Warnings) != 0 {
				t.Errorf("expected no warnings for the routine ClickHouse unsupported-repave case, got %d: %v",
					len(result.Warnings), result.Warnings)
			}
		})

	t.Run("D3.3: repave fallback on unsupported store - AbandonSlip failure is still surfaced as a warning",
		func(t *testing.T) {
			store := NewMockStore()
			github := NewMockGitHubAPI()
			client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

			store.AddSlip(&Slip{
				CorrelationID: "corr-ch-abandon-fail",
				Repository:    "owner/repo",
				Branch:        "main",
				CommitSHA:     "ch-commit-abandon-fail",
				Status:        SlipStatusFailed,
				Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
				StateHistory:  []StateHistoryEntry{},
			})
			store.RepaveError = ErrRepaveUnsupported
			store.UpdateSlipStatusError = errors.New("store unavailable")

			result, err := client.CreateSlipForPush(ctx, PushOptions{
				CorrelationID: "corr-ch-new-abandon-fail",
				Repository:    "owner/repo",
				Branch:        "main",
				CommitSHA:     "ch-commit-abandon-fail",
				Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Slip == nil || result.Slip.CorrelationID != "corr-ch-new-abandon-fail" {
				t.Errorf("expected the fresh slip to still be created, got %+v", result.Slip)
			}
			if len(result.Warnings) != 1 {
				t.Fatalf("expected exactly 1 warning (the genuine abandon failure), got %d: %v",
					len(result.Warnings), result.Warnings)
			}
			if !strings.Contains(result.Warnings[0].Error(), "failed to abandon") {
				t.Errorf("expected a warning recording the AbandonSlip failure, got: %v", result.Warnings[0])
			}
		})

	t.Run("D3.3: repave log only claims a repave after the delete actually succeeds", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		logs := &capturingLogger{}
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig(), Logger: logs})

		store.AddSlip(&Slip{
			CorrelationID: "corr-ch-log-unsupported",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "ch-commit-log",
			Status:        SlipStatusFailed,
			Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
			StateHistory:  []StateHistoryEntry{},
		})
		store.RepaveError = ErrRepaveUnsupported

		_, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-ch-log-new",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "ch-commit-log",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, call := range logs.calls {
			if strings.Contains(call.message, "delete + recreate") {
				t.Errorf("must not claim a repave (delete + recreate) happened when the "+
					"store rejected the delete; got log: %+v", call)
			}
		}
	})

	t.Run("D3.3: repave log fires only once the delete actually succeeds", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		logs := &capturingLogger{}
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig(), Logger: logs})

		store.AddSlip(&Slip{
			CorrelationID: "corr-ch-log-success",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "ch-commit-log-ok",
			Status:        SlipStatusFailed,
			Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
			StateHistory:  []StateHistoryEntry{},
		})

		_, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-ch-log-new-ok",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "ch-commit-log-ok",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := false
		for _, call := range logs.calls {
			if strings.Contains(call.message, "Repaved") {
				found = true
			}
		}
		if !found {
			t.Error("expected a 'Repaved' log after a successful repave delete")
		}
	})

	t.Run("B4: LoadByCommit fails with a non-not-found error - returns the error, does not create", func(t *testing.T) {
		// A DB timeout or connection-refused error must not be treated as "no
		// existing slip" - that would let Create insert a second row while a LIVE
		// slip for this commit might already exist, and the caller would fully
		// re-dispatch a build that's already running. Failing the message (so Kafka
		// redelivers) is safer than guessing.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})
		store.LoadByCommitError = errors.New("connection refused")

		_, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-lookup-fail",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "sha-lookup-fail",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		})
		if err == nil {
			t.Fatal("expected an error from the non-not-found LoadByCommit failure")
		}
		if !strings.Contains(err.Error(), "connection refused") {
			t.Errorf("expected the underlying error to be wrapped, got: %v", err)
		}
		if len(store.CreateCalls) != 0 {
			t.Errorf("expected no Create call on lookup failure, got %d", len(store.CreateCalls))
		}
	})

	t.Run("B5: duplicate-create backstop applies the empty-run guard - dedups without deleting", func(t *testing.T) {
		// The backstop used to omit the empty-run guard the main path applies,
		// diverging on identical inputs: a componentless push racing into the
		// backstop against an ended conflicting row would repave (delete) it, even
		// though nothing would be dispatched and the main path would have returned
		// the existing slip untouched. Fixed by applying the same guard at both
		// sites.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		conflicting := &Slip{
			CorrelationID: "corr-conflict-empty-run",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-backstop-empty-run",
			Status:        SlipStatusCompleted,
			Steps:         map[string]Step{"builds": {Status: StepStatusCompleted}},
			StateHistory:  []StateHistoryEntry{},
		}
		store.SeedOnCreate["corr-caller-empty"] = conflicting
		store.CreateErrorOnce["corr-caller-empty"] = ErrDuplicateSlip

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-caller-empty",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-backstop-empty-run",
			Components:    nil, // componentless push
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Slip == nil || result.Slip.CorrelationID != "corr-conflict-empty-run" {
			t.Errorf("expected dedup onto the conflicting slip, got %+v", result.Slip)
		}
		for _, superseded := range store.RepaveCalls {
			if superseded == "corr-conflict-empty-run" {
				t.Errorf("componentless push must not repave the conflicting slip, got Repave calls: %v",
					store.RepaveCalls)
			}
		}
		if len(store.CreateCalls) != 1 {
			t.Errorf("expected exactly one Create attempt (no retry after dedup), got %d", len(store.CreateCalls))
		}
		if !result.AncestryResolved {
			t.Error("expected AncestryResolved=true (dedup onto a pre-existing slip)")
		}
	})

	t.Run(
		"D3.1: duplicate-create backstop - conflicting repave returns ErrSlipWentLive - dedups onto reloaded live slip",
		func(t *testing.T) {
			// Before D3.1, handleDuplicateSlipBackstop treated EVERY repave error as
			// fatal, including the two sentinels its own doc comment claims it handles
			// "exactly like the main path". Post-Phase-B, a lost insert race whose
			// conflicting row goes live between the backstop's decision and its repave
			// would fail the WHOLE message instead of deduping onto the live run - the
			// exact outcome the main path's went-live branch (repaveExistingSlip) exists
			// to avoid.
			store := NewMockStore()
			github := NewMockGitHubAPI()
			client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

			conflicting := &Slip{
				CorrelationID: "corr-conflict-went-live",
				Repository:    "owner/repo",
				Branch:        "integration",
				CommitSHA:     "sha-backstop-went-live",
				Status:        SlipStatusFailed, // ended at the backstop's decision time
				Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
				StateHistory:  []StateHistoryEntry{},
			}
			store.SeedOnCreate["corr-caller-went-live"] = conflicting
			store.CreateErrorOnce["corr-caller-went-live"] = ErrDuplicateSlip
			store.RepaveError = ErrSlipWentLive
			store.RepaveWentLiveStatus = map[string]SlipStatus{"corr-conflict-went-live": SlipStatusInProgress}

			result, err := client.CreateSlipForPush(ctx, PushOptions{
				CorrelationID: "corr-caller-went-live",
				Repository:    "owner/repo",
				Branch:        "integration",
				CommitSHA:     "sha-backstop-went-live",
				Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Slip == nil || result.Slip.CorrelationID != "corr-conflict-went-live" {
				t.Errorf("expected dedup onto the now-live conflicting slip, got %+v", result.Slip)
			}
			if result.Slip.Status != SlipStatusInProgress {
				t.Errorf("expected the returned slip to reflect its reloaded live status, got %s", result.Slip.Status)
			}
			callerCreates := 0
			for _, call := range store.CreateCalls {
				if call.Slip.CorrelationID == "corr-caller-went-live" {
					callerCreates++
				}
			}
			if callerCreates != 1 {
				t.Errorf("expected exactly 1 Create attempt for the caller (the failed one; no retry "+
					"after the went-live dedup), got %d", callerCreates)
			}
		},
	)

	t.Run("D3.1: duplicate-create backstop - conflicting delete returns ErrRepaveUnsupported - abandons and continues",
		func(t *testing.T) {
			store := NewMockStore()
			github := NewMockGitHubAPI()
			client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

			conflicting := &Slip{
				CorrelationID: "corr-conflict-ch",
				Repository:    "owner/repo",
				Branch:        "integration",
				CommitSHA:     "sha-backstop-ch",
				Status:        SlipStatusFailed,
				Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
				StateHistory:  []StateHistoryEntry{},
			}
			store.SeedOnCreate["corr-caller-ch"] = conflicting
			store.CreateErrorOnce["corr-caller-ch"] = ErrDuplicateSlip
			store.RepaveError = ErrRepaveUnsupported

			result, err := client.CreateSlipForPush(ctx, PushOptions{
				CorrelationID: "corr-caller-ch",
				Repository:    "owner/repo",
				Branch:        "integration",
				CommitSHA:     "sha-backstop-ch",
				Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Slip == nil || result.Slip.CorrelationID != "corr-caller-ch" {
				t.Errorf("expected the caller's own fresh slip after the unsupported-delete fallback, got %+v",
					result.Slip)
			}
			foundAbandon := false
			for _, call := range store.UpdateSlipStatusCalls {
				if call.CorrelationID == "corr-conflict-ch" && call.Status == SlipStatusAbandoned {
					foundAbandon = true
				}
			}
			if !foundAbandon {
				t.Error("expected AbandonSlip fallback to mark the conflicting slip abandoned")
			}
			callerCreates := 0
			for _, call := range store.CreateCalls {
				if call.Slip.CorrelationID == "corr-caller-ch" {
					callerCreates++
				}
			}
			if callerCreates != 2 {
				t.Errorf("expected exactly two Create attempts (failed first + retry after abandon fallback), got %d",
					callerCreates)
			}
		})

	t.Run("D3.2: repave delete returns ErrSlipWentLive - routes through handlePushRetry for audit trail",
		func(t *testing.T) {
			// Before D3.2, the went-live abort dedup skipped handlePushRetry entirely, so
			// there was no push_parsed reset and no "retry detected" history entry - no
			// audit record that a second push arrived, unlike the real IsLive() path.
			store := NewMockStore()
			github := NewMockGitHubAPI()
			client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

			store.AddSlip(&Slip{
				CorrelationID: "corr-went-live-retry",
				Repository:    "owner/repo",
				Branch:        "integration",
				CommitSHA:     "sha-went-live-retry",
				Status:        SlipStatusFailed,
				Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
				StateHistory:  []StateHistoryEntry{},
			})
			store.RepaveError = ErrSlipWentLive
			store.RepaveWentLiveStatus = map[string]SlipStatus{"corr-went-live-retry": SlipStatusInProgress}

			result, err := client.CreateSlipForPush(ctx, PushOptions{
				CorrelationID: "corr-would-be-fresh-2",
				Repository:    "owner/repo",
				Branch:        "integration",
				CommitSHA:     "sha-went-live-retry",
				Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Slip == nil || result.Slip.CorrelationID != "corr-went-live-retry" {
				t.Fatalf("expected dedup onto corr-went-live-retry, got %+v", result.Slip)
			}
			if store.UpdateStepWithHistoryCallCount != 1 {
				t.Errorf("expected exactly 1 atomic UpdateStepWithHistory call for the push_parsed retry reset, got %d",
					store.UpdateStepWithHistoryCallCount)
			}
			foundRetryEntry := false
			for _, entry := range result.Slip.StateHistory {
				if entry.Step == "push_parsed" && strings.Contains(entry.Message, "retry detected") {
					foundRetryEntry = true
				}
			}
			if !foundRetryEntry {
				t.Error(
					"expected a 'retry detected' state history entry after went-live dedup, matching the IsLive() case",
				)
			}
		})

	t.Run("D3.2: went-live abort must not clobber AncestryResolved when resolution ran and failed", func(t *testing.T) {
		// Before D3.2, this path forced AncestryResolved = true unconditionally, even
		// though resolveAndAbandonAncestors already ran (and failed) for this push before
		// the repave delete was ever attempted - clobbering the accurate false while
		// Warnings still holds the ancestry error, contradicting AncestryResolved's
		// documented meaning.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		store.AddSlip(&Slip{
			CorrelationID: "corr-went-live-ancestry",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-went-live-ancestry",
			Status:        SlipStatusFailed,
			Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
			StateHistory:  []StateHistoryEntry{},
		})
		github.GetCommitAncestryError = errors.New("github unavailable")
		store.RepaveError = ErrSlipWentLive
		store.RepaveWentLiveStatus = map[string]SlipStatus{"corr-went-live-ancestry": SlipStatusInProgress}

		result, err := client.CreateSlipForPush(ctx, PushOptions{
			CorrelationID: "corr-would-be-fresh-3",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-went-live-ancestry",
			Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Warnings) == 0 {
			t.Fatal("expected an ancestry-resolution warning to be recorded")
		}
		if result.AncestryResolved {
			t.Error("expected AncestryResolved=false: ancestry resolution ran and failed (Warnings is " +
				"non-empty), so the went-live dedup must preserve that, not force true")
		}
	})

	t.Run("D3.4: FF-merge self-ancestor - squash-merge fallback must not select the pushed commit's own ended slip",
		func(t *testing.T) {
			// Verified chain (D3.4): push of SHA X whose message references PR #N where
			// GetPRHeadCommit(#N) == X (a fast-forward merge keeps the head SHA); an ended
			// slip exists for (repo, X); no ancestors within search depth (the git-history
			// search skips X itself, so only the squash-merge fallback can find it, since
			// findSlipsInPRBranchHistory deliberately includes the head commit). Without
			// the guard, the fallback selects the existing (repo, X) slip as its own
			// "ancestor": PromoteSlip would promote it, repaveExistingSlip then deletes it
			// (same commit, ended -> repaved), and InsertAncestryLink would write the
			// newborn slip's parent pointing at the row just deleted - a dangling
			// self-reference from birth.
			store := NewMockStore()
			github := NewMockGitHubAPI()
			client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

			store.AddSlip(&Slip{
				CorrelationID: "corr-ff-self",
				Repository:    "owner/repo",
				Branch:        "main",
				CommitSHA:     "sha-ff-self",
				Status:        SlipStatusCompleted, // ended
				Steps:         map[string]Step{"builds": {Status: StepStatusCompleted}},
				StateHistory:  []StateHistoryEntry{},
			})
			// No ancestor commits at all: the git-history search must find nothing (it
			// skips the pushed commit itself), so only the squash-merge fallback can
			// surface a candidate.
			github.SetAncestry("owner", "repo", "sha-ff-self", []string{"sha-ff-self"})
			github.SetPRHeadCommit("owner", "repo", 55, "sha-ff-self") // FF merge: head == pushed commit

			result, err := client.CreateSlipForPush(ctx, PushOptions{
				CorrelationID: "corr-ff-new",
				Repository:    "owner/repo",
				Branch:        "main",
				CommitSHA:     "sha-ff-self",
				CommitMessage: "Merge pull request #55 from owner/feature",
				Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Slip == nil || result.Slip.CorrelationID != "corr-ff-new" {
				t.Fatalf("expected the fresh repaved slip, got %+v", result.Slip)
			}
			for _, entry := range result.Slip.Ancestry {
				if entry.CorrelationID == "corr-ff-self" {
					t.Errorf("must not record the just-repaved same-commit slip as its own ancestor, got ancestry: %+v",
						result.Slip.Ancestry)
				}
			}
			if _, ok := store.Slips["corr-ff-self"]; ok {
				t.Error("expected the old same-commit slip to be repaved (deleted)")
			}
		})
}

func TestDropSelfAncestorLink(t *testing.T) {
	t.Run("drops the entry matching repavedCorrelationID", func(t *testing.T) {
		ancestry := []AncestryEntry{
			{CorrelationID: "corr-parent-real", CommitSHA: "sha-parent"},
		}
		filtered := dropSelfAncestorLink(ancestry, "corr-parent-real")
		if len(filtered) != 0 {
			t.Errorf("expected the self-referential entry to be dropped, got %+v", filtered)
		}
	})

	t.Run("does not drop unrelated entries", func(t *testing.T) {
		ancestry := []AncestryEntry{
			{CorrelationID: "corr-parent-real", CommitSHA: "sha-parent"},
		}
		filtered := dropSelfAncestorLink(ancestry, "corr-something-else")
		if len(filtered) != 1 {
			t.Errorf("must not drop unrelated ancestry entries, got %+v", filtered)
		}
	})

	t.Run("nil ancestry in, nil out", func(t *testing.T) {
		if got := dropSelfAncestorLink(nil, "corr-x"); got != nil {
			t.Errorf("expected nil in, nil out, got %+v", got)
		}
	})

	t.Run("empty repavedCorrelationID is a no-op", func(t *testing.T) {
		ancestry := []AncestryEntry{{CorrelationID: "corr-parent-real"}}
		filtered := dropSelfAncestorLink(ancestry, "")
		if len(filtered) != 1 {
			t.Errorf("expected no filtering with an empty repavedCorrelationID, got %+v", filtered)
		}
	})

	t.Run("drops only the matching entry among several", func(t *testing.T) {
		ancestry := []AncestryEntry{
			{CorrelationID: "corr-a"},
			{CorrelationID: "corr-self"},
			{CorrelationID: "corr-b"},
		}
		filtered := dropSelfAncestorLink(ancestry, "corr-self")
		if len(filtered) != 2 {
			t.Fatalf("expected 2 remaining entries, got %d: %+v", len(filtered), filtered)
		}
		for _, e := range filtered {
			if e.CorrelationID == "corr-self" {
				t.Errorf("self-referential entry must be dropped, got %+v", filtered)
			}
		}
	})
}

func TestClient_InitializeSlipForPush(t *testing.T) {
	store := NewMockStore()
	github := NewMockGitHubAPI()
	config := testPipelineConfig()
	client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

	opts := PushOptions{
		CorrelationID: "corr-init-1",
		Repository:    "owner/repo",
		Branch:        "feature/test",
		CommitSHA:     "init123",
		Components: []ComponentDefinition{
			{Name: "frontend", DockerfilePath: "frontend/Dockerfile"},
			{Name: "backend", DockerfilePath: "backend/Dockerfile"},
		},
	}

	slip := client.initializeSlipForPush(opts, nil)

	// Verify basic fields
	if slip.CorrelationID != "corr-init-1" {
		t.Errorf("expected CorrelationID 'corr-init-1', got '%s'", slip.CorrelationID)
	}
	if slip.Repository != "owner/repo" {
		t.Errorf("expected Repository 'owner/repo', got '%s'", slip.Repository)
	}
	if slip.Branch != "feature/test" {
		t.Errorf("expected Branch 'feature/test', got '%s'", slip.Branch)
	}
	if slip.CommitSHA != "init123" {
		t.Errorf("expected CommitSHA 'init123', got '%s'", slip.CommitSHA)
	}
	if slip.Status != SlipStatusInProgress {
		t.Errorf("expected Status 'in_progress', got '%s'", slip.Status)
	}

	// Verify timestamps are set
	if slip.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if slip.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}

	// Verify aggregates have component data - use config to get the aggregate step name
	aggregateSteps := config.GetAggregateSteps()
	if len(aggregateSteps) == 0 {
		t.Fatal("expected at least one aggregate step in config")
	}
	aggregateColumnName := aggregateSteps[0].Name
	if len(slip.Aggregates[aggregateColumnName]) != 2 {
		t.Fatalf(
			"expected 2 components in %s aggregate, got %d",
			aggregateColumnName,
			len(slip.Aggregates[aggregateColumnName]),
		)
	}
	if slip.Aggregates[aggregateColumnName][0].Component != "frontend" {
		t.Errorf("expected first component 'frontend', got '%s'", slip.Aggregates[aggregateColumnName][0].Component)
	}
	if slip.Aggregates[aggregateColumnName][1].Component != "backend" {
		t.Errorf("expected second component 'backend', got '%s'", slip.Aggregates[aggregateColumnName][1].Component)
	}
	if slip.Aggregates[aggregateColumnName][0].Status != StepStatusPending {
		t.Errorf("expected build status 'pending', got '%s'", slip.Aggregates[aggregateColumnName][0].Status)
	}

	// Verify all pipeline steps from config are initialized
	for _, step := range config.Steps {
		if _, ok := slip.Steps[step.Name]; !ok {
			t.Errorf("expected step '%s' to be initialized", step.Name)
		}
	}

	// Verify first step is running, others pending
	firstStepName := config.Steps[0].Name
	if slip.Steps[firstStepName].Status != StepStatusRunning {
		t.Errorf("expected %s status 'running', got '%s'", firstStepName, slip.Steps[firstStepName].Status)
	}
	if slip.Steps[firstStepName].StartedAt == nil {
		t.Errorf("expected %s StartedAt to be set", firstStepName)
	}
	lastStepName := config.Steps[len(config.Steps)-1].Name
	if slip.Steps[lastStepName].Status != StepStatusPending {
		t.Errorf("expected %s status 'pending', got '%s'", lastStepName, slip.Steps[lastStepName].Status)
	}

	// Verify history
	if len(slip.StateHistory) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(slip.StateHistory))
	}
	if slip.StateHistory[0].Step != firstStepName {
		t.Errorf("expected history step '%s', got '%s'", firstStepName, slip.StateHistory[0].Step)
	}
	if slip.StateHistory[0].Status != StepStatusRunning {
		t.Errorf("expected history status 'running', got '%s'", slip.StateHistory[0].Status)
	}
	if slip.StateHistory[0].Actor != "slippy-library" {
		t.Errorf("expected history actor 'slippy-library', got '%s'", slip.StateHistory[0].Actor)
	}
}

func TestClient_InitializeSlipForPush_EmptyComponents(t *testing.T) {
	store := NewMockStore()
	github := NewMockGitHubAPI()
	config := testPipelineConfig()
	client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

	opts := PushOptions{
		CorrelationID: "corr-init-empty",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "empty123",
		Components:    []ComponentDefinition{}, // Empty
	}

	slip := client.initializeSlipForPush(opts, nil)

	// Verify aggregates have empty component data
	if slip.Aggregates == nil {
		t.Error("expected Aggregates to be initialized (not nil)")
	}
	// With no components, the aggregates should have empty arrays
	aggregateSteps := config.GetAggregateSteps()
	for _, aggStep := range aggregateSteps {
		if len(slip.Aggregates[aggStep.Name]) != 0 {
			t.Errorf("expected 0 components in %s aggregate, got %d", aggStep.Name, len(slip.Aggregates[aggStep.Name]))
		}
	}
}

// TestClient_InitializeSlipForPush_MobileApp tests initialization for mobile builds (zero components on aggregate first step).
// Mobile builds have no Docker images, so the first step should NOT be marked as RUNNING.
// This prevents the pipeline from getting stuck with a RUNNING step that has no components to complete.
func TestClient_InitializeSlipForPush_MobileApp(t *testing.T) {
	store := NewMockStore()
	github := NewMockGitHubAPI()

	// Create a config where the first step is an aggregate
	// (typical for pipelines that build components)
	config := &PipelineConfig{
		Version:     "1",
		Name:        "test-pipeline",
		Description: "Test pipeline config",
		Steps: []StepConfig{
			{
				Name:        "builds",
				Description: "Builds completed",
				Aggregates:  "build", // First step IS an aggregate
			},
			{Name: "unit_tests", Description: "Unit tests", Prerequisites: []string{"builds"}},
		},
	}
	// Initialize internal lookup maps
	config.stepsByName = make(map[string]*StepConfig)
	config.aggregateMap = make(map[string]string)
	config.gateSteps = make([]string, 0)
	for i := range config.Steps {
		step := &config.Steps[i]
		step.order = i
		config.stepsByName[step.Name] = step
		if step.Aggregates != "" {
			config.aggregateMap[step.Aggregates] = step.Name
		}
	}

	client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

	opts := PushOptions{
		CorrelationID: "corr-mobile-app",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "mobile123",
		Components:    []ComponentDefinition{}, // Mobile app: zero components
	}

	slip := client.initializeSlipForPush(opts, nil)

	// CRITICAL: First step should be PENDING (not RUNNING) when it's an aggregate with zero components
	// This allows the step to auto-complete since there are no components to wait for
	firstStepName := config.Steps[0].Name
	if slip.Steps[firstStepName].Status != StepStatusPending {
		t.Errorf(
			"expected %s status 'pending' for zero-component aggregate (mobile app), got '%s'",
			firstStepName,
			slip.Steps[firstStepName].Status,
		)
	}
	if slip.Steps[firstStepName].StartedAt != nil {
		t.Errorf("expected %s StartedAt to be nil for zero-component aggregate", firstStepName)
	}

	// Verify aggregates have empty component data
	if slip.Aggregates == nil {
		t.Error("expected Aggregates to be initialized (not nil)")
	}
	if len(slip.Aggregates["builds"]) != 0 {
		t.Errorf("expected 0 components in builds aggregate, got %d", len(slip.Aggregates["builds"]))
	}
}

func TestClient_resolveAndAbandonAncestors(t *testing.T) {
	ctx := context.Background()

	t.Run("no ancestor commits found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// No ancestry configured - GetCommitAncestry returns empty
		opts := PushOptions{
			CorrelationID: "corr-new-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
		}

		ancestry, warnings := client.resolveAndAbandonAncestors(ctx, opts)
		if len(warnings) > 0 {
			t.Fatalf("unexpected warnings: %v", warnings)
		}

		if ancestry != nil {
			t.Errorf("expected nil ancestry, got %v", ancestry)
		}
	})

	t.Run("finds and abandons ancestor slip", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// Setup: ancestor slip exists at commit "parent123"
		now := time.Now()
		ancestorSlip := &Slip{
			CorrelationID: "corr-ancestor-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "parent123",
			CreatedAt:     now.Add(-10 * time.Minute),
			UpdatedAt:     now.Add(-10 * time.Minute),
			Status:        SlipStatusInProgress, // Non-terminal - should be abandoned
			Steps:         make(map[string]Step),
		}
		store.AddSlip(ancestorSlip)

		// Configure GitHub to return ancestry chain
		github.SetAncestry("owner", "repo", "abc123", []string{"abc123", "parent123", "grandparent456"})

		opts := PushOptions{
			CorrelationID: "corr-new-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
		}

		ancestry, warnings := client.resolveAndAbandonAncestors(ctx, opts)
		if len(warnings) > 0 {
			t.Fatalf("unexpected warnings: %v", warnings)
		}

		// Verify ancestry chain was built
		if len(ancestry) != 1 {
			t.Fatalf("expected 1 ancestry entry, got %d", len(ancestry))
		}
		if ancestry[0].CorrelationID != "corr-ancestor-1" {
			t.Errorf("expected ancestor ID 'corr-ancestor-1', got '%s'", ancestry[0].CorrelationID)
		}

		// Verify ancestor was abandoned (atomic status path)
		if len(store.UpdateSlipStatusCalls) != 1 {
			t.Fatalf("expected 1 UpdateSlipStatus call (abandon), got %d", len(store.UpdateSlipStatusCalls))
		}
		if store.UpdateSlipStatusCalls[0].Status != SlipStatusAbandoned {
			t.Errorf("expected ancestor to be abandoned, got status '%s'", store.UpdateSlipStatusCalls[0].Status)
		}
	})

	t.Run("inherits ancestry from parent slip", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// Setup: ancestor slip with its own ancestry chain
		now := time.Now()
		ancestorSlip := &Slip{
			CorrelationID: "corr-parent-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "parent123",
			CreatedAt:     now.Add(-10 * time.Minute),
			UpdatedAt:     now.Add(-10 * time.Minute),
			Status:        SlipStatusCompleted, // Terminal - won't be abandoned
			Steps:         make(map[string]Step),
			Ancestry: []AncestryEntry{
				{
					CorrelationID: "corr-grandparent-1",
					CommitSHA:     "grandparent456",
					Status:        SlipStatusCompleted,
					CreatedAt:     now.Add(-20 * time.Minute),
				},
			},
		}
		store.AddSlip(ancestorSlip)

		// Configure GitHub to return ancestry chain
		github.SetAncestry("owner", "repo", "abc123", []string{"abc123", "parent123"})

		opts := PushOptions{
			CorrelationID: "corr-new-3",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
		}

		ancestry, warnings := client.resolveAndAbandonAncestors(ctx, opts)
		if len(warnings) > 0 {
			t.Fatalf("unexpected warnings: %v", warnings)
		}

		// With the slip_ancestry table model, resolveAndAbandonAncestors only returns
		// direct ancestors found via git history. Transitive ancestry (grandparent)
		// is resolved at query time via the slip_ancestry table, not inherited inline.
		if len(ancestry) != 1 {
			t.Fatalf("expected 1 ancestry entry (direct parent only), got %d", len(ancestry))
		}
		if ancestry[0].CorrelationID != "corr-parent-1" {
			t.Errorf("expected first entry 'corr-parent-1', got '%s'", ancestry[0].CorrelationID)
		}

		// Verify no abandonment (ancestor was terminal)
		if len(store.UpdateSlipStatusCalls) != 0 {
			t.Errorf(
				"expected 0 UpdateSlipStatus calls (ancestor was terminal), got %d",
				len(store.UpdateSlipStatusCalls),
			)
		}
	})

	t.Run("records failed step in ancestry", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// Setup: ancestor slip that failed at a specific step
		now := time.Now()
		ancestorSlip := &Slip{
			CorrelationID: "corr-failed-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "failed123",
			CreatedAt:     now.Add(-10 * time.Minute),
			UpdatedAt:     now.Add(-10 * time.Minute),
			Status:        SlipStatusFailed,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusCompleted},
				"unit_tests":  {Status: StepStatusFailed}, // This one failed
				"dev_deploy":  {Status: StepStatusPending},
			},
		}
		store.AddSlip(ancestorSlip)

		github.SetAncestry("owner", "repo", "abc123", []string{"abc123", "failed123"})

		opts := PushOptions{
			CorrelationID: "corr-new-4",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
		}

		ancestry, warnings := client.resolveAndAbandonAncestors(ctx, opts)
		if len(warnings) > 0 {
			t.Fatalf("unexpected warnings: %v", warnings)
		}

		// Verify failed step is recorded even though the slip was abandoned.
		// Failed slips are non-terminal and get abandoned when superseded by a new push,
		// but the failure context (which step failed) is captured before abandonment.
		if len(ancestry) != 1 {
			t.Fatalf("expected 1 ancestry entry, got %d", len(ancestry))
		}
		if ancestry[0].FailedStep != "unit_tests" {
			t.Errorf("expected FailedStep 'unit_tests', got '%s'", ancestry[0].FailedStep)
		}
		if ancestry[0].Status != SlipStatusAbandoned {
			t.Errorf("expected Status 'abandoned' (failed slip superseded by new push), got '%s'", ancestry[0].Status)
		}
	})

	t.Run("records error step in ancestry failedStep", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// A slip can be marked Failed due to a step with Error status (not just Failed).
		// The failedStep extraction must capture Error and Timeout statuses too.
		now := time.Now()
		ancestorSlip := &Slip{
			CorrelationID: "corr-error-step-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "error123",
			CreatedAt:     now.Add(-10 * time.Minute),
			UpdatedAt:     now.Add(-10 * time.Minute),
			Status:        SlipStatusFailed,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusCompleted},
				"unit_tests":  {Status: StepStatusError}, // Error, not Failed
				"dev_deploy":  {Status: StepStatusPending},
			},
		}
		store.AddSlip(ancestorSlip)

		github.SetAncestry("owner", "repo", "abc456", []string{"abc456", "error123"})

		opts := PushOptions{
			CorrelationID: "corr-new-err",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc456",
		}

		ancestry, warnings := client.resolveAndAbandonAncestors(ctx, opts)
		if len(warnings) > 0 {
			t.Fatalf("unexpected warnings: %v", warnings)
		}

		if len(ancestry) != 1 {
			t.Fatalf("expected 1 ancestry entry, got %d", len(ancestry))
		}
		if ancestry[0].FailedStep != "unit_tests" {
			t.Errorf("expected FailedStep 'unit_tests' (error status), got '%s'", ancestry[0].FailedStep)
		}
	})

	t.Run("records timeout step in ancestry failedStep", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		now := time.Now()
		ancestorSlip := &Slip{
			CorrelationID: "corr-timeout-step-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "timeout123",
			CreatedAt:     now.Add(-10 * time.Minute),
			UpdatedAt:     now.Add(-10 * time.Minute),
			Status:        SlipStatusFailed,
			Steps: map[string]Step{
				"push_parsed": {Status: StepStatusCompleted},
				"unit_tests":  {Status: StepStatusTimeout}, // Timeout, not Failed
				"dev_deploy":  {Status: StepStatusPending},
			},
		}
		store.AddSlip(ancestorSlip)

		github.SetAncestry("owner", "repo", "abc789", []string{"abc789", "timeout123"})

		opts := PushOptions{
			CorrelationID: "corr-new-timeout",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc789",
		}

		ancestry, warnings := client.resolveAndAbandonAncestors(ctx, opts)
		if len(warnings) > 0 {
			t.Fatalf("unexpected warnings: %v", warnings)
		}

		if len(ancestry) != 1 {
			t.Fatalf("expected 1 ancestry entry, got %d", len(ancestry))
		}
		if ancestry[0].FailedStep != "unit_tests" {
			t.Errorf("expected FailedStep 'unit_tests' (timeout status), got '%s'", ancestry[0].FailedStep)
		}
	})

	t.Run("invalid repository format", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		opts := PushOptions{
			CorrelationID: "corr-invalid",
			Repository:    "invalid-repo-format", // Missing owner/repo separator
			CommitSHA:     "abc123",
		}

		_, warnings := client.resolveAndAbandonAncestors(ctx, opts)
		if len(warnings) == 0 {
			t.Fatal("expected warning for invalid repository format")
		}
	})

	t.Run("GitHub API error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		github.GetCommitAncestryError = errors.New("GitHub API unavailable")
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		opts := PushOptions{
			CorrelationID: "corr-err-1",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
		}

		_, warnings := client.resolveAndAbandonAncestors(ctx, opts)
		if len(warnings) == 0 {
			t.Fatal("expected warning from GitHub API")
		}
	})

	t.Run("store FindAllByCommits error", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		store.FindAllByCommitsError = errors.New("database unavailable")
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// Configure GitHub to return ancestry
		github.SetAncestry("owner", "repo", "abc123", []string{"abc123", "parent123"})

		opts := PushOptions{
			CorrelationID: "corr-err-2",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
		}

		_, warnings := client.resolveAndAbandonAncestors(ctx, opts)
		if len(warnings) == 0 {
			t.Fatal("expected warning from store")
		}
	})

	t.Run("does not abandon ancestor slip on a different branch", func(t *testing.T) {
		// Regression: a push to branch "main" whose git ancestry walks through
		// commits shared with "integration" must NOT abandon the "integration" slip.
		// The "integration" slip is still in-flight on its own branch.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		now := time.Now()
		integrationSlip := &Slip{
			CorrelationID: "corr-integration-1",
			Repository:    "owner/repo",
			Branch:        "integration", // different branch from the push below
			CommitSHA:     "shared-commit-abc",
			CreatedAt:     now.Add(-5 * time.Minute),
			UpdatedAt:     now.Add(-5 * time.Minute),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(integrationSlip)

		// Push is to "main"; its ancestry includes "shared-commit-abc" from integration
		github.SetAncestry("owner", "repo", "main-commit-xyz", []string{"main-commit-xyz", "shared-commit-abc"})

		opts := PushOptions{
			CorrelationID: "corr-main-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "main-commit-xyz",
		}

		ancestry, warnings := client.resolveAndAbandonAncestors(ctx, opts)
		if len(warnings) > 0 {
			t.Fatalf("unexpected warnings: %v", warnings)
		}

		// The integration slip should appear in ancestry but must NOT be abandoned
		if len(ancestry) != 1 {
			t.Fatalf("expected 1 ancestry entry, got %d", len(ancestry))
		}
		if ancestry[0].CorrelationID != "corr-integration-1" {
			t.Errorf("expected ancestry entry 'corr-integration-1', got '%s'", ancestry[0].CorrelationID)
		}

		// No abandon — integration slip must remain in_progress
		if len(store.UpdateSlipStatusCalls) != 0 {
			t.Errorf("expected 0 UpdateSlipStatus calls (cross-branch slip must not be touched), got %d: %v",
				len(store.UpdateSlipStatusCalls), store.UpdateSlipStatusCalls)
		}

		// The integration slip is still in_progress in the store
		loaded, err := store.Load(ctx, "corr-integration-1")
		if err != nil {
			t.Fatalf("failed to load integration slip: %v", err)
		}
		if loaded.Status != SlipStatusInProgress {
			t.Errorf("integration slip status = %q, want %q", loaded.Status, SlipStatusInProgress)
		}
	})

	t.Run("abandons ancestor slip only when on the same branch", func(t *testing.T) {
		// Sanity check: a second push to "integration" SHOULD still abandon the
		// previous "integration" slip (same-branch behaviour is unchanged).
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		now := time.Now()
		prevIntegrationSlip := &Slip{
			CorrelationID: "corr-integration-old",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "old-commit-abc",
			CreatedAt:     now.Add(-10 * time.Minute),
			UpdatedAt:     now.Add(-10 * time.Minute),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(prevIntegrationSlip)

		// New push is also to "integration"
		github.SetAncestry("owner", "repo", "new-commit-xyz", []string{"new-commit-xyz", "old-commit-abc"})

		opts := PushOptions{
			CorrelationID: "corr-integration-new",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "new-commit-xyz",
		}

		ancestry, warnings := client.resolveAndAbandonAncestors(ctx, opts)
		if len(warnings) > 0 {
			t.Fatalf("unexpected warnings: %v", warnings)
		}

		if len(ancestry) != 1 {
			t.Fatalf("expected 1 ancestry entry, got %d", len(ancestry))
		}

		// Same-branch slip MUST be abandoned
		if len(store.UpdateSlipStatusCalls) != 1 {
			t.Fatalf("expected 1 UpdateSlipStatus call (same-branch abandon), got %d", len(store.UpdateSlipStatusCalls))
		}
		if store.UpdateSlipStatusCalls[0].Status != SlipStatusAbandoned {
			t.Errorf("expected SlipStatusAbandoned, got %q", store.UpdateSlipStatusCalls[0].Status)
		}
	})

	t.Run("cross-branch does not abandon, same-branch does, when both exist in ancestry", func(t *testing.T) {
		// Edge case: ancestry contains two slips — one from "integration" (cross-branch)
		// and one from "main" itself (same-branch). Only the "main" slip should be abandoned.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		now := time.Now()
		mainOldSlip := &Slip{
			CorrelationID: "corr-main-old",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "main-old-commit",
			CreatedAt:     now.Add(-3 * time.Minute),
			UpdatedAt:     now.Add(-3 * time.Minute),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		integrationSlip := &Slip{
			CorrelationID: "corr-integration-1",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "shared-commit-abc",
			CreatedAt:     now.Add(-8 * time.Minute),
			UpdatedAt:     now.Add(-8 * time.Minute),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(mainOldSlip)
		store.AddSlip(integrationSlip)

		// main-old-commit is most recent ancestor on main; shared-commit-abc is older/cross-branch
		github.SetAncestry("owner", "repo", "main-new-commit", []string{
			"main-new-commit", "main-old-commit", "shared-commit-abc",
		})

		opts := PushOptions{
			CorrelationID: "corr-main-new",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "main-new-commit",
		}

		_, warnings := client.resolveAndAbandonAncestors(ctx, opts)
		if len(warnings) > 0 {
			t.Fatalf("unexpected warnings: %v", warnings)
		}

		// Only the same-branch (main) slip should have been abandoned.
		if len(store.UpdateSlipStatusCalls) != 1 {
			t.Fatalf(
				"expected exactly 1 UpdateSlipStatus call (main-old-slip), got %d",
				len(store.UpdateSlipStatusCalls),
			)
		}
		if store.UpdateSlipStatusCalls[0].CorrelationID != "corr-main-old" {
			t.Errorf("expected 'corr-main-old' to be abandoned, got '%s'", store.UpdateSlipStatusCalls[0].CorrelationID)
		}

		// Integration slip must remain untouched
		loaded, err := store.Load(ctx, "corr-integration-1")
		if err != nil {
			t.Fatalf("failed to load integration slip: %v", err)
		}
		if loaded.Status != SlipStatusInProgress {
			t.Errorf("integration slip = %q, want %q", loaded.Status, SlipStatusInProgress)
		}
	})

	t.Run("abandons same-branch slip even when cross-branch slip sorts first in ancestry", func(t *testing.T) {
		// Regression for the i==0 ordering bug:
		// FindAllByCommits returns slips in commit-list order. If a cross-branch
		// slip sits at a more recent commit position (index 0) than the same-branch
		// slip (index 1), the same-branch slip must still be abandoned.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		now := time.Now()
		// Cross-branch slip at a NEWER shared commit (will be returned first by FindAllByCommits)
		crossBranchSlip := &Slip{
			CorrelationID: "corr-integration-newer",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "shared-newer-commit", // nearer to HEAD in ancestry list
			CreatedAt:     now.Add(-2 * time.Minute),
			UpdatedAt:     now.Add(-2 * time.Minute),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		// Same-branch slip at an OLDER commit (will be returned second)
		sameBranchSlip := &Slip{
			CorrelationID: "corr-main-older",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "main-older-commit",
			CreatedAt:     now.Add(-10 * time.Minute),
			UpdatedAt:     now.Add(-10 * time.Minute),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(crossBranchSlip)
		store.AddSlip(sameBranchSlip)

		// Ancestry: newer shared commit comes before the older main commit
		github.SetAncestry("owner", "repo", "main-new-commit", []string{
			"main-new-commit", "shared-newer-commit", "main-older-commit",
		})

		opts := PushOptions{
			CorrelationID: "corr-main-new",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "main-new-commit",
		}

		_, warnings := client.resolveAndAbandonAncestors(ctx, opts)
		if len(warnings) > 0 {
			t.Fatalf("unexpected warnings: %v", warnings)
		}

		// Exactly one abandon — the same-branch slip must be abandoned via the atomic path.
		if len(store.UpdateSlipStatusCalls) != 1 {
			t.Fatalf("expected 1 UpdateSlipStatus call (same-branch abandon), got %d", len(store.UpdateSlipStatusCalls))
		}
		if store.UpdateSlipStatusCalls[0].CorrelationID != "corr-main-older" {
			t.Errorf(
				"expected 'corr-main-older' to be abandoned, got '%s'",
				store.UpdateSlipStatusCalls[0].CorrelationID,
			)
		}
		if store.UpdateSlipStatusCalls[0].Status != SlipStatusAbandoned {
			t.Errorf("expected SlipStatusAbandoned, got %q", store.UpdateSlipStatusCalls[0].Status)
		}

		// Cross-branch slip must remain untouched
		loaded, err := store.Load(ctx, "corr-integration-newer")
		if err != nil {
			t.Fatalf("failed to load integration slip: %v", err)
		}
		if loaded.Status != SlipStatusInProgress {
			t.Errorf("integration slip status = %q, want %q", loaded.Status, SlipStatusInProgress)
		}
	})
}

func TestClient_findAncestorSlipsWithProgressiveDepth(t *testing.T) {
	ctx := context.Background()

	t.Run("finds ancestor at initial depth", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// Setup: ancestor slip exists
		now := time.Now()
		ancestorSlip := &Slip{
			CorrelationID: "corr-ancestor-init",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "parent123",
			CreatedAt:     now.Add(-10 * time.Minute),
			UpdatedAt:     now.Add(-10 * time.Minute),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(ancestorSlip)

		// Configure ancestry with just a few commits (within initial depth)
		github.SetAncestry("owner", "repo", "abc123", []string{"abc123", "parent123"})

		opts := PushOptions{
			CorrelationID: "corr-new-init",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
		}

		results, err := client.findAncestorSlipsWithProgressiveDepth(ctx, "owner", "repo", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].Slip.CorrelationID != "corr-ancestor-init" {
			t.Errorf("expected 'corr-ancestor-init', got '%s'", results[0].Slip.CorrelationID)
		}

		// Verify only one GetCommitAncestry call (initial depth was sufficient)
		if len(github.GetCommitAncestryCalls) != 1 {
			t.Errorf("expected 1 GetCommitAncestry call, got %d", len(github.GetCommitAncestryCalls))
		}
		if github.GetCommitAncestryCalls[0].Depth != 25 {
			t.Errorf("expected initial depth 25, got %d", github.GetCommitAncestryCalls[0].Depth)
		}
	})

	t.Run("expands to max depth when no ancestor at initial depth", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// Setup: ancestor slip exists at a commit far in the ancestry
		now := time.Now()
		ancestorSlip := &Slip{
			CorrelationID: "corr-ancestor-deep",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "deep123", // Far in ancestry
			CreatedAt:     now.Add(-60 * time.Minute),
			UpdatedAt:     now.Add(-60 * time.Minute),
			Status:        SlipStatusCompleted,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(ancestorSlip)

		// Create a commit chain that's longer than initial depth
		// Simulate: at depth 25, we only see commits without slips
		// At depth 100, we find the slip at "deep123"
		longCommitChain := make([]string, 50)
		longCommitChain[0] = "abc123" // Current commit
		for i := 1; i < 49; i++ {
			longCommitChain[i] = "intermediate" + string(rune('a'+i))
		}
		longCommitChain[49] = "deep123" // The ancestor with a slip

		github.SetAncestry("owner", "repo", "abc123", longCommitChain)

		opts := PushOptions{
			CorrelationID: "corr-new-deep",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
		}

		results, err := client.findAncestorSlipsWithProgressiveDepth(ctx, "owner", "repo", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(results) != 1 {
			t.Fatalf("expected 1 result (found at max depth), got %d", len(results))
		}
		if results[0].Slip.CorrelationID != "corr-ancestor-deep" {
			t.Errorf("expected 'corr-ancestor-deep', got '%s'", results[0].Slip.CorrelationID)
		}

		// Verify two GetCommitAncestry calls (initial + expanded)
		if len(github.GetCommitAncestryCalls) != 2 {
			t.Errorf("expected 2 GetCommitAncestry calls, got %d", len(github.GetCommitAncestryCalls))
		}
		if github.GetCommitAncestryCalls[0].Depth != 25 {
			t.Errorf("expected first call depth 25, got %d", github.GetCommitAncestryCalls[0].Depth)
		}
		if github.GetCommitAncestryCalls[1].Depth != 100 {
			t.Errorf("expected second call depth 100, got %d", github.GetCommitAncestryCalls[1].Depth)
		}
	})

	t.Run("no commits returns nil", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// No ancestry configured - returns empty
		opts := PushOptions{
			CorrelationID: "corr-no-commits",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
		}

		results, err := client.findAncestorSlipsWithProgressiveDepth(ctx, "owner", "repo", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if results != nil {
			t.Errorf("expected nil results for no commits, got %v", results)
		}

		// Should only call once - no point retrying with no commits
		if len(github.GetCommitAncestryCalls) != 1 {
			t.Errorf("expected 1 GetCommitAncestry call, got %d", len(github.GetCommitAncestryCalls))
		}
	})

	t.Run("skips current commit in ancestry", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 100,
		})

		// Slip exists at the CURRENT commit (should be skipped)
		now := time.Now()
		currentSlip := &Slip{
			CorrelationID: "corr-current",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123", // Same as current commit
			CreatedAt:     now,
			UpdatedAt:     now,
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(currentSlip)

		// Parent slip
		parentSlip := &Slip{
			CorrelationID: "corr-parent",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "parent123",
			CreatedAt:     now.Add(-10 * time.Minute),
			UpdatedAt:     now.Add(-10 * time.Minute),
			Status:        SlipStatusCompleted,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(parentSlip)

		// Ancestry includes current commit first
		github.SetAncestry("owner", "repo", "abc123", []string{"abc123", "parent123"})

		opts := PushOptions{
			CorrelationID: "corr-new-skip",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
		}

		results, err := client.findAncestorSlipsWithProgressiveDepth(ctx, "owner", "repo", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should find parent, not current
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].Slip.CorrelationID != "corr-parent" {
			t.Errorf("expected 'corr-parent', got '%s'", results[0].Slip.CorrelationID)
		}
	})

	t.Run("does not expand if max depth equals initial", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth:    25,
			AncestryMaxDepth: 25, // Same as initial - no expansion
		})

		// Configure ancestry without matching slips
		github.SetAncestry("owner", "repo", "abc123", []string{"abc123", "parent123", "grandparent456"})

		opts := PushOptions{
			CorrelationID: "corr-no-expand",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
		}

		results, err := client.findAncestorSlipsWithProgressiveDepth(ctx, "owner", "repo", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// No slips found
		if results != nil {
			t.Errorf("expected nil results, got %v", results)
		}

		// Only one call - no expansion when max == initial
		if len(github.GetCommitAncestryCalls) != 1 {
			t.Errorf("expected 1 GetCommitAncestry call (no expansion), got %d", len(github.GetCommitAncestryCalls))
		}
	})
}

// TestClient_CreateSlipForPush_DuplicateBackstopFailurePaths covers the two ways the
// fresh-create path can still fail after a duplicate: the backstop itself failing fatally,
// and the post-backstop retry failing. Both must surface as errors so Kafka redelivers,
// never as a success reporting a slip that was not written.
func TestClient_CreateSlipForPush_DuplicateBackstopFailurePaths(t *testing.T) {
	ctx := context.Background()

	opts := PushOptions{
		CorrelationID: "corr-caller",
		Repository:    "owner/repo",
		Branch:        "integration",
		CommitSHA:     "sha-dup",
		Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
	}

	t.Run("backstop repave fails fatally", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		// The conflicting row appears at Create time (the lost-race shape), is ended, and
		// the backstop's repave of it fails with a non-sentinel error.
		store.SeedOnCreate["corr-caller"] = &Slip{
			CorrelationID: "corr-conflict",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-dup",
			Status:        SlipStatusCompleted,
			Steps:         map[string]Step{},
			StateHistory:  []StateHistoryEntry{},
		}
		store.CreateErrorOnce["corr-caller"] = ErrDuplicateSlip
		store.RepaveError = errors.New("postgres unavailable")

		result, err := client.CreateSlipForPush(ctx, opts)
		if err == nil {
			t.Fatal("expected a fatal backstop failure to fail the push")
		}
		if !strings.Contains(err.Error(), "postgres unavailable") {
			t.Errorf("expected the underlying store error to be wrapped, got %v", err)
		}
		if result != nil {
			t.Errorf("expected no result alongside the error, got %+v", result)
		}
	})

	t.Run("retry after the backstop falls through also fails", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		// First Create reports the duplicate; the backstop finds no conflicting row (its
		// own LoadByCommit misses), so it falls through to the single retry — which fails.
		store.CreateErrorOnce["corr-caller"] = ErrDuplicateSlip
		store.CreateErrorFor["corr-caller"] = errors.New("still conflicting")

		result, err := client.CreateSlipForPush(ctx, opts)
		if err == nil {
			t.Fatal("expected the failed retry to fail the push")
		}
		if !strings.Contains(err.Error(), "after duplicate backstop") {
			t.Errorf("expected the error to identify the post-backstop retry, got %v", err)
		}
		if result != nil {
			t.Errorf("expected no result alongside the error, got %+v", result)
		}
		if len(store.AncestryLinkCalls) != 0 {
			t.Error("no link may be written when the slip itself was never created")
		}
	})
}

// TestDispatchIntent_String pins how the two new log fields render. The zero value is the
// case that matters: without an explicit name it would log as an empty string, on the one
// line an operator reads to explain why a run's history was preserved or destroyed.
func TestDispatchIntent_String(t *testing.T) {
	tests := []struct {
		intent DispatchIntent
		want   string
	}{
		{DispatchIntentUnspecified, "unspecified"},
		{DispatchIntentSomething, "something"},
		{DispatchIntentNothing, "nothing"},
		{DispatchIntent("garbage"), "garbage"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if got := tc.intent.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}

	// The type must satisfy fmt.Stringer for zap to use it: zap matches fmt.Stringer but
	// would otherwise reflect a named string type, which is the drift this guards.
	var _ fmt.Stringer = DispatchIntentSomething
}

// TestPushOptions_dispatchesNothing is the table-driven contract for the single predicate
// both empty-run guards share. Keeping it in one place is the point: the guard used to be
// two copies of `len(opts.Components) == 0` in CreateSlipForPush and
// handleDuplicateSlipBackstop, which are documented as converging on the same outcome for
// the same inputs and could silently diverge.
func TestPushOptions_dispatchesNothing(t *testing.T) {
	withComponents := []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}}

	tests := []struct {
		name       string
		dispatch   DispatchIntent
		components []ComponentDefinition
		want       bool
		why        string
	}{
		{
			name:     "explicit Nothing wins over present components",
			dispatch: DispatchIntentNothing, components: withComponents, want: true,
			why: "intent is authoritative in both directions, not just when components are empty",
		},
		{
			name:     "explicit Something wins over absent components",
			dispatch: DispatchIntentSomething, components: nil, want: false,
			why: "the tests-only repo case: no build components, but unit tests will dispatch",
		},
		{
			name:     "unspecified with no components falls back to true",
			dispatch: DispatchIntentUnspecified, components: nil, want: true,
			why: "legacy behavior must be preserved for callers that have not adopted the field",
		},
		{
			name:     "unspecified with components falls back to false",
			dispatch: DispatchIntentUnspecified, components: withComponents, want: false,
			why: "legacy behavior must be preserved for callers that have not adopted the field",
		},
		{
			name:     "out-of-range value falls back to the inference rather than assuming work runs",
			dispatch: DispatchIntent("garbage"), components: nil, want: true,
			why: "a garbage value must never license destroying a prior run's history",
		},
		{
			name:     "out-of-range value with components infers work",
			dispatch: DispatchIntent("garbage"), components: withComponents, want: false,
			why: "the fallback is the inference, not a hardcoded answer",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := PushOptions{
				CorrelationID: "c1",
				Repository:    "owner/repo",
				CommitSHA:     "sha1",
				Components:    tc.components,
				Dispatch:      tc.dispatch,
			}
			if got := opts.dispatchesNothing(); got != tc.want {
				t.Errorf("dispatchesNothing() = %v, want %v — %s", got, tc.want, tc.why)
			}
		})
	}
}

// TestClient_CreateSlipForPush_BackstopHonorsDispatchIntent pins that the backstop's
// mirrored guard follows the same intent as the main path. Dormant until Phase B (see
// CreateSlipForPush's doc), but if the two guards disagreed, the same push would repave or
// dedup depending only on which path it happened to take.
func TestClient_CreateSlipForPush_BackstopHonorsDispatchIntent(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	github := NewMockGitHubAPI()
	client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

	// The conflicting row appears at Create time, so only the backstop's own lookup sees
	// it — the same shape the other backstop tests use.
	store.SeedOnCreate["corr-caller-tests-only"] = &Slip{
		CorrelationID: "corr-conflict-tests-only",
		Repository:    "owner/repo",
		Branch:        "integration",
		CommitSHA:     "sha-backstop-tests-only",
		Status:        SlipStatusFailed,
		Steps:         map[string]Step{"unit_tests": {Status: StepStatusFailed}},
		StateHistory:  []StateHistoryEntry{},
	}
	store.CreateErrorOnce["corr-caller-tests-only"] = ErrDuplicateSlip

	result, err := client.CreateSlipForPush(ctx, PushOptions{
		CorrelationID: "corr-caller-tests-only",
		Repository:    "owner/repo",
		Branch:        "integration",
		CommitSHA:     "sha-backstop-tests-only",
		Components:    nil,                     // tests-only: no build components...
		Dispatch:      DispatchIntentSomething, // ...but unit tests will dispatch
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Under the old component-count guard the backstop would have deduped onto the failed
	// conflicting row, suppressing the retrigger. It must repave instead.
	if len(store.RepaveCalls) != 1 || store.RepaveCalls[0] != "corr-conflict-tests-only" {
		t.Errorf("expected the backstop to repave the failed conflicting row, got %v", store.RepaveCalls)
	}
	if result.Slip == nil || result.Slip.CorrelationID != "corr-caller-tests-only" {
		t.Errorf("expected the caller's own fresh slip, got %+v", result.Slip)
	}
}

// TestClient_CreateSlipForPush_RepaveDuplicateBackstop covers the repave path's own
// ErrDuplicateSlip branch. It is dormant until Phase B's unique index exists, but it is
// reachable after that through the concurrent same-commit push the feature exists for: two
// pushes repave the same row, the loser's guarded delete finds the row already gone (the
// winner committed), so the loser proceeds to insert its own successor and conflicts with
// the winner's on uq_routing_slips_repo_sha. Both failure exits are exercised here so the
// branch is not merely executed.
func TestClient_CreateSlipForPush_RepaveDuplicateBackstop(t *testing.T) {
	ctx := context.Background()

	opts := PushOptions{
		CorrelationID: "corr-loser",
		Repository:    "owner/repo",
		Branch:        "integration",
		CommitSHA:     "sha-raced",
		Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
	}

	// An ended row for the pushed commit, so the push takes the repave path rather than
	// the plain-create path.
	seedEndedRow := func(store *MockStore) {
		store.AddSlip(&Slip{
			CorrelationID: "corr-superseded",
			Repository:    "owner/repo",
			Branch:        "integration",
			CommitSHA:     "sha-raced",
			Status:        SlipStatusFailed,
			Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
			StateHistory:  []StateHistoryEntry{},
		})
	}

	t.Run("backstop failure propagates", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})
		seedEndedRow(store)

		// Every Repave reports the duplicate: the push's own, and the backstop's attempt on
		// the conflicting row it finds. The latter is fatal inside the backstop, and that
		// error must reach the caller rather than be swallowed.
		store.RepaveError = ErrDuplicateSlip

		result, err := client.CreateSlipForPush(ctx, opts)
		if err == nil {
			t.Fatal("expected the backstop's fatal repave failure to fail the push")
		}
		if !strings.Contains(err.Error(), "conflicting slip") {
			t.Errorf("expected the backstop's error to propagate, got %v", err)
		}
		if result != nil {
			t.Errorf("expected no result alongside the error, got %+v", result)
		}
	})

	t.Run("retry after the backstop falls through also fails", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})
		seedEndedRow(store)

		store.RepaveError = ErrDuplicateSlip
		// Force the backstop's own lookup (LoadByCommit call 2; call 1 is
		// CreateSlipForPush's initial one) to find nothing, so the backstop falls through
		// with handled=false instead of deduping — leaving the single retry, which fails.
		store.LoadByCommitNilOnCall = 2

		result, err := client.CreateSlipForPush(ctx, opts)
		if err == nil {
			t.Fatal("expected the failed post-backstop retry to fail the push")
		}
		if !strings.Contains(err.Error(), "after duplicate backstop") {
			t.Errorf("expected the error to identify the post-backstop retry, got %v", err)
		}
		if result != nil {
			t.Errorf("expected no result alongside the error, got %+v", result)
		}
		// Convergence: nothing was written, so the superseded row is still there for the
		// redelivery to repave.
		if _, ok := store.Slips["corr-superseded"]; !ok {
			t.Error("the superseded row must survive so a redelivery can converge")
		}
		if _, ok := store.Slips["corr-loser"]; ok {
			t.Error("no successor may be left behind")
		}
	})
}

// TestClient_CreateSlipForPush_LinkWriteRouting pins WHERE the successor's direct-parent
// link is written, which the transactional Repave changes. On the fresh-create path the link
// is a separate, best-effort store call; on the repave path it is handed to Repave so it
// lands in the same transaction as the successor's row and can never be left missing.
//
// This is only assertable because MockStore.InsertAncestryLink now records its calls; while
// it was a bare `return nil`, no test could tell whether a link had been written at all.
func TestClient_CreateSlipForPush_LinkWriteRouting(t *testing.T) {
	ctx := context.Background()

	// A commit whose parent GitHub will report, so resolveAndAbandonAncestors produces an
	// ancestry entry for the push to hand onward.
	newClientWithAncestor := func(store *MockStore) *Client {
		github := NewMockGitHubAPI()
		github.Ancestry["owner/repo:sha-child"] = []string{"sha-child", "sha-ancestor"}
		store.AddSlip(&Slip{
			CorrelationID: "corr-ancestor",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "sha-ancestor",
			Status:        SlipStatusCompleted,
			Steps:         map[string]Step{},
			StateHistory:  []StateHistoryEntry{},
		})
		return NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})
	}

	opts := PushOptions{
		CorrelationID: "corr-child-new",
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-child",
		Components:    []ComponentDefinition{{Name: "api", DockerfilePath: "src/MC.Api"}},
	}

	t.Run("fresh create writes the link as a separate store call", func(t *testing.T) {
		store := NewMockStore()
		client := newClientWithAncestor(store)

		result, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(store.RepaveCalls) != 0 {
			t.Errorf("no row existed for this commit, so nothing should be repaved: %v", store.RepaveCalls)
		}
		if len(store.AncestryLinkCalls) != 1 {
			t.Fatalf("expected one non-transactional link write, got %d", len(store.AncestryLinkCalls))
		}
		link := store.AncestryLinkCalls[0]
		if link.Slip.CorrelationID != "corr-child-new" {
			t.Errorf("expected the link to be keyed on the new slip, got %s", link.Slip.CorrelationID)
		}
		if link.Parent.CorrelationID != "corr-ancestor" {
			t.Errorf("expected the resolved ancestor as parent, got %s", link.Parent.CorrelationID)
		}
		if result.Slip.CorrelationID != "corr-child-new" {
			t.Errorf("expected the fresh slip, got %s", result.Slip.CorrelationID)
		}
	})

	t.Run("fresh create records a link failure as a warning, not a push failure", func(t *testing.T) {
		// The slip exists and CI can run; only the lineage hop is missing. Failing the
		// push here would be a regression against pre-DEVOPS-231 behavior.
		store := NewMockStore()
		client := newClientWithAncestor(store)
		store.AncestryLinkError = errors.New("ancestry table unavailable")

		result, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("a link failure on the fresh path must not fail the push: %v", err)
		}
		if result.Slip.CorrelationID != "corr-child-new" {
			t.Errorf("expected the fresh slip, got %s", result.Slip.CorrelationID)
		}
		foundWarning := false
		for _, w := range result.Warnings {
			if strings.Contains(w.Error(), "ancestry table unavailable") {
				foundWarning = true
			}
		}
		if !foundWarning {
			t.Errorf("expected the link failure to surface as a warning, got %v", result.Warnings)
		}
	})

	t.Run("repave hands the link to the store instead of writing it separately", func(t *testing.T) {
		store := NewMockStore()
		client := newClientWithAncestor(store)
		// An ended row for the pushed commit, so this push repaves rather than creates.
		store.AddSlip(&Slip{
			CorrelationID: "corr-child-old",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "sha-child",
			Status:        SlipStatusFailed,
			Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
			StateHistory:  []StateHistoryEntry{},
		})

		result, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(store.RepaveCalls) != 1 || store.RepaveCalls[0] != "corr-child-old" {
			t.Fatalf("expected the ended row to be repaved, got %v", store.RepaveCalls)
		}
		// The link must travel INTO Repave — that is what puts it in the same transaction
		// as the successor's row.
		if len(store.RepaveParents) != 1 || store.RepaveParents[0] == nil {
			t.Fatalf("expected the resolved parent to be handed to Repave, got %v", store.RepaveParents)
		}
		if store.RepaveParents[0].CorrelationID != "corr-ancestor" {
			t.Errorf("expected the resolved ancestor as the repave parent, got %s",
				store.RepaveParents[0].CorrelationID)
		}
		// ...and must NOT also be written as a separate, non-transactional call.
		if len(store.AncestryLinkCalls) != 0 {
			t.Errorf("a repave must not write the link outside its transaction, got %v", store.AncestryLinkCalls)
		}
		if result.Slip.CorrelationID != "corr-child-new" {
			t.Errorf("expected the successor, got %s", result.Slip.CorrelationID)
		}
	})

	t.Run("repave passes a nil parent when this push resolved no ancestry", func(t *testing.T) {
		// nil is meaningful, not merely absent: it tells the store to carry the superseded
		// run's OWN parent link forward rather than destroy the lineage hop with its row.
		// A GitHub outage is exactly this case, and it must reach the store as nil rather
		// than as a fabricated entry.
		store := NewMockStore()
		github := NewMockGitHubAPI()
		github.GetCommitAncestryError = errors.New("github unavailable")
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: testPipelineConfig()})

		store.AddSlip(&Slip{
			CorrelationID: "corr-child-old",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "sha-child",
			Status:        SlipStatusFailed,
			Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
			StateHistory:  []StateHistoryEntry{},
		})

		if _, err := client.CreateSlipForPush(ctx, opts); err != nil {
			t.Fatalf("an ancestry-resolution failure must not fail the push: %v", err)
		}
		if len(store.RepaveParents) != 1 {
			t.Fatalf("expected one repave, got %v", store.RepaveParents)
		}
		if store.RepaveParents[0] != nil {
			t.Errorf("expected a nil parent so the store carries the old link forward, got %+v",
				store.RepaveParents[0])
		}
	})

	t.Run("unsupported repave falls back to create plus a separate link write", func(t *testing.T) {
		// A ClickHouse-backed client cannot repave, so it abandons the superseded slip and
		// then takes the fresh-create path — including that path's separate link write,
		// since there is no transaction to put it in.
		store := NewMockStore()
		client := newClientWithAncestor(store)
		store.AddSlip(&Slip{
			CorrelationID: "corr-child-old",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "sha-child",
			Status:        SlipStatusFailed,
			Steps:         map[string]Step{"builds": {Status: StepStatusFailed}},
			StateHistory:  []StateHistoryEntry{},
		})
		store.RepaveError = ErrRepaveUnsupported

		result, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Slip.CorrelationID != "corr-child-new" {
			t.Errorf("expected the fresh slip, got %s", result.Slip.CorrelationID)
		}
		if len(store.CreateCalls) != 1 {
			t.Errorf("expected the fallback to Create, got %d calls", len(store.CreateCalls))
		}
		if len(store.AncestryLinkCalls) != 1 {
			t.Errorf("expected the fallback to write the link separately, got %d", len(store.AncestryLinkCalls))
		}
	})
}

func TestExtractPRNumber(t *testing.T) {
	tests := []struct {
		name          string
		commitMessage string
		expected      int
	}{
		{
			name:          "GitHub auto-generated squash merge",
			commitMessage: "Add new feature (#42)\n\nDetailed description here",
			expected:      42,
		},
		{
			name:          "explicit pull request reference",
			commitMessage: "Merge pull request #123 from feature-branch",
			expected:      123,
		},
		{
			name:          "no PR number",
			commitMessage: "Regular commit without PR reference",
			expected:      0,
		},
		{
			name:          "PR number in middle of message",
			commitMessage: "fix: resolve bug introduced in #789",
			expected:      789,
		},
		{
			name:          "multiple PR references returns first",
			commitMessage: "fix: resolve #45 and #67",
			expected:      45,
		},
		{
			name:          "empty commit message",
			commitMessage: "",
			expected:      0,
		},
		{
			name:          "number without hash not matched",
			commitMessage: "Fixed issue 42",
			expected:      0,
		},
		{
			name:          "hash at end of line",
			commitMessage: "Merged PR #999",
			expected:      999,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractPRNumber(tt.commitMessage)
			if result != tt.expected {
				t.Errorf("extractPRNumber(%q) = %d, want %d", tt.commitMessage, result, tt.expected)
			}
		})
	}
}

func TestExtractAllPRNumbers(t *testing.T) {
	tests := []struct {
		name          string
		commitMessage string
		expected      []int
	}{
		{
			name:          "single PR",
			commitMessage: "Add feature (#42)",
			expected:      []int{42},
		},
		{
			name:          "multiple PRs",
			commitMessage: "Merge dev (#45) which includes fix (#67)",
			expected:      []int{45, 67},
		},
		{
			name:          "duplicate PRs deduplicated",
			commitMessage: "Fix #45, closes #45",
			expected:      []int{45},
		},
		{
			name:          "no PRs",
			commitMessage: "Regular commit",
			expected:      nil,
		},
		{
			name:          "nested merge message",
			commitMessage: "Merge pull request #100\n\nContains:\n- Feature (#90)\n- Fix (#91)",
			expected:      []int{100, 90, 91},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractAllPRNumbers(tt.commitMessage)
			if len(result) != len(tt.expected) {
				t.Errorf(
					"extractAllPRNumbers(%q) returned %d PRs, want %d",
					tt.commitMessage,
					len(result),
					len(tt.expected),
				)
				return
			}
			for i, pr := range tt.expected {
				if result[i] != pr {
					t.Errorf("extractAllPRNumbers(%q)[%d] = %d, want %d", tt.commitMessage, i, result[i], pr)
				}
			}
		})
	}
}

func TestIsCherryPick(t *testing.T) {
	tests := []struct {
		name          string
		commitMessage string
		expected      bool
	}{
		{
			name:          "cherry-pick with hyphen",
			commitMessage: "cherry-pick: fix from main",
			expected:      true,
		},
		{
			name:          "cherry pick with space",
			commitMessage: "cherry pick abc123",
			expected:      true,
		},
		{
			name:          "picked from",
			commitMessage: "Picked from release branch",
			expected:      true,
		},
		{
			name:          "backport",
			commitMessage: "Backport security fix",
			expected:      true,
		},
		{
			name:          "regular commit",
			commitMessage: "Add new feature",
			expected:      false,
		},
		{
			name:          "case insensitive",
			commitMessage: "CHERRY-PICK from v1.0",
			expected:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isCherryPick(tt.commitMessage)
			if result != tt.expected {
				t.Errorf("isCherryPick(%q) = %v, want %v", tt.commitMessage, result, tt.expected)
			}
		})
	}
}

func TestClient_FindAncestorViaSquashMerge(t *testing.T) {
	ctx := context.Background()

	t.Run("finds slip via PR head commit", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		// Set up the feature branch slip that was created before the squash merge
		featureSlip := &Slip{
			CorrelationID: "corr-feature",
			Repository:    "owner/repo",
			CommitSHA:     "feature-commit-sha",
			Status:        SlipStatusInProgress,
		}
		store.Slips["corr-feature"] = featureSlip
		store.CommitIndex["owner/repo:feature-commit-sha"] = "corr-feature"

		// Set up PR head commit lookup and its ancestry
		github.SetPRHeadCommit("owner", "repo", 42, "feature-commit-sha")
		github.SetAncestry("owner", "repo", "feature-commit-sha", []string{"feature-commit-sha"})

		opts := PushOptions{
			CorrelationID: "corr-merge",
			Repository:    "owner/repo",
			CommitSHA:     "merge-commit-sha",
			CommitMessage: "Add feature (#42)\n\nSquash merged",
		}

		result, found := client.findAncestorViaSquashMerge(ctx, "owner", "repo", opts)

		if !found {
			t.Fatal("expected to find ancestor via squash merge")
		}
		if result.Slip.CorrelationID != "corr-feature" {
			t.Errorf("expected correlation ID 'corr-feature', got '%s'", result.Slip.CorrelationID)
		}
		if result.MatchedCommit != "feature-commit-sha" {
			t.Errorf("expected matched commit 'feature-commit-sha', got '%s'", result.MatchedCommit)
		}

		// Verify PR head commit was looked up
		if len(github.GetPRHeadCommitCalls) != 1 {
			t.Fatalf("expected 1 GetPRHeadCommit call, got %d", len(github.GetPRHeadCommitCalls))
		}
		call := github.GetPRHeadCommitCalls[0]
		if call.Owner != "owner" || call.Repo != "repo" || call.PRNumber != 42 {
			t.Errorf("unexpected GetPRHeadCommit call: %+v", call)
		}
	})

	t.Run("finds slip when PR head is non-slip commit", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		// Set up a slip from an earlier commit in the PR
		featureSlip := &Slip{
			CorrelationID: "corr-feature",
			Repository:    "owner/repo",
			CommitSHA:     "earlier-commit-sha",
			Status:        SlipStatusInProgress,
		}
		store.Slips["corr-feature"] = featureSlip
		store.CommitIndex["owner/repo:earlier-commit-sha"] = "corr-feature"

		// PR head is a non-slip commit (e.g., docs change) that comes after the slip commit
		github.SetPRHeadCommit("owner", "repo", 99, "docs-commit-sha")
		// Ancestry from docs commit includes the earlier slip-creating commit
		github.SetAncestry("owner", "repo", "docs-commit-sha", []string{"docs-commit-sha", "earlier-commit-sha"})

		opts := PushOptions{
			CorrelationID: "corr-merge",
			Repository:    "owner/repo",
			CommitSHA:     "merge-commit-sha",
			CommitMessage: "Add feature (#99)",
		}

		result, found := client.findAncestorViaSquashMerge(ctx, "owner", "repo", opts)

		if !found {
			t.Fatal("expected to find ancestor via PR ancestry walk")
		}
		if result.Slip.CorrelationID != "corr-feature" {
			t.Errorf("expected correlation ID 'corr-feature', got '%s'", result.Slip.CorrelationID)
		}
		// Should match the slip's commit, not the PR head
		if result.MatchedCommit != "earlier-commit-sha" {
			t.Errorf("expected matched commit 'earlier-commit-sha', got '%s'", result.MatchedCommit)
		}
	})

	t.Run("returns false when no PR number in commit message", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		opts := PushOptions{
			CorrelationID: "corr-no-pr",
			Repository:    "owner/repo",
			CommitSHA:     "commit-sha",
			CommitMessage: "Regular commit without PR reference",
		}

		_, found := client.findAncestorViaSquashMerge(ctx, "owner", "repo", opts)

		if found {
			t.Error("expected not to find ancestor when no PR number in message")
		}
		if len(github.GetPRHeadCommitCalls) != 0 {
			t.Error("should not call GetPRHeadCommit when no PR number")
		}
	})

	t.Run("returns false when PR head commit lookup fails", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		// Set error for PR lookup
		github.GetPRHeadCommitError = errors.New("PR not found")

		opts := PushOptions{
			CorrelationID: "corr-pr-error",
			Repository:    "owner/repo",
			CommitSHA:     "commit-sha",
			CommitMessage: "Fix (#99)",
		}

		_, found := client.findAncestorViaSquashMerge(ctx, "owner", "repo", opts)

		if found {
			t.Error("expected not to find ancestor when PR lookup fails")
		}
	})

	t.Run("returns false when no slip found for PR head commit", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		// PR lookup succeeds but no slip exists in that commit's ancestry
		github.SetPRHeadCommit("owner", "repo", 50, "orphan-commit-sha")
		github.SetAncestry("owner", "repo", "orphan-commit-sha", []string{"orphan-commit-sha"})

		opts := PushOptions{
			CorrelationID: "corr-no-slip",
			Repository:    "owner/repo",
			CommitSHA:     "commit-sha",
			CommitMessage: "Merge (#50)",
		}

		_, found := client.findAncestorViaSquashMerge(ctx, "owner", "repo", opts)

		if found {
			t.Error("expected not to find ancestor when no slip exists in PR ancestry")
		}
	})

	t.Run("tries multiple PR numbers for nested merges", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		// Set up slip from feature branch
		featureSlip := &Slip{
			CorrelationID: "corr-feature",
			Repository:    "owner/repo",
			CommitSHA:     "feature-sha",
			Status:        SlipStatusInProgress,
		}
		store.Slips["corr-feature"] = featureSlip
		store.CommitIndex["owner/repo:feature-sha"] = "corr-feature"

		// First PR (#100) not found (use ErrorFor to be specific)
		github.GetPRHeadCommitErrorFor = map[string]error{
			"owner/repo:100": errors.New("PR not found"),
		}

		// Second PR (#90) has the slip
		github.SetPRHeadCommit("owner", "repo", 90, "feature-sha")
		github.SetAncestry("owner", "repo", "feature-sha", []string{"feature-sha"})

		opts := PushOptions{
			CorrelationID: "corr-merge",
			Repository:    "owner/repo",
			CommitSHA:     "merge-sha",
			CommitMessage: "Merge dev (#100) with feature (#90)",
		}

		result, found := client.findAncestorViaSquashMerge(ctx, "owner", "repo", opts)

		if !found {
			t.Fatal("expected to find ancestor via second PR")
		}
		if result.Slip.CorrelationID != "corr-feature" {
			t.Errorf("expected correlation ID 'corr-feature', got '%s'", result.Slip.CorrelationID)
		}

		// Should have tried both PRs
		if len(github.GetPRHeadCommitCalls) < 2 {
			t.Errorf("expected at least 2 GetPRHeadCommit calls, got %d", len(github.GetPRHeadCommitCalls))
		}
	})

	t.Run("does not select the pushed commit's own ended slip as its ancestor (FF-merge self-reference, D3.4)",
		func(t *testing.T) {
			// A fast-forward (or otherwise no-op) merge keeps the PR head SHA identical to
			// the commit being pushed. findSlipsInPRBranchHistory deliberately includes
			// the head commit in its search, so without the guard this would return the
			// pushed commit's own ended slip as its "ancestor" - a self-reference.
			store := NewMockStore()
			github := NewMockGitHubAPI()
			client := NewClientWithDependencies(store, github, Config{})

			selfSlip := &Slip{
				CorrelationID: "corr-self",
				Repository:    "owner/repo",
				CommitSHA:     "sha-x",
				Status:        SlipStatusCompleted,
			}
			store.Slips["corr-self"] = selfSlip
			store.CommitIndex["owner/repo:sha-x"] = "corr-self"

			github.SetPRHeadCommit("owner", "repo", 7, "sha-x") // FF merge: PR head == pushed commit
			github.SetAncestry("owner", "repo", "sha-x", []string{"sha-x"})

			opts := PushOptions{
				CorrelationID: "corr-merge-x",
				Repository:    "owner/repo",
				CommitSHA:     "sha-x", // same commit as the "ancestor" candidate
				CommitMessage: "Merge pull request #7 from owner/feature",
			}

			_, found := client.findAncestorViaSquashMerge(ctx, "owner", "repo", opts)

			if found {
				t.Error("must not select the pushed commit's own slip as its ancestor (self-reference)")
			}
		})
}

func TestClient_PromoteSlip(t *testing.T) {
	ctx := context.Background()

	t.Run("promotes active slip", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-to-promote",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
		}
		store.Slips["corr-to-promote"] = slip

		err := client.PromoteSlip(ctx, "corr-to-promote", "corr-target")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify status was updated
		updated := store.Slips["corr-to-promote"]
		if updated.Status != SlipStatusPromoted {
			t.Errorf("expected status 'promoted', got '%s'", updated.Status)
		}
		if updated.PromotedTo != "corr-target" {
			t.Errorf("expected PromotedTo 'corr-target', got '%s'", updated.PromotedTo)
		}
	})

	t.Run("skips already terminal slip", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-completed",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
			Status:        SlipStatusCompleted,
		}
		store.Slips["corr-completed"] = slip

		err := client.PromoteSlip(ctx, "corr-completed", "corr-target")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should remain completed, not promoted
		updated := store.Slips["corr-completed"]
		if updated.Status != SlipStatusCompleted {
			t.Errorf("expected status to remain 'completed', got '%s'", updated.Status)
		}
	})

	t.Run("returns error when slip not found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		err := client.PromoteSlip(ctx, "non-existent", "corr-target")
		if err == nil {
			t.Fatal("expected error for non-existent slip")
		}
	})

	t.Run("returns error when update fails", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-update-fail",
			Repository:    "owner/repo",
			CommitSHA:     "abc123",
			Status:        SlipStatusInProgress,
		}
		store.Slips["corr-update-fail"] = slip
		store.UpdateError = errors.New("database error")

		err := client.PromoteSlip(ctx, "corr-update-fail", "corr-target")
		if err == nil {
			t.Fatal("expected error when update fails")
		}
	})
}

// TestClient_PromoteSlip_Immutable verifies that a promoted slip is pipeline-terminal and
// immutable: late step events (FailStep, UpdateStepWithStatus) must not overwrite slip.status.
//
// STATE_MACHINE_V3.md §Pipeline termination states:
//
//	"abandoned / promoted: pipeline-terminal, bypass checkPipelineCompletion"
func TestClient_PromoteSlip_Immutable(t *testing.T) {
	ctx := context.Background()

	// Sub-test 1: PromoteSlip itself works correctly (current behavior, spec-correct).
	t.Run("PromoteSlip sets slip status to promoted", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-promote-basic",
			Repository:    "owner/repo",
			Branch:        "feature/thing",
			CommitSHA:     "sha-promote-basic",
			Status:        SlipStatusInProgress,
			Steps:         map[string]Step{},
		}
		store.AddSlip(slip)

		if err := client.PromoteSlip(ctx, "corr-promote-basic", "corr-main-merge"); err != nil {
			t.Fatalf("PromoteSlip returned unexpected error: %v", err)
		}

		loaded, err := store.Load(ctx, "corr-promote-basic")
		if err != nil {
			t.Fatalf("failed to load slip: %v", err)
		}
		if loaded.Status != SlipStatusPromoted {
			t.Errorf("expected slip.status %q after PromoteSlip, got %q", SlipStatusPromoted, loaded.Status)
		}
		if loaded.PromotedTo != "corr-main-merge" {
			t.Errorf("expected PromotedTo %q, got %q", "corr-main-merge", loaded.PromotedTo)
		}
	})

	// Sub-test 2: CompleteStep on a promoted slip — current behavior PASSES spec.
	// checkPipelineCompletion with no failures and no prod_steady_state step leaves slip.status
	// unchanged when it is neither "failed" nor "completed", so promoted is preserved.
	t.Run("post-promotion CompleteStep does not change slip status (current behavior)", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-promoted-complete",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "sha-promoted-complete",
			Status:        SlipStatusPromoted,
			Steps: map[string]Step{
				"unit_tests": {Status: StepStatusRunning},
			},
		}
		store.AddSlip(slip)

		if err := client.CompleteStep(ctx, "corr-promoted-complete", "unit_tests", ""); err != nil {
			t.Fatalf("CompleteStep returned unexpected error: %v", err)
		}

		loaded, err := store.Load(ctx, "corr-promoted-complete")
		if err != nil {
			t.Fatalf("failed to load slip: %v", err)
		}
		if loaded.Status != SlipStatusPromoted {
			t.Errorf("expected slip.status %q to remain unchanged after CompleteStep on promoted slip, got %q",
				SlipStatusPromoted, loaded.Status)
		}
	})

	// Sub-test 3: FailStep on a promoted slip must leave slip.status as promoted.
	// checkPipelineCompletion short-circuits on promoted per STATE_MACHINE_V3.md.
	t.Run("post-promotion FailStep does not change slip status", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-promoted-fail",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "sha-promoted-fail",
			Status:        SlipStatusPromoted,
			Steps: map[string]Step{
				"unit_tests": {Status: StepStatusRunning},
				"dev_deploy": {Status: StepStatusPending},
			},
		}
		store.AddSlip(slip)

		if err := client.FailStep(ctx, "corr-promoted-fail", "unit_tests", "", "late failure"); err != nil {
			t.Fatalf("FailStep returned unexpected error: %v", err)
		}

		loaded, err := store.Load(ctx, "corr-promoted-fail")
		if err != nil {
			t.Fatalf("failed to load slip: %v", err)
		}
		if loaded.Status != SlipStatusPromoted {
			t.Errorf("expected slip.status %q to remain unchanged after FailStep on promoted slip, got %q",
				SlipStatusPromoted, loaded.Status)
		}
	})

	// Sub-test 5: UpdateStepWithStatus on a promoted slip must leave slip.status as promoted.
	// checkPipelineCompletion short-circuits on promoted per STATE_MACHINE_V3.md.
	t.Run("post-promotion UpdateStepWithStatus does not change slip status", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-promoted-update",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "sha-promoted-update",
			Status:        SlipStatusPromoted,
			Steps: map[string]Step{
				"builds": {Status: StepStatusRunning},
			},
		}
		store.AddSlip(slip)

		if err := client.UpdateStepWithStatus(
			ctx,
			"corr-promoted-update",
			"builds",
			"",
			StepStatusFailed,
			"late build failure",
		); err != nil {
			t.Fatalf("UpdateStepWithStatus returned unexpected error: %v", err)
		}

		loaded, err := store.Load(ctx, "corr-promoted-update")
		if err != nil {
			t.Fatalf("failed to load slip: %v", err)
		}
		if loaded.Status != SlipStatusPromoted {
			t.Errorf("expected slip.status %q to remain unchanged after UpdateStepWithStatus on promoted slip, got %q",
				SlipStatusPromoted, loaded.Status)
		}
	})
}

// TestClient_AbandonSlip_Immutable verifies that an abandoned slip is pipeline-terminal and
// immutable: late step events (FailStep, CompleteStep, UpdateStepWithStatus) must not overwrite
// slip.status.
//
// STATE_MACHINE_V3.md §Pipeline termination states:
//
//	"abandoned / promoted: pipeline-terminal, bypass checkPipelineCompletion"
func TestClient_AbandonSlip_Immutable(t *testing.T) {
	ctx := context.Background()

	// Sub-test 1: AbandonSlip itself sets slip.status to abandoned.
	t.Run("AbandonSlip sets slip status to abandoned", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-abandon-basic",
			Repository:    "owner/repo",
			Branch:        "feature/thing",
			CommitSHA:     "sha-abandon-basic",
			Status:        SlipStatusInProgress,
			Steps:         map[string]Step{},
		}
		store.AddSlip(slip)

		if err := client.AbandonSlip(ctx, "corr-abandon-basic", "corr-main-newer"); err != nil {
			t.Fatalf("AbandonSlip returned unexpected error: %v", err)
		}

		loaded, err := store.Load(ctx, "corr-abandon-basic")
		if err != nil {
			t.Fatalf("failed to load slip: %v", err)
		}
		if loaded.Status != SlipStatusAbandoned {
			t.Errorf("expected slip.status %q after AbandonSlip, got %q", SlipStatusAbandoned, loaded.Status)
		}
	})

	// Sub-test 2: FailStep on an abandoned slip must leave slip.status as abandoned.
	// checkPipelineCompletion short-circuits on abandoned per STATE_MACHINE_V3.md.
	t.Run("post-abandon FailStep does not change slip status", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-abandoned-fail",
			Repository:    "owner/repo",
			Branch:        "feature/thing",
			CommitSHA:     "sha-abandoned-fail",
			Status:        SlipStatusAbandoned,
			Steps: map[string]Step{
				"unit_tests": {Status: StepStatusRunning},
				"dev_deploy": {Status: StepStatusPending},
			},
		}
		store.AddSlip(slip)

		if err := client.FailStep(ctx, "corr-abandoned-fail", "unit_tests", "", "late failure"); err != nil {
			t.Fatalf("FailStep returned unexpected error: %v", err)
		}

		loaded, err := store.Load(ctx, "corr-abandoned-fail")
		if err != nil {
			t.Fatalf("failed to load slip: %v", err)
		}
		if loaded.Status != SlipStatusAbandoned {
			t.Errorf("expected slip.status %q to remain unchanged after FailStep on abandoned slip, got %q",
				SlipStatusAbandoned, loaded.Status)
		}
	})

	// Sub-test 3: CompleteStep on an abandoned slip must leave slip.status as abandoned.
	t.Run("post-abandon CompleteStep does not change slip status", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-abandoned-complete",
			Repository:    "owner/repo",
			Branch:        "feature/thing",
			CommitSHA:     "sha-abandoned-complete",
			Status:        SlipStatusAbandoned,
			Steps: map[string]Step{
				"unit_tests": {Status: StepStatusRunning},
			},
		}
		store.AddSlip(slip)

		if err := client.CompleteStep(ctx, "corr-abandoned-complete", "unit_tests", ""); err != nil {
			t.Fatalf("CompleteStep returned unexpected error: %v", err)
		}

		loaded, err := store.Load(ctx, "corr-abandoned-complete")
		if err != nil {
			t.Fatalf("failed to load slip: %v", err)
		}
		if loaded.Status != SlipStatusAbandoned {
			t.Errorf("expected slip.status %q to remain unchanged after CompleteStep on abandoned slip, got %q",
				SlipStatusAbandoned, loaded.Status)
		}
	})

	// Sub-test 4: UpdateStepWithStatus with a failed status on an abandoned slip must leave
	// slip.status as abandoned.
	t.Run("post-abandon UpdateStepWithStatus does not change slip status", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-abandoned-update",
			Repository:    "owner/repo",
			Branch:        "feature/thing",
			CommitSHA:     "sha-abandoned-update",
			Status:        SlipStatusAbandoned,
			Steps: map[string]Step{
				"builds": {Status: StepStatusRunning},
			},
		}
		store.AddSlip(slip)

		if err := client.UpdateStepWithStatus(
			ctx,
			"corr-abandoned-update",
			"builds",
			"",
			StepStatusFailed,
			"late build failure",
		); err != nil {
			t.Fatalf("UpdateStepWithStatus returned unexpected error: %v", err)
		}

		loaded, err := store.Load(ctx, "corr-abandoned-update")
		if err != nil {
			t.Fatalf("failed to load slip: %v", err)
		}
		if loaded.Status != SlipStatusAbandoned {
			t.Errorf("expected slip.status %q to remain unchanged after UpdateStepWithStatus on abandoned slip, got %q",
				SlipStatusAbandoned, loaded.Status)
		}
	})
}

func TestClient_CreateSlipForPush_SquashMergePromotion(t *testing.T) {
	ctx := context.Background()

	t.Run("promotes feature branch slip on squash merge", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		// Set up feature branch slip
		featureSlip := &Slip{
			CorrelationID: "corr-feature-branch",
			Repository:    "owner/repo",
			CommitSHA:     "feature-head-sha",
			Branch:        "feature/add-thing",
			Status:        SlipStatusInProgress,
			CreatedAt:     time.Now().Add(-1 * time.Hour),
		}
		store.Slips["corr-feature-branch"] = featureSlip
		store.CommitIndex["owner/repo:feature-head-sha"] = "corr-feature-branch"

		// Set up PR head commit lookup (no git ancestry - simulates squash merge)
		github.SetPRHeadCommit("owner", "repo", 77, "feature-head-sha")
		// No ancestry from merge commit - squash merge creates new commit with no git parent link
		github.SetAncestry("owner", "repo", "squash-merge-sha", []string{"squash-merge-sha"})
		// But the PR head has its own ancestry that we can search
		github.SetAncestry("owner", "repo", "feature-head-sha", []string{"feature-head-sha"})

		opts := PushOptions{
			CorrelationID: "corr-merge-commit",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "squash-merge-sha",
			CommitMessage: "Add thing (#77)\n\n* First commit\n* Second commit",
			Components: []ComponentDefinition{
				{Name: "svc", DockerfilePath: "Dockerfile"},
			},
		}

		result, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		slip := result.Slip

		// Verify the new slip was created
		if slip.CorrelationID != "corr-merge-commit" {
			t.Errorf("expected correlation ID 'corr-merge-commit', got '%s'", slip.CorrelationID)
		}

		// Verify ancestry contains the promoted slip
		if len(slip.Ancestry) != 1 {
			t.Fatalf("expected 1 ancestry entry, got %d", len(slip.Ancestry))
		}
		ancestryEntry := slip.Ancestry[0]
		if ancestryEntry.CorrelationID != "corr-feature-branch" {
			t.Errorf("expected ancestry correlation ID 'corr-feature-branch', got '%s'", ancestryEntry.CorrelationID)
		}

		// Verify feature slip was promoted (not abandoned)
		promotedSlip := store.Slips["corr-feature-branch"]
		if promotedSlip.Status != SlipStatusPromoted {
			t.Errorf("expected feature slip status 'promoted', got '%s'", promotedSlip.Status)
		}
		if promotedSlip.PromotedTo != "corr-merge-commit" {
			t.Errorf("expected PromotedTo 'corr-merge-commit', got '%s'", promotedSlip.PromotedTo)
		}
	})

	t.Run("falls back to git ancestry when no PR in message", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		config := testPipelineConfig()
		client := NewClientWithDependencies(store, github, Config{PipelineConfig: config})

		// Set up ancestor slip
		ancestorSlip := &Slip{
			CorrelationID: "corr-ancestor",
			Repository:    "owner/repo",
			CommitSHA:     "parent-sha",
			Branch:        "main",
			Status:        SlipStatusInProgress,
			CreatedAt:     time.Now().Add(-1 * time.Hour),
		}
		store.Slips["corr-ancestor"] = ancestorSlip
		store.CommitIndex["owner/repo:parent-sha"] = "corr-ancestor"

		// Set up git ancestry
		github.SetAncestry("owner", "repo", "child-sha", []string{"child-sha", "parent-sha"})

		opts := PushOptions{
			CorrelationID: "corr-child",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "child-sha",
			CommitMessage: "Regular commit without PR reference",
			Components: []ComponentDefinition{
				{Name: "svc", DockerfilePath: "Dockerfile"},
			},
		}

		result, err := client.CreateSlipForPush(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		slip := result.Slip

		// Verify ancestry was resolved via git history
		if len(slip.Ancestry) != 1 {
			t.Fatalf("expected 1 ancestry entry, got %d", len(slip.Ancestry))
		}

		// Verify ancestor slip was abandoned (regular push, not squash merge)
		abandonedSlip := store.Slips["corr-ancestor"]
		if abandonedSlip.Status != SlipStatusAbandoned {
			t.Errorf("expected ancestor slip status 'abandoned', got '%s'", abandonedSlip.Status)
		}
	})
}
