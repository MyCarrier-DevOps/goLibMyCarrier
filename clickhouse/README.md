# ClickHouse Handler Library

This library provides a utility for interacting with a ClickHouse database. It includes functionality for loading configuration, establishing a connection, executing queries, and managing the database session.

## Features

- Load ClickHouse configuration from environment variables.
- Establish a secure connection to the ClickHouse database.
- Execute queries and statements.
- Manage the database session lifecycle.

## Configuration

The library expects the following environment variables to be set:

- `CLICKHOUSE_HOSTNAME`: The hostname of the ClickHouse server.
- `CLICKHOUSE_USERNAME`: The username for authentication.
- `CLICKHOUSE_PASSWORD`: The password for authentication.
- `CLICKHOUSE_DATABASE`: The name of the database to connect to.
- `CLICKHOUSE_PORT`: The port number of the ClickHouse server.
- `CLICKHOUSE_SKIP_VERIFY`: Whether to skip TLS verification (`true` or `false`).

## Usage

### Loading Configuration

Use the `ClickhouseLoadConfig` function to load the configuration from environment variables:

```go
config, err := ClickhouseLoadConfig()
if err != nil {
    log.Fatalf("Failed to load ClickHouse configuration: %v", err.Error())
}
```

### Creating a ClickHouse Session

Use the `NewClickhouseSession` function to create a new session:

```go
ctx := context.Background()
session, err := NewClickhouseSession(config, ctx)
if err != nil {
    log.Fatalf("Failed to create ClickHouse session: %v", err.Error())
}
defer session.Close()
```

### Executing Queries

#### Querying Data

Use the `Query` method to execute a query and retrieve rows:

```go
rows, err := session.Query(ctx, "SELECT * FROM my_table")
if err != nil {
    log.Fatalf("Query failed: %v", err.Error())
}
defer rows.Close()
```

#### Executing Statements

Use the `Exec` method to execute a statement:

```go
err := session.Exec(ctx, "INSERT INTO my_table (id, name) VALUES (1, 'example')")
if err != nil {
    log.Fatalf("Execution failed: %v", err.Error())
}
```

### Closing the Session

Always close the session when done:

```go
err := session.Close()
if err != nil {
    log.Fatalf("Failed to close ClickHouse session: %v", err.Error())
}
```

## Error Handling

Ensure proper error handling when loading configuration, creating a session, or executing queries, as invalid credentials or connection issues will result in errors.

## Dependencies

This library uses the following dependencies:

- [ClickHouse Go](https://github.com/ClickHouse/clickhouse-go): Go client for ClickHouse.
- [spf13/viper](https://github.com/spf13/viper): Configuration management for Go.

