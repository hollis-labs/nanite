package store

import (
	"errors"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// IsForeignKeyViolation identifies a rejected relational reference while
// leaving infrastructure failures (busy, abort, IO, closed DB) distinct.
func IsForeignKeyViolation(err error) bool {
	var dbErr *sqlite.Error
	return errors.As(err, &dbErr) && dbErr.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
}

// IsUniqueConstraint identifies a duplicate value, including wrapped causes.
func IsUniqueConstraint(err error) bool {
	var dbErr *sqlite.Error
	return errors.As(err, &dbErr) && dbErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}
