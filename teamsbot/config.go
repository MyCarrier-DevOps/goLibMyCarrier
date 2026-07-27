package teamsbot

import (
	"errors"
	"fmt"

	"github.com/spf13/viper"
)

// defaultServiceURL is the global Bot Connector service URL for Microsoft Teams
// in the public cloud, used for send-only proactive messaging.
const defaultServiceURL = "https://smba.trafficmanager.net/teams"

// ErrNilViper is returned by LoadConfigFromViper when passed a nil *viper.Viper.
var ErrNilViper = errors.New("viper instance cannot be nil")

// Config holds the credentials and endpoint for the Teams bot. AppID, AppSecret,
// and TenantID are required; ServiceURL is optional and defaults to the global
// Teams Bot Connector URL.
type Config struct {
	AppID      string `mapstructure:"app_id"`
	AppSecret  string `mapstructure:"app_secret"`
	TenantID   string `mapstructure:"tenant_id"`
	ServiceURL string `mapstructure:"service_url"`
}

// LoadConfig loads the Teams bot config from environment variables:
//   - TEAMS_BOT_APP_ID     -> AppID      (required)
//   - TEAMS_BOT_APP_SECRET -> AppSecret  (required)
//   - TEAMS_BOT_TENANT_ID  -> TenantID   (required)
//   - TEAMS_SERVICE_URL    -> ServiceURL (optional; defaults to the global URL)
//
// It uses an isolated viper instance to avoid global-state pollution.
func LoadConfig() (*Config, error) {
	vp := viper.New()
	binds := map[string]string{
		"app_id":      "TEAMS_BOT_APP_ID",
		"app_secret":  "TEAMS_BOT_APP_SECRET",
		"tenant_id":   "TEAMS_BOT_TENANT_ID",
		"service_url": "TEAMS_SERVICE_URL",
	}
	for key, env := range binds {
		if err := vp.BindEnv(key, env); err != nil {
			return nil, fmt.Errorf("error binding %s: %w", env, err)
		}
	}
	vp.AutomaticEnv()
	return LoadConfigFromViper(vp)
}

// LoadConfigFromViper loads config from a caller-provided viper instance. The
// caller owns the instance — this does NOT call BindEnv/AutomaticEnv. Use it to
// inject secrets from a secret manager (vp.Set("app_secret", ...)) or in tests.
func LoadConfigFromViper(vp *viper.Viper) (*Config, error) {
	if vp == nil {
		return nil, ErrNilViper
	}
	var config Config
	if err := vp.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("error unmarshalling config: %w", err)
	}
	if config.ServiceURL == "" {
		config.ServiceURL = defaultServiceURL
	}
	if err := validateConfig(&config); err != nil {
		return nil, fmt.Errorf("error validating config: %w", err)
	}
	return &config, nil
}

func validateConfig(config *Config) error {
	if config.AppID == "" {
		return fmt.Errorf("TEAMS_BOT_APP_ID is required")
	}
	if config.AppSecret == "" {
		return fmt.Errorf("TEAMS_BOT_APP_SECRET is required")
	}
	if config.TenantID == "" {
		return fmt.Errorf("TEAMS_BOT_TENANT_ID is required")
	}
	return nil
}
