# postgres

A pgx-based Postgres client for MyCarrier Go services — config loading,
validation, and a pooled session. Mirrors the shape of the sibling
[`clickhouse`](../clickhouse) module so consumers adopt it with the same
ergonomics.

## Configuration

Loaded from `POSTGRES_*` environment variables via `PostgresLoadConfig()` (an
isolated viper instance), or injected via `PostgresLoadConfigFromViper(v)`.

| Env var | Field | Required | Default |
|---|---|---|---|
| `POSTGRES_HOSTNAME` | `PgHostname` | yes | — |
| `POSTGRES_PORT` | `PgPort` | no | `5432` |
| `POSTGRES_USERNAME` | `PgUsername` | yes | — |
| `POSTGRES_PASSWORD` | `PgPassword` | yes | — |
| `POSTGRES_DATABASE` | `PgDatabase` | yes | — |
| `POSTGRES_SSLMODE` | `PgSSLMode` | no | `require` (Azure PG enforces TLS) |
| `POSTGRES_MAX_CONNS` | `MaxConns` | no | `50` |
| `POSTGRES_MIN_CONNS` | `MinConns` | no | `2` |
| `POSTGRES_CONN_MAX_LIFETIME` | `ConnMaxLifetime` | no | `60s` |

`PgSSLMode` accepts the libpq/pgx values: `disable`, `allow`, `prefer`,
`require`, `verify-ca`, `verify-full`.

## Usage

```go
cfg, err := postgres.PostgresLoadConfig()
if err != nil { /* ... */ }

sess, err := postgres.NewPostgresSession(ctx, cfg)
if err != nil { /* ... */ }
defer sess.Close()

var n int
err = sess.Pool().QueryRow(ctx, "SELECT 1").Scan(&n)
```

`NewPostgresSession` validates the config, builds a `pgxpool.Pool` with the
resolved pool settings, and pings (retrying transient failures; server-side
errors like auth fail fast).

## Testing

Unit tests run without Docker. The connect test is an integration test using a
`postgres:16` [testcontainer](https://golang.testcontainers.org/) and is skipped
under `go test -short`.

Testcontainers must be able to reach a Docker daemon. With **Rancher Desktop**
(and some rootless/non-default setups) it does not auto-detect the socket, so
export:

```bash
export DOCKER_HOST=unix://$HOME/.rd/docker.sock
export TESTCONTAINERS_RYUK_DISABLED=true   # Ryuk reaper doesn't come up under Rancher Desktop
```

(Docker Desktop / standard `/var/run/docker.sock` needs neither.)
