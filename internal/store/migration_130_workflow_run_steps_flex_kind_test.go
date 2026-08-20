package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrate130WidensWorkflowRunStepsKindCheck is the regression test for
// TASKS/teams/03-stepkindflex-schema.md: inserting a workflow_run_steps row
// with kind='flex' must succeed post-migration where it previously violated
// 051_agent_workflows.sql's original CHECK(kind IN ('llm','tool','gate')),
// and every pre-existing kind value must keep working unaffected by the
// table rebuild.
func TestMigrate130WidensWorkflowRunStepsKindCheck(t *testing.T) {
	s := newTestStore(t)

	if err := s.CreateWorkflowRun(&WorkflowRunRow{ID: "run-130", DefinitionName: "flex-check", Status: "running"}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}

	for _, kind := range []string{"llm", "tool", "gate", "flex"} {
		row := &WorkflowRunStepRow{
			WorkflowRunID: "run-130",
			StepID:        "step-" + kind,
			Kind:          kind,
			Status:        "pending",
		}
		if err := s.UpsertWorkflowRunStep(row); err != nil {
			t.Fatalf("UpsertWorkflowRunStep(kind=%q): %v", kind, err)
		}
	}

	// A genuinely unknown kind must still be rejected by the CHECK — the
	// widening is exactly (llm,tool,gate,flex), not "anything goes".
	badRow := &WorkflowRunStepRow{WorkflowRunID: "run-130", StepID: "step-bogus", Kind: "bogus", Status: "pending"}
	if err := s.UpsertWorkflowRunStep(badRow); err == nil {
		t.Fatal("UpsertWorkflowRunStep(kind=\"bogus\") succeeded, want CHECK violation")
	}

	steps, err := s.ListWorkflowRunSteps("run-130")
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps: %v", err)
	}
	gotKinds := map[string]bool{}
	for _, st := range steps {
		gotKinds[st.Kind] = true
	}
	for _, want := range []string{"llm", "tool", "gate", "flex"} {
		if !gotKinds[want] {
			t.Errorf("kind %q missing from persisted workflow_run_steps rows: %v", want, gotKinds)
		}
	}
	if len(steps) != 4 {
		t.Fatalf("len(steps) = %d, want 4 (the bogus-kind insert must not have landed)", len(steps))
	}

	assertGooseHasNothingPending(t, s)

	// Simulated restart: a second full migrate() must be a clean no-op,
	// and the flex row must still be there afterward.
	if err := s.migrate(); err != nil {
		t.Fatalf("re-migrate after 130 already applied: %v", err)
	}
	stepsAfter, err := s.ListWorkflowRunSteps("run-130")
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps after re-migrate: %v", err)
	}
	if len(stepsAfter) != 4 {
		t.Fatalf("len(stepsAfter) = %d, want 4 (rows must survive a re-migrate no-op)", len(stepsAfter))
	}
}

// TestMigrate130PreservesWorkflowRunStepsIndexes confirms the table rebuild
// (rename-recreate-copy, same pattern as
// 119_agent_reflex_dispatch_to_agent.sql) leaves both original indexes in
// place with their original definitions — in particular the UNIQUE
// (workflow_run_id, step_id) index task 03's Done-means explicitly calls
// out as needing to survive exactly.
func TestMigrate130PreservesWorkflowRunStepsIndexes(t *testing.T) {
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
		t.Fatal("idx_workflow_run_steps_run_step index missing after migration 130's rebuild")
	}
	if !strings.Contains(uniqueSQL, "UNIQUE") || !strings.Contains(uniqueSQL, "workflow_run_id") || !strings.Contains(uniqueSQL, "step_id") {
		t.Errorf("idx_workflow_run_steps_run_step sql = %q, want a UNIQUE index on (workflow_run_id, step_id)", uniqueSQL)
	}

	statusSQL, ok := found["idx_workflow_run_steps_status"]
	if !ok {
		t.Fatal("idx_workflow_run_steps_status index missing after migration 130's rebuild")
	}
	if !strings.Contains(statusSQL, "workflow_run_id") || !strings.Contains(statusSQL, "status") {
		t.Errorf("idx_workflow_run_steps_status sql = %q, want an index on (workflow_run_id, status)", statusSQL)
	}
}

// TestRealBackupWorkflowRunStepsSurviveFlexKindMigration is the "no data
// lost, no false-positive rejection of already-live data" check
// EXECUTION-PROCESS.md's schema-migration testing requirement and this
// task's own Done-means both require: every real, pre-existing
// workflow_run_steps row from an actual production backup must come out
// the other side of migration 130's table rebuild intact, and a new
// kind='flex' row must be insertable afterward.
//
// Per EXECUTION-PROCESS.md, the real backup file is never opened in place
// — it is copied into t.TempDir() (an absolute, per-test scratch path)
// before store.New ever touches it. Skips (rather than fails) when the
// backup isn't present on this machine, since it's a real operator
// artifact, not a checked-in fixture.
func TestRealBackupWorkflowRunStepsSurviveFlexKindMigration(t *testing.T) {
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

	// Collect every row fully (and close the cursor) before issuing any
	// further query, matching this package's established real-backup-test
	// convention (rs's connection pool is a single connection —
	// sqlitekit.OpenSingle — so a second query while this outer *sql.Rows
	// is still open would deadlock).
	type stepRow struct {
		workflowRunID, stepID, kind, status string
	}
	var realRows []stepRow
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
		realRows = append(realRows, r)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		t.Fatalf("iterate workflow_run_steps rows: %v", rowsErr)
	}

	if len(realRows) == 0 {
		t.Fatal("real backup workflow_run_steps row count = 0, want > 0 (spot-check requires real pre-existing rows, not an empty/synthetic db)")
	}

	validKinds := map[string]bool{"llm": true, "tool": true, "gate": true, "flex": true}
	for _, r := range realRows {
		if !validKinds[r.kind] {
			t.Errorf("real backup row (run=%s, step=%s) has kind=%q after migration — not one of llm/tool/gate/flex, table rebuild likely corrupted it", r.workflowRunID, r.stepID, r.kind)
		}
		if r.status == "" {
			t.Errorf("real backup row (run=%s, step=%s) has empty status after migration — likely lost across the table rebuild", r.workflowRunID, r.stepID)
		}
	}
	t.Logf("verified %d real workflow_run_steps rows from backup all survive migration 130's table rebuild with a valid kind/status", len(realRows))

	// And the new capability actually works against this real, migrated
	// copy: a flex-kind row can now be inserted where it couldn't before.
	if err := rs.CreateWorkflowRun(&WorkflowRunRow{ID: "post-130-flex-check", DefinitionName: "flex-check", Status: "running"}); err != nil {
		t.Fatalf("CreateWorkflowRun on real backup copy: %v", err)
	}
	if err := rs.UpsertWorkflowRunStep(&WorkflowRunStepRow{
		WorkflowRunID: "post-130-flex-check", StepID: "flex-step", Kind: "flex", Status: "pending",
	}); err != nil {
		t.Fatalf("UpsertWorkflowRunStep(kind=flex) on real backup copy: %v", err)
	}
}
