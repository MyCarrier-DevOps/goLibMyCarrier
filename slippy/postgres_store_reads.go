package slippy

import (
	"context"
	"fmt"
	"strings"
)

// FindByCommits returns the slip matching the highest-priority commit in the ordered
// list (earliest in the list wins; ties broken by most-recent update). Terminal-
// superseded statuses (abandoned, promoted, compensated) are excluded. Returns
// ErrSlipNotFound when no live slip matches any commit.
func (s *PostgresStore) FindByCommits(
	ctx context.Context,
	repository string,
	commits []string,
) (*Slip, string, error) {
	if len(commits) == 0 {
		return nil, "", fmt.Errorf("no commits provided")
	}

	query := fmt.Sprintf(`
		SELECT %s, c.commit_sha AS matched_commit
		FROM routing_slips s
		JOIN unnest($2::text[]) WITH ORDINALITY AS c(commit_sha, priority) ON s.commit_sha = c.commit_sha
		WHERE lower(s.repository) = lower($1)
		  AND s.status NOT IN ('abandoned', 'promoted', 'compensated')
		ORDER BY c.priority ASC, s.updated_at DESC
		LIMIT 1`, s.slipColumnsPrefixed("s."))

	var matched string
	sc, dest := s.newSlipScan(&matched)
	if err := s.pool.QueryRow(ctx, query, repository, commits).Scan(dest...); err != nil {
		if isNoRows(err) {
			return nil, "", ErrSlipNotFound
		}
		return nil, "", fmt.Errorf("failed to query slip by commits: %w", err)
	}
	return s.populate(sc), matched, nil
}

// FindAllByCommits returns every slip matching any commit in the ordered list, ordered by
// commit priority then most-recent update. Unlike FindByCommits it does not exclude
// terminal-superseded statuses. An empty commit list returns an empty result (not an error).
func (s *PostgresStore) FindAllByCommits(
	ctx context.Context,
	repository string,
	commits []string,
) ([]SlipWithCommit, error) {
	if len(commits) == 0 {
		return nil, nil
	}

	query := fmt.Sprintf(`
		SELECT %s, c.commit_sha AS matched_commit
		FROM routing_slips s
		JOIN unnest($2::text[]) WITH ORDINALITY AS c(commit_sha, priority) ON s.commit_sha = c.commit_sha
		WHERE lower(s.repository) = lower($1)
		ORDER BY c.priority ASC, s.updated_at DESC`, s.slipColumnsPrefixed("s."))

	rows, err := s.pool.Query(ctx, query, repository, commits)
	if err != nil {
		return nil, fmt.Errorf("failed to query slips by commits: %w", err)
	}
	defer rows.Close()

	var results []SlipWithCommit
	for rows.Next() {
		var matched string
		sc, dest := s.newSlipScan(&matched)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("failed to scan slip from rows: %w", err)
		}
		results = append(results, SlipWithCommit{Slip: s.populate(sc), MatchedCommit: matched})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate slips by commits: %w", err)
	}
	return results, nil
}

// InsertAncestryLink writes (or updates) the direct-parent link for a slip. The row is
// keyed by the child's (repository, branch, correlation_id); re-linking upserts.
func (s *PostgresStore) InsertAncestryLink(ctx context.Context, slip *Slip, parent AncestryEntry) error {
	const query = `
		INSERT INTO slip_ancestry (
			repository, branch, correlation_id,
			parent_correlation_id, parent_commit_sha, parent_status,
			parent_failed_step, parent_repository, parent_branch,
			created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (repository, branch, correlation_id) DO UPDATE SET
			parent_correlation_id = EXCLUDED.parent_correlation_id,
			parent_commit_sha     = EXCLUDED.parent_commit_sha,
			parent_status         = EXCLUDED.parent_status,
			parent_failed_step    = EXCLUDED.parent_failed_step,
			parent_repository     = EXCLUDED.parent_repository,
			parent_branch         = EXCLUDED.parent_branch,
			created_at            = EXCLUDED.created_at`

	if _, err := s.pool.Exec(ctx, query,
		slip.Repository, slip.Branch, slip.CorrelationID,
		parent.CorrelationID, parent.CommitSHA, string(parent.Status),
		parent.FailedStep, parent.Repository, parent.Branch,
		parent.CreatedAt,
	); err != nil {
		return fmt.Errorf("failed to insert ancestry link for %s: %w", slip.CorrelationID, err)
	}
	return nil
}

// ResolveAncestry walks parent links to reconstruct the full ancestry chain, ordered from
// direct parent to oldest ancestor, capped at maxDepth. Each hop follows the parent's own
// repository/branch, so cross-branch lineage resolves correctly.
func (s *PostgresStore) ResolveAncestry(
	ctx context.Context,
	repository, branch, correlationID string,
	maxDepth int,
) ([]AncestryEntry, error) {
	const query = `
		SELECT parent_correlation_id, parent_commit_sha, parent_status,
		       parent_failed_step, parent_repository, parent_branch, created_at
		FROM slip_ancestry
		WHERE repository = $1 AND branch = $2 AND correlation_id = $3
		LIMIT 1`

	var chain []AncestryEntry
	currentID := correlationID
	currentRepo := repository
	currentBranch := branch

	for range maxDepth {
		var entry AncestryEntry
		var statusStr string
		err := s.pool.QueryRow(ctx, query, currentRepo, currentBranch, currentID).Scan(
			&entry.CorrelationID, &entry.CommitSHA, &statusStr,
			&entry.FailedStep, &entry.Repository, &entry.Branch, &entry.CreatedAt,
		)
		if err != nil {
			if isNoRows(err) {
				break
			}
			return nil, fmt.Errorf("scanning ancestry row: %w", err)
		}
		entry.Status = SlipStatus(statusStr)

		chain = append(chain, entry)
		currentID = entry.CorrelationID
		currentRepo = entry.Repository
		currentBranch = entry.Branch
	}
	return chain, nil
}

// slipColumnsPrefixed returns slipColumns() joined with a table-alias prefix on each name,
// for queries that alias routing_slips (e.g. the commit-priority join).
func (s *PostgresStore) slipColumnsPrefixed(prefix string) string {
	cols := s.slipColumns()
	prefixed := make([]string, len(cols))
	for i, c := range cols {
		prefixed[i] = prefix + c
	}
	return strings.Join(prefixed, ", ")
}
