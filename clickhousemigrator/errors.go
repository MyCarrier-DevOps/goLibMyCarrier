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

	// ErrMigrationFailed is returned when a migration fails to apply.
	ErrMigrationFailed = errors.New("migration failed")

	// ErrMigrationRevertFailed is returned when a migration fails to revert.
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

// Unwrap returns the underlying error.
func (e *MigrationError) Unwrap() error {
	return e.Err
}

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
