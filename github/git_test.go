package github_handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	ghhttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/google/go-github/v73/github"
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

	_, err = CloneRepositorySimple(session, "https://github.com/test/repo.git", tempDir, "main")
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
	_, err := CloneRepositorySimple(session, "https://github.com/invalid/repo.git", "", "main")
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

	_, err = CloneRepositoryWithAuth(
		session,
		"https://github.com/invalid/repo.git",
		tempDir,
		"main",
		customAuth,
		customOptions,
	)
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

	_, err = CloneRepositoryWithAuth(session, "https://github.com/invalid/repo.git", tempDir, "main", customAuth, nil)
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
	err := CreateCheckRunSimple(
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
	err := CreateCheckRunSimple(
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
	assert.Contains(t, err.Error(), "error creating check run")
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
			err := CreateCheckRunSimple(
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

// Tests for commit functions

func TestCommitChanges_Success(t *testing.T) {
	// Create a temporary directory for the test repository
	tempDir, err := os.MkdirTemp("", "test-commit-*")
	require.NoError(t, err)
	defer func() {
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			t.Logf("failed to remove temp dir: %v", rmErr)
		}
	}()

	// Initialize a git repository
	repo, err := git.PlainInit(tempDir, false)
	require.NoError(t, err)

	// Create a test file
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	require.NoError(t, err)

	// Test commit with specific files
	options := CommitOptions{
		Branch:  "main",
		Message: "Test commit",
		Files:   []string{"test.txt"},
		Author: AuthorInfo{
			Name:  "Test User",
			Email: "test@example.com",
		},
		// No Auth means no push
	}

	commitHash, err := CommitChanges(repo, options)
	assert.NoError(t, err)
	assert.NotEmpty(t, commitHash)
}

func TestCommitChanges_AddAll(t *testing.T) {
	// Create a temporary directory for the test repository
	tempDir, err := os.MkdirTemp("", "test-commit-all-*")
	require.NoError(t, err)
	defer func() {
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			t.Logf("failed to remove temp dir: %v", rmErr)
		}
	}()

	// Initialize a git repository
	repo, err := git.PlainInit(tempDir, false)
	require.NoError(t, err)

	// Create multiple test files
	testFile1 := filepath.Join(tempDir, "test1.txt")
	testFile2 := filepath.Join(tempDir, "test2.txt")
	err = os.WriteFile(testFile1, []byte("test content 1"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(testFile2, []byte("test content 2"), 0644)
	require.NoError(t, err)

	// Test commit with AddAll
	options := CommitOptions{
		Branch:  "main",
		Message: "Test commit all",
		AddAll:  true,
		Author:  GetDefaultAuthor(),
	}

	commitHash, err := CommitChanges(repo, options)
	assert.NoError(t, err)
	assert.NotEmpty(t, commitHash)
}

func TestCommitChanges_ValidationErrors(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-commit-validation-*")
	require.NoError(t, err)
	defer func() {
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			t.Logf("failed to remove temp dir: %v", rmErr)
		}
	}()

	repo, err := git.PlainInit(tempDir, false)
	require.NoError(t, err)

	testCases := []struct {
		name          string
		options       CommitOptions
		expectedError string
	}{
		{
			name: "missing branch",
			options: CommitOptions{
				Message: "Test commit",
				Files:   []string{"test.txt"},
			},
			expectedError: "branch is required",
		},
		{
			name: "missing message",
			options: CommitOptions{
				Branch: "main",
				Files:  []string{"test.txt"},
			},
			expectedError: "commit message is required",
		},
		{
			name: "no files and no AddAll",
			options: CommitOptions{
				Branch:  "main",
				Message: "Test commit",
			},
			expectedError: "either specify files to add or set AddAll to true",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CommitChanges(repo, tc.options)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedError)
		})
	}
}

func TestCommitChanges_NonexistentFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-commit-nonexistent-*")
	require.NoError(t, err)
	defer func() {
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			t.Logf("failed to remove temp dir: %v", rmErr)
		}
	}()

	repo, err := git.PlainInit(tempDir, false)
	require.NoError(t, err)

	options := CommitOptions{
		Branch:  "main",
		Message: "Test commit",
		Files:   []string{"nonexistent.txt"},
		Author:  GetDefaultAuthor(),
	}

	_, err = CommitChanges(repo, options)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error adding file")
}

func TestCommitChanges_DefaultAuthor(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-commit-default-author-*")
	require.NoError(t, err)
	defer func() {
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			t.Logf("failed to remove temp dir: %v", rmErr)
		}
	}()

	repo, err := git.PlainInit(tempDir, false)
	require.NoError(t, err)

	// Create a test file
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	require.NoError(t, err)

	// Test with empty author (should use default)
	options := CommitOptions{
		Branch:  "main",
		Message: "Test commit with default author",
		Files:   []string{"test.txt"},
		Author:  AuthorInfo{}, // Empty author
	}

	commitHash, err := CommitChanges(repo, options)
	assert.NoError(t, err)
	assert.NotEmpty(t, commitHash)

	// Verify the commit exists and has the correct author
	commitObj, err := repo.CommitObject(plumbing.NewHash(commitHash))
	assert.NoError(t, err)
	defaultAuthor := GetDefaultAuthor()
	assert.Equal(t, defaultAuthor.Name, commitObj.Author.Name)
	assert.Equal(t, defaultAuthor.Email, commitObj.Author.Email)
}

func TestCommitChangesWithToken_Success(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-commit-token-*")
	require.NoError(t, err)
	defer func() {
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			t.Logf("failed to remove temp dir: %v", rmErr)
		}
	}()

	repo, err := git.PlainInit(tempDir, false)
	require.NoError(t, err)

	// Create a test file
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	require.NoError(t, err)

	// Test convenience function (without actual push since we don't have a remote)
	// This will fail at push stage, but that's expected
	_, err = CommitChangesWithToken(repo, "main", "Test commit", []string{"test.txt"}, "fake-token")

	// The commit should succeed, but push will fail (which is expected without a remote)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error pushing changes")
}

func TestCommitAllChangesWithToken_Success(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-commit-all-token-*")
	require.NoError(t, err)
	defer func() {
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			t.Logf("failed to remove temp dir: %v", rmErr)
		}
	}()

	repo, err := git.PlainInit(tempDir, false)
	require.NoError(t, err)

	// Create test files
	testFile1 := filepath.Join(tempDir, "test1.txt")
	testFile2 := filepath.Join(tempDir, "test2.txt")
	err = os.WriteFile(testFile1, []byte("test content 1"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(testFile2, []byte("test content 2"), 0644)
	require.NoError(t, err)

	// Test convenience function for all changes
	_, err = CommitAllChangesWithToken(repo, "main", "Test commit all", "fake-token")

	// The commit should succeed, but push will fail (which is expected without a remote)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error pushing changes")
}

func TestCommitChanges_WithMockPush(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-commit-mock-push-*")
	require.NoError(t, err)
	defer func() {
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			t.Logf("failed to remove temp dir: %v", rmErr)
		}
	}()

	// Initialize a git repository directly (simpler than cloning from bare)
	repo, err := git.PlainInit(tempDir, false)
	require.NoError(t, err)

	// Create a test file
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	require.NoError(t, err)

	// Test commit with auth (push will fail since there's no remote, but that's expected)
	options := CommitOptions{
		Branch:  "main",
		Message: "Test commit with auth",
		Files:   []string{"test.txt"},
		Author:  GetDefaultAuthor(),
		Auth: &ghhttp.BasicAuth{
			Username: "test",
			Password: "test",
		},
	}

	commitHash, err := CommitChanges(repo, options)
	// The commit should succeed, but push will fail due to no remote
	// The function should still return the commit hash even when push fails
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error pushing changes")
	assert.NotEmpty(t, commitHash)
}

func TestCommitChanges_MultipleFiles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-commit-multiple-*")
	require.NoError(t, err)
	defer func() {
		if rmErr := os.RemoveAll(tempDir); rmErr != nil {
			t.Logf("failed to remove temp dir: %v", rmErr)
		}
	}()

	repo, err := git.PlainInit(tempDir, false)
	require.NoError(t, err)

	// Create multiple test files
	files := []string{"file1.txt", "file2.txt", "subdir/file3.txt"}

	// Create subdirectory
	err = os.MkdirAll(filepath.Join(tempDir, "subdir"), 0755)
	require.NoError(t, err)

	for i, file := range files {
		fullPath := filepath.Join(tempDir, file)
		err = os.WriteFile(fullPath, []byte(fmt.Sprintf("content %d", i+1)), 0644)
		require.NoError(t, err)
	}

	// Commit multiple specific files
	options := CommitOptions{
		Branch:  "main",
		Message: "Test multiple files commit",
		Files:   files,
		Author: AuthorInfo{
			Name:  "Multi File Test",
			Email: "multifile@test.com",
		},
	}

	commitHash, err := CommitChanges(repo, options)
	assert.NoError(t, err)
	assert.NotEmpty(t, commitHash)

	// Verify all files were committed
	commitObj, err := repo.CommitObject(plumbing.NewHash(commitHash))
	assert.NoError(t, err)

	tree, err := commitObj.Tree()
	assert.NoError(t, err)

	// Check that files exist in the tree
	for _, file := range files {
		_, err := tree.File(file)
		assert.NoError(t, err, "File %s should exist in commit", file)
	}
}
