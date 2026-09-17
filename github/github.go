package github_handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-github/v82/github"
	"github.com/jferrl/go-githubauth"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

// Timeouts for the HTTP calls made while authenticating as a GitHub App.
//
// Neither go-githubauth's default client nor the client returned by
// oauth2.NewClient sets an overall http.Client.Timeout or a
// ResponseHeaderTimeout: they bound the dial (30s) and the TLS handshake (10s)
// and nothing else. A connected-but-stalled GitHub therefore has no bound at
// all, and because the token mint runs synchronously inside a per-worker
// message handler in pushhookparser, a stall wedges that goroutine permanently
// rather than merely being slow.
//
// A token mint is a single small POST, and its caller already works to a
// 15-second budget for its own reachability calls, so 15s overall is generous
// for the real call and short enough that a stalled endpoint frees the worker;
// a multi-minute bound would not. Response headers are expected far sooner, and
// are bounded at the same 10s the transport already allows for the TLS
// handshake. REST calls made through the authenticated client can return
// larger payloads, so they get the 30s overall budget this module's GraphQL
// client already uses (see NewGraphQLClient), with the same 10s on headers.
const (
	tokenMintTimeout               = 15 * time.Second
	tokenMintResponseHeaderTimeout = 10 * time.Second
	apiRequestTimeout              = 30 * time.Second
	apiResponseHeaderTimeout       = 10 * time.Second
)

// ErrNilViper is returned by GithubLoadConfigFromViper when the caller
// passes a nil *viper.Viper. Exported as a sentinel so callers can match it
// with errors.Is rather than string comparison.
var ErrNilViper = errors.New("viper instance cannot be nil")

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
	// Load the configuration from environment variables using an isolated viper instance
	// to prevent cross-package state pollution.
	vp := viper.New()
	vp.SetEnvPrefix("GITHUB")
	if err := vp.BindEnv("pem", "GITHUB_APP_PRIVATE_KEY"); err != nil {
		return nil, fmt.Errorf("error binding env GITHUB_APP_PRIVATE_KEY: %w", err)
	}
	if err := vp.BindEnv("app_id", "GITHUB_APP_ID"); err != nil {
		return nil, fmt.Errorf("error binding env GITHUB_APP_ID: %w", err)
	}
	if err := vp.BindEnv("install_id", "GITHUB_APP_INSTALLATION_ID"); err != nil {
		return nil, fmt.Errorf("error binding env GITHUB_APP_INSTALLATION_ID: %w", err)
	}

	// Read environment variables
	vp.AutomaticEnv()

	return GithubLoadConfigFromViper(vp)
}

// GithubLoadConfigFromViper loads the configuration from a caller-provided
// viper instance. The caller owns the viper instance — this function does NOT
// call BindEnv/AutomaticEnv/SetEnvPrefix on it. The caller is responsible for
// any env binding they need, and may pre-populate values via vp.Set(...) to
// override secrets without touching process environment.
//
// Use this constructor when you need to:
//   - Inject explicit values for testing (vp.Set("pem", pemBytes))
//   - Share a viper instance across multiple configs in your application
//   - Override secrets pulled from a secret manager without setting env vars
//
// For the default env-binding behaviour, use GithubLoadConfig().
func GithubLoadConfigFromViper(vp *viper.Viper) (*GithubConfig, error) {
	if vp == nil {
		return nil, ErrNilViper
	}

	var GithubConfig GithubConfig

	// Unmarshal viper values into the Config struct
	if err := vp.Unmarshal(&GithubConfig); err != nil {
		return nil, fmt.Errorf("unable to decode into struct, %w", err)
	}

	if err := validateConfig(&GithubConfig); err != nil {
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
		return fmt.Errorf("error creating application token source: invalid private key: %w", err)
	}
	appID, err := strconv.ParseInt(s.appID, 10, 64)
	if err != nil {
		return fmt.Errorf("error parsing appId: %w", err)
	}
	installationID, err := strconv.ParseInt(s.installID, 10, 64)
	if err != nil {
		return fmt.Errorf("error parsing installationID: %w", err)
	}
	appTokenSource, err := githubauth.NewApplicationTokenSource(appID, privateKey)
	if err != nil {
		return fmt.Errorf("error creating application token source: %w", err)
	}
	installationTokenSource := githubauth.NewInstallationTokenSource(
		installationID,
		appTokenSource,
		githubauth.WithHTTPClient(boundedHTTPClient(tokenMintTimeout, tokenMintResponseHeaderTimeout)),
	)
	// oauth2.NewClient takes the base transport, and the overall timeout, of the
	// client it returns from the context — so the bounded client goes in there
	// rather than around the result, which would drop the token transport.
	ctx := context.WithValue(
		context.Background(),
		oauth2.HTTPClient,
		boundedHTTPClient(apiRequestTimeout, apiResponseHeaderTimeout),
	)
	httpClient := oauth2.NewClient(ctx, installationTokenSource)
	token, err := installationTokenSource.Token()
	if err != nil {
		return fmt.Errorf("error generating token: %w", err)
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

// boundedHTTPClient returns a client that cannot block forever: an overall
// timeout covering the whole request including the body, and a
// ResponseHeaderTimeout for the connected-but-silent server that the dial and
// TLS bounds never see. It starts from a clone of http.DefaultTransport so
// those bounds, proxy support and connection pooling are kept rather than
// discarded.
func boundedHTTPClient(timeout, responseHeaderTimeout time.Duration) *http.Client {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// http.DefaultTransport is an *http.Transport in every Go release to
		// date; should that ever change, the overall timeout still applies.
		return &http.Client{Timeout: timeout}
	}

	transport := defaultTransport.Clone()
	transport.ResponseHeaderTimeout = responseHeaderTimeout

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}
