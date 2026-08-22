package store

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrate144WidensBothTablesWaitingOnLoopStatus is the regression test
// for TASKS/loops/09-stepkindloop-executor-and-waiting-status.md: setting
// workflow_runs.status or workflow_run_steps.status to 'waiting_on_loop'
// must succeed post-migration where it previously violated 133's
// CHECK(status IN (...)) on both tables, every pre-existing status value
// (including 'waiting_on_gate'/'waiting_on_flex', added by 133) must keep
// working unaffected by the rebuild, and workflow_run_steps' new
// loop_run_id column must round-trip through UpsertWorkflowRunStep/
// GetWorkflowRunStepByLoopRunID.
func TestMigrate144WidensBothTablesWaitingOnLoopStatus(t *testing.T) {
	s := newTestStore(t)

	for _, status := range []string{"running", "completed", "failed", "cancelled", "waiting_on_gate", "waiting_on_flex", "waiting_on_loop"} {
		runID := "run-144-" + status
		if err := s.CreateWorkflowRun(context.Background(), &WorkflowRunRow{ID: runID, DefinitionName: "loop-status-check", Status: status}); err != nil {
			t.Fatalf("CreateWorkflowRun(status=%q): %v", status, err)
		}
	}
	if err := s.CreateWorkflowRun(context.Background(), &WorkflowRunRow{ID: "run-144-bogus", DefinitionName: "loop-status-check", Status: "bogus"}); err == nil {
		t.Fatal("CreateWorkflowRun(status=\"bogus\") succeeded, want CHECK violation")
	}

	if err := s.CreateWorkflowRun(context.Background(), &WorkflowRunRow{ID: "run-144-steps", DefinitionName: "loop-status-check", Status: "running"}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}
	for _, status := range []string{"pending", "running", "completed", "failed", "waiting_on_gate", "waiting_on_flex", "waiting_on_loop", "skipped"} {
		row := &WorkflowRunStepRow{
			WorkflowRunID: "run-144-steps",
			StepID:        "step-" + status,
			Kind:          "loop",
			Status:        status,
		}
		if err := s.UpsertWorkflowRunStep(context.Background(), row); err != nil {
			t.Fatalf("UpsertWorkflowRunStep(status=%q): %v", status, err)
		}
	}
	badStep := &WorkflowRunStepRow{WorkflowRunID: "run-144-steps", StepID: "step-bogus", Kind: "loop", Status: "bogus"}
	if err := s.UpsertWorkflowRunStep(context.Background(), badStep); err == nil {
		t.Fatal("UpsertWorkflowRunStep(status=\"bogus\") succeeded, want CHECK violation")
	}

	// loop_run_id round-trip -- workflow_run_steps.loop_run_id REFERENCES
	// loop_runs(id) with FK enforcement on, so this needs a real goals/
	// loop_runs row chain, not an arbitrary string.
	loopRunID := createTestLoopRunForMigration144(t, s, "loop-144-check")
	waitingRow := &WorkflowRunStepRow{
		// Deliberately a distinct StepID from the "step-waiting_on_loop"
		// row already inserted above — reusing it would UPSERT (update)
		// that existing row in place rather than add a new one, since
		// workflowRunStepID's synthetic PK is (workflow_run_id, step_id).
		WorkflowRunID: "run-144-steps", StepID: "step-loop-round-trip", Kind: "loop", Status: "waiting_on_loop",
		LoopRunID: &loopRunID,
	}
	if err := s.UpsertWorkflowRunStep(context.Background(), waitingRow); err != nil {
		t.Fatalf("UpsertWorkflowRunStep with LoopRunID: %v", err)
	}
	got, err := s.GetWorkflowRunStepByLoopRunID(context.Background(), loopRunID)
	if err != nil {
		t.Fatalf("GetWorkflowRunStepByLoopRunID: %v", err)
	}
	if got.StepID != "step-loop-round-trip" || got.WorkflowRunID != "run-144-steps" {
		t.Errorf("GetWorkflowRunStepByLoopRunID returned (run=%s, step=%s), want (run-144-steps, step-loop-round-trip)", got.WorkflowRunID, got.StepID)
	}
	if got.LoopRunID == nil || *got.LoopRunID != loopRunID {
		t.Errorf("GetWorkflowRunStepByLoopRunID row LoopRunID = %v, want %q", got.LoopRunID, loopRunID)
	}
	if _, err := s.GetWorkflowRunStepByLoopRunID(context.Background(), "no-such-loop-run"); err != ErrWorkflowRunStepNotFound {
		t.Errorf("GetWorkflowRunStepByLoopRunID(unknown) error = %v, want ErrWorkflowRunStepNotFound", err)
	}

	assertGooseHasNothingPending(t, s)

	// Simulated restart: a second full migrate() must be a clean no-op.
	if err := s.migrate(context.Background()); err != nil {
		t.Fatalf("re-migrate after 144 already applied: %v", err)
	}
	stepsAfter, err := s.ListWorkflowRunSteps(context.Background(), "run-144-steps")
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps after re-migrate: %v", err)
	}
	if len(stepsAfter) != 9 { // 8 status values + the extra waiting_on_loop-with-LoopRunID row
		t.Fatalf("len(stepsAfter) = %d, want 9 (rows must survive a re-migrate no-op)", len(stepsAfter))
	}
}

// TestMigrate144PreservesIndexes confirms the table rebuild (rename-
// recreate-copy, same pattern as 130/133/139) leaves every original index
// on both tables in place with their original definitions.
func TestMigrate144PreservesIndexes(t *testing.T) {
	s := newTestStore(t)

	assertIndexExists := func(t *testing.T, table, indexName string, wantSubstrings ...string) {
		t.Helper()
		var sqlText sql.NullString
		row := s.DB.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND name = ?`, table, indexName)
		if err := row.Scan(&sqlText); err != nil {
			t.Fatalf("query index %s on %s: %v", indexName, table, err)
		}
		if !sqlText.Valid {
			t.Fatalf("index %s on %s has no SQL (auto-index?)", indexName, table)
		}
		for _, want := range wantSubstrings {
			if !strings.Contains(sqlText.String, want) {
				t.Errorf("index %s sql = %q, want it to contain %q", indexName, sqlText.String, want)
			}
		}
	}

	assertIndexExists(t, "workflow_runs", "idx_workflow_runs_status", "status", "started_at")
	assertIndexExists(t, "workflow_runs", "idx_workflow_runs_loop_run", "loop_run_id", "loop_iteration")
	assertIndexExists(t, "workflow_run_steps", "idx_workflow_run_steps_run_step", "UNIQUE", "workflow_run_id", "step_id")
	assertIndexExists(t, "workflow_run_steps", "idx_workflow_run_steps_status", "workflow_run_id", "status")
}

// TestRealBackupWorkflowRunWaitingRowsSurviveLoop144Migration is the "no
// data lost, no false-positive rejection of already-live data" check
// EXECUTION-PROCESS.md's schema-migration testing requirement and this
// task's own Done-means both require: every real, pre-existing
// workflow_runs/workflow_run_steps row from an actual production backup
// must survive migration 144's table rebuild intact, AND — since this
// task's Done-means specifically requires verifying preservation of
// existing flex/gate *waiting* rows across the rebuild — synthetic
// waiting_on_gate and waiting_on_flex rows are inserted directly against
// the real backup copy immediately before migration 144 runs (goose
// DownTo(143) then Up()), because the one real backup available on this
// machine (main.db.pre-execution-backup-20260818-132726) predates
// 130_workflow_run_steps_flex_kind.sql / 133_workflow_run_flex_waiting_
// status.sql entirely — its on-disk status CHECK is still the original
// 5-value set and it has zero gate/flex waiting rows of its own (confirmed
// by direct inspection this session, mirroring migration 139's own test
// file's identical finding). Splitting the migration run at the 143/144
// boundary lets this test exercise the exact scenario the Done-means
// describes using this machine's only real backup as the base.
//
// The synthetic rows are inserted via raw SQL, not the Go
// CreateWorkflowRun/UpsertWorkflowRunStep helpers: those helpers' column
// lists are written against THIS worktree's head schema (which already
// includes workflow_run_steps.loop_run_id, added by migration 144 itself)
// and would fail against the intentionally-rolled-back pre-144 schema this
// test inserts into.
//
// Per EXECUTION-PROCESS.md, the real backup file is never opened in place
// — it is copied into t.TempDir() before store.New ever touches it. Skips
// (rather than fails) when the backup isn't present on this machine.
func TestRealBackupWorkflowRunWaitingRowsSurviveLoop144Migration(t *testing.T) {
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
	defer rs.Close(context.Background())

	type runRow struct{ id, status string }
	type stepRow struct{ workflowRunID, stepID, kind, status string }

	collectRuns := func() []runRow {
		rows, err := rs.DB.QueryContext(ctx, `SELECT id, status FROM workflow_runs`)
		if err != nil {
			t.Fatalf("query workflow_runs: %v", err)
		}
		defer rows.Close()
		var out []runRow
		for rows.Next() {
			var r runRow
			if err := rows.Scan(&r.id, &r.status); err != nil {
				t.Fatalf("scan workflow_runs row: %v", err)
			}
			out = append(out, r)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate workflow_runs rows: %v", err)
		}
		return out
	}
	collectSteps := func() []stepRow {
		rows, err := rs.DB.QueryContext(ctx, `SELECT workflow_run_id, step_id, kind, status FROM workflow_run_steps`)
		if err != nil {
			t.Fatalf("query workflow_run_steps: %v", err)
		}
		defer rows.Close()
		var out []stepRow
		for rows.Next() {
			var r stepRow
			if err := rows.Scan(&r.workflowRunID, &r.stepID, &r.kind, &r.status); err != nil {
				t.Fatalf("scan workflow_run_steps row: %v", err)
			}
			out = append(out, r)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate workflow_run_steps rows: %v", err)
		}
		return out
	}

	runsBefore := collectRuns()
	stepsBefore := collectSteps()
	if len(runsBefore) == 0 || len(stepsBefore) == 0 {
		t.Fatal("real backup has zero pre-existing workflow_runs/workflow_run_steps rows -- spot-check requires real pre-existing rows")
	}
	t.Logf("real backup copy has %d workflow_runs and %d workflow_run_steps rows before the 143->144 boundary replay", len(runsBefore), len(stepsBefore))

	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, rs.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("construct goose provider: %v", err)
	}

	if _, err := provider.DownTo(ctx, 143); err != nil {
		t.Fatalf("goose DownTo 143 (reverse migration 144 on real backup copy): %v", err)
	}

	// Insert synthetic waiting_on_gate / waiting_on_flex rows via raw SQL
	// (pre-144 schema: workflow_run_steps has no loop_run_id column yet;
	// this task's own Go helpers assume head schema and would fail here).
	const gateRunID = "real-backup-pre-144-gate-run"
	const gateStepID = "gate-step-pre-144"
	const flexRunID = "real-backup-pre-144-flex-run"
	const flexStepID = "flex-step-pre-144"

	insertRun := func(id, status string) {
		if _, err := rs.DB.ExecContext(ctx, `
			INSERT INTO workflow_runs
				(id, definition_name, status, input_json, error, started_at, completed_at, updated_at, loop_run_id, loop_iteration)
			VALUES (?, 'pre-144-waiting-check', ?, '{}', '', datetime('now'), '', datetime('now'), NULL, NULL)`,
			id, status,
		); err != nil {
			t.Fatalf("insert synthetic workflow_runs row (id=%s, status=%s) at schema version 143: %v", id, status, err)
		}
	}
	insertStep := func(id, runID, stepID, kind, status string) {
		if _, err := rs.DB.ExecContext(ctx, `
			INSERT INTO workflow_run_steps
				(id, workflow_run_id, step_id, kind, status, output, is_error, tool_calls_json, verify_json, error, started_at, completed_at, updated_at, gate_input)
			VALUES (?, ?, ?, ?, ?, 'pre-144 waiting row', 0, '[]', '', '', '', '', datetime('now'), '')`,
			id, runID, stepID, kind, status,
		); err != nil {
			t.Fatalf("insert synthetic workflow_run_steps row (run=%s, step=%s) at schema version 143: %v", runID, stepID, err)
		}
	}

	insertRun(gateRunID, "waiting_on_gate")
	insertStep(gateRunID+":"+gateStepID, gateRunID, gateStepID, "gate", "waiting_on_gate")
	insertRun(flexRunID, "waiting_on_flex")
	insertStep(flexRunID+":"+flexStepID, flexRunID, flexStepID, "flex", "waiting_on_flex")

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up (replay forward past migration 144 over real backup copy + synthetic waiting rows): %v", err)
	}

	runsAfter := collectRuns()
	stepsAfter := collectSteps()

	if len(runsAfter) != len(runsBefore)+2 {
		t.Fatalf("workflow_runs row count after migration 144 = %d, want %d (the %d real backup rows plus 2 synthetic waiting rows)",
			len(runsAfter), len(runsBefore)+2, len(runsBefore))
	}
	if len(stepsAfter) != len(stepsBefore)+2 {
		t.Fatalf("workflow_run_steps row count after migration 144 = %d, want %d (the %d real backup rows plus 2 synthetic waiting rows)",
			len(stepsAfter), len(stepsBefore)+2, len(stepsBefore))
	}

	runsByID := map[string]runRow{}
	for _, r := range runsAfter {
		runsByID[r.id] = r
	}
	for _, want := range runsBefore {
		got, ok := runsByID[want.id]
		if !ok {
			t.Errorf("real backup workflow_runs row (id=%s) missing after migration 144's rebuild", want.id)
			continue
		}
		if got.status != want.status {
			t.Errorf("real backup workflow_runs row (id=%s) status changed across migration 144's rebuild: before=%s after=%s", want.id, want.status, got.status)
		}
	}
	stepsByKey := map[string]stepRow{}
	for _, r := range stepsAfter {
		stepsByKey[r.workflowRunID+"/"+r.stepID] = r
	}
	for _, want := range stepsBefore {
		got, ok := stepsByKey[want.workflowRunID+"/"+want.stepID]
		if !ok {
			t.Errorf("real backup workflow_run_steps row (run=%s, step=%s) missing after migration 144's rebuild", want.workflowRunID, want.stepID)
			continue
		}
		if got.kind != want.kind || got.status != want.status {
			t.Errorf("real backup workflow_run_steps row (run=%s, step=%s) changed across migration 144's rebuild: before kind=%s status=%s, after kind=%s status=%s",
				want.workflowRunID, want.stepID, want.kind, want.status, got.kind, got.status)
		}
	}

	gateRun, ok := runsByID[gateRunID]
	if !ok || gateRun.status != "waiting_on_gate" {
		t.Errorf("synthetic pre-144 gate run (id=%s) missing or changed after migration 144's rebuild: %+v", gateRunID, gateRun)
	}
	flexRun, ok := runsByID[flexRunID]
	if !ok || flexRun.status != "waiting_on_flex" {
		t.Errorf("synthetic pre-144 flex run (id=%s) missing or changed after migration 144's rebuild: %+v", flexRunID, flexRun)
	}
	gateStep, ok := stepsByKey[gateRunID+"/"+gateStepID]
	if !ok || gateStep.kind != "gate" || gateStep.status != "waiting_on_gate" {
		t.Errorf("synthetic pre-144 gate step missing or changed after migration 144's rebuild: %+v", gateStep)
	}
	flexStep, ok := stepsByKey[flexRunID+"/"+flexStepID]
	if !ok || flexStep.kind != "flex" || flexStep.status != "waiting_on_flex" {
		t.Errorf("synthetic pre-144 flex step missing or changed after migration 144's rebuild: %+v", flexStep)
	}

	t.Logf("verified %d real workflow_runs + %d real workflow_run_steps rows, plus 2 pre-existing synthetic gate/flex waiting rows, all survive migration 144's table rebuild",
		len(runsBefore), len(stepsBefore))

	// And the new capability actually works against this real, migrated
	// copy: a waiting_on_loop row (both tables) and a loop_run_id value
	// can now be inserted where they couldn't before.
	if err := rs.CreateWorkflowRun(context.Background(), &WorkflowRunRow{ID: "post-144-loop-check", DefinitionName: "loop-check", Status: "waiting_on_loop"}); err != nil {
		t.Fatalf("CreateWorkflowRun(status=waiting_on_loop) on real backup copy: %v", err)
	}
	realLoopRunID := createTestLoopRunForMigration144(t, rs, "post-144-loop-check")
	if err := rs.UpsertWorkflowRunStep(context.Background(), &WorkflowRunStepRow{
		WorkflowRunID: "post-144-loop-check", StepID: "loop-step", Kind: "loop", Status: "waiting_on_loop",
		LoopRunID: &realLoopRunID,
	}); err != nil {
		t.Fatalf("UpsertWorkflowRunStep(status=waiting_on_loop, LoopRunID set) on real backup copy: %v", err)
	}
	got, err := rs.GetWorkflowRunStepByLoopRunID(context.Background(), realLoopRunID)
	if err != nil {
		t.Fatalf("GetWorkflowRunStepByLoopRunID on real backup copy: %v", err)
	}
	if got.StepID != "loop-step" {
		t.Errorf("GetWorkflowRunStepByLoopRunID on real backup copy returned step %q, want loop-step", got.StepID)
	}
}

// createTestLoopRunForMigration144 creates a minimal real goals -> loop_runs
// row chain and returns the new loop_runs.id — workflow_run_steps.loop_run_id
// REFERENCES loop_runs(id) with FK enforcement on (this Store always runs
// with PRAGMA foreign_keys = ON), so an arbitrary string won't satisfy it.
func createTestLoopRunForMigration144(t *testing.T, s *Store, definitionName string) string {
	t.Helper()
	ctx := context.Background()
	goal := &Goal{Intent: "migration 144 test goal for " + definitionName}
	if err := s.CreateGoal(ctx, goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	lr := &LoopRun{GoalID: goal.ID, DefinitionName: definitionName}
	if err := lr.SetBudget(Budget{}); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if err := s.CreateLoopRun(ctx, lr); err != nil {
		t.Fatalf("CreateLoopRun: %v", err)
	}
	return lr.ID
}
