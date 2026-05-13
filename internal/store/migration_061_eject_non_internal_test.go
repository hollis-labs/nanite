package store

import (
	"context"
	"path/filepath"
	"sort"
	"testing"
)

// TestMigration061_EjectsNonInternalProfiles is the acceptance smoke for
// CW-20260512-0112 Wave 2: every agent_profiles row whose source != 'internal'
// is wiped, and the four canonical internal rows seeded by migration 060
// (plus any additional source='internal' rows) survive.
//
// Test shape:
//   1. Open a fresh DB. Migrations 001-061 run in order; migration 060 seeds
//      the four canonical internal slugs (default, worker, planner,
//      hint-selector). Migration 061 runs against the fresh state and is a
//      no-op (no non-internal rows exist).
//   2. INSERT fixture rows with non-internal sources (auto, nanite, user,
//      claude) plus an additional source='internal' row (to confirm that
//      MULTIPLE internal rows are preserved, not just the canonical four).
//   3. Re-execute the migration 061 DELETE against the now-populated DB to
//      simulate what happens on the next boot.
//   4. Assert: total row count == 5 (4 canonical seeded + 1 additional
//      internal), every surviving row has source='internal', and none of
//      the fixture-inserted non-internal slugs survive.
func TestMigration061_EjectsNonInternalProfiles(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "fresh.db")
	s, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	// Sanity: migration 060 should have seeded the four canonical rows,
	// and the (just-run) migration 061 should not have touched them.
	canonical := []string{"default", "worker", "planner", "hint-selector"}
	for _, slug := range canonical {
		got, err := s.GetAgentBySlug(slug)
		if err != nil {
			t.Fatalf("post-migration GetAgentBySlug %q: %v", slug, err)
		}
		if got.Source != "internal" {
			t.Errorf("canonical slug %q: Source = %q, want 'internal'", slug, got.Source)
		}
	}

	// Insert fixture rows representing the four classes the live DB carries
	// pre-W2 (auto stubs, nanite project agents, user-authored UI rows,
	// claude-imported rows) plus an additional source='internal' row.
	// Only minimal columns are populated — table defaults handle the rest.
	fixtures := []struct {
		id, name, slug, body, source string
	}{
		{"fix-auto-1", "Analyst", "analyst-fix", "", "auto"},
		{"fix-auto-2", "Backend Stub", "backend-fix", "", "auto"},
		{"fix-nanite-1", "Nanite Backend", "nanite-backend-fix", "project agent body", "nanite"},
		{"fix-nanite-2", "Nanite Frontend", "nanite-frontend-fix", "project agent body", "nanite"},
		{"fix-user-1", "User Agent", "user-fix", "user-authored body", "user"},
		{"fix-claude-1", "Claude Agent", "claude-fix", "claude body", "claude"},
		{"fix-internal-extra", "Extra Internal", "extra-internal-fix", "extra internal body", "internal"},
	}
	for _, f := range fixtures {
		if _, err := s.DB.Exec(
			`INSERT INTO agent_profiles (id, name, slug, system_prompt, source) VALUES (?, ?, ?, ?, ?)`,
			f.id, f.name, f.slug, f.body, f.source,
		); err != nil {
			t.Fatalf("insert fixture %q: %v", f.slug, err)
		}
	}

	// Confirm pre-DELETE row count: 4 canonical + 7 fixtures = 11.
	var pre int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM agent_profiles`).Scan(&pre); err != nil {
		t.Fatalf("pre-count: %v", err)
	}
	if pre != 11 {
		t.Fatalf("pre-DELETE row count = %d, want 11 (4 canonical + 7 fixtures)", pre)
	}

	// Execute migration 061's DELETE manually. This simulates the second
	// boot — migration 061 re-runs on every Nanite start, and on the boot
	// after fixtures appear (e.g. from the dispatcher writing rows during
	// a previous session) the DELETE wipes the non-internal subset.
	if _, err := s.DB.Exec(`DELETE FROM agent_profiles WHERE source != 'internal'`); err != nil {
		t.Fatalf("simulate migration 061 DELETE: %v", err)
	}

	// Post-DELETE: 4 canonical + 1 additional internal = 5. Every row's
	// source must be 'internal'.
	var post int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM agent_profiles`).Scan(&post); err != nil {
		t.Fatalf("post-count: %v", err)
	}
	if post != 5 {
		t.Errorf("post-DELETE row count = %d, want 5 (4 canonical + 1 extra internal)", post)
	}

	rows, err := s.DB.Query(`SELECT slug, source FROM agent_profiles ORDER BY slug`)
	if err != nil {
		t.Fatalf("post-DELETE query: %v", err)
	}
	defer rows.Close()
	var gotSlugs []string
	for rows.Next() {
		var slug, source string
		if err := rows.Scan(&slug, &source); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if source != "internal" {
			t.Errorf("surviving row slug=%q has source=%q, want 'internal'", slug, source)
		}
		gotSlugs = append(gotSlugs, slug)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	wantSlugs := []string{"default", "extra-internal-fix", "hint-selector", "planner", "worker"}
	sort.Strings(wantSlugs)
	if len(gotSlugs) != len(wantSlugs) {
		t.Fatalf("surviving slugs = %v, want %v", gotSlugs, wantSlugs)
	}
	for i, want := range wantSlugs {
		if gotSlugs[i] != want {
			t.Errorf("surviving slugs[%d] = %q, want %q", i, gotSlugs[i], want)
		}
	}

	// Re-run the DELETE: idempotency check. A second pass must be a no-op.
	if _, err := s.DB.Exec(`DELETE FROM agent_profiles WHERE source != 'internal'`); err != nil {
		t.Fatalf("re-run DELETE: %v", err)
	}
	var post2 int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM agent_profiles`).Scan(&post2); err != nil {
		t.Fatalf("post2-count: %v", err)
	}
	if post2 != post {
		t.Errorf("idempotency: second DELETE changed count from %d to %d", post, post2)
	}
}
