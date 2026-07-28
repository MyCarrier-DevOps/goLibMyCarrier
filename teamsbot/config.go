package teamsbot

import (
	"errors"
	"fmt"
	"net/url"
	"os"

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
	AppSecret  string `mapstructure:"app_secret"  json:"-"`
	TenantID   string `mapstructure:"tenant_id"`
	ServiceURL string `mapstructure:"service_url"`
}

// String implements fmt.Stringer so printing a Config never discloses AppSecret.
func (c Config) String() string {
	return fmt.Sprintf("teamsbot.Config{AppID:%q, AppSecret:REDACTED, TenantID:%q, ServiceURL:%q}",
		c.AppID, c.TenantID, c.ServiceURL)
}

// GoString implements fmt.GoStringer so %#v also redacts AppSecret.
func (c Config) GoString() string { return c.String() }

// LoadConfig loads the Teams bot config directly from environment variables:
//   - TEAMS_BOT_APP_ID     -> AppID      (required)
//   - TEAMS_BOT_APP_SECRET -> AppSecret  (required)
//   - TEAMS_BOT_TENANT_ID  -> TenantID   (required)
//   - TEAMS_SERVICE_URL    -> ServiceURL (optional; defaults to the global URL)
//
// It reads with os.Getenv rather than viper: viper's AutomaticEnv() makes every
// key in the struct ambiently overridable by any similarly-named environment
// variable, which is a surprising and hard-to-audit source of misconfiguration.
// LoadConfigFromViper remains the opt-in seam for callers that want to inject
// config from a secret manager or other viper-backed source.
func LoadConfig() (*Config, error) {
	config := &Config{
		AppID:      os.Getenv("TEAMS_BOT_APP_ID"),
		AppSecret:  os.Getenv("TEAMS_BOT_APP_SECRET"),
		TenantID:   os.Getenv("TEAMS_BOT_TENANT_ID"),
		ServiceURL: os.Getenv("TEAMS_SERVICE_URL"),
	}
	if config.ServiceURL == "" {
		config.ServiceURL = defaultServiceURL
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return config, nil
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

// ValidateServiceURLShape rejects values that cannot address the Bot Connector.
func ValidateServiceURLShape(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("teamsbot: TEAMS_SERVICE_URL is not a valid URL: %w", err)
	}
	if !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("teamsbot: TEAMS_SERVICE_URL must be an absolute URL with a host, got %q", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("teamsbot: TEAMS_SERVICE_URL must not carry a query or fragment, got %q", raw)
	}
	return nil
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
	if err := ValidateServiceURLShape(config.ServiceURL); err != nil {
		return err
	}
	u, err := url.Parse(config.ServiceURL)
	if err != nil {
		return fmt.Errorf("teamsbot: TEAMS_SERVICE_URL is not a valid URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("teamsbot: TEAMS_SERVICE_URL must use https, got scheme %q", u.Scheme)
	}
	return nil
}
