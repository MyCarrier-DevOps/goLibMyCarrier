package vault

import (
	"context"
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
	// We can't easily create a real vault client for testing, so this test focuses on the no-credentials path
	err := authenticator.Authenticate(ctx, nil, config)

	// Should return nil when no credentials are provided
	if err != nil {
		t.Errorf("AppRoleAuthenticator.Authenticate() with no credentials should return nil, got %v", err)
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
