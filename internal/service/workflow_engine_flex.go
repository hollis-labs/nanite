package service

// TASKS/teams/06-stepkindflex-executor.md — the real flex-step (StepKindFlex)
// executor: what happens during a flex step's wait (nothing new here — see
// runStep's flex branch in workflow_engine.go) and what resolves it (this
// file). docs/engineering/architecture/15-teams.md Decision 2 is explicit
// that a flex step reuses the existing gate pause/external-resolve/Resume
// shape, with the resolution condition itself (the exit trigger) reusing
// the reflex system's own predicate/event/interval trigger-spec AST rather
// than a second condition language — every function below either parses
// that reuse or evaluates it; none of them invent new steering vocabulary.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
)

// TeamMembershipStore is the narrow team_run_members surface a flex step
// needs: which concrete (agent_id, session_id) members are currently active
// for its active_slots, and a way to stand a member down once the phase
// they were active for closes. *store.Store satisfies this structurally
// (internal/store/team_run_members.go, TASKS/teams/02-team-run-members-
// table.md) — this interface exists so BuiltinWorkflowEngine's dependency
// footprint states exactly what it uses, matching WorkflowRunStore's own
// narrow-interface precedent, and so it stays decoupled from
// WorkflowRunStore's own "just workflow_runs/workflow_run_steps" scope.
type TeamMembershipStore interface {
	ListTeamRunMembersByRun(ctx context.Context, runID string) ([]store.TeamRunMember, error)
	UpdateTeamRunMemberStatus(ctx context.Context, id, status string) error
}

// FlexStepStateCollector is the narrow reflexes.StateCollector surface a
// flex step needs to build the session-state snapshot its exit trigger
// evaluates against. *reflexes.StateCollector (internal/agent/reflexes/
// state.go) satisfies this directly — reused as-is, not reimplemented,
// per this file's own header note.
type FlexStepStateCollector interface {
	Collect(ctx context.Context, sessionID, agentID, agentClass string) (reflexes.State, error)
}

// flexStepConfig is a StepKindFlex StepDefinition's parsed, validated
// Config — docs/engineering/architecture/15-teams.md's illustrative
// compiled shape: `config: { active_slots: [...], exit_trigger: {...} }`.
type flexStepConfig struct {
	// ActiveSlots names the Team Slots participating in this phase —
	// cross-referenced against team_run_members (task 02's table) at
	// resolution time, never a second source of truth for who's active
	// (this task's own "What to do" instruction).
	ActiveSlots []string

	// TriggerKind / TriggerSpecJSON are the underlying reflexes trigger
	// vocabulary (internal/agent/reflexes.EvaluateTrigger's own
	// triggerKind/triggerSpec parameters — "predicate", "event", or
	// "interval") that ExitTrigger's shorthand notations below compile
	// down to. Always populated by parseFlexStepConfig, regardless of
	// which shorthand (or the raw kind+spec escape hatch) a caller used.
	TriggerKind     string
	TriggerSpecJSON string

	// AuthorizedSlot is this task's exit-trigger-authority provisional
	// default (see evaluateFlexExit's doc comment): empty means any
	// active_slots member may satisfy the exit trigger; a non-empty value
	// restricts satisfaction to that one Team Slot name.
	AuthorizedSlot string
}

// parseFlexStepConfig decodes and validates one flex step's Config into a
// flexStepConfig. exit_trigger supports two shorthand forms mirroring
// 15-teams.md's own illustrative examples verbatim —
// `{"self_tool": "<tool_name>"}` (`exit_trigger: {self_tool:
// mark_ready_for_review}`) and `{"event": "<event_name>"}` (`exit_trigger:
// {event: review_approved}`) — plus a `{"kind": "...", "spec": {...}}`
// escape hatch for any predicate/interval trigger those two shorthands
// don't cover, passing the reflexes trigger_kind/trigger_spec shape
// straight through. Every form is translated into the same underlying
// reflexes vocabulary; there is no separate condition language.
func parseFlexStepConfig(cfg map[string]any) (flexStepConfig, error) {
	var out flexStepConfig

	out.ActiveSlots = configStringSlice(cfg, "active_slots")
	if len(out.ActiveSlots) == 0 {
		return out, fmt.Errorf("flex step config requires a non-empty \"active_slots\" list")
	}

	trigger := configMap(cfg, "exit_trigger")
	if trigger == nil {
		return out, fmt.Errorf("flex step config requires an \"exit_trigger\"")
	}
	out.AuthorizedSlot = configString(trigger, "authorized_slot")

	switch {
	case configString(trigger, "self_tool") != "":
		toolName := configString(trigger, "self_tool")
		spec := map[string]any{
			"kind": "tool_name_window", "names": []string{toolName}, "window": 1, "mode": "any",
		}
		b, err := json.Marshal(spec)
		if err != nil {
			return out, fmt.Errorf("flex step config: encode self_tool exit trigger: %w", err)
		}
		out.TriggerKind = "predicate"
		out.TriggerSpecJSON = string(b)

	case configString(trigger, "event") != "":
		b, err := json.Marshal(map[string]any{"name": configString(trigger, "event")})
		if err != nil {
			return out, fmt.Errorf("flex step config: encode event exit trigger: %w", err)
		}
		out.TriggerKind = "event"
		out.TriggerSpecJSON = string(b)

	case configString(trigger, "kind") != "":
		spec := configMap(trigger, "spec")
		if spec == nil {
			return out, fmt.Errorf("flex step config: exit_trigger.kind %q requires a \"spec\" object", configString(trigger, "kind"))
		}
		b, err := json.Marshal(spec)
		if err != nil {
			return out, fmt.Errorf("flex step config: encode exit trigger spec: %w", err)
		}
		out.TriggerKind = configString(trigger, "kind")
		out.TriggerSpecJSON = string(b)

	default:
		return out, fmt.Errorf(`flex step config: exit_trigger must set "self_tool", "event", or "kind"+"spec"`)
	}

	return out, nil
}

// activeMembersForSlots filters members down to the ones both currently
// TeamRunMemberStatusActive and resolved into one of slots.
func activeMembersForSlots(members []store.TeamRunMember, slots []string) []store.TeamRunMember {
	slotSet := make(map[string]bool, len(slots))
	for _, s := range slots {
		slotSet[s] = true
	}
	out := make([]store.TeamRunMember, 0, len(members))
	for _, m := range members {
		if m.Status == store.TeamRunMemberStatusActive && slotSet[m.SlotName] {
			out = append(out, m)
		}
	}
	return out
}

// recheckFlexStep is execute()'s entry point for a flex step already
// sitting in waiting_on_flex: it re-evaluates the exit trigger and, if
// satisfied by an authorized member, persists the terminal resolution.
// Returns resolved=false to mean "still legitimately waiting" (a no-op —
// no store write happens, and the step stays waiting_on_flex for the next
// Resume-driven pass). err is reserved for infra-level failures (a store
// read/write failing) that should abort the whole run, matching runStep's
// own Err-vs-Result.IsError split — a malformed config or a reflex
// evaluation error is instead folded into a resolved=true, IsError=true
// StepResult, since neither of those will ever change on a later retry and
// the run should fail cleanly rather than wait forever.
func (e *BuiltinWorkflowEngine) recheckFlexStep(ctx context.Context, runID string, step agentworkflow.StepDefinition) (resolved bool, sr agentworkflow.StepResult, err error) {
	resolved, sr, evalErr := e.evaluateFlexExit(ctx, runID, step)
	if evalErr != nil {
		return false, agentworkflow.StepResult{}, evalErr
	}
	if !resolved {
		return false, agentworkflow.StepResult{}, nil
	}

	status := "completed"
	if sr.IsError {
		status = "failed"
	}
	if err := e.store.UpsertWorkflowRunStep(&store.WorkflowRunStepRow{
		WorkflowRunID: runID, StepID: step.ID, Kind: string(step.Kind), Status: status,
		Output: sr.Output, IsError: sr.IsError, CompletedAt: time.Now().UTC(),
	}); err != nil {
		return false, agentworkflow.StepResult{}, fmt.Errorf("flex step %q: persist resolution: %w", step.ID, err)
	}
	return true, sr, nil
}

// evaluateFlexExit is the real exit-trigger evaluation body: parse config,
// resolve active members from team_run_members, evaluate the exit trigger
// against each authorized-or-not active member's live session state, and —
// on a genuine, authorized firing — stand down the whole phase.
//
// Exit-trigger-authority default (docs/engineering/architecture/
// 15-teams.md leaves this open: "whether it's always orchestrator-only,
// any(active_slots), a specific slot, or varies per flex step... the
// default policy is not [decided]"): any member of active_slots may
// satisfy a fired exit trigger, UNLESS this step's own config names a
// specific AuthorizedSlot — matching the flex step's own "fluid,
// self-organizing" framing (15-teams.md's SME illustrative shape has no
// single designated "closer" for a phase; any participant reasonably
// finishing the work should be able to say so). This is a provisional
// default, not a permanent policy: task 04's team_authority_grants/
// AuthorizedForVerb (internal/store/team_authority.go) is the real
// enforcement primitive tasks 08/09 wire for may_spawn/may_message/
// may_not_review, and its own header comment does not list this task as a
// caller — deliberately, since AuthorizedForVerb's verb vocabulary has no
// verb for "may close this phase" yet (only may_spawn/may_message/
// may_not_review). Wiring exit-trigger authority through AuthorizedForVerb
// for real (e.g. a future may_signal verb, matching the design doc's own
// "may_delegate, may_approve, may_signal" suggestion) is explicitly left
// for whichever later task compiles a Team's authority config down into a
// flex step's Config — this task's AuthorizedSlot field is the hook that
// later work attaches to, not a claim that this is the final mechanism.
//
// Phase-closure race (15-teams.md's "Validating this design" stress
// test, this task's required concrete answer, not deferred): option (a),
// fire immediately. Once an authorized member's exit trigger fires, every
// active member for this step's active_slots — including the one that
// fired it — is explicitly stood down (team_run_members.status ->
// TeamRunMemberStatusStopped, migration 129's own vocabulary: "stopped —
// the member's participation in this run ended normally (phase closed...)").
// Any other member still mid-turn at that moment is not waited for and not
// specially notified; its eventual output (if any) lands wherever its own
// harness turn naturally routes it, with no further claim on this flex
// step. This is the simplest of the three options 15-teams.md lists, and
// the only one directly implementable without new schema/timeout
// machinery this task's scope doesn't otherwise need (option (c)'s
// "closing flag + timeout" would need a new team_run_members status/
// timestamp; option (b)'s "route eventual output to whatever phase is
// active by then" needs a live post-hoc message-routing hook this task
// doesn't own) — see this task's Work Log for the full reasoning.
func (e *BuiltinWorkflowEngine) evaluateFlexExit(ctx context.Context, runID string, step agentworkflow.StepDefinition) (bool, agentworkflow.StepResult, error) {
	cfg, cfgErr := parseFlexStepConfig(step.Config)
	if cfgErr != nil {
		return true, agentworkflow.StepResult{
			StepID: step.ID, Kind: step.Kind, IsError: true,
			Output: fmt.Sprintf("flex step config: %v", cfgErr),
		}, nil
	}

	if e.teamMembers == nil || e.flexState == nil {
		// Defensive: an engine constructed without WithFlexSupport
		// reached a flex step anyway. Production wiring
		// (cmd/nanite/main.go) always calls WithFlexSupport; this is a
		// clear, terminal config error rather than an infinite wait for
		// a caller that built the engine bare.
		return true, agentworkflow.StepResult{
			StepID: step.ID, Kind: step.Kind, IsError: true,
			Output: "flex step: BuiltinWorkflowEngine.WithFlexSupport was never called — no team_run_members store or reflex state collector configured",
		}, nil
	}

	members, err := e.teamMembers.ListTeamRunMembersByRun(ctx, runID)
	if err != nil {
		return false, agentworkflow.StepResult{}, fmt.Errorf("flex step %q: list team_run_members: %w", step.ID, err)
	}
	active := activeMembersForSlots(members, cfg.ActiveSlots)
	if len(active) == 0 {
		// No active member resolved yet for any declared active_slots
		// (e.g. a normally-dormant SME slot not yet woken) — legitimately
		// still waiting, not an error.
		return false, agentworkflow.StepResult{}, nil
	}

	var firedBy *store.TeamRunMember
	for i := range active {
		m := active[i]
		state, collectErr := e.flexState.Collect(ctx, m.SessionID, m.AgentID, "")
		if collectErr != nil {
			return false, agentworkflow.StepResult{}, fmt.Errorf("flex step %q: collect state for member %s: %w", step.ID, m.ID, collectErr)
		}
		fired, evalErr := reflexes.EvaluateTrigger(cfg.TriggerKind, cfg.TriggerSpecJSON, state)
		if evalErr != nil {
			return true, agentworkflow.StepResult{
				StepID: step.ID, Kind: step.Kind, IsError: true,
				Output: fmt.Sprintf("flex step %q: evaluate exit trigger: %v", step.ID, evalErr),
			}, nil
		}
		if !fired {
			continue
		}
		if cfg.AuthorizedSlot != "" && m.SlotName != cfg.AuthorizedSlot {
			// Fired, but this member's slot isn't this step's
			// AuthorizedSlot — ignored, not an error (an unauthorized
			// self_tool call from a fluid, self-organizing member is a
			// normal occurrence, not a fault). Keep scanning: a
			// different, authorized active member may also have fired
			// it (or may yet).
			continue
		}
		firedBy = &active[i]
		break
	}
	if firedBy == nil {
		return false, agentworkflow.StepResult{}, nil
	}

	if err := e.standDownFlexMembers(ctx, active); err != nil {
		return false, agentworkflow.StepResult{}, fmt.Errorf("flex step %q: stand down members: %w", step.ID, err)
	}

	return true, agentworkflow.StepResult{
		StepID: step.ID, Kind: step.Kind,
		Output: fmt.Sprintf("exit trigger satisfied by slot %q (agent %s, member %s)", firedBy.SlotName, firedBy.AgentID, firedBy.ID),
	}, nil
}

// standDownFlexMembers transitions every member in active to
// TeamRunMemberStatusStopped — the phase-closure-race resolution's
// "abandon in-flight work" half made concrete and observable, not silent.
// Idempotent by construction: UpdateTeamRunMemberStatus sets status
// unconditionally by id, so re-running this (e.g. a second Resume call
// hitting recheckFlexStep again before the workflow_run_steps row's own
// terminal write lands) just re-writes the same terminal value.
// ErrTeamRunMemberNotFound is tolerated, not fatal — a row task 08/09
// logic already removed or reassigned by the time this runs shouldn't
// abort the whole run.
func (e *BuiltinWorkflowEngine) standDownFlexMembers(ctx context.Context, active []store.TeamRunMember) error {
	for _, m := range active {
		if err := e.teamMembers.UpdateTeamRunMemberStatus(ctx, m.ID, store.TeamRunMemberStatusStopped); err != nil {
			if errors.Is(err, store.ErrTeamRunMemberNotFound) {
				continue
			}
			return err
		}
	}
	return nil
}
