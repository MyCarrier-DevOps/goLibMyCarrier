package config

import (
	"fmt"

	"github.com/spf13/viper"
)

// KafkaConfig represents the configuration for Kafka.
type KafkaConfig struct {
	Address  string `mapstructure:"address"`
	Topic    string `mapstructure:"topic"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	GroupID  string `mapstructure:"groupid"`
}

// Config holds the application-wide configuration.
type Config struct {
	Kafka    KafkaConfig `mapstructure:"kafka"`
	ApiKey   string      `mapstructure:"apikey"`
	Loglevel string      `mapstructure:"loglevel"`
}

// LoadConfig loads the configuration from environment variables using Viper.
func LoadConfig() (*Config, error) {
	viper.SetEnvPrefix("APP") // Set environment variable prefix (e.g., APP_KAFKA_ADDRESS)

	// Bind environment variables
	viper.BindEnv("kafka.address", "APP_KAFKA_ADDRESS")
	viper.BindEnv("kafka.topic", "APP_KAFKA_TOPIC")
	viper.BindEnv("kafka.username", "APP_KAFKA_USERNAME")
	viper.BindEnv("kafka.password", "APP_KAFKA_PASSWORD")
	viper.BindEnv("kafka.groupid", "APP_KAFKA_GROUPID")
	viper.BindEnv("apikey", "APP_APIKEY")
	viper.BindEnv("loglevel", "APP_LOG_LEVEL")

	// Read environment variables
	viper.AutomaticEnv()

	var config Config

	// Unmarshal environment variables into the Config struct
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("unable to decode into struct, %v", err)
	}

	// Validate the configuration
	if err := validateConfig(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

// validateConfig validates the loaded configuration.
func validateConfig(config *Config) error {
	if config.Kafka.Address == "" {
		return fmt.Errorf("kafka address is required")
	}
	if config.Kafka.Topic == "" {
		return fmt.Errorf("kafka topic is required")
	}
	if config.Kafka.Username == "" {
		return fmt.Errorf("kafka username is required")
	}
	if config.Kafka.Password == "" {
		return fmt.Errorf("kafka password is required")
	}
	if config.Kafka.GroupID == "" {
		return fmt.Errorf("kafka groupid is required")
	}
	if config.ApiKey == "" {
		return fmt.Errorf("apikey is required")
	}
	if config.Loglevel == "" {
		config.Loglevel = "info"
	}
	return nil
}
