package store

import "testing"

// TestMigrateSkipsRecreateOnceTableIsCurrent is the regression test for
// CW-20260817: nanite-api-service crash-looped in production because
// migrations 019/065/067 each recreate subagent_runs from their OWN
// historical column/CHECK set (SQLite can't ALTER a CHECK constraint), and
// with no schema_migrations table every migration file re-runs on every
// boot. The first time a real row ever got a status only migration 065's
// widened CHECK permits ('stalled', from the reaper — see
// migration092_last_activity_at, CW-20260816-0004), the very next restart's
// re-run of migration 019 tried to copy that row into a table rebuilt with
// 019's original 7-value CHECK and failed outright, taking the whole daemon
// down. The migrate:skip-if-column-exists directive on 019/065/067 (see
// store.go's migrate()) makes each skip its destructive rebuild entirely
// once subagent_runs already has migration 092's last_activity_at column —
// proof the table has already moved past anything those three migrations
// would otherwise redo.
func TestMigrateSkipsRecreateOnceTableIsCurrent(t *testing.T) {
	s := newTestStore(t)

	// A status only permitted by migration 065's widened CHECK, not
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

	// Simulate a restart: migrate() re-runs every file, including
	// 019/065/067, exactly as it does on every real boot.
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
		t.Errorf("retry_count: got %d, want 2 (would reset to 0 if 019/065 recreated the table)", retryCount)
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
