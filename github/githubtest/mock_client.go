// Package githubtest provides test fixtures and mocks for testing code that uses the github package.
// This follows the Go standard library pattern (e.g., net/http/httptest).
//
// Example usage:
//
//	func TestMyFunction(t *testing.T) {
//	    client := githubtest.NewMockGraphQLClient()
//	    client.SetCommitAncestry("MyOrg", "my-repo", "main", []string{"abc123", "def456"})
//
//	    // Use the mock client in your tests
//	    commits, err := client.GetCommitAncestry(ctx, "MyOrg", "my-repo", "main", 10)
//	    if err != nil {
//	        t.Fatal(err)
//	    }
//
//	    // Verify interactions
//	    if len(client.GetCommitAncestryCalls) != 1 {
//	        t.Error("expected one call to GetCommitAncestry")
//	    }
//	}
package githubtest

import (
	"context"
	"sync"
)

// MockGraphQLClient is a mock implementation of the GitHub GraphQL client for testing.
// It provides configurable commit ancestry responses and error injection.
type MockGraphQLClient struct {
	mu sync.RWMutex

	// CommitAncestry maps "owner/repo:ref" -> []string (commit SHAs from newest to oldest)
	CommitAncestry map[string][]string

	// Call tracking
	GetCommitAncestryCalls []GetCommitAncestryCall
	ClearCacheCalls        int

	// Error injection
	GetCommitAncestryError error

	// Conditional error injection per owner/repo:ref
	GetCommitAncestryErrorFor map[string]error
}

// GetCommitAncestryCall records a GetCommitAncestry call.
type GetCommitAncestryCall struct {
	Owner string
	Repo  string
	Ref   string
	Depth int
}

// NewMockGraphQLClient creates a new MockGraphQLClient with initialized maps.
func NewMockGraphQLClient() *MockGraphQLClient {
	return &MockGraphQLClient{
		CommitAncestry:            make(map[string][]string),
		GetCommitAncestryErrorFor: make(map[string]error),
	}
}

// GetCommitAncestry retrieves the commit ancestry for a given ref.
// Returns commits from newest to oldest, limited by depth.
func (m *MockGraphQLClient) GetCommitAncestry(ctx context.Context, owner, repo, ref string, depth int) ([]string, error) {
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

	ancestry, ok := m.CommitAncestry[key]
	if !ok {
		return []string{}, nil
	}

	// Limit to requested depth
	if depth > 0 && len(ancestry) > depth {
		return ancestry[:depth], nil
	}

	return ancestry, nil
}

// ClearCache clears any cached data.
func (m *MockGraphQLClient) ClearCache() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ClearCacheCalls++
}

// SetCommitAncestry configures the commit ancestry for a specific owner/repo/ref.
// The commits should be ordered from newest to oldest.
func (m *MockGraphQLClient) SetCommitAncestry(owner, repo, ref string, commits []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := owner + "/" + repo + ":" + ref
	m.CommitAncestry[key] = commits
}

// SetError configures a global error to return for all GetCommitAncestry calls.
func (m *MockGraphQLClient) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetCommitAncestryError = err
}

// SetErrorFor configures an error to return for a specific owner/repo/ref.
func (m *MockGraphQLClient) SetErrorFor(owner, repo, ref string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := owner + "/" + repo + ":" + ref
	m.GetCommitAncestryErrorFor[key] = err
}

// Reset clears all configured ancestry, errors, and call tracking.
func (m *MockGraphQLClient) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CommitAncestry = make(map[string][]string)
	m.GetCommitAncestryCalls = nil
	m.ClearCacheCalls = 0
	m.GetCommitAncestryError = nil
	m.GetCommitAncestryErrorFor = make(map[string]error)
}

// CallCount returns the total number of GetCommitAncestry calls.
func (m *MockGraphQLClient) CallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.GetCommitAncestryCalls)
}

// LastCall returns the last GetCommitAncestry call, or nil if no calls were made.
func (m *MockGraphQLClient) LastCall() *GetCommitAncestryCall {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.GetCommitAncestryCalls) == 0 {
		return nil
	}
	return &m.GetCommitAncestryCalls[len(m.GetCommitAncestryCalls)-1]
}
