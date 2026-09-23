// Package slippytest provides test fixtures and mocks for testing code that uses the slippy package.
// This follows the Go standard library pattern (e.g., net/http/httptest).
//
// Example usage:
//
//	func TestMyFunction(t *testing.T) {
//	    store := slippytest.NewMockStore()
//	    github := slippytest.NewMockGitHubAPI()
//	    client := slippy.NewClientWithDependencies(store, github, slippy.Config{})
//
//	    // Configure mock behavior
//	    store.AddSlip(&slippy.Slip{CorrelationID: "test-123"})
//
//	    // Run your test
//	    result, err := myFunction(client)
//
//	    // Verify interactions
//	    if len(store.CreateCalls) != 1 {
//	        t.Error("expected one create call")
//	    }
//	}
package slippytest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

// pluralize converts a singular step name to its plural form for column naming.
// This matches the pluralize function in the slippy package.
func pluralize(name string) string {
	if strings.HasSuffix(name, "s") {
		return name + "es"
	}
	return name + "s"
}

// commitKey builds the commit-identity key for a repository/commit pair. The repository is
// lowercased to mirror PostgresStore's case-insensitive `lower(repository) = lower($1)`
// comparison (postgres_store.go); the commit SHA is compared as-is, matching production.
// Every commit comparison in this file goes through this helper so no call site can disagree
// with another about which rows share a commit - a partial fix (some sites lowercased, others
// not) is worse than none.
func commitKey(repository, commitSHA string) string {
	return strings.ToLower(repository) + ":" + commitSHA
}

// supersededTerminal reports whether a status is one the store excludes from its
// "current slip for this commit" reads. It mirrors the SQL predicate
// `status NOT IN ('abandoned','promoted','compensated')`, which appears on exactly two of the
// four commit lookups: LoadLiveByCommit (postgres_store.go) and FindByCommits
// (postgres_store_reads.go). LoadByCommit and FindAllByCommits deliberately carry no such
// filter, so do not add one here without changing those queries too.
func supersededTerminal(status slippy.SlipStatus) bool {
	return status == slippy.SlipStatusAbandoned ||
		status == slippy.SlipStatusPromoted ||
		status == slippy.SlipStatusCompensated
}

// loadOrder sorts rows the way the store's two Load* queries order them: live rows first, then
// updated_at DESC (`ORDER BY (status IN (repaveable)) ASC, updated_at DESC`, postgres_store.go).
//
// The live/ended split reads SlipStatus.IsLive - the same predicate repaveableSlipStatusesSQL is
// pinned against by TestRepaveableSlipStatusesSQL_MatchesIsLive - so this and the store cannot
// drift on which statuses count as ended without that test failing.
func loadOrder(rows []*slippy.Slip) {
	sort.Slice(rows, func(i, j int) bool {
		liveI, liveJ := rows[i].Status.IsLive(), rows[j].Status.IsLive()
		if liveI != liveJ {
			return liveI
		}
		return tieBreak(rows[i], rows[j])
	})
}

// findOrder sorts rows the way the store's two Find* queries order them: updated_at DESC with
// NO live-first term (`ORDER BY c.priority ASC, s.updated_at DESC`, postgres_store_reads.go).
// c.priority orders ACROSS commits and is handled by the callers' loop over the commit list, so
// only the within-commit tie-break belongs here.
//
// The difference from loadOrder is deliberate and load-bearing: sorting Find* results live-first
// makes this double disagree with the store, which returns the newest row regardless of whether
// an older one is still running. Whether the store SHOULD order live-first is a separate open
// question about the store, not something a double gets to decide.
func findOrder(rows []*slippy.Slip) {
	sort.Slice(rows, func(i, j int) bool { return tieBreak(rows[i], rows[j]) })
}

// tieBreak is the updated_at DESC comparison both orderings share, falling back to correlation
// ID so this double stays deterministic where Postgres (ORDER BY ... over equal keys) is not.
// No test should depend on which of two otherwise-identical rows wins; seed distinct timestamps
// when the choice matters.
func tieBreak(a, b *slippy.Slip) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.CorrelationID < b.CorrelationID
}

// MockStore is an in-memory implementation of slippy.SlipStore for testing.
//
// It provides configurable behavior and tracking of method calls.
// Features:
//   - In-memory storage with thread-safe access
//   - Call tracking for all methods
//   - Error injection (global and per-ID)
//   - Helper methods for test setup (AddSlip, Reset)
type MockStore struct {
	mu sync.RWMutex

	// Storage maps correlation_id -> Slip
	Slips map[string]*slippy.Slip

	// Call tracking
	CreateCalls           []CreateCall
	LoadCalls             []string
	LoadByCommitCalls     []LoadByCommitCall
	LoadLiveByCommitCalls []LoadByCommitCall
	FindByCommitsCalls    []FindByCommitsCall
	FindAllByCommitsCalls []FindAllByCommitsCall
	UpdateCalls           []UpdateCall
	UpdateStepCalls       []UpdateStepCall
	UpdateComponentCalls  []UpdateComponentCall
	AppendHistoryCalls    []AppendHistoryCall
	SetImageTagCalls      []SetImageTagCall
	UpdateSlipStatusCalls []UpdateSlipStatusCall
	ClaimSlipCalls        []ClaimSlipCall
	ReleaseClaimCalls     []ReleaseClaimCall
	ResetInPlaceCalls     []string
	RepaveCalls           []string
	// RepaveSuccessorCalls parallels RepaveCalls with the successor's correlation ID from
	// the same call (empty string when a nil successor was passed). The in-memory mock has
	// no slip_ancestry-equivalent table to repoint (InsertAncestryLink/ResolveAncestry are
	// no-ops below), so it does not replicate PostgresStore's descendant-repoint behavior —
	// this only records the argument for assertions.
	RepaveSuccessorCalls []string
	// RepaveParents parallels RepaveCalls with the parent link argument from the same call
	// (nil when the caller resolved no ancestry). The mock cannot carry a superseded run's
	// own link forward the way PostgresStore does — it has no ancestry table to read one
	// from — so tests assert on what the caller passed in.
	RepaveParents []*slippy.AncestryEntry
	CloseCalls    int

	// Ping tracking and error injection
	PingCalls int
	PingError error

	// Error injection for testing error paths
	CreateError           error
	LoadError             error
	LoadByCommitError     error
	LoadLiveByCommitError error
	FindByCommitsError    error
	FindAllByCommitsError error
	UpdateError           error
	UpdateStepError       error
	UpdateComponentError  error
	AppendHistoryError    error
	SetImageTagError      error
	UpdateSlipStatusError error
	ClaimSlipError        error
	ReleaseClaimError     error
	ResetInPlaceError     error
	ProbeSchemaError      error
	RepaveError           error
	CloseError            error

	// Conditional error injection (returns error only for specific IDs)
	CreateErrorFor          map[string]error
	LoadErrorFor            map[string]error
	UpdateStepErrorFor      map[string]error
	UpdateComponentErrorFor map[string]error
	AppendHistoryErrorFor   map[string]error

	// RepaveWentLiveStatus simulates a slip transitioning to a live status in the window
	// between a caller's repave decision (an earlier LoadByCommit/Load saw it ended) and
	// the Repave call itself: when Repave is invoked for a correlation ID present in this
	// map WHILE RepaveError is set (e.g. to slippy.ErrSlipWentLive), the mock mutates the
	// stored row's status to the mapped value before returning the injected error, then
	// removes the entry (one-shot). This lets a subsequent Load (the caller's
	// reload-after-ErrSlipWentLive) observe the new state instead of the stale
	// decision-time snapshot. Mirrors the internal slippy package's
	// MockStore.RepaveWentLiveStatus (DEVOPS-231 review D1.2).
	//
	// Note this is only needed to force the error path. Repave's own live guard already
	// returns slippy.ErrSlipWentLive for a stored slip whose status IsLive(), so a test
	// that can arrange the live status directly does not need this field at all.
	RepaveWentLiveStatus map[string]slippy.SlipStatus
}

// CreateCall records a Create call.
type CreateCall struct {
	Slip *slippy.Slip
}

// LoadByCommitCall records a LoadByCommit call.
type LoadByCommitCall struct {
	Repository string
	CommitSHA  string
}

// FindByCommitsCall records a FindByCommits call.
type FindByCommitsCall struct {
	Repository string
	Commits    []string
}

// FindAllByCommitsCall records a FindAllByCommits call.
type FindAllByCommitsCall struct {
	Repository string
	Commits    []string
}

// UpdateCall records an Update call.
type UpdateCall struct {
	Slip *slippy.Slip
}

// UpdateStepCall records an UpdateStep call.
type UpdateStepCall struct {
	CorrelationID string
	StepName      string
	ComponentName string
	Status        slippy.StepStatus
}

// UpdateComponentCall records an UpdateComponentStatus call.
type UpdateComponentCall struct {
	CorrelationID string
	ComponentName string
	StepType      string
	Status        slippy.StepStatus
}

// AppendHistoryCall records an AppendHistory call.
type AppendHistoryCall struct {
	CorrelationID string
	Entry         slippy.StateHistoryEntry
}

// SetImageTagCall records a SetComponentImageTag call.
type SetImageTagCall struct {
	CorrelationID string
	StepName      string
	ComponentName string
	ImageTag      string
}

// UpdateSlipStatusCall records an UpdateSlipStatus call.
type UpdateSlipStatusCall struct {
	CorrelationID string
	Status        slippy.SlipStatus
}

// ReleaseClaimCall records a call to ReleaseClaim.
type ReleaseClaimCall struct {
	CorrelationID string
	ReleasedBy    string
	Reason        string
}

// ClaimSlipCall records a call to ClaimSlip.
type ClaimSlipCall struct {
	CorrelationID string
	Expected      []slippy.SlipStatus
	ClaimedBy     string
	Reason        string
}

// NewMockStore creates a new MockStore with initialized maps.
func NewMockStore() *MockStore {
	return &MockStore{
		Slips:                   make(map[string]*slippy.Slip),
		CreateErrorFor:          make(map[string]error),
		LoadErrorFor:            make(map[string]error),
		UpdateStepErrorFor:      make(map[string]error),
		UpdateComponentErrorFor: make(map[string]error),
		AppendHistoryErrorFor:   make(map[string]error),
		RepaveWentLiveStatus:    make(map[string]slippy.SlipStatus),
	}
}

// Create persists a new routing slip.
func (m *MockStore) Create(ctx context.Context, slip *slippy.Slip) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CreateCalls = append(m.CreateCalls, CreateCall{Slip: slip})

	if m.CreateError != nil {
		return m.CreateError
	}

	if err, ok := m.CreateErrorFor[slip.CorrelationID]; ok {
		return err
	}

	// Deep copy the slip to avoid mutations, and let the STORE own claimed_from on both arms
	// of the upsert — a Create never takes a claim from its caller, on either.
	//
	// An existing row keeps the claim it already had: PostgresStore's Create is an ON CONFLICT
	// DO UPDATE whose SET list is slipColumns(), which excludes the SELECT-only claimed_from,
	// so a redelivered Create resets the row but not the claim.
	//
	// A FRESH INSERT IS ALWAYS UNCLAIMED, which is the half this double used to get wrong (PR
	// #87, jhicks review): claimed_from is absent from the INSERT column list too, not merely
	// from the conflict arm's SET list — buildCreateQuery builds both from slipColumns() —
	// so the column is NULL on a first insert whatever the caller's Slip carried. Storing the
	// caller's value here made a consumer test that Creates a claimed Slip and then asserts
	// ErrSlipWentLive on Repave pass against a row Postgres would have left unclaimed and
	// repaved: green on the double, the opposite outcome in production.
	slipCopy := DeepCopySlip(slip)
	slipCopy.ClaimedFrom = ""
	if existing, ok := m.Slips[slip.CorrelationID]; ok {
		slipCopy.ClaimedFrom = existing.ClaimedFrom
	}
	m.Slips[slip.CorrelationID] = slipCopy

	return nil
}

// Repave removes the superseded slip and stores newSlip in its place (children live on the
// Slip struct in the mock, so removing the slip removes everything). Modelling both halves is what makes this double faithful to
// slippy.SlipStore.Repave: a caller never observes one without the other, so on any error
// nothing here changes, and on success the superseded slip is gone AND the successor is
// present.
//
// The removal happens ONLY when the stored slip's status is no longer live — mirroring
// PostgresStore's ended-status guard. A live slip (Status.IsLive() true) is rejected with
// slippy.ErrSlipWentLive, left untouched, and its successor is not created, so a downstream
// consumer's went-live handling is exercisable against this mock exactly as it would be
// against Postgres (DEVOPS-231 review D1.2).
//
// parent is recorded in RepaveParents but otherwise unused, and there are no descendant
// links to repoint: the mock has no slip_ancestry-equivalent table (see
// RepaveSuccessorCalls's doc comment).
func (m *MockStore) Repave(
	ctx context.Context,
	oldCorrelationID string,
	newSlip *slippy.Slip,
	parent *slippy.AncestryEntry,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.RepaveCalls = append(m.RepaveCalls, oldCorrelationID)
	successorID := ""
	if newSlip != nil {
		successorID = newSlip.CorrelationID
	}
	m.RepaveSuccessorCalls = append(m.RepaveSuccessorCalls, successorID)
	m.RepaveParents = append(m.RepaveParents, parent)

	if m.RepaveError != nil {
		if newStatus, ok := m.RepaveWentLiveStatus[oldCorrelationID]; ok {
			delete(m.RepaveWentLiveStatus, oldCorrelationID)
			if slip, exists := m.Slips[oldCorrelationID]; exists {
				slip.Status = newStatus
			}
		}
		return m.RepaveError
	}

	if newSlip == nil {
		return fmt.Errorf("%w: Repave requires a successor slip", slippy.ErrInvalidConfiguration)
	}

	// Mirrors PostgresStore's self-repave rejection (slippy.SlipStore.Repave: "newSlip
	// .CorrelationID must differ from oldCorrelationID"). It is not decorative, for the same
	// reason the live guard below is not: without it this double deletes and re-inserts the
	// same map key, silently destroying an ended run's state history exactly as the real store
	// now refuses to — so a consumer's test would pass here and fail against Postgres.
	if oldCorrelationID == newSlip.CorrelationID {
		return fmt.Errorf("%w: Repave successor %s is the slip being repaved",
			slippy.ErrInvalidConfiguration, newSlip.CorrelationID)
	}

	removedOld := false
	if slip, ok := m.Slips[oldCorrelationID]; ok {
		// A claimed row is refused like a live one: a claimant's run is in flight whatever
		// the status says — the claim never writes status, so a claimed rerun of a failed
		// slip still reads failed (slipUnclaimedSQL, postgres_store_updates.go).
		if slip.Status.IsLive() || slip.ClaimedFrom != "" {
			return slippy.ErrSlipWentLive
		}
		removedOld = true
		delete(m.Slips, oldCorrelationID)
	}

	// A missing superseded row is not an error, matching PostgresStore: the successor is
	// still created, so a redelivery converges rather than failing forever.
	//
	// The successor discards a caller-supplied claim and then restores any claim already
	// recorded under the successor's OWN id — the identical two steps Create takes above, and
	// for the identical reason, because this is the identical write: PostgresStore.Repave
	// inserts through createTx -> buildCreateQuery -> slipColumns(), whose INSERT list and
	// conflict arm both omit claimed_from. So the replacement row's claimed_from is NULL on a
	// fresh insert, and an existing row under that id KEEPS its claim through the ON CONFLICT
	// arm (PR #87, jhicks review; the restore half added in the PR #87 review).
	//
	// Modelling only the discard made this method contradict Create about one write path. It is
	// unreachable in production — Repave rejects oldCorrelationID == newSlip.CorrelationID, and
	// correlation IDs are minted per delivery, so the conflict arm never fires — but a consumer
	// test that seeds a slip and repaves onto that same id got opposite answers from the two
	// methods, with no way to tell which one Postgres would have given.
	stored := DeepCopySlip(newSlip)
	stored.ClaimedFrom = ""
	if existing, ok := m.Slips[newSlip.CorrelationID]; ok {
		stored.ClaimedFrom = existing.ClaimedFrom
	}
	if removedOld {
		// Mirrors the state-history entry PostgresStore.Repave appends to the successor so
		// the replacement is visible on the row afterwards — without it the successor carries
		// no evidence a prior run existed for this commit, since the old row is gone. Gated
		// on removedOld exactly as the real store gates it, so a repave that replaced nothing
		// does not record a predecessor it never had.
		//
		// The message names the predecessor's correlation ID, which is the property consumers
		// can assert on. The precise rendering is the store's business — PostgresStore
		// abbreviates the commit SHA using an unexported helper — so this deliberately does
		// not try to be byte-identical.
		stored.StateHistory = append(stored.StateHistory, slippy.StateHistoryEntry{
			Step:      slippy.PushParsedStep,
			Status:    slippy.StepStatusRunning,
			Timestamp: time.Now(),
			Actor:     slippy.LibraryActor,
			Message: fmt.Sprintf("repaved %s for commit %s", oldCorrelationID,
				newSlip.CommitSHA),
		})
	}
	// No commit index to reconcile: the commit lookups derive their answer from the stored rows
	// (rowsForCommit), so removing the superseded row and adding the successor is the whole
	// update. The shape this used to get wrong — store holds ended slip A and live slip C for
	// one commit, Repave(A, B) re-points the commit at B and hides the still-live C — is now
	// unrepresentable rather than guarded.
	m.Slips[newSlip.CorrelationID] = stored
	return nil
}

// Load retrieves a slip by its correlation ID.
func (m *MockStore) Load(ctx context.Context, correlationID string) (*slippy.Slip, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.LoadCalls = append(m.LoadCalls, correlationID)

	if m.LoadError != nil {
		return nil, m.LoadError
	}

	if err, ok := m.LoadErrorFor[correlationID]; ok {
		return nil, err
	}

	slip, ok := m.Slips[correlationID]
	if !ok {
		return nil, slippy.ErrSlipNotFound
	}

	return DeepCopySlip(slip), nil
}

// LoadByCommit retrieves a slip by repository and commit SHA.
func (m *MockStore) LoadByCommit(ctx context.Context, repository, commitSHA string) (*slippy.Slip, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.LoadByCommitCalls = append(m.LoadByCommitCalls, LoadByCommitCall{
		Repository: repository,
		CommitSHA:  commitSHA,
	})

	if m.LoadByCommitError != nil {
		return nil, m.LoadByCommitError
	}

	rows := m.rowsForCommit(repository, commitSHA)
	if len(rows) == 0 {
		return nil, slippy.ErrSlipNotFound
	}
	loadOrder(rows)

	return DeepCopySlip(rows[0]), nil
}

// LoadLiveByCommit retrieves the most recent live slip by repository and commit SHA,
// excluding superseded terminal statuses (abandoned, promoted, compensated).
func (m *MockStore) LoadLiveByCommit(ctx context.Context, repository, commitSHA string) (*slippy.Slip, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.LoadLiveByCommitCalls = append(m.LoadLiveByCommitCalls, LoadByCommitCall{
		Repository: repository,
		CommitSHA:  commitSHA,
	})

	if m.LoadLiveByCommitError != nil {
		return nil, m.LoadLiveByCommitError
	}

	// Mirror prod semantics: exclude terminal-superseded statuses. The filter is applied per
	// row, matching the store's WHERE clause — filtering the single already-chosen row instead
	// reports ErrSlipNotFound for a commit that still has a live slip, whenever an excluded
	// duplicate happens to sort first.
	rows := m.rowsForCommit(repository, commitSHA)
	loadOrder(rows)
	for _, slip := range rows {
		if supersededTerminal(slip.Status) {
			continue
		}
		return DeepCopySlip(slip), nil
	}

	return nil, slippy.ErrSlipNotFound
}

// FindByCommits finds a slip matching any commit in the ordered list.
func (m *MockStore) FindByCommits(
	ctx context.Context,
	repository string,
	commits []string,
) (*slippy.Slip, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.FindByCommitsCalls = append(m.FindByCommitsCalls, FindByCommitsCall{
		Repository: repository,
		Commits:    commits,
	})

	if m.FindByCommitsError != nil {
		return nil, "", m.FindByCommitsError
	}

	// Find the first matching commit in order. The store's query carries
	// `AND s.status NOT IN ('abandoned','promoted','compensated')`
	// (postgres_store_reads.go) — a filter LoadByCommit does NOT have — so a commit whose only
	// rows are superseded-terminal is skipped entirely and the search moves to the next commit,
	// exactly as `LIMIT 1` over the filtered join does.
	for _, commit := range commits {
		rows := m.rowsForCommit(repository, commit)
		findOrder(rows)
		for _, slip := range rows {
			if supersededTerminal(slip.Status) {
				continue
			}
			return DeepCopySlip(slip), commit, nil
		}
	}

	return nil, "", slippy.ErrSlipNotFound
}

// FindAllByCommits finds all slips matching commits in the given list.
// Returns slips in the order they appear in the commit list.
func (m *MockStore) FindAllByCommits(
	ctx context.Context,
	repository string,
	commits []string,
) ([]slippy.SlipWithCommit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.FindAllByCommitsCalls = append(m.FindAllByCommitsCalls, FindAllByCommitsCall{
		Repository: repository,
		Commits:    commits,
	})

	if m.FindAllByCommitsError != nil {
		return nil, m.FindAllByCommitsError
	}

	// EVERY matching row, not one per commit: the store's query has no LIMIT and appends each
	// row it scans (postgres_store_reads.go). Multiplicity is this method's whole contract —
	// interfaces.go documents it as "finds all slips matching any commit in the ordered list" —
	// so collapsing duplicates to one row per commit is the one shape a double must not take.
	//
	// No status filter either, unlike FindByCommits: that query's WHERE clause has one and this
	// one does not.
	var results []slippy.SlipWithCommit
	for _, commit := range commits {
		rows := m.rowsForCommit(repository, commit)
		findOrder(rows)
		for _, slip := range rows {
			results = append(results, slippy.SlipWithCommit{
				Slip:          DeepCopySlip(slip),
				MatchedCommit: commit,
			})
		}
	}

	return results, nil
}

// Update persists changes to an existing slip.
func (m *MockStore) Update(ctx context.Context, slip *slippy.Slip) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.UpdateCalls = append(m.UpdateCalls, UpdateCall{Slip: slip})

	if m.UpdateError != nil {
		return m.UpdateError
	}

	existing, ok := m.Slips[slip.CorrelationID]
	if !ok {
		return slippy.ErrSlipNotFound
	}

	// claimed_from is SELECT-only in PostgresStore: the full-row Update never writes it,
	// whatever status the snapshot carries, so a caller's stale snapshot can clear neither a
	// claim it never loaded nor one taken after its read. Only UpdateSlipStatus on a terminal
	// status ends a claim.
	stored := DeepCopySlip(slip)
	stored.ClaimedFrom = existing.ClaimedFrom
	m.Slips[slip.CorrelationID] = stored

	return nil
}

// UpdateStep updates a specific step's status.
func (m *MockStore) UpdateStep(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status slippy.StepStatus,
) error {
	// Refused before the call is recorded, matching PostgresStore.updateStepTx, which refuses
	// before its transaction opens: a consumer asserting on the rejection must see the same
	// answer and the same (empty) call log from the double as from the real store.
	if err := slippy.GuardReservedStepWrite(stepName, nil); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.UpdateStepCalls = append(m.UpdateStepCalls, UpdateStepCall{
		CorrelationID: correlationID,
		StepName:      stepName,
		ComponentName: componentName,
		Status:        status,
	})

	if m.UpdateStepError != nil {
		return m.UpdateStepError
	}

	if err, ok := m.UpdateStepErrorFor[correlationID]; ok {
		return err
	}

	slip, ok := m.Slips[correlationID]
	if !ok {
		return slippy.ErrSlipNotFound
	}

	if slip.Steps == nil {
		slip.Steps = make(map[string]slippy.Step)
	}

	step := slip.Steps[stepName]
	step.Status = status
	slip.Steps[stepName] = step

	return nil
}

// UpdateComponentStatus updates a component's build or test status.
func (m *MockStore) UpdateComponentStatus(
	ctx context.Context,
	correlationID, componentName, stepType string,
	status slippy.StepStatus,
) error {
	// stepType is the step name here, so it enters the same namespace UpdateStep guards.
	if err := slippy.GuardReservedStepWrite(stepType, nil); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.UpdateComponentCalls = append(m.UpdateComponentCalls, UpdateComponentCall{
		CorrelationID: correlationID,
		ComponentName: componentName,
		StepType:      stepType,
		Status:        status,
	})

	if m.UpdateComponentError != nil {
		return m.UpdateComponentError
	}

	if err, ok := m.UpdateComponentErrorFor[correlationID]; ok {
		return err
	}

	slip, ok := m.Slips[correlationID]
	if !ok {
		return slippy.ErrSlipNotFound
	}

	// Update the component status in the Aggregates
	// stepType is the component type (e.g., "build", "unit_test")
	columnName := pluralize(stepType)
	if componentData, ok := slip.Aggregates[columnName]; ok {
		for i := range componentData {
			if componentData[i].Component == componentName {
				componentData[i].Status = status
				return nil
			}
		}
	}

	return nil
}

// AppendHistory adds a state history entry to the slip.
func (m *MockStore) AppendHistory(ctx context.Context, correlationID string, entry slippy.StateHistoryEntry) error {
	if err := slippy.GuardReservedStepWrite("", &entry); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.AppendHistoryCalls = append(m.AppendHistoryCalls, AppendHistoryCall{
		CorrelationID: correlationID,
		Entry:         entry,
	})

	if m.AppendHistoryError != nil {
		return m.AppendHistoryError
	}

	if err, ok := m.AppendHistoryErrorFor[correlationID]; ok {
		return err
	}

	slip, ok := m.Slips[correlationID]
	if !ok {
		return slippy.ErrSlipNotFound
	}

	slip.StateHistory = append(slip.StateHistory, entry)

	return nil
}

// UpdateSlipStatus atomically updates the slip's status field.
func (m *MockStore) UpdateSlipStatus(ctx context.Context, correlationID string, status slippy.SlipStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.UpdateSlipStatusCalls = append(m.UpdateSlipStatusCalls, UpdateSlipStatusCall{
		CorrelationID: correlationID,
		Status:        status,
	})

	if m.UpdateSlipStatusError != nil {
		return m.UpdateSlipStatusError
	}

	slip, ok := m.Slips[correlationID]
	if !ok {
		return slippy.ErrSlipNotFound
	}

	slip.Status = status
	if status.IsTerminal() {
		// A terminal status ends the run, so it ends the claim — and ending a claim has to be
		// VISIBLE, because the readers that derive claim identity read the markers, not the
		// column (see PostgresStore.UpdateSlipStatus). Clearing the column without appending
		// the release would leave this double reporting claimed_from empty with slip_claimed
		// still newest: the exact shape GuardReservedStepWrite refuses a caller for forging,
		// and a divergence from Postgres that a consumer test could not see (PR #87 review).
		if slip.ClaimedFrom != "" {
			slip.StateHistory = append(slip.StateHistory,
				slippy.ReleaseMarker(status, slippy.LibraryActor, "claim ended by a terminal status write"))
		}
		slip.ClaimedFrom = ""
	}
	return nil
}

// ClaimSlip mirrors PostgresStore.ClaimSlip through the shared slippy.DecideClaim: a
// compare-and-set on the CURRENT status whether or not a claim is held, idempotent
// (ClaimOutcome{Claimed: false}, nothing written) once one is, never writing status. The
// in-flight evidence is read from the same stored slip the decision is made on, as the store
// reads both from one locked row.
func (m *MockStore) ClaimSlip(
	ctx context.Context, correlationID string, expected []slippy.SlipStatus, claimedBy, reason string,
) (slippy.ClaimOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Copied, not aliased: storing the caller's slice would keep its backing array, so a
	// caller that reuses one buffer across claims would see every recorded call mutate
	// together and a test would assert against whatever the last call left behind.
	m.ClaimSlipCalls = append(m.ClaimSlipCalls, ClaimSlipCall{
		CorrelationID: correlationID,
		Expected:      append([]slippy.SlipStatus(nil), expected...),
		ClaimedBy:     claimedBy,
		Reason:        reason,
	})
	if m.ClaimSlipError != nil {
		return slippy.ClaimOutcome{}, m.ClaimSlipError
	}
	slip, ok := m.Slips[correlationID]
	if !ok {
		return slippy.ClaimOutcome{}, slippy.ErrSlipNotFound
	}
	inFlight := slippy.RunInFlight(slip)
	prior, write, err := slippy.DecideClaim(slip.Status, slip.ClaimedFrom, inFlight, expected)
	if err != nil {
		return slippy.ClaimOutcome{}, fmt.Errorf("claim %s: %w", correlationID, err)
	}
	if write {
		slip.StateHistory = append(slip.StateHistory, slippy.ClaimMarker(prior, claimedBy, reason))
		slip.ClaimedFrom = prior
	}
	return slippy.ClaimOutcome{Claimed: write, Prior: prior, InFlight: inFlight}, nil
}

// ReleaseClaim mirrors PostgresStore.ReleaseClaim through the shared slippy.DecideRelease:
// the claim is KEPT (Released=false, nothing written) while any step or component is in
// flight, cleared otherwise, and status is never written either way.
func (m *MockStore) ReleaseClaim(
	ctx context.Context, correlationID, releasedBy, reason string,
) (slippy.ReleaseOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ReleaseClaimCalls = append(m.ReleaseClaimCalls, ReleaseClaimCall{
		CorrelationID: correlationID, ReleasedBy: releasedBy, Reason: reason,
	})
	if m.ReleaseClaimError != nil {
		return slippy.ReleaseOutcome{}, m.ReleaseClaimError
	}
	slip, ok := m.Slips[correlationID]
	if !ok {
		return slippy.ReleaseOutcome{}, slippy.ErrSlipNotFound
	}
	release, err := slippy.DecideRelease(slip)
	if err != nil {
		return slippy.ReleaseOutcome{}, fmt.Errorf("release %s: %w", correlationID, err)
	}
	if !release {
		return slippy.ReleaseOutcome{Released: false, Status: slip.Status}, nil
	}
	slip.ClaimedFrom = ""
	slip.StateHistory = append(slip.StateHistory, slippy.ReleaseMarker(slip.Status, releasedBy, reason))
	return slippy.ReleaseOutcome{Released: true, Status: slip.Status}, nil
}

// ResetSlipInPlace mirrors PostgresStore.ResetSlipInPlace through the shared
// slippy.DecideReset, so this double and the real store cannot disagree about when an
// in-delivery retry's reset is refused.
//
// What it models, and why each half matters to a consumer test:
//
//   - The decision is made from the STORED row at call time, never from what the caller
//     passed or last read. That is the whole behaviour under test in the race this method
//     closes (DEVOPS-367): a consumer can claim the slip between its LoadByCommit and its
//     push's write and see the reset refused, exactly as Postgres refuses it under the row
//     lock.
//   - A refusal writes NOTHING and returns slippy.ErrSlipClaimed wrapped, so a
//     consumer asserting with errors.Is sees the same sentinel production raises.
//   - An allowed reset is reached ONLY for an unclaimed row, since the decision refuses on any
//     claim, so there is no claim to keep across it and no marker to carry. The invariant a
//     marker-reading consumer depends on — a set claimed_from always has a slip_claimed
//     marker — holds here by construction: nothing overwrites the history of a claimed row.
//   - An absent row is upserted rather than refused, matching the store's behaviour when a
//     concurrent repave removed the target between the caller's read and this write.
func (m *MockStore) ResetSlipInPlace(ctx context.Context, slip *slippy.Slip) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if slip == nil {
		return fmt.Errorf("%w: ResetSlipInPlace requires a successor slip", slippy.ErrInvalidConfiguration)
	}
	m.ResetInPlaceCalls = append(m.ResetInPlaceCalls, slip.CorrelationID)
	if m.ResetInPlaceError != nil {
		return m.ResetInPlaceError
	}

	slipCopy := DeepCopySlip(slip)
	existing, ok := m.Slips[slip.CorrelationID]
	if !ok {
		// No row to decide about: the upsert degenerates to a fresh insert, which is always
		// unclaimed — claimed_from is absent from the INSERT column list, not merely from the
		// conflict arm's SET list (see Create).
		slipCopy.ClaimedFrom = ""
		m.Slips[slip.CorrelationID] = slipCopy
		return nil
	}

	// Refused on ANY claim, matching PostgresStore: the reset rewrites every step, aggregate
	// and the whole history, and a claimed row belongs to a run that was already dispatched
	// even when it has not reported a step yet.
	if err := slippy.DecideReset(existing.ClaimedFrom, slippy.RunInFlight(existing)); err != nil {
		return fmt.Errorf("reset %s in place: %w", slip.CorrelationID, err)
	}
	// Unclaimed by the decision above, so there is no claim to carry and none to restore.
	slipCopy.ClaimedFrom = ""
	m.Slips[slip.CorrelationID] = slipCopy
	return nil
}

// ProbeSchema mirrors PostgresStore.ProbeSchema's readiness gate. The double has no schema,
// so it reports ready unless ProbeSchemaError is set.
func (m *MockStore) ProbeSchema(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ProbeSchemaError
}

// UpdateStepWithHistory updates a step's status AND appends a history entry atomically.
// This is the combined operation that prevents race conditions.
func (m *MockStore) UpdateStepWithHistory(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status slippy.StepStatus,
	entry slippy.StateHistoryEntry,
) error {
	if err := slippy.GuardReservedStepWrite(stepName, &entry); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Track both calls
	m.UpdateStepCalls = append(m.UpdateStepCalls, UpdateStepCall{
		CorrelationID: correlationID,
		StepName:      stepName,
		ComponentName: componentName,
		Status:        status,
	})
	m.AppendHistoryCalls = append(m.AppendHistoryCalls, AppendHistoryCall{
		CorrelationID: correlationID,
		Entry:         entry,
	})

	if m.UpdateStepError != nil {
		return m.UpdateStepError
	}
	if err, ok := m.UpdateStepErrorFor[correlationID]; ok {
		return err
	}

	slip, ok := m.Slips[correlationID]
	if !ok {
		return slippy.ErrSlipNotFound
	}

	// Update step
	if slip.Steps == nil {
		slip.Steps = make(map[string]slippy.Step)
	}
	step := slip.Steps[stepName]
	step.Status = status
	slip.Steps[stepName] = step

	// Append history
	slip.StateHistory = append(slip.StateHistory, entry)

	return nil
}

// Close releases any resources held by the store.
func (m *MockStore) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CloseCalls++

	if m.CloseError != nil {
		return m.CloseError
	}

	return nil
}

// SetComponentImageTag records the container image tag for a component in the in-memory slip.
func (m *MockStore) SetComponentImageTag(
	_ context.Context,
	correlationID, stepName, componentName, imageTag string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.SetImageTagCalls = append(m.SetImageTagCalls, SetImageTagCall{
		CorrelationID: correlationID,
		StepName:      stepName,
		ComponentName: componentName,
		ImageTag:      imageTag,
	})

	if m.SetImageTagError != nil {
		return m.SetImageTagError
	}

	slip, ok := m.Slips[correlationID]
	if !ok {
		return slippy.ErrSlipNotFound
	}

	// Target the aggregate column derived from the step name first.
	// If that column is not present, or the component is stored in other
	// aggregate columns, fall back to scanning all aggregates and update
	// every matching entry for this component.
	columnName := pluralize(stepName)

	found := false
	if componentData, ok := slip.Aggregates[columnName]; ok {
		for i := range componentData {
			if componentData[i].Component == componentName {
				slip.Aggregates[columnName][i].ImageTag = imageTag
				found = true
			}
		}
	}

	for colName, componentData := range slip.Aggregates {
		if colName == columnName {
			continue
		}
		for i := range componentData {
			if componentData[i].Component == componentName {
				slip.Aggregates[colName][i].ImageTag = imageTag
				found = true
			}
		}
	}

	if found {
		return nil
	}
	return fmt.Errorf("component %s not found in any aggregate for step %s", componentName, stepName)
}

// Ping verifies the database connection is alive (mock always returns PingError).
func (m *MockStore) Ping(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.PingCalls++
	return m.PingError
}

// Reset clears all stored data and call tracking.
func (m *MockStore) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Slips = make(map[string]*slippy.Slip)
	m.CreateCalls = nil
	m.LoadCalls = nil
	m.LoadByCommitCalls = nil
	m.LoadLiveByCommitCalls = nil
	m.FindByCommitsCalls = nil
	m.FindAllByCommitsCalls = nil
	m.UpdateCalls = nil
	m.UpdateStepCalls = nil
	m.UpdateComponentCalls = nil
	m.AppendHistoryCalls = nil
	m.SetImageTagCalls = nil
	m.UpdateSlipStatusCalls = nil
	m.ResetInPlaceCalls = nil
	// Claim state. Omitted when the claim recorders were added (PR #87), the third instance of
	// the identical class documented for Repave below — and the one most likely to be hit,
	// since claim assertions are what a consumer writes against this double now that claims are
	// the shipped feature. TestReset_ClearsEveryCallRecorder walks these fields by reflection so
	// the fourth instance fails a test instead of reaching a consumer.
	m.ClaimSlipCalls = nil
	m.ReleaseClaimCalls = nil
	// Repave state. Omitted before, which broke this method's own "clears all stored data
	// and call tracking" contract: a consumer that Resets between scenarios kept stale
	// repave records, so len(RepaveCalls) assertions passed or failed on the previous
	// scenario's calls. This is published API, so the bug was theirs to hit, not ours.
	m.RepaveCalls = nil
	m.RepaveSuccessorCalls = nil
	m.RepaveParents = nil
	// RepaveWentLiveStatus is a one-shot hook: an entry that never fired would otherwise
	// survive Reset and mutate a later scenario's slip. RepaveError goes with it — the hook's
	// own doc says the went-live mutation fires only while RepaveError is set, so clearing one
	// and not the other leaves the pair split: error armed, hook disarmed. The next scenario
	// then gets a stale error with no mutation, which is neither scenario's configured
	// behaviour and reads as deliberate.
	//
	// RepaveError is the ONLY injected error Reset clears, and only because of that coupling.
	// Every other error field (CreateError, LoadError, PingError, ...) and every ...ErrorFor map
	// deliberately SURVIVES Reset, so a fixture that arms one in a setup helper keeps it across
	// sub-tests. Clearing them all would silently disarm those fixtures — a test would stop
	// erroring and pass for the wrong reason — so the asymmetry is intentional, not an oversight.
	m.RepaveWentLiveStatus = make(map[string]slippy.SlipStatus)
	m.RepaveError = nil
	m.CloseCalls = 0
	m.PingCalls = 0
}

// AddSlip adds a slip directly to the store for testing.
// This bypasses the Create method and doesn't record a call.
func (m *MockStore) AddSlip(slip *slippy.Slip) {
	m.mu.Lock()
	defer m.mu.Unlock()

	slipCopy := DeepCopySlip(slip)
	m.Slips[slip.CorrelationID] = slipCopy
}

// DeepCopySlip creates a deep copy of a Slip to prevent test interference.
func DeepCopySlip(slip *slippy.Slip) *slippy.Slip {
	if slip == nil {
		return nil
	}

	cpy := &slippy.Slip{
		CorrelationID: slip.CorrelationID,
		Repository:    slip.Repository,
		Branch:        slip.Branch,
		CommitSHA:     slip.CommitSHA,
		CreatedAt:     slip.CreatedAt,
		UpdatedAt:     slip.UpdatedAt,
		Status:        slip.Status,
		ClaimedFrom:   slip.ClaimedFrom,
	}

	// Deep copy steps map
	if slip.Steps != nil {
		cpy.Steps = make(map[string]slippy.Step, len(slip.Steps))
		for k, v := range slip.Steps {
			cpy.Steps[k] = v
		}
	}

	// Deep copy aggregates
	if slip.Aggregates != nil {
		cpy.Aggregates = make(map[string][]slippy.ComponentStepData)
		for k, v := range slip.Aggregates {
			componentData := make([]slippy.ComponentStepData, len(v))
			copy(componentData, v)
			cpy.Aggregates[k] = componentData
		}
	}

	// Deep copy state history
	if slip.StateHistory != nil {
		cpy.StateHistory = make([]slippy.StateHistoryEntry, len(slip.StateHistory))
		copy(cpy.StateHistory, slip.StateHistory)
	}

	return cpy
}

// InsertAncestryLink writes a direct-parent link (no-op in mock).
func (m *MockStore) InsertAncestryLink(ctx context.Context, slip *slippy.Slip, parent slippy.AncestryEntry) error {
	return nil
}

// ResolveAncestry walks parent links to reconstruct ancestry (returns empty in mock).
func (m *MockStore) ResolveAncestry(
	ctx context.Context,
	repository, branch, correlationID string,
	maxDepth int,
) ([]slippy.AncestryEntry, error) {
	return []slippy.AncestryEntry{}, nil
}

// rowsForCommit returns every stored slip for one (repository, commit SHA), UNORDERED. Callers
// apply loadOrder or findOrder depending on which store query they mirror — the two differ, and
// picking the wrong one is what made this double disagree with the store on FindByCommits.
//
// Phase A can hold more than one routing_slips row per commit, so this double stores rows and
// derives each answer on read rather than keeping a "current slip for this commit" index. An
// index can only name one row, which is what previously made the ended-shadows-live hazard
// unrepresentable here: seed a live row A and a newer ended row B for one commit and the index
// pointed only at B, so the double reported B — or, for LoadLiveByCommit with B abandoned,
// reported nothing — where Postgres returns A. A double that answers confidently and wrongly is
// worse for a consumer than one that cannot answer, so the index is gone.
func (m *MockStore) rowsForCommit(repository, commitSHA string) []*slippy.Slip {
	want := commitKey(repository, commitSHA)
	rows := make([]*slippy.Slip, 0, 1)
	for _, slip := range m.Slips {
		if commitKey(slip.Repository, slip.CommitSHA) == want {
			rows = append(rows, slip)
		}
	}
	return rows
}

// Ensure MockStore implements slippy.SlipStore at compile time.
var _ slippy.SlipStore = (*MockStore)(nil)
