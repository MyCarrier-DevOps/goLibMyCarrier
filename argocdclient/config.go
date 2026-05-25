package argocdclient

import (
	"errors"
	"fmt"

	"github.com/spf13/viper"
)

// ErrNilViper is returned by LoadConfigFromViper when the caller
// passes a nil *viper.Viper. Exported as a sentinel so callers can match it
// with errors.Is rather than string comparison.
var ErrNilViper = errors.New("viper instance cannot be nil")

// Config holds the configuration for ArgoCD client operations.
// Only ServerUrl and AuthToken are required fields; AppName and Revision are optional.
type Config struct {
	ServerUrl string `mapstructure:"server_url"`
	AuthToken string `mapstructure:"auth_token"`
}

// ConfigLoaderInterface defines the contract for configuration loading
type ConfigLoaderInterface interface {
	// LoadConfig loads configuration from environment variables
	LoadConfig() (*Config, error)
	// ValidateConfig validates the provided configuration
	ValidateConfig(config *Config) error
}

// LoadConfig loads the ArgoCD client configuration from environment variables.
// It binds the following environment variables to configuration fields:
//   - ARGOCD_SERVER -> ServerUrl (required)
//   - ARGOCD_AUTHTOKEN -> AuthToken (required)
//
// Uses an isolated viper instance to avoid global state pollution that could
// affect other packages using viper with different env prefixes.
//
// Returns an error if required environment variables are missing or if
// there are issues with configuration binding or validation.
func LoadConfig() (*Config, error) {
	vp := viper.New()

	if err := vp.BindEnv("server_url", "ARGOCD_SERVER"); err != nil {
		return nil, fmt.Errorf("error binding ARGOCD_SERVER: %w", err)
	}
	if err := vp.BindEnv("auth_token", "ARGOCD_AUTHTOKEN"); err != nil {
		return nil, fmt.Errorf("error binding ARGOCD_AUTHTOKEN: %w", err)
	}

	vp.AutomaticEnv()

	return LoadConfigFromViper(vp)
}

// LoadConfigFromViper loads the ArgoCD client configuration from a
// caller-provided viper instance. The caller owns the viper instance — this
// function does NOT call BindEnv/AutomaticEnv/SetEnvPrefix on it. The caller
// is responsible for any env binding they need, and may pre-populate values
// via vp.Set(...) to override secrets without touching process environment.
//
// Use this constructor when you need to:
//   - Inject explicit values for testing (vp.Set("auth_token", "..."))
//   - Share a viper instance across multiple configs in your application
//   - Override secrets pulled from a secret manager without setting env vars
//
// For the default env-binding behaviour, use LoadConfig().
func LoadConfigFromViper(vp *viper.Viper) (*Config, error) {
	if vp == nil {
		return nil, ErrNilViper
	}

	var config Config

	if err := vp.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("error unmarshalling config: %w", err)
	}

	if err := validateConfig(&config); err != nil {
		return nil, fmt.Errorf("error validating config: %w", err)
	}
	return &config, nil
}

// validateConfig validates that all required configuration fields are present.
// Currently only validates ServerUrl and AuthToken as required fields.
// AppName and Revision are optional and not validated.
func validateConfig(config *Config) error {
	if config.ServerUrl == "" {
		return fmt.Errorf("ARGOCD_SERVER is required")
	}
	if config.AuthToken == "" {
		return fmt.Errorf("ARGOCD_AUTHTOKEN is required")
	}

	return nil
}
