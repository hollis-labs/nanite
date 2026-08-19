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
//
// todos_legacy_d1 itself is now dropped by migration 095
// (095_drop_legacy_rename_tables.sql,
// TASKS/phase-0/19-cut-legacy-rename-tables.md), which every full
// migration run applies — including newTestStore's initial boot, before
// this test's own seed/re-migrate steps even happen. See
// TestMigrateDropsLegacyRenameTables below for that coverage. This test's
// own concern is narrower and still valid post-095: does a second
// s.migrate() re-attempt 043's rebuild SQL against a live todos row that
// only 043's widened CHECK permits. It does not — and correspondingly,
// todos_legacy_d1 stays gone (dropped once, by 095, on the very first
// boot) rather than staying present, which is the inverse of what this
// test checked before 19 landed.
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

	// 19-cut-legacy-rename-tables has landed (migration 095): the legacy
	// rename-artifact table is gone by the time newTestStore's initial boot
	// finishes, and this second s.migrate() call must not resurrect it.
	legacyExists, err := s.tableExists(context.Background(), "todos_legacy_d1")
	if err != nil {
		t.Fatalf("check todos_legacy_d1 exists: %v", err)
	}
	if legacyExists {
		t.Error("todos_legacy_d1 still exists — migration 095 (19-cut-legacy-rename-tables) should have dropped it on the first boot, and it must stay dropped across a re-migrate")
	}
}

// TestMigrateDropsLegacyRenameTables is the concrete regression test for
// 19-cut-legacy-rename-tables.md (TASKS/phase-0/19-cut-legacy-rename-tables.md):
// migration 095 (095_drop_legacy_rename_tables.sql) drops
// agent_messages_legacy_089 and todos_legacy_d1 — the two rename-artifact
// tables 09-adopt-goose-migrations deliberately left in place (see this
// file's other tests) once goose's real ledger made the swallowed-rename
// idempotency trick those tables existed for structurally unnecessary.
//
// Confirms all of this task's Done-means criteria in one place:
//   - both legacy tables are gone after a normal migration run (exercised
//     via newTestStore, which always boots through every embedded
//     migration, including 095, from an empty database)
//   - the live agent_messages/todos tables are unaffected: same columns,
//     same row counts (zero, on a fresh store) before and after
//   - a second s.migrate() (simulating a restart) is a clean no-op — goose
//     has nothing pending, and neither legacy table comes back
func TestMigrateDropsLegacyRenameTables(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, table := range []string{"agent_messages_legacy_089", "todos_legacy_d1"} {
		exists, err := s.tableExists(ctx, table)
		if err != nil {
			t.Fatalf("check %s exists: %v", table, err)
		}
		if exists {
			t.Errorf("%s still exists after a fresh full migration run — migration 095 should have dropped it", table)
		}
	}

	// Live tables must still be present and queryable with their real
	// schema (same columns 090/043 defined for them) — dropping the legacy
	// rename artifacts must not touch the tables they were renamed away
	// from.
	var agentMessagesCount, todosCount int
	if err := s.DB.QueryRow(`SELECT count(*) FROM agent_messages`).Scan(&agentMessagesCount); err != nil {
		t.Fatalf("live agent_messages table unusable after 095: %v", err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM todos`).Scan(&todosCount); err != nil {
		t.Fatalf("live todos table unusable after 095: %v", err)
	}
	if agentMessagesCount != 0 {
		t.Errorf("agent_messages row count: got %d, want 0 on a fresh store", agentMessagesCount)
	}
	if todosCount != 0 {
		t.Errorf("todos row count: got %d, want 0 on a fresh store", todosCount)
	}
	// kind/channel columns only exist on the post-090 live agent_messages
	// schema; project_id only exists on the post-043 live todos schema.
	// Querying them confirms the live tables kept their real (not
	// legacy-renamed) shape.
	if _, err := s.DB.Exec(`INSERT INTO agent_messages (id, from_session_id, from_agent_id, to_session_id, to_agent_id, body, kind, channel) VALUES ('m1','s1','a1','s2','a2','hi','subagent_result','chat')`); err != nil {
		t.Fatalf("insert into live agent_messages using post-090 columns: %v", err)
	}
	if _, err := s.DB.Exec(`INSERT INTO todos (id, scope, scope_id, project_id, title) VALUES ('t1','turn','sess-1','proj-1','a todo')`); err != nil {
		t.Fatalf("insert into live todos using post-043 columns: %v", err)
	}

	assertGooseHasNothingPending(t, s)

	if err := s.migrate(); err != nil {
		t.Fatalf("re-migrate after legacy tables already dropped: %v", err)
	}

	for _, table := range []string{"agent_messages_legacy_089", "todos_legacy_d1"} {
		exists, err := s.tableExists(ctx, table)
		if err != nil {
			t.Fatalf("check %s exists after re-migrate: %v", table, err)
		}
		if exists {
			t.Errorf("%s exists after a re-migrate — migration 095 must not be reapplied/resurrect the table", table)
		}
	}

	var m1Body, t1Title string
	if err := s.DB.QueryRow(`SELECT body FROM agent_messages WHERE id = 'm1'`).Scan(&m1Body); err != nil {
		t.Fatalf("seeded agent_messages row lost across re-migrate: %v", err)
	}
	if m1Body != "hi" {
		t.Errorf("agent_messages row: got body %q, want %q", m1Body, "hi")
	}
	if err := s.DB.QueryRow(`SELECT title FROM todos WHERE id = 't1'`).Scan(&t1Title); err != nil {
		t.Fatalf("seeded todos row lost across re-migrate: %v", err)
	}
	if t1Title != "a todo" {
		t.Errorf("todos row: got title %q, want %q", t1Title, "a todo")
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
