package kafka

import (
	"crypto/tls"
	"fmt"

	"strconv"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/scram"
	"github.com/spf13/viper"
)

// KafkaConfig represents the configuration for Kafka.
type KafkaConfig struct {
	Address            string `mapstructure:"address"`
	Topic              string `mapstructure:"topic"`
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	GroupID            string `mapstructure:"groupid"`
	Partition          string `mapstructure:"partition"`
	InsecureSkipVerify string `mapstructure:"insecure_skip_verify"`
}

// LoadConfig loads the configuration from environment variables using Viper.
func LoadConfig() (*KafkaConfig, error) {
	// Bind environment variables
	if err := viper.BindEnv("address", "KAFKA_ADDRESS"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_ADDRESS: %v", err.Error())
	}
	if err := viper.BindEnv("topic", "KAFKA_TOPIC"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_TOPIC: %v", err.Error())
	}
	if err := viper.BindEnv("username", "KAFKA_USERNAME"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_USERNAME: %v", err.Error())
	}
	if err := viper.BindEnv("password", "KAFKA_PASSWORD"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_PASSWORD: %v", err.Error())
	}
	if err := viper.BindEnv("groupid", "KAFKA_GROUPID"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_GROUPID: %v", err.Error())
	}
	if err := viper.BindEnv("partition", "KAFKA_PARTITION"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_PARTITION: %v", err.Error())
	}
	if err := viper.BindEnv("insecure_skip_verify", "KAFKA_INSECURE_SKIP_VERIFY"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_INSECURE_SKIP_VERIFY: %v", err.Error())
	}

	// Read environment variables
	viper.AutomaticEnv()

	var kafkaConfig KafkaConfig

	// Unmarshal environment variables into the Config struct
	if err := viper.Unmarshal(&kafkaConfig); err != nil {
		return nil, fmt.Errorf("unable to decode into struct, %v", err.Error())
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
	if kafkaConfig.Partition == "" {
		kafkaConfig.Partition = "0"
	} else {
		if _, err := strconv.Atoi(kafkaConfig.Partition); err != nil {
			return fmt.Errorf("kafka partition must be a valid numeric value")
		}
	}
	if kafkaConfig.InsecureSkipVerify == "" {
		kafkaConfig.InsecureSkipVerify = "false"
	} else if kafkaConfig.InsecureSkipVerify != "true" && kafkaConfig.InsecureSkipVerify != "false" {
		return fmt.Errorf("kafka insecure_skip_verify must be true or false")
	}
	return nil
}

func InitializeKafkaReader(kafkacfg *KafkaConfig) (*kafka.Reader, error) {
	mechanism, mech_err := scram.Mechanism(scram.SHA512, kafkacfg.Username, kafkacfg.Password)
	if mech_err != nil {
		return nil, fmt.Errorf("error creating sasl mechanism: %v", mech_err.Error())
	}

	dialer := &kafka.Dialer{
		SASLMechanism: mechanism,
		TLS: &tls.Config{
			InsecureSkipVerify: bool(kafkacfg.InsecureSkipVerify == "true"),
		},
	}

	// Create a new Kafka reader
	readerConfig := kafka.ReaderConfig{
		Brokers:     []string{kafkacfg.Address},
		Topic:       kafkacfg.Topic,
		GroupID:     kafkacfg.GroupID,
		MinBytes:    1,    // 1 Byte
		MaxBytes:    10e6, // 10MB
		StartOffset: kafka.FirstOffset,
		Dialer:      dialer,
		MaxAttempts: 5,
	}

	// Set Partition based on GroupID presence
	if kafkacfg.GroupID == "" {
		partition, err := strconv.Atoi(kafkacfg.Partition)
		if err != nil {
			return nil, fmt.Errorf("invalid partition value: %v", err.Error())
		}
		readerConfig.Partition = partition
	}

	reader := kafka.NewReader(readerConfig)
	return reader, nil
}

// InitializeKafkaWriter initializes a Kafka writer with the provided configuration.
func InitializeKafkaWriter(kafkacfg *KafkaConfig) (*kafka.Writer, error) {
	// Initialize Kafka writer
	mechanism, err := scram.Mechanism(scram.SHA512, kafkacfg.Username, kafkacfg.Password)
	if err != nil {
		return nil, fmt.Errorf("error creating SASL mechanism: %v", err.Error())
	}

	dialer := &kafka.Dialer{
		SASLMechanism: mechanism,
		TLS: &tls.Config{
			InsecureSkipVerify: bool(kafkacfg.InsecureSkipVerify == "true"),
		},
	}

	writer := kafka.NewWriter(kafka.WriterConfig{
		Brokers:     []string{kafkacfg.Address},
		Topic:       kafkacfg.Topic,
		Dialer:      dialer,
		Balancer:    &kafka.LeastBytes{},
		Async:       true,
		MaxAttempts: 5,
	})

	return writer, nil
}
