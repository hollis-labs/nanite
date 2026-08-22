package store

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrate145SeedsResumeLoopRunActionKind is the regression test for
// TASKS/loops/11-loop-event-predicate-trigger.md: reflex_action_kinds
// gets a new resume_loop_run row (execute_action / all_applicable / no
// cooldown), and reflex_action_kind_provenance_allow gets
// resume_loop_run x {system, operator} (not plugin), matching this
// migration's own doc comment byte-for-byte.
func TestMigrate145SeedsResumeLoopRunActionKind(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.GetReflexActionKind(ctx, ReflexActionResumeLoopRun)
	if err != nil {
		t.Fatalf("GetReflexActionKind(resume_loop_run): %v", err)
	}
	if got.Category != "execute_action" {
		t.Errorf("resume_loop_run.category = %q, want execute_action", got.Category)
	}
	if got.CombiningAlgorithm != "all_applicable" {
		t.Errorf("resume_loop_run.combining_algorithm = %q, want all_applicable", got.CombiningAlgorithm)
	}
	if got.DefaultRecurrenceSeconds == nil || *got.DefaultRecurrenceSeconds != 0 {
		t.Errorf("resume_loop_run.default_recurrence_seconds = %v, want 0", ptrInt64String(got.DefaultRecurrenceSeconds))
	}

	for _, tc := range []struct {
		tier      string
		wantAllow bool
	}{
		{"system", true},
		{"operator", true},
		{"plugin", false},
	} {
		allowed, err := s.ActionKindAllowsProvenanceTier(ctx, ReflexActionResumeLoopRun, tc.tier)
		if err != nil {
			t.Fatalf("ActionKindAllowsProvenanceTier(resume_loop_run, %q): %v", tc.tier, err)
		}
		if allowed != tc.wantAllow {
			t.Errorf("ActionKindAllowsProvenanceTier(resume_loop_run, %q) = %v, want %v", tc.tier, allowed, tc.wantAllow)
		}
	}

	assertGooseHasNothingPending(t, s)

	// Simulated restart: a second full migrate() must be a clean no-op.
	if err := s.migrate(); err != nil {
		t.Fatalf("re-migrate after 145 already applied: %v", err)
	}
}

// TestMigrate145WidensActionKindCheckAndRoundTrips confirms the actual
// widened constraint: an agent_reflexes row with
// action_kind='resume_loop_run' can be inserted and read back (previously
// a CHECK violation against 131's six-value list), and an unrecognized
// action_kind still fails (the CHECK/FK didn't just get dropped).
func TestMigrate145WidensActionKindCheckAndRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, err := s.InsertAgentReflex(ctx, AgentReflex{
		Name:        "resume-loop-run-probe",
		ClassTag:    "process",
		TriggerKind: ReflexTriggerEvent,
		TriggerSpec: `{"name":"external_check_passed"}`,
		ActionKind:  ReflexActionResumeLoopRun,
		ActionSpec:  `{"loop_run_id":"lr-probe-1"}`,
		CreatedBy:   "system",
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex(resume_loop_run): %v", err)
	}
	row, err := s.GetAgentReflex(ctx, id)
	if err != nil {
		t.Fatalf("GetAgentReflex: %v", err)
	}
	if row.ActionKind != ReflexActionResumeLoopRun {
		t.Errorf("row.ActionKind = %q, want %q", row.ActionKind, ReflexActionResumeLoopRun)
	}
	if row.ProvenanceTier != "system" {
		t.Errorf("row.ProvenanceTier = %q, want system (created_by=system)", row.ProvenanceTier)
	}

	if _, err := s.InsertAgentReflex(ctx, AgentReflex{
		Name:        "bogus-action-kind-probe",
		ClassTag:    "process",
		TriggerKind: ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  "not_a_real_action_kind",
		ActionSpec:  `{}`,
		CreatedBy:   "system",
	}); err == nil {
		t.Fatal("InsertAgentReflex(action_kind=not_a_real_action_kind) succeeded, want CHECK/FK violation")
	}

	// ListAgentReflexesForLoopRun's own json_extract-based lookup —
	// confirms both the widened CHECK/FK and the read path this task adds
	// work together against a real (non-empty) database.
	found, err := s.ListAgentReflexesForLoopRun(ctx, "lr-probe-1")
	if err != nil {
		t.Fatalf("ListAgentReflexesForLoopRun: %v", err)
	}
	if len(found) != 1 || found[0].ID != id {
		t.Fatalf("ListAgentReflexesForLoopRun(lr-probe-1) = %+v, want exactly the probe row (id=%s)", found, id)
	}
	if none, err := s.ListAgentReflexesForLoopRun(ctx, "lr-does-not-exist"); err != nil || len(none) != 0 {
		t.Fatalf("ListAgentReflexesForLoopRun(unknown loop_run_id) = %+v, err=%v, want empty/nil, nil", none, err)
	}
}

// TestMigrate145PreservesExistingRowsAcrossRebuild rolls the schema back to
// just before 145, inserts a probe row under 131's own (pre-145) six-value
// CHECK, replays 145's Up, and confirms the pre-existing row survives the
// rebuild unchanged — same technique as 124's own
// TestMigrate124BackfillsProvenanceTierFromCreatedBy.
func TestMigrate145PreservesExistingRowsAcrossRebuild(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("construct goose provider: %v", err)
	}

	if _, err := provider.DownTo(ctx, 144); err != nil {
		t.Fatalf("goose DownTo 144 (reverse migration 145): %v", err)
	}

	// Pre-145 shape still has the 131-era six-value CHECK -- confirm
	// resume_loop_run is rejected here, then insert a probe row under an
	// already-existing kind (raw SQL against the exact pre-145 column
	// list, matching 144's own test file's technique for inserting against
	// a rolled-back schema).
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO agent_reflexes
		    (id, agent_id, class_tag, name, trigger_kind, trigger_spec,
		     action_kind, action_spec, status, priority, fired_count,
		     last_fired_at, created_at, created_by, opt_out_allowed,
		     provenance_tier, recurrence_override_seconds, workflow_run_id)
		 VALUES ('rfx-145-probe', NULL, 'process', 'pre-145-probe', 'event', '{"name":"probe"}',
		         'halt_session', '{"reason":"probe"}', 'active', 0, 0,
		         NULL, datetime('now'), 'system', 0,
		         'system', NULL, NULL)`,
	); err != nil {
		t.Fatalf("insert pre-145 probe row: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO agent_reflexes
		    (id, agent_id, class_tag, name, trigger_kind, trigger_spec,
		     action_kind, action_spec, status, priority, fired_count,
		     last_fired_at, created_at, created_by, opt_out_allowed,
		     provenance_tier, recurrence_override_seconds, workflow_run_id)
		 VALUES ('rfx-145-bogus', NULL, 'process', 'pre-145-bogus', 'event', '{"name":"probe"}',
		         'resume_loop_run', '{"loop_run_id":"lr-x"}', 'active', 0, 0,
		         NULL, datetime('now'), 'system', 0,
		         'system', NULL, NULL)`,
	); err == nil {
		t.Fatal("insert pre-145 row with action_kind=resume_loop_run succeeded, want CHECK/FK violation against the pre-145 six-value constraint")
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up (replay migration 145): %v", err)
	}

	row, err := s.GetAgentReflex(ctx, "rfx-145-probe")
	if err != nil {
		t.Fatalf("GetAgentReflex(pre-145 probe row) after migration 145: %v", err)
	}
	if row.ActionKind != ReflexActionHaltSession || row.Name != "pre-145-probe" {
		t.Errorf("pre-145 probe row changed across migration 145's rebuild: %+v", row)
	}

	// And the new capability actually works now that 145 has run.
	newID, err := s.InsertAgentReflex(ctx, AgentReflex{
		Name:        "post-145-probe",
		ClassTag:    "process",
		TriggerKind: ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  ReflexActionResumeLoopRun,
		ActionSpec:  `{"loop_run_id":"lr-x"}`,
		CreatedBy:   "system",
	})
	if err != nil {
		t.Fatalf("InsertAgentReflex(resume_loop_run) after migration 145: %v", err)
	}
	if _, err := s.GetAgentReflex(ctx, newID); err != nil {
		t.Fatalf("GetAgentReflex(post-145 probe row): %v", err)
	}
}

// TestRealBackupAgentReflexesSurviveResumeLoopRunMigration is the "no data
// lost, no false-positive rejection of already-live data" check
// EXECUTION-PROCESS.md's schema-migration testing requirement calls for:
// every real, pre-existing agent_reflexes row from an actual production
// backup must survive migration 145's table rebuild intact. Per
// EXECUTION-PROCESS.md, the real backup file is never opened in place —
// it is copied into t.TempDir() before store.New ever touches it. Skips
// (rather than fails) when the backup isn't present on this machine.
func TestRealBackupAgentReflexesSurviveResumeLoopRunMigration(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot resolve home directory: %v", err)
	}
	backupSrc := filepath.Join(home, ".local", "share", "nanite", "workspaces", "default", "backups",
		"main.db.pre-execution-backup-20260818-132726")
	if _, err := os.Stat(backupSrc); err != nil {
		t.Skipf("real backup db not present at %s (skipping live-backup verification): %v", backupSrc, err)
	}

	scratchDir := t.TempDir()
	dstPath := filepath.Join(scratchDir, "main.db")
	if err := copyFile(dstPath, backupSrc); err != nil {
		t.Fatalf("copy real backup db to scratch path: %v", err)
	}
	if walInfo, err := os.Stat(backupSrc + "-wal"); err == nil && walInfo.Size() > 0 {
		if err := copyFile(dstPath+"-wal", backupSrc+"-wal"); err != nil {
			t.Fatalf("copy real backup -wal to scratch path: %v", err)
		}
	}
	absPath, err := filepath.Abs(dstPath)
	if err != nil {
		t.Fatalf("resolve scratch db path: %v", err)
	}

	ctx := context.Background()
	rs, err := New(ctx, absPath)
	if err != nil {
		t.Fatalf("open+migrate scratch copy of real backup db: %v", err)
	}
	defer rs.Close()

	type reflexRow struct{ id, actionKind, name string }
	var rows []reflexRow
	r, err := rs.DB.QueryContext(ctx, `SELECT id, action_kind, name FROM agent_reflexes`)
	if err != nil {
		t.Fatalf("query agent_reflexes on real backup copy (post-migration): %v", err)
	}
	for r.Next() {
		var row reflexRow
		if err := r.Scan(&row.id, &row.actionKind, &row.name); err != nil {
			r.Close()
			t.Fatalf("scan agent_reflexes row: %v", err)
		}
		rows = append(rows, row)
	}
	rowsErr := r.Err()
	r.Close()
	if rowsErr != nil {
		t.Fatalf("iterate agent_reflexes rows: %v", rowsErr)
	}
	if len(rows) < 14 {
		t.Fatalf("real backup agent_reflexes row count = %d, want >= 14 (spot-check requires the real pre-existing rows, not an empty/synthetic db)", len(rows))
	}
	for _, row := range rows {
		switch row.actionKind {
		case ReflexActionInjectReminder, ReflexActionForceToolChoice, ReflexActionSendMessage,
			ReflexActionHaltSession, ReflexActionAddSchedule, ReflexActionDispatchToAgent, ReflexActionResumeLoopRun:
		default:
			t.Errorf("real backup agent_reflexes row %s (name=%q) has unrecognized action_kind %q after migration 145 -- data corrupted by the rebuild", row.id, row.name, row.actionKind)
		}
	}
	t.Logf("verified %d real agent_reflexes rows from backup survive migration 145's rebuild with their action_kind intact", len(rows))

	// And the new capability actually works against this real, migrated
	// copy.
	if _, err := rs.InsertAgentReflex(ctx, AgentReflex{
		Name:        "post-145-real-backup-probe",
		ClassTag:    "process",
		TriggerKind: ReflexTriggerEvent,
		TriggerSpec: `{"name":"probe"}`,
		ActionKind:  ReflexActionResumeLoopRun,
		ActionSpec:  `{"loop_run_id":"lr-real-backup-probe"}`,
		CreatedBy:   "system",
	}); err != nil {
		t.Fatalf("InsertAgentReflex(resume_loop_run) on real backup copy: %v", err)
	}
}
