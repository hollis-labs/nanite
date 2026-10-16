package store

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/core/util/sqlite/sqlitekit"
	"github.com/hollis-labs/nanite/internal/storetest/migratedtemplate"
	"github.com/pressly/goose/v3"
)

// Tests that start from an older schema used to walk goose down from the latest
// version on every run (and every subtest): ~25-70 migrations re-parsed per
// test, which dominates internal/store under -race. newTestStoreAtVersion walks
// down once per distinct version, from the nearest already-built higher
// version, and hands every caller a private copy of the result.
var versionTemplates = struct {
	mu    sync.Mutex
	paths map[int64]string
}{paths: map[int64]string{}}

// newTestStoreAtVersion returns a private Store whose schema is at goose
// version `version` (what provider.DownTo(ctx, version) would leave), without
// walking the migrations down for every caller.
func newTestStoreAtVersion(t *testing.T, version int64) *Store {
	t.Helper()
	src := versionTemplatePath(t, version)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := copyFile(dbPath, src); err != nil {
		t.Fatalf("copy schema v%d template: %v", version, err)
	}
	s := openTestStoreFile(t, dbPath)
	t.Cleanup(func() { s.Close(context.Background()) })
	if got := mustDBVersion(t, s); got != version {
		t.Fatalf("copied schema template reports version %d, want %d", got, version)
	}
	return s
}

func versionTemplatePath(t *testing.T, version int64) string {
	t.Helper()
	versionTemplates.mu.Lock()
	defer versionTemplates.mu.Unlock()
	if p, ok := versionTemplates.paths[version]; ok {
		return p
	}

	dir, err := os.MkdirTemp("", "nanite-store-v*-template-")
	if err != nil {
		t.Fatalf("create schema template dir: %v", err)
	}
	dst := filepath.Join(dir, "template.db")

	// Start from the nearest cached version above the target; else the latest.
	var best int64 = -1
	for v, p := range versionTemplates.paths {
		if v > version && (best == -1 || v < best) {
			best = v
			if err := copyFile(dst, p); err != nil {
				t.Fatalf("copy schema v%d template: %v", v, err)
			}
		}
	}
	if best == -1 {
		migratedtemplate.Copy(t, dst, func(path string) error {
			s, err := New(context.Background(), path)
			if err != nil {
				return err
			}
			return s.Close(context.Background())
		})
	}

	s := openTestStoreFile(t, dst)
	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("construct goose provider: %v", err)
	}
	if _, err := provider.DownTo(context.Background(), version); err != nil {
		t.Fatalf("goose DownTo %d: %v", version, err)
	}
	if got := mustDBVersion(t, s); got != version {
		t.Fatalf("schema template reports version %d after DownTo, want %d", got, version)
	}
	// Close so the template file is self-contained (WAL checkpointed).
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("close schema v%d template: %v", version, err)
	}
	versionTemplates.paths[version] = dst
	return dst
}

func openTestStoreFile(t *testing.T, dbPath string) *Store {
	t.Helper()
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
	return &Store{DB: db, dbPath: absPath}
}

func mustDBVersion(t *testing.T, s *Store) int64 {
	t.Helper()
	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("construct goose provider: %v", err)
	}
	v, err := provider.GetDBVersion(context.Background())
	if err != nil {
		t.Fatalf("goose version: %v", err)
	}
	return v
}
