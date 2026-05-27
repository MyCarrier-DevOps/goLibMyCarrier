[![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/pollyapi.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/pollyapi) [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/pollyapi)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/pollyapi)
# Polly API Client

Lightweight, stdlib-only client for [polly-api](https://github.com/mycarrier/polly-api) — MyCarrier's centralized CI/CD notification service. It resolves the polly-api base URL from the environment and posts check-run and PR-comment payloads the same way the `polly` CLI does, without pulling in polly-api's transitive dependency graph (Gin, Huma, go-github, Redis). Modeled on the sibling `goLibMyCarrier/slippyapi` package.

## Installation

```bash
go get github.com/MyCarrier-DevOps/goLibMyCarrier/pollyapi
```

## Usage

```go
import (
    "context"
    "errors"

    "github.com/MyCarrier-DevOps/goLibMyCarrier/pollyapi"
)

// Construct from the environment (POLLY_API_URL / K8S_NAMESPACE + POLLY_API_KEY).
client, err := pollyapi.NewClientFromEnv()
if errors.Is(err, pollyapi.ErrNotConfigured) {
    return // polly is disabled in this environment; skip
}
if err != nil {
    return err
}

// Upsert a check run with a typed payload.
err = client.UpsertCheckRun(context.Background(), "build", pollyapi.BuildRequest{
    BaseCheckRun: pollyapi.BaseCheckRun{Owner: "MyCarrier-DevOps", Repo: "polly", SHA: sha, RunID: runID},
    Tag:          "v1.2.3",
    GrafanaURL:   grafanaURL,
})

// Upsert a PR comment.
err = client.Comment(context.Background(), pollyapi.CommentRequest{
    Owner: "MyCarrier-DevOps", Repo: "polly", PR: 42,
    Marker: "<!-- deploy-status -->", Body: "Deployed to prod ✅",
})
```

Or construct explicitly:

```go
client := pollyapi.NewClient("http://polly-api.argo-events.svc.cluster.local:8080",
    pollyapi.WithAPIKey(apiKey))
```

## Configuration

`NewClientFromEnv` and `ResolveAPIURL` consult:

| Environment Variable | Description |
|----------------------|-------------|
| `POLLY_API_URL` | Explicit base URL override (validated as http(s), trimmed of whitespace and trailing slash). |
| `K8S_NAMESPACE` | Mapped to a known in-cluster polly-api URL when `POLLY_API_URL` is unset. |
| `POLLY_API_KEY` | Bearer token sent in the `Authorization` header (read by `NewClientFromEnv`). |

### URL resolution order

1. `POLLY_API_URL` — explicit override.
2. `K8S_NAMESPACE` mapped to a known cluster:
   - `argo-events` → `http://polly-api.argo-events.svc.cluster.local:8080`
   - `argo-events-test` → `http://polly-api-test.argo-events-test.svc.cluster.local:8080`
3. `ErrNotConfigured` when `K8S_NAMESPACE` is unset/blank.
4. Error when `K8S_NAMESPACE` is set to an unknown value (fail-fast, not silent disable).

Unlike slippy, polly-api has no public ingress — the namespace-mapped URLs are the in-cluster service addresses. Callers outside the cluster (local dev, integration tests) must set `POLLY_API_URL`. The returned URL never ends with a trailing slash and carries no path suffix; the client appends the versioned route (`/v1/github/...`).

## Endpoints

- `UpsertCheckRun(ctx, workflowType, payload)` → `POST /v1/github/checkrun/workflow/{workflowType}`. Pass the workflow type as a string (`"build"`, `"unittest"`, `"deploy"`, `"offload"`, `"npmartifact"`, `"nugetartifact"`, `"artifactvalidation"`, `"secretsvalidation"`, `"secretscan"`, `"ghassecretsscan"`, `"alertgate"`, `"gitopsrollback"`, `"custom"`) so new polly-api workflow types work without a library bump. Typed payload structs (`BuildRequest`, `UnittestRequest`, …) are provided for compile-checked construction.
- `Comment(ctx, req)` → `POST /v1/github/comment`.

A 2xx response returns `nil`. Any non-2xx response returns an `*APIError`:

```go
var apiErr *pollyapi.APIError
if errors.As(err, &apiErr) && apiErr.Status == http.StatusTooManyRequests {
    time.Sleep(time.Duration(apiErr.RetryAfter) * time.Second)
}
```

`APIError` decoding tolerates both error body shapes polly-api emits: the `{error, code, retry_after}` envelope (auth/401) and Huma's `application/problem+json` (`{title, status, detail}`) for handler errors. When the body omits `retry_after`, the `Retry-After` header is used as a fallback.
