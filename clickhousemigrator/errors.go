package clickhousemigrator

import (
	"errors"
	"fmt"
)

// Sentinel errors for the clickhousemigrator package.
var (
	// ErrNilConnection is returned when a nil database connection is provided.
	ErrNilConnection = errors.New("database connection cannot be nil")

	// ErrNilLogger is returned when a nil logger is provided.
	ErrNilLogger = errors.New("logger cannot be nil")

	// ErrSchemaVersionTableNotFound is returned when the schema_version table doesn't exist.
	ErrSchemaVersionTableNotFound = errors.New("schema_version table not found")

	// ErrMigrationFailed is wrapped by every *MigrationError, so
	// errors.Is(err, ErrMigrationFailed) is true for any migration that failed to apply or
	// revert, regardless of the underlying cause. Use errors.As(err, &me) when you also need
	// the version or operation (DEVOPS-344, mirrored from postgresmigrator).
	ErrMigrationFailed = errors.New("migration failed")

	// ErrMigrationRevertFailed is additionally wrapped by a *MigrationError whose Operation
	// is "down", so errors.Is(err, ErrMigrationRevertFailed) singles out revert failures.
	ErrMigrationRevertFailed = errors.New("migration revert failed")

	// ErrSchemaValidationFailed is returned when schema validation fails.
	ErrSchemaValidationFailed = errors.New("schema validation failed")

	// ErrTableNotFound is returned when an expected table is not found.
	ErrTableNotFound = errors.New("required table not found")

	// ErrInvalidMigration is returned when a migration is invalid.
	ErrInvalidMigration = errors.New("invalid migration")
)

// MigrationError represents an error that occurred during migration.
type MigrationError struct {
	Version     int
	Name        string
	Description string
	Operation   string // "up" or "down"
	Err         error
}

// Error returns the error message.
func (e *MigrationError) Error() string {
	return fmt.Sprintf("migration %s failed for version %d (%s): %v", e.Operation, e.Version, e.Name, e.Err)
}

// Unwrap exposes the ErrMigrationFailed sentinel (plus ErrMigrationRevertFailed for a
// down), then the underlying cause, so errors.Is works for any of them and errors.As still
// reaches the driver error (DEVOPS-344). Before this the sentinels were declared and never
// wrapped, so errors.Is against them was always false.
//
// Breaking change from the single-error Unwrap this replaced: errors.Unwrap only calls the
// `Unwrap() error` form, so errors.Unwrap(err) and a direct migErr.Unwrap() no longer return
// the cause — use Cause() for that. Every constructor in this package sets a non-nil Err;
// the nil case only defends a hand-built value.
func (e *MigrationError) Unwrap() []error {
	out := []error{ErrMigrationFailed}
	if e.Operation == "down" {
		out = append(out, ErrMigrationRevertFailed)
	}
	if e.Err != nil {
		out = append(out, e.Err)
	}
	return out
}

// Cause returns the underlying error the migration failed with — the value errors.Unwrap
// returned before Unwrap became multi-error. nil only for a hand-built MigrationError.
func (e *MigrationError) Cause() error { return e.Err }

// SchemaValidationError represents an error during schema validation.
type SchemaValidationError struct {
	Table   string
	Message string
	Err     error
}

// Error returns the error message.
func (e *SchemaValidationError) Error() string {
	if e.Table != "" {
		return "schema validation failed for table " + e.Table + ": " + e.Message
	}
	return "schema validation failed: " + e.Message
}

// Unwrap returns the underlying error.
func (e *SchemaValidationError) Unwrap() error {
	return e.Err
}
