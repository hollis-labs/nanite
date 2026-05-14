package store

import (
	"context"
	"strings"
	"testing"
)

// TestMigration063_Idempotent_NoTxLeak pins the c195-deploy crash-loop
// regression (CW-20260514-0052).
//
// Before the migration-063 fix, the file was:
//
//	BEGIN;
//	ALTER TABLE agent_profiles DROP COLUMN default_mode;
//	END;
//
// On second boot the column is already gone, so ALTER fails with
// "no such column: default_mode". The migrate runner suppresses
// DROP-COLUMN errors of that exact shape (store.go:117-122) and
// continues — but the suppressed Exec never reached END, so the
// transaction opened by BEGIN stays open on the pinned migration
// connection. The next caller of `s.DB.Begin()` (in production:
// SeedProviders) then trips SQLite's "cannot start a transaction
// within a transaction" and the process exits, which under launchd
// becomes a crash-loop.
//
// Test shape: open the DB twice, then exercise s.DB.Begin() on the
// second open. The first open runs 063 against a column that exists.
// The second open runs 063 against a DB where 063 already landed
// (the column is gone) — the migrate runner must absorb the
// "no such column" error AND leave the connection clean so Begin
// succeeds.
func TestMigration063_Idempotent_NoTxLeak(t *testing.T) {
	dbPath := t.TempDir() + "/migration063.db"

	s1, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	s1.Close()

	s2, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("second New (post-063 re-run): %v", err)
	}
	defer s2.Close()

	// Mirror SeedProviders' first move: open a transaction on the
	// shared pool. Pre-fix this errors with "cannot start a
	// transaction within a transaction"; post-fix it succeeds.
	tx, err := s2.DB.Begin()
	if err != nil {
		if strings.Contains(err.Error(), "transaction within a transaction") {
			t.Fatalf("migration 063 leaked an open transaction across boots — Begin() failed with: %v", err)
		}
		t.Fatalf("Begin after second New: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
}
