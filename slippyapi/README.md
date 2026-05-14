[![Go Reference](https://pkg.go.dev/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/slippyapi.svg)](https://pkg.go.dev/github.com/MyCarrier-DevOps/goLibMyCarrier/slippyapi) [![Go Report Card](https://goreportcard.com/badge/github.com/MyCarrier-DevOps/goLibMyCarrier/slippyapi)](https://goreportcard.com/report/github.com/MyCarrier-DevOps/goLibMyCarrier/slippyapi)
# Slippy API URL Resolver

Lightweight, stdlib-only helpers for discovering the slippy-api HTTP base URL from the current environment. Kept separate from `goLibMyCarrier/slippy` (the state-machine library) so CLI tools, migrators, deploy-race checks, and push-event parsers can resolve the URL without pulling in ClickHouse, GitHub, or pipeline-config dependencies.

## Usage

```go
import (
    "errors"

    "github.com/MyCarrier-DevOps/goLibMyCarrier/slippyapi"
)

url, err := slippyapi.ResolveAPIURL()
if errors.Is(err, slippyapi.ErrNotConfigured) {
    // slippy is disabled in this environment; skip or no-op
    return
}
if err != nil {
    return err
}
// use url
```

## Resolution Order

1. `SLIPPY_API_URL` — explicit override (validated as http(s); trimmed of whitespace and trailing slash).
2. `K8S_NAMESPACE` mapped to a known cluster:
   - `argo-events` → `https://slippy-api.api.mycarrier.tech/v1`
   - `argo-events-test` → `https://slippy-api-test.api.mycarrier.tech/v1`
3. `ErrNotConfigured` when `K8S_NAMESPACE` is unset/blank.
4. Error when `K8S_NAMESPACE` is set to an unknown value (fail-fast, not silent disable).

The returned URL never ends with a trailing slash.

Note: this allow-list is intentionally narrower than `slippy/config.go`'s K8S_NAMESPACE classification. Sibling-package namespaces such as `feature-*` or `*-dev` will error here — operators on those namespaces must set `SLIPPY_API_URL` explicitly.
