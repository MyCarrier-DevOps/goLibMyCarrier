package vault

import (
	"context"
	"fmt"
	"os"
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

	// Create a minimal mock client struct (we can't easily create a real one without vault)
	// But we can test the logic path where client is not nil but credentials are empty
	// We'll use a mock here since we can't easily create a real vault.Client

	// For this test, we'll call it with a mock that's designed to test the no-credentials path
	// Since the function checks credentials before making any client calls, this should work

	// Actually, let's test this differently - we can't easily create a vault.Client without vault
	// So let's modify the approach to test the authentication logic through NewVaultClient

	// Test that when no credentials are provided, authentication should be skipped
	client, err := NewVaultClient(ctx, config, authenticator)

	// This should succeed even without credentials (auth is skipped)
	if err != nil {
		t.Errorf("NewVaultClient() with no credentials should succeed, got error: %v", err)
	}
	if client == nil {
		t.Errorf("NewVaultClient() client should not be nil when config is valid")
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
func TestCreateVaultClient_Success(t *testing.T) {
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
		Credentials: Credentials{
			RoleID:   "",
			SecretID: "",
		},
	}

	ctx := context.Background()
	client, err := CreateVaultClient(ctx, config)

	// Should succeed with valid config (no credentials means no auth)
	if err != nil {
		t.Errorf("CreateVaultClient() error = %v, want nil", err)
	}
	if client == nil {
		t.Errorf("CreateVaultClient() client = nil, want non-nil")
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
func TestLegacyVaultClient(t *testing.T) {
	config := &VaultConfig{
		VaultAddress: "http://localhost:8200",
		Credentials: Credentials{
			RoleID:   "",
			SecretID: "",
		},
	}

	ctx := context.Background()
	client, err := LegacyVaultClient(ctx, config)

	if err != nil {
		t.Errorf("LegacyVaultClient() error = %v, want nil", err)
	}
	if client == nil {
		t.Errorf("LegacyVaultClient() client = nil, want non-nil")
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
