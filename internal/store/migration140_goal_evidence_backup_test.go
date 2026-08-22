package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestRealBackupGoalEvidenceMigrationAppliesCleanly is this migration's "no
// data lost, no false-positive rejection of already-live data" check --
// EXECUTION-PROCESS.md's schema-migration testing requirement and
// TASKS/loops/02-goal-evidence-schema.md's own Done-means both require the
// migration to apply cleanly against a real backup copy of the database,
// not just an empty fixture. Mirrors this package's established
// real-backup-test convention exactly (see
// TestRealBackupGoalsMigrationAppliesCleanly in
// migration138_goals_backup_test.go): brand-new table, so there's no
// pre-existing goal_evidence data to survive a rebuild -- what matters
// here is that CREATE TABLE goal_evidence (plus its two indexes and its
// FK against goals, which migration 138 already added to this same real
// backup copy) applies without error alongside every other real, populated
// table already in the backup, and that full GoalEvidence CRUD plus the
// EvaluateGoalEvidence/EvidenceSatisfiesGoal walk works on top of the
// resulting schema.
//
// Per EXECUTION-PROCESS.md, the real backup file is never opened in place
// -- it is copied into t.TempDir() (an absolute, per-test scratch path)
// before store.New ever touches it. Skips (rather than fails) when the
// backup isn't present on this machine, since it's a real operator
// artifact, not a checked-in fixture.
func TestRealBackupGoalEvidenceMigrationAppliesCleanly(t *testing.T) {
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
		t.Fatalf("open+migrate scratch copy of real backup db (migration 140 goal_evidence): %v", err)
	}
	defer rs.Close(context.

		// Sanity-check this is a real, populated backup (not an accidentally-
		// empty file) -- confirms the migration ran against a real prior schema
		// state with real application data alongside it, not a no-op empty DB
		// indistinguishable from the fixture-based tests in
		// goal_evidence_test.go.
		Background())

	var sessionCount int
	if err := rs.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&sessionCount); err != nil {
		t.Fatalf("sanity-check pre-existing sessions count on real backup copy: %v", err)
	}
	if sessionCount == 0 {
		t.Fatal("real backup sessions row count = 0, want > 0 (spot-check requires a real, populated backup, not an empty/synthetic db)")
	}
	t.Logf("real backup copy has %d pre-existing sessions rows; goal_evidence migration applied cleanly alongside them", sessionCount)

	// Full GoalEvidence CRUD plus the evidence-walk evaluation against the
	// migrated real-backup copy, on top of a real goals row (migration 138,
	// already applied earlier in this same migration chain).
	goal := &Goal{Intent: "real-backup goal_evidence migration smoke test"}
	if err := goal.SetAcceptanceCriteria([]string{"real_backup_smoke_test_passes"}); err != nil {
		t.Fatalf("SetAcceptanceCriteria: %v", err)
	}
	if err := rs.CreateGoal(ctx, goal); err != nil {
		t.Fatalf("CreateGoal against migrated real-backup copy: %v", err)
	}

	// Zero evidence -- not satisfied.
	metBefore, _, err := rs.EvidenceSatisfiesGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("EvidenceSatisfiesGoal (zero evidence) against migrated real-backup copy: %v", err)
	}
	if metBefore {
		t.Fatal("EvidenceSatisfiesGoal: got true, want false for zero recorded evidence")
	}

	evidence := &GoalEvidence{
		GoalID:       goal.ID,
		EvidenceType: GoalEvidenceTypeTestSuite,
		RefTable:     "workflow_run_steps",
		RefID:        "real-backup-step-1",
		Result:       "pass",
		Summary:      "real_backup_smoke_test_passes",
	}
	if err := rs.RecordGoalEvidence(ctx, evidence); err != nil {
		t.Fatalf("RecordGoalEvidence against migrated real-backup copy: %v", err)
	}

	got, err := rs.GetGoalEvidence(ctx, evidence.ID)
	if err != nil {
		t.Fatalf("GetGoalEvidence against migrated real-backup copy: %v", err)
	}
	if got.RefTable != "workflow_run_steps" || got.RefID != "real-backup-step-1" {
		t.Fatalf("GoalEvidence ref pointer round-trip against migrated real-backup copy: %+v", got)
	}

	metAfter, _, err := rs.EvidenceSatisfiesGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("EvidenceSatisfiesGoal (full coverage) against migrated real-backup copy: %v", err)
	}
	if !metAfter {
		t.Fatal("EvidenceSatisfiesGoal: got false, want true for full coverage against migrated real-backup copy")
	}

	if err := rs.DeleteGoalEvidence(ctx, evidence.ID); err != nil {
		t.Fatalf("DeleteGoalEvidence against migrated real-backup copy: %v", err)
	}
	if err := rs.DeleteGoal(ctx, goal.ID); err != nil {
		t.Fatalf("DeleteGoal against migrated real-backup copy: %v", err)
	}
}
