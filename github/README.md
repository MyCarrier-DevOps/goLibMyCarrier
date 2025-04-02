# GitHub Handler Library

This library provides a utility for interacting with the GitHub API using a GitHub App's credentials. It includes functionality for loading configuration, authenticating with GitHub, and accessing the GitHub API client.

## Features

- Load GitHub App configuration from environment variables.
- Authenticate with GitHub using a private key, App ID, and Installation ID.
- Retrieve an authenticated GitHub client and access token.

## Configuration

The library expects the following environment variables to be set:

- `GITHUB_APP_PRIVATE_KEY`: The private key of the GitHub App.
- `GITHUB_APP_ID`: The App ID of the GitHub App.
- `GITHUB_APP_INSTALLATION_ID`: The Installation ID of the GitHub App.

## Usage

### Loading Configuration

Use the `GithubLoadConfig` function to load the configuration from environment variables:

```go
config, err := GithubLoadConfig()
if err != nil {
    log.Fatalf("Failed to load GitHub configuration: %v", err)
}
```

### Creating a GitHub Session

Use the `NewGithubSession` function to create a new session:

```go
session, err := NewGithubSession(config.Pem, config.AppId, config.InstallId)
if err != nil {
    log.Fatalf("Failed to create GitHub session: %v", err)
}
```

### Accessing the Authenticated Client

Retrieve the authenticated GitHub client and token:

```go
client := session.Client()
token := session.AuthToken()
```

You can now use the `client` to interact with the GitHub API.

## Error Handling

Ensure proper error handling when loading configuration or creating a session, as invalid credentials or missing environment variables will result in errors.

## Dependencies

This library uses the following dependencies:

- [go-github](https://github.com/google/go-github): GitHub API client for Go.
- [go-githubauth](https://github.com/jferrl/go-githubauth): Authentication utilities for GitHub Apps.
- [golang-jwt](https://github.com/golang-jwt/jwt): JWT library for Go.
- [spf13/viper](https://github.com/spf13/viper): Configuration management for Go.
