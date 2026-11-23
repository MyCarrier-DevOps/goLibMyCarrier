package slippy

import (
	"context"
	"strings"
	"testing"
)

// retiredRehearsalStep is the name of the preprod rollback rehearsal step. It left the example
// configs and the skip guard in DEVOPS-634, but slips created before the live config dropped it
// still carry it, and workflows that started before the cutover can still write it. The tests
// that pin that tolerance name it here once.
const retiredRehearsalStep = "preprod_rollback_test"

// The skip guard no longer lists the retired step, so a skip of it is accepted instead of
// answering ErrSkipOverFailedCanary. This describes the state after the live config drops the
// step: PostgresStore builds slip.Steps from the config, so no loaded slip carries the step and
// the status the guard would read is never failed. The mock slip holds a failed entry only to
// show that SkipStep does not read it.
func TestClient_SkipStep_RetiredStepIsNotGuarded(t *testing.T) {
	ctx := context.Background()
	store := NewMockStore()
	slip := createTestSlip("corr-retired")
	slip.Steps[retiredRehearsalStep] = Step{Status: StepStatusFailed}
	store.AddSlip(slip)
	client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{})

	if err := client.SkipStep(ctx, "corr-retired", retiredRehearsalStep, "", "not canary"); err != nil {
		t.Fatalf("SkipStep on the retired step failed the caller: %v", err)
	}
	if len(store.UpdateStepCalls) != 1 || store.UpdateStepCalls[0].Status != StepStatusSkipped {
		t.Errorf("expected one skipped write, got %+v", store.UpdateStepCalls)
	}
}

// The guard still covers the steps the canary writers skip.
func TestSkipGuardedSteps_CoverCanaryAndProdSteps(t *testing.T) {
	for _, step := range []string{"prod_canary", "prod_deploy", "prod_tests"} {
		if _, ok := skipGuardedSteps[step]; !ok {
			t.Errorf("%s must stay in skipGuardedSteps", step)
		}
	}
	if _, ok := skipGuardedSteps[retiredRehearsalStep]; ok {
		t.Errorf("%s must not be in skipGuardedSteps", retiredRehearsalStep)
	}
}

// R3 at the Client: a write to the retired step returns nil for every verb that a pre-cutover
// workflow can send. The store decides what an unknown step means; the real Postgres behavior is
// pinned by TestRetiredStep_WritesAreAccepted_Integration.
func TestClient_WriteToRetiredStep_DoesNotFailCaller(t *testing.T) {
	ctx := context.Background()
	writes := map[string]func(*Client) error{
		"start": func(c *Client) error {
			return c.StartStep(ctx, "corr-w", retiredRehearsalStep, "", "run-1")
		},
		"complete": func(c *Client) error { return c.CompleteStep(ctx, "corr-w", retiredRehearsalStep, "") },
		"fail":     func(c *Client) error { return c.FailStep(ctx, "corr-w", retiredRehearsalStep, "", "rehearsal failed") },
		"skip":     func(c *Client) error { return c.SkipStep(ctx, "corr-w", retiredRehearsalStep, "", "not canary") },
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			store := NewMockStore()
			store.AddSlip(createTestSlip("corr-w"))
			client := NewClientWithDependencies(store, NewMockGitHubAPI(), Config{})

			if err := write(client); err != nil {
				t.Fatalf("write to the retired step failed the caller: %v", err)
			}
		})
	}
}

// R5: the config-driven ensurers only add columns. A config without the step emits no ensurer
// for it and nothing that drops a column, so the step's column stays in routing_slips.
func TestPostgresDynamicEnsurers_RetiredStepIsNeverDropped(t *testing.T) {
	cfg, err := LoadPipelineConfigFromFile("production.json")
	if err != nil {
		t.Fatalf("load production.json: %v", err)
	}
	column := stepStatusColumn(retiredRehearsalStep)

	for _, e := range GetPostgresDynamicEnsurers(cfg, nil) {
		if strings.Contains(e.Name, retiredRehearsalStep) || strings.Contains(e.SQL, column) {
			t.Errorf("ensurer %q mentions the retired step: %s", e.Name, e.SQL)
		}
		if strings.Contains(strings.ToUpper(e.SQL), "DROP") {
			t.Errorf("ensurer %q must not drop anything: %s", e.Name, e.SQL)
		}
	}
}
