package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigration081_RerunAllowsStoppedStatus guards the no-ledger migration
// model: once later boots have introduced durable_agent_instances rows with
// status='stopped', rerunning migration 081 must still succeed before 082
// replays. This regressed when 081 rebuilt the table with the pre-stopped
// CHECK vocabulary.
func TestMigration081_RerunAllowsStoppedStatus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration081-rerun.db")
	s, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close(context.Background())

	var profileID string
	if err := s.DB.QueryRow(`SELECT id FROM agent_profiles ORDER BY id LIMIT 1`).Scan(&profileID); err != nil {
		t.Fatalf("select profile id: %v", err)
	}

	if _, err := s.DB.Exec(`
		INSERT INTO durable_agent_instances (
			id, name, slug, profile_id, lifecycle_class, provider, model,
			runtime_kind, launch_source_type, launch_source_id, work_root,
			status, current_session_id, failure_reason, metadata_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "durable-stopped-1", "Stopped Agent", "stopped-agent", profileID,
		DurableAgentClassAdvisor, "anthropic", "claude-sonnet-4", "api",
		DurableAgentLaunchDurableAdvisor, "stopped-agent", "/tmp/project",
		DurableAgentStatusStopped, "", "", "{}"); err != nil {
		t.Fatalf("insert stopped durable instance: %v", err)
	}

	if err := s.migrate(context.Background()); err != nil {
		t.Fatalf("rerun migrate with stopped durable instance: %v", err)
	}
}
