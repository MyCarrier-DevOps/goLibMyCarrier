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
