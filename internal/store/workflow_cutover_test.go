package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWorkflowCutoverFencesLeaseAndPersistsLegacyDispositionAudit(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 4, 17, 0, 0, 0, time.UTC)

	for _, run := range []struct {
		id     string
		status string
	}{
		{id: "legacy-cancel", status: "running"},
		{id: "legacy-fail", status: "waiting_on_gate"},
		{id: "legacy-drain", status: "running"},
		{id: "legacy-terminal", status: "completed"},
	} {
		createWorkflowCutoverRun(t, s, run.id, run.status, WorkflowEngineIdentityLegacy)
	}
	createWorkflowCutoverRun(t, s, "pilot-active", "running", WorkflowEngineIdentityPilot)
	createWorkflowCutoverRun(t, s, "shared-active", "running", WorkflowEngineIdentityShared)

	state, err := s.EnsureWorkflowCutoverState(ctx, now)
	if err != nil {
		t.Fatalf("EnsureWorkflowCutoverState: %v", err)
	}
	if !state.AllowsLegacyLaunch() || state.AllowsSharedLaunch() || state.TargetEngine != WorkflowEngineIdentityShared {
		t.Fatalf("initial cutover state = %+v", state)
	}
	lease, err := s.AcquireWorkflowCutoverLease(ctx, AcquireWorkflowCutoverLeaseRequest{
		Owner: "deploy-a", Token: "lease-a", ExpectedGeneration: 0,
		Now: now, Duration: time.Hour,
	})
	if err != nil || lease.Fence != 1 {
		t.Fatalf("AcquireWorkflowCutoverLease = %+v, %v", lease, err)
	}
	replayedLease, err := s.AcquireWorkflowCutoverLease(ctx, AcquireWorkflowCutoverLeaseRequest{
		Owner: "deploy-a", Token: "lease-a", ExpectedGeneration: 0,
		Now: now.Add(30 * time.Second), Duration: time.Hour,
	})
	if err != nil || replayedLease != lease {
		t.Fatalf("AcquireWorkflowCutoverLease replay = %+v, %v; want %+v", replayedLease, err, lease)
	}
	if _, acquireErr := s.AcquireWorkflowCutoverLease(ctx, AcquireWorkflowCutoverLeaseRequest{
		Owner: "deploy-b", Token: "lease-b", ExpectedGeneration: 0,
		Now: now.Add(time.Minute), Duration: time.Hour,
	}); !errors.Is(acquireErr, ErrWorkflowCutoverLeaseHeld) {
		t.Fatalf("second live lease error = %v", acquireErr)
	}

	mutation := workflowCutoverMutation(lease, now.Add(2*time.Minute))
	quiescing, err := s.TransitionWorkflowCutover(ctx, TransitionWorkflowCutoverRequest{
		Lease: mutation, To: WorkflowCutoverQuiescing,
	})
	if err != nil || quiescing.Generation != 1 || quiescing.Phase != WorkflowCutoverQuiescing ||
		quiescing.AllowsLegacyLaunch() || quiescing.AllowsSharedLaunch() {
		t.Fatalf("quiescing transition = %+v, %v", quiescing, err)
	}
	mutation = workflowCutoverMutation(quiescing, now.Add(3*time.Minute))
	dispositions, err := s.ListWorkflowLegacyRunDispositions(ctx, quiescing.Generation)
	if err != nil || len(dispositions) != 3 {
		t.Fatalf("legacy cohort = %+v, %v", dispositions, err)
	}
	for _, disposition := range dispositions {
		if disposition.RunID == "legacy-terminal" || disposition.RunID == "pilot-active" || disposition.RunID == "shared-active" {
			t.Fatalf("non-active/non-legacy run captured: %+v", disposition)
		}
	}
	if _, transitionErr := s.TransitionWorkflowCutover(ctx, TransitionWorkflowCutoverRequest{
		Lease: mutation, To: WorkflowCutoverSharedOnly,
	}); !errors.Is(transitionErr, ErrWorkflowCutoverLegacyRunsPending) {
		t.Fatalf("shared-only with pending cohort error = %v", transitionErr)
	}

	canceled := RecordWorkflowLegacyDispositionRequest{
		Lease: mutation, RunID: "legacy-cancel", Disposition: WorkflowLegacyDispositionCanceled,
		Actor: "operator", Reason: "explicit cutover cancellation",
	}
	gotCanceled, err := s.RecordWorkflowLegacyRunDisposition(ctx, canceled)
	if err != nil || gotCanceled.FinalStatus != "canceled" {
		t.Fatalf("canceled disposition = %+v, %v", gotCanceled, err)
	}
	if replay, replayErr := s.RecordWorkflowLegacyRunDisposition(ctx, canceled); replayErr != nil || replay != gotCanceled {
		t.Fatalf("canceled disposition replay = %+v, %v", replay, replayErr)
	}
	conflict := canceled
	conflict.Reason = "rewritten reason"
	if _, conflictErr := s.RecordWorkflowLegacyRunDisposition(ctx, conflict); !errors.Is(conflictErr, ErrWorkflowLegacyDispositionConflict) {
		t.Fatalf("rewritten final disposition error = %v", conflictErr)
	}

	failed, err := s.RecordWorkflowLegacyRunDisposition(ctx, RecordWorkflowLegacyDispositionRequest{
		Lease: mutation, RunID: "legacy-fail", Disposition: WorkflowLegacyDispositionFailed,
		Actor: "operator", Reason: "explicit cutover failure",
	})
	if err != nil || failed.FinalStatus != "failed" {
		t.Fatalf("failed disposition = %+v, %v", failed, err)
	}
	if statusErr := s.SetWorkflowRunStatus(ctx, "legacy-drain", "completed", "", now.Add(4*time.Minute)); statusErr != nil {
		t.Fatalf("finish drained legacy run: %v", statusErr)
	}
	drained, err := s.RecordWorkflowLegacyRunDisposition(ctx, RecordWorkflowLegacyDispositionRequest{
		Lease: mutation, RunID: "legacy-drain", Disposition: WorkflowLegacyDispositionDrained,
		Actor: "legacy-engine", Reason: "completed during quiesce",
	})
	if err != nil || drained.FinalStatus != "completed" {
		t.Fatalf("drained disposition = %+v, %v", drained, err)
	}

	assertWorkflowCutoverRunStatus(t, s, "legacy-cancel", "canceled", "skipped")
	assertWorkflowCutoverRunStatus(t, s, "legacy-fail", "failed", "failed")
	terminal, err := s.GetWorkflowRun(ctx, "legacy-terminal")
	if err != nil || terminal.Status != "completed" {
		t.Fatalf("terminal legacy run no longer queryable = %+v, %v", terminal, err)
	}
	if _, deleteErr := s.DB.ExecContext(ctx, `DELETE FROM workflow_legacy_run_dispositions WHERE run_id = 'legacy-cancel'`); deleteErr == nil {
		t.Fatal("legacy disposition audit delete unexpectedly succeeded")
	}

	sharedOnly, err := s.TransitionWorkflowCutover(ctx, TransitionWorkflowCutoverRequest{
		Lease: mutation, To: WorkflowCutoverSharedOnly,
	})
	if err != nil || sharedOnly.Generation != 2 || !sharedOnly.AllowsSharedLaunch() || sharedOnly.AllowsLegacyLaunch() {
		t.Fatalf("shared-only transition = %+v, %v", sharedOnly, err)
	}
	if _, err := s.ReleaseWorkflowCutoverLease(ctx, mutation); !errors.Is(err, ErrWorkflowCutoverGenerationStale) {
		t.Fatalf("pre-transition generation release error = %v", err)
	}
	if _, err := s.ReleaseWorkflowCutoverLease(ctx, workflowCutoverMutation(sharedOnly, now.Add(5*time.Minute))); err != nil {
		t.Fatalf("ReleaseWorkflowCutoverLease: %v", err)
	}
}

func TestWorkflowCutoverPersistsLateLegacyCohortOnBlockedSharedTransition(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 4, 18, 0, 0, 0, time.UTC)
	state, err := s.EnsureWorkflowCutoverState(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.AcquireWorkflowCutoverLease(ctx, AcquireWorkflowCutoverLeaseRequest{
		Owner: "deploy", Token: "token", ExpectedGeneration: state.Generation,
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
	createWorkflowCutoverRun(t, s, "late-legacy", "running", WorkflowEngineIdentityLegacy)
	mutation := workflowCutoverMutation(quiescing, now.Add(2*time.Minute))
	if _, transitionErr := s.TransitionWorkflowCutover(ctx, TransitionWorkflowCutoverRequest{
		Lease: mutation, To: WorkflowCutoverSharedOnly,
	}); !errors.Is(transitionErr, ErrWorkflowCutoverLegacyRunsPending) {
		t.Fatalf("late legacy transition error = %v", transitionErr)
	}
	cohort, err := s.ListWorkflowLegacyRunDispositions(ctx, quiescing.Generation)
	if err != nil || len(cohort) != 1 || cohort[0].RunID != "late-legacy" {
		t.Fatalf("persisted late cohort = %+v, %v", cohort, err)
	}
	if _, err := s.RecordWorkflowLegacyRunDisposition(ctx, RecordWorkflowLegacyDispositionRequest{
		Lease: mutation, RunID: "late-legacy", Disposition: WorkflowLegacyDispositionCanceled,
		Actor: "operator", Reason: "late legacy launch",
	}); err != nil {
		t.Fatalf("dispose late legacy run: %v", err)
	}
	if _, err := s.TransitionWorkflowCutover(ctx, TransitionWorkflowCutoverRequest{
		Lease: mutation, To: WorkflowCutoverSharedOnly,
	}); err != nil {
		t.Fatalf("shared transition after late disposition: %v", err)
	}
}

func TestWorkflowCutoverExpiredLeaseAdvancesFenceAndSupportsRollbackForward(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 4, 19, 0, 0, 0, time.UTC)
	if _, err := s.EnsureWorkflowCutoverState(ctx, now); err != nil {
		t.Fatal(err)
	}
	first, err := s.AcquireWorkflowCutoverLease(ctx, AcquireWorkflowCutoverLeaseRequest{
		Owner: "first", Token: "first-token", ExpectedGeneration: 0,
		Now: now, Duration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.AcquireWorkflowCutoverLease(ctx, AcquireWorkflowCutoverLeaseRequest{
		Owner: "second", Token: "second-token", ExpectedGeneration: 0,
		Now: now.Add(2 * time.Minute), Duration: time.Hour,
	})
	if err != nil || second.Fence != first.Fence+1 {
		t.Fatalf("second fenced lease = %+v, %v", second, err)
	}
	if _, renewErr := s.RenewWorkflowCutoverLease(ctx, workflowCutoverMutation(first, now.Add(3*time.Minute)), time.Hour); !errors.Is(renewErr, ErrWorkflowCutoverFenceStale) {
		t.Fatalf("stale fenced lease renew error = %v", renewErr)
	}
	quiescing, err := s.TransitionWorkflowCutover(ctx, TransitionWorkflowCutoverRequest{
		Lease: workflowCutoverMutation(second, now.Add(3*time.Minute)), To: WorkflowCutoverQuiescing,
	})
	if err != nil || quiescing.Generation != 1 {
		t.Fatalf("quiesce = %+v, %v", quiescing, err)
	}
	legacy, err := s.TransitionWorkflowCutover(ctx, TransitionWorkflowCutoverRequest{
		Lease: workflowCutoverMutation(quiescing, now.Add(4*time.Minute)), To: WorkflowCutoverLegacy,
	})
	if err != nil || legacy.Generation != 2 || !legacy.AllowsLegacyLaunch() {
		t.Fatalf("rollback to legacy = %+v, %v", legacy, err)
	}
	again, err := s.TransitionWorkflowCutover(ctx, TransitionWorkflowCutoverRequest{
		Lease: workflowCutoverMutation(legacy, now.Add(5*time.Minute)), To: WorkflowCutoverQuiescing,
	})
	if err != nil || again.Generation != 3 {
		t.Fatalf("forward quiesce = %+v, %v", again, err)
	}
}

func createWorkflowCutoverRun(t *testing.T, s *Store, id, status string, engine WorkflowEngineIdentity) {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateWorkflowRun(ctx, &WorkflowRunRow{
		ID: id, DefinitionName: "definition-" + id, Status: status, InputJSON: `{}`,
	}); err != nil {
		t.Fatalf("CreateWorkflowRun(%s): %v", id, err)
	}
	if err := s.UpsertWorkflowRunStep(ctx, &WorkflowRunStepRow{
		WorkflowRunID: id, StepID: "step", Kind: "tool", Status: "running",
	}); err != nil {
		t.Fatalf("UpsertWorkflowRunStep(%s): %v", id, err)
	}
	if engine != WorkflowEngineIdentityLegacy {
		if _, err := s.DB.ExecContext(ctx, `
UPDATE workflow_runs SET engine_kind = ?, engine_contract_version = ? WHERE id = ?`,
			engine.Kind, engine.ContractVersion, id); err != nil {
			t.Fatalf("set workflow engine identity %s: %v", id, err)
		}
	}
}

func assertWorkflowCutoverRunStatus(t *testing.T, s *Store, runID, runStatus, stepStatus string) {
	t.Helper()
	run, err := s.GetWorkflowRun(context.Background(), runID)
	if err != nil || run.Status != runStatus {
		t.Fatalf("run %s status = %+v, %v; want %s", runID, run, err, runStatus)
	}
	steps, err := s.ListWorkflowRunSteps(context.Background(), runID)
	if err != nil || len(steps) != 1 || steps[0].Status != stepStatus {
		t.Fatalf("run %s steps = %+v, %v; want status %s", runID, steps, err, stepStatus)
	}
}

func workflowCutoverMutation(state WorkflowCutoverState, now time.Time) WorkflowCutoverLeaseMutation {
	return WorkflowCutoverLeaseMutation{
		Owner: state.LeaseOwner, Token: state.LeaseToken,
		ExpectedGeneration: state.Generation, Fence: state.Fence, Now: now,
	}
}
