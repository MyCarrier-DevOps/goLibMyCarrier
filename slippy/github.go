// Package slippy provides routing slip functionality for CI/CD pipelines.
//
// This file re-exports GitHub types from the goLibMyCarrier/github package
// for backward compatibility and convenience within the slippy package.
//go:build !unittest

package slippy

import (
	gh "github.com/MyCarrier-DevOps/goLibMyCarrier/github"
)

// GitHubConfig is an alias for the GitHub GraphQL configuration.
// It holds GitHub App authentication configuration for GraphQL API access.
type GitHubConfig = gh.GraphQLConfig

// GitHubClient is an alias for the GitHub GraphQL client.
// It provides commit ancestry queries with automatic installation discovery.
type GitHubClient = gh.GraphQLClient

// Installation is an alias for the GitHub App installation type.
type Installation = gh.Installation

// NewGitHubClient creates a new GitHub client with App authentication.
// The private key can be provided as PEM content (starts with "-----BEGIN")
// or as a file path.
//
// This function wraps github.NewGraphQLClient for backward compatibility.
func NewGitHubClient(cfg GitHubConfig, logger Logger) (*GitHubClient, error) {
	return gh.NewGraphQLClient(cfg, logger)
}
