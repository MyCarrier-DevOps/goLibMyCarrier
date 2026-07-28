package teamsbot

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestLoadConfigFromViper(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*viper.Viper)
		wantErr bool
		wantURL string
	}{
		{
			name: "valid with default service url",
			setup: func(vp *viper.Viper) {
				vp.Set("app_id", "app")
				vp.Set("app_secret", "secret")
				vp.Set("tenant_id", "tenant")
			},
			wantURL: defaultServiceURL,
		},
		{
			name: "custom service url is kept",
			setup: func(vp *viper.Viper) {
				vp.Set("app_id", "app")
				vp.Set("app_secret", "secret")
				vp.Set("tenant_id", "tenant")
				vp.Set("service_url", "https://example.test/teams")
			},
			wantURL: "https://example.test/teams",
		},
		{
			name:    "missing app id errors",
			setup:   func(vp *viper.Viper) { vp.Set("app_secret", "s"); vp.Set("tenant_id", "t") },
			wantErr: true,
		},
		{
			name:    "missing tenant id errors",
			setup:   func(vp *viper.Viper) { vp.Set("app_id", "a"); vp.Set("app_secret", "s") },
			wantErr: true,
		},
		{
			name:    "missing app secret errors",
			setup:   func(vp *viper.Viper) { vp.Set("app_id", "a"); vp.Set("tenant_id", "t") },
			wantErr: true,
		},
		{
			name: "non-https service url errors",
			setup: func(vp *viper.Viper) {
				vp.Set("app_id", "a")
				vp.Set("app_secret", "s")
				vp.Set("tenant_id", "t")
				vp.Set("service_url", "http://example.test/teams")
			},
			wantErr: true,
		},
		{
			name: "service url with query errors",
			setup: func(vp *viper.Viper) {
				vp.Set("app_id", "a")
				vp.Set("app_secret", "s")
				vp.Set("tenant_id", "t")
				vp.Set("service_url", "https://example.test/teams?x=1")
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vp := viper.New()
			tt.setup(vp)
			cfg, err := LoadConfigFromViper(vp)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.ServiceURL != tt.wantURL {
				t.Errorf("ServiceURL = %q, want %q", cfg.ServiceURL, tt.wantURL)
			}
		})
	}
}

func TestLoadConfigFromViperNil(t *testing.T) {
	if _, err := LoadConfigFromViper(nil); !errors.Is(err, ErrNilViper) {
		t.Fatalf("expected ErrNilViper, got %v", err)
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("TEAMS_BOT_APP_ID", "env-app")
	t.Setenv("TEAMS_BOT_APP_SECRET", "env-secret")
	t.Setenv("TEAMS_BOT_TENANT_ID", "env-tenant")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AppID != "env-app" || cfg.AppSecret != "env-secret" || cfg.TenantID != "env-tenant" {
		t.Fatalf("got %+v", cfg)
	}
	if cfg.ServiceURL != defaultServiceURL {
		t.Fatalf("ServiceURL = %q, want %q", cfg.ServiceURL, defaultServiceURL)
	}
}

func TestLoadConfigCustomServiceURL(t *testing.T) {
	t.Setenv("TEAMS_BOT_APP_ID", "env-app")
	t.Setenv("TEAMS_BOT_APP_SECRET", "env-secret")
	t.Setenv("TEAMS_BOT_TENANT_ID", "env-tenant")
	t.Setenv("TEAMS_SERVICE_URL", "https://example.test/teams")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ServiceURL != "https://example.test/teams" {
		t.Fatalf("ServiceURL = %q, want %q", cfg.ServiceURL, "https://example.test/teams")
	}
}

func TestLoadConfigMissingAppSecret(t *testing.T) {
	t.Setenv("TEAMS_BOT_APP_ID", "env-app")
	t.Setenv("TEAMS_BOT_APP_SECRET", "")
	t.Setenv("TEAMS_BOT_TENANT_ID", "env-tenant")

	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "TEAMS_BOT_APP_SECRET is required") {
		t.Fatalf("expected TEAMS_BOT_APP_SECRET error, got %v", err)
	}
}

func TestLoadConfigRejectsNonHTTPSServiceURL(t *testing.T) {
	t.Setenv("TEAMS_BOT_APP_ID", "env-app")
	t.Setenv("TEAMS_BOT_APP_SECRET", "env-secret")
	t.Setenv("TEAMS_BOT_TENANT_ID", "env-tenant")
	t.Setenv("TEAMS_SERVICE_URL", "http://example.test/teams")

	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected https-required error, got %v", err)
	}
}

func TestLoadConfigRejectsServiceURLWithQuery(t *testing.T) {
	t.Setenv("TEAMS_BOT_APP_ID", "env-app")
	t.Setenv("TEAMS_BOT_APP_SECRET", "env-secret")
	t.Setenv("TEAMS_BOT_TENANT_ID", "env-tenant")
	t.Setenv("TEAMS_SERVICE_URL", "https://example.test/teams?x=1")

	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "query or fragment") {
		t.Fatalf("expected query/fragment error, got %v", err)
	}
}

func TestValidateServiceURLShape(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{name: "valid https url", raw: "https://smba.example.com/teams"},
		{name: "invalid url escape", raw: "https://example.com/%zz", wantErr: "not a valid URL"},
		{name: "relative url has no host", raw: "/teams", wantErr: "absolute URL with a host"},
		{name: "query not allowed", raw: "https://example.com/teams?x=1", wantErr: "query or fragment"},
		{name: "fragment not allowed", raw: "https://example.com/teams#frag", wantErr: "query or fragment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateServiceURLShape(tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestConfigStringAndJSONRedactSecret(t *testing.T) {
	cfg := Config{
		AppID:      "app-1",
		AppSecret:  "super-secret-value",
		TenantID:   "tenant-1",
		ServiceURL: "https://example.test/teams",
	}

	formatted := []string{
		fmt.Sprintf("%v", cfg),
		fmt.Sprintf("%+v", cfg),
		fmt.Sprintf("%#v", cfg),
	}
	for _, got := range formatted {
		if strings.Contains(got, cfg.AppSecret) {
			t.Fatalf("formatted output leaked secret: %q", got)
		}
		if !strings.Contains(got, "REDACTED") {
			t.Fatalf("formatted output missing REDACTED marker: %q", got)
		}
	}

	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(b), cfg.AppSecret) {
		t.Fatalf("json output leaked secret: %s", b)
	}
}
