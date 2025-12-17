package github_handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/shurcooL/githubv4"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/logger"
)

// GraphQLConfig holds GitHub GraphQL API authentication configuration.
// It supports automatic installation discovery per organization.
type GraphQLConfig struct {
	// AppID is the GitHub App ID
	AppID int64

	// PrivateKey is the PEM-encoded private key or path to key file
	PrivateKey string

	// EnterpriseURL is the GitHub Enterprise base URL (optional)
	// Leave empty for github.com
	EnterpriseURL string
}

// GraphQLClient wraps the GitHub GraphQL client with automatic installation discovery.
// It caches installation IDs and authenticated clients per organization for efficiency.
// This client is useful when you need to make queries across multiple organizations
// without knowing the installation IDs upfront.
type GraphQLClient struct {
	appID         int64
	privateKey    []byte
	enterpriseURL string
	logger        logger.Logger

	// Cache of org -> installation ID mappings
	installationCache map[string]int64
	cacheMutex        sync.RWMutex

	// Cache of org -> authenticated client
	clientCache map[string]*githubv4.Client
	clientMutex sync.RWMutex
}

// Installation represents a GitHub App installation.
type Installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
}

// NewGraphQLClient creates a new GitHub GraphQL client with App authentication.
// The private key can be provided as PEM content (starts with "-----BEGIN")
// or as a file path.
func NewGraphQLClient(cfg GraphQLConfig, log logger.Logger) (*GraphQLClient, error) {
	if log == nil {
		log = &logger.NopLogger{}
	}

	var privateKey []byte

	// Support both inline key and file path
	if len(cfg.PrivateKey) > 0 && cfg.PrivateKey[0] == '-' {
		// Looks like PEM content (starts with "-----BEGIN")
		privateKey = []byte(cfg.PrivateKey)
	} else {
		// Treat as file path
		var err error
		privateKey, err = os.ReadFile(cfg.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to read private key file: %w", err)
		}
	}

	// Validate the private key
	if _, err := jwt.ParseRSAPrivateKeyFromPEM(privateKey); err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	return &GraphQLClient{
		appID:             cfg.AppID,
		privateKey:        privateKey,
		enterpriseURL:     cfg.EnterpriseURL,
		logger:            log,
		installationCache: make(map[string]int64),
		clientCache:       make(map[string]*githubv4.Client),
	}, nil
}

// GetAPIBaseURL returns the appropriate REST API base URL.
func (g *GraphQLClient) GetAPIBaseURL() string {
	if g.enterpriseURL != "" {
		return g.enterpriseURL + "/api/v3"
	}
	return "https://api.github.com"
}

// GetGraphQLURL returns the appropriate GraphQL API URL.
func (g *GraphQLClient) GetGraphQLURL() string {
	if g.enterpriseURL != "" {
		return g.enterpriseURL + "/api/graphql"
	}
	return "https://api.github.com/graphql"
}

// generateAppJWT creates a JWT for authenticating as the GitHub App.
func (g *GraphQLClient) generateAppJWT() (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"iss": g.appID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

	key, err := jwt.ParseRSAPrivateKeyFromPEM(g.privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	return token.SignedString(key)
}

// DiscoverInstallationID finds the installation ID for a given organization.
// Results are cached to avoid repeated API calls.
func (g *GraphQLClient) DiscoverInstallationID(ctx context.Context, org string) (int64, error) {
	// Check cache first
	g.cacheMutex.RLock()
	if id, ok := g.installationCache[org]; ok {
		g.cacheMutex.RUnlock()
		return id, nil
	}
	g.cacheMutex.RUnlock()

	g.logger.Debug(ctx, "Discovering installation ID", map[string]interface{}{
		"organization": org,
	})

	// Generate JWT for app-level authentication
	jwtToken, err := g.generateAppJWT()
	if err != nil {
		return 0, fmt.Errorf("failed to generate app JWT: %w", err)
	}

	// Query all installations for this app
	req, err := http.NewRequestWithContext(ctx, "GET",
		g.GetAPIBaseURL()+"/app/installations", nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to query installations: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("failed to query installations: %s - %s", resp.Status, string(body))
	}

	var installations []Installation
	if err := json.NewDecoder(resp.Body).Decode(&installations); err != nil {
		return 0, fmt.Errorf("failed to decode installations: %w", err)
	}

	// Find installation for the target organization
	for _, inst := range installations {
		g.logger.Debug(ctx, "Found installation", map[string]interface{}{
			"installation_id": inst.ID,
			"account":         inst.Account.Login,
			"type":            inst.Account.Type,
		})

		// Cache all discovered installations
		g.cacheMutex.Lock()
		g.installationCache[inst.Account.Login] = inst.ID
		g.cacheMutex.Unlock()

		if inst.Account.Login == org {
			g.logger.Info(ctx, "Resolved installation ID", map[string]interface{}{
				"installation_id": inst.ID,
				"organization":    org,
			})
			return inst.ID, nil
		}
	}

	return 0, fmt.Errorf("no installation found for organization: %s", org)
}

// GetClientForOrg returns an authenticated GraphQL client for the given organization.
// Clients are cached per organization.
func (g *GraphQLClient) GetClientForOrg(ctx context.Context, org string) (*githubv4.Client, error) {
	// Check client cache first
	g.clientMutex.RLock()
	if client, ok := g.clientCache[org]; ok {
		g.clientMutex.RUnlock()
		return client, nil
	}
	g.clientMutex.RUnlock()

	// Discover installation ID
	installationID, err := g.DiscoverInstallationID(ctx, org)
	if err != nil {
		return nil, err
	}

	// Create installation-authenticated transport
	transport, err := ghinstallation.New(
		http.DefaultTransport,
		g.appID,
		installationID,
		g.privateKey,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create installation transport: %w", err)
	}

	// Configure for GitHub Enterprise if URL provided
	if g.enterpriseURL != "" {
		transport.BaseURL = g.GetAPIBaseURL()
	}

	httpClient := &http.Client{Transport: transport}

	var client *githubv4.Client
	if g.enterpriseURL != "" {
		client = githubv4.NewEnterpriseClient(g.GetGraphQLURL(), httpClient)
	} else {
		client = githubv4.NewClient(httpClient)
	}

	// Cache the client
	g.clientMutex.Lock()
	g.clientCache[org] = client
	g.clientMutex.Unlock()

	return client, nil
}

// GetCommitAncestry retrieves the commit ancestry for a given ref.
// Returns a slice of commit SHAs in order from newest to oldest.
// This is useful for finding routing slips or tracking commit history.
func (g *GraphQLClient) GetCommitAncestry(ctx context.Context, owner, repo, ref string, depth int) ([]string, error) {
	// Get authenticated client for this organization
	client, err := g.GetClientForOrg(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for org %s: %w", owner, err)
	}

	var query struct {
		Repository struct {
			Object struct {
				Commit struct {
					History struct {
						Nodes []struct {
							Oid string
						}
					} `graphql:"history(first: $depth)"`
				} `graphql:"... on Commit"`
			} `graphql:"object(expression: $ref)"`
		} `graphql:"repository(owner: $owner, name: $repo)"`
	}

	variables := map[string]interface{}{
		"owner": githubv4.String(owner),
		"repo":  githubv4.String(repo),
		"ref":   githubv4.String(ref),
		"depth": githubv4.Int(depth),
	}

	if err := client.Query(ctx, &query, variables); err != nil {
		return nil, fmt.Errorf("failed to query commit history: %w", err)
	}

	commits := make([]string, 0, len(query.Repository.Object.Commit.History.Nodes))
	for _, node := range query.Repository.Object.Commit.History.Nodes {
		commits = append(commits, node.Oid)
	}

	g.logger.Debug(ctx, "Retrieved commit ancestry", map[string]interface{}{
		"count": len(commits),
		"owner": owner,
		"repo":  repo,
		"ref":   ref,
	})
	return commits, nil
}

// ClearCache clears the installation and client caches.
// This is useful for testing or when installations change.
func (g *GraphQLClient) ClearCache() {
	g.cacheMutex.Lock()
	g.installationCache = make(map[string]int64)
	g.cacheMutex.Unlock()

	g.clientMutex.Lock()
	g.clientCache = make(map[string]*githubv4.Client)
	g.clientMutex.Unlock()
}

// GetCachedInstallationIDs returns a copy of the cached installation IDs.
// This is useful for debugging or monitoring.
func (g *GraphQLClient) GetCachedInstallationIDs() map[string]int64 {
	g.cacheMutex.RLock()
	defer g.cacheMutex.RUnlock()

	result := make(map[string]int64, len(g.installationCache))
	for k, v := range g.installationCache {
		result[k] = v
	}
	return result
}
