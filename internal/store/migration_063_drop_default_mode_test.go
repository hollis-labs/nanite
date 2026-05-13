package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigration063_DropsDefaultModeColumn is the acceptance smoke for
// CW-20260512-0115 review-round-1 column drop: after migration 063 runs the
// `agent_profiles.default_mode` column is gone and unrelated columns + rows
// survive intact.
//
// Test shape:
//  1. Open a fresh DB. Migrations 001-063 run in order.
//  2. Assert `default_mode` column is ABSENT from `agent_profiles` (proves
//     migration 063 actually executed).
//  3. Assert canonical internal rows still exist with their other columns
//     (slug, source, system_prompt) intact (proves DROP COLUMN didn't
//     scramble data or knock out the seeded rows from migration 060).
//  4. Insert a fresh row through the store layer + read it back to prove the
//     post-063 schema still round-trips through agentColumns/scanAgent.
func TestMigration063_DropsDefaultModeColumn(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "fresh.db")
	s, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	// Verify the column is gone. PRAGMA table_info returns one row per column.
	rows, err := s.DB.Query(`PRAGMA table_info(agent_profiles)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    any
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	for _, c := range cols {
		if c == "default_mode" {
			t.Errorf("agent_profiles.default_mode column still present after migration 063; got columns: %v", cols)
		}
	}

	// Sanity: the canonical migration-060 seed rows must survive the DROP
	// COLUMN. SQLite's ALTER TABLE DROP COLUMN rewrites the table in place
	// for older versions; a buggy drop would lose rows.
	canonical := []string{"default", "worker", "planner", "hint-selector"}
	for _, slug := range canonical {
		got, err := s.GetAgentBySlug(slug)
		if err != nil {
			t.Fatalf("post-063 GetAgentBySlug %q: %v (DROP COLUMN may have scrambled data)", slug, err)
		}
		if got.Source != "internal" {
			t.Errorf("canonical slug %q: Source = %q, want 'internal' after migration 063", slug, got.Source)
		}
		if got.SystemPrompt == "" {
			t.Errorf("canonical slug %q: SystemPrompt empty after migration 063 (data loss?)", slug)
		}
	}

	// Round-trip: CreateAgent + GetAgentBySlug must work against the
	// post-063 schema. This proves agentColumns + scanAgent are consistent
	// with the dropped column. (Set after the Go-side edits — if the column
	// is still in agentColumns the SELECT would error here.)
	fresh := &AgentProfile{
		Name:         "Round Trip 063",
		Slug:         "round-trip-063",
		SystemPrompt: "test agent",
		Source:       "user",
	}
	if err := s.CreateAgent(fresh); err != nil {
		t.Fatalf("CreateAgent on post-063 schema: %v", err)
	}
	got, err := s.GetAgentBySlug("round-trip-063")
	if err != nil {
		t.Fatalf("GetAgentBySlug round-trip-063: %v", err)
	}
	if got.Slug != "round-trip-063" {
		t.Errorf("round-trip slug = %q, want round-trip-063", got.Slug)
	}
	if got.Source != "user" {
		t.Errorf("round-trip source = %q, want user", got.Source)
	}
}
