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

type ClickhouseSession struct {
	conn       driver.Conn
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

// NewClickhouseSession creates a new Clickhouse session.
func NewClickhouseSession(ch *ClickhouseConfig, context context.Context) (*ClickhouseSession, error) {
	conn := &ClickhouseSession{
		db:         ch.ChDatabase,
		addr:       []string{ch.ChHostname + ":" + ch.ChPort},
		username:   ch.ChUsername,
		password:   ch.ChPassword,
		skipVerify: ch.ChSkipVerify == "true",
	}

	err := conn.Connect(ch, context)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// Connect to ClickHouse database
func (chsession *ClickhouseSession) Connect(ch *ClickhouseConfig, context context.Context) error {
	var (
		ctx       = context
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
func (ch *ClickhouseSession) Query(ctx context.Context, query string) (driver.Rows, error) {
	rows, err := ch.conn.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// Exec ClickHouse query
func (ch *ClickhouseSession) Exec(ctx context.Context, stmt string) error {
	err := ch.conn.Exec(ctx, stmt)
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
func (ch *ClickhouseSession) Conn() driver.Conn {
	return ch.conn
}
