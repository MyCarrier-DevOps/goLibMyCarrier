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
	"strings"
	"sync"

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

// commitIndexKey builds the CommitIndex lookup key for a repository/commit pair. The
// repository is lowercased to mirror PostgresStore's case-insensitive
// `lower(repository) = lower($1)` comparison (postgres_store.go); the commit SHA is
// compared as-is, matching production. Every CommitIndex read or write in this file
// must go through this helper so reads and writes stay in agreement (DEVOPS-231
// review D1.1) - a partial fix (some sites lowercased, others not) is worse than none.
func commitIndexKey(repository, commitSHA string) string {
	return strings.ToLower(repository) + ":" + commitSHA
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

	// CommitIndex maps "repo:commit" -> correlation_id for LoadByCommit
	CommitIndex map[string]string

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
		CommitIndex:             make(map[string]string),
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

	// Index by commit for LoadByCommit
	key := commitIndexKey(slip.Repository, slip.CommitSHA)
	m.CommitIndex[key] = slip.CorrelationID

	return nil
}

// Repave removes the superseded slip and its commit index entry (children live on the Slip
// struct in the mock, so removing the slip removes everything) and stores newSlip in its
// place. Modelling both halves is what makes this double faithful to
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

	if slip, ok := m.Slips[oldCorrelationID]; ok {
		if slip.Status.IsLive() {
			return slippy.ErrSlipWentLive
		}
		// Only unmap the commit index entry if it still points at THIS slip. Create
		// permits duplicate (repo, sha) rows and re-points the index at the newest one, so
		// an older row's removal must not clear an index entry that has since moved on to
		// a different, still-live row (DEVOPS-231 review D1.1).
		key := commitIndexKey(slip.Repository, slip.CommitSHA)
		if id, idOK := m.CommitIndex[key]; idOK && id == oldCorrelationID {
			delete(m.CommitIndex, key)
		}
		delete(m.Slips, oldCorrelationID)
	}

	// A missing superseded row is not an error, matching PostgresStore: the successor is
	// still created, so a redelivery converges rather than failing forever.
	m.Slips[newSlip.CorrelationID] = DeepCopySlip(newSlip)
	m.CommitIndex[commitIndexKey(newSlip.Repository, newSlip.CommitSHA)] = newSlip.CorrelationID
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

	key := commitIndexKey(repository, commitSHA)
	correlationID, ok := m.CommitIndex[key]
	if !ok {
		return nil, slippy.ErrSlipNotFound
	}

	slip, ok := m.Slips[correlationID]
	if !ok {
		return nil, slippy.ErrSlipNotFound
	}

	return DeepCopySlip(slip), nil
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

	key := commitIndexKey(repository, commitSHA)
	correlationID, ok := m.CommitIndex[key]
	if !ok {
		return nil, slippy.ErrSlipNotFound
	}

	slip, ok := m.Slips[correlationID]
	if !ok {
		return nil, slippy.ErrSlipNotFound
	}

	// Mirror prod semantics: exclude terminal-superseded statuses.
	if slip.Status == slippy.SlipStatusAbandoned ||
		slip.Status == slippy.SlipStatusPromoted ||
		slip.Status == slippy.SlipStatusCompensated {
		return nil, slippy.ErrSlipNotFound
	}

	return DeepCopySlip(slip), nil
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

	// Find the first matching commit in order
	for _, commit := range commits {
		key := commitIndexKey(repository, commit)
		if correlationID, ok := m.CommitIndex[key]; ok {
			if slip, ok := m.Slips[correlationID]; ok {
				return DeepCopySlip(slip), commit, nil
			}
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

	// Find all matching commits in order
	var results []slippy.SlipWithCommit
	for _, commit := range commits {
		key := commitIndexKey(repository, commit)
		if correlationID, ok := m.CommitIndex[key]; ok {
			if slip, ok := m.Slips[correlationID]; ok {
				results = append(results, slippy.SlipWithCommit{
					Slip:          DeepCopySlip(slip),
					MatchedCommit: commit,
				})
			}
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
	m.CommitIndex = make(map[string]string)
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

	key := commitIndexKey(slip.Repository, slip.CommitSHA)
	m.CommitIndex[key] = slip.CorrelationID
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

// Ensure MockStore implements slippy.SlipStore at compile time.
var _ slippy.SlipStore = (*MockStore)(nil)
