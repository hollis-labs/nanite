package store

import (
	"context"
	"testing"
)

// TestMigration005_Idempotent verifies that running store.New twice on the
// same DB (simulating a service restart) does not fail. The migration runner
// re-executes all SQL files on every startup.
func TestMigration005_Idempotent(t *testing.T) {
	dbPath := t.TempDir() + "/idempotent005.db"

	s1, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first New failed: %v", err)
	}
	s1.Close(context.Background())

	s2, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("second New (restart simulation) failed: %v", err)
	}
	s2.Close(context.Background())
}
