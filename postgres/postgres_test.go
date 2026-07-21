package postgres

import (
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validBaseConfig() *PostgresConfig {
	return &PostgresConfig{
		PgHostname: "db.example.com",
		PgUsername: "slippy_write",
		PgPassword: "secret",
		PgDatabase: "ci",
		PgPort:     "5432",
		PgSSLMode:  "require",
	}
}

func TestPostgresValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*PostgresConfig)
		wantErr string // substring expected in the error; empty means no error
	}{
		{"valid", func(*PostgresConfig) {}, ""},
		{"missing hostname", func(c *PostgresConfig) { c.PgHostname = "" }, "hostname"},
		{"missing username", func(c *PostgresConfig) { c.PgUsername = "" }, "username"},
		{"missing password", func(c *PostgresConfig) { c.PgPassword = "" }, "password"},
		{"missing database", func(c *PostgresConfig) { c.PgDatabase = "" }, "database"},
		{"missing port", func(c *PostgresConfig) { c.PgPort = "" }, "port"},
		{"missing sslmode", func(c *PostgresConfig) { c.PgSSLMode = "" }, "sslmode"},
		{"invalid sslmode", func(c *PostgresConfig) { c.PgSSLMode = "bogus" }, "sslmode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBaseConfig()
			tt.mutate(cfg)
			err := PostgresValidateConfig(cfg)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestPostgresValidateConfig_Nil(t *testing.T) {
	require.Error(t, PostgresValidateConfig(nil))
}

func TestResolvePoolSettings(t *testing.T) {
	t.Run("nil returns defaults", func(t *testing.T) {
		maxC, minC, life := ResolvePoolSettings(nil)
		assert.Equal(t, DefaultMaxConns, maxC)
		assert.Equal(t, DefaultMinConns, minC)
		assert.Equal(t, DefaultConnMaxLifetime, life)
	})
	t.Run("zero fields inherit defaults", func(t *testing.T) {
		maxC, minC, life := ResolvePoolSettings(&PostgresConfig{})
		assert.Equal(t, DefaultMaxConns, maxC)
		assert.Equal(t, DefaultMinConns, minC)
		assert.Equal(t, DefaultConnMaxLifetime, life)
	})
	t.Run("non-zero fields override", func(t *testing.T) {
		maxC, minC, life := ResolvePoolSettings(&PostgresConfig{
			MaxConns: 10, MinConns: 3, ConnMaxLifetime: 5 * time.Minute,
		})
		assert.Equal(t, int32(10), maxC)
		assert.Equal(t, int32(3), minC)
		assert.Equal(t, 5*time.Minute, life)
	})
}

func TestPostgresLoadConfig_FromEnv(t *testing.T) {
	t.Setenv("POSTGRES_HOSTNAME", "pg.internal")
	t.Setenv("POSTGRES_USERNAME", "slippy_write")
	t.Setenv("POSTGRES_PASSWORD", "hunter2")
	t.Setenv("POSTGRES_DATABASE", "ci_test")
	// PORT and SSLMODE intentionally unset — loader must default them.

	cfg, err := PostgresLoadConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "pg.internal", cfg.PgHostname)
	assert.Equal(t, "slippy_write", cfg.PgUsername)
	assert.Equal(t, "hunter2", cfg.PgPassword)
	assert.Equal(t, "ci_test", cfg.PgDatabase)
	assert.Equal(t, "5432", cfg.PgPort, "port should default to 5432")
	assert.Equal(t, "require", cfg.PgSSLMode, "sslmode should default to require")
}

func TestPostgresLoadConfig_MissingRequired(t *testing.T) {
	// Only hostname set; username/password/database missing -> validation error.
	t.Setenv("POSTGRES_HOSTNAME", "pg.internal")
	_, err := PostgresLoadConfig()
	require.Error(t, err)
}

func TestPostgresLoadConfigFromViper_Nil(t *testing.T) {
	_, err := PostgresLoadConfigFromViper(nil)
	require.ErrorIs(t, err, ErrNilViper)
}

func TestPostgresLoadConfigFromViper_Overrides(t *testing.T) {
	v := viper.New()
	v.Set("pghostname", "explicit-host")
	v.Set("pgusername", "u")
	v.Set("pgpassword", "p")
	v.Set("pgdatabase", "d")
	v.Set("pgport", "6543")
	v.Set("pgsslmode", "verify-full")
	v.Set("connmaxlifetime", "90s")

	cfg, err := PostgresLoadConfigFromViper(v)
	require.NoError(t, err)
	assert.Equal(t, "explicit-host", cfg.PgHostname)
	assert.Equal(t, "6543", cfg.PgPort)
	assert.Equal(t, "verify-full", cfg.PgSSLMode)
	assert.Equal(t, 90*time.Second, cfg.ConnMaxLifetime)
}
