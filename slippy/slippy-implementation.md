# Slippy Implementation Plan

_Last updated: December 11, 2025_

## Executive Summary

This document provides a phased implementation plan for **Slippy**, the routing slip coordination layer for the MyCarrier CI/CD pipeline. Slippy is designed as both a **standalone application** (for use as an init container and exit handler) and a **reusable library** (published to `goLibMyCarrier` for use by other Go applications).

---

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [Dual-Mode Design: Library + Application](#dual-mode-design-library--application)
3. [Slip Resolution Strategy](#slip-resolution-strategy)
4. [Phase 1: Core Library Development](#phase-1-core-library-development)
5. [Phase 2: Standalone Application](#phase-2-standalone-application)
6. [Phase 3: ClickHouse Schema & Migrations](#phase-3-clickhouse-schema--migrations)
7. [Phase 4: push-hook-parser Integration](#phase-4-push-hook-parser-integration)
8. [Phase 5: Workflow Template Modifications](#phase-5-workflow-template-modifications)
9. [Phase 6: Testing Strategy](#phase-6-testing-strategy)
10. [Phase 7: Observability & Tooling](#phase-7-observability--tooling)
11. [Phase 8: Rollout Plan](#phase-8-rollout-plan)
12. [Risk Mitigation](#risk-mitigation)
13. [Timeline & Milestones](#timeline--milestones)

---

## Architecture Overview

### Component Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           goLibMyCarrier                                    │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                    github.com/MyCarrier-DevOps/goLibMyCarrier/slippy  │  │
│  │                                                                       │  │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐   │  │
│  │  │   types.go  │  │  client.go  │  │  prereqs.go │  │  history.go │   │  │
│  │  │             │  │             │  │             │  │             │   │  │
│  │  │ • Slip      │  │ • Load()    │  │ • Check()   │  │ • Record()  │   │  │
│  │  │ • Component │  │ • Save()    │  │ • Wait()    │  │ • Query()   │   │  │
│  │  │ • StepState │  │ • Update()  │  │ • Timeout() │  │             │   │  │
│  │  │ • History   │  │             │  │             │  │             │   │  │
│  │  └─────────────┘  └─────────────┘  └─────────────┘  └─────────────┘   │  │
│  │                                                                       │  │
│  │  ┌─────────────┐  ┌─────────────┐                                     │  │
│  │  │ executor.go │  │  config.go  │                                     │  │
│  │  │             │  │             │                                     │  │
│  │  │ • PreExec() │  │ • Options   │                                     │  │
│  │  │ • PostExec()│  │ • Defaults  │                                     │  │
│  │  └─────────────┘  └─────────────┘                                     │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                    ┌───────────────┴───────────────┐
                    │                               │
                    ▼                               ▼
    ┌───────────────────────────┐   ┌───────────────────────────┐
    │   ci/slippy (standalone)  │   │   Other Go Applications   │
    │                           │   │                           │
    │  • main.go (CLI wrapper)  │   │  • push-hook-parser       │
    │  • Dockerfile             │   │  • deploy-reporter        │
    │  • Runs as init/exit      │   │  • autotriggertests       │
    │    container              │   │  • etc.                   │
    └───────────────────────────┘   └───────────────────────────┘
```

### Data Flow

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  Git Push    │────▶│ push-hook-   │────▶│  ClickHouse  │
│  Webhook     │     │ parser       │     │  (slip store)│
└──────────────┘     │              │     └──────────────┘
                     │ Creates slip │              │
                     │ via library  │              │
                     └──────────────┘              │
                                                   │
┌──────────────────────────────────────────────────┼──────────────────────────┐
│                    Argo Workflow Pod             │                          │
│                                                  │                          │
│  ┌────────────────┐                              │                          │
│  │ Slippy (init)  │◀─────────────────────────────┘                          │
│  │                │      Load slip, check prereqs                           │
│  │ Uses library   │─────────────────────────────────▶ Update slip status    │
│  └────────────────┘                                                         │
│          │                                                                  │
│          ▼ (proceed/hold/abort)                                             │
│  ┌────────────────┐                                                         │
│  │ Main Workload  │                                                         │
│  │ (buildkit,     │                                                         │
│  │  unit-test,    │                                                         │
│  │  deploy, etc.) │                                                         │
│  └────────────────┘                                                         │
│          │                                                                  │
│          ▼                                                                  │
│  ┌────────────────┐                                                         │
│  │ Slippy (exit)  │─────────────────────────────────▶ Update final status   │
│  │                │                                                         │
│  │ Uses library   │                                                         │
│  └────────────────┘                                                         │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Dual-Mode Design: Library + Application

A critical requirement is that Slippy must function in **two modes**:

### Mode 1: Importable Library (`goLibMyCarrier/slippy`)

Other Go applications can import Slippy's core functionality:

```go
import (
    "github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

// Example: push-hook-parser creating a slip
func handlePush(ctx context.Context, event PushEvent) error {
    client, err := slippy.NewClient(slippy.DefaultConfig())
    if err != nil {
        return err
    }
    
    slip := slippy.NewSlip(slippy.SlipOptions{
        CorrelationID: event.CorrelationID,
        Repository:    event.Repository,
        Branch:        event.Branch,
        CommitSHA:     event.CommitSHA,
        Components:    detectComponents(event),
    })
    
    return client.Create(ctx, slip)
}
```

### Mode 2: Standalone Application (`ci/slippy`)

A thin CLI wrapper around the library for use as init container / exit handler:

```go
// cmd/slippy/main.go
package main

import (
    "context"
    "os"
    
    "github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
    "github.com/MyCarrier-DevOps/goLibMyCarrier/otel"
)

func main() {
    ctx := context.Background()
    logger := otel.NewAppLogger()
    
    config := slippy.ConfigFromEnv()
    client, err := slippy.NewClient(config)
    if err != nil {
        logger.Errorf("Failed to create slippy client: %v", err)
        os.Exit(1)
    }
    
    var exitCode int
    switch config.Mode {
    case slippy.ModePre:
        exitCode = runPreExecution(ctx, client, config, logger)
    case slippy.ModePost:
        exitCode = runPostExecution(ctx, client, config, logger)
    default:
        logger.Errorf("Unknown mode: %s", config.Mode)
        exitCode = 1
    }
    
    os.Exit(exitCode)
}
```

### Benefits of Dual-Mode Design

| Benefit | Description |
|---------|-------------|
| **Code reuse** | Core logic lives in one place, used everywhere |
| **Consistency** | Same slip operations regardless of caller |
| **Testability** | Library can be unit tested independently |
| **Flexibility** | Applications can extend/customize behavior |
| **Versioning** | Library versioned with goLibMyCarrier releases |

---

## Slip Resolution Strategy

### The Challenge

A core challenge with routing slips is **how to find the correct slip** when a workflow runs. The routing slip IS the correlation persistence mechanism, so we cannot use `correlation_id` to look up the slip we need. Additionally:

1. **Skip-CI commits** break linear commit → slip relationships
2. **Multiple concurrent pipelines** may be in flight for the same branch
3. **GitOps deploys** are triggered by ArgoCD detecting manifest changes, not Kafka events
4. **No native persistence** of correlation IDs between workflow jobs

### Scenario Example

```
Commit A (abc123) → Full CI pipeline → Slip created ✓
Commit B (def456) → [skip ci] docs change → No slip created
Commit C (ghi789) → Full CI pipeline → Slip created ✓
```

When `dev_deploy` runs for Commit C, slippy must find the slip for `ghi789`, not `def456`.

### Resolution Methods Evaluated

| Method | Description | Reliability | Concurrent-Safe |
|--------|-------------|-------------|-----------------|
| **Commit Ancestry** | Walk git history to find nearest slip | High | ✅ Yes |
| Latest Active | Find most recent in-progress slip | Low | ❌ No |
| Component Hash | Match by component set | Medium | ❌ No |
| **Image Tag** | Extract commit SHA from image tag | High | ✅ Yes |
| Manifest Annotation | Read from K8s manifest | High | ✅ Yes |

### Selected Approach: Commit Ancestry (Primary) + Image Tag (Fallback)

**Commit Ancestry** is the primary resolution strategy because:
1. Git history is immutable and deterministic
2. Handles skip-ci commits naturally (they're skipped in ancestry)
3. Works even when events lose metadata
4. Concurrent-safe (each workflow knows its own HEAD)

**Image Tag** serves as fallback for deploy steps where we know exactly which image we're deploying.

### GitHub API Integration

Git access is achieved via the **GitHub GraphQL API** using a GitHub App created in the MyCarrier GitHub Cloud Enterprise. The app is installed across multiple organizations, and the appropriate installation ID is dynamically resolved based on the target repository's organization.

#### Environment Variables for Authentication

| Variable | Description | Required |
|----------|-------------|----------|
| `SLIPPY_GITHUB_APP_ID` | GitHub App ID | Yes |
| `SLIPPY_GITHUB_APP_PRIVATE_KEY` | PEM-encoded private key (or path to file) | Yes |
| `SLIPPY_GITHUB_ENTERPRISE_URL` | GitHub Enterprise base URL (if using GHE Server) | No |

> **Note**: Installation ID is **not** required as an environment variable. The library automatically discovers the correct installation for each organization by querying the GitHub App's installations.

#### Library Implementation

**File**: `goLibMyCarrier/slippy/github.go`

```go
package slippy

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
)

// GitHubConfig holds GitHub API authentication configuration
type GitHubConfig struct {
    AppID         int64  `mapstructure:"SLIPPY_GITHUB_APP_ID"`
    PrivateKey    string `mapstructure:"SLIPPY_GITHUB_APP_PRIVATE_KEY"`
    EnterpriseURL string `mapstructure:"SLIPPY_GITHUB_ENTERPRISE_URL"`
}

// GitHubClient wraps the GitHub GraphQL client with automatic installation discovery
type GitHubClient struct {
    appID         int64
    privateKey    []byte
    enterpriseURL string
    logger        Logger
    
    // Cache of org -> installation ID mappings
    installationCache map[string]int64
    cacheMutex        sync.RWMutex
    
    // Cache of org -> authenticated client
    clientCache map[string]*githubv4.Client
    clientMutex sync.RWMutex
}

// Installation represents a GitHub App installation
type Installation struct {
    ID      int64 `json:"id"`
    Account struct {
        Login string `json:"login"`
        Type  string `json:"type"`
    } `json:"account"`
}

// NewGitHubClient creates a new GitHub client with App authentication
func NewGitHubClient(cfg GitHubConfig, logger Logger) (*GitHubClient, error) {
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

    return &GitHubClient{
        appID:             cfg.AppID,
        privateKey:        privateKey,
        enterpriseURL:     cfg.EnterpriseURL,
        logger:            logger,
        installationCache: make(map[string]int64),
        clientCache:       make(map[string]*githubv4.Client),
    }, nil
}

// getAPIBaseURL returns the appropriate API base URL
func (g *GitHubClient) getAPIBaseURL() string {
    if g.enterpriseURL != "" {
        return g.enterpriseURL + "/api/v3"
    }
    return "https://api.github.com"
}

// getGraphQLURL returns the appropriate GraphQL URL
func (g *GitHubClient) getGraphQLURL() string {
    if g.enterpriseURL != "" {
        return g.enterpriseURL + "/api/graphql"
    }
    return "https://api.github.com/graphql"
}

// generateAppJWT creates a JWT for authenticating as the GitHub App
func (g *GitHubClient) generateAppJWT() (string, error) {
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

// discoverInstallationID finds the installation ID for a given organization
func (g *GitHubClient) discoverInstallationID(ctx context.Context, org string) (int64, error) {
    // Check cache first
    g.cacheMutex.RLock()
    if id, ok := g.installationCache[org]; ok {
        g.cacheMutex.RUnlock()
        return id, nil
    }
    g.cacheMutex.RUnlock()

    g.logger.Debugf("Discovering installation ID for organization: %s", org)

    // Generate JWT for app-level authentication
    jwtToken, err := g.generateAppJWT()
    if err != nil {
        return 0, fmt.Errorf("failed to generate app JWT: %w", err)
    }

    // Query all installations for this app
    req, err := http.NewRequestWithContext(ctx, "GET", 
        g.getAPIBaseURL()+"/app/installations", nil)
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
        g.logger.Debugf("Found installation %d for %s (%s)", 
            inst.ID, inst.Account.Login, inst.Account.Type)
        
        // Cache all discovered installations
        g.cacheMutex.Lock()
        g.installationCache[inst.Account.Login] = inst.ID
        g.cacheMutex.Unlock()

        if inst.Account.Login == org {
            g.logger.Infof("Resolved installation ID %d for organization %s", inst.ID, org)
            return inst.ID, nil
        }
    }

    return 0, fmt.Errorf("no installation found for organization: %s", org)
}

// getClientForOrg returns an authenticated GraphQL client for the given organization
func (g *GitHubClient) getClientForOrg(ctx context.Context, org string) (*githubv4.Client, error) {
    // Check client cache first
    g.clientMutex.RLock()
    if client, ok := g.clientCache[org]; ok {
        g.clientMutex.RUnlock()
        return client, nil
    }
    g.clientMutex.RUnlock()

    // Discover installation ID
    installationID, err := g.discoverInstallationID(ctx, org)
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
        transport.BaseURL = g.getAPIBaseURL()
    }

    httpClient := &http.Client{Transport: transport}
    
    var client *githubv4.Client
    if g.enterpriseURL != "" {
        client = githubv4.NewEnterpriseClient(g.getGraphQLURL(), httpClient)
    } else {
        client = githubv4.NewClient(httpClient)
    }

    // Cache the client
    g.clientMutex.Lock()
    g.clientCache[org] = client
    g.clientMutex.Unlock()

    return client, nil
}

// GetCommitAncestry retrieves the commit ancestry for a given ref
func (g *GitHubClient) GetCommitAncestry(ctx context.Context, owner, repo, ref string, depth int) ([]string, error) {
    // Get authenticated client for this organization
    client, err := g.getClientForOrg(ctx, owner)
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

    g.logger.Debugf("Retrieved %d commits in ancestry for %s/%s@%s", len(commits), owner, repo, ref)
    return commits, nil
}

// ClearCache clears the installation and client caches (useful for testing or rotation)
func (g *GitHubClient) ClearCache() {
    g.cacheMutex.Lock()
    g.installationCache = make(map[string]int64)
    g.cacheMutex.Unlock()
    
    g.clientMutex.Lock()
    g.clientCache = make(map[string]*githubv4.Client)
    g.clientMutex.Unlock()
}
```

#### Slip Resolution Implementation

**File**: `goLibMyCarrier/slippy/resolve.go`

```go
package slippy

import (
    "context"
    "fmt"
    "regexp"
    "strings"
)

// ResolveOptions contains parameters for slip resolution
type ResolveOptions struct {
    // Repository in owner/repo format
    Repository string
    
    // Branch name (e.g., "main", "feature/xyz")
    Branch string
    
    // Ref to resolve ancestry from (e.g., "HEAD", "main", commit SHA)
    Ref string
    
    // ImageTag for fallback resolution (e.g., "mycarrier/svc:abc123-1234567890")
    ImageTag string
    
    // AncestryDepth is how many commits to check (default: 20)
    AncestryDepth int
}

// ResolveResult contains the resolved slip and metadata
type ResolveResult struct {
    Slip          *Slip
    ResolvedBy    string // "ancestry", "image_tag", "commit_sha"
    MatchedCommit string
}

// ResolveSlip finds the correct routing slip for a workflow execution
func (c *Client) ResolveSlip(ctx context.Context, opts ResolveOptions) (*ResolveResult, error) {
    if opts.AncestryDepth == 0 {
        opts.AncestryDepth = 20
    }

    // Parse owner/repo
    owner, repo, err := parseRepository(opts.Repository)
    if err != nil {
        return nil, err
    }

    // Primary: Commit ancestry via GitHub GraphQL API
    if opts.Ref != "" {
        c.logger.Infof("Resolving slip via commit ancestry for %s@%s", opts.Repository, opts.Ref)
        
        commits, err := c.github.GetCommitAncestry(ctx, owner, repo, opts.Ref, opts.AncestryDepth)
        if err != nil {
            c.logger.Warnf("Failed to get commit ancestry: %v", err)
        } else {
            slip, matchedCommit, err := c.store.FindByCommits(ctx, opts.Repository, commits)
            if err == nil && slip != nil {
                c.logger.Infof("Resolved slip %s via ancestry (commit %s)", slip.SlipID, shortSHA(matchedCommit))
                return &ResolveResult{
                    Slip:          slip,
                    ResolvedBy:    "ancestry",
                    MatchedCommit: matchedCommit,
                }, nil
            }
        }
    }

    // Fallback: Extract commit SHA from image tag
    if opts.ImageTag != "" {
        c.logger.Infof("Attempting fallback resolution via image tag: %s", opts.ImageTag)
        
        commitSHA := extractCommitFromImageTag(opts.ImageTag)
        if commitSHA != "" {
            slip, err := c.store.FindByCommit(ctx, opts.Repository, commitSHA)
            if err == nil && slip != nil {
                c.logger.Infof("Resolved slip %s via image tag (commit %s)", slip.SlipID, shortSHA(commitSHA))
                return &ResolveResult{
                    Slip:          slip,
                    ResolvedBy:    "image_tag",
                    MatchedCommit: commitSHA,
                }, nil
            }
        }
    }

    return nil, fmt.Errorf("no slip found for repository %s", opts.Repository)
}

// parseRepository splits "owner/repo" into components
func parseRepository(fullName string) (owner, repo string, err error) {
    parts := strings.SplitN(fullName, "/", 2)
    if len(parts) != 2 {
        return "", "", fmt.Errorf("invalid repository format: %s (expected owner/repo)", fullName)
    }
    return parts[0], parts[1], nil
}

// extractCommitFromImageTag extracts commit SHA from image tag
// Supports formats:
//   - mycarrier/svc:abc1234-1234567890 (commit-timestamp)
//   - mycarrier/svc:abc1234 (short sha)
//   - mycarrier/svc:v1.2.3-abc1234 (semver with sha)
var imageTagCommitPattern = regexp.MustCompile(`(?:^|[:-])([a-f0-9]{7,40})(?:$|-)`)

func extractCommitFromImageTag(tag string) string {
    // Get just the tag portion after the colon
    if idx := strings.LastIndex(tag, ":"); idx != -1 {
        tag = tag[idx+1:]
    }
    
    matches := imageTagCommitPattern.FindStringSubmatch(tag)
    if len(matches) >= 2 {
        return matches[1]
    }
    return ""
}
```

### ClickHouse Query for Ancestry Lookup

**File**: `goLibMyCarrier/slippy/clickhouse_store.go` (addition)

```go
// FindByCommits finds a slip matching any commit in the ordered list
// Returns the slip for the first (most recent) matching commit
func (s *ClickHouseStore) FindByCommits(ctx context.Context, repository string, commits []string) (*Slip, string, error) {
    if len(commits) == 0 {
        return nil, "", fmt.Errorf("no commits provided")
    }

    // Build query with ordered commit matching
    // Uses arrayJoin to preserve order priority
    query := `
        WITH 
            commits AS (
                SELECT 
                    arrayJoin(arrayEnumerate({commits:Array(String)})) AS priority,
                    arrayJoin({commits:Array(String)}) AS commit_sha
            )
        SELECT 
            s.*,
            c.commit_sha AS matched_commit
        FROM ci.routing_slips s FINAL
        JOIN commits c ON s.commit_sha = c.commit_sha
        WHERE s.repository = {repository:String}
        ORDER BY c.priority ASC
        LIMIT 1
    `

    var slip Slip
    var matchedCommit string
    
    row := s.conn.QueryRow(ctx, query, 
        clickhouse.Named("repository", repository),
        clickhouse.Named("commits", commits),
    )
    
    if err := row.ScanStruct(&slip); err != nil {
        if err == sql.ErrNoRows {
            return nil, "", ErrSlipNotFound
        }
        return nil, "", fmt.Errorf("failed to query slip by commits: %w", err)
    }

    return &slip, matchedCommit, nil
}
```

### Pre-Execution Flow with Resolution

```go
// Updated RunPreExecution in client.go
func (c *Client) RunPreExecution(ctx context.Context, opts PreExecutionOptions) (*PreExecutionResult, error) {
    // Step 1: Resolve the correct slip
    resolveResult, err := c.ResolveSlip(ctx, ResolveOptions{
        Repository:    opts.Repository,
        Branch:        opts.Branch,
        Ref:           opts.Ref,           // Usually "HEAD" or the checked-out commit
        ImageTag:      opts.ImageTag,      // For deploy steps
        AncestryDepth: 20,
    })
    
    if err != nil {
        if c.config.ShadowMode {
            c.logger.Warnf("[SHADOW] No slip found, would proceed anyway: %v", err)
            return &PreExecutionResult{Outcome: PreExecutionOutcomeProceed}, nil
        }
        return nil, fmt.Errorf("failed to resolve slip: %w", err)
    }

    slip := resolveResult.Slip
    c.logger.Infof("Resolved slip %s via %s (commit %s)", 
        slip.SlipID, resolveResult.ResolvedBy, shortSHA(resolveResult.MatchedCommit))

    // Step 2: Now we have the correlation_id for downstream use
    // Set it in the result for the workflow to propagate
    result := &PreExecutionResult{
        SlipID:        slip.SlipID.String(),
        CorrelationID: slip.CorrelationID,
    }

    // Step 3: Check prerequisites as before
    // ... rest of existing logic ...
}
```

### Environment Variables for Standalone App

The standalone slippy application receives these environment variables:

| Variable | Description | Example |
|----------|-------------|---------|
| `SLIPPY_MODE` | Execution mode | `pre` or `post` |
| `SLIPPY_REPOSITORY` | Target repository | `MyCarrier-DevOps/my-service` |
| `SLIPPY_BRANCH` | Git branch | `main` |
| `SLIPPY_REF` | Git ref to resolve from | `HEAD` |
| `SLIPPY_STEP_NAME` | Current pipeline step | `dev_deploy` |
| `SLIPPY_COMPONENT_NAME` | Component name (if applicable) | `svc-api` |
| `SLIPPY_IMAGE_TAG` | Image being deployed (for fallback) | `mycarrier/svc:abc123-1234567890` |
| `SLIPPY_PREREQUISITES` | Comma-separated prereq steps | `builds_completed,unit_tests_completed` |
| `SLIPPY_HOLD_TIMEOUT` | Max time to wait for prereqs | `60m` |
| `SLIPPY_POLL_INTERVAL` | Interval between prereq checks | `60s` |
| `SLIPPY_GITHUB_APP_ID` | GitHub App ID | `12345` |
| `SLIPPY_GITHUB_APP_PRIVATE_KEY` | Private key (or path) | `/secrets/github-app.pem` |
| `SLIPPY_GITHUB_ENTERPRISE_URL` | GHE Server base URL (optional) | `https://github.mycompany.com` |
| `SLIPPY_CLICKHOUSE_DSN` | ClickHouse connection string | `clickhouse://host:9000/ci` |

> **Note**: The GitHub App installation ID is automatically discovered based on the repository's organization. No manual configuration required.

---

## Phase 1: Core Library Development


**Goal**: Build the foundational `goLibMyCarrier/slippy` library with all core types and operations.

### 1.1 Repository Setup

**Location**: `github.com/MyCarrier-DevOps/goLibMyCarrier/slippy/`

**File Structure**:
```
goLibMyCarrier/
└── slippy/
    ├── types.go           # Core data structures
    ├── status.go          # Status enums and transitions
    ├── config.go          # Configuration management
    ├── client.go          # Main client with all dependencies
    ├── github.go          # GitHub API client with installation discovery
    ├── resolve.go         # Slip resolution via commit ancestry
    ├── slip.go            # Slip CRUD operations
    ├── prereqs.go         # Prerequisite checking logic
    ├── hold.go            # Hold/wait logic with polling
    ├── history.go         # State history recording
    ├── executor.go        # Pre/Post execution orchestration
    ├── clickhouse_store.go # ClickHouse persistence layer
    ├── errors.go          # Custom error types
    ├── interfaces.go      # Interfaces for testing/mocking
    ├── z_types_test.go    # Unit tests
    ├── z_client_test.go
    ├── z_github_test.go
    ├── z_resolve_test.go
    ├── z_prereqs_test.go
    ├── z_hold_test.go
    ├── z_executor_test.go
    └── z_integration_test.go
```

### 1.2 Core Types (`types.go`)

```go
package slippy

import (
    "time"
    
    "github.com/google/uuid"
)

// Slip represents a routing slip tracking a pipeline execution
type Slip struct {
    SlipID        uuid.UUID         `json:"slip_id"`
    CorrelationID string            `json:"correlation_id"`
    Repository    string            `json:"repository"`
    Branch        string            `json:"branch"`
    CommitSHA     string            `json:"commit_sha"`
    CreatedAt     time.Time         `json:"created_at"`
    UpdatedAt     time.Time         `json:"updated_at"`
    Status        SlipStatus        `json:"status"`
    Components    []Component       `json:"components"`
    Steps         map[string]Step   `json:"steps"`
    StateHistory  []StateHistoryEntry `json:"state_history"`
}

// Component represents a buildable component within a repository
type Component struct {
    Name           string     `json:"name"`
    DockerfilePath string     `json:"dockerfile_path"`
    BuildStatus    StepStatus `json:"build_status"`
    UnitTestStatus StepStatus `json:"unit_test_status"`
    ImageTag       string     `json:"image_tag,omitempty"`
}

// Step represents a pipeline step's current state
type Step struct {
    Status      StepStatus `json:"status"`
    StartedAt   *time.Time `json:"started_at,omitempty"`
    CompletedAt *time.Time `json:"completed_at,omitempty"`
    Actor       string     `json:"actor,omitempty"`
    Error       string     `json:"error,omitempty"`
}

// StateHistoryEntry records a state transition for audit
type StateHistoryEntry struct {
    Step      string     `json:"step"`
    Component string     `json:"component,omitempty"`
    Status    StepStatus `json:"status"`
    Timestamp time.Time  `json:"timestamp"`
    Actor     string     `json:"actor"`
    Message   string     `json:"message,omitempty"`
}

// SlipOptions configures a new slip
type SlipOptions struct {
    CorrelationID string
    Repository    string
    Branch        string
    CommitSHA     string
    Components    []ComponentDefinition
}

// ComponentDefinition defines a component to track
type ComponentDefinition struct {
    Name           string
    DockerfilePath string
}
```

### 1.3 Status Types (`status.go`)

```go
package slippy

// SlipStatus represents the overall status of a routing slip
type SlipStatus string

const (
    SlipStatusPending      SlipStatus = "pending"
    SlipStatusInProgress   SlipStatus = "in_progress"
    SlipStatusCompleted    SlipStatus = "completed"
    SlipStatusFailed       SlipStatus = "failed"
    SlipStatusCompensating SlipStatus = "compensating"
    SlipStatusCompensated  SlipStatus = "compensated"
)

// StepStatus represents the status of an individual step
type StepStatus string

const (
    StepStatusPending   StepStatus = "pending"
    StepStatusHeld      StepStatus = "held"
    StepStatusRunning   StepStatus = "running"
    StepStatusCompleted StepStatus = "completed"
    StepStatusFailed    StepStatus = "failed"
    StepStatusError     StepStatus = "error"
    StepStatusAborted   StepStatus = "aborted"
    StepStatusTimeout   StepStatus = "timeout"
    StepStatusSkipped   StepStatus = "skipped"
)

// IsTerminal returns true if the status is a terminal state
func (s StepStatus) IsTerminal() bool {
    switch s {
    case StepStatusCompleted, StepStatusFailed, StepStatusError,
         StepStatusAborted, StepStatusTimeout, StepStatusSkipped:
        return true
    }
    return false
}

// IsSuccess returns true if the status indicates success
func (s StepStatus) IsSuccess() bool {
    return s == StepStatusCompleted || s == StepStatusSkipped
}

// IsFailure returns true if the status indicates failure
func (s StepStatus) IsFailure() bool {
    switch s {
    case StepStatusFailed, StepStatusError, StepStatusAborted, StepStatusTimeout:
        return true
    }
    return false
}
```

### 1.4 Interfaces (`interfaces.go`)

```go
package slippy

import "context"

// SlipStore defines the interface for slip persistence
type SlipStore interface {
    Create(ctx context.Context, slip *Slip) error
    Load(ctx context.Context, correlationID string) (*Slip, error)
    LoadByCommit(ctx context.Context, repo, commitSHA string) (*Slip, error)
    Update(ctx context.Context, slip *Slip) error
    UpdateStep(ctx context.Context, slipID string, step string, component string, status StepStatus) error
    AppendHistory(ctx context.Context, slipID string, entry StateHistoryEntry) error
}

// PrerequisiteChecker checks if prerequisites are satisfied
type PrerequisiteChecker interface {
    Check(ctx context.Context, slip *Slip, prerequisites []string, component string) (PrereqResult, error)
}

// PrereqResult represents the result of a prerequisite check
type PrereqResult struct {
    Status          PrereqStatus
    FailedPrereqs   []string
    RunningPrereqs  []string
    CompletedPrereqs []string
}

// PrereqStatus represents the aggregate prerequisite status
type PrereqStatus string

const (
    PrereqStatusCompleted PrereqStatus = "completed"
    PrereqStatusRunning   PrereqStatus = "running"
    PrereqStatusFailed    PrereqStatus = "failed"
)

// Logger interface for dependency injection
type Logger interface {
    Info(args ...interface{})
    Infof(format string, args ...interface{})
    Error(args ...interface{})
    Errorf(format string, args ...interface{})
    Debug(args ...interface{})
    Debugf(format string, args ...interface{})
}
```

### 1.5 Client & Configuration (`client.go`, `config.go`)

```go
// config.go
package slippy

import (
    "fmt"
    "os"
    "strconv"
    "time"
)

// Config holds configuration for the slippy client
type Config struct {
    // ClickHouse connection
    ClickHouseDSN string
    
    // GitHub App authentication
    GitHubAppID         int64
    GitHubPrivateKey    string // PEM content or file path
    GitHubEnterpriseURL string // Optional: for GHE Server
    
    // Behavior
    Logger       Logger
    HoldTimeout  time.Duration // Default: 60m
    PollInterval time.Duration // Default: 60s
    ShadowMode   bool          // If true, never actually hold/skip
}

// DefaultConfig returns a Config with sensible defaults
func DefaultConfig() Config {
    return Config{
        HoldTimeout:  60 * time.Minute,
        PollInterval: 60 * time.Second,
    }
}

// ConfigFromEnv loads configuration from environment variables
func ConfigFromEnv() Config {
    cfg := DefaultConfig()
    
    // ClickHouse
    if dsn := os.Getenv("SLIPPY_CLICKHOUSE_DSN"); dsn != "" {
        cfg.ClickHouseDSN = dsn
    }
    
    // GitHub App authentication
    if appID := os.Getenv("SLIPPY_GITHUB_APP_ID"); appID != "" {
        if id, err := strconv.ParseInt(appID, 10, 64); err == nil {
            cfg.GitHubAppID = id
        }
    }
    cfg.GitHubPrivateKey = os.Getenv("SLIPPY_GITHUB_APP_PRIVATE_KEY")
    cfg.GitHubEnterpriseURL = os.Getenv("SLIPPY_GITHUB_ENTERPRISE_URL")
    
    // Behavior
    if timeout, err := time.ParseDuration(os.Getenv("SLIPPY_HOLD_TIMEOUT")); err == nil {
        cfg.HoldTimeout = timeout
    }
    if interval, err := time.ParseDuration(os.Getenv("SLIPPY_POLL_INTERVAL")); err == nil {
        cfg.PollInterval = interval
    }
    cfg.ShadowMode = os.Getenv("SLIPPY_SHADOW_MODE") == "true"
    
    return cfg
}

// Validate checks that all required configuration is present
func (c Config) Validate() error {
    if c.ClickHouseDSN == "" {
        return fmt.Errorf("ClickHouseDSN is required")
    }
    if c.GitHubAppID == 0 {
        return fmt.Errorf("GitHubAppID is required")
    }
    if c.GitHubPrivateKey == "" {
        return fmt.Errorf("GitHubPrivateKey is required")
    }
    return nil
}
```

```go
// client.go
package slippy

import (
    "context"
    "fmt"
)

// Client is the main entry point for slippy operations
type Client struct {
    store  SlipStore
    github *GitHubClient
    config Config
    logger Logger
}

// NewClient creates a new slippy client with all dependencies
func NewClient(config Config) (*Client, error) {
    if err := config.Validate(); err != nil {
        return nil, fmt.Errorf("invalid configuration: %w", err)
    }
    
    if config.Logger == nil {
        config.Logger = &NopLogger{}
    }
    
    // Initialize ClickHouse store
    store, err := NewClickHouseStore(config.ClickHouseDSN)
    if err != nil {
        return nil, fmt.Errorf("failed to create store: %w", err)
    }
    
    // Initialize GitHub client for commit ancestry resolution
    githubClient, err := NewGitHubClient(GitHubConfig{
        AppID:         config.GitHubAppID,
        PrivateKey:    config.GitHubPrivateKey,
        EnterpriseURL: config.GitHubEnterpriseURL,
    }, config.Logger)
    if err != nil {
        return nil, fmt.Errorf("failed to create GitHub client: %w", err)
    }
    
    return &Client{
        store:  store,
        github: githubClient,
        config: config,
        logger: config.Logger,
    }, nil
}

// NewClientWithDependencies creates a client with custom dependencies (for testing)
func NewClientWithDependencies(store SlipStore, github *GitHubClient, config Config) *Client {
    if config.Logger == nil {
        config.Logger = &NopLogger{}
    }
    return &Client{
        store:  store,
        github: github,
        config: config,
        logger: config.Logger,
    }
}

// Load retrieves a slip by correlation ID
func (c *Client) Load(ctx context.Context, correlationID string) (*Slip, error) {
    return c.store.Load(ctx, correlationID)
}

// LoadByCommit retrieves a slip by repository and commit SHA
func (c *Client) LoadByCommit(ctx context.Context, repo, commitSHA string) (*Slip, error) {
    return c.store.LoadByCommit(ctx, repo, commitSHA)
}
```

### 1.6 Push Event Handling (`push.go`)

```go
// push.go - Slip creation for push events
package slippy

import (
    "context"
    "fmt"
    "time"

    "github.com/google/uuid"
)

// PushOptions contains the information needed to create a slip from a push event
type PushOptions struct {
    CorrelationID string
    Repository    string
    Branch        string
    CommitSHA     string
    Components    []ComponentDefinition
}

// Validate checks that all required fields are present
func (o PushOptions) Validate() error {
    if o.CorrelationID == "" {
        return fmt.Errorf("correlation_id is required")
    }
    if o.Repository == "" {
        return fmt.Errorf("repository is required")
    }
    if o.CommitSHA == "" {
        return fmt.Errorf("commit_sha is required")
    }
    return nil
}

// CreateSlipForPush creates a new routing slip for a git push event.
// If a slip already exists for this commit (retry scenario), it resets
// the push_parsed step and returns the existing slip.
func (c *Client) CreateSlipForPush(ctx context.Context, opts PushOptions) (*Slip, error) {
    if err := opts.Validate(); err != nil {
        return nil, fmt.Errorf("invalid push options: %w", err)
    }

    c.logger.Infof("Creating routing slip for %s@%s", opts.Repository, shortSHA(opts.CommitSHA))

    // Check for existing slip (retry detection)
    existingSlip, err := c.store.LoadByCommit(ctx, opts.Repository, opts.CommitSHA)
    if err == nil && existingSlip != nil {
        return c.handlePushRetry(ctx, existingSlip)
    }

    // Create new slip with full initialization
    slip := c.initializeSlipForPush(opts)

    if err := c.store.Create(ctx, slip); err != nil {
        return nil, fmt.Errorf("failed to create slip: %w", err)
    }

    c.logger.Infof("Created routing slip %s with %d components", slip.SlipID, len(opts.Components))
    return slip, nil
}

// handlePushRetry resets a slip for retry processing
func (c *Client) handlePushRetry(ctx context.Context, slip *Slip) (*Slip, error) {
    c.logger.Infof("Found existing slip %s for commit %s, handling retry...",
        slip.SlipID, shortSHA(slip.CommitSHA))

    entry := StateHistoryEntry{
        Step:      "push_parsed",
        Status:    StepStatusRunning,
        Timestamp: time.Now(),
        Actor:     "slippy-library",
        Message:   "retry detected, resetting push_parsed",
    }

    if err := c.store.UpdateStep(ctx, slip.SlipID.String(), "push_parsed", "", StepStatusRunning); err != nil {
        return nil, fmt.Errorf("failed to reset push_parsed: %w", err)
    }

    if err := c.store.AppendHistory(ctx, slip.SlipID.String(), entry); err != nil {
        c.logger.Errorf("Failed to append history for retry: %v", err)
    }

    return c.store.Load(ctx, slip.CorrelationID)
}

// initializeSlipForPush creates a fully initialized slip for a push event
func (c *Client) initializeSlipForPush(opts PushOptions) *Slip {
    now := time.Now()
    slipID := uuid.New()

    // Initialize components
    components := make([]Component, len(opts.Components))
    for i, def := range opts.Components {
        components[i] = Component{
            Name:           def.Name,
            DockerfilePath: def.DockerfilePath,
            BuildStatus:    StepStatusPending,
            UnitTestStatus: StepStatusPending,
        }
    }

    // Initialize all pipeline steps as pending (push_parsed starts as running)
    steps := map[string]Step{
        "push_parsed":           {Status: StepStatusRunning, StartedAt: &now},
        "builds_completed":      {Status: StepStatusPending},
        "unit_tests_completed":  {Status: StepStatusPending},
        "secret_scan_completed": {Status: StepStatusPending},
        "dev_deploy":            {Status: StepStatusPending},
        "dev_tests":             {Status: StepStatusPending},
        "preprod_deploy":        {Status: StepStatusPending},
        "preprod_tests":         {Status: StepStatusPending},
        "prod_release_created":  {Status: StepStatusPending},
        "prod_deploy":           {Status: StepStatusPending},
        "prod_tests":            {Status: StepStatusPending},
        "alert_gate":            {Status: StepStatusPending},
        "prod_steady_state":     {Status: StepStatusPending},
    }

    history := []StateHistoryEntry{
        {
            Step:      "push_parsed",
            Status:    StepStatusRunning,
            Timestamp: now,
            Actor:     "slippy-library",
            Message:   "processing push event",
        },
    }

    return &Slip{
        SlipID:        slipID,
        CorrelationID: opts.CorrelationID,
        Repository:    opts.Repository,
        Branch:        opts.Branch,
        CommitSHA:     opts.CommitSHA,
        CreatedAt:     now,
        UpdatedAt:     now,
        Status:        SlipStatusInProgress,
        Components:    components,
        Steps:         steps,
        StateHistory:  history,
    }
}

func shortSHA(sha string) string {
    if len(sha) > 7 {
        return sha[:7]
    }
    return sha
}
```

### 1.7 Step Management (`steps.go`)

```go
// steps.go - Step status updates and aggregate management
package slippy

import (
    "context"
    "fmt"
    "time"
)

// CompleteStep marks a step as completed and records history
func (c *Client) CompleteStep(ctx context.Context, slipID, stepName, componentName string) error {
    return c.UpdateStepWithStatus(ctx, slipID, stepName, componentName, 
        StepStatusCompleted, "step completed successfully")
}

// FailStep marks a step as failed and records history
func (c *Client) FailStep(ctx context.Context, slipID, stepName, componentName, reason string) error {
    return c.UpdateStepWithStatus(ctx, slipID, stepName, componentName, 
        StepStatusFailed, reason)
}

// StartStep marks a step as running
func (c *Client) StartStep(ctx context.Context, slipID, stepName, componentName string) error {
    return c.UpdateStepWithStatus(ctx, slipID, stepName, componentName,
        StepStatusRunning, "step started")
}

// HoldStep marks a step as held waiting for prerequisites
func (c *Client) HoldStep(ctx context.Context, slipID, stepName, componentName, waitingFor string) error {
    return c.UpdateStepWithStatus(ctx, slipID, stepName, componentName,
        StepStatusHeld, fmt.Sprintf("waiting for: %s", waitingFor))
}

// AbortStep marks a step as aborted due to upstream failure
func (c *Client) AbortStep(ctx context.Context, slipID, stepName, componentName, reason string) error {
    return c.UpdateStepWithStatus(ctx, slipID, stepName, componentName,
        StepStatusAborted, reason)
}

// TimeoutStep marks a step as timed out
func (c *Client) TimeoutStep(ctx context.Context, slipID, stepName, componentName, reason string) error {
    return c.UpdateStepWithStatus(ctx, slipID, stepName, componentName,
        StepStatusTimeout, reason)
}

// UpdateStepWithStatus updates a step's status with a message and records history
func (c *Client) UpdateStepWithStatus(ctx context.Context, slipID, stepName, componentName string, status StepStatus, message string) error {
    entry := StateHistoryEntry{
        Step:      stepName,
        Component: componentName,
        Status:    status,
        Timestamp: time.Now(),
        Actor:     "slippy-library",
        Message:   message,
    }

    if err := c.store.UpdateStep(ctx, slipID, stepName, componentName, status); err != nil {
        return fmt.Errorf("failed to update step %s: %w", stepName, err)
    }

    if err := c.store.AppendHistory(ctx, slipID, entry); err != nil {
        c.logger.Errorf("Failed to append history: %v", err)
        // Non-fatal - continue
    }

    // Check if this affects aggregates (build/unit_test -> aggregate steps)
    if stepName == "build" || stepName == "unit_test" {
        c.checkAndUpdateAggregates(ctx, slipID, stepName)
    }

    return nil
}

// checkAndUpdateAggregates checks if all components have completed a step type
// and updates the corresponding aggregate step (builds_completed, unit_tests_completed)
func (c *Client) checkAndUpdateAggregates(ctx context.Context, slipID, stepName string) {
    aggregateMap := map[string]string{
        "build":     "builds_completed",
        "unit_test": "unit_tests_completed",
    }

    aggregateStep, ok := aggregateMap[stepName]
    if !ok {
        return
    }

    slip, err := c.store.Load(ctx, slipID)
    if err != nil {
        c.logger.Errorf("Failed to load slip for aggregate check: %v", err)
        return
    }

    allCompleted := true
    anyFailed := false

    for _, comp := range slip.Components {
        var status StepStatus
        switch stepName {
        case "build":
            status = comp.BuildStatus
        case "unit_test":
            status = comp.UnitTestStatus
        }

        if !status.IsTerminal() {
            allCompleted = false
        }
        if status.IsFailure() {
            anyFailed = true
        }
    }

    if !allCompleted {
        c.logger.Debugf("Aggregate %s not yet complete, some components still running", aggregateStep)
        return
    }

    var aggregateStatus StepStatus
    var message string
    if anyFailed {
        aggregateStatus = StepStatusFailed
        message = "one or more components failed"
    } else {
        aggregateStatus = StepStatusCompleted
        message = "all components completed successfully"
    }

    c.logger.Infof("Updating aggregate %s to %s", aggregateStep, aggregateStatus)
    _ = c.UpdateStepWithStatus(ctx, slip.SlipID.String(), aggregateStep, "", aggregateStatus, message)
}

// UpdateSlipStatus updates the overall slip status
func (c *Client) UpdateSlipStatus(ctx context.Context, slipID string, status SlipStatus) error {
    slip, err := c.store.Load(ctx, slipID)
    if err != nil {
        return fmt.Errorf("failed to load slip: %w", err)
    }
    
    slip.Status = status
    slip.UpdatedAt = time.Now()
    
    return c.store.Update(ctx, slip)
}
```

### 1.8 Prerequisite Checking (`prereqs.go`)

```go
// prereqs.go - Prerequisite checking and hold logic
package slippy

import (
    "context"
    "fmt"
    "strings"
    "time"
)

// CheckPrerequisites checks if all prerequisites are satisfied for a step
func (c *Client) CheckPrerequisites(ctx context.Context, slip *Slip, prerequisites []string, componentName string) (PrereqResult, error) {
    result := PrereqResult{
        Status:           PrereqStatusCompleted,
        CompletedPrereqs: make([]string, 0),
        RunningPrereqs:   make([]string, 0),
        FailedPrereqs:    make([]string, 0),
    }

    for _, prereq := range prerequisites {
        prereq = strings.TrimSpace(prereq)
        if prereq == "" {
            continue
        }

        status := c.getPrereqStatus(slip, prereq, componentName)
        
        switch {
        case status.IsSuccess():
            result.CompletedPrereqs = append(result.CompletedPrereqs, prereq)
        case status.IsFailure():
            result.FailedPrereqs = append(result.FailedPrereqs, prereq)
            result.Status = PrereqStatusFailed
        default:
            // pending, running, held
            result.RunningPrereqs = append(result.RunningPrereqs, prereq)
            if result.Status != PrereqStatusFailed {
                result.Status = PrereqStatusRunning
            }
        }
    }

    return result, nil
}

// getPrereqStatus gets the status of a specific prerequisite
func (c *Client) getPrereqStatus(slip *Slip, prereq, componentName string) StepStatus {
    // Check if it's a component-specific step
    if componentName != "" {
        if prereq == "build" {
            for _, comp := range slip.Components {
                if comp.Name == componentName {
                    return comp.BuildStatus
                }
            }
        }
        if prereq == "unit_test" {
            for _, comp := range slip.Components {
                if comp.Name == componentName {
                    return comp.UnitTestStatus
                }
            }
        }
    }

    // Check aggregate/pipeline steps
    if step, ok := slip.Steps[prereq]; ok {
        return step.Status
    }

    c.logger.Errorf("Unknown prerequisite: %s", prereq)
    return StepStatusPending
}

// WaitForPrerequisites waits for prerequisites with hold/timeout logic
// Returns nil if prerequisites are satisfied, error if failed or timeout
func (c *Client) WaitForPrerequisites(ctx context.Context, correlationID string, prerequisites []string, componentName string, holdTimeout, pollInterval time.Duration) error {
    if len(prerequisites) == 0 {
        return nil
    }

    holdStart := time.Now()
    holdDeadline := holdStart.Add(holdTimeout)

    for {
        select {
        case <-ctx.Done():
            return fmt.Errorf("context cancelled while waiting for prerequisites")
        default:
        }

        slip, err := c.store.Load(ctx, correlationID)
        if err != nil {
            return fmt.Errorf("failed to load slip: %w", err)
        }

        result, err := c.CheckPrerequisites(ctx, slip, prerequisites, componentName)
        if err != nil {
            return fmt.Errorf("failed to check prerequisites: %w", err)
        }

        switch result.Status {
        case PrereqStatusCompleted:
            c.logger.Infof("All prerequisites completed: %v", result.CompletedPrereqs)
            return nil

        case PrereqStatusFailed:
            return fmt.Errorf("prerequisites failed: %v", result.FailedPrereqs)

        case PrereqStatusRunning:
            if time.Now().After(holdDeadline) {
                return fmt.Errorf("hold timeout exceeded after %v, still waiting for: %v",
                    holdTimeout, result.RunningPrereqs)
            }

            c.logger.Infof("Waiting for prerequisites: %v (elapsed: %v, timeout: %v)",
                result.RunningPrereqs, time.Since(holdStart).Round(time.Second), holdTimeout)

            select {
            case <-ctx.Done():
                return fmt.Errorf("context cancelled during wait")
            case <-time.After(pollInterval):
                // Continue to next iteration
            }
        }
    }
}
```

### 1.9 Executor (`executor.go`)

```go
// executor.go - High-level pre/post execution orchestration
package slippy

import (
    "context"
    "fmt"
    "time"
)

// ExecutorConfig holds configuration for pre/post execution
type ExecutorConfig struct {
    CorrelationID string
    StepName      string
    ComponentName string
    Prerequisites []string
    HoldTimeout   time.Duration
    PollInterval  time.Duration
}

// PreExecute runs pre-execution logic: check prerequisites, hold if needed, set running
func (c *Client) PreExecute(ctx context.Context, cfg ExecutorConfig) error {
    c.logger.Infof("Pre-execution for step %s (component: %s)", cfg.StepName, cfg.ComponentName)

    slip, err := c.store.Load(ctx, cfg.CorrelationID)
    if err != nil {
        return fmt.Errorf("failed to load slip: %w", err)
    }

    // If no prerequisites, proceed immediately
    if len(cfg.Prerequisites) == 0 {
        return c.StartStep(ctx, slip.SlipID.String(), cfg.StepName, cfg.ComponentName)
    }

    // Wait for prerequisites (handles hold logic internally)
    err = c.WaitForPrerequisites(ctx, cfg.CorrelationID, cfg.Prerequisites, 
        cfg.ComponentName, cfg.HoldTimeout, cfg.PollInterval)
    
    if err != nil {
        // Determine if it was a timeout or a failure
        if isTimeout(err) {
            _ = c.TimeoutStep(ctx, slip.SlipID.String(), cfg.StepName, cfg.ComponentName, err.Error())
        } else {
            _ = c.AbortStep(ctx, slip.SlipID.String(), cfg.StepName, cfg.ComponentName, err.Error())
        }
        return err
    }

    // Prerequisites satisfied, mark as running
    return c.StartStep(ctx, slip.SlipID.String(), cfg.StepName, cfg.ComponentName)
}

// PostExecute runs post-execution logic: update step status based on workflow result
func (c *Client) PostExecute(ctx context.Context, cfg ExecutorConfig, workflowSucceeded bool, failureMessage string) error {
    c.logger.Infof("Post-execution for step %s (success: %v)", cfg.StepName, workflowSucceeded)

    slip, err := c.store.Load(ctx, cfg.CorrelationID)
    if err != nil {
        return fmt.Errorf("failed to load slip: %w", err)
    }

    if workflowSucceeded {
        if err := c.CompleteStep(ctx, slip.SlipID.String(), cfg.StepName, cfg.ComponentName); err != nil {
            return err
        }
    } else {
        reason := failureMessage
        if reason == "" {
            reason = "workflow failed"
        }
        if err := c.FailStep(ctx, slip.SlipID.String(), cfg.StepName, cfg.ComponentName, reason); err != nil {
            return err
        }
    }

    // Check if pipeline is complete
    c.checkPipelineCompletion(ctx, slip.SlipID.String())

    return nil
}

// checkPipelineCompletion checks if the entire pipeline is complete
func (c *Client) checkPipelineCompletion(ctx context.Context, slipID string) {
    slip, err := c.store.Load(ctx, slipID)
    if err != nil {
        c.logger.Errorf("Failed to load slip for completion check: %v", err)
        return
    }

    // Check if prod_steady_state is completed
    if step, ok := slip.Steps["prod_steady_state"]; ok && step.Status == StepStatusCompleted {
        c.logger.Info("Pipeline complete! Updating slip status to completed")
        _ = c.UpdateSlipStatus(ctx, slipID, SlipStatusCompleted)
        return
    }

    // Check if any terminal step failed (pipeline failed)
    for _, step := range slip.Steps {
        if step.Status == StepStatusFailed || step.Status == StepStatusAborted {
            c.logger.Info("Pipeline failed, updating slip status")
            _ = c.UpdateSlipStatus(ctx, slipID, SlipStatusFailed)
            return
        }
    }
}

func isTimeout(err error) bool {
    return err != nil && (contains(err.Error(), "timeout") || contains(err.Error(), "Timeout"))
}

func contains(s, substr string) bool {
    return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr, 0))
}

func containsAt(s, substr string, start int) bool {
    for i := start; i <= len(s)-len(substr); i++ {
        if s[i:i+len(substr)] == substr {
            return true
        }
    }
    return false
}
```

### 1.10 Updated File Structure

```
goLibMyCarrier/
└── slippy/
    ├── types.go           # Core data structures (Slip, Component, Step, etc.)
    ├── status.go          # Status enums and helper methods
    ├── config.go          # Configuration management
    ├── client.go          # Client creation and basic operations
    ├── push.go            # CreateSlipForPush and push-related logic
    ├── steps.go           # Step status updates and aggregates
    ├── prereqs.go         # Prerequisite checking and hold logic
    ├── executor.go        # Pre/Post execution orchestration
    ├── store.go           # ClickHouse store implementation
    ├── errors.go          # Custom error types
    ├── interfaces.go      # Interfaces for testing/mocking
    ├── z_types_test.go
    ├── z_client_test.go
    ├── z_push_test.go
    ├── z_steps_test.go
    ├── z_prereqs_test.go
    ├── z_executor_test.go
    └── z_integration_test.go
```

### 1.11 Deliverables

| Deliverable | Description | Acceptance Criteria |
|-------------|-------------|---------------------|
| `types.go` | Core data structures | All types compile, JSON serialization works |
| `status.go` | Status enums with helper methods | Status transitions validated |
| `config.go` | Configuration loading | Env vars and defaults work |
| `client.go` | Client creation and basic operations | Client initializes correctly |
| `push.go` | CreateSlipForPush, retry handling | Creates/updates slips correctly |
| `steps.go` | Step updates, aggregates | Updates propagate correctly |
| `prereqs.go` | Prerequisite checking, hold logic | Hold/proceed/fail logic works |
| `executor.go` | Pre/Post execution orchestration | End-to-end flow works |
| `interfaces.go` | Interfaces for DI/mocking | Enables mock implementations |
| Unit tests | 80%+ coverage | `go test` passes |

### 1.12 Dependencies

```go
// go.mod additions for goLibMyCarrier/slippy
require (
    github.com/ClickHouse/clickhouse-go/v2 v2.40.3
    github.com/google/uuid v1.6.0
    github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse v1.3.38
    github.com/MyCarrier-DevOps/goLibMyCarrier/otel v1.3.38
)
```

---

## Phase 2: Standalone Application

**Goal**: Build the `ci/slippy` standalone application that wraps the library for use as Kubernetes init container and exit handler.

### 2.1 Repository Structure

**Location**: `github.com/MyCarrier-DevOps/ci/slippy/`

**File Structure**:
```
ci/
└── slippy/
    ├── makefile              # Build targets
    ├── README.md             # Documentation
    └── slippy/
        ├── main.go           # Entry point
        ├── config.go         # CLI/env config loading
        ├── pre.go            # Pre-execution handler
        ├── post.go           # Post-execution handler
        ├── interfaces.go     # Local interfaces
        ├── Dockerfile        # Container image
        ├── go.mod
        ├── go.sum
        ├── z_main_test.go
        ├── z_pre_test.go
        ├── z_post_test.go
        └── z_integration_test.go
```

### 2.2 Main Entry Point (`main.go`)

```go
package main

import (
    "context"
    "os"
    "os/signal"
    "syscall"

    "github.com/MyCarrier-DevOps/goLibMyCarrier/otel"
    "github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

func main() {
    // Initialize context with cancellation
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Handle shutdown signals
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    go func() {
        <-sigChan
        cancel()
    }()

    // Initialize logger
    logger := otel.NewAppLogger()
    logger.Info("Starting slippy...")

    // Load configuration from environment
    config, err := LoadConfig()
    if err != nil {
        logger.Errorf("Failed to load configuration: %v", err)
        os.Exit(1)
    }
    logger.Infof("Mode: %s, Workflow: %s, Step: %s", 
        config.Mode, config.WorkflowName, config.StepName)

    // Create slippy client
    clientConfig := slippy.Config{
        ClickHouseDSN: config.ClickHouseDSN,
        Logger:        logger,
    }
    client, err := slippy.NewClient(clientConfig)
    if err != nil {
        logger.Errorf("Failed to create slippy client: %v", err)
        os.Exit(1)
    }

    // Create runner with dependencies
    runner := NewRunner(logger, client, config)

    // Execute based on mode
    var exitCode int
    switch config.Mode {
    case ModePre:
        exitCode = runner.RunPreExecution(ctx)
    case ModePost:
        exitCode = runner.RunPostExecution(ctx)
    default:
        logger.Errorf("Unknown mode: %s (expected 'pre' or 'post')", config.Mode)
        exitCode = 1
    }

    os.Exit(exitCode)
}
```

### 2.3 Configuration (`config.go`)

```go
package main

import (
    "fmt"
    "os"
    "strings"
    "time"
)

// Mode represents the execution mode
type Mode string

const (
    ModePre  Mode = "pre"
    ModePost Mode = "post"
)

// Config holds all configuration for the slippy application
type Config struct {
    // Execution mode
    Mode Mode

    // Workflow identification
    WorkflowName     string
    WorkflowTemplate string
    CorrelationID    string

    // Step identification
    StepName      string
    ComponentName string

    // Prerequisites (comma-separated)
    Prerequisites []string

    // Hold configuration
    HoldTimeout  time.Duration
    PollInterval time.Duration

    // ClickHouse connection
    ClickHouseDSN string

    // Debug mode
    Debug bool
}

// LoadConfig loads configuration from environment variables
func LoadConfig() (*Config, error) {
    config := &Config{
        Mode:             Mode(getEnvOrDefault("SLIPPY_MODE", "")),
        WorkflowName:     getEnvOrDefault("SLIPPY_WORKFLOW_NAME", ""),
        WorkflowTemplate: getEnvOrDefault("SLIPPY_WORKFLOW_TEMPLATE", ""),
        CorrelationID:    getEnvOrDefault("SLIPPY_CORRELATION_ID", ""),
        StepName:         getEnvOrDefault("SLIPPY_STEP_NAME", ""),
        ComponentName:    getEnvOrDefault("SLIPPY_COMPONENT_NAME", ""),
        ClickHouseDSN:    getEnvOrDefault("SLIPPY_CLICKHOUSE_DSN", ""),
        Debug:            getEnvOrDefault("SLIPPY_DEBUG", "false") == "true",
    }

    // Parse prerequisites
    prereqStr := getEnvOrDefault("SLIPPY_PREREQUISITES", "")
    if prereqStr != "" {
        config.Prerequisites = strings.Split(prereqStr, ",")
        for i, p := range config.Prerequisites {
            config.Prerequisites[i] = strings.TrimSpace(p)
        }
    }

    // Parse durations with defaults
    var err error
    config.HoldTimeout, err = parseDurationOrDefault("SLIPPY_HOLD_TIMEOUT", 60*time.Minute)
    if err != nil {
        return nil, fmt.Errorf("invalid SLIPPY_HOLD_TIMEOUT: %w", err)
    }

    config.PollInterval, err = parseDurationOrDefault("SLIPPY_POLL_INTERVAL", 60*time.Second)
    if err != nil {
        return nil, fmt.Errorf("invalid SLIPPY_POLL_INTERVAL: %w", err)
    }

    // Validate required fields
    if err := config.Validate(); err != nil {
        return nil, err
    }

    return config, nil
}

// Validate checks that all required configuration is present
func (c *Config) Validate() error {
    if c.Mode == "" {
        return fmt.Errorf("SLIPPY_MODE is required (must be 'pre' or 'post')")
    }
    if c.Mode != ModePre && c.Mode != ModePost {
        return fmt.Errorf("SLIPPY_MODE must be 'pre' or 'post', got '%s'", c.Mode)
    }
    if c.CorrelationID == "" {
        return fmt.Errorf("SLIPPY_CORRELATION_ID is required")
    }
    if c.StepName == "" {
        return fmt.Errorf("SLIPPY_STEP_NAME is required")
    }
    if c.ClickHouseDSN == "" {
        return fmt.Errorf("SLIPPY_CLICKHOUSE_DSN is required")
    }
    return nil
}

func getEnvOrDefault(key, defaultVal string) string {
    if val := os.Getenv(key); val != "" {
        return val
    }
    return defaultVal
}

func parseDurationOrDefault(key string, defaultVal time.Duration) (time.Duration, error) {
    val := os.Getenv(key)
    if val == "" {
        return defaultVal, nil
    }
    return time.ParseDuration(val)
}
```

### 2.4 Pre-Execution Handler (`pre.go`)

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

// RunPreExecution handles pre-execution mode
func (r *Runner) RunPreExecution(ctx context.Context) int {
    r.logger.Info("Starting pre-execution check...")

    // Load the routing slip
    slip, err := r.client.Load(ctx, r.config.CorrelationID)
    if err != nil {
        r.logger.Errorf("Failed to load routing slip: %v", err)
        return 1
    }
    r.logger.Infof("Loaded slip %s for repo %s", slip.SlipID, slip.Repository)

    // If no prerequisites, proceed immediately
    if len(r.config.Prerequisites) == 0 {
        r.logger.Info("No prerequisites defined, proceeding...")
        return r.setRunningAndProceed(ctx, slip)
    }

    // Start hold timer
    holdStart := time.Now()
    holdDeadline := holdStart.Add(r.config.HoldTimeout)

    for {
        // Check for context cancellation
        select {
        case <-ctx.Done():
            r.logger.Error("Context cancelled, aborting...")
            r.setStatus(ctx, slip, slippy.StepStatusAborted, "context cancelled")
            return 1
        default:
        }

        // Check prerequisites
        result, err := r.client.CheckPrerequisites(ctx, slip, r.config.Prerequisites, r.config.ComponentName)
        if err != nil {
            r.logger.Errorf("Failed to check prerequisites: %v", err)
            return 1
        }

        switch result.Status {
        case slippy.PrereqStatusCompleted:
            // All prerequisites completed successfully
            r.logger.Infof("All prerequisites completed: %v", result.CompletedPrereqs)
            return r.setRunningAndProceed(ctx, slip)

        case slippy.PrereqStatusFailed:
            // One or more prerequisites failed
            r.logger.Errorf("Prerequisites failed: %v", result.FailedPrereqs)
            r.setStatus(ctx, slip, slippy.StepStatusAborted, 
                fmt.Sprintf("upstream prerequisites failed: %v", result.FailedPrereqs))
            return 1

        case slippy.PrereqStatusRunning:
            // Prerequisites still running, check timeout
            if time.Now().After(holdDeadline) {
                r.logger.Errorf("Hold timeout exceeded after %v, still waiting for: %v", 
                    r.config.HoldTimeout, result.RunningPrereqs)
                r.setStatus(ctx, slip, slippy.StepStatusTimeout,
                    fmt.Sprintf("hold timeout waiting for: %v", result.RunningPrereqs))
                return 1
            }

            // Update status to held (if not already)
            if err := r.setHeld(ctx, slip, result.RunningPrereqs); err != nil {
                r.logger.Errorf("Failed to set held status: %v", err)
                return 1
            }

            // Wait before next check
            r.logger.Infof("Waiting for prerequisites: %v (elapsed: %v, timeout: %v)",
                result.RunningPrereqs, time.Since(holdStart).Round(time.Second), r.config.HoldTimeout)
            
            select {
            case <-ctx.Done():
                r.logger.Error("Context cancelled during wait")
                return 1
            case <-time.After(r.config.PollInterval):
                // Refresh slip for next iteration
                slip, err = r.client.Load(ctx, r.config.CorrelationID)
                if err != nil {
                    r.logger.Errorf("Failed to reload slip: %v", err)
                    return 1
                }
            }
        }
    }
}

func (r *Runner) setRunningAndProceed(ctx context.Context, slip *slippy.Slip) int {
    if err := r.setStatus(ctx, slip, slippy.StepStatusRunning, ""); err != nil {
        r.logger.Errorf("Failed to set running status: %v", err)
        return 1
    }
    r.logger.Info("Prerequisites satisfied, proceeding with workflow execution")
    return 0
}

func (r *Runner) setHeld(ctx context.Context, slip *slippy.Slip, waitingFor []string) error {
    return r.client.UpdateStepStatus(ctx, slip.SlipID.String(), r.config.StepName, 
        r.config.ComponentName, slippy.StepStatusHeld,
        slippy.StateHistoryEntry{
            Step:      r.config.StepName,
            Component: r.config.ComponentName,
            Status:    slippy.StepStatusHeld,
            Timestamp: time.Now(),
            Actor:     "slippy-pre",
            Message:   fmt.Sprintf("waiting for: %v", waitingFor),
        })
}

func (r *Runner) setStatus(ctx context.Context, slip *slippy.Slip, status slippy.StepStatus, message string) error {
    return r.client.UpdateStepStatus(ctx, slip.SlipID.String(), r.config.StepName,
        r.config.ComponentName, status,
        slippy.StateHistoryEntry{
            Step:      r.config.StepName,
            Component: r.config.ComponentName,
            Status:    status,
            Timestamp: time.Now(),
            Actor:     "slippy-pre",
            Message:   message,
        })
}
```

### 2.5 Post-Execution Handler (`post.go`)

```go
package main

import (
    "context"
    "os"
    "time"

    "github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

// RunPostExecution handles post-execution mode
func (r *Runner) RunPostExecution(ctx context.Context) int {
    r.logger.Info("Starting post-execution update...")

    // Load the routing slip
    slip, err := r.client.Load(ctx, r.config.CorrelationID)
    if err != nil {
        r.logger.Errorf("Failed to load routing slip: %v", err)
        return 1
    }
    r.logger.Infof("Loaded slip %s for repo %s", slip.SlipID, slip.Repository)

    // Determine workflow result
    // Argo sets ARGO_WORKFLOW_STATUS environment variable in exit handlers
    workflowStatus := os.Getenv("ARGO_WORKFLOW_STATUS")
    r.logger.Infof("Workflow status from Argo: %s", workflowStatus)

    var finalStatus slippy.StepStatus
    var message string

    switch workflowStatus {
    case "Succeeded":
        finalStatus = slippy.StepStatusCompleted
        message = "workflow completed successfully"
    case "Failed":
        finalStatus = slippy.StepStatusFailed
        message = os.Getenv("ARGO_WORKFLOW_FAILURE_MESSAGE")
        if message == "" {
            message = "workflow failed"
        }
    case "Error":
        finalStatus = slippy.StepStatusError
        message = "workflow encountered an error"
    default:
        r.logger.Errorf("Unknown workflow status: %s", workflowStatus)
        finalStatus = slippy.StepStatusError
        message = "unknown workflow status: " + workflowStatus
    }

    // Update the step status
    entry := slippy.StateHistoryEntry{
        Step:      r.config.StepName,
        Component: r.config.ComponentName,
        Status:    finalStatus,
        Timestamp: time.Now(),
        Actor:     "slippy-post",
        Message:   message,
    }

    if err := r.client.UpdateStepStatus(ctx, slip.SlipID.String(), 
        r.config.StepName, r.config.ComponentName, finalStatus, entry); err != nil {
        r.logger.Errorf("Failed to update step status: %v", err)
        return 1
    }

    r.logger.Infof("Updated step %s to status %s", r.config.StepName, finalStatus)

    // Check and update aggregate steps if applicable
    if err := r.checkAggregates(ctx, slip); err != nil {
        r.logger.Errorf("Failed to check aggregates: %v", err)
        // Don't fail the exit handler for aggregate check failures
    }

    // Check if slip is complete
    if err := r.checkSlipCompletion(ctx, slip); err != nil {
        r.logger.Errorf("Failed to check slip completion: %v", err)
    }

    return 0
}

// checkAggregates updates aggregate steps like builds_completed, unit_tests_completed
func (r *Runner) checkAggregates(ctx context.Context, slip *slippy.Slip) error {
    // Map of step -> aggregate step
    aggregateMap := map[string]string{
        "build":     "builds_completed",
        "unit_test": "unit_tests_completed",
    }

    aggregateStep, ok := aggregateMap[r.config.StepName]
    if !ok {
        return nil // Not an aggregatable step
    }

    // Check if all components have completed this step
    allCompleted := true
    anyFailed := false

    for _, comp := range slip.Components {
        var compStatus slippy.StepStatus
        switch r.config.StepName {
        case "build":
            compStatus = comp.BuildStatus
        case "unit_test":
            compStatus = comp.UnitTestStatus
        }

        if !compStatus.IsTerminal() {
            allCompleted = false
        }
        if compStatus.IsFailure() {
            anyFailed = true
        }
    }

    if !allCompleted {
        r.logger.Infof("Aggregate %s not yet complete, some components still running", aggregateStep)
        return nil
    }

    // Update aggregate step
    var aggregateStatus slippy.StepStatus
    if anyFailed {
        aggregateStatus = slippy.StepStatusFailed
    } else {
        aggregateStatus = slippy.StepStatusCompleted
    }

    entry := slippy.StateHistoryEntry{
        Step:      aggregateStep,
        Status:    aggregateStatus,
        Timestamp: time.Now(),
        Actor:     "slippy-post",
        Message:   "all components completed",
    }

    r.logger.Infof("Updating aggregate step %s to %s", aggregateStep, aggregateStatus)
    return r.client.UpdateStepStatus(ctx, slip.SlipID.String(), aggregateStep, "", aggregateStatus, entry)
}

// checkSlipCompletion checks if the entire slip is complete
func (r *Runner) checkSlipCompletion(ctx context.Context, slip *slippy.Slip) error {
    // Only check completion on terminal steps
    if r.config.StepName != "alert_gate" && r.config.StepName != "prod_steady_state" {
        return nil
    }

    // Reload slip to get latest state
    slip, err := r.client.Load(ctx, r.config.CorrelationID)
    if err != nil {
        return err
    }

    // Check if prod_steady_state is completed
    if step, ok := slip.Steps["prod_steady_state"]; ok && step.Status == slippy.StepStatusCompleted {
        r.logger.Info("Pipeline complete! Updating slip status to completed")
        return r.client.UpdateSlipStatus(ctx, slip.SlipID.String(), slippy.SlipStatusCompleted)
    }

    return nil
}
```

### 2.6 Runner Structure (`runner.go`)

```go
package main

import "github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"

// Runner encapsulates application dependencies
type Runner struct {
    logger Logger
    client *slippy.Client
    config *Config
}

// NewRunner creates a new Runner instance
func NewRunner(logger Logger, client *slippy.Client, config *Config) *Runner {
    return &Runner{
        logger: logger,
        client: client,
        config: config,
    }
}

// Logger interface for dependency injection
type Logger interface {
    Info(args ...interface{})
    Infof(format string, args ...interface{})
    Error(args ...interface{})
    Errorf(format string, args ...interface{})
}
```

### 2.7 Dockerfile

```dockerfile
# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o slippy .

# Runtime stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/slippy .

# Run as non-root user
RUN adduser -D -g '' appuser
USER appuser

ENTRYPOINT ["./slippy"]
```

### 2.8 Makefile

```makefile
.PHONY: build test lint docker-build docker-push

APP_NAME := slippy
IMAGE_NAME := ci/$(APP_NAME)
VERSION := $(shell git describe --tags --always --dirty)

build:
	cd $(APP_NAME) && go build -o ../bin/$(APP_NAME) .

test:
	cd $(APP_NAME) && go test -v -race -coverprofile=coverage.out ./...

lint:
	cd $(APP_NAME) && golangci-lint run

docker-build:
	docker build -t $(IMAGE_NAME):$(VERSION) -t $(IMAGE_NAME):latest $(APP_NAME)/

docker-push:
	docker push $(IMAGE_NAME):$(VERSION)
	docker push $(IMAGE_NAME):latest
```

### 2.9 Deliverables

| Deliverable | Description | Acceptance Criteria |
|-------------|-------------|---------------------|
| `main.go` | Entry point with mode selection | Compiles, handles signals |
| `config.go` | Environment config loading | All env vars parsed correctly |
| `pre.go` | Pre-execution handler | Hold/proceed/abort logic works |
| `post.go` | Post-execution handler | Status updates correctly |
| `Dockerfile` | Container image | Builds, runs as non-root |
| Unit tests | 80%+ coverage | All tests pass |

---

## Phase 3: ClickHouse Schema & Migrations

**Goal**: Design and implement the ClickHouse schema for routing slips with proper indexing, partitioning, and migration strategy.

### 3.1 Schema Design Principles

| Principle | Implementation |
|-----------|----------------|
| **Query Performance** | Denormalized step statuses for fast filtering |
| **Audit Trail** | JSON state_history for complete transition log |
| **Flexibility** | JSON components array for variable component counts |
| **Data Retention** | Monthly partitioning for efficient data management |
| **Upsert Support** | ReplacingMergeTree for atomic updates |

### 3.2 Primary Table: `ci.routing_slips`

```sql
-- Migration: 001_create_routing_slips.sql
CREATE TABLE IF NOT EXISTS ci.routing_slips
(
    -- Primary identifiers
    slip_id UUID DEFAULT generateUUIDv4(),
    correlation_id String,
    
    -- Repository metadata
    repository String,
    branch String,
    commit_sha String,
    
    -- Timestamps
    created_at DateTime64(3) DEFAULT now64(3),
    updated_at DateTime64(3) DEFAULT now64(3),
    
    -- Overall slip status
    status Enum8(
        'pending' = 1,
        'in_progress' = 2,
        'completed' = 3,
        'failed' = 4,
        'compensating' = 5,
        'compensated' = 6
    ) DEFAULT 'pending',
    
    -- Components as JSON array
    -- Schema: [{"name": "...", "dockerfile_path": "...", "build_status": "...", 
    --           "unit_test_status": "...", "image_tag": "..."}]
    components String DEFAULT '[]',
    
    -- Denormalized step statuses for query performance
    -- Each step can be: pending(1), held(2), running(3), completed(4), 
    --                   failed(5), error(6), aborted(7), timeout(8), skipped(9)
    push_parsed_status Enum8(
        'pending'=1, 'held'=2, 'running'=3, 'completed'=4, 
        'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
    ) DEFAULT 'pending',
    
    builds_completed_status Enum8(
        'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
        'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
    ) DEFAULT 'pending',
    
    unit_tests_completed_status Enum8(
        'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
        'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
    ) DEFAULT 'pending',
    
    secret_scan_completed_status Enum8(
        'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
        'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
    ) DEFAULT 'pending',
    
    dev_deploy_status Enum8(
        'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
        'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
    ) DEFAULT 'pending',
    
    dev_tests_status Enum8(
        'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
        'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
    ) DEFAULT 'pending',
    
    preprod_deploy_status Enum8(
        'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
        'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
    ) DEFAULT 'pending',
    
    preprod_tests_status Enum8(
        'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
        'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
    ) DEFAULT 'pending',
    
    prod_release_created_status Enum8(
        'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
        'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
    ) DEFAULT 'pending',
    
    prod_deploy_status Enum8(
        'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
        'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
    ) DEFAULT 'pending',
    
    prod_tests_status Enum8(
        'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
        'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
    ) DEFAULT 'pending',
    
    alert_gate_status Enum8(
        'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
        'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
    ) DEFAULT 'pending',
    
    prod_steady_state_status Enum8(
        'pending'=1, 'held'=2, 'running'=3, 'completed'=4,
        'failed'=5, 'error'=6, 'aborted'=7, 'timeout'=8, 'skipped'=9
    ) DEFAULT 'pending',
    
    -- Step timestamps (JSON object for flexibility)
    -- Schema: {"step_name": {"started_at": "...", "completed_at": "..."}}
    step_timestamps String DEFAULT '{}',
    
    -- Full state history as JSON array
    -- Schema: [{"step": "...", "component": "...", "status": "...", 
    --           "timestamp": "...", "actor": "...", "message": "..."}]
    state_history String DEFAULT '[]',
    
    -- Bloom filter indexes for fast lookups
    INDEX idx_correlation correlation_id TYPE bloom_filter GRANULARITY 1,
    INDEX idx_repository repository TYPE bloom_filter GRANULARITY 1,
    INDEX idx_commit commit_sha TYPE bloom_filter GRANULARITY 1,
    INDEX idx_branch branch TYPE bloom_filter GRANULARITY 1
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (slip_id)
PARTITION BY toYYYYMM(created_at)
SETTINGS index_granularity = 8192;
```

### 3.3 State History View (Optional Materialized View)

```sql
-- Migration: 002_create_state_history_view.sql
-- Materialized view for querying state history efficiently
CREATE MATERIALIZED VIEW IF NOT EXISTS ci.routing_slip_history_mv
ENGINE = MergeTree()
ORDER BY (slip_id, timestamp)
PARTITION BY toYYYYMM(timestamp)
AS SELECT
    slip_id,
    correlation_id,
    repository,
    JSONExtractString(entry, 'step') AS step,
    JSONExtractString(entry, 'component') AS component,
    JSONExtractString(entry, 'status') AS status,
    parseDateTimeBestEffort(JSONExtractString(entry, 'timestamp')) AS timestamp,
    JSONExtractString(entry, 'actor') AS actor,
    JSONExtractString(entry, 'message') AS message
FROM ci.routing_slips
ARRAY JOIN JSONExtractArrayRaw(state_history) AS entry;
```

### 3.4 Useful Indexes

```sql
-- Migration: 003_create_additional_indexes.sql

-- Secondary index for finding slips by status
ALTER TABLE ci.routing_slips 
ADD INDEX idx_status status TYPE set(10) GRANULARITY 1;

-- Secondary index for finding held steps
ALTER TABLE ci.routing_slips
ADD INDEX idx_dev_deploy_held dev_deploy_status TYPE set(10) GRANULARITY 1;

ALTER TABLE ci.routing_slips
ADD INDEX idx_preprod_deploy_held preprod_deploy_status TYPE set(10) GRANULARITY 1;

ALTER TABLE ci.routing_slips
ADD INDEX idx_prod_deploy_held prod_deploy_status TYPE set(10) GRANULARITY 1;
```

### 3.5 Migration Strategy

**Migration Tool**: Use a simple Go-based migration runner

```go
// migrations/runner.go
package migrations

import (
    "context"
    "embed"
    "fmt"
    "sort"
    "strings"
    
    "github.com/ClickHouse/clickhouse-go/v2"
)

//go:embed sql/*.sql
var migrationFS embed.FS

type Migration struct {
    Version int
    Name    string
    SQL     string
}

func LoadMigrations() ([]Migration, error) {
    entries, err := migrationFS.ReadDir("sql")
    if err != nil {
        return nil, err
    }
    
    var migrations []Migration
    for _, entry := range entries {
        if !strings.HasSuffix(entry.Name(), ".sql") {
            continue
        }
        
        content, err := migrationFS.ReadFile("sql/" + entry.Name())
        if err != nil {
            return nil, err
        }
        
        // Parse version from filename: 001_name.sql
        var version int
        fmt.Sscanf(entry.Name(), "%d_", &version)
        
        migrations = append(migrations, Migration{
            Version: version,
            Name:    entry.Name(),
            SQL:     string(content),
        })
    }
    
    sort.Slice(migrations, func(i, j int) bool {
        return migrations[i].Version < migrations[j].Version
    })
    
    return migrations, nil
}

func RunMigrations(ctx context.Context, conn clickhouse.Conn) error {
    // Create migrations tracking table
    err := conn.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS ci.schema_migrations (
            version Int32,
            name String,
            applied_at DateTime DEFAULT now()
        ) ENGINE = MergeTree() ORDER BY version
    `)
    if err != nil {
        return fmt.Errorf("failed to create migrations table: %w", err)
    }
    
    // Get applied migrations
    rows, err := conn.Query(ctx, "SELECT version FROM ci.schema_migrations")
    if err != nil {
        return fmt.Errorf("failed to query migrations: %w", err)
    }
    defer rows.Close()
    
    applied := make(map[int]bool)
    for rows.Next() {
        var version int
        if err := rows.Scan(&version); err != nil {
            return err
        }
        applied[version] = true
    }
    
    // Load and run pending migrations
    migrations, err := LoadMigrations()
    if err != nil {
        return err
    }
    
    for _, m := range migrations {
        if applied[m.Version] {
            continue
        }
        
        fmt.Printf("Running migration %s...\n", m.Name)
        if err := conn.Exec(ctx, m.SQL); err != nil {
            return fmt.Errorf("migration %s failed: %w", m.Name, err)
        }
        
        if err := conn.Exec(ctx, 
            "INSERT INTO ci.schema_migrations (version, name) VALUES (?, ?)",
            m.Version, m.Name); err != nil {
            return fmt.Errorf("failed to record migration %s: %w", m.Name, err)
        }
    }
    
    return nil
}
```

### 3.6 Common Queries

**Load slip by correlation_id:**
```sql
SELECT *
FROM ci.routing_slips FINAL
WHERE correlation_id = {correlation_id:String}
LIMIT 1;
```

**Load slip by commit (for retry detection):**
```sql
SELECT *
FROM ci.routing_slips FINAL
WHERE repository = {repository:String}
  AND commit_sha = {commit_sha:String}
ORDER BY created_at DESC
LIMIT 1;
```

**Find slips stuck in held state:**
```sql
SELECT 
    slip_id,
    correlation_id,
    repository,
    branch,
    CASE 
        WHEN dev_deploy_status = 'held' THEN 'dev_deploy'
        WHEN preprod_deploy_status = 'held' THEN 'preprod_deploy'
        WHEN prod_deploy_status = 'held' THEN 'prod_deploy'
    END AS held_step,
    updated_at
FROM ci.routing_slips FINAL
WHERE (dev_deploy_status = 'held' 
    OR preprod_deploy_status = 'held' 
    OR prod_deploy_status = 'held')
  AND updated_at < now() - INTERVAL 1 HOUR
ORDER BY updated_at;
```

**Pipeline completion rate:**
```sql
SELECT 
    toStartOfDay(created_at) AS day,
    count() AS total_slips,
    countIf(status = 'completed') AS completed,
    countIf(status = 'failed') AS failed,
    round(completed / total_slips * 100, 2) AS completion_rate
FROM ci.routing_slips FINAL
WHERE created_at >= now() - INTERVAL 30 DAY
GROUP BY day
ORDER BY day;
```

**Average time to production:**
```sql
SELECT
    repository,
    avg(dateDiff('minute', created_at, 
        parseDateTimeBestEffort(JSONExtractString(step_timestamps, 'prod_steady_state.completed_at'))
    )) AS avg_minutes_to_prod
FROM ci.routing_slips FINAL
WHERE status = 'completed'
  AND created_at >= now() - INTERVAL 30 DAY
GROUP BY repository
ORDER BY avg_minutes_to_prod;
```

### 3.7 Data Retention Policy

```sql
-- Migration: 004_create_ttl.sql
-- Retain data for 1 year, then auto-delete
ALTER TABLE ci.routing_slips
MODIFY TTL created_at + INTERVAL 365 DAY;
```

### 3.8 Deliverables

| Deliverable | Description | Acceptance Criteria |
|-------------|-------------|---------------------|
| `001_create_routing_slips.sql` | Main table DDL | Table creates successfully |
| `002_create_state_history_view.sql` | MV for history | View populates correctly |
| `003_create_additional_indexes.sql` | Performance indexes | Queries use indexes |
| `004_create_ttl.sql` | Data retention | TTL configured |
| Migration runner | Go migration tool | Runs idempotently |

---

## Phase 4: push-hook-parser Integration

**Goal**: Modify push-hook-parser to create routing slips using the slippy library's high-level API.

### 4.1 Design Principle: Library Owns Business Logic

The slippy library should encapsulate **all routing slip business logic**. Push-hook-parser (and other consumers) should only:

1. Call library methods with simple inputs
2. Handle errors appropriately
3. Not duplicate slip logic

```
┌─────────────────────────────────────────────────────────────────────────┐
│                          goLibMyCarrier/slippy                          │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                    Slip Creation API                            │   │
│  │                                                                 │   │
│  │  func (c *Client) CreateSlipForPush(ctx, opts) (*Slip, error)   │   │
│  │    • Checks for existing slip (retry detection)                 │   │
│  │    • Creates new slip with proper initialization                │   │
│  │    • Sets push_parsed = running                                 │   │
│  │    • Records state history                                      │   │
│  │    • Returns slip for correlation_id propagation                │   │
│  │                                                                 │   │
│  │  func (c *Client) CompleteStep(ctx, slipID, step, ...) error    │   │
│  │    • Updates step to completed                                  │   │
│  │    • Records state history                                      │   │
│  │    • Checks/updates aggregates                                  │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                       push-hook-parser (consumer)                       │
│                                                                         │
│  slip, err := slippyClient.CreateSlipForPush(ctx, slippy.PushOptions{  │
│      CorrelationID: event.CorrelationID,                               │
│      Repository:    event.Repository,                                   │
│      Branch:        event.Branch,                                       │
│      CommitSHA:     event.CommitSHA,                                    │
│      Components:    components,                                         │
│  })                                                                     │
│                                                                         │
│  // ... emit Kafka events ...                                           │
│                                                                         │
│  err = slippyClient.CompleteStep(ctx, slip.SlipID, "push_parsed", "")  │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 4.2 Library Additions (goLibMyCarrier/slippy)

**File**: `goLibMyCarrier/slippy/push.go`

```go
package slippy

import (
    "context"
    "fmt"
    "time"

    "github.com/google/uuid"
)

// PushOptions contains the information needed to create a slip from a push event
type PushOptions struct {
    CorrelationID string
    Repository    string
    Branch        string
    CommitSHA     string
    Components    []ComponentDefinition
}

// CreateSlipForPush creates a new routing slip for a git push event.
// If a slip already exists for this commit (retry scenario), it resets
// the push_parsed step and returns the existing slip.
func (c *Client) CreateSlipForPush(ctx context.Context, opts PushOptions) (*Slip, error) {
    if err := opts.Validate(); err != nil {
        return nil, fmt.Errorf("invalid push options: %w", err)
    }

    c.logger.Infof("Creating routing slip for %s@%s", opts.Repository, shortSHA(opts.CommitSHA))

    // Check for existing slip (retry detection)
    existingSlip, err := c.store.LoadByCommit(ctx, opts.Repository, opts.CommitSHA)
    if err == nil && existingSlip != nil {
        return c.handleRetry(ctx, existingSlip)
    }

    // Create new slip
    slip := c.initializeSlip(opts)

    if err := c.store.Create(ctx, slip); err != nil {
        return nil, fmt.Errorf("failed to create slip: %w", err)
    }

    c.logger.Infof("Created routing slip %s with %d components", slip.SlipID, len(opts.Components))
    return slip, nil
}

// handleRetry handles the case where a slip already exists for a commit
func (c *Client) handleRetry(ctx context.Context, slip *Slip) (*Slip, error) {
    c.logger.Infof("Found existing slip %s for commit %s, handling retry...",
        slip.SlipID, shortSHA(slip.CommitSHA))

    entry := StateHistoryEntry{
        Step:      "push_parsed",
        Status:    StepStatusRunning,
        Timestamp: time.Now(),
        Actor:     "slippy-library",
        Message:   "retry detected, resetting push_parsed",
    }

    if err := c.store.UpdateStep(ctx, slip.SlipID.String(), "push_parsed", "", StepStatusRunning); err != nil {
        return nil, fmt.Errorf("failed to reset push_parsed: %w", err)
    }

    if err := c.store.AppendHistory(ctx, slip.SlipID.String(), entry); err != nil {
        c.logger.Errorf("Failed to append history for retry: %v", err)
        // Non-fatal - continue
    }

    // Reload to get updated slip
    return c.store.Load(ctx, slip.CorrelationID)
}

// initializeSlip creates a new slip with all fields properly initialized
func (c *Client) initializeSlip(opts PushOptions) *Slip {
    now := time.Now()
    slipID := uuid.New()

    // Initialize components
    components := make([]Component, len(opts.Components))
    for i, def := range opts.Components {
        components[i] = Component{
            Name:           def.Name,
            DockerfilePath: def.DockerfilePath,
            BuildStatus:    StepStatusPending,
            UnitTestStatus: StepStatusPending,
        }
    }

    // Initialize all steps as pending
    steps := map[string]Step{
        "push_parsed":           {Status: StepStatusRunning, StartedAt: &now},
        "builds_completed":      {Status: StepStatusPending},
        "unit_tests_completed":  {Status: StepStatusPending},
        "secret_scan_completed": {Status: StepStatusPending},
        "dev_deploy":            {Status: StepStatusPending},
        "dev_tests":             {Status: StepStatusPending},
        "preprod_deploy":        {Status: StepStatusPending},
        "preprod_tests":         {Status: StepStatusPending},
        "prod_release_created":  {Status: StepStatusPending},
        "prod_deploy":           {Status: StepStatusPending},
        "prod_tests":            {Status: StepStatusPending},
        "alert_gate":            {Status: StepStatusPending},
        "prod_steady_state":     {Status: StepStatusPending},
    }

    // Initial history entry
    history := []StateHistoryEntry{
        {
            Step:      "push_parsed",
            Status:    StepStatusRunning,
            Timestamp: now,
            Actor:     "slippy-library",
            Message:   "processing push event",
        },
    }

    return &Slip{
        SlipID:        slipID,
        CorrelationID: opts.CorrelationID,
        Repository:    opts.Repository,
        Branch:        opts.Branch,
        CommitSHA:     opts.CommitSHA,
        CreatedAt:     now,
        UpdatedAt:     now,
        Status:        SlipStatusInProgress,
        Components:    components,
        Steps:         steps,
        StateHistory:  history,
    }
}

// Validate checks that all required fields are present
func (o PushOptions) Validate() error {
    if o.CorrelationID == "" {
        return fmt.Errorf("correlation_id is required")
    }
    if o.Repository == "" {
        return fmt.Errorf("repository is required")
    }
    if o.CommitSHA == "" {
        return fmt.Errorf("commit_sha is required")
    }
    return nil
}

func shortSHA(sha string) string {
    if len(sha) > 7 {
        return sha[:7]
    }
    return sha
}
```

**File**: `goLibMyCarrier/slippy/steps.go`

```go
package slippy

import (
    "context"
    "fmt"
    "time"
)

// CompleteStep marks a step as completed and records history
func (c *Client) CompleteStep(ctx context.Context, slipID, stepName, componentName string) error {
    return c.UpdateStepWithStatus(ctx, slipID, stepName, componentName, StepStatusCompleted, "step completed successfully")
}

// FailStep marks a step as failed and records history
func (c *Client) FailStep(ctx context.Context, slipID, stepName, componentName, reason string) error {
    return c.UpdateStepWithStatus(ctx, slipID, stepName, componentName, StepStatusFailed, reason)
}

// UpdateStepWithStatus updates a step's status with a message
func (c *Client) UpdateStepWithStatus(ctx context.Context, slipID, stepName, componentName string, status StepStatus, message string) error {
    entry := StateHistoryEntry{
        Step:      stepName,
        Component: componentName,
        Status:    status,
        Timestamp: time.Now(),
        Actor:     "slippy-library",
        Message:   message,
    }

    if err := c.store.UpdateStep(ctx, slipID, stepName, componentName, status); err != nil {
        return fmt.Errorf("failed to update step %s: %w", stepName, err)
    }

    if err := c.store.AppendHistory(ctx, slipID, entry); err != nil {
        c.logger.Errorf("Failed to append history: %v", err)
        // Non-fatal
    }

    // Check if this affects aggregates
    if stepName == "build" || stepName == "unit_test" {
        slip, err := c.store.Load(ctx, slipID)
        if err != nil {
            c.logger.Errorf("Failed to load slip for aggregate check: %v", err)
            return nil // Non-fatal
        }
        c.checkAndUpdateAggregates(ctx, slip, stepName)
    }

    return nil
}

// checkAndUpdateAggregates checks if all components have completed a step
func (c *Client) checkAndUpdateAggregates(ctx context.Context, slip *Slip, stepName string) {
    aggregateMap := map[string]string{
        "build":     "builds_completed",
        "unit_test": "unit_tests_completed",
    }

    aggregateStep, ok := aggregateMap[stepName]
    if !ok {
        return
    }

    allCompleted := true
    anyFailed := false

    for _, comp := range slip.Components {
        var status StepStatus
        switch stepName {
        case "build":
            status = comp.BuildStatus
        case "unit_test":
            status = comp.UnitTestStatus
        }

        if !status.IsTerminal() {
            allCompleted = false
        }
        if status.IsFailure() {
            anyFailed = true
        }
    }

    if !allCompleted {
        return
    }

    var aggregateStatus StepStatus
    var message string
    if anyFailed {
        aggregateStatus = StepStatusFailed
        message = "one or more components failed"
    } else {
        aggregateStatus = StepStatusCompleted
        message = "all components completed successfully"
    }

    c.logger.Infof("Updating aggregate %s to %s", aggregateStep, aggregateStatus)
    _ = c.UpdateStepWithStatus(ctx, slip.SlipID.String(), aggregateStep, "", aggregateStatus, message)
}
```

### 4.3 Push-Hook-Parser Changes (Minimal)

Now push-hook-parser becomes a thin consumer of the library:

**File**: `pushhookparser/pushparser.go`

```go
import (
    "github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

type PushParser struct {
    // ... existing fields ...
    slippyClient *slippy.Client
}

// ProcessPush handles a git push event
func (p *PushParser) ProcessPush(ctx context.Context, event *PushEvent) error {
    p.logger.Infof("Processing push for %s branch %s", event.Repository, event.Branch)

    // 1. Detect components (existing logic)
    components, err := p.detectComponents(ctx, event)
    if err != nil {
        return fmt.Errorf("failed to detect components: %w", err)
    }

    // Convert to slippy component definitions
    slippyComponents := make([]slippy.ComponentDefinition, len(components))
    for i, c := range components {
        slippyComponents[i] = slippy.ComponentDefinition{
            Name:           c.Name,
            DockerfilePath: c.DockerfilePath,
        }
    }

    // 2. Create routing slip via library (simple API call)
    var slip *slippy.Slip
    if p.config.SlippyEnabled {
        slip, err = p.slippyClient.CreateSlipForPush(ctx, slippy.PushOptions{
            CorrelationID: event.CorrelationID,
            Repository:    event.Repository,
            Branch:        event.Branch,
            CommitSHA:     event.CommitSHA,
            Components:    slippyComponents,
        })
        if err != nil {
            p.logger.Errorf("Failed to create routing slip: %v", err)
            // Continue - slip creation failure shouldn't block pipeline
        }
    }

    // 3. Emit Kafka events (existing logic)
    for _, component := range components {
        if err := p.emitBuildEvent(ctx, component, event); err != nil {
            p.logger.Errorf("Failed to emit build event for %s: %v", component.Name, err)
        }
    }

    if err := p.emitSecretScanEvent(ctx, event); err != nil {
        p.logger.Errorf("Failed to emit secret scan event: %v", err)
    }

    // 4. Mark push_parsed as completed via library (simple API call)
    if slip != nil {
        if err := p.slippyClient.CompleteStep(ctx, slip.SlipID.String(), "push_parsed", ""); err != nil {
            p.logger.Errorf("Failed to complete push_parsed step: %v", err)
        }
    }

    p.logger.Infof("Push processing complete for %s", event.Repository)
    return nil
}
```

### 4.4 Benefits of Library-First Approach

| Aspect | Library Approach | Inline Approach (Avoided) |
|--------|------------------|---------------------------|
| **Code location** | Business logic in `goLibMyCarrier/slippy` | Scattered across consumers |
| **Testability** | Library tested in isolation | Requires mocking whole consumer |
| **Consistency** | Same behavior everywhere | Risk of divergence |
| **Maintenance** | Fix once, benefit everywhere | Fix in each consumer |
| **API surface** | Clean, documented methods | Implementation details exposed |

### 4.5 Configuration Changes

**File**: `pushhookparser/config.go`

```go
type Config struct {
    // ... existing fields ...
    
    // Slippy configuration
    SlippyEnabled       bool   `mapstructure:"SLIPPY_ENABLED"`
    SlippyClickHouseDSN string `mapstructure:"SLIPPY_CLICKHOUSE_DSN"`
}
```

### 4.6 Testing Strategy

**Library tests** (in `goLibMyCarrier/slippy/`):
```go
// z_push_test.go
func TestCreateSlipForPush_NewPush(t *testing.T) {
    mockStore := &MockSlipStore{}
    mockStore.On("LoadByCommit", mock.Anything, "owner/repo", "abc123").
        Return(nil, ErrNotFound)
    mockStore.On("Create", mock.Anything, mock.AnythingOfType("*Slip")).
        Return(nil)

    client := &Client{store: mockStore, logger: &NopLogger{}}

    slip, err := client.CreateSlipForPush(context.Background(), PushOptions{
        CorrelationID: "corr-123",
        Repository:    "owner/repo",
        Branch:        "main",
        CommitSHA:     "abc123",
        Components: []ComponentDefinition{
            {Name: "svc-a", DockerfilePath: "src/A/Dockerfile"},
        },
    })

    assert.NoError(t, err)
    assert.NotNil(t, slip)
    assert.Equal(t, "corr-123", slip.CorrelationID)
    assert.Equal(t, StepStatusRunning, slip.Steps["push_parsed"].Status)
    mockStore.AssertExpectations(t)
}

func TestCreateSlipForPush_RetryDetected(t *testing.T) {
    existingSlip := &Slip{
        SlipID:        uuid.New(),
        CorrelationID: "corr-123",
        CommitSHA:     "abc123",
    }

    mockStore := &MockSlipStore{}
    mockStore.On("LoadByCommit", mock.Anything, "owner/repo", "abc123").
        Return(existingSlip, nil)
    mockStore.On("UpdateStep", mock.Anything, mock.Anything, "push_parsed", "", StepStatusRunning).
        Return(nil)
    mockStore.On("AppendHistory", mock.Anything, mock.Anything, mock.Anything).
        Return(nil)
    mockStore.On("Load", mock.Anything, "corr-123").
        Return(existingSlip, nil)

    client := &Client{store: mockStore, logger: &NopLogger{}}

    slip, err := client.CreateSlipForPush(context.Background(), PushOptions{
        CorrelationID: "corr-123",
        Repository:    "owner/repo",
        CommitSHA:     "abc123",
    })

    assert.NoError(t, err)
    assert.Equal(t, existingSlip.SlipID, slip.SlipID)
    mockStore.AssertExpectations(t)
}
```

**Consumer tests** (in `pushhookparser/`):
```go
// z_slippy_test.go - much simpler, just verify library is called correctly
func TestProcessPush_CreatesSlip(t *testing.T) {
    mockSlippy := &MockSlippyClient{}
    mockSlippy.On("CreateSlipForPush", mock.Anything, mock.MatchedBy(func(opts slippy.PushOptions) bool {
        return opts.Repository == "owner/repo" && opts.CorrelationID == "corr-123"
    })).Return(&slippy.Slip{SlipID: uuid.New()}, nil)
    mockSlippy.On("CompleteStep", mock.Anything, mock.Anything, "push_parsed", "").
        Return(nil)

    parser := &PushParser{
        slippyClient: mockSlippy,
        config:       &Config{SlippyEnabled: true},
    }

    err := parser.ProcessPush(ctx, &PushEvent{
        CorrelationID: "corr-123",
        Repository:    "owner/repo",
    })

    assert.NoError(t, err)
    mockSlippy.AssertExpectations(t)
}
```

### 4.7 Deliverables

| Deliverable | Description | Owner |
|-------------|-------------|-------|
| `goLibMyCarrier/slippy/push.go` | CreateSlipForPush API | Library team |
| `goLibMyCarrier/slippy/steps.go` | CompleteStep, FailStep APIs | Library team |
| `goLibMyCarrier/slippy/z_push_test.go` | Library unit tests | Library team |
| `pushhookparser/pushparser.go` changes | Call library methods | Consumer team |
| `pushhookparser/config.go` changes | Add slippy config | Consumer team |

### 4.8 Rollout Approach

1. **Phase A**: Release `goLibMyCarrier/slippy` v1.0.0 with full API
2. **Phase B**: Deploy push-hook-parser with `SLIPPY_ENABLED=false`
3. **Phase C**: Enable for single test repository
4. **Phase D**: Enable for all repositories, monitor
5. **Phase E**: Remove feature flag after stable period

---

## Phase 5: Workflow Template Modifications

**Goal**: Add Slippy init containers and exit handlers to all workflow templates using Kustomize overlays for maintainability.

### 5.1 Modification Strategy

Rather than modifying workflow templates directly, we use **Kustomize strategic merge patches** to inject Slippy containers. This keeps the base templates clean and allows easy enable/disable of Slippy.

### 5.2 Directory Structure

```
workflow-dev/
├── workflows/
│   ├── buildkit.yaml              # Base workflow
│   ├── unit-test.yaml
│   ├── secretscan.yaml
│   ├── render-manualv2.yaml
│   ├── autotriggertests.yaml
│   ├── create-github-release.yaml
│   ├── releasev2.yaml
│   ├── deploy-reporter.yaml
│   └── alert-gate.yaml
│
└── overlays/
    └── slippy/
        ├── kustomization.yaml     # Main kustomization
        ├── slippy-config.yaml     # ConfigMap for slippy settings
        ├── patches/
        │   ├── buildkit-slippy.yaml
        │   ├── unit-test-slippy.yaml
        │   ├── secretscan-slippy.yaml
        │   ├── render-manualv2-slippy.yaml
        │   ├── autotriggertests-slippy.yaml
        │   ├── create-github-release-slippy.yaml
        │   ├── releasev2-slippy.yaml
        │   └── alert-gate-slippy.yaml
        └── templates/
            └── slippy-exit-handler.yaml  # Shared exit handler template
```

### 5.3 Slippy ConfigMap

**File**: `overlays/slippy/slippy-config.yaml`

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: slippy-config
  namespace: argo
data:
  # Maximum time to wait for prerequisites (default: 60 minutes)
  hold_timeout: "60m"
  
  # Interval between prerequisite checks (default: 60 seconds)  
  poll_interval: "60s"
  
  # ClickHouse connection string
  clickhouse_dsn: "clickhouse://clickhouse.ci.svc.cluster.local:9000/ci"
  
  # Debug logging
  debug: "false"
```

### 5.4 Shared Exit Handler Template

**File**: `overlays/slippy/templates/slippy-exit-handler.yaml`

```yaml
# This template is included via templateRef in each workflow
apiVersion: argoproj.io/v1alpha1
kind: WorkflowTemplate
metadata:
  name: slippy-exit-handler
  namespace: argo
spec:
  templates:
    - name: slippy-post
      inputs:
        parameters:
          - name: workflow_name
          - name: workflow_template
          - name: correlation_id
          - name: step_name
          - name: component_name
            default: ""
      container:
        image: ci/slippy:latest
        env:
          - name: SLIPPY_MODE
            value: "post"
          - name: SLIPPY_WORKFLOW_NAME
            value: "{{inputs.parameters.workflow_name}}"
          - name: SLIPPY_WORKFLOW_TEMPLATE
            value: "{{inputs.parameters.workflow_template}}"
          - name: SLIPPY_CORRELATION_ID
            value: "{{inputs.parameters.correlation_id}}"
          - name: SLIPPY_STEP_NAME
            value: "{{inputs.parameters.step_name}}"
          - name: SLIPPY_COMPONENT_NAME
            value: "{{inputs.parameters.component_name}}"
          - name: SLIPPY_CLICKHOUSE_DSN
            valueFrom:
              configMapKeyRef:
                name: slippy-config
                key: clickhouse_dsn
          - name: SLIPPY_DEBUG
            valueFrom:
              configMapKeyRef:
                name: slippy-config
                key: debug
                optional: true
          # Argo provides these in exit handlers
          - name: ARGO_WORKFLOW_STATUS
            value: "{{workflow.status}}"
          - name: ARGO_WORKFLOW_FAILURE_MESSAGE
            value: "{{workflow.failures}}"
```

### 5.5 Buildkit Patch

**File**: `overlays/slippy/patches/buildkit-slippy.yaml`

```yaml
apiVersion: argoproj.io/v1alpha1
kind: WorkflowTemplate
metadata:
  name: buildkit
spec:
  # Add onExit handler
  onExit: slippy-exit
  
  templates:
    # Patch the main build template to add slippy init container
    - name: build
      initContainers:
        # Slippy pre-execution check (runs FIRST)
        - name: slippy-pre
          image: ci/slippy:latest
          env:
            - name: SLIPPY_MODE
              value: "pre"
            - name: SLIPPY_WORKFLOW_NAME
              value: "{{workflow.name}}"
            - name: SLIPPY_WORKFLOW_TEMPLATE
              value: "buildkit"
            - name: SLIPPY_CORRELATION_ID
              value: "{{workflow.parameters.correlation_id}}"
            - name: SLIPPY_STEP_NAME
              value: "build"
            - name: SLIPPY_COMPONENT_NAME
              value: "{{workflow.parameters.component_name}}"
            - name: SLIPPY_PREREQUISITES
              value: "push_parsed"
            - name: SLIPPY_HOLD_TIMEOUT
              valueFrom:
                configMapKeyRef:
                  name: slippy-config
                  key: hold_timeout
                  optional: true
            - name: SLIPPY_POLL_INTERVAL
              valueFrom:
                configMapKeyRef:
                  name: slippy-config
                  key: poll_interval
                  optional: true
            - name: SLIPPY_CLICKHOUSE_DSN
              valueFrom:
                configMapKeyRef:
                  name: slippy-config
                  key: clickhouse_dsn
            - name: SLIPPY_DEBUG
              valueFrom:
                configMapKeyRef:
                  name: slippy-config
                  key: debug
                  optional: true
    
    # Exit handler template
    - name: slippy-exit
      templateRef:
        name: slippy-exit-handler
        template: slippy-post
      arguments:
        parameters:
          - name: workflow_name
            value: "{{workflow.name}}"
          - name: workflow_template
            value: "buildkit"
          - name: correlation_id
            value: "{{workflow.parameters.correlation_id}}"
          - name: step_name
            value: "build"
          - name: component_name
            value: "{{workflow.parameters.component_name}}"
```

### 5.6 Render-ManualV2 Patch (Dev Deploy Example)

**File**: `overlays/slippy/patches/render-manualv2-slippy.yaml`

```yaml
apiVersion: argoproj.io/v1alpha1
kind: WorkflowTemplate
metadata:
  name: render-manualv2
spec:
  onExit: slippy-exit
  
  templates:
    - name: render
      initContainers:
        - name: slippy-pre
          image: ci/slippy:latest
          env:
            - name: SLIPPY_MODE
              value: "pre"
            - name: SLIPPY_WORKFLOW_NAME
              value: "{{workflow.name}}"
            - name: SLIPPY_WORKFLOW_TEMPLATE
              value: "render-manualv2"
            - name: SLIPPY_CORRELATION_ID
              value: "{{workflow.parameters.correlation_id}}"
            - name: SLIPPY_STEP_NAME
              # Dynamic based on environment parameter
              value: "{{workflow.parameters.environment}}_deploy"
            - name: SLIPPY_PREREQUISITES
              # Prerequisites depend on environment
              # dev: builds_completed, unit_tests_completed, secret_scan_completed
              # preprod: dev_tests
              value: "{{workflow.parameters.slippy_prerequisites}}"
            - name: SLIPPY_HOLD_TIMEOUT
              # Longer timeout for deploy steps (may wait for builds)
              value: "120m"
            - name: SLIPPY_POLL_INTERVAL
              valueFrom:
                configMapKeyRef:
                  name: slippy-config
                  key: poll_interval
                  optional: true
            - name: SLIPPY_CLICKHOUSE_DSN
              valueFrom:
                configMapKeyRef:
                  name: slippy-config
                  key: clickhouse_dsn
    
    - name: slippy-exit
      templateRef:
        name: slippy-exit-handler
        template: slippy-post
      arguments:
        parameters:
          - name: workflow_name
            value: "{{workflow.name}}"
          - name: workflow_template
            value: "render-manualv2"
          - name: correlation_id
            value: "{{workflow.parameters.correlation_id}}"
          - name: step_name
            value: "{{workflow.parameters.environment}}_deploy"
          - name: component_name
            value: ""
```

### 5.7 Workflow-Prerequisite Mapping

| Workflow | Step Name | Prerequisites | Hold Timeout |
|----------|-----------|---------------|--------------|
| **buildkit** | `build` | `push_parsed` | 60m |
| **unit-test** | `unit_test` | `push_parsed` | 60m |
| **secretscan** | `secret_scan` | `push_parsed` | 60m |
| **render-manualv2** (dev) | `dev_deploy` | `builds_completed,unit_tests_completed,secret_scan_completed` | 120m |
| **autotriggertests** (dev) | `dev_tests` | `dev_deploy` | 60m |
| **render-manualv2** (preprod) | `preprod_deploy` | `dev_tests` | 60m |
| **autotriggertests** (preprod) | `preprod_tests` | `preprod_deploy` | 60m |
| **create-github-release** | `prod_release_created` | `preprod_tests` | 60m |
| **releasev2** | `prod_deploy` | `prod_release_created` | 60m |
| **autotriggertests** (prod) | `prod_tests` | `prod_deploy` | 60m |
| **alert-gate** | `alert_gate` | `prod_tests` | 60m |

### 5.8 Kustomization File

**File**: `overlays/slippy/kustomization.yaml`

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: argo

resources:
  - ../../workflows/  # Base workflow templates
  - slippy-config.yaml
  - templates/slippy-exit-handler.yaml

patchesStrategicMerge:
  - patches/buildkit-slippy.yaml
  - patches/unit-test-slippy.yaml
  - patches/secretscan-slippy.yaml
  - patches/render-manualv2-slippy.yaml
  - patches/autotriggertests-slippy.yaml
  - patches/create-github-release-slippy.yaml
  - patches/releasev2-slippy.yaml
  - patches/alert-gate-slippy.yaml

images:
  - name: ci/slippy
    newTag: latest  # Override with specific version in production
```

### 5.9 Sensor Modifications

Sensors that trigger workflows must pass `correlation_id` and (for environment-aware steps) `slippy_prerequisites`:

**Example**: `render-manualv2-sensor.yaml` modification

```yaml
spec:
  triggers:
    - template:
        name: render-manualv2-trigger
        argoWorkflow:
          operation: submit
          source:
            resource:
              apiVersion: argoproj.io/v1alpha1
              kind: Workflow
              metadata:
                generateName: render-manualv2-
              spec:
                workflowTemplateRef:
                  name: render-manualv2
                arguments:
                  parameters:
                    - name: correlation_id
                      value: "{{correlation_id}}"  # From event payload
                    - name: environment
                      value: "{{environment}}"
                    # Map environment to prerequisites
                    - name: slippy_prerequisites
                      value: >-
                        {{#switch environment}}
                          {{#case "dev"}}builds_completed,unit_tests_completed,secret_scan_completed{{/case}}
                          {{#case "preprod"}}dev_tests{{/case}}
                          {{#default}}{{/default}}
                        {{/switch}}
```

### 5.10 Deliverables

| Deliverable | Description | Acceptance Criteria |
|-------------|-------------|---------------------|
| `slippy-config.yaml` | ConfigMap for settings | Deploys to cluster |
| `slippy-exit-handler.yaml` | Shared exit handler | Template works |
| Patch files (8) | Per-workflow patches | All workflows patched |
| `kustomization.yaml` | Main kustomization | `kustomize build` succeeds |
| Sensor updates | Pass correlation_id | Events include correlation |

### 5.11 Testing Workflow Changes

```bash
# Validate kustomization
kustomize build overlays/slippy/ | kubectl apply --dry-run=client -f -

# Apply to test namespace
kustomize build overlays/slippy/ | kubectl apply -n argo-test -f -

# Trigger test workflow
argo submit --from workflowtemplate/buildkit \
  -p correlation_id=test-123 \
  -p component_name=test-service \
  -n argo-test
```

---

## Phase 6: Testing Strategy

**Goal**: Comprehensive testing at library, application, and integration levels to ensure reliability before production rollout.

### 6.1 Testing Pyramid

```
                    ╱╲
                   ╱  ╲
                  ╱ E2E╲          ← Few: Full pipeline tests
                 ╱──────╲
                ╱        ╲
               ╱Integration╲      ← Medium: Component interactions
              ╱────────────╲
             ╱              ╲
            ╱   Unit Tests   ╲    ← Many: Library functions
           ╱──────────────────╲
```

### 6.2 Unit Tests (Library Layer)

**Location**: `goLibMyCarrier/slippy/z_*_test.go`

**Coverage Targets**:
- All public functions ≥90% coverage
- All error paths tested
- Edge cases for status transitions

#### 6.2.1 Type Tests

**File**: `goLibMyCarrier/slippy/z_types_test.go`

```go
package slippy

import (
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/stretchr/testify/assert"
)

func TestStepStatus_IsTerminal(t *testing.T) {
    tests := []struct {
        status   StepStatus
        terminal bool
    }{
        {StepStatusPending, false},
        {StepStatusRunning, false},
        {StepStatusHeld, false},
        {StepStatusCompleted, true},
        {StepStatusFailed, true},
        {StepStatusSkipped, true},
    }

    for _, tc := range tests {
        t.Run(string(tc.status), func(t *testing.T) {
            assert.Equal(t, tc.terminal, tc.status.IsTerminal())
        })
    }
}

func TestStepStatus_IsFailure(t *testing.T) {
    assert.True(t, StepStatusFailed.IsFailure())
    assert.False(t, StepStatusCompleted.IsFailure())
    assert.False(t, StepStatusPending.IsFailure())
}

func TestSlip_GetStep(t *testing.T) {
    slip := &Slip{
        Steps: map[string]Step{
            "push_parsed": {Status: StepStatusCompleted},
            "build":       {Status: StepStatusRunning},
        },
    }

    step, ok := slip.GetStep("push_parsed")
    assert.True(t, ok)
    assert.Equal(t, StepStatusCompleted, step.Status)

    _, ok = slip.GetStep("nonexistent")
    assert.False(t, ok)
}

func TestSlip_GetComponentStep(t *testing.T) {
    slip := &Slip{
        Components: []Component{
            {Name: "svc-a", BuildStatus: StepStatusCompleted},
            {Name: "svc-b", BuildStatus: StepStatusRunning},
        },
    }

    status, ok := slip.GetComponentStep("svc-a", "build")
    assert.True(t, ok)
    assert.Equal(t, StepStatusCompleted, status)

    status, ok = slip.GetComponentStep("svc-b", "build")
    assert.True(t, ok)
    assert.Equal(t, StepStatusRunning, status)

    _, ok = slip.GetComponentStep("svc-c", "build")
    assert.False(t, ok)
}

func TestSlip_IsComplete(t *testing.T) {
    tests := []struct {
        name     string
        status   SlipStatus
        expected bool
    }{
        {"in_progress", SlipStatusInProgress, false},
        {"completed", SlipStatusCompleted, true},
        {"failed", SlipStatusFailed, true},
        {"cancelled", SlipStatusCancelled, true},
    }

    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            slip := &Slip{Status: tc.status}
            assert.Equal(t, tc.expected, slip.IsComplete())
        })
    }
}
```

#### 6.2.2 Prerequisite Tests

**File**: `goLibMyCarrier/slippy/z_prereq_test.go`

```go
package slippy

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/mock"
)

func TestCheckPrerequisites_AllMet(t *testing.T) {
    mockStore := &MockSlipStore{}
    slip := &Slip{
        SlipID: uuid.New(),
        Steps: map[string]Step{
            "push_parsed":      {Status: StepStatusCompleted},
            "build":            {Status: StepStatusCompleted},
            "unit_test":        {Status: StepStatusCompleted},
            "secret_scan":      {Status: StepStatusCompleted},
            "builds_completed": {Status: StepStatusCompleted},
        },
    }
    mockStore.On("Load", mock.Anything, "corr-123").Return(slip, nil)

    client := NewClient(Config{}, mockStore, &NopLogger{})

    result, err := client.CheckPrerequisites(context.Background(), CheckPrereqOptions{
        CorrelationID: "corr-123",
        Prerequisites: []string{"builds_completed"},
    })

    assert.NoError(t, err)
    assert.True(t, result.AllMet)
    assert.Empty(t, result.Pending)
}

func TestCheckPrerequisites_SomePending(t *testing.T) {
    mockStore := &MockSlipStore{}
    slip := &Slip{
        SlipID: uuid.New(),
        Steps: map[string]Step{
            "push_parsed": {Status: StepStatusCompleted},
            "build":       {Status: StepStatusRunning},
        },
        Components: []Component{
            {Name: "svc-a", BuildStatus: StepStatusRunning},
        },
    }
    mockStore.On("Load", mock.Anything, "corr-123").Return(slip, nil)

    client := NewClient(Config{}, mockStore, &NopLogger{})

    result, err := client.CheckPrerequisites(context.Background(), CheckPrereqOptions{
        CorrelationID: "corr-123",
        Prerequisites: []string{"builds_completed"},
    })

    assert.NoError(t, err)
    assert.False(t, result.AllMet)
    assert.Contains(t, result.Pending, "builds_completed")
}

func TestCheckPrerequisites_Failed(t *testing.T) {
    mockStore := &MockSlipStore{}
    slip := &Slip{
        SlipID: uuid.New(),
        Steps: map[string]Step{
            "push_parsed": {Status: StepStatusCompleted},
        },
        Components: []Component{
            {Name: "svc-a", BuildStatus: StepStatusFailed},
        },
    }
    mockStore.On("Load", mock.Anything, "corr-123").Return(slip, nil)

    client := NewClient(Config{}, mockStore, &NopLogger{})

    result, err := client.CheckPrerequisites(context.Background(), CheckPrereqOptions{
        CorrelationID: "corr-123",
        Prerequisites: []string{"builds_completed"},
    })

    assert.NoError(t, err)
    assert.False(t, result.AllMet)
    assert.True(t, result.HasFailed)
    assert.Contains(t, result.Failed, "builds_completed")
}

func TestCheckPrerequisites_ComponentSpecific(t *testing.T) {
    mockStore := &MockSlipStore{}
    slip := &Slip{
        SlipID: uuid.New(),
        Components: []Component{
            {Name: "svc-a", BuildStatus: StepStatusCompleted, UnitTestStatus: StepStatusCompleted},
            {Name: "svc-b", BuildStatus: StepStatusRunning, UnitTestStatus: StepStatusPending},
        },
    }
    mockStore.On("Load", mock.Anything, "corr-123").Return(slip, nil)

    client := NewClient(Config{}, mockStore, &NopLogger{})

    // Check for component svc-a only
    result, err := client.CheckPrerequisites(context.Background(), CheckPrereqOptions{
        CorrelationID: "corr-123",
        ComponentName: "svc-a",
        Prerequisites: []string{"build", "unit_test"},
    })

    assert.NoError(t, err)
    assert.True(t, result.AllMet)
}
```

#### 6.2.3 Client Tests

**File**: `goLibMyCarrier/slippy/z_client_test.go`

```go
package slippy

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/mock"
)

func TestRunPreExecution_PrereqsMet(t *testing.T) {
    mockStore := &MockSlipStore{}
    slip := &Slip{
        SlipID: uuid.New(),
        Steps: map[string]Step{
            "builds_completed":      {Status: StepStatusCompleted},
            "unit_tests_completed":  {Status: StepStatusCompleted},
            "secret_scan_completed": {Status: StepStatusCompleted},
        },
    }

    mockStore.On("Load", mock.Anything, "corr-123").Return(slip, nil)
    mockStore.On("UpdateStep", mock.Anything, mock.Anything, "dev_deploy", "", StepStatusRunning).Return(nil)
    mockStore.On("AppendHistory", mock.Anything, mock.Anything, mock.Anything).Return(nil)

    client := NewClient(Config{}, mockStore, &NopLogger{})

    result, err := client.RunPreExecution(context.Background(), PreExecutionOptions{
        CorrelationID: "corr-123",
        StepName:      "dev_deploy",
        Prerequisites: []string{"builds_completed", "unit_tests_completed", "secret_scan_completed"},
        HoldTimeout:   time.Minute,
        PollInterval:  time.Second,
    })

    assert.NoError(t, err)
    assert.Equal(t, PreExecutionOutcomeProceed, result.Outcome)
    mockStore.AssertExpectations(t)
}

func TestRunPreExecution_HoldThenProceed(t *testing.T) {
    mockStore := &MockSlipStore{}

    // First call: prereq not met
    slipNotReady := &Slip{
        SlipID: uuid.New(),
        Steps: map[string]Step{
            "builds_completed": {Status: StepStatusRunning},
        },
    }

    // Second call: prereq met
    slipReady := &Slip{
        SlipID: uuid.New(),
        Steps: map[string]Step{
            "builds_completed": {Status: StepStatusCompleted},
        },
    }

    // Set up mock to return different values on successive calls
    mockStore.On("Load", mock.Anything, "corr-123").Return(slipNotReady, nil).Once()
    mockStore.On("UpdateStep", mock.Anything, mock.Anything, "dev_deploy", "", StepStatusHeld).Return(nil).Once()
    mockStore.On("AppendHistory", mock.Anything, mock.Anything, mock.Anything).Return(nil)
    mockStore.On("Load", mock.Anything, "corr-123").Return(slipReady, nil).Once()
    mockStore.On("UpdateStep", mock.Anything, mock.Anything, "dev_deploy", "", StepStatusRunning).Return(nil).Once()

    client := NewClient(Config{}, mockStore, &NopLogger{})

    result, err := client.RunPreExecution(context.Background(), PreExecutionOptions{
        CorrelationID: "corr-123",
        StepName:      "dev_deploy",
        Prerequisites: []string{"builds_completed"},
        HoldTimeout:   time.Minute,
        PollInterval:  100 * time.Millisecond,
    })

    assert.NoError(t, err)
    assert.Equal(t, PreExecutionOutcomeProceed, result.Outcome)
}

func TestRunPreExecution_PrereqFailed(t *testing.T) {
    mockStore := &MockSlipStore{}
    slip := &Slip{
        SlipID: uuid.New(),
        Steps: map[string]Step{
            "builds_completed": {Status: StepStatusFailed},
        },
    }

    mockStore.On("Load", mock.Anything, "corr-123").Return(slip, nil)
    mockStore.On("UpdateStep", mock.Anything, mock.Anything, "dev_deploy", "", StepStatusSkipped).Return(nil)
    mockStore.On("AppendHistory", mock.Anything, mock.Anything, mock.Anything).Return(nil)

    client := NewClient(Config{}, mockStore, &NopLogger{})

    result, err := client.RunPreExecution(context.Background(), PreExecutionOptions{
        CorrelationID: "corr-123",
        StepName:      "dev_deploy",
        Prerequisites: []string{"builds_completed"},
        HoldTimeout:   time.Minute,
        PollInterval:  time.Second,
    })

    assert.NoError(t, err)
    assert.Equal(t, PreExecutionOutcomeSkip, result.Outcome)
}

func TestRunPostExecution_Success(t *testing.T) {
    mockStore := &MockSlipStore{}
    slip := &Slip{SlipID: uuid.New(), Status: SlipStatusInProgress}

    mockStore.On("Load", mock.Anything, "corr-123").Return(slip, nil)
    mockStore.On("UpdateStep", mock.Anything, mock.Anything, "build", "svc-a", StepStatusCompleted).Return(nil)
    mockStore.On("AppendHistory", mock.Anything, mock.Anything, mock.Anything).Return(nil)

    client := NewClient(Config{}, mockStore, &NopLogger{})

    err := client.RunPostExecution(context.Background(), PostExecutionOptions{
        CorrelationID:  "corr-123",
        StepName:       "build",
        ComponentName:  "svc-a",
        WorkflowStatus: "Succeeded",
    })

    assert.NoError(t, err)
    mockStore.AssertExpectations(t)
}

func TestRunPostExecution_Failure(t *testing.T) {
    mockStore := &MockSlipStore{}
    slip := &Slip{SlipID: uuid.New(), Status: SlipStatusInProgress}

    mockStore.On("Load", mock.Anything, "corr-123").Return(slip, nil)
    mockStore.On("UpdateStep", mock.Anything, mock.Anything, "build", "svc-a", StepStatusFailed).Return(nil)
    mockStore.On("AppendHistory", mock.Anything, mock.Anything, mock.Anything).Return(nil)

    client := NewClient(Config{}, mockStore, &NopLogger{})

    err := client.RunPostExecution(context.Background(), PostExecutionOptions{
        CorrelationID:  "corr-123",
        StepName:       "build",
        ComponentName:  "svc-a",
        WorkflowStatus: "Failed",
        FailureMessage: "build command exited with code 1",
    })

    assert.NoError(t, err)
    mockStore.AssertExpectations(t)
}
```

### 6.3 Integration Tests

**Location**: `goLibMyCarrier/slippy/z_integration_test.go`

Tests require a real ClickHouse instance (via test container).

```go
//go:build integration

package slippy

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/suite"
    "github.com/testcontainers/testcontainers-go"
    "github.com/testcontainers/testcontainers-go/wait"
)

type IntegrationTestSuite struct {
    suite.Suite
    clickhouse testcontainers.Container
    dsn        string
    client     *Client
}

func (s *IntegrationTestSuite) SetupSuite() {
    ctx := context.Background()

    // Start ClickHouse container
    req := testcontainers.ContainerRequest{
        Image:        "clickhouse/clickhouse-server:23.8",
        ExposedPorts: []string{"9000/tcp"},
        WaitingFor:   wait.ForLog("Ready for connections").WithStartupTimeout(60 * time.Second),
    }

    container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
        ContainerRequest: req,
        Started:          true,
    })
    s.Require().NoError(err)

    s.clickhouse = container

    host, _ := container.Host(ctx)
    port, _ := container.MappedPort(ctx, "9000")
    s.dsn = fmt.Sprintf("clickhouse://%s:%s/default", host, port.Port())

    // Run migrations
    store := NewClickHouseStore(s.dsn)
    s.Require().NoError(store.RunMigrations(ctx))

    // Create client
    s.client = NewClient(Config{ClickHouseDSN: s.dsn}, store, NewStdoutLogger())
}

func (s *IntegrationTestSuite) TearDownSuite() {
    if s.clickhouse != nil {
        s.clickhouse.Terminate(context.Background())
    }
}

func (s *IntegrationTestSuite) TestFullSlipLifecycle() {
    ctx := context.Background()

    // 1. Create slip via push
    slip, err := s.client.CreateSlipForPush(ctx, PushOptions{
        CorrelationID: "integration-test-1",
        Repository:    "mycarrier/test-repo",
        Branch:        "main",
        CommitSHA:     "abc123def456",
        Components: []ComponentDefinition{
            {Name: "svc-a", DockerfilePath: "src/A/Dockerfile"},
            {Name: "svc-b", DockerfilePath: "src/B/Dockerfile"},
        },
    })
    s.Require().NoError(err)
    s.Require().NotNil(slip)
    s.Equal(StepStatusRunning, slip.Steps["push_parsed"].Status)

    // 2. Complete push_parsed
    err = s.client.CompleteStep(ctx, slip.SlipID.String(), "push_parsed", "")
    s.Require().NoError(err)

    // 3. Run pre-execution for build (should proceed, prereq push_parsed is done)
    result, err := s.client.RunPreExecution(ctx, PreExecutionOptions{
        CorrelationID: "integration-test-1",
        StepName:      "build",
        ComponentName: "svc-a",
        Prerequisites: []string{"push_parsed"},
        HoldTimeout:   time.Second,
        PollInterval:  100 * time.Millisecond,
    })
    s.Require().NoError(err)
    s.Equal(PreExecutionOutcomeProceed, result.Outcome)

    // 4. Complete build for svc-a
    err = s.client.RunPostExecution(ctx, PostExecutionOptions{
        CorrelationID:  "integration-test-1",
        StepName:       "build",
        ComponentName:  "svc-a",
        WorkflowStatus: "Succeeded",
    })
    s.Require().NoError(err)

    // 5. Complete build for svc-b
    err = s.client.RunPostExecution(ctx, PostExecutionOptions{
        CorrelationID:  "integration-test-1",
        StepName:       "build",
        ComponentName:  "svc-b",
        WorkflowStatus: "Succeeded",
    })
    s.Require().NoError(err)

    // 6. Verify builds_completed aggregate is now completed
    reloaded, err := s.client.store.Load(ctx, "integration-test-1")
    s.Require().NoError(err)
    s.Equal(StepStatusCompleted, reloaded.Steps["builds_completed"].Status)
}

func (s *IntegrationTestSuite) TestRetryDetection() {
    ctx := context.Background()

    // Create first slip
    slip1, err := s.client.CreateSlipForPush(ctx, PushOptions{
        CorrelationID: "retry-test-1",
        Repository:    "mycarrier/retry-repo",
        Branch:        "main",
        CommitSHA:     "retry123",
        Components:    []ComponentDefinition{{Name: "svc-a"}},
    })
    s.Require().NoError(err)

    // Attempt to create again (simulating retry)
    slip2, err := s.client.CreateSlipForPush(ctx, PushOptions{
        CorrelationID: "retry-test-1", // Same correlation ID
        Repository:    "mycarrier/retry-repo",
        Branch:        "main",
        CommitSHA:     "retry123", // Same commit
        Components:    []ComponentDefinition{{Name: "svc-a"}},
    })
    s.Require().NoError(err)

    // Should return same slip ID
    s.Equal(slip1.SlipID, slip2.SlipID)
}

func TestIntegrationSuite(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration tests in short mode")
    }
    suite.Run(t, new(IntegrationTestSuite))
}
```

### 6.4 Shadow Mode Testing

Before fully enabling slippy, run in shadow mode where:
1. Routing slips are created and updated
2. Hold logic is evaluated but never actually holds
3. All workflows proceed as normal
4. Results are logged for comparison

**Implementation in library**:

```go
// Config option for shadow mode
type Config struct {
    // ... other fields ...
    ShadowMode bool `mapstructure:"SLIPPY_SHADOW_MODE"`
}

// In RunPreExecution
func (c *Client) RunPreExecution(ctx context.Context, opts PreExecutionOptions) (*PreExecutionResult, error) {
    // ... prerequisite check logic ...

    if c.config.ShadowMode {
        // Log what WOULD happen, but always proceed
        if !prereqResult.AllMet {
            c.logger.Warnf("[SHADOW] Would hold %s waiting for %v", opts.StepName, prereqResult.Pending)
        }
        if prereqResult.HasFailed {
            c.logger.Warnf("[SHADOW] Would skip %s due to failed prereqs %v", opts.StepName, prereqResult.Failed)
        }
        return &PreExecutionResult{Outcome: PreExecutionOutcomeProceed}, nil
    }

    // ... actual hold/skip logic ...
}
```

### 6.5 End-to-End Tests

Create test workflows that exercise the full pipeline with known outcomes:

**File**: `ci/slippy/tests/e2e_test.yaml`

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Workflow
metadata:
  generateName: slippy-e2e-test-
spec:
  entrypoint: main
  arguments:
    parameters:
      - name: test_correlation_id
        value: "e2e-test-{{workflow.name}}"
  
  templates:
    - name: main
      dag:
        tasks:
          # Simulate push_parsed
          - name: simulate-push
            template: create-slip
          
          # Simulate parallel builds
          - name: build-a
            template: simulate-build
            dependencies: [simulate-push]
            arguments:
              parameters:
                - name: component
                  value: "svc-a"
          
          - name: build-b
            template: simulate-build
            dependencies: [simulate-push]
            arguments:
              parameters:
                - name: component
                  value: "svc-b"
          
          # Dev deploy should wait for builds
          - name: dev-deploy
            template: simulate-deploy
            dependencies: [build-a, build-b]
            arguments:
              parameters:
                - name: environment
                  value: "dev"
          
          # Verify final state
          - name: verify
            template: verify-slip
            dependencies: [dev-deploy]
    
    - name: create-slip
      container:
        image: ci/slippy:test
        command: ["/app/test-helper", "create-slip"]
        env:
          - name: SLIPPY_CORRELATION_ID
            value: "{{workflow.parameters.test_correlation_id}}"
    
    - name: simulate-build
      inputs:
        parameters:
          - name: component
      initContainers:
        - name: slippy-pre
          image: ci/slippy:test
          env:
            - name: SLIPPY_MODE
              value: "pre"
            - name: SLIPPY_CORRELATION_ID
              value: "{{workflow.parameters.test_correlation_id}}"
            - name: SLIPPY_STEP_NAME
              value: "build"
            - name: SLIPPY_COMPONENT_NAME
              value: "{{inputs.parameters.component}}"
            - name: SLIPPY_PREREQUISITES
              value: "push_parsed"
      container:
        image: busybox
        command: ["echo", "Building {{inputs.parameters.component}}"]
    
    - name: simulate-deploy
      inputs:
        parameters:
          - name: environment
      initContainers:
        - name: slippy-pre
          image: ci/slippy:test
          env:
            - name: SLIPPY_MODE
              value: "pre"
            - name: SLIPPY_CORRELATION_ID
              value: "{{workflow.parameters.test_correlation_id}}"
            - name: SLIPPY_STEP_NAME
              value: "{{inputs.parameters.environment}}_deploy"
            - name: SLIPPY_PREREQUISITES
              value: "builds_completed,unit_tests_completed,secret_scan_completed"
            - name: SLIPPY_HOLD_TIMEOUT
              value: "5m"
      container:
        image: busybox
        command: ["echo", "Deploying to {{inputs.parameters.environment}}"]
    
    - name: verify-slip
      container:
        image: ci/slippy:test
        command: ["/app/test-helper", "verify-slip"]
        env:
          - name: SLIPPY_CORRELATION_ID
            value: "{{workflow.parameters.test_correlation_id}}"
          - name: EXPECTED_STATUS
            value: "in_progress"
          - name: EXPECTED_DEV_DEPLOY
            value: "completed"
```

### 6.6 Test Coverage Requirements

| Component | Minimum Coverage | Focus Areas |
|-----------|------------------|-------------|
| `goLibMyCarrier/slippy` | 90% | All public functions |
| `ci/slippy` standalone | 80% | Main execution paths |
| `pushhookparser` changes | 85% | Slip creation path |
| Integration tests | N/A | Full lifecycle |

### 6.7 Deliverables

| Deliverable | Description | Location |
|-------------|-------------|----------|
| Unit test files | Per-file test coverage | `goLibMyCarrier/slippy/z_*_test.go` |
| Mock implementations | Test doubles | `goLibMyCarrier/slippy/z_mocks_test.go` |
| Integration tests | ClickHouse tests | `goLibMyCarrier/slippy/z_integration_test.go` |
| E2E workflow | Full pipeline test | `ci/slippy/tests/e2e_test.yaml` |
| Test documentation | How to run tests | `goLibMyCarrier/slippy/TESTING.md` |

---

## Phase 7: Observability

**Goal**: Comprehensive monitoring, alerting, and debugging capabilities for the slippy system.

### 7.1 Metrics Strategy

Use OpenTelemetry metrics exported to Prometheus/Grafana.

#### 7.1.1 Library Metrics

**File**: `goLibMyCarrier/slippy/metrics.go`

```go
package slippy

import (
    "context"
    "time"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/metric"
)

var (
    meter = otel.Meter("slippy")

    // Counters
    slipsCreated, _        = meter.Int64Counter("slippy_slips_created_total",
        metric.WithDescription("Total routing slips created"))
    slipsCompleted, _      = meter.Int64Counter("slippy_slips_completed_total",
        metric.WithDescription("Total routing slips completed"))
    slipsFailed, _         = meter.Int64Counter("slippy_slips_failed_total",
        metric.WithDescription("Total routing slips failed"))
    stepsCompleted, _      = meter.Int64Counter("slippy_steps_completed_total",
        metric.WithDescription("Steps completed by step name"))
    stepsFailed, _         = meter.Int64Counter("slippy_steps_failed_total",
        metric.WithDescription("Steps failed by step name"))
    prereqChecks, _        = meter.Int64Counter("slippy_prereq_checks_total",
        metric.WithDescription("Prerequisite checks performed"))
    holdEvents, _          = meter.Int64Counter("slippy_hold_events_total",
        metric.WithDescription("Times a step entered hold state"))
    holdTimeouts, _        = meter.Int64Counter("slippy_hold_timeouts_total",
        metric.WithDescription("Hold timeouts exceeded"))

    // Histograms
    prereqCheckDuration, _ = meter.Float64Histogram("slippy_prereq_check_duration_seconds",
        metric.WithDescription("Time spent checking prerequisites"))
    holdDuration, _        = meter.Float64Histogram("slippy_hold_duration_seconds",
        metric.WithDescription("Time spent in hold state before proceeding"))
    stepDuration, _        = meter.Float64Histogram("slippy_step_duration_seconds",
        metric.WithDescription("Time to complete a step"))
    slipDuration, _        = meter.Float64Histogram("slippy_slip_duration_seconds",
        metric.WithDescription("Total time from slip creation to completion"))

    // Gauges
    activeSlips, _ = meter.Int64UpDownCounter("slippy_active_slips",
        metric.WithDescription("Currently in-progress slips"))
    heldSteps, _   = meter.Int64UpDownCounter("slippy_held_steps",
        metric.WithDescription("Currently held steps"))
)

// RecordSlipCreated records a new slip creation
func RecordSlipCreated(ctx context.Context, repo string, componentCount int) {
    slipsCreated.Add(ctx, 1, metric.WithAttributes(
        attribute.String("repository", repo),
        attribute.Int("component_count", componentCount),
    ))
    activeSlips.Add(ctx, 1)
}

// RecordSlipCompleted records a slip completion
func RecordSlipCompleted(ctx context.Context, repo string, status SlipStatus, duration time.Duration) {
    attrs := metric.WithAttributes(
        attribute.String("repository", repo),
        attribute.String("status", string(status)),
    )

    if status == SlipStatusCompleted {
        slipsCompleted.Add(ctx, 1, attrs)
    } else {
        slipsFailed.Add(ctx, 1, attrs)
    }

    slipDuration.Record(ctx, duration.Seconds(), attrs)
    activeSlips.Add(ctx, -1)
}

// RecordStepCompleted records step completion
func RecordStepCompleted(ctx context.Context, stepName string, status StepStatus, duration time.Duration) {
    attrs := metric.WithAttributes(
        attribute.String("step", stepName),
    )

    if status == StepStatusCompleted {
        stepsCompleted.Add(ctx, 1, attrs)
    } else if status == StepStatusFailed {
        stepsFailed.Add(ctx, 1, attrs)
    }

    stepDuration.Record(ctx, duration.Seconds(), attrs)
}

// RecordHoldEvent records when a step enters hold state
func RecordHoldEvent(ctx context.Context, stepName string, pendingPrereqs []string) {
    holdEvents.Add(ctx, 1, metric.WithAttributes(
        attribute.String("step", stepName),
        attribute.StringSlice("pending", pendingPrereqs),
    ))
    heldSteps.Add(ctx, 1)
}

// RecordHoldResolved records when a hold is resolved
func RecordHoldResolved(ctx context.Context, stepName string, duration time.Duration, timedOut bool) {
    attrs := metric.WithAttributes(
        attribute.String("step", stepName),
        attribute.Bool("timed_out", timedOut),
    )

    holdDuration.Record(ctx, duration.Seconds(), attrs)
    heldSteps.Add(ctx, -1)

    if timedOut {
        holdTimeouts.Add(ctx, 1, attrs)
    }
}
```

### 7.2 Distributed Tracing

Integrate with existing OpenTelemetry setup in goLibMyCarrier.

```go
package slippy

import (
    "context"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("slippy")

// Example: instrumented method
func (c *Client) CreateSlipForPush(ctx context.Context, opts PushOptions) (*Slip, error) {
    ctx, span := tracer.Start(ctx, "slippy.CreateSlipForPush",
        trace.WithAttributes(
            attribute.String("correlation_id", opts.CorrelationID),
            attribute.String("repository", opts.Repository),
            attribute.String("branch", opts.Branch),
            attribute.Int("component_count", len(opts.Components)),
        ),
    )
    defer span.End()

    // ... implementation ...

    span.SetAttributes(attribute.String("slip_id", slip.SlipID.String()))
    return slip, nil
}
```

### 7.3 Logging Standards

Structured logging with consistent fields:

```go
// Standard log fields for all slippy operations
type LogContext struct {
    CorrelationID string `json:"correlation_id"`
    SlipID        string `json:"slip_id,omitempty"`
    Repository    string `json:"repository,omitempty"`
    StepName      string `json:"step,omitempty"`
    ComponentName string `json:"component,omitempty"`
}

// Example log output
// {
//   "level": "info",
//   "ts": "2024-01-15T10:30:00Z",
//   "msg": "prerequisite check complete",
//   "correlation_id": "abc-123",
//   "slip_id": "uuid-here",
//   "step": "dev_deploy",
//   "prereqs_met": true,
//   "pending": []
// }
```

### 7.4 Grafana Dashboard

**File**: `grafana/dashboards/slippy-overview.json`

Dashboard panels:

1. **Pipeline Overview**
   - Active slips (gauge)
   - Slip creation rate (time series)
   - Completion rate % (stat)
   - Failure rate % (stat)

2. **Step Performance**
   - Step completion times (heatmap by step)
   - Steps in hold state (bar gauge)
   - Hold durations (histogram)
   - Hold timeout rate (stat)

3. **Prerequisites**
   - Prereq check latency (histogram)
   - Most common blocking prereqs (table)
   - Average hold time by step (bar chart)

4. **Repository Metrics**
   - Slips by repository (pie chart)
   - Average time to production (table)
   - Failure hotspots (heatmap)

5. **Detailed View**
   - Individual slip timeline (logs panel)
   - Step state transitions (table)
   - Error messages (logs panel)

**Example Prometheus queries**:

```promql
# Slip creation rate
rate(slippy_slips_created_total[5m])

# Success rate
(
  rate(slippy_slips_completed_total{status="completed"}[1h]) 
  / rate(slippy_slips_completed_total[1h])
) * 100

# Average time to production (when prod_steady_state completes)
histogram_quantile(0.5, sum(rate(slippy_slip_duration_seconds_bucket[1h])) by (le))

# Steps currently held
slippy_held_steps

# Hold timeout rate
rate(slippy_hold_timeouts_total[1h])
```

### 7.5 Alerting Rules

**File**: `prometheus/rules/slippy-alerts.yaml`

```yaml
groups:
  - name: slippy
    interval: 30s
    rules:
      # High hold timeout rate
      - alert: SlippyHighHoldTimeoutRate
        expr: |
          rate(slippy_hold_timeouts_total[10m]) > 0.1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High rate of Slippy hold timeouts"
          description: "{{ $value | humanizePercentage }} of holds are timing out"

      # Many slips stuck in held state
      - alert: SlippyManyStepsHeld
        expr: |
          slippy_held_steps > 10
        for: 15m
        labels:
          severity: warning
        annotations:
          summary: "Many Slippy steps in held state"
          description: "{{ $value }} steps are currently held"

      # Pipeline failure rate spike
      - alert: SlippyPipelineFailureSpike
        expr: |
          (
            rate(slippy_slips_failed_total[15m])
            / (rate(slippy_slips_completed_total[15m]) + rate(slippy_slips_failed_total[15m]))
          ) > 0.2
        for: 10m
        labels:
          severity: critical
        annotations:
          summary: "High Slippy pipeline failure rate"
          description: "{{ $value | humanizePercentage }} of pipelines are failing"

      # ClickHouse connectivity
      - alert: SlippyClickHouseDown
        expr: |
          up{job="slippy-clickhouse"} == 0
        for: 2m
        labels:
          severity: critical
        annotations:
          summary: "Slippy cannot reach ClickHouse"
          description: "ClickHouse is unreachable for slip storage"

      # Slip creation errors
      - alert: SlippyCreationErrors
        expr: |
          rate(slippy_errors_total{operation="create"}[5m]) > 0
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Errors creating Slippy routing slips"
          description: "{{ $value }} errors per second creating slips"
```

### 7.6 ClickHouse Monitoring Queries

Pre-built queries for operational monitoring:

```sql
-- Slips stuck in held state > 30 minutes
SELECT 
    slip_id,
    correlation_id,
    repository,
    arrayJoin(arrayFilter(
        x -> x.1 = 'held',
        arrayZip(
            [dev_deploy_status, preprod_deploy_status, prod_deploy_status],
            ['dev_deploy', 'preprod_deploy', 'prod_deploy']
        )
    )).2 AS held_step,
    updated_at,
    dateDiff('minute', updated_at, now()) AS minutes_held
FROM ci.routing_slips FINAL
WHERE status = 'in_progress'
  AND (dev_deploy_status = 'held' 
       OR preprod_deploy_status = 'held' 
       OR prod_deploy_status = 'held')
  AND updated_at < now() - INTERVAL 30 MINUTE
ORDER BY minutes_held DESC;

-- Recent failures with details
SELECT 
    slip_id,
    correlation_id,
    repository,
    branch,
    status,
    created_at,
    updated_at,
    dateDiff('minute', created_at, updated_at) AS duration_minutes,
    JSONExtract(state_history, 'Array(Tuple(step String, status String, message String))') AS history
FROM ci.routing_slips FINAL
WHERE status = 'failed'
  AND created_at > now() - INTERVAL 1 DAY
ORDER BY created_at DESC
LIMIT 20;

-- Average time per step by repository
SELECT 
    repository,
    avg(dateDiff('second', 
        parseDateTimeBestEffort(JSONExtractString(step_timestamps, 'push_parsed.started_at')),
        parseDateTimeBestEffort(JSONExtractString(step_timestamps, 'dev_deploy.completed_at'))
    )) AS avg_seconds_to_dev
FROM ci.routing_slips FINAL
WHERE status = 'completed'
  AND created_at > now() - INTERVAL 7 DAY
GROUP BY repository
ORDER BY avg_seconds_to_dev DESC;
```

### 7.7 Deliverables

| Deliverable | Description | Location |
|-------------|-------------|----------|
| `metrics.go` | OTel metrics definitions | `goLibMyCarrier/slippy/` |
| Grafana dashboard | Slippy overview dashboard | `grafana/dashboards/slippy-overview.json` |
| Alert rules | Prometheus alerting rules | `prometheus/rules/slippy-alerts.yaml` |
| Runbook | Operational procedures | `docs/runbooks/slippy.md` |
| Monitoring queries | ClickHouse SQL queries | `docs/slippy-monitoring-queries.sql` |

---

## Phase 8: Rollout Plan

**Goal**: Safe, incremental production deployment with easy rollback capability.

### 8.1 Rollout Stages

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         SLIPPY ROLLOUT TIMELINE                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Week 1        Week 2        Week 3        Week 4        Week 5+            │
│    │             │             │             │             │                │
│    ▼             ▼             ▼             ▼             ▼                │
│  ┌─────┐      ┌─────┐      ┌─────┐      ┌─────┐      ┌─────┐               │
│  │Stage│      │Stage│      │Stage│      │Stage│      │Stage│               │
│  │  1  │ ───► │  2  │ ───► │  3  │ ───► │  4  │ ───► │  5  │               │
│  └─────┘      └─────┘      └─────┘      └─────┘      └─────┘               │
│                                                                             │
│  Shadow        Single       Expand       All          Feature              │
│  Mode          Repo         Repos        Repos        Flag                 │
│  Only          Test         (10%)        (100%)       Removal              │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 8.2 Stage 1: Shadow Mode (Week 1)

**Goal**: Validate slip creation and updates without affecting workflows.

**Configuration**:
```yaml
# ConfigMap for Stage 1
apiVersion: v1
kind: ConfigMap
metadata:
  name: slippy-config
data:
  shadow_mode: "true"
  enabled: "true"
```

**Actions**:
1. Deploy `goLibMyCarrier/slippy` library
2. Deploy `ci/slippy` container to cluster
3. Update `push-hook-parser` with `SLIPPY_ENABLED=true`, `SLIPPY_SHADOW_MODE=true`
4. Monitor logs for shadow mode predictions

**Validation**:
- Slips are being created (check ClickHouse)
- No workflow failures attributed to slippy
- Shadow logs show expected hold/skip decisions

**Rollback**: Set `SLIPPY_ENABLED=false` in push-hook-parser

### 8.3 Stage 2: Single Repository Test (Week 2)

**Goal**: Full slippy enforcement on one low-risk repository.

**Configuration**:
```yaml
# ConfigMap for Stage 2
data:
  shadow_mode: "false"
  enabled_repositories: "mycarrier/slippy-test-repo"
```

**Library modification** for repo filtering:

```go
func (c *Client) CreateSlipForPush(ctx context.Context, opts PushOptions) (*Slip, error) {
    if !c.isRepositoryEnabled(opts.Repository) {
        c.logger.Debugf("Slippy not enabled for repository %s", opts.Repository)
        return nil, nil // Return nil slip, workflows proceed normally
    }
    // ... rest of implementation
}

func (c *Client) isRepositoryEnabled(repo string) bool {
    // If no filter specified, all repos enabled
    if len(c.config.EnabledRepositories) == 0 {
        return true
    }
    for _, r := range c.config.EnabledRepositories {
        if r == repo || r == "*" {
            return true
        }
    }
    return false
}
```

**Actions**:
1. Deploy workflow patches to slippy-test-repo's workflows
2. Trigger full pipeline (commit → production)
3. Verify hold behavior works correctly
4. Test failure scenarios (failed build, failed deploy)
5. Test timeout scenarios

**Validation**:
- Full pipeline succeeds with slippy active
- Hold states work as expected
- Failed prerequisites cause correct skip behavior
- Metrics are being recorded

**Rollback**: Remove workflow patches from test repo

### 8.4 Stage 3: Expanded Rollout (Week 3)

**Goal**: Enable slippy for 10% of repositories.

**Configuration**:
```yaml
data:
  shadow_mode: "false"
  enabled_repositories: |
    mycarrier/slippy-test-repo
    mycarrier/service-a
    mycarrier/service-b
    mycarrier/service-c
```

**Actions**:
1. Select 10% of repositories (prefer lower-traffic repos first)
2. Apply workflow patches to selected repos
3. Monitor for 5 days
4. Gather feedback from developers

**Validation**:
- No increase in workflow failures
- Developer feedback is positive
- Grafana dashboard shows healthy metrics
- No alerts firing

**Rollback**: Remove workflow patches from affected repos

### 8.5 Stage 4: Full Rollout (Week 4)

**Goal**: Enable slippy for all repositories.

**Configuration**:
```yaml
data:
  shadow_mode: "false"
  enabled_repositories: "*"  # All repos
```

**Actions**:
1. Apply workflow patches to all workflow templates
2. Monitor closely for first 48 hours
3. Hold daily check-ins for first week
4. Document any issues and resolutions

**Validation**:
- Pipeline success rate remains stable
- No significant increase in deployment time
- All metrics within expected ranges

**Rollback**: 
1. Remove workflow patches (revert to base templates)
2. Set `SLIPPY_ENABLED=false` in push-hook-parser

### 8.6 Stage 5: Feature Flag Removal (Week 5+)

**Goal**: Remove feature flags, slippy becomes standard.

**Actions**:
1. Remove `SLIPPY_ENABLED` flag checks from push-hook-parser
2. Remove shadow mode code
3. Remove repository filtering code
4. Archive rollback procedures
5. Update documentation

### 8.7 Rollback Procedures

**Level 1: Disable for specific repository**
```bash
# Update ConfigMap to exclude repository
kubectl edit configmap slippy-config -n argo
# Change enabled_repositories to exclude problem repo
```

**Level 2: Disable all slippy hold behavior**
```bash
# Enable shadow mode (slips created but no holds)
kubectl patch configmap slippy-config -n argo \
  -p '{"data":{"shadow_mode":"true"}}'
```

**Level 3: Disable slippy entirely**
```bash
# Disable at push-hook-parser level
kubectl set env deployment/push-hook-parser SLIPPY_ENABLED=false -n ci

# Or revert workflow templates to base (no slippy patches)
kustomize build workflows/ | kubectl apply -f -
```

**Level 4: Emergency - skip all slippy containers**
```bash
# If slippy containers are causing issues, scale down or patch
# to use a no-op image
kubectl set image workflowtemplate/buildkit \
  slippy-pre=busybox:latest -n argo
```

### 8.8 Success Criteria

| Metric | Target | Measurement |
|--------|--------|-------------|
| Pipeline success rate | ≥99.5% (same as pre-slippy) | Grafana dashboard |
| Additional workflow latency | <30s average | Step duration histogram |
| Hold timeout rate | <5% of holds | Prometheus alert |
| Developer satisfaction | Positive feedback | Survey/Slack |
| Incident count | 0 P1/P2 incidents | Incident tracker |

### 8.9 Communication Plan

| Audience | Channel | Timing | Message |
|----------|---------|--------|---------|
| Platform team | Slack #platform | Daily during rollout | Status updates |
| All developers | Slack #engineering | Stage transitions | What's changing |
| Leadership | Email | Weekly | Progress summary |
| On-call | PagerDuty runbook | Before Stage 2 | Runbook update |

### 8.10 Deliverables

| Deliverable | Description | Owner |
|-------------|-------------|-------|
| Rollout checklist | Per-stage checklist | Platform team |
| Runbook | Operational procedures | Platform team |
| Dashboard | Rollout monitoring | SRE |
| Communication templates | Announcements | PM |
| Rollback scripts | Automated rollback | Platform team |

---

## Summary

### Implementation Order

```
Phase 1 ─► Phase 2 ─► Phase 3 ─► Phase 4 ─► Phase 5 ─► Phase 6 ─► Phase 7 ─► Phase 8
Library    Standalone  ClickHouse  PushParser  Workflows   Testing   Observability  Rollout
```

### Timeline Estimate

| Phase | Duration | Dependencies |
|-------|----------|--------------|
| Phase 1: Library | 2 weeks | None |
| Phase 2: Standalone App | 1 week | Phase 1 |
| Phase 3: ClickHouse | 1 week | Phase 1 |
| Phase 4: Push-Hook-Parser | 1 week | Phase 1, 3 |
| Phase 5: Workflows | 1 week | Phase 2 |
| Phase 6: Testing | 2 weeks | All above |
| Phase 7: Observability | 1 week | Phase 1 |
| Phase 8: Rollout | 5 weeks | All above |

**Total estimated duration**: ~14 weeks from start to full production

### Key Success Factors

1. **Library-first architecture** ensures consistency and maintainability
2. **Shadow mode** allows validation without risk
3. **Incremental rollout** limits blast radius
4. **Comprehensive observability** enables quick issue detection
5. **Clear rollback procedures** reduce risk

