# Kafka Utilities

The `kafka` package provides utilities to interact with Kafka, including configuration management, message production, and message consumption. It simplifies the process of integrating Kafka into your Go applications.

## Features

- Load Kafka configuration from environment variables.
- Validate Kafka configuration.
- Initialize Kafka readers and writers with SASL authentication.
- Support for partitioned and grouped message consumption.

## Configuration

The Kafka configuration is loaded using environment variables. The following variables are supported:

| Environment Variable | Description                  | Required | Default Value   |
|-----------------------|------------------------------|----------|-----------------|
| `KAFKA_ADDRESS`       | Kafka broker address         | Yes      | -               |
| `KAFKA_TOPIC`         | Kafka topic name            | Yes      | -               |
| `KAFKA_USERNAME`      | SASL username               | Yes      | -               |
| `KAFKA_PASSWORD`      | SASL password               | Yes      | -               |
| `KAFKA_GROUPID`       | Consumer group ID           | No       | `default-group` |
| `KAFKA_PARTITION`     | Partition number (numeric)  | No       | `0`             |

## Usage

### Loading Configuration

Use the `LoadConfig` function to load and validate Kafka configuration:

```go
config, err := kafka.LoadConfig()
if err != nil {
    log.Fatalf("Failed to load Kafka config: %v", err)
}
```

### Initializing a Kafka Reader

Use the `InitializeKafkaReader` function to create a Kafka reader:

```go
reader, err := kafka.InitializeKafkaReader(config)
if err != nil {
    log.Fatalf("Failed to initialize Kafka reader: %v", err)
}
defer reader.Close()
```

### Initializing a Kafka Writer

Use the `InitializeKafkaWriter` function to create a Kafka writer:

```go
writer, err := kafka.InitializeKafkaWriter(config)
if err != nil {
    log.Fatalf("Failed to initialize Kafka writer: %v", err)
}
defer writer.Close()
```

## Example

Below is an example of producing and consuming messages using the `kafka` package:

```go
package main

import (
	"context"
	"log"
	"github.com/MyCarrier-DevOps/goLibMyCarrier/kafka"
)

func main() {
	// Load Kafka configuration
	config, err := kafka.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load Kafka config: %v", err)
	}

	// Initialize Kafka writer
	writer, err := kafka.InitializeKafkaWriter(config)
	if err != nil {
		log.Fatalf("Failed to initialize Kafka writer: %v", err)
	}
	defer writer.Close()

	// Produce a message
	err = writer.WriteMessages(context.Background(), kafka.Message{
		Value: []byte("Hello, Kafka!"),
	})
	if err != nil {
		log.Fatalf("Failed to write message: %v", err)
	}

	// Initialize Kafka reader
	reader, err := kafka.InitializeKafkaReader(config)
	if err != nil {
		log.Fatalf("Failed to initialize Kafka reader: %v", err)
	}
	defer reader.Close()

	// Consume a message
	msg, err := reader.ReadMessage(context.Background())
	if err != nil {
		log.Fatalf("Failed to read message: %v", err)
	}
	log.Printf("Received message: %s", string(msg.Value))
}
```

## Notes

- Ensure that the Kafka broker supports SASL authentication if using `KAFKA_USERNAME` and `KAFKA_PASSWORD`.
- The `KAFKA_PARTITION` variable is only used when `KAFKA_GROUPID` is not set.

For more details, refer to the [Segment Kafka Go documentation](https://pkg.go.dev/github.com/segmentio/kafka-go).
