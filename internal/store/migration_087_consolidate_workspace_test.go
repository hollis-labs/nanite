package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestMigration087_ConsolidatesPersonalIntoDefault simulates a
// pre-consolidation DB (both "default" and "personal" workspaces exist,
// with a real session parked under "personal") and re-opens the store —
// every migration file re-runs on every boot (no schema_migrations table,
// see store.go migrate()), so migration 087 runs against this
// now-populated "personal" state for the first time on the second open,
// the same way it would against a real pre-existing production DB.
func TestMigration087_ConsolidatesPersonalIntoDefault(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "consolidate.db")
	s1, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("New first open: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, id := range []string{"default", "personal"} {
		if _, err := s1.DB.Exec(
			`INSERT INTO workspaces (id, name, description, settings, created_at, updated_at) VALUES (?, ?, '', '{}', ?, ?)`,
			id, id, now, now,
		); err != nil {
			t.Fatalf("seed workspace %s: %v", id, err)
		}
	}
	sess := &Session{WorkspaceID: "personal", Title: "pre-consolidation session"}
	if err := s1.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close before re-open: %v", err)
	}

	s2, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("New second open (re-run migrations): %v", err)
	}
	defer s2.Close()

	got, err := s2.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession after migration: %v", err)
	}
	if got.WorkspaceID != "default" {
		t.Errorf("expected session re-pointed to workspace 'default', got %q", got.WorkspaceID)
	}

	if _, err := s2.GetWorkspace("personal"); err == nil {
		t.Error("expected 'personal' workspace to be removed by migration 087")
	}
}

// TestMigration087_IdempotentReRun proves the migration is safe to run
// repeatedly (it re-runs on every boot by design) even once "personal"
// has already been fully consolidated away — the UPDATE and DELETE both
// match zero rows on the second and later runs.
func TestMigration087_IdempotentReRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idempotent.db")
	s1, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("New first call: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close first call: %v", err)
	}

	s2, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("New second call (re-run migrations): %v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close second call: %v", err)
	}

	s3, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("New third call (re-run migrations again): %v", err)
	}
	defer s3.Close()
}
