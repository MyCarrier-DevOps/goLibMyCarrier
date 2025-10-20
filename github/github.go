package github_handler

import (
	"context"
	"fmt"
	"strconv"

	"github.com/golang-jwt/jwt"
	"github.com/google/go-github/v73/github"
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
	if err := viper.BindEnv("pem", "GITHUB_APP_PRIVATE_KEY"); err != nil {
		return nil, fmt.Errorf("error binding env GITHUB_APP_PRIVATE_KEY: %w", err)
	}
	if err := viper.BindEnv("app_id", "GITHUB_APP_ID"); err != nil {
		return nil, fmt.Errorf("error binding env GITHUB_APP_ID: %w", err)
	}
	if err := viper.BindEnv("install_id", "GITHUB_APP_INSTALLATION_ID"); err != nil {
		return nil, fmt.Errorf("error binding env GITHUB_APP_INSTALLATION_ID: %w", err)
	}

	// Read environment variables
	viper.AutomaticEnv()

	var GithubConfig GithubConfig

	// Unmarshal environment variables into the Config struct
	if err := viper.Unmarshal(&GithubConfig); err != nil {
		return nil, fmt.Errorf("unable to decode into struct, %w", err)
	}

	err := validateConfig(&GithubConfig)
	if err != nil {
		return nil, err
	}
	return &GithubConfig, nil
}

// Validate the configuration
func validateConfig(config *GithubConfig) error {
	if config.Pem == "" || len(config.Pem) < 10 { // Ensure the key is not only non-empty but also valid
		return fmt.Errorf("GITHUB_APP_PRIVATE_KEY is required and must be valid")
	}
	if config.AppId == "" {
		return fmt.Errorf("GITHUB_APP_ID is required")
	}
	if config.InstallId == "" {
		return fmt.Errorf("GITHUB_APP_INSTALLATION_ID is required")
	}
	return nil
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

// PullRequestOptions contains options for creating a pull request
type PullRequestOptions struct {
	Title               string   `json:"title"`
	Head                string   `json:"head"`
	Base                string   `json:"base"`
	Body                *string  `json:"body,omitempty"`
	Draft               *bool    `json:"draft,omitempty"`
	MaintainerCanModify *bool    `json:"maintainer_can_modify,omitempty"`
	Assignees           []string `json:"assignees,omitempty"`
	Reviewers           []string `json:"reviewers,omitempty"`
	TeamReviewers       []string `json:"team_reviewers,omitempty"`
	Labels              []string `json:"labels,omitempty"`
	Milestone           *int     `json:"milestone,omitempty"`
}

// Validate validates the pull request options
func (opts *PullRequestOptions) Validate() error {
	if opts.Title == "" {
		return fmt.Errorf("title is required")
	}
	if opts.Head == "" {
		return fmt.Errorf("head branch is required")
	}
	if opts.Base == "" {
		return fmt.Errorf("base branch is required")
	}
	if opts.Head == opts.Base {
		return fmt.Errorf("head and base branches cannot be the same")
	}
	return nil
}

// CreatePullRequest creates a new pull request in the specified repository using the provided options
func (s *GithubSession) CreatePullRequest(
	ctx context.Context,
	owner, repo string,
	opts *PullRequestOptions,
) (*github.PullRequest, error) {
	if err := s.validateCreatePRRequest(opts); err != nil {
		return nil, err
	}

	pr, err := s.createBasePullRequest(ctx, owner, repo, opts)
	if err != nil {
		return nil, err
	}

	if err := s.configurePullRequest(ctx, owner, repo, pr, opts); err != nil {
		return pr, err
	}

	return pr, nil
}

// CreatePullRequestSimple creates a pull request with basic options (backward compatibility)
func (s *GithubSession) CreatePullRequestSimple(
	ctx context.Context,
	owner, repo, title, head, base string,
) (*github.PullRequest, error) {
	opts := &PullRequestOptions{
		Title: title,
		Head:  head,
		Base:  base,
	}
	return s.CreatePullRequest(ctx, owner, repo, opts)
}

// Get AuthToken returns the authentication token
func (s *GithubSession) AuthToken() *oauth2.Token {
	return s.auth
}

// Get Client returns the authenticated Github client
func (s *GithubSession) Client() *github.Client {
	return s.client
}

// authenticate with Github using the provided PEM file, App ID, and Install ID
func (s *GithubSession) authenticate() error {
	privateKey := []byte(s.pem)
	if _, err := jwt.ParseRSAPrivateKeyFromPEM(privateKey); err != nil {
		return fmt.Errorf("error creating application token source: invalid private key: %s", err.Error())
	}
	appID, err := strconv.ParseInt(s.appID, 10, 64)
	if err != nil {
		return fmt.Errorf("error parsing appId ")
	}
	installationID, err := strconv.ParseInt(s.installID, 10, 64)
	if err != nil {
		return fmt.Errorf("error parsing installationID: %s", err.Error())
	}
	appTokenSource, err := githubauth.NewApplicationTokenSource(appID, privateKey)
	if err != nil {
		return fmt.Errorf("error creating application token source: %s", err.Error())
	}
	installationTokenSource := githubauth.NewInstallationTokenSource(installationID, appTokenSource)
	httpClient := oauth2.NewClient(context.Background(), installationTokenSource)
	token, err := installationTokenSource.Token()
	if err != nil {
		return fmt.Errorf("error generating token: %s", err.Error())
	}
	s.client = github.NewClient(httpClient)
	s.auth = token
	return nil
}

// validateCreatePRRequest validates the pull request creation request
func (s *GithubSession) validateCreatePRRequest(opts *PullRequestOptions) error {
	if opts == nil {
		return fmt.Errorf("pull request options cannot be nil")
	}
	if err := opts.Validate(); err != nil {
		return fmt.Errorf("invalid pull request options: %w", err)
	}
	if s.client == nil {
		return fmt.Errorf("github client is not initialized")
	}
	return nil
}

// createBasePullRequest creates the basic pull request
func (s *GithubSession) createBasePullRequest(
	ctx context.Context,
	owner, repo string,
	opts *PullRequestOptions,
) (*github.PullRequest, error) {
	prRequest := &github.NewPullRequest{
		Title:               &opts.Title,
		Head:                &opts.Head,
		Base:                &opts.Base,
		Body:                opts.Body,
		Draft:               opts.Draft,
		MaintainerCanModify: opts.MaintainerCanModify,
	}

	pr, _, err := s.client.PullRequests.Create(ctx, owner, repo, prRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to create pull request: %w", err)
	}
	return pr, nil
}

// configurePullRequest adds assignees, reviewers, labels, and milestone to the pull request
func (s *GithubSession) configurePullRequest(
	ctx context.Context,
	owner, repo string,
	pr *github.PullRequest,
	opts *PullRequestOptions,
) error {
	if err := s.addAssignees(ctx, owner, repo, pr, opts); err != nil {
		return err
	}
	if err := s.addReviewers(ctx, owner, repo, pr, opts); err != nil {
		return err
	}
	if err := s.addLabels(ctx, owner, repo, pr, opts); err != nil {
		return err
	}
	return s.setMilestone(ctx, owner, repo, pr, opts)
}

// addAssignees adds assignees to the pull request
func (s *GithubSession) addAssignees(
	ctx context.Context,
	owner, repo string,
	pr *github.PullRequest,
	opts *PullRequestOptions,
) error {
	if len(opts.Assignees) > 0 {
		_, _, err := s.client.Issues.AddAssignees(ctx, owner, repo, pr.GetNumber(), opts.Assignees)
		if err != nil {
			return fmt.Errorf("pull request created but failed to add assignees: %w", err)
		}
	}
	return nil
}

// addReviewers adds reviewers to the pull request
func (s *GithubSession) addReviewers(
	ctx context.Context,
	owner, repo string,
	pr *github.PullRequest,
	opts *PullRequestOptions,
) error {
	if len(opts.Reviewers) > 0 || len(opts.TeamReviewers) > 0 {
		reviewersRequest := github.ReviewersRequest{
			Reviewers:     opts.Reviewers,
			TeamReviewers: opts.TeamReviewers,
		}
		_, _, err := s.client.PullRequests.RequestReviewers(ctx, owner, repo, pr.GetNumber(), reviewersRequest)
		if err != nil {
			return fmt.Errorf("pull request created but failed to add reviewers: %w", err)
		}
	}
	return nil
}

// addLabels adds labels to the pull request
func (s *GithubSession) addLabels(
	ctx context.Context,
	owner, repo string,
	pr *github.PullRequest,
	opts *PullRequestOptions,
) error {
	if len(opts.Labels) > 0 {
		_, _, err := s.client.Issues.AddLabelsToIssue(ctx, owner, repo, pr.GetNumber(), opts.Labels)
		if err != nil {
			return fmt.Errorf("pull request created but failed to add labels: %w", err)
		}
	}
	return nil
}

// setMilestone sets milestone for the pull request
func (s *GithubSession) setMilestone(
	ctx context.Context,
	owner, repo string,
	pr *github.PullRequest,
	opts *PullRequestOptions,
) error {
	if opts.Milestone != nil {
		issueRequest := &github.IssueRequest{
			Milestone: opts.Milestone,
		}
		_, _, err := s.client.Issues.Edit(ctx, owner, repo, pr.GetNumber(), issueRequest)
		if err != nil {
			return fmt.Errorf("pull request created but failed to set milestone: %w", err)
		}
	}
	return nil
}
