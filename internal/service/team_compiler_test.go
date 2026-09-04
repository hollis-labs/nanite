package service

// TASKS/teams/07-team-compiler.md's "Done means" regression coverage:
//   - the SME example (docs/engineering/architecture/15-teams.md's
//     "Illustrative shape") compiles into a WorkflowDefinition matching it
//     exactly -- kinds, DependsOn chain, Config keys present and correctly
//     typed;
//   - a fully fluid (gate-free) Team compiles into a valid
//     WorkflowDefinition with only flex steps;
//   - the compiled definition carries no legacy engine selector;
//   - every flex step's compiled Config genuinely round-trips through
//     the shared-host product resolver's parser (same package, called directly --
//     not just asserted to "look right").
//
// No live database anywhere in this file -- CompileTeam takes an
// already-decoded []store.TeamPhase and a resolvedMembers map as plain Go
// values, per this task's own "no store I/O beyond reading its inputs, no
// slot resolution" scope.

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// smeTeamPhases is 15-teams.md's own "Illustrative shape" SME example,
// authored as store.TeamPhase input (the pre-compile shape) rather than
// the doc's already-compiled WorkflowDefinition YAML.
func smeTeamPhases() []store.TeamPhase {
	return []store.TeamPhase{
		{
			ID:          "scope_work",
			Kind:        "flex",
			ActiveSlots: []string{"orchestrator", "engineer", "architect"},
			ExitTrigger: map[string]any{"self_tool": "mark_ready_for_review"},
		},
		{
			ID:           "review_gate",
			Kind:         "gate",
			ApproverSlot: "reviewer",
		},
		{
			ID:          "address_feedback",
			Kind:        "flex",
			ActiveSlots: []string{"engineer", "reviewer", "architect"},
			ExitTrigger: map[string]any{"event": "review_approved"},
		},
		{
			ID:           "merge_gate",
			Kind:         "gate",
			ApproverSlot: "operator", // human, not a Team Slot.
		},
	}
}

func stepByID(t *testing.T, wf agentworkflow.WorkflowDefinition, id string) agentworkflow.StepDefinition {
	t.Helper()
	for _, s := range wf.Steps {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no step %q in compiled definition (have: %v)", id, stepIDs(wf))
	return agentworkflow.StepDefinition{}
}

func stepIDs(wf agentworkflow.WorkflowDefinition) []string {
	out := make([]string, len(wf.Steps))
	for i, s := range wf.Steps {
		out[i] = s.ID
	}
	return out
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCompileTeam_SMEExample is the task's literal acceptance target: a
// direct, literal reproduction of 15-teams.md's own illustrative compiled
// shape, not just "something reasonable."
func TestCompileTeam_SMEExample(t *testing.T) {
	wf, err := CompileTeam("Feature Development", smeTeamPhases(), nil)
	if err != nil {
		t.Fatalf("CompileTeam: %v", err)
	}

	if wf.Name != "Feature Development" {
		t.Errorf("Name = %q, want %q", wf.Name, "Feature Development")
	}
	if wf.Engine != "" {
		t.Errorf("Engine = %q, want empty shared-host selector", wf.Engine)
	}
	if len(wf.Steps) != 4 {
		t.Fatalf("len(Steps) = %d, want 4: %v", len(wf.Steps), stepIDs(wf))
	}

	// scope_work: flex, no DependsOn (first phase), active_slots +
	// exit_trigger:self_tool.
	scopeWork := stepByID(t, wf, "scope_work")
	if scopeWork.Kind != agentworkflow.StepKindFlex {
		t.Errorf("scope_work.Kind = %q, want flex", scopeWork.Kind)
	}
	if len(scopeWork.DependsOn) != 0 {
		t.Errorf("scope_work.DependsOn = %v, want empty (first phase)", scopeWork.DependsOn)
	}
	activeSlots, ok := scopeWork.Config["active_slots"].([]string)
	if !ok || !stringSliceEqual(activeSlots, []string{"orchestrator", "engineer", "architect"}) {
		t.Errorf("scope_work.Config[active_slots] = %#v, want [orchestrator engineer architect]", scopeWork.Config["active_slots"])
	}
	exitTrigger, ok := scopeWork.Config["exit_trigger"].(map[string]any)
	if !ok || exitTrigger["self_tool"] != "mark_ready_for_review" {
		t.Errorf("scope_work.Config[exit_trigger] = %#v, want {self_tool: mark_ready_for_review}", scopeWork.Config["exit_trigger"])
	}

	// review_gate: gate, depends_on [scope_work], approver_slot: reviewer.
	reviewGate := stepByID(t, wf, "review_gate")
	if reviewGate.Kind != agentworkflow.StepKindGate {
		t.Errorf("review_gate.Kind = %q, want gate", reviewGate.Kind)
	}
	if !stringSliceEqual(reviewGate.DependsOn, []string{"scope_work"}) {
		t.Errorf("review_gate.DependsOn = %v, want [scope_work]", reviewGate.DependsOn)
	}
	if reviewGate.Config["approver_slot"] != "reviewer" {
		t.Errorf("review_gate.Config[approver_slot] = %#v, want reviewer", reviewGate.Config["approver_slot"])
	}

	// address_feedback: flex, depends_on [review_gate], active_slots +
	// exit_trigger:event.
	addressFeedback := stepByID(t, wf, "address_feedback")
	if addressFeedback.Kind != agentworkflow.StepKindFlex {
		t.Errorf("address_feedback.Kind = %q, want flex", addressFeedback.Kind)
	}
	if !stringSliceEqual(addressFeedback.DependsOn, []string{"review_gate"}) {
		t.Errorf("address_feedback.DependsOn = %v, want [review_gate]", addressFeedback.DependsOn)
	}
	activeSlots2, ok := addressFeedback.Config["active_slots"].([]string)
	if !ok || !stringSliceEqual(activeSlots2, []string{"engineer", "reviewer", "architect"}) {
		t.Errorf("address_feedback.Config[active_slots] = %#v, want [engineer reviewer architect]", addressFeedback.Config["active_slots"])
	}
	exitTrigger2, ok := addressFeedback.Config["exit_trigger"].(map[string]any)
	if !ok || exitTrigger2["event"] != "review_approved" {
		t.Errorf("address_feedback.Config[exit_trigger] = %#v, want {event: review_approved}", addressFeedback.Config["exit_trigger"])
	}

	// merge_gate: gate, depends_on [address_feedback], approver_slot:
	// operator (human, not a Team Slot -- carried through as an opaque
	// string, same as any other approver_slot value).
	mergeGate := stepByID(t, wf, "merge_gate")
	if mergeGate.Kind != agentworkflow.StepKindGate {
		t.Errorf("merge_gate.Kind = %q, want gate", mergeGate.Kind)
	}
	if !stringSliceEqual(mergeGate.DependsOn, []string{"address_feedback"}) {
		t.Errorf("merge_gate.DependsOn = %v, want [address_feedback]", mergeGate.DependsOn)
	}
	if mergeGate.Config["approver_slot"] != "operator" {
		t.Errorf("merge_gate.Config[approver_slot] = %#v, want operator", mergeGate.Config["approver_slot"])
	}

	// Whole definition must also pass agentworkflow.Validate on its own
	// (CompileTeam already calls this internally and would have errored
	// above if it failed, but assert it explicitly too).
	if err := agentworkflow.Validate(wf); err != nil {
		t.Fatalf("agentworkflow.Validate(compiled SME definition): %v", err)
	}
}

// TestCompileTeam_SMEExample_FlexStepsRoundTripProductConfig is the
// genuine integration check this task's own required reading calls for:
// every compiled flex step's Config must actually parse successfully
// through the product resolver's real config parser (same package, called
// directly here) -- not just "look like" the right shape.
func TestCompileTeam_SMEExample_FlexStepsRoundTripProductConfig(t *testing.T) {
	wf, err := CompileTeam("Feature Development", smeTeamPhases(), nil)
	if err != nil {
		t.Fatalf("CompileTeam: %v", err)
	}

	for _, s := range wf.Steps {
		if s.Kind != agentworkflow.StepKindFlex {
			continue
		}
		cfg, err := parseFlexStepConfig(s.Config)
		if err != nil {
			t.Fatalf("parseFlexStepConfig(%s.Config) = %v, want success", s.ID, err)
		}
		if len(cfg.ActiveSlots) == 0 {
			t.Errorf("%s: parsed ActiveSlots is empty", s.ID)
		}
		if cfg.TriggerKind == "" || cfg.TriggerSpecJSON == "" {
			t.Errorf("%s: parsed TriggerKind/TriggerSpecJSON not populated: %+v", s.ID, cfg)
		}
	}
}

// TestCompileTeam_FullyFluidTeam covers 15-teams.md's explicit statement
// that "a Team is not required to declare any gates at all -- a fully
// fluid team is a legitimate, gate-free shape": a phase sequence of only
// flex steps must compile correctly.
func TestCompileTeam_FullyFluidTeam(t *testing.T) {
	phases := []store.TeamPhase{
		{
			ID:          "collaborate",
			Kind:        "flex",
			ActiveSlots: []string{"orchestrator", "engineer"},
			ExitTrigger: map[string]any{"event": "work_done"},
		},
		{
			ID:          "polish",
			Kind:        "flex",
			ActiveSlots: []string{"engineer", "reviewer"},
			ExitTrigger: map[string]any{"self_tool": "mark_ready_for_review"},
		},
	}

	wf, err := CompileTeam("Fully Fluid Team", phases, nil)
	if err != nil {
		t.Fatalf("CompileTeam: %v", err)
	}
	if wf.Engine != "" {
		t.Errorf("Engine = %q, want empty shared-host selector", wf.Engine)
	}
	if len(wf.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(wf.Steps))
	}
	for _, s := range wf.Steps {
		if s.Kind != agentworkflow.StepKindFlex {
			t.Errorf("step %q Kind = %q, want flex (fully fluid team has no gates)", s.ID, s.Kind)
		}
		if _, err := parseFlexStepConfig(s.Config); err != nil {
			t.Errorf("parseFlexStepConfig(%s.Config) = %v, want success", s.ID, err)
		}
	}
	polish := stepByID(t, wf, "polish")
	if !stringSliceEqual(polish.DependsOn, []string{"collaborate"}) {
		t.Errorf("polish.DependsOn = %v, want [collaborate] (linear chain)", polish.DependsOn)
	}

	if err := agentworkflow.Validate(wf); err != nil {
		t.Fatalf("agentworkflow.Validate(fully fluid definition): %v", err)
	}
}

// TestCompileTeam_SingleFlexPhaseTeam covers the minimal fully fluid case
// -- one flex phase, zero gates.
func TestCompileTeam_SingleFlexPhaseTeam(t *testing.T) {
	phases := []store.TeamPhase{
		{
			ID:          "only_phase",
			Kind:        "flex",
			ActiveSlots: []string{"orchestrator"},
			ExitTrigger: map[string]any{"event": "done"},
		},
	}
	wf, err := CompileTeam("Single Flex Team", phases, nil)
	if err != nil {
		t.Fatalf("CompileTeam: %v", err)
	}
	if len(wf.Steps) != 1 || wf.Steps[0].Kind != agentworkflow.StepKindFlex {
		t.Fatalf("Steps = %+v, want exactly one flex step", wf.Steps)
	}
	if wf.Engine != "" {
		t.Errorf("Engine = %q, want empty shared-host selector", wf.Engine)
	}
}

// TestCompileTeam_UsesSharedHostIdentity keeps generated Teams from
// reintroducing a per-definition engine-selection path.
func TestCompileTeam_UsesSharedHostIdentity(t *testing.T) {
	wf, err := CompileTeam("Feature Development", smeTeamPhases(), nil)
	if err != nil {
		t.Fatalf("CompileTeam: %v", err)
	}
	if wf.Engine != "" {
		t.Fatalf("Engine = %q, want empty shared-host selector", wf.Engine)
	}
	if wf.Engine == agentworkflow.EngineLangGraph || wf.Engine == agentworkflow.EngineCrewAI {
		t.Fatalf("Engine leaked an external-engine value: %q", wf.Engine)
	}
}

// --- error paths ---

func TestCompileTeam_Errors(t *testing.T) {
	t.Run("empty name", func(t *testing.T) {
		if _, err := CompileTeam("", smeTeamPhases(), nil); err == nil {
			t.Fatal("want error for empty name")
		}
	})

	t.Run("empty phase sequence", func(t *testing.T) {
		if _, err := CompileTeam("Empty Team", nil, nil); err == nil {
			t.Fatal("want error for empty phase sequence")
		}
	})

	t.Run("unknown phase kind", func(t *testing.T) {
		phases := []store.TeamPhase{{ID: "p1", Kind: "bogus"}}
		if _, err := CompileTeam("Bad Team", phases, nil); err == nil {
			t.Fatal("want error for unknown phase kind")
		}
	})

	t.Run("gate phase missing approver_slot", func(t *testing.T) {
		phases := []store.TeamPhase{{ID: "p1", Kind: "gate"}}
		if _, err := CompileTeam("Bad Team", phases, nil); err == nil {
			t.Fatal("want error for gate phase missing approver_slot")
		}
	})

	t.Run("flex phase missing active_slots fails the product config round-trip", func(t *testing.T) {
		phases := []store.TeamPhase{{
			ID:          "p1",
			Kind:        "flex",
			ExitTrigger: map[string]any{"event": "x"},
		}}
		if _, err := CompileTeam("Bad Team", phases, nil); err == nil {
			t.Fatal("want error for flex phase missing active_slots")
		}
	})

	t.Run("flex phase missing exit_trigger fails the product config round-trip", func(t *testing.T) {
		phases := []store.TeamPhase{{
			ID:          "p1",
			Kind:        "flex",
			ActiveSlots: []string{"orchestrator"},
		}}
		if _, err := CompileTeam("Bad Team", phases, nil); err == nil {
			t.Fatal("want error for flex phase missing exit_trigger")
		}
	})

	t.Run("duplicate phase ids caught by agentworkflow.Validate", func(t *testing.T) {
		phases := []store.TeamPhase{
			{ID: "dup", Kind: "flex", ActiveSlots: []string{"a"}, ExitTrigger: map[string]any{"event": "x"}},
			{ID: "dup", Kind: "flex", ActiveSlots: []string{"b"}, ExitTrigger: map[string]any{"event": "y"}},
		}
		if _, err := CompileTeam("Bad Team", phases, nil); err == nil {
			t.Fatal("want error for duplicate phase ids")
		}
	})
}

// TestCompileTeam_ResolvedMembersUnconsumed confirms the documented design
// call is real, not just asserted in a comment: passing a non-nil, fully
// populated resolvedMembers map produces byte-for-byte the same compiled
// Config as passing nil -- resolved (agent_id, session_id) identity is
// never baked into the compiled definition.
func TestCompileTeam_ResolvedMembersUnconsumed(t *testing.T) {
	phases := smeTeamPhases()

	withNil, err := CompileTeam("Feature Development", phases, nil)
	if err != nil {
		t.Fatalf("CompileTeam(nil resolvedMembers): %v", err)
	}

	resolved := map[string][]store.TeamRunMember{
		"orchestrator": {{ID: "m1", SlotName: "orchestrator", AgentID: "agent-1", SessionID: "sess-1", Status: store.TeamRunMemberStatusActive}},
		"engineer":     {{ID: "m2", SlotName: "engineer", AgentID: "agent-2", SessionID: "sess-2", Status: store.TeamRunMemberStatusActive}},
		"architect":    {}, // declared, dormant, not yet resolved.
	}
	withResolved, err := CompileTeam("Feature Development", phases, resolved)
	if err != nil {
		t.Fatalf("CompileTeam(populated resolvedMembers): %v", err)
	}

	scopeWorkNil := stepByID(t, withNil, "scope_work")
	scopeWorkResolved := stepByID(t, withResolved, "scope_work")
	slotsNil, _ := scopeWorkNil.Config["active_slots"].([]string)
	slotsResolved, _ := scopeWorkResolved.Config["active_slots"].([]string)
	if !stringSliceEqual(slotsNil, slotsResolved) {
		t.Fatalf("active_slots differ between nil and populated resolvedMembers: %v vs %v -- resolvedMembers must not be baked into Config", slotsNil, slotsResolved)
	}
	// Confirm no agent_id/session_id ever leaked into the Config -- the
	// only strings present are the declared slot names.
	for _, s := range slotsResolved {
		if s == "agent-1" || s == "sess-1" || s == "agent-2" || s == "sess-2" {
			t.Fatalf("active_slots leaked a resolved (agent_id, session_id) value: %v", slotsResolved)
		}
	}
}
