package store

import (
	"strings"
	"testing"
)

// TestMigration008_EnvelopeResponseRoleAllowed verifies the patched CHECK
// constraint on messages.role permits the new 'envelope_response' value while
// still rejecting unknown roles.
func TestMigration008_EnvelopeResponseRoleAllowed(t *testing.T) {
	s := newTestStore(t)

	// Seed a session so the FK is satisfied.
	if _, err := s.DB.Exec(
		`INSERT INTO workspaces (id, name, created_at) VALUES ('w1', 'ws', CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := s.DB.Exec(
		`INSERT INTO sessions (id, workspace_id, title, short_code, created_at) VALUES ('s1', 'w1', 't', 'sc1', CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	// envelope_response should be accepted.
	if _, err := s.DB.Exec(
		`INSERT INTO messages (id, session_id, role, content) VALUES ('m1','s1','envelope_response','{}')`,
	); err != nil {
		t.Fatalf("insert envelope_response role: %v", err)
	}

	// Unknown roles should still be rejected.
	_, err := s.DB.Exec(
		`INSERT INTO messages (id, session_id, role, content) VALUES ('m2','s1','bogus','{}')`,
	)
	if err == nil {
		t.Fatal("expected CHECK violation for unknown role")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "check") {
		t.Fatalf("expected CHECK constraint error, got: %v", err)
	}
}

// TestMigration008_Idempotent verifies running the migration twice leaves the
// schema in the correct state (replace() no-ops on second run).
func TestMigration008_Idempotent(t *testing.T) {
	s := newTestStore(t)

	// Re-run migrate() (simulates a second startup).
	if err := s.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	// Schema should still allow envelope_response.
	var sql string
	if err := s.DB.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='messages'`,
	).Scan(&sql); err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if !strings.Contains(sql, "envelope_response") {
		t.Fatalf("envelope_response not present in schema after re-migrate: %s", sql)
	}
	// And no duplication (confirm there's exactly one occurrence).
	if strings.Count(sql, "envelope_response") != 1 {
		t.Fatalf("expected exactly one envelope_response in CHECK, got: %s", sql)
	}
}
