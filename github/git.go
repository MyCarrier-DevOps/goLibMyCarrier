package github_handler

import (
	"fmt"
	"os"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	ghhttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// CloneRepository clones the specified GitHub repository into a local directory named "/work".
// It uses the provided GithubSession for authentication and clones the specific branch
// indicated in the data map.
// repository_url: The URL of the GitHub repository to clone. Requires https:// format.
// branch: The branch to clone from the repository. Do not include the "refs/heads/" prefix, just the branch name.
// gitCloneOptions: Optional git clone options. If nil, default options will be used.
// Returns a pointer to the cloned git.Repository or an error if the clone operation fails.
func CloneRepository(session GithubSession, repository_url, branch string, gitCloneOptions *git.CloneOptions) (*git.Repository, error) {
	token := session.AuthToken()
	if token == nil {
		return nil, fmt.Errorf("error getting auth token for GitHub session")
	}
	if gitCloneOptions == nil {
		// Default clone options if none provided
		gitCloneOptions = &git.CloneOptions{
			URL:           fmt.Sprintf("%v", repository_url),
			Progress:      os.Stdout,
			SingleBranch:  true,
			Depth:         1,
			ReferenceName: plumbing.NewBranchReferenceName(branch),
			Auth: &ghhttp.BasicAuth{
				Username: "x-access-token", // This can be anything, GitHub uses the token as the password
				Password: token.AccessToken,
			},
		}
	}
	repo, err := git.PlainClone("/work", false, gitCloneOptions)
	if err != nil {
		return nil, fmt.Errorf("error cloning repository: %v", err.Error())
	}
	return repo, nil
}
