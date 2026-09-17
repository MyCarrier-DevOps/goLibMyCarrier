package github_handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-github/v82/github"
	"github.com/jferrl/go-githubauth"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
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

// TestGithubSession_Authenticate_ErrorWrapping verifies that authenticate()
// consistently wraps errors with %w so callers can use errors.Is / errors.As.
func TestGithubSession_Authenticate_ErrorWrapping(t *testing.T) {
	validPEM := testPrivateKey(t)

	t.Run("invalid appID wraps with strconv.ErrSyntax", func(t *testing.T) {
		session := &GithubSession{
			pem:       validPEM,
			appID:     "not-a-number",
			installID: "67890",
		}
		err := session.authenticate()
		assert.Error(t, err)
		assert.ErrorIs(t, err, strconv.ErrSyntax, "appID parse error should wrap the original strconv error")
	})

	t.Run("invalid installID wraps with strconv.ErrSyntax", func(t *testing.T) {
		session := &GithubSession{
			pem:       validPEM,
			appID:     "12345",
			installID: "not-a-number",
		}
		err := session.authenticate()
		assert.Error(t, err)
		assert.ErrorIs(t, err, strconv.ErrSyntax, "installID parse error should wrap the original strconv error")
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

// TestGithubLoadConfigFromViper_CallerViperWins verifies that values set
// explicitly on the caller-provided viper instance are honoured by the
// constructor, even when no GITHUB_* env vars are bound on that viper.
// This is the key property that lets callers inject secrets via vp.Set
// without having to mutate process environment.
func TestGithubLoadConfigFromViper_CallerViperWins(t *testing.T) {
	// Explicitly clear env so we can prove caller-provided values are sourced
	// from the viper instance, not from environment fall-through.
	if err := os.Unsetenv("GITHUB_APP_PRIVATE_KEY"); err != nil {
		t.Fatalf("Failed to unset GITHUB_APP_PRIVATE_KEY: %v", err)
	}
	if err := os.Unsetenv("GITHUB_APP_ID"); err != nil {
		t.Fatalf("Failed to unset GITHUB_APP_ID: %v", err)
	}
	if err := os.Unsetenv("GITHUB_APP_INSTALLATION_ID"); err != nil {
		t.Fatalf("Failed to unset GITHUB_APP_INSTALLATION_ID: %v", err)
	}

	vp := viper.New()
	// PEM must be >= 10 chars to pass validation; use an obviously fake value.
	vp.Set("pem", "explicit-test-pem-payload")
	vp.Set("app_id", "99999")
	vp.Set("install_id", "88888")

	cfg, err := GithubLoadConfigFromViper(vp)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "explicit-test-pem-payload", cfg.Pem)
	assert.Equal(t, "99999", cfg.AppId)
	assert.Equal(t, "88888", cfg.InstallId)
}

// TestGithubLoadConfigFromViper_NilViper guards against nil deref.
func TestGithubLoadConfigFromViper_NilViper(t *testing.T) {
	cfg, err := GithubLoadConfigFromViper(nil)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	// Primary assertion: callers should be able to match the sentinel via errors.Is.
	assert.True(t, errors.Is(err, ErrNilViper), "expected error to wrap ErrNilViper, got: %v", err)
	// Belt-and-suspenders: preserve string-match check until callers migrate.
	assert.Contains(t, err.Error(), "viper instance cannot be nil")
}

// --- DEVOPS-342: the HTTP calls made while authenticating must be bounded ---

// stallSafetyValve caps how long a stalling test handler blocks. httptest's
// Close waits for outstanding requests, so without this a regression would hang
// the whole test binary instead of failing the one test that caught it.
const stallSafetyValve = 30 * time.Second

// stallHandler returns a handler that blocks until the client gives up. When
// writeHeaderFirst is true it sends response headers before stalling, so the
// caller chooses which bound is under test: ResponseHeaderTimeout (no headers
// ever arrive) or the overall http.Client.Timeout (headers arrive, body stalls).
func stallHandler(writeHeaderFirst bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if writeHeaderFirst {
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		select {
		case <-r.Context().Done():
		case <-time.After(stallSafetyValve):
		}
	}
}

// getAndDrain performs a GET and reads the body, returning the first error from
// either step. A response whose headers arrive but whose body never does only
// fails on the read, which is exactly what an overall timeout has to catch.
func getAndDrain(client *http.Client, url string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

func TestBoundedHTTPClient_StalledServerDoesNotHangForever(t *testing.T) {
	// Far longer than any bound under test: reaching it means "no bound at all".
	const generousDeadline = 10 * time.Second

	tests := []struct {
		name                  string
		handler               http.HandlerFunc
		timeout               time.Duration
		responseHeaderTimeout time.Duration
		wantErr               bool
	}{
		{
			name:                  "server stalls before sending response headers",
			handler:               stallHandler(false),
			timeout:               5 * time.Second,
			responseHeaderTimeout: 100 * time.Millisecond,
			wantErr:               true,
		},
		{
			name:                  "server sends headers then stalls the body",
			handler:               stallHandler(true),
			timeout:               200 * time.Millisecond,
			responseHeaderTimeout: 5 * time.Second,
			wantErr:               true,
		},
		{
			name: "responsive server is unaffected by the bounds",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("ok"))
			},
			timeout:               5 * time.Second,
			responseHeaderTimeout: 5 * time.Second,
			wantErr:               false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			t.Cleanup(server.Close)

			client := boundedHTTPClient(tt.timeout, tt.responseHeaderTimeout)

			done := make(chan error, 1)
			go func() { done <- getAndDrain(client, server.URL) }()

			select {
			case err := <-done:
				if tt.wantErr {
					require.Error(t, err, "a stalled server must surface an error, not block")
				} else {
					require.NoError(t, err)
				}
			case <-time.After(generousDeadline):
				t.Fatalf("request did not return within %s: the client is unbounded", generousDeadline)
			}
		})
	}
}

func TestBoundedHTTPClient_KeepsDialAndTLSBounds(t *testing.T) {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok, "http.DefaultTransport is expected to be an *http.Transport")

	client := boundedHTTPClient(tokenMintTimeout, tokenMintResponseHeaderTimeout)
	require.Equal(t, tokenMintTimeout, client.Timeout, "overall timeout must be set")

	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok, "bounded client must carry its own *http.Transport")

	assert.Equal(t, tokenMintResponseHeaderTimeout, transport.ResponseHeaderTimeout)
	assert.Equal(t, defaultTransport.TLSHandshakeTimeout, transport.TLSHandshakeTimeout,
		"TLS handshake bound must be kept, not discarded")
	assert.Equal(t, defaultTransport.ExpectContinueTimeout, transport.ExpectContinueTimeout)
	assert.NotNil(t, transport.DialContext, "dial bound must be kept, not discarded")
	assert.NotNil(t, transport.Proxy, "proxy support must be kept")

	assert.NotSame(t, defaultTransport, transport, "must clone rather than mutate http.DefaultTransport")
	assert.Zero(t, defaultTransport.ResponseHeaderTimeout, "http.DefaultTransport must be left untouched")
}

// TestInstallationTokenMint_IsBounded is the regression test for the wedged
// worker: go-githubauth's own client sets no overall timeout and no
// ResponseHeaderTimeout, so before this bound a stalled token endpoint never
// returned. It exercises the production constants through go-githubauth's real
// plumbing, so it takes about tokenMintResponseHeaderTimeout to run.
func TestInstallationTokenMint_IsBounded(t *testing.T) {
	// Generous: anything short of this proves a bound exists, and reaching it
	// proves one does not.
	deadline := tokenMintTimeout + 10*time.Second

	server := httptest.NewServer(stallHandler(false))
	t.Cleanup(server.Close)

	appTokenSource, err := githubauth.NewApplicationTokenSource(int64(12345), []byte(testPrivateKey(t)))
	require.NoError(t, err)

	tokenSource := githubauth.NewInstallationTokenSource(
		int64(67890),
		appTokenSource,
		githubauth.WithHTTPClient(boundedHTTPClient(tokenMintTimeout, tokenMintResponseHeaderTimeout)),
		githubauth.WithBaseURL(server.URL),
	)

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, tokenErr := tokenSource.Token()
		done <- tokenErr
	}()

	select {
	case tokenErr := <-done:
		require.Error(t, tokenErr, "a stalled token endpoint must surface an error")
		assert.Less(t, time.Since(start), deadline)
	case <-time.After(deadline):
		t.Fatalf("token mint did not return within %s: the worker calling it would be wedged", deadline)
	}
}

// TestOAuth2Client_InheritsBoundedBase pins the subtlety in authenticate():
// oauth2.NewClient takes its base transport, and its overall timeout, from the
// client stored in the context under oauth2.HTTPClient. Injecting there is what
// bounds the client handed to github.NewClient.
func TestOAuth2Client_InheritsBoundedBase(t *testing.T) {
	const (
		timeout               = 500 * time.Millisecond
		responseHeaderTimeout = 200 * time.Millisecond
		generousDeadline      = 10 * time.Second
	)

	server := httptest.NewServer(stallHandler(false))
	t.Cleanup(server.Close)

	base := boundedHTTPClient(timeout, responseHeaderTimeout)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, base)
	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"}))

	assert.Equal(t, timeout, client.Timeout, "oauth2 client must inherit the bounded overall timeout")

	done := make(chan error, 1)
	go func() { done <- getAndDrain(client, server.URL) }()

	select {
	case err := <-done:
		require.Error(t, err, "a stalled API must surface an error, not block")
	case <-time.After(generousDeadline):
		t.Fatalf("oauth2 client did not return within %s: it is unbounded", generousDeadline)
	}
}
