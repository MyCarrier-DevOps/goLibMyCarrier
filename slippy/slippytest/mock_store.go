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
	FindByCommitsCalls    []FindByCommitsCall
	FindAllByCommitsCalls []FindAllByCommitsCall
	UpdateCalls           []UpdateCall
	UpdateStepCalls       []UpdateStepCall
	UpdateComponentCalls  []UpdateComponentCall
	AppendHistoryCalls    []AppendHistoryCall
	CloseCalls            int

	// Error injection for testing error paths
	CreateError           error
	LoadError             error
	LoadByCommitError     error
	FindByCommitsError    error
	FindAllByCommitsError error
	UpdateError           error
	UpdateStepError       error
	UpdateComponentError  error
	AppendHistoryError    error
	CloseError            error

	// Conditional error injection (returns error only for specific IDs)
	CreateErrorFor          map[string]error
	LoadErrorFor            map[string]error
	UpdateStepErrorFor      map[string]error
	UpdateComponentErrorFor map[string]error
	AppendHistoryErrorFor   map[string]error
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
	key := slip.Repository + ":" + slip.CommitSHA
	m.CommitIndex[key] = slip.CorrelationID

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

	key := repository + ":" + commitSHA
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
		key := repository + ":" + commit
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
		key := repository + ":" + commit
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

// Reset clears all stored data and call tracking.
func (m *MockStore) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Slips = make(map[string]*slippy.Slip)
	m.CommitIndex = make(map[string]string)
	m.CreateCalls = nil
	m.LoadCalls = nil
	m.LoadByCommitCalls = nil
	m.FindByCommitsCalls = nil
	m.FindAllByCommitsCalls = nil
	m.UpdateCalls = nil
	m.UpdateStepCalls = nil
	m.UpdateComponentCalls = nil
	m.AppendHistoryCalls = nil
	m.CloseCalls = 0
}

// AddSlip adds a slip directly to the store for testing.
// This bypasses the Create method and doesn't record a call.
func (m *MockStore) AddSlip(slip *slippy.Slip) {
	m.mu.Lock()
	defer m.mu.Unlock()

	slipCopy := DeepCopySlip(slip)
	m.Slips[slip.CorrelationID] = slipCopy

	key := slip.Repository + ":" + slip.CommitSHA
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

// Ensure MockStore implements slippy.SlipStore at compile time.
var _ slippy.SlipStore = (*MockStore)(nil)
