[![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient) [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient)
# ArgoCD Client

A Go client library for interacting with the ArgoCD API. This package retrieves ArgoCD application data and manifests, reports the status of Argo Rollouts grouped by correlation ID, and runs Rollout actions on the ArgoCD instance an application routes to. Reads retry on transient failures and HTTP failures that are not retried surface as a typed error.

## Features

- **Retry Logic**: Built-in exponential backoff retry strategy for network failures and server errors (5xx)
- **Typed Errors**: HTTP failures that are not retried return `*APIError` and match the sentinels `ErrPermissionDenied`, `ErrNotFound` and `ErrConflict` with `errors.Is`; GETs retry transient failures (network errors, 429 and 5xx other than 501)
- **Configuration Management**: Environment variable-based configuration with validation
- **Application Data**: Retrieve ArgoCD application information with soft refresh
- **Manifest Retrieval**: Get application manifests for specific revisions
- **Rollout Status**: List the Argo Rollouts of an Application that carry a correlation ID, with health, phase, step index, canary weight and step plugin statuses
- **Routed Resource Actions**: Run `abort`, `promote-full`, `retry` and `resume` on a Rollout through a `Router` that always uses the token of the instance the application routes to, and refuses when that instance is not configured

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

`NewClient` returns the concrete `*Client`. Consumers that want to substitute a test
double define a small interface with only the methods they call and accept it in their
constructors:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient"
)

// applicationReader is the part of *argocdclient.Client this consumer uses.
type applicationReader interface {
    GetApplicationWithContext(ctx context.Context, argoAppName string) (map[string]interface{}, error)
    GetManifestsWithContext(ctx context.Context, revision, argoAppName string) ([]string, error)
}

func printSyncStatus(ctx context.Context, reader applicationReader, appName string) error {
    appData, err := reader.GetApplicationWithContext(ctx, appName)
    if err != nil {
        return err
    }
    fmt.Printf("Application sync status: %v\n", appData["status"])
    return nil
}

func main() {
    config := &argocdclient.Config{
        ServerUrl: "https://argocd.example.com",
        AuthToken: "your-bearer-token",
    }
    client := argocdclient.NewClient(config)
    if err := printSyncStatus(context.Background(), client, "my-application"); err != nil {
        log.Fatal("Failed to get application:", err)
    }
}
```

## API Reference

### Types

#### Config

```go
type Config struct {
    ServerUrl string // ArgoCD server URL (required)
    AuthToken string // Bearer token for authentication (required)
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

#### Rollout and action API

- `const CorrelationIDLabel = "mycarrier.tech/correlationId"`: the Rollout label that carries the correlation ID
- `type ResourceRef struct{ Group, Version, Kind, Namespace, Name string }`: identifies a live resource in an Application
- `type HealthStatus string`: ArgoCD health code (`HealthStatusHealthy`, `HealthStatusProgressing`, `HealthStatusSuspended`, `HealthStatusDegraded`, `HealthStatusMissing`, `HealthStatusUnknown`)
- `type RolloutPhase string`: Argo Rollouts `status.phase` (`RolloutPhaseHealthy`, `RolloutPhaseProgressing`, `RolloutPhasePaused`, `RolloutPhaseDegraded`)
- `type StepPluginPhase string`: step plugin phase (`StepPluginPhaseRunning`, `StepPluginPhaseSuccessful`, `StepPluginPhaseFailed`, `StepPluginPhaseError`)
- `type StepPluginOperation string`: step plugin operation (`StepPluginOperationRun`, `StepPluginOperationTerminate`, `StepPluginOperationAbort`)
- `type StepPluginStatus struct{ Index int32; Name string; Operation StepPluginOperation; Phase StepPluginPhase; Message string }`: one entry of `status.canary.stepPluginStatuses`
- `type RolloutStatus struct{ ... }`: combined resource-tree and live state of one Rollout (see [Argo Rollouts](#argo-rollouts))
- `(c *Client) ListRolloutGroup(ctx context.Context, appName, correlationID string) ([]RolloutStatus, error)`: Rollouts of an Application labeled with the correlation ID
- `type ResourceAction string`: `ActionAbort`, `ActionPromoteFull`, `ActionRetry`, `ActionResume`
- `type Router struct{ ... }`: one `Client` per configured `Instance`
- `NewRouter(configs map[Instance]*Config) *Router`: builds a `Router`; nil or incomplete configs leave an instance unconfigured
- `(r *Router) ClientFor(appName string) (*Client, error)`: the client of the instance `appName` routes to, or `ErrInstanceNotConfigured`
- `(r *Router) ListRolloutGroup(ctx context.Context, appName, correlationID string) ([]RolloutStatus, error)`: `ClientFor` followed by `Client.ListRolloutGroup`
- `(r *Router) RunResourceAction(ctx context.Context, appName string, ref ResourceRef, action ResourceAction) error`: runs an action on the routed instance
- `type APIError struct{ StatusCode int; Body string }`: returned for an HTTP status of 400 and above that is not retried (see [Error Handling](#error-handling))
- `ErrPermissionDenied`, `ErrNotFound`, `ErrConflict`, `ErrInstanceNotConfigured`: sentinel errors (see [Error Handling](#error-handling))

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

## Argo Rollouts

`ListRolloutGroup(ctx, appName, correlationID)` returns the status of every Argo
Rollout in an Application whose `mycarrier.tech/correlationId` label
(`CorrelationIDLabel`) equals `correlationID`. It is available on `Client` and on
`Router`; `Router.ListRolloutGroup` routes `appName` to its instance and delegates.

The call works in these steps:

1. An empty `appName` or `correlationID` returns an error; unlabeled Rollouts are never matched.
2. It reads the Application's resource tree
   (`GET /api/v1/applications/{app}/resource-tree`) and keeps the nodes with group
   `argoproj.io` and kind `Rollout` that have a `uid`. Tree nodes carry no labels.
   ArgoCD adds a node without a `uid` for a managed Rollout that does not exist in the
   cluster (not yet created, rejected by admission, or deleted out of band).
3. For each Rollout node it reads the live manifest
   (`GET /api/v1/applications/{app}/resource`). A Rollout with no live object (not yet
   created, or deleted since ArgoCD last refreshed the tree) is skipped; any other
   failure to read a live Rollout fails the whole call.
4. It keeps the Rollouts whose label matches and returns them sorted by namespace,
   then name. When none match, the result is an empty, non-nil slice and the error is
   `nil`.

HTTP answers of 400 or above from the resource tree and live reads that are not retried wrap
`*APIError`, so `errors.As` gives the status and body and `errors.Is` reaches
`ErrPermissionDenied`. A missing Application surfaces as `ErrPermissionDenied` (HTTP 403),
because ArgoCD answers 403 for an Application that does not exist when no project is sent. A
live-read error names the Rollout (`<namespace>/<name>`). Retried statuses return the untyped
give-up error described in [Error Handling](#error-handling). Decode errors, transport errors
the retry client does not retry (such as TLS verification failures) and context cancellation
are returned wrapped as-is.

### RolloutStatus

| Field | Source | Description |
|-------|--------|-------------|
| `Ref` | resource tree | Group, version, kind, namespace and name of the Rollout |
| `Health` | resource tree | ArgoCD health code (`health.status`) |
| `Message` | resource tree | ArgoCD health message (`health.message`) |
| `Phase` | live Rollout | `status.phase` |
| `CurrentStepIndex` | live Rollout | `status.currentStepIndex`; `nil` when unset |
| `Aborted` | live Rollout | `status.abort` |
| `CanaryWeight` | live Rollout | Percentage of traffic on the canary, see below |
| `StepPluginStatuses` | live Rollout | `status.canary.stepPluginStatuses` (index, name, operation, phase, message) |

Argo Rollouts keeps one step plugin entry per step and operation: an abort or a full
promotion adds an `Abort` or `Terminate` entry (same index and name) next to the step's
`Run` entry. An empty `Phase` on a `Run` entry means the plugin is globally disabled.

### CanaryWeight

`CanaryWeight` is the traffic share of the canary. For a canary with traffic routing it is the
weight the router reports, as `kubectl argo rollouts` shows it. Without traffic routing it is
the current step's `setWeight` (the desired weight), whereas `kubectl argo rollouts` reports the
ratio of available canary replicas. The precedence:

1. The Rollout is aborted (`status.abort`): `0`.
2. The Rollout has no canary strategy (for example blue-green): `0`.
3. There is no current canary step, which means the canary strategy has no steps or
   `currentStepIndex` is past the last step (fully promoted): the max traffic weight,
   `spec.strategy.canary.trafficRouting.maxTrafficWeight`, default `100`. A traffic
   router such as Istio reports a canary weight of `0` after full promotion, so
   `status.canary.weights` is not used here.
4. A traffic router is configured and `status.canary.weights` reports a canary weight:
   that weight.
5. Otherwise: the last `setWeight` step at or before the current step (an unset
   `currentStepIndex` counts as step `0`), or `0` when there is none.

```go
statuses, err := router.ListRolloutGroup(ctx, "mycarrier-frontend-prod", correlationID)
if err != nil {
    log.Fatal(err)
}
for _, s := range statuses {
    fmt.Printf("%s/%s health=%s phase=%s weight=%d%%\n",
        s.Ref.Namespace, s.Ref.Name, s.Health, s.Phase, s.CanaryWeight)
}
```

## Resource Actions

Rollout actions are reachable only through a `Router`; `Client` has no exported
action method. Because the `Router` picks the client by `RouteInstance(appName)`,
an action is always sent with the token of the instance the application routes to.

The module does not read the per-instance environment variables itself. The caller
loads them into a `map[Instance]*Config`:

```go
configs := map[argocdclient.Instance]*argocdclient.Config{
    argocdclient.InstanceDev: {
        ServerUrl: os.Getenv("ARGOCD_SERVER_DEV"),
        AuthToken: os.Getenv("ARGOCD_AUTHTOKEN_DEV"),
    },
    argocdclient.InstanceMgmt: {
        ServerUrl: os.Getenv("ARGOCD_SERVER_MGMT"),
        AuthToken: os.Getenv("ARGOCD_AUTHTOKEN_MGMT"),
    },
    argocdclient.InstanceProd: {
        ServerUrl: os.Getenv("ARGOCD_SERVER_PROD"),
        AuthToken: os.Getenv("ARGOCD_AUTHTOKEN_PROD"),
    },
}
router := argocdclient.NewRouter(configs)

ref := argocdclient.ResourceRef{
    Group: "argoproj.io", Version: "v1alpha1", Kind: "Rollout",
    Namespace: "default", Name: "my-rollout",
}
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
err := router.RunResourceAction(ctx, "mycarrier-frontend-prod", ref, argocdclient.ActionAbort)
```

`ctx` is the only deadline for an action: the client sets no timeout of its own and never
retries the POST, so pass a `ctx` with a deadline. A deadline that fires after ArgoCD accepted
the action leaves the outcome unknown, so re-read the Rollout with `ListRolloutGroup` before
acting again.

### Fail-closed routing

`NewRouter` leaves an instance unconfigured when its config is `nil` or has an empty
`ServerUrl` or `AuthToken`. `ClientFor`, `ListRolloutGroup` and `RunResourceAction`
return an error wrapping `ErrInstanceNotConfigured` (naming the application and the
instance) for an application that routes to an unconfigured instance. No HTTP call is
made and no other instance is used as a fallback. For example, a `Router` configured
with only `InstanceDev` refuses actions for `mycarrier-frontend-prod`.

### Actions

Only these four actions are accepted, and only on `argoproj.io` Rollouts (`Group` `argoproj.io`,
`Kind` `Rollout`). Any other action or resource kind, such as `restart` on a Deployment, and an
empty application name, are refused with an error before any HTTP call.

| Constant | Action | Effect | Repeat-safe |
|----------|--------|--------|-------------|
| `ActionAbort` | `abort` | Sets `status.abort` on the Rollout | Yes |
| `ActionPromoteFull` | `promote-full` | Skips the remaining canary steps | Yes |
| `ActionRetry` | `retry` | Clears an abort so the Rollout tries again | Yes |
| `ActionResume` | `resume` | Clears the pause conditions | No |

Repeat-safe means a repeat never changes the Rollout, not that it always succeeds. `abort` is not offered once the Rollout is aborted or fully promoted; both cases return `ErrConflict`, so re-read `RolloutStatus.Aborted` to tell them apart. `retry` is not offered once the abort is cleared. `promote-full` is not offered once the Rollout is fully promoted; while a promotion is still in progress a repeat is posted again, harmlessly.

`resume` is not idempotent: a repeated resume can release the next `pause: {}` step.

### Request flow

1. `ref.Name`, `ref.Kind`, `ref.Version` and the action must be non-empty, otherwise an
   error is returned without any HTTP call.
2. The action is checked against ArgoCD's action discovery
   (`GET /api/v1/applications/{name}/resource/actions`). ArgoCD does not enforce an
   action's `disabled` flag when running it, so an action that is absent or disabled
   returns an error wrapping `ErrConflict` and nothing is posted.
3. The action is sent with `POST /api/v1/applications/{name}/resource/actions/v2`. The
   POST is a single attempt and is never retried. A non-2xx answer is returned as
   `*APIError`.

### Required ArgoCD RBAC

The token of each instance needs these policies on the Application:

```
p, <role>, applications, get, <project>/<app>, allow
p, <role>, applications, action/argoproj.io/Rollout/<action>, <project>/<app>, allow
```

where `<action>` is each of `abort`, `promote-full`, `retry` and `resume` that the
caller runs.

## Context Support

The read methods `GetApplication`, `GetManifests` and `GetArgoApplicationResourceTree`
have context-aware variants suffixed with `WithContext`. `ListRolloutGroup` and
`RunResourceAction` take `ctx` as their first parameter and have no no-ctx variant.
The ctx-aware variants honor `ctx` cancellation and deadline so callers can bound HTTP latency (including connect, TLS handshake, and read)
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
short-circuits retries and returns promptly. Action calls take a `ctx` directly and
honor it the same way.

## Retry Strategy

All GET requests (application data, manifests, resource tree, rollout group reads and
action discovery) share one retry strategy:

- **Maximum Retries**: 3 retries, so 4 attempts in total
- **Backoff Strategy**: Exponential backoff with delays of 1s, 2s, 4s between attempts. When ArgoCD answers 429 or 503 with a `Retry-After` header, the retry client waits for that long instead, without capping it at the 4s maximum
- **Retry Conditions**: Network errors, HTTP 429, and HTTP 5xx server errors other than 501
- **No Retry Conditions**: Other HTTP 4xx client errors (authentication, authorization, etc.) and HTTP 501

Action POSTs are never retried: they are sent exactly once, because a repeated `resume`
can release the next `pause: {}` step.

## Error Handling

An HTTP answer with a status of 400 or above that is not retried is returned as
`*APIError`: any status on an action POST, and on GETs every 4xx except 429, plus 501.
A GET still answered with 429, or with a status of 500 or above other than 501, after its
retries returns the retry client's `giving up after N attempt(s)` error instead: an untyped
error with no body whose text does not name the status.

```go
type APIError struct {
    StatusCode int    // HTTP status code
    Body       string // raw response body, typically grpc-gateway JSON
}
```

`Error()` renders `client error <code>: <body>` for 4xx statuses and
`server error <code>: <body>` for 5xx statuses.

Sentinel errors match through `errors.Is`:

| Sentinel | Matches |
|----------|---------|
| `ErrPermissionDenied` | HTTP 403. ArgoCD answers 403, not 404, for an Application that does not exist (this client never sends a project), so a 403 can mean a missing permission, a missing Application, or ArgoCD failing to read the Application; that last one is an ArgoCD-side failure, usually transient, and is not retried |
| `ErrNotFound` | HTTP 404, and HTTP 400 whose body contains `not found as part of application` (a resource that is not in the Application) |
| `ErrConflict` | HTTP 409, or an action the resource does not currently offer (disabled or absent in ArgoCD's action discovery) |
| `ErrInstanceNotConfigured` | An application that routes to an instance without a configured server URL and token |

```go
_, err := client.GetApplicationWithContext(ctx, "my-application")
switch {
case errors.Is(err, argocdclient.ErrNotFound):
    // the Application was deleted while the request was in flight; one already
    // missing is reported as ErrPermissionDenied
case errors.Is(err, argocdclient.ErrPermissionDenied):
    // the token may not read it, the Application does not exist, or ArgoCD failed to read it
}

var apiErr *argocdclient.APIError
if errors.As(err, &apiErr) {
    fmt.Println(apiErr.StatusCode, apiErr.Body)
}
```

Other failures:

- **Network Errors**: Retried with exponential backoff for GET requests
- **Server Errors (5xx other than 501)**: Retried with exponential backoff for GET requests; returned immediately as `*APIError` for action POSTs
- **Too Many Requests (429)**: Retried with exponential backoff for GET requests
- **Client Errors (4xx other than 429)**: Returned immediately without retry
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

Run the tests from the repository root:

```bash
make test PKG=argocdclient
```

Run the linter:

```bash
make lint PKG=argocdclient
```

Check the coverage threshold:

```bash
make check-coverage PKG=argocdclient
```

## License

This project is licensed under the terms specified in the repository's LICENSE file.
