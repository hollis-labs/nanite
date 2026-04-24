package store

import (
	"testing"
)

// TestMigration025_ProviderColumnExists verifies that the migration-025
// provider column is present on subagent_runs with the correct DEFAULT
// behaviour: existing rows read back as empty string (agent-profile
// default path), and newly inserted rows can store an explicit override.
func TestMigration025_ProviderColumnExists(t *testing.T) {
	s := newTestStore(t)

	// Insert a row without specifying provider — must land at DEFAULT ''.
	_, err := s.DB.Exec(`
		INSERT INTO subagent_runs
			(id, parent_session_id, child_session_id, role, prompt, mode, status,
			 inputs_json, result_json, error, timeout_seconds,
			 created_at, started_at, completed_at)
		VALUES
			('run-default', 'sess-1', '', 'tester', 'p', 'sync', 'running',
			 '{}', '{}', '', 300,
			 '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '')
	`)
	if err != nil {
		t.Fatalf("insert without provider: %v", err)
	}

	var providerDefault string
	if err := s.DB.QueryRow(
		`SELECT provider FROM subagent_runs WHERE id = 'run-default'`,
	).Scan(&providerDefault); err != nil {
		t.Fatalf("select provider (default row): %v", err)
	}
	if providerDefault != "" {
		t.Errorf("provider default = %q, want empty string", providerDefault)
	}

	// Insert a row with an explicit provider override.
	_, err = s.DB.Exec(`
		INSERT INTO subagent_runs
			(id, parent_session_id, child_session_id, role, prompt, mode, provider, status,
			 inputs_json, result_json, error, timeout_seconds,
			 created_at, started_at, completed_at)
		VALUES
			('run-override', 'sess-1', '', 'tester', 'p', 'sync', 'pty-claude', 'running',
			 '{}', '{}', '', 300,
			 '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '')
	`)
	if err != nil {
		t.Fatalf("insert with provider override: %v", err)
	}

	var providerOverride string
	if err := s.DB.QueryRow(
		`SELECT provider FROM subagent_runs WHERE id = 'run-override'`,
	).Scan(&providerOverride); err != nil {
		t.Fatalf("select provider (override row): %v", err)
	}
	if providerOverride != "pty-claude" {
		t.Errorf("provider override = %q, want %q", providerOverride, "pty-claude")
	}
}
