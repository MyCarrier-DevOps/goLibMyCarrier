package slippy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
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
		{SlipStatusFailed, true},
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

// TestAllSlipStatusesInEnum verifies that all SlipStatus constants defined in code
// are represented in the database enum definition in dynamic_migrations.go.
// This test automatically discovers both the status constants AND the enum definitions,
// ensuring no status value is forgotten when updating either file.
func TestAllSlipStatusesInEnum(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)

	// Parse status.go to automatically discover all SlipStatus constants
	statusFile := filepath.Join(dir, "status.go")
	discoveredStatuses := parseStatusConstants(t, statusFile, "SlipStatus")

	if len(discoveredStatuses) == 0 {
		t.Fatal("No SlipStatus constants found in status.go - parsing may have failed")
	}

	// Parse dynamic_migrations.go to extract enum definitions from SQL
	migrationsFile := filepath.Join(dir, "dynamic_migrations.go")
	enumValues := parseSlipStatusEnumsFromMigrations(t, migrationsFile)

	if len(enumValues) == 0 {
		t.Fatal("No slip status enum values found in dynamic_migrations.go - parsing may have failed")
	}

	// Verify all discovered statuses have enum values
	for statusName, statusValue := range discoveredStatuses {
		enumValue, exists := enumValues[statusValue]
		if !exists {
			t.Errorf(
				"SlipStatus constant %s = %q is missing from enum definition in dynamic_migrations.go",
				statusName,
				statusValue,
			)
			continue
		}
		if enumValue < 1 {
			t.Errorf("SlipStatus %q has invalid enum value %d - must be >= 1", statusValue, enumValue)
		}
	}

	// Verify no enum values exist for non-existent statuses
	for enumStatus := range enumValues {
		found := false
		for _, discoveredStatus := range discoveredStatuses {
			if discoveredStatus == enumStatus {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Enum definition includes %q but this status is not defined in status.go", enumStatus)
		}
	}

	// Verify no duplicate enum values
	seenValues := make(map[int]string)
	for status, value := range enumValues {
		if existing, found := seenValues[value]; found {
			t.Errorf("Duplicate enum value %d used for both %q and %q", value, existing, status)
		}
		seenValues[value] = status
	}

	// Verify counts match
	if len(discoveredStatuses) != len(enumValues) {
		t.Errorf(
			"Mismatch: %d slip statuses in status.go but %d enum values in dynamic_migrations.go",
			len(discoveredStatuses),
			len(enumValues),
		)
		t.Logf("Discovered statuses: %v", discoveredStatuses)
		t.Logf("Enum values: %v", enumValues)
	}
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

// parseSlipStatusEnumsFromMigrations parses dynamic_migrations.go and extracts
// the slip status enum values from the SQL strings.
func parseSlipStatusEnumsFromMigrations(t *testing.T, filename string) map[string]int {
	t.Helper()

	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", filename, err)
	}

	// Look for the slip status Enum8 definition in generateBaseTableMigration
	// Pattern: 'status_name' = N
	enumRegex := regexp.MustCompile(`'(\w+)'\s*=\s*(\d+)`)

	// Find the slip status enum block - look for "status Enum8(" which is the slip status
	// We want the one in generateBaseTableMigration, identified by context
	statusEnumPattern := regexp.MustCompile(`(?s)status Enum8\(\s*((?:'[^']+'\s*=\s*\d+[,\s]*)+)\)`)
	matches := statusEnumPattern.FindAllStringSubmatch(string(content), -1)

	if len(matches) == 0 {
		t.Fatal("Could not find slip status Enum8 definition in dynamic_migrations.go")
	}

	// Use the first match (from generateBaseTableMigration)
	enumBlock := matches[0][1]

	enumValues := make(map[string]int)
	enumMatches := enumRegex.FindAllStringSubmatch(enumBlock, -1)
	for _, match := range enumMatches {
		name := match[1]
		value, _ := strconv.Atoi(match[2])
		enumValues[name] = value
	}

	return enumValues
}

// TestAllStepStatusesInEnum verifies that all StepStatus constants defined in code
// are represented in the database enum definition in dynamic_migrations.go.
// This test automatically discovers both the status constants AND the enum definitions,
// ensuring no status value is forgotten when updating either file.
func TestAllStepStatusesInEnum(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)

	// Parse status.go to automatically discover all StepStatus constants
	statusFile := filepath.Join(dir, "status.go")
	discoveredStatuses := parseStatusConstants(t, statusFile, "StepStatus")

	if len(discoveredStatuses) == 0 {
		t.Fatal("No StepStatus constants found in status.go - parsing may have failed")
	}

	// Parse dynamic_migrations.go to extract enum definitions from SQL
	migrationsFile := filepath.Join(dir, "dynamic_migrations.go")
	enumValues := parseStepStatusEnumsFromMigrations(t, migrationsFile)

	if len(enumValues) == 0 {
		t.Fatal("No step status enum values found in dynamic_migrations.go - parsing may have failed")
	}

	// Verify all discovered statuses have enum values
	for statusName, statusValue := range discoveredStatuses {
		enumValue, exists := enumValues[statusValue]
		if !exists {
			t.Errorf(
				"StepStatus constant %s = %q is missing from enum definition in generateStepColumnEnsurer() in dynamic_migrations.go",
				statusName,
				statusValue,
			)
			continue
		}
		if enumValue < 1 {
			t.Errorf("StepStatus %q has invalid enum value %d - must be >= 1", statusValue, enumValue)
		}
	}

	// Verify no enum values exist for non-existent statuses
	for enumStatus := range enumValues {
		found := false
		for _, discoveredStatus := range discoveredStatuses {
			if discoveredStatus == enumStatus {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Enum definition includes %q but this status is not defined in status.go", enumStatus)
		}
	}

	// Verify no duplicate enum values
	seenValues := make(map[int]string)
	for status, value := range enumValues {
		if existing, found := seenValues[value]; found {
			t.Errorf("Duplicate enum value %d used for both %q and %q", value, existing, status)
		}
		seenValues[value] = status
	}

	// Verify counts match
	if len(discoveredStatuses) != len(enumValues) {
		t.Errorf(
			"Mismatch: %d step statuses in status.go but %d enum values in dynamic_migrations.go",
			len(discoveredStatuses),
			len(enumValues),
		)
		t.Logf("Discovered statuses: %v", discoveredStatuses)
		t.Logf("Enum values: %v", enumValues)
	}
}

// parseStepStatusEnumsFromMigrations parses dynamic_migrations.go and extracts
// the step status enum values from the SQL strings in generateStepColumnEnsurer.
func parseStepStatusEnumsFromMigrations(t *testing.T, filename string) map[string]int {
	t.Helper()

	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", filename, err)
	}

	// Look for the step status Enum8 definition in generateStepColumnEnsurer
	// Format: 'pending'=1, 'held'=2, ... (no spaces around =)
	enumRegex := regexp.MustCompile(`'(\w+)'\s*=\s*(\d+)`)

	// Find the generateStepColumnEnsurer enum block
	// This is identified by ADD COLUMN IF NOT EXISTS %s Enum8
	stepEnumPattern := regexp.MustCompile(`(?s)ADD COLUMN IF NOT EXISTS %s Enum8\(\s*((?:'[^']+'\s*=\s*\d+[,\s]*)+)\)`)
	matches := stepEnumPattern.FindAllStringSubmatch(string(content), -1)

	if len(matches) == 0 {
		t.Fatal("Could not find step status Enum8 definition in dynamic_migrations.go")
	}

	// Use the first match (from generateStepColumnEnsurer)
	enumBlock := matches[0][1]

	enumValues := make(map[string]int)
	enumMatches := enumRegex.FindAllStringSubmatch(enumBlock, -1)
	for _, match := range enumMatches {
		name := match[1]
		value, _ := strconv.Atoi(match[2])
		enumValues[name] = value
	}

	return enumValues
}
