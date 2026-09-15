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

// ClaimSlip takes ownership of an ended slip so a same-commit push deduplicates onto it
// instead of repaving it (DEVOPS-285). expected bounds which statuses may be claimed out of;
// nil means any ended status. See SlipStore.ClaimSlip for the full contract and error set.
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
