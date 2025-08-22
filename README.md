# goLibMyCarrier

`goLibMyCarrier` is a Go library that provides utilities for authentication, configuration management, interaction with ClickHouse and OpenTelemetry integration. It is designed to streamline the development of Go applications by providing essential components for these common tasks.

## Overview

`goLibMyCarrier` includes the following components:

-   **`argocdclient`**: Provides a client to fetch argo application and manifest data.
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

# Example
go get -u github.com/MyCarrier-DevOps/goLibMyCarrier/kafka@v1.3.6

```
