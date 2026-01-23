package slippy

import (
	"errors"
	"testing"
)

func TestSlipError_Error(t *testing.T) {
	tests := []struct {
		name        string
		err         *SlipError
		expectedMsg string
	}{
		{
			name: "with correlation ID",
			err: &SlipError{
				Op:            "create",
				CorrelationID: "slip-123",
				Err:           errors.New("connection failed"),
			},
			expectedMsg: "create slip slip-123: connection failed",
		},
		{
			name: "without correlation ID",
			err: &SlipError{
				Op:  "validate",
				Err: errors.New("invalid config"),
			},
			expectedMsg: "validate: invalid config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.expectedMsg {
				t.Errorf("SlipError.Error() = %q, want %q", got, tt.expectedMsg)
			}
		})
	}
}

func TestSlipError_Unwrap(t *testing.T) {
	underlying := errors.New("underlying error")
	err := &SlipError{
		Op:  "test",
		Err: underlying,
	}

	if unwrapped := err.Unwrap(); unwrapped != underlying {
		t.Errorf("SlipError.Unwrap() = %v, want %v", unwrapped, underlying)
	}
}

func TestSlipError_ErrorsIs(t *testing.T) {
	slipErr := NewSlipError("load", "corr-id", ErrSlipNotFound)

	if !errors.Is(slipErr, ErrSlipNotFound) {
		t.Error("errors.Is() should return true for wrapped sentinel error")
	}
}

func TestNewSlipError(t *testing.T) {
	err := NewSlipError("create", "my-slip-id", ErrStoreConnection)

	if err.Op != "create" {
		t.Errorf("Op = %q, want 'create'", err.Op)
	}
	if err.CorrelationID != "my-slip-id" {
		t.Errorf("CorrelationID = %q, want 'my-slip-id'", err.CorrelationID)
	}
	if !errors.Is(err, ErrStoreConnection) {
		t.Error("underlying error should be ErrStoreConnection")
	}
}

func TestStepError_Error(t *testing.T) {
	tests := []struct {
		name        string
		err         *StepError
		expectedMsg string
	}{
		{
			name: "full context",
			err: &StepError{
				Op:            "complete",
				CorrelationID: "slip-456",
				StepName:      "build",
				ComponentName: "api",
				Err:           errors.New("build failed"),
			},
			expectedMsg: "complete step build (component: api) on slip slip-456: build failed",
		},
		{
			name: "without component",
			err: &StepError{
				Op:            "start",
				CorrelationID: "slip-789",
				StepName:      "deploy",
				Err:           errors.New("deploy failed"),
			},
			expectedMsg: "start step deploy on slip slip-789: deploy failed",
		},
		{
			name: "without correlation ID",
			err: &StepError{
				Op:       "validate",
				StepName: "test",
				Err:      errors.New("validation error"),
			},
			expectedMsg: "validate step test: validation error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.expectedMsg {
				t.Errorf("StepError.Error() = %q, want %q", got, tt.expectedMsg)
			}
		})
	}
}

func TestStepError_Unwrap(t *testing.T) {
	underlying := errors.New("step underlying error")
	err := &StepError{
		Op:       "test",
		StepName: "step1",
		Err:      underlying,
	}

	if unwrapped := err.Unwrap(); unwrapped != underlying {
		t.Errorf("StepError.Unwrap() = %v, want %v", unwrapped, underlying)
	}
}

func TestNewStepError(t *testing.T) {
	err := NewStepError("fail", "corr-123", "build", "worker", ErrPrerequisiteFailed)

	if err.Op != "fail" {
		t.Errorf("Op = %q, want 'fail'", err.Op)
	}
	if err.CorrelationID != "corr-123" {
		t.Errorf("CorrelationID = %q, want 'corr-123'", err.CorrelationID)
	}
	if err.StepName != "build" {
		t.Errorf("StepName = %q, want 'build'", err.StepName)
	}
	if err.ComponentName != "worker" {
		t.Errorf("ComponentName = %q, want 'worker'", err.ComponentName)
	}
	if !errors.Is(err, ErrPrerequisiteFailed) {
		t.Error("underlying error should be ErrPrerequisiteFailed")
	}
}

func TestResolveError_Error(t *testing.T) {
	err := &ResolveError{
		Repository: "owner/repo",
		Ref:        "abc123",
		Err:        errors.New("not found"),
	}

	expected := "resolve slip for owner/repo@abc123: not found"
	if got := err.Error(); got != expected {
		t.Errorf("ResolveError.Error() = %q, want %q", got, expected)
	}
}

func TestResolveError_Unwrap(t *testing.T) {
	underlying := errors.New("resolve underlying")
	err := &ResolveError{
		Repository: "owner/repo",
		Ref:        "main",
		Err:        underlying,
	}

	if unwrapped := err.Unwrap(); unwrapped != underlying {
		t.Errorf("ResolveError.Unwrap() = %v, want %v", unwrapped, underlying)
	}
}

func TestNewResolveError(t *testing.T) {
	err := NewResolveError("myorg/myrepo", "feature-branch", ErrGitHubAPI)

	if err.Repository != "myorg/myrepo" {
		t.Errorf("Repository = %q, want 'myorg/myrepo'", err.Repository)
	}
	if err.Ref != "feature-branch" {
		t.Errorf("Ref = %q, want 'feature-branch'", err.Ref)
	}
	if !errors.Is(err, ErrGitHubAPI) {
		t.Error("underlying error should be ErrGitHubAPI")
	}
}

func TestSentinelErrors(t *testing.T) {
	// Test that sentinel errors are distinct and can be compared with errors.Is
	sentinels := []error{
		ErrSlipNotFound,
		ErrHoldTimeout,
		ErrPrerequisiteFailed,
		ErrInvalidConfiguration,
		ErrInvalidRepository,
		ErrInvalidCorrelationID,
		ErrStoreConnection,
		ErrGitHubAPI,
		ErrNoInstallation,
		ErrContextCancelled,
	}

	// Ensure each sentinel is unique
	for i, err1 := range sentinels {
		for j, err2 := range sentinels {
			if i != j && errors.Is(err1, err2) {
				t.Errorf("sentinel errors %d and %d should not match", i, j)
			}
		}
	}

	// Ensure sentinel errors have meaningful messages
	for _, err := range sentinels {
		if err.Error() == "" {
			t.Errorf("sentinel error has empty message: %v", err)
		}
	}
}

func TestNewAncestryError(t *testing.T) {
	underlying := errors.New("database connection lost")
	err := NewAncestryError("owner/repo", "abc123def456", "slip_lookup", underlying)

	if err.Repository != "owner/repo" {
		t.Errorf("Repository = %q, want 'owner/repo'", err.Repository)
	}
	if err.CommitSHA != "abc123def456" {
		t.Errorf("CommitSHA = %q, want 'abc123def456'", err.CommitSHA)
	}
	if err.Phase != "slip_lookup" {
		t.Errorf("Phase = %q, want 'slip_lookup'", err.Phase)
	}
	if err.Err != underlying {
		t.Errorf("Err = %v, want %v", err.Err, underlying)
	}
}

func TestNewAncestorUpdateError(t *testing.T) {
	underlying := errors.New("abandon failed")
	err := NewAncestorUpdateError("owner/repo", "abc123", "abandon", "ancestor-corr-123", underlying)

	if err.Repository != "owner/repo" {
		t.Errorf("Repository = %q, want 'owner/repo'", err.Repository)
	}
	if err.AncestorCorrelationID != "ancestor-corr-123" {
		t.Errorf("AncestorCorrelationID = %q, want 'ancestor-corr-123'", err.AncestorCorrelationID)
	}
	if err.Phase != "abandon" {
		t.Errorf("Phase = %q, want 'abandon'", err.Phase)
	}
}

func TestAncestryError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *AncestryError
		contains string
	}{
		{
			name: "setup phase",
			err: &AncestryError{
				Repository: "owner/repo",
				CommitSHA:  "abc123def456",
				Phase:      "setup",
				Err:        errors.New("init failed"),
			},
			contains: "setup error",
		},
		{
			name: "github_api phase",
			err: &AncestryError{
				Repository: "owner/repo",
				CommitSHA:  "abc123def456",
				Phase:      "github_api",
				Err:        errors.New("rate limited"),
			},
			contains: "GitHub API error",
		},
		{
			name: "slip_lookup phase",
			err: &AncestryError{
				Repository: "owner/repo",
				CommitSHA:  "abc123def456",
				Phase:      "slip_lookup",
				Err:        errors.New("db error"),
			},
			contains: "database query failed",
		},
		{
			name: "abandon phase",
			err: &AncestryError{
				Repository:            "owner/repo",
				CommitSHA:             "abc123def456",
				Phase:                 "abandon",
				AncestorCorrelationID: "old-slip-123",
				Err:                   errors.New("timeout"),
			},
			contains: "failed to abandon superseded slip",
		},
		{
			name: "promote phase",
			err: &AncestryError{
				Repository:            "owner/repo",
				CommitSHA:             "abc123def456",
				Phase:                 "promote",
				AncestorCorrelationID: "feature-slip",
				Err:                   errors.New("conflict"),
			},
			contains: "failed to promote feature branch slip",
		},
		{
			name: "unknown phase",
			err: &AncestryError{
				Repository: "owner/repo",
				CommitSHA:  "abc123def456",
				Phase:      "unknown",
				Err:        errors.New("mystery"),
			},
			contains: "ancestry error for",
		},
		{
			name: "short commit SHA unchanged",
			err: &AncestryError{
				Repository: "owner/repo",
				CommitSHA:  "abc",
				Phase:      "setup",
				Err:        errors.New("error"),
			},
			contains: "abc:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			if !containsString(msg, tt.contains) {
				t.Errorf("Error() = %q, want to contain %q", msg, tt.contains)
			}
		})
	}
}

func TestAncestryError_Unwrap(t *testing.T) {
	tests := []struct {
		name     string
		phase    string
		expected error
	}{
		{"abandon phase", "abandon", ErrAncestorUpdateFailed},
		{"promote phase", "promote", ErrAncestorUpdateFailed},
		{"setup phase", "setup", ErrAncestryResolutionFailed},
		{"github_api phase", "github_api", ErrAncestryResolutionFailed},
		{"slip_lookup phase", "slip_lookup", ErrAncestryResolutionFailed},
		{"unknown phase", "unknown", ErrAncestryResolutionFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &AncestryError{
				Repository: "owner/repo",
				CommitSHA:  "abc123",
				Phase:      tt.phase,
				Err:        errors.New("test"),
			}
			if !errors.Is(err, tt.expected) {
				t.Errorf("Unwrap() for phase %q should return %v", tt.phase, tt.expected)
			}
		})
	}
}

// containsString is a helper to check if a string contains a substring
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
