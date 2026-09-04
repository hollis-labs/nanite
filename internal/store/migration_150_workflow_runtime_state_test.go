package store

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestMigration150AddsRuntimeStateWithoutReplacingProductRuns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("fs.Sub migrations: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("goose.NewProvider: %v", err)
	}
	if _, downErr := provider.DownTo(ctx, 149); downErr != nil {
		t.Fatalf("DownTo(149): %v", downErr)
	}

	legacy := &WorkflowRunRow{
		ID: "legacy-run-150", DefinitionName: "existing-workflow", Status: "running",
		InputJSON: `{"message":"preserve me"}`,
	}
	if createErr := s.CreateWorkflowRun(ctx, legacy); createErr != nil {
		t.Fatalf("CreateWorkflowRun before migration 150: %v", createErr)
	}
	if upsertErr := s.UpsertWorkflowRunStep(ctx, &WorkflowRunStepRow{
		WorkflowRunID: legacy.ID, StepID: "existing-step", Kind: "tool", Status: "pending",
	}); upsertErr != nil {
		t.Fatalf("UpsertWorkflowRunStep before migration 150: %v", upsertErr)
	}

	if _, upErr := provider.Up(ctx); upErr != nil {
		t.Fatalf("Up migration 150: %v", upErr)
	}
	assertGooseHasNothingPending(t, s)

	got, err := s.GetWorkflowRun(ctx, legacy.ID)
	if err != nil {
		t.Fatalf("GetWorkflowRun after migration 150: %v", err)
	}
	if got.DefinitionName != legacy.DefinitionName || got.Status != legacy.Status || got.InputJSON != legacy.InputJSON {
		t.Fatalf("legacy product run changed across migration 150: got=%+v want=%+v", got, legacy)
	}
	steps, err := s.ListWorkflowRunSteps(ctx, legacy.ID)
	if err != nil || len(steps) != 1 || steps[0].StepID != "existing-step" {
		t.Fatalf("legacy product steps after migration 150 = %+v, %v", steps, err)
	}

	var engineKind, engineVersion string
	var planDigest, runtimeStatus sql.NullString
	var generation int64
	if queryErr := s.DB.QueryRowContext(ctx, `
	SELECT engine_kind, engine_contract_version, plan_digest, runtime_status, runtime_generation
	FROM workflow_runs WHERE id = ?`, legacy.ID).Scan(
		&engineKind, &engineVersion, &planDigest, &runtimeStatus, &generation,
	); queryErr != nil {
		t.Fatalf("read migration 150 runtime columns: %v", queryErr)
	}
	if engineKind != "nanite_builtin_v1" || engineVersion != "" || planDigest.Valid || runtimeStatus.Valid || generation != 0 {
		t.Fatalf("legacy runtime defaults = kind=%q version=%q plan=%v status=%v generation=%d", engineKind, engineVersion, planDigest, runtimeStatus, generation)
	}

	for _, table := range []string{
		"workflow_plan_refs", "workflow_node_invocations", "workflow_attempts", "workflow_waits",
		"workflow_events", "workflow_external_operations", "workflow_retry_activations",
		"workflow_fanouts", "workflow_cancellation_intents", "workflow_replay_provenance",
		"workflow_control_decisions", "workflow_scheduler_resources", "workflow_memo_entries",
		"workflow_services", "workflow_reactors", "workflow_compensation_ledgers",
		"workflow_plan_node_projections",
	} {
		var count int
		if queryErr := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); queryErr != nil {
			t.Fatalf("look up table %s: %v", table, queryErr)
		}
		if count != 1 {
			t.Errorf("migration 150 table %s count = %d, want 1", table, count)
		}
	}

	// Exercise the real parent relationship before downgrade: workflow_runs is
	// the only run table, and dropping runtime history must first detach its
	// plan reference so foreign-key enforcement does not strand the rollback.
	if _, insertErr := s.DB.ExecContext(ctx, `
	INSERT INTO workflow_plan_refs(digest, plan_id, version, schema_version)
	VALUES ('sha256:test-150', 'plan-150', 'v1', 'workflow.execution-plan/v1')`); insertErr != nil {
		t.Fatalf("insert runtime plan: %v", insertErr)
	}
	if _, updateErr := s.DB.ExecContext(ctx, `
	UPDATE workflow_runs
	SET engine_kind='hadron_v0.5.0-beta.2', engine_contract_version='v0.5.0-beta.2',
	    plan_digest='sha256:test-150', runtime_status='pending', runtime_generation=1
	WHERE id=?`, legacy.ID); updateErr != nil {
		t.Fatalf("attach runtime identity to product run: %v", updateErr)
	}
	if _, downErr := provider.DownTo(ctx, 149); downErr != nil {
		t.Fatalf("DownTo(149) with an attached runtime plan: %v", downErr)
	}
	got, err = s.GetWorkflowRun(ctx, legacy.ID)
	if err != nil || got.DefinitionName != legacy.DefinitionName || got.Status != legacy.Status {
		t.Fatalf("product run after migration 150 downgrade = %+v, %v", got, err)
	}
}
