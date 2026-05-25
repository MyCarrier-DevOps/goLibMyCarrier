[![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient) [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient)
# ArgoCD Client

A Go client library for interacting with the ArgoCD API. This package provides functionality to retrieve ArgoCD application data and manifests with built-in retry logic and error handling.

## Features

- **Retry Logic**: Built-in exponential backoff retry strategy for network failures and server errors (5xx)
- **Error Handling**: Smart error handling that avoids retries for client errors (4xx) 
- **Configuration Management**: Environment variable-based configuration with validation
- **Application Data**: Retrieve ArgoCD application information with soft refresh
- **Manifest Retrieval**: Get application manifests for specific revisions

## Installation

```bash
go get github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient
```

## Configuration

The client uses environment variables for configuration:

| Environment Variable | Description | Required |
|---------------------|-------------|----------|
| `ARGOCD_SERVER` | ArgoCD server URL (e.g., `https://argocd.example.com`) | Yes |
| `ARGOCD_AUTHTOKEN` | Bearer token for authentication | Yes |
| `ARGOCD_APP_NAME` | ArgoCD application name | No |
| `ARGOCD_REVISION` | Git revision/commit hash | No |

## Usage

### Basic Setup

```go
package main

import (
    "fmt"
    "log"
    "github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient"
)

func main() {
    // Load configuration from environment variables
    config, err := argocdclient.LoadConfig()
    if err != nil {
        log.Fatal("Failed to load config:", err)
    }

    // Create a new client
    client := argocdclient.NewClient(config)

    // Or create config manually for testing purposes
    manualConfig := &argocdclient.Config{
        ServerUrl: "https://argocd.example.com",
        AuthToken: "your-bearer-token",
    }
    manualClient := argocdclient.NewClient(manualConfig)
}
```

### Get Application Data

```go
// Get current application data with soft refresh
appData, err := client.GetApplication("my-application")
if err != nil {
    log.Fatal("Failed to get application:", err)
}

fmt.Printf("Application sync status: %v\n", appData["status"])
fmt.Printf("Application health: %v\n", appData["health"])
```

### Get Application Manifests

```go
// Get manifests for the latest revision
manifests, err := client.GetManifests("", "my-application")
if err != nil {
    log.Fatal("Failed to get manifests:", err)
}

// Get manifests for a specific revision
manifests, err := client.GetManifests("abc123def456", "my-application")
if err != nil {
    log.Fatal("Failed to get manifests:", err)
}

for i, manifest := range manifests {
    fmt.Printf("Manifest %d:\n%s\n\n", i+1, manifest)
}
```

## Usage with Interfaces

### ApplicationService Example

```go
package main

import (
    "fmt"
    "log"
    "github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient"
)

func main() {
    config := &argocdclient.Config{
        ServerUrl: "https://argocd.example.com",
        AuthToken: "your-bearer-token",
        AppName:   "my-application",
    }
    var appSvc argocdclient.ApplicationService = argocdclient.NewClient()
    appData, err := appSvc.GetApplication(config)
    if err != nil {
        log.Fatal("Failed to get application:", err)
    }
    fmt.Printf("Application sync status: %v\n", appData["status"])
}
```

### ManifestService Example

```go
package main

import (
    "fmt"
    "log"
    "github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient"
)

func main() {
    config := &argocdclient.Config{
        ServerUrl: "https://argocd.example.com",
        AuthToken: "your-bearer-token",
        AppName:   "my-application",
    }
    var manifestSvc argocdclient.ManifestService = argocdclient.NewClient()
    manifests, err := manifestSvc.GetManifests("", config)
    if err != nil {
        log.Fatal("Failed to get manifests:", err)
    }
    fmt.Printf("Manifests: %v\n", manifests)
}
```

## API Reference

### Types

#### Config

```go
type Config struct {
    ServerUrl string // ArgoCD server URL (required)
    AuthToken string // Bearer token for authentication (required)
    AppName   string // ArgoCD application name (optional)
    Revision  string // Git revision/commit hash (optional)
}
```

### Functions

#### LoadConfig() (*Config, error)

Loads configuration from environment variables and validates required fields (ServerUrl and AuthToken).

**Returns:**
- `*Config`: Populated configuration struct
- `error`: Validation or binding error

#### LoadConfigFromViper(vp *viper.Viper) (*Config, error)

Loads configuration from a caller-provided viper instance — useful for tests,
secret-manager-backed values, or applications that already manage configuration
through a shared viper:

```go
v := viper.New()
v.Set("server_url", "https://argocd.example.com")
v.Set("auth_token", tokenFromVault) // no env mutation needed

config, err := argocdclient.LoadConfigFromViper(v)
```

The function does NOT call `BindEnv` / `AutomaticEnv` on the passed-in viper —
the caller owns env binding. Returns `ErrNilViper` if `vp` is nil. For the
default env-binding behaviour, use `LoadConfig()`.

#### (c *Client) GetApplication(argoAppName string) (map[string]interface{}, error)

Retrieves ArgoCD application data for the specified application name using the configured client.

**Parameters:**
- `argoAppName`: Name of the ArgoCD application

**Returns:**
- `map[string]interface{}`: Application data as parsed JSON
- `error`: Request or parsing error

#### (c *Client) GetManifests(revision, argoAppName string) ([]string, error)

Retrieves application manifests from ArgoCD for the specified application and revision using the configured client.

**Parameters:**
- `revision`: Git revision to get manifests for (empty string for latest)
- `argoAppName`: Name of the ArgoCD application

**Returns:**
- `[]string`: Array of manifest YAML strings
- `error`: Request or parsing error

#### Context-aware variants

Each of the above methods has a `*WithContext` variant that accepts a
`context.Context` as the first argument. See the [Context Support](#context-support)
section above for details and the recommended usage pattern.

- `(c *Client) GetApplicationWithContext(ctx context.Context, argoAppName string) (map[string]interface{}, error)`
- `(c *Client) GetManifestsWithContext(ctx context.Context, revision, argoAppName string) ([]string, error)`
- `(c *Client) GetArgoApplicationResourceTreeWithContext(ctx context.Context, argoAppName string) (map[string]interface{}, error)`

## Instance Routing

MyCarrier runs three ArgoCD control planes (DEV / MGMT / PROD). The
`RouteInstance(appName)` helper classifies an ArgoCD application name to
the correct control plane so the caller can pick the matching
`(server, token)` pair from environment-scoped configuration.

### Routing rules (priority order)

| # | Pattern in `appName` | Instance | Use case |
|---|---|---|---|
| 1 | contains `-offload-` | `InstanceDev` | Legacy feature offload (any env shape) |
| 2 | prefix `development-dev-` or `development-preprod-` | `InstanceMgmt` | Legacy dev / preprod |
| 3 | prefix `production-csp-` | `InstanceMgmt` | Legacy prod cluster selector |
| 4 | suffix `-prod` (NOT `-preprod`) | `InstanceProd` | New `mc-environment` scheme prod |
| 5 | _otherwise_ (default) | `InstanceDev` | New `mc-environment` scheme dev / preprod / feature |

Empty `appName` returns `InstanceDev`. The `-preprod` carve-out for rule
4 prevents preprod app names from mis-routing to PROD even though they
match `-prod` as a substring.

The `Instance.String()` method returns the canonical short label
(`DEV` / `MGMT` / `PROD`), or `UNKNOWN` for any unmapped value so
operators reading logs aren't misled when an unmapped int slips
through. These strings are used as env-var suffixes
(e.g. `ARGOCD_SERVER_DEV`) and observability tags.

**API shape note.** `Names.ArgoCDAppName` is a *method* on `Names`
because its input is the canonical service form — binding it to the
struct enforces the precondition that the caller has already routed the
raw repo identifier through `FromRaw`. `RouteInstance`, by contrast, is
a *free function* because its input is an arbitrary ArgoCD application
name arriving from heterogeneous sources (ArgoCD webhooks, GitOps
events, CLI tools) that are not bound to a canonical struct. Keeping
routing free-function avoids forcing every caller to construct a
`Names` they don't otherwise need.

### Worked examples

| `appName` | `RouteInstance` | Reason |
|---|---|---|
| `mycarrier-frontend-offload-feature20` | `InstanceDev` | rule 1 (offload) |
| `development-dev-mycarrier-frontend` | `InstanceMgmt` | rule 2 |
| `development-preprod-mycarrier-frontend` | `InstanceMgmt` | rule 2 |
| `production-csp-prod-mycarrier-frontend` | `InstanceMgmt` | rule 3 |
| `mycarrier-frontend-prod` | `InstanceProd` | rule 4 |
| `mycarrier-frontend-preprod` | `InstanceDev` | rule 4 carve-out → default |
| `mycarrier-frontend-dev` | `InstanceDev` | default |
| `mycarrier-frontend-feature20` | `InstanceDev` | default (new scheme feature) |
| `""` | `InstanceDev` | empty fallback |

```go
appName := "mycarrier-frontend-prod"
instance := argocdclient.RouteInstance(appName)
fmt.Println(instance)        // InstanceProd
fmt.Println(instance.String()) // "PROD"
// Caller then reads ARGOCD_SERVER_PROD / ARGOCD_AUTHTOKEN_PROD.
```

## Context Support

All public HTTP methods on `Client` have context-aware variants suffixed with
`WithContext`. The ctx-aware variants honor `ctx` cancellation and deadline so
callers can bound HTTP latency (including connect, TLS handshake, and read)
by their own timeouts — important when an upstream operation like
`WaitForSyncStart` advertises ctx-cancellation honoring and must not be
outlived by a hung TCP connection.

| Existing (no-ctx)                       | Context-aware variant (preferred in new code)          |
|----------------------------------------|--------------------------------------------------------|
| `GetApplication(name)`                 | `GetApplicationWithContext(ctx, name)`                 |
| `GetManifests(rev, name)`              | `GetManifestsWithContext(ctx, rev, name)`              |
| `GetArgoApplicationResourceTree(name)` | `GetArgoApplicationResourceTreeWithContext(ctx, name)` |

The no-ctx variants are retained for backward compatibility and delegate
internally to the ctx-aware variants with `context.Background()` — behavior
for existing callers is unchanged.

```go
ctx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
defer cancel()

appData, err := client.GetApplicationWithContext(ctx, "my-application")
if err != nil {
    // errors.Is(err, context.DeadlineExceeded) // deadline hit
    // errors.Is(err, context.Canceled)         // parent cancelled
    log.Fatal(err)
}
```

Note on retry interaction: the underlying `retryablehttp.Client` uses
`DefaultRetryPolicy`, which does **not** retry requests that fail with
`context.Canceled` or `context.DeadlineExceeded`. A cancelled or expired ctx
short-circuits retries and returns promptly.

## Retry Strategy

Both `GetArgoApplication` and `GetManifests` implement the same retry strategy:

- **Maximum Retries**: 3 attempts
- **Backoff Strategy**: Exponential backoff with delays of 1s, 2s, 4s
- **Retry Conditions**: Network errors and HTTP 5xx server errors
- **No Retry Conditions**: HTTP 4xx client errors (authentication, authorization, etc.)

## Error Handling

The client distinguishes between different types of errors:

- **Network Errors**: Retried with exponential backoff
- **Server Errors (5xx)**: Retried with exponential backoff  
- **Client Errors (4xx)**: Returned immediately without retry
- **Parsing Errors**: Returned immediately without retry

## Examples

### Complete Example

```go
package main

import (
    "fmt"
    "log"
    "os"
    argo "github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient"
)

func main() {
    // Set environment variables (or use your preferred method)
    os.Setenv("ARGOCD_SERVER", "https://argocd.example.com")
    os.Setenv("ARGOCD_AUTHTOKEN", "your-bearer-token")

    // Load configuration
    config, err := argo.LoadConfig()
    if err != nil {
        log.Fatal("Config error:", err)
    }

    // Create a new client
    c := argo.NewClient(config)

    // Get application data
    fmt.Println("Fetching application data...")
    appData, err := c.GetApplication("my-app")
    if err != nil {
        log.Fatal("Application error:", err)
    }

    fmt.Printf("Application: %s\n", appData["metadata"].(map[string]interface{})["name"])

    // Get manifests
    fmt.Println("\nFetching manifests...")
    manifests, err := c.GetManifests("cbbb3fb9c683ae8b75bb182482a59", "my-app")
    if err != nil {
        log.Fatal("Manifests error:", err)
    }

    fmt.Printf("Retrieved %d manifests\n", len(manifests))
}
```

## Testing

Run the tests with:

```bash
cd argocdclient
go test -v
```

Run tests with coverage:

```bash
go test -v -cover
```

Generate coverage report:

```bash
go test -coverprofile=coverage.out
go tool cover -html=coverage.out -o coverage.html
```

## License

This project is licensed under the terms specified in the repository's LICENSE file.
