package github_handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetClientForOrg_Success tests successful client creation and caching
func TestGetClientForOrg_Success(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		// Mock JWT endpoint
		if r.URL.Path == "/api/v3/app/installations" {
			response := []map[string]interface{}{
				{
					"id": json.Number("123456"),
					"account": map[string]interface{}{
						"login": "test-org",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(GraphQLConfig{
		AppID:         123,
		PrivateKey:    testPrivateKey(t),
		EnterpriseURL: server.URL,
	}, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// First call should discover installation and cache
	ghClient, err := client.GetClientForOrg(ctx, "test-org")
	require.NoError(t, err)
	assert.NotNil(t, ghClient)

	firstCallCount := callCount

	// Second call should use cached client
	ghClient2, err := client.GetClientForOrg(ctx, "test-org")
	require.NoError(t, err)
	assert.NotNil(t, ghClient2)
	assert.Equal(t, ghClient, ghClient2, "should return same cached client")
	assert.Equal(t, firstCallCount, callCount, "should not make additional API calls for cached client")
}

// TestGetClientForOrg_DiscoveryError tests error handling when installation discovery fails
func TestGetClientForOrg_DiscoveryError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(
		GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t), EnterpriseURL: server.URL},
		nil,
	)
	require.NoError(t, err)

	ctx := context.Background()

	_, err = client.GetClientForOrg(ctx, "unknown-org")
	assert.Error(t, err)
	// Updated error message with actionable guidance
	assert.Contains(t, err.Error(), "failed to authenticate GitHub App")
	assert.Contains(t, err.Error(), "Check SLIPPY_GITHUB_APP_ID")
}

// TestGetCommitAncestry_Success tests successful commit ancestry retrieval
func TestGetCommitAncestry_Success(t *testing.T) {
	queryReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock JWT endpoint for installation discovery
		if r.URL.Path == "/api/v3/app/installations" {
			response := []map[string]interface{}{
				{
					"id": json.Number("123456"),
					"account": map[string]interface{}{
						"login": "test-owner",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock access token endpoint
		if r.URL.Path == "/api/v3/app/installations/123456/access_tokens" {
			response := map[string]interface{}{
				"token":      "ghs_mock_token",
				"expires_at": "2099-12-31T23:59:59Z",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock GraphQL endpoint
		if r.URL.Path == "/graphql" || r.URL.Path == "/api/graphql" {
			queryReceived = true
			// Mock GraphQL response with commit history including parent OIDs
			// for first-parent chain filtering
			response := map[string]interface{}{
				"data": map[string]interface{}{
					"repository": map[string]interface{}{
						"object": map[string]interface{}{
							"history": map[string]interface{}{
								"nodes": []map[string]interface{}{
									{
										"oid": "abc123def456",
										"parents": map[string]interface{}{
											"nodes": []map[string]interface{}{{"oid": "def456ghi789"}},
										},
									},
									{
										"oid": "def456ghi789",
										"parents": map[string]interface{}{
											"nodes": []map[string]interface{}{{"oid": "ghi789jkl012"}},
										},
									},
									{
										"oid":     "ghi789jkl012",
										"parents": map[string]interface{}{"nodes": []map[string]interface{}{}},
									},
								},
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(
		GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t), EnterpriseURL: server.URL},
		nil,
	)
	require.NoError(t, err)

	ctx := context.Background()

	commits, err := client.GetCommitAncestry(ctx, "test-owner", "test-repo", "main", 25)
	require.NoError(t, err)
	assert.True(t, queryReceived, "GraphQL query should have been made")
	assert.Len(t, commits, 3)
	assert.Equal(t, "abc123def456", commits[0])
	assert.Equal(t, "def456ghi789", commits[1])
	assert.Equal(t, "ghi789jkl012", commits[2])
}

// TestGetCommitAncestry_ClientError tests error handling when client creation fails
func TestGetCommitAncestry_ClientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(
		GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t), EnterpriseURL: server.URL},
		nil,
	)
	require.NoError(t, err)

	ctx := context.Background()

	_, err = client.GetCommitAncestry(ctx, "test-owner", "test-repo", "main", 25)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get client for org")
}

// TestGetCommitAncestry_GraphQLError tests error handling when GraphQL query fails
func TestGetCommitAncestry_GraphQLError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock JWT endpoint for installation discovery
		if r.URL.Path == "/api/v3/app/installations" {
			response := []map[string]interface{}{
				{
					"id": json.Number("123456"),
					"account": map[string]interface{}{
						"login": "test-owner",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock access token endpoint
		if r.URL.Path == "/api/v3/app/installations/123456/access_tokens" {
			response := map[string]interface{}{
				"token":      "ghs_mock_token",
				"expires_at": "2099-12-31T23:59:59Z",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock GraphQL endpoint with error
		if r.URL.Path == "/graphql" || r.URL.Path == "/api/graphql" {
			response := map[string]interface{}{
				"errors": []map[string]interface{}{
					{
						"message": "Repository not found",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK) // GraphQL errors return 200 with errors in body
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(
		GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t), EnterpriseURL: server.URL},
		nil,
	)
	require.NoError(t, err)

	ctx := context.Background()

	_, err = client.GetCommitAncestry(ctx, "test-owner", "test-repo", "main", 25)
	assert.Error(t, err)
	// Updated: GraphQL errors now include structured context
	assert.Contains(t, err.Error(), "GetCommitAncestry failed")
	assert.Contains(t, err.Error(), "test-owner/test-repo")
}

// TestGetPRHeadCommit_Success tests successful PR head commit retrieval
func TestGetPRHeadCommit_Success(t *testing.T) {
	queryReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock JWT endpoint for installation discovery
		if r.URL.Path == "/api/v3/app/installations" {
			response := []map[string]interface{}{
				{
					"id": json.Number("123456"),
					"account": map[string]interface{}{
						"login": "test-owner",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock access token endpoint
		if r.URL.Path == "/api/v3/app/installations/123456/access_tokens" {
			response := map[string]interface{}{
				"token":      "ghs_mock_token",
				"expires_at": "2099-12-31T23:59:59Z",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock GraphQL endpoint
		if r.URL.Path == "/graphql" || r.URL.Path == "/api/graphql" {
			queryReceived = true
			// Mock GraphQL response with PR head commit
			response := map[string]interface{}{
				"data": map[string]interface{}{
					"repository": map[string]interface{}{
						"pullRequest": map[string]interface{}{
							"headRefOid": "abc123def456789",
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(
		GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t), EnterpriseURL: server.URL},
		nil,
	)
	require.NoError(t, err)

	ctx := context.Background()

	headCommit, err := client.GetPRHeadCommit(ctx, "test-owner", "test-repo", 42)
	require.NoError(t, err)
	assert.True(t, queryReceived, "GraphQL query should have been made")
	assert.Equal(t, "abc123def456789", headCommit)
}

// TestGetPRHeadCommit_ClientError tests error handling when client creation fails
func TestGetPRHeadCommit_ClientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(
		GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t), EnterpriseURL: server.URL},
		nil,
	)
	require.NoError(t, err)

	ctx := context.Background()

	_, err = client.GetPRHeadCommit(ctx, "test-owner", "test-repo", 42)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get client for org")
}

// TestGetPRHeadCommit_GraphQLError tests error handling when GraphQL query fails
func TestGetPRHeadCommit_GraphQLError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock JWT endpoint for installation discovery
		if r.URL.Path == "/api/v3/app/installations" {
			response := []map[string]interface{}{
				{
					"id": json.Number("123456"),
					"account": map[string]interface{}{
						"login": "test-owner",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock access token endpoint
		if r.URL.Path == "/api/v3/app/installations/123456/access_tokens" {
			response := map[string]interface{}{
				"token":      "ghs_mock_token",
				"expires_at": "2099-12-31T23:59:59Z",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock GraphQL endpoint with error
		if r.URL.Path == "/graphql" || r.URL.Path == "/api/graphql" {
			response := map[string]interface{}{
				"errors": []map[string]interface{}{
					{
						"message": "Pull request not found",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(
		GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t), EnterpriseURL: server.URL},
		nil,
	)
	require.NoError(t, err)

	ctx := context.Background()

	_, err = client.GetPRHeadCommit(ctx, "test-owner", "test-repo", 42)
	assert.Error(t, err)
	// Updated: GraphQL errors now include structured context
	assert.Contains(t, err.Error(), "GetPRHeadCommit failed")
	assert.Contains(t, err.Error(), "test-owner/test-repo")
}

// TestGetPRHeadCommit_EmptyHeadRefOid tests error handling when PR has no head commit
func TestGetPRHeadCommit_EmptyHeadRefOid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock JWT endpoint for installation discovery
		if r.URL.Path == "/api/v3/app/installations" {
			response := []map[string]interface{}{
				{
					"id": json.Number("123456"),
					"account": map[string]interface{}{
						"login": "test-owner",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock access token endpoint
		if r.URL.Path == "/api/v3/app/installations/123456/access_tokens" {
			response := map[string]interface{}{
				"token":      "ghs_mock_token",
				"expires_at": "2099-12-31T23:59:59Z",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock GraphQL endpoint with empty headRefOid
		if r.URL.Path == "/graphql" || r.URL.Path == "/api/graphql" {
			response := map[string]interface{}{
				"data": map[string]interface{}{
					"repository": map[string]interface{}{
						"pullRequest": map[string]interface{}{
							"headRefOid": "",
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(
		GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t), EnterpriseURL: server.URL},
		nil,
	)
	require.NoError(t, err)

	ctx := context.Background()

	_, err = client.GetPRHeadCommit(ctx, "test-owner", "test-repo", 999)
	assert.Error(t, err)
	// Updated: PR not found error message
	assert.Contains(t, err.Error(), "pull request not found")
	assert.Contains(t, err.Error(), "PR #999")
}

// TestGetCommitAncestry_EmptyResult tests handling of empty commit history
func TestGetCommitAncestry_EmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock JWT endpoint for installation discovery
		if r.URL.Path == "/api/v3/app/installations" {
			response := []map[string]interface{}{
				{
					"id": json.Number("123456"),
					"account": map[string]interface{}{
						"login": "test-owner",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock access token endpoint
		if r.URL.Path == "/api/v3/app/installations/123456/access_tokens" {
			response := map[string]interface{}{
				"token":      "ghs_mock_token",
				"expires_at": "2099-12-31T23:59:59Z",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock GraphQL endpoint with empty history
		if r.URL.Path == "/graphql" || r.URL.Path == "/api/graphql" {
			response := map[string]interface{}{
				"data": map[string]interface{}{
					"repository": map[string]interface{}{
						"object": map[string]interface{}{
							"history": map[string]interface{}{
								"nodes": []map[string]interface{}{},
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(
		GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t), EnterpriseURL: server.URL},
		nil,
	)
	require.NoError(t, err)

	ctx := context.Background()

	commits, err := client.GetCommitAncestry(ctx, "test-owner", "test-repo", "empty-branch", 25)
	require.NoError(t, err)
	assert.Empty(t, commits)
}

// TestGetClientForOrg_ContextCancellation tests context cancellation handling
func TestGetClientForOrg_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Delay to allow context cancellation
		<-r.Context().Done()
	}))
	defer server.Close()

	client, err := NewGraphQLClient(
		GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t), EnterpriseURL: server.URL},
		nil,
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err = client.GetClientForOrg(ctx, "test-org")
	assert.Error(t, err)
}

// TestGetPRHeadCommit_ShortSHALogging tests that short SHA is logged correctly
func TestGetPRHeadCommit_ShortSHALogging(t *testing.T) {
	mockLog := &mockLogger{fields: make(map[string]interface{})}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock JWT endpoint for installation discovery
		if r.URL.Path == "/api/v3/app/installations" {
			response := []map[string]interface{}{
				{
					"id": json.Number("123456"),
					"account": map[string]interface{}{
						"login": "test-owner",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock access token endpoint
		if r.URL.Path == "/api/v3/app/installations/123456/access_tokens" {
			response := map[string]interface{}{
				"token":      "ghs_mock_token",
				"expires_at": "2099-12-31T23:59:59Z",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock GraphQL endpoint
		if r.URL.Path == "/graphql" || r.URL.Path == "/api/graphql" {
			response := map[string]interface{}{
				"data": map[string]interface{}{
					"repository": map[string]interface{}{
						"pullRequest": map[string]interface{}{
							"headRefOid": "1234567890abcdef",
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(
		GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t), EnterpriseURL: server.URL},
		mockLog,
	)
	require.NoError(t, err)

	ctx := context.Background()

	headCommit, err := client.GetPRHeadCommit(ctx, "test-owner", "test-repo", 42)
	require.NoError(t, err)
	assert.Equal(t, "1234567890abcdef", headCommit)

	// Verify debug logging was called
	mockLog.mu.Lock()
	defer mockLog.mu.Unlock()
	assert.NotEmpty(t, mockLog.debugCalls, "Debug should have been called for PR head commit retrieval")
}

// TestGetCommitAncestry_DebugLogging tests that debug logging includes correct information
func TestGetCommitAncestry_DebugLogging(t *testing.T) {
	mockLog := &mockLogger{fields: make(map[string]interface{})}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock JWT endpoint for installation discovery
		if r.URL.Path == "/api/v3/app/installations" {
			response := []map[string]interface{}{
				{
					"id": json.Number("123456"),
					"account": map[string]interface{}{
						"login": "test-owner",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock access token endpoint
		if r.URL.Path == "/api/v3/app/installations/123456/access_tokens" {
			response := map[string]interface{}{
				"token":      "ghs_mock_token",
				"expires_at": "2099-12-31T23:59:59Z",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock GraphQL endpoint with parent OIDs for first-parent filtering
		if r.URL.Path == "/graphql" || r.URL.Path == "/api/graphql" {
			response := map[string]interface{}{
				"data": map[string]interface{}{
					"repository": map[string]interface{}{
						"object": map[string]interface{}{
							"history": map[string]interface{}{
								"nodes": []map[string]interface{}{
									{
										"oid": "commit1",
										"parents": map[string]interface{}{
											"nodes": []map[string]interface{}{{"oid": "commit2"}},
										},
									},
									{
										"oid":     "commit2",
										"parents": map[string]interface{}{"nodes": []map[string]interface{}{}},
									},
								},
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(
		GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t), EnterpriseURL: server.URL},
		mockLog,
	)
	require.NoError(t, err)

	ctx := context.Background()

	commits, err := client.GetCommitAncestry(ctx, "test-owner", "test-repo", "main", 25)
	require.NoError(t, err)
	assert.Len(t, commits, 2)

	// Verify debug logging was called
	mockLog.mu.Lock()
	defer mockLog.mu.Unlock()
	assert.NotEmpty(t, mockLog.debugCalls, "Debug should have been called for commit ancestry retrieval")
}

// TestGetCommitAncestry_FirstParentFiltering tests that merge commits from other branches
// are excluded from the ancestry chain. This prevents incorrect slip resolution when
// the default branch (main/integration) is merged into a feature branch.
func TestGetCommitAncestry_FirstParentFiltering(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock JWT endpoint for installation discovery
		if r.URL.Path == "/api/v3/app/installations" {
			response := []map[string]interface{}{
				{
					"id": json.Number("123456"),
					"account": map[string]interface{}{
						"login": "test-owner",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock access token endpoint
		if r.URL.Path == "/api/v3/app/installations/123456/access_tokens" {
			response := map[string]interface{}{
				"token":      "ghs_mock_token",
				"expires_at": "2099-12-31T23:59:59Z",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		// Mock GraphQL endpoint with a merge scenario:
		//
		// feature-3 (HEAD)
		// |
		// merge-commit  <-- git merge main
		// ├── feature-2 (first parent)
		// │   └── feature-1
		// └── main-X (second parent, from main)
		//     └── main-Y
		//
		// GitHub's linearized history returns all commits interleaved:
		//   [feature-3, merge-commit, main-X, feature-2, main-Y, feature-1]
		//
		// After first-parent filtering, only the first-parent chain remains:
		//   [feature-3, merge-commit, feature-2, feature-1]
		if r.URL.Path == "/graphql" || r.URL.Path == "/api/graphql" {
			response := map[string]interface{}{
				"data": map[string]interface{}{
					"repository": map[string]interface{}{
						"object": map[string]interface{}{
							"history": map[string]interface{}{
								"nodes": []map[string]interface{}{
									// feature-3: first parent is merge-commit
									{
										"oid": "feature3aaa",
										"parents": map[string]interface{}{
											"nodes": []map[string]interface{}{{"oid": "mergecommit"}},
										},
									},
									// merge-commit: first parent is feature-2 (branch we were on), second parent is main-X
									{
										"oid": "mergecommit",
										"parents": map[string]interface{}{
											"nodes": []map[string]interface{}{{"oid": "feature2bbb"}},
										},
									},
									// main-X: from the merged-in main branch (should be filtered out)
									{
										"oid": "mainXcccccc",
										"parents": map[string]interface{}{
											"nodes": []map[string]interface{}{{"oid": "mainYdddddd"}},
										},
									},
									// feature-2: first parent is feature-1
									{
										"oid": "feature2bbb",
										"parents": map[string]interface{}{
											"nodes": []map[string]interface{}{{"oid": "feature1eee"}},
										},
									},
									// main-Y: from main branch (should be filtered out)
									{
										"oid":     "mainYdddddd",
										"parents": map[string]interface{}{"nodes": []map[string]interface{}{}},
									},
									// feature-1: root of feature branch
									{
										"oid":     "feature1eee",
										"parents": map[string]interface{}{"nodes": []map[string]interface{}{}},
									},
								},
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewGraphQLClient(
		GraphQLConfig{AppID: 123, PrivateKey: testPrivateKey(t), EnterpriseURL: server.URL},
		nil,
	)
	require.NoError(t, err)

	ctx := context.Background()

	commits, err := client.GetCommitAncestry(ctx, "test-owner", "test-repo", "HEAD", 25)
	require.NoError(t, err)

	// Should only contain the first-parent chain: feature-3 -> merge -> feature-2 -> feature-1
	assert.Len(t, commits, 4)
	assert.Equal(t, "feature3aaa", commits[0])
	assert.Equal(t, "mergecommit", commits[1])
	assert.Equal(t, "feature2bbb", commits[2])
	assert.Equal(t, "feature1eee", commits[3])

	// main-X and main-Y must NOT be in the result
	for _, c := range commits {
		assert.NotEqual(t, "mainXcccccc", c, "main branch commit should be filtered out")
		assert.NotEqual(t, "mainYdddddd", c, "main branch commit should be filtered out")
	}
}
