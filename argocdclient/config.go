package argocdclient

import (
	"fmt"

	"github.com/spf13/viper"
)

type Config struct {
	ServerUrl string `mapstructure:"server_url"`
	AuthToken string `mapstructure:"auth_token"`
	AppName   string `mapstructure:"app_name"`
	Revision  string `mapstructure:"revision"`
}

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

func validateConfig(config *Config) error {
	if config.ServerUrl == "" {
		return fmt.Errorf("ARGOCD_SERVER is required")
	}
	if config.AuthToken == "" {
		return fmt.Errorf("ARGOCD_AUTHTOKEN is required")
	}
	if config.AppName == "" {
		return fmt.Errorf("ARGOCD_APP_NAME is required")
	}
	if config.Revision == "" {
		return fmt.Errorf("ARGOCD_REVISION is required")
	}

	return nil
}
