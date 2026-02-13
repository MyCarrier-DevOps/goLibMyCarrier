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
	// StartOffset determines where a new consumer group starts reading.
	// Valid values: "first" (default, oldest messages) or "last" (newest messages only).
	// Only applies when GroupID is set and the consumer group has no committed offsets.
	StartOffset string `mapstructure:"start_offset"`
}

// LoadConfig loads the configuration from environment variables using Viper.
// An isolated viper instance is used to prevent cross-package state pollution.
func LoadConfig() (*KafkaConfig, error) {
	vp := viper.New()

	// Bind environment variables
	if err := vp.BindEnv("address", "KAFKA_ADDRESS"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_ADDRESS: %w", err)
	}
	prefixerr := vp.BindEnv("topic_prefix", "KAFKA_TOPIC_PREFIX")
	topicerr := vp.BindEnv("topic", "KAFKA_TOPIC")
	if prefixerr != nil && topicerr != nil {
		return nil, fmt.Errorf(`error binding KAFKA_TOPIC or KAFKA_TOPIC_PREFIX. 
			Prefix error: %w, Topic error: %w`, prefixerr, topicerr)
	}
	if err := vp.BindEnv("username", "KAFKA_USERNAME"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_USERNAME: %w", err)
	}
	if err := vp.BindEnv("password", "KAFKA_PASSWORD"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_PASSWORD: %w", err)
	}
	if err := vp.BindEnv("groupid", "KAFKA_GROUPID"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_GROUPID: %w", err)
	}
	if err := vp.BindEnv("partition", "KAFKA_PARTITION"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_PARTITION: %w", err)
	}
	if err := vp.BindEnv("insecure_skip_verify", "KAFKA_INSECURE_SKIP_VERIFY"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_INSECURE_SKIP_VERIFY: %w", err)
	}
	if err := vp.BindEnv("start_offset", "KAFKA_START_OFFSET"); err != nil {
		return nil, fmt.Errorf("error binding KAFKA_START_OFFSET: %w", err)
	}

	// Read environment variables
	vp.AutomaticEnv()

	var kafkaConfig KafkaConfig

	// Unmarshal environment variables into the Config struct
	if err := vp.Unmarshal(&kafkaConfig); err != nil {
		return nil, fmt.Errorf("unable to decode into struct, %w", err)
	}

	// Apply sensible defaults, then validate
	applyDefaults(&kafkaConfig)
	if err := validateConfig(&kafkaConfig); err != nil {
		return nil, err
	}

	return &kafkaConfig, nil
}

// applyDefaults fills in sensible default values for optional configuration fields.
// This is separated from validateConfig so that validation is a pure check with no side effects.
func applyDefaults(kafkaConfig *KafkaConfig) {
	if kafkaConfig.GroupID == "" {
		kafkaConfig.GroupID = "default-group"
	}
	if kafkaConfig.Partition == "" {
		kafkaConfig.Partition = "0"
	}
	if kafkaConfig.InsecureSkipVerify == "" {
		kafkaConfig.InsecureSkipVerify = "false"
	}
}

// validateConfig validates the loaded configuration.
// This is a pure validation function with no side effects — call applyDefaults first.
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
	if _, err := strconv.Atoi(kafkaConfig.Partition); err != nil {
		return fmt.Errorf("kafka partition must be a valid numeric value")
	}
	if kafkaConfig.InsecureSkipVerify != "true" && kafkaConfig.InsecureSkipVerify != "false" {
		return fmt.Errorf("kafka insecure_skip_verify must be true or false")
	}
	return nil
}

// getTopicName returns the appropriate topic name based on configuration.
// If Topic is set, it returns the Topic directly.
// If TopicPrefix is set and Topic is empty, it concatenates TopicPrefix with the separator and suffix.
// If TopicPrefix is set but no suffix is provided, it returns an error.
func getTopicName(kafkacfg *KafkaConfig, suffix, separator string) (string, error) {
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
	// Determine start offset for new consumer groups (default: FirstOffset)
	startOffset := kafka.FirstOffset
	if kafkacfg.StartOffset == "last" || kafkacfg.StartOffset == "latest" {
		startOffset = kafka.LastOffset
	}

	readerConfig := kafka.ReaderConfig{
		Brokers:     []string{kafkacfg.Address},
		Topic:       topicName,
		GroupID:     kafkacfg.GroupID,
		MinBytes:    1,    // 1 Byte
		MaxBytes:    10e6, // 10MB
		StartOffset: startOffset,
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

	// Async is explicitly set to false so that write errors are surfaced to the
	// caller instead of being silently dropped.
	writer := kafka.NewWriter(kafka.WriterConfig{
		Brokers:     []string{kafkacfg.Address},
		Topic:       topicName,
		Dialer:      dialer,
		Balancer:    &kafka.LeastBytes{},
		Async:       false,
		MaxAttempts: 5,
	})

	return writer, nil
}
