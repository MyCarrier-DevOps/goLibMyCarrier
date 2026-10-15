package slippy

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// RollbackStatus is the outcome of one rollback attempt (DEVOPS-320). The five values are the
// only ones slip_rollbacks.status accepts: migration v7's ck_slip_rollbacks_status names the
// same set.
type RollbackStatus string

const (
	// RollbackStatusSuccess means the revert commit landed.
	RollbackStatusSuccess RollbackStatus = "success"
	// RollbackStatusNoop means there was nothing to revert (already reverted, or out of scope).
	RollbackStatusNoop RollbackStatus = "noop"
	// RollbackStatusSkipped means the attempt was deliberately not made, for example because a
	// newer release superseded the one being rolled back.
	RollbackStatusSkipped RollbackStatus = "skipped"
	// RollbackStatusFailed means the attempt was made and did not land.
	RollbackStatusFailed RollbackStatus = "failed"
	// RollbackStatusDryRun means the attempt ran in dry-run mode and changed nothing.
	RollbackStatusDryRun RollbackStatus = "dry_run"
)

// Field limits for a RollbackRecord, counted in characters (runes) as JSON Schema's maxLength
// counts them. slippy-api's maxLength tags on the record-rollback body use the same numbers,
// so an over-long field is refused with the same limit at the edge and here.
const (
	maxRollbackCorrelationIDLen = 128
	maxRollbackWorkflowLen      = 253
	maxRollbackLabelLen         = 64
	maxRollbackRepoLen          = 128
	maxRollbackCommitLen        = 64
	maxRollbackMessageLen       = 1000
	maxRollbackOutcomeLen       = 1000
)

// RollbackRecord is one row of slip_rollbacks: a single rollback attempt for a slip, written
// by gitops-rollback through slippy-api (DEVOPS-320). ID and RecordedAt are assigned by the
// database; RecordRollback ignores whatever the caller puts in them. Message says why the
// rollback started (the publisher's text); Outcome says what the attempt did, or why it did not
// land.
type RollbackRecord struct {
	ID            int64          `json:"id"`
	CorrelationID string         `json:"correlation_id"`
	Workflow      string         `json:"workflow"`
	RecordedAt    time.Time      `json:"recorded_at"`
	Environment   string         `json:"environment"`
	Reason        string         `json:"reason"`
	Source        string         `json:"source"`
	RolloutPhase  string         `json:"rollout_phase"`
	PodHash       string         `json:"pod_hash"`
	GitOpsRepo    string         `json:"gitops_repo"`
	GitOpsCommit  string         `json:"gitops_commit"`
	RevertCommit  string         `json:"revert_commit"`
	Status        RollbackStatus `json:"status"`
	Forced        bool           `json:"forced"`
	Message       string         `json:"message"`
	Outcome       string         `json:"outcome"`
}

// Valid reports whether s is one of the five recorded rollback statuses.
func (s RollbackStatus) Valid() bool {
	switch s {
	case RollbackStatusSuccess, RollbackStatusNoop, RollbackStatusSkipped,
		RollbackStatusFailed, RollbackStatusDryRun:
		return true
	default:
		return false
	}
}

// Validate reports every field that would make r unwritable, as one error wrapping
// ErrInvalidRollback, or nil when r can be recorded. The descriptive fields (reason, source,
// rollout_phase, pod_hash, message, outcome) are free text with a length bound only: gitops-rollback
// normalises unknown values, and a rollback record must never be refused for its metadata. Every
// text field must also be storable: Postgres text holds no NUL byte and no invalid UTF-8.
func (r *RollbackRecord) Validate() error {
	var problems []string
	addf := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if r.CorrelationID == "" {
		addf("correlation_id is empty")
	} else if exceedsRunes(r.CorrelationID, maxRollbackCorrelationIDLen) {
		addf("correlation_id exceeds %d characters", maxRollbackCorrelationIDLen)
	}
	if r.Workflow == "" {
		addf("workflow is empty")
	} else if exceedsRunes(r.Workflow, maxRollbackWorkflowLen) {
		addf("workflow exceeds %d characters", maxRollbackWorkflowLen)
	}
	if r.Environment != "dev" && r.Environment != "preprod" && r.Environment != "prod" {
		addf("environment %q is not dev, preprod or prod", r.Environment)
	}
	if !r.Status.Valid() {
		addf("status %q is not one of success, noop, skipped, failed, dry_run", r.Status)
	}
	fields := []rollbackTextField{
		{"reason", r.Reason, maxRollbackLabelLen},
		{"source", r.Source, maxRollbackLabelLen},
		{"rollout_phase", r.RolloutPhase, maxRollbackLabelLen},
		{"pod_hash", r.PodHash, maxRollbackLabelLen},
		{"gitops_repo", r.GitOpsRepo, maxRollbackRepoLen},
		{"gitops_commit", r.GitOpsCommit, maxRollbackCommitLen},
		{"revert_commit", r.RevertCommit, maxRollbackCommitLen},
		{"message", r.Message, maxRollbackMessageLen},
		{"outcome", r.Outcome, maxRollbackOutcomeLen},
	}
	for _, f := range fields {
		if exceedsRunes(f.value, f.limit) {
			addf("%s exceeds %d characters", f.name, f.limit)
		}
	}
	identity := []rollbackTextField{
		{"correlation_id", r.CorrelationID, maxRollbackCorrelationIDLen},
		{"workflow", r.Workflow, maxRollbackWorkflowLen},
	}
	for _, f := range append(identity, fields...) {
		if !storableText(f.value) {
			addf("%s contains a NUL byte or invalid UTF-8", f.name)
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidRollback, strings.Join(problems, "; "))
	}
	return nil
}

// rollbackTextField is one text field of a RollbackRecord and its character limit.
type rollbackTextField struct {
	name  string
	value string
	limit int
}

// storableText reports whether Postgres text can hold v: no NUL byte and valid UTF-8 (otherwise
// SQLSTATE 22021).
func storableText(v string) bool {
	return utf8.ValidString(v) && !strings.ContainsRune(v, 0)
}

// exceedsRunes reports whether v is longer than limit characters (runes, not bytes).
func exceedsRunes(v string, limit int) bool {
	return utf8.RuneCountInString(v) > limit
}
