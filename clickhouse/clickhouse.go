package clickhouse

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

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

// DefaultRetryIntervals are the default retry intervals for production use.
// This can be overridden in tests for faster execution.
var DefaultRetryIntervals = []time.Duration{2 * time.Second, 3 * time.Second, 5 * time.Second}

// isTransientError determines whether a ClickHouse error is transient (and thus
// worth retrying) or permanent (e.g. syntax error, authentication failure).
// Server-side exceptions (clickhouse.Exception) are treated as non-transient
// since they indicate query or configuration problems that won't resolve on retry.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	var exception *clickhouse.Exception
	if errors.As(err, &exception) {
		// Server-side exceptions are deterministic and won't resolve on retry
		return false
	}
	// Everything else (network errors, timeouts, EOF) is transient
	return true
}

// retryOperation retries a clickhouse operation with exponential backoff.
// Only transient errors (network issues, timeouts) are retried.
// Non-transient errors (server exceptions like syntax/auth errors) fail immediately.
// Tests can override DefaultRetryIntervals for faster execution.
func retryOperation(ctx context.Context, operation func() error) error {
	backoffIntervals := DefaultRetryIntervals

	// First attempt
	err := operation()
	if err == nil {
		return nil
	}
	// Don't retry non-transient errors (e.g. SQL syntax errors, auth failures)
	if !isTransientError(err) {
		return err
	}

	// Retry with backoff for transient errors only
	for i, interval := range backoffIntervals {
		select {
		case <-ctx.Done():
			return fmt.Errorf("operation cancelled after %d retries: %w", i, ctx.Err())
		case <-time.After(interval):
			err = operation()
			if err == nil {
				return nil
			}
			if !isTransientError(err) {
				return err
			}
		}
	}

	return fmt.Errorf("operation failed after %d retries: %w", len(backoffIntervals), err)
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
	Hostname   string `mapstructure:"chhostname"`
	Username   string `mapstructure:"chusername"`
	Password   string `mapstructure:"chpassword"`
	Database   string `mapstructure:"chdatabase"`
	SkipVerify string `mapstructure:"chskipverify"`
	Port       string `mapstructure:"chport"`
}

// LoadConfig loads the configuration from environment variables using Viper.
// Uses an isolated viper instance to avoid global state pollution that could
// affect other packages using viper with different env prefixes.
func ClickhouseLoadConfig() (*ClickhouseConfig, error) {
	v := viper.New() // Isolated instance - does not affect global viper state
	v.SetEnvPrefix("CLICKHOUSE")

	// Bind environment variables
	if err := v.BindEnv("chhostname", "CLICKHOUSE_HOSTNAME"); err != nil {
		return nil, fmt.Errorf("failed to bind environment variable for chhostname: %w", err)
	}
	if err := v.BindEnv("chusername", "CLICKHOUSE_USERNAME"); err != nil {
		return nil, fmt.Errorf("failed to bind environment variable for chusername: %w", err)
	}
	if err := v.BindEnv("chpassword", "CLICKHOUSE_PASSWORD"); err != nil {
		return nil, fmt.Errorf("failed to bind environment variable for chpassword: %w", err)
	}
	if err := v.BindEnv("chdatabase", "CLICKHOUSE_DATABASE"); err != nil {
		return nil, fmt.Errorf("failed to bind environment variable for chdatabase: %w", err)
	}
	if err := v.BindEnv("chskipverify", "CLICKHOUSE_SKIP_VERIFY"); err != nil {
		return nil, fmt.Errorf("failed to bind environment variable for chskipverify: %w", err)
	}
	if err := v.BindEnv("chport", "CLICKHOUSE_PORT"); err != nil {
		return nil, fmt.Errorf("failed to bind environment variable for chport: %w", err)
	}

	// Set defaults for optional fields
	v.SetDefault("chport", "9440")
	v.SetDefault("chskipverify", "false")

	// Read environment variables
	v.AutomaticEnv()

	var ClickhouseConfig ClickhouseConfig

	// Unmarshal environment variables into the Config struct
	if err := v.Unmarshal(&ClickhouseConfig); err != nil {
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
	if clickhouseConfig.Hostname == "" {
		return fmt.Errorf("clickhouse hostname is required")
	}
	if clickhouseConfig.Username == "" {
		return fmt.Errorf("clickhouse username is required")
	}
	if clickhouseConfig.Password == "" {
		return fmt.Errorf("clickhouse password is required")
	}
	if clickhouseConfig.Database == "" {
		return fmt.Errorf("clickhouse database is required")
	}
	// Port and SkipVerify have defaults, so they should always be set
	// But validate they have reasonable values if present
	if clickhouseConfig.Port == "" {
		return fmt.Errorf("clickhouse port is required (should default to 9440)")
	}
	if clickhouseConfig.SkipVerify == "" {
		return fmt.Errorf("clickhouse skip verify is required (should default to false)")
	}
	if clickhouseConfig.SkipVerify != "true" && clickhouseConfig.SkipVerify != "false" {
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
		db:         ch.Database,
		addr:       []string{ch.Hostname + ":" + ch.Port},
		username:   ch.Username,
		password:   ch.Password,
		skipVerify: ch.SkipVerify == "true",
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

	return retryOperation(ctx, func() error {
		conn, err := clickhouse.Open(&clickhouse.Options{
			Addr: []string{ch.Hostname + ":" + ch.Port},
			Auth: clickhouse.Auth{
				Database: ch.Database,
				Username: ch.Username,
				Password: ch.Password,
			},

			TLS: &tls.Config{
				InsecureSkipVerify: chsession.skipVerify,
			},
		})
		if err != nil {
			return fmt.Errorf("error connecting to ClickHouse: %w", err)
		}

		if err := conn.Ping(ctx); err != nil {
			var exception *clickhouse.Exception
			if errors.As(err, &exception) {
				return fmt.Errorf(
					"exception [%d] %s %s: %w",
					exception.Code,
					exception.Message,
					exception.StackTrace,
					exception,
				)
			}
			return err
		}

		chsession.conn = conn
		return nil
	})
}

// Query ClickHouse database
func (ch *ClickhouseSession) Query(ctx context.Context, query string) (Rows, error) {
	if ch.conn == nil {
		return nil, fmt.Errorf("clickhouse connection is not established")
	}
	var rows Rows
	err := retryOperation(ctx, func() error {
		var queryErr error
		rows, queryErr = ch.conn.Query(ctx, query)
		return queryErr
	})
	return rows, err
}

// QueryWithArgs executes a query with arguments and returns rows
func (ch *ClickhouseSession) QueryWithArgs(ctx context.Context, query string, args ...interface{}) (Rows, error) {
	if ch.conn == nil {
		return nil, fmt.Errorf("clickhouse connection is not established")
	}
	var rows Rows
	err := retryOperation(ctx, func() error {
		var queryErr error
		rows, queryErr = ch.conn.Query(ctx, query, args...)
		return queryErr
	})
	return rows, err
}

// QueryRow executes a query with arguments and returns a single row
// Note: QueryRow doesn't return an error directly; errors are deferred until Scan() is called.
// Therefore, retry logic is not applicable here.
func (ch *ClickhouseSession) QueryRow(ctx context.Context, query string, args ...interface{}) Row {
	return ch.conn.QueryRow(ctx, query, args...)
}

// Exec ClickHouse query
func (ch *ClickhouseSession) Exec(ctx context.Context, stmt string) error {
	if ch.conn == nil {
		return fmt.Errorf("clickhouse connection is not established")
	}
	return retryOperation(ctx, func() error {
		return ch.conn.Exec(ctx, stmt)
	})
}

// ExecWithArgs executes a statement with arguments
func (ch *ClickhouseSession) ExecWithArgs(ctx context.Context, stmt string, args ...interface{}) error {
	if ch.conn == nil {
		return fmt.Errorf("clickhouse connection is not established")
	}
	return retryOperation(ctx, func() error {
		return ch.conn.Exec(ctx, stmt, args...)
	})
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
	var rows Rows
	err := retryOperation(ctx, func() error {
		var queryErr error
		rows, queryErr = w.conn.Query(ctx, query)
		return queryErr
	})
	return rows, err
}

func (w *connWrapper) QueryWithArgs(ctx context.Context, query string, args ...interface{}) (Rows, error) {
	var rows Rows
	err := retryOperation(ctx, func() error {
		var queryErr error
		rows, queryErr = w.conn.Query(ctx, query, args...)
		return queryErr
	})
	return rows, err
}

func (w *connWrapper) QueryRow(ctx context.Context, query string, args ...interface{}) Row {
	return w.conn.QueryRow(ctx, query, args...)
}

func (w *connWrapper) Exec(ctx context.Context, stmt string) error {
	return retryOperation(ctx, func() error {
		return w.conn.Exec(ctx, stmt)
	})
}

func (w *connWrapper) ExecWithArgs(ctx context.Context, stmt string, args ...interface{}) error {
	return retryOperation(ctx, func() error {
		return w.conn.Exec(ctx, stmt, args...)
	})
}

func (w *connWrapper) Close() error {
	return w.conn.Close()
}

func (w *connWrapper) Conn() Conn {
	return w.conn
}

// Ensure connWrapper implements ClickhouseSessionInterface.
var _ ClickhouseSessionInterface = (*connWrapper)(nil)
