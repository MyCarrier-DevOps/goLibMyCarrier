package kafka

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

// LoadConfig loads the configuration from environment variables using Viper.
func LoadConfig() (*KafkaConfig, error) {
	viper.SetEnvPrefix("APP") // Set environment variable prefix (e.g., APP_KAFKA_ADDRESS)

	// Bind environment variables
	viper.BindEnv("kafka.address", "APP_KAFKA_ADDRESS")
	viper.BindEnv("kafka.topic", "APP_KAFKA_TOPIC")
	viper.BindEnv("kafka.username", "APP_KAFKA_USERNAME")
	viper.BindEnv("kafka.password", "APP_KAFKA_PASSWORD")
	viper.BindEnv("kafka.groupid", "APP_KAFKA_GROUPID")

	// Read environment variables
	viper.AutomaticEnv()

	var kafkaConfig KafkaConfig

	// Unmarshal environment variables into the Config struct
	if err := viper.Unmarshal(&kafkaConfig); err != nil {
		return nil, fmt.Errorf("unable to decode into struct, %v", err)
	}

	// Validate the configuration
	if err := validateConfig(&kafkaConfig); err != nil {
		return nil, err
	}

	return &kafkaConfig, nil
}

// validateConfig validates the loaded configuration.
func validateConfig(kafkaConfig *KafkaConfig) error {
	if kafkaConfig.Address == "" {
		return fmt.Errorf("kafka address is required")
	}
	if kafkaConfig.Topic == "" {
		return fmt.Errorf("kafka topic is required")
	}
	if kafkaConfig.Username == "" {
		return fmt.Errorf("kafka username is required")
	}
	if kafkaConfig.Password == "" {
		return fmt.Errorf("kafka password is required")
	}
	if kafkaConfig.GroupID == "" {
		kafkaConfig.GroupID = "default-group"
	}
	return nil
}
