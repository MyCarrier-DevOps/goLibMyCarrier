package slippytest

import (
	"context"
	"fmt"
	"sync"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

// MockGitHubAPI is a mock implementation of slippy.GitHubAPI for testing.
// It allows configuration of commit ancestry, PR head commits, and error injection.
//
// Example:
//
//	github := slippytest.NewMockGitHubAPI()
//	github.SetAncestry("owner", "repo", "main", []string{"abc123", "def456", "ghi789"})
//	github.SetPRHeadCommit("owner", "repo", 42, "feature-commit-sha")
//
//	// Now calls to GetCommitAncestry and GetPRHeadCommit will return the configured values
type MockGitHubAPI struct {
	mu sync.RWMutex

	// Ancestry maps "owner/repo:ref" -> []string (commit ancestry)
	Ancestry map[string][]string

	// PRHeadCommits maps "owner/repo:prNumber" -> string (PR head commit SHA)
	PRHeadCommits map[string]string

	// Call tracking
	GetCommitAncestryCalls []GetCommitAncestryCall
	GetPRHeadCommitCalls   []GetPRHeadCommitCall
	ClearCacheCalls        int

	// Error injection
	GetCommitAncestryError error
	GetPRHeadCommitError   error

	// Conditional error injection (returns error only for specific refs)
	GetCommitAncestryErrorFor map[string]error
	GetPRHeadCommitErrorFor   map[string]error
}

// GetCommitAncestryCall records a GetCommitAncestry call.
type GetCommitAncestryCall struct {
	Owner string
	Repo  string
	Ref   string
	Depth int
}

// GetPRHeadCommitCall records a GetPRHeadCommit call.
type GetPRHeadCommitCall struct {
	Owner    string
	Repo     string
	PRNumber int
}

// NewMockGitHubAPI creates a new MockGitHubAPI with initialized maps.
func NewMockGitHubAPI() *MockGitHubAPI {
	return &MockGitHubAPI{
		Ancestry:                  make(map[string][]string),
		PRHeadCommits:             make(map[string]string),
		GetCommitAncestryErrorFor: make(map[string]error),
		GetPRHeadCommitErrorFor:   make(map[string]error),
	}
}

// GetCommitAncestry retrieves the commit ancestry for a given ref.
func (m *MockGitHubAPI) GetCommitAncestry(ctx context.Context, owner, repo, ref string, depth int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.GetCommitAncestryCalls = append(m.GetCommitAncestryCalls, GetCommitAncestryCall{
		Owner: owner,
		Repo:  repo,
		Ref:   ref,
		Depth: depth,
	})

	if m.GetCommitAncestryError != nil {
		return nil, m.GetCommitAncestryError
	}

	key := owner + "/" + repo + ":" + ref
	if err, ok := m.GetCommitAncestryErrorFor[key]; ok {
		return nil, err
	}

	// Look up the ancestry
	ancestry, ok := m.Ancestry[key]
	if !ok {
		// Return empty slice if not configured (no ancestry found)
		return []string{}, nil
	}

	// Limit to requested depth
	if depth > 0 && len(ancestry) > depth {
		return ancestry[:depth], nil
	}

	return ancestry, nil
}

// GetPRHeadCommit retrieves the head commit SHA for a pull request.
func (m *MockGitHubAPI) GetPRHeadCommit(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.GetPRHeadCommitCalls = append(m.GetPRHeadCommitCalls, GetPRHeadCommitCall{
		Owner:    owner,
		Repo:     repo,
		PRNumber: prNumber,
	})

	if m.GetPRHeadCommitError != nil {
		return "", m.GetPRHeadCommitError
	}

	key := fmt.Sprintf("%s/%s:%d", owner, repo, prNumber)
	if err, ok := m.GetPRHeadCommitErrorFor[key]; ok {
		return "", err
	}

	// Look up the PR head commit
	headCommit, ok := m.PRHeadCommits[key]
	if !ok {
		return "", fmt.Errorf("PR #%d not found", prNumber)
	}

	return headCommit, nil
}

// ClearCache clears any cached data.
func (m *MockGitHubAPI) ClearCache() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ClearCacheCalls++
}

// SetAncestry configures the ancestry for a specific owner/repo/ref.
// The commits should be ordered from newest to oldest.
func (m *MockGitHubAPI) SetAncestry(owner, repo, ref string, commits []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := owner + "/" + repo + ":" + ref
	m.Ancestry[key] = commits
}

// SetPRHeadCommit configures the head commit for a specific PR.
func (m *MockGitHubAPI) SetPRHeadCommit(owner, repo string, prNumber int, headCommit string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s/%s:%d", owner, repo, prNumber)
	m.PRHeadCommits[key] = headCommit
}

// Reset clears all configured ancestry and call tracking.
func (m *MockGitHubAPI) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Ancestry = make(map[string][]string)
	m.PRHeadCommits = make(map[string]string)
	m.GetCommitAncestryCalls = nil
	m.GetPRHeadCommitCalls = nil
	m.ClearCacheCalls = 0
	m.GetCommitAncestryError = nil
	m.GetPRHeadCommitError = nil
	m.GetCommitAncestryErrorFor = make(map[string]error)
	m.GetPRHeadCommitErrorFor = make(map[string]error)
}

// Ensure MockGitHubAPI implements slippy.GitHubAPI at compile time.
var _ slippy.GitHubAPI = (*MockGitHubAPI)(nil)
