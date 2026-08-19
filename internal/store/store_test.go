package store

import (
	"context"
	"database/sql"
	"errors"
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
	//
	// agent_modes was dropped by migration 104 (Phase 0 item 21, "Cut
	// Modes, in full") — Legacy Agent Mode no longer has a backing table.
	// Swapped in skills as a still-real post-104 table so this check
	// still exercises "migrations ran to completion" rather than
	// asserting a table that no longer exists. workspaces was dropped by
	// migration 109 (Phase 0 item 20, retire workspaces) — projects
	// (nested under it, now flat) is the still-real replacement check.
	tables := []string{"projects", "sessions", "messages", "agent_profiles", "skills", "session_agents"}
	for _, tbl := range tables {
		var name string
		err := s.DB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found after migration: %v", tbl, err)
		}
	}

	// workspaces must be gone post-migration (migration 109).
	var wsName string
	err = s.DB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='workspaces'").Scan(&wsName)
	if err == nil {
		t.Errorf("table \"workspaces\" still present after migration — expected it dropped by migration 109")
	} else if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("check workspaces absence: %v", err)
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

	// Verify data is the same — should have exactly 1 user_settings row
	// (the new idempotency gate; Phase 0 item 20 retired the workspaces
	// table this check used to count).
	var count int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM user_settings").Scan(&count); err != nil {
		t.Fatalf("count user_settings: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 user_settings row after idempotent seed, got %d", count)
	}
}
