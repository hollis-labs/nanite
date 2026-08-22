package loop

// TASKS/loops/12-loop-run-tick-scheduled-trigger.md's own "Done means":
// "the real creation path for a loop_run_tick schedule is identified and
// either already exists ... or is built here." This file tests
// tick_schedule.go's scheduleLoopRunTick directly (unit coverage: insertion
// shape, the deterministic-id dedup property) and evaluateDecideAndAct's
// DecisionWait/DecisionEscalate call site (integration coverage: a WAIT
// decision creates the schedule row, an ESCALATE decision does not).
//
// evaluateDecideAndAct is called directly here rather than driving a full
// Run()/Resume() call through a real no-progress-triggering
// WorkflowDefinition -- mirrors this same test file's own established
// precedent (TestLoopEngine_DriveIterations_CallerContextDead_...) for
// exercising an internal engine method directly when the realistic public
// path to a given scenario (here: engineering a WorkflowDefinition whose
// real Verify-checked steps deterministically classify NO_PROGRESS) is
// substantially more complex than the one code path actually under test
// warrants. A real WorkflowRun is still launched first (via the package's
// own unexported e.launcher) so iterRow/launchResult carry a genuine,
// FK-valid workflow_run_id -- only StepResults is then hand-crafted to
// force the desired classifyIterationProgress outcome.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// --- scheduleLoopRunTick: direct unit coverage -----------------------------

func TestScheduleLoopRunTick_InsertsRow(t *testing.T) {
	ctx := context.Background()
	st, _, eng := newLoopEngineTestFixtures(t, &fakeStepExecutor{})
	profile := createTestLoopAgentProfile(t, st, "tick-agent-1")

	before := time.Now().UTC()
	if err := eng.scheduleLoopRunTick(ctx, "lr-tick-1", profile.ID); err != nil {
		t.Fatalf("scheduleLoopRunTick: %v", err)
	}

	row, err := st.GetAgentSchedule(ctx, loopRunTickScheduleID("lr-tick-1"))
	if err != nil {
		t.Fatalf("GetAgentSchedule: %v", err)
	}
	if row.AgentID != profile.ID {
		t.Errorf("AgentID = %q, want %q", row.AgentID, profile.ID)
	}
	if row.JobType != store.ScheduleJobTypeLoopRunTick {
		t.Errorf("JobType = %q, want %q", row.JobType, store.ScheduleJobTypeLoopRunTick)
	}
	if row.ScheduleKind != store.ScheduleKindOneShot {
		t.Errorf("ScheduleKind = %q, want %q", row.ScheduleKind, store.ScheduleKindOneShot)
	}
	if row.Status != store.ScheduleStatusActive {
		t.Errorf("Status = %q, want %q", row.Status, store.ScheduleStatusActive)
	}

	var payload struct {
		LoopRunID string `json:"loop_run_id"`
	}
	if err := json.Unmarshal([]byte(row.JobPayload), &payload); err != nil {
		t.Fatalf("decode JobPayload: %v", err)
	}
	if payload.LoopRunID != "lr-tick-1" {
		t.Errorf("payload.LoopRunID = %q, want lr-tick-1", payload.LoopRunID)
	}

	nextRun, err := time.Parse(time.RFC3339, row.NextRun)
	if err != nil {
		t.Fatalf("parse NextRun: %v", err)
	}
	wantAround := before.Add(defaultLoopRunTickPollInterval)
	if nextRun.Before(wantAround.Add(-time.Minute)) || nextRun.After(wantAround.Add(time.Minute)) {
		t.Errorf("NextRun = %v, want roughly now+%v (%v)", nextRun, defaultLoopRunTickPollInterval, wantAround)
	}
}

// TestScheduleLoopRunTick_DeterministicID_ReplacesNotDuplicates proves this
// file's own documented dedup property: a LoopRun that cycles through WAIT
// more than once replaces its own single outstanding tick schedule rather
// than accumulating one row per WAIT episode.
func TestScheduleLoopRunTick_DeterministicID_ReplacesNotDuplicates(t *testing.T) {
	ctx := context.Background()
	st, _, eng := newLoopEngineTestFixtures(t, &fakeStepExecutor{})
	profile := createTestLoopAgentProfile(t, st, "tick-agent-2")

	if err := eng.scheduleLoopRunTick(ctx, "lr-tick-2", profile.ID); err != nil {
		t.Fatalf("scheduleLoopRunTick (1st): %v", err)
	}
	first, err := st.GetAgentSchedule(ctx, loopRunTickScheduleID("lr-tick-2"))
	if err != nil {
		t.Fatalf("GetAgentSchedule (1st): %v", err)
	}

	if err := eng.scheduleLoopRunTick(ctx, "lr-tick-2", profile.ID); err != nil {
		t.Fatalf("scheduleLoopRunTick (2nd): %v", err)
	}
	second, err := st.GetAgentSchedule(ctx, loopRunTickScheduleID("lr-tick-2"))
	if err != nil {
		t.Fatalf("GetAgentSchedule (2nd): %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected the same deterministic id across calls, got %q then %q", first.ID, second.ID)
	}

	all, err := st.ListAgentSchedules(ctx, profile.ID)
	if err != nil {
		t.Fatalf("ListAgentSchedules: %v", err)
	}
	count := 0
	for _, row := range all {
		if row.JobType == store.ScheduleJobTypeLoopRunTick {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("loop_run_tick schedule row count = %d, want exactly 1 (INSERT OR REPLACE should dedup)", count)
	}
}

func TestScheduleLoopRunTick_MissingAgentProfileID_ReturnsError(t *testing.T) {
	ctx := context.Background()
	_, _, eng := newLoopEngineTestFixtures(t, &fakeStepExecutor{})
	if err := eng.scheduleLoopRunTick(ctx, "lr-tick-3", ""); err == nil {
		t.Fatalf("scheduleLoopRunTick: expected error for empty agent_profile_id")
	}
}

// --- evaluateDecideAndAct's DecisionWait/DecisionEscalate call site --------

// launchRealIterationForTickTest launches one real, trivial one-llm-step
// WorkflowRun (so the returned RunID is genuinely FK-valid for
// CompleteLoopRunIteration) and creates its matching, in-flight
// loop_run_iterations row -- the same ordering launchIteration itself uses
// (Launch, then CreateLoopRunIteration).
func launchRealIterationForTickTest(t *testing.T, ctx context.Context, st *store.Store, registry *agentworkflow.Registry, eng *LoopEngine, lr *store.LoopRun, profileID string) *store.LoopRunIteration {
	t.Helper()
	wf := oneStepIterationDefinition("tick-test-iteration-" + lr.ID)
	if err := registry.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}
	launchResult, err := eng.launcher.Launch(ctx, service.WorkflowLaunchRequest{
		WorkflowName:   wf.Name,
		AgentProfileID: profileID,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	iterRow := &store.LoopRunIteration{LoopRunID: lr.ID, IterationNumber: 1, WorkflowRunID: launchResult.RunID}
	if err := st.CreateLoopRunIteration(ctx, iterRow); err != nil {
		t.Fatalf("CreateLoopRunIteration: %v", err)
	}
	return iterRow
}

// mixedVerifyLaunchResult crafts a WorkflowLaunchResult whose StepResults
// deterministically classify NO_PROGRESS via classifyIterationProgress
// (one passed verify, one failed -- "failed > 0 && passed > 0" branch) --
// independent of what the real launched WorkflowRun's own steps actually
// did, since evaluateDecideAndAct only ever reads this in-memory
// StepResults map, not the DB's own workflow_run_steps rows.
func mixedVerifyLaunchResult(runID string) *service.WorkflowLaunchResult {
	return &service.WorkflowLaunchResult{
		RunID:  runID,
		Status: agentworkflow.RunStatusCompleted,
		StepResults: map[string]agentworkflow.StepResult{
			"a": {VerifyResult: &agentworkflow.VerifyResult{Passed: true}},
			"b": {VerifyResult: &agentworkflow.VerifyResult{Passed: false, Reason: "not yet"}},
		},
	}
}

func TestEvaluateDecideAndAct_DecisionWait_SchedulesLoopRunTick(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{
		llmFunc: func(req agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
			if req.SystemPrompt == reasoningSystemPrompt {
				return agentworkflow.LLMStepResult{Text: `{"decision":"wait","reason":"waiting on an external condition"}`}, nil
			}
			return agentworkflow.LLMStepResult{Text: "ok"}, nil
		},
	}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "tick-wait-agent")

	goal := store.Goal{Intent: "must not complete yet"}
	if err := goal.SetAcceptanceCriteria([]string{"unmet criterion"}); err != nil {
		t.Fatalf("SetAcceptanceCriteria: %v", err)
	}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	policy := ContinuationPolicy{Provider: "anthropic", Model: "claude"}
	cfg := loopRunPersistentConfig{ContinuationPolicy: policy, AgentProfileID: profile.ID}
	cfgJSON, err := encodeLoopRunPersistentConfig(cfg)
	if err != nil {
		t.Fatalf("encodeLoopRunPersistentConfig: %v", err)
	}
	lr := &store.LoopRun{GoalID: goal.ID, DefinitionName: "unused-in-this-test", ContinuationPolicyJSON: cfgJSON}
	if err := lr.SetBudget(store.Budget{MaxNoProgressIterations: 1}); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if err := st.CreateLoopRun(ctx, lr); err != nil {
		t.Fatalf("CreateLoopRun: %v", err)
	}

	iterRow := launchRealIterationForTickTest(t, ctx, st, registry, eng, lr, profile.ID)
	launchResult := mixedVerifyLaunchResult(iterRow.WorkflowRunID)
	budget, err := lr.Budget()
	if err != nil {
		t.Fatalf("Budget: %v", err)
	}

	result, terminal, _, err := eng.evaluateDecideAndAct(ctx, exec, lr, &goal, iterRow, launchResult, policy, budget, nil)
	if err != nil {
		t.Fatalf("evaluateDecideAndAct: %v", err)
	}
	if !terminal {
		t.Fatalf("terminal = false, want true (WAIT pauses the loop)")
	}
	if result.LastDecision.Kind != DecisionWait {
		t.Fatalf("LastDecision.Kind = %q, want wait (test setup should have driven the reasoning fallback to WAIT)", result.LastDecision.Kind)
	}
	if result.Status != store.LoopRunStatusWaitingOnEscalation {
		t.Fatalf("Status = %q, want waiting_on_escalation", result.Status)
	}

	row, err := st.GetAgentSchedule(ctx, loopRunTickScheduleID(lr.ID))
	if err != nil {
		t.Fatalf("GetAgentSchedule: expected a loop_run_tick schedule row to exist after a WAIT decision, got error: %v", err)
	}
	if row.JobType != store.ScheduleJobTypeLoopRunTick {
		t.Errorf("JobType = %q, want %q", row.JobType, store.ScheduleJobTypeLoopRunTick)
	}
	if row.AgentID != profile.ID {
		t.Errorf("AgentID = %q, want %q", row.AgentID, profile.ID)
	}
	var payload struct {
		LoopRunID string `json:"loop_run_id"`
	}
	if err := json.Unmarshal([]byte(row.JobPayload), &payload); err != nil {
		t.Fatalf("decode JobPayload: %v", err)
	}
	if payload.LoopRunID != lr.ID {
		t.Errorf("payload.LoopRunID = %q, want %q", payload.LoopRunID, lr.ID)
	}
}

// TestEvaluateDecideAndAct_DecisionEscalate_DoesNotScheduleLoopRunTick is
// the negative case: an ESCALATE decision (budget exhaustion here, the
// cheapest deterministic way to force it -- no LLM stub needed) must NOT
// create a loop_run_tick schedule. ESCALATE means "needs a human," resolved
// via an operator action or a direct Resume call, not an automatic re-poll
// -- see tick_schedule.go's own package doc comment.
func TestEvaluateDecideAndAct_DecisionEscalate_DoesNotScheduleLoopRunTick(t *testing.T) {
	ctx := context.Background()
	exec := &fakeStepExecutor{}
	st, registry, eng := newLoopEngineTestFixtures(t, exec)
	profile := createTestLoopAgentProfile(t, st, "tick-escalate-agent")

	goal := store.Goal{Intent: "must not complete yet"}
	if err := goal.SetAcceptanceCriteria([]string{"unmet criterion"}); err != nil {
		t.Fatalf("SetAcceptanceCriteria: %v", err)
	}
	if err := st.CreateGoal(ctx, &goal); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	policy := ContinuationPolicy{}
	cfg := loopRunPersistentConfig{ContinuationPolicy: policy, AgentProfileID: profile.ID}
	cfgJSON, err := encodeLoopRunPersistentConfig(cfg)
	if err != nil {
		t.Fatalf("encodeLoopRunPersistentConfig: %v", err)
	}
	lr := &store.LoopRun{GoalID: goal.ID, DefinitionName: "unused-in-this-test", ContinuationPolicyJSON: cfgJSON}
	if err := lr.SetBudget(store.Budget{MaxIterations: 1, OnExhausted: store.LoopRunOnExhaustedEscalate}); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if err := st.CreateLoopRun(ctx, lr); err != nil {
		t.Fatalf("CreateLoopRun: %v", err)
	}

	iterRow := launchRealIterationForTickTest(t, ctx, st, registry, eng, lr, profile.ID)
	// Clean completion (no mixed verify needed) -- budget exhaustion alone
	// (MaxIterations: 1, history len becomes 1) forces ESCALATE regardless
	// of progress classification.
	launchResult := &service.WorkflowLaunchResult{RunID: iterRow.WorkflowRunID, Status: agentworkflow.RunStatusCompleted}
	budget, err := lr.Budget()
	if err != nil {
		t.Fatalf("Budget: %v", err)
	}

	// exec.calls already reflects launchRealIterationForTickTest's own real
	// one-llm-step iteration call above -- capture it here so the assertion
	// below is about evaluateDecideAndAct's OWN behavior (no additional
	// reasoning-fallback call), not the total across both calls.
	callsBeforeDecide := exec.calls

	result, terminal, _, err := eng.evaluateDecideAndAct(ctx, exec, lr, &goal, iterRow, launchResult, policy, budget, nil)
	if err != nil {
		t.Fatalf("evaluateDecideAndAct: %v", err)
	}
	if !terminal {
		t.Fatalf("terminal = false, want true (ESCALATE pauses the loop)")
	}
	if result.LastDecision.Kind != DecisionEscalate {
		t.Fatalf("LastDecision.Kind = %q, want escalate (test setup should have exhausted the budget)", result.LastDecision.Kind)
	}
	if exec.calls != callsBeforeDecide {
		t.Fatalf("exec.calls grew by %d during evaluateDecideAndAct, want 0 -- budget exhaustion is deterministic, no reasoning-fallback LLM call expected", exec.calls-callsBeforeDecide)
	}

	if _, err := st.GetAgentSchedule(ctx, loopRunTickScheduleID(lr.ID)); err == nil {
		t.Fatalf("GetAgentSchedule: expected no loop_run_tick schedule row after an ESCALATE decision, but one exists")
	} else if err != store.ErrAgentScheduleNotFound {
		t.Fatalf("GetAgentSchedule: unexpected error %v, want %v", err, store.ErrAgentScheduleNotFound)
	}
}
