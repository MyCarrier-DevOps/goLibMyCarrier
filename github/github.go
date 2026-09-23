package github_handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-github/v82/github"
	"github.com/jferrl/go-githubauth"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

// Bounds on the HTTP calls this package makes to GitHub. Every call goes
// through one shared transport (see sharedTransport), which bounds the wait for
// response headers and keeps http.DefaultTransport's dial and TLS handshake
// bounds; each client adds an overall timeout, which also covers a body that
// stalls after its headers arrive. The bounds are per HTTP request.
const (
	// TokenMintTimeout bounds one installation-token mint, end to end. A mint is
	// a single small POST and a throttled one is not retried (see
	// newInstallationClient), so this is the whole of what a mint can cost: in
	// NewGithubSession, and on every later refresh.
	TokenMintTimeout = 15 * time.Second

	// DefaultRequestTimeout bounds each REST request made through
	// GithubSession.Client(), and each GraphQLClient request. A session can
	// replace it with WithRequestTimeout.
	DefaultRequestTimeout = 30 * time.Second

	// ResponseHeaderTimeout bounds the wait for a response's headers on every
	// connection this package opens, so a server that accepts the connection and
	// then says nothing fails here rather than holding the caller until the
	// overall bound.
	ResponseHeaderTimeout = 10 * time.Second
)

// ErrNilViper is returned by GithubLoadConfigFromViper when the caller
// passes a nil *viper.Viper. Exported as a sentinel so callers can match it
// with errors.Is rather than string comparison.
var ErrNilViper = errors.New("viper instance cannot be nil")

type GithubSession struct {
	pem       string
	appID     string
	installID string
	cfg       sessionConfig
	auth      *oauth2.Token
	client    *github.Client
}

// SessionOption configures a session built by NewGithubSessionWithOptions.
type SessionOption func(*sessionConfig)

// WithContext sets the context the session mints installation tokens under: the
// mint NewGithubSessionWithOptions performs, and every refresh after it.
// Cancelling it aborts a mint in flight and fails every later one, so it must
// live as long as the session does; for a session built per unit of work, that
// work's context is the right one. The default is context.Background().
//
// A refresh that falls inside a REST call is bounded by this context and by
// TokenMintTimeout, not by that call's own context: the oauth2 transport fetches
// the token before it reads the request's context.
func WithContext(ctx context.Context) SessionOption {
	return func(cfg *sessionConfig) { cfg.ctx = ctx }
}

// WithRequestTimeout replaces DefaultRequestTimeout as the overall bound on each
// REST request made through Client(), for a session whose calls legitimately
// take longer, such as large content or archive downloads. Zero removes the
// overall bound, leaving the request's own context and ResponseHeaderTimeout.
func WithRequestTimeout(timeout time.Duration) SessionOption {
	return func(cfg *sessionConfig) { cfg.requestTimeout = timeout }
}

// sessionConfig holds what a session's HTTP calls are bounded by. mintTimeout,
// transport and mintBaseURL are not options: tests set them to exercise the real
// wiring against a local server with short bounds.
type sessionConfig struct {
	ctx            context.Context
	mintTimeout    time.Duration
	requestTimeout time.Duration
	transport      http.RoundTripper
	mintBaseURL    string // "": github.com
}

func defaultSessionConfig() sessionConfig {
	return sessionConfig{
		ctx:            context.Background(),
		mintTimeout:    TokenMintTimeout,
		requestTimeout: DefaultRequestTimeout,
		transport:      sharedTransport(),
	}
}

// newSessionConfig applies opts to the default config and rejects the values no
// option may leave behind.
func newSessionConfig(opts ...SessionOption) (sessionConfig, error) {
	cfg := defaultSessionConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.ctx == nil {
		return sessionConfig{}, errors.New("WithContext: context must not be nil")
	}
	if cfg.requestTimeout < 0 {
		return sessionConfig{}, fmt.Errorf(
			"WithRequestTimeout: timeout must not be negative, got %s", cfg.requestTimeout,
		)
	}
	return cfg, nil
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

// NewGithubSession creates a new Github session using the provided PEM file, App ID, and Install ID.
// It is NewGithubSessionWithOptions with no options.
func NewGithubSession(pem, appID, installID string) (*GithubSession, error) {
	return NewGithubSessionWithOptions(pem, appID, installID)
}

// NewGithubSessionWithOptions creates a new Github session, configured by opts;
// see WithContext and WithRequestTimeout. It is a separate function rather than a
// variadic NewGithubSession because consumers hold NewGithubSession as a typed
// function value, which an added parameter would stop compiling.
func NewGithubSessionWithOptions(pem, appID, installID string, opts ...SessionOption) (*GithubSession, error) {
	cfg, err := newSessionConfig(opts...)
	if err != nil {
		return nil, err
	}
	session := &GithubSession{
		pem:       pem,
		appID:     appID,
		installID: installID,
		cfg:       cfg,
	}

	if err := session.authenticate(); err != nil {
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
	installationTokenSource, httpClient := newInstallationClient(appTokenSource, installationID, s.cfg)
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

// newInstallationClient builds the installation-token source and the
// authenticated REST client a session uses. authenticate() calls it with the
// session's config, and the tests call it with the same config shortened, so
// what they exercise is this wiring rather than a copy of it.
//
// The mint runs on cfg.transport with an overall bound of cfg.mintTimeout, under
// cfg.ctx, and is not retried when GitHub throttles it. go-githubauth otherwise
// sleeps up to 60s on a 429 or a rate-limit 403 and tries again, outside any
// client timeout, so a mint's worst case would be that sleep plus a second
// request. A throttled mint instead fails at once with an error wrapping this package's
// ErrRateLimited and githubauth.ErrRateLimited, and the caller decides whether to retry.
// go-githubauth's default client is not used: its 30s header bound could never
// fire inside the mint's shorter overall one.
//
// The REST client takes its transport and overall timeout from the client stored
// under oauth2.HTTPClient, because oauth2.NewClient copies both from there;
// wrapping its result instead would drop the token transport.
func newInstallationClient(
	app oauth2.TokenSource,
	installationID int64,
	cfg sessionConfig,
) (oauth2.TokenSource, *http.Client) {
	mintOpts := []githubauth.InstallationTokenSourceOpt{
		githubauth.WithHTTPClient(&http.Client{Transport: cfg.transport, Timeout: cfg.mintTimeout}),
		githubauth.WithContext(cfg.ctx),
		githubauth.WithRetryOnThrottle(false),
	}
	if cfg.mintBaseURL != "" {
		mintOpts = append(mintOpts, githubauth.WithBaseURL(cfg.mintBaseURL))
	}
	tokenSource := rateLimitJoiner{githubauth.NewInstallationTokenSource(installationID, app, mintOpts...)}

	base := &http.Client{Transport: cfg.transport, Timeout: cfg.requestTimeout}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, base)
	return tokenSource, oauth2.NewClient(ctx, tokenSource)
}

// rateLimitJoiner adds this package's ErrRateLimited to a throttled mint's error, so a caller
// can branch on this package's sentinel rather than on its auth library's. It wraps the token
// source once, so both mint paths carry it: the mint in NewGithubSession, and a refresh inside a
// REST call, which oauth2.Transport returns unchanged. errors.As still reaches
// *githubauth.RateLimitError for the wait GitHub asked for.
type rateLimitJoiner struct{ oauth2.TokenSource }

func (r rateLimitJoiner) Token() (*oauth2.Token, error) {
	token, err := r.TokenSource.Token()
	if err != nil && errors.Is(err, githubauth.ErrRateLimited) {
		return nil, fmt.Errorf("%w: %w", ErrRateLimited, err)
	}
	return token, err
}

// sharedTransport returns the one transport every HTTP call in this package goes
// through, so all sessions and GraphQL clients in a process share one connection
// pool to GitHub, keeping up to MaxIdleConns (100) idle connections between bursts (see
// newBoundedTransport). A transport per session would cost a consumer that builds a
// session per message a TCP and TLS handshake on every one, and leave each
// session's idle connections behind until they time out. ghinstallation asks for
// the same sharing of the transport it is given.
//
// It is package state because sessions are built independently and share
// nothing else; it is built on first use, so it clones http.DefaultTransport as
// it stands then.
var sharedTransport = sync.OnceValue(func() http.RoundTripper {
	return newBoundedTransport(ResponseHeaderTimeout)
})

// newBoundedTransport returns a clone of http.DefaultTransport that bounds the
// wait for response headers. The clone keeps its proxy support, its dial bound
// (30s, with 30s keep-alive) and its TLS handshake bound (10s).
func newBoundedTransport(responseHeaderTimeout time.Duration) http.RoundTripper {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// http.DefaultTransport is an *http.Transport in every Go release to
		// date; should that ever change, the clients' overall timeouts still apply.
		return http.DefaultTransport
	}

	transport := defaultTransport.Clone()
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	// Every call this package makes goes to one host, so the per-host idle limit is the
	// transport's whole idle limit (MaxIdleConns, 100 on the clone) rather than the clone's
	// default of 2. This governs HTTP/1.1 connections only: api.github.com negotiates HTTP/2,
	// where a pooled connection is not consumed by the request using it, so one connection
	// carries a whole burst and this limit does not decide whether the next one re-handshakes.
	// A cold burst dials one connection per pending request on either protocol. Kept as a
	// cheap default for an HTTP/1.1 endpoint, or a proxy that downgrades to one.
	transport.MaxIdleConnsPerHost = transport.MaxIdleConns
	return transport
}
