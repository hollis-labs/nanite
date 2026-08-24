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

	// Seed() inserts the official source.
	sources, err := s.ListCatalogSources(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("expected 1 seeded source, got %d", len(sources))
	}
	if sources[0].Name != "Hollis Labs" {
		t.Errorf("expected seeded source 'Hollis Labs', got %q", sources[0].Name)
	}
	if sources[0].Type != "official" {
		t.Errorf("expected type 'official', got %q", sources[0].Type)
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

	// List should now have 2 (official at priority 100 first, custom at 50).
	sources, _ = s.ListCatalogSources(context.Background())
	if len(sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(sources))
	}
	if sources[0].Name != "Hollis Labs" {
		t.Errorf("first source should be official (higher priority), got %q", sources[0].Name)
	}

	// Update custom source.
	err = s.UpdateCatalogSource(context.Background(), custom.ID, "My Forked Catalog", custom.URL, true, 150)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// After update, custom should be first (priority 150 > 100).
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
	if len(sources) != 1 {
		t.Fatalf("expected 1 source after delete, got %d", len(sources))
	}

	// Delete nonexistent.
	if err := s.DeleteCatalogSource(context.Background(), "nonexistent"); err == nil {
		t.Error("expected error deleting nonexistent source")
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
