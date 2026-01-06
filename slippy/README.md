# Slippy - Routing Slip Library for CI/CD Pipeline Orchestration

Slippy is a Go library that provides **routing slip** functionality for CI/CD pipeline orchestration. It tracks the state of pipeline executions across multiple stages, components, and steps, enabling intelligent hold/proceed decisions based on prerequisite completion status.

## Table of Contents

- [Overview](#overview)
- [Execution Model](#execution-model)
- [Core Concepts](#core-concepts)
- [Installation](#installation)
- [Configuration](#configuration)
- [Pipeline Configuration](#pipeline-configuration)
- [Quick Start](#quick-start)
- [Pipeline Stages](#pipeline-stages)
- [Usage Patterns](#usage-patterns)
- [Slip Resolution Strategies](#slip-resolution-strategies)
- [Prerequisite Checking and Holds](#prerequisite-checking-and-holds)
- [Error Handling](#error-handling)
- [Testing](#testing)
- [Database Schema](#database-schema)

---

## Overview

Slippy implements the **Routing Slip** pattern for distributed pipeline orchestration. A routing slip is a persistent document that travels with a pipeline execution, recording:

- The overall pipeline state
- Individual component build/test statuses
- Step completion across all pipeline stages
- Complete audit history of all state transitions

### Key Features

- **ClickHouse-backed persistence** for high-performance queries and analytics
- **Context-based slip resolution** - finds the correct slip using commit SHA, ancestry, or image tags
- **Pre-job/Post-job execution model** - bookend operations around existing jobs
- **Prerequisite-based holds** - intelligent waiting for dependent steps
- **Commit ancestry resolution** - finds slips across rebases and merges
- **Component-level tracking** - per-component build and test status
- **Shadow mode** - gradual rollout without blocking pipelines
- **Full audit trail** - complete history of all state changes

---

## Execution Model

### Pre-Job / Post-Job Bookends

**Slippy does NOT wrap job execution.** Instead, it operates as **bookends** around existing CI/CD jobs:

```
┌──────────────────────────────────────────────────────────────────────────┐
│                          CI/CD Job Execution                              │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                           │
│   ┌──────────────┐       ┌──────────────────┐       ┌──────────────┐     │
│   │   PRE-JOB    │       │   ACTUAL JOB     │       │   POST-JOB   │     │
│   │   (Slippy)   │──────▶│   (Your Code)    │──────▶│   (Slippy)   │     │
│   └──────────────┘       └──────────────────┘       └──────────────┘     │
│          │                        │                        │              │
│          │   correlation_id       │   correlation_id       │              │
│          └───────────────────────▶│───────────────────────▶│              │
│                                                                           │
│   • Resolve slip          • Build/Test/Deploy      • Update status       │
│   • Check prereqs         • Any pipeline work      • Record outcome      │
│   • Hold if needed        • Has correlation_id     • Has correlation_id  │
│   • Output correlation_id                                                 │
│                                                                           │
└──────────────────────────────────────────────────────────────────────────┘
```

Each stage involves **two separate slippy executions**:

1. **Pre-Job Execution**: Resolves the slip from context, checks prerequisites, outputs correlation ID
2. **Actual Job**: Receives correlation ID from pre-job, performs work, passes it to post-job
3. **Post-Job Execution**: Uses correlation ID directly to update step status

### Correlation ID Flow

**Only the pre-job needs to resolve the slip.** Once resolved, the correlation ID can be passed forward:

| Phase | Has Correlation ID? | How to Get It |
|-------|---------------------|---------------|
| Pre-Job | ❌ No | Resolve from commit SHA, ancestry, or image tag |
| Actual Job | ✅ Yes | Received from pre-job (env var, file, output) |
| Post-Job | ✅ Yes | Received from job (env var, file, input) |

```go
// ❌ WRONG (in pre-job) - You don't have correlation ID yet
slip, err := client.Load(ctx, correlationID) // Where would this come from?

// ✅ CORRECT (in pre-job) - Resolve from available context
result, err := client.ResolveSlip(ctx, slippy.ResolveOptions{
    Repository: os.Getenv("GITHUB_REPOSITORY"),
    Ref:        os.Getenv("GITHUB_SHA"),
})
correlationID := result.Slip.CorrelationID  // Now you have it - pass it forward!

// ✅ CORRECT (in post-job) - Use correlation ID from pre-job
correlationID := os.Getenv("SLIPPY_CORRELATION_ID")  // Set by pre-job
slip, err := client.Load(ctx, correlationID)         // Direct lookup, no resolution needed
```

### Typical Job Integration

```go
// PRE-JOB: Resolve slip and output correlation ID
func preJob(ctx context.Context, client *slippy.Client) (string, error) {
    // Resolve slip from CI environment context
    result, err := client.ResolveSlip(ctx, slippy.ResolveOptions{
        Repository:    os.Getenv("GITHUB_REPOSITORY"),
        Ref:           os.Getenv("GITHUB_SHA"),
        AncestryDepth: 20,
    })
    if err != nil {
        return "", fmt.Errorf("failed to resolve slip: %w", err)
    }
    
    correlationID := result.Slip.CorrelationID
    
    // Check prerequisites - may hold/wait
    err = client.WaitForPrerequisites(ctx, slippy.HoldOptions{
        CorrelationID: correlationID,
        Prerequisites: []string{"builds_completed"},
        StepName:      "dev_deploy",
    })
    if err != nil {
        return "", err
    }
    
    // Mark step as running
    client.StartStep(ctx, correlationID, "dev_deploy", "")
    
    // Return correlation ID to be passed to job and post-job
    return correlationID, nil
}

// POST-JOB: Use correlation ID directly (no resolution needed)
func postJob(ctx context.Context, client *slippy.Client, correlationID string, jobSuccess bool) error {
    // correlationID was passed from pre-job through the actual job
    // No need to resolve - just use it directly
    
    if jobSuccess {
        return client.CompleteStep(ctx, correlationID, "dev_deploy", "")
    }
    return client.FailStep(ctx, correlationID, "dev_deploy", "", "job failed")
}
```

### Passing Correlation ID Between Phases

How you pass the correlation ID depends on your CI/CD system:

**GitHub Actions:**
```yaml
jobs:
  deploy:
    steps:
      - name: Pre-Job (Slippy)
        id: prejob
        run: |
          CORRELATION_ID=$(slippy pre-job --repo $GITHUB_REPOSITORY --sha $GITHUB_SHA)
          echo "correlation_id=$CORRELATION_ID" >> $GITHUB_OUTPUT
      
      - name: Actual Deploy
        env:
          SLIPPY_CORRELATION_ID: ${{ steps.prejob.outputs.correlation_id }}
        run: ./deploy.sh
      
      - name: Post-Job (Slippy)
        if: always()
        run: slippy post-job --correlation-id ${{ steps.prejob.outputs.correlation_id }} --success ${{ job.status == 'success' }}
```

**Argo Workflows:**
```yaml
templates:
  - name: deploy-with-slippy
    steps:
      - - name: pre-job
          template: slippy-pre
      - - name: deploy
          template: actual-deploy
          arguments:
            parameters:
              - name: correlation_id
                value: "{{steps.pre-job.outputs.parameters.correlation_id}}"
      - - name: post-job
          template: slippy-post
          arguments:
            parameters:
              - name: correlation_id
                value: "{{steps.pre-job.outputs.parameters.correlation_id}}"
```

---

## Core Concepts

### Slip Resolution (Finding the Right Slip)

Since slippy executes as separate pre-job and post-job processes, it must **resolve** which slip to work with based on available context. This is the most important concept to understand.

**Resolution methods** (in order of preference):

1. **Commit Ancestry** - Walk git history via GitHub API to find the first commit with an existing slip
2. **Direct Commit Lookup** - Find slip by exact repository + commit SHA
3. **Image Tag Parsing** - Extract commit SHA from container image tags (fallback for deploys)

```go
// Primary method: ResolveSlip handles all resolution strategies
result, err := client.ResolveSlip(ctx, slippy.ResolveOptions{
    Repository:    "myorg/myrepo",
    Ref:           "abc123def456",     // Commit SHA or git ref
    ImageTag:      "myorg/api:abc123", // Fallback for deploy stages
    AncestryDepth: 20,
})

// result.Slip - The resolved routing slip
// result.ResolvedBy - "ancestry", "commit_sha", or "image_tag"  
// result.MatchedCommit - The commit that matched
correlationID := result.Slip.CorrelationID
```

### Correlation ID

The `CorrelationID` is THE unique identifier for a routing slip. This ID:
- Persists through the entire slip lifecycle
- Links to Kafka events, workflows, and logging systems
- Is consistent with MyCarrier's organization-wide job identification pattern
- **Is NOT available at job runtime** - must be obtained via slip resolution

```go
// After resolving a slip, you have the correlation ID
result, _ := client.ResolveSlip(ctx, opts)
correlationID := result.Slip.CorrelationID

// Now you can use it for step updates
client.CompleteStep(ctx, correlationID, "dev_deploy", "")
```

### Slip

A `Slip` represents the complete state of a pipeline execution:

```go
type Slip struct {
    CorrelationID string                           // Unique identifier
    Repository    string                           // Full repo name (owner/repo)
    Branch        string                           // Git branch
    CommitSHA     string                           // Full git commit SHA
    Status        SlipStatus                       // Overall status
    Steps         map[string]Step                  // Pipeline step states
    Aggregates    map[string][]ComponentStepData   // Per-component data for aggregate steps
    StateHistory  []StateHistoryEntry              // Audit trail
    CreatedAt     time.Time
    UpdatedAt     time.Time
}
```

### Aggregates and Component Tracking

Aggregate steps (like `builds_completed`) track per-component progress. The `Aggregates` map holds component-level data:

```go
type ComponentStepData struct {
    Component      string     // Unique identifier (e.g., "api", "worker")
    DockerfilePath string     // Path to Dockerfile (for builds)
    Status         StepStatus // Current status for this component's step
    StartedAt      *time.Time // When this component's step began
    CompletedAt    *time.Time // When this component's step finished
    Actor          string     // System/user that performed this step
    Error          string     // Error details if failed
    ImageTag       string     // Container image tag (for build steps)
}
```

When all components in an aggregate complete, the parent step (e.g., `builds_completed`) is automatically updated.

### Steps

Steps track individual pipeline stages. Each step has:

```go
type Step struct {
    Status      StepStatus  // pending, running, completed, failed, etc.
    StartedAt   *time.Time  // When execution started
    CompletedAt *time.Time  // When execution finished
    Actor       string      // System/user that performed the step
    Error       string      // Error details if failed
}
```

---

## Installation

```bash
go get github.com/MyCarrier-DevOps/goLibMyCarrier/slippy
```

---

## Configuration

### Environment Variables

Slippy can be configured via environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `CLICKHOUSE_HOSTNAME` | ClickHouse server hostname | required |
| `CLICKHOUSE_PORT` | ClickHouse server port | `9000` |
| `CLICKHOUSE_USERNAME` | ClickHouse username | required |
| `CLICKHOUSE_PASSWORD` | ClickHouse password | required |
| `CLICKHOUSE_DATABASE` | ClickHouse database | required |
| `CLICKHOUSE_SKIP_VERIFY` | Skip TLS verification | `false` |
| `SLIPPY_PIPELINE_CONFIG` | Pipeline configuration (file path or raw JSON) | required |
| `SLIPPY_DATABASE` | ClickHouse database name for slippy tables | `ci` |
| `SLIPPY_GITHUB_APP_ID` | GitHub App ID | required |
| `SLIPPY_GITHUB_APP_PRIVATE_KEY` | GitHub App private key (PEM content or path) | required |
| `SLIPPY_GITHUB_ENTERPRISE_URL` | GitHub Enterprise base URL | empty (uses github.com) |
| `SLIPPY_HOLD_TIMEOUT` | Max wait time for prerequisites | `60m` |
| `SLIPPY_POLL_INTERVAL` | Interval between prerequisite checks | `60s` |
| `SLIPPY_SHADOW_MODE` | Enable shadow mode (no blocking) | `false` |
| `SLIPPY_ANCESTRY_DEPTH` | Commits to check for resolution | `20` |

### Programmatic Configuration

```go
import (
    "github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse"
    "github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

// Load ClickHouse config from environment
chConfig, err := clickhouse.ClickhouseLoadConfig()
if err != nil {
    log.Fatal(err)
}

// Load pipeline configuration (from file or environment)
pipelineConfig, err := slippy.LoadPipelineConfig()
if err != nil {
    log.Fatal(err)
}

// Create slippy config
config := slippy.Config{
    ClickHouseConfig:    chConfig,
    PipelineConfig:      pipelineConfig,
    GitHubAppID:         123456,
    GitHubPrivateKey:    "/path/to/private-key.pem",
    GitHubEnterpriseURL: "https://github.mycompany.com", // optional
    HoldTimeout:         30 * time.Minute,
    PollInterval:        30 * time.Second,
    AncestryDepth:       20,
    ShadowMode:          false,
    Database:            "ci",
}

// Create client
client, err := slippy.NewClient(config)
if err != nil {
    log.Fatal(err)
}
defer client.Close()
```

---

## Pipeline Configuration

Slippy uses a JSON configuration to define the pipeline steps, their prerequisites, and aggregation relationships. This makes the library flexible and unopinionated about the specific CI/CD workflow being tracked.

### Configuration Loading

Pipeline configuration can be loaded from:
- A file path (if the environment variable looks like a path)
- Raw JSON content (if the environment variable contains JSON)

```go
// From environment variable SLIPPY_PIPELINE_CONFIG
pipelineConfig, err := slippy.LoadPipelineConfig()

// From a specific file
pipelineConfig, err := slippy.LoadPipelineConfigFromFile("/path/to/pipeline.json")

// From a string (JSON or file path)
pipelineConfig, err := slippy.LoadPipelineConfigFromString(configValue)
```

### Configuration Schema

```json
{
  "version": "1.0",
  "name": "my-pipeline",
  "description": "Description of your CI/CD pipeline",
  "steps": [
    {
      "name": "push_parsed",
      "description": "Initial push event processing",
      "prerequisites": []
    },
    {
      "name": "builds_completed",
      "description": "All component builds finished",
      "prerequisites": ["push_parsed"],
      "aggregates": "build"
    },
    {
      "name": "dev_deploy",
      "description": "Deploy to development",
      "prerequisites": ["builds_completed", "unit_tests_completed"],
      "is_gate": false
    }
  ]
}
```

### Step Configuration Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | ✅ | Unique identifier for the step (used in column names) |
| `description` | string | ❌ | Human-readable description |
| `prerequisites` | string[] | ✅ | Step names that must complete before this step |
| `aggregates` | string | ❌ | Component-level step name this step aggregates |
| `is_gate` | bool | ❌ | If true, this step's status is an implicit prerequisite for subsequent steps |

### Aggregate Steps

Aggregate steps automatically track per-component progress. When you define:

```json
{
  "name": "builds_completed",
  "aggregates": "build"
}
```

Slippy will:
1. Create a JSON column `builds` (pluralized) for per-component tracking
2. Automatically update `builds_completed` when all components finish
3. Mark it completed if all succeed, or failed if any fail

### Gate Steps

Steps marked with `is_gate: true` act as checkpoints. If a gate fails, subsequent steps are automatically blocked.

### Validation Rules

The configuration is validated on load:
- Must have at least one step
- First step must have no prerequisites
- No duplicate step names
- All prerequisites must reference valid steps
- No circular dependencies
- Each aggregate name can only be used once

---

## Quick Start

```go
package main

import (
    "context"
    "log"
    "os"
    
    "github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
)

func main() {
    ctx := context.Background()

    // Load config from environment
    config := slippy.ConfigFromEnv()
    
    // Create client
    client, err := slippy.NewClient(config)
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    // Resolve slip from CI context (NOT by correlation ID)
    result, err := client.ResolveSlip(ctx, slippy.ResolveOptions{
        Repository: os.Getenv("GITHUB_REPOSITORY"),
        Ref:        os.Getenv("GITHUB_SHA"),
    })
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Resolved slip %s (status: %s) via %s", 
        result.Slip.CorrelationID, 
        result.Slip.Status,
        result.ResolvedBy)
}
```

---

## Pipeline Stages

The default MyCarrier pipeline configuration (`default.json`) tracks the following stages. Your pipeline configuration may differ based on your specific workflow needs.

| Stage | Step Name | Description |
|-------|-----------|-------------|
| Push Parsing | `push_parsed` | Initial commit processing |
| Build | `builds_completed` | All component builds done (aggregates `build`) |
| Unit Tests | `unit_tests_completed` | All unit tests passed (aggregates `unit_test`) |
| Secret Scan | `secret_scan_completed` | Security scanning passed |
| Dev Deploy | `dev_deploy` | Deployed to dev environment |
| Dev Tests | `dev_tests` | Dev environment tests passed |
| Pre-Prod Deploy | `preprod_deploy` | Deployed to pre-production |
| Pre-Prod Tests | `preprod_tests` | Pre-production tests passed |
| Prod Release | `prod_release_created` | Production release created |
| Prod Deploy | `prod_deploy` | Deployed to production |
| Prod Tests | `prod_tests` | Production tests passed |
| Alert Gate | `alert_gate` | Alert monitoring passed (gate step) |
| Steady State | `prod_steady_state` | Production is stable |

---

## Usage Patterns

### Pattern 1: Slip Creation (Push Parsing Only)

The push parsing stage is **unique** - it's the only stage where a slip is **created** (not resolved). This happens when Argo Events receives a push notification.

```go
// Create a new slip for the push - ONLY done once per commit
slip, err := client.CreateSlipForPush(ctx, slippy.PushOptions{
    CorrelationID: uuid.New().String(),  // Generated once, used everywhere
    Repository:    "myorg/myrepo",
    Branch:        "main",
    CommitSHA:     "abc123def456789...",
    Components: []slippy.ComponentDefinition{
        {Name: "api", DockerfilePath: "services/api"},
        {Name: "worker", DockerfilePath: "services/worker"},
    },
})

// Mark push parsing as complete
client.CompleteStep(ctx, slip.CorrelationID, "push_parsed", "")
```

### Pattern 2: Simple Step (No Prerequisites)

Use this pattern for build, test, and other steps that don't need to wait for prerequisites.

```go
// PRE-JOB: Resolve slip and mark step as started
func simplePreJob(ctx context.Context, client *slippy.Client, stepName, componentName string) (string, error) {
    result, err := client.ResolveSlip(ctx, slippy.ResolveOptions{
        Repository: os.Getenv("GITHUB_REPOSITORY"),
        Ref:        os.Getenv("GITHUB_SHA"),
    })
    if err != nil {
        return "", fmt.Errorf("failed to resolve slip: %w", err)
    }
    
    correlationID := result.Slip.CorrelationID
    client.StartStep(ctx, correlationID, stepName, componentName)
    
    return correlationID, nil  // Pass to job and post-job
}

// POST-JOB: Update step status (correlation ID passed from pre-job)
func simplePostJob(ctx context.Context, client *slippy.Client, correlationID, stepName, componentName string, success bool) error {
    if success {
        return client.CompleteStep(ctx, correlationID, stepName, componentName)
    }
    return client.FailStep(ctx, correlationID, stepName, componentName, "step failed")
}
```

**Applicable to:** `build`, `unit_test`, `secret_scan`, `dev_tests`, `preprod_tests`, `prod_tests`

### Pattern 3: Gated Step (With Prerequisites)

Use this pattern for deployment steps that must wait for upstream steps to complete.

```go
// PRE-JOB: Resolve slip, check prerequisites, then start step
func gatedPreJob(ctx context.Context, client *slippy.Client, stepName string, prerequisites []string) (string, error) {
    // Resolve slip (use ancestry for later stages that may have rebased)
    result, err := client.ResolveSlip(ctx, slippy.ResolveOptions{
        Repository:    os.Getenv("GITHUB_REPOSITORY"),
        Ref:           os.Getenv("GITHUB_SHA"),  // Or "HEAD" for ancestry walking
        AncestryDepth: 20,
    })
    if err != nil {
        return "", fmt.Errorf("failed to resolve slip: %w", err)
    }
    
    correlationID := result.Slip.CorrelationID
    
    // Wait for prerequisites - may HOLD until satisfied or timeout
    err = client.WaitForPrerequisites(ctx, slippy.HoldOptions{
        CorrelationID: correlationID,
        Prerequisites: prerequisites,
        StepName:      stepName,
        Timeout:       30 * time.Minute,
        PollInterval:  30 * time.Second,
    })
    if err != nil {
        // Handle specific error cases
        if errors.Is(err, slippy.ErrPrerequisiteFailed) {
            client.AbortStep(ctx, correlationID, stepName, "", "prerequisites failed")
        }
        return "", err
    }

    client.StartStep(ctx, correlationID, stepName, "")
    return correlationID, nil
}

// POST-JOB: Same as simple pattern
func gatedPostJob(ctx context.Context, client *slippy.Client, correlationID, stepName string, success bool) error {
    if success {
        return client.CompleteStep(ctx, correlationID, stepName, "")
    }
    return client.FailStep(ctx, correlationID, stepName, "", "step failed")
}
```

**Applicable to:** `dev_deploy`, `preprod_deploy`, `prod_deploy`, `alert_gate`

**Example prerequisite configurations:**
```go
// Dev deploy waits for CI completion
devPrereqs := []string{"builds_completed", "unit_tests_completed", "secret_scan_completed"}

// Pre-prod waits for dev success
preprodPrereqs := []string{"dev_deploy", "dev_tests"}

// Prod waits for pre-prod success
prodPrereqs := []string{"preprod_deploy", "preprod_tests"}
```

### Pattern 4: Resolution with Fallback (Production)

For production deployments where the commit may have been rebased/squashed, use image tag parsing as a fallback.

```go
func prodPreJob(ctx context.Context, client *slippy.Client, imageTag string) (string, error) {
    result, err := client.ResolveSlip(ctx, slippy.ResolveOptions{
        Repository:    os.Getenv("GITHUB_REPOSITORY"),
        Ref:           "HEAD",
        ImageTag:      imageTag,  // Fallback: parse "myorg/api:abc123-1234567890"
        AncestryDepth: 20,
    })
    if err != nil {
        return "", err
    }
    
    // Continue with gated pattern...
    correlationID := result.Slip.CorrelationID
    // ... prerequisite checking and step start
    return correlationID, nil
}
```

---

## Slip Resolution Strategies

When commits are rebased or squashed, the original commit SHA no longer exists. Slippy provides intelligent resolution strategies to handle this.

### Resolution Order

1. **Commit Ancestry** (Primary) - Uses GitHub GraphQL API to walk git history and find the first ancestor with a slip
2. **Direct Commit** - Exact match on repository + commit SHA
3. **Image Tag Parsing** (Fallback) - Extracts commit SHA from container image tags

### When to Use Each Strategy

| Scenario | Resolution Method | Example |
|----------|-------------------|---------|
| Build/Test jobs | Direct commit or ancestry | `Ref: commitSHA` |
| Deploy after rebase | Ancestry walking | `Ref: "HEAD"` |
| Production deploy | Ancestry + image tag fallback | `Ref: "HEAD", ImageTag: imageTag` |
| Direct lookup (rare) | LoadByCommit | When you have exact commit |

### Resolution Examples

```go
// Simple: Direct commit lookup
result, err := client.ResolveSlip(ctx, slippy.ResolveOptions{
    Repository: "myorg/myrepo",
    Ref:        "abc123def456789",  // Exact commit SHA
})

// With ancestry walking (handles rebases)
result, err := client.ResolveSlip(ctx, slippy.ResolveOptions{
    Repository:    "myorg/myrepo",
    Ref:           "HEAD",          // Walk from HEAD
    AncestryDepth: 20,              // Check up to 20 ancestors
})

// With image tag fallback (for production deploys)
result, err := client.ResolveSlip(ctx, slippy.ResolveOptions{
    Repository:    "myorg/myrepo",
    Ref:           "HEAD",
    ImageTag:      "myorg/api:abc123-1234567890",  // Parse commit from tag
    AncestryDepth: 20,
})

// Result contains resolution details
log.Printf("Slip: %s", result.Slip.CorrelationID)
log.Printf("Resolved via: %s", result.ResolvedBy)      // "ancestry", "commit_sha", or "image_tag"
log.Printf("Matched commit: %s", result.MatchedCommit)
```

---

## Prerequisite Checking and Holds

### Quick Check (Non-Blocking)

After resolving a slip, you can check prerequisites without blocking:

```go
result, _ := client.ResolveSlip(ctx, opts)
correlationID := result.Slip.CorrelationID

// Check if prerequisites are met without waiting
met, err := client.AllPrerequisitesMet(ctx, correlationID, 
    []string{"builds_completed", "unit_tests_completed"}, "")

// Check for failures
failed, failedList, err := client.AnyPrerequisiteFailed(ctx, correlationID,
    []string{"builds_completed"}, "")
```

### Blocking Wait (Pre-Job Hold)

In pre-job execution, you typically want to wait for prerequisites:

```go
result, _ := client.ResolveSlip(ctx, opts)
correlationID := result.Slip.CorrelationID

// Wait up to 30 minutes for prerequisites
err := client.WaitForPrerequisites(ctx, slippy.HoldOptions{
    CorrelationID: correlationID,
    Prerequisites: []string{"dev_deploy", "dev_tests"},
    StepName:      "preprod_deploy",
    Timeout:       30 * time.Minute,
    PollInterval:  30 * time.Second,
})
```

### Shadow Mode

For gradual rollout, enable shadow mode to log decisions without blocking:

```go
config := slippy.ConfigFromEnv()
config.ShadowMode = true

client, _ := slippy.NewClient(config)
// Prerequisites will be checked and logged, but never block
```

---

## Error Handling

Slippy provides sentinel errors for common conditions:

```go
import "errors"

// Resolution errors
result, err := client.ResolveSlip(ctx, opts)
if err != nil {
    switch {
    case errors.Is(err, slippy.ErrSlipNotFound):
        // No slip found for this commit/ancestry
    case errors.Is(err, slippy.ErrGitHubAPI):
        // GitHub API error during ancestry resolution
    case errors.Is(err, slippy.ErrStoreConnection):
        // ClickHouse connection issue
    default:
        // Unexpected error
    }
}

// Prerequisite errors (in pre-job)
err = client.WaitForPrerequisites(ctx, opts)
if err != nil {
    switch {
    case errors.Is(err, slippy.ErrHoldTimeout):
        // Timed out waiting for prerequisites
    case errors.Is(err, slippy.ErrPrerequisiteFailed):
        // A prerequisite step failed
    case errors.Is(err, slippy.ErrContextCancelled):
        // Context was cancelled
    }
}
```

### Available Errors

| Error | Description |
|-------|-------------|
| `ErrSlipNotFound` | No slip matches the query |
| `ErrHoldTimeout` | Hold exceeded time limit |
| `ErrPrerequisiteFailed` | A prerequisite step failed |
| `ErrInvalidConfiguration` | Config is incomplete/invalid |
| `ErrInvalidRepository` | Repository format invalid |
| `ErrStoreConnection` | Storage connection error |
| `ErrGitHubAPI` | GitHub API error |
| `ErrNoInstallation` | No GitHub App installation found |
| `ErrContextCancelled` | Operation cancelled via context |

---

## Testing

Slippy provides a `slippytest` package with mocks for unit testing pre-job and post-job logic:

```go
import (
    "testing"
    
    "github.com/MyCarrier-DevOps/goLibMyCarrier/slippy"
    "github.com/MyCarrier-DevOps/goLibMyCarrier/slippy/slippytest"
)

func TestPreJobLogic(t *testing.T) {
    // Create mock dependencies
    store := slippytest.NewMockStore()
    github := slippytest.NewMockGitHubAPI()
    
    // Load pipeline config (or create minimal test config)
    pipelineConfig, _ := slippy.LoadPipelineConfigFromFile("testdata/pipeline.json")
    
    // Create client with mock dependencies
    client := slippy.NewClientWithDependencies(store, github, slippy.Config{
        PipelineConfig: pipelineConfig,
        HoldTimeout:    5 * time.Second,
        PollInterval:   100 * time.Millisecond,
        AncestryDepth:  10,
    })

    // Pre-populate test data (simulating existing slip from push parsing)
    store.AddSlip(&slippy.Slip{
        CorrelationID: "test-123",
        Repository:    "myorg/myrepo",
        CommitSHA:     "abc123def456",
        Status:        slippy.SlipStatusInProgress,
        Steps: map[string]slippy.Step{
            "builds_completed": {Status: slippy.StepStatusCompleted},
        },
    })
    
    // Mock GitHub ancestry response
    github.SetAncestry("myorg", "myrepo", "abc123def456", []string{"abc123def456"})

    // Test resolution (what pre-job does)
    ctx := context.Background()
    result, err := client.ResolveSlip(ctx, slippy.ResolveOptions{
        Repository: "myorg/myrepo",
        Ref:        "abc123def456",
    })
    if err != nil {
        t.Fatal(err)
    }
    
    if result.Slip.CorrelationID != "test-123" {
        t.Errorf("expected correlation ID test-123, got %s", result.Slip.CorrelationID)
    }
}
```

### Error Injection for Pre/Post-Job Testing

```go
// Test resolution failure (slip not found)
store.LoadByCommitError = slippy.ErrSlipNotFound

result, err := client.ResolveSlip(ctx, opts)
if !errors.Is(err, slippy.ErrSlipNotFound) {
    t.Error("expected ErrSlipNotFound")
}

// Test post-job update failure
store.UpdateStepError = errors.New("connection lost")

err = client.CompleteStep(ctx, correlationID, "dev_deploy", "")
if err == nil {
    t.Error("expected error on step update")
}
```

---

## Database Schema

Slippy uses ClickHouse with the following schema design:

### Table: `ci.routing_slips`

Uses `ReplacingMergeTree` engine for efficient updates via INSERT.

| Column | Type | Description |
|--------|------|-------------|
| `correlation_id` | String | Primary key - unique slip identifier |
| `repository` | String | Full repository name (owner/repo) |
| `branch` | String | Git branch name |
| `commit_sha` | String | Full git commit SHA |
| `status` | Enum8 | Overall slip status |
| `components` | JSON | Array of component definitions |
| `{step}_status` | Enum8 | Denormalized status per step |
| `step_timestamps` | String (JSON) | Step timing information |
| `state_history` | String (JSON) | Complete audit trail |
| `created_at` | DateTime64(3) | Creation timestamp |
| `updated_at` | DateTime64(3) | Last update timestamp |

### Migrations

Migrations are managed automatically on client creation. To run migrations manually:

```go
// Load pipeline configuration
config, err := slippy.LoadPipelineConfig()
if err != nil {
    log.Fatal(err)
}

// Run migrations with the pipeline config
result, err := slippy.RunMigrations(ctx, conn, slippy.MigrateOptions{
    Database:       "ci",
    DryRun:         false,
    TargetVersion:  0, // 0 = latest
    PipelineConfig: config,
})
```

---

## Step Status Reference

| Status | Description |
|--------|-------------|
| `pending` | Step has not started |
| `held` | Waiting for prerequisites |
| `running` | Currently executing |
| `completed` | Finished successfully |
| `failed` | Failed during execution |
| `error` | Unexpected error occurred |
| `aborted` | Aborted due to upstream failure |
| `timeout` | Exceeded time limit |
| `skipped` | Intentionally skipped |