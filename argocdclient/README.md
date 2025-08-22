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
    
    // Or create config manually for testing purposes
    config := &argocdclient.Config{
        ServerUrl: "https://argocd.example.com",
        AuthToken: "your-bearer-token",
        AppName:   "my-application",
        Revision:  "main",
    }
}
```

### Get Application Data

```go
// Get current application data with soft refresh
appData, err := argocdclient.GetArgoApplication("", config)
if err != nil {
    log.Fatal("Failed to get application:", err)
}

fmt.Printf("Application sync status: %v\n", appData["status"])
fmt.Printf("Application health: %v\n", appData["health"])
```

### Get Application Manifests

```go
// Get manifests for the latest revision
manifests, err := argocdclient.GetManifests("", config)
if err != nil {
    log.Fatal("Failed to get manifests:", err)
}

// Get manifests for a specific revision
manifests, err := argocdclient.GetManifests("abc123def456", config)
if err != nil {
    log.Fatal("Failed to get manifests:", err)
}

for i, manifest := range manifests {
    fmt.Printf("Manifest %d:\n%s\n\n", i+1, manifest)
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

#### GetArgoApplication(revision string, config *Config) (map[string]interface{}, error)

Retrieves ArgoCD application data with retry logic.

**Parameters:**
- `revision`: Git revision (currently unused, pass empty string)
- `config`: ArgoCD client configuration

**Returns:**
- `map[string]interface{}`: Application data as parsed JSON
- `error`: Request or parsing error

#### GetManifests(revision string, config *Config) ([]string, error)

Retrieves application manifests from ArgoCD.

**Parameters:**
- `revision`: Git revision to get manifests for (empty string for latest)
- `config`: ArgoCD client configuration

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
    
    "github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient"
)

func main() {
    // Set environment variables (or use your preferred method)
    // Only ServerUrl and AuthToken are required
    os.Setenv("ARGOCD_SERVER", "https://argocd.example.com")
    os.Setenv("ARGOCD_AUTHTOKEN", "your-bearer-token")
    
    // Optional: Set application-specific variables
    os.Setenv("ARGOCD_APP_NAME", "my-app")
    os.Setenv("ARGOCD_REVISION", "main")
    
    // Load configuration
    config, err := argocdclient.LoadConfig()
    if err != nil {
        log.Fatal("Config error:", err)
    }
    
    // Get application data
    fmt.Println("Fetching application data...")
    appData, err := argocdclient.GetArgoApplication("", config)
    if err != nil {
        log.Fatal("Application error:", err)
    }
    
    fmt.Printf("Application: %s\n", appData["metadata"].(map[string]interface{})["name"])
    
    // Get manifests
    fmt.Println("\nFetching manifests...")
    manifests, err := argocdclient.GetManifests("", config)
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

### Test Coverage

The package includes comprehensive tests covering:

- **Success scenarios**: Normal API responses and data parsing
- **Retry logic**: Server errors that trigger exponential backoff retries
- **Error handling**: Client errors, network failures, and invalid responses
- **Configuration management**: Environment variable loading and validation
- **Edge cases**: Invalid JSON responses, request creation errors

Current test coverage is maintained at 80%+ to ensure reliability.

## Changelog

### Recent Updates

- **Configuration Changes**: `ARGOCD_APP_NAME` and `ARGOCD_REVISION` are now optional fields
- **Enhanced Documentation**: Added comprehensive function documentation with Go doc comments
- **Improved Test Coverage**: Expanded test suite to 80%+ coverage including:
  - Additional error scenarios for both `GetArgoApplication` and `GetManifests`
  - Request creation error handling
  - Invalid JSON response handling
  - Client error scenarios (4xx responses)
  - Maximum retry exhaustion scenarios
- **Configuration Flexibility**: LoadConfig now only validates required fields (ServerUrl and AuthToken)

## License

This project is licensed under the terms specified in the repository's LICENSE file.
