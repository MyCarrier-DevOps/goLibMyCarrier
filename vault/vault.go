package vault

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/vault-client-go"
	"github.com/hashicorp/vault-client-go/schema"
	"github.com/spf13/viper"
)

// VaultClientInterface defines the interface for Vault operations
type VaultClientInterface interface {
	GetKVSecret(ctx context.Context, path, mount string) (map[string]interface{}, error)
	GetKVSecretList(ctx context.Context, path, mount string) ([]string, error)
	GetAzureDynamicCredentials(ctx context.Context, azureRole string) (map[string]interface{}, error)
	SetToken(token string) error
}

// ConfigLoader interface allows for dependency injection of configuration loading
type ConfigLoader interface {
	LoadConfig() (*VaultConfig, error)
}

// VaultAuthenticator interface for authentication strategies
type VaultAuthenticator interface {
	Authenticate(ctx context.Context, client *vault.Client, config *VaultConfig) error
}

type Credentials struct {
	RoleID   string `mapstructure:"role_id"`
	SecretID string `mapstructure:"secret_id"`
}

type VaultConfig struct {
	VaultAddress string      `mapstructure:"vaultaddress"`
	Credentials  Credentials `mapstructure:"credentials"`
}

// VaultClient wraps the actual Vault client and implements VaultClientInterface
type VaultClient struct {
	client *vault.Client
}

// AppRoleAuthenticator implements authentication using AppRole
type AppRoleAuthenticator struct{}

// ViperConfigLoader implements configuration loading using Viper
type ViperConfigLoader struct{}

// viperMutex protects concurrent access to Viper operations
var viperMutex sync.Mutex

// ViperConfigLoader implements ConfigLoader interface
func (v *ViperConfigLoader) LoadConfig() (*VaultConfig, error) {
	// Lock to protect concurrent access when multiple loaders run simultaneously
	viperMutex.Lock()
	defer viperMutex.Unlock()

	// Use an isolated viper instance to avoid global state pollution
	// that could affect other packages using viper with different env prefixes
	vp := viper.New()

	// Bind environment variables
	if err := vp.BindEnv("vaultaddress", "VAULT_ADDRESS"); err != nil {
		return nil, fmt.Errorf("error binding environment variable VAULT_ADDRESS: %w", err)
	}
	if err := vp.BindEnv("credentials.role_id", "VAULT_ROLE_ID"); err != nil {
		return nil, fmt.Errorf("error binding environment variable VAULT_ROLE_ID: %w", err)
	}
	if err := vp.BindEnv("credentials.secret_id", "VAULT_SECRET_ID"); err != nil {
		return nil, fmt.Errorf("error binding environment variable VAULT_SECRET_ID: %w", err)
	}

	// Read environment variables
	vp.AutomaticEnv()

	var config VaultConfig

	// Unmarshal environment variables into the Config struct
	if err := vp.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("unable to decode into struct, %w", err)
	}

	// Validate the configuration
	if err := VaultValidateConfig(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

// VaultLoadConfig is a convenience function that uses the default ViperConfigLoader
func VaultLoadConfig() (*VaultConfig, error) {
	loader := &ViperConfigLoader{}
	return loader.LoadConfig()
}

// validateConfig validates the loaded configuration.
func VaultValidateConfig(config *VaultConfig) error {
	if config.VaultAddress == "" {
		return fmt.Errorf("vault address is required")
	}
	return nil
}

// AppRoleAuthenticator implements VaultAuthenticator interface
func (a *AppRoleAuthenticator) Authenticate(ctx context.Context, client *vault.Client, config *VaultConfig) error {
	if client == nil {
		return fmt.Errorf("vault client cannot be nil")
	}
	if config == nil {
		return fmt.Errorf("vault config cannot be nil")
	}
	if config.Credentials.RoleID == "" && config.Credentials.SecretID == "" {
		return fmt.Errorf("vault AppRole credentials are required: both role_id and secret_id must be provided")
	}
	if config.Credentials.RoleID == "" {
		return fmt.Errorf("vault AppRole role_id is required")
	}
	if config.Credentials.SecretID == "" {
		return fmt.Errorf("vault AppRole secret_id is required")
	}

	// Authenticate with Vault using AppRole
	resp, err := client.Auth.AppRoleLogin(
		ctx,
		schema.AppRoleLoginRequest{
			RoleId:   config.Credentials.RoleID,
			SecretId: config.Credentials.SecretID,
		})
	if err != nil {
		return fmt.Errorf("error authenticating with vault: %w", err)
	}

	clientToken := resp.Auth.ClientToken
	// Set the client token for future requests
	if err := client.SetToken(clientToken); err != nil {
		return fmt.Errorf("error setting token: %w", err)
	}

	return nil
}

// NewVaultClient creates a new Vault client with the given configuration and authenticator
func NewVaultClient(ctx context.Context, config *VaultConfig, authenticator VaultAuthenticator) (*VaultClient, error) {
	// prepare a client with the given base address
	client, err := vault.New(
		vault.WithAddress(config.VaultAddress),
		vault.WithRequestTimeout(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("error configuring vault: %w", err)
	}

	// Authenticate if authenticator is provided
	if authenticator != nil {
		if err := authenticator.Authenticate(ctx, client, config); err != nil {
			return nil, err
		}
	}

	return &VaultClient{client: client}, nil
}

// CreateVaultClient is a convenience function that uses AppRole authentication
func CreateVaultClient(ctx context.Context, config *VaultConfig) (*VaultClient, error) {
	authenticator := &AppRoleAuthenticator{}
	return NewVaultClient(ctx, config, authenticator)
}

// VaultClient methods implementing VaultClientInterface
func (vc *VaultClient) GetKVSecret(ctx context.Context, path, mount string) (map[string]interface{}, error) {
	// Read a secret from Vault's KV v2 secrets engine
	secret, err := vc.client.Secrets.KvV2Read(
		ctx,
		path,
		vault.WithMountPath(mount),
	)
	if err != nil {
		return nil, fmt.Errorf("error reading secret: %w", err)
	}
	return secret.Data.Data, nil
}

func (vc *VaultClient) GetKVSecretList(ctx context.Context, path, mount string) ([]string, error) {
	// Read a secret from Vault's KV v2 secrets engine
	secret, err := vc.client.Secrets.KvV2List(
		ctx,
		path,
		vault.WithMountPath(mount),
	)
	if err != nil {
		return nil, fmt.Errorf("error reading secret: %w", err)
	}
	return secret.Data.Keys, nil
}

func (vc *VaultClient) GetAzureDynamicCredentials(
	ctx context.Context,
	azureRole string,
) (map[string]interface{}, error) {
	// Read dynamic credentials from Vault's Azure secrets engine
	secret, err := vc.client.Secrets.AzureRequestServicePrincipalCredentials(
		ctx,
		azureRole,
		vault.WithMountPath("azure"),
	)
	if err != nil {
		return nil, fmt.Errorf("error reading secret: %w", err)
	}
	return secret.Data, nil
}

func (vc *VaultClient) SetToken(token string) error {
	return vc.client.SetToken(token)
}

// Backward compatibility functions (deprecated, use the new interface-based approach)

// LegacyVaultClient maintains backward compatibility with the old function signature
func LegacyVaultClient(ctx context.Context, config *VaultConfig) (*vault.Client, error) {
	vaultClient, err := CreateVaultClient(ctx, config)
	if err != nil {
		return nil, err
	}
	return vaultClient.client, nil
}

func GetKVSecret(ctx context.Context, client *vault.Client, path, mount string) (map[string]interface{}, error) {
	vc := &VaultClient{client: client}
	return vc.GetKVSecret(ctx, path, mount)
}

func GetAzureDynamicCredentials(
	ctx context.Context,
	client *vault.Client,
	azure_role string,
) (map[string]interface{}, error) {
	vc := &VaultClient{client: client}
	return vc.GetAzureDynamicCredentials(ctx, azure_role)
}
