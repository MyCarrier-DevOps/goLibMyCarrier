package slippy

import (
	"os"
	"strconv"
	"time"
)

// Config holds configuration for the slippy client: pipeline definition, GitHub App
// authentication, and behaviour options. It carries no store connection settings; the
// caller builds the store and passes it to NewClientWithDependencies.
type Config struct {
	// PipelineConfig holds the pipeline step configuration.
	// This defines all steps, their prerequisites, and aggregation relationships.
	// Load via LoadPipelineConfig() from SLIPPY_PIPELINE_CONFIG env var,
	// or LoadPipelineConfigFromFile() for a specific file path.
	PipelineConfig *PipelineConfig

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

	// AncestryDepth is the initial number of commits to check for slip resolution (default: 25)
	// If no ancestor is found, slippy will progressively increase up to AncestryMaxDepth.
	AncestryDepth int

	// AncestryMaxDepth is the maximum number of commits to check when no ancestor is found (default: 100)
	// This handles cases where many commits occur between slip creations.
	AncestryMaxDepth int
}

// DefaultConfig returns a Config with sensible default values.
func DefaultConfig() Config {
	return Config{
		HoldTimeout:      60 * time.Minute,
		PollInterval:     60 * time.Second,
		AncestryDepth:    25,
		AncestryMaxDepth: 100,
	}
}

// ConfigFromEnv loads configuration from environment variables.
// Environment variables:
//   - SLIPPY_PIPELINE_CONFIG: Pipeline configuration (file path or raw JSON). PipelineConfig
//     is left nil when it is unset or fails to load; call LoadPipelineConfig for the error.
//   - SLIPPY_GITHUB_APP_ID: GitHub App ID
//   - SLIPPY_GITHUB_APP_PRIVATE_KEY: Private key (PEM content or file path)
//   - SLIPPY_GITHUB_ENTERPRISE_URL: GitHub Enterprise base URL (optional)
//   - SLIPPY_HOLD_TIMEOUT: Max time to wait for prerequisites (e.g., "60m")
//   - SLIPPY_POLL_INTERVAL: Interval between prereq checks (e.g., "60s")
//   - SLIPPY_SHADOW_MODE: Set to "true" for shadow mode
//   - SLIPPY_ANCESTRY_DEPTH: Initial ancestry search depth (default: 25)
//   - SLIPPY_ANCESTRY_MAX_DEPTH: Max depth for progressive search (default: 100)
func ConfigFromEnv() Config {
	cfg := DefaultConfig()

	// Pipeline configuration
	if pipelineConfig, err := LoadPipelineConfig(); err == nil {
		cfg.PipelineConfig = pipelineConfig
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

	if maxDepth := os.Getenv("SLIPPY_ANCESTRY_MAX_DEPTH"); maxDepth != "" {
		if d, err := strconv.Atoi(maxDepth); err == nil && d > 0 {
			cfg.AncestryMaxDepth = d
		}
	}

	return cfg
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

// WithPipelineConfig returns a copy of the config with the specified pipeline config.
func (c Config) WithPipelineConfig(pipelineConfig *PipelineConfig) Config {
	c.PipelineConfig = pipelineConfig
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
