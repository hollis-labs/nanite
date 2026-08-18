package store

import (
	"strings"
	"testing"
)

// splitSQLStatementsForTest splits sql into its semicolon-terminated
// statements, dropping any pure-comment fragment (leading goose annotations
// like "-- +goose Up"/"-- +goose Down" and this repo's historical migration
// header comments are just SQL comments as far as this is concerned).
//
// This exists only for tests that want to re-execute one or more of a
// migration file's statements directly, independent of the production
// migration runner (goose), to probe the SQL's own idempotency —
// deliberately simpler than a real SQL parser: it does NOT track
// BEGIN...END nesting (goose's own StatementBegin/StatementEnd markers
// handle that in production; see migrations 003/043's CREATE TRIGGER
// blocks), so it must only be used against migration files that contain
// neither trigger definitions nor semicolons embedded in string literals.
func splitSQLStatementsForTest(t *testing.T, sql string) []string {
	t.Helper()
	var stmts []string
	for _, raw := range strings.Split(sql, ";") {
		stmt := strings.TrimSpace(raw)
		if stmt == "" || isCommentOnlyForTest(stmt) {
			continue
		}
		stmts = append(stmts, stmt)
	}
	return stmts
}

func isCommentOnlyForTest(stmt string) bool {
	for _, line := range strings.Split(stmt, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		return false
	}
	return true
}
