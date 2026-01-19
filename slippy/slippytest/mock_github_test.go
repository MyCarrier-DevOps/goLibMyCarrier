package slippytest

import (
	"context"
	"errors"
	"testing"
)

func TestMockGitHubAPI_NewMockGitHubAPI(t *testing.T) {
	api := NewMockGitHubAPI()

	if api.Ancestry == nil {
		t.Error("Ancestry should be initialized")
	}
	if api.PRHeadCommits == nil {
		t.Error("PRHeadCommits should be initialized")
	}
	if api.GetCommitAncestryErrorFor == nil {
		t.Error("GetCommitAncestryErrorFor should be initialized")
	}
	if api.GetPRHeadCommitErrorFor == nil {
		t.Error("GetPRHeadCommitErrorFor should be initialized")
	}
}

func TestMockGitHubAPI_GetCommitAncestry(t *testing.T) {
	api := NewMockGitHubAPI()
	ctx := context.Background()

	// Configure ancestry
	api.SetAncestry("owner", "repo", "main", []string{"commit1", "commit2", "commit3"})

	commits, err := api.GetCommitAncestry(ctx, "owner", "repo", "main", 10)
	if err != nil {
		t.Fatalf("GetCommitAncestry failed: %v", err)
	}

	if len(commits) != 3 {
		t.Errorf("expected 3 commits, got %d", len(commits))
	}

	if len(api.GetCommitAncestryCalls) != 1 {
		t.Errorf("expected 1 call, got %d", len(api.GetCommitAncestryCalls))
	}

	call := api.GetCommitAncestryCalls[0]
	if call.Owner != "owner" || call.Repo != "repo" || call.Ref != "main" || call.Depth != 10 {
		t.Errorf("unexpected call parameters: %+v", call)
	}
}

func TestMockGitHubAPI_GetCommitAncestry_WithDepth(t *testing.T) {
	api := NewMockGitHubAPI()
	ctx := context.Background()

	// Configure ancestry with more commits than requested depth
	api.SetAncestry("owner", "repo", "main", []string{"commit1", "commit2", "commit3", "commit4", "commit5"})

	commits, err := api.GetCommitAncestry(ctx, "owner", "repo", "main", 3)
	if err != nil {
		t.Fatalf("GetCommitAncestry failed: %v", err)
	}

	if len(commits) != 3 {
		t.Errorf("expected 3 commits (limited by depth), got %d", len(commits))
	}
}

func TestMockGitHubAPI_GetCommitAncestry_NotConfigured(t *testing.T) {
	api := NewMockGitHubAPI()
	ctx := context.Background()

	// Query without configuring ancestry
	commits, err := api.GetCommitAncestry(ctx, "owner", "repo", "unconfigured", 10)
	if err != nil {
		t.Fatalf("GetCommitAncestry failed: %v", err)
	}

	if len(commits) != 0 {
		t.Errorf("expected 0 commits for unconfigured ref, got %d", len(commits))
	}
}

func TestMockGitHubAPI_GetCommitAncestry_WithError(t *testing.T) {
	api := NewMockGitHubAPI()
	ctx := context.Background()

	testErr := errors.New("ancestry error")
	api.GetCommitAncestryError = testErr

	_, err := api.GetCommitAncestry(ctx, "owner", "repo", "main", 10)
	if err != testErr {
		t.Errorf("expected GetCommitAncestryError, got %v", err)
	}
}

func TestMockGitHubAPI_GetCommitAncestry_WithErrorFor(t *testing.T) {
	api := NewMockGitHubAPI()
	ctx := context.Background()

	testErr := errors.New("specific ancestry error")
	api.GetCommitAncestryErrorFor["owner/repo:main"] = testErr

	_, err := api.GetCommitAncestry(ctx, "owner", "repo", "main", 10)
	if err != testErr {
		t.Errorf("expected GetCommitAncestryErrorFor error, got %v", err)
	}
}

func TestMockGitHubAPI_GetPRHeadCommit(t *testing.T) {
	api := NewMockGitHubAPI()
	ctx := context.Background()

	// Configure PR head commit
	api.SetPRHeadCommit("owner", "repo", 42, "abc123")

	commit, err := api.GetPRHeadCommit(ctx, "owner", "repo", 42)
	if err != nil {
		t.Fatalf("GetPRHeadCommit failed: %v", err)
	}

	if commit != "abc123" {
		t.Errorf("expected commit abc123, got %s", commit)
	}

	if len(api.GetPRHeadCommitCalls) != 1 {
		t.Errorf("expected 1 call, got %d", len(api.GetPRHeadCommitCalls))
	}

	call := api.GetPRHeadCommitCalls[0]
	if call.Owner != "owner" || call.Repo != "repo" || call.PRNumber != 42 {
		t.Errorf("unexpected call parameters: %+v", call)
	}
}

func TestMockGitHubAPI_GetPRHeadCommit_NotFound(t *testing.T) {
	api := NewMockGitHubAPI()
	ctx := context.Background()

	_, err := api.GetPRHeadCommit(ctx, "owner", "repo", 999)
	if err == nil {
		t.Error("expected error for unconfigured PR, got nil")
	}
}

func TestMockGitHubAPI_GetPRHeadCommit_WithError(t *testing.T) {
	api := NewMockGitHubAPI()
	ctx := context.Background()

	testErr := errors.New("PR error")
	api.GetPRHeadCommitError = testErr

	_, err := api.GetPRHeadCommit(ctx, "owner", "repo", 42)
	if err != testErr {
		t.Errorf("expected GetPRHeadCommitError, got %v", err)
	}
}

func TestMockGitHubAPI_GetPRHeadCommit_WithErrorFor(t *testing.T) {
	api := NewMockGitHubAPI()
	ctx := context.Background()

	testErr := errors.New("specific PR error")
	api.GetPRHeadCommitErrorFor["owner/repo:42"] = testErr

	_, err := api.GetPRHeadCommit(ctx, "owner", "repo", 42)
	if err != testErr {
		t.Errorf("expected GetPRHeadCommitErrorFor error, got %v", err)
	}
}

func TestMockGitHubAPI_ClearCache(t *testing.T) {
	api := NewMockGitHubAPI()

	api.ClearCache()
	api.ClearCache()

	if api.ClearCacheCalls != 2 {
		t.Errorf("expected 2 ClearCache calls, got %d", api.ClearCacheCalls)
	}
}

func TestMockGitHubAPI_Reset(t *testing.T) {
	api := NewMockGitHubAPI()
	ctx := context.Background()

	// Configure some data
	api.SetAncestry("owner", "repo", "main", []string{"commit1"})
	api.SetPRHeadCommit("owner", "repo", 42, "abc123")
	api.GetCommitAncestryError = errors.New("error")
	api.GetPRHeadCommitError = errors.New("error")
	api.GetCommitAncestryErrorFor["key"] = errors.New("error")
	api.GetPRHeadCommitErrorFor["key"] = errors.New("error")

	// Make some calls
	_, _ = api.GetCommitAncestry(ctx, "owner", "repo", "main", 10)
	_, _ = api.GetPRHeadCommit(ctx, "owner", "repo", 42)
	api.ClearCache()

	// Reset
	api.Reset()

	// Verify everything is cleared
	if len(api.Ancestry) != 0 {
		t.Error("Ancestry should be cleared")
	}
	if len(api.PRHeadCommits) != 0 {
		t.Error("PRHeadCommits should be cleared")
	}
	if len(api.GetCommitAncestryCalls) != 0 {
		t.Error("GetCommitAncestryCalls should be cleared")
	}
	if len(api.GetPRHeadCommitCalls) != 0 {
		t.Error("GetPRHeadCommitCalls should be cleared")
	}
	if api.ClearCacheCalls != 0 {
		t.Error("ClearCacheCalls should be cleared")
	}
	if api.GetCommitAncestryError != nil {
		t.Error("GetCommitAncestryError should be cleared")
	}
	if api.GetPRHeadCommitError != nil {
		t.Error("GetPRHeadCommitError should be cleared")
	}
}

func TestMockGitHubAPI_SetAncestry(t *testing.T) {
	api := NewMockGitHubAPI()

	// Set ancestry for multiple refs
	api.SetAncestry("owner", "repo", "main", []string{"a", "b"})
	api.SetAncestry("owner", "repo", "feature", []string{"c", "d", "e"})

	if len(api.Ancestry) != 2 {
		t.Errorf("expected 2 ancestry entries, got %d", len(api.Ancestry))
	}

	// Verify keys are correct
	if _, ok := api.Ancestry["owner/repo:main"]; !ok {
		t.Error("expected owner/repo:main key")
	}
	if _, ok := api.Ancestry["owner/repo:feature"]; !ok {
		t.Error("expected owner/repo:feature key")
	}
}

func TestMockGitHubAPI_SetPRHeadCommit(t *testing.T) {
	api := NewMockGitHubAPI()

	api.SetPRHeadCommit("owner", "repo", 1, "commit1")
	api.SetPRHeadCommit("owner", "repo", 2, "commit2")

	if len(api.PRHeadCommits) != 2 {
		t.Errorf("expected 2 PR head commits, got %d", len(api.PRHeadCommits))
	}

	// Verify keys are correct
	if _, ok := api.PRHeadCommits["owner/repo:1"]; !ok {
		t.Error("expected owner/repo:1 key")
	}
	if _, ok := api.PRHeadCommits["owner/repo:2"]; !ok {
		t.Error("expected owner/repo:2 key")
	}
}
