package store

import (
	"testing"
)

// TestMigration019_ExistingRowsPreserved verifies that the recreated
// subagent_runs table has the correct shape: old columns are preserved and new
// audit columns default to empty string.
func TestMigration019_ExistingRowsPreserved(t *testing.T) {
	s := newTestStore(t)

	// Insert a row using only the pre-019 columns; new cols stay at DEFAULT ''.
	_, err := s.DB.Exec(`
		INSERT INTO subagent_runs
			(id, parent_session_id, child_session_id, role, prompt, mode, status,
			 inputs_json, result_json, error, timeout_seconds,
			 created_at, started_at, completed_at)
		VALUES
			('run-1', 'sess-1', 'child-1', 'tester', 'do the thing',
			 'sync', 'completed',
			 '{}', '{"ok":true}', '', 300,
			 '2026-01-01T00:00:00Z', '2026-01-01T00:00:01Z', '2026-01-01T00:00:02Z')
	`)
	if err != nil {
		t.Fatalf("insert pre-019 row: %v", err)
	}

	// Read it back and verify old cols preserved + new cols are empty string.
	var (
		id, parentSessionID, childSessionID   string
		role, prompt, mode, status            string
		inputsJSON, resultJSON, errCol        string
		createdAt, startedAt, completedAt     string
		parentAgentID, envelopeInstanceID     string
		approvedAt, approvedBy                string
		rejectedAt, rejectionReason           string
		timeoutSeconds                        int
	)
	row := s.DB.QueryRow(`
		SELECT id, parent_session_id, child_session_id, role, prompt, mode, status,
		       inputs_json, result_json, error, timeout_seconds,
		       created_at, started_at, completed_at,
		       parent_agent_id, envelope_instance_id,
		       approved_at, approved_by,
		       rejected_at, rejection_reason
		FROM subagent_runs WHERE id = 'run-1'
	`)
	if err := row.Scan(
		&id, &parentSessionID, &childSessionID, &role, &prompt, &mode, &status,
		&inputsJSON, &resultJSON, &errCol, &timeoutSeconds,
		&createdAt, &startedAt, &completedAt,
		&parentAgentID, &envelopeInstanceID,
		&approvedAt, &approvedBy,
		&rejectedAt, &rejectionReason,
	); err != nil {
		t.Fatalf("scan row: %v", err)
	}

	// Old columns preserved.
	if id != "run-1" {
		t.Errorf("id: got %q, want %q", id, "run-1")
	}
	if parentSessionID != "sess-1" {
		t.Errorf("parent_session_id: got %q, want %q", parentSessionID, "sess-1")
	}
	if childSessionID != "child-1" {
		t.Errorf("child_session_id: got %q, want %q", childSessionID, "child-1")
	}
	if role != "tester" {
		t.Errorf("role: got %q, want %q", role, "tester")
	}
	if prompt != "do the thing" {
		t.Errorf("prompt: got %q, want %q", prompt, "do the thing")
	}
	if mode != "sync" {
		t.Errorf("mode: got %q, want %q", mode, "sync")
	}
	if status != "completed" {
		t.Errorf("status: got %q, want %q", status, "completed")
	}
	if resultJSON != `{"ok":true}` {
		t.Errorf("result_json: got %q, want %q", resultJSON, `{"ok":true}`)
	}
	if timeoutSeconds != 300 {
		t.Errorf("timeout_seconds: got %d, want 300", timeoutSeconds)
	}

	// New audit columns default to empty string.
	for name, val := range map[string]string{
		"parent_agent_id":      parentAgentID,
		"envelope_instance_id": envelopeInstanceID,
		"approved_at":          approvedAt,
		"approved_by":          approvedBy,
		"rejected_at":          rejectedAt,
		"rejection_reason":     rejectionReason,
	} {
		if val != "" {
			t.Errorf("new column %q: expected empty string default, got %q", name, val)
		}
	}
}

// TestMigration019_AcceptsNewEnumValues verifies that 'interactive' mode and
// 'rejected' status pass the updated CHECK constraints.
func TestMigration019_AcceptsNewEnumValues(t *testing.T) {
	s := newTestStore(t)

	_, err := s.DB.Exec(`
		INSERT INTO subagent_runs
			(id, parent_session_id, mode, status, created_at)
		VALUES
			('run-2', 'sess-2', 'interactive', 'rejected', '2026-01-01T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("insert row with interactive/rejected: %v", err)
	}

	var mode, status string
	if err := s.DB.QueryRow(
		`SELECT mode, status FROM subagent_runs WHERE id = 'run-2'`,
	).Scan(&mode, &status); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if mode != "interactive" {
		t.Errorf("mode: got %q, want %q", mode, "interactive")
	}
	if status != "rejected" {
		t.Errorf("status: got %q, want %q", status, "rejected")
	}
}

// TestMigration019_RerunPreservesBothUserSettingsColumns verifies the tail
// ALTER TABLE statements survive an idempotent re-run. splitSQL emits each
// ALTER as its own Exec (guaranteed by migration 019 ending its transaction
// with `END;` — not `COMMIT;`, which the splitter would not treat as a
// BEGIN-block closer, causing the two ALTERs to collapse into one multi-
// statement Exec where a "duplicate column" error on the first can mask the
// second under some driver semantics).
//
// Regression for Copilot review on PR #60, finding 3.
func TestMigration019_RerunPreservesBothUserSettingsColumns(t *testing.T) {
	s := newTestStore(t)

	// Re-run migrate(), which will re-hit the ALTER TABLE ADD COLUMN lines
	// and exercise the idempotent "duplicate column" path for each.
	if err := s.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	// Both columns must still exist and be queryable.
	if _, err := s.DB.Exec(`INSERT OR IGNORE INTO user_settings (id) VALUES (1)`); err != nil {
		t.Fatalf("seed user_settings: %v", err)
	}
	var approvalRequired, approvalTimeoutSeconds int
	if err := s.DB.QueryRow(
		`SELECT subagent_approval_required, subagent_approval_timeout_seconds FROM user_settings WHERE id = 1`,
	).Scan(&approvalRequired, &approvalTimeoutSeconds); err != nil {
		t.Fatalf("scan user_settings post re-migrate: %v", err)
	}
	if approvalRequired != 1 {
		t.Errorf("subagent_approval_required: got %d, want 1", approvalRequired)
	}
	if approvalTimeoutSeconds != 86400 {
		t.Errorf("subagent_approval_timeout_seconds: got %d, want 86400", approvalTimeoutSeconds)
	}
}

// TestMigration019_UserSettingsColumns verifies that the two new columns were
// added to user_settings with the correct defaults.
func TestMigration019_UserSettingsColumns(t *testing.T) {
	s := newTestStore(t)

	// user_settings uses integer PK id=1; insert the singleton row directly
	// so we can read it without relying on Seed side-effects.
	_, err := s.DB.Exec(`
		INSERT OR IGNORE INTO user_settings (id) VALUES (1)
	`)
	if err != nil {
		t.Fatalf("insert user_settings row: %v", err)
	}

	var approvalRequired int
	var approvalTimeoutSeconds int
	if err := s.DB.QueryRow(`
		SELECT subagent_approval_required, subagent_approval_timeout_seconds
		FROM user_settings WHERE id = 1
	`).Scan(&approvalRequired, &approvalTimeoutSeconds); err != nil {
		t.Fatalf("scan user_settings: %v", err)
	}

	if approvalRequired != 1 {
		t.Errorf("subagent_approval_required: got %d, want 1", approvalRequired)
	}
	if approvalTimeoutSeconds != 86400 {
		t.Errorf("subagent_approval_timeout_seconds: got %d, want 86400", approvalTimeoutSeconds)
	}
}
