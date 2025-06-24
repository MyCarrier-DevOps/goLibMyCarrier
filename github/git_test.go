package github_handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	ghhttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/google/go-github/v69/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// SimpleGithubSession is a simple implementation for testing
type SimpleGithubSession struct {
	token  *oauth2.Token
	client *github.Client
}

func (s *SimpleGithubSession) AuthToken() *oauth2.Token {
	return s.token
}

func (s *SimpleGithubSession) Client() *github.Client {
	return s.client
}

func TestCloneRepository_NoAuthToken(t *testing.T) {
	session := &SimpleGithubSession{
		token: nil,
	}

	tempDir, err := os.MkdirTemp("", "test-clone-*")
	require.NoError(t, err)
	defer func() {
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			t.Logf("failed to remove temp dir: %v", rmErr)
		}
	}()

	_, err = CloneRepository(session, "https://github.com/test/repo.git", tempDir, "main", nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error getting auth token for GitHub session")
}

func TestCloneRepository_DefaultWorkdir(t *testing.T) {
	session := &SimpleGithubSession{
		token: &oauth2.Token{
			AccessToken: "test-token",
		},
	}

	// Test that default workdir is used when empty string is passed
	_, err := CloneRepository(session, "https://github.com/invalid/repo.git", "", "main", nil, nil)
	// We expect an error because the repo doesn't exist, but the function should have tried to use "/work"
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error cloning repository")
}

func TestCloneRepository_CustomCloneOptions(t *testing.T) {
	session := &SimpleGithubSession{
		token: &oauth2.Token{
			AccessToken: "test-token",
		},
	}

	tempDir, err := os.MkdirTemp("", "test-clone-*")
	require.NoError(t, err)
	defer func() {
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			t.Logf("failed to remove temp dir: %v", rmErr)
		}
	}()

	// Create custom clone options
	customAuth := &ghhttp.BasicAuth{
		Username: "custom-user",
		Password: "custom-token",
	}
	customOptions := &git.CloneOptions{
		URL:           "https://github.com/invalid/repo.git",
		SingleBranch:  true,
		Depth:         1,
		ReferenceName: plumbing.NewBranchReferenceName("main"),
		Auth:          customAuth,
	}

	_, err = CloneRepository(session, "https://github.com/invalid/repo.git", tempDir, "main", customAuth, customOptions)
	// We expect an error because the repo doesn't exist
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error cloning repository")
}

func TestCloneRepository_CustomBasicAuth(t *testing.T) {
	session := &SimpleGithubSession{
		token: &oauth2.Token{
			AccessToken: "test-token",
		},
	}

	tempDir, err := os.MkdirTemp("", "test-clone-*")
	require.NoError(t, err)
	defer func() {
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			t.Logf("failed to remove temp dir: %v", rmErr)
		}
	}()

	// Create custom basic auth
	customAuth := &ghhttp.BasicAuth{
		Username: "custom-user",
		Password: "custom-password",
	}

	_, err = CloneRepository(session, "https://github.com/invalid/repo.git", tempDir, "main", customAuth, nil)
	// We expect an error because the repo doesn't exist
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error cloning repository")
}

func TestCreateCheckRun_Success(t *testing.T) {
	// Create a test server to mock GitHub API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, "/repos/test-org/test-repo/check-runs")

		// Return a successful response
		checkRun := github.CheckRun{
			ID:   github.Ptr(int64(123)),
			Name: github.Ptr("test-check"),
		}
		w.WriteHeader(http.StatusCreated)
		if encErr := json.NewEncoder(w).Encode(checkRun); encErr != nil {
			t.Logf("failed to encode response: %v", encErr)
		}
	}))
	defer server.Close()

	// Create client with test server
	client := github.NewClient(nil)
	baseURL, _ := url.Parse(server.URL + "/")
	client.BaseURL = baseURL

	session := &SimpleGithubSession{
		client: client,
	}

	ctx := context.Background()
	err := CreateCheckRun(
		ctx,
		session,
		"abc123",       // headSha
		"test-repo",    // repo
		"test-org",     // org
		"test-check",   // name
		"Test Title",   // title
		"success",      // conclusion
		"completed",    // status
		"Test Summary", // summary
	)

	assert.NoError(t, err)
}

func TestCreateCheckRun_APIError(t *testing.T) {
	// Create a test server to mock GitHub API error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		if _, writeErr := w.Write([]byte(`{"message": "Validation Failed"}`)); writeErr != nil {
			t.Logf("failed to write response: %v", writeErr)
		}
	}))
	defer server.Close()

	// Create client with test server
	client := github.NewClient(nil)
	baseURL, _ := url.Parse(server.URL + "/")
	client.BaseURL = baseURL

	session := &SimpleGithubSession{
		client: client,
	}

	ctx := context.Background()
	err := CreateCheckRun(
		ctx,
		session,
		"abc123",
		"test-repo",
		"test-org",
		"test-check",
		"Test Title",
		"failure",
		"completed",
		"Test failed",
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error creating checkrun")
}

func TestCreateCheckRun_DifferentStatuses(t *testing.T) {
	testCases := []struct {
		name       string
		status     string
		conclusion string
	}{
		{"queued", "queued", ""},
		{"in_progress", "in_progress", ""},
		{"completed_success", "completed", "success"},
		{"completed_failure", "completed", "failure"},
		{"completed_cancelled", "completed", "cancelled"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a test server to verify the request
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Parse the request body to verify status and conclusion
				var opts github.CreateCheckRunOptions
				err := json.NewDecoder(r.Body).Decode(&opts)
				require.NoError(t, err)

				assert.Equal(t, tc.status, *opts.Status)
				if tc.conclusion != "" {
					assert.Equal(t, tc.conclusion, *opts.Conclusion)
				}

				// Return success
				checkRun := github.CheckRun{
					ID:   github.Ptr(int64(123)),
					Name: github.Ptr("test-check"),
				}
				w.WriteHeader(http.StatusCreated)
				if encErr := json.NewEncoder(w).Encode(checkRun); encErr != nil {
					t.Logf("failed to encode response: %v", encErr)
				}
			}))
			defer server.Close()

			// Create client with test server
			client := github.NewClient(nil)
			baseURL, _ := url.Parse(server.URL + "/")
			client.BaseURL = baseURL

			session := &SimpleGithubSession{
				client: client,
			}

			ctx := context.Background()
			err := CreateCheckRun(
				ctx,
				session,
				"abc123",
				"test-repo",
				"test-org",
				"test-check",
				"Test Title",
				tc.conclusion,
				tc.status,
				"Test Summary",
			)

			assert.NoError(t, err)
		})
	}
}
