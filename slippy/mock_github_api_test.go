package slippy

import (
	"context"
	"errors"
	"testing"
)

func TestMockGitHubAPI_GetCommitAncestry(t *testing.T) {
	ctx := context.Background()

	t.Run("returns configured ancestry", func(t *testing.T) {
		mock := NewMockGitHubAPI()
		mock.SetAncestry("owner", "repo", "main", []string{"commit1", "commit2", "commit3"})

		ancestry, err := mock.GetCommitAncestry(ctx, "owner", "repo", "main", 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ancestry) != 3 {
			t.Errorf("expected 3 commits, got %d", len(ancestry))
		}
		if ancestry[0] != "commit1" {
			t.Errorf("expected first commit 'commit1', got '%s'", ancestry[0])
		}
	})

	t.Run("respects depth limit", func(t *testing.T) {
		mock := NewMockGitHubAPI()
		mock.SetAncestry("owner", "repo", "feature", []string{"c1", "c2", "c3", "c4", "c5"})

		ancestry, err := mock.GetCommitAncestry(ctx, "owner", "repo", "feature", 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ancestry) != 3 {
			t.Errorf("expected 3 commits (limited by depth), got %d", len(ancestry))
		}
	})

	t.Run("returns empty for unconfigured ref", func(t *testing.T) {
		mock := NewMockGitHubAPI()

		ancestry, err := mock.GetCommitAncestry(ctx, "owner", "repo", "unknown", 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ancestry) != 0 {
			t.Errorf("expected 0 commits for unconfigured ref, got %d", len(ancestry))
		}
	})

	t.Run("returns global error", func(t *testing.T) {
		mock := NewMockGitHubAPI()
		mock.GetCommitAncestryError = errors.New("API rate limited")

		_, err := mock.GetCommitAncestry(ctx, "owner", "repo", "main", 10)
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "API rate limited" {
			t.Errorf("expected 'API rate limited', got '%s'", err.Error())
		}
	})

	t.Run("returns ref-specific error", func(t *testing.T) {
		mock := NewMockGitHubAPI()
		mock.GetCommitAncestryErrorFor["owner/repo:broken"] = errors.New("ref not found")

		_, err := mock.GetCommitAncestry(ctx, "owner", "repo", "broken", 10)
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "ref not found" {
			t.Errorf("expected 'ref not found', got '%s'", err.Error())
		}

		// Other refs should work
		_, err = mock.GetCommitAncestry(ctx, "owner", "repo", "main", 10)
		if err != nil {
			t.Fatalf("unexpected error for different ref: %v", err)
		}
	})

	t.Run("tracks calls", func(t *testing.T) {
		mock := NewMockGitHubAPI()

		_, _ = mock.GetCommitAncestry(ctx, "org1", "repo1", "main", 5)
		_, _ = mock.GetCommitAncestry(ctx, "org2", "repo2", "feature", 10)

		if len(mock.GetCommitAncestryCalls) != 2 {
			t.Fatalf("expected 2 calls, got %d", len(mock.GetCommitAncestryCalls))
		}

		call1 := mock.GetCommitAncestryCalls[0]
		if call1.Owner != "org1" || call1.Repo != "repo1" || call1.Ref != "main" || call1.Depth != 5 {
			t.Errorf("first call not recorded correctly: %+v", call1)
		}

		call2 := mock.GetCommitAncestryCalls[1]
		if call2.Owner != "org2" || call2.Repo != "repo2" || call2.Ref != "feature" || call2.Depth != 10 {
			t.Errorf("second call not recorded correctly: %+v", call2)
		}
	})
}

func TestMockGitHubAPI_ClearCache(t *testing.T) {
	mock := NewMockGitHubAPI()

	mock.ClearCache()
	mock.ClearCache()
	mock.ClearCache()

	if mock.ClearCacheCalls != 3 {
		t.Errorf("expected 3 ClearCache calls, got %d", mock.ClearCacheCalls)
	}
}

func TestMockGitHubAPI_Reset(t *testing.T) {
	ctx := context.Background()
	mock := NewMockGitHubAPI()

	// Configure some state
	mock.SetAncestry("owner", "repo", "main", []string{"c1", "c2"})
	_, _ = mock.GetCommitAncestry(ctx, "owner", "repo", "main", 10)
	mock.ClearCache()
	mock.GetCommitAncestryError = errors.New("some error")
	mock.GetCommitAncestryErrorFor["owner/repo:bad"] = errors.New("bad ref")

	// Reset
	mock.Reset()

	// Verify state is cleared
	if len(mock.Ancestry) != 0 {
		t.Error("expected Ancestry to be cleared")
	}
	if len(mock.GetCommitAncestryCalls) != 0 {
		t.Error("expected GetCommitAncestryCalls to be cleared")
	}
	if mock.ClearCacheCalls != 0 {
		t.Error("expected ClearCacheCalls to be 0")
	}
	if mock.GetCommitAncestryError != nil {
		t.Error("expected GetCommitAncestryError to be nil")
	}
	if len(mock.GetCommitAncestryErrorFor) != 0 {
		t.Error("expected GetCommitAncestryErrorFor to be cleared")
	}
}

func TestMockGitHubAPI_ImplementsInterface(t *testing.T) {
	var _ GitHubAPI = (*MockGitHubAPI)(nil)
}
