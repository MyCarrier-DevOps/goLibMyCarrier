package github_handler

import (
	"errors"
	"fmt"
)

// Sentinel errors for GitHub API operations.
// These can be used with errors.Is() for error handling and provide
// clear, actionable messages for operators.
var (
	// ErrNoInstallation indicates the GitHub App is not installed for the organization.
	// Action: Install the GitHub App on the target organization via GitHub settings.
	ErrNoInstallation = errors.New("GitHub App not installed for organization")

	// ErrInstallationLookupFailed indicates the API call to list installations failed.
	// This could be due to network issues, invalid credentials, or GitHub API problems.
	ErrInstallationLookupFailed = errors.New("failed to query GitHub App installations")

	// ErrInvalidPrivateKey indicates the private key is malformed or incorrect.
	// Action: Check SLIPPY_GITHUB_APP_PRIVATE_KEY environment variable.
	ErrInvalidPrivateKey = errors.New("invalid GitHub App private key")

	// ErrGraphQLQuery indicates a GraphQL query failed.
	// This wraps the underlying GraphQL error with additional context.
	ErrGraphQLQuery = errors.New("GitHub GraphQL query failed")

	// ErrCommitNotFound indicates the specified commit was not found.
	ErrCommitNotFound = errors.New("commit not found in repository")

	// ErrPRNotFound indicates the specified pull request was not found.
	ErrPRNotFound = errors.New("pull request not found")

	// ErrRateLimited indicates the GitHub API rate limit was exceeded.
	ErrRateLimited = errors.New("GitHub API rate limit exceeded")

	// ErrAuthenticationFailed indicates authentication to GitHub failed.
	ErrAuthenticationFailed = errors.New("GitHub authentication failed")
)

// InstallationError provides detailed information about installation lookup failures.
type InstallationError struct {
	Organization        string
	AvailableOrgs       []string
	UnderlyingError     error
	IsNotInstalled      bool
	IsAuthenticationErr bool
}

func (e *InstallationError) Error() string {
	if e.IsNotInstalled {
		if len(e.AvailableOrgs) > 0 {
			return fmt.Sprintf(
				"GitHub App not installed for organization %q. App is installed on: %v. "+
					"Install the app on %q via GitHub organization settings → Applications → Configure",
				e.Organization, e.AvailableOrgs, e.Organization)
		}
		return fmt.Sprintf(
			"GitHub App not installed for organization %q. "+
				"No installations found for this app. "+
				"Install the app via GitHub organization settings → Applications → Configure",
			e.Organization)
	}
	if e.IsAuthenticationErr {
		return fmt.Sprintf(
			"failed to authenticate GitHub App when looking up organization %q: %v. "+
				"Check SLIPPY_GITHUB_APP_ID and SLIPPY_GITHUB_APP_PRIVATE_KEY",
			e.Organization, e.UnderlyingError)
	}
	return fmt.Sprintf(
		"failed to lookup GitHub App installation for organization %q: %v",
		e.Organization, e.UnderlyingError)
}

func (e *InstallationError) Unwrap() error {
	if e.IsNotInstalled {
		return ErrNoInstallation
	}
	if e.IsAuthenticationErr {
		return ErrAuthenticationFailed
	}
	return e.UnderlyingError
}

// GraphQLError provides detailed information about GraphQL query failures.
type GraphQLError struct {
	Operation       string
	Owner           string
	Repo            string
	Ref             string
	UnderlyingError error
}

func (e *GraphQLError) Error() string {
	return fmt.Sprintf(
		"GitHub GraphQL %s failed for %s/%s (ref: %s): %v",
		e.Operation, e.Owner, e.Repo, e.Ref, e.UnderlyingError)
}

func (e *GraphQLError) Unwrap() error {
	return ErrGraphQLQuery
}

// NewInstallationNotFoundError creates an error for when an app isn't installed on an org.
func NewInstallationNotFoundError(org string, availableOrgs []string) error {
	return &InstallationError{
		Organization:   org,
		AvailableOrgs:  availableOrgs,
		IsNotInstalled: true,
	}
}

// NewInstallationLookupError creates an error for when installation lookup fails.
func NewInstallationLookupError(org string, err error, isAuthErr bool) error {
	return &InstallationError{
		Organization:        org,
		UnderlyingError:     err,
		IsAuthenticationErr: isAuthErr,
	}
}

// NewGraphQLError creates a detailed GraphQL error.
func NewGraphQLError(operation, owner, repo, ref string, err error) error {
	return &GraphQLError{
		Operation:       operation,
		Owner:           owner,
		Repo:            repo,
		Ref:             ref,
		UnderlyingError: err,
	}
}
