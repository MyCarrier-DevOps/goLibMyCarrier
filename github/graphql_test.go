package github_handler

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/logger"
)

// testPrivateKey generates a test RSA private key in PEM format
func testPrivateKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "failed to generate RSA key")

	keyBytes := x509.MarshalPKCS1PrivateKey(key)
	pemBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: keyBytes,
	}
	return string(pem.EncodeToMemory(pemBlock))
}

// mockLogger implements logger.Logger for testing
type mockLogger struct {
	debugCalls   []logCall
	infoCalls    []logCall
	warnCalls    []logCall
	warningCalls []logCall
	errorCalls   []errorLogCall
	mu           sync.Mutex
	fields       map[string]interface{}
}

type logCall struct {
	message string
	fields  map[string]interface{}
}

type errorLogCall struct {
	message string
	err     error
	fields  map[string]interface{}
}

func (m *mockLogger) Debug(ctx context.Context, msg string, fields map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.debugCalls = append(m.debugCalls, logCall{message: msg, fields: fields})
}

func (m *mockLogger) Info(ctx context.Context, msg string, fields map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.infoCalls = append(m.infoCalls, logCall{message: msg, fields: fields})
}

func (m *mockLogger) Warn(ctx context.Context, msg string, fields map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.warnCalls = append(m.warnCalls, logCall{message: msg, fields: fields})
}

func (m *mockLogger) Warning(ctx context.Context, msg string, fields map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.warningCalls = append(m.warningCalls, logCall{message: msg, fields: fields})
}

func (m *mockLogger) Error(ctx context.Context, msg string, err error, fields map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errorCalls = append(m.errorCalls, errorLogCall{message: msg, err: err, fields: fields})
}

func (m *mockLogger) WithFields(fields map[string]interface{}) logger.Logger {
	m.mu.Lock()
	defer m.mu.Unlock()
	newLogger := &mockLogger{
		debugCalls:   m.debugCalls,
		infoCalls:    m.infoCalls,
		warnCalls:    m.warnCalls,
		warningCalls: m.warningCalls,
		errorCalls:   m.errorCalls,
		fields:       make(map[string]interface{}),
	}
	for k, v := range m.fields {
		newLogger.fields[k] = v
	}
	for k, v := range fields {
		newLogger.fields[k] = v
	}
	return newLogger
}

// ---- NewGraphQLClient Tests ----

func TestNewGraphQLClient_WithInlineKey(t *testing.T) {
	privateKey := testPrivateKey(t)

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:      12345,
		PrivateKey: privateKey,
	}, nil)

	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, int64(12345), client.appID)
	assert.NotEmpty(t, client.privateKey)
	assert.Empty(t, client.enterpriseURL)
}

func TestNewGraphQLClient_WithFilePath(t *testing.T) {
	privateKey := testPrivateKey(t)

	// Write key to temp file
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "private-key.pem")
	err := os.WriteFile(keyPath, []byte(privateKey), 0600)
	require.NoError(t, err)

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:      12345,
		PrivateKey: keyPath,
	}, nil)

	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, int64(12345), client.appID)
}

func TestNewGraphQLClient_InvalidFilePath(t *testing.T) {
	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:      12345,
		PrivateKey: "/nonexistent/path/to/key.pem",
	}, nil)

	require.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "failed to read private key file")
}

func TestNewGraphQLClient_InvalidKey(t *testing.T) {
	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:      12345,
		PrivateKey: "-----BEGIN RSA PRIVATE KEY-----\ninvalid-key-data\n-----END RSA PRIVATE KEY-----",
	}, nil)

	require.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "invalid private key")
}

func TestNewGraphQLClient_WithEnterpriseURL(t *testing.T) {
	privateKey := testPrivateKey(t)

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: "https://github.mycompany.com",
	}, nil)

	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, "https://github.mycompany.com", client.enterpriseURL)
}

func TestNewGraphQLClient_WithCustomLogger(t *testing.T) {
	privateKey := testPrivateKey(t)
	log := &mockLogger{}

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:      12345,
		PrivateKey: privateKey,
	}, log)

	require.NoError(t, err)
	assert.NotNil(t, client)
	// Logger should be set (we can't easily check it, but no panic means it works)
}

func TestNewGraphQLClient_NilLoggerUsesNopLogger(t *testing.T) {
	privateKey := testPrivateKey(t)

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:      12345,
		PrivateKey: privateKey,
	}, nil)

	require.NoError(t, err)
	assert.NotNil(t, client)
	// Should not panic when using the logger
	assert.NotNil(t, client.logger)
}

// ---- URL Helper Tests ----

func TestGetAPIBaseURL(t *testing.T) {
	privateKey := testPrivateKey(t)

	tests := []struct {
		name          string
		enterpriseURL string
		expected      string
	}{
		{
			name:          "github.com",
			enterpriseURL: "",
			expected:      "https://api.github.com",
		},
		{
			name:          "enterprise",
			enterpriseURL: "https://github.mycompany.com",
			expected:      "https://github.mycompany.com/api/v3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewGraphQLClient(GraphQLConfig{
				AppID:         12345,
				PrivateKey:    privateKey,
				EnterpriseURL: tt.enterpriseURL,
			}, nil)
			require.NoError(t, err)

			assert.Equal(t, tt.expected, client.GetAPIBaseURL())
		})
	}
}

func TestGetGraphQLURL(t *testing.T) {
	privateKey := testPrivateKey(t)

	tests := []struct {
		name          string
		enterpriseURL string
		expected      string
	}{
		{
			name:          "github.com",
			enterpriseURL: "",
			expected:      "https://api.github.com/graphql",
		},
		{
			name:          "enterprise",
			enterpriseURL: "https://github.mycompany.com",
			expected:      "https://github.mycompany.com/api/graphql",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewGraphQLClient(GraphQLConfig{
				AppID:         12345,
				PrivateKey:    privateKey,
				EnterpriseURL: tt.enterpriseURL,
			}, nil)
			require.NoError(t, err)

			assert.Equal(t, tt.expected, client.GetGraphQLURL())
		})
	}
}

// ---- DiscoverInstallationID Tests ----

func TestDiscoverInstallationID_Success(t *testing.T) {
	privateKey := testPrivateKey(t)
	log := &mockLogger{}

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v3/app/installations", r.URL.Path)
		assert.Contains(t, r.Header.Get("Authorization"), "Bearer ")
		assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))

		installations := []Installation{
			{ID: 11111, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "other-org", Type: "Organization"}},
			{ID: 22222, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "my-org", Type: "Organization"}},
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(installations)
		require.NoError(t, err)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: server.URL,
	}, log)
	require.NoError(t, err)

	ctx := context.Background()
	installID, err := client.DiscoverInstallationID(ctx, "my-org")

	require.NoError(t, err)
	assert.Equal(t, int64(22222), installID)

	// Verify logging
	assert.NotEmpty(t, log.debugCalls)
	assert.NotEmpty(t, log.infoCalls)
}

func TestDiscoverInstallationID_NotFound(t *testing.T) {
	privateKey := testPrivateKey(t)

	// Create mock server returning empty installations
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		installations := []Installation{
			{ID: 11111, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "other-org", Type: "Organization"}},
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(installations)
		require.NoError(t, err)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: server.URL,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = client.DiscoverInstallationID(ctx, "unknown-org")

	require.Error(t, err)
	// Updated: Error message now includes actionable guidance
	assert.Contains(t, err.Error(), "GitHub App not installed")
	assert.Contains(t, err.Error(), "unknown-org")
	assert.Contains(t, err.Error(), "other-org") // Should list available orgs
}

func TestDiscoverInstallationID_CachesResults(t *testing.T) {
	privateKey := testPrivateKey(t)

	var requestCount int32

	// Create mock server that counts requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)

		installations := []Installation{
			{ID: 22222, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "my-org", Type: "Organization"}},
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(installations)
		require.NoError(t, err)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: server.URL,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// First call - should hit API
	id1, err := client.DiscoverInstallationID(ctx, "my-org")
	require.NoError(t, err)
	assert.Equal(t, int64(22222), id1)

	// Second call - should use cache
	id2, err := client.DiscoverInstallationID(ctx, "my-org")
	require.NoError(t, err)
	assert.Equal(t, int64(22222), id2)

	// Verify only one API call was made
	assert.Equal(t, int32(1), atomic.LoadInt32(&requestCount))
}

func TestDiscoverInstallationID_CachesAllInstallations(t *testing.T) {
	privateKey := testPrivateKey(t)

	var requestCount int32

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)

		// Note: The implementation caches installations as it iterates.
		// When looking for org-c (last), all three get cached.
		// When looking for org-a (first), only org-a gets cached.
		installations := []Installation{
			{ID: 11111, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "org-a", Type: "Organization"}},
			{ID: 22222, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "org-b", Type: "Organization"}},
			{ID: 33333, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "org-c", Type: "Organization"}},
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(installations)
		require.NoError(t, err)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: server.URL,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// First call for org-c (last in list) - should hit API and cache all orgs
	id3, err := client.DiscoverInstallationID(ctx, "org-c")
	require.NoError(t, err)
	assert.Equal(t, int64(33333), id3)

	// Calls for org-a and org-b should use cache (they were cached during iteration)
	id1, err := client.DiscoverInstallationID(ctx, "org-a")
	require.NoError(t, err)
	assert.Equal(t, int64(11111), id1)

	id2, err := client.DiscoverInstallationID(ctx, "org-b")
	require.NoError(t, err)
	assert.Equal(t, int64(22222), id2)

	// Verify only one API call was made
	assert.Equal(t, int32(1), atomic.LoadInt32(&requestCount))
}

func TestDiscoverInstallationID_APIError(t *testing.T) {
	privateKey := testPrivateKey(t)

	// Create mock server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message": "Bad credentials"}`))
	}))
	defer server.Close()

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: server.URL,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = client.DiscoverInstallationID(ctx, "my-org")

	require.Error(t, err)
	// Updated: Error message now includes actionable guidance
	assert.Contains(t, err.Error(), "failed to authenticate GitHub App")
	assert.Contains(t, err.Error(), "401")
	assert.Contains(t, err.Error(), "Check SLIPPY_GITHUB_APP_ID")
}

func TestDiscoverInstallationID_InvalidJSON(t *testing.T) {
	privateKey := testPrivateKey(t)

	// Create mock server that returns invalid JSON
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`invalid json`))
	}))
	defer server.Close()

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: server.URL,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = client.DiscoverInstallationID(ctx, "my-org")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode installations")
}

func TestDiscoverInstallationID_ContextCancellation(t *testing.T) {
	privateKey := testPrivateKey(t)

	// Create mock server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		installations := []Installation{}
		err := json.NewEncoder(w).Encode(installations)
		require.NoError(t, err)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: server.URL,
	}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err = client.DiscoverInstallationID(ctx, "my-org")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}

// ---- ClearCache Tests ----

func TestClearCache(t *testing.T) {
	privateKey := testPrivateKey(t)

	var requestCount int32

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)

		installations := []Installation{
			{ID: 22222, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "my-org", Type: "Organization"}},
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(installations)
		require.NoError(t, err)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: server.URL,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// First call
	_, err = client.DiscoverInstallationID(ctx, "my-org")
	require.NoError(t, err)

	// Clear cache
	client.ClearCache()

	// Second call - should hit API again because cache was cleared
	_, err = client.DiscoverInstallationID(ctx, "my-org")
	require.NoError(t, err)

	// Verify two API calls were made
	assert.Equal(t, int32(2), atomic.LoadInt32(&requestCount))
}

func TestClearCache_Empty(t *testing.T) {
	privateKey := testPrivateKey(t)

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:      12345,
		PrivateKey: privateKey,
	}, nil)
	require.NoError(t, err)

	// Should not panic when clearing empty cache
	client.ClearCache()

	// Cache should still be empty
	cached := client.GetCachedInstallationIDs()
	assert.Empty(t, cached)
}

// ---- GetCachedInstallationIDs Tests ----

func TestGetCachedInstallationIDs(t *testing.T) {
	privateKey := testPrivateKey(t)

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Put the target org last so all installations get cached
		installations := []Installation{
			{ID: 11111, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "org-a", Type: "Organization"}},
			{ID: 22222, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "org-b", Type: "Organization"}},
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(installations)
		require.NoError(t, err)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: server.URL,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Request the last org to ensure all get cached during iteration
	_, err = client.DiscoverInstallationID(ctx, "org-b")
	require.NoError(t, err)

	cached := client.GetCachedInstallationIDs()

	assert.Len(t, cached, 2)
	assert.Equal(t, int64(11111), cached["org-a"])
	assert.Equal(t, int64(22222), cached["org-b"])
}

func TestGetCachedInstallationIDs_ReturnsCopy(t *testing.T) {
	privateKey := testPrivateKey(t)

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		installations := []Installation{
			{ID: 11111, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "org-a", Type: "Organization"}},
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(installations)
		require.NoError(t, err)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: server.URL,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Populate cache
	_, err = client.DiscoverInstallationID(ctx, "org-a")
	require.NoError(t, err)

	// Get cached IDs and modify them
	cached := client.GetCachedInstallationIDs()
	cached["org-a"] = 99999
	cached["new-org"] = 88888

	// Original cache should not be affected
	cached2 := client.GetCachedInstallationIDs()
	assert.Equal(t, int64(11111), cached2["org-a"])
	assert.NotContains(t, cached2, "new-org")
}

// ---- Concurrency Tests ----

func TestDiscoverInstallationID_ConcurrentAccess(t *testing.T) {
	privateKey := testPrivateKey(t)

	var requestCount int32

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)

		installations := []Installation{
			{ID: 22222, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "my-org", Type: "Organization"}},
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(installations)
		require.NoError(t, err)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: server.URL,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()
	var wg sync.WaitGroup
	errChan := make(chan error, 100)

	// Make concurrent requests
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := client.DiscoverInstallationID(ctx, "my-org")
			if err != nil {
				errChan <- err
				return
			}
			if id != 22222 {
				errChan <- fmt.Errorf("unexpected ID: %d", id)
			}
		}()
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("concurrent access error: %v", err)
	}

	// Verify all requests got correct results - the important thing is no data races
	// The number of API calls can vary due to timing but should stabilize after caching
	t.Logf("Total API requests: %d", atomic.LoadInt32(&requestCount))
}

func TestClearCache_ConcurrentAccess(t *testing.T) {
	privateKey := testPrivateKey(t)

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:      12345,
		PrivateKey: privateKey,
	}, nil)
	require.NoError(t, err)

	// Pre-populate cache
	client.cacheMutex.Lock()
	client.installationCache["org-a"] = 11111
	client.installationCache["org-b"] = 22222
	client.cacheMutex.Unlock()

	var wg sync.WaitGroup

	// Concurrent ClearCache calls should not panic
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client.ClearCache()
		}()
	}

	wg.Wait()
	// Test passes if no panics occurred
}

// ---- Installation Type Tests ----

func TestInstallation_JSON(t *testing.T) {
	jsonData := `{
		"id": 12345,
		"account": {
			"login": "test-org",
			"type": "Organization"
		}
	}`

	var inst Installation
	err := json.Unmarshal([]byte(jsonData), &inst)

	require.NoError(t, err)
	assert.Equal(t, int64(12345), inst.ID)
	assert.Equal(t, "test-org", inst.Account.Login)
	assert.Equal(t, "Organization", inst.Account.Type)
}

func TestInstallation_UserAccount(t *testing.T) {
	privateKey := testPrivateKey(t)

	// Create mock server with user account
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		installations := []Installation{
			{ID: 11111, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "test-user", Type: "User"}},
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(installations)
		require.NoError(t, err)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: server.URL,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()
	id, err := client.DiscoverInstallationID(ctx, "test-user")

	require.NoError(t, err)
	assert.Equal(t, int64(11111), id)
}

// ---- GraphQLConfig Tests ----

func TestGraphQLConfig_Defaults(t *testing.T) {
	cfg := GraphQLConfig{
		AppID:      12345,
		PrivateKey: testPrivateKey(t),
	}

	client, err := NewGraphQLClient(cfg, nil)
	require.NoError(t, err)

	// Default should be github.com
	assert.Equal(t, "https://api.github.com", client.GetAPIBaseURL())
	assert.Equal(t, "https://api.github.com/graphql", client.GetGraphQLURL())
}

// ---- JWT Generation Tests ----

func TestGenerateAppJWT(t *testing.T) {
	privateKey := testPrivateKey(t)

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:      12345,
		PrivateKey: privateKey,
	}, nil)
	require.NoError(t, err)

	token, err := client.generateAppJWT()

	require.NoError(t, err)
	assert.NotEmpty(t, token)
	// JWT tokens have 3 parts separated by dots
	assert.Regexp(t, `^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`, token)
}

// ---- Edge Cases ----

func TestNewGraphQLClient_EmptyPrivateKey(t *testing.T) {
	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:      12345,
		PrivateKey: "",
	}, nil)

	require.Error(t, err)
	assert.Nil(t, client)
}

func TestNewGraphQLClient_ZeroAppID(t *testing.T) {
	privateKey := testPrivateKey(t)

	// This should succeed - AppID 0 might be valid in some contexts
	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:      0,
		PrivateKey: privateKey,
	}, nil)

	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, int64(0), client.appID)
}

// ---- Logger Integration Tests ----

func TestDiscoverInstallationID_Logging(t *testing.T) {
	privateKey := testPrivateKey(t)
	log := &mockLogger{}

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		installations := []Installation{
			{ID: 11111, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "org-a", Type: "Organization"}},
			{ID: 22222, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "my-org", Type: "Organization"}},
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(installations)
		require.NoError(t, err)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: server.URL,
	}, log)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = client.DiscoverInstallationID(ctx, "my-org")
	require.NoError(t, err)

	// Verify debug logging for discovery start
	foundDiscoveryDebug := false
	for _, call := range log.debugCalls {
		if call.message == "Discovering installation ID" {
			foundDiscoveryDebug = true
			assert.Equal(t, "my-org", call.fields["organization"])
		}
	}
	assert.True(t, foundDiscoveryDebug, "expected 'Discovering installation ID' debug log")

	// Verify debug logging for found installations
	foundInstallationDebug := false
	for _, call := range log.debugCalls {
		if call.message == "Found installation" {
			foundInstallationDebug = true
		}
	}
	assert.True(t, foundInstallationDebug, "expected 'Found installation' debug log")

	// Verify info logging for resolved installation
	foundResolvedInfo := false
	for _, call := range log.infoCalls {
		if call.message == "Resolved installation ID" {
			foundResolvedInfo = true
			assert.Equal(t, int64(22222), call.fields["installation_id"])
			assert.Equal(t, "my-org", call.fields["organization"])
		}
	}
	assert.True(t, foundResolvedInfo, "expected 'Resolved installation ID' info log")
}

// ---- NopLogger Tests ----

func TestNopLogger_DoesNotPanic(t *testing.T) {
	nop := &logger.NopLogger{}
	ctx := context.Background()

	// These should not panic
	nop.Debug(ctx, "test", map[string]interface{}{"key": "value"})
	nop.Info(ctx, "test", map[string]interface{}{"key": "value"})
	nop.Warn(ctx, "test", map[string]interface{}{"key": "value"})
	nop.Warning(ctx, "test", map[string]interface{}{"key": "value"})
	nop.Error(ctx, "test", nil, map[string]interface{}{"key": "value"})
	_ = nop.WithFields(map[string]interface{}{"key": "value"})
}

// ---- HTTP Header Tests ----

func TestDiscoverInstallationID_SetsCorrectHeaders(t *testing.T) {
	privateKey := testPrivateKey(t)

	var capturedHeaders http.Header

	// Create mock server that captures headers
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()

		installations := []Installation{
			{ID: 22222, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "my-org", Type: "Organization"}},
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(installations)
		require.NoError(t, err)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: server.URL,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = client.DiscoverInstallationID(ctx, "my-org")
	require.NoError(t, err)

	// Verify headers
	assert.Contains(t, capturedHeaders.Get("Authorization"), "Bearer ")
	assert.Equal(t, "application/vnd.github+json", capturedHeaders.Get("Accept"))
	assert.Equal(t, "2022-11-28", capturedHeaders.Get("X-Github-Api-Version"))
}

// ---- Benchmark Tests ----

func BenchmarkDiscoverInstallationID_Cached(b *testing.B) {
	privateKey := testPrivateKeyBench(b)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		installations := []Installation{
			{ID: 22222, Account: struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			}{Login: "my-org", Type: "Organization"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(installations)
	}))
	defer server.Close()

	client, _ := NewGraphQLClient(GraphQLConfig{
		AppID:         12345,
		PrivateKey:    privateKey,
		EnterpriseURL: server.URL,
	}, nil)

	ctx := context.Background()

	// Prime the cache
	_, _ = client.DiscoverInstallationID(ctx, "my-org")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = client.DiscoverInstallationID(ctx, "my-org")
	}
}

func BenchmarkGetCachedInstallationIDs(b *testing.B) {
	privateKey := testPrivateKeyBench(b)

	client, _ := NewGraphQLClient(GraphQLConfig{
		AppID:      12345,
		PrivateKey: privateKey,
	}, nil)

	// Pre-populate cache
	client.cacheMutex.Lock()
	for i := 0; i < 100; i++ {
		client.installationCache[fmt.Sprintf("org-%d", i)] = int64(i)
	}
	client.cacheMutex.Unlock()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = client.GetCachedInstallationIDs()
	}
}

// testPrivateKeyBench generates a test RSA private key for benchmarks
func testPrivateKeyBench(b *testing.B) string {
	b.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		b.Fatalf("failed to generate RSA key: %v", err)
	}

	keyBytes := x509.MarshalPKCS1PrivateKey(key)
	pemBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: keyBytes,
	}
	return string(pem.EncodeToMemory(pemBlock))
}
