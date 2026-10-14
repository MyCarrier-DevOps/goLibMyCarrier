package slippy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validRollbackRecord is the smallest record Validate accepts, with every optional field set
// to a realistic value so a test can blank or stretch exactly one of them.
func validRollbackRecord() RollbackRecord {
	return RollbackRecord{
		CorrelationID: "0f6e0a3c-6a1d-4c52-9c5e-3f1e2d4c5b6a",
		Workflow:      "gitops-rollback-abc12",
		Environment:   "prod",
		Reason:        "canary_abort",
		Source:        "canary-coordinator",
		RolloutPhase:  "canary",
		PodHash:       "5d8b9c7f6",
		GitOpsRepo:    "GitOps-prod",
		GitOpsCommit:  "a1b2c3d",
		RevertCommit:  "e4f5a6b",
		Status:        RollbackStatusSuccess,
		Message:       "reverted",
	}
}

func TestRollbackStatus_Valid(t *testing.T) {
	for _, s := range []RollbackStatus{
		RollbackStatusSuccess, RollbackStatusNoop, RollbackStatusSkipped,
		RollbackStatusFailed, RollbackStatusDryRun,
	} {
		assert.True(t, s.Valid(), "%q must be valid", s)
	}
	for _, s := range []RollbackStatus{"", "error", "conflict", "dry-run", "SUCCESS"} {
		assert.False(t, s.Valid(), "%q must be invalid", s)
	}
}

func TestRollbackRecord_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(r *RollbackRecord)
		wantErr string // empty means valid
	}{
		{name: "valid record", mutate: func(*RollbackRecord) {}},
		{name: "metadata may be empty", mutate: func(r *RollbackRecord) {
			r.Reason, r.Source, r.RolloutPhase, r.PodHash = "", "", "", ""
			r.GitOpsRepo, r.GitOpsCommit, r.RevertCommit, r.Message = "", "", "", ""
		}},
		{name: "every status is accepted", mutate: func(r *RollbackRecord) { r.Status = RollbackStatusDryRun }},
		{name: "dev environment", mutate: func(r *RollbackRecord) { r.Environment = "dev" }},
		{name: "preprod environment", mutate: func(r *RollbackRecord) { r.Environment = "preprod" }},
		{name: "message at the limit in multibyte runes", mutate: func(r *RollbackRecord) {
			r.Message = strings.Repeat("é", 1000)
		}},
		{name: "empty correlation id", mutate: func(r *RollbackRecord) { r.CorrelationID = "" },
			wantErr: "correlation_id is empty"},
		{
			name:    "correlation id too long",
			mutate:  func(r *RollbackRecord) { r.CorrelationID = strings.Repeat("c", 129) },
			wantErr: "correlation_id exceeds 128 characters",
		},
		{name: "empty workflow", mutate: func(r *RollbackRecord) { r.Workflow = "" },
			wantErr: "workflow is empty"},
		{name: "workflow too long", mutate: func(r *RollbackRecord) { r.Workflow = strings.Repeat("w", 254) },
			wantErr: "workflow exceeds 253 characters"},
		{name: "uppercase environment", mutate: func(r *RollbackRecord) { r.Environment = "PROD" },
			wantErr: `environment "PROD" is not dev, preprod or prod`},
		{name: "empty environment", mutate: func(r *RollbackRecord) { r.Environment = "" },
			wantErr: `environment "" is not dev, preprod or prod`},
		{name: "feature environment", mutate: func(r *RollbackRecord) { r.Environment = "feature14" },
			wantErr: `environment "feature14" is not dev, preprod or prod`},
		{name: "empty status", mutate: func(r *RollbackRecord) { r.Status = "" },
			wantErr: `status "" is not one of success, noop, skipped, failed, dry_run`},
		{name: "artifact spelling of dry run", mutate: func(r *RollbackRecord) { r.Status = "dry-run" },
			wantErr: `status "dry-run" is not one of`},
		{name: "reason too long", mutate: func(r *RollbackRecord) { r.Reason = strings.Repeat("r", 65) },
			wantErr: "reason exceeds 64 characters"},
		{name: "source too long", mutate: func(r *RollbackRecord) { r.Source = strings.Repeat("s", 65) },
			wantErr: "source exceeds 64 characters"},
		{name: "rollout phase too long", mutate: func(r *RollbackRecord) { r.RolloutPhase = strings.Repeat("p", 65) },
			wantErr: "rollout_phase exceeds 64 characters"},
		{name: "pod hash too long", mutate: func(r *RollbackRecord) { r.PodHash = strings.Repeat("h", 65) },
			wantErr: "pod_hash exceeds 64 characters"},
		{name: "gitops repo too long", mutate: func(r *RollbackRecord) { r.GitOpsRepo = strings.Repeat("g", 129) },
			wantErr: "gitops_repo exceeds 128 characters"},
		{name: "gitops commit too long", mutate: func(r *RollbackRecord) { r.GitOpsCommit = strings.Repeat("a", 65) },
			wantErr: "gitops_commit exceeds 64 characters"},
		{name: "revert commit too long", mutate: func(r *RollbackRecord) { r.RevertCommit = strings.Repeat("b", 65) },
			wantErr: "revert_commit exceeds 64 characters"},
		{name: "message one rune over the limit", mutate: func(r *RollbackRecord) {
			r.Message = strings.Repeat("é", 1001)
		}, wantErr: "message exceeds 1000 characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validRollbackRecord()
			tt.mutate(&r)
			err := r.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrInvalidRollback)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// Every problem is reported in one error, so a caller fixes them in one round trip.
func TestRollbackRecord_Validate_ReportsEveryProblem(t *testing.T) {
	r := validRollbackRecord()
	r.Workflow, r.Environment, r.Status = "", "qa", "error"
	err := r.Validate()
	require.ErrorIs(t, err, ErrInvalidRollback)
	for _, want := range []string{"workflow is empty", `environment "qa"`, `status "error"`} {
		assert.Contains(t, err.Error(), want)
	}
}
