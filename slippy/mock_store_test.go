package slippy

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// pluralizeMock converts a singular step name to its plural form for column naming.
func pluralizeMock(name string) string {
	if strings.HasSuffix(name, "s") {
		return name + "es"
	}
	return name + "s"
}

// commitIndexKey builds the CommitIndex lookup key for a repository/commit pair.
// The repository is lowercased to mirror PostgresStore's case-insensitive
// `lower(repository) = lower($1)` comparison (postgres_store.go); the commit SHA is
// compared as-is, matching production. Every CommitIndex read or write in this file
// must go through this helper so reads and writes stay in agreement (DEVOPS-231
// review D1.1) - a partial fix (some sites lowercased, others not) is worse than none.
func commitIndexKey(repository, commitSHA string) string {
	return strings.ToLower(repository) + ":" + commitSHA
}

// deepCopySlip creates a deep copy of a Slip to prevent shared map/slice references.
// This is used by MockStore to ensure test isolation.
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
		Sign:          slip.Sign,
		Version:       slip.Version,
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

	// Deep copy ancestry
	if slip.Ancestry != nil {
		cpy.Ancestry = make([]AncestryEntry, len(slip.Ancestry))
		copy(cpy.Ancestry, slip.Ancestry)
	}

	return cpy
}

// UpdateSlipStatusCall records an UpdateSlipStatus call.
type UpdateSlipStatusCall struct {
	CorrelationID string
	Status        SlipStatus
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
	// from — so tests assert on what the push path passed in.
	RepaveParents []*AncestryEntry
	// AncestryLinkCalls records InsertAncestryLink calls — the NON-transactional link
	// writes, i.e. the fresh-create path. A repave writes its successor's link inside
	// Repave instead, so a repave contributes to RepaveParents and NOT to this slice;
	// that difference is itself worth asserting.
	AncestryLinkCalls []AncestryLinkCall
	CloseCalls        int

	// UpdateStepWithHistoryCallCount counts calls to the atomic UpdateStepWithHistory
	// method specifically, separate from UpdateStepCalls/AppendHistoryCalls (which
	// UpdateStepWithHistory also appends to, alongside the standalone UpdateStep/
	// AppendHistory methods). This lets tests distinguish "one atomic call" from "two
	// separate calls that happen to add up to the same UpdateStepCalls/AppendHistoryCalls
	// counts" — see handlePushRetry's push_parsed reset (bd mycarrier-5dv5 F1).
	UpdateStepWithHistoryCallCount int

	// SwallowedHistoryErrors records AppendHistoryError/AppendHistoryErrorFor errors that
	// UpdateStepWithHistory swallowed (Warn + return nil) rather than propagated, mirroring
	// the real store's best-effort history write-back semantics (clickhouse_store.go's
	// pure-step branch, #75). Tests that need to observe a swallowed failure should assert
	// against this field instead of expecting UpdateStepWithHistory to return the error.
	SwallowedHistoryErrors []error

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
	AncestryLinkError     error
	CloseError            error

	// Conditional error injection (returns error only for specific IDs)
	CreateErrorFor          map[string]error
	LoadErrorFor            map[string]error
	UpdateStepErrorFor      map[string]error
	UpdateComponentErrorFor map[string]error
	AppendHistoryErrorFor   map[string]error

	// CreateErrorOnce injects an error for a single Create call keyed by correlation
	// ID, then clears itself: unlike CreateErrorFor (which fires on every attempt for
	// that id), this fires exactly once and is removed on first use. This is needed to
	// simulate "fail first, succeed on retry" races such as the ErrDuplicateSlip
	// backstop (DEVOPS-231), where CreateErrorFor's every-attempt semantics can't
	// express a create that succeeds on the backstop's retry.
	CreateErrorOnce map[string]error

	// SeedOnCreate injects a slip into the store the moment Create is invoked for the
	// matching correlation ID, then clears itself (one-shot, like CreateErrorOnce).
	// This simulates a concurrent winner's insert landing BETWEEN our own initial
	// LoadByCommit (which found nothing) and our own Create attempt - the exact
	// no-existing-row race the ErrDuplicateSlip backstop exists for (DEVOPS-231).
	// Pair it with CreateErrorOnce for the same correlation ID so the call both seeds
	// the winner row and then reports the duplicate-key conflict, letting the
	// backstop's subsequent LoadByCommit observe a row that was not there a moment
	// ago.
	SeedOnCreate map[string]*Slip

	// LoadByCommitNilOnCall forces the Nth LoadByCommit call (1-indexed; 0 disables the
	// hook) to return (nil, nil) regardless of whether it would have hit or missed. No
	// known real store returns (nil, nil) from LoadByCommit - a miss always carries
	// ErrSlipNotFound - but handleDuplicateSlipBackstop's
	// `loadErr != nil || conflicting == nil` guard is written to be safe against it anyway
	// (DEVOPS-231), and pinning that guard requires forcing the exact response.
	//
	// It is call-indexed rather than "next miss" because the caller's own initial lookup in
	// CreateSlipForPush is itself a LoadByCommit: a "next miss" hook would be consumed
	// there, never reaching the backstop's own lookup. Setting this to 2 targets the
	// backstop's call while leaving the initial one behaving normally.
	LoadByCommitNilOnCall int

	// RepaveWentLiveStatus simulates a slip transitioning to a live status in the
	// window between a caller's repave decision (an earlier LoadByCommit/Load saw it
	// ended) and the Repave call itself: when Repave is invoked for a correlation ID
	// present in this map WHILE RepaveError is set (e.g. to ErrSlipWentLive), the mock
	// mutates the stored row's status to the mapped value before returning the injected
	// error, then removes the entry (one-shot). This lets a subsequent Load (the caller's
	// reload-after-ErrSlipWentLive) observe the new state instead of the stale
	// decision-time snapshot - LoadByCommit and Load would otherwise always read the same
	// never-mutated row (DEVOPS-231 review finding B1).
	//
	// Note this is only needed to force the error path. Repave's own live guard already
	// returns ErrSlipWentLive for a stored slip whose status IsLive(), so a test that can
	// arrange the live status directly does not need this field at all.
	RepaveWentLiveStatus map[string]SlipStatus
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

// AncestryLinkCall records an InsertAncestryLink call.
type AncestryLinkCall struct {
	Slip   *Slip
	Parent AncestryEntry
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

// SetImageTagCall records a SetComponentImageTag call.
type SetImageTagCall struct {
	CorrelationID string
	StepName      string
	ComponentName string
	ImageTag      string
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
		CreateErrorOnce:         make(map[string]error),
		SeedOnCreate:            make(map[string]*Slip),
		RepaveWentLiveStatus:    make(map[string]SlipStatus),
	}
}

// Create persists a new routing slip.
func (m *MockStore) Create(ctx context.Context, slip *Slip) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CreateCalls = append(m.CreateCalls, CreateCall{Slip: slip})

	if seed, ok := m.SeedOnCreate[slip.CorrelationID]; ok {
		delete(m.SeedOnCreate, slip.CorrelationID)
		seedCopy := deepCopySlip(seed)
		m.Slips[seed.CorrelationID] = seedCopy
		m.CommitIndex[commitIndexKey(seed.Repository, seed.CommitSHA)] = seed.CorrelationID
	}

	if m.CreateError != nil {
		return m.CreateError
	}
	if err, ok := m.CreateErrorOnce[slip.CorrelationID]; ok {
		delete(m.CreateErrorOnce, slip.CorrelationID)
		return err
	}
	if err, ok := m.CreateErrorFor[slip.CorrelationID]; ok {
		return err
	}

	// Deep copy the slip to avoid mutations
	slipCopy := deepCopySlip(slip)
	m.Slips[slip.CorrelationID] = slipCopy

	// Index by commit for LoadByCommit
	key := commitIndexKey(slip.Repository, slip.CommitSHA)
	m.CommitIndex[key] = slip.CorrelationID

	return nil
}

// Repave removes the superseded slip and its commit index entry (children live on the
// Slip struct in the mock, so removing the slip removes everything) and stores newSlip in
// its place. Modelling both halves is what makes this double faithful: SlipStore.Repave
// guarantees a caller never observes one without the other, so on any error nothing here
// changes, and on success the superseded slip is gone AND the successor is present.
//
// The live guard is enforced here too, per SlipStore.Repave's contract. It is not
// decorative: without it this double would delete a live slip that the real store refuses
// to touch, letting push tests pass against behavior production rejects.
//
// The mock has no slip_ancestry-equivalent table, so it records rather than replicates the
// relational effects: parent is captured in RepaveParents for assertions, and there are no
// descendant links to repoint (see RepaveSuccessorCalls's doc comment).
func (m *MockStore) Repave(
	ctx context.Context,
	oldCorrelationID string,
	newSlip *Slip,
	parent *AncestryEntry,
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
		return fmt.Errorf("%w: Repave requires a successor slip", ErrInvalidConfiguration)
	}

	// Mirrors PostgresStore's self-repave rejection (SlipStore.Repave: "newSlip
	// .CorrelationID must differ from oldCorrelationID"). Same argument as the live guard
	// below: without it this double deletes and re-inserts the same map key, silently
	// destroying an ended run's history exactly as the real store now refuses to.
	if oldCorrelationID == newSlip.CorrelationID {
		return fmt.Errorf("%w: Repave successor %s is the slip being repaved",
			ErrInvalidConfiguration, newSlip.CorrelationID)
	}

	removedOld := false
	if slip, ok := m.Slips[oldCorrelationID]; ok {
		if slip.Status.IsLive() {
			// Went live between the caller's repave decision and this call: the
			// superseded run survives and the successor is NOT created.
			return ErrSlipWentLive
		}
		removedOld = true
		// Only unmap the commit index entry if it still points at THIS slip. The mock's
		// Create permits duplicate (repo, sha) rows and re-points the index at the
		// newest one, so an older row's removal must not clear an index entry that has
		// since moved on to a different, still-live row (DEVOPS-231 review D1.1).
		key := commitIndexKey(slip.Repository, slip.CommitSHA)
		if id, ok := m.CommitIndex[key]; ok && id == oldCorrelationID {
			delete(m.CommitIndex, key)
		}
		delete(m.Slips, oldCorrelationID)
	}

	// A missing superseded row is not an error: the successor is still created, so a
	// redelivery converges rather than failing forever.
	stored := deepCopySlip(newSlip)
	if removedOld {
		// Mirrors the predecessor marker PostgresStore.Repave appends to the successor, gated
		// on removedOld the same way so a repave that replaced nothing records nothing.
		stored.StateHistory = append(stored.StateHistory, StateHistoryEntry{
			Step:      "push_parsed",
			Status:    StepStatusRunning,
			Timestamp: time.Now(),
			Actor:     "slippy-library",
			Message:   fmt.Sprintf("repaved %s for commit %s", oldCorrelationID, shortSHA(newSlip.CommitSHA)),
		})
	}
	m.Slips[newSlip.CorrelationID] = stored
	m.CommitIndex[commitIndexKey(newSlip.Repository, newSlip.CommitSHA)] = newSlip.CorrelationID
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
	defer m.mu.Unlock()

	m.LoadByCommitCalls = append(m.LoadByCommitCalls, LoadByCommitCall{
		Repository: repository,
		CommitSHA:  commitSHA,
	})

	if m.LoadByCommitError != nil {
		return nil, m.LoadByCommitError
	}

	if m.LoadByCommitNilOnCall > 0 && len(m.LoadByCommitCalls) == m.LoadByCommitNilOnCall {
		return nil, nil
	}

	key := commitIndexKey(repository, commitSHA)
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

// LoadLiveByCommit retrieves the most recent live slip by repository and commit SHA,
// excluding superseded terminal statuses (abandoned, promoted, compensated).
func (m *MockStore) LoadLiveByCommit(ctx context.Context, repository, commitSHA string) (*Slip, error) {
	m.mu.Lock()
	m.LoadLiveByCommitCalls = append(m.LoadLiveByCommitCalls, LoadByCommitCall{
		Repository: repository,
		CommitSHA:  commitSHA,
	})
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.LoadLiveByCommitError != nil {
		return nil, m.LoadLiveByCommitError
	}

	key := commitIndexKey(repository, commitSHA)
	correlationID, ok := m.CommitIndex[key]
	if !ok {
		return nil, ErrSlipNotFound
	}

	slip, ok := m.Slips[correlationID]
	if !ok {
		return nil, ErrSlipNotFound
	}

	// Mirror prod semantics: exclude terminal-superseded statuses.
	if slip.Status == SlipStatusAbandoned ||
		slip.Status == SlipStatusPromoted ||
		slip.Status == SlipStatusCompensated {
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
		key := commitIndexKey(repository, commit)
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
		key := commitIndexKey(repository, commit)
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

// UpdateSlipStatus atomically updates the slip's status field.
func (m *MockStore) UpdateSlipStatus(ctx context.Context, correlationID string, status SlipStatus) error {
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
		return ErrSlipNotFound
	}

	slip.Status = status
	return nil
}

// UpdateStepWithHistory updates a step's status AND appends a history entry atomically.
// This is the combined operation that prevents race conditions.
func (m *MockStore) UpdateStepWithHistory(
	ctx context.Context,
	correlationID, stepName, componentName string,
	status StepStatus,
	entry StateHistoryEntry,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.UpdateStepWithHistoryCallCount++

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

	// UpdateStepError/UpdateStepErrorFor simulate the event-insert/gate write failing.
	// The real store's UpdateStepWithHistory hard-fails in this case (insertComponentState
	// or gate-check error), so the mock must too.
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

	// Update step
	if slip.Steps == nil {
		slip.Steps = make(map[string]Step)
	}
	step := slip.Steps[stepName]
	step.Status = status
	slip.Steps[stepName] = step

	// AppendHistoryError/AppendHistoryErrorFor simulate the history write-back failing.
	// The real store's pure-step branch (clickhouse_store.go) treats this as best-effort:
	// the event/step-status write is already durable, so the history append error is
	// Warn-logged and swallowed (return nil), not propagated. Mirror that here — record
	// the swallowed error for tests that want to assert it happened, but do not return it.
	if m.AppendHistoryError != nil {
		m.SwallowedHistoryErrors = append(m.SwallowedHistoryErrors, m.AppendHistoryError)
		return nil
	}
	if err, ok := m.AppendHistoryErrorFor[correlationID]; ok {
		m.SwallowedHistoryErrors = append(m.SwallowedHistoryErrors, err)
		return nil
	}

	// Append history
	slip.StateHistory = append(slip.StateHistory, entry)

	return nil
}

// InsertAncestryLink records a direct-parent link write. The mock has no
// slip_ancestry-equivalent table, so nothing is stored — but recording the call is what
// makes the push path's link writes visible to tests at all. While this was a bare
// `return nil` the unit suite could not tell whether a successor's parent hop had been
// written, which is exactly the class of bug the repave path is prone to.
func (m *MockStore) InsertAncestryLink(ctx context.Context, slip *Slip, parent AncestryEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.AncestryLinkCalls = append(m.AncestryLinkCalls, AncestryLinkCall{Slip: slip, Parent: parent})
	return m.AncestryLinkError
}

// ResolveAncestry walks parent links to reconstruct ancestry (returns empty in mock).
func (m *MockStore) ResolveAncestry(
	ctx context.Context,
	repository, branch, correlationID string,
	maxDepth int,
) ([]AncestryEntry, error) {
	return []AncestryEntry{}, nil
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
		return ErrSlipNotFound
	}

	// Target the aggregate column derived from the step name first.
	// If that column is not present, or the component is stored in other
	// aggregate columns, fall back to scanning all aggregates and update
	// every matching entry for this component.
	columnName := pluralizeMock(stepName)

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

	m.Slips = make(map[string]*Slip)
	m.CommitIndex = make(map[string]string)
	m.CreateCalls = nil
	m.LoadCalls = nil
	m.LoadByCommitCalls = nil
	m.LoadLiveByCommitCalls = nil
	m.FindByCommitsCalls = nil
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
func (m *MockStore) AddSlip(slip *Slip) {
	m.mu.Lock()
	defer m.mu.Unlock()

	slipCopy := deepCopySlip(slip)
	m.Slips[slip.CorrelationID] = slipCopy

	key := commitIndexKey(slip.Repository, slip.CommitSHA)
	m.CommitIndex[key] = slip.CorrelationID
}

// Ensure MockStore implements SlipStore.
var _ SlipStore = (*MockStore)(nil)
