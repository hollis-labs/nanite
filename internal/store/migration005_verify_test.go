package store

import (
	"database/sql"
	"testing"
)

func TestMigration005_MessagingSessionScoping(t *testing.T) {
	dbPath := t.TempDir() + "/migration005test.db"
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("store.New failed: %v", err)
	}
	defer s.Close()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// Verify old columns are gone. Table was renamed from a2a_messages to
	// agent_messages by migration 018, so columns are inspected on the new
	// name here — the session-scoping changes from migration 005 persist
	// through the rename.
	agentMsgCols := tableColumns(t, db, "agent_messages")
	for _, col := range []string{"from_agent", "to_agent"} {
		if agentMsgCols[col] {
			t.Errorf("column %q should have been dropped from agent_messages", col)
		}
	}

	// Verify new session-scoped columns exist.
	for _, col := range []string{"from_session_id", "from_agent_id", "to_session_id", "to_agent_id"} {
		if !agentMsgCols[col] {
			t.Errorf("expected column %q in agent_messages, not found", col)
		}
	}

	// Verify session_handoffs table exists.
	handoffCols := tableColumns(t, db, "session_handoffs")
	for _, col := range []string{"id", "session_id", "from_agent_id", "to_agent_id", "requested_by", "status", "requested_at", "approved_at", "approved_by_user", "context_message_count", "notes"} {
		if !handoffCols[col] {
			t.Errorf("expected column %q in session_handoffs, not found", col)
		}
	}

	t.Logf("agent_messages columns: %v", keys(agentMsgCols))
	t.Logf("session_handoffs columns: %v", keys(handoffCols))
}

func tableColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		cols[name] = true
	}
	return cols
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

