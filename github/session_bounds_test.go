package github_handler

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jferrl/go-githubauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// --- DEVOPS-342: every HTTP call this package makes to GitHub is bounded ---
//
// These tests drive the functions production uses — newInstallationClient, which
// authenticate() calls, and NewGraphQLClient — against a local server, with the
// bounds shortened through the same config production fills in. Removing any one
// of the bounds from those functions fails a test here.

const (
	// shortBound is the bound under test: short, so waiting it out is cheap.
	shortBound = 200 * time.Millisecond

	// boundSlack is how far past shortBound a bounded call may return. The
	// elapsed-time assertions use shortBound+boundSlack, which is well inside
	// testDeadline, so they are not implied by the select that enforces it.
	boundSlack = time.Second

	// testDeadline is how long a test waits before declaring a call unbounded.
	// It is far longer than shortBound+boundSlack and far shorter than any
	// production bound, including go-githubauth's own 30s header timeout, so
	// falling back to an unbounded or default client fails the test.
	testDeadline = 5 * time.Second

	// stallSafetyValve caps how long a stalling handler blocks, so a regression
	// fails its test instead of hanging the binary in httptest's Close.
	stallSafetyValve = 30 * time.Second

	testInstallationID = int64(67890)
	testTokenPath      = "/app/installations/67890/access_tokens"
	testAccessToken    = "ghs_test_token"
)

// stallHandler blocks until the client gives up. With writeHeaderFirst it sends
// response headers before stalling, which selects the bound under test: the
// response-header bound (no headers ever arrive) or the overall timeout
// (headers arrive, the body never does).
//
// It reads the request body first: the server only notices a client that has
// gone away, and cancels r.Context(), once the body has been consumed.
func stallHandler(writeHeaderFirst bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
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

// tokenHandler answers an installation-token mint.
func tokenHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"token":      testAccessToken,
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
}

// newStallServer starts a server and, on cleanup, drops its client connections
// before closing it, so a handler still stalling on a test that already failed
// does not hold Close for the whole safety valve.
func newStallServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	return server
}

// newConnCountingServer starts a server that counts the connections opened to it.
func newConnCountingServer(t *testing.T, handler http.Handler, newConns *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConns.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	return server
}

// testAppTokenSource returns a GitHub App JWT source for a throwaway key.
func testAppTokenSource(t *testing.T) oauth2.TokenSource {
	t.Helper()
	app, err := githubauth.NewApplicationTokenSource(int64(12345), []byte(testPrivateKey(t)))
	require.NoError(t, err)
	return app
}

// testSessionConfig is the production default config with its bounds shortened
// and its mint pointed at serverURL.
func testSessionConfig(serverURL string) sessionConfig {
	cfg := defaultSessionConfig()
	cfg.mintTimeout = shortBound
	cfg.requestTimeout = shortBound
	cfg.transport = newBoundedTransport(shortBound)
	cfg.mintBaseURL = serverURL
	return cfg
}

// runBounded runs call and fails the test if it has not returned by testDeadline.
func runBounded(t *testing.T, call func() error) (error, time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err, time.Since(start)
	case <-time.After(testDeadline):
		t.Fatalf("call did not return within %s: it is unbounded", testDeadline)
		return nil, 0
	}
}

// getAndDrain performs a GET and reads the body, returning the first error from
// either step: a body that stalls after its headers only fails on the read.
func getAndDrain(client *http.Client, url string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

func TestNewInstallationClient_MintIsOneBoundedRequest(t *testing.T) {
	tests := []struct {
		name          string
		handler       http.HandlerFunc
		wantErr       bool
		wantRateLimit bool
	}{
		{name: "token endpoint answers", handler: tokenHandler},
		{name: "token endpoint stalls before headers", handler: stallHandler(false), wantErr: true},
		{name: "token endpoint stalls the body", handler: stallHandler(true), wantErr: true},
		{
			// go-githubauth sleeps Retry-After and retries once by default, outside
			// any client timeout. The mint must fail at once instead.
			name: "429 with Retry-After",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "3")
				w.WriteHeader(http.StatusTooManyRequests)
			},
			wantErr:       true,
			wantRateLimit: true,
		},
		{
			// With no hint go-githubauth falls back to a 60s sleep.
			name: "rate-limit 403 with no hint",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.WriteHeader(http.StatusForbidden)
			},
			wantErr:       true,
			wantRateLimit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc(testTokenPath, tt.handler)
			server := newStallServer(t, mux)

			tokenSource, _ := newInstallationClient(testAppTokenSource(t), testInstallationID, testSessionConfig(server.URL))

			var token *oauth2.Token
			err, elapsed := runBounded(t, func() error {
				var tokenErr error
				token, tokenErr = tokenSource.Token()
				return tokenErr
			})

			assert.Less(t, elapsed, shortBound+boundSlack, "a mint is one request, bounded by the mint timeout")
			if !tt.wantErr {
				require.NoError(t, err)
				assert.Equal(t, testAccessToken, token.AccessToken)
				return
			}
			require.Error(t, err)
			if tt.wantRateLimit {
				assert.ErrorIs(t, err, githubauth.ErrRateLimited, "a throttled mint must be recognisable to the caller that retries it")
			}
		})
	}
}

func TestNewInstallationClient_RESTClientIsBoundedAndAuthenticated(t *testing.T) {
	var gotAuth atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc(testTokenPath, tokenHandler)
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/stall-headers", stallHandler(false))
	mux.HandleFunc("/stall-body", stallHandler(true))
	server := newStallServer(t, mux)

	cfg := testSessionConfig(server.URL)
	tokenSource, client := newInstallationClient(testAppTokenSource(t), testInstallationID, cfg)
	_, err := tokenSource.Token()
	require.NoError(t, err)

	assert.Equal(t, cfg.requestTimeout, client.Timeout, "the REST client carries the session's request timeout")

	err, _ = runBounded(t, func() error { return getAndDrain(client, server.URL+"/ok") })
	require.NoError(t, err)
	assert.Equal(t, "Bearer "+testAccessToken, gotAuth.Load(), "the bounded client must keep the token transport")

	for _, path := range []string{"/stall-headers", "/stall-body"} {
		t.Run(path, func(t *testing.T) {
			err, elapsed := runBounded(t, func() error { return getAndDrain(client, server.URL+path) })
			require.Error(t, err, "a stalled API must surface an error, not block")
			assert.Less(t, elapsed, shortBound+boundSlack)
		})
	}
}

// TestNewInstallationClient_SessionsShareOneConnectionPool pins that sessions
// reuse connections to GitHub. A consumer that builds a session per message
// would otherwise pay a TCP and TLS handshake for every one, and leave that
// session's idle connections behind.
func TestNewInstallationClient_SessionsShareOneConnectionPool(t *testing.T) {
	var newConns atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc(testTokenPath, tokenHandler)
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	server := newConnCountingServer(t, mux, &newConns)

	for range 3 {
		// A fresh production config per session, as NewGithubSession builds one,
		// with only the mint pointed at the local server.
		cfg := defaultSessionConfig()
		cfg.mintBaseURL = server.URL
		tokenSource, client := newInstallationClient(testAppTokenSource(t), testInstallationID, cfg)
		_, err := tokenSource.Token()
		require.NoError(t, err)
		require.NoError(t, getAndDrain(client, server.URL+"/ok"))
	}

	assert.Equal(t, int32(1), newConns.Load(), "three sessions' mints and requests must reuse one connection")
}

func TestGraphQLClient_CallsAreBounded(t *testing.T) {
	installations := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 123456, "account": map[string]any{"login": "test-owner"}},
		})
	}
	tokens := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"token":      testAccessToken,
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	}

	tests := []struct {
		name          string
		installations http.HandlerFunc
		tokens        http.HandlerFunc
		graphql       http.HandlerFunc
	}{
		{name: "installation discovery stalls", installations: stallHandler(false), tokens: tokens, graphql: stallHandler(false)},
		{name: "graphql query stalls before headers", installations: installations, tokens: tokens, graphql: stallHandler(false)},
		{name: "graphql query stalls the body", installations: installations, tokens: tokens, graphql: stallHandler(true)},
		{name: "token refresh inside the query stalls", installations: installations, tokens: stallHandler(false), graphql: stallHandler(false)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v3/app/installations", tt.installations)
			mux.HandleFunc("/api/v3/app/installations/123456/access_tokens", tt.tokens)
			mux.HandleFunc("/api/graphql", tt.graphql)
			server := newStallServer(t, mux)

			client, err := NewGraphQLClient(
				GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t), EnterpriseURL: server.URL},
				nil,
			)
			require.NoError(t, err)
			client.httpClient = &http.Client{Transport: newBoundedTransport(shortBound), Timeout: shortBound}

			err, elapsed := runBounded(t, func() error {
				_, ancestryErr := client.GetCommitAncestry(context.Background(), "test-owner", "repo", "main", 5)
				return ancestryErr
			})
			require.Error(t, err)
			assert.Less(t, elapsed, shortBound+boundSlack)
		})
	}
}

func TestNewInstallationClient_MintHonorsTheSessionContext(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testTokenPath, tokenHandler)
	server := newStallServer(t, mux)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := testSessionConfig(server.URL)
	cfg.ctx = ctx

	tokenSource, _ := newInstallationClient(testAppTokenSource(t), testInstallationID, cfg)
	err, _ := runBounded(t, func() error {
		_, tokenErr := tokenSource.Token()
		return tokenErr
	})

	require.Error(t, err, "a mint under a cancelled session context must not succeed")
	assert.ErrorIs(t, err, context.Canceled)
}

func TestNewSessionConfig(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "caller")

	tests := []struct {
		name           string
		opts           []SessionOption
		wantCtx        context.Context
		wantTimeout    time.Duration
		wantErrContain string
	}{
		{name: "defaults", wantCtx: context.Background(), wantTimeout: DefaultRequestTimeout},
		{name: "WithContext", opts: []SessionOption{WithContext(ctx)}, wantCtx: ctx, wantTimeout: DefaultRequestTimeout},
		{
			name:        "WithRequestTimeout raises the bound",
			opts:        []SessionOption{WithRequestTimeout(2 * time.Minute)},
			wantCtx:     context.Background(),
			wantTimeout: 2 * time.Minute,
		},
		{
			name:        "WithRequestTimeout(0) removes the overall bound",
			opts:        []SessionOption{WithRequestTimeout(0)},
			wantCtx:     context.Background(),
			wantTimeout: 0,
		},
		{
			name:           "nil context is rejected",
			opts:           []SessionOption{WithContext(nil)}, //nolint:staticcheck // SA1012: the nil is the input under test
			wantErrContain: "WithContext",
		},
		{
			name:           "negative timeout is rejected",
			opts:           []SessionOption{WithRequestTimeout(-time.Second)},
			wantErrContain: "WithRequestTimeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := newSessionConfig(tt.opts...)
			if tt.wantErrContain != "" {
				require.ErrorContains(t, err, tt.wantErrContain)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantCtx, cfg.ctx)
			assert.Equal(t, tt.wantTimeout, cfg.requestTimeout)
			assert.Equal(t, TokenMintTimeout, cfg.mintTimeout, "no option changes the mint bound")
			assert.Same(t, sharedTransport(), cfg.transport, "every session uses the shared transport")
		})
	}
}

func TestNewGithubSessionWithOptions_RejectsAnInvalidOptionBeforeAuthenticating(t *testing.T) {
	// The PEM is garbage: an option error has to be reported before authenticate
	// would reject it.
	_, err := NewGithubSessionWithOptions("not-a-pem", "1", "2", WithRequestTimeout(-time.Second))
	require.ErrorContains(t, err, "WithRequestTimeout")
}

// withMintBaseURLForTest points a session's mint at a local server. These tests
// are in the package, so a SessionOption can reach the same field production
// leaves empty, and the public constructor can be driven end to end.
func withMintBaseURLForTest(baseURL string) SessionOption {
	return func(cfg *sessionConfig) { cfg.mintBaseURL = baseURL }
}

// TestNewGithubSessionWithOptions_AppliesTheSessionConfig pins that
// authenticate() hands the session's own config to newInstallationClient: the
// request timeout reaches the REST client, and the context reaches the mint.
func TestNewGithubSessionWithOptions_AppliesTheSessionConfig(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testTokenPath, tokenHandler)
	server := newStallServer(t, mux)
	pem := testPrivateKey(t)

	t.Run("the request timeout reaches the REST client", func(t *testing.T) {
		session, err := NewGithubSessionWithOptions(pem, "12345", "67890",
			withMintBaseURLForTest(server.URL), WithRequestTimeout(7*time.Second))
		require.NoError(t, err)
		assert.Equal(t, 7*time.Second, session.Client().Client().Timeout)
	})

	t.Run("the session context reaches the mint", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := NewGithubSessionWithOptions(pem, "12345", "67890",
			withMintBaseURLForTest(server.URL), WithContext(ctx))
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

// TestNewInstallationClient_ThrottledRefreshIsThisPackagesErrRateLimited pins the second
// go-githubauth path: a token refresh inside a REST call. oauth2.Transport returns the token
// source's error unchanged, so unless the source itself joins the sentinel, a consumer that keeps
// one session for its whole life (MC.TestEngine does) sees only the library's sentinel on a
// throttle (PR #89 review, bcarlock).
func TestNewInstallationClient_ThrottledRefreshIsThisPackagesErrRateLimited(t *testing.T) {
	var mints atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc(testTokenPath, func(w http.ResponseWriter, _ *http.Request) {
		if mints.Add(1) == 1 {
			// The first mint hands out a token that is already expired, so the next REST call
			// refreshes it.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":      testAccessToken,
				"expires_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
			})
			return
		}
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	server := newStallServer(t, mux)

	tokenSource, client := newInstallationClient(testAppTokenSource(t), testInstallationID, testSessionConfig(server.URL))
	_, err := tokenSource.Token()
	require.NoError(t, err, "the first mint succeeds")

	err = getAndDrain(client, server.URL+"/ok")

	require.Error(t, err, "the refresh inside the call is throttled")
	assert.ErrorIs(t, err, ErrRateLimited, "this package's own sentinel, on the refresh path too")
	assert.ErrorIs(t, err, githubauth.ErrRateLimited)
	var rateLimit *githubauth.RateLimitError
	require.ErrorAs(t, err, &rateLimit)
	assert.Equal(t, 3*time.Second, rateLimit.RetryAfter)
}

// TestNewGithubSession_ThrottledMintIsThisPackagesErrRateLimited pins that a caller can
// branch on this package's own sentinel rather than on its auth library's, and still reach
// the wait GitHub asked for. A branch written against a transitive dependency's sentinel
// would keep compiling but stop firing if the package ever changed auth library.
func TestNewGithubSession_ThrottledMintIsThisPackagesErrRateLimited(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(testTokenPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	server := newStallServer(t, mux)

	_, err := NewGithubSessionWithOptions(testPrivateKey(t), "12345", "67890", withMintBaseURLForTest(server.URL))

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRateLimited, "this package's own sentinel")
	assert.ErrorIs(t, err, githubauth.ErrRateLimited, "and the library's, for callers already branching on it")
	var rateLimit *githubauth.RateLimitError
	require.ErrorAs(t, err, &rateLimit, "the wait GitHub asked for stays reachable")
	assert.Equal(t, 3*time.Second, rateLimit.RetryAfter)
}

func TestNewBoundedTransport_KeepsTheDefaultTransportBounds(t *testing.T) {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok, "http.DefaultTransport is expected to be an *http.Transport")

	transport, ok := newBoundedTransport(ResponseHeaderTimeout).(*http.Transport)
	require.True(t, ok, "the bounded transport must be an *http.Transport of its own")

	assert.Equal(t, ResponseHeaderTimeout, transport.ResponseHeaderTimeout)
	assert.Equal(t, defaultTransport.TLSHandshakeTimeout, transport.TLSHandshakeTimeout, "the TLS handshake bound must be kept")
	assert.Equal(t, defaultTransport.ExpectContinueTimeout, transport.ExpectContinueTimeout)
	assert.Equal(t, defaultTransport.IdleConnTimeout, transport.IdleConnTimeout)
	assert.NotNil(t, transport.DialContext, "the dial bound and keep-alive must be kept")
	assert.NotNil(t, transport.Proxy, "proxy support must be kept")
	assert.Equal(t, transport.MaxIdleConns, transport.MaxIdleConnsPerHost,
		"every call goes to one host, so the per-host idle limit is the whole idle limit")
	assert.NotSame(t, defaultTransport, transport, "must clone rather than mutate http.DefaultTransport")
}

func TestSharedTransport_BoundsEveryClientThisPackageBuilds(t *testing.T) {
	shared := sharedTransport()
	require.Same(t, shared, sharedTransport(), "the shared transport is built once")

	transport, ok := shared.(*http.Transport)
	require.True(t, ok)
	assert.Equal(t, ResponseHeaderTimeout, transport.ResponseHeaderTimeout)

	assert.Same(t, shared, defaultSessionConfig().transport)

	graphQL, err := NewGraphQLClient(GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t)}, nil)
	require.NoError(t, err)
	assert.Same(t, shared, graphQL.httpClient.Transport, "GraphQL installation discovery uses the shared transport")
	assert.Equal(t, DefaultRequestTimeout, graphQL.httpClient.Timeout)
}

// TestGraphQLClient_SharesTheConnectionPool pins that installation discovery,
// the token mint ghinstallation performs, and the GraphQL query itself all reuse
// one connection: the per-organization client is built on the discovery
// client's transport, which is the package's shared one.
func TestGraphQLClient_SharesTheConnectionPool(t *testing.T) {
	var newConns atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/app/installations", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 123456, "account": map[string]any{"login": "test-owner"}},
		})
	})
	mux.HandleFunc("/api/v3/app/installations/123456/access_tokens", tokenHandler)
	mux.HandleFunc("/api/graphql", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"repository":{"object":{"history":{"nodes":[]}}}}}`))
	})
	server := newConnCountingServer(t, mux, &newConns)

	client, err := NewGraphQLClient(
		GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t), EnterpriseURL: server.URL},
		nil,
	)
	require.NoError(t, err)

	for range 2 {
		_, err := client.GetCommitAncestry(context.Background(), "test-owner", "repo", "main", 5)
		require.NoError(t, err)
	}

	assert.Equal(t, int32(1), newConns.Load(), "discovery, mint and queries must reuse one connection")
}

// TestNewBoundedTransport_KeepsAConcurrentBurstPooled pins the idle pool's size against
// concurrency rather than CPU count: with the clone's default of 2, or go-githubauth's
// GOMAXPROCS+1, a burst wider than that re-handshakes the surplus on the next burst. Every
// call here goes to one host, so the per-host idle limit is the transport's whole idle limit.
//
// That is an HTTP/1.1 property, which is what httptest serves. api.github.com negotiates
// HTTP/2, where a pooled connection is not consumed by the request using it, so one
// connection carries a whole burst and the limit makes no difference there.
//
// 25 requests are held open together, so the burst needs 25 connections at once; a second
// identical burst must find all 25 idle and open none. 25 is above GOMAXPROCS+1 on any runner
// with fewer than 24 cores, which is what makes the old sizing fail here.
func TestNewBoundedTransport_KeepsAConcurrentBurstPooled(t *testing.T) {
	const burst = 25

	// Each burst gets its own gate, which holds every request until the whole burst has
	// arrived, so none can finish and hand its connection to another request of the burst.
	type gate struct {
		arrived atomic.Int32
		release chan struct{}
		once    sync.Once
	}
	var current atomic.Pointer[gate]

	var newConns atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) {
		g := current.Load()
		if g.arrived.Add(1) == burst {
			g.once.Do(func() { close(g.release) })
		}
		select {
		case <-g.release:
		case <-time.After(testDeadline):
		}
		_, _ = w.Write([]byte("ok"))
	})
	server := newConnCountingServer(t, mux, &newConns)
	client := &http.Client{Transport: newBoundedTransport(ResponseHeaderTimeout), Timeout: testDeadline}

	runBurst := func() {
		current.Store(&gate{release: make(chan struct{})})
		var wg sync.WaitGroup
		for range burst {
			wg.Add(1)
			go func() {
				defer wg.Done()
				assert.NoError(t, getAndDrain(client, server.URL+"/ok"))
			}()
		}
		wg.Wait()
	}

	runBurst()
	require.Equal(t, int32(burst), newConns.Load(), "the first burst opens one connection per request")

	// The transport returns a drained connection to its idle pool from its own read loop,
	// just after the caller sees EOF; give the last few a moment to land there.
	time.Sleep(100 * time.Millisecond)
	runBurst()

	assert.Equal(t, int32(burst), newConns.Load(), "a second burst must reuse every pooled connection")
}
