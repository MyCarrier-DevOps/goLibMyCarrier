package github_handler

import (
	"context"
	"fmt"
	"os"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	ghhttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/google/go-github/v73/github"
	"golang.org/x/oauth2"
)

// GithubSessionInterface defines the interface for Github sessions
type GithubSessionInterface interface {
	AuthToken() *oauth2.Token
	Client() *github.Client
}

// CloneOptions defines the configuration for cloning repositories
type CloneOptions struct {
	// Repository URL to clone
	RepositoryURL string
	// Local directory to clone into
	WorkDir string
	// Branch to clone (optional, defaults to default branch)
	Branch string
	// Authentication credentials (optional, will use session token if not provided)
	Auth *ghhttp.BasicAuth
	// Git clone options (optional, will use defaults if not provided)
	GitCloneOptions *git.CloneOptions
	// Progress output (optional, defaults to os.Stdout)
	Progress *os.File
	// Single branch clone (defaults to true)
	SingleBranch bool
	// Clone depth (defaults to 1 for shallow clone)
	Depth int
	// No checkout - clone without checking out files (defaults to false)
	NoCheckout bool
}

// CheckRunOptions defines the configuration for creating check runs
type CheckRunOptions struct {
	// Required fields
	HeadSHA    string
	Repository string
	Org        string
	Name       string

	// Optional fields with defaults
	Title      string
	Conclusion string
	Status     string
	Summary    string
	Text       string

	// Advanced options
	DetailsURL  string
	ExternalID  string
	StartedAt   *time.Time
	CompletedAt *time.Time
	Actions     []*github.CheckRunAction
	Images      []*github.CheckRunImage
	Annotations []*github.CheckRunAnnotation
}

// DefaultCloneOptions provides sensible defaults for cloning
func DefaultCloneOptions() CloneOptions {
	return CloneOptions{
		WorkDir:      "/work",
		Progress:     os.Stdout,
		SingleBranch: true,
		Depth:        1,
	}
}

// CloneRepository clones the specified GitHub repository with the provided options
func CloneRepository(session GithubSessionInterface, options CloneOptions) (*git.Repository, error) {
	if options.RepositoryURL == "" {
		return nil, fmt.Errorf("repository URL is required")
	}

	token := session.AuthToken()
	if token == nil {
		return nil, fmt.Errorf("error getting auth token for GitHub session")
	}

	// Apply defaults
	if options.WorkDir == "" {
		options.WorkDir = "/work"
	}
	if options.Progress == nil {
		options.Progress = os.Stdout
	}
	if options.Depth == 0 {
		options.Depth = 1
	}

	// Setup authentication if not provided
	if options.Auth == nil {
		options.Auth = &ghhttp.BasicAuth{
			Username: "x-access-token",
			Password: token.AccessToken,
		}
	}

	// Setup git clone options if not provided
	if options.GitCloneOptions == nil {
		gitOptions := &git.CloneOptions{
			URL:          options.RepositoryURL,
			Progress:     options.Progress,
			SingleBranch: options.SingleBranch,
			Depth:        options.Depth,
			Auth:         options.Auth,
			NoCheckout:   options.NoCheckout,
		}

		// Set branch reference if specified
		if options.Branch != "" {
			gitOptions.ReferenceName = plumbing.NewBranchReferenceName(options.Branch)
		}

		options.GitCloneOptions = gitOptions
	}

	repo, err := git.PlainClone(options.WorkDir, false, options.GitCloneOptions)
	if err != nil {
		return nil, fmt.Errorf("error cloning repository: %w", err)
	}

	return repo, nil
}

// validateCheckRunOptions validates the required fields for creating a check run
func validateCheckRunOptions(options CheckRunOptions) error {
	if options.HeadSHA == "" {
		return fmt.Errorf("head SHA is required")
	}
	if options.Repository == "" {
		return fmt.Errorf("repository name is required")
	}
	if options.Org == "" {
		return fmt.Errorf("organization name is required")
	}
	if options.Name == "" {
		return fmt.Errorf("check run name is required")
	}
	return nil
}

// applyCheckRunDefaults applies default values to check run options
func applyCheckRunDefaults(options *CheckRunOptions) {
	if options.Status == "" {
		options.Status = "completed"
	}
	if options.Conclusion == "" && options.Status == "completed" {
		options.Conclusion = "success"
	}
}

// hasOutputContent checks if any output content is provided for check run
func hasOutputContent(options CheckRunOptions) bool {
	return options.Title != "" ||
		options.Summary != "" ||
		options.Text != "" ||
		len(options.Images) > 0 ||
		len(options.Annotations) > 0
}

// buildCheckRunOutput creates check run output from options
func buildCheckRunOutput(options CheckRunOptions) *github.CheckRunOutput {
	output := &github.CheckRunOutput{}

	if options.Title != "" {
		output.Title = github.Ptr(options.Title)
	}
	if options.Summary != "" {
		output.Summary = github.Ptr(options.Summary)
	}
	if options.Text != "" {
		output.Text = github.Ptr(options.Text)
	}
	if len(options.Images) > 0 {
		output.Images = options.Images
	}
	if len(options.Annotations) > 0 {
		output.Annotations = options.Annotations
	}

	return output
}

// buildCheckRunOptions creates GitHub check run options from our options
func buildCheckRunOptions(options CheckRunOptions) *github.CreateCheckRunOptions {
	checkRunOptions := &github.CreateCheckRunOptions{
		Name:    options.Name,
		HeadSHA: options.HeadSHA,
		Status:  github.Ptr(options.Status),
	}

	// Add optional fields
	if options.Conclusion != "" {
		checkRunOptions.Conclusion = github.Ptr(options.Conclusion)
	}
	if options.DetailsURL != "" {
		checkRunOptions.DetailsURL = github.Ptr(options.DetailsURL)
	}
	if options.ExternalID != "" {
		checkRunOptions.ExternalID = github.Ptr(options.ExternalID)
	}
	if options.StartedAt != nil {
		checkRunOptions.StartedAt = &github.Timestamp{Time: *options.StartedAt}
	}
	if options.CompletedAt != nil {
		checkRunOptions.CompletedAt = &github.Timestamp{Time: *options.CompletedAt}
	}
	if len(options.Actions) > 0 {
		checkRunOptions.Actions = options.Actions
	}

	// Setup output if any content is provided
	if hasOutputContent(options) {
		checkRunOptions.Output = buildCheckRunOutput(options)
	}

	return checkRunOptions
}

// CreateCheckRun creates a check run in the specified GitHub repository with the provided options
func CreateCheckRun(ctx context.Context, session GithubSessionInterface, options CheckRunOptions) error {
	if err := validateCheckRunOptions(options); err != nil {
		return err
	}

	applyCheckRunDefaults(&options)
	checkRunOptions := buildCheckRunOptions(options)

	_, _, err := session.Client().Checks.CreateCheckRun(ctx, options.Org, options.Repository, *checkRunOptions)
	if err != nil {
		return fmt.Errorf("error creating check run: %w", err)
	}

	return nil
}

// CommitOptions defines the configuration for committing changes
type CommitOptions struct {
	// Branch to commit to
	Branch string
	// Commit message
	Message string
	// Files to add to the commit (if empty, will add all changes)
	Files []string
	// Add all changes if true, otherwise only add specified files
	AddAll bool
	// Author information
	Author AuthorInfo
	// Authentication for pushing
	Auth *ghhttp.BasicAuth
	// Remote URL (optional, will use existing remote if not specified)
	RemoteURL string
	// Remote name (defaults to "origin")
	RemoteName string
}

// AuthorInfo defines the author information for commits
type AuthorInfo struct {
	Name  string
	Email string
}

// GetDefaultAuthor provides a default author configuration
func GetDefaultAuthor() AuthorInfo {
	return AuthorInfo{
		Name:  "mycarrier-automation [bot]",
		Email: "devops@mycarrier.io",
	}
}

// validateCommitOptions validates the required fields for committing changes
func validateCommitOptions(options CommitOptions) error {
	if options.Branch == "" {
		return fmt.Errorf("branch is required")
	}
	if options.Message == "" {
		return fmt.Errorf("commit message is required")
	}
	return nil
}

// applyCommitDefaults applies default values to commit options
func applyCommitDefaults(options *CommitOptions) {
	if options.RemoteName == "" {
		options.RemoteName = "origin"
	}
	if options.Author.Name == "" || options.Author.Email == "" {
		options.Author = GetDefaultAuthor()
	}
}

// addFilesToStaging adds files to git staging area
func addFilesToStaging(worktree *git.Worktree, options CommitOptions) error {
	var err error
	switch {
	case options.AddAll:
		err = worktree.AddWithOptions(&git.AddOptions{
			All: true,
		})
	case len(options.Files) > 0:
		// Add specific files
		for _, file := range options.Files {
			err = worktree.AddWithOptions(&git.AddOptions{
				All:  false,
				Path: file,
			})
			if err != nil {
				return fmt.Errorf("error adding file %s to commit: %w", file, err)
			}
		}
	default:
		return fmt.Errorf("either specify files to add or set AddAll to true")
	}

	if err != nil {
		return fmt.Errorf("error adding files to commit: %w", err)
	}
	return nil
}

// createCommit creates a git commit with the provided options
func createCommit(worktree *git.Worktree, options CommitOptions) (plumbing.Hash, error) {
	commitOptions := &git.CommitOptions{
		All: false,
		Author: &object.Signature{
			Name:  options.Author.Name,
			Email: options.Author.Email,
			When:  time.Now(),
		},
	}

	commit, err := worktree.Commit(options.Message, commitOptions)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("error committing changes: %w", err)
	}
	return commit, nil
}

// pushChanges pushes the committed changes to the remote repository
func pushChanges(repo *git.Repository, options CommitOptions) error {
	if options.Auth == nil {
		return nil // No push if no auth provided
	}

	pushOptions := &git.PushOptions{
		RemoteName: options.RemoteName,
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/heads/%s", options.Branch, options.Branch)),
		},
		Auth: options.Auth,
	}

	// Set remote URL if specified
	if options.RemoteURL != "" {
		pushOptions.RemoteURL = options.RemoteURL
	}

	err := repo.Push(pushOptions)
	if err != nil {
		return fmt.Errorf("error pushing changes: %w", err)
	}
	return nil
}

// CommitChanges commits changes to the repository with the provided options
func CommitChanges(repo *git.Repository, options CommitOptions) (string, error) {
	if err := validateCommitOptions(options); err != nil {
		return "", err
	}

	applyCommitDefaults(&options)

	worktree, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("error getting worktree: %w", err)
	}

	if err := addFilesToStaging(worktree, options); err != nil {
		return "", err
	}

	commit, err := createCommit(worktree, options)
	if err != nil {
		return "", err
	}

	if err := pushChanges(repo, options); err != nil {
		return commit.String(), err
	}

	return commit.String(), nil
}

// CommitChangesWithToken is a convenience function that creates CommitOptions with token authentication
func CommitChangesWithToken(
	repo *git.Repository,
	branch, message string,
	files []string,
	githubAccessToken string,
) (string, error) {
	options := CommitOptions{
		Branch:  branch,
		Message: message,
		Files:   files,
		Author:  GetDefaultAuthor(),
		Auth: &ghhttp.BasicAuth{
			Username: "x-access-token",
			Password: githubAccessToken,
		},
	}
	return CommitChanges(repo, options)
}

// CommitAllChangesWithToken is a convenience function that commits all changes with token authentication
func CommitAllChangesWithToken(
	repo *git.Repository,
	branch, message string,
	githubAccessToken string,
) (string, error) {
	options := CommitOptions{
		Branch:  branch,
		Message: message,
		AddAll:  true,
		Author:  GetDefaultAuthor(),
		Auth: &ghhttp.BasicAuth{
			Username: "x-access-token",
			Password: githubAccessToken,
		},
	}
	return CommitChanges(repo, options)
}

// Convenience functions for backwards compatibility

// CloneRepositorySimple is a convenience function that clones a repository with basic options
func CloneRepositorySimple(session GithubSessionInterface,
	repositoryURL, workDir, branch string) (*git.Repository, error) {
	options := CloneOptions{
		RepositoryURL: repositoryURL,
		WorkDir:       workDir,
		Branch:        branch,
		SingleBranch:  true,
		Depth:         1,
	}
	return CloneRepository(session, options)
}

// CloneRepositoryNoCheckout is a convenience function that clones a repository without checking out files
func CloneRepositoryNoCheckout(session GithubSessionInterface,
	repositoryURL, workDir, branch string) (*git.Repository, error) {
	options := CloneOptions{
		RepositoryURL: repositoryURL,
		WorkDir:       workDir,
		Branch:        branch,
		SingleBranch:  true,
		Depth:         1,
		NoCheckout:    true,
	}
	return CloneRepository(session, options)
}

// CloneRepositoryWithAuth is a convenience function that clones a repository with custom authentication
func CloneRepositoryWithAuth(
	session GithubSessionInterface,
	repositoryURL, workDir, branch string,
	auth *ghhttp.BasicAuth,
	gitOptions *git.CloneOptions,
) (*git.Repository, error) {
	options := CloneOptions{
		RepositoryURL:   repositoryURL,
		WorkDir:         workDir,
		Branch:          branch,
		Auth:            auth,
		GitCloneOptions: gitOptions,
	}
	return CloneRepository(session, options)
}

// CreateCheckRunSimple is a convenience function that creates a basic check run
func CreateCheckRunSimple(
	ctx context.Context,
	session GithubSessionInterface,
	headSHA, repo, org, name, title, conclusion, status, summary string,
) error {
	options := CheckRunOptions{
		HeadSHA:    headSHA,
		Repository: repo,
		Org:        org,
		Name:       name,
		Title:      title,
		Conclusion: conclusion,
		Status:     status,
		Summary:    summary,
	}
	return CreateCheckRun(ctx, session, options)
}
