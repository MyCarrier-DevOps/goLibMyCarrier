package slippy

import (
	"context"
	"strings"
	"sync"
)

// pluralizeMock converts a singular step name to its plural form for column naming.
func pluralizeMock(name string) string {
	if strings.HasSuffix(name, "s") {
		return name + "es"
	}
	return name + "s"
}

// MockStore is an in-memory implementation of SlipStore for testing.
// It provides configurable behavior and tracking of method calls.
type MockStore struct {
	mu sync.RWMutex

	// Storage maps correlation_id -> Slip
	Slips map[string]*Slip

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
	Slip *Slip
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
	Slip *Slip
}

// UpdateStepCall records an UpdateStep call.
type UpdateStepCall struct {
	CorrelationID string
	StepName      string
	ComponentName string
	Status        StepStatus
}

// UpdateComponentCall records an UpdateComponentStatus call.
type UpdateComponentCall struct {
	CorrelationID string
	ComponentName string
	StepType      string
	Status        StepStatus
}

// AppendHistoryCall records an AppendHistory call.
type AppendHistoryCall struct {
	CorrelationID string
	Entry         StateHistoryEntry
}

// NewMockStore creates a new MockStore with initialized maps.
func NewMockStore() *MockStore {
	return &MockStore{
		Slips:                   make(map[string]*Slip),
		CommitIndex:             make(map[string]string),
		CreateErrorFor:          make(map[string]error),
		LoadErrorFor:            make(map[string]error),
		UpdateStepErrorFor:      make(map[string]error),
		UpdateComponentErrorFor: make(map[string]error),
		AppendHistoryErrorFor:   make(map[string]error),
	}
}

// Create persists a new routing slip.
func (m *MockStore) Create(ctx context.Context, slip *Slip) error {
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
	slipCopy := deepCopySlip(slip)
	m.Slips[slip.CorrelationID] = slipCopy

	// Index by commit for LoadByCommit
	key := slip.Repository + ":" + slip.CommitSHA
	m.CommitIndex[key] = slip.CorrelationID

	return nil
}

// Load retrieves a slip by its correlation ID.
func (m *MockStore) Load(ctx context.Context, correlationID string) (*Slip, error) {
	m.mu.Lock()
	m.LoadCalls = append(m.LoadCalls, correlationID)
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.LoadError != nil {
		return nil, m.LoadError
	}
	if err, ok := m.LoadErrorFor[correlationID]; ok {
		return nil, err
	}

	slip, ok := m.Slips[correlationID]
	if !ok {
		return nil, ErrSlipNotFound
	}

	return deepCopySlip(slip), nil
}

// LoadByCommit retrieves a slip by repository and commit SHA.
func (m *MockStore) LoadByCommit(ctx context.Context, repository, commitSHA string) (*Slip, error) {
	m.mu.Lock()
	m.LoadByCommitCalls = append(m.LoadByCommitCalls, LoadByCommitCall{
		Repository: repository,
		CommitSHA:  commitSHA,
	})
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.LoadByCommitError != nil {
		return nil, m.LoadByCommitError
	}

	key := repository + ":" + commitSHA
	correlationID, ok := m.CommitIndex[key]
	if !ok {
		return nil, ErrSlipNotFound
	}

	slip, ok := m.Slips[correlationID]
	if !ok {
		return nil, ErrSlipNotFound
	}

	return deepCopySlip(slip), nil
}

// FindByCommits finds a slip matching any commit in the ordered list.
func (m *MockStore) FindByCommits(ctx context.Context, repository string, commits []string) (*Slip, string, error) {
	m.mu.Lock()
	m.FindByCommitsCalls = append(m.FindByCommitsCalls, FindByCommitsCall{
		Repository: repository,
		Commits:    commits,
	})
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.FindByCommitsError != nil {
		return nil, "", m.FindByCommitsError
	}

	// Find the first matching commit in order
	for _, commit := range commits {
		key := repository + ":" + commit
		if correlationID, ok := m.CommitIndex[key]; ok {
			if slip, ok := m.Slips[correlationID]; ok {
				return deepCopySlip(slip), commit, nil
			}
		}
	}

	return nil, "", ErrSlipNotFound
}

// FindAllByCommits finds all slips matching any commit in the ordered list.
func (m *MockStore) FindAllByCommits(
	ctx context.Context,
	repository string,
	commits []string,
) ([]SlipWithCommit, error) {
	m.mu.Lock()
	m.FindAllByCommitsCalls = append(m.FindAllByCommitsCalls, FindAllByCommitsCall{
		Repository: repository,
		Commits:    commits,
	})
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.FindAllByCommitsError != nil {
		return nil, m.FindAllByCommitsError
	}

	// Find all matching slips in commit order
	var results []SlipWithCommit
	for _, commit := range commits {
		key := repository + ":" + commit
		if correlationID, ok := m.CommitIndex[key]; ok {
			if slip, ok := m.Slips[correlationID]; ok {
				results = append(results, SlipWithCommit{
					Slip:          deepCopySlip(slip),
					MatchedCommit: commit,
				})
			}
		}
	}

	return results, nil
}

// Update persists changes to an existing slip.
func (m *MockStore) Update(ctx context.Context, slip *Slip) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.UpdateCalls = append(m.UpdateCalls, UpdateCall{Slip: slip})

	if m.UpdateError != nil {
		return m.UpdateError
	}

	if _, ok := m.Slips[slip.CorrelationID]; !ok {
		return ErrSlipNotFound
	}

	m.Slips[slip.CorrelationID] = deepCopySlip(slip)
	return nil
}

// UpdateStep updates a specific step's status.
func (m *MockStore) UpdateStep(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status StepStatus,
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
		return ErrSlipNotFound
	}

	if slip.Steps == nil {
		slip.Steps = make(map[string]Step)
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
	status StepStatus,
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
		return ErrSlipNotFound
	}

	// Update the component status in Aggregates
	columnName := pluralizeMock(stepType)
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
func (m *MockStore) AppendHistory(ctx context.Context, correlationID string, entry StateHistoryEntry) error {
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
		return ErrSlipNotFound
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

	m.Slips = make(map[string]*Slip)
	m.CommitIndex = make(map[string]string)
	m.CreateCalls = nil
	m.LoadCalls = nil
	m.LoadByCommitCalls = nil
	m.FindByCommitsCalls = nil
	m.UpdateCalls = nil
	m.UpdateStepCalls = nil
	m.UpdateComponentCalls = nil
	m.AppendHistoryCalls = nil
	m.CloseCalls = 0
}

// AddSlip adds a slip directly to the store for testing.
func (m *MockStore) AddSlip(slip *Slip) {
	m.mu.Lock()
	defer m.mu.Unlock()

	slipCopy := deepCopySlip(slip)
	m.Slips[slip.CorrelationID] = slipCopy

	key := slip.Repository + ":" + slip.CommitSHA
	m.CommitIndex[key] = slip.CorrelationID
}

// deepCopySlip creates a deep copy of a Slip to prevent test interference.
func deepCopySlip(slip *Slip) *Slip {
	if slip == nil {
		return nil
	}

	cpy := &Slip{
		CorrelationID: slip.CorrelationID,
		Repository:    slip.Repository,
		Branch:        slip.Branch,
		CommitSHA:     slip.CommitSHA,
		CreatedAt:     slip.CreatedAt,
		UpdatedAt:     slip.UpdatedAt,
		Status:        slip.Status,
		PromotedTo:    slip.PromotedTo,
	}

	// Deep copy steps map
	if slip.Steps != nil {
		cpy.Steps = make(map[string]Step, len(slip.Steps))
		for k, v := range slip.Steps {
			cpy.Steps[k] = v
		}
	}

	// Deep copy aggregates
	if slip.Aggregates != nil {
		cpy.Aggregates = make(map[string][]ComponentStepData)
		for k, v := range slip.Aggregates {
			componentData := make([]ComponentStepData, len(v))
			copy(componentData, v)
			cpy.Aggregates[k] = componentData
		}
	}

	// Deep copy state history
	if slip.StateHistory != nil {
		cpy.StateHistory = make([]StateHistoryEntry, len(slip.StateHistory))
		copy(cpy.StateHistory, slip.StateHistory)
	}

	// Deep copy ancestry chain
	if slip.Ancestry != nil {
		cpy.Ancestry = make([]AncestryEntry, len(slip.Ancestry))
		copy(cpy.Ancestry, slip.Ancestry)
	}

	return cpy
}

// Ensure MockStore implements SlipStore.
var _ SlipStore = (*MockStore)(nil)
