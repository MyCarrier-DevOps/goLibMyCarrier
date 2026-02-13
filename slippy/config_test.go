package slippy

import (
	"errors"
	"os"
	"testing"
	"time"

	ch "github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse"
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
	// Save original env vars for ClickHouse (loaded via clickhouse package)
	origCHHost := os.Getenv("CLICKHOUSE_HOSTNAME")
	origCHPort := os.Getenv("CLICKHOUSE_PORT")
	origCHUser := os.Getenv("CLICKHOUSE_USERNAME")
	origCHPass := os.Getenv("CLICKHOUSE_PASSWORD")
	origCHDB := os.Getenv("CLICKHOUSE_DATABASE")
	origCHSkip := os.Getenv("CLICKHOUSE_SKIP_VERIFY")

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
		_ = os.Setenv("CLICKHOUSE_HOSTNAME", origCHHost)
		_ = os.Setenv("CLICKHOUSE_PORT", origCHPort)
		_ = os.Setenv("CLICKHOUSE_USERNAME", origCHUser)
		_ = os.Setenv("CLICKHOUSE_PASSWORD", origCHPass)
		_ = os.Setenv("CLICKHOUSE_DATABASE", origCHDB)
		_ = os.Setenv("CLICKHOUSE_SKIP_VERIFY", origCHSkip)
		_ = os.Setenv("SLIPPY_GITHUB_APP_ID", origAppID)
		_ = os.Setenv("SLIPPY_GITHUB_APP_PRIVATE_KEY", origKey)
		_ = os.Setenv("SLIPPY_GITHUB_ENTERPRISE_URL", origEnterprise)
		_ = os.Setenv("SLIPPY_HOLD_TIMEOUT", origTimeout)
		_ = os.Setenv("SLIPPY_POLL_INTERVAL", origInterval)
		_ = os.Setenv("SLIPPY_SHADOW_MODE", origShadow)
		_ = os.Setenv("SLIPPY_ANCESTRY_DEPTH", origDepth)
	}()

	// Set test values for ClickHouse (via clickhouse package env vars)
	_ = os.Setenv("CLICKHOUSE_HOSTNAME", "localhost")
	_ = os.Setenv("CLICKHOUSE_PORT", "9000")
	_ = os.Setenv("CLICKHOUSE_USERNAME", "testuser")
	_ = os.Setenv("CLICKHOUSE_PASSWORD", "testpass")
	_ = os.Setenv("CLICKHOUSE_DATABASE", "testdb")
	_ = os.Setenv("CLICKHOUSE_SKIP_VERIFY", "true")

	// Set test values for Slippy
	_ = os.Setenv("SLIPPY_GITHUB_APP_ID", "12345")
	_ = os.Setenv("SLIPPY_GITHUB_APP_PRIVATE_KEY", "test-private-key")
	_ = os.Setenv("SLIPPY_GITHUB_ENTERPRISE_URL", "https://github.example.com")
	_ = os.Setenv("SLIPPY_HOLD_TIMEOUT", "30m")
	_ = os.Setenv("SLIPPY_POLL_INTERVAL", "30s")
	_ = os.Setenv("SLIPPY_SHADOW_MODE", "true")
	_ = os.Setenv("SLIPPY_ANCESTRY_DEPTH", "50")

	cfg := ConfigFromEnv()

	// Verify ClickHouse config was loaded
	if cfg.ClickHouseConfig == nil {
		t.Error("ClickHouseConfig should not be nil")
	} else {
		if cfg.ClickHouseConfig.ChHostname != "localhost" {
			t.Errorf("ChHostname = %q, want 'localhost'", cfg.ClickHouseConfig.ChHostname)
		}
		if cfg.ClickHouseConfig.ChPort != "9000" {
			t.Errorf("ChPort = %q, want '9000'", cfg.ClickHouseConfig.ChPort)
		}
		if cfg.ClickHouseConfig.ChUsername != "testuser" {
			t.Errorf("ChUsername = %q, want 'testuser'", cfg.ClickHouseConfig.ChUsername)
		}
		if cfg.ClickHouseConfig.ChPassword != "testpass" {
			t.Errorf("ChPassword = %q, want 'testpass'", cfg.ClickHouseConfig.ChPassword)
		}
		if cfg.ClickHouseConfig.ChDatabase != "testdb" {
			t.Errorf("ChDatabase = %q, want 'testdb'", cfg.ClickHouseConfig.ChDatabase)
		}
	}

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

func TestConfigFromEnv_MaxDepthAndDatabase(t *testing.T) {
	// Save and restore env vars
	origMaxDepth := os.Getenv("SLIPPY_ANCESTRY_MAX_DEPTH")
	origDatabase := os.Getenv("SLIPPY_DATABASE")
	defer func() {
		_ = os.Setenv("SLIPPY_ANCESTRY_MAX_DEPTH", origMaxDepth)
		_ = os.Setenv("SLIPPY_DATABASE", origDatabase)
	}()

	// Test valid max depth
	_ = os.Setenv("SLIPPY_ANCESTRY_MAX_DEPTH", "200")
	_ = os.Setenv("SLIPPY_DATABASE", "custom_db")

	cfg := ConfigFromEnv()

	if cfg.AncestryMaxDepth != 200 {
		t.Errorf("AncestryMaxDepth = %d, want 200", cfg.AncestryMaxDepth)
	}
	if cfg.Database != "custom_db" {
		t.Errorf("Database = %q, want 'custom_db'", cfg.Database)
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

func TestConfig_Validate(t *testing.T) {
	// Helper to create a valid ClickHouseConfig for tests
	validCHConfig := &ch.ClickhouseConfig{
		ChHostname:   "localhost",
		ChPort:       "9000",
		ChDatabase:   "testdb",
		ChUsername:   "user",
		ChPassword:   "pass",
		ChSkipVerify: "true",
	}

	// Helper to create a valid PipelineConfig for tests
	validPipelineConfig := &PipelineConfig{
		Version:     "1",
		Name:        "test-pipeline",
		Description: "Test pipeline",
		Steps: []StepConfig{
			{Name: "push_parsed", Description: "Push parsed"},
			{Name: "builds_completed", Description: "Builds completed", Prerequisites: []string{"push_parsed"}},
		},
	}

	tests := []struct {
		name      string
		config    Config
		wantError bool
		errorIs   error
	}{
		{
			name: "valid config",
			config: Config{
				ClickHouseConfig: validCHConfig,
				PipelineConfig:   validPipelineConfig,
				GitHubAppID:      12345,
				GitHubPrivateKey: "key-content",
				HoldTimeout:      time.Minute,
				PollInterval:     time.Second,
				AncestryDepth:    10,
				AncestryMaxDepth: 100,
			},
			wantError: false,
		},
		{
			name: "missing ClickHouseConfig",
			config: Config{
				PipelineConfig:   validPipelineConfig,
				GitHubAppID:      12345,
				GitHubPrivateKey: "key",
				HoldTimeout:      time.Minute,
				PollInterval:     time.Second,
				AncestryDepth:    10,
				AncestryMaxDepth: 100,
			},
			wantError: true,
			errorIs:   ErrInvalidConfiguration,
		},
		{
			name: "missing PipelineConfig",
			config: Config{
				ClickHouseConfig: validCHConfig,
				GitHubAppID:      12345,
				GitHubPrivateKey: "key",
				HoldTimeout:      time.Minute,
				PollInterval:     time.Second,
				AncestryDepth:    10,
				AncestryMaxDepth: 100,
			},
			wantError: true,
			errorIs:   ErrInvalidConfiguration,
		},
		{
			name: "missing GitHubAppID",
			config: Config{
				ClickHouseConfig: validCHConfig,
				PipelineConfig:   validPipelineConfig,
				GitHubPrivateKey: "key",
				HoldTimeout:      time.Minute,
				PollInterval:     time.Second,
				AncestryDepth:    10,
				AncestryMaxDepth: 100,
			},
			wantError: true,
			errorIs:   ErrInvalidConfiguration,
		},
		{
			name: "missing GitHubPrivateKey",
			config: Config{
				ClickHouseConfig: validCHConfig,
				PipelineConfig:   validPipelineConfig,
				GitHubAppID:      12345,
				HoldTimeout:      time.Minute,
				PollInterval:     time.Second,
				AncestryDepth:    10,
				AncestryMaxDepth: 100,
			},
			wantError: true,
			errorIs:   ErrInvalidConfiguration,
		},
		{
			name: "zero HoldTimeout",
			config: Config{
				ClickHouseConfig: validCHConfig,
				PipelineConfig:   validPipelineConfig,
				GitHubAppID:      12345,
				GitHubPrivateKey: "key",
				HoldTimeout:      0,
				PollInterval:     time.Second,
				AncestryDepth:    10,
				AncestryMaxDepth: 100,
			},
			wantError: true,
			errorIs:   ErrInvalidConfiguration,
		},
		{
			name: "negative HoldTimeout",
			config: Config{
				ClickHouseConfig: validCHConfig,
				PipelineConfig:   validPipelineConfig,
				GitHubAppID:      12345,
				GitHubPrivateKey: "key",
				HoldTimeout:      -time.Minute,
				PollInterval:     time.Second,
				AncestryDepth:    10,
				AncestryMaxDepth: 100,
			},
			wantError: true,
			errorIs:   ErrInvalidConfiguration,
		},
		{
			name: "zero PollInterval",
			config: Config{
				ClickHouseConfig: validCHConfig,
				PipelineConfig:   validPipelineConfig,
				GitHubAppID:      12345,
				GitHubPrivateKey: "key",
				HoldTimeout:      time.Minute,
				PollInterval:     0,
				AncestryDepth:    10,
				AncestryMaxDepth: 100,
			},
			wantError: true,
			errorIs:   ErrInvalidConfiguration,
		},
		{
			name: "zero AncestryDepth",
			config: Config{
				ClickHouseConfig: validCHConfig,
				PipelineConfig:   validPipelineConfig,
				GitHubAppID:      12345,
				GitHubPrivateKey: "key",
				HoldTimeout:      time.Minute,
				PollInterval:     time.Second,
				AncestryDepth:    0,
				AncestryMaxDepth: 100,
			},
			wantError: true,
			errorIs:   ErrInvalidConfiguration,
		},
		{
			name: "AncestryMaxDepth less than AncestryDepth",
			config: Config{
				ClickHouseConfig: validCHConfig,
				PipelineConfig:   validPipelineConfig,
				GitHubAppID:      12345,
				GitHubPrivateKey: "key",
				HoldTimeout:      time.Minute,
				PollInterval:     time.Second,
				AncestryDepth:    50,
				AncestryMaxDepth: 25, // less than AncestryDepth
			},
			wantError: true,
			errorIs:   ErrInvalidConfiguration,
		},
		{
			name: "ClickHouse load error stored",
			config: Config{
				ClickHouseConfig:  nil,
				clickhouseLoadErr: errors.New("connection failed"),
				PipelineConfig:    validPipelineConfig,
				GitHubAppID:       12345,
				GitHubPrivateKey:  "key",
				HoldTimeout:       time.Minute,
				PollInterval:      time.Second,
				AncestryDepth:     10,
				AncestryMaxDepth:  100,
			},
			wantError: true,
			errorIs:   ErrInvalidConfiguration,
		},
		{
			name: "Pipeline load error stored",
			config: Config{
				ClickHouseConfig: validCHConfig,
				PipelineConfig:   nil,
				pipelineLoadErr:  errors.New("invalid config file"),
				GitHubAppID:      12345,
				GitHubPrivateKey: "key",
				HoldTimeout:      time.Minute,
				PollInterval:     time.Second,
				AncestryDepth:    10,
				AncestryMaxDepth: 100,
			},
			wantError: true,
			errorIs:   ErrInvalidConfiguration,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantError {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errorIs != nil && !errors.Is(err, tt.errorIs) {
					t.Errorf("error should wrap %v", tt.errorIs)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
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

func TestConfig_ValidateMinimal(t *testing.T) {
	validCHConfig := &ch.ClickhouseConfig{
		ChHostname:   "localhost",
		ChPort:       "9000",
		ChDatabase:   "testdb",
		ChUsername:   "user",
		ChPassword:   "pass",
		ChSkipVerify: "true",
	}

	validPipelineConfig := &PipelineConfig{
		Version:     "1",
		Name:        "test-pipeline",
		Description: "Test pipeline",
		Steps: []StepConfig{
			{Name: "push_parsed", Description: "Push parsed"},
		},
	}

	tests := []struct {
		name      string
		config    Config
		wantError bool
	}{
		{
			name: "valid minimal config",
			config: Config{
				ClickHouseConfig: validCHConfig,
				PipelineConfig:   validPipelineConfig,
			},
			wantError: false,
		},
		{
			name: "missing ClickHouseConfig",
			config: Config{
				PipelineConfig: validPipelineConfig,
			},
			wantError: true,
		},
		{
			name: "missing PipelineConfig",
			config: Config{
				ClickHouseConfig: validCHConfig,
			},
			wantError: true,
		},
		{
			name:      "missing both",
			config:    Config{},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.ValidateMinimal()
			if tt.wantError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
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

func TestConfig_WithDatabase(t *testing.T) {
	cfg := DefaultConfig()

	newCfg := cfg.WithDatabase("custom_db")

	if newCfg.Database != "custom_db" {
		t.Errorf("WithDatabase should set database to 'custom_db', got '%s'", newCfg.Database)
	}
	// Original should be unchanged (default is "ci")
	if cfg.Database != "ci" {
		t.Errorf("original config should have default database 'ci', got '%s'", cfg.Database)
	}
}
