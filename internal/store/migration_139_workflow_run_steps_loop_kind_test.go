package store

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrate139WidensWorkflowRunStepsKindCheck is the regression test for
// TASKS/loops/06-stepkindloop-schema.md: inserting a workflow_run_steps row
// with kind='loop' must succeed post-migration where it previously violated
// 130/133's CHECK(kind IN ('llm','tool','gate','flex')), and every
// pre-existing kind value (including 'flex', added by 130) must keep
// working unaffected by the table rebuild.
func TestMigrate139WidensWorkflowRunStepsKindCheck(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateWorkflowRun(context.Background(), &WorkflowRunRow{ID: "run-139", DefinitionName: "loop-check", Status: "running"}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}

	for _, kind := range []string{"llm", "tool", "gate", "flex", "loop"} {
		row := &WorkflowRunStepRow{
			WorkflowRunID: "run-139",
			StepID:        "step-" + kind,
			Kind:          kind,
			Status:        "pending",
		}
		if err := s.UpsertWorkflowRunStep(context.Background(), row); err != nil {
			t.Fatalf("UpsertWorkflowRunStep(kind=%q): %v", kind, err)
		}
	}

	// A genuinely unknown kind must still be rejected by the CHECK — the
	// widening is exactly (llm,tool,gate,flex,loop), not "anything goes".
	badRow := &WorkflowRunStepRow{WorkflowRunID: "run-139", StepID: "step-bogus", Kind: "bogus", Status: "pending"}
	if err := s.UpsertWorkflowRunStep(context.Background(), badRow); err == nil {
		t.Fatal("UpsertWorkflowRunStep(kind=\"bogus\") succeeded, want CHECK violation")
	}

	steps, err := s.ListWorkflowRunSteps(context.Background(), "run-139")
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps: %v", err)
	}
	gotKinds := map[string]bool{}
	for _, st := range steps {
		gotKinds[st.Kind] = true
	}
	for _, want := range []string{"llm", "tool", "gate", "flex", "loop"} {
		if !gotKinds[want] {
			t.Errorf("kind %q missing from persisted workflow_run_steps rows: %v", want, gotKinds)
		}
	}
	if len(steps) != 5 {
		t.Fatalf("len(steps) = %d, want 5 (the bogus-kind insert must not have landed)", len(steps))
	}

	assertGooseHasNothingPending(t, s)

	// Simulated restart: a second full migrate() must be a clean no-op,
	// and the loop row must still be there afterward.
	if err := s.migrate(context.Background()); err != nil {
		t.Fatalf("re-migrate after 139 already applied: %v", err)
	}
	stepsAfter, err := s.ListWorkflowRunSteps(context.Background(), "run-139")
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps after re-migrate: %v", err)
	}
	if len(stepsAfter) != 5 {
		t.Fatalf("len(stepsAfter) = %d, want 5 (rows must survive a re-migrate no-op)", len(stepsAfter))
	}
}

// TestMigrate139PreservesWorkflowRunStepsIndexes confirms the table rebuild
// (rename-recreate-copy, same pattern as 130/133) leaves both original
// indexes in place with their original definitions — in particular the
// UNIQUE (workflow_run_id, step_id) index this task's Done-means (mirroring
// task 03/06's own) needs to survive exactly.
func TestMigrate139PreservesWorkflowRunStepsIndexes(t *testing.T) {
	s := newTestStore(t)

	rows, err := s.DB.Query(`SELECT name, sql FROM sqlite_master WHERE type = 'index' AND tbl_name = 'workflow_run_steps' AND sql IS NOT NULL ORDER BY name`)
	if err != nil {
		t.Fatalf("query sqlite_master indexes: %v", err)
	}
	defer rows.Close()

	found := map[string]string{}
	for rows.Next() {
		var name, sqlText string
		if err := rows.Scan(&name, &sqlText); err != nil {
			t.Fatalf("scan index row: %v", err)
		}
		found[name] = sqlText
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index rows: %v", err)
	}

	uniqueSQL, ok := found["idx_workflow_run_steps_run_step"]
	if !ok {
		t.Fatal("idx_workflow_run_steps_run_step index missing after migration 139's rebuild")
	}
	if !strings.Contains(uniqueSQL, "UNIQUE") || !strings.Contains(uniqueSQL, "workflow_run_id") || !strings.Contains(uniqueSQL, "step_id") {
		t.Errorf("idx_workflow_run_steps_run_step sql = %q, want a UNIQUE index on (workflow_run_id, step_id)", uniqueSQL)
	}

	statusSQL, ok := found["idx_workflow_run_steps_status"]
	if !ok {
		t.Fatal("idx_workflow_run_steps_status index missing after migration 139's rebuild")
	}
	if !strings.Contains(statusSQL, "workflow_run_id") || !strings.Contains(statusSQL, "status") {
		t.Errorf("idx_workflow_run_steps_status sql = %q, want an index on (workflow_run_id, status)", statusSQL)
	}
}

// TestRealBackupWorkflowRunStepsSurviveLoopKindMigration is the "no data
// lost, no false-positive rejection of already-live data" check
// EXECUTION-PROCESS.md's schema-migration testing requirement and this
// task's own Done-means both require: every real, pre-existing
// workflow_run_steps row from an actual production backup must survive
// migration 139's table rebuild intact, AND — since this task's Done-means
// specifically requires verifying preservation of a *flex*-kind row across
// the rebuild — a synthetic flex-kind row is inserted directly against the
// real backup copy immediately before migration 139 runs (goose UpTo(134)
// then UpTo(139), rather than one full migrate() in a single step), because
// the one real backup available on this machine
// (main.db.pre-execution-backup-20260818-132726) predates
// 130_workflow_run_steps_flex_kind.sql — its on-disk kind CHECK is still
// the original 3-value ('llm','tool','gate') and it has zero flex rows of
// its own (confirmed by direct inspection this session). Splitting the
// migration run at the 134/139 boundary lets this test exercise the exact
// scenario the Done-means describes — "a real backup copy... with real
// flex-kind rows already present" — using this machine's only real backup
// as the base, rather than skip the flex-preservation requirement entirely
// for lack of a backup that happens to already contain one.
//
// Per EXECUTION-PROCESS.md, the real backup file is never opened in place
// — it is copied into t.TempDir() (an absolute, per-test scratch path)
// before store.New ever touches it. Skips (rather than fails) when the
// backup isn't present on this machine, since it's a real operator
// artifact, not a checked-in fixture.
func TestRealBackupWorkflowRunStepsSurviveLoopKindMigration(t *testing.T) {
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
	defer rs.Close(context.

		// Collect every real, pre-existing row fully (and close the cursor)
		// before issuing any further query, matching this package's
		// established real-backup-test convention (rs's connection pool is a
		// single connection — sqlitekit.OpenSingle — so a second query while
		// this outer *sql.Rows is still open would deadlock).
		Background())

	type stepRow struct {
		workflowRunID, stepID, kind, status string
	}
	var realRowsBefore []stepRow
	rows, err := rs.DB.QueryContext(ctx, `SELECT workflow_run_id, step_id, kind, status FROM workflow_run_steps`)
	if err != nil {
		t.Fatalf("query workflow_run_steps on real backup copy: %v", err)
	}
	for rows.Next() {
		var r stepRow
		if err := rows.Scan(&r.workflowRunID, &r.stepID, &r.kind, &r.status); err != nil {
			rows.Close()
			t.Fatalf("scan workflow_run_steps row: %v", err)
		}
		realRowsBefore = append(realRowsBefore, r)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		t.Fatalf("iterate workflow_run_steps rows: %v", rowsErr)
	}
	if len(realRowsBefore) == 0 {
		t.Fatal("real backup workflow_run_steps row count = 0, want > 0 (spot-check requires real pre-existing rows, not an empty/synthetic db)")
	}
	realRowCount := len(realRowsBefore)
	t.Logf("real backup copy has %d pre-existing workflow_run_steps rows before the 134->139 boundary replay", realRowCount)

	// rs.New() already ran every migration, including 139, in one Up()
	// call — there was never a moment mid-run where the real backup's rows
	// coexisted with a flex row before 139's rebuild. Roll the schema back
	// to immediately before 139 (goose DownTo 134), insert a synthetic
	// flex-kind row (kind='flex' is legal at that schema version, per 130),
	// then replay forward past 139 — this is the actual "flex row already
	// present when the widening migration's rebuild runs" scenario the
	// Done-means describes.
	//
	// The flexRunID parent workflow_runs row is created *before* DownTo(134)
	// runs, while rs is still at head schema (via CreateWorkflowRun, which
	// is coupled to the current WorkflowRunRow shape) — TASKS/loops/
	// 05-workflow-runs-loop-scoping-columns.md's migration 143 added
	// workflow_runs.loop_run_id/loop_iteration after this task's own
	// migration 139 landed, so CreateWorkflowRun can no longer be called
	// against a schema rolled back below 143 (it unconditionally inserts
	// those two columns). ALTER TABLE ... DROP COLUMN (143's Down) removes
	// only those two columns, not the row itself, so the row survives
	// DownTo(134) intact and is still a valid FK target for the
	// workflow_run_steps insert below, which is unaffected either way
	// (migration 139 never touches workflow_runs).
	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("sub migrations fs: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, rs.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("construct goose provider: %v", err)
	}

	const flexRunID = "real-backup-pre-139-flex-run"
	const flexStepID = "flex-step-pre-139"
	if err := rs.CreateWorkflowRun(context.Background(), &WorkflowRunRow{ID: flexRunID, DefinitionName: "pre-139-flex-check", Status: "waiting_on_flex"}); err != nil {
		t.Fatalf("CreateWorkflowRun (pre-139 flex probe) on real backup copy: %v", err)
	}

	if _, err := provider.DownTo(ctx, 134); err != nil {
		t.Fatalf("goose DownTo 134 (reverse migration 139 on real backup copy): %v", err)
	}

	// Inserted via raw SQL, not the Go UpsertWorkflowRunStep helper: task
	// 09 (TASKS/loops/09-stepkindloop-executor-and-waiting-status.md)
	// widened workflowRunStepColumns to always include the new
	// workflow_run_steps.loop_run_id column it added in migration 144 —
	// that helper's INSERT statement is written against this worktree's
	// HEAD schema and fails against the intentionally-rolled-back schema
	// version 134 this test runs at here (no loop_run_id column exists
	// yet at that version). The row shape below matches 133's own rebuilt
	// CREATE TABLE exactly (the live schema at version 134 — no 'loop'
	// kind, no loop_run_id column, both added later by 139/144).
	if _, err := rs.DB.ExecContext(ctx, `
		INSERT INTO workflow_run_steps
			(id, workflow_run_id, step_id, kind, status, output, is_error, tool_calls_json, verify_json, error, started_at, completed_at, updated_at, gate_input)
		VALUES (?, ?, ?, 'flex', 'waiting_on_flex', ?, 0, '[]', '', '', '', '', datetime('now'), '')`,
		flexRunID+":"+flexStepID, flexRunID, flexStepID, "flex row inserted before migration 139's rebuild",
	); err != nil {
		t.Fatalf("insert synthetic flex row on real backup copy at schema version 134: %v", err)
	}

	// Replay all the way back to this worktree's head (not just UpTo(139))
	// so the "new capability" CreateWorkflowRun call below (which needs
	// migration 143's columns) succeeds too — none of 140-143 touch
	// workflow_run_steps, so this doesn't weaken the 139-rebuild-specific
	// assertions that follow.
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up (replay forward past migration 139 over real backup copy + synthetic flex row): %v", err)
	}

	// Every real, pre-existing row must still be there, with a valid kind
	// and non-empty status, plus the synthetic flex row makes
	// realRowCount+1.
	var realRowsAfter []stepRow
	rowsAfter, err := rs.DB.QueryContext(ctx, `SELECT workflow_run_id, step_id, kind, status FROM workflow_run_steps`)
	if err != nil {
		t.Fatalf("query workflow_run_steps after migration 139: %v", err)
	}
	for rowsAfter.Next() {
		var r stepRow
		if err := rowsAfter.Scan(&r.workflowRunID, &r.stepID, &r.kind, &r.status); err != nil {
			rowsAfter.Close()
			t.Fatalf("scan workflow_run_steps row after migration 139: %v", err)
		}
		realRowsAfter = append(realRowsAfter, r)
	}
	rowsAfterErr := rowsAfter.Err()
	rowsAfter.Close()
	if rowsAfterErr != nil {
		t.Fatalf("iterate workflow_run_steps rows after migration 139: %v", rowsAfterErr)
	}

	if len(realRowsAfter) != realRowCount+1 {
		t.Fatalf("row count after migration 139 rebuild = %d, want %d (the %d real backup rows plus the synthetic flex row) — the rebuild lost or duplicated rows",
			len(realRowsAfter), realRowCount+1, realRowCount)
	}

	validKinds := map[string]bool{"llm": true, "tool": true, "gate": true, "flex": true, "loop": true}
	byKey := map[string]stepRow{}
	for _, r := range realRowsAfter {
		if !validKinds[r.kind] {
			t.Errorf("row (run=%s, step=%s) has kind=%q after migration 139 — not one of llm/tool/gate/flex/loop, table rebuild likely corrupted it", r.workflowRunID, r.stepID, r.kind)
		}
		if r.status == "" {
			t.Errorf("row (run=%s, step=%s) has empty status after migration 139 — likely lost across the table rebuild", r.workflowRunID, r.stepID)
		}
		byKey[r.workflowRunID+"/"+r.stepID] = r
	}

	// Spot-check: every original real backup row is present, unchanged,
	// by (workflow_run_id, step_id, kind, status).
	for _, want := range realRowsBefore {
		got, ok := byKey[want.workflowRunID+"/"+want.stepID]
		if !ok {
			t.Errorf("real backup row (run=%s, step=%s, kind=%s) missing after migration 139's rebuild", want.workflowRunID, want.stepID, want.kind)
			continue
		}
		if got.kind != want.kind || got.status != want.status {
			t.Errorf("real backup row (run=%s, step=%s) changed across migration 139's rebuild: before kind=%s status=%s, after kind=%s status=%s",
				want.workflowRunID, want.stepID, want.kind, want.status, got.kind, got.status)
		}
	}

	// Spot-check the synthetic pre-existing flex row specifically — this
	// is the literal "flex-kind row already present, verify by row count
	// and a spot-check" requirement from this task's Done-means.
	var flexOutput string
	if err := rs.DB.QueryRowContext(ctx,
		`SELECT output FROM workflow_run_steps WHERE workflow_run_id = ? AND step_id = ?`,
		flexRunID, flexStepID,
	).Scan(&flexOutput); err != nil {
		t.Fatalf("read back pre-139 flex row after migration 139's rebuild: %v", err)
	}
	if flexOutput != "flex row inserted before migration 139's rebuild" {
		t.Errorf("pre-139 flex row's output column changed across the rebuild: got %q", flexOutput)
	}

	t.Logf("verified %d real workflow_run_steps rows plus 1 pre-existing synthetic flex row all survive migration 139's table rebuild with a valid kind/status", realRowCount)

	// And the new capability actually works against this real, migrated
	// copy: a loop-kind row can now be inserted where it couldn't before.
	if err := rs.CreateWorkflowRun(context.Background(), &WorkflowRunRow{ID: "post-139-loop-check", DefinitionName: "loop-check", Status: "running"}); err != nil {
		t.Fatalf("CreateWorkflowRun on real backup copy: %v", err)
	}
	if err := rs.UpsertWorkflowRunStep(context.Background(), &WorkflowRunStepRow{
		WorkflowRunID: "post-139-loop-check", StepID: "loop-step", Kind: "loop", Status: "pending",
	}); err != nil {
		t.Fatalf("UpsertWorkflowRunStep(kind=loop) on real backup copy: %v", err)
	}
}
