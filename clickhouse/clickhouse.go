package clickhouse

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"

	"github.com/spf13/viper"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Re-export driver types so consumers don't need to import the driver package directly.
// This provides a clean abstraction layer over the underlying ClickHouse driver.
type (
	// Conn is the ClickHouse connection type
	Conn = driver.Conn
	// Rows represents query result rows
	Rows = driver.Rows
	// Row represents a single query result row
	Row = driver.Row
	// Options is the ClickHouse connection options type
	Options = clickhouse.Options
)

// Named creates a named parameter for use in queries.
// Re-exported from clickhouse-go for convenience.
func Named(name string, value interface{}) driver.NamedValue {
	return clickhouse.Named(name, value)
}

// ClickhouseSessionInterface defines the interface for ClickHouse session operations
type ClickhouseSessionInterface interface {
	Connect(ch *ClickhouseConfig, ctx context.Context) error
	Query(ctx context.Context, query string) (Rows, error)
	QueryWithArgs(ctx context.Context, query string, args ...interface{}) (Rows, error)
	QueryRow(ctx context.Context, query string, args ...interface{}) Row
	Exec(ctx context.Context, stmt string) error
	ExecWithArgs(ctx context.Context, stmt string, args ...interface{}) error
	Close() error
	Conn() Conn
}

// ConfigLoaderInterface defines the interface for configuration loading
type ConfigLoaderInterface interface {
	LoadConfig() (*ClickhouseConfig, error)
}

// ConfigValidatorInterface defines the interface for configuration validation
type ConfigValidatorInterface interface {
	ValidateConfig(config *ClickhouseConfig) error
}

// SessionFactoryInterface defines the interface for creating ClickHouse sessions
type SessionFactoryInterface interface {
	NewSession(ch *ClickhouseConfig, ctx context.Context) (ClickhouseSessionInterface, error)
}

// ClickhouseSession implements ClickhouseSessionInterface
type ClickhouseSession struct {
	conn       Conn
	db         string
	addr       []string
	username   string
	password   string
	skipVerify bool
}

type ClickhouseConfig struct {
	ChHostname   string `mapstructure:"chhostname"`
	ChUsername   string `mapstructure:"chusername"`
	ChPassword   string `mapstructure:"chpassword"`
	ChDatabase   string `mapstructure:"chdatabase"`
	ChSkipVerify string `mapstructure:"chskipverify"`
	ChPort       string `mapstructure:"chport"`
}

// LoadConfig loads the configuration from environment variables using Viper.
func ClickhouseLoadConfig() (*ClickhouseConfig, error) {
	viper.SetEnvPrefix("CLICKHOUSE")

	// Bind environment variables
	if err := viper.BindEnv("chhostname", "CLICKHOUSE_HOSTNAME"); err != nil {
		return nil, fmt.Errorf("failed to bind environment variable for chhostname: %w", err)
	}
	if err := viper.BindEnv("chusername", "CLICKHOUSE_USERNAME"); err != nil {
		return nil, fmt.Errorf("failed to bind environment variable for chusername: %w", err)
	}
	if err := viper.BindEnv("chpassword", "CLICKHOUSE_PASSWORD"); err != nil {
		return nil, fmt.Errorf("failed to bind environment variable for chpassword: %w", err)
	}
	if err := viper.BindEnv("chdatabase", "CLICKHOUSE_DATABASE"); err != nil {
		return nil, fmt.Errorf("failed to bind environment variable for chdatabase: %w", err)
	}
	if err := viper.BindEnv("chskipverify", "CLICKHOUSE_SKIP_VERIFY"); err != nil {
		return nil, fmt.Errorf("failed to bind environment variable for chskipverify: %w", err)
	}
	if err := viper.BindEnv("chport", "CLICKHOUSE_PORT"); err != nil {
		return nil, fmt.Errorf("failed to bind environment variable for chport: %w", err)
	}

	// Read environment variables
	viper.AutomaticEnv()

	var ClickhouseConfig ClickhouseConfig

	// Unmarshal environment variables into the Config struct
	if err := viper.Unmarshal(&ClickhouseConfig); err != nil {
		return nil, fmt.Errorf("unable to decode into struct, %w", err)
	}

	// Validate the configuration
	if err := ClickhouseValidateConfig(&ClickhouseConfig); err != nil {
		return nil, err
	}

	return &ClickhouseConfig, nil
}

// validateConfig validates the loaded configuration.
func ClickhouseValidateConfig(clickhouseConfig *ClickhouseConfig) error {
	if clickhouseConfig.ChHostname == "" {
		return fmt.Errorf("clickhouse hostname is required")
	}
	if clickhouseConfig.ChUsername == "" {
		return fmt.Errorf("clickhouse username is required")
	}
	if clickhouseConfig.ChPassword == "" {
		return fmt.Errorf("clickhouse password is required")
	}
	if clickhouseConfig.ChDatabase == "" {
		return fmt.Errorf("clickhouse database is required")
	}
	if clickhouseConfig.ChPort == "" {
		return fmt.Errorf("clickhouse port is required")
	}
	if clickhouseConfig.ChSkipVerify == "" {
		return fmt.Errorf("clickhouse skip verify is required")
	}
	if clickhouseConfig.ChSkipVerify != "true" && clickhouseConfig.ChSkipVerify != "false" {
		return fmt.Errorf("clickhouse skip verify must be true or false")
	}
	return nil
}

// DefaultConfigLoader implements ConfigLoaderInterface
type DefaultConfigLoader struct{}

// LoadConfig implements ConfigLoaderInterface
func (c *DefaultConfigLoader) LoadConfig() (*ClickhouseConfig, error) {
	return ClickhouseLoadConfig()
}

// DefaultConfigValidator implements ConfigValidatorInterface
type DefaultConfigValidator struct{}

// ValidateConfig implements ConfigValidatorInterface
func (v *DefaultConfigValidator) ValidateConfig(config *ClickhouseConfig) error {
	return ClickhouseValidateConfig(config)
}

// DefaultSessionFactory implements SessionFactoryInterface
type DefaultSessionFactory struct{}

// NewSession implements SessionFactoryInterface
func (f *DefaultSessionFactory) NewSession(
	ch *ClickhouseConfig,
	ctx context.Context,
) (ClickhouseSessionInterface, error) {
	return NewClickhouseSession(ch, ctx)
}

// NewClickhouseSession creates a new Clickhouse session.
func NewClickhouseSession(ch *ClickhouseConfig, ctx context.Context) (*ClickhouseSession, error) {
	if ch == nil {
		return nil, fmt.Errorf("clickhouse config cannot be nil")
	}

	conn := &ClickhouseSession{
		db:         ch.ChDatabase,
		addr:       []string{ch.ChHostname + ":" + ch.ChPort},
		username:   ch.ChUsername,
		password:   ch.ChPassword,
		skipVerify: ch.ChSkipVerify == "true",
	}

	err := conn.Connect(ch, ctx)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// Connect to ClickHouse database
func (chsession *ClickhouseSession) Connect(ch *ClickhouseConfig, ctx context.Context) error {
	if ch == nil {
		return fmt.Errorf("clickhouse config cannot be nil")
	}
	if ctx == nil {
		return fmt.Errorf("context cannot be nil")
	}

	var (
		conn, err = clickhouse.Open(&clickhouse.Options{
			Addr: []string{ch.ChHostname + ":" + ch.ChPort},
			Auth: clickhouse.Auth{
				Database: ch.ChDatabase,
				Username: ch.ChUsername,
				Password: ch.ChPassword,
			},

			TLS: &tls.Config{
				InsecureSkipVerify: false,
			},
		})
	)
	if err != nil {
		return fmt.Errorf("error connecting to ClickHouse: %w", err)
	}

	if err := conn.Ping(ctx); err != nil {
		var exception *clickhouse.Exception
		if errors.As(err, &exception) {
			return fmt.Errorf("exception [%d] %s %s", exception.Code, exception.Message, exception.StackTrace)
		}
		return err
	}

	chsession.conn = conn
	return nil
}

// Query ClickHouse database
func (ch *ClickhouseSession) Query(ctx context.Context, query string) (Rows, error) {
	if ch.conn == nil {
		return nil, fmt.Errorf("clickhouse connection is not established")
	}
	rows, err := ch.conn.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// QueryWithArgs executes a query with arguments and returns rows
func (ch *ClickhouseSession) QueryWithArgs(ctx context.Context, query string, args ...interface{}) (Rows, error) {
	if ch.conn == nil {
		return nil, fmt.Errorf("clickhouse connection is not established")
	}
	rows, err := ch.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// QueryRow executes a query with arguments and returns a single row
func (ch *ClickhouseSession) QueryRow(ctx context.Context, query string, args ...interface{}) Row {
	return ch.conn.QueryRow(ctx, query, args...)
}

// Exec ClickHouse query
func (ch *ClickhouseSession) Exec(ctx context.Context, stmt string) error {
	if ch.conn == nil {
		return fmt.Errorf("clickhouse connection is not established")
	}
	err := ch.conn.Exec(ctx, stmt)
	if err != nil {
		return err
	}
	return nil
}

// ExecWithArgs executes a statement with arguments
func (ch *ClickhouseSession) ExecWithArgs(ctx context.Context, stmt string, args ...interface{}) error {
	if ch.conn == nil {
		return fmt.Errorf("clickhouse connection is not established")
	}
	err := ch.conn.Exec(ctx, stmt, args...)
	if err != nil {
		return err
	}
	return nil
}

// Close ClickHouse connection
func (ch *ClickhouseSession) Close() error {
	if ch.conn != nil {
		if err := ch.conn.Close(); err != nil {
			return err
		}
	}
	return nil
}

// return ClickHouse connection
func (ch *ClickhouseSession) Conn() Conn {
	return ch.conn
}

// connWrapper wraps a Conn to implement ClickhouseSessionInterface.
// This is useful when you already have a connection and need to use
// code that expects a ClickhouseSessionInterface.
type connWrapper struct {
	conn Conn
}

// NewSessionFromConn creates a ClickhouseSessionInterface from an existing connection.
// This is useful for backward compatibility or when you already have an established connection.
func NewSessionFromConn(conn Conn) ClickhouseSessionInterface {
	return &connWrapper{conn: conn}
}

func (w *connWrapper) Connect(cfg *ClickhouseConfig, ctx context.Context) error {
	return nil // Already connected
}

func (w *connWrapper) Query(ctx context.Context, query string) (Rows, error) {
	return w.conn.Query(ctx, query)
}

func (w *connWrapper) QueryWithArgs(ctx context.Context, query string, args ...interface{}) (Rows, error) {
	return w.conn.Query(ctx, query, args...)
}

func (w *connWrapper) QueryRow(ctx context.Context, query string, args ...interface{}) Row {
	return w.conn.QueryRow(ctx, query, args...)
}

func (w *connWrapper) Exec(ctx context.Context, stmt string) error {
	return w.conn.Exec(ctx, stmt)
}

func (w *connWrapper) ExecWithArgs(ctx context.Context, stmt string, args ...interface{}) error {
	return w.conn.Exec(ctx, stmt, args...)
}

func (w *connWrapper) Close() error {
	return w.conn.Close()
}

func (w *connWrapper) Conn() Conn {
	return w.conn
}

// Ensure connWrapper implements ClickhouseSessionInterface.
var _ ClickhouseSessionInterface = (*connWrapper)(nil)
