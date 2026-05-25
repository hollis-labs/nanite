package store

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hollis-labs/go-sqlite/sqlitekit"
)

var (
	testStoreTemplateOnce sync.Once
	testStoreTemplatePath string
	testStoreTemplateErr  error
)

// newTestStore creates an in-memory Store for testing.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	templatePath := testStoreTemplate(t)
	if err := copyFile(dbPath, templatePath); err != nil {
		t.Fatalf("copy test store template: %v", err)
	}
	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		t.Fatalf("resolve test db path: %v", err)
	}
	db, err := sqlitekit.OpenSingle(context.Background(), absPath, sqlitekit.OpenOptions{
		Options:         sqlitekit.WriterOptions(),
		CreateParentDir: true,
	})
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	s := &Store{DB: db, dbPath: absPath}
	t.Cleanup(func() { s.Close() })
	return s
}

func testStoreTemplate(t *testing.T) string {
	t.Helper()
	testStoreTemplateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nanite-store-template-*")
		if err != nil {
			testStoreTemplateErr = err
			return
		}
		testStoreTemplatePath = filepath.Join(dir, "template.db")
		s, err := New(context.Background(), testStoreTemplatePath)
		if err != nil {
			testStoreTemplateErr = err
			return
		}
		if err := s.Close(); err != nil {
			testStoreTemplateErr = err
			return
		}
	})
	if testStoreTemplateErr != nil {
		t.Fatalf("create test store template: %v", testStoreTemplateErr)
	}
	return testStoreTemplatePath
}

func copyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if info, err := os.Stat(src); err == nil {
		if err := os.Chmod(dst, info.Mode()); err != nil {
			return fmt.Errorf("chmod copied file: %w", err)
		}
	}
	return nil
}

func TestNew(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Verify the database is open by running a simple query.
	var n int
	if err := s.DB.QueryRow("SELECT 1").Scan(&n); err != nil {
		t.Fatalf("query after New: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1, got %d", n)
	}

	// Verify migrations ran by checking that tables exist.
	tables := []string{"workspaces", "sessions", "messages", "agent_profiles", "agent_modes", "session_agents"}
	for _, tbl := range tables {
		var name string
		err := s.DB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found after migration: %v", tbl, err)
		}
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
}

func TestSeedIdempotent(t *testing.T) {
	s := newTestStore(t)

	// First seed should succeed.
	if err := s.Seed(); err != nil {
		t.Fatalf("Seed() first call error: %v", err)
	}

	// Second seed should also succeed (no-op).
	if err := s.Seed(); err != nil {
		t.Fatalf("Seed() second call error: %v", err)
	}

	// Verify data is the same — should have exactly 2 workspaces from seed.
	var count int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM workspaces").Scan(&count); err != nil {
		t.Fatalf("count workspaces: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 workspaces after idempotent seed, got %d", count)
	}
}
