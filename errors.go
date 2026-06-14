// Package filedb provides a SQL-queryable, file-backed local datastore that
// maps CSV and JSON files to named tables. It is designed to be embedded in
// Go applications (library) and is also available as a standalone CLI tool.
package filedb

import "errors"

// Sentinel errors returned by filedb operations. Callers may compare returned
// errors with errors.Is to distinguish failure categories.
var (
	// ErrTableExists is returned when AddTable is called with a name that is
	// already registered in the datastore.
	ErrTableExists = errors.New("filedb: table already exists")

	// ErrTableNotFound is returned when an operation references a table name
	// that has not been registered.
	ErrTableNotFound = errors.New("filedb: table not found")

	// ErrColumnNotFound is returned when a query references a column that is
	// absent from the table schema.
	ErrColumnNotFound = errors.New("filedb: column not found")

	// ErrTypeMismatch is returned when a value cannot be coerced to the
	// expected column type.
	ErrTypeMismatch = errors.New("filedb: type mismatch")

	// ErrNullNotAllowed is returned when a null or missing value is supplied
	// for a non-nullable column that carries no default.
	ErrNullNotAllowed = errors.New("filedb: null value not allowed")

	// ErrUniqueViolation is returned when an INSERT or UPDATE would produce a
	// duplicate key in a unique index.
	ErrUniqueViolation = errors.New("filedb: unique constraint violation")

	// ErrInvalidSchema is returned when a TableDef fails validation on
	// AddTable.
	ErrInvalidSchema = errors.New("filedb: invalid schema")

	// ErrInvalidSQL is returned when a SQL statement cannot be parsed or
	// executed against the registered tables.
	ErrInvalidSQL = errors.New("filedb: invalid SQL")
)
