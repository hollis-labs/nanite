package store

import (
	"context"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrateSkipsRecreateOnceTableIsCurrent is the regression test for
// CW-20260817: nanite-api-service crash-looped in production because
// migrations 019/066/068 (numbered 019/065/067 before goose's duplicate-051
// renumbering — see 09-adopt-goose-migrations's Work Log) each recreate
// subagent_runs from their OWN historical column/CHECK set (SQLite can't
// ALTER a CHECK constraint), and the old runner had no schema_migrations
// table, so every migration file re-ran on every boot. The first time a
// real row ever got a status only migration 066's widened CHECK permits
// ('stalled', from the reaper), the very next restart's re-run of migration
// 019 tried to copy that row into a table rebuilt with 019's original
// 7-value CHECK and failed outright, taking the whole daemon down.
//
// Before goose, the fix was a "migrate:skip-if-column-exists" directive on
// 019/066/068 (retired along with the rest of the old runner — see
// store.go). Under goose, the same guarantee comes structurally from the
// ledger itself: goose tracks each migration version as applied exactly
// once, ever, so re-running s.migrate() (simulating a restart) never
// attempts 019/066/068's rebuild SQL a second time in the first place —
// there's nothing to skip because there's nothing pending.
func TestMigrateSkipsRecreateOnceTableIsCurrent(t *testing.T) {
	s := newTestStore(t)

	// A status only permitted by migration 066's widened CHECK, not
	// migration 019's original one — this is exactly what crashed the live
	// daemon.
	_, err := s.DB.Exec(`
		INSERT INTO subagent_runs
			(id, parent_session_id, mode, status, created_at, provider,
			 retry_count, max_retries, on_fail, attempts_json, last_activity_at)
		VALUES
			('run-stalled', 'sess-stalled', 'async', 'stalled', '2026-01-01T00:00:00Z',
			 'anthropic', 2, 3, 'block', '[{"attempt":1}]', '2026-01-01T00:05:00Z')
	`)
	if err != nil {
		t.Fatalf("insert stalled row: %v", err)
	}

	assertGooseHasNothingPending(t, s)

	// Simulate a restart: migrate() re-runs goose's Up() against the same
	// database, exactly as it does on every real boot.
	if err := s.migrate(); err != nil {
		t.Fatalf("re-migrate after a real 'stalled' row exists: %v", err)
	}

	var status, provider, onFail, attemptsJSON, lastActivity string
	var retryCount, maxRetries int
	err = s.DB.QueryRow(`
		SELECT status, provider, retry_count, max_retries, on_fail, attempts_json, last_activity_at
		FROM subagent_runs WHERE id = 'run-stalled'
	`).Scan(&status, &provider, &retryCount, &maxRetries, &onFail, &attemptsJSON, &lastActivity)
	if err != nil {
		t.Fatalf("row lost across re-migrate: %v", err)
	}

	if status != "stalled" {
		t.Errorf("status: got %q, want %q", status, "stalled")
	}
	if provider != "anthropic" {
		t.Errorf("provider: got %q, want %q (a recreate-based migration that doesn't know about this column would reset it to '')", provider, "anthropic")
	}
	if retryCount != 2 {
		t.Errorf("retry_count: got %d, want 2 (would reset to 0 if 019/066 recreated the table)", retryCount)
	}
	if maxRetries != 3 {
		t.Errorf("max_retries: got %d, want 3", maxRetries)
	}
	if onFail != "block" {
		t.Errorf("on_fail: got %q, want %q (would reset to 'retry' default)", onFail, "block")
	}
	if attemptsJSON != `[{"attempt":1}]` {
		t.Errorf("attempts_json: got %q, want %q (would reset to '[]')", attemptsJSON, `[{"attempt":1}]`)
	}
	if lastActivity != "2026-01-01T00:05:00Z" {
		t.Errorf("last_activity_at: got %q, want %q (would reset to '')", lastActivity, "2026-01-01T00:05:00Z")
	}
}

// TestMigrateDoesNotReRecreateLegacyRenameMigrations is the concrete
// regression test called for by 09-adopt-goose-migrations's Done-means
// criteria: migration 043 (todos_legacy_d1) and 090 (agent_messages_legacy_089,
// numbered 089 before goose's duplicate-051 renumbering) follow the same
// rename -> recreate -> copy pattern that crashed the daemon in
// subagent_runs, but were never guarded by the old
// "migrate:skip-if-column-exists" directive (see
// docs/architecture-decision-log-2026-08-17.md §13 and
// 09-adopt-goose-migrations's Context section). Unlike 019/065/067, no
// *later* migration further widens todos.scope or agent_messages.kind past
// what 043/089 already defined, so their old-mechanism risk was narrower
// (the RENAME's "already another table" failure was swallowed, and
// CREATE TABLE IF NOT EXISTS / INSERT OR IGNORE were already accidentally
// safe on rerun) — but they still depended on that swallowed-error
// machinery for correctness, rather than a real ledger.
//
// This test seeds a todos row with scope='turn' — a value 043's widened
// CHECK permits that migration 003's original CHECK
// ('workspace','project','session') did not — and confirms that
// re-running s.migrate() (simulating a restart) leaves it completely
// untouched, because goose's ledger already has migration 43 marked
// applied and never attempts its rebuild SQL again.
func TestMigrateDoesNotReRecreateLegacyRenameMigrations(t *testing.T) {
	s := newTestStore(t)

	_, err := s.DB.Exec(`
		INSERT INTO todos (id, scope, scope_id, title, description, status, priority,
		                    created_by, created_at, updated_at)
		VALUES ('todo-turn', 'turn', 'sess-turn', 'ephemeral todo', 'seeded for regression test',
		        'pending', 'high', 'user', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("insert turn-scoped todo: %v", err)
	}

	assertGooseHasNothingPending(t, s)

	if err := s.migrate(); err != nil {
		t.Fatalf("re-migrate after a real 'turn'-scoped todo exists: %v", err)
	}

	var scope, scopeID, title, description, status, priority, createdBy string
	err = s.DB.QueryRow(`
		SELECT scope, scope_id, title, description, status, priority, created_by
		FROM todos WHERE id = 'todo-turn'
	`).Scan(&scope, &scopeID, &title, &description, &status, &priority, &createdBy)
	if err != nil {
		t.Fatalf("row lost across re-migrate (043's rebuild ran again and dropped it): %v", err)
	}
	if scope != "turn" {
		t.Errorf("scope: got %q, want %q", scope, "turn")
	}
	if title != "ephemeral todo" {
		t.Errorf("title: got %q, want %q (a re-recreate would have skipped this row via INSERT OR IGNORE against todos_legacy_d1's original, pre-043 copy — but it must never even attempt to)", title, "ephemeral todo")
	}
	if priority != "high" {
		t.Errorf("priority: got %q, want %q", priority, "high")
	}

	// The legacy rename-artifact table stays exactly where
	// 09-adopt-goose-migrations's task file requires it: present, not
	// dropped, not renamed again (that's 19-cut-legacy-rename-tables's job,
	// once it lands after this one).
	legacyExists, err := s.tableExists(context.Background(), "todos_legacy_d1")
	if err != nil {
		t.Fatalf("check todos_legacy_d1 exists: %v", err)
	}
	if !legacyExists {
		t.Error("todos_legacy_d1 no longer exists — it must stay in place until 19-cut-legacy-rename-tables drops it")
	}
}

// assertGooseHasNothingPending fails the test if goose considers any
// migration pending against s's database. Called right before a simulated
// "restart" (a second s.migrate() call) so a failure clearly means goose's
// ledger didn't already cover every migration from the first boot, rather
// than the re-migrate call happening to be a no-op for some other reason.
func assertGooseHasNothingPending(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("construct goose provider: %v", err)
	}
	pending, err := provider.HasPending(ctx)
	if err != nil {
		t.Fatalf("check goose HasPending: %v", err)
	}
	if pending {
		t.Fatal("goose has pending migrations before the simulated restart — the ledger from newTestStore's initial boot is incomplete")
	}
}
