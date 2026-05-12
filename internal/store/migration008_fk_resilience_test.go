package store

import (
	"context"
	"testing"
)

// TestMigration008_SurvivesBookmarkFK verifies that re-running migrations on a
// DB with a bookmark row FK-referencing a message does NOT fail on migration
// 008's `DROP TABLE messages` step.
//
// Regression for CW-20260417-0477: migration 008 wraps its rebuild in
// `BEGIN; ... END;` with `PRAGMA foreign_keys = OFF` inside the transaction.
// Per SQLite docs, PRAGMA foreign_keys is a no-op inside an open transaction,
// so FKs stay enforced. `DROP TABLE messages` performs an implicit
// `DELETE FROM messages`, which trips the FK from `bookmarks.message_id`.
func TestMigration008_SurvivesBookmarkFK(t *testing.T) {
	s := newTestStore(t)

	// Seed a workspace → session → message → bookmark chain.
	mustExec(t, s, `INSERT INTO workspaces (id, name) VALUES ('w1', 'ws')`)
	mustExec(t, s, `INSERT INTO sessions (id, workspace_id, title, short_code) VALUES ('s1', 'w1', 't', 'sc1')`)
	mustExec(t, s, `INSERT INTO messages (id, session_id, role, content) VALUES ('m1','s1','user','hi')`)
	mustExec(t, s, `INSERT INTO bookmarks (id, message_id, session_id) VALUES ('b1','m1','s1')`)

	// Re-run migrate() — simulates the next boot. Every boot re-runs all
	// migrations because there is no schema_migrations table.
	if err := s.migrate(); err != nil {
		t.Fatalf("migrate with bookmark→message FK should succeed, got: %v", err)
	}

	// The bookmark→message chain must still be intact post-migrate.
	var count int
	if err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM bookmarks b JOIN messages m ON m.id = b.message_id`,
	).Scan(&count); err != nil {
		t.Fatalf("query post-migrate: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected bookmark→message chain preserved, got count=%d", count)
	}
}

// TestMigration008_SurvivesOrphanMessage verifies that re-running migrations on
// a DB with an orphan message (session_id pointing to a deleted session) does
// NOT fail on migration 008's `INSERT INTO messages_new SELECT ... FROM messages`
// step.
//
// Regression for CW-20260417-0477: the INSERT copies the orphan row into the
// new table, whose `session_id NOT NULL REFERENCES sessions(id)` rejects it
// because FKs are still ON (PRAGMA no-op inside BEGIN). This is the specific
// scenario that crashed the backend at 2026-04-17 17:29 after a GUI
// session-delete left orphan message rows (separate bug — CW-20260417-0481).
func TestMigration008_SurvivesOrphanMessage(t *testing.T) {
	s := newTestStore(t)

	// Seed workspace, session, message.
	mustExec(t, s, `INSERT INTO workspaces (id, name) VALUES ('w1', 'ws')`)
	mustExec(t, s, `INSERT INTO sessions (id, workspace_id, title, short_code) VALUES ('s1', 'w1', 't', 'sc1')`)
	mustExec(t, s, `INSERT INTO messages (id, session_id, role, content) VALUES ('m1','s1','user','orphan-me')`)

	// Orphan the message by deleting the session with FKs disabled on a
	// dedicated connection (PRAGMA + DELETE must be on the same conn). The
	// orphan-check query and connection release must happen before migrate()
	// is called, because sqlitekit.OpenSingle forces MaxOpenConns=1 — any
	// s.DB.* call while this conn is pinned would deadlock.
	ctx := context.Background()
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		t.Fatalf("get conn: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		conn.Close()
		t.Fatalf("disable fk: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM sessions WHERE id='s1'`); err != nil {
		_, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
		conn.Close()
		t.Fatalf("orphan the message: %v", err)
	}

	var orphans int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages m LEFT JOIN sessions s ON s.id=m.session_id WHERE s.id IS NULL`,
	).Scan(&orphans); err != nil {
		_, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
		conn.Close()
		t.Fatalf("query orphans: %v", err)
	}

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		conn.Close()
		t.Errorf("re-enable fk: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Errorf("close conn: %v", err)
	}

	if orphans == 0 {
		t.Fatal("test setup failed: expected 1 orphan message")
	}

	if err := s.migrate(); err != nil {
		t.Fatalf("migrate with orphan message should succeed, got: %v", err)
	}
}

func mustExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.DB.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
