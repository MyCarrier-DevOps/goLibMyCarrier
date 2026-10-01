//go:build integration

package slippy

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
)

// Integration-test helpers shared across the package's integration-tagged files. They lived
// in the ClickHouse integration files until DEVOPS-343 removed the ClickHouse store.

func init() {
	// Disable ryuk (reaper) for Podman compatibility
	// Ryuk has issues connecting to Docker socket inside Podman containers
	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
}

// mockGitHubAPIForE2E implements GitHubAPI for E2E tests
type mockGitHubAPIForE2E struct {
	mu           sync.RWMutex
	ancestryMap  map[string][]string // repo/ref -> []commitSHA
	prHeadCommit map[string]string   // repo/prNum -> headCommitSHA
}

func newMockGitHubAPIForE2E() *mockGitHubAPIForE2E {
	return &mockGitHubAPIForE2E{
		ancestryMap:  make(map[string][]string),
		prHeadCommit: make(map[string]string),
	}
}

func (m *mockGitHubAPIForE2E) GetCommitAncestry(
	ctx context.Context,
	owner, repo, ref string,
	depth int,
) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := fmt.Sprintf("%s/%s/%s", owner, repo, ref)
	if commits, ok := m.ancestryMap[key]; ok {
		if depth > 0 && len(commits) > depth {
			return commits[:depth], nil
		}
		return commits, nil
	}
	return nil, nil
}

func (m *mockGitHubAPIForE2E) GetPRHeadCommit(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := fmt.Sprintf("%s/%s/%d", owner, repo, prNumber)
	if commit, ok := m.prHeadCommit[key]; ok {
		return commit, nil
	}
	return "", fmt.Errorf("PR not found")
}

func (m *mockGitHubAPIForE2E) ClearCache() {}

func (m *mockGitHubAPIForE2E) SetAncestry(owner, repo, ref string, commits []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s/%s/%s", owner, repo, ref)
	m.ancestryMap[key] = commits
}

// e2eTestLogger implements Logger for E2E tests
type e2eTestLogger struct {
	t      *testing.T
	fields map[string]interface{}
}

func (l *e2eTestLogger) Debug(ctx context.Context, msg string, fields map[string]interface{}) {
	// Suppress debug in tests unless verbose
}

func (l *e2eTestLogger) Info(ctx context.Context, msg string, fields map[string]interface{}) {
	l.t.Logf("[INFO] %s %v", msg, fields)
}

func (l *e2eTestLogger) Warn(ctx context.Context, msg string, fields map[string]interface{}) {
	l.t.Logf("[WARN] %s %v", msg, fields)
}

func (l *e2eTestLogger) Warning(ctx context.Context, msg string, fields map[string]interface{}) {
	l.t.Logf("[WARN] %s %v", msg, fields)
}

func (l *e2eTestLogger) Error(ctx context.Context, msg string, err error, fields map[string]interface{}) {
	l.t.Logf("[ERROR] %s err=%v %v", msg, err, fields)
}

func (l *e2eTestLogger) WithFields(fields map[string]interface{}) Logger {
	newFields := make(map[string]interface{})
	for k, v := range l.fields {
		newFields[k] = v
	}
	for k, v := range fields {
		newFields[k] = v
	}
	return &e2eTestLogger{t: l.t, fields: newFields}
}
