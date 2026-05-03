package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// Glass-3 verification: migration 052_session_intent.sql applies on a fresh
// DB and yields the expected column shape, AND the CHECK constraint actually
// enforces the enum at the SQL layer (not just in the Go helper). Mirrors
// the boot-prompt's "sqlite3 .schema sessions | grep intent" check plus an
// INSERT/UPDATE round-trip that exercises the constraint directly so future
// schema edits cannot quietly drop the CHECK without breaking this test.
func TestMigration052_SessionIntentApplied(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fresh.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New(%s): %v", dbPath, err)
	}
	defer s.Close()

	var sql string
	row := s.DB.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='sessions'`)
	if err := row.Scan(&sql); err != nil {
		t.Fatalf("read sessions schema: %v", err)
	}
	if !strings.Contains(sql, "intent") {
		t.Fatalf("sessions schema missing intent column:\n%s", sql)
	}

	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='intent'`).Scan(&n); err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly one 'intent' column, got %d", n)
	}

	// Exercise the CHECK constraint directly via raw SQL, bypassing the
	// SetSessionIntent helper (the helper rejects in Go before reaching the
	// DB, so it can't tell us whether the CHECK constraint is still in
	// place). Insert a session with a valid enum value, then attempt to
	// UPDATE it to a non-enum value — the second must fail.
	seedWorkspace(t, s, "ws-migration-052")
	sess := &Session{WorkspaceID: "ws-migration-052", Title: "check-constraint-probe"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE sessions SET intent = 'long-running' WHERE id = ?`, sess.ID); err != nil {
		t.Fatalf("raw UPDATE to valid enum value rejected unexpectedly: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE sessions SET intent = 'garbage' WHERE id = ?`, sess.ID); err == nil {
		t.Fatalf("raw UPDATE to non-enum value 'garbage' should have failed CHECK constraint, got nil error")
	}
}

// Glass-3 verification: migrations are idempotent — re-opening the DB
// re-runs every migration cleanly. ALTER TABLE ADD COLUMN is gated by the
// "duplicate column" guard in store.go.
func TestMigration052_IdempotentReRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idempotent.db")
	s1, err := New(dbPath)
	if err != nil {
		t.Fatalf("New first call: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close first call: %v", err)
	}

	s2, err := New(dbPath)
	if err != nil {
		t.Fatalf("New second call (re-run migrations): %v", err)
	}
	defer s2.Close()
}
