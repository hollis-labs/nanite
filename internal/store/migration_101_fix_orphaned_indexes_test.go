package store

import (
	"context"
	"testing"
	"time"
)

// wantIndexes101 are the 7 real indexes 101_fix_orphaned_indexes_agent_messages_todos.sql
// creates on the live agent_messages/todos tables, per
// TASKS/phase-0/35-fix-orphaned-indexes-agent-messages-todos.md.
var wantIndexes101 = []struct {
	table string
	name  string
}{
	{"agent_messages", "idx_agent_messages_thread"},
	{"agent_messages", "idx_agent_messages_to_session_agent"},
	{"agent_messages", "idx_agent_messages_from_session_agent"},
	{"agent_messages", "idx_agent_messages_channel"},
	{"todos", "idx_todos_scope"},
	{"todos", "idx_todos_parent"},
	{"todos", "idx_todos_status"},
}

// TestMigrateCreatesOrphanedIndexesAndTrigger is the concrete regression
// test for 35-fix-orphaned-indexes-agent-messages-todos.md
// (TASKS/phase-0/35-fix-orphaned-indexes-agent-messages-todos.md): 4
// indexes on agent_messages and 3 indexes + 1 trigger on todos were
// silently orphaned for years because SQLite's `ALTER TABLE ... RENAME TO`
// carried their names forward onto the legacy rename-artifact tables
// (agent_messages_legacy_089, todos_legacy_d1), so the original
// `CREATE INDEX/TRIGGER IF NOT EXISTS` statements against the live tables
// silently no-op'd. Migration 095 (19-cut-legacy-rename-tables) drops both
// legacy tables, freeing the names; migration 101 recreates the real
// objects against the live tables.
//
// Confirms this task's Done-means criteria: all 7 indexes + 1 trigger exist
// on live agent_messages/todos (direct schema query, not just migration-file
// presence), the live tables are otherwise unaffected, and a second
// s.migrate() (simulated restart) is a clean no-op.
func TestMigrateCreatesOrphanedIndexesAndTrigger(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, want := range wantIndexes101 {
		exists, err := indexExists(ctx, s, want.name)
		if err != nil {
			t.Fatalf("check index %s exists: %v", want.name, err)
		}
		if !exists {
			t.Errorf("index %s on %s does not exist after a fresh full migration run — migration 101 should have created it", want.name, want.table)
		}
	}

	trgExists, err := triggerExists(ctx, s, "trg_todos_updated_at")
	if err != nil {
		t.Fatalf("check trigger trg_todos_updated_at exists: %v", err)
	}
	if !trgExists {
		t.Error("trigger trg_todos_updated_at does not exist after a fresh full migration run — migration 101 should have created it")
	}

	// Live tables must still be queryable and empty on a fresh store —
	// creating indexes/a trigger must not touch existing data.
	var agentMessagesCount, todosCount int
	if err := s.DB.QueryRow(`SELECT count(*) FROM agent_messages`).Scan(&agentMessagesCount); err != nil {
		t.Fatalf("live agent_messages table unusable after 101: %v", err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM todos`).Scan(&todosCount); err != nil {
		t.Fatalf("live todos table unusable after 101: %v", err)
	}
	if agentMessagesCount != 0 {
		t.Errorf("agent_messages row count: got %d, want 0 on a fresh store", agentMessagesCount)
	}
	if todosCount != 0 {
		t.Errorf("todos row count: got %d, want 0 on a fresh store", todosCount)
	}

	assertGooseHasNothingPending(t, s)

	if err := s.migrate(context.Background()); err != nil {
		t.Fatalf("re-migrate after indexes/trigger already created: %v", err)
	}

	for _, want := range wantIndexes101 {
		exists, err := indexExists(ctx, s, want.name)
		if err != nil {
			t.Fatalf("check index %s exists after re-migrate: %v", want.name, err)
		}
		if !exists {
			t.Errorf("index %s missing after a re-migrate — migration 101 must not be lost on restart", want.name)
		}
	}
	trgExists, err = triggerExists(ctx, s, "trg_todos_updated_at")
	if err != nil {
		t.Fatalf("check trigger exists after re-migrate: %v", err)
	}
	if !trgExists {
		t.Error("trg_todos_updated_at missing after a re-migrate")
	}
}

// TestTrgTodosUpdatedAt_UsesRFC3339Format is the regression test for this
// task's correction to trg_todos_updated_at's originally-defined body: the
// pre-orphan definition (003_todos_and_plans.sql) used SQLite's default
// `CURRENT_TIMESTAMP` (space-separated "YYYY-MM-DD HH:MM:SS"), but every
// live write path in internal/store/todos.go (UpdateTodo, UpdateTodoScope,
// CreateTodo) already writes RFC3339 ("...T...Z") explicitly. Replaying the
// original body verbatim would have the trigger's own AFTER-UPDATE write
// silently overwrite the app's RFC3339 value with a different format on
// every single todo update. 101_fix_orphaned_indexes_agent_messages_todos.sql
// recreates the trigger with `strftime('%Y-%m-%dT%H:%M:%SZ','now')` instead,
// matching UpdateTodoScope's own convention, so it stays a same-format
// backstop rather than a source of drift. This test updates a todos row via
// raw SQL (bypassing the Go UpdateTodo path entirely, so only the trigger
// can be setting updated_at) and confirms the result parses as RFC3339.
func TestTrgTodosUpdatedAt_UsesRFC3339Format(t *testing.T) {
	s := newTestStore(t)

	_, err := s.DB.Exec(`
		INSERT INTO todos (id, scope, scope_id, title, description, status, priority,
		                    created_by, created_at, updated_at)
		VALUES ('todo-trg', 'session', 'sess-trg', 'trigger test', '', 'pending', 'medium',
		        'user', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("seed todo: %v", err)
	}

	// Update a column the trigger's own body does not touch, via raw SQL —
	// exercises AFTER UPDATE without going through the Go UpdateTodo path
	// (which would also set updated_at itself, masking whether the trigger
	// fired at all).
	if _, err := s.DB.Exec(`UPDATE todos SET priority = 'high' WHERE id = 'todo-trg'`); err != nil {
		t.Fatalf("update todo priority: %v", err)
	}

	var updatedAt string
	if err := s.DB.QueryRow(`SELECT updated_at FROM todos WHERE id = 'todo-trg'`).Scan(&updatedAt); err != nil {
		t.Fatalf("read back updated_at: %v", err)
	}
	if updatedAt == "2026-01-01T00:00:00Z" {
		t.Fatal("updated_at unchanged after UPDATE — trg_todos_updated_at did not fire")
	}
	if _, err := time.Parse(time.RFC3339, updatedAt); err != nil {
		t.Errorf("updated_at %q set by trg_todos_updated_at is not RFC3339 (would mismatch every app write path's format): %v", updatedAt, err)
	}
}

func indexExists(ctx context.Context, s *Store, name string) (bool, error) {
	var count int
	err := s.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?`, name,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func triggerExists(ctx context.Context, s *Store, name string) (bool, error) {
	var count int
	err := s.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
