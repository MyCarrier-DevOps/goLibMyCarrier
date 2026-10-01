package slippy

import (
	"os"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.HoldTimeout != 60*time.Minute {
		t.Errorf("HoldTimeout = %v, want 60m", cfg.HoldTimeout)
	}
	if cfg.PollInterval != 60*time.Second {
		t.Errorf("PollInterval = %v, want 60s", cfg.PollInterval)
	}
	if cfg.AncestryDepth != 25 {
		t.Errorf("AncestryDepth = %d, want 25", cfg.AncestryDepth)
	}
	if cfg.AncestryMaxDepth != 100 {
		t.Errorf("AncestryMaxDepth = %d, want 100", cfg.AncestryMaxDepth)
	}
	if cfg.ShadowMode {
		t.Error("ShadowMode should default to false")
	}
}

func TestConfigFromEnv(t *testing.T) {
	// Save original env vars for Slippy
	origAppID := os.Getenv("SLIPPY_GITHUB_APP_ID")
	origKey := os.Getenv("SLIPPY_GITHUB_APP_PRIVATE_KEY")
	origEnterprise := os.Getenv("SLIPPY_GITHUB_ENTERPRISE_URL")
	origTimeout := os.Getenv("SLIPPY_HOLD_TIMEOUT")
	origInterval := os.Getenv("SLIPPY_POLL_INTERVAL")
	origShadow := os.Getenv("SLIPPY_SHADOW_MODE")
	origDepth := os.Getenv("SLIPPY_ANCESTRY_DEPTH")

	// Restore env vars after test
	defer func() {
		_ = os.Setenv("SLIPPY_GITHUB_APP_ID", origAppID)
		_ = os.Setenv("SLIPPY_GITHUB_APP_PRIVATE_KEY", origKey)
		_ = os.Setenv("SLIPPY_GITHUB_ENTERPRISE_URL", origEnterprise)
		_ = os.Setenv("SLIPPY_HOLD_TIMEOUT", origTimeout)
		_ = os.Setenv("SLIPPY_POLL_INTERVAL", origInterval)
		_ = os.Setenv("SLIPPY_SHADOW_MODE", origShadow)
		_ = os.Setenv("SLIPPY_ANCESTRY_DEPTH", origDepth)
	}()

	// Set test values for Slippy
	_ = os.Setenv("SLIPPY_GITHUB_APP_ID", "12345")
	_ = os.Setenv("SLIPPY_GITHUB_APP_PRIVATE_KEY", "test-private-key")
	_ = os.Setenv("SLIPPY_GITHUB_ENTERPRISE_URL", "https://github.example.com")
	_ = os.Setenv("SLIPPY_HOLD_TIMEOUT", "30m")
	_ = os.Setenv("SLIPPY_POLL_INTERVAL", "30s")
	_ = os.Setenv("SLIPPY_SHADOW_MODE", "true")
	_ = os.Setenv("SLIPPY_ANCESTRY_DEPTH", "50")

	cfg := ConfigFromEnv()

	if cfg.GitHubAppID != 12345 {
		t.Errorf("GitHubAppID = %d, want 12345", cfg.GitHubAppID)
	}
	if cfg.GitHubPrivateKey != "test-private-key" {
		t.Errorf("GitHubPrivateKey = %q, want 'test-private-key'", cfg.GitHubPrivateKey)
	}
	if cfg.GitHubEnterpriseURL != "https://github.example.com" {
		t.Errorf("GitHubEnterpriseURL = %q, want expected URL", cfg.GitHubEnterpriseURL)
	}
	if cfg.HoldTimeout != 30*time.Minute {
		t.Errorf("HoldTimeout = %v, want 30m", cfg.HoldTimeout)
	}
	if cfg.PollInterval != 30*time.Second {
		t.Errorf("PollInterval = %v, want 30s", cfg.PollInterval)
	}
	if !cfg.ShadowMode {
		t.Error("ShadowMode should be true")
	}
	if cfg.AncestryDepth != 50 {
		t.Errorf("AncestryDepth = %d, want 50", cfg.AncestryDepth)
	}
}

func TestConfigFromEnv_InvalidValues(t *testing.T) {
	// Save and restore env vars
	origAppID := os.Getenv("SLIPPY_GITHUB_APP_ID")
	origTimeout := os.Getenv("SLIPPY_HOLD_TIMEOUT")
	origDepth := os.Getenv("SLIPPY_ANCESTRY_DEPTH")
	defer func() {
		_ = os.Setenv("SLIPPY_GITHUB_APP_ID", origAppID)
		_ = os.Setenv("SLIPPY_HOLD_TIMEOUT", origTimeout)
		_ = os.Setenv("SLIPPY_ANCESTRY_DEPTH", origDepth)
	}()

	// Set invalid values
	_ = os.Setenv("SLIPPY_GITHUB_APP_ID", "not-a-number")
	_ = os.Setenv("SLIPPY_HOLD_TIMEOUT", "invalid-duration")
	_ = os.Setenv("SLIPPY_ANCESTRY_DEPTH", "-5")

	cfg := ConfigFromEnv()

	// Should fall back to defaults for invalid values
	if cfg.GitHubAppID != 0 {
		t.Errorf("GitHubAppID should be 0 for invalid value, got %d", cfg.GitHubAppID)
	}
	if cfg.HoldTimeout != 60*time.Minute {
		t.Errorf("HoldTimeout should be default for invalid value, got %v", cfg.HoldTimeout)
	}
	// Negative depth should not be applied
	if cfg.AncestryDepth != 25 {
		t.Errorf("AncestryDepth should be default for negative value, got %d", cfg.AncestryDepth)
	}
}

func TestConfigFromEnv_MaxDepth(t *testing.T) {
	// Save and restore env vars
	origMaxDepth := os.Getenv("SLIPPY_ANCESTRY_MAX_DEPTH")
	defer func() {
		_ = os.Setenv("SLIPPY_ANCESTRY_MAX_DEPTH", origMaxDepth)
	}()

	// Test valid max depth
	_ = os.Setenv("SLIPPY_ANCESTRY_MAX_DEPTH", "200")

	cfg := ConfigFromEnv()

	if cfg.AncestryMaxDepth != 200 {
		t.Errorf("AncestryMaxDepth = %d, want 200", cfg.AncestryMaxDepth)
	}

	// Test invalid max depth (negative)
	_ = os.Setenv("SLIPPY_ANCESTRY_MAX_DEPTH", "-10")
	cfg = ConfigFromEnv()

	if cfg.AncestryMaxDepth != 100 { // default
		t.Errorf("AncestryMaxDepth should be default (100) for negative value, got %d", cfg.AncestryMaxDepth)
	}

	// Test invalid max depth (non-numeric)
	_ = os.Setenv("SLIPPY_ANCESTRY_MAX_DEPTH", "not-a-number")
	cfg = ConfigFromEnv()

	if cfg.AncestryMaxDepth != 100 { // default
		t.Errorf("AncestryMaxDepth should be default (100) for invalid value, got %d", cfg.AncestryMaxDepth)
	}
}

func TestConfig_WithShadowMode(t *testing.T) {
	cfg := DefaultConfig()

	newCfg := cfg.WithShadowMode(true)

	if !newCfg.ShadowMode {
		t.Error("WithShadowMode(true) should enable shadow mode")
	}
	// Original should be unchanged
	if cfg.ShadowMode {
		t.Error("original config should not be modified")
	}

	// Test disabling
	enabled := Config{ShadowMode: true}
	disabled := enabled.WithShadowMode(false)
	if disabled.ShadowMode {
		t.Error("WithShadowMode(false) should disable shadow mode")
	}
}

func TestConfig_WithLogger(t *testing.T) {
	cfg := DefaultConfig()
	logger := newTestLogger()

	newCfg := cfg.WithLogger(logger)

	if newCfg.Logger == nil {
		t.Error("WithLogger should set the logger")
	}
	// Original should be unchanged
	if cfg.Logger != nil {
		t.Error("original config should not be modified")
	}
}

func TestConfig_GitHubConfig(t *testing.T) {
	cfg := Config{
		GitHubAppID:         12345,
		GitHubPrivateKey:    "my-private-key",
		GitHubEnterpriseURL: "https://github.enterprise.com",
	}

	ghConfig := cfg.GitHubConfig()

	if ghConfig.AppID != 12345 {
		t.Errorf("AppID = %d, want 12345", ghConfig.AppID)
	}
	if ghConfig.PrivateKey != "my-private-key" {
		t.Errorf("PrivateKey = %q, want 'my-private-key'", ghConfig.PrivateKey)
	}
	if ghConfig.EnterpriseURL != "https://github.enterprise.com" {
		t.Errorf("EnterpriseURL = %q, want expected URL", ghConfig.EnterpriseURL)
	}
}

func TestConfig_WithPipelineConfig(t *testing.T) {
	cfg := DefaultConfig()

	pipelineConfig := &PipelineConfig{
		Version:     "1",
		Name:        "test-pipeline",
		Description: "Test pipeline",
	}

	newCfg := cfg.WithPipelineConfig(pipelineConfig)

	if newCfg.PipelineConfig != pipelineConfig {
		t.Error("WithPipelineConfig should set the pipeline config")
	}
	// Original should be unchanged
	if cfg.PipelineConfig != nil {
		t.Error("original config should not be modified")
	}
}
