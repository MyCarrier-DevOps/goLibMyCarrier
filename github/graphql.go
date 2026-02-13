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
	if cfg.PrivateKey != "" && cfg.PrivateKey[0] == '-' {
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
		g.GetAPIBaseURL()+"/app/installations", http.NoBody)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-Github-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to query installations: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			g.logger.Debug(ctx, "Failed to close response body", map[string]interface{}{"error": closeErr.Error()})
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		var bodyStr string
		if readErr != nil {
			bodyStr = fmt.Sprintf("(could not read body: %v)", readErr)
		} else {
			bodyStr = string(body)
		}
		// Detect authentication vs other errors
		isAuthErr := resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
		return 0, NewInstallationLookupError(org, fmt.Errorf("%s - %s", resp.Status, bodyStr), isAuthErr)
	}

	var installations []Installation
	if err := json.NewDecoder(resp.Body).Decode(&installations); err != nil {
		return 0, fmt.Errorf("failed to decode installations: %w", err)
	}

	// Collect all available organizations for error reporting
	availableOrgs := make([]string, 0, len(installations))

	// Find installation for the target organization
	for _, inst := range installations {
		g.logger.Debug(ctx, "Found installation", map[string]interface{}{
			"installation_id": inst.ID,
			"account":         inst.Account.Login,
			"type":            inst.Account.Type,
		})

		availableOrgs = append(availableOrgs, inst.Account.Login)

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

	// Return error with available orgs - let caller decide how to log based on shadow mode
	// The InstallationError includes available orgs for actionable debugging
	return 0, NewInstallationNotFoundError(org, availableOrgs)
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
//
// Only the first-parent chain is returned. This prevents merge commits from
// polluting ancestry with commits from other branches (e.g., merging main
// into a feature branch would otherwise include main's commits, causing
// incorrect slip resolution).
func (g *GraphQLClient) GetCommitAncestry(ctx context.Context, owner, repo, ref string, depth int) ([]string, error) {
	// Get authenticated client for this organization
	client, err := g.GetClientForOrg(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for org %s: %w", owner, err)
	}

	// Over-fetch to ensure we get enough commits for the first-parent chain.
	// The linearized history includes commits from merged branches; after filtering
	// to first-parent only, we need at least `depth` first-parent commits remaining.
	// GitHub's GraphQL API enforces a hard limit of 100 records per connection,
	// so we cap at 100. This means for very deep ancestry searches, the caller
	// may need multiple resolution strategies (e.g., image tag fallback).
	fetchDepth := depth * 5
	if fetchDepth > 100 {
		fetchDepth = 100
	}

	// Include parent OIDs so we can walk the first-parent chain locally.
	// GitHub's GraphQL history endpoint returns a linearized view that includes
	// commits from all parents of merge commits, which pollutes ancestry when
	// the default branch is merged into a feature branch.
	var query struct {
		Repository struct {
			Object struct {
				Commit struct {
					History struct {
						Nodes []struct {
							Oid     string
							Parents struct {
								Nodes []struct {
									Oid string
								}
							} `graphql:"parents(first: 1)"`
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
		"depth": githubv4.Int(fetchDepth),
	}

	if err := client.Query(ctx, &query, variables); err != nil {
		return nil, NewGraphQLError("GetCommitAncestry", owner, repo, ref, err)
	}

	allNodes := query.Repository.Object.Commit.History.Nodes

	// Filter to first-parent chain only.
	// Starting from HEAD, follow only the first parent of each commit.
	// This excludes commits brought in via merges from other branches.
	commits := filterFirstParentChain(allNodes, depth)

	g.logger.Debug(ctx, "Retrieved commit ancestry (first-parent)", map[string]interface{}{
		"fetched":          len(allNodes),
		"first_parent_len": len(commits),
		"owner":            owner,
		"repo":             repo,
		"ref":              ref,
	})
	return commits, nil
}

// filterFirstParentChain walks the first-parent chain from the fetched commit history.
// It starts at the first node (HEAD) and follows only the first parent of each commit,
// skipping any commits that were brought in via merges from other branches.
// Returns at most `depth` commit SHAs.
func filterFirstParentChain(nodes []struct {
	Oid     string
	Parents struct {
		Nodes []struct {
			Oid string
		}
	} `graphql:"parents(first: 1)"`
}, depth int) []string {
	if len(nodes) == 0 {
		return nil
	}

	// Build a lookup map: OID -> node for O(1) access
	nodeMap := make(map[string]int, len(nodes))
	for i, n := range nodes {
		nodeMap[n.Oid] = i
	}

	commits := make([]string, 0, depth)
	currentIdx := 0
	commits = append(commits, nodes[currentIdx].Oid)

	for len(commits) < depth {
		current := nodes[currentIdx]

		// No parents means we've reached the root commit
		if len(current.Parents.Nodes) == 0 {
			break
		}

		// Follow the first parent only (index 0 is the first parent in git)
		firstParentOid := current.Parents.Nodes[0].Oid
		nextIdx, exists := nodeMap[firstParentOid]
		if !exists {
			// First parent not in our fetched set — we've exhausted available data
			break
		}

		commits = append(commits, nodes[nextIdx].Oid)
		currentIdx = nextIdx
	}

	return commits
}

// GetPRHeadCommit retrieves the head commit SHA for a pull request.
// This is used to link squash merge commits back to the original feature branch slip.
// Returns the SHA of the PR's head commit before merging.
func (g *GraphQLClient) GetPRHeadCommit(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	// Get authenticated client for this organization
	client, err := g.GetClientForOrg(ctx, owner)
	if err != nil {
		return "", fmt.Errorf("failed to get client for org %s: %w", owner, err)
	}

	var query struct {
		Repository struct {
			PullRequest struct {
				HeadRefOid string
			} `graphql:"pullRequest(number: $prNumber)"`
		} `graphql:"repository(owner: $owner, name: $repo)"`
	}

	variables := map[string]interface{}{
		"owner":    githubv4.String(owner),
		"repo":     githubv4.String(repo),
		"prNumber": githubv4.Int(prNumber),
	}

	if err := client.Query(ctx, &query, variables); err != nil {
		return "", NewGraphQLError("GetPRHeadCommit", owner, repo, fmt.Sprintf("PR#%d", prNumber), err)
	}

	headCommit := query.Repository.PullRequest.HeadRefOid
	if headCommit == "" {
		return "", fmt.Errorf("%w: PR #%d in %s/%s", ErrPRNotFound, prNumber, owner, repo)
	}

	g.logger.Debug(ctx, "Retrieved PR head commit", map[string]interface{}{
		"owner":       owner,
		"repo":        repo,
		"pr_number":   prNumber,
		"head_commit": headCommit[:7], // Short SHA for logging
	})
	return headCommit, nil
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
