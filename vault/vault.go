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
	Address string      `mapstructure:"address"`
	Local   LocalConfig `mapstructure:"local"`
}

func VaultLoadConfig() (*VaultConfig, error) {
	viper.SetEnvPrefix("APP") // Set environment variable prefix

	// Bind environment variables
	viper.BindEnv("address", "APP_VAULT_ADDRESS")
	viper.BindEnv("local.role_id", "APP_VAULT_LOCAL_ROLE_ID")
	viper.BindEnv("local.secret_id", "APP_VAULT_LOCAL_SECRET_ID")

	// Read environment variables
	viper.AutomaticEnv()

	var config VaultConfig

	// Unmarshal environment variables into the Config struct
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("unable to decode into struct, %v", err)
	}

	// Validate the configuration
	if err := VaultValidateConfig(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

// validateConfig validates the loaded configuration.
func VaultValidateConfig(config *VaultConfig) error {
	if config.Address == "" {
		return fmt.Errorf("vault address is required")
	}
	return nil
}

func VaultClient(ctx context.Context) (*vault.Client, error) {
	// Load the configuration
	config, err := VaultLoadConfig()
	if err != nil {
		return nil, fmt.Errorf("Unable to load config, %v", err)
	}

	// prepare a client with the given base address
	client, err := vault.New(
		vault.WithAddress(config.Address),
		vault.WithRequestTimeout(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("Error configuring vault, %v", err)
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
			return nil, fmt.Errorf("Error authenticating with vault, %v", err)
		}
		clientToken := resp.Auth.ClientToken
		// Set the client token for future requests
		err = client.SetToken(clientToken)
		if err != nil {
			// Return an error if setting the token fails
			return nil, fmt.Errorf("Error retrieving token %v", err)
		}
	}
	return client, nil
}

func getKVSecret(ctx context.Context, client *vault.Client, path string, mount string) (map[string]interface{}, error) {
	// Read a secret from Vault's KV v2 secrets engine
	secret, err := client.Secrets.KvV2Read(
		ctx,
		path,
		vault.WithMountPath(mount),
	)
	if err != nil {
		// Return an error if reading the secret fails
		return nil, fmt.Errorf("Error reading secret, %v", err)
	}
	// Return the secret data
	return secret.Data.Data, nil
}
