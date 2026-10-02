package slippy

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// DEVOPS-314: the pipeline gains two steps, preprod_rollback_test (after preprod_tests, a
// prerequisite of prod_gate) and prod_canary (after prod_release_created, a prerequisite of
// prod_deploy). These tests pin the STATE_MACHINE_V3 invariants for the new shape. They are
// in their own file so they do not collide with state_machine_invariants_test.go.
//
// Scope note (library path only). The four slip-level tests below exercise the library hold
// path. The aborted-downstream behaviour they assert is what Client.WaitForPrerequisites does
// (hold.go). The Slippy CLI, which is what Argo pre-jobs actually run, polls the read-only
// step-prerequisites API (slippy-api reads never write `aborted`): a downstream step stays
// `pending` and the pre-job exits non-zero. Retry still works on that path because the
// recovery reset (executor.go checkPipelineCompletion) only touches `aborted` steps, and a
// `pending` step needs no reset.

func canarySlip(corrID string, status SlipStatus, steps map[string]Step) *Slip {
	return &Slip{
		CorrelationID: corrID,
		Repository:    "owner/repo",
		Branch:        "main",
		CommitSHA:     "sha-" + corrID,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		Status:        status,
		Steps:         steps,
	}
}

func canaryTestClient() (*Client, *MockStore) {
	store := NewMockStore()
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{
		HoldTimeout:  500 * time.Millisecond,
		PollInterval: 10 * time.Millisecond,
	})
	return client, store
}

func canaryLoad(t *testing.T, store *MockStore, corrID string) *Slip {
	t.Helper()
	loaded, err := store.Load(context.Background(), corrID)
	if err != nil {
		t.Fatalf("load slip: %v%s", err, stateMachineRef)
	}
	return loaded
}

func canaryStepStatus(t *testing.T, store *MockStore, corrID, step string) StepStatus {
	t.Helper()
	return canaryLoad(t, store, corrID).Steps[step].Status
}

// canaryProdDeploySlip is the shared start state: prod_canary running, its siblings done or pending.
func canaryProdDeploySlip(corrID string) *Slip {
	return canarySlip(corrID, SlipStatusInProgress, map[string]Step{
		"prod_gate":            {Status: StepStatusCompleted},
		"prod_release_created": {Status: StepStatusCompleted},
		"prod_canary":          {Status: StepStatusRunning},
		"prod_deploy":          {Status: StepStatusPending},
		"prod_tests":           {Status: StepStatusPending},
		"prod_steady_state":    {Status: StepStatusPending},
	})
}

// canaryHoldProdDeploy holds prod_deploy on its three prerequisites.
func canaryHoldProdDeploy(client *Client, corrID string) error {
	return client.WaitForPrerequisites(context.Background(), HoldOptions{
		CorrelationID: corrID,
		Prerequisites: []string{"prod_gate", "prod_release_created", "prod_canary"},
		StepName:      "prod_deploy",
	})
}

// Precondition: prod_canary running, prod_gate and prod_release_created completed.
// Action:       FailStep(prod_canary), then hold prod_deploy on its three prerequisites.
// Expected:     slip=failed; the hold returns ErrPrerequisiteFailed and aborts prod_deploy;
//
//	prod_tests and prod_steady_state stay non-terminal (nothing else is touched).
func TestStateMachine_CanarySteps_FailProdCanary_SlipFailed_DownstreamAbortsLazily(t *testing.T) {
	ctx := context.Background()
	client, store := canaryTestClient()
	corrID := "canary-fail"
	store.AddSlip(canaryProdDeploySlip(corrID))

	if err := client.FailStep(ctx, corrID, "prod_canary", "", "canary analysis failed"); err != nil {
		t.Fatalf("FailStep returned unexpected error: %v%s", err, stateMachineRef)
	}
	loaded := canaryLoad(t, store, corrID)
	if loaded.Status != SlipStatusFailed {
		t.Errorf("expected slip.status=%q after FailStep(prod_canary), got %q%s",
			SlipStatusFailed, loaded.Status, stateMachineRef)
	}
	// The failed step itself is the primary failure, not an abort.
	if got := loaded.Steps["prod_canary"].Status; got != StepStatusFailed {
		t.Errorf("expected prod_canary=%q, got %q%s", StepStatusFailed, got, stateMachineRef)
	}
	// Downstream steps are untouched until their own hold runs (lazy abort).
	for _, s := range []string{"prod_deploy", "prod_tests", "prod_steady_state"} {
		if got := loaded.Steps[s].Status; got != StepStatusPending {
			t.Errorf("expected %s to stay %q before its own hold, got %q%s", s, StepStatusPending, got, stateMachineRef)
		}
	}

	if err := canaryHoldProdDeploy(client, corrID); !errors.Is(err, ErrPrerequisiteFailed) {
		t.Fatalf("expected ErrPrerequisiteFailed from prod_deploy hold, got %v%s", err, stateMachineRef)
	}
	if got := canaryStepStatus(t, store, corrID, "prod_deploy"); got != StepStatusAborted {
		t.Errorf("expected prod_deploy=%q after failed hold, got %q%s", StepStatusAborted, got, stateMachineRef)
	}
	// prod_tests and prod_steady_state have not held yet: still non-terminal, no new status.
	for _, s := range []string{"prod_tests", "prod_steady_state"} {
		if got := canaryStepStatus(t, store, corrID, s); got != StepStatusPending {
			t.Errorf("expected %s to stay %q until its own hold, got %q%s", s, StepStatusPending, got, stateMachineRef)
		}
	}
	if got := canaryStepStatus(t, store, corrID, "prod_canary"); got != StepStatusFailed {
		t.Errorf("prod_canary must stay %q after the downstream abort, got %q%s", StepStatusFailed, got, stateMachineRef)
	}
}

// Precondition: preprod_rollback_test failed, everything else upstream of prod_gate completed.
// Action:       hold prod_gate on preprod_deploy, preprod_tests, preprod_rollback_test.
// Expected:     ErrPrerequisiteFailed; prod_gate is aborted, never running or completed.
func TestStateMachine_CanarySteps_FailedPreprodRollbackTest_HoldsProdGate(t *testing.T) {
	ctx := context.Background()
	client, store := canaryTestClient()
	corrID := "rollback-test-fail"
	store.AddSlip(canarySlip(corrID, SlipStatusInProgress, map[string]Step{
		"preprod_deploy":        {Status: StepStatusCompleted},
		"preprod_tests":         {Status: StepStatusCompleted},
		"preprod_rollback_test": {Status: StepStatusRunning},
		"prod_gate":             {Status: StepStatusPending},
	}))

	if err := client.FailStep(ctx, corrID, "preprod_rollback_test", "", "rollback rehearsal failed"); err != nil {
		t.Fatalf("FailStep returned unexpected error: %v%s", err, stateMachineRef)
	}

	err := client.WaitForPrerequisites(ctx, HoldOptions{
		CorrelationID: corrID,
		Prerequisites: []string{"preprod_deploy", "preprod_tests", "preprod_rollback_test"},
		StepName:      "prod_gate",
	})
	if !errors.Is(err, ErrPrerequisiteFailed) {
		t.Fatalf("expected ErrPrerequisiteFailed from prod_gate hold, got %v%s", err, stateMachineRef)
	}
	// Aborted implies it never reached running or completed.
	if got := canaryStepStatus(t, store, corrID, "prod_gate"); got != StepStatusAborted {
		t.Errorf("expected prod_gate=%q (library hold path), got %q%s", StepStatusAborted, got, stateMachineRef)
	}
}

// Precondition: both new steps skipped (the non-canary repo path), rest of the prereqs completed.
// Action:       CheckPrerequisites for prod_gate and prod_deploy; skip then re-check.
// Expected:     PrereqStatusCompleted for both; the slip is not failed.
func TestStateMachine_CanarySteps_SkippedNewSteps_SatisfyPrereqs(t *testing.T) {
	ctx := context.Background()
	client, store := canaryTestClient()
	corrID := "canary-skipped"
	store.AddSlip(canarySlip(corrID, SlipStatusInProgress, map[string]Step{
		"preprod_deploy":        {Status: StepStatusCompleted},
		"preprod_tests":         {Status: StepStatusCompleted},
		"preprod_rollback_test": {Status: StepStatusPending},
		"prod_gate":             {Status: StepStatusCompleted},
		"prod_release_created":  {Status: StepStatusCompleted},
		"prod_canary":           {Status: StepStatusPending},
	}))

	gatePrereqs := []string{"preprod_deploy", "preprod_tests", "preprod_rollback_test"}
	deployPrereqs := []string{"prod_gate", "prod_release_created", "prod_canary"}

	// checkBoth asserts both dependants resolve to want.
	checkBoth := func(when string, want PrereqStatus) *Slip {
		slip := canaryLoad(t, store, corrID)
		for name, prereqs := range map[string][]string{"prod_gate": gatePrereqs, "prod_deploy": deployPrereqs} {
			res, err := client.CheckPrerequisites(ctx, slip, prereqs, "")
			if err != nil {
				t.Fatalf("CheckPrerequisites(%s): %v%s", name, err, stateMachineRef)
			}
			if res.Status != want {
				t.Errorf("%s %s: expected %q, got %q%s", name, when, want, res.Status, stateMachineRef)
			}
		}
		return slip
	}

	// Before the skip a pending new step holds its dependants. This is why the skip writer
	// must run before prod_gate on every repo that does not use canary.
	checkBoth("with a pending new step", PrereqStatusRunning)
	for _, s := range []string{"preprod_rollback_test", "prod_canary"} {
		if err := client.SkipStep(ctx, corrID, s, "", "deployment-strategy is not canary"); err != nil {
			t.Fatalf("SkipStep(%s): %v%s", s, err, stateMachineRef)
		}
	}
	slip := checkBoth("with skipped new steps", PrereqStatusCompleted)
	if slip.Status == SlipStatusFailed {
		t.Errorf("skipping the new steps must not fail the slip%s", stateMachineRef)
	}
	if got := slip.Steps["prod_canary"].Status; got != StepStatusSkipped {
		t.Errorf("expected prod_canary=%q, got %q%s", StepStatusSkipped, got, stateMachineRef)
	}
}

// Precondition: prod_canary failed, prod_deploy aborted by its hold, slip=failed.
// Action:       CompleteStep(prod_canary) (retry succeeds).
// Expected:     prod_deploy aborted -> pending and slip failed -> in_progress.
func TestStateMachine_CanarySteps_AbortedResetOnRetry(t *testing.T) {
	ctx := context.Background()
	client, store := canaryTestClient()
	corrID := "canary-retry"
	store.AddSlip(canaryProdDeploySlip(corrID))

	if err := client.FailStep(ctx, corrID, "prod_canary", "", "canary analysis failed"); err != nil {
		t.Fatalf("FailStep: %v%s", err, stateMachineRef)
	}
	if err := canaryHoldProdDeploy(client, corrID); !errors.Is(err, ErrPrerequisiteFailed) {
		t.Fatalf("expected ErrPrerequisiteFailed, got %v%s", err, stateMachineRef)
	}
	if got := canaryStepStatus(t, store, corrID, "prod_deploy"); got != StepStatusAborted {
		t.Fatalf("setup: expected prod_deploy=%q, got %q%s", StepStatusAborted, got, stateMachineRef)
	}

	if err := client.CompleteStep(ctx, corrID, "prod_canary", ""); err != nil {
		t.Fatalf("CompleteStep(prod_canary): %v%s", err, stateMachineRef)
	}

	loaded := canaryLoad(t, store, corrID)
	if got := loaded.Steps["prod_deploy"].Status; got != StepStatusPending {
		t.Errorf("expected prod_deploy reset to %q after retry, got %q%s", StepStatusPending, got, stateMachineRef)
	}
	if loaded.Status != SlipStatusInProgress {
		t.Errorf("expected slip.status=%q after retry, got %q%s", SlipStatusInProgress, loaded.Status, stateMachineRef)
	}
}

// canaryExampleGraph is the expected prerequisite graph for the four steps DEVOPS-314 changed
// or added, shared by both example files.
var canaryExampleGraph = map[string][]string{
	"preprod_tests":         {"preprod_deploy"},
	"preprod_rollback_test": {"preprod_tests"},
	"prod_gate":             {"preprod_deploy", "preprod_tests", "preprod_rollback_test"},
	"prod_release_created":  {"prod_gate"},
	"prod_canary":           {"prod_release_created"},
	"prod_deploy":           {"prod_gate", "prod_release_created", "prod_canary"},
	"prod_tests":            {"prod_gate", "prod_deploy"},
}

// TestPipelineConfig_ExampleFiles_CanaryGraph pins the DEVOPS-314 graph in both example configs.
// These files are examples (the live config is in Vault), so only the steps this change
// touches are pinned; other drift between the files and Vault is deliberately not asserted.
func TestPipelineConfig_ExampleFiles_CanaryGraph(t *testing.T) {
	// devDeployPrereqs records each file's CURRENT dev_deploy prerequisites so an accidental
	// edit is caught. default.json differs from production.json and the live config (which
	// follow STATE_MACHINE_V3 rule 7, [builds]); that drift predates DEVOPS-314 and is out of
	// scope here.
	cases := []struct {
		file           string
		devDeployPreqs []string
	}{
		{"default.json", []string{"builds", "unit_tests", "secret_scan"}},
		{"production.json", []string{"builds"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			cfg, err := LoadPipelineConfigFromFile(tc.file)
			if err != nil {
				t.Fatalf("load %s: %v", tc.file, err)
			}
			// Membership, not an exact count: unrelated steps may be added without breaking this.
			if len(cfg.Steps) < len(canaryExampleGraph) {
				t.Errorf("%s: only %d steps, fewer than the %d pinned", tc.file, len(cfg.Steps), len(canaryExampleGraph))
			}
			for name, want := range canaryExampleGraph {
				step := cfg.GetStep(name)
				if step == nil {
					t.Errorf("%s: step %q missing", tc.file, name)
					continue
				}
				if !slices.Equal(step.Prerequisites, want) {
					t.Errorf("%s: %s prerequisites = %v, want %v", tc.file, name, step.Prerequisites, want)
				}
			}
			if dd := cfg.GetStep("dev_deploy"); dd == nil || !slices.Equal(dd.Prerequisites, tc.devDeployPreqs) {
				t.Errorf("%s: dev_deploy prerequisites changed, want %v", tc.file, tc.devDeployPreqs)
			}
			// Full dependent sets (reverse edges): extra edges onto unpinned steps must fail.
			dependents := func(prereq string) []string {
				var out []string
				for _, st := range cfg.Steps {
					if slices.Contains(st.Prerequisites, prereq) {
						out = append(out, st.Name)
					}
				}
				slices.Sort(out)
				return out
			}
			if got := dependents("preprod_rollback_test"); !slices.Equal(got, []string{"prod_gate"}) {
				t.Errorf("%s: steps depending on preprod_rollback_test = %v, want only [prod_gate]", tc.file, got)
			}
			if got := dependents("prod_canary"); !slices.Equal(got, []string{"prod_deploy"}) {
				t.Errorf("%s: steps depending on prod_canary = %v, want only [prod_deploy]", tc.file, got)
			}
			// The new steps must not be gates and must not aggregate component work.
			for _, name := range []string{"preprod_rollback_test", "prod_canary"} {
				if s := cfg.GetStep(name); s != nil && (s.IsGate || s.Aggregates != "") {
					t.Errorf("%s: %s must be a plain step (is_gate=%v aggregates=%q)", tc.file, name, s.IsGate, s.Aggregates)
				}
			}
		})
	}
}

// TestPipelineConfig_ExampleFiles_ProdSteadyStateReachable pins STATE_MACHINE_V3 rule 7
// ("confirm prod_steady_state is reachable") for the example configs by walking the
// prerequisite graph from an empty slip. It is reachable when the two canary-only steps are
// skipped and when they complete (those two positive checks are sanity checks only: any
// config that loads is acyclic, so they cannot catch a wrong edge; the CanaryGraph reverse-edge
// assertions do that). A canary step stuck in neither state blocks prod_deploy,
// prod_tests and prod_steady_state, which is why the skip writer must run on non-canary repos.
func TestPipelineConfig_ExampleFiles_ProdSteadyStateReachable(t *testing.T) {
	// reach returns the steps that can finish. A step in satisfied counts as met without
	// running (skipped); a step in stuck never finishes (pending/failed forever).
	reach := func(cfg *PipelineConfig, satisfied, stuck map[string]bool) map[string]bool {
		done := map[string]bool{}
		for progressed := true; progressed; {
			progressed = false
			for _, s := range cfg.Steps {
				if done[s.Name] || satisfied[s.Name] || stuck[s.Name] {
					continue
				}
				ok := true
				for _, p := range s.Prerequisites {
					if !done[p] && !satisfied[p] {
						ok = false
						break
					}
				}
				if ok {
					done[s.Name] = true
					progressed = true
				}
			}
		}
		return done
	}
	for _, file := range []string{"default.json", "production.json"} {
		t.Run(file, func(t *testing.T) {
			cfg, err := LoadPipelineConfigFromFile(file)
			if err != nil {
				t.Fatalf("load %s: %v", file, err)
			}
			skipped := map[string]bool{"prod_canary": true, "preprod_rollback_test": true}
			if !reach(cfg, skipped, nil)["prod_steady_state"] {
				t.Errorf("%s: prod_steady_state unreachable with the canary steps skipped", file)
			}
			if !reach(cfg, nil, nil)["prod_steady_state"] {
				t.Errorf("%s: prod_steady_state unreachable with the canary steps completing", file)
			}
			stuckCanary := reach(cfg, nil, map[string]bool{"prod_canary": true})
			for _, s := range []string{"prod_deploy", "prod_tests", "prod_steady_state"} {
				if stuckCanary[s] {
					t.Errorf("%s: %s must be blocked while prod_canary is neither completed nor skipped", file, s)
				}
			}
			stuckRollback := reach(cfg, nil, map[string]bool{"preprod_rollback_test": true})
			for _, s := range []string{"prod_gate", "prod_release_created", "prod_canary", "prod_deploy", "prod_tests", "prod_steady_state"} {
				if stuckRollback[s] {
					t.Errorf("%s: %s must be blocked while preprod_rollback_test is neither completed nor skipped", file, s)
				}
			}
		})
	}
}
