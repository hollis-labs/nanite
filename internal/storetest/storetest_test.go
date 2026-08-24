package storetest

import (
	"context"
	"path/filepath"
	"testing"
)

func TestNewCreatesIsolatedStoreCopies(t *testing.T) {
	t.Parallel()

	firstPath := filepath.Join(t.TempDir(), "first.db")
	secondPath := filepath.Join(t.TempDir(), "second.db")
	first, err := New(t, context.Background(), firstPath)
	if err != nil {
		t.Fatalf("New(first): %v", err)
	}
	t.Cleanup(func() { _ = first.Close(context.Background()) })
	second, err := New(t, context.Background(), secondPath)
	if err != nil {
		t.Fatalf("New(second): %v", err)
	}
	t.Cleanup(func() { _ = second.Close(context.Background()) })

	if first.DBPath(context.Background()) == second.DBPath(context.Background()) {
		t.Fatalf("stores share database path %q", first.DBPath(context.Background()))
	}
	if first.DBPath(context.Background()) != firstPath {
		t.Fatalf("first DB path = %q, want %q", first.DBPath(context.Background()), firstPath)
	}
	if second.DBPath(context.Background()) != secondPath {
		t.Fatalf("second DB path = %q, want %q", second.DBPath(context.Background()), secondPath)
	}

	if _, err := first.DB.Exec(`CREATE TABLE fixture_isolation (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table in first store: %v", err)
	}
	var count int
	if err := second.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'fixture_isolation'`).Scan(&count); err != nil {
		t.Fatalf("query second store schema: %v", err)
	}
	if count != 0 {
		t.Fatal("schema mutation in first store leaked into second store")
	}
}
