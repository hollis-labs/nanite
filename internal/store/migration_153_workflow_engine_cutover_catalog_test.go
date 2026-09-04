package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func TestMigration153UpgradesPre150WithoutFabricatingLegacyPlan(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	provider := workflowMigration153Provider(t, s)
	if _, err := provider.DownTo(ctx, 149); err != nil {
		t.Fatalf("DownTo(149): %v", err)
	}
	if err := s.CreateWorkflowRun(ctx, &WorkflowRunRow{
		ID: "pre-150-legacy", DefinitionName: "authored legacy name", Status: "running",
		InputJSON: `{"request":"preserve"}`,
	}); err != nil {
		t.Fatalf("CreateWorkflowRun before migration 150: %v", err)
	}
	if err := s.UpsertWorkflowRunStep(ctx, &WorkflowRunStepRow{
		WorkflowRunID: "pre-150-legacy", StepID: "legacy-step", Kind: "tool", Status: "pending",
	}); err != nil {
		t.Fatalf("UpsertWorkflowRunStep before migration 150: %v", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up through migration 153: %v", err)
	}
	assertGooseHasNothingPending(t, s)
	run, err := s.GetWorkflowRun(ctx, "pre-150-legacy")
	if err != nil || run.DefinitionName != "authored legacy name" || run.Status != "running" || run.InputJSON != `{"request":"preserve"}` {
		t.Fatalf("pre-150 product row after migration = %+v, %v", run, err)
	}
	var engineKind, engineVersion string
	var planDigest, runtimeStatus sql.NullString
	var runtimeGeneration int64
	if err := s.DB.QueryRowContext(ctx, `
SELECT engine_kind, engine_contract_version, plan_digest, runtime_status, runtime_generation
FROM workflow_runs WHERE id = 'pre-150-legacy'`).Scan(
		&engineKind, &engineVersion, &planDigest, &runtimeStatus, &runtimeGeneration,
	); err != nil {
		t.Fatalf("read migrated runtime identity: %v", err)
	}
	if engineKind != WorkflowEngineIdentityLegacy.Kind || engineVersion != "" || planDigest.Valid || runtimeStatus.Valid || runtimeGeneration != 0 {
		t.Fatalf("migration fabricated legacy runtime material: kind=%q version=%q plan=%v status=%v generation=%d",
			engineKind, engineVersion, planDigest, runtimeStatus, runtimeGeneration)
	}
	for _, table := range []string{
		"workflow_engine_cutovers", "workflow_definition_revisions",
		"workflow_definition_heads", "workflow_legacy_run_dispositions",
	} {
		var count int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatalf("look up %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s count = %d, want 1", table, count)
		}
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM workflow_plan_refs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("legacy plan refs = %d, %v; migration must not fabricate a plan", count, err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM workflow_engine_cutovers`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("schema migration seeded cutover control state = %d, %v", count, err)
	}
}

func TestMigration153PreservesPilot152AndSupportsRollbackForward(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	provider := workflowMigration153Provider(t, s)
	if _, err := provider.DownTo(ctx, 152); err != nil {
		t.Fatalf("DownTo(152): %v", err)
	}
	at := time.Date(2026, 9, 4, 20, 0, 0, 0, time.UTC)
	revision := workflowDefinitionRevisionFixture(
		"pilot-revision-152", "pilot authored name", "workflow: pilot fixture",
		WorkflowEngineIdentityPilot, at,
	)
	if _, err := s.DB.ExecContext(ctx, `
INSERT INTO workflow_plan_refs(digest, plan_id, version, schema_version)
VALUES (?, 'pilot-plan', 'v1', 'workflow.execution-plan/v1')`, revision.CompiledPlanDigest); err != nil {
		t.Fatalf("insert pilot plan ref at migration 152: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx, `
INSERT INTO workflow_plan_materials(
    plan_digest, plan_json, visibility_json, source_locator, source_format,
    source_digest, source_content, product_definition_name,
    stepkind_catalog_json, stepkind_catalog_digest, verifier_catalog_json,
    verifier_catalog_digest, host_contract_json, host_contract_digest, created_at
) VALUES (?, '{}', '{}', ?, 'workflow', ?, ?, ?, '[]', ?, '[]', ?, '[]', ?, ?)`,
		revision.CompiledPlanDigest, revision.SourceLocator,
		revision.SourceDigest, revision.SourceContent, revision.DefinitionName,
		workflowSHA256Digest([]byte("stepkind catalog")), workflowSHA256Digest([]byte("verifier catalog")),
		workflowSHA256Digest([]byte("host contract")), workflowCutoverTime(at)); err != nil {
		t.Fatalf("insert pilot plan material at migration 152: %v", err)
	}
	if err := s.CreateWorkflowRun(ctx, &WorkflowRunRow{
		ID: "pilot-run-152", DefinitionName: revision.DefinitionName, Status: "running", InputJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `
UPDATE workflow_runs
SET engine_kind = ?, engine_contract_version = ?, plan_digest = ?,
    runtime_status = 'running', runtime_generation = 1
WHERE id = 'pilot-run-152'`, WorkflowEngineIdentityPilot.Kind,
		WorkflowEngineIdentityPilot.ContractVersion, revision.CompiledPlanDigest); err != nil {
		t.Fatalf("stamp pilot runtime identity: %v", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("Up migration 153 over pilot 152 fixture: %v", err)
	}
	var recoveryIndexSQL string
	if err := s.DB.QueryRowContext(ctx, `
SELECT sql FROM sqlite_master
WHERE type = 'index' AND name = 'idx_workflow_runs_embedded_runtime_recovery'`).Scan(&recoveryIndexSQL); err != nil {
		t.Fatalf("load dual-identity recovery index: %v", err)
	}
	if !containsAll(recoveryIndexSQL, WorkflowEngineIdentityPilot.Kind, WorkflowEngineIdentityShared.Kind) {
		t.Fatalf("recovery index does not pin both exact identities: %s", recoveryIndexSQL)
	}
	if _, err := s.CreateWorkflowDefinitionRevision(ctx, revision); err != nil {
		t.Fatalf("register preserved pilot definition revision: %v", err)
	}
	if _, err := s.SetWorkflowDefinitionHead(ctx, SetWorkflowDefinitionHeadRequest{
		DefinitionName: revision.DefinitionName, RevisionID: revision.RevisionID,
		ExpectedGeneration: 0, At: at,
	}); err != nil {
		t.Fatalf("select pilot definition revision: %v", err)
	}
	if migrateErr := s.migrate(ctx); migrateErr != nil {
		t.Fatalf("restart migration replay: %v", migrateErr)
	}
	active, head, err := s.GetActiveWorkflowDefinitionRevision(ctx, revision.DefinitionName)
	if err != nil || active.RevisionID != revision.RevisionID || head.Generation != 1 {
		t.Fatalf("catalog after restart = %+v / %+v, %v", active, head, err)
	}
	assertPilot152RuntimeIdentity(t, s, revision.CompiledPlanDigest)

	if _, err := provider.DownTo(ctx, 152); err != nil {
		t.Fatalf("DownTo(152) after catalog use: %v", err)
	}
	if _, err := s.GetWorkflowRun(ctx, "pilot-run-152"); err != nil {
		t.Fatalf("pilot product run lost on rollback: %v", err)
	}
	assertPilot152RuntimeIdentity(t, s, revision.CompiledPlanDigest)
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("forward migration 153 after rollback: %v", err)
	}
	if _, _, err := s.GetActiveWorkflowDefinitionRevision(ctx, revision.DefinitionName); !errors.Is(err, ErrWorkflowDefinitionHeadNotFound) {
		t.Fatalf("rolled-back catalog was fabricated on forward migration: %v", err)
	}
	assertPilot152RuntimeIdentity(t, s, revision.CompiledPlanDigest)
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}

func TestMigration153PreservesPostCutoverStateAcrossRestart(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 4, 21, 0, 0, 0, time.UTC)
	state, err := s.EnsureWorkflowCutoverState(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.AcquireWorkflowCutoverLease(ctx, AcquireWorkflowCutoverLeaseRequest{
		Owner: "deployment", Token: "deployment-token", ExpectedGeneration: state.Generation,
		Now: now, Duration: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	quiescing, err := s.TransitionWorkflowCutover(ctx, TransitionWorkflowCutoverRequest{
		Lease: workflowCutoverMutation(lease, now.Add(time.Minute)), To: WorkflowCutoverQuiescing,
	})
	if err != nil {
		t.Fatal(err)
	}
	sharedOnly, err := s.TransitionWorkflowCutover(ctx, TransitionWorkflowCutoverRequest{
		Lease: workflowCutoverMutation(quiescing, now.Add(2*time.Minute)), To: WorkflowCutoverSharedOnly,
	})
	if err != nil {
		t.Fatal(err)
	}
	if migrateErr := s.migrate(ctx); migrateErr != nil {
		t.Fatalf("restart migration replay: %v", migrateErr)
	}
	got, err := s.LoadWorkflowCutoverState(ctx)
	if err != nil || got.Phase != WorkflowCutoverSharedOnly || got.Generation != sharedOnly.Generation ||
		got.Fence != sharedOnly.Fence || got.TargetEngine != WorkflowEngineIdentityShared {
		t.Fatalf("post-cutover state after restart = %+v, %v; want %+v", got, err, sharedOnly)
	}
}

func workflowMigration153Provider(t *testing.T, s *Store) *goose.Provider {
	t.Helper()
	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("fs.Sub migrations: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("goose.NewProvider: %v", err)
	}
	return provider
}

func assertPilot152RuntimeIdentity(t *testing.T, s *Store, planDigest string) {
	t.Helper()
	var engineKind, engineVersion, gotPlanDigest, runtimeStatus string
	var generation int64
	if err := s.DB.QueryRowContext(context.Background(), `
SELECT engine_kind, engine_contract_version, plan_digest, runtime_status, runtime_generation
FROM workflow_runs WHERE id = 'pilot-run-152'`).Scan(
		&engineKind, &engineVersion, &gotPlanDigest, &runtimeStatus, &generation,
	); err != nil {
		t.Fatalf("load pilot 152 runtime identity: %v", err)
	}
	if engineKind != WorkflowEngineIdentityPilot.Kind || engineVersion != WorkflowEngineIdentityPilot.ContractVersion ||
		gotPlanDigest != planDigest || runtimeStatus != "running" || generation != 1 {
		t.Fatalf("pilot 152 identity changed: kind=%q version=%q plan=%q status=%q generation=%d",
			engineKind, engineVersion, gotPlanDigest, runtimeStatus, generation)
	}
}
