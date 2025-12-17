// Package clickhousemigrator provides a generic, reusable database migration framework
// for ClickHouse databases. It is designed to be used by any application that needs
// to manage ClickHouse schema migrations in a consistent and reliable way.
//
// The package follows the methodology defined in the unit-tester application but is
// abstracted to serve any implementation. Applications define their own migrations
// using the Migration struct and register them with a Migrator instance.
//
// Key features:
//   - Version-tracked migrations with up/down SQL support
//   - Automatic schema version detection and validation
//   - Migration rollback support (MigrateToVersion)
//   - Schema validation to ensure expected tables exist
//   - Timeout handling for complex migrations
//   - Structured logging integration
//
// Example usage:
//
//	// Define your migrations
//	migrations := []clickhousemigrator.Migration{
//	    {
//	        Version:     1,
//	        Name:        "create_users_table",
//	        Description: "Create users table",
//	        UpSQL:       "CREATE TABLE IF NOT EXISTS users (...)",
//	        DownSQL:     "DROP TABLE IF EXISTS users",
//	    },
//	}
//
//	// Create connection and migrator
//	conn, _ := clickhousemigrator.NewConnection(config, logger)
//	migrator := clickhousemigrator.NewMigrator(conn, logger,
//	    clickhousemigrator.WithMigrations(migrations),
//	    clickhousemigrator.WithExpectedTables([]string{"users"}),
//	)
//
//	// Run migrations
//	err := migrator.CreateTables(ctx)
package clickhousemigrator
