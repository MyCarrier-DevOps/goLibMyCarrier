# Vault Package

## Description
The `vault` package is a Go library designed to simplify interactions with HashiCorp Vault. It provides a testable, interface-based design with utilities for securely managing secrets, tokens, and configurations within your Go applications.

## Features
- **Interface-based design** for easy testing and mocking
- **AppRole authentication** support
- **KV v2 secrets engine** integration
- **Azure dynamic credentials** support
- **Configurable authentication strategies**
- **Environment-based configuration** using Viper

## Installation
To install the vault package, use `go get`:
```bash
go get github.com/MyCarrier-DevOps/goLibMyCarrier/vault
```

## Reference
- [HashiCorp Vault Client Go](https://github.com/hashicorp/vault-client-go)
- [Vault Documentation](https://www.vaultproject.io/docs)

## Configuration

The package uses environment variables for configuration:

```bash
export VAULT_ADDRESS="https://vault.example.com:8200"
export VAULT_ROLE_ID="your-role-id"
export VAULT_SECRET_ID="your-secret-id"
```

## Usage

### Basic Usage (Recommended)

```go
package main

import (
    "context"
    "log"
    
    "github.com/MyCarrier-DevOps/goLibMyCarrier/vault"
)

func main() {
    ctx := context.Background()
    
    // Load configuration from environment variables
    config, err := vault.VaultLoadConfig()
    if err != nil {
        log.Fatalf("Error loading vault config: %v", err)
    }
    
    // Create vault client with AppRole authentication
    vaultClient, err := vault.CreateVaultClient(ctx, config)
    if err != nil {
        log.Fatalf("Error creating vault client: %v", err)
    }
    
    // Read KV secret
    secretData, err := vaultClient.GetKVSecret(ctx, "myapp/config", "secret")
    if err != nil {
        log.Fatalf("Error reading secret: %v", err)
    }
    
    log.Printf("Secret read successfully: %+v", secretData)
    
    // Get Azure dynamic credentials
    azureCreds, err := vaultClient.GetAzureDynamicCredentials(ctx, "my-azure-role")
    if err != nil {
        log.Fatalf("Error getting Azure credentials: %v", err)
    }
    
    log.Printf("Azure credentials: %+v", azureCreds)
}
```

### Advanced Usage with Custom Configuration

```go
package main

import (
    "context"
    "log"
    
    "github.com/MyCarrier-DevOps/goLibMyCarrier/vault"
)

func main() {
    ctx := context.Background()
    
    // Create custom configuration
    config := &vault.VaultConfig{
        VaultAddress: "https://vault.example.com:8200",
        Credentials: vault.Credentials{
            RoleID:   "your-role-id",
            SecretID: "your-secret-id",
        },
    }
    
    // Use custom authenticator
    authenticator := &vault.AppRoleAuthenticator{}
    
    // Create vault client with dependency injection
    vaultClient, err := vault.NewVaultClient(ctx, config, authenticator)
    if err != nil {
        log.Fatalf("Error creating vault client: %v", err)
    }
    
    // Use the client...
}
```

### Testing with Mocks

```go
package main

import (
    "context"
    "testing"
    
    "github.com/MyCarrier-DevOps/goLibMyCarrier/vault"
)

// Mock implementation for testing
type MockVaultClient struct {
    secrets map[string]interface{}
}

func (m *MockVaultClient) GetKVSecret(ctx context.Context, path string, mount string) (map[string]interface{}, error) {
    return m.secrets, nil
}

func (m *MockVaultClient) GetAzureDynamicCredentials(ctx context.Context, azureRole string) (map[string]interface{}, error) {
    return map[string]interface{}{
        "client_id":     "test-client-id",
        "client_secret": "test-client-secret",
    }, nil
}

func (m *MockVaultClient) SetToken(token string) error {
    return nil
}

func TestMyFunction(t *testing.T) {
    mockClient := &MockVaultClient{
        secrets: map[string]interface{}{
            "username": "testuser",
            "password": "testpass",
        },
    }
    
    // Use mockClient in your tests...
    ctx := context.Background()
    secrets, err := mockClient.GetKVSecret(ctx, "test/path", "secret")
    if err != nil {
        t.Errorf("Unexpected error: %v", err)
    }
    
    if secrets["username"] != "testuser" {
        t.Errorf("Expected username 'testuser', got %v", secrets["username"])
    }
}
```

## Interfaces

### VaultClientInterface
```go
type VaultClientInterface interface {
    GetKVSecret(ctx context.Context, path string, mount string) (map[string]interface{}, error)
    GetAzureDynamicCredentials(ctx context.Context, azureRole string) (map[string]interface{}, error)
    SetToken(token string) error
}
```

### ConfigLoader
```go
type ConfigLoader interface {
    LoadConfig() (*VaultConfig, error)
}
```

### VaultAuthenticator
```go
type VaultAuthenticator interface {
    Authenticate(ctx context.Context, client *vault.Client, config *VaultConfig) error
}
```

## Backward Compatibility

The package maintains backward compatibility with the old API:

```go
// Legacy usage (deprecated)
vaultClient, err := vault.LegacyVaultClient(ctx, config)
if err != nil {
    log.Fatalf("Error: %v", err)
}

secretData, err := vault.GetKVSecret(ctx, vaultClient, "path", "mount")
```

## Error Handling

All functions return detailed errors with context:

```go
vaultClient, err := vault.CreateVaultClient(ctx, config)
if err != nil {
    // Handle specific error types
    log.Printf("Vault client creation failed: %v", err)
    return
}
```

## Environment Variables

| Variable | Description | Required |
|----------|-------------|----------|
| `VAULT_ADDRESS` | Vault server address | Yes |
| `VAULT_ROLE_ID` | AppRole Role ID | No* |
| `VAULT_SECRET_ID` | AppRole Secret ID | No* |

*Required for AppRole authentication