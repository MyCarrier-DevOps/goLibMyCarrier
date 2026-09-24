package slippytest

import (
	"context"
	"errors"
	"testing"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

// ClaimContractCase is one step of the shared claim-contract sequence: a named operation to
// run against a store, and the observable state the store must be in afterwards.
type ClaimContractCase struct {
	Name string
	// Run performs the operation. Returning an error is part of the expected outcome, so it
	// is handed to Check rather than failing the case.
	Run func(ctx context.Context, store slippy.SlipStore) error
	// Check asserts on the error and on the seeded row as the store reports it.
	Check func(t *testing.T, err error, slip *slippy.Slip)
	// CheckOther is for a case whose subject is a row other than the seeded one, and runs
	// after Check. Optional.
	CheckOther func(ctx context.Context, t *testing.T, store slippy.SlipStore)
}

// RunClaimContract runs the claim, release, reset and terminal-write sequence against any
// SlipStore and asserts the observable state after each step.
//
// It exists because three separate rounds of PR #87 review found the same class of defect: a
// fix landed on PostgresStore and one or both test doubles kept the old behaviour. Create
// preserving a caller's ClaimedFrom, GuardReservedStepWrite reaching 5 of 16 entry points, and
// the slip_released marker on a terminal write were each caught by a human reading the diff,
// because nothing in the suite asserted the three implementers agree.
//
// All three now fail here, and it is worth recording that they did not at first: an earlier
// version of this paragraph claimed they would, while the sequence called neither Create nor
// UpdateComponentStatus, so it covered one of the three it named (PR #87 review, jhicks). The
// cases for the other two were added in response. What this suite covers is exactly the method
// calls below — when the next divergence is found in a method the sequence does not exercise,
// the case belongs here before the fix does.
//
// The point is that it is STORE-AGNOSTIC: run it against slippytest.MockStore, against the
// in-package double from a package slippy_test file, and against PostgresStore from the
// integration suite, and a divergence fails on whichever implementer drifted. A downstream
// SlipStore implementation can run it too — that is why it is exported from slippytest rather
// than kept in the library's own tests.
//
// newStore must return an empty store and a seeded slip's correlation ID. The slip must exist,
// be unclaimed, and carry a non-terminal status.
func RunClaimContract(t *testing.T, newStore func(t *testing.T) (slippy.SlipStore, string)) {
	t.Helper()
	ctx := context.Background()

	for _, name := range contractCaseNames() {
		t.Run(name, func(t *testing.T) {
			store, id := newStore(t)
			tc := caseByName(claimContractSequence(id), name)
			err := tc.Run(ctx, store)
			slip, loadErr := store.Load(ctx, id)
			if loadErr != nil {
				t.Fatalf("loading the slip after %q: %v", tc.Name, loadErr)
			}
			tc.Check(t, err, slip)
			if tc.CheckOther != nil {
				tc.CheckOther(ctx, t, store)
			}
		})
	}
}

// claimContractSequence is the contract itself, as data, so the cases are the same for every
// implementer by construction.
//
// id is the correlation ID the FACTORY seeded, threaded in rather than hardcoded. It was a
// package-level literal once, shadowed by the runner's own `id` from newStore, and the two
// agreed only because every in-repo wiring happened to return the same string (PR #87 review,
// pkuzmenko and jhicks). A downstream that honoured the documented contract and seeded its own
// ID — which a Postgres factory wants to do anyway, to avoid collisions between runs — had
// every operation run against a row that did not exist in its store: five cases failing with
// messages that pointed at the wrong thing, and the reserved-name case passing VACUOUSLY,
// because ErrSlipNotFound satisfied its only assertion.
func claimContractSequence(id string) []ClaimContractCase {
	return []ClaimContractCase{
		{
			Name: "a claim sets claimed_from and appends a marker naming the claimant",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				_, err := s.ClaimSlip(ctx, id, nil, "conformance/claimant", "")
				return err
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				contractRequireNoErr(t, err, "claim")
				if slip.ClaimedFrom == "" {
					t.Error("claimed_from must be set by a successful claim")
				}
				if n := contractCountStep(slip, slippy.ClaimMarkerStep); n != 1 {
					t.Errorf("expected exactly 1 %s marker, got %d", slippy.ClaimMarkerStep, n)
				}
				if a := contractLastActor(slip, slippy.ClaimMarkerStep); a != "conformance/claimant" {
					t.Errorf("the marker must name the claimant, got %q", a)
				}
			},
		},
		{
			Name: "a terminal status ends the claim AND records the release",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				if _, err := s.ClaimSlip(ctx, id, nil, "conformance/claimant", ""); err != nil {
					return err
				}
				return s.UpdateSlipStatus(ctx, id, slippy.SlipStatusCompleted)
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				contractRequireNoErr(t, err, "terminal write")
				if slip.ClaimedFrom != "" {
					t.Error("a terminal status must clear claimed_from")
				}
				// The column and the markers are read by different consumers, so clearing one
				// without recording the other is a divergence a marker-reading consumer sees.
				if n := contractCountStep(slip, slippy.ReleaseMarkerStep); n != 1 {
					t.Errorf("a terminal write that ended a claim must append exactly 1 %s marker, got %d",
						slippy.ReleaseMarkerStep, n)
				}
			},
		},
		{
			Name: "a terminal status on an UNCLAIMED slip records no release",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				return s.UpdateSlipStatus(ctx, id, slippy.SlipStatusCompleted)
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				contractRequireNoErr(t, err, "terminal write")
				if n := contractCountStep(slip, slippy.ReleaseMarkerStep); n != 0 {
					t.Errorf("no claim was held, so no release marker belongs on the row, got %d", n)
				}
			},
		},
		{
			Name: "a release on a quiescent claim clears it and appends a release marker",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				if _, err := s.ClaimSlip(ctx, id, nil, "conformance/claimant", ""); err != nil {
					return err
				}
				_, err := s.ReleaseClaim(ctx, id, "conformance/releaser", "")
				return err
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				contractRequireNoErr(t, err, "release")
				if slip.ClaimedFrom != "" {
					t.Error("a release with nothing in flight must clear claimed_from")
				}
				if n := contractCountStep(slip, slippy.ReleaseMarkerStep); n != 1 {
					t.Errorf("expected exactly 1 %s marker, got %d", slippy.ReleaseMarkerStep, n)
				}
			},
		},
		{
			Name: "an in-place reset is REFUSED on a claimed row and writes nothing",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				if _, err := s.ClaimSlip(ctx, id, nil, "conformance/claimant", ""); err != nil {
					return err
				}
				// Quiescent: no step has been reported, which is the shape that used to be
				// allowed through and wiped a dispatched run.
				return s.ResetSlipInPlace(ctx, &slippy.Slip{
					CorrelationID: id, Repository: "owner/repo", Branch: "main",
					CommitSHA: "conformance-sha", Status: slippy.SlipStatusPending,
				})
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				if err == nil {
					t.Fatal("a reset onto a claimed row must be refused")
				}
				if !isClaimedRefusal(err) {
					t.Errorf("the refusal must wrap ErrSlipClaimed, got %v", err)
				}
				if slip.ClaimedFrom == "" {
					t.Error("a refused reset writes nothing, so the claim must survive")
				}
				if n := contractCountStep(slip, slippy.ClaimMarkerStep); n != 1 {
					t.Errorf("nothing written means exactly the claim's own marker, got %d", n)
				}
			},
		},
		{
			// DEVOPS-373. The three implementers diverged here unseen: both doubles set the step's
			// status on every write, while Postgres dropped a componentless aggregate write.
			Name: "a componentless start of an aggregate step is in flight",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				return s.UpdateStep(ctx, id, "builds", "", slippy.StepStatusRunning)
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				contractRequireNoErr(t, err, "componentless start")
				if !slippy.RunInFlight(slip) {
					t.Error("a componentless start of the aggregate step must count as in flight")
				}
			},
		},
		{
			Name: "a claim is not released while a componentless aggregate start is in flight",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				if _, err := s.ClaimSlip(ctx, id, nil, "conformance/claimant", ""); err != nil {
					return err
				}
				if err := s.UpdateStep(ctx, id, "builds", "", slippy.StepStatusRunning); err != nil {
					return err
				}
				out, err := s.ReleaseClaim(ctx, id, "conformance/claimant", "")
				if err == nil && out.Released {
					return errors.New("release cleared a claim with a step in flight")
				}
				return err
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				contractRequireNoErr(t, err, "claim, start, release")
				if slip.ClaimedFrom == "" {
					t.Error("the claim must survive a release while the aggregate step is running")
				}
			},
		},
		{
			Name: "a componentless completion ends that start and the claim releases",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				if _, err := s.ClaimSlip(ctx, id, nil, "conformance/claimant", ""); err != nil {
					return err
				}
				if err := s.UpdateStep(ctx, id, "builds", "", slippy.StepStatusRunning); err != nil {
					return err
				}
				if err := s.UpdateStep(ctx, id, "builds", "", slippy.StepStatusCompleted); err != nil {
					return err
				}
				out, err := s.ReleaseClaim(ctx, id, "conformance/claimant", "")
				if err == nil && !out.Released {
					return errors.New("release refused with nothing in flight")
				}
				return err
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				contractRequireNoErr(t, err, "claim, start, complete, release")
				if slip.ClaimedFrom != "" {
					t.Error("the release must clear the claim once the aggregate step completed")
				}
				if slippy.RunInFlight(slip) {
					t.Error("nothing is in flight after the componentless completion")
				}
				if got := slip.Steps["builds"].Status; got != slippy.StepStatusCompleted {
					t.Errorf("a componentless completion must land on the aggregate step's own "+
						"status, got %q", got)
				}
			},
		},
		{
			// Defect A spanned four methods; the reserved-name case below exercises only
			// UpdateStepWithHistory. UpdateComponentStatus was unguarded on BOTH doubles
			// pre-fix and an implementation with that gap passed this contract until this
			// case existed (PR #87 review, jhicks).
			Name: "a caller may not address a reserved marker name as a step either",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				return s.UpdateComponentStatus(ctx, id, "api", slippy.ClaimMarkerStep,
					slippy.StepStatusRunning)
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				if err == nil {
					t.Fatal("a reserved marker name as the step type must be refused")
				}
				if !errors.Is(err, slippy.ErrReservedStepName) {
					t.Errorf("the refusal must wrap ErrReservedStepName, got %v", err)
				}
				if n := contractCountStep(slip, slippy.ClaimMarkerStep); n != 0 {
					t.Errorf("the refusal must write nothing, got %d forged markers", n)
				}
			},
		},
		{
			// The FIRST divergence this harness's godoc claims it would have caught, and the
			// one it could not see until now: a fresh insert must not persist a ClaimedFrom
			// the store's INSERT column list cannot write. Both doubles kept the caller's
			// value once, so a consumer asserting ErrSlipWentLive on a later Repave passed
			// against a row Postgres would have left unclaimed (PR #87 review, jhicks).
			Name: "Create never persists a caller-supplied claim",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				return s.Create(ctx, &slippy.Slip{
					CorrelationID: id + "-fresh",
					Repository:    "owner/repo",
					Branch:        "main",
					CommitSHA:     "conformance-create-sha",
					Status:        slippy.SlipStatusPending,
					ClaimedFrom:   slippy.SlipStatusFailed,
				})
			},
			Check: func(t *testing.T, err error, _ *slippy.Slip) {
				contractRequireNoErr(t, err, "create")
			},
			// The assertion is on the row Create wrote, not on the seeded one.
			CheckOther: func(ctx context.Context, t *testing.T, s slippy.SlipStore) {
				fresh, loadErr := s.Load(ctx, id+"-fresh")
				if loadErr != nil {
					t.Fatalf("loading the created slip: %v", loadErr)
				}
				if fresh.ClaimedFrom != "" {
					t.Errorf("claimed_from is absent from the INSERT column list, so a fresh "+
						"insert is always unclaimed; got %q", fresh.ClaimedFrom)
				}
			},
		},
		{
			Name: "a caller may not write a step under a reserved marker name",
			Run: func(ctx context.Context, s slippy.SlipStore) error {
				return s.UpdateStepWithHistory(ctx, id, "builds", "", slippy.StepStatusRunning,
					slippy.StateHistoryEntry{Step: slippy.ReleaseMarkerStep, Actor: "impostor"})
			},
			Check: func(t *testing.T, err error, slip *slippy.Slip) {
				if err == nil {
					t.Fatal("a caller-supplied entry under a marker name must be refused")
				}
				// The SENTINEL, not merely a non-nil error. Accepting any error let this case
				// report conformance for an unrelated failure — and it is what made the case
				// pass against a store the sequence was never operating on.
				if !errors.Is(err, slippy.ErrReservedStepName) {
					t.Errorf("the refusal must wrap ErrReservedStepName, got %v", err)
				}
				if n := contractCountStep(slip, slippy.ReleaseMarkerStep); n != 0 {
					t.Errorf("the refusal must write nothing, got %d forged markers", n)
				}
			},
		},
	}
}

// isClaimedRefusal keeps the sentinel check in one place, so a store that wraps it differently
// still satisfies the contract as long as errors.Is reaches it.
func isClaimedRefusal(err error) bool {
	return errors.Is(err, slippy.ErrSlipClaimed)
}

// contractCaseNames lists the cases without needing a real id: names do not depend on it, and
// the runner has to iterate before it has called the factory.
func contractCaseNames() []string {
	seq := claimContractSequence("")
	names := make([]string, 0, len(seq))
	for _, c := range seq {
		names = append(names, c.Name)
	}
	return names
}

// caseByName finds the case to run once the sequence has been rebuilt with the factory's id.
func caseByName(seq []ClaimContractCase, name string) ClaimContractCase {
	for _, c := range seq {
		if c.Name == name {
			return c
		}
	}
	panic("slippytest: unknown claim-contract case " + name)
}

func contractCountStep(slip *slippy.Slip, step string) int {
	n := 0
	for _, e := range slip.StateHistory {
		if e.Step == step {
			n++
		}
	}
	return n
}

func contractLastActor(slip *slippy.Slip, step string) string {
	actor := ""
	for _, e := range slip.StateHistory {
		if e.Step == step {
			actor = e.Actor
		}
	}
	return actor
}

func contractRequireNoErr(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", what, err)
	}
}
