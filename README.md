[![CI Status](https://github.com/MyCarrier-DevOps/goLibMyCarrier/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/MyCarrier-DevOps/goLibMyCarrier/actions/workflows/ci.yml)
# goLibMyCarrier

`goLibMyCarrier` is a Go library that provides utilities for authentication, configuration management, interaction with ClickHouse and OpenTelemetry integration. It is designed to streamline the development of Go applications by providing essential components for these common tasks.

## Overview

`goLibMyCarrier` includes the following components:

-   **`argocdclient`**: Provides a client to fetch ArgoCD application and manifest data with retry logic and error handling.
-   **`auth`**: Provides authentication middleware for Gin framework.
-   **`clickhouse`**: Provides utilities to connect and query ClickHouse database.
-   **`github`**: Provides utilities to authenticate and interact with Github.
-   **`kafka`**: Provides utilities to produce and consume messages from Kafka.
-   **`logger`**: Provides a pre-configured logger using `go.uber.org/zap`.
-   **`otel`**: Integrates with OpenTelemetry for distributed tracing and logging.
-   **`vault`**: Provides utilities to interact with HashiCorp Vault.
-   **`yaml`**: Provides utilities to read and write yaml files.

## Installation

To install `goLibMyCarrier`, use the following Go command:

```bash
go get -u github.com/MyCarrier-DevOps/goLibMyCarrier/<module>@<version>
```

### Example

```bash
go get -u github.com/MyCarrier-DevOps/goLibMyCarrier/kafka@v1.3.6
```

## Go Pkg + Reports

| Package | Go Reference | Go Report Card |
|---------|--------------|----------------|
| `argocdclient` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/argocdclient) |
| `auth` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/auth.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/auth) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/auth)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/auth) |
| `clickhouse` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse) |
| `github` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/github.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/github) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/github)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/github) |
| `kafka` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/kafka.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/kafka) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/kafka)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/kafka) |
| `logger` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/logger.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/logger) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/logger)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/logger) |
| `otel` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/otel.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/otel) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/otel)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/otel) |
| `vault` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/vault.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/vault) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/vault)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/vault) |
| `yaml` | [![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/yaml.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/yaml) | [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/yaml)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/yaml) |

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

When contributing:
1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Write tests for your changes
4. Run lint checks (`make lint`)
5. Ensure all tests pass (`make test`)
6. Cleanup go deps (`make tidy`)
7. Commit your changes (`git commit -m 'Add some amazing feature'`)
8. Push to the branch (`git push origin feature/amazing-feature`)
9. Open a Pull Request

## Versioning

This library follows [Semantic Versioning](https://semver.org/).

## Support

For issues, questions, or contributions, please visit the [GitHub repository](https://github.com/MyCarrier-DevOps/go-client-langfuse).
