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

	// Deep copy the slip to avoid mutations
	slipCopy := DeepCopySlip(slip)
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
		if slip.Status.IsLive() {
			return slippy.ErrSlipWentLive
		}
		removedOld = true
		delete(m.Slips, oldCorrelationID)
	}

	// A missing superseded row is not an error, matching PostgresStore: the successor is
	// still created, so a redelivery converges rather than failing forever.
	stored := DeepCopySlip(newSlip)
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
			Step:      "push_parsed",
			Status:    slippy.StepStatusRunning,
			Timestamp: time.Now(),
			Actor:     "slippy-library",
			Message: fmt.Sprintf("repaved %s for commit %s", oldCorrelationID,
				newSlip.CommitSHA),
		})
	}
	// No commit index to reconcile: the commit lookups derive their answer from the stored rows
	// (slipsForCommit), so removing the superseded row and adding the successor is the whole
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

	if _, ok := m.Slips[slip.CorrelationID]; !ok {
		return slippy.ErrSlipNotFound
	}

	m.Slips[slip.CorrelationID] = DeepCopySlip(slip)

	return nil
}

// UpdateStep updates a specific step's status.
func (m *MockStore) UpdateStep(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status slippy.StepStatus,
) error {
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
	return nil
}

// UpdateStepWithHistory updates a step's status AND appends a history entry atomically.
// This is the combined operation that prevents race conditions.
func (m *MockStore) UpdateStepWithHistory(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status slippy.StepStatus,
	entry slippy.StateHistoryEntry,
) error {
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
