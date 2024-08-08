package clickhouse

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"

	"github.com/spf13/viper"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

type ClickhouseConfig struct {
	ChHostname string `mapstructure:"chhostname"`
	ChUsername string `mapstructure:"chusername"`
	ChPassword string `mapstructure:"chpassword"`
	ChDatabase string `mapstructure:"chdatabase"`
}

// LoadConfig loads the configuration from environment variables using Viper.
func ClickhouseLoadConfig() (*ClickhouseConfig, error) {
	viper.SetEnvPrefix("APP")

	// Bind environment variables
	viper.BindEnv("chhostname", "APP_CLICKHOUSE_HOSTNAME")
	viper.BindEnv("chusername", "APP_CLICKHOUSE_USERNAME")
	viper.BindEnv("chpassword", "APP_CLICKHOUSE_PASSWORD")
	viper.BindEnv("chdatabase", "APP_CLICKHOUSE_DATABASE")

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
		return fmt.Errorf("Clickhouse Hostname is required")
	}
	if ClickhouseConfig.ChUsername == "" {
		return fmt.Errorf("Clickhouse Username is required")
	}
	if ClickhouseConfig.ChPassword == "" {
		return fmt.Errorf("Clickhouse Password is required")
	}
	if ClickhouseConfig.ChDatabase == "" {
		return fmt.Errorf("Clickhouse Database is required")
	}
	return nil
}

func ClickhouseConnect(ctx context.Context) (driver.Conn, error) {
	chConfig, config_err := ClickhouseLoadConfig()
	if config_err != nil {
		return nil, fmt.Errorf("Unable to load config, %v", config_err)
	}
	log.Printf("Clickhouse Configuration loaded successfully: %s", chConfig.ChHostname)

	var (
		conn, err = clickhouse.Open(&clickhouse.Options{
			Addr: []string{chConfig.ChHostname},
			Auth: clickhouse.Auth{
				Database: chConfig.ChDatabase,
				Username: chConfig.ChUsername,
				Password: chConfig.ChPassword,
			},
			ClientInfo: clickhouse.ClientInfo{
				Products: []struct {
					Name    string
					Version string
				}{
					{Name: "teamsbotworker", Version: "0.1"},
				},
			},

			Debugf: func(format string, v ...interface{}) {
				fmt.Printf(format, v)
			},
			TLS: &tls.Config{
				InsecureSkipVerify: true,
			},
		})
	)

	if err != nil {
		return nil, err
	}

	if err := conn.Ping(ctx); err != nil {
		if exception, ok := err.(*clickhouse.Exception); ok {
			fmt.Printf("Exception [%d] %s \n%s\n", exception.Code, exception.Message, exception.StackTrace)
		}
		return nil, err
	}
	return conn, nil
}
