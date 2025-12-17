package clickhousemigrator

import (
	"errors"
	"testing"
)

// ============================================================================
// MigrationError Tests
// ============================================================================

func TestMigrationError_Error(t *testing.T) {
	testCases := []struct {
		name     string
		err      *MigrationError
		expected string
	}{
		{
			name: "up migration error",
			err: &MigrationError{
				Version:     1,
				Name:        "create_users",
				Description: "Creates the users table",
				Operation:   "up",
				Err:         errors.New("syntax error"),
			},
			expected: "migration up failed for version 1 (create_users): syntax error",
		},
		{
			name: "down migration error",
			err: &MigrationError{
				Version:     5,
				Name:        "add_column",
				Description: "Adds a column",
				Operation:   "down",
				Err:         errors.New("table not found"),
			},
			expected: "migration down failed for version 5 (add_column): table not found",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.err.Error()
			if result != tc.expected {
				t.Errorf("expected '%s', got '%s'", tc.expected, result)
			}
		})
	}
}

func TestMigrationError_Unwrap(t *testing.T) {
	underlyingErr := errors.New("underlying error")
	migErr := &MigrationError{
		Version:   1,
		Name:      "test",
		Operation: "up",
		Err:       underlyingErr,
	}

	unwrapped := migErr.Unwrap()
	if unwrapped != underlyingErr {
		t.Error("expected Unwrap to return the underlying error")
	}
}

func TestMigrationError_ErrorsIs(t *testing.T) {
	underlyingErr := errors.New("underlying error")
	migErr := &MigrationError{
		Version:   1,
		Name:      "test",
		Operation: "up",
		Err:       underlyingErr,
	}

	if !errors.Is(migErr, underlyingErr) {
		t.Error("expected errors.Is to match underlying error")
	}
}

func TestMigrationError_ErrorsAs(t *testing.T) {
	underlyingErr := errors.New("underlying error")
	migErr := &MigrationError{
		Version:   1,
		Name:      "test",
		Operation: "up",
		Err:       underlyingErr,
	}

	var target *MigrationError
	if !errors.As(migErr, &target) {
		t.Error("expected errors.As to match MigrationError")
	}

	if target.Version != 1 {
		t.Errorf("expected version 1, got %d", target.Version)
	}
}

// ============================================================================
// SchemaValidationError Tests
// ============================================================================

func TestSchemaValidationError_Error_WithTable(t *testing.T) {
	err := &SchemaValidationError{
		Table:   "users",
		Message: "required table does not exist",
		Err:     ErrTableNotFound,
	}

	expected := "schema validation failed for table users: required table does not exist"
	if err.Error() != expected {
		t.Errorf("expected '%s', got '%s'", expected, err.Error())
	}
}

func TestSchemaValidationError_Error_WithoutTable(t *testing.T) {
	err := &SchemaValidationError{
		Table:   "",
		Message: "general validation error",
		Err:     ErrSchemaValidationFailed,
	}

	expected := "schema validation failed: general validation error"
	if err.Error() != expected {
		t.Errorf("expected '%s', got '%s'", expected, err.Error())
	}
}

func TestSchemaValidationError_Unwrap(t *testing.T) {
	underlyingErr := errors.New("underlying error")
	validationErr := &SchemaValidationError{
		Table:   "test",
		Message: "test message",
		Err:     underlyingErr,
	}

	unwrapped := validationErr.Unwrap()
	if unwrapped != underlyingErr {
		t.Error("expected Unwrap to return the underlying error")
	}
}

func TestSchemaValidationError_ErrorsIs(t *testing.T) {
	validationErr := &SchemaValidationError{
		Table:   "test",
		Message: "test message",
		Err:     ErrTableNotFound,
	}

	if !errors.Is(validationErr, ErrTableNotFound) {
		t.Error("expected errors.Is to match ErrTableNotFound")
	}
}

func TestSchemaValidationError_ErrorsAs(t *testing.T) {
	validationErr := &SchemaValidationError{
		Table:   "users",
		Message: "test message",
		Err:     ErrTableNotFound,
	}

	var target *SchemaValidationError
	if !errors.As(validationErr, &target) {
		t.Error("expected errors.As to match SchemaValidationError")
	}

	if target.Table != "users" {
		t.Errorf("expected table 'users', got '%s'", target.Table)
	}
}

// ============================================================================
// Sentinel Errors Tests
// ============================================================================

func TestSentinelErrors(t *testing.T) {
	testCases := []struct {
		name string
		err  error
	}{
		{"ErrNilConnection", ErrNilConnection},
		{"ErrNilLogger", ErrNilLogger},
		{"ErrSchemaVersionTableNotFound", ErrSchemaVersionTableNotFound},
		{"ErrMigrationFailed", ErrMigrationFailed},
		{"ErrMigrationRevertFailed", ErrMigrationRevertFailed},
		{"ErrSchemaValidationFailed", ErrSchemaValidationFailed},
		{"ErrTableNotFound", ErrTableNotFound},
		{"ErrInvalidMigration", ErrInvalidMigration},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Errorf("%s should not be nil", tc.name)
			}

			// Verify error message is not empty
			if tc.err.Error() == "" {
				t.Errorf("%s should have a non-empty error message", tc.name)
			}
		})
	}
}

func TestSentinelErrors_ErrorsIs(t *testing.T) {
	// Test that sentinel errors can be matched with errors.Is
	testCases := []struct {
		err    error
		target error
	}{
		{ErrNilConnection, ErrNilConnection},
		{ErrTableNotFound, ErrTableNotFound},
	}

	for _, tc := range testCases {
		if !errors.Is(tc.err, tc.target) {
			t.Errorf("expected errors.Is(%v, %v) to be true", tc.err, tc.target)
		}
	}
}
