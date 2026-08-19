package store

import (
	"context"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrate103DropsSessionIntentColumn is the regression test for
// TASKS/phase-0/28-cut-session-intent-classifier.md: a fresh full migration
// run must leave sessions without the intent column, and a simulated
// restart (a second s.migrate() call) must be a clean no-op. Mirrors
// migration_102_drop_session_compaction_test.go's shape (same task family,
// same post-goose-adoption drop-column pattern).
func TestMigrate103DropsSessionIntentColumn(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	exists, err := sessionsColumnExists(ctx, s, "intent")
	if err != nil {
		t.Fatalf("check sessions.intent exists: %v", err)
	}
	if exists {
		t.Errorf("sessions.intent still exists after a fresh full migration run — migration 103 should have dropped it")
	}

	// The sessions table must still be otherwise usable — dropping the
	// column must not touch the rest of the schema or any existing rows.
	sess := &Session{Title: "post-drop-probe"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession after migration 103: %v", err)
	}
	got, err := s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession after migration 103: %v", err)
	}
	if got.Title != "post-drop-probe" {
		t.Errorf("GetSession.Title: got %q, want %q", got.Title, "post-drop-probe")
	}

	assertGooseHasNothingPending(t, s)

	if err := s.migrate(); err != nil {
		t.Fatalf("re-migrate after column already dropped: %v", err)
	}

	exists, err = sessionsColumnExists(ctx, s, "intent")
	if err != nil {
		t.Fatalf("check sessions.intent exists after re-migrate: %v", err)
	}
	if exists {
		t.Errorf("sessions.intent reappeared after a re-migrate — migration 103 must not be lost or reapplied on restart")
	}
}

// TestMigrate103DownReaddsSessionIntentColumn is the tested Down half of
// migration 103: goose's DownTo must be able to reverse this migration,
// re-adding the intent column with its original nullable
// TEXT-plus-CHECK-enum shape (migrations/053_session_intent.sql), and the
// CHECK constraint must actually be enforced at the SQL layer again, not
// just structurally present. This also covers what the deleted
// migration_053_verify_test.go used to verify for the Up side (the CHECK
// constraint rejects a non-enum value at the SQL layer) — now exercised on
// the Down-recreated column instead, since a fresh DB no longer has it.
func TestMigrate103DownReaddsSessionIntentColumn(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Confirm the Up side actually ran before exercising Down.
	exists, err := sessionsColumnExists(ctx, s, "intent")
	if err != nil {
		t.Fatalf("check sessions.intent exists before down: %v", err)
	}
	if exists {
		t.Fatalf("sessions.intent exists before down migration — Up did not drop it as expected")
	}

	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("construct goose provider: %v", err)
	}

	if _, err := provider.DownTo(ctx, 102); err != nil {
		t.Fatalf("goose DownTo 102 (reverse migration 103): %v", err)
	}

	exists, err = sessionsColumnExists(ctx, s, "intent")
	if err != nil {
		t.Fatalf("check sessions.intent exists after down: %v", err)
	}
	if !exists {
		t.Fatalf("sessions.intent missing after Down migration — 103's Down section should have re-added it")
	}

	// The re-added column must be writable/readable, and its CHECK
	// constraint must actually be enforced by SQLite again — exercised via
	// raw SQL, bypassing any Go-level validation (there is none left; the
	// classifier and its SetSessionIntent helper are both deleted), so this
	// verifies the SQL-layer constraint itself.
	sess := &Session{Title: "down-check-probe"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession after down: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE sessions SET intent = 'long-running' WHERE id = ?`, sess.ID); err != nil {
		t.Fatalf("raw UPDATE to valid enum value rejected unexpectedly: %v", err)
	}
	var intent string
	if err := s.DB.QueryRow(`SELECT intent FROM sessions WHERE id = ?`, sess.ID).Scan(&intent); err != nil {
		t.Fatalf("read back re-added intent column: %v", err)
	}
	if intent != "long-running" {
		t.Errorf("re-added intent column round-trip: got %q, want %q", intent, "long-running")
	}
	if _, err := s.DB.Exec(`UPDATE sessions SET intent = 'garbage' WHERE id = ?`, sess.ID); err == nil {
		t.Fatalf("raw UPDATE to non-enum value 'garbage' should have failed CHECK constraint, got nil error")
	}

	// Back up to the top so the store is left in its normal fully-migrated
	// state (mirrors the shape of a real down-then-back-up-again cycle).
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up after DownTo 102: %v", err)
	}
	exists, err = sessionsColumnExists(ctx, s, "intent")
	if err != nil {
		t.Fatalf("check sessions.intent exists after re-up: %v", err)
	}
	if exists {
		t.Errorf("sessions.intent reappeared after Up replayed 103 — Up should drop it again")
	}
}
