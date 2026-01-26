[![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/otel.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/otel) [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/otel)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/otel)

# Otel Logger

A lightweight, structured logging library that combines JSON logging with comprehensive OpenTelemetry integration. This logger provides structured logging capabilities while seamlessly sending both log records and trace data to OpenTelemetry collectors for complete observability.

## Features

- **📊 Structured JSON Logging**: All logs output as structured JSON for easy parsing
- **🔭 Dual OpenTelemetry Export**: Automatic export of both logs and traces to OTLP collectors
- **📋 OTLP Log Records**: Native OpenTelemetry log records with proper severity mapping
- **🔗 Trace Correlation**: Automatic correlation between log entries and trace spans
- **🌐 Web Framework Integration**: Built-in middleware for Gin with request/response logging
- **⚙️ Environment-Driven Configuration**: Configure via environment variables
- **🎯 Configurable Log Levels**: Support for debug, info, warn, and error levels
- **📝 Contextual Attributes**: Add structured attributes to log entries and traces
- **🛡️ Graceful Fallback**: Falls back to standard logging if OpenTelemetry fails
- **⚡ High Performance**: Minimal overhead with efficient JSON marshaling and batched export
- **☸️ Kubernetes Ready**: Built-in support for Kubernetes resource attributes

## Installation

```bash
go get github.com/MyCarrier-DevOps/goLibMyCarrier/otel
```

## Quick Start

```go
package main

import (
    "context"
    "os"
    "time"
    
    "github.com/MyCarrier-DevOps/goLibMyCarrier/otel"
)

func main() {
    // Optional: Configure OpenTelemetry collector endpoint
    // Method 1: Using standard OTLP endpoint
    os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
    
    // Method 2: Using separate host IP and port (takes precedence)
    os.Setenv("OTEL_HOST_IP", "192.168.1.100")
    os.Setenv("OTEL_HOST_PORT", "4318")
    
    // Create logger
    logger := otel.NewAppLogger()
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
| `OTEL_EXPORTER_OTLP_PROTOCOL` | OTLP transport protocol | `grpc` | `grpc`, `http/protobuf` |
| `OTEL_EXPORTER_OTLP_HEADERS` | Headers for OTLP requests | - | `api-key=abc123` |
| `OTEL_SDK_DISABLED` | Disable OpenTelemetry | `false` | `true` |

### Protocol Selection

The logger supports both gRPC and HTTP protocols for OTLP export. Use `OTEL_EXPORTER_OTLP_PROTOCOL` to select:

- **`grpc`** (default): More efficient, uses port 4317 by default
- **`http/protobuf`**: Uses HTTP/1.1, uses port 4318 by default

```bash
# Use gRPC protocol (default, more efficient)
export OTEL_EXPORTER_OTLP_PROTOCOL="grpc"
export OTEL_HOST_IP="localhost"
export OTEL_HOST_PORT="4317"

# Use HTTP protocol
export OTEL_EXPORTER_OTLP_PROTOCOL="http/protobuf"
export OTEL_HOST_IP="localhost"
export OTEL_HOST_PORT="4318"
```

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
logger := otel.NewAppLogger()

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
ctx := otel.WithLogger(context.Background(), logger)

// Retrieve logger from context
func processRequest(ctx context.Context) {
    logger := otel.FromContext(ctx)
    logger.Info("Processing request")
}
```

### Gin Web Framework Integration

The library provides built-in middleware for the Gin web framework:

```go
package main

import (
    "github.com/gin-gonic/gin"
    "github.com/MyCarrier-DevOps/goLibMyCarrier/otel"
)

func main() {
    // Create logger
    logger := otel.NewAppLogger()
    defer logger.Shutdown(context.Background())
    
    // Create Gin router
    router := gin.New()
    
    // Add otel logging middleware
    router.Use(otel.GinLoggerMiddleware(logger))
    
    // Add your routes
    router.GET("/api/users", func(c *gin.Context) {
        c.JSON(200, gin.H{"message": "success"})
    })
    
    router.Run(":8080")
}
```

#### Middleware Features

- **Structured Request Logging**: Logs all HTTP requests with structured data
- **Response Time Tracking**: Includes request latency in logs
- **Client IP Detection**: Captures real client IP (handles X-Forwarded-For)
- **Query Parameter Logging**: Includes full request path with query parameters
- **Status Code Tracking**: Logs HTTP response status codes
- **OpenTelemetry Integration**: Automatic trace correlation for HTTP requests

#### Example Log Output

```json
{
  "timestamp": "2025-01-20T15:04:05Z",
  "level": "info", 
  "message": "192.168.1.100 - [20/Jan/2025:15:04:05 -0700] \"GET /api/users?page=1&limit=10\" 200 15.2ms",
  "app_name": "my-service"
}
```

### Graceful Shutdown

```go
func main() {
    logger := otel.NewAppLogger()
    
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

The logger provides comprehensive OpenTelemetry integration with dual export capabilities:

### Dual Export Architecture

1. **Log Records**: Uses OpenTelemetry's native log records API (`otlploghttp`) for structured log export
2. **Trace Spans**: Generates trace spans (`otlptracehttp`) for correlation and distributed tracing

### Log Records Export

Each log entry is exported as a native OpenTelemetry log record with:

- **Severity Mapping**: Automatic conversion from log levels to OpenTelemetry severity
  - `debug` → `SeverityDebug`
  - `info` → `SeverityInfo` 
  - `warn` → `SeverityWarn`
  - `error` → `SeverityError`
- **Structured Attributes**: All custom attributes are preserved in the log record
- **Service Context**: Automatic service name and version attributes
- **Timestamp Precision**: High-precision timestamps for accurate correlation

### Trace Spans

In addition to log records, the logger creates trace spans for distributed tracing:

- **Span Attributes**: Log level, message content, and custom attributes
- **Service Information**: Application name, version, and instance ID
- **Kubernetes Context**: Pod, node, and namespace information when available

### OTLP Export

The logger uses dual OTLP exporters for comprehensive observability:

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

#### Export Capabilities:

- **Logs**: Native OpenTelemetry log records via OTLP/HTTP
- **Traces**: Distributed tracing spans via OTLP/HTTP  
- **Batched Export**: Efficient batching for both logs and traces
- **Resource Attributes**: Automatic service and Kubernetes resource information

Supported backends:
- **Jaeger**: `http://jaeger:4318` (supports both logs and traces)
- **OTEL Collector**: `http://otel-collector:4318` (full pipeline support)
- **Grafana**: Compatible with Loki (logs) and Tempo (traces)
- **Elastic**: Compatible with Elasticsearch and APM
- **Datadog**: `https://trace.agent.datadoghq.com` (requires agent configuration)
- **New Relic**: `https://otlp.nr-data.net` (unified platform)

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
      - OTEL_HOST_IP=otel-collector
      - OTEL_HOST_PORT=4318
      # Alternative: OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318
    depends_on:
      - otel-collector

  # OpenTelemetry Collector with dual export
  otel-collector:
    image: otel/opentelemetry-collector-contrib:latest
    command: ["--config=/etc/otel-collector-config.yml"]
    volumes:
      - ./otel-collector-config.yml:/etc/otel-collector-config.yml
    ports:
      - "4317:4317"   # OTLP gRPC
      - "4318:4318"   # OTLP HTTP
      - "8889:8889"   # Prometheus metrics
    depends_on:
      - jaeger

  # Jaeger for traces and logs
  jaeger:
    image: jaegertracing/all-in-one:latest
    ports:
      - "16686:16686" # Jaeger UI
      - "14250:14250" # Jaeger gRPC
    environment:
      - COLLECTOR_OTLP_ENABLED=true
```

### Example Gin Web Service

Here's a complete example of a Gin web service with otel logging:

```dockerfile
# Dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o main .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/main .
CMD ["./main"]
```

```go
// main.go
package main

import (
    "context"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/gin-gonic/gin"
    "github.com/MyCarrier-DevOps/goLibMyCarrier/otel"
)

func main() {
    // Create logger
    logger := otel.NewAppLogger()
    
    // Setup graceful shutdown
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    
    // Create Gin router
    router := gin.New()
    router.Use(otel.GinLoggerMiddleware(logger))
    
    // Add routes
    router.GET("/health", func(c *gin.Context) {
        c.JSON(200, gin.H{"status": "healthy"})
    })
    
    router.GET("/api/users/:id", func(c *gin.Context) {
        userID := c.Param("id")
        logger.With("user_id", userID).Info("Fetching user details")
        c.JSON(200, gin.H{"id": userID, "name": "John Doe"})
    })
    
    // Start server
    srv := &http.Server{
        Addr:    ":8080",
        Handler: router,
    }
    
    go func() {
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("Server failed to start: %v", err)
        }
    }()
    
    logger.Info("Server started on :8080")
    
    // Wait for interrupt signal
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit
    
    logger.Info("Shutting down server...")
    
    // Graceful shutdown
    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer shutdownCancel()
    
    if err := srv.Shutdown(shutdownCtx); err != nil {
        logger.Errorf("Server forced to shutdown: %v", err)
    }
    
    if err := logger.Shutdown(shutdownCtx); err != nil {
        log.Printf("Logger shutdown error: %v", err)
    }
    
    logger.Info("Server exited")
}
```

```yaml
# docker-compose.yml for Gin service
version: '3.8'
services:
  web-service:
    build: .
    ports:
      - "8080:8080"
    environment:
      - LOG_LEVEL=info
      - LOG_APP_NAME=gin-web-service
      - LOG_APP_VERSION=1.0.0
      - OTEL_HOST_IP=otel-collector
      - OTEL_HOST_PORT=4318
    depends_on:
      - otel-collector

  otel-collector:
    image: otel/opentelemetry-collector-contrib:latest
    command: ["--config=/etc/otel-collector-config.yml"]
    volumes:
      - ./otel-collector-config.yml:/etc/otel-collector-config.yml
    ports:
      - "4318:4318"   # OTLP HTTP
    depends_on:
      - jaeger

  jaeger:
    image: jaegertracing/all-in-one:latest
    ports:
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

Benchmarks on a typical development machine with dual export enabled:

```
BenchmarkLogStructured-8           500000    2.8 μs/op    1.2 KB/op    4 allocs/op
BenchmarkLogWithOTLP-8            200000    6.2 μs/op    2.8 KB/op    8 allocs/op
BenchmarkWithAttributes-8         200000    4.2 μs/op    2.1 KB/op    7 allocs/op
BenchmarkDualExport-8             150000    8.1 μs/op    3.5 KB/op   12 allocs/op
```

### Performance Characteristics:

- **Structured Logging Only**: ~2.8μs per log entry
- **With OpenTelemetry Export**: ~6-8μs per log entry (includes both logs and traces)
- **Batched Export**: Reduces network overhead significantly
- **Memory Efficient**: Minimal allocation overhead with connection pooling
- **Background Processing**: Non-blocking export via background goroutines

## Observability & Monitoring

### Complete Observability Stack

The dual export capability enables comprehensive observability:

```bash
# Enable full observability
export OTEL_HOST_IP="otel-collector"
export OTEL_HOST_PORT="4318"
export LOG_LEVEL="info"
```

### Correlation & Analysis

1. **Log-Trace Correlation**: Every log entry includes trace context for seamless correlation
2. **Service Topology**: Trace spans reveal service dependencies and call paths
3. **Error Analysis**: Error logs are automatically linked to failed trace spans
4. **Performance Insights**: Duration metrics from spans correlated with log timestamps

### Monitoring Queries

Examples for popular observability platforms:

#### Grafana + Loki + Tempo
```
# Find logs for a specific trace
{service_name="my-service"} | json | trace_id="abc123"

# Correlate errors with traces  
{level="error"} | json | line_format "{{.trace_id}}"
```

#### Jaeger UI
- View trace spans with embedded log attributes
- Filter traces by service name and operation
- Analyze error rates and latency patterns

## Error Handling

The logger is designed to be resilient with multiple fallback mechanisms:

- **OpenTelemetry Export Failures**: Falls back to structured JSON logging only
- **Log Export Failures**: Continues with trace export if available
- **Trace Export Failures**: Continues with log export if available
- **JSON Marshaling Failures**: Falls back to plain text logging  
- **Network Issues**: Buffers logs and traces, retries automatically with exponential backoff
- **Configuration Errors**: Uses sensible defaults and continues operation
- **Resource Creation Failures**: Merges with default resources or creates new ones

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

### 1. Use Structured Attributes for Better Observability
```go
// Good: Structured attributes enable powerful querying and correlation
logger.With("user_id", userID).
       With("action", "login").
       With("request_id", reqID).
       Info("User authentication successful")

// Avoid: String formatting loses structure and makes querying difficult
logger.Infof("User %s performed %s with request %s", userID, action, reqID)
```

### 2. Set Appropriate Log Levels for Performance
```go
logger.Debug("Detailed debugging information")  // Development only - filtered in production
logger.Info("General application flow")         // Normal operations - always useful
logger.Warn("Unexpected but recoverable")       // Potential issues - requires attention
logger.Error("Error that needs attention")     // Failures - critical for debugging
```

### 3. Use Context for Request Tracing
```go
func handleRequest(w http.ResponseWriter, r *http.Request) {
    requestID := generateRequestID()
    requestLogger := logger.With("request_id", requestID).With("user_id", getUserID(r))
    ctx := otel.WithLogger(r.Context(), requestLogger)
    
    // All downstream operations will inherit the enriched logger
    processRequest(ctx)
}
```

### 4. Include Relevant Attributes for Correlation
```go
// Include business context that helps with debugging and analysis
logger.With("user_id", user.ID).
       With("order_id", order.ID).
       With("payment_method", order.PaymentMethod).
       With("amount", order.Total).
       With("currency", order.Currency).
       Info("Payment processed successfully")
```

### 5. Optimize for Dual Export Performance
```go
// Use logger instances with pre-configured attributes to reduce overhead
serviceLogger := logger.With("component", "payment-service").With("version", "2.1.0")

// Reuse the configured logger throughout the service
func processPayment(ctx context.Context, order Order) {
    requestLogger := serviceLogger.With("order_id", order.ID)
    requestLogger.Info("Starting payment processing")
    // ... processing logic
    requestLogger.Info("Payment processing completed")
}
```

### 6. Graceful Shutdown for Data Integrity
```go
func main() {
    logger := otel.NewAppLogger()
    
    // Setup graceful shutdown to ensure all logs and traces are exported
    c := make(chan os.Signal, 1)
    signal.Notify(c, os.Interrupt, syscall.SIGTERM)
    
    go func() {
        <-c
        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        
        if err := logger.Shutdown(ctx); err != nil {
            log.Printf("Failed to shutdown logger: %v", err)
        }
        os.Exit(0)
    }()
    
    // Your application code...
}
```

### 7. Web Framework Integration Best Practices
```go
// Good: Use middleware early in the chain for complete request coverage
router := gin.New()
router.Use(otel.GinLoggerMiddleware(logger))
router.Use(gin.Recovery()) // Add after logging middleware

// Good: Combine with request-scoped loggers for better tracing
router.GET("/api/orders/:id", func(c *gin.Context) {
    orderID := c.Param("id")
    requestLogger := logger.With("order_id", orderID).With("request_id", generateRequestID())
    
    // Use the request logger throughout the handler
    requestLogger.Info("Processing order request")
    // ... business logic
    requestLogger.Info("Order request completed")
})

// Good: Add custom attributes to middleware for consistent logging
func customGinLogger(logger otel.AppLogger) gin.HandlerFunc {
    baseLogger := logger.With("component", "http-server")
    return otel.GinLoggerMiddleware(baseLogger)
}
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Write tests for your changes
4. Ensure all tests pass (`go test ./...`)
5. Commit your changes (`git commit -am 'Add amazing feature'`)
6. Push to the branch (`git push origin feature/amazing-feature`)
7. Open a Pull Request

## Support

- 📖 [Documentation](https://github.com/MyCarrier-DevOps/goLibMyCarrier/tree/main/otel)
- 🐛 [Issue Tracker](https://github.com/MyCarrier-DevOps/goLibMyCarrier/issues)
- 💬 [Discussions](https://github.com/MyCarrier-DevOps/goLibMyCarrier/discussions)
