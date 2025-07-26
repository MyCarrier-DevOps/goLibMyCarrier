# pureotel Logger

A lightweight, structured logging library that combines JSON logging with OpenTelemetry tracing integration. This logger provides structured logging capabilities while seamlessly sending log data to OpenTelemetry collectors for observability.

## Features

- ** Structured JSON Logging**: All logs output as structured JSON for easy parsing
- **🔭 OpenTelemetry Integration**: Automatic trace generation and OTLP export
- **⚙️ Environment-Driven Configuration**: Configure via environment variables
- **🎯 Configurable Log Levels**: Support for debug, info, warn, and error levels
- **📝 Contextual Attributes**: Add structured attributes to log entries
- **🛡️ Graceful Fallback**: Falls back to standard logging if OpenTelemetry fails
- **⚡ High Performance**: Minimal overhead with efficient JSON marshaling

## Installation

```bash
go get github.com/MyCarrier-DevOps/goLibMyCarrier/pureotel
```

## Quick Start

```go
package main

import (
    "context"
    "os"
    "time"
    
    "github.com/MyCarrier-DevOps/goLibMyCarrier/pureotel"
)

func main() {
    // Optional: Configure OpenTelemetry collector endpoint
    // Method 1: Using standard OTLP endpoint
    os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
    
    // Method 2: Using separate host IP and port (takes precedence)
    os.Setenv("OTEL_HOST_IP", "192.168.1.100")
    os.Setenv("OTEL_HOST_PORT", "4318")
    
    // Create logger
    logger := pureotel.NewAppLogger()
    defer logger.Shutdown(context.Background())
    
    // Basic logging
    logger.Info("Application started")
    logger.Warn("This is a warning")
    logger.Error("Something went wrong")
    
    // Structured logging with attributes
    enhanced := logger.With("component", "main").With("version", "1.0.0")
    enhanced.Info("Processing request")
    
    // Formatted logging
    enhanced.Infof("Processing %d items", 42)
}
```

## Configuration

Configure the logger using environment variables:

| Environment Variable | Description | Default | Example |
|---------------------|-------------|---------|---------|
| `LOG_LEVEL` | Set minimum log level | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_APP_NAME` | Application name for logs | `test_application_abcd` | `my-service` |
| `LOG_APP_VERSION` | Application version for logs | `1.0.0` | `2.1.0` |
| `OTEL_HOST_IP` | OpenTelemetry collector host IP/hostname (takes precedence) | - | `192.168.1.100`, `otel-collector` |
| `OTEL_HOST_PORT` | OpenTelemetry collector port (used with OTEL_HOST_IP) | - | `4317`, `4318` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Full OpenTelemetry collector endpoint (fallback) | - | `http://localhost:4318` |
| `OTEL_EXPORTER_OTLP_HEADERS` | Headers for OTLP requests | - | `api-key=abc123` |
| `OTEL_SDK_DISABLED` | Disable OpenTelemetry | `false` | `true` |

### Endpoint Configuration Priority

The logger uses the following priority order for endpoint configuration:

1. **`OTEL_HOST_IP` + `OTEL_HOST_PORT`**: If `OTEL_HOST_IP` is set, it takes precedence
   - If `OTEL_HOST_PORT` is also set and `OTEL_HOST_IP` doesn't contain a port, they are combined
   - If `OTEL_HOST_IP` already contains a port, `OTEL_HOST_PORT` is ignored
2. **`OTEL_EXPORTER_OTLP_ENDPOINT`**: Used as fallback when `OTEL_HOST_IP` is not set
3. **No endpoint**: Uses noop exporter (structured logging only)

#### Examples:

```bash
# Example 1: Using separate host and port
export OTEL_HOST_IP="192.168.1.100"
export OTEL_HOST_PORT="4318"
# Result: http://192.168.1.100:4318

# Example 2: Host IP with port already included
export OTEL_HOST_IP="otel-collector:4317"
export OTEL_HOST_PORT="4318"  # This will be ignored
# Result: http://otel-collector:4317

# Example 3: Fallback to standard endpoint
export OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4318"
# Result: http://localhost:4318

# Example 4: Kubernetes deployment with node IP
export OTEL_HOST_IP="${NODE_IP}"  # Injected by Kubernetes
export OTEL_HOST_PORT="4317"
# Result: http://<node-ip>:4317
```

## Usage Examples

### Basic Logging

```go
logger := pureotel.NewAppLogger()

logger.Debug("Debug information")
logger.Info("General information") 
logger.Warn("Warning message")
logger.Error("Error occurred")
```

### Formatted Logging

```go
logger.Infof("User %s logged in from %s", userID, ipAddress)
logger.Errorf("Failed to process order %d: %v", orderID, err)
```

### Structured Logging with Attributes

```go
// Add contextual information
requestLogger := logger.With("request_id", "req-123").With("user_id", "user-456")
requestLogger.Info("Processing payment")

// Chain multiple attributes
enhanced := logger.
    With("component", "payment-service").
    With("version", "2.1.0").
    With("environment", "production")
    
enhanced.Info("Payment processed successfully")
```

### Context Integration

```go
// Store logger in context
ctx := pureotel.WithLogger(context.Background(), logger)

// Retrieve logger from context
func processRequest(ctx context.Context) {
    logger := pureotel.FromContext(ctx)
    logger.Info("Processing request")
}
```

### Graceful Shutdown

```go
func main() {
    logger := pureotel.NewAppLogger()
    
    // Setup graceful shutdown
    defer func() {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        
        if err := logger.Shutdown(ctx); err != nil {
            log.Printf("Failed to shutdown logger: %v", err)
        }
    }()
    
    // Your application code...
}
```

## Log Output Format

All logs are output as structured JSON:

```json
{
  "timestamp": "2025-01-20T15:04:05Z",
  "level": "info",
  "message": "User logged in successfully",
  "app_name": "my-service",
  "attributes": {
    "user_id": "user-123",
    "session_id": "sess-456",
    "component": "auth-service"
  }
}
```

## OpenTelemetry Integration

The logger automatically creates OpenTelemetry spans for each log entry, enabling correlation between logs and traces:

### Trace Attributes

Each log entry generates a span with the following attributes:

- `log.severity`: Log level (debug, info, warn, error)
- `log.body`: Log message content
- `service.name`: Application name
- `log.timestamp`: ISO 8601 timestamp
- `instrumentation.name`: Library identifier
- Custom attributes with `log.` prefix

### OTLP Export

Configure the OTLP endpoint to send traces to your observability platform:

```bash
# Method 1: Using separate host and port (recommended for dynamic environments)
export OTEL_HOST_IP="jaeger"
export OTEL_HOST_PORT="4318"

# Method 2: Using full endpoint URL
export OTEL_EXPORTER_OTLP_ENDPOINT="http://jaeger:4318"

# For Kubernetes deployments with node IP
export OTEL_HOST_IP="${NODE_IP}"
export OTEL_HOST_PORT="4317"
```

Supported backends:
- **Jaeger**: `http://jaeger:4318`
- **Zipkin**: `http://zipkin:9411`
- **OTLP Collector**: `http://otel-collector:4318`
- **Datadog**: `https://trace.agent.datadoghq.com`
- **New Relic**: `https://otlp.nr-data.net`

## Docker Compose Example

```yaml
version: '3.8'
services:
  app:
    build: .
    environment:
      - LOG_LEVEL=debug
      - LOG_APP_NAME=my-service
      - LOG_APP_VERSION=1.0.0
      - OTEL_HOST_IP=jaeger
      - OTEL_HOST_PORT=4318
      # Alternative: OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318
    depends_on:
      - jaeger

  jaeger:
    image: jaegertracing/all-in-one:latest
    ports:
      - "4318:4318"   # OTLP HTTP
      - "16686:16686" # Jaeger UI
    environment:
      - COLLECTOR_OTLP_ENABLED=true
```

## Kubernetes Deployment Example

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    spec:
      containers:
      - name: my-app
        image: my-app:latest
        env:
        - name: LOG_LEVEL
          value: "info"
        - name: LOG_APP_NAME
          value: "my-service"
        - name: NODE_IP
          valueFrom:
            fieldRef:
              fieldPath: status.hostIP
        - name: OTEL_HOST_IP
          value: "$(NODE_IP)"
        - name: OTEL_HOST_PORT
          value: "4317"
        # Kubernetes resource attributes for better observability
        - name: OTEL_RESOURCE_ATTRIBUTES_NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        - name: OTEL_RESOURCE_ATTRIBUTES_POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: OTEL_RESOURCE_ATTRIBUTES_POD_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
        - name: OTEL_RESOURCE_ATTRIBUTES_POD_UID
          valueFrom:
            fieldRef:
              fieldPath: metadata.uid
        - name: OTEL_RESOURCE_ATTRIBUTES_POD_IP
          valueFrom:
            fieldRef:
              fieldPath: status.podIP
```

## Performance

Benchmarks on a typical development machine:

```
BenchmarkLogStructured-8     500000    2.8 μs/op    1.2 KB/op    4 allocs/op
BenchmarkWithAttributes-8    200000    4.2 μs/op    2.1 KB/op    7 allocs/op
```

## Error Handling

The logger is designed to be resilient:

- **OpenTelemetry Failures**: Falls back to structured JSON logging
- **JSON Marshaling Failures**: Falls back to plain text logging  
- **Network Issues**: Buffers traces and retries automatically
- **Configuration Errors**: Uses sensible defaults

## Testing

Run the test suite:

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Run benchmarks
go test -bench=. -benchmem
```

## Best Practices

### 1. Use Structured Attributes
```go
// Good: Structured attributes
logger.With("user_id", userID).With("action", "login").Info("User authentication")

// Avoid: String formatting in message
logger.Infof("User %s performed %s", userID, action)
```

### 2. Set Appropriate Log Levels
```go
logger.Debug("Detailed debugging information")  // Development only
logger.Info("General application flow")         // Normal operations  
logger.Warn("Unexpected but recoverable")       // Potential issues
logger.Error("Error that needs attention")     // Failures
```

### 3. Use Context for Request Tracing
```go
func handleRequest(w http.ResponseWriter, r *http.Request) {
    requestLogger := logger.With("request_id", generateRequestID())
    ctx := pureotel.WithLogger(r.Context(), requestLogger)
    
    processRequest(ctx)
}
```

### 4. Include Relevant Attributes
```go
// Include context that helps with debugging
logger.With("user_id", user.ID).
       With("order_id", order.ID).
       With("amount", order.Total).
       Info("Order processed successfully")
```

## Migration from Other Loggers

### From logrus
```go
// logrus
logrus.WithFields(logrus.Fields{"key": "value"}).Info("message")

// pureotel
logger.With("key", "value").Info("message")
```

### From standard log
```go
// standard log
log.Printf("User %s logged in", userID)

// pureotel
logger.With("user_id", userID).Info("User logged in")
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Write tests for your changes
4. Ensure all tests pass (`go test ./...`)
5. Commit your changes (`git commit -am 'Add amazing feature'`)
6. Push to the branch (`git push origin feature/amazing-feature`)
7. Open a Pull Request

## License

This project is licensed under the MIT License - see the LICENSE file for details.

## Support

- 📖 [Documentation](https://github.com/MyCarrier-DevOps/goLibMyCarrier/tree/main/pureotel)
- 🐛 [Issue Tracker](https://github.com/MyCarrier-DevOps/goLibMyCarrier/issues)
- 💬 [Discussions](https://github.com/MyCarrier-DevOps/goLibMyCarrier/discussions)
