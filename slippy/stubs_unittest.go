//go:build unittest

package slippy

import "errors"

// GitHubConfig holds GitHub authentication configuration.
// This stub mirrors the real github.GraphQLConfig structure for unit testing.
type GitHubConfig struct {
	// AppID is the GitHub App ID
	AppID int64

	// PrivateKey is the PEM-encoded private key or path to key file
	PrivateKey string

	// EnterpriseURL is the GitHub Enterprise base URL (optional)
	EnterpriseURL string
}

// NewClickHouseStore is a stub that returns an error during unit tests.
// The real implementation is in clickhouse_store.go.
func NewClickHouseStore(dsn string) (SlipStore, error) {
	return nil, errors.New("ClickHouse store not available in unit tests - use NewClientWithDependencies with MockStore")
}

// NewGitHubClient is a stub that returns an error during unit tests.
// The real implementation is in github.go.
func NewGitHubClient(config GitHubConfig, logger Logger) (GitHubAPI, error) {
	return nil, errors.New("GitHub client not available in unit tests - use NewClientWithDependencies with MockGitHubAPI")
}
