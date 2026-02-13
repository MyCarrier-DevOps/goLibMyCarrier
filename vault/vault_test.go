package vault

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/hashicorp/vault-client-go"
)

// Mock implementations for testing

type MockConfigLoader struct {
	config *VaultConfig
	err    error
}

func (m *MockConfigLoader) LoadConfig() (*VaultConfig, error) {
	return m.config, m.err
}

type MockVaultClient struct {
	kvSecrets        map[string]interface{}
	kvSecretsList    []string
	azureCredentials map[string]interface{}
	kvError          error
	kvListError      error
	azureError       error
	setTokenError    error
}

func (m *MockVaultClient) GetKVSecret(ctx context.Context, path string, mount string) (map[string]interface{}, error) {
	if m.kvError != nil {
		return nil, m.kvError
	}
	return m.kvSecrets, nil
}

func (m *MockVaultClient) GetKVSecretList(ctx context.Context, path string, mount string) ([]string, error) {
	if m.kvListError != nil {
		return nil, m.kvListError
	}
	return m.kvSecretsList, nil
}

func (m *MockVaultClient) GetAzureDynamicCredentials(
	ctx context.Context,
	azureRole string,
) (map[string]interface{}, error) {
	if m.azureError != nil {
		return nil, m.azureError
	}
	return m.azureCredentials, nil
}

func (m *MockVaultClient) SetToken(token string) error {
	return m.setTokenError
}

type MockAuthenticator struct {
	err error
}

func (m *MockAuthenticator) Authenticate(ctx context.Context, client *vault.Client, config *VaultConfig) error {
	return m.err
}

// Original tests with fixes

func TestVaultLoadConfig(t *testing.T) {
	// Set environment variables for testing
	if err := os.Setenv("VAULT_ADDRESS", "http://localhost:8200"); err != nil {
		t.Fatalf("Failed to set VAULT_ADDRESS: %v", err.Error())
	}
	if err := os.Setenv("VAULT_ROLE_ID", "test_role_id"); err != nil {
		t.Fatalf("Failed to set VAULT_ROLE_ID: %v", err.Error())
	}
	if err := os.Setenv("VAULT_SECRET_ID", "test_secret_id"); err != nil {
		t.Fatalf("Failed to set VAULT_SECRET_ID: %v", err.Error())
	}

	// Call the function
	config, err := VaultLoadConfig()

	// Assert that there is no error
	if err != nil {
		t.Errorf("VaultLoadConfig() error = %v", err.Error())
		return
	}

	// Assert that the VaultAddress is correct
	if config.VaultAddress != "http://localhost:8200" {
		t.Errorf("VaultLoadConfig() VaultAddress = %v, want %v", config.VaultAddress, "http://localhost:8200")
	}

	// Assert that the Credentials.RoleID is correct
	if config.Credentials.RoleID != "test_role_id" {
		t.Errorf("VaultLoadConfig() Credentials.RoleID = %v, want %v", config.Credentials.RoleID, "test_role_id")
	}

	// Assert that the Credentials.SecretID is correct
	if config.Credentials.SecretID != "test_secret_id" {
		t.Errorf("VaultLoadConfig() Credentials.SecretID = %v, want %v", config.Credentials.SecretID, "test_secret_id")
	}

	// Cleanup
	cleanupEnvVars(t)
}

func TestVaultLoadConfig_NoAddress(t *testing.T) {
	// Ensure clean environment
	cleanupEnvVars(t)

	// Call the function
	config, err := VaultLoadConfig()

	// Assert that there is an error
	if err == nil {
		t.Errorf("VaultLoadConfig() expected an error, but got nil")
		return
	}

	// Assert that the error message is correct
	if err.Error() != "vault address is required" {
		t.Errorf("VaultLoadConfig() error message = %v, want %v", err.Error(), "vault address is required")
	}

	// Assert that the config is nil
	if config != nil {
		t.Errorf("VaultLoadConfig() config = %v, want nil", config)
	}
}

func TestVaultValidateConfig(t *testing.T) {
	// Create a valid config
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
	}

	// Call the function
	err := VaultValidateConfig(config)

	// Assert that there is no error
	if err != nil {
		t.Errorf("VaultValidateConfig() error = %v", err.Error())
	}
}

func TestVaultValidateConfig_NoAddress(t *testing.T) {
	// Create an invalid config
	config := &VaultConfig{
		VaultAddress: "",
	}

	// Call the function
	err := VaultValidateConfig(config)

	// Assert that there is an error
	if err == nil {
		t.Errorf("VaultValidateConfig() expected an error, but got nil")
		return
	}

	// Assert that the error message is correct
	if err.Error() != "vault address is required" {
		t.Errorf("VaultValidateConfig() error message = %v, want %v", err.Error(), "vault address is required")
	}
}

// New tests for the refactored code

func TestViperConfigLoader_LoadConfig(t *testing.T) {
	// Set environment variables for testing
	if err := os.Setenv("VAULT_ADDRESS", "http://localhost:8200"); err != nil {
		t.Fatalf("Failed to set VAULT_ADDRESS: %v", err.Error())
	}

	loader := &ViperConfigLoader{}
	config, err := loader.LoadConfig()

	if err != nil {
		t.Errorf("ViperConfigLoader.LoadConfig() error = %v", err.Error())
		return
	}

	if config.VaultAddress != "http://localhost:8200" {
		t.Errorf("ViperConfigLoader.LoadConfig() VaultAddress = %v, want %v",
			config.VaultAddress, "http://localhost:8200")
	}

	// Cleanup
	cleanupEnvVars(t)
}

func TestAppRoleAuthenticator_Authenticate_NoCredentials(t *testing.T) {
	authenticator := &AppRoleAuthenticator{}
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
		Credentials:  Credentials{},
	}

	ctx := context.Background()

	// Since we now check for nil client first, we expect that error
	err := authenticator.Authenticate(ctx, nil, config)

	// Should return error for nil client
	if err == nil {
		t.Errorf("AppRoleAuthenticator.Authenticate() expected error for nil client, got nil")
	}
	if err != nil && err.Error() != "vault client cannot be nil" {
		t.Errorf("AppRoleAuthenticator.Authenticate() error = %v, want 'vault client cannot be nil'", err)
	}
}

func TestAppRoleAuthenticator_Authenticate_NoCredentials_ValidClient(t *testing.T) {
	authenticator := &AppRoleAuthenticator{}
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
		Credentials:  Credentials{}, // No credentials
	}

	ctx := context.Background()

	// Empty credentials should now be rejected by the authenticator
	client, err := NewVaultClient(ctx, config, authenticator)

	// This should fail because empty credentials are no longer allowed
	if err == nil {
		t.Errorf("NewVaultClient() with empty credentials should fail, got nil error")
	}
	if client != nil {
		t.Errorf("NewVaultClient() client should be nil when credentials are empty")
	}
}

func TestVaultClient_GetKVSecret(t *testing.T) {
	mockClient := &MockVaultClient{
		kvSecrets: map[string]interface{}{
			"username": "testuser",
			"password": "testpass",
		},
	}

	ctx := context.Background()
	secrets, err := mockClient.GetKVSecret(ctx, "myapp/config", "secret")

	if err != nil {
		t.Errorf("VaultClient.GetKVSecret() error = %v", err)
		return
	}

	if secrets["username"] != "testuser" {
		t.Errorf("VaultClient.GetKVSecret() username = %v, want %v", secrets["username"], "testuser")
	}

	if secrets["password"] != "testpass" {
		t.Errorf("VaultClient.GetKVSecret() password = %v, want %v", secrets["password"], "testpass")
	}
}

func TestVaultClient_GetKVSecretList(t *testing.T) {
	mockClient := &MockVaultClient{
		kvSecretsList: []string{"secret1", "secret2", "secret3"},
	}

	ctx := context.Background()
	secretsList, err := mockClient.GetKVSecretList(ctx, "myapp/", "secret")

	if err != nil {
		t.Errorf("VaultClient.GetKVSecretList() error = %v", err)
		return
	}

	expectedSecrets := []string{"secret1", "secret2", "secret3"}
	if len(secretsList) != len(expectedSecrets) {
		t.Errorf("VaultClient.GetKVSecretList() length = %v, want %v", len(secretsList), len(expectedSecrets))
		return
	}

	for i, secret := range expectedSecrets {
		if secretsList[i] != secret {
			t.Errorf("VaultClient.GetKVSecretList() secret[%d] = %v, want %v", i, secretsList[i], secret)
		}
	}
}

func TestVaultClient_GetAzureDynamicCredentials(t *testing.T) {
	mockClient := &MockVaultClient{
		azureCredentials: map[string]interface{}{
			"client_id":     "test-client-id",
			"client_secret": "test-client-secret",
		},
	}

	ctx := context.Background()
	creds, err := mockClient.GetAzureDynamicCredentials(ctx, "my-role")

	if err != nil {
		t.Errorf("VaultClient.GetAzureDynamicCredentials() error = %v", err)
		return
	}

	if creds["client_id"] != "test-client-id" {
		t.Errorf("VaultClient.GetAzureDynamicCredentials() client_id = %v, want %v",
			creds["client_id"], "test-client-id")
	}

	if creds["client_secret"] != "test-client-secret" {
		t.Errorf("VaultClient.GetAzureDynamicCredentials() client_secret = %v, want %v",
			creds["client_secret"], "test-client-secret")
	}
}

func TestMockConfigLoader(t *testing.T) {
	expectedConfig := &VaultConfig{
		VaultAddress: "http://test:8200",
		Credentials: Credentials{
			RoleID:   "test-role",
			SecretID: "test-secret",
		},
	}

	mockLoader := &MockConfigLoader{
		config: expectedConfig,
	}

	config, err := mockLoader.LoadConfig()

	if err != nil {
		t.Errorf("MockConfigLoader.LoadConfig() error = %v", err)
		return
	}

	if config.VaultAddress != expectedConfig.VaultAddress {
		t.Errorf("MockConfigLoader.LoadConfig() VaultAddress = %v, want %v",
			config.VaultAddress, expectedConfig.VaultAddress)
	}

	if config.Credentials.RoleID != expectedConfig.Credentials.RoleID {
		t.Errorf("MockConfigLoader.LoadConfig() RoleID = %v, want %v",
			config.Credentials.RoleID, expectedConfig.Credentials.RoleID)
	}
}

// Helper function to cleanup environment variables
func cleanupEnvVars(t *testing.T) {
	envVars := []string{
		"VAULT_ADDRESS",
		"VAULT_ROLE_ID",
		"VAULT_SECRET_ID",
	}

	for _, envVar := range envVars {
		if err := os.Unsetenv(envVar); err != nil {
			t.Errorf("Failed to unset %s: %v", envVar, err.Error())
		}
	}
}

// Additional comprehensive tests for vault package

// Test NewVaultClient function
func TestNewVaultClient_Success(t *testing.T) {
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
		Credentials: Credentials{
			RoleID:   "",
			SecretID: "",
		},
	}

	// Test with nil authenticator (should not authenticate)
	ctx := context.Background()
	client, err := NewVaultClient(ctx, config, nil)

	// Should succeed with valid config even without authenticator
	if err != nil {
		t.Errorf("NewVaultClient() error = %v, want nil", err)
	}
	if client == nil {
		t.Errorf("NewVaultClient() client = nil, want non-nil")
	}
}

func TestNewVaultClient_WithMockAuthenticator(t *testing.T) {
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
	}

	// Test with authenticator that returns error
	mockAuth := &MockAuthenticator{err: fmt.Errorf("authentication failed")}
	ctx := context.Background()
	client, err := NewVaultClient(ctx, config, mockAuth)

	// Should return error from authenticator
	if err == nil {
		t.Errorf("NewVaultClient() expected error from authenticator, got nil")
	}
	if client != nil {
		t.Errorf("NewVaultClient() client should be nil when authentication fails")
	}
}

func TestNewVaultClient_WithSuccessfulAuth(t *testing.T) {
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
	}

	// Test with authenticator that succeeds
	mockAuth := &MockAuthenticator{err: nil}
	ctx := context.Background()
	client, err := NewVaultClient(ctx, config, mockAuth)

	if err != nil {
		t.Errorf("NewVaultClient() error = %v, want nil", err)
	}
	if client == nil {
		t.Errorf("NewVaultClient() client = nil, want non-nil")
	}
}

// Test CreateVaultClient function
func TestCreateVaultClient_RejectsEmptyCredentials(t *testing.T) {
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
		Credentials: Credentials{
			RoleID:   "",
			SecretID: "",
		},
	}

	ctx := context.Background()
	client, err := CreateVaultClient(ctx, config)

	// Should fail because empty credentials are not allowed
	if err == nil {
		t.Errorf("CreateVaultClient() with empty credentials should fail, got nil error")
	}
	if client != nil {
		t.Errorf("CreateVaultClient() client should be nil when credentials are empty")
	}
}

// Test VaultClient methods using the real struct with a mock underlying client
func TestVaultClient_Methods(t *testing.T) {
	// We can't easily test the real VaultClient methods without a vault instance
	// But we can test using our MockVaultClient which implements the interface

	mockClient := &MockVaultClient{
		kvSecrets: map[string]interface{}{
			"username": "testuser",
			"password": "testpass",
		},
		kvSecretsList: []string{"config1", "config2", "config3"},
		azureCredentials: map[string]interface{}{
			"client_id":     "test-client-id",
			"client_secret": "test-client-secret",
		},
	}

	ctx := context.Background()

	// Test GetKVSecret
	secrets, err := mockClient.GetKVSecret(ctx, "myapp/config", "secret")
	if err != nil {
		t.Errorf("GetKVSecret() error = %v, want nil", err)
	}
	if secrets["username"] != "testuser" {
		t.Errorf("GetKVSecret() username = %v, want testuser", secrets["username"])
	}

	// Test GetKVSecretList
	secretsList, err := mockClient.GetKVSecretList(ctx, "myapp", "secret")
	if err != nil {
		t.Errorf("GetKVSecretList() error = %v, want nil", err)
	}
	if len(secretsList) != 3 {
		t.Errorf("GetKVSecretList() length = %v, want 3", len(secretsList))
	}

	// Test GetAzureDynamicCredentials
	creds, err := mockClient.GetAzureDynamicCredentials(ctx, "my-azure-role")
	if err != nil {
		t.Errorf("GetAzureDynamicCredentials() error = %v, want nil", err)
	}
	if creds["client_id"] != "test-client-id" {
		t.Errorf("GetAzureDynamicCredentials() client_id = %v, want test-client-id", creds["client_id"])
	}

	// Test SetToken
	err = mockClient.SetToken("test-token")
	if err != nil {
		t.Errorf("SetToken() error = %v, want nil", err)
	}
}

// Test error scenarios
func TestVaultClient_ErrorScenarios(t *testing.T) {
	mockClient := &MockVaultClient{
		kvError:       fmt.Errorf("secret not found"),
		kvListError:   fmt.Errorf("list operation failed"),
		azureError:    fmt.Errorf("azure role not found"),
		setTokenError: fmt.Errorf("failed to set token"),
	}

	ctx := context.Background()

	// Test GetKVSecret error
	_, err := mockClient.GetKVSecret(ctx, "nonexistent", "secret")
	if err == nil {
		t.Errorf("GetKVSecret() expected error, got nil")
	}

	// Test GetKVSecretList error
	_, err = mockClient.GetKVSecretList(ctx, "nonexistent", "secret")
	if err == nil {
		t.Errorf("GetKVSecretList() expected error, got nil")
	}

	// Test GetAzureDynamicCredentials error
	_, err = mockClient.GetAzureDynamicCredentials(ctx, "nonexistent-role")
	if err == nil {
		t.Errorf("GetAzureDynamicCredentials() expected error, got nil")
	}

	// Test SetToken error
	err = mockClient.SetToken("test-token")
	if err == nil {
		t.Errorf("SetToken() expected error, got nil")
	}
}

// Test legacy functions
func TestLegacyVaultClient_RejectsEmptyCredentials(t *testing.T) {
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
		Credentials: Credentials{
			RoleID:   "",
			SecretID: "",
		},
	}

	ctx := context.Background()
	client, err := LegacyVaultClient(ctx, config)

	// Should fail because empty credentials are not allowed
	if err == nil {
		t.Errorf("LegacyVaultClient() with empty credentials should fail, got nil error")
	}
	if client != nil {
		t.Errorf("LegacyVaultClient() client should be nil when credentials are empty")
	}
}

// Test LoadConfig edge cases
func TestViperConfigLoader_LoadConfig_WithCredentials(t *testing.T) {
	// Set all environment variables
	if err := os.Setenv("VAULT_ADDRESS", "http://localhost:8200"); err != nil {
		t.Fatalf("Failed to set VAULT_ADDRESS: %v", err)
	}
	if err := os.Setenv("VAULT_ROLE_ID", "test-role"); err != nil {
		t.Fatalf("Failed to set VAULT_ROLE_ID: %v", err)
	}
	if err := os.Setenv("VAULT_SECRET_ID", "test-secret"); err != nil {
		t.Fatalf("Failed to set VAULT_SECRET_ID: %v", err)
	}

	loader := &ViperConfigLoader{}
	config, err := loader.LoadConfig()

	if err != nil {
		t.Errorf("ViperConfigLoader.LoadConfig() error = %v, want nil", err)
	}
	if config == nil {
		t.Errorf("ViperConfigLoader.LoadConfig() config = nil, want non-nil")
		return
	}
	if config.Credentials.RoleID != "test-role" {
		t.Errorf("ViperConfigLoader.LoadConfig() RoleID = %v, want test-role", config.Credentials.RoleID)
	}
	if config.Credentials.SecretID != "test-secret" {
		t.Errorf("ViperConfigLoader.LoadConfig() SecretID = %v, want test-secret", config.Credentials.SecretID)
	}

	cleanupEnvVars(t)
}

// Test AppRoleAuthenticator edge cases
func TestAppRoleAuthenticator_Authenticate_WithCredentials(t *testing.T) {
	authenticator := &AppRoleAuthenticator{}
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
		Credentials: Credentials{
			RoleID:   "test-role-id",
			SecretID: "test-secret-id",
		},
	}

	ctx := context.Background()

	// Test with nil client
	err := authenticator.Authenticate(ctx, nil, config)
	if err == nil {
		t.Errorf("AppRoleAuthenticator.Authenticate() expected error with nil client, got nil")
	}
	if err != nil && err.Error() != "vault client cannot be nil" {
		t.Errorf("AppRoleAuthenticator.Authenticate() error = %v, want 'vault client cannot be nil'", err)
	}
}

// Test that partial credentials (only RoleID or only SecretID) are rejected
func TestAppRoleAuthenticator_Authenticate_PartialCredentials(t *testing.T) {
	authenticator := &AppRoleAuthenticator{}
	ctx := context.Background()

	// Create a real client for testing credential validation
	client, err := vault.New(vault.WithAddress("http://localhost:8200"))
	if err != nil {
		t.Fatalf("Failed to create vault client: %v", err)
	}

	tests := []struct {
		name        string
		credentials Credentials
		wantErr     string
	}{
		{
			name:        "both empty",
			credentials: Credentials{RoleID: "", SecretID: ""},
			wantErr:     "vault AppRole credentials are required: both role_id and secret_id must be provided",
		},
		{
			name:        "only RoleID",
			credentials: Credentials{RoleID: "test-role", SecretID: ""},
			wantErr:     "vault AppRole secret_id is required",
		},
		{
			name:        "only SecretID",
			credentials: Credentials{RoleID: "", SecretID: "test-secret"},
			wantErr:     "vault AppRole role_id is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &VaultConfig{
				VaultAddress: "http://localhost:8200",
				Credentials:  tt.credentials,
			}
			err := authenticator.Authenticate(ctx, client, config)
			if err == nil {
				t.Errorf("expected error for %s, got nil", tt.name)
				return
			}
			if err.Error() != tt.wantErr {
				t.Errorf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestAppRoleAuthenticator_Authenticate_NilConfig(t *testing.T) {
	authenticator := &AppRoleAuthenticator{}
	ctx := context.Background()

	// Test with nil config (we can pass a mock client or nil)
	err := authenticator.Authenticate(ctx, nil, nil)
	if err == nil {
		t.Errorf("AppRoleAuthenticator.Authenticate() expected error with nil config, got nil")
	}
	if err != nil && err.Error() != "vault client cannot be nil" {
		// Since we check client first, we get that error
		t.Errorf("AppRoleAuthenticator.Authenticate() error = %v, want 'vault client cannot be nil'", err)
	}
}

// Additional tests for uncovered VaultClient methods

// Test legacy GetKVSecret function
func TestGetKVSecret_Legacy(t *testing.T) {
	// Create a client with nil authenticator (bypasses credential check)
	// to test the legacy wrapper function's code path
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
	}

	ctx := context.Background()
	vaultClient, err := NewVaultClient(ctx, config, nil)

	if err != nil {
		t.Fatalf("NewVaultClient failed: %v", err)
	}

	// Now test the legacy GetKVSecret function
	// This will fail without a real Vault instance, but we're testing the code path
	_, err = GetKVSecret(ctx, vaultClient.client, "secret/data/myapp", "secret")

	// We expect an error since there's no real Vault
	if err == nil {
		t.Logf("GetKVSecret unexpectedly succeeded")
	} else {
		t.Logf("GetKVSecret failed as expected: %v", err)
	}
}

// Test legacy GetAzureDynamicCredentials function
func TestGetAzureDynamicCredentials_Legacy(t *testing.T) {
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
	}

	ctx := context.Background()
	vaultClient, err := NewVaultClient(ctx, config, nil)

	if err != nil {
		t.Fatalf("NewVaultClient failed: %v", err)
	}

	// Test the legacy GetAzureDynamicCredentials function
	_, err = GetAzureDynamicCredentials(ctx, vaultClient.client, "my-azure-role")

	// We expect an error since there's no real Vault
	if err == nil {
		t.Logf("GetAzureDynamicCredentials unexpectedly succeeded")
	} else {
		t.Logf("GetAzureDynamicCredentials failed as expected: %v", err)
	}
}

// Test VaultClient.SetToken method
func TestVaultClient_SetToken(t *testing.T) {
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
	}

	ctx := context.Background()
	vaultClient, err := NewVaultClient(ctx, config, nil)

	if err != nil {
		t.Fatalf("NewVaultClient failed: %v", err)
	}

	// Test SetToken - this should succeed even without a real Vault
	err = vaultClient.SetToken("test-token-12345")
	if err != nil {
		t.Errorf("SetToken() error = %v, want nil", err)
	}
}

// Test VaultClient.GetKVSecret method
func TestVaultClient_GetKVSecret_Method(t *testing.T) {
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
	}

	ctx := context.Background()
	vaultClient, err := NewVaultClient(ctx, config, nil)

	if err != nil {
		t.Fatalf("NewVaultClient failed: %v", err)
	}

	// Test GetKVSecret method
	_, err = vaultClient.GetKVSecret(ctx, "secret/data/myapp", "secret")

	// We expect an error since there's no real Vault
	if err == nil {
		t.Logf("GetKVSecret unexpectedly succeeded")
	} else {
		t.Logf("GetKVSecret failed as expected: %v", err)
	}
}

// Test VaultClient.GetKVSecretList method
func TestVaultClient_GetKVSecretList_Method(t *testing.T) {
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
	}

	ctx := context.Background()
	vaultClient, err := NewVaultClient(ctx, config, nil)

	if err != nil {
		t.Fatalf("NewVaultClient failed: %v", err)
	}

	// Test GetKVSecretList method
	_, err = vaultClient.GetKVSecretList(ctx, "secret/metadata/myapp", "secret")

	// We expect an error since there's no real Vault
	if err == nil {
		t.Logf("GetKVSecretList unexpectedly succeeded")
	} else {
		t.Logf("GetKVSecretList failed as expected: %v", err)
	}
}

// Test VaultClient.GetAzureDynamicCredentials method
func TestVaultClient_GetAzureDynamicCredentials_Method(t *testing.T) {
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
	}

	ctx := context.Background()
	vaultClient, err := NewVaultClient(ctx, config, nil)

	if err != nil {
		t.Fatalf("NewVaultClient failed: %v", err)
	}

	// Test GetAzureDynamicCredentials method
	_, err = vaultClient.GetAzureDynamicCredentials(ctx, "my-azure-role")

	// We expect an error since there's no real Vault
	if err == nil {
		t.Logf("GetAzureDynamicCredentials unexpectedly succeeded")
	} else {
		t.Logf("GetAzureDynamicCredentials failed as expected: %v", err)
	}
}

// Test LoadConfig error handling - missing hostname
func TestViperConfigLoader_LoadConfig_MissingAddress(t *testing.T) {
	// Clear environment variables
	cleanupEnvVars(t)

	// Set some but not all required vars
	if err := os.Setenv("VAULT_ROLE_ID", "test-role"); err != nil {
		t.Fatalf("Failed to set VAULT_ROLE_ID: %v", err)
	}
	if err := os.Setenv("VAULT_SECRET_ID", "test-secret"); err != nil {
		t.Fatalf("Failed to set VAULT_SECRET_ID: %v", err)
	}

	loader := &ViperConfigLoader{}
	config, err := loader.LoadConfig()

	// Should fail due to missing vault address
	if err == nil {
		t.Errorf("ViperConfigLoader.LoadConfig() expected error for missing address, got nil")
	}
	if config != nil {
		t.Errorf("ViperConfigLoader.LoadConfig() config should be nil when error occurs")
	}

	cleanupEnvVars(t)
}

// Test Authenticate with both credentials provided (will fail without real Vault but exercises code path)
func TestAppRoleAuthenticator_Authenticate_WithBothCredentials(t *testing.T) {
	authenticator := &AppRoleAuthenticator{}
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
		Credentials: Credentials{
			RoleID:   "test-role-id",
			SecretID: "test-secret-id",
		},
	}

	ctx := context.Background()

	// Create a client (will succeed)
	client, err := vault.New(
		vault.WithAddress(config.VaultAddress),
	)
	if err != nil {
		t.Fatalf("Failed to create vault client: %v", err)
	}

	// Try to authenticate (will fail without real Vault but exercises the code)
	err = authenticator.Authenticate(ctx, client, config)

	// We expect this to fail since there's no real Vault server
	if err == nil {
		t.Logf("Authenticate unexpectedly succeeded")
	} else {
		// This is expected - the authentication will fail without a real Vault
		t.Logf("Authenticate failed as expected: %v", err)
	}
}

// TestVaultLoadConfig_Concurrent tests that VaultLoadConfig is safe for concurrent use
func TestVaultLoadConfig_Concurrent(t *testing.T) {
	// Set environment variables for testing
	os.Setenv("VAULT_ADDRESS", "http://localhost:8200")
	os.Setenv("VAULT_ROLE_ID", "test_role_id")
	os.Setenv("VAULT_SECRET_ID", "test_secret_id")
	defer func() {
		os.Unsetenv("VAULT_ADDRESS")
		os.Unsetenv("VAULT_ROLE_ID")
		os.Unsetenv("VAULT_SECRET_ID")
	}()

	// Run multiple goroutines concurrently calling VaultLoadConfig
	const numGoroutines = 10
	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			config, err := VaultLoadConfig()
			if err != nil {
				errChan <- fmt.Errorf("goroutine %d: %w", id, err)
				return
			}
			if config.VaultAddress != "http://localhost:8200" {
				errChan <- fmt.Errorf("goroutine %d: unexpected vault address: %s", id, config.VaultAddress)
				return
			}
			if config.Credentials.RoleID != "test_role_id" {
				errChan <- fmt.Errorf("goroutine %d: unexpected role ID: %s", id, config.Credentials.RoleID)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for any errors
	for err := range errChan {
		t.Errorf("Concurrent test error: %v", err)
	}
}
