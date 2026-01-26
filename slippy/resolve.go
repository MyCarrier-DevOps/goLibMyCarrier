package slippy

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// ResolveOptions contains parameters for slip resolution.
type ResolveOptions struct {
	// Repository in owner/repo format
	Repository string

	// Branch name (e.g., "main", "feature/xyz")
	Branch string

	// Ref to resolve ancestry from (e.g., "HEAD", "main", commit SHA)
	Ref string

	// ImageTag for fallback resolution (e.g., "mycarrier/svc:abc123-1234567890")
	ImageTag string

	// AncestryDepth is how many commits to check (default: 20)
	AncestryDepth int
}

// ResolveResult contains the resolved slip and metadata.
type ResolveResult struct {
	// Slip is the resolved routing slip
	Slip *Slip

	// ResolvedBy indicates how the slip was found ("ancestry", "image_tag", "commit_sha")
	ResolvedBy string

	// MatchedCommit is the commit SHA that matched
	MatchedCommit string

	// Warnings contains non-fatal errors encountered during resolution.
	// For example, ancestry lookup might fail but image tag resolution succeeds.
	Warnings []error
}

// ResolveSlip finds the correct routing slip for a workflow execution.
// It uses commit ancestry as the primary resolution strategy, with image tag
// extraction as a fallback for deploy steps.
//
// Resolution order:
// 1. Commit ancestry via GitHub GraphQL API (walks git history)
// 2. Image tag extraction (parses commit SHA from image tag)
//
// The returned ResolveResult includes a Warnings slice containing any errors
// encountered during resolution. For example, if ancestry lookup fails but
// image tag resolution succeeds, the ancestry error is included as a warning.
func (c *Client) ResolveSlip(ctx context.Context, opts ResolveOptions) (*ResolveResult, error) {
	if opts.AncestryDepth == 0 {
		opts.AncestryDepth = c.config.AncestryDepth
	}

	// Parse owner/repo
	owner, repo, err := parseRepository(opts.Repository)
	if err != nil {
		return nil, err
	}

	var warnings []error

	// Primary: Commit ancestry via GitHub GraphQL API
	if opts.Ref != "" {
		c.logger.Info(ctx, "Resolving slip via commit ancestry", map[string]interface{}{
			"repository": opts.Repository,
			"ref":        opts.Ref,
		})

		commits, err := c.github.GetCommitAncestry(ctx, owner, repo, opts.Ref, opts.AncestryDepth)
		switch {
		case err != nil:
			// Log at Info level - error is captured as warning and returned to caller
			// Caller decides logging level based on shadow mode
			c.logger.Info(ctx, "Failed to get commit ancestry, will try fallback", map[string]interface{}{
				"repository": opts.Repository,
				"ref":        opts.Ref,
				"error":      err.Error(),
			})
			// Capture as warning - we'll try fallback resolution
			warnings = append(warnings, fmt.Errorf("ancestry resolution failed: %w", err))
		case len(commits) > 0:
			c.logger.Info(ctx, "Got commit ancestry from GitHub", map[string]interface{}{
				"repository":   opts.Repository,
				"ref":          opts.Ref,
				"commit_count": len(commits),
				"first_commit": shortSHA(commits[0]),
			})

			slip, matchedCommit, err := c.store.FindByCommits(ctx, opts.Repository, commits)
			if err != nil {
				c.logger.Info(ctx, "FindByCommits returned error", map[string]interface{}{
					"repository": opts.Repository,
					"error":      err.Error(),
				})
			}
			if err == nil && slip != nil {
				c.logger.Info(ctx, "Resolved slip via ancestry", map[string]interface{}{
					"correlation_id": slip.CorrelationID,
					"commit":         shortSHA(matchedCommit),
				})
				return &ResolveResult{
					Slip:          slip,
					ResolvedBy:    "ancestry",
					MatchedCommit: matchedCommit,
					Warnings:      warnings,
				}, nil
			}
			// Log when commits were found but no slip matched
			c.logger.Info(ctx, "No slip found for ancestry commits", map[string]interface{}{
				"repository":   opts.Repository,
				"commit_count": len(commits),
			})
		default:
			c.logger.Info(ctx, "No commits returned from ancestry lookup", map[string]interface{}{
				"repository": opts.Repository,
				"ref":        opts.Ref,
			})
		}
	}

	// Fallback: Extract commit SHA from image tag
	if opts.ImageTag != "" {
		c.logger.Info(ctx, "Attempting fallback resolution via image tag", map[string]interface{}{
			"image_tag": opts.ImageTag,
		})

		commitSHA := extractCommitFromImageTag(opts.ImageTag)
		if commitSHA != "" {
			slip, err := c.store.LoadByCommit(ctx, opts.Repository, commitSHA)
			if err == nil && slip != nil {
				c.logger.Info(ctx, "Resolved slip via image tag", map[string]interface{}{
					"correlation_id": slip.CorrelationID,
					"commit":         shortSHA(commitSHA),
					"warnings":       len(warnings),
				})
				return &ResolveResult{
					Slip:          slip,
					ResolvedBy:    "image_tag",
					MatchedCommit: commitSHA,
					Warnings:      warnings, // Include any ancestry warnings
				}, nil
			}
		}
	}

	return nil, NewResolveError(opts.Repository, opts.Ref, ErrSlipNotFound)
}

// parseRepository splits "owner/repo" into components.
func parseRepository(fullName string) (owner, repo string, err error) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("%w: %s", ErrInvalidRepository, fullName)
	}
	return parts[0], parts[1], nil
}

// imageTagCommitPattern matches commit SHAs in image tags.
// Supports formats:
//   - mycarrier/svc:abc1234-1234567890 (commit-timestamp)
//   - mycarrier/svc:abc1234 (short sha)
//   - mycarrier/svc:v1.2.3-abc1234 (semver with sha)
var imageTagCommitPattern = regexp.MustCompile(`(?:^|[:-])([a-f0-9]{7,40})(?:$|-)`)

// extractCommitFromImageTag extracts commit SHA from image tag.
// Returns empty string if no commit SHA is found.
func extractCommitFromImageTag(tag string) string {
	// Get just the tag portion after the colon
	if idx := strings.LastIndex(tag, ":"); idx != -1 {
		tag = tag[idx+1:]
	}

	matches := imageTagCommitPattern.FindStringSubmatch(tag)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// shortSHA returns a shortened SHA for logging.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
