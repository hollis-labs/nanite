package reactions

import (
	"context"
	"os"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// newTestStore opens a fresh, fully-migrated *store.Store rooted at a
// t.TempDir() path — same pattern internal/selftools/self_tools_test.go's
// own newTestStore and internal/agent/reflexes/state_test.go's
// newReflexTestStore already use for this exact purpose.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := t.TempDir() + "/test.db"
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close(); os.Remove(dbPath) })
	return s
}

// insertReaction is a small test helper wrapping store.InsertSelftoolReaction
// with t.Fatalf on error, to keep the seed setup in each test terse.
func insertReaction(t *testing.T, s *store.Store, r store.SelftoolReaction) {
	t.Helper()
	if err := s.InsertSelftoolReaction(context.Background(), r); err != nil {
		t.Fatalf("InsertSelftoolReaction(%+v): %v", r, err)
	}
}
