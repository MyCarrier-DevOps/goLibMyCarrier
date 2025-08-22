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
	TopicPrefix        string `mapstructure:"topic_prefix"`
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
		return nil, fmt.Errorf("error binding KAFKA_ADDRESS: %w", err)
	}
	prefixerr := viper.BindEnv("topic_prefix", "KAFKA_TOPIC_PREFIX")
	topicerr := viper.BindEnv("topic", "KAFKA_TOPIC")
	if prefixerr != nil && topicerr != nil {
		return nil, fmt.Errorf(`error binding KAFKA_TOPIC or KAFKA_TOPIC_PREFIX. 
			Prefix error: %w, Topic error: %w`, prefixerr, topicerr)
	}
	if err := viper.BindEnv("username", "KAFKA_USERNAME"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_USERNAME: %w", err)
	}
	if err := viper.BindEnv("password", "KAFKA_PASSWORD"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_PASSWORD: %w", err)
	}
	if err := viper.BindEnv("groupid", "KAFKA_GROUPID"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_GROUPID: %w", err)
	}
	if err := viper.BindEnv("partition", "KAFKA_PARTITION"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_PARTITION: %w", err)
	}
	if err := viper.BindEnv("insecure_skip_verify", "KAFKA_INSECURE_SKIP_VERIFY"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_INSECURE_SKIP_VERIFY: %w", err)
	}

	// Read environment variables
	viper.AutomaticEnv()

	var kafkaConfig KafkaConfig

	// Unmarshal environment variables into the Config struct
	if err := viper.Unmarshal(&kafkaConfig); err != nil {
		return nil, fmt.Errorf("unable to decode into struct, %w", err)
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
	if kafkaConfig.Topic == "" && kafkaConfig.TopicPrefix == "" {
		return fmt.Errorf("kafka topic or topic prefix is required")
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

// getTopicName returns the appropriate topic name based on configuration.
// If Topic is set, it returns the Topic directly.
// If TopicPrefix is set and Topic is empty, it concatenates TopicPrefix with the separator and suffix.
// If TopicPrefix is set but no suffix is provided, it returns an error.
func getTopicName(kafkacfg *KafkaConfig, suffix string, separator string) (string, error) {
	// Use default separator if not provided
	if separator == "" {
		separator = "."
	}

	// If Topic is set, return it directly
	if kafkacfg.Topic != "" {
		return kafkacfg.Topic, nil
	}

	// If TopicPrefix is set but Topic is not
	if kafkacfg.TopicPrefix != "" {
		if suffix == "" {
			return "", fmt.Errorf("suffix is required when using topic prefix")
		}
		return kafkacfg.TopicPrefix + separator + suffix, nil
	}

	return "", fmt.Errorf("either topic or topic prefix must be configured")
}

// InitializeKafkaReader initializes a Kafka reader with the provided configuration.
func InitializeKafkaReader(kafkacfg *KafkaConfig, separator string, suffix ...string) (*kafka.Reader, error) {
	mechanism, mechErr := scram.Mechanism(scram.SHA512, kafkacfg.Username, kafkacfg.Password)
	if mechErr != nil {
		return nil, fmt.Errorf("error creating sasl mechanism: %w", mechErr)
	}

	dialer := &kafka.Dialer{
		SASLMechanism: mechanism,
		TLS: &tls.Config{
			InsecureSkipVerify: bool(kafkacfg.InsecureSkipVerify == "true"),
		},
	}
	topicSuffix := ""
	if len(suffix) > 0 {
		topicSuffix = suffix[0]
	}
	// Create a new Kafka reader
	topicName, err := getTopicName(kafkacfg, topicSuffix, separator)
	if err != nil {
		return nil, fmt.Errorf("error determining topic name: %w", err)
	}
	readerConfig := kafka.ReaderConfig{
		Brokers:     []string{kafkacfg.Address},
		Topic:       topicName,
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
			return nil, fmt.Errorf("invalid partition value: %w", err)
		}
		readerConfig.Partition = partition
	}

	reader := kafka.NewReader(readerConfig)
	return reader, nil
}

// InitializeKafkaWriter initializes a Kafka writer with the provided configuration and optional topic suffix.
// If KAFKA_TOPIC is set, the suffix is ignored and the topic is used directly.
// If KAFKA_TOPIC_PREFIX is set and KAFKA_TOPIC is not set, the topic will be KAFKA_TOPIC_PREFIX + "." + suffix.
// The suffix parameter is optional - pass an empty string or omit it for backward compatibility.
func InitializeKafkaWriter(kafkacfg *KafkaConfig, separator string, suffix ...string) (*kafka.Writer, error) {
	// Initialize Kafka writer
	mechanism, err := scram.Mechanism(scram.SHA512, kafkacfg.Username, kafkacfg.Password)
	if err != nil {
		return nil, fmt.Errorf("error creating SASL mechanism: %w", err)
	}

	// Determine suffix - use empty string if not provided
	topicSuffix := ""
	if len(suffix) > 0 {
		topicSuffix = suffix[0]
	}

	// Determine topic name based on configuration and suffix
	topicName, err := getTopicName(kafkacfg, topicSuffix, separator)
	if err != nil {
		return nil, fmt.Errorf("error determining topic name: %w", err)
	}

	dialer := &kafka.Dialer{
		SASLMechanism: mechanism,
		TLS: &tls.Config{
			InsecureSkipVerify: bool(kafkacfg.InsecureSkipVerify == "true"),
		},
	}

	writer := kafka.NewWriter(kafka.WriterConfig{
		Brokers:     []string{kafkacfg.Address},
		Topic:       topicName,
		Dialer:      dialer,
		Balancer:    &kafka.LeastBytes{},
		Async:       true,
		MaxAttempts: 5,
	})

	return writer, nil
}
