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

	_, err := InitializeKafkaReader(config, ".")
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

	writer, err := InitializeKafkaWriter(config, ".")
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
		TopicPrefix: "",
		Username:  "test-user",
		Password:  "test-password",
		GroupID:   "test-group",
		Partition: "1",
	}

	err := validateConfig(config)
	assert.Error(t, err)
	assert.Equal(t, "kafka topic or topic prefix is required", err.Error())
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

// Test getTopicName function with various scenarios
func TestGetTopicName(t *testing.T) {
	tests := []struct {
		name          string
		config        *KafkaConfig
		suffix        string
		separator     string
		expected      string
		expectedError string
		description   string
	}{
		{
			name: "Topic set - suffix ignored",
			config: &KafkaConfig{
				Topic:       "my-topic",
				TopicPrefix: "dev.mycarrier",
			},
			suffix:      "unit.tester",
			separator:   ".",
			expected:    "my-topic",
			description: "When Topic is set, it should be returned directly, ignoring suffix",
		},
		{
			name: "TopicPrefix with suffix",
			config: &KafkaConfig{
				Topic:       "",
				TopicPrefix: "dev.mycarrier.ci",
			},
			suffix:      "unit.tester",
			separator:   ".",
			expected:    "dev.mycarrier.ci.unit.tester",
			description: "When TopicPrefix is set and Topic is empty, should concatenate with suffix",
		},
		{
			name: "TopicPrefix with custom separator",
			config: &KafkaConfig{
				Topic:       "",
				TopicPrefix: "dev-mycarrier-ci",
			},
			suffix:      "unit-tester",
			separator:   "-",
			expected:    "dev-mycarrier-ci-unit-tester",
			description: "Should support custom separators",
		},
		{
			name: "TopicPrefix without suffix - should error",
			config: &KafkaConfig{
				Topic:       "",
				TopicPrefix: "dev.mycarrier.ci",
			},
			suffix:        "",
			separator:     ".",
			expectedError: "suffix is required when using topic prefix",
			description:   "When TopicPrefix is set but no suffix provided, should return error",
		},
		{
			name: "Empty config - should error",
			config: &KafkaConfig{
				Topic:       "",
				TopicPrefix: "",
			},
			suffix:        "unit.tester",
			separator:     ".",
			expectedError: "either topic or topic prefix must be configured",
			description:   "When both Topic and TopicPrefix are empty, should return error",
		},
		{
			name: "Default separator when empty",
			config: &KafkaConfig{
				Topic:       "",
				TopicPrefix: "dev.mycarrier.ci",
			},
			suffix:      "unit.tester",
			separator:   "", // Empty separator should default to "."
			expected:    "dev.mycarrier.ci.unit.tester",
			description: "Should use default '.' separator when separator is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := getTopicName(tt.config, tt.suffix, tt.separator)
			
			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result, tt.description)
			}
		})
	}
}

// Test LoadConfig with TopicPrefix
func TestLoadConfig_WithTopicPrefix(t *testing.T) {
	t.Setenv("KAFKA_ADDRESS", "localhost:9092")
	t.Setenv("KAFKA_TOPIC_PREFIX", "dev.mycarrier.ci")
	t.Setenv("KAFKA_USERNAME", "test-user")
	t.Setenv("KAFKA_PASSWORD", "test-password")
	t.Setenv("KAFKA_GROUPID", "test-group")
	t.Setenv("KAFKA_PARTITION", "1")
	t.Setenv("KAFKA_INSECURE_SKIP_VERIFY", "true")

	config, err := LoadConfig()
	assert.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "localhost:9092", config.Address)
	assert.Equal(t, "", config.Topic) // Topic should be empty
	assert.Equal(t, "dev.mycarrier.ci", config.TopicPrefix)
	assert.Equal(t, "test-user", config.Username)
	assert.Equal(t, "test-password", config.Password)
	assert.Equal(t, "test-group", config.GroupID)
	assert.Equal(t, "1", config.Partition)
	assert.Equal(t, "true", config.InsecureSkipVerify)
}

// Test validateConfig with TopicPrefix
func TestValidateConfig_WithTopicPrefix(t *testing.T) {
	config := &KafkaConfig{
		Address:     "localhost:9092",
		Topic:       "",
		TopicPrefix: "dev.mycarrier.ci",
		Username:    "test-user",
		Password:    "test-password",
		GroupID:     "test-group",
		Partition:   "1",
	}

	err := validateConfig(config)
	assert.NoError(t, err)
}

// Test InitializeKafkaWriter with TopicPrefix and suffix
func TestInitializeKafkaWriter_WithTopicPrefix(t *testing.T) {
	config := &KafkaConfig{
		Address:     "localhost:9092",
		Topic:       "",
		TopicPrefix: "dev.mycarrier.ci",
		Username:    "test-user",
		Password:    "test-password",
		GroupID:     "test-group",
		Partition:   "1",
	}

	writer, err := InitializeKafkaWriter(config, ".", "unit.tester")
	assert.NoError(t, err)
	assert.NotNil(t, writer)

	// Close the writer to prevent resource leaks
	err = writer.Close()
	assert.NoError(t, err)
}

// Test InitializeKafkaWriter without suffix
func TestInitializeKafkaWriter_WithoutSuffix(t *testing.T) {
	config := &KafkaConfig{
		Address:   "localhost:9092",
		Topic:     "my-topic",
		Username:  "test-user",
		Password:  "test-password",
		GroupID:   "test-group",
		Partition: "1",
	}

	writer, err := InitializeKafkaWriter(config, ".")
	assert.NoError(t, err)
	assert.NotNil(t, writer)

	// Close the writer to prevent resource leaks
	err = writer.Close()
	assert.NoError(t, err)
}

// Test InitializeKafkaReader with TopicPrefix and suffix
func TestInitializeKafkaReader_WithTopicPrefix(t *testing.T) {
	config := &KafkaConfig{
		Address:     "localhost:9092",
		Topic:       "",
		TopicPrefix: "dev.mycarrier.ci",
		Username:    "test-user",
		Password:    "test-password",
		GroupID:     "test-group",
		Partition:   "1",
	}

	reader, err := InitializeKafkaReader(config, ".", "unit.tester")
	assert.NoError(t, err)
	assert.NotNil(t, reader)

	// Close the reader to prevent resource leaks
	err = reader.Close()
	assert.NoError(t, err)
}

// Test InitializeKafkaReader without suffix
func TestInitializeKafkaReader_WithoutSuffix(t *testing.T) {
	config := &KafkaConfig{
		Address:   "localhost:9092",
		Topic:     "my-topic",
		Username:  "test-user",
		Password:  "test-password",
		GroupID:   "test-group",
		Partition: "1",
	}

	reader, err := InitializeKafkaReader(config, ".")
	assert.NoError(t, err)
	assert.NotNil(t, reader)

	// Close the reader to prevent resource leaks
	err = reader.Close()
	assert.NoError(t, err)
}

// Test error cases for InitializeKafkaWriter
func TestInitializeKafkaWriter_EmptyTopicConfiguration(t *testing.T) {
	config := &KafkaConfig{
		Address:     "localhost:9092",
		Topic:       "",
		TopicPrefix: "",
		Username:    "test-user",
		Password:    "test-password",
		GroupID:     "test-group",
		Partition:   "1",
	}

	_, err := InitializeKafkaWriter(config, ".")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "either topic or topic prefix must be configured")
}

// Test InitializeKafkaWriter error when TopicPrefix is set but no suffix provided
func TestInitializeKafkaWriter_TopicPrefixWithoutSuffix(t *testing.T) {
	config := &KafkaConfig{
		Address:     "localhost:9092",
		Topic:       "",
		TopicPrefix: "dev.mycarrier.ci",
		Username:    "test-user",
		Password:    "test-password",
		GroupID:     "test-group",
		Partition:   "1",
	}

	_, err := InitializeKafkaWriter(config, ".")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "suffix is required when using topic prefix")
}

// Test InitializeKafkaReader error when TopicPrefix is set but no suffix provided
func TestInitializeKafkaReader_TopicPrefixWithoutSuffix(t *testing.T) {
	config := &KafkaConfig{
		Address:     "localhost:9092",
		Topic:       "",
		TopicPrefix: "dev.mycarrier.ci",
		Username:    "test-user",
		Password:    "test-password",
		GroupID:     "test-group",
		Partition:   "1",
	}

	_, err := InitializeKafkaReader(config, ".")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "suffix is required when using topic prefix")
}

// Test InitializeKafkaReader error when both Topic and TopicPrefix are empty
func TestInitializeKafkaReader_EmptyTopicConfiguration(t *testing.T) {
	config := &KafkaConfig{
		Address:     "localhost:9092",
		Topic:       "",
		TopicPrefix: "",
		Username:    "test-user",
		Password:    "test-password",
		GroupID:     "test-group",
		Partition:   "1",
	}

	_, err := InitializeKafkaReader(config, ".")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "either topic or topic prefix must be configured")
}

// Test InitializeKafkaWriter with custom separator
func TestInitializeKafkaWriter_CustomSeparator(t *testing.T) {
	config := &KafkaConfig{
		Address:     "localhost:9092",
		Topic:       "",
		TopicPrefix: "dev-mycarrier-ci",
		Username:    "test-user",
		Password:    "test-password",
		GroupID:     "test-group",
		Partition:   "1",
	}

	writer, err := InitializeKafkaWriter(config, "-", "unit-tester")
	assert.NoError(t, err)
	assert.NotNil(t, writer)

	// Close the writer to prevent resource leaks
	err = writer.Close()
	assert.NoError(t, err)
}

// Test InitializeKafkaReader with custom separator
func TestInitializeKafkaReader_CustomSeparator(t *testing.T) {
	config := &KafkaConfig{
		Address:     "localhost:9092",
		Topic:       "",
		TopicPrefix: "dev-mycarrier-ci",
		Username:    "test-user",
		Password:    "test-password",
		GroupID:     "test-group",
		Partition:   "1",
	}

	reader, err := InitializeKafkaReader(config, "-", "unit-tester")
	assert.NoError(t, err)
	assert.NotNil(t, reader)

	// Close the reader to prevent resource leaks
	err = reader.Close()
	assert.NoError(t, err)
}

// Test LoadConfig with both KAFKA_TOPIC and KAFKA_TOPIC_PREFIX set (Topic should take precedence)
func TestLoadConfig_BothTopicAndPrefix(t *testing.T) {
	t.Setenv("KAFKA_ADDRESS", "localhost:9092")
	t.Setenv("KAFKA_TOPIC", "explicit-topic")
	t.Setenv("KAFKA_TOPIC_PREFIX", "dev.mycarrier.ci")
	t.Setenv("KAFKA_USERNAME", "test-user")
	t.Setenv("KAFKA_PASSWORD", "test-password")
	t.Setenv("KAFKA_GROUPID", "test-group")
	t.Setenv("KAFKA_PARTITION", "1")

	config, err := LoadConfig()
	assert.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "explicit-topic", config.Topic)
	assert.Equal(t, "dev.mycarrier.ci", config.TopicPrefix)
}

// Test validateConfig with default values
func TestValidateConfig_DefaultValues(t *testing.T) {
	config := &KafkaConfig{
		Address:  "localhost:9092",
		Topic:    "test-topic",
		Username: "test-user",
		Password: "test-password",
		// GroupID, Partition, and InsecureSkipVerify are not set
	}

	err := validateConfig(config)
	assert.NoError(t, err)
	assert.Equal(t, "default-group", config.GroupID)
	assert.Equal(t, "0", config.Partition)
	assert.Equal(t, "false", config.InsecureSkipVerify)
}

// Test partition validation edge cases
func TestValidateConfig_PartitionEdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		partition string
		expectErr bool
	}{
		{"Valid partition 0", "0", false},
		{"Valid partition 999", "999", false},
		{"Invalid partition text", "abc", true},
		{"Invalid partition float", "1.5", true},
		{"Valid negative partition", "-1", false}, // Go's strconv.Atoi accepts negative numbers
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &KafkaConfig{
				Address:   "localhost:9092",
				Topic:     "test-topic",
				Username:  "test-user",
				Password:  "test-password",
				GroupID:   "test-group",
				Partition: tt.partition,
			}

			err := validateConfig(config)
			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "kafka partition must be a valid numeric value")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test the specific example from requirements: KAFKA_TOPIC_PREFIX=dev.mycarrier.ci + suffix="unit.tester" = "dev.mycarrier.ci.unit.tester"
func TestGetTopicName_RequirementsExample(t *testing.T) {
	config := &KafkaConfig{
		Topic:       "",
		TopicPrefix: "dev.mycarrier.ci",
	}

	result, err := getTopicName(config, "unit.tester", ".")
	assert.NoError(t, err)
	assert.Equal(t, "dev.mycarrier.ci.unit.tester", result)
}

// Test InitializeKafkaWriter with the specific requirements example
func TestInitializeKafkaWriter_RequirementsExample(t *testing.T) {
	config := &KafkaConfig{
		Address:     "localhost:9092",
		Topic:       "",
		TopicPrefix: "dev.mycarrier.ci",
		Username:    "test-user",
		Password:    "test-password",
		GroupID:     "test-group",
		Partition:   "1",
	}

	writer, err := InitializeKafkaWriter(config, ".", "unit.tester")
	assert.NoError(t, err)
	assert.NotNil(t, writer)

	// Close the writer to prevent resource leaks
	err = writer.Close()
	assert.NoError(t, err)
}
