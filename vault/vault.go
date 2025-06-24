package vault

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/vault-client-go"
	"github.com/hashicorp/vault-client-go/schema"
	"github.com/spf13/viper"
)

type LocalConfig struct {
	RoleID   string `mapstructure:"role_id"`
	SecretID string `mapstructure:"secret_id"`
}

type VaultConfig struct {
	VaultAddress string      `mapstructure:"vaultaddress"`
	Local        LocalConfig `mapstructure:"local"`
}

func VaultLoadConfig() (*VaultConfig, error) {
	// Bind environment variables
	if err := viper.BindEnv("vaultaddress", "VAULT_ADDRESS"); err != nil {
		return nil, fmt.Errorf("error binding environment variable VAULT_ADDRESS: %w", err)
	}
	if err := viper.BindEnv("local.role_id", "VAULT_LOCAL_ROLE_ID"); err != nil {
		return nil, fmt.Errorf("error binding environment variable VAULT_LOCAL_ROLE_ID: %w", err)
	}
	if err := viper.BindEnv("local.secret_id", "VAULT_LOCAL_SECRET_ID"); err != nil {
		return nil, fmt.Errorf("error binding environment variable VAULT_LOCAL_SECRET_ID: %w", err)
	}

	// Read environment variables
	viper.AutomaticEnv()

	var config VaultConfig

	// Unmarshal environment variables into the Config struct
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("unable to decode into struct, %w", err)
	}

	// Validate the configuration
	if err := VaultValidateConfig(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

// validateConfig validates the loaded configuration.
func VaultValidateConfig(config *VaultConfig) error {
	if config.VaultAddress == "" {
		return fmt.Errorf("vault address is required")
	}
	return nil
}

func VaultClient(ctx context.Context) (*vault.Client, error) {
	// Load the configuration
	config, err := VaultLoadConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load config, %w", err)
	}

	// prepare a client with the given base address
	client, err := vault.New(
		vault.WithAddress(config.VaultAddress),
		vault.WithRequestTimeout(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("error configuring vault, %w", err)
	}

	// Load local configuration if needed to authenticate
	if config.Local.RoleID != "" && config.Local.SecretID != "" {
		// Authenticate with Vault using AppRole
		resp, err := client.Auth.AppRoleLogin(
			ctx,
			schema.AppRoleLoginRequest{
				RoleId:   config.Local.RoleID,
				SecretId: config.Local.SecretID,
			})
		if err != nil {
			// Return an error if authentication fails
			return nil, fmt.Errorf("error authenticating with vault, %w", err)
		}
		clientToken := resp.Auth.ClientToken
		// Set the client token for future requests
		err = client.SetToken(clientToken)
		if err != nil {
			// Return an error if setting the token fails
			return nil, fmt.Errorf("error retrieving token %w", err)
		}
	}
	return client, nil
}

func GetKVSecret(ctx context.Context, client *vault.Client, path string, mount string) (map[string]interface{}, error) {
	// Read a secret from Vault's KV v2 secrets engine
	secret, err := client.Secrets.KvV2Read(
		ctx,
		path,
		vault.WithMountPath(mount),
	)
	if err != nil {
		// Return an error if reading the secret fails
		return nil, fmt.Errorf("error reading secret, %w", err)
	}
	// Return the secret data
	return secret.Data.Data, nil
}

func GetAzureDynamicCredentials(
	ctx context.Context,
	client *vault.Client,
	azure_role string,
) (map[string]interface{}, error) {
	// Read dynamic credentials from Vault's Azure secrets engine
	secret, err := client.Secrets.AzureRequestServicePrincipalCredentials(
		ctx,
		azure_role,
		vault.WithMountPath("azure"),
	)
	if err != nil {
		// Return an error if reading the secret fails
		return nil, fmt.Errorf("error reading secret, %w", err)
	}
	// Return the secret data
	return secret.Data, nil
}
