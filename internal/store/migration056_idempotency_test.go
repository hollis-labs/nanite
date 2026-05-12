package store

import (
	"context"
	"strings"
	"testing"
)

// TestMigration056_Idempotency covers the PR #114 review concerns
// (comments 3213258308, 3213258313):
//
//  1. The UPDATE must succeed once on a row that has the pre-B5 capability
//     block, inserting the new executor-handoff bullet.
//  2. Re-running the migration on an already-updated row must be a true
//     no-op — the WHERE NOT LIKE guard skips the row, so updated_at is
//     not bumped on every boot.
//  3. The WHERE LIKE guard must align with the REPLACE needle: a row that
//     contains the meta-tools bullet but lacks the exact "\n\n## Style"
//     anchor (e.g. user-edited template) must be skipped — the migration
//     must not silently no-op the REPLACE while still bumping updated_at.
//
// This test re-applies the 056 statement directly against rows we craft,
// rather than relying on schema_migrations bookkeeping (the codebase has
// none — migrations re-run on every boot, which is exactly why
// idempotency matters).
func TestMigration056_Idempotency(t *testing.T) {
	dbPath := t.TempDir() + "/migration056test.db"
	s, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	const newBullet = "routes you to a specialized executor"
	const oldMarker = "memorise every schema."

	// Sanity: migration 027's seed already includes the new bullet
	// (PROMPT-SYNC keeps fresh-install template in sync with default.md).
	var seeded string
	if err := s.DB.QueryRow(
		`SELECT template FROM prompt_templates WHERE id = 'blt-chat-harness-001'`,
	).Scan(&seeded); err != nil {
		t.Fatalf("query seeded template: %v", err)
	}
	if !strings.Contains(seeded, newBullet) {
		t.Fatalf("fresh-install template (migration 027) missing executor-handoff bullet")
	}

	// Reset the row to the pre-B5 state (drop the new bullet) so we can
	// observe migration 056's behavior end-to-end.
	preB5 := strings.Replace(seeded,
		"\n- For multi-step flows like rendering envelope cards, you don't need to own the recipe — describe the intent and the harness routes you to a specialized executor.",
		"",
		1)
	if !strings.Contains(preB5, oldMarker) || strings.Contains(preB5, newBullet) {
		t.Fatalf("preB5 fixture not shaped correctly")
	}
	if _, err := s.DB.Exec(
		`UPDATE prompt_templates SET template = ?, updated_at = '2000-01-01 00:00:00' WHERE id = 'blt-chat-harness-001'`,
		preB5,
	); err != nil {
		t.Fatalf("seed pre-B5 state: %v", err)
	}

	// Load the migration 056 SQL from the embedded FS so the test exercises
	// the real statement, not a copy.
	mig, err := migrationsFS.ReadFile("migrations/056_chat_harness_executor_handoff_capability.sql")
	if err != nil {
		t.Fatalf("read migration 056: %v", err)
	}
	stmts := splitSQL(string(mig))
	// The file is a single SQL statement preceded by comments. Find the
	// statement that contains the UPDATE keyword (leading "--" comment
	// block is fine — SQLite skips it).
	var update056 string
	for _, st := range stmts {
		st = strings.TrimSpace(st)
		if strings.Contains(strings.ToUpper(st), "UPDATE PROMPT_TEMPLATES") {
			update056 = st
			break
		}
	}
	if update056 == "" {
		t.Fatalf("no UPDATE prompt_templates statement found in migration 056")
	}

	// First run: should rewrite the row with the new bullet and bump updated_at.
	res, err := s.DB.Exec(update056)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("first run RowsAffected = %d, want 1", n)
	}
	var afterFirst, updatedAtFirst string
	if err := s.DB.QueryRow(
		`SELECT template, updated_at FROM prompt_templates WHERE id = 'blt-chat-harness-001'`,
	).Scan(&afterFirst, &updatedAtFirst); err != nil {
		t.Fatalf("query after first: %v", err)
	}
	if !strings.Contains(afterFirst, newBullet) {
		t.Fatalf("first run failed to insert new bullet")
	}
	if updatedAtFirst == "2000-01-01 00:00:00" {
		t.Fatalf("first run did not bump updated_at")
	}

	// Second run: identical row — must be a no-op (NOT LIKE guard skips).
	res, err = s.DB.Exec(update056)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Fatalf("second run RowsAffected = %d, want 0 (idempotent re-run must not touch row)", n)
	}
	var updatedAtSecond string
	if err := s.DB.QueryRow(
		`SELECT updated_at FROM prompt_templates WHERE id = 'blt-chat-harness-001'`,
	).Scan(&updatedAtSecond); err != nil {
		t.Fatalf("query after second: %v", err)
	}
	if updatedAtSecond != updatedAtFirst {
		t.Fatalf("second run bumped updated_at (was %q, now %q) — re-run is not a true no-op",
			updatedAtFirst, updatedAtSecond)
	}

	// Resilience check: simulate a user-edited template that has the
	// meta-tools bullet but NOT the exact "\n\n## Style" anchor that REPLACE
	// expects. The previous form (LIKE '%memorise every schema.%' alone)
	// would match this row, REPLACE would silently no-op, and updated_at
	// would still get bumped on every boot. The fix is the WHERE clause
	// LIKE-matching the EXACT needle.
	const edited = `## Capability

- You have **meta-tools** for discovery (tool_describe), pre-flight validation (tool_validate), and learning capture (lesson_capture). Reach for them when a tool's contract is unfamiliar or after a call fails — you don't have to memorise every schema.
- A user-added bullet that breaks the "\n\n## Style" anchor.

## Style
`
	if _, err := s.DB.Exec(
		`UPDATE prompt_templates SET template = ?, updated_at = '1999-12-31 00:00:00' WHERE id = 'blt-chat-harness-001'`,
		edited,
	); err != nil {
		t.Fatalf("seed edited state: %v", err)
	}

	res, err = s.DB.Exec(update056)
	if err != nil {
		t.Fatalf("edited-row run: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Fatalf("edited-row run RowsAffected = %d, want 0 — WHERE guard must skip rows whose REPLACE needle is absent (no perpetual updated_at writes)", n)
	}
	var updatedAtEdited string
	if err := s.DB.QueryRow(
		`SELECT updated_at FROM prompt_templates WHERE id = 'blt-chat-harness-001'`,
	).Scan(&updatedAtEdited); err != nil {
		t.Fatalf("query after edited run: %v", err)
	}
	if updatedAtEdited != "1999-12-31 00:00:00" {
		t.Fatalf("edited-row run bumped updated_at to %q — perpetual-write bug regressed", updatedAtEdited)
	}
}
