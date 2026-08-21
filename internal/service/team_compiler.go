package service

// TASKS/teams/07-team-compiler.md -- the literal "compile phase/gate
// sequence into a WorkflowDefinition" step from docs/engineering/
// architecture/15-teams.md's own Guardrail diagram:
//
//	Team definition
//	    ↓ resolve slots
//	    ↓ install run-scoped routing/reflex policy
//	    ↓ compile phase/gate sequence into a WorkflowDefinition   <- this file
//	    ↓ launch an ordinary WorkflowRun
//	    ↓ existing agents + messaging + lifecycle do the actual work
//
// Nothing more: CompileTeam performs no store I/O, no slot resolution
// (TASKS/teams/08-team-run-launcher.md's job), and no launching (also
// task 08's job, via the existing WorkflowLauncher). It is a pure(ish)
// function -- given already-resolved inputs, it returns a plain
// agentworkflow.WorkflowDefinition value, testable without a live
// database. If implementing this ever seemed to need a
// TeamExecutionEngine/TeamMessageBus/TeamScheduler/TeamMemory/
// TeamGateManager, that would be the Guardrail's own drift signal -- it
// didn't: this file is a walk-and-map over an already-decoded phase slice.

import (
	"fmt"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// CompileTeam compiles a Team's phase/gate sequence into a plain
// agentworkflow.WorkflowDefinition. phases and resolvedMembers are
// separate, explicit inputs (not one opaque Team blob) per this task's own
// "What to do" instruction and 15-teams.md's forward-compat requirement
// ("What this session did not decide," third bullet): keeping the phase
// sequence as its own addressable input means a later "accept a phase
// sequence from a second source" split is a call-site change, not a
// rewrite of this function.
//
// name becomes the compiled WorkflowDefinition.Name (agentworkflow.Validate
// requires a non-empty name; the caller -- task 08's launcher -- is
// expected to pass the Team's own store.Team.Name, but CompileTeam itself
// takes it as a plain string rather than a *store.Team so this function
// never needs to read any Team field beyond the phase sequence it was
// explicitly handed).
//
// resolvedMembers is accepted as an explicit input (satisfying "phases and
// resolved-member data are separate, explicit inputs") but is deliberately
// NOT consumed to build any compiled step's Config -- see this function's
// own design-call note below ("active_slots: names, not tuples") for the
// full reasoning. A caller may pass nil; the compiled output is identical
// either way. This is not dead-parameter oversight: its presence in the
// signature is itself part of the documented design call -- resolved
// member identity is available to this function at compile time, and the
// decision made here is not to bake it into the compiled definition.
//
// Design call -- active_slots holds Team Slot *names*, not resolved
// (agent_id, session_id) tuples (this task's own "What to do" item 3,
// "recommend slot names only"): a flex phase's ActiveSlots (store.TeamPhase,
// authored at Team-definition time) is copied into the compiled flex
// step's Config["active_slots"] verbatim. This is not just the smaller
// surface area -- it is very likely the *only* shape that actually
// interoperates with what task 06 already built and merged:
// internal/service/workflow_engine_flex.go's evaluateFlexExit calls
// e.teamMembers.ListTeamRunMembersByRun(ctx, runID) and filters that live
// team_run_members list down to cfg.ActiveSlots (activeMembersForSlots) --
// it re-resolves slot names against team_run_members at flex-step-entry
// (and on every subsequent Resume-driven re-check) time, not once at
// compile time. Baking in concrete (agent_id, session_id) tuples instead
// would (a) go stale the moment a member is replaced mid-run (a
// `replaced`-status member from task 02's status vocabulary would require
// recompiling the whole WorkflowDefinition to pick up, defeating the
// "compiled definition reusable/inspectable independent of a specific
// run's member churn" property 15-teams.md asks for), and (b) not even be
// read by task 06's real executor, which only ever calls
// configStringSlice(cfg, "active_slots") for slot *names* and separately
// looks up team_run_members itself -- there is no code path today that
// would consume a baked-in tuple. Slot names is therefore not a stylistic
// preference here; it is the shape the already-merged consumer actually
// requires.
//
// Gate phases: ApproverSlot compiles to Config["approver_slot"] as
// documentation-carrying metadata only. Confirmed directly against
// internal/service/workflow_engine.go's runStep (the real StepKindGate
// branch): on entry it unconditionally marks the step "waiting_on_gate"
// and returns -- it reads zero keys from the gate step's Config. The only
// real resolution path today is external: store.ResolveGate(runID,
// stepID, input) (called by internal/service/a2a_task_manager.go's
// ProvideTaskInput, itself driven by an A2A "input-required" client
// action), which accepts an arbitrary string input with no approver-slot
// check of any kind. So 15-teams.md's illustrative `config: {approver_slot:
// reviewer}` is not, today, a literal read of anything the engine
// enforces -- it is included here anyway (matching the design doc's own
// illustrative shape verbatim) as forward-compatible metadata for a
// not-yet-built gate-approval-enforcement mechanism. This is a documented
// finding, not a claim that approval enforcement exists.
//
// Engine is a hard, not-configurable EngineBuiltin -- there is no caller
// input that can set it to anything else. Routing a Team through an
// external engine (LangGraph/CrewAI/GoogleADK/AutoGen/LangChain) would be
// meaningless: none of those consume Steps at all (agentworkflow.
// WorkflowDefinition.Engine's own doc comment), so a Team compiled against
// one would silently run nothing.
func CompileTeam(name string, phases []store.TeamPhase, resolvedMembers map[string][]store.TeamRunMember) (agentworkflow.WorkflowDefinition, error) {
	// resolvedMembers is deliberately unconsumed here -- see doc comment
	// above ("Design call -- active_slots holds Team Slot names").

	if name == "" {
		return agentworkflow.WorkflowDefinition{}, fmt.Errorf("compile team: name is required")
	}
	if len(phases) == 0 {
		return agentworkflow.WorkflowDefinition{}, fmt.Errorf("compile team %q: phase sequence is empty", name)
	}

	steps := make([]agentworkflow.StepDefinition, 0, len(phases))
	var prevID string
	for i, phase := range phases {
		if phase.ID == "" {
			return agentworkflow.WorkflowDefinition{}, fmt.Errorf("compile team %q: phase %d has an empty id", name, i)
		}

		step := agentworkflow.StepDefinition{ID: phase.ID}
		// Linear chain: each phase depends on exactly the phase before it
		// in the authored sequence -- this task's own instruction ("a
		// Team's phase sequence is not assumed to be non-linear in this
		// v1"), matching 15-teams.md's SME illustrative example, which
		// never branches. store.TeamPhase has no depends_on field for
		// this reason (see its own doc comment): DependsOn is derived
		// here, not authored per-phase.
		if i > 0 {
			step.DependsOn = []string{prevID}
		}

		switch phase.Kind {
		case "flex":
			step.Kind = agentworkflow.StepKindFlex
			step.Config = map[string]any{
				"active_slots": phase.ActiveSlots,
				"exit_trigger": phase.ExitTrigger,
			}
			// Real integration check, not a shape assumption: this must
			// actually parse through task 06's real parseFlexStepConfig
			// (same package, called directly -- not re-implemented or
			// approximated here). A phase whose Config would fail task
			// 06's executor at run time fails to compile instead, with a
			// precise error, rather than producing a WorkflowDefinition
			// that silently can't run.
			if _, err := parseFlexStepConfig(step.Config); err != nil {
				return agentworkflow.WorkflowDefinition{}, fmt.Errorf("compile team %q: phase %q: %w", name, phase.ID, err)
			}

		case "gate":
			step.Kind = agentworkflow.StepKindGate
			if phase.ApproverSlot == "" {
				return agentworkflow.WorkflowDefinition{}, fmt.Errorf("compile team %q: gate phase %q requires approver_slot", name, phase.ID)
			}
			step.Config = map[string]any{
				"approver_slot": phase.ApproverSlot,
			}

		default:
			return agentworkflow.WorkflowDefinition{}, fmt.Errorf("compile team %q: phase %q has unknown kind %q (want \"flex\" or \"gate\")", name, phase.ID, phase.Kind)
		}

		steps = append(steps, step)
		prevID = phase.ID
	}

	wf := agentworkflow.WorkflowDefinition{
		Name:   name,
		Engine: agentworkflow.EngineBuiltin,
		Steps:  steps,
	}
	if err := agentworkflow.Validate(wf); err != nil {
		return agentworkflow.WorkflowDefinition{}, fmt.Errorf("compile team %q: %w", name, err)
	}
	return wf, nil
}
