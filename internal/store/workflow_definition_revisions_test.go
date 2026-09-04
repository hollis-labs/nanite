package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestWorkflowDefinitionCatalogConcurrentIdempotencyAndHeadConflictTyping(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	peer, err := New(ctx, s.dbPath)
	if err != nil {
		t.Fatalf("open peer store: %v", err)
	}
	t.Cleanup(func() { _ = peer.Close(ctx) })
	at := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)
	revisionA := workflowDefinitionRevisionFixture("concurrent-a", "concurrent workflow", "workflow: concurrent-a", WorkflowEngineIdentityShared, at)
	revisionB := workflowDefinitionRevisionFixture("concurrent-b", revisionA.DefinitionName, "workflow: concurrent-b", WorkflowEngineIdentityShared, at.Add(time.Second))
	for _, revision := range []WorkflowDefinitionRevision{revisionA, revisionB} {
		persistWorkflowDefinitionPlanMaterial(t, s, revision)
	}

	stores := []*Store{s, peer}
	start := make(chan struct{})
	errorsByCall := make([]error, 12)
	var group sync.WaitGroup
	for index := range errorsByCall {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			_, errorsByCall[index] = stores[index%len(stores)].CreateWorkflowDefinitionRevision(ctx, revisionA)
		}(index)
	}
	close(start)
	group.Wait()
	for index, createErr := range errorsByCall {
		if createErr != nil {
			t.Fatalf("concurrent exact revision call %d: %v", index, createErr)
		}
	}
	if _, err := s.CreateWorkflowDefinitionRevision(ctx, revisionB); err != nil {
		t.Fatal(err)
	}

	start = make(chan struct{})
	errorsByCall = make([]error, 12)
	for index := range errorsByCall {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			_, errorsByCall[index] = stores[index%len(stores)].SetWorkflowDefinitionHead(ctx, SetWorkflowDefinitionHeadRequest{
				DefinitionName: revisionA.DefinitionName, RevisionID: revisionA.RevisionID,
				ExpectedGeneration: 0, At: at.Add(2 * time.Second),
			})
		}(index)
	}
	close(start)
	group.Wait()
	for index, headErr := range errorsByCall {
		if headErr != nil {
			t.Fatalf("concurrent identical head call %d: %v", index, headErr)
		}
	}

	otherA := workflowDefinitionRevisionFixture("head-race-a", "head race", "workflow: head-race-a", WorkflowEngineIdentityShared, at)
	otherB := workflowDefinitionRevisionFixture("head-race-b", otherA.DefinitionName, "workflow: head-race-b", WorkflowEngineIdentityShared, at.Add(time.Second))
	for _, revision := range []WorkflowDefinitionRevision{otherA, otherB} {
		persistWorkflowDefinitionPlanMaterial(t, s, revision)
		if _, err := s.CreateWorkflowDefinitionRevision(ctx, revision); err != nil {
			t.Fatal(err)
		}
	}
	start = make(chan struct{})
	conflicts := make([]error, 2)
	for index, revision := range []WorkflowDefinitionRevision{otherA, otherB} {
		group.Add(1)
		go func(index int, revision WorkflowDefinitionRevision) {
			defer group.Done()
			<-start
			_, conflicts[index] = stores[index].SetWorkflowDefinitionHead(ctx, SetWorkflowDefinitionHeadRequest{
				DefinitionName: revision.DefinitionName, RevisionID: revision.RevisionID,
				ExpectedGeneration: 0, At: at.Add(3 * time.Second),
			})
		}(index, revision)
	}
	close(start)
	group.Wait()
	succeeded := 0
	for _, headErr := range conflicts {
		if headErr == nil {
			succeeded++
			continue
		}
		if !errors.Is(headErr, ErrWorkflowDefinitionHeadConflict) {
			t.Fatalf("concurrent initial head error is not typed conflict: %v", headErr)
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent conflicting initial heads succeeded=%d errors=%v", succeeded, conflicts)
	}
}

func TestWorkflowDefinitionRevisionsAreExactImmutableAndDualIdentity(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	createdAt := time.Date(2026, 9, 4, 15, 0, 0, 0, time.UTC)

	pilot := workflowDefinitionRevisionFixture("revision-pilot", "review flow", "workflow: pilot", WorkflowEngineIdentityPilot, createdAt)
	persistWorkflowDefinitionPlanMaterial(t, s, pilot)
	got, err := s.CreateWorkflowDefinitionRevision(ctx, pilot)
	if err != nil {
		t.Fatalf("CreateWorkflowDefinitionRevision(pilot): %v", err)
	}
	if got.Engine != WorkflowEngineIdentityPilot || string(got.SourceContent) != string(pilot.SourceContent) {
		t.Fatalf("persisted pilot revision = %+v", got)
	}
	if _, replayErr := s.CreateWorkflowDefinitionRevision(ctx, pilot); replayErr != nil {
		t.Fatalf("exact revision replay: %v", replayErr)
	}

	shared := workflowDefinitionRevisionFixture("revision-shared", "review flow", "workflow: shared", WorkflowEngineIdentityShared, createdAt.Add(time.Second))
	persistWorkflowDefinitionPlanMaterial(t, s, shared)
	if _, createErr := s.CreateWorkflowDefinitionRevision(ctx, shared); createErr != nil {
		t.Fatalf("CreateWorkflowDefinitionRevision(shared): %v", createErr)
	}
	revisions, err := s.ListWorkflowDefinitionRevisions(ctx, "review flow")
	if err != nil || len(revisions) != 2 {
		t.Fatalf("ListWorkflowDefinitionRevisions = %+v, %v", revisions, err)
	}

	changed := pilot
	changed.CompiledPlanDigest = workflowSHA256Digest([]byte("different plan"))
	if _, err := s.CreateWorkflowDefinitionRevision(ctx, changed); !errors.Is(err, ErrWorkflowDefinitionRevisionConflict) {
		t.Fatalf("changed immutable revision error = %v, want conflict", err)
	}
	badSource := shared
	badSource.RevisionID = "revision-bad-digest"
	badSource.SourceDigest = workflowSHA256Digest([]byte("other bytes"))
	if _, err := s.CreateWorkflowDefinitionRevision(ctx, badSource); err == nil {
		t.Fatal("mismatched authored source digest unexpectedly accepted")
	}
	unsupported := shared
	unsupported.RevisionID = "revision-floating"
	unsupported.Engine = WorkflowEngineIdentity{Kind: WorkflowEngineIdentityShared.Kind, ContractVersion: "latest"}
	if _, err := s.CreateWorkflowDefinitionRevision(ctx, unsupported); err == nil {
		t.Fatal("floating engine contract unexpectedly accepted")
	}

	if _, err := s.DB.ExecContext(ctx, `UPDATE workflow_definition_revisions SET source_locator = 'moved' WHERE revision_id = ?`, pilot.RevisionID); err == nil {
		t.Fatal("immutable definition revision update unexpectedly succeeded")
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM workflow_definition_revisions WHERE revision_id = ?`, pilot.RevisionID); err == nil {
		t.Fatal("immutable definition revision delete unexpectedly succeeded")
	}
	if _, err := s.DB.ExecContext(ctx, `
INSERT INTO workflow_definition_revisions(
    revision_id, definition_name, source_locator, source_format, schema_version,
    source_digest, source_content, compiled_graph_digest, compiled_plan_digest,
    engine_kind, engine_contract_version, registered_by, created_at
) VALUES ('raw-invalid', 'review flow', 'fixture', 'yaml', 'v1', ?, X'01', ?, ?,
          'go_workflow_v0.1.0', 'v0.2.0', 'test', ?)`,
		workflowSHA256Digest([]byte{1}), workflowSHA256Digest([]byte("graph")),
		shared.CompiledPlanDigest, workflowCutoverTime(createdAt)); err == nil {
		t.Fatal("schema accepted unsupported engine identity")
	}
	missingPlan := workflowDefinitionRevisionFixture("revision-missing-plan", "review flow", "workflow: missing", WorkflowEngineIdentityShared, createdAt)
	if _, err := s.CreateWorkflowDefinitionRevision(ctx, missingPlan); !errors.Is(err, ErrWorkflowDefinitionRevisionConflict) {
		t.Fatalf("unrecoverable compiled plan error = %v, want conflict", err)
	}
}

func TestWorkflowDefinitionHeadUsesGenerationCASAndSupportsRollback(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 4, 16, 0, 0, 0, time.UTC)
	revision1 := workflowDefinitionRevisionFixture("revision-1", "worker reviewer gate", "workflow: one", WorkflowEngineIdentityPilot, at)
	revision2 := workflowDefinitionRevisionFixture("revision-2", revision1.DefinitionName, "workflow: two", WorkflowEngineIdentityShared, at.Add(time.Second))
	other := workflowDefinitionRevisionFixture("revision-other", "other flow", "workflow: other", WorkflowEngineIdentityShared, at)
	for _, revision := range []WorkflowDefinitionRevision{revision1, revision2, other} {
		persistWorkflowDefinitionPlanMaterial(t, s, revision)
		if _, err := s.CreateWorkflowDefinitionRevision(ctx, revision); err != nil {
			t.Fatalf("CreateWorkflowDefinitionRevision(%s): %v", revision.RevisionID, err)
		}
	}

	head, err := s.SetWorkflowDefinitionHead(ctx, SetWorkflowDefinitionHeadRequest{
		DefinitionName: revision1.DefinitionName, RevisionID: revision1.RevisionID,
		ExpectedGeneration: 0, At: at,
	})
	if err != nil || head.Generation != 1 || head.RevisionID != revision1.RevisionID {
		t.Fatalf("initial head = %+v, %v", head, err)
	}
	replay, err := s.SetWorkflowDefinitionHead(ctx, SetWorkflowDefinitionHeadRequest{
		DefinitionName: revision1.DefinitionName, RevisionID: revision1.RevisionID,
		ExpectedGeneration: 0, At: at.Add(time.Minute),
	})
	if err != nil || replay != head {
		t.Fatalf("idempotent head replay = %+v, %v; want %+v", replay, err, head)
	}
	if _, staleErr := s.SetWorkflowDefinitionHead(ctx, SetWorkflowDefinitionHeadRequest{
		DefinitionName: revision1.DefinitionName, RevisionID: revision2.RevisionID,
		ExpectedGeneration: 0, At: at.Add(time.Minute),
	}); !errors.Is(staleErr, ErrWorkflowDefinitionHeadConflict) {
		t.Fatalf("stale head CAS error = %v", staleErr)
	}
	headingShared, err := s.SetWorkflowDefinitionHead(ctx, SetWorkflowDefinitionHeadRequest{
		DefinitionName: revision1.DefinitionName, RevisionID: revision2.RevisionID,
		ExpectedGeneration: 1, At: at.Add(2 * time.Minute),
	})
	if err != nil || headingShared.Generation != 2 {
		t.Fatalf("shared head = %+v, %v", headingShared, err)
	}
	rolledBack, err := s.SetWorkflowDefinitionHead(ctx, SetWorkflowDefinitionHeadRequest{
		DefinitionName: revision1.DefinitionName, RevisionID: revision1.RevisionID,
		ExpectedGeneration: 2, At: at.Add(3 * time.Minute),
	})
	if err != nil || rolledBack.Generation != 3 || rolledBack.RevisionID != revision1.RevisionID {
		t.Fatalf("rolled-back head = %+v, %v", rolledBack, err)
	}
	active, activeHead, err := s.GetActiveWorkflowDefinitionRevision(ctx, revision1.DefinitionName)
	if err != nil || active.RevisionID != revision1.RevisionID || activeHead != rolledBack {
		t.Fatalf("active revision/head = %+v / %+v, %v", active, activeHead, err)
	}
	if _, err := s.SetWorkflowDefinitionHead(ctx, SetWorkflowDefinitionHeadRequest{
		DefinitionName: revision1.DefinitionName, RevisionID: other.RevisionID,
		ExpectedGeneration: 3, At: at.Add(4 * time.Minute),
	}); !errors.Is(err, ErrWorkflowDefinitionHeadConflict) {
		t.Fatalf("cross-definition head error = %v", err)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM workflow_definition_heads WHERE definition_name = ?`, revision1.DefinitionName); err == nil {
		t.Fatal("durable definition head delete unexpectedly succeeded")
	}
}

func persistWorkflowDefinitionPlanMaterial(t *testing.T, s *Store, revision WorkflowDefinitionRevision) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `
INSERT INTO workflow_plan_refs(digest, plan_id, version, schema_version)
VALUES (?, ?, 'v1', 'workflow.execution-plan/v1')`, revision.CompiledPlanDigest, "plan-"+revision.RevisionID); err != nil {
		t.Fatalf("insert workflow plan ref for %s: %v", revision.RevisionID, err)
	}
	if _, err := s.DB.ExecContext(ctx, `
INSERT INTO workflow_plan_materials(
    plan_digest, plan_json, visibility_json, source_locator, source_format,
    source_digest, source_content, product_definition_name,
    stepkind_catalog_json, stepkind_catalog_digest, verifier_catalog_json,
    verifier_catalog_digest, host_contract_json, host_contract_digest, created_at
) VALUES (?, '{}', '{}', ?, 'workflow', ?, ?, ?, '[]', ?, '[]', ?, '[]', ?, ?)`,
		revision.CompiledPlanDigest, revision.SourceLocator, revision.SourceDigest,
		revision.SourceContent, revision.DefinitionName,
		workflowSHA256Digest([]byte("stepkind:"+revision.RevisionID)),
		workflowSHA256Digest([]byte("verifier:"+revision.RevisionID)),
		workflowSHA256Digest([]byte("host:"+revision.RevisionID)),
		workflowCutoverTime(revision.CreatedAt)); err != nil {
		t.Fatalf("insert workflow plan material for %s: %v", revision.RevisionID, err)
	}
}

func workflowDefinitionRevisionFixture(id, name, source string, engine WorkflowEngineIdentity, at time.Time) WorkflowDefinitionRevision {
	content := []byte(source)
	return WorkflowDefinitionRevision{
		RevisionID:          id,
		DefinitionName:      name,
		SourceLocator:       "definitions/" + id + ".workflow.yaml",
		SourceFormat:        "workflow-yaml",
		SchemaVersion:       "workflow.definition/v1",
		SourceDigest:        workflowSHA256Digest(content),
		SourceContent:       content,
		CompiledGraphDigest: workflowSHA256Digest([]byte("graph:" + id)),
		CompiledPlanDigest:  workflowSHA256Digest([]byte("plan:" + id)),
		Engine:              engine,
		RegisteredBy:        "store-test",
		CreatedAt:           at,
	}
}
