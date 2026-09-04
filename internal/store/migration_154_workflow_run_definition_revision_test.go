package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMigration154BindsRunToExactImmutableRevisionAcrossRestart(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	provider := workflowMigration153Provider(t, s)
	if _, err := provider.DownTo(ctx, 153); err != nil {
		t.Fatalf("DownTo(153): %v", err)
	}
	assertWorkflowRunRevisionColumn(t, s, false)
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("apply migration 154: %v", err)
	}
	assertWorkflowRunRevisionColumn(t, s, true)

	at := time.Date(2026, 9, 4, 22, 0, 0, 0, time.UTC)
	revision := workflowDefinitionRevisionFixture(
		"shared-run-revision", "authored shared definition", "workflow: exact shared source",
		WorkflowEngineIdentityShared, at,
	)
	persistWorkflowDefinitionPlanMaterial(t, s, revision)
	if _, err := s.CreateWorkflowDefinitionRevision(ctx, revision); err != nil {
		t.Fatalf("CreateWorkflowDefinitionRevision: %v", err)
	}
	resolved, err := s.ResolveWorkflowDefinitionRevision(
		ctx, revision.DefinitionName, revision.CompiledPlanDigest, revision.Engine,
	)
	if err != nil || resolved.RevisionID != revision.RevisionID {
		t.Fatalf("ResolveWorkflowDefinitionRevision = %+v, %v", resolved, err)
	}
	if createErr := s.CreateWorkflowRun(ctx, &WorkflowRunRow{
		ID: "shared-run-154", DefinitionName: revision.DefinitionName,
		DefinitionRevisionID: &revision.RevisionID, Status: "running",
	}); createErr != nil {
		t.Fatalf("CreateWorkflowRun: %v", createErr)
	}

	run, err := s.GetWorkflowRun(ctx, "shared-run-154")
	if err != nil || run.DefinitionRevisionID == nil || *run.DefinitionRevisionID != revision.RevisionID {
		t.Fatalf("GetWorkflowRun revision association = %+v, %v", run, err)
	}
	bound, err := s.GetWorkflowRunDefinitionRevision(ctx, run.ID)
	if err != nil || !equalWorkflowDefinitionRevision(bound, revision, true) {
		t.Fatalf("GetWorkflowRunDefinitionRevision = %+v, %v; want %+v", bound, err, revision)
	}
	if migrateErr := s.migrate(ctx); migrateErr != nil {
		t.Fatalf("restart migration replay: %v", migrateErr)
	}
	bound, err = s.GetWorkflowRunDefinitionRevision(ctx, run.ID)
	if err != nil || bound.RevisionID != revision.RevisionID {
		t.Fatalf("definition association after restart = %+v, %v", bound, err)
	}
}

func TestMigration154RollbackForwardDoesNotFabricateRunRevision(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	provider := workflowMigration153Provider(t, s)
	if err := s.CreateWorkflowRun(ctx, &WorkflowRunRow{
		ID: "unbound-shared-run", DefinitionName: "unrecoverable historical source",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 153); err != nil {
		t.Fatalf("DownTo(153): %v", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("forward migration 154: %v", err)
	}
	run, err := s.GetWorkflowRun(ctx, "unbound-shared-run")
	if err != nil || run.DefinitionRevisionID != nil {
		t.Fatalf("forward migration fabricated run revision: %+v, %v", run, err)
	}
	if _, err := s.GetWorkflowRunDefinitionRevision(ctx, run.ID); !errors.Is(err, ErrWorkflowDefinitionRevisionNotFound) {
		t.Fatalf("unbound historical run revision error = %v", err)
	}
}

func assertWorkflowRunRevisionColumn(t *testing.T, s *Store, want bool) {
	t.Helper()
	var count int
	if err := s.DB.QueryRow(`
SELECT COUNT(1) FROM pragma_table_info('workflow_runs')
WHERE name = 'definition_revision_id'`).Scan(&count); err != nil {
		t.Fatalf("inspect workflow_runs revision column: %v", err)
	}
	if got := count == 1; got != want {
		t.Fatalf("workflow_runs.definition_revision_id present=%v, want %v", got, want)
	}
}
