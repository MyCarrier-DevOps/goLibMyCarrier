package github_handler

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGithubLoadConfig_Success(t *testing.T) {
	// Set environment variables
	if err := os.Setenv("GITHUB_APP_PRIVATE_KEY", "test-private-key"); err != nil {
		t.Fatalf("Failed to set GITHUB_APP_PRIVATE_KEY: %v", err.Error())
	}
	if err := os.Setenv("GITHUB_APP_ID", "12345"); err != nil {
		t.Fatalf("Failed to set GITHUB_APP_ID: %v", err.Error())
	}
	if err := os.Setenv("GITHUB_APP_INSTALLATION_ID", "67890"); err != nil {
		t.Fatalf("Failed to set GITHUB_APP_INSTALLATION_ID: %v", err.Error())
	}
	defer func() {
		if err := os.Unsetenv("GITHUB_APP_PRIVATE_KEY"); err != nil {
			t.Errorf("Failed to unset GITHUB_APP_PRIVATE_KEY: %v", err.Error())
		}
		if err := os.Unsetenv("GITHUB_APP_ID"); err != nil {
			t.Errorf("Failed to unset GITHUB_APP_ID: %v", err.Error())
		}
		if err := os.Unsetenv("GITHUB_APP_INSTALLATION_ID"); err != nil {
			t.Errorf("Failed to unset GITHUB_APP_INSTALLATION_ID: %v", err.Error())
		}
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
	if err := os.Unsetenv("GITHUB_APP_PRIVATE_KEY"); err != nil {
		t.Errorf("Failed to unset GITHUB_APP_PRIVATE_KEY: %v", err)
	}
	if err := os.Unsetenv("GITHUB_APP_ID"); err != nil {
		t.Errorf("Failed to unset GITHUB_APP_ID: %v", err)
	}
	if err := os.Unsetenv("GITHUB_APP_INSTALLATION_ID"); err != nil {
		t.Errorf("Failed to unset GITHUB_APP_INSTALLATION_ID: %v", err)
	}

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
