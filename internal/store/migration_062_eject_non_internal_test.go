package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigration062_EjectsNonInternalProfiles is the acceptance smoke for
// CW-20260512-0112 Wave 2 originally wiped every agent_profiles row whose
// source != 'internal'. It was later amended to keep historical
// source='project' rows while ejecting every other ambiguous source. Today
// those rows are ordinary database records; project provenance does not grant
// a file authority path.
//
// Test shape:
//  1. Open a fresh DB. Migrations 001-062 run in order; migration 061 seeds
//     the canonical internal slugs (default, worker, planner, hint-selector,
//     plus whatever future migrations layer on). Migration 062 runs against
//     the fresh state and is a no-op (no non-internal rows exist).
//  2. Capture `baselineCount` from the post-migration state (BEFORE any
//     fixtures land) so this test stays robust as future migrations seed
//     additional internal profiles (e.g. CW-20260512-0113 / W4 will add
//     ~7 more internal profile rows).
//  3. INSERT fixture rows with mixed sources (auto, nanite, user, claude)
//     plus one additional source='internal' row and two source='project'
//     rows to confirm that both keep-list classes survive.
//  4. Re-execute the migration 062 DELETE against the now-populated DB to
//     simulate what happens on the next boot.
//  5. Assert: total row count == baselineCount + 3 (the extra internal
//     fixture plus the 2 project fixtures survive, the remaining 4
//     ambiguous fixtures get wiped), every surviving row has source in the
//     keep-list, and the expected slugs survive.
func TestMigration062_EjectsNonInternalProfiles(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "fresh.db")
	s, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close(context.

		// Capture the post-migration baseline BEFORE any fixtures land. Future
		// migrations may seed additional internal profiles; this baseline floats
		// with them so the test does not need updates when W4 (or later) adds
		// rows to the migration-061 seed.
		Background())

	var baselineCount int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM agent_profiles`).Scan(&baselineCount); err != nil {
		t.Fatalf("baseline count: %v", err)
	}
	if baselineCount < 4 {
		t.Fatalf("baseline count = %d, want >= 4 (migration 061 seeds at least four canonical rows)", baselineCount)
	}

	// Sanity: migration 061 should have seeded the four canonical rows,
	// and the (just-run) migration 062 should not have touched them.
	canonical := []string{"default", "worker", "planner", "hint-selector"}
	for _, slug := range canonical {
		got, err := s.GetAgentBySlug(context.Background(), slug)
		if err != nil {
			t.Fatalf("post-migration GetAgentBySlug %q: %v", slug, err)
		}
		if got.Source != "internal" {
			t.Errorf("canonical slug %q: Source = %q, want 'internal'", slug, got.Source)
		}
	}

	// Insert fixture rows representing ambiguous legacy sources plus the two
	// keep-list classes (internal and operator-managed project provenance).
	// Only minimal columns are populated — table defaults handle the rest.
	fixtures := []struct {
		id, name, slug, body, source string
	}{
		{"fix-auto-1", "Analyst", "analyst-fix", "", "auto"},
		{"fix-auto-2", "Backend Stub", "backend-fix", "", "auto"},
		{"fix-nanite-1", "Nanite Backend", "nanite-backend-fix", "project agent body", "nanite"},
		{"fix-project-1", "Project Advisor", "project-advisor-fix", "project agent body", "project"},
		{"fix-project-2", "Project Writer", "project-writer-fix", "project agent body", "project"},
		{"fix-user-1", "User Agent", "user-fix", "user-authored body", "user"},
		{"fix-internal-extra", "Extra Internal", "extra-internal-fix", "extra internal body", "internal"},
	}
	const fixtureCount = 7
	for _, f := range fixtures {
		if _, err := s.DB.Exec(
			`INSERT INTO agent_profiles (id, name, slug, system_prompt, source) VALUES (?, ?, ?, ?, ?)`,
			f.id, f.name, f.slug, f.body, f.source,
		); err != nil {
			t.Fatalf("insert fixture %q: %v", f.slug, err)
		}
	}

	// Confirm pre-DELETE row count: baseline + 7 fixtures.
	var pre int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM agent_profiles`).Scan(&pre); err != nil {
		t.Fatalf("pre-count: %v", err)
	}
	if pre != baselineCount+fixtureCount {
		t.Fatalf("pre-DELETE row count = %d, want %d (baseline %d + %d fixtures)", pre, baselineCount+fixtureCount, baselineCount, fixtureCount)
	}

	// Execute migration 062's DELETE manually, independent of goose (whose
	// real ledger means the migration itself only ever runs once — see
	// 09-adopt-goose-migrations). This still verifies the statement's own
	// idempotency and correctness against a DB that has picked up
	// non-internal fixture rows after the migration already ran once.
	if _, err := s.DB.Exec(`DELETE FROM agent_profiles WHERE source NOT IN ('internal', 'project')`); err != nil {
		t.Fatalf("simulate migration 062 DELETE: %v", err)
	}

	// Post-DELETE: baseline + 1 additional internal fixture + 2 project
	// fixtures survive; the remaining 4 ambiguous fixtures got wiped.
	var post int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM agent_profiles`).Scan(&post); err != nil {
		t.Fatalf("post-count: %v", err)
	}
	if post != baselineCount+3 {
		t.Errorf("post-DELETE row count = %d, want %d (baseline %d + 3 keep-list fixtures)", post, baselineCount+3, baselineCount)
	}

	rows, err := s.DB.Query(`SELECT slug, source FROM agent_profiles`)
	if err != nil {
		t.Fatalf("post-DELETE query: %v", err)
	}
	defer rows.Close()
	gotSlugs := map[string]bool{}
	for rows.Next() {
		var slug, source string
		if err := rows.Scan(&slug, &source); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if source != "internal" && source != "project" {
			t.Errorf("surviving row slug=%q has source=%q, want keep-list source", slug, source)
		}
		gotSlugs[slug] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	// Required presence: canonical internal slugs + the explicit keep-list fixtures.
	wantPresent := []string{
		"default",
		"worker",
		"planner",
		"hint-selector",
		"extra-internal-fix",
		"project-advisor-fix",
		"project-writer-fix",
	}
	for _, slug := range wantPresent {
		if !gotSlugs[slug] {
			t.Errorf("expected surviving slug %q missing from result", slug)
		}
	}

	// Required absence: every ambiguous-source fixture slug must have been wiped.
	wantAbsent := []string{"analyst-fix", "backend-fix", "nanite-backend-fix", "user-fix"}
	for _, slug := range wantAbsent {
		if gotSlugs[slug] {
			t.Errorf("non-internal fixture slug %q survived DELETE; expected wipe", slug)
		}
	}

	// Re-run the DELETE: idempotency check. A second pass must be a no-op.
	if _, err := s.DB.Exec(`DELETE FROM agent_profiles WHERE source NOT IN ('internal', 'project')`); err != nil {
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
