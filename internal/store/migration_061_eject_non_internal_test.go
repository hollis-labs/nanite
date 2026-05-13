package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigration061_EjectsNonInternalProfiles is the acceptance smoke for
// CW-20260512-0112 Wave 2: every agent_profiles row whose source != 'internal'
// is wiped, and the canonical internal rows seeded by migration 060 (plus any
// additional source='internal' rows) survive.
//
// Test shape:
//   1. Open a fresh DB. Migrations 001-061 run in order; migration 060 seeds
//      the canonical internal slugs (default, worker, planner, hint-selector,
//      plus whatever future migrations layer on). Migration 061 runs against
//      the fresh state and is a no-op (no non-internal rows exist).
//   2. Capture `baselineCount` from the post-migration state (BEFORE any
//      fixtures land) so this test stays robust as future migrations seed
//      additional internal profiles (e.g. CW-20260512-0113 / W4 will add
//      ~7 more internal profile rows).
//   3. INSERT fixture rows with non-internal sources (auto, nanite, user,
//      claude) plus an additional source='internal' row (to confirm that
//      MULTIPLE internal rows are preserved, not just the canonical set).
//   4. Re-execute the migration 061 DELETE against the now-populated DB to
//      simulate what happens on the next boot.
//   5. Assert: total row count == baselineCount + 1 (the extra internal
//      fixture survives, the 6 non-internal fixtures get wiped), every
//      surviving row has source='internal', the canonical slugs and the
//      extra-internal fixture are present, and none of the non-internal
//      fixture slugs survive.
func TestMigration061_EjectsNonInternalProfiles(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "fresh.db")
	s, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	// Capture the post-migration baseline BEFORE any fixtures land. Future
	// migrations may seed additional internal profiles; this baseline floats
	// with them so the test does not need updates when W4 (or later) adds
	// rows to the migration-060 seed.
	var baselineCount int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM agent_profiles`).Scan(&baselineCount); err != nil {
		t.Fatalf("baseline count: %v", err)
	}
	if baselineCount < 4 {
		t.Fatalf("baseline count = %d, want >= 4 (migration 060 seeds at least four canonical rows)", baselineCount)
	}

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

	// Execute migration 061's DELETE manually. This simulates the second
	// boot — migration 061 re-runs on every Nanite start, and on the boot
	// after fixtures appear (e.g. from the dispatcher writing rows during
	// a previous session) the DELETE wipes the non-internal subset.
	if _, err := s.DB.Exec(`DELETE FROM agent_profiles WHERE source != 'internal'`); err != nil {
		t.Fatalf("simulate migration 061 DELETE: %v", err)
	}

	// Post-DELETE: baseline + 1 additional internal fixture survives; the
	// 6 non-internal fixtures got wiped. Every row's source must be 'internal'.
	var post int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM agent_profiles`).Scan(&post); err != nil {
		t.Fatalf("post-count: %v", err)
	}
	if post != baselineCount+1 {
		t.Errorf("post-DELETE row count = %d, want %d (baseline %d + 1 extra-internal fixture)", post, baselineCount+1, baselineCount)
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
		if source != "internal" {
			t.Errorf("surviving row slug=%q has source=%q, want 'internal'", slug, source)
		}
		gotSlugs[slug] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	// Required presence: canonical internal slugs + the extra-internal fixture.
	wantPresent := []string{"default", "worker", "planner", "hint-selector", "extra-internal-fix"}
	for _, slug := range wantPresent {
		if !gotSlugs[slug] {
			t.Errorf("expected surviving slug %q missing from result", slug)
		}
	}

	// Required absence: every non-internal fixture slug must have been wiped.
	wantAbsent := []string{"analyst-fix", "backend-fix", "nanite-backend-fix", "nanite-frontend-fix", "user-fix", "claude-fix"}
	for _, slug := range wantAbsent {
		if gotSlugs[slug] {
			t.Errorf("non-internal fixture slug %q survived DELETE; expected wipe", slug)
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
