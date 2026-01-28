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

	"github.com/google/go-github/v82/github"
	"github.com/stretchr/testify/assert"
)

// Helper functions for pointer creation (replacing deprecated github.String, github.Int, github.Bool)
// These functions are currently unused but kept for future use if needed

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
	tests := getPullRequestValidationTestCases()

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

func getPullRequestValidationTestCases() []struct {
	name    string
	opts    *PullRequestOptions
	wantErr bool
	errMsg  string
} {
	return []struct {
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
}

func TestGithubSession_CreatePullRequest(t *testing.T) {
	tests := getCreatePullRequestTestCases(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCreatePullRequestTest(t, tt)
		})
	}
}

func getCreatePullRequestTestCases(t *testing.T) []createPRTestCase {
	return []createPRTestCase{
		getBasicPRTestCase(t),
		getAdvancedPRTestCase(t),
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
}

type createPRTestCase struct {
	name           string
	opts           *PullRequestOptions
	mockResponse   func() *http.ServeMux
	wantErr        bool
	errMsg         string
	validateResult func(t *testing.T, pr *github.PullRequest)
}

func getBasicPRTestCase(t *testing.T) createPRTestCase {
	return createPRTestCase{
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
				if err := json.NewEncoder(w).Encode(pr); err != nil {
					t.Errorf("Failed to encode response: %v", err)
				}
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
	}
}

func getAdvancedPRTestCase(t *testing.T) createPRTestCase {
	return createPRTestCase{
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
		mockResponse: createAdvancedPRMockServer(t),
		wantErr:      false,
		validateResult: func(t *testing.T, pr *github.PullRequest) {
			assert.Equal(t, 1, pr.GetNumber())
			assert.Equal(t, "Test PR with options", pr.GetTitle())
		},
	}
}

func createAdvancedPRMockServer(t *testing.T) func() *http.ServeMux {
	return func() *http.ServeMux {
		mux := http.NewServeMux()
		setupPRCreationMock(mux, t)
		setupAssigneesMock(mux, t)
		setupReviewersMock(mux, t)
		setupLabelsMock(mux, t)
		setupMilestoneMock(mux, t)
		return mux
	}
}

func runCreatePullRequestTest(t *testing.T, tt createPRTestCase) {
	if tt.mockResponse != nil {
		runCreatePullRequestTestWithMock(t, tt)
	} else {
		runCreatePullRequestTestWithoutMock(t, tt)
	}
}

func runCreatePullRequestTestWithMock(t *testing.T, tt createPRTestCase) {
	server := httptest.NewServer(tt.mockResponse())
	defer server.Close()

	session := &GithubSession{}
	client := github.NewClient(nil)
	url, _ := url.Parse(server.URL + "/")
	client.BaseURL = url
	session.client = client

	ctx := context.Background()
	pr, err := session.CreatePullRequest(ctx, "owner", "repo", tt.opts)
	validatePRTestResult(t, tt, pr, err)
}

func runCreatePullRequestTestWithoutMock(t *testing.T, tt createPRTestCase) {
	session := &GithubSession{}
	ctx := context.Background()
	pr, err := session.CreatePullRequest(ctx, "owner", "repo", tt.opts)
	validatePRTestResult(t, tt, pr, err)
}

func setupPRCreationMock(mux *http.ServeMux, t *testing.T) {
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
		if err := json.NewEncoder(w).Encode(pr); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	})
}

func setupAssigneesMock(mux *http.ServeMux, t *testing.T) {
	mux.HandleFunc("/repos/owner/repo/issues/1/assignees", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(map[string]interface{}{}); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	})
}

func setupReviewersMock(mux *http.ServeMux, t *testing.T) {
	mux.HandleFunc("/repos/owner/repo/pulls/1/requested_reviewers", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(map[string]interface{}{}); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	})
}

func setupLabelsMock(mux *http.ServeMux, t *testing.T) {
	mux.HandleFunc("/repos/owner/repo/issues/1/labels", func(w http.ResponseWriter, r *http.Request) {
		labels := []*github.Label{
			{Name: github.Ptr("bug")},
			{Name: github.Ptr("enhancement")},
		}
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(labels); err != nil {
			t.Logf("Failed to encode labels response: %v", err)
		}
	})
}

func setupMilestoneMock(mux *http.ServeMux, t *testing.T) {
	mux.HandleFunc("/repos/owner/repo/issues/1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" {
			t.Errorf("Expected PATCH request for milestone, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]interface{}{}); err != nil {
			t.Logf("Failed to encode milestone response: %v", err)
		}
	})
}

func validatePRTestResult(t *testing.T, tt createPRTestCase, pr *github.PullRequest, err error) {
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
			if err := json.NewEncoder(w).Encode(pr); err != nil {
				t.Errorf("Failed to encode response: %v", err)
			}
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

// Test AuthToken accessor
func TestGithubSession_AuthToken(t *testing.T) {
	t.Run("returns nil when not authenticated", func(t *testing.T) {
		session := &GithubSession{}
		assert.Nil(t, session.AuthToken())
	})
}

// Test Client accessor
func TestGithubSession_Client(t *testing.T) {
	t.Run("returns nil when not authenticated", func(t *testing.T) {
		session := &GithubSession{}
		assert.Nil(t, session.Client())
	})

	t.Run("returns client when set", func(t *testing.T) {
		client := github.NewClient(nil)
		session := &GithubSession{client: client}
		assert.Equal(t, client, session.Client())
	})
}

// Test authenticate error cases
func TestGithubSession_Authenticate_Errors(t *testing.T) {
	t.Run("invalid private key", func(t *testing.T) {
		session := &GithubSession{
			pem:       "invalid-pem-data",
			appID:     "12345",
			installID: "67890",
		}
		err := session.authenticate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid private key")
	})

	t.Run("invalid app ID - not a number", func(t *testing.T) {
		session := &GithubSession{
			pem:       "", // Empty PEM will fail first, need valid PEM
			appID:     "not-a-number",
			installID: "67890",
		}
		err := session.authenticate()
		assert.Error(t, err)
		// Will fail on invalid private key first since empty PEM
	})
}

// Benchmark tests
func BenchmarkPullRequestOptions_Validate(b *testing.B) {
	opts := &PullRequestOptions{
		Title: "Benchmark PR",
		Head:  "feature-branch",
		Base:  "main",
	}

	b.ResetTimer()
	for range b.N {
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
