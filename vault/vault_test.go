package vault

import (
	"os"
	"testing"
)

func TestVaultLoadConfig(t *testing.T) {
	// Set environment variables for testing
	os.Setenv("VAULT_ADDRESS", "http://localhost:8200")
	os.Setenv("VAULT_LOCAL_ROLE_ID", "test_role_id")
	os.Setenv("VAULT_LOCAL_SECRET_ID", "test_secret_id")

	// Call the function
	config, err := VaultLoadConfig()

	// Assert that there is no error
	if err != nil {
		t.Errorf("VaultLoadConfig() error = %v", err)
		return
	}

	// Assert that the VaultAddress is correct
	if config.VaultAddress != "http://localhost:8200" {
		t.Errorf("VaultLoadConfig() VaultAddress = %v, want %v", config.VaultAddress, "http://localhost:8200")
	}

	// Assert that the Local.RoleID is correct
	if config.Local.RoleID != "test_role_id" {
		t.Errorf("VaultLoadConfig() Local.RoleID = %v, want %v", config.Local.RoleID, "test_role_id")
	}

	// Assert that the Local.SecretID is correct
	if config.Local.SecretID != "test_secret_id" {
		t.Errorf("VaultLoadConfig() Local.SecretID = %v, want %v", config.Local.SecretID, "test_secret_id")
	}

	// Unset environment variables after testing
	os.Unsetenv("VAULT_ADDRESS")
	os.Unsetenv("VAULT_LOCAL_ROLE_ID")
	os.Unsetenv("VAULT_LOCAL_SECRET_ID")
}

func TestVaultLoadConfig_NoAddress(t *testing.T) {
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
		t.Errorf("VaultValidateConfig() error = %v", err)
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
