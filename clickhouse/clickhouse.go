package clickhouse

import (
	"context"
	"crypto/tls"
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
	viper.BindEnv("chhostname", "CLICKHOUSE_HOSTNAME")
	viper.BindEnv("chusername", "CLICKHOUSE_USERNAME")
	viper.BindEnv("chpassword", "CLICKHOUSE_PASSWORD")
	viper.BindEnv("chdatabase", "CLICKHOUSE_DATABASE")
	viper.BindEnv("chskipverify", "CLICKHOUSE_SKIP_VERIFY")
	viper.BindEnv("chport", "CLICKHOUSE_PORT")

	// Read environment variables
	viper.AutomaticEnv()

	var ClickhouseConfig ClickhouseConfig

	// Unmarshal environment variables into the Config struct
	if err := viper.Unmarshal(&ClickhouseConfig); err != nil {
		return nil, fmt.Errorf("unable to decode into struct, %v", err)
	}

	// Validate the configuration
	if err := ClickhouseValidateConfig(&ClickhouseConfig); err != nil {
		return nil, err
	}

	return &ClickhouseConfig, nil
}

// validateConfig validates the loaded configuration.
func ClickhouseValidateConfig(ClickhouseConfig *ClickhouseConfig) error {
	if ClickhouseConfig.ChHostname == "" {
		return fmt.Errorf("clickhouse hostname is required")
	}
	if ClickhouseConfig.ChUsername == "" {
		return fmt.Errorf("clickhouse username is required")
	}
	if ClickhouseConfig.ChPassword == "" {
		return fmt.Errorf("clickhouse password is required")
	}
	if ClickhouseConfig.ChDatabase == "" {
		return fmt.Errorf("clickhouse database is required")
	}
	if ClickhouseConfig.ChPort == "" {
		return fmt.Errorf("clickhouse port is required")
	}
	if ClickhouseConfig.ChSkipVerify == "" {
		return fmt.Errorf("clickhouse skip verify is required")
	}
	if ClickhouseConfig.ChSkipVerify != "true" && ClickhouseConfig.ChSkipVerify != "false" {
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
		return fmt.Errorf("error connecting to ClickHouse: %v", err)
	}

	if err := conn.Ping(ctx); err != nil {
		if exception, ok := err.(*clickhouse.Exception); ok {
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

// return ClickHouse connection
func (ch *ClickhouseSession) Conn() driver.Conn {
	return ch.conn
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
