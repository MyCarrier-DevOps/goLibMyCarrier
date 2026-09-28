package slippy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

func TestSlipStatus_String(t *testing.T) {
	tests := []struct {
		status   SlipStatus
		expected string
	}{
		{SlipStatusPending, "pending"},
		{SlipStatusInProgress, "in_progress"},
		{SlipStatusCompleted, "completed"},
		{SlipStatusFailed, "failed"},
		{SlipStatusCompensating, "compensating"},
		{SlipStatusCompensated, "compensated"},
		{SlipStatusAbandoned, "abandoned"},
		{SlipStatusPromoted, "promoted"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.status.String(); got != tt.expected {
				t.Errorf("SlipStatus.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestSlipStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		status   SlipStatus
		expected bool
	}{
		{SlipStatusPending, false},
		{SlipStatusInProgress, false},
		{SlipStatusCompleted, true},
		{SlipStatusFailed, false},
		{SlipStatusCompensating, false},
		{SlipStatusCompensated, true},
		{SlipStatusAbandoned, true},
		{SlipStatusPromoted, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsTerminal(); got != tt.expected {
				t.Errorf("SlipStatus(%q).IsTerminal() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestSlipStatus_IsLive(t *testing.T) {
	// IsLive is the repave decision predicate shared by CreateSlipForPush's main path
	// and handleDuplicateSlipBackstop (DEVOPS-231, review finding B5): true for every
	// status that is neither terminal nor SlipStatusFailed. Covers all eight SlipStatus
	// values so a future ninth status must be deliberately classified here too.
	tests := []struct {
		status   SlipStatus
		expected bool
	}{
		{SlipStatusPending, true},
		{SlipStatusInProgress, true},
		{SlipStatusCompleted, false},
		{SlipStatusFailed, false},
		{SlipStatusCompensating, true},
		{SlipStatusCompensated, false},
		{SlipStatusAbandoned, false},
		{SlipStatusPromoted, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsLive(); got != tt.expected {
				t.Errorf("SlipStatus(%q).IsLive() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestStepStatus_String(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected string
	}{
		{StepStatusPending, "pending"},
		{StepStatusHeld, "held"},
		{StepStatusRunning, "running"},
		{StepStatusCompleted, "completed"},
		{StepStatusFailed, "failed"},
		{StepStatusError, "error"},
		{StepStatusAborted, "aborted"},
		{StepStatusTimeout, "timeout"},
		{StepStatusSkipped, "skipped"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.status.String(); got != tt.expected {
				t.Errorf("StepStatus.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestStepStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected bool
	}{
		{StepStatusPending, false},
		{StepStatusHeld, false},
		{StepStatusRunning, false},
		{StepStatusCompleted, true},
		{StepStatusFailed, true},
		{StepStatusError, true},
		{StepStatusAborted, true},
		{StepStatusTimeout, true},
		{StepStatusSkipped, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsTerminal(); got != tt.expected {
				t.Errorf("StepStatus(%q).IsTerminal() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestStepStatus_IsSuccess(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected bool
	}{
		{StepStatusPending, false},
		{StepStatusHeld, false},
		{StepStatusRunning, false},
		{StepStatusCompleted, true},
		{StepStatusFailed, false},
		{StepStatusError, false},
		{StepStatusAborted, false},
		{StepStatusTimeout, false},
		{StepStatusSkipped, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsSuccess(); got != tt.expected {
				t.Errorf("StepStatus(%q).IsSuccess() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestStepStatus_IsFailure(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected bool
	}{
		{StepStatusPending, false},
		{StepStatusHeld, false},
		{StepStatusRunning, false},
		{StepStatusCompleted, false},
		{StepStatusFailed, true},
		{StepStatusError, true},
		{StepStatusAborted, true},
		{StepStatusTimeout, true},
		{StepStatusSkipped, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsFailure(); got != tt.expected {
				t.Errorf("StepStatus(%q).IsFailure() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestStepStatus_IsRunning(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected bool
	}{
		{StepStatusPending, false},
		{StepStatusHeld, true},
		{StepStatusRunning, true},
		{StepStatusCompleted, false},
		{StepStatusFailed, false},
		{StepStatusError, false},
		{StepStatusAborted, false},
		{StepStatusTimeout, false},
		{StepStatusSkipped, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsRunning(); got != tt.expected {
				t.Errorf("StepStatus(%q).IsRunning() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestStepStatus_IsPending(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected bool
	}{
		{StepStatusPending, true},
		{StepStatusHeld, false},
		{StepStatusRunning, false},
		{StepStatusCompleted, false},
		{StepStatusFailed, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsPending(); got != tt.expected {
				t.Errorf("StepStatus(%q).IsPending() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestPrereqStatus_String(t *testing.T) {
	tests := []struct {
		status   PrereqStatus
		expected string
	}{
		{PrereqStatusCompleted, "completed"},
		{PrereqStatusRunning, "running"},
		{PrereqStatusFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.status.String(); got != tt.expected {
				t.Errorf("PrereqStatus.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// TestAllSlipStatusesInEnum verifies that the SlipStatus constants in status.go and the values
// the slip_status DOMAIN admits (postgres_migrations.go, migration v1) are the same set, so
// neither can gain or lose a status without the other. It discovers both sides rather than
// listing them, so adding a status to only one of the two files fails here.
func TestAllSlipStatusesInEnum(t *testing.T) {
	assertStatusConstantsMatchDomain(t, "SlipStatus", "slip_status")
}

// TestAllStepStatusesInEnum is TestAllSlipStatusesInEnum for StepStatus and the step_status DOMAIN.
func TestAllStepStatusesInEnum(t *testing.T) {
	assertStatusConstantsMatchDomain(t, "StepStatus", "step_status")
}

// assertStatusConstantsMatchDomain compares the typeName constants declared in status.go with
// the values in the named DOMAIN's CHECK (VALUE IN (...)) list in migration v1's UpSQL.
func assertStatusConstantsMatchDomain(t *testing.T, typeName, domain string) {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	discovered := parseStatusConstants(t, filepath.Join(filepath.Dir(filename), "status.go"), typeName)
	if len(discovered) == 0 {
		t.Fatalf("no %s constants found in status.go - parsing may have failed", typeName)
	}
	domainValues := parseDomainValues(t, domain)

	constByValue := make(map[string]string, len(discovered))
	for name, value := range discovered {
		constByValue[value] = name
	}
	for value, name := range constByValue {
		if _, ok := domainValues[value]; !ok {
			t.Errorf("%s constant %s = %q is missing from the %s DOMAIN in postgres_migrations.go",
				typeName, name, value, domain)
		}
	}
	for value := range domainValues {
		if _, ok := constByValue[value]; !ok {
			t.Errorf("the %s DOMAIN admits %q but status.go defines no %s with that value", domain, value, typeName)
		}
	}
}

// parseDomainValues returns the quoted values of `CREATE DOMAIN <domain> AS text CHECK (VALUE IN
// (...))` in migration v1's UpSQL. It fails the test when the DOMAIN is absent or lists a value twice.
func parseDomainValues(t *testing.T, domain string) map[string]struct{} {
	t.Helper()
	up := NewPostgresDynamicMigrationManager(testPipelineConfig(), nil).enumsMigration().UpSQL
	block := regexp.MustCompile(`(?s)CREATE DOMAIN ` + regexp.QuoteMeta(domain) +
		`\s+AS\s+text\s+CHECK\s*\(\s*VALUE\s+IN\s*\(([^)]*)\)\s*\)`).FindStringSubmatch(up)
	if block == nil {
		t.Fatalf("could not find the %s DOMAIN in migration v1's UpSQL", domain)
	}
	values := make(map[string]struct{})
	for _, m := range regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(block[1], -1) {
		if _, dup := values[m[1]]; dup {
			t.Errorf("the %s DOMAIN lists %q twice", domain, m[1])
		}
		values[m[1]] = struct{}{}
	}
	if len(values) == 0 {
		t.Fatalf("the %s DOMAIN lists no values - parsing may have failed", domain)
	}
	return values
}

// parseStatusConstants parses status.go and extracts all constants of the given type.
func parseStatusConstants(t *testing.T, filename, typeName string) map[string]string {
	t.Helper()

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse %s: %v", filename, err)
	}

	discovered := make(map[string]string)
	ast.Inspect(node, func(n ast.Node) bool {
		genDecl, ok := n.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			return true
		}

		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}

			if ident, ok := valueSpec.Type.(*ast.Ident); ok && ident.Name == typeName {
				for i, name := range valueSpec.Names {
					if len(valueSpec.Values) > i {
						if lit, ok := valueSpec.Values[i].(*ast.BasicLit); ok {
							// Remove quotes from string literal
							statusValue := lit.Value[1 : len(lit.Value)-1]
							discovered[name.Name] = statusValue
						}
					}
				}
			}
		}
		return true
	})

	return discovered
}

func TestSlipStatus_IsTerminal_UnknownStatus(t *testing.T) {
	// Test that unknown status values return false
	unknownStatus := SlipStatus("unknown")
	if unknownStatus.IsTerminal() {
		t.Error("unknown SlipStatus should not be terminal")
	}
}

func TestStepStatus_IsTerminal_UnknownStatus(t *testing.T) {
	// Test that unknown status values return false
	unknownStatus := StepStatus("unknown")
	if unknownStatus.IsTerminal() {
		t.Error("unknown StepStatus should not be terminal")
	}
}

func TestStepStatus_IsFailure_UnknownStatus(t *testing.T) {
	// Test that unknown status values return false
	unknownStatus := StepStatus("unknown")
	if unknownStatus.IsFailure() {
		t.Error("unknown StepStatus should not be considered a failure")
	}
}
