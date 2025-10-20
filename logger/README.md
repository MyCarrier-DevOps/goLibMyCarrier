[![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/logger.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/logger) [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/logger)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/logger)

# Logger Utility

This is a simple logging utility written in Go, designed to provide structured and easy-to-read logs for your application.

## Features

- Log levels: `INFO`, `WARN`, `ERROR`, `DEBUG`
- Timestamped log entries
- Easy-to-use API

## Installation

To use the logger, simply import it into your Go project:

```go
go get github.com/MyCarrier-DevOps/goLibMyCarrier/logger
```

## Usage

Below is an example of how to use the logger in your application:

```go
package main

import (
    "path/to/logger"
)

func main() {
    log := logger.NewAppLogger()

    log.Info("Application started")
    log.Debug("Debugging application")
    log.Warn("This is a warning")
    log.Error("An error occurred")
}
```

## API Reference

### `New()`

Creates a new instance of the logger.

### `Info(message string)`

Logs an informational message.

### `Debug(message string)`

Logs a debug message.

### `Warn(message string)`

Logs a warning message.

### `Error(message string)`

Logs an error message.
