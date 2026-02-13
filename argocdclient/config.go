package argocdclient

import (
	"fmt"

	"github.com/spf13/viper"
)

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
