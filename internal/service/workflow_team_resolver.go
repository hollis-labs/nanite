package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/store"
)

// WorkflowTeamMembershipStore is the narrow durable membership surface used
// by the shared-host TeamRun resolver.
type WorkflowTeamMembershipStore interface {
	ListTeamRunMembersByRun(context.Context, string) ([]store.TeamRunMember, error)
	GetTeamSignalResolution(context.Context, string, string) (*store.TeamSignalResolution, error)
	PrepareTeamSignalResolution(context.Context, store.TeamSignalResolution) (*store.TeamSignalResolution, error)
	CompleteTeamSignalResolution(context.Context, string, string) error
	StopTeamRunMembers(context.Context, string) (int, error)
}

// WorkflowTeamStateCollector builds the live reflex state for one resolved
// team member.
type WorkflowTeamStateCollector interface {
	Collect(context.Context, string, string, string) (reflexes.State, error)
}

type flexStepConfig struct {
	ActiveSlots     []string
	TriggerKind     string
	TriggerSpecJSON string
	AuthorizedSlot  string
}

func parseFlexStepConfig(config map[string]any) (flexStepConfig, error) {
	out := flexStepConfig{ActiveSlots: workflowConfigStringSlice(config, "active_slots")}
	if len(out.ActiveSlots) == 0 {
		return out, errors.New("flex step config requires a non-empty \"active_slots\" list")
	}
	trigger := workflowConfigMap(config, "exit_trigger")
	if trigger == nil {
		return out, errors.New("flex step config requires an \"exit_trigger\"")
	}
	out.AuthorizedSlot = workflowConfigString(trigger, "authorized_slot")
	var spec map[string]any
	switch {
	case workflowConfigString(trigger, "self_tool") != "":
		out.TriggerKind = "predicate"
		spec = map[string]any{"kind": "tool_name_window", "names": []string{workflowConfigString(trigger, "self_tool")}, "window": 1, "mode": "any"}
	case workflowConfigString(trigger, "event") != "":
		out.TriggerKind = "event"
		spec = map[string]any{"name": workflowConfigString(trigger, "event")}
	case workflowConfigString(trigger, "kind") != "":
		out.TriggerKind = workflowConfigString(trigger, "kind")
		spec = workflowConfigMap(trigger, "spec")
		if spec == nil {
			return out, fmt.Errorf("flex step config: exit_trigger.kind %q requires a \"spec\" object", out.TriggerKind)
		}
	default:
		return out, errors.New(`flex step config: exit_trigger must set "self_tool", "event", or "kind"+"spec"`)
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return out, fmt.Errorf("flex step config: encode exit trigger spec: %w", err)
	}
	out.TriggerSpecJSON = string(encoded)
	return out, nil
}

func activeMembersForSlots(members []store.TeamRunMember, slots []string) []store.TeamRunMember {
	allowed := make(map[string]bool, len(slots))
	for _, slot := range slots {
		allowed[slot] = true
	}
	active := make([]store.TeamRunMember, 0, len(members))
	for _, member := range members {
		if member.Status == store.TeamRunMemberStatusActive && allowed[member.SlotName] {
			active = append(active, member)
		}
	}
	return active
}

// WorkflowTeamStepResolution is the product result of evaluating one
// persisted TeamRun phase. The shared workflow host owns the workflow wait;
// this service owns Nanite membership, reflex, authority, and stand-down
// semantics.
type WorkflowTeamStepResolution struct {
	Resolved           bool
	Output             string
	ResponderReference string
	IsError            bool
}

// WorkflowTeamStepResolver is the service-native TeamRun collaborator used
// by both the retired pilot engine and the shared workflow-host bridge.
type WorkflowTeamStepResolver struct {
	teamMembers WorkflowTeamMembershipStore
	flexState   WorkflowTeamStateCollector
}

func NewWorkflowTeamStepResolver(teamMembers WorkflowTeamMembershipStore, flexState WorkflowTeamStateCollector) *WorkflowTeamStepResolver {
	return &WorkflowTeamStepResolver{teamMembers: teamMembers, flexState: flexState}
}

// CompleteWorkflowTeamStep acknowledges that the shared runtime has durably
// closed the canonical wait. Keeping this separate from Prepare makes a crash
// in the stand-down -> wait-close window replayable in either direction.
func (r *WorkflowTeamStepResolver) CompleteWorkflowTeamStep(ctx context.Context, runID, stepID string) error {
	if r == nil || r.teamMembers == nil {
		return errors.New("complete Team step: no team membership store configured")
	}
	return r.teamMembers.CompleteTeamSignalResolution(ctx, runID, stepID)
}

// CancelWorkflowTeamRun acknowledges a durable terminal transition by
// idempotently standing down every still-active member. The periodic Team
// reconciler independently repairs a crash before this acknowledgement.
func (r *WorkflowTeamStepResolver) CancelWorkflowTeamRun(ctx context.Context, runID string) error {
	if r == nil || r.teamMembers == nil {
		return errors.New("cancel Team run: no team membership store configured")
	}
	_, err := r.teamMembers.StopTeamRunMembers(ctx, runID)
	return err
}

// ResolveWorkflowTeamStep evaluates the authored exit trigger against the
// live active members and stands the phase down exactly once it resolves.
func (r *WorkflowTeamStepResolver) ResolveWorkflowTeamStep(ctx context.Context, runID, stepID string, config map[string]any) (WorkflowTeamStepResolution, error) {
	if r == nil || r.teamMembers == nil || r.flexState == nil {
		return WorkflowTeamStepResolution{
			Resolved: true,
			IsError:  true,
			Output:   "flex step: no team_run_members store or reflex state collector configured",
		}, nil
	}
	if prepared, err := r.teamMembers.GetTeamSignalResolution(ctx, runID, stepID); err == nil {
		return WorkflowTeamStepResolution{Resolved: true, Output: prepared.Output, ResponderReference: prepared.ResponderReference}, nil
	} else if !errors.Is(err, store.ErrTeamSignalResolutionNotFound) {
		return WorkflowTeamStepResolution{}, fmt.Errorf("flex step %q: load prepared resolution: %w", stepID, err)
	}
	cfg, cfgErr := parseFlexStepConfig(config)
	if cfgErr != nil {
		return WorkflowTeamStepResolution{Resolved: true, IsError: true, Output: fmt.Sprintf("flex step config: %v", cfgErr)}, nil
	}

	members, err := r.teamMembers.ListTeamRunMembersByRun(ctx, runID)
	if err != nil {
		return WorkflowTeamStepResolution{}, fmt.Errorf("flex step %q: list team_run_members: %w", stepID, err)
	}
	active := activeMembersForSlots(members, cfg.ActiveSlots)
	if len(active) == 0 {
		return WorkflowTeamStepResolution{}, nil
	}

	var firedBy *store.TeamRunMember
	for i := range active {
		member := active[i]
		state, collectErr := r.flexState.Collect(ctx, member.SessionID, member.AgentID, "")
		if collectErr != nil {
			return WorkflowTeamStepResolution{}, fmt.Errorf("flex step %q: collect state for member %s: %w", stepID, member.ID, collectErr)
		}
		fired, evalErr := reflexes.EvaluateTrigger(cfg.TriggerKind, cfg.TriggerSpecJSON, state)
		if evalErr != nil {
			return WorkflowTeamStepResolution{
				Resolved: true,
				IsError:  true,
				Output:   fmt.Sprintf("flex step %q: evaluate exit trigger: %v", stepID, evalErr),
			}, nil
		}
		if !fired || (cfg.AuthorizedSlot != "" && member.SlotName != cfg.AuthorizedSlot) {
			continue
		}
		firedBy = &active[i]
		break
	}
	if firedBy == nil {
		return WorkflowTeamStepResolution{}, nil
	}
	memberIDs := make([]string, 0, len(active))
	for _, member := range active {
		memberIDs = append(memberIDs, member.ID)
	}
	prepared, err := r.teamMembers.PrepareTeamSignalResolution(ctx, store.TeamSignalResolution{
		WorkflowRunID: runID, StepID: stepID, ResponderReference: firedBy.ID,
		Output:    fmt.Sprintf("exit trigger satisfied by slot %q (agent %s, member %s)", firedBy.SlotName, firedBy.AgentID, firedBy.ID),
		MemberIDs: memberIDs,
	})
	if err != nil {
		return WorkflowTeamStepResolution{}, fmt.Errorf("flex step %q: prepare durable resolution: %w", stepID, err)
	}
	return WorkflowTeamStepResolution{Resolved: true, ResponderReference: prepared.ResponderReference, Output: prepared.Output}, nil
}

func workflowConfigString(config map[string]any, key string) string {
	value, _ := config[key].(string)
	return value
}

func workflowConfigMap(config map[string]any, key string) map[string]any {
	value, _ := config[key].(map[string]any)
	return value
}

func workflowConfigStringSlice(config map[string]any, key string) []string {
	value := config[key]
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}
