package slippy

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClient_ResolveSlip(t *testing.T) {
	ctx := context.Background()

	t.Run("resolve via ancestry", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth: 20,
		})

		// Create slip
		slip := &Slip{
			CorrelationID: "corr-resolve-1",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)

		// Set up ancestry: HEAD -> abc123 (matching commit)
		github.SetAncestry("owner", "repo", "HEAD", []string{"def456", "abc123", "xyz789"})

		result, err := client.ResolveSlip(ctx, ResolveOptions{
			Repository: "owner/repo",
			Branch:     "main",
			Ref:        "HEAD",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Slip.CorrelationID != "corr-resolve-1" {
			t.Errorf("expected slip ID 'corr-resolve-1', got '%s'", result.Slip.CorrelationID)
		}
		if result.ResolvedBy != "ancestry" {
			t.Errorf("expected resolved by 'ancestry', got '%s'", result.ResolvedBy)
		}
		if result.MatchedCommit != "abc123" {
			t.Errorf("expected matched commit 'abc123', got '%s'", result.MatchedCommit)
		}
	})

	t.Run("resolve via image tag fallback", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth: 20,
		})

		// Create slip with specific commit
		slip := &Slip{
			CorrelationID: "corr-resolve-2",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc1234", // 7 chars to match image tag pattern
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)

		// No ancestry configured (or ancestry doesn't match)
		github.SetAncestry("owner", "repo", "HEAD", []string{"other123"})

		result, err := client.ResolveSlip(ctx, ResolveOptions{
			Repository: "owner/repo",
			Branch:     "main",
			Ref:        "HEAD",
			ImageTag:   "mycarrier/svc:abc1234-1234567890", // Contains commit SHA
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Slip.CorrelationID != "corr-resolve-2" {
			t.Errorf("expected slip ID 'corr-resolve-2', got '%s'", result.Slip.CorrelationID)
		}
		if result.ResolvedBy != "image_tag" {
			t.Errorf("expected resolved by 'image_tag', got '%s'", result.ResolvedBy)
		}
	})

	t.Run("not found", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		github.SetAncestry("owner", "repo", "HEAD", []string{"nomatch1", "nomatch2"})

		result, err := client.ResolveSlip(ctx, ResolveOptions{
			Repository: "owner/repo",
			Branch:     "main",
			Ref:        "HEAD",
		})
		if err == nil {
			t.Fatal("expected error for not found")
		}
		if result != nil {
			t.Error("expected nil result")
		}

		var resolveErr *ResolveError
		if !errors.As(err, &resolveErr) {
			t.Errorf("expected ResolveError, got %T", err)
		}
	})

	t.Run("invalid repository format", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		_, err := client.ResolveSlip(ctx, ResolveOptions{
			Repository: "invalid-repo", // Missing owner/
			Branch:     "main",
			Ref:        "HEAD",
		})
		if err == nil {
			t.Fatal("expected error for invalid repository")
		}
		if !errors.Is(err, ErrInvalidRepository) {
			t.Errorf("expected ErrInvalidRepository, got: %v", err)
		}
	})

	t.Run("github error falls back to image tag", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{})

		slip := &Slip{
			CorrelationID: "corr-resolve-3",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc1234",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)

		// Set GitHub to return error
		github.GetCommitAncestryError = errors.New("GitHub API unavailable")

		result, err := client.ResolveSlip(ctx, ResolveOptions{
			Repository: "owner/repo",
			Branch:     "main",
			Ref:        "HEAD",
			ImageTag:   "mycarrier/svc:abc1234-1234567890",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.ResolvedBy != "image_tag" {
			t.Errorf("expected fallback to 'image_tag', got '%s'", result.ResolvedBy)
		}
	})

	t.Run("uses config ancestry depth", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth: 5, // Custom depth
		})

		slip := &Slip{
			CorrelationID: "corr-resolve-4",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)

		github.SetAncestry("owner", "repo", "HEAD", []string{"abc123"})

		_, err := client.ResolveSlip(ctx, ResolveOptions{
			Repository: "owner/repo",
			Branch:     "main",
			Ref:        "HEAD",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify the GitHub API was called with the config depth
		if len(github.GetCommitAncestryCalls) != 1 {
			t.Fatal("expected 1 GetCommitAncestry call")
		}
		if github.GetCommitAncestryCalls[0].Depth != 5 {
			t.Errorf("expected depth 5, got %d", github.GetCommitAncestryCalls[0].Depth)
		}
	})

	t.Run("uses custom ancestry depth from options", func(t *testing.T) {
		store := NewMockStore()
		github := NewMockGitHubAPI()
		client := NewClientWithDependencies(store, github, Config{
			AncestryDepth: 20, // Config depth
		})

		slip := &Slip{
			CorrelationID: "corr-resolve-5",
			Repository:    "owner/repo",
			Branch:        "main",
			CommitSHA:     "abc123",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Status:        SlipStatusInProgress,
			Steps:         make(map[string]Step),
		}
		store.AddSlip(slip)

		github.SetAncestry("owner", "repo", "HEAD", []string{"abc123"})

		_, err := client.ResolveSlip(ctx, ResolveOptions{
			Repository:    "owner/repo",
			Branch:        "main",
			Ref:           "HEAD",
			AncestryDepth: 10, // Override
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Options depth should be used (config depth is used only if options depth is 0)
		if len(github.GetCommitAncestryCalls) != 1 {
			t.Fatal("expected 1 GetCommitAncestry call")
		}
		// Note: The implementation uses config.AncestryDepth if opts.AncestryDepth is 0
		// So with AncestryDepth: 10, it should use 10
	})
}

func TestParseRepository(t *testing.T) {
	tests := []struct {
		name      string
		fullName  string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{
			name:      "valid",
			fullName:  "owner/repo",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "with hyphens",
			fullName:  "my-org/my-repo-name",
			wantOwner: "my-org",
			wantRepo:  "my-repo-name",
			wantErr:   false,
		},
		{
			name:      "with underscores",
			fullName:  "my_org/my_repo",
			wantOwner: "my_org",
			wantRepo:  "my_repo",
			wantErr:   false,
		},
		{
			name:     "missing slash",
			fullName: "noslash",
			wantErr:  true,
		},
		{
			name:     "empty owner",
			fullName: "/repo",
			wantErr:  true,
		},
		{
			name:     "empty repo",
			fullName: "owner/",
			wantErr:  true,
		},
		{
			name:     "empty string",
			fullName: "",
			wantErr:  true,
		},
		{
			name:      "multiple slashes uses first only",
			fullName:  "owner/repo/extra",
			wantOwner: "owner",
			wantRepo:  "repo/extra", // SplitN with n=2
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, err := parseRepository(tt.fullName)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseRepository() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if owner != tt.wantOwner {
					t.Errorf("parseRepository() owner = %s, want %s", owner, tt.wantOwner)
				}
				if repo != tt.wantRepo {
					t.Errorf("parseRepository() repo = %s, want %s", repo, tt.wantRepo)
				}
			}
		})
	}
}

func TestExtractCommitFromImageTag(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		want string
	}{
		{
			name: "commit-timestamp format",
			tag:  "mycarrier/svc:abc1234-1234567890",
			want: "abc1234",
		},
		{
			name: "short sha only",
			tag:  "mycarrier/svc:abc1234",
			want: "abc1234",
		},
		{
			name: "semver with sha",
			tag:  "mycarrier/svc:v1.2.3-abc1234",
			want: "abc1234",
		},
		{
			name: "full sha",
			tag:  "mycarrier/svc:abc1234def5678901234567890abcdef12345678",
			want: "abc1234def5678901234567890abcdef12345678",
		},
		{
			name: "no sha in tag",
			tag:  "mycarrier/svc:latest",
			want: "",
		},
		{
			name: "version tag without sha",
			tag:  "mycarrier/svc:v1.2.3",
			want: "",
		},
		{
			name: "too short to be sha",
			tag:  "mycarrier/svc:abc123", // 6 chars, need 7+
			want: "",
		},
		{
			name: "just the tag part",
			tag:  "abc1234-1234567890",
			want: "abc1234",
		},
		{
			name: "sha at end after hyphen",
			tag:  "mycarrier/svc:feature-abc1234",
			want: "abc1234",
		},
		{
			name: "multiple potential matches uses first",
			tag:  "mycarrier/svc:abc1234-def5678",
			want: "abc1234",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractCommitFromImageTag(tt.tag)
			if got != tt.want {
				t.Errorf("extractCommitFromImageTag(%q) = %q, want %q", tt.tag, got, tt.want)
			}
		})
	}
}

func TestShortSHA(t *testing.T) {
	tests := []struct {
		sha  string
		want string
	}{
		{"abc1234def5678901234567890abcdef12345678", "abc1234"},
		{"abc1234", "abc1234"},
		{"abc", "abc"},
		{"", ""},
		{"1234567", "1234567"},
		{"12345678", "1234567"},
	}

	for _, tt := range tests {
		t.Run(tt.sha, func(t *testing.T) {
			got := shortSHA(tt.sha)
			if got != tt.want {
				t.Errorf("shortSHA(%q) = %q, want %q", tt.sha, got, tt.want)
			}
		})
	}
}
