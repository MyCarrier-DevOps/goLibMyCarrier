package github_handler

import (
	"context"
	"fmt"
	"os"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	ghhttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/google/go-github/v69/github"
	"golang.org/x/oauth2"
)

// GithubSessionInterface defines the interface for Github sessions
type GithubSessionInterface interface {
	AuthToken() *oauth2.Token
	Client() *github.Client
}

// CloneRepository clones the specified GitHub repository into a local directory named "/work".
// It uses the provided GithubSession for authentication and clones the specific branch
// indicated in the data map.
// repository_url: The URL of the GitHub repository to clone. Requires https:// format.
// branch: The branch to clone from the repository. Do not include the "refs/heads/" prefix, just the branch name.
// gitCloneOptions: Optional git clone options. If nil, default options will be used.
// basicAuth: Optional basic authentication credentials. If nil, it will use the token from the session.
// Returns a pointer to the cloned git.Repository or an error if the clone operation fails.
func CloneRepository(session GithubSessionInterface,
	repository_url,
	workdir,
	branch string,
	basicAuth *ghhttp.BasicAuth,
	gitCloneOptions *git.CloneOptions,
) (*git.Repository, error) {
	token := session.AuthToken()
	if token == nil {
		return nil, fmt.Errorf("error getting auth token for GitHub session")
	}
	if workdir == "" {
		workdir = "/work" // Default work directory if none provided
	}
	if gitCloneOptions == nil {
		if basicAuth == nil {
			basicAuth = &ghhttp.BasicAuth{
				Username: "x-access-token", // This can be anything, GitHub uses the token as the password
				Password: token.AccessToken,
			}
		}
		// Default clone options if none provided
		gitCloneOptions = &git.CloneOptions{
			URL:           fmt.Sprintf("%v", repository_url),
			Progress:      os.Stdout,
			SingleBranch:  true,
			Depth:         1,
			ReferenceName: plumbing.NewBranchReferenceName(branch),
			Auth:          basicAuth,
		}
	}
	repo, err := git.PlainClone(workdir, false, gitCloneOptions)
	if err != nil {
		return nil, fmt.Errorf("error cloning repository: %w", err)
	}
	return repo, nil
}

// CreateCheckRun creates a check run in the specified GitHub repository.
// It uses the provided GithubSession for authentication and requires the head SHA, repository name,
// organization name, check run name, title, conclusion, status, and summary.
// headSha: The SHA of the commit to create the check run for.
// repo: The name of the repository where the check run will be created.
// org: The name of the organization that owns the repository.
// name: The name of the check run.
// title: The title of the check run.
// conclusion: The conclusion of the check run (e.g., "success", "failure").
// status: The status of the check run (e.g., "queued", "in_progress", "completed").
// summary: A summary of the check run results.
// Returns an error if the check run creation fails.
func CreateCheckRun(
	ctx context.Context,
	s GithubSessionInterface,
	headSha, repo, org, name, title, conclusion, status, summary string,
) error {
	checkRunOptions := &github.CreateCheckRunOptions{
		Name:       name,
		HeadSHA:    headSha,
		Status:     github.Ptr(status),
		Conclusion: github.Ptr(conclusion),
		Output: &github.CheckRunOutput{
			Title:   github.Ptr(title),
			Summary: github.Ptr(summary),
		},
	}
	_, _, err := s.Client().Checks.CreateCheckRun(ctx, org, repo, *checkRunOptions)
	if err != nil {
		return fmt.Errorf("error creating checkrun: %w", err)
	}
	return nil
}
