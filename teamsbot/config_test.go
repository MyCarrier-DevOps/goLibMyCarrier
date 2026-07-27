package teamsbot

import (
	"errors"
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
