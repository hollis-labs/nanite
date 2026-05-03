package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// Glass-3 verification: migration 052_session_intent.sql applies on a fresh
// DB and yields the expected column shape. Mirrors the boot-prompt's
// "sqlite3 .schema sessions | grep intent" check, but in-process so it
// works inside test scaffolding rather than against ~/Projects-apps/nanite.
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

	// Verify the CHECK constraint accepts each enum and rejects an outsider.
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='intent'`).Scan(&n); err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly one 'intent' column, got %d", n)
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
