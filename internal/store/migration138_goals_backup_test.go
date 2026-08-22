package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestRealBackupGoalsMigrationAppliesCleanly is this migration's "no data
// lost, no false-positive rejection of already-live data" check --
// EXECUTION-PROCESS.md's schema-migration testing requirement and
// TASKS/loops/01-goals-schema.md's own Done-means both require the
// migration to apply cleanly against a real backup copy of the database,
// not just an empty fixture. Mirrors this package's established
// real-backup-test convention exactly (see
// TestRealBackupWorkflowRunStepsSurviveFlexKindMigration in
// migration_130_workflow_run_steps_flex_kind_test.go): brand-new table, so
// there's no pre-existing goals data to survive a rebuild -- what matters
// here is that CREATE TABLE goals (plus its two indexes) applies without
// error alongside every other real, populated table already in the
// backup, and that full Goal CRUD (including the parent_goal_id
// self-reference FK and the status-transition timestamp side effects)
// works on top of the resulting schema.
//
// Per EXECUTION-PROCESS.md, the real backup file is never opened in place
// -- it is copied into t.TempDir() (an absolute, per-test scratch path)
// before store.New ever touches it. Skips (rather than fails) when the
// backup isn't present on this machine, since it's a real operator
// artifact, not a checked-in fixture.
func TestRealBackupGoalsMigrationAppliesCleanly(t *testing.T) {
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
		t.Fatalf("open+migrate scratch copy of real backup db (migration 138 goals): %v", err)
	}
	defer rs.Close()

	// Sanity-check this is a real, populated backup (not an accidentally-
	// empty file) -- confirms the migration ran against a real prior
	// schema state with real application data alongside it, not a no-op
	// empty DB indistinguishable from the fixture-based tests in
	// goals_test.go.
	var sessionCount int
	if err := rs.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&sessionCount); err != nil {
		t.Fatalf("sanity-check pre-existing sessions count on real backup copy: %v", err)
	}
	if sessionCount == 0 {
		t.Fatal("real backup sessions row count = 0, want > 0 (spot-check requires a real, populated backup, not an empty/synthetic db)")
	}
	t.Logf("real backup copy has %d pre-existing sessions rows; goals migration applied cleanly alongside them", sessionCount)

	// Full Goal CRUD against the migrated real-backup copy, including the
	// parent_goal_id self-reference FK and UpdateGoalStatus's activated_at
	// side effect -- the same round-trip goals_test.go's
	// TestGoal_RoundTrip already covers against an empty fixture, run here
	// on top of a real prior schema instead.
	parent := &Goal{Intent: "real-backup migration smoke test"}
	if err := parent.SetDesiredState([]string{"migration applies cleanly to a real backup"}); err != nil {
		t.Fatalf("SetDesiredState: %v", err)
	}
	if err := rs.CreateGoal(ctx, parent); err != nil {
		t.Fatalf("CreateGoal against migrated real-backup copy: %v", err)
	}

	child := &Goal{Intent: "child of real-backup migration smoke test", ParentGoalID: parent.ID}
	if err := rs.CreateGoal(ctx, child); err != nil {
		t.Fatalf("CreateGoal (child, parent_goal_id FK) against migrated real-backup copy: %v", err)
	}

	got, err := rs.GetGoal(ctx, parent.ID)
	if err != nil {
		t.Fatalf("GetGoal against migrated real-backup copy: %v", err)
	}
	desired, err := got.DesiredState()
	if err != nil || len(desired) != 1 {
		t.Fatalf("DesiredState round-trip against migrated real-backup copy: %v (%v)", desired, err)
	}

	if err := rs.UpdateGoalStatus(ctx, parent.ID, GoalStatusActive); err != nil {
		t.Fatalf("UpdateGoalStatus against migrated real-backup copy: %v", err)
	}
	afterActive, err := rs.GetGoal(ctx, parent.ID)
	if err != nil || afterActive.ActivatedAt == "" {
		t.Fatalf("UpdateGoalStatus did not set activated_at against migrated real-backup copy: %+v (%v)", afterActive, err)
	}

	if err := rs.DeleteGoal(ctx, child.ID); err != nil {
		t.Fatalf("DeleteGoal (child) against migrated real-backup copy: %v", err)
	}
	if err := rs.DeleteGoal(ctx, parent.ID); err != nil {
		t.Fatalf("DeleteGoal (parent) against migrated real-backup copy: %v", err)
	}
}
