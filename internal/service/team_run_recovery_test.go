package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/workflowhost"
)

type switchTeamStateCollector struct{ fired atomic.Bool }

func (c *switchTeamStateCollector) Collect(context.Context, string, string, string) (reflexes.State, error) {
	if !c.fired.Load() {
		return reflexes.State{}, nil
	}
	return reflexes.State{Events: []reflexes.EventSignal{{EventType: "done"}}}, nil
}

type teamResolverHost struct{ resolver *WorkflowTeamStepResolver }

func (h teamResolverHost) ResolveWorkflowTeamStep(ctx context.Context, request workflowhost.TeamStepResolveRequest) (workflowhost.TeamStepResolveResult, error) {
	resolved, err := h.resolver.ResolveWorkflowTeamStep(ctx, request.WorkflowRunID, request.StepID, request.Config)
	return workflowhost.TeamStepResolveResult{
		Resolved: resolved.Resolved, Output: resolved.Output, ResponderReference: resolved.ResponderReference,
	}, err
}

func (h teamResolverHost) CompleteWorkflowTeamStep(ctx context.Context, runID, stepID string) error {
	return h.resolver.CompleteWorkflowTeamStep(ctx, runID, stepID)
}

func (h teamResolverHost) CancelWorkflowTeamRun(ctx context.Context, runID string) error {
	return h.resolver.CancelWorkflowTeamRun(ctx, runID)
}

type teamResolverHostWithoutAck struct{ resolver *WorkflowTeamStepResolver }

func (h teamResolverHostWithoutAck) ResolveWorkflowTeamStep(ctx context.Context, request workflowhost.TeamStepResolveRequest) (workflowhost.TeamStepResolveResult, error) {
	resolved, err := h.resolver.ResolveWorkflowTeamStep(ctx, request.WorkflowRunID, request.StepID, request.Config)
	return workflowhost.TeamStepResolveResult{
		Resolved: resolved.Resolved, Output: resolved.Output, ResponderReference: resolved.ResponderReference,
	}, err
}

func createSinglePhaseRecoveryTeam(t *testing.T, st *store.Store) (*store.Team, *store.AgentProfile) {
	t.Helper()
	profile := createTestRoleBoundAgent(t, st, "worker")
	team := &store.Team{Name: "Recovery Team"}
	if err := team.SetSlots([]store.TeamSlotDefinition{{
		Name: "worker", RoleSlug: "worker", Resolution: "fresh", ActivationMode: "singleton", Required: true,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := team.SetPhases([]store.TeamPhase{{
		ID: "work", Kind: "flex", ActiveSlots: []string{"worker"}, ExitTrigger: map[string]any{"event": "done"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTeam(t.Context(), team); err != nil {
		t.Fatal(err)
	}
	return team, profile
}

func TestTeamRunLaunchKeyReplaysOneRunAndRecoversMemberProjection(t *testing.T) {
	st, _, launcher := newTeamRunLauncherTestFixtures(t)
	team, _ := createSinglePhaseRecoveryTeam(t, st)
	request := TeamRunOverrides{IdempotencyKey: "team-recovery-one", Params: map[string]any{"objective": "ship"}}
	first, err := launcher.LaunchTeamRun(t.Context(), team.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := launcher.LaunchTeamRun(t.Context(), team.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.RunID == "" || second.RunID != first.RunID {
		t.Fatalf("run ids = %q/%q", first.RunID, second.RunID)
	}
	if _, conflictErr := launcher.LaunchTeamRun(t.Context(), team.ID, TeamRunOverrides{
		IdempotencyKey: request.IdempotencyKey, Params: map[string]any{"objective": "different"},
	}); !errors.Is(conflictErr, store.ErrTeamRunLaunchConflict) {
		t.Fatalf("conflicting replay error = %v", conflictErr)
	}

	members, err := st.ListTeamRunMembersByRun(t.Context(), first.RunID)
	if err != nil || len(members) != 1 {
		t.Fatalf("members = %+v, %v", members, err)
	}
	if _, deleteErr := st.DB.ExecContext(t.Context(), `DELETE FROM team_run_members WHERE id=?`, members[0].ID); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	if _, updateErr := st.DB.ExecContext(t.Context(), `UPDATE team_run_launches SET status='launched' WHERE idempotency_key=?`, request.IdempotencyKey); updateErr != nil {
		t.Fatal(updateErr)
	}
	restarted := NewTeamRunLauncher(st, launcher.launcher, launcher.durable)
	report := restarted.ReconcileTeamRuns(t.Context(), 10)
	if len(report.Failures) != 0 || report.Recovered == 0 {
		t.Fatalf("recovery report = %+v", report)
	}
	recovered, err := st.ListTeamRunMembersByRun(t.Context(), first.RunID)
	if err != nil || len(recovered) != 1 || recovered[0].ID != members[0].ID {
		t.Fatalf("recovered members = %+v, %v", recovered, err)
	}
	var runCount int
	if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workflow_runs WHERE id=?`, first.RunID).Scan(&runCount); err != nil || runCount != 1 {
		t.Fatalf("workflow run count = %d, %v", runCount, err)
	}
}

func TestConcurrentTeamRunLaunchKeyProvisionsDurableMemberOnce(t *testing.T) {
	st, _, launcher := newTeamRunLauncherTestFixtures(t)
	profile := createTestDurableCandidateAgent(t, st, "concurrent-durable-worker")
	profileID := profile.ID
	team := &store.Team{Name: "Concurrent Durable Team"}
	if err := team.SetSlots([]store.TeamSlotDefinition{{
		Name: "worker", Resolution: "durable", AgentID: &profileID, ActivationMode: "singleton", Required: true,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := team.SetPhases([]store.TeamPhase{{
		ID: "work", Kind: "flex", ActiveSlots: []string{"worker"}, ExitTrigger: map[string]any{"event": "done"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTeam(t.Context(), team); err != nil {
		t.Fatal(err)
	}

	const key = "team-concurrent-same-key"
	start := make(chan struct{})
	results := make(chan *agentworkflow.WorkflowResult, 2)
	errs := make(chan error, 2)
	var callers sync.WaitGroup
	callers.Add(2)
	for range 2 {
		go func() {
			defer callers.Done()
			<-start
			result, launchErr := launcher.LaunchTeamRun(t.Context(), team.ID, TeamRunOverrides{IdempotencyKey: key})
			results <- result
			errs <- launchErr
		}()
	}
	close(start)
	callers.Wait()
	close(results)
	close(errs)
	for launchErr := range errs {
		if launchErr != nil {
			t.Fatalf("concurrent launch error = %v", launchErr)
		}
	}
	var runID string
	for result := range results {
		if result == nil || result.RunID == "" {
			t.Fatalf("concurrent launch result = %+v", result)
		}
		if runID == "" {
			runID = result.RunID
		} else if result.RunID != runID {
			t.Fatalf("concurrent run ids = %q and %q", runID, result.RunID)
		}
	}

	instance, err := st.GetDurableAgentInstanceByProfileID(t.Context(), profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []string{
		store.DurableAgentEventStartRequested,
		store.DurableAgentEventSessionAttached,
		store.DurableAgentEventStartSucceeded,
	} {
		var count int
		if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM durable_agent_events WHERE instance_id=? AND event_type=?`, instance.ID, eventType).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s lifecycle events = %d, want 1", eventType, count)
		}
	}
	var sessions, runs int
	if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sessions WHERE id=?`, stableTeamRunSessionID(key, 0)).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workflow_runs WHERE id=?`, runID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 || runs != 1 {
		t.Fatalf("stable sessions/runs = %d/%d, want 1/1", sessions, runs)
	}
}

func TestTeamRunMemberIntentRecoversSessionProvisionedBeforeCrash(t *testing.T) {
	st, _, launcher := newTeamRunLauncherTestFixtures(t)
	team, profile := createSinglePhaseRecoveryTeam(t, st)
	const key = "team-member-provision-crash"
	overrides := TeamRunOverrides{IdempotencyKey: key}
	slots, err := team.Slots()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := launcher.planEagerResolution(t.Context(), team.ID, slots, overrides)
	if err != nil {
		t.Fatal(err)
	}
	planningJSON, err := json.Marshal(teamRunPlanningRecord{Team: *team, Plan: plan, Overrides: overrides})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := teamRunRequestDigest(team.ID, overrides)
	if err != nil {
		t.Fatal(err)
	}
	if _, beginErr := st.BeginTeamRunLaunch(t.Context(), store.TeamRunLaunch{
		IdempotencyKey: key, TeamID: team.ID, RequestDigest: digest, PlanningJSON: string(planningJSON),
	}); beginErr != nil {
		t.Fatal(beginErr)
	}
	sessionID := stableTeamRunSessionID(key, 0)
	_, err = st.CreateTeamRunMemberIntent(t.Context(), store.TeamRunMemberIntent{
		IdempotencyKey: key, Ordinal: 0, MemberID: stableTeamRunMemberID(key, 0), TeamID: team.ID,
		SlotName: "worker", AgentID: profile.ID, SessionID: sessionID, ProvisioningKind: "fresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a process death after the stable session committed but before
	// agent attachment and intent completion.
	if createErr := st.CreateSession(t.Context(), &store.Session{ID: sessionID, ContextType: "team_slot", ContextID: "worker"}); createErr != nil {
		t.Fatal(createErr)
	}
	report := launcher.ReconcileTeamRuns(t.Context(), 10)
	if len(report.Failures) != 0 || report.Recovered == 0 {
		t.Fatalf("autonomous planning recovery = %+v", report)
	}
	journal, err := st.GetTeamRunLaunch(t.Context(), key)
	if err != nil || journal.WorkflowRunID == "" {
		t.Fatalf("launch journal = %+v, %v", journal, err)
	}
	intent, err := st.GetTeamRunMemberIntent(t.Context(), key, 0)
	if err != nil || intent.Status != store.TeamRunMemberIntentProvisioned || intent.SessionID != sessionID {
		t.Fatalf("intent = %+v, %v", intent, err)
	}
	members, err := st.ListTeamRunMembersByRun(t.Context(), journal.WorkflowRunID)
	if err != nil || len(members) != 1 || members[0].SessionID != sessionID {
		t.Fatalf("members = %+v, %v", members, err)
	}
	var sessions int
	if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sessions WHERE id=?`, sessionID).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("stable sessions = %d, %v", sessions, err)
	}
}

type countingTeamStateCollector struct{ seen sync.Map }

func (c *countingTeamStateCollector) Collect(_ context.Context, sessionID, _, _ string) (reflexes.State, error) {
	c.seen.Store(sessionID, struct{}{})
	return reflexes.State{}, nil
}

func (c *countingTeamStateCollector) unique() int {
	count := 0
	c.seen.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

func TestTeamSignalReconciliationRotatesPastBoundedLimit(t *testing.T) {
	collector := &countingTeamStateCollector{}
	st := newTestWorkflowStore(t)
	resolver := NewWorkflowTeamStepResolver(st, collector)
	state, err := workflowhost.NewWorkflowStateStore(st)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := workflowhost.NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	engine.WithTeamStepHost(teamResolverHost{resolver: resolver})
	durable := NewDurableAgentService(st)
	wfLauncher := NewWorkflowLauncher(agentworkflow.NewRegistry(nil), engine, &fakeStepExecutor{}, durable)
	launcher := NewTeamRunLauncher(st, wfLauncher, durable)
	team, _ := createSinglePhaseRecoveryTeam(t, st)
	for i := 0; i < 3; i++ {
		if _, launchErr := launcher.LaunchTeamRun(t.Context(), team.ID, TeamRunOverrides{IdempotencyKey: fmt.Sprintf("fair-team-%d", i)}); launchErr != nil {
			t.Fatal(launchErr)
		}
	}
	first := launcher.ReconcileTeamRuns(t.Context(), 2)
	second := launcher.ReconcileTeamRuns(t.Context(), 2)
	if len(first.Failures) != 0 || len(second.Failures) != 0 || first.SignalsInspected != 2 || second.SignalsInspected != 2 {
		t.Fatalf("bounded reconciliation reports = %+v / %+v", first, second)
	}
	if collector.unique() != 3 {
		t.Fatalf("unique signal runs evaluated = %d, want all 3 across wrapped pages", collector.unique())
	}
}

type failingTeamRoutingInstaller struct{ seen sync.Map }

func (i *failingTeamRoutingInstaller) InstallTeamRunRouting(_ context.Context, runID, _ string) ([]string, error) {
	i.seen.Store(runID, struct{}{})
	return nil, errors.New("routing dependency unavailable")
}

func (i *failingTeamRoutingInstaller) unique() int {
	count := 0
	i.seen.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

func TestTeamRoutingReconciliationRotatesPastPersistentFailure(t *testing.T) {
	st, _, launcher := newTeamRunLauncherTestFixtures(t)
	team, _ := createSinglePhaseRecoveryTeam(t, st)
	for i := 0; i < 3; i++ {
		if _, launchErr := launcher.LaunchTeamRun(t.Context(), team.ID, TeamRunOverrides{IdempotencyKey: fmt.Sprintf("fair-routing-%d", i)}); launchErr != nil {
			t.Fatal(launchErr)
		}
	}
	failing := &failingTeamRoutingInstaller{}
	launcher.WithRoutingInstaller(failing)
	first := launcher.ReconcileTeamRuns(t.Context(), 2)
	second := launcher.ReconcileTeamRuns(t.Context(), 2)
	if first.RoutingInspected != 2 || second.RoutingInspected != 2 {
		t.Fatalf("bounded routing reports = %+v / %+v", first, second)
	}
	if failing.unique() != 3 {
		t.Fatalf("unique routing launches evaluated = %d, want all 3 despite persistent errors", failing.unique())
	}
}

func TestDurableTeamMemberIntentReplayDoesNotWakeOrCreateSessionTwice(t *testing.T) {
	st, _, launcher := newTeamRunLauncherTestFixtures(t)
	profile := createTestDurableCandidateAgent(t, st, "stable-durable-worker")
	profileID := profile.ID
	slot := store.TeamSlotDefinition{
		Name: "worker", Resolution: "durable", AgentID: &profileID, ActivationMode: "singleton", Required: true,
	}
	team := &store.Team{Name: "Durable Recovery Team"}
	if err := team.SetSlots([]store.TeamSlotDefinition{slot}); err != nil {
		t.Fatal(err)
	}
	if err := team.SetPhases([]store.TeamPhase{{
		ID: "work", Kind: "flex", ActiveSlots: []string{"worker"}, ExitTrigger: map[string]any{"event": "done"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTeam(t.Context(), team); err != nil {
		t.Fatal(err)
	}
	const key = "team-durable-member-crash"
	sessionID := stableTeamRunSessionID(key, 0)
	if _, err := st.CreateTeamRunMemberIntent(t.Context(), store.TeamRunMemberIntent{
		IdempotencyKey: key, Ordinal: 0, MemberID: stableTeamRunMemberID(key, 0), TeamID: team.ID,
		SlotName: slot.Name, AgentID: profile.ID, SessionID: sessionID, ProvisioningKind: "durable",
	}); err != nil {
		t.Fatal(err)
	}
	if _, gotSession, err := launcher.resolveDurableMember(t.Context(), slot, sessionID); err != nil || gotSession != sessionID {
		t.Fatalf("initial durable provision = %q, %v", gotSession, err)
	}
	instance, err := st.GetDurableAgentInstanceByProfileID(t.Context(), profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	var wakeEventsBefore int
	if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM durable_agent_events WHERE instance_id=? AND event_type=?`, instance.ID, store.DurableAgentEventStartRequested).Scan(&wakeEventsBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := launcher.LaunchTeamRun(t.Context(), team.ID, TeamRunOverrides{IdempotencyKey: key}); err != nil {
		t.Fatal(err)
	}
	var wakeEventsAfter, sessions int
	if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM durable_agent_events WHERE instance_id=? AND event_type=?`, instance.ID, store.DurableAgentEventStartRequested).Scan(&wakeEventsAfter); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sessions WHERE id=?`, sessionID).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if wakeEventsAfter != wakeEventsBefore || sessions != 1 {
		t.Fatalf("replay wake events/sessions = %d/%d, before wakes=%d", wakeEventsAfter, sessions, wakeEventsBefore)
	}
}

func TestTeamRoutingRecoveryIsIdempotentAfterMembersCommit(t *testing.T) {
	st, _, launcher := newTeamRunLauncherTestFixtures(t)
	team, _ := createSinglePhaseRecoveryTeam(t, st)
	if err := team.SetRouting(store.TeamRouting{
		Rules:           []store.TeamRoutingRule{{Name: "worker", Phrases: []string{"work"}, TargetSlot: "worker"}},
		CoordinatorSlot: "worker",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateTeam(t.Context(), team); err != nil {
		t.Fatal(err)
	}
	const key = "team-routing-crash"
	result, err := launcher.LaunchTeamRun(t.Context(), team.ID, TeamRunOverrides{IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	journal, err := st.GetTeamRunLaunch(t.Context(), key)
	if err != nil || journal.Status != store.TeamRunLaunchMembersReady {
		t.Fatalf("pre-routing journal = %+v, %v", journal, err)
	}
	routing := NewTeamRoutingService(st, nil, launcher)
	restarted := NewTeamRunLauncher(st, launcher.launcher, launcher.durable).WithRoutingInstaller(routing)
	report := restarted.ReconcileTeamRuns(t.Context(), 10)
	if len(report.Failures) != 0 || report.RoutingInspected != 1 {
		t.Fatalf("routing recovery = %+v", report)
	}
	journal, err = st.GetTeamRunLaunch(t.Context(), key)
	if err != nil || journal.Status != store.TeamRunLaunchRoutingReady {
		t.Fatalf("routing-ready journal = %+v, %v", journal, err)
	}
	var before int
	if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM agent_reflexes WHERE workflow_run_id=?`, result.RunID).Scan(&before); err != nil || before != 2 {
		t.Fatalf("routing rows = %d, %v", before, err)
	}
	if _, err := st.DB.ExecContext(t.Context(), `UPDATE agent_reflexes SET fired_count=7 WHERE id=(SELECT id FROM agent_reflexes WHERE workflow_run_id=? ORDER BY id LIMIT 1)`, result.RunID); err != nil {
		t.Fatal(err)
	}

	// Simulate a crash after idempotent routing rows committed but before the
	// readiness acknowledgement. Reconciliation must preserve those exact rows.
	if _, err := st.DB.ExecContext(t.Context(), `UPDATE team_run_launches SET status='members_ready' WHERE idempotency_key=?`, key); err != nil {
		t.Fatal(err)
	}
	report = restarted.ReconcileTeamRuns(t.Context(), 10)
	if len(report.Failures) != 0 || report.RoutingInspected != 1 {
		t.Fatalf("routing acknowledgement recovery = %+v", report)
	}
	var after int
	if err := st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM agent_reflexes WHERE workflow_run_id=?`, result.RunID).Scan(&after); err != nil || after != before {
		t.Fatalf("routing rows after replay = %d, want %d, err=%v", after, before, err)
	}
	var maxFired int
	if err := st.DB.QueryRowContext(t.Context(), `SELECT MAX(fired_count) FROM agent_reflexes WHERE workflow_run_id=?`, result.RunID).Scan(&maxFired); err != nil || maxFired != 7 {
		t.Fatalf("routing replay reset mutable counters: max fired=%d err=%v", maxFired, err)
	}
}

func TestTeamSignalStandDownWaitCloseCompletionCrashRecovery(t *testing.T) {
	collector := &switchTeamStateCollector{}
	st := newTestWorkflowStore(t)
	resolver := NewWorkflowTeamStepResolver(st, collector)
	state, err := workflowhost.NewWorkflowStateStore(st)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := workflowhost.NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	engine.WithTeamStepHost(teamResolverHostWithoutAck{resolver: resolver})
	durable := NewDurableAgentService(st)
	wfLauncher := NewWorkflowLauncher(agentworkflow.NewRegistry(nil), engine, &fakeStepExecutor{}, durable)
	launcher := NewTeamRunLauncher(st, wfLauncher, durable)
	team, _ := createSinglePhaseRecoveryTeam(t, st)
	waiting, err := launcher.LaunchTeamRun(t.Context(), team.ID, TeamRunOverrides{IdempotencyKey: "team-signal-crash"})
	if err != nil {
		t.Fatal(err)
	}
	collector.fired.Store(true)
	completed, err := wfLauncher.Resume(t.Context(), waiting.RunID)
	if err != nil || completed.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("resume = %+v, %v", completed, err)
	}
	receipt, err := st.GetTeamSignalResolution(t.Context(), waiting.RunID, "work")
	if err != nil || receipt.Status != store.TeamSignalResolutionPrepared {
		t.Fatalf("prepared receipt = %+v, %v", receipt, err)
	}
	report := launcher.ReconcileTeamRuns(t.Context(), 10)
	if len(report.Failures) != 0 || report.SignalsCompleted != 1 {
		t.Fatalf("completion recovery = %+v", report)
	}
	receipt, err = st.GetTeamSignalResolution(t.Context(), waiting.RunID, "work")
	if err != nil || receipt.Status != store.TeamSignalResolutionCompleted {
		t.Fatalf("completed receipt = %+v, %v", receipt, err)
	}
}

func TestTeamRunCancellationStopsMembersAndCadenceRepairsMissedAcknowledgement(t *testing.T) {
	t.Run("host acknowledgement", func(t *testing.T) {
		collector := &switchTeamStateCollector{}
		st := newTestWorkflowStore(t)
		resolver := NewWorkflowTeamStepResolver(st, collector)
		state, err := workflowhost.NewWorkflowStateStore(st)
		if err != nil {
			t.Fatal(err)
		}
		engine, err := workflowhost.NewEngine(state)
		if err != nil {
			t.Fatal(err)
		}
		engine.WithTeamStepHost(teamResolverHost{resolver: resolver})
		durable := NewDurableAgentService(st)
		wfLauncher := NewWorkflowLauncher(agentworkflow.NewRegistry(nil), engine, &fakeStepExecutor{}, durable)
		launcher := NewTeamRunLauncher(st, wfLauncher, durable)
		team, _ := createSinglePhaseRecoveryTeam(t, st)
		waiting, err := launcher.LaunchTeamRun(t.Context(), team.ID, TeamRunOverrides{IdempotencyKey: "team-cancel-ack"})
		if err != nil {
			t.Fatal(err)
		}
		canceled, err := wfLauncher.Cancel(t.Context(), waiting.RunID, "operator canceled")
		if err != nil || canceled.Status != agentworkflow.RunStatusCanceled {
			t.Fatalf("cancel = %+v, %v", canceled, err)
		}
		members, err := st.ListTeamRunMembersByRun(t.Context(), waiting.RunID)
		if err != nil || len(members) != 1 || members[0].Status != store.TeamRunMemberStatusStopped {
			t.Fatalf("members after cancel = %+v, %v", members, err)
		}
	})

	t.Run("cadence after crash", func(t *testing.T) {
		collector := &switchTeamStateCollector{}
		st := newTestWorkflowStore(t)
		resolver := NewWorkflowTeamStepResolver(st, collector)
		state, err := workflowhost.NewWorkflowStateStore(st)
		if err != nil {
			t.Fatal(err)
		}
		engine, err := workflowhost.NewEngine(state)
		if err != nil {
			t.Fatal(err)
		}
		// This host intentionally omits CancelWorkflowTeamRun, simulating a
		// process death after the canonical cancellation commit.
		engine.WithTeamStepHost(teamResolverHostWithoutAck{resolver: resolver})
		durable := NewDurableAgentService(st)
		wfLauncher := NewWorkflowLauncher(agentworkflow.NewRegistry(nil), engine, &fakeStepExecutor{}, durable)
		launcher := NewTeamRunLauncher(st, wfLauncher, durable)
		team, _ := createSinglePhaseRecoveryTeam(t, st)
		waiting, err := launcher.LaunchTeamRun(t.Context(), team.ID, TeamRunOverrides{IdempotencyKey: "team-cancel-crash"})
		if err != nil {
			t.Fatal(err)
		}
		if _, cancelErr := wfLauncher.Cancel(t.Context(), waiting.RunID, "operator canceled"); cancelErr != nil {
			t.Fatal(cancelErr)
		}
		members, err := st.ListTeamRunMembersByRun(t.Context(), waiting.RunID)
		if err != nil || len(members) != 1 || members[0].Status != store.TeamRunMemberStatusActive {
			t.Fatalf("pre-recovery members = %+v, %v", members, err)
		}
		report := launcher.ReconcileTeamRuns(t.Context(), 10)
		if len(report.Failures) != 0 || report.MembersStopped != 1 {
			t.Fatalf("cancellation recovery = %+v", report)
		}
		members, err = st.ListTeamRunMembersByRun(t.Context(), waiting.RunID)
		if err != nil || len(members) != 1 || members[0].Status != store.TeamRunMemberStatusStopped {
			t.Fatalf("recovered members = %+v, %v", members, err)
		}
	})
}

func TestTeamSignalProductionCadenceReconcilesLaterTrigger(t *testing.T) {
	collector := &switchTeamStateCollector{}
	st := newTestWorkflowStore(t)
	resolver := NewWorkflowTeamStepResolver(st, collector)
	state, err := workflowhost.NewWorkflowStateStore(st)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := workflowhost.NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	engine.WithTeamStepHost(teamResolverHost{resolver: resolver})
	durable := NewDurableAgentService(st)
	wfLauncher := NewWorkflowLauncher(agentworkflow.NewRegistry(nil), engine, &fakeStepExecutor{}, durable)
	launcher := NewTeamRunLauncher(st, wfLauncher, durable)
	team, _ := createSinglePhaseRecoveryTeam(t, st)
	waiting, err := launcher.LaunchTeamRun(t.Context(), team.ID, TeamRunOverrides{IdempotencyKey: "team-signal-cadence"})
	if err != nil || waiting.Status != agentworkflow.RunStatusWaitingOnFlex {
		t.Fatalf("launch = %+v, %v", waiting, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go launcher.RunReconciler(ctx, 5*time.Millisecond, 10, nil)
	collector.fired.Store(true)
	deadline := time.Now().Add(3 * time.Second)
	for {
		run, loadErr := st.GetWorkflowRun(t.Context(), waiting.RunID)
		if loadErr == nil && run.Status == string(agentworkflow.RunStatusCompleted) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Team run did not complete on cadence: run=%+v err=%v", run, loadErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	var receipt *store.TeamSignalResolution
	for {
		receipt, err = st.GetTeamSignalResolution(t.Context(), waiting.RunID, "work")
		if err == nil && receipt.Status == store.TeamSignalResolutionCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resolution receipt = %+v, %v", receipt, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	members, err := st.ListTeamRunMembersByRun(t.Context(), waiting.RunID)
	if err != nil || len(members) != 1 || members[0].Status != store.TeamRunMemberStatusStopped {
		t.Fatalf("stopped members = %+v, %v", members, err)
	}
}
