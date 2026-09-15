package slippy

import (
	"context"
	"fmt"
	"time"
)

// ClaimMarkerStep is the state_history step name of the adoption marker ClaimSlip appends.
// It is deliberately not a pipeline step name, so no aggregate or phase-duration reader can
// mistake it for a real step event. pushhookparser's stranded-slip cleanup keys on it.
const ClaimMarkerStep = "slip_claimed"

// ClaimMarker builds the adoption history entry every caller writes, so the marker's shape
// is defined once here rather than per client. The prior status goes in the message: an
// operator reading the history needs to know what was adopted, and once the claim lands the
// slip's status no longer says.
func ClaimMarker(prior SlipStatus, claimedBy, reason string) StateHistoryEntry {
	msg := fmt.Sprintf("adopted %s slip before dispatching", prior)
	if reason != "" {
		msg += ": " + reason
	}
	return StateHistoryEntry{
		Step:      ClaimMarkerStep,
		Status:    StepStatusRunning,
		Timestamp: time.Now(),
		Actor:     claimedBy,
		Message:   msg,
	}
}

// ReleaseMarkerStep is the state_history step name of the marker ReleaseClaim appends.
// pushhookparser's stranded-slip cleanup treats a release as ending the claim's exemption.
const ReleaseMarkerStep = "slip_released"

// ReleaseMarker builds the release history entry. status is the slip's status after the
// release; restored says whether the release put it there (the run wrote nothing, so the
// pre-claim status came back) or found it already written by the run and kept it. The
// message names both, because once claimed_from is cleared the row no longer says.
// StepStatusAborted is the closest existing step status to "this claim ended without its
// work reporting"; it is never read as a pipeline step because the step name is not one.
func ReleaseMarker(status SlipStatus, restored bool, releasedBy, reason string) StateHistoryEntry {
	msg := fmt.Sprintf("released claim; kept %s written by the pipeline", status)
	if restored {
		msg = fmt.Sprintf("released claim; restored %s", status)
	}
	if reason != "" {
		msg += ": " + reason
	}
	return StateHistoryEntry{
		Step:      ReleaseMarkerStep,
		Status:    StepStatusAborted,
		Timestamp: time.Now(),
		Actor:     releasedBy,
		Message:   msg,
	}
}

// ReleaseClaim ends a claim when the claimant's run is over: the post-job calls it on every
// exit, and an adopter calls it when it decides not to dispatch after all. The claim ends
// whatever the pipeline wrote meanwhile; only the status is conditional — restored to the
// pre-claim value if the run wrote nothing, kept as the run left it otherwise. An unclaimed
// slip is ErrNotClaimed and untouched. See SlipStore.ReleaseClaim for the contract.
func (c *Client) ReleaseClaim(
	ctx context.Context, correlationID, releasedBy, reason string,
) (SlipStatus, error) {
	status, err := c.store.ReleaseClaim(ctx, correlationID, releasedBy, reason)
	if err != nil {
		return "", NewSlipError("release claim", correlationID, err)
	}
	c.logger.Info(ctx, "Released slip claim", map[string]interface{}{
		"correlation_id": correlationID,
		"status":         string(status),
		"released_by":    releasedBy,
	})
	return status, nil
}

// ClaimSlip takes ownership of a slip so a same-commit push deduplicates onto it instead of
// repaving it, for as long as the claim is held — until ReleaseClaim, or until the run
// reaches a terminal status (DEVOPS-285, DEVOPS-367). expected bounds
// which statuses may be claimed out of; nil admits any status except an unclaimed
// in_progress, which is a live run — so nil also claims pending and compensating, not only
// the ended statuses. See SlipStore.ClaimSlip for the full contract and error set.
//
// One store call: the store reads the prior status under lock and builds the marker from
// it, so there is no read-then-write here and the marker can never name a stale status.
func (c *Client) ClaimSlip(
	ctx context.Context, correlationID string, expected []SlipStatus, claimedBy, reason string,
) (SlipStatus, error) {
	prior, err := c.store.ClaimSlip(ctx, correlationID, expected, claimedBy, reason)
	if err != nil {
		return "", NewSlipError("claim", correlationID, err)
	}
	c.logger.Info(ctx, "Claimed slip", map[string]interface{}{
		"correlation_id": correlationID,
		"prior_status":   string(prior),
		"claimed_by":     claimedBy,
	})
	return prior, nil
}
