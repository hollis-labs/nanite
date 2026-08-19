package store

import (
	"context"
	"testing"
)

// TestMigration088_ConsolidatesPersonalIntoDefault (the pre-consolidation
// "default"+"personal" workspaces scenario, migration 088) was removed by
// Phase 0 item 20 (TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md):
// the in-app `workspaces` table and `sessions.workspace_id` — the two
// columns that test constructed directly against a live, fully-migrated
// Store — no longer exist past the new drop migrations, so the scenario
// can't even be built anymore, let alone exercised. The test's own doc
// comment already noted (pre-removal) that no live code path could
// recreate the scenario it guarded against; the columns' removal makes
// that permanent. Migration 088 itself is untouched (historical migrations
// are never rewritten) — this only removes the now-unconstructable
// regression test for it.

// TestMigration088_IdempotentReRun proves that opening the same database
// repeatedly is safe under goose: the ledger makes every migration,
// including 088, a clean no-op on the second and later boots. Unrelated to
// the workspaces/sessions.workspace_id columns above — kept as-is.
func TestMigration088_IdempotentReRun(t *testing.T) {
	dbPath := t.TempDir() + "/idempotent.db"
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
