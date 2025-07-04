package github_handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/google/go-github/v73/github"
	"github.com/stretchr/testify/assert"
)

// Helper functions for pointer creation (replacing deprecated github.String, github.Int, github.Bool)
func stringPtr(s string) *string { return &s }
func intPtr(i int) *int          { return &i }
func boolPtr(b bool) *bool       { return &b }

func TestGithubLoadConfig_Success(t *testing.T) {
	// Set environment variables
	if err := os.Setenv("GITHUB_APP_PRIVATE_KEY", "test-private-key"); err != nil {
		t.Fatalf("Failed to set GITHUB_APP_PRIVATE_KEY: %v", err.Error())
	}
	if err := os.Setenv("GITHUB_APP_ID", "12345"); err != nil {
		t.Fatalf("Failed to set GITHUB_APP_ID: %v", err.Error())
	}
	if err := os.Setenv("GITHUB_APP_INSTALLATION_ID", "67890"); err != nil {
		t.Fatalf("Failed to set GITHUB_APP_INSTALLATION_ID: %v", err.Error())
	}
	defer func() {
		if err := os.Unsetenv("GITHUB_APP_PRIVATE_KEY"); err != nil {
			t.Errorf("Failed to unset GITHUB_APP_PRIVATE_KEY: %v", err.Error())
		}
		if err := os.Unsetenv("GITHUB_APP_ID"); err != nil {
			t.Errorf("Failed to unset GITHUB_APP_ID: %v", err.Error())
		}
		if err := os.Unsetenv("GITHUB_APP_INSTALLATION_ID"); err != nil {
			t.Errorf("Failed to unset GITHUB_APP_INSTALLATION_ID: %v", err.Error())
		}
	}()

	config, err := GithubLoadConfig()
	assert.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "test-private-key", config.Pem)
	assert.Equal(t, "12345", config.AppId)
	assert.Equal(t, "67890", config.InstallId)
}

func TestGithubLoadConfig_MissingEnvVars(t *testing.T) {
	// Ensure environment variables are not set
	if err := os.Unsetenv("GITHUB_APP_PRIVATE_KEY"); err != nil {
		t.Errorf("Failed to unset GITHUB_APP_PRIVATE_KEY: %v", err)
	}
	if err := os.Unsetenv("GITHUB_APP_ID"); err != nil {
		t.Errorf("Failed to unset GITHUB_APP_ID: %v", err)
	}
	if err := os.Unsetenv("GITHUB_APP_INSTALLATION_ID"); err != nil {
		t.Errorf("Failed to unset GITHUB_APP_INSTALLATION_ID: %v", err)
	}

	config, err := GithubLoadConfig()
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "GITHUB_APP_PRIVATE_KEY is required")
}

func TestNewGithubSession_InvalidInputs(t *testing.T) {
	// Mock invalid inputs
	pem := ""
	appID := "invalid-app-id"
	installID := "invalid-install-id"

	session, err := NewGithubSession(pem, appID, installID)
	assert.Error(t, err)
	assert.Nil(t, session)
	assert.Contains(t, err.Error(), "error creating application token source")
}

func TestPullRequestOptions_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    *PullRequestOptions
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid options",
			opts: &PullRequestOptions{
				Title: "Test PR",
				Head:  "feature-branch",
				Base:  "main",
			},
			wantErr: false,
		},
		{
			name: "missing title",
			opts: &PullRequestOptions{
				Head: "feature-branch",
				Base: "main",
			},
			wantErr: true,
			errMsg:  "title is required",
		},
		{
			name: "missing head",
			opts: &PullRequestOptions{
				Title: "Test PR",
				Base:  "main",
			},
			wantErr: true,
			errMsg:  "head branch is required",
		},
		{
			name: "missing base",
			opts: &PullRequestOptions{
				Title: "Test PR",
				Head:  "feature-branch",
			},
			wantErr: true,
			errMsg:  "base branch is required",
		},
		{
			name: "same head and base",
			opts: &PullRequestOptions{
				Title: "Test PR",
				Head:  "main",
				Base:  "main",
			},
			wantErr: true,
			errMsg:  "head and base branches cannot be the same",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGithubSession_CreatePullRequest(t *testing.T) {
	tests := []struct {
		name           string
		opts           *PullRequestOptions
		mockResponse   func() *http.ServeMux
		wantErr        bool
		errMsg         string
		validateResult func(t *testing.T, pr *github.PullRequest)
	}{
		{
			name: "successful PR creation with basic options",
			opts: &PullRequestOptions{
				Title: "Test PR",
				Head:  "feature-branch",
				Base:  "main",
			},
			mockResponse: func() *http.ServeMux {
				mux := http.NewServeMux()
				mux.HandleFunc("/repos/owner/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "POST" {
						t.Errorf("Expected POST request, got %s", r.Method)
					}

					pr := &github.PullRequest{
						Number: github.Ptr(1),
						Title:  github.Ptr("Test PR"),
						Head: &github.PullRequestBranch{
							Ref: github.Ptr("feature-branch"),
						},
						Base: &github.PullRequestBranch{
							Ref: github.Ptr("main"),
						},
					}

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					json.NewEncoder(w).Encode(pr)
				})
				return mux
			},
			wantErr: false,
			validateResult: func(t *testing.T, pr *github.PullRequest) {
				assert.Equal(t, 1, pr.GetNumber())
				assert.Equal(t, "Test PR", pr.GetTitle())
				assert.Equal(t, "feature-branch", pr.GetHead().GetRef())
				assert.Equal(t, "main", pr.GetBase().GetRef())
			},
		},
		{
			name: "successful PR creation with all options",
			opts: &PullRequestOptions{
				Title:               "Test PR with options",
				Head:                "feature-branch",
				Base:                "main",
				Body:                github.Ptr("Test body"),
				Draft:               github.Ptr(true),
				MaintainerCanModify: github.Ptr(true),
				Assignees:           []string{"user1"},
				Reviewers:           []string{"reviewer1"},
				TeamReviewers:       []string{"team1"},
				Labels:              []string{"bug", "enhancement"},
				Milestone:           github.Ptr(1),
			},
			mockResponse: func() *http.ServeMux {
				mux := http.NewServeMux()

				// Mock PR creation
				mux.HandleFunc("/repos/owner/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
					pr := &github.PullRequest{
						Number: github.Ptr(1),
						Title:  github.Ptr("Test PR with options"),
						Head: &github.PullRequestBranch{
							Ref: github.Ptr("feature-branch"),
						},
						Base: &github.PullRequestBranch{
							Ref: github.Ptr("main"),
						},
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					json.NewEncoder(w).Encode(pr)
				})

				// Mock assignees
				mux.HandleFunc("/repos/owner/repo/issues/1/assignees", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
					json.NewEncoder(w).Encode(map[string]interface{}{})
				})

				// Mock reviewers
				mux.HandleFunc("/repos/owner/repo/pulls/1/requested_reviewers", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
					json.NewEncoder(w).Encode(map[string]interface{}{})
				})

				// Mock labels
				mux.HandleFunc("/repos/owner/repo/issues/1/labels", func(w http.ResponseWriter, r *http.Request) {
					labels := []*github.Label{
						{Name: github.Ptr("bug")},
						{Name: github.Ptr("enhancement")},
					}
					w.WriteHeader(http.StatusCreated)
					json.NewEncoder(w).Encode(labels)
				})

				// Mock milestone
				mux.HandleFunc("/repos/owner/repo/issues/1", func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "PATCH" {
						t.Errorf("Expected PATCH request for milestone, got %s", r.Method)
					}
					w.WriteHeader(http.StatusOK)
					json.NewEncoder(w).Encode(map[string]interface{}{})
				})

				return mux
			},
			wantErr: false,
			validateResult: func(t *testing.T, pr *github.PullRequest) {
				assert.Equal(t, 1, pr.GetNumber())
				assert.Equal(t, "Test PR with options", pr.GetTitle())
			},
		},
		{
			name:    "nil options",
			opts:    nil,
			wantErr: true,
			errMsg:  "pull request options cannot be nil",
		},
		{
			name: "invalid options",
			opts: &PullRequestOptions{
				Head: "feature-branch",
				Base: "main",
				// Missing title
			},
			wantErr: true,
			errMsg:  "invalid pull request options",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockResponse != nil {
				server := httptest.NewServer(tt.mockResponse())
				defer server.Close()

				// Create a session with a mock client
				session := &GithubSession{}
				client := github.NewClient(nil)
				url, _ := url.Parse(server.URL + "/")
				client.BaseURL = url
				session.client = client

				ctx := context.Background()
				pr, err := session.CreatePullRequest(ctx, "owner", "repo", tt.opts)

				if tt.wantErr {
					assert.Error(t, err)
					assert.Contains(t, err.Error(), tt.errMsg)
					assert.Nil(t, pr)
				} else {
					assert.NoError(t, err)
					assert.NotNil(t, pr)
					if tt.validateResult != nil {
						tt.validateResult(t, pr)
					}
				}
			} else {
				// Test cases that don't need HTTP mocking
				session := &GithubSession{}
				ctx := context.Background()
				pr, err := session.CreatePullRequest(ctx, "owner", "repo", tt.opts)

				if tt.wantErr {
					assert.Error(t, err)
					assert.Contains(t, err.Error(), tt.errMsg)
					assert.Nil(t, pr)
				} else {
					assert.NoError(t, err)
					assert.NotNil(t, pr)
				}
			}
		})
	}
}

func TestGithubSession_CreatePullRequest_ClientNotInitialized(t *testing.T) {
	session := &GithubSession{} // No client initialized
	opts := &PullRequestOptions{
		Title: "Test PR",
		Head:  "feature-branch",
		Base:  "main",
	}

	ctx := context.Background()
	pr, err := session.CreatePullRequest(ctx, "owner", "repo", opts)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "github client is not initialized")
	assert.Nil(t, pr)
}

func TestGithubSession_CreatePullRequestSimple(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/owner/repo/pulls" && r.Method == "POST" {
			pr := &github.PullRequest{
				Number: github.Ptr(1),
				Title:  github.Ptr("Simple PR"),
				Head: &github.PullRequestBranch{
					Ref: github.Ptr("feature"),
				},
				Base: &github.PullRequestBranch{
					Ref: github.Ptr("main"),
				},
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(pr)
		}
	}))
	defer server.Close()

	session := &GithubSession{}
	client := github.NewClient(nil)
	url, _ := url.Parse(server.URL + "/")
	client.BaseURL = url
	session.client = client

	ctx := context.Background()
	pr, err := session.CreatePullRequestSimple(ctx, "owner", "repo", "Simple PR", "feature", "main")

	assert.NoError(t, err)
	assert.NotNil(t, pr)
	assert.Equal(t, 1, pr.GetNumber())
	assert.Equal(t, "Simple PR", pr.GetTitle())
	assert.Equal(t, "feature", pr.GetHead().GetRef())
	assert.Equal(t, "main", pr.GetBase().GetRef())
}

// Benchmark tests
func BenchmarkPullRequestOptions_Validate(b *testing.B) {
	opts := &PullRequestOptions{
		Title: "Benchmark PR",
		Head:  "feature-branch",
		Base:  "main",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = opts.Validate()
	}
}

// Example test demonstrating usage
func ExampleGithubSession_CreatePullRequest() {
	// This example shows how to use the new extensible CreatePullRequest function
	session := &GithubSession{} // Assume properly initialized

	opts := &PullRequestOptions{
		Title:               "Add new feature",
		Head:                "feature-branch",
		Base:                "main",
		Body:                github.Ptr("This PR adds a new feature with comprehensive tests"),
		Draft:               github.Ptr(false),
		MaintainerCanModify: github.Ptr(true),
		Assignees:           []string{"developer1"},
		Reviewers:           []string{"reviewer1", "reviewer2"},
		Labels:              []string{"enhancement", "needs-review"},
		Milestone:           github.Ptr(1),
	}

	ctx := context.Background()
	pr, err := session.CreatePullRequest(ctx, "owner", "repo", opts)
	if err != nil {
		fmt.Printf("Error creating PR: %v\n", err)
		return
	}

	fmt.Printf("Created PR #%d: %s\n", pr.GetNumber(), pr.GetTitle())
}
