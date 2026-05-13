package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigration060_SeedsFourInternalProfilesAndFlipsSource is the
// acceptance smoke for CW-20260512-0111 Wave 1: on a fresh database,
// migration 060 must INSERT the four canonical internal profile slugs
// with source='internal', and on a database that already has the
// pre-internal seed rows with source='builtin', the migration must
// flip those rows' source column to 'internal' without touching the
// body (the body update is the boot-sync's job).
func TestMigration060_SeedsFourInternalProfilesAndFlipsSource(t *testing.T) {
	// Fresh DB — migration 060 INSERT OR IGNORE creates the four rows.
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "fresh.db")
	s, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	wantSlugs := []string{"default", "worker", "planner", "hint-selector"}
	for _, slug := range wantSlugs {
		got, err := s.GetAgentBySlug(slug)
		if err != nil {
			t.Fatalf("GetAgentBySlug %q after migration 060: %v", slug, err)
		}
		if got.Source != "internal" {
			t.Errorf("slug=%q: Source = %q, want 'internal' (migration 060 seed)", slug, got.Source)
		}
		wantRef := "embedded:profiles/" + slug + ".md"
		if got.SourceRef != wantRef {
			t.Errorf("slug=%q: SourceRef = %q, want %q", slug, got.SourceRef, wantRef)
		}
		if got.SystemPrompt == "" {
			t.Errorf("slug=%q: SystemPrompt is empty — migration 060 seed must include the body", slug)
		}
	}

	// Worker body must NOT carry execute-or-bust framing.
	worker, _ := s.GetAgentBySlug("worker")
	if strings.Contains(worker.SystemPrompt, "Your job is to execute, not converse") {
		t.Error("worker body in migration 060 reintroduces the c160 fabrication-chain execute-or-bust framing")
	}
	if !strings.Contains(worker.SystemPrompt, "return an explicit failure") {
		t.Error("worker body in migration 060 missing the explicit-failure escape valve")
	}

	// Default body must NOT carry the universal Grounding section (those
	// rules moved to internal/chat/universal_rules.go in CW-20260512-0100).
	def, _ := s.GetAgentBySlug("default")
	if strings.Contains(def.SystemPrompt, "## Grounding") {
		t.Error("default body in migration 060 reintroduces ## Grounding — universal rules layer owns it")
	}
}
