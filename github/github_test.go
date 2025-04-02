package github_handler

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGithubLoadConfig_Success(t *testing.T) {
	// Set environment variables
	os.Setenv("GITHUB_APP_PRIVATE_KEY", "test-private-key")
	os.Setenv("GITHUB_APP_ID", "12345")
	os.Setenv("GITHUB_APP_INSTALLATION_ID", "67890")
	defer func() {
		os.Unsetenv("GITHUB_APP_PRIVATE_KEY")
		os.Unsetenv("GITHUB_APP_ID")
		os.Unsetenv("GITHUB_APP_INSTALLATION_ID")
	}()

	config, err := GithubLoadConfig()
	assert.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "test-private-key", config.Pem)
	assert.Equal(t, "12345", config.AppId)
	assert.Equal(t, "67890", config.InstallId)
}

func TestGithubLoadConfig_MissingEnvVars(t *testing.T) {
	// Ensure environment variables are not set
	os.Unsetenv("GITHUB_APP_PRIVATE_KEY")
	os.Unsetenv("GITHUB_APP_ID")
	os.Unsetenv("GITHUB_APP_INSTALLATION_ID")

	config, err := GithubLoadConfig()
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "GITHUB_APP_PRIVATE_KEY is required")
}

func TestNewGithubSession_InvalidInputs(t *testing.T) {
	// Mock invalid inputs
	pem := ""
	appID := "invalid-app-id"
	installID := "invalid-install-id"

	session, err := NewGithubSession(pem, appID, installID)
	assert.Error(t, err)
	assert.Nil(t, session)
	assert.Contains(t, err.Error(), "error creating application token source")
}
