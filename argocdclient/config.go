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
	AppName   string `mapstructure:"app_name"`
	Revision  string `mapstructure:"revision"`
}

// LoadConfig loads the ArgoCD client configuration from environment variables.
// It binds the following environment variables to configuration fields:
//   - ARGOCD_SERVER -> ServerUrl (required)
//   - ARGOCD_AUTHTOKEN -> AuthToken (required)
//   - ARGOCD_APP_NAME -> AppName (optional)
//   - ARGOCD_REVISION -> Revision (optional)
//
// Returns an error if required environment variables are missing or if
// there are issues with configuration binding or validation.
func LoadConfig() (*Config, error) {
	if err := viper.BindEnv("server_url", "ARGOCD_SERVER"); err != nil {
		return nil, fmt.Errorf("error binding ARGOCD_SERVER: %w", err)
	}
	if err := viper.BindEnv("auth_token", "ARGOCD_AUTHTOKEN"); err != nil {
		return nil, fmt.Errorf("error binding ARGOCD_AUTHTOKEN: %w", err)
	}
	if err := viper.BindEnv("app_name", "ARGOCD_APP_NAME"); err != nil {
		return nil, fmt.Errorf("error binding ARGOCD_APP_NAME: %w", err)
	}
	if err := viper.BindEnv("revision", "ARGOCD_REVISION"); err != nil {
		return nil, fmt.Errorf("error binding ARGOCD_REVISION: %w", err)
	}

	viper.AutomaticEnv()
	var config Config

	if err := viper.Unmarshal(&config); err != nil {
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
