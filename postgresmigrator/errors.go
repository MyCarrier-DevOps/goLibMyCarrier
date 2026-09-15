package postgresmigrator

import (
	"errors"
	"fmt"
)

// Sentinel errors for the postgresmigrator package.
var (
	// ErrNilConnection is returned when a nil database connection is provided.
	ErrNilConnection = errors.New("database connection cannot be nil")

	// ErrMigrationFailed is wrapped by every *MigrationError, so
	// errors.Is(err, ErrMigrationFailed) is true for any migration that failed to apply or
	// revert, regardless of the underlying cause. Use errors.As(err, &me) when you also need
	// the version or operation (DEVOPS-344).
	ErrMigrationFailed = errors.New("migration failed")

	// ErrSchemaValidationFailed is returned when schema validation fails.
	ErrSchemaValidationFailed = errors.New("schema validation failed")

	// ErrTableNotFound is returned when an expected table is not found.
	ErrTableNotFound = errors.New("required table not found")

	// ErrInvalidMigration is returned when a migration is invalid.
	ErrInvalidMigration = errors.New("invalid migration")
)

// MigrationError represents an error that occurred while applying or reverting
// a migration.
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

// Unwrap exposes both the ErrMigrationFailed sentinel and the underlying cause, so
// errors.Is works for either and errors.As still reaches a driver error such as
// *pgconn.PgError (DEVOPS-344).
//
// Breaking change from the single-error Unwrap this replaced: errors.Unwrap only calls the
// `Unwrap() error` form, so errors.Unwrap(err) and a direct migErr.Unwrap() no longer return
// the cause — use Cause() for that. Every constructor in this package sets a non-nil Err;
// the nil branch only defends a hand-built value.
func (e *MigrationError) Unwrap() []error {
	if e.Err == nil {
		return []error{ErrMigrationFailed}
	}
	return []error{ErrMigrationFailed, e.Err}
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
