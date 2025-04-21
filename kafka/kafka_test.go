package kafka

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("KAFKA_ADDRESS", "localhost:9092")
	t.Setenv("KAFKA_TOPIC", "test-topic")
	t.Setenv("KAFKA_USERNAME", "test-user")
	t.Setenv("KAFKA_PASSWORD", "test-password")
	t.Setenv("KAFKA_GROUPID", "test-group")
	t.Setenv("KAFKA_PARTITION", "1")
	t.Setenv("KAFKA_INSECURE_SKIP_VERIFY", "true")

	config, err := LoadConfig()
	assert.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "localhost:9092", config.Address)
	assert.Equal(t, "test-topic", config.Topic)
	assert.Equal(t, "test-user", config.Username)
	assert.Equal(t, "test-password", config.Password)
	assert.Equal(t, "test-group", config.GroupID)
	assert.Equal(t, "1", config.Partition)
	assert.Equal(t, "true", config.InsecureSkipVerify)
}

func TestLoadConfig_MissingRequiredFields(t *testing.T) {
	t.Setenv("KAFKA_ADDRESS", "")
	t.Setenv("KAFKA_TOPIC", "")
	t.Setenv("KAFKA_USERNAME", "")
	t.Setenv("KAFKA_PASSWORD", "")
	t.Setenv("KAFKA_INSECURE_SKIP_VERIFY", "")

	_, err := LoadConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "kafka address is required")
}

func TestInitializeKafkaReader_InvalidPartition(t *testing.T) {
	config := &KafkaConfig{
		Address:  "localhost:9092",
		Topic:    "test-topic",
		Username: "test-user",
		Password: "test-password",
		// Remove GroupID as it's causing validation issues with the reader creation
		Partition: "invalid",
	}

	_, err := InitializeKafkaReader(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid partition value")
}

func TestInitializeKafkaWriter(t *testing.T) {
	config := &KafkaConfig{
		Address:   "localhost:9092",
		Topic:     "test-topic",
		Username:  "test-user",
		Password:  "test-password",
		GroupID:   "test-group",
		Partition: "1",
	}

	writer, err := InitializeKafkaWriter(config)
	assert.NoError(t, err)
	assert.NotNil(t, writer)

	// Close the writer to prevent resource leaks
	err = writer.Close()
	assert.NoError(t, err)
}

func TestValidateConfig_MissingAddress(t *testing.T) {
	config := &KafkaConfig{
		Address:   "",
		Topic:     "test-topic",
		Username:  "test-user",
		Password:  "test-password",
		GroupID:   "test-group",
		Partition: "1",
	}

	err := validateConfig(config)
	assert.Error(t, err)
	assert.Equal(t, "kafka address is required", err.Error())
}

func TestValidateConfig_MissingTopic(t *testing.T) {
	config := &KafkaConfig{
		Address:   "localhost:9092",
		Topic:     "",
		Username:  "test-user",
		Password:  "test-password",
		GroupID:   "test-group",
		Partition: "1",
	}

	err := validateConfig(config)
	assert.Error(t, err)
	assert.Equal(t, "kafka topic is required", err.Error())
}

func TestValidateConfig_MissingUsername(t *testing.T) {
	config := &KafkaConfig{
		Address:   "localhost:9092",
		Topic:     "test-topic",
		Username:  "",
		Password:  "test-password",
		GroupID:   "test-group",
		Partition: "1",
	}

	err := validateConfig(config)
	assert.Error(t, err)
	assert.Equal(t, "kafka username is required", err.Error())
}

func TestValidateConfig_MissingPassword(t *testing.T) {
	config := &KafkaConfig{
		Address:   "localhost:9092",
		Topic:     "test-topic",
		Username:  "test-user",
		Password:  "",
		GroupID:   "test-group",
		Partition: "1",
	}

	err := validateConfig(config)
	assert.Error(t, err)
	assert.Equal(t, "kafka password is required", err.Error())
}

func TestValidateConfig_InvalidPartition(t *testing.T) {
	config := &KafkaConfig{
		Address:   "localhost:9092",
		Topic:     "test-topic",
		Username:  "test-user",
		Password:  "test-password",
		GroupID:   "test-group",
		Partition: "invalid",
	}

	err := validateConfig(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "kafka partition must be a valid numeric value")
}

func TestValidateConfig_InvalidInsecureSkipVerify(t *testing.T) {
	config := &KafkaConfig{
		Address:            "localhost:9092",
		Topic:              "test-topic",
		Username:           "test-user",
		Password:           "test-password",
		GroupID:            "test-group",
		Partition:          "1",
		InsecureSkipVerify: "invalid",
	}

	err := validateConfig(config)
	assert.Error(t, err)
	assert.Equal(t, "kafka insecure_skip_verify must be true or false", err.Error())
}

func TestValidateConfig_DefaultInsecureSkipVerify(t *testing.T) {
	config := &KafkaConfig{
		Address:   "localhost:9092",
		Topic:     "test-topic",
		Username:  "test-user",
		Password:  "test-password",
		GroupID:   "test-group",
		Partition: "1",
	}

	err := validateConfig(config)
	assert.NoError(t, err)
	assert.Equal(t, "false", config.InsecureSkipVerify)
}
