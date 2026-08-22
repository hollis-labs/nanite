package service

// TASKS/teams/06-stepkindflex-executor.md's "Done means" regression
// coverage: a flex step correctly enters a waiting state and correctly
// resumes via Resume once its exit trigger fires (driven through the real
// BuiltinWorkflowEngine, not a mock); the phase-closure-race default is
// implemented and covered by a named test simulating the race directly;
// the exit-trigger-authority default is implemented and covered by a test
// where an unauthorized slot's firing is ignored; a restart-mid-flex-step
// scenario does not double-resolve or corrupt team_run_members state.
//
// Every test here uses a real on-disk SQLite *store.Store (newTestWorkflowStore,
// workflow_engine_test.go) — never a fake/mock store — per this codebase's
// own testing standard for store-backed behavior.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// --- fixtures ---

// makeFlexTestAgent creates a minimal agent_profiles row so team_run_members'
// FK holds — mirrors internal/store/agents_test.go's makeTestAgent, kept
// local to this package since that helper is unexported across packages.
func makeFlexTestAgent(t *testing.T, s *store.Store, slug string) *store.AgentProfile {
	t.Helper()
	a := &store.AgentProfile{
		Name:         "Flex Test Agent " + slug,
		Slug:         slug,
		SystemPrompt: "test",
	}
	if err := s.CreateAgent(context.Background(), a); err != nil {
		t.Fatalf("CreateAgent(%s): %v", slug, err)
	}
	return a
}

// makeFlexTestSession creates a minimal sessions row so team_run_members'
// FK holds — mirrors internal/store/session_objects_test.go's makeTestSession.
func makeFlexTestSession(t *testing.T, s *store.Store) *store.Session {
	t.Helper()
	sess := &store.Session{Status: "active"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess
}

// makeFlexTeamMember resolves one concrete (agent, session) member into
// slotName for runID, exactly the shape task 02's team_run_members table
// (internal/store/team_run_members.go) records a real slot resolution as.
func makeFlexTeamMember(t *testing.T, ctx context.Context, s *store.Store, runID, slotName string) store.TeamRunMember {
	t.Helper()
	agent := makeFlexTestAgent(t, s, slotName+"-"+runID)
	session := makeFlexTestSession(t, s)
	m, err := s.InsertTeamRunMember(ctx, store.TeamRunMember{
		WorkflowRunID: runID, SlotName: slotName, AgentID: agent.ID, SessionID: session.ID,
	})
	if err != nil {
		t.Fatalf("InsertTeamRunMember(%s): %v", slotName, err)
	}
	return *m
}

// fireSelfTool persists a real assistant message carrying a tool_calls
// envelope naming toolName — the same {"tool_calls":[{"name":...}]} shape
// internal/agent/reflexes/state.go's structuredRefs parses, and what a
// real self-tool call would leave behind in the messages table. This is
// what makes exit_trigger: {self_tool: toolName} evaluate true against
// member's own session state.
func fireSelfTool(t *testing.T, s *store.Store, sessionID, agentID, toolName string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"tool_calls": []map[string]any{{"name": toolName}},
	})
	if err != nil {
		t.Fatalf("marshal tool_calls envelope: %v", err)
	}
	if err := s.CreateMessage(context.Background(), &store.Message{
		SessionID: sessionID, AgentID: agentID, Role: "assistant", Content: string(body),
	}); err != nil {
		t.Fatalf("CreateMessage(fireSelfTool): %v", err)
	}
}

func newFlexTestEngine(runStore *store.Store) *BuiltinWorkflowEngine {
	eng := NewBuiltinWorkflowEngine(runStore)
	eng.WithFlexSupport(runStore, &reflexes.StateCollector{Store: runStore, Window: 5})
	return eng
}

func flexWorkflowDef(name string, activeSlots []string, exitTrigger map[string]any) agentworkflow.WorkflowDefinition {
	cfg := map[string]any{"active_slots": activeSlots}
	if exitTrigger != nil {
		cfg["exit_trigger"] = exitTrigger
	}
	return agentworkflow.WorkflowDefinition{
		Name: name,
		Steps: []agentworkflow.StepDefinition{
			{ID: "scope_work", Kind: agentworkflow.StepKindFlex, Config: cfg},
		},
	}
}

func onlyRunID(t *testing.T, s *store.Store) string {
	t.Helper()
	runs := listAllRuns(t, s)
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1", len(runs))
	}
	return runs[0]
}

func flexStepRow(t *testing.T, s *store.Store, runID string) *store.WorkflowRunStepRow {
	t.Helper()
	steps, err := s.ListWorkflowRunSteps(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListWorkflowRunSteps: %v", err)
	}
	for _, row := range steps {
		if row.StepID == "scope_work" {
			return row
		}
	}
	t.Fatal("no scope_work row persisted")
	return nil
}

func memberStatus(t *testing.T, ctx context.Context, s *store.Store, id string) string {
	t.Helper()
	m, err := s.GetTeamRunMember(ctx, id)
	if err != nil {
		t.Fatalf("GetTeamRunMember(%s): %v", id, err)
	}
	return m.Status
}

// --- Done-means bullet 2: enters waiting, resumes once the exit trigger fires ---

func TestBuiltinWorkflowEngine_FlexStepResumesOnExitTrigger(t *testing.T) {
	ctx := context.Background()
	runStore := newTestWorkflowStore(t)
	eng := newFlexTestEngine(runStore)

	wf := flexWorkflowDef("flex-resume",
		[]string{"orchestrator", "engineer"},
		map[string]any{"self_tool": "mark_ready_for_review"},
	)
	exec := &fakeStepExecutor{}

	result, err := eng.Run(ctx, wf, agentworkflow.WorkflowInput{}, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != agentworkflow.RunStatusWaitingOnFlex {
		t.Fatalf("Status = %q, want %q", result.Status, agentworkflow.RunStatusWaitingOnFlex)
	}
	runID := result.RunID

	orchestrator := makeFlexTeamMember(t, ctx, runStore, runID, "orchestrator")
	engineer := makeFlexTeamMember(t, ctx, runStore, runID, "engineer")

	// No one has fired the exit trigger yet — Resume must leave the run
	// legitimately waiting, not error and not falsely resolve.
	result, err = eng.Resume(ctx, runID, wf, exec)
	if err != nil {
		t.Fatalf("Resume (before firing): %v", err)
	}
	if result.Status != agentworkflow.RunStatusWaitingOnFlex {
		t.Fatalf("Status after unfired Resume = %q, want %q", result.Status, agentworkflow.RunStatusWaitingOnFlex)
	}
	if got := memberStatus(t, ctx, runStore, engineer.ID); got != store.TeamRunMemberStatusActive {
		t.Fatalf("engineer status before firing = %q, want active", got)
	}

	// The default exit-trigger-authority policy: any active_slots member
	// (here, "engineer" — deliberately not "orchestrator", to prove this
	// isn't accidentally hardcoded to always be the first-listed slot) may
	// satisfy a self_tool exit trigger when no authorized_slot is set.
	fireSelfTool(t, runStore, engineer.SessionID, engineer.AgentID, "mark_ready_for_review")

	result, err = eng.Resume(ctx, runID, wf, exec)
	if err != nil {
		t.Fatalf("Resume (after firing): %v", err)
	}
	if result.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("Status after firing = %q, want completed (error=%s)", result.Status, result.Error)
	}
	sr, ok := result.StepResults["scope_work"]
	if !ok {
		t.Fatal("scope_work missing from StepResults after resolution")
	}
	if sr.IsError {
		t.Fatalf("scope_work IsError = true, want false: %s", sr.Output)
	}

	row := flexStepRow(t, runStore, runID)
	if row.Status != "completed" {
		t.Fatalf("persisted status = %q, want completed", row.Status)
	}

	// Both active_slots members are stood down once the phase closes —
	// see the phase-closure-race tests below for the "still working"
	// case; this asserts the firing member itself is also stood down,
	// not just the ones that didn't fire.
	if got := memberStatus(t, ctx, runStore, orchestrator.ID); got != store.TeamRunMemberStatusStopped {
		t.Fatalf("orchestrator status after resolution = %q, want stopped", got)
	}
	if got := memberStatus(t, ctx, runStore, engineer.ID); got != store.TeamRunMemberStatusStopped {
		t.Fatalf("engineer status after resolution = %q, want stopped", got)
	}
}

// --- Done-means bullet 3: phase-closure race, concretely simulated ---

// TestBuiltinWorkflowEngine_FlexStepPhaseClosureRace_StandsDownStillActiveMember
// is the required regression test for the phase-closure race
// (docs/engineering/architecture/15-teams.md's "Validating this design"
// stress test): the exit trigger fires while another active_slots member
// (architect) has produced no completion signal of its own — the
// observable, store-level proxy for "still mid-turn" this engine can
// actually simulate deterministically (a live in-flight LLM call isn't
// something a store-backed unit test can suspend mid-flight; a member
// with no evidence of finishing, still active at the moment of
// resolution, is the faithful equivalent). This task's documented policy
// (option (a), evaluateFlexExit's doc comment) is fire immediately and
// explicitly stand down every active member, not just the one that fired
// — this test asserts that really happens, not just that the firing
// member's own status changes.
func TestBuiltinWorkflowEngine_FlexStepPhaseClosureRace_StandsDownStillActiveMember(t *testing.T) {
	ctx := context.Background()
	runStore := newTestWorkflowStore(t)
	eng := newFlexTestEngine(runStore)

	wf := flexWorkflowDef("flex-phase-closure-race",
		[]string{"engineer", "architect"},
		map[string]any{"self_tool": "mark_ready_for_review"},
	)
	exec := &fakeStepExecutor{}

	result, err := eng.Run(ctx, wf, agentworkflow.WorkflowInput{}, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	runID := result.RunID

	engineer := makeFlexTeamMember(t, ctx, runStore, runID, "engineer")
	architect := makeFlexTeamMember(t, ctx, runStore, runID, "architect")

	// architect is deliberately given no message at all — simulating
	// "still actively working, hasn't produced anything yet" — while
	// engineer fires the exit trigger.
	fireSelfTool(t, runStore, engineer.SessionID, engineer.AgentID, "mark_ready_for_review")

	if got := memberStatus(t, ctx, runStore, architect.ID); got != store.TeamRunMemberStatusActive {
		t.Fatalf("precondition: architect status = %q, want active (still mid-turn)", got)
	}

	result, err = eng.Resume(ctx, runID, wf, exec)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("Status = %q, want completed (error=%s)", result.Status, result.Error)
	}

	// The race's real answer: architect's in-flight participation is
	// explicitly, observably abandoned (stood down), not silently left
	// "active" forever and not specially waited for.
	if got := memberStatus(t, ctx, runStore, architect.ID); got != store.TeamRunMemberStatusStopped {
		t.Fatalf("architect status after phase close = %q, want stopped (abandoned, not left dangling)", got)
	}
	if got := memberStatus(t, ctx, runStore, engineer.ID); got != store.TeamRunMemberStatusStopped {
		t.Fatalf("engineer status after phase close = %q, want stopped", got)
	}

	// A late arrival from architect (its turn finally finishing after the
	// phase already closed) must not reopen or re-resolve anything —
	// Resume is idempotent once the step is terminal.
	fireSelfTool(t, runStore, architect.SessionID, architect.AgentID, "mark_ready_for_review")
	result, err = eng.Resume(ctx, runID, wf, exec)
	if err != nil {
		t.Fatalf("Resume (after late arrival): %v", err)
	}
	if result.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("Status after late arrival = %q, want completed (must not reopen)", result.Status)
	}
}

// --- Done-means bullet 4: exit-trigger authority default ---

// TestBuiltinWorkflowEngine_FlexStepExitTriggerAuthority_UnauthorizedSlotIgnored
// covers this task's documented exit-trigger-authority default: any
// active_slots member may satisfy a self_tool exit trigger UNLESS the
// step's config names a specific authorized_slot (evaluateFlexExit's doc
// comment) — an unauthorized slot's firing is ignored (the phase stays
// open), and the authorized slot's own firing still resolves it.
func TestBuiltinWorkflowEngine_FlexStepExitTriggerAuthority_UnauthorizedSlotIgnored(t *testing.T) {
	ctx := context.Background()
	runStore := newTestWorkflowStore(t)
	eng := newFlexTestEngine(runStore)

	wf := flexWorkflowDef("flex-authority",
		[]string{"orchestrator", "engineer"},
		map[string]any{"self_tool": "mark_ready_for_review", "authorized_slot": "orchestrator"},
	)
	exec := &fakeStepExecutor{}

	result, err := eng.Run(ctx, wf, agentworkflow.WorkflowInput{}, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	runID := result.RunID

	orchestrator := makeFlexTeamMember(t, ctx, runStore, runID, "orchestrator")
	engineer := makeFlexTeamMember(t, ctx, runStore, runID, "engineer")

	// engineer is not the authorized_slot — its firing must be ignored,
	// not treated as an error and not resolving the phase.
	fireSelfTool(t, runStore, engineer.SessionID, engineer.AgentID, "mark_ready_for_review")
	result, err = eng.Resume(ctx, runID, wf, exec)
	if err != nil {
		t.Fatalf("Resume (unauthorized firing): %v", err)
	}
	if result.Status != agentworkflow.RunStatusWaitingOnFlex {
		t.Fatalf("Status after unauthorized firing = %q, want %q (must be ignored, not resolved)", result.Status, agentworkflow.RunStatusWaitingOnFlex)
	}
	if got := memberStatus(t, ctx, runStore, engineer.ID); got != store.TeamRunMemberStatusActive {
		t.Fatalf("engineer status after its own unauthorized firing = %q, want still active (nothing closed)", got)
	}

	// The authorized slot firing it resolves the phase normally.
	fireSelfTool(t, runStore, orchestrator.SessionID, orchestrator.AgentID, "mark_ready_for_review")
	result, err = eng.Resume(ctx, runID, wf, exec)
	if err != nil {
		t.Fatalf("Resume (authorized firing): %v", err)
	}
	if result.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("Status after authorized firing = %q, want completed (error=%s)", result.Status, result.Error)
	}
	sr := result.StepResults["scope_work"]
	if sr.IsError {
		t.Fatalf("scope_work IsError = true, want false: %s", sr.Output)
	}
}

// --- Done-means bullet 5: restart-mid-flex-step idempotency ---

// TestBuiltinWorkflowEngine_ResumeRestartMidFlexStep_DoesNotDoubleCountMembers
// simulates the engine's own documented at-least-once semantics
// (BuiltinWorkflowEngine.Resume's doc comment: a step caught in "running"
// status at crash time is re-run, not resumed in place) landing on a flex
// step specifically: a crash between runStep's "running" write and its
// "waiting_on_flex" write leaves the persisted row at status="running".
// Resume must re-enter the flex step's first-entry branch without
// spawning or corrupting any team_run_members row — this task's own
// idempotency requirement, since task 06 is not the slot-resolution owner
// (task 08 is) and must never invent a second path that mutates
// team_run_members on its own.
func TestBuiltinWorkflowEngine_ResumeRestartMidFlexStep_DoesNotDoubleCountMembers(t *testing.T) {
	ctx := context.Background()
	runStore := newTestWorkflowStore(t)
	eng := newFlexTestEngine(runStore)

	wf := flexWorkflowDef("flex-restart-mid-running",
		[]string{"engineer"},
		map[string]any{"self_tool": "mark_ready_for_review"},
	)

	const runID = "run-flex-crash-1"
	if err := runStore.CreateWorkflowRun(context.Background(), &store.WorkflowRunRow{ID: runID, DefinitionName: wf.Name, Status: "running"}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}
	// Simulates a crash between runStep's "running" write and its
	// "waiting_on_flex" write — exactly the interrupted-step shape
	// Resume's own doc comment describes for llm/tool steps, now for flex.
	if err := runStore.UpsertWorkflowRunStep(context.Background(), &store.WorkflowRunStepRow{
		WorkflowRunID: runID, StepID: "scope_work", Kind: "flex", Status: "running",
	}); err != nil {
		t.Fatalf("UpsertWorkflowRunStep: %v", err)
	}

	// A prior process had already resolved the slot (task 08's future
	// job, simulated here) before the crash.
	engineer := makeFlexTeamMember(t, ctx, runStore, runID, "engineer")

	exec := &fakeStepExecutor{}
	result, err := eng.Resume(ctx, runID, wf, exec)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Status != agentworkflow.RunStatusWaitingOnFlex {
		t.Fatalf("Status = %q, want %q (re-entered first-entry branch, waiting again)", result.Status, agentworkflow.RunStatusWaitingOnFlex)
	}

	members, err := runStore.ListTeamRunMembersByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListTeamRunMembersByRun: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("len(team_run_members) = %d, want 1 (re-entering the flex step must not spawn/duplicate members)", len(members))
	}
	if members[0].ID != engineer.ID || members[0].Status != store.TeamRunMemberStatusActive {
		t.Fatalf("team_run_members row corrupted by restart: %+v", members[0])
	}

	row := flexStepRow(t, runStore, runID)
	if row.Status != "waiting_on_flex" {
		t.Fatalf("persisted status = %q, want waiting_on_flex", row.Status)
	}

	// A second, unrelated Resume call (no new signal at all) must remain
	// a stable no-op too.
	result, err = eng.Resume(ctx, runID, wf, exec)
	if err != nil {
		t.Fatalf("second Resume: %v", err)
	}
	if result.Status != agentworkflow.RunStatusWaitingOnFlex {
		t.Fatalf("Status after second Resume = %q, want %q", result.Status, agentworkflow.RunStatusWaitingOnFlex)
	}
	members, err = runStore.ListTeamRunMembersByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListTeamRunMembersByRun (2nd): %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("len(team_run_members) after second Resume = %d, want 1", len(members))
	}

	// Now let it actually resolve, and confirm resolving after a restart
	// behaves exactly as the non-restart path does (no double-resolve,
	// single completed StepResult, member stood down exactly once).
	fireSelfTool(t, runStore, engineer.SessionID, engineer.AgentID, "mark_ready_for_review")
	result, err = eng.Resume(ctx, runID, wf, exec)
	if err != nil {
		t.Fatalf("Resume (after firing): %v", err)
	}
	if result.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("Status = %q, want completed (error=%s)", result.Status, result.Error)
	}
	if got := memberStatus(t, ctx, runStore, engineer.ID); got != store.TeamRunMemberStatusStopped {
		t.Fatalf("engineer status after resolution = %q, want stopped", got)
	}

	// A further Resume call after the step is already terminal
	// (results-backed, no longer in `waiting`) must not error or attempt
	// to re-resolve.
	result, err = eng.Resume(ctx, runID, wf, exec)
	if err != nil {
		t.Fatalf("Resume (post-terminal): %v", err)
	}
	if result.Status != agentworkflow.RunStatusCompleted {
		t.Fatalf("Status after post-terminal Resume = %q, want completed", result.Status)
	}
}

// --- parseFlexStepConfig: pure-function coverage for every exit_trigger form ---

func TestParseFlexStepConfig(t *testing.T) {
	t.Run("self_tool shorthand", func(t *testing.T) {
		cfg, err := parseFlexStepConfig(map[string]any{
			"active_slots": []any{"orchestrator", "engineer"},
			"exit_trigger": map[string]any{"self_tool": "mark_ready_for_review"},
		})
		if err != nil {
			t.Fatalf("parseFlexStepConfig: %v", err)
		}
		if len(cfg.ActiveSlots) != 2 {
			t.Fatalf("ActiveSlots = %v, want 2 entries", cfg.ActiveSlots)
		}
		if cfg.TriggerKind != "predicate" {
			t.Fatalf("TriggerKind = %q, want predicate", cfg.TriggerKind)
		}
		if cfg.AuthorizedSlot != "" {
			t.Fatalf("AuthorizedSlot = %q, want empty (no override supplied)", cfg.AuthorizedSlot)
		}
		// Round-trip through the real reflexes evaluator to prove the
		// translated spec is actually valid, not just non-empty JSON.
		state := reflexes.State{Messages: []reflexes.MessageSignal{{ToolNames: []string{"mark_ready_for_review"}}}}
		fired, evalErr := reflexes.EvaluateTrigger(cfg.TriggerKind, cfg.TriggerSpecJSON, state)
		if evalErr != nil {
			t.Fatalf("EvaluateTrigger: %v", evalErr)
		}
		if !fired {
			t.Fatal("EvaluateTrigger: want fired=true for a matching tool_names window")
		}
	})

	t.Run("event shorthand", func(t *testing.T) {
		cfg, err := parseFlexStepConfig(map[string]any{
			"active_slots": []any{"reviewer"},
			"exit_trigger": map[string]any{"event": "review_approved", "authorized_slot": "reviewer"},
		})
		if err != nil {
			t.Fatalf("parseFlexStepConfig: %v", err)
		}
		if cfg.TriggerKind != "event" {
			t.Fatalf("TriggerKind = %q, want event", cfg.TriggerKind)
		}
		if cfg.AuthorizedSlot != "reviewer" {
			t.Fatalf("AuthorizedSlot = %q, want reviewer", cfg.AuthorizedSlot)
		}
		state := reflexes.State{Events: []reflexes.EventSignal{{EventType: "review_approved"}}}
		fired, evalErr := reflexes.EvaluateTrigger(cfg.TriggerKind, cfg.TriggerSpecJSON, state)
		if evalErr != nil {
			t.Fatalf("EvaluateTrigger: %v", evalErr)
		}
		if !fired {
			t.Fatal("EvaluateTrigger: want fired=true for a matching event")
		}
	})

	t.Run("kind+spec escape hatch", func(t *testing.T) {
		cfg, err := parseFlexStepConfig(map[string]any{
			"active_slots": []any{"orchestrator"},
			"exit_trigger": map[string]any{
				"kind": "interval",
				"spec": map[string]any{"every_n_ticks": 3},
			},
		})
		if err != nil {
			t.Fatalf("parseFlexStepConfig: %v", err)
		}
		if cfg.TriggerKind != "interval" {
			t.Fatalf("TriggerKind = %q, want interval", cfg.TriggerKind)
		}
		fired, evalErr := reflexes.EvaluateTrigger(cfg.TriggerKind, cfg.TriggerSpecJSON, reflexes.State{TickN: 6})
		if evalErr != nil {
			t.Fatalf("EvaluateTrigger: %v", evalErr)
		}
		if !fired {
			t.Fatal("EvaluateTrigger: want fired=true at tick 6 with every_n_ticks=3")
		}
	})

	t.Run("missing active_slots is an error", func(t *testing.T) {
		if _, err := parseFlexStepConfig(map[string]any{
			"exit_trigger": map[string]any{"self_tool": "x"},
		}); err == nil {
			t.Fatal("want error for missing active_slots")
		}
	})

	t.Run("missing exit_trigger is an error", func(t *testing.T) {
		if _, err := parseFlexStepConfig(map[string]any{
			"active_slots": []any{"orchestrator"},
		}); err == nil {
			t.Fatal("want error for missing exit_trigger")
		}
	})

	t.Run("unrecognized exit_trigger shape is an error", func(t *testing.T) {
		if _, err := parseFlexStepConfig(map[string]any{
			"active_slots": []any{"orchestrator"},
			"exit_trigger": map[string]any{"unrelated_key": "x"},
		}); err == nil {
			t.Fatal("want error for an exit_trigger with none of self_tool/event/kind+spec")
		}
	})

	t.Run("kind without spec is an error", func(t *testing.T) {
		if _, err := parseFlexStepConfig(map[string]any{
			"active_slots": []any{"orchestrator"},
			"exit_trigger": map[string]any{"kind": "predicate"},
		}); err == nil {
			t.Fatal("want error for kind set without a spec object")
		}
	})
}
