package slippy

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds configuration for the slippy client.
// It includes connection settings, authentication, and behavior options.
type Config struct {
	// ClickHouseDSN is the connection string for ClickHouse
	// Format: clickhouse://user:password@host:port/database
	ClickHouseDSN string

	// GitHubAppID is the GitHub App ID for authentication
	GitHubAppID int64

	// GitHubPrivateKey is the PEM-encoded private key or path to key file
	GitHubPrivateKey string

	// GitHubEnterpriseURL is the base URL for GitHub Enterprise Server (optional)
	// Leave empty for github.com
	GitHubEnterpriseURL string

	// Logger is the logger implementation to use
	Logger Logger

	// HoldTimeout is the maximum time to wait for prerequisites (default: 60m)
	HoldTimeout time.Duration

	// PollInterval is the interval between prerequisite checks (default: 60s)
	PollInterval time.Duration

	// ShadowMode if true, never actually hold/skip - useful for gradual rollout
	ShadowMode bool

	// AncestryDepth is how many commits to check for slip resolution (default: 20)
	AncestryDepth int
}

// DefaultConfig returns a Config with sensible default values.
func DefaultConfig() Config {
	return Config{
		HoldTimeout:   60 * time.Minute,
		PollInterval:  60 * time.Second,
		AncestryDepth: 20,
	}
}

// ConfigFromEnv loads configuration from environment variables.
// Environment variables:
//   - SLIPPY_CLICKHOUSE_DSN: ClickHouse connection string
//   - SLIPPY_GITHUB_APP_ID: GitHub App ID
//   - SLIPPY_GITHUB_APP_PRIVATE_KEY: Private key (PEM content or file path)
//   - SLIPPY_GITHUB_ENTERPRISE_URL: GitHub Enterprise base URL (optional)
//   - SLIPPY_HOLD_TIMEOUT: Max time to wait for prerequisites (e.g., "60m")
//   - SLIPPY_POLL_INTERVAL: Interval between prereq checks (e.g., "60s")
//   - SLIPPY_SHADOW_MODE: Set to "true" for shadow mode
//   - SLIPPY_ANCESTRY_DEPTH: Number of commits to check (default: 20)
func ConfigFromEnv() Config {
	cfg := DefaultConfig()

	// ClickHouse
	if dsn := os.Getenv("SLIPPY_CLICKHOUSE_DSN"); dsn != "" {
		cfg.ClickHouseDSN = dsn
	}

	// GitHub App authentication
	if appID := os.Getenv("SLIPPY_GITHUB_APP_ID"); appID != "" {
		if id, err := strconv.ParseInt(appID, 10, 64); err == nil {
			cfg.GitHubAppID = id
		}
	}
	cfg.GitHubPrivateKey = os.Getenv("SLIPPY_GITHUB_APP_PRIVATE_KEY")
	cfg.GitHubEnterpriseURL = os.Getenv("SLIPPY_GITHUB_ENTERPRISE_URL")

	// Behavior settings
	if timeout, err := time.ParseDuration(os.Getenv("SLIPPY_HOLD_TIMEOUT")); err == nil {
		cfg.HoldTimeout = timeout
	}
	if interval, err := time.ParseDuration(os.Getenv("SLIPPY_POLL_INTERVAL")); err == nil {
		cfg.PollInterval = interval
	}
	cfg.ShadowMode = os.Getenv("SLIPPY_SHADOW_MODE") == "true"

	if depth := os.Getenv("SLIPPY_ANCESTRY_DEPTH"); depth != "" {
		if d, err := strconv.Atoi(depth); err == nil && d > 0 {
			cfg.AncestryDepth = d
		}
	}

	return cfg
}

// Validate checks that all required configuration is present and valid.
// Returns an error describing any missing or invalid settings.
func (c Config) Validate() error {
	if c.ClickHouseDSN == "" {
		return fmt.Errorf("%w: ClickHouseDSN is required", ErrInvalidConfiguration)
	}
	if c.GitHubAppID == 0 {
		return fmt.Errorf("%w: GitHubAppID is required", ErrInvalidConfiguration)
	}
	if c.GitHubPrivateKey == "" {
		return fmt.Errorf("%w: GitHubPrivateKey is required", ErrInvalidConfiguration)
	}
	if c.HoldTimeout <= 0 {
		return fmt.Errorf("%w: HoldTimeout must be positive", ErrInvalidConfiguration)
	}
	if c.PollInterval <= 0 {
		return fmt.Errorf("%w: PollInterval must be positive", ErrInvalidConfiguration)
	}
	if c.AncestryDepth <= 0 {
		return fmt.Errorf("%w: AncestryDepth must be positive", ErrInvalidConfiguration)
	}
	return nil
}

// WithLogger returns a copy of the config with the specified logger.
func (c Config) WithLogger(logger Logger) Config {
	c.Logger = logger
	return c
}

// WithShadowMode returns a copy of the config with shadow mode enabled.
func (c Config) WithShadowMode(enabled bool) Config {
	c.ShadowMode = enabled
	return c
}

// GitHubConfig returns a GitHubConfig derived from this Config.
func (c Config) GitHubConfig() GitHubConfig {
	return GitHubConfig{
		AppID:         c.GitHubAppID,
		PrivateKey:    c.GitHubPrivateKey,
		EnterpriseURL: c.GitHubEnterpriseURL,
	}
}
