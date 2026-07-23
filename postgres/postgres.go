// Package postgres provides configuration loading, validation, and (in later
// files) a pgx-based connection pool for MyCarrier Go services. It mirrors the
// shape of the goLibMyCarrier/clickhouse package so consumers can adopt it with
// the same ergonomics: a POSTGRES_*-prefixed env config, an isolated viper
// loader, and a validated config struct.
package postgres

import (
	"errors"
	"fmt"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Connection-pool defaults, applied when PostgresConfig leaves a field
// zero-valued. ConnMaxLifetime is kept short so connections behind
// LBs/firewalls that silently drop idle peers are reaped promptly.
const (
	DefaultMaxConns        int32         = 50
	DefaultMinConns        int32         = 2
	DefaultConnMaxLifetime time.Duration = 60 * time.Second

	// Server-side statement/lock/transaction timeouts, applied as connection
	// startup parameters (see session.buildRuntimeParams). They cap the blast
	// radius of a hung query or a transaction that acquires a per-slip FOR UPDATE
	// lock and then stalls — without them one stuck transaction blocks every
	// mutator for that slip indefinitely. Conservative for a control-plane store
	// whose operations are sub-second; override per env when a workload needs more.
	DefaultStatementTimeout       time.Duration = 30 * time.Second
	DefaultLockTimeout            time.Duration = 10 * time.Second
	DefaultIdleInTxSessionTimeout time.Duration = 60 * time.Second

	defaultPort = "5432"
	// verify-full is the most hardened sslmode: it encrypts AND authenticates the
	// server (certificate chain + hostname), closing the MITM gap that plain
	// "require" leaves open. Azure Database for PostgreSQL presents a publicly
	// rooted certificate (DigiCert Global Root G2), so verify-full works against
	// the system trust store with no extra config. A deployment whose runtime lacks
	// that root, or that fronts PG with a private CA, sets POSTGRES_SSLROOTCERT to a
	// CA bundle (or, as a last resort, POSTGRES_SSLMODE=require to opt back down).
	defaultSSLMode = "verify-full"
)

// validSSLModes is the set of libpq/pgx sslmode values accepted by the driver.
var validSSLModes = map[string]struct{}{
	"disable":     {},
	"allow":       {},
	"prefer":      {},
	"require":     {},
	"verify-ca":   {},
	"verify-full": {},
}

// ErrNilViper is returned by PostgresLoadConfigFromViper when the caller passes
// a nil *viper.Viper. Exported as a sentinel so callers can match it with
// errors.Is rather than string comparison.
var ErrNilViper = errors.New("viper instance cannot be nil")

// PostgresConfig holds the connection parameters and pool tunables for a
// Postgres client. Leave the pool tunables zero to inherit the package
// defaults (see ResolvePoolSettings).
type PostgresConfig struct {
	PgHostname string `mapstructure:"pghostname"`
	PgUsername string `mapstructure:"pgusername"`
	PgPassword string `mapstructure:"pgpassword"`
	PgDatabase string `mapstructure:"pgdatabase"`
	PgPort     string `mapstructure:"pgport"`
	PgSSLMode  string `mapstructure:"pgsslmode"`
	// PgSSLRootCert is an optional path to a PEM CA bundle used to verify the
	// server certificate. Leave empty to use the OS trust store (sufficient for
	// Azure's publicly rooted certificate); set it when fronting PG with a private
	// CA under sslmode verify-ca/verify-full.
	PgSSLRootCert string `mapstructure:"pgsslrootcert"`

	MaxConns        int32         `mapstructure:"maxconns"`
	MinConns        int32         `mapstructure:"minconns"`
	ConnMaxLifetime time.Duration `mapstructure:"connmaxlifetime"`

	// Server-side timeouts (zero inherits the package default; see ResolveTimeouts).
	StatementTimeout       time.Duration `mapstructure:"statementtimeout"`
	LockTimeout            time.Duration `mapstructure:"locktimeout"`
	IdleInTxSessionTimeout time.Duration `mapstructure:"idleintxsessiontimeout"`
}

// ResolvePoolSettings returns the pool values to use, substituting the package
// defaults for any zero-valued field in cfg. Exported so callers and tests can
// introspect what will actually be applied to the driver.
func ResolvePoolSettings(cfg *PostgresConfig) (maxConns, minConns int32, lifetime time.Duration) {
	maxConns, minConns, lifetime = DefaultMaxConns, DefaultMinConns, DefaultConnMaxLifetime
	if cfg == nil {
		return maxConns, minConns, lifetime
	}
	if cfg.MaxConns > 0 {
		maxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		minConns = cfg.MinConns
	}
	if cfg.ConnMaxLifetime > 0 {
		lifetime = cfg.ConnMaxLifetime
	}
	return maxConns, minConns, lifetime
}

// ResolveTimeouts returns the server-side timeouts to apply, substituting the
// package defaults for any zero-valued field in cfg. Exported so callers and
// tests can introspect what will actually be sent as connection parameters.
func ResolveTimeouts(cfg *PostgresConfig) (statement, lock, idleInTx time.Duration) {
	statement, lock, idleInTx = DefaultStatementTimeout, DefaultLockTimeout, DefaultIdleInTxSessionTimeout
	if cfg == nil {
		return statement, lock, idleInTx
	}
	if cfg.StatementTimeout > 0 {
		statement = cfg.StatementTimeout
	}
	if cfg.LockTimeout > 0 {
		lock = cfg.LockTimeout
	}
	if cfg.IdleInTxSessionTimeout > 0 {
		idleInTx = cfg.IdleInTxSessionTimeout
	}
	return statement, lock, idleInTx
}

// PostgresLoadConfig loads configuration from POSTGRES_* environment variables
// using an isolated viper instance (so it does not pollute global viper state).
func PostgresLoadConfig() (*PostgresConfig, error) {
	v := viper.New()
	v.SetEnvPrefix("POSTGRES")

	binds := map[string]string{
		"pghostname":             "POSTGRES_HOSTNAME",
		"pgusername":             "POSTGRES_USERNAME",
		"pgpassword":             "POSTGRES_PASSWORD",
		"pgdatabase":             "POSTGRES_DATABASE",
		"pgport":                 "POSTGRES_PORT",
		"pgsslmode":              "POSTGRES_SSLMODE",
		"pgsslrootcert":          "POSTGRES_SSLROOTCERT",
		"maxconns":               "POSTGRES_MAX_CONNS",
		"minconns":               "POSTGRES_MIN_CONNS",
		"connmaxlifetime":        "POSTGRES_CONN_MAX_LIFETIME",
		"statementtimeout":       "POSTGRES_STATEMENT_TIMEOUT",
		"locktimeout":            "POSTGRES_LOCK_TIMEOUT",
		"idleintxsessiontimeout": "POSTGRES_IDLE_IN_TX_SESSION_TIMEOUT",
	}
	for key, env := range binds {
		if err := v.BindEnv(key, env); err != nil {
			return nil, fmt.Errorf("failed to bind environment variable %s: %w", env, err)
		}
	}

	v.AutomaticEnv()
	return PostgresLoadConfigFromViper(v)
}

// PostgresLoadConfigFromViper loads configuration from a caller-provided viper
// instance. The caller owns the instance: this function does not call
// BindEnv/AutomaticEnv/SetEnvPrefix on it, so callers may pre-populate values
// with v.Set(...) (e.g. to inject secrets or test values) without touching the
// process environment. Defaults for optional fields (pgport, pgsslmode) are
// applied via SetDefault and only take effect if the key is unset.
func PostgresLoadConfigFromViper(v *viper.Viper) (*PostgresConfig, error) {
	if v == nil {
		return nil, ErrNilViper
	}

	v.SetDefault("pgport", defaultPort)
	v.SetDefault("pgsslmode", defaultSSLMode)

	var cfg PostgresConfig
	if err := v.Unmarshal(&cfg, viper.DecodeHook(
		mapstructure.StringToTimeDurationHookFunc(),
	)); err != nil {
		return nil, fmt.Errorf("unable to decode postgres config: %w", err)
	}

	if err := PostgresValidateConfig(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// PostgresValidateConfig checks that all required connection settings are
// present and that sslmode is a recognised value.
func PostgresValidateConfig(cfg *PostgresConfig) error {
	if cfg == nil {
		return errors.New("postgres config cannot be nil")
	}
	if cfg.PgHostname == "" {
		return errors.New("postgres hostname is required")
	}
	if cfg.PgUsername == "" {
		return errors.New("postgres username is required")
	}
	if cfg.PgPassword == "" {
		return errors.New("postgres password is required")
	}
	if cfg.PgDatabase == "" {
		return errors.New("postgres database is required")
	}
	if cfg.PgPort == "" {
		return errors.New("postgres port is required (should default to 5432)")
	}
	if cfg.PgSSLMode == "" {
		return errors.New("postgres sslmode is required (should default to verify-full)")
	}
	if _, ok := validSSLModes[cfg.PgSSLMode]; !ok {
		return fmt.Errorf(
			"postgres sslmode %q is invalid (want disable/allow/prefer/require/verify-ca/verify-full)",
			cfg.PgSSLMode,
		)
	}
	return nil
}
