package github_handler

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/go-github/v69/github"
	"github.com/jferrl/go-githubauth"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

type GithubSession struct {
	pem       string
	appID     string
	installID string
	auth      *oauth2.Token
	client    *github.Client
}

type GithubConfig struct {
	Pem       string `mapstructure:"pem"`
	AppId     string `mapstructure:"app_id"`
	InstallId string `mapstructure:"install_id"`
}

func GithubLoadConfig() (*GithubConfig, error) {
	// Load the configuration from environment variables or a config file
	viper.SetEnvPrefix("GITHUB")
	if err := viper.BindEnv("github.pem", "GITHUB_APP_PRIVATE_KEY"); err != nil {
		return nil, fmt.Errorf("error binding env GITHUB_APP_PRIVATE_KEY: %v", err)
	}
	if err := viper.BindEnv("github.app_id", "GITHUB_APP_ID"); err != nil {
		return nil, fmt.Errorf("error binding env GITHUB_APP_ID: %v", err)
	}
	if err := viper.BindEnv("github.install_id", "GITHUB_APP_INSTALLATION_ID"); err != nil {
		return nil, fmt.Errorf("error binding env GITHUB_APP_INSTALLATION_ID: %v", err)
	}

	// Read environment variables
	viper.AutomaticEnv()

	var GithubConfig GithubConfig

	// Unmarshal environment variables into the Config struct
	if err := viper.Unmarshal(&GithubConfig); err != nil {
		return nil, fmt.Errorf("unable to decode into struct, %v", err)
	}

	return &GithubConfig, nil
}

// NewGithubSession creates a new Github session using the provided PEM file, App ID, and Install ID
func NewGithubSession(pem, appID, installID string) (*GithubSession, error) {
	session := &GithubSession{
		pem:       pem,
		appID:     appID,
		installID: installID,
	}

	err := session.authenticate()
	if err != nil {
		return nil, err
	}

	return session, nil
}

// Authenticate with Github using the provided PEM file, App ID, and Install ID
func (s *GithubSession) authenticate() error {
	privateKey := []byte(s.pem)
	appID, _ := strconv.ParseInt(s.appID, 10, 64)
	installationID, _ := strconv.ParseInt(s.installID, 10, 64)
	appTokenSource, err := githubauth.NewApplicationTokenSource(appID, privateKey)
	if err != nil {
		return fmt.Errorf("error creating application token source: %s", err)
	}
	installationTokenSource := githubauth.NewInstallationTokenSource(installationID, appTokenSource)
	httpClient := oauth2.NewClient(context.Background(), installationTokenSource)
	token, err := installationTokenSource.Token()
	if err != nil {
		return fmt.Errorf("error generating token: %s", err)
	}
	s.client = github.NewClient(httpClient)
	s.auth = token
	return nil
}

// Get AuthToken returns the authentication token
func (s *GithubSession) AuthToken() *oauth2.Token {
	return s.auth
}

// Get Client returns the authenticated Github client
func (s *GithubSession) Client() *github.Client {
	return s.client
}
