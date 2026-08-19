package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentBrokerDecision_InsertAndList exercises the happy path:
// InsertAgentBrokerDecision writes a row, populates ID + CreatedAt, and
// ListRecentAgentBrokerDecisions(N) returns the N most-recent rows in
// created_at DESC order.
func TestAgentBrokerDecision_InsertAndList(t *testing.T) {
	s := newTestStore(t)

	// Seed three rows. SQLite's datetime('now') has 1-second resolution, so
	// rows inserted in the same call may share a created_at; the helper's
	// secondary `id DESC` sort guarantees stable ordering even in that case.
	rows := []*AgentBrokerDecision{
		{
			SessionID:     "sess-1",
			TurnID:        "turn-1",
			UserInputHash: "hash-aaa",
			ModeSignal:    "chat",
			ScopeTier:     "trivial",
			ReflexID:      "",
			Decision:      "",
			Reason:        "default-chat-handle",
			Confidence:    1.0,
		},
		{
			SessionID:     "sess-1",
			TurnID:        "turn-2",
			UserInputHash: "hash-bbb",
			ModeSignal:    "work",
			ScopeTier:     "small",
			ReflexID:      "",
			Decision:      "worker",
			Reason:        "mode=work,confidence>=0.85",
			Confidence:    0.91,
		},
		{
			SessionID:     "sess-2",
			TurnID:        "turn-3",
			UserInputHash: "hash-ccc",
			ModeSignal:    "plan",
			ScopeTier:     "open",
			ReflexID:      "reflex-plan-decompose-001",
			Decision:      "planner",
			Reason:        "reflex-match",
			Confidence:    0.88,
		},
	}

	for i, row := range rows {
		if err := s.InsertAgentBrokerDecision(row); err != nil {
			t.Fatalf("Insert row %d: %v", i, err)
		}
		if row.ID == 0 {
			t.Errorf("row %d: ID was not assigned", i)
		}
		if row.CreatedAt == "" {
			t.Errorf("row %d: CreatedAt was not populated", i)
		}
	}

	// IDs should be monotonically increasing — INTEGER PRIMARY KEY without
	// AUTOINCREMENT in SQLite is the rowid alias, which is monotonic for
	// inserts within a single connection.
	for i := 1; i < len(rows); i++ {
		if rows[i].ID <= rows[i-1].ID {
			t.Errorf("ID monotonicity broke: rows[%d].ID = %d, rows[%d].ID = %d",
				i-1, rows[i-1].ID, i, rows[i].ID)
		}
	}

	// ListRecent(2) should return the 2 most-recent (turn-3, turn-2).
	recent, err := s.ListRecentAgentBrokerDecisions(2)
	if err != nil {
		t.Fatalf("ListRecent(2): %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("ListRecent(2) length: got %d, want 2", len(recent))
	}
	if recent[0].TurnID != "turn-3" {
		t.Errorf("ListRecent(2)[0].TurnID: got %q, want %q", recent[0].TurnID, "turn-3")
	}
	if recent[1].TurnID != "turn-2" {
		t.Errorf("ListRecent(2)[1].TurnID: got %q, want %q", recent[1].TurnID, "turn-2")
	}

	// Round-trip a row to verify every column scanned cleanly.
	got := recent[0]
	if got.SessionID != "sess-2" {
		t.Errorf("SessionID: got %q, want %q", got.SessionID, "sess-2")
	}
	if got.UserInputHash != "hash-ccc" {
		t.Errorf("UserInputHash: got %q, want %q", got.UserInputHash, "hash-ccc")
	}
	if got.ModeSignal != "plan" {
		t.Errorf("ModeSignal: got %q, want %q", got.ModeSignal, "plan")
	}
	if got.ScopeTier != "open" {
		t.Errorf("ScopeTier: got %q, want %q", got.ScopeTier, "open")
	}
	if got.ReflexID != "reflex-plan-decompose-001" {
		t.Errorf("ReflexID: got %q, want %q", got.ReflexID, "reflex-plan-decompose-001")
	}
	if got.Decision != "planner" {
		t.Errorf("Decision: got %q, want %q", got.Decision, "planner")
	}
	if got.Reason != "reflex-match" {
		t.Errorf("Reason: got %q, want %q", got.Reason, "reflex-match")
	}
	if got.Confidence != 0.88 {
		t.Errorf("Confidence: got %v, want %v", got.Confidence, 0.88)
	}
	if got.CreatedAt == "" {
		t.Error("CreatedAt empty after ListRecent")
	}
}

// TestAgentBrokerDecision_ListRecent_DefaultLimit confirms that limit ≤ 0
// uses the documented default of 50.
func TestAgentBrokerDecision_ListRecent_DefaultLimit(t *testing.T) {
	s := newTestStore(t)

	// Seed 60 rows so the default-50 cap is observable.
	for i := 0; i < 60; i++ {
		row := &AgentBrokerDecision{
			SessionID:     "sess-bulk",
			TurnID:        "turn-bulk",
			UserInputHash: "hash-bulk",
			Decision:      "",
			Reason:        "default-chat-handle",
			Confidence:    1.0,
		}
		if err := s.InsertAgentBrokerDecision(row); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	for _, lim := range []int{0, -1} {
		got, err := s.ListRecentAgentBrokerDecisions(lim)
		if err != nil {
			t.Fatalf("ListRecent(%d): %v", lim, err)
		}
		if len(got) != 50 {
			t.Errorf("ListRecent(%d) length: got %d, want 50 (default cap)", lim, len(got))
		}
	}
}

// TestAgentBrokerDecision_Insert_NilRow guards the nil-row early return so a
// programmer error surfaces with a clear message instead of a SQL nil-deref.
func TestAgentBrokerDecision_Insert_NilRow(t *testing.T) {
	s := newTestStore(t)
	err := s.InsertAgentBrokerDecision(nil)
	if err == nil {
		t.Fatal("InsertAgentBrokerDecision(nil): want error, got nil")
	}
}

// TestMigration058_TableExists confirms the migration ran on a fresh store
// and the table + indexes are present. Belt-and-suspenders against a future
// rename or accidental delete of the migration file.
func TestMigration058_TableExists(t *testing.T) {
	s := newTestStore(t)

	var tableName string
	err := s.DB.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='agent_broker_decisions'`,
	).Scan(&tableName)
	if err != nil {
		t.Fatalf("agent_broker_decisions table not found: %v", err)
	}

	// Verify the indexes exist (session lookup + recent-N admin query).
	wantIndexes := []string{
		"idx_agent_broker_decisions_session",
		"idx_agent_broker_decisions_created_at",
	}
	for _, idx := range wantIndexes {
		var name string
		err := s.DB.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='index' AND name=?`,
			idx,
		).Scan(&name)
		if err != nil {
			t.Errorf("index %q not found: %v", idx, err)
		}
	}

	// Historical note: this used to also assert that the pre-existing
	// tool-broker `broker_decisions` table survived migration 058 (i.e.
	// migration 058 doesn't collide with it). That assertion no longer
	// holds by design — TASKS/phase-0/23-export-and-drop-decision-tables.md
	// (migration 111_drop_broker_and_strategy_decisions.sql) drops
	// `broker_decisions` outright, after exporting its rows to event_log.
	// The two tables never collided at 058-authoring time; 058's own
	// non-collision property is unaffected by 111 dropping one of them
	// later, so no replacement assertion is needed here.
}

// TestMigration058_Idempotent re-runs migration 058's own SQL directly
// against an already-migrated store and confirms it's a true no-op. Goose's
// real ledger (see docs/engineering/architecture/05-storage-and-migrations.md)
// now guarantees this migration only ever runs once against any given
// database, but the underlying SQL's own idempotency (CREATE TABLE IF NOT
// EXISTS not truncating existing data) is still worth verifying directly —
// e.g. for `goose redo`, or as a pattern check for future migrations.
func TestMigration058_Idempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration058.db")
	s, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	mig, err := migrationsFS.ReadFile("migrations/058_agent_broker_decisions.sql")
	if err != nil {
		t.Fatalf("read migration 058: %v", err)
	}

	// Seed a row before the re-run so we can confirm the re-run preserves
	// data (CREATE TABLE IF NOT EXISTS must not truncate).
	row := &AgentBrokerDecision{
		SessionID:     "sess-pre-rerun",
		TurnID:        "turn-pre-rerun",
		UserInputHash: "hash-pre",
		Decision:      "worker",
		Reason:        "pre-rerun-check",
		Confidence:    1.0,
	}
	if err := s.InsertAgentBrokerDecision(row); err != nil {
		t.Fatalf("seed row before re-run: %v", err)
	}

	// Apply each statement in the migration directly, independent of
	// goose, to probe the SQL's own idempotency.
	stmts := splitSQLStatementsForTest(t, string(mig))
	for _, stmt := range stmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := s.DB.Exec(stmt); err != nil {
			t.Fatalf("re-run statement failed:\nERR: %v\nSQL: %s", err, stmt)
		}
	}

	// Row must survive the re-run.
	var count int
	if err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM agent_broker_decisions WHERE turn_id = 'turn-pre-rerun'`,
	).Scan(&count); err != nil {
		t.Fatalf("count after re-run: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count after re-run: got %d, want 1 (CREATE TABLE IF NOT EXISTS must not truncate)", count)
	}
}
