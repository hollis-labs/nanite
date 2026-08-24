package store

import (
	"context"
	"testing"
)

func TestCatalogSourceCRUD(t *testing.T) {
	s := newTestStore(t)

	if err := s.Seed(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A fresh seed deliberately leaves catalog source configuration empty.
	sources, err := s.ListCatalogSources(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if sources == nil {
		t.Fatal("fresh catalog source list is nil, want a non-nil empty slice")
	}
	if len(sources) != 0 {
		t.Fatalf("expected no seeded sources, got %d", len(sources))
	}

	// Create a custom source.
	custom, err := s.CreateCatalogSource(context.Background(), "My Fork", "https://example.com/catalog.yaml", "custom", 50)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if custom.Name != "My Fork" {
		t.Errorf("name: expected 'My Fork', got %q", custom.Name)
	}
	if custom.Priority != 50 {
		t.Errorf("priority: expected 50, got %d", custom.Priority)
	}

	// List should now contain only the operator-created source.
	sources, _ = s.ListCatalogSources(context.Background())
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	if sources[0].Name != "My Fork" {
		t.Errorf("listed source: expected 'My Fork', got %q", sources[0].Name)
	}

	// Update custom source.
	err = s.UpdateCatalogSource(context.Background(), custom.ID, "My Forked Catalog", custom.URL, true, 150)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// Updated fields should be reflected in the list.
	sources, _ = s.ListCatalogSources(context.Background())
	if sources[0].Name != "My Forked Catalog" {
		t.Errorf("expected updated custom first, got %q", sources[0].Name)
	}

	// Get by ID.
	got, err := s.GetCatalogSource(context.Background(), custom.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "My Forked Catalog" {
		t.Errorf("get name: expected 'My Forked Catalog', got %q", got.Name)
	}

	// Delete custom source.
	if err := s.DeleteCatalogSource(context.Background(), custom.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	sources, _ = s.ListCatalogSources(context.Background())
	if sources == nil {
		t.Fatal("catalog source list after deleting the final row is nil, want a non-nil empty slice")
	}
	if len(sources) != 0 {
		t.Fatalf("expected no sources after delete, got %d", len(sources))
	}

	// Delete nonexistent.
	if err := s.DeleteCatalogSource(context.Background(), "nonexistent"); err == nil {
		t.Error("expected error deleting nonexistent source")
	}
}

func TestListCatalogSourcesEmptyReturnsNonNilSlice(t *testing.T) {
	s := newTestStore(t)

	sources, err := s.ListCatalogSources(context.Background())
	if err != nil {
		t.Fatalf("ListCatalogSources: %v", err)
	}
	if sources == nil {
		t.Fatal("ListCatalogSources returned nil for an empty table")
	}
	if len(sources) != 0 {
		t.Fatalf("ListCatalogSources returned %d rows for an empty table", len(sources))
	}
}

func TestCatalogSourceDuplicateURL(t *testing.T) {
	s := newTestStore(t)

	_, err := s.CreateCatalogSource(context.Background(), "Dup 1", "https://example.com/dup.yaml", "custom", 10)
	if err != nil {
		t.Fatalf("create first: %v", err)
	}

	_, err = s.CreateCatalogSource(context.Background(), "Dup 2", "https://example.com/dup.yaml", "custom", 20)
	if err == nil {
		t.Error("expected error for duplicate URL")
	}
}
