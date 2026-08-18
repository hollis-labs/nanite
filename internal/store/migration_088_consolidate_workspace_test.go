package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestMigration088_ConsolidatesPersonalIntoDefault re-applies migration
// 088's own UPDATE/DELETE statements directly against a database seeded to
// look like the real pre-consolidation state ("default" and "personal"
// workspaces both exist, with a real session parked under "personal") that
// this migration was originally written against (CW-20260815-0010: 22 real
// sessions under "personal", confirmed via a live-data read before the
// migration was authored).
//
// Before goose, this test drove the scenario through a second New() call,
// relying on the old runner's "every migration file re-runs on every boot"
// behavior to fire migration 087's (this file's pre-renumbering name)
// consolidation logic a second time, after seeding "personal" data between
// the two opens. That behavior is exactly what goose adoption removes —
// goose's ledger means migration 088 now runs at most once, ever, against
// any given database (see docs/engineering/architecture/05-storage-and-migrations.md
// and 09-adopt-goose-migrations's Work Log) — and store/seed.go has not
// created a "personal" workspace on a fresh install since CW-20260815-0010,
// so there is no live code path left that could ever recreate the scenario
// this migration guards against. Re-running it via a second boot is no
// longer a meaningful test of anything that can actually happen; the
// SQL's own correctness (does the UPDATE/DELETE pair actually consolidate
// personal into default, and is it idempotent if ever re-applied by hand,
// e.g. via `goose redo`) still is, so this test exercises that directly.
func TestMigration088_ConsolidatesPersonalIntoDefault(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "consolidate.db")
	s, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// Seed the pre-consolidation shape this migration was actually written
	// against: both workspaces present (sessions.workspace_id FK-references
	// workspaces(id), so "default" must exist too), a real session under
	// "personal".
	now := time.Now().UTC().Format(time.RFC3339)
	for _, id := range []string{"default", "personal"} {
		if _, err := s.DB.Exec(
			`INSERT INTO workspaces (id, name, description, settings, created_at, updated_at) VALUES (?, ?, '', '{}', ?, ?)`,
			id, id, now, now,
		); err != nil {
			t.Fatalf("seed workspace %s: %v", id, err)
		}
	}
	sess := &Session{WorkspaceID: "personal", Title: "pre-consolidation session"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	mig, err := migrationsFS.ReadFile("migrations/088_consolidate_personal_workspace.sql")
	if err != nil {
		t.Fatalf("read migration 088: %v", err)
	}
	stmts := splitSQLStatementsForTest(t, string(mig))
	for _, stmt := range stmts {
		if _, err := s.DB.Exec(stmt); err != nil {
			t.Fatalf("exec migration 088 statement: %v\nSQL: %s", err, stmt)
		}
	}

	got, err := s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession after migration: %v", err)
	}
	if got.WorkspaceID != "default" {
		t.Errorf("expected session re-pointed to workspace 'default', got %q", got.WorkspaceID)
	}
	if _, err := s.GetWorkspace("personal"); err == nil {
		t.Error("expected 'personal' workspace to be removed by migration 088")
	}

	// Re-applying the same statements a second time (e.g. `goose redo`, or
	// a hand re-run) must be a true no-op — both statements match zero rows.
	for _, stmt := range stmts {
		if _, err := s.DB.Exec(stmt); err != nil {
			t.Fatalf("exec migration 088 statement (second pass): %v\nSQL: %s", err, stmt)
		}
	}
	got2, err := s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession after second pass: %v", err)
	}
	if got2.WorkspaceID != "default" {
		t.Errorf("second pass: expected workspace still 'default', got %q", got2.WorkspaceID)
	}
}

// TestMigration088_IdempotentReRun proves that opening the same database
// repeatedly is safe under goose: the ledger makes every migration,
// including 088, a clean no-op on the second and later boots.
func TestMigration088_IdempotentReRun(t *testing.T) {
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
		t.Fatalf("New second call (re-open, goose ledger already applied): %v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close second call: %v", err)
	}

	s3, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("New third call (re-open again): %v", err)
	}
	defer s3.Close()
}
