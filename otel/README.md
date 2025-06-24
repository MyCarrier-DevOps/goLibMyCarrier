# otel.go

## Description
`otel.go` is a Go library for integrating OpenTelemetry into your Go applications. It provides easy-to-use functions and utilities to instrument your code and collect telemetry data.

## Installation
To install `otel.go`, use `go get`:
```sh
go get github.com/mycarrier-devops/goLibMyCarrier/otel
```

## Usage Example
``` go
package main

import (
    "log"
    "net/http"

    "github.com/gin-gonic/gin"
    "github.com/mycarrier-devops/goLibMyCarrier/otel"
)

func main() {
    // Initialize Zap logger
    logger, err := otel.InitZapLogger()
    if err != nil {
        log.Fatalf("failed to initialize logger: %v", err.Error())
    }
    defer logger.Sync() // flushes buffer, if any

    // Initialize Gin
    r := gin.New()

    // Add Zap logger middleware
    r.Use(otel.ZapLoggerForGin(logger))

    // Define a sample route
    r.GET("/ping", func(c *gin.Context) {
        c.JSON(http.StatusOK, gin.H{
            "message": "pong",
        })
    })

    // Start the server
    if err := r.Run(":8080"); err != nil {
        log.Fatalf("failed to run server: %v", err.Error())
    }
}
```

## OTEL Configuration
REF: https://github.com/odigos-io/opentelemetry-zap-bridge?tab=readme-ov-file#configuration
