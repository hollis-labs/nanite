package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

const (
	NativePolicyExtension = "com.hollislabs.nanite/native-policy"
	ReflexPolicyExtension = "com.hollislabs.nanite/reflex-policy"
)

type nativePolicyExtension struct {
	PermissionProfile string          `json:"permission_profile,omitempty"`
	PermissionMode    string          `json:"permission_mode,omitempty"`
	ModelSelection    *ModelSelection `json:"model_selection,omitempty"`
}

type reflexPolicyExtension struct {
	Reflexes []reflexPolicyReflex `json:"reflexes"`
}

type reflexPolicyReflex struct {
	Name                      string         `json:"name"`
	TriggerKind               string         `json:"trigger_kind"`
	TriggerSpec               map[string]any `json:"trigger_spec"`
	ActionKind                string         `json:"action_kind"`
	ActionSpec                map[string]any `json:"action_spec"`
	Priority                  int64          `json:"priority,omitempty"`
	OptOutAllowed             *bool          `json:"opt_out_allowed,omitempty"`
	RecurrenceOverrideSeconds *int64         `json:"recurrence_override_seconds,omitempty"`
}

func naniteAgentdefExtensions(namespace, version string) (func(agentdef.Extension) error, bool) {
	if version != "1" {
		return nil, false
	}
	switch namespace {
	case NativePolicyExtension:
		return validateNativePolicyExtension, true
	case ReflexPolicyExtension:
		return validateReflexPolicyExtension, true
	default:
		return nil, false
	}
}

func validateNativePolicyExtension(e agentdef.Extension) error {
	p, err := decodeNativePolicy(e)
	if err != nil {
		return err
	}
	if p.PermissionProfile != "" && p.PermissionMode != "" && p.PermissionProfile != p.PermissionMode {
		return fmt.Errorf("permission_profile and permission_mode must match when both are set")
	}
	permission := p.permissionProfile()
	if permission != "" {
		if err := validateNativePermissionProfile(permission); err != nil {
			return err
		}
	}
	if p.ModelSelection != nil {
		if strings.TrimSpace(p.ModelSelection.Provider) == "" || strings.TrimSpace(p.ModelSelection.Model) == "" {
			return fmt.Errorf("model_selection requires provider and model")
		}
	}
	return nil
}

func validateReflexPolicyExtension(e agentdef.Extension) error {
	p, err := decodeReflexPolicy(e)
	if err != nil {
		return err
	}
	if len(p.Reflexes) == 0 {
		return fmt.Errorf("reflexes must contain at least one reflex")
	}
	if len(p.Reflexes) > 32 {
		return fmt.Errorf("reflexes must contain at most 32 reflexes")
	}
	seen := map[string]bool{}
	for i, r := range p.Reflexes {
		row, err := r.storeRow("")
		if err != nil {
			return fmt.Errorf("reflexes[%d]: %w", i, err)
		}
		if seen[row.Name] {
			return fmt.Errorf("reflexes[%d].name duplicates %q", i, row.Name)
		}
		seen[row.Name] = true
		if errs := validateReflexShape(row); len(errs) > 0 {
			return fmt.Errorf("reflexes[%d]: %s", i, strings.Join(errs, "; "))
		}
	}
	return nil
}

func decodeNativePolicy(e agentdef.Extension) (nativePolicyExtension, error) {
	var out nativePolicyExtension
	if err := decodeExtensionData(e.Data, map[string]bool{"permission_profile": true, "permission_mode": true, "model_selection": true}, &out); err != nil {
		return out, err
	}
	return out, nil
}

func decodeReflexPolicy(e agentdef.Extension) (reflexPolicyExtension, error) {
	var out reflexPolicyExtension
	if err := decodeExtensionData(e.Data, map[string]bool{"reflexes": true}, &out); err != nil {
		return out, err
	}
	return out, nil
}

func decodeExtensionData(data map[string]any, allowed map[string]bool, out any) error {
	for key := range data {
		if !allowed[key] {
			return fmt.Errorf("unknown field %q", key)
		}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return err
	}
	return nil
}

func (p nativePolicyExtension) permissionProfile() string {
	if p.PermissionProfile != "" {
		return p.PermissionProfile
	}
	return p.PermissionMode
}

func validateNativePermissionProfile(p string) error {
	switch p {
	case "default", "read-only":
		return nil
	default:
		return fmt.Errorf("permission profile %q must be default or read-only", p)
	}
}

func (r reflexPolicyReflex) storeRow(agentID string) (store.AgentReflex, error) {
	trigger, err := json.Marshal(r.TriggerSpec)
	if err != nil {
		return store.AgentReflex{}, err
	}
	action, err := json.Marshal(r.ActionSpec)
	if err != nil {
		return store.AgentReflex{}, err
	}
	optOut := true
	if r.OptOutAllowed != nil {
		optOut = *r.OptOutAllowed
	}
	return store.AgentReflex{
		AgentID:                   agentID,
		Name:                      r.Name,
		TriggerKind:               r.TriggerKind,
		TriggerSpec:               string(trigger),
		ActionKind:                r.ActionKind,
		ActionSpec:                string(action),
		Status:                    store.ReflexStatusActive,
		Priority:                  r.Priority,
		CreatedBy:                 "operator:agentdef",
		ProvenanceTier:            "operator",
		OptOutAllowed:             optOut,
		RecurrenceOverrideSeconds: r.RecurrenceOverrideSeconds,
	}, nil
}

func validateReflexShape(row store.AgentReflex) []string {
	var errs []string
	if row.Name == "" {
		errs = append(errs, "name is required")
	}
	switch row.TriggerKind {
	case store.ReflexTriggerPredicate, store.ReflexTriggerEvent, store.ReflexTriggerInterval:
	default:
		errs = append(errs, fmt.Sprintf("invalid trigger_kind %q", row.TriggerKind))
	}
	if row.TriggerSpec == "" || row.TriggerSpec == "null" {
		errs = append(errs, "trigger_spec is required")
	} else {
		var spec map[string]any
		if err := json.Unmarshal([]byte(row.TriggerSpec), &spec); err != nil {
			errs = append(errs, "trigger_spec: invalid JSON: "+err.Error())
		}
	}
	switch row.ActionKind {
	case store.ReflexActionInjectReminder, store.ReflexActionForceToolChoice,
		store.ReflexActionSendMessage, store.ReflexActionHaltSession, store.ReflexActionAddSchedule,
		store.ReflexActionDispatchToAgent, store.ReflexActionResumeLoopRun:
	default:
		errs = append(errs, fmt.Sprintf("invalid action_kind %q", row.ActionKind))
	}
	if row.ActionSpec == "" || row.ActionSpec == "null" {
		errs = append(errs, "action_spec is required")
	} else {
		var spec map[string]any
		if err := json.Unmarshal([]byte(row.ActionSpec), &spec); err != nil {
			errs = append(errs, "action_spec: invalid JSON: "+err.Error())
		} else if row.ActionKind == store.ReflexActionDispatchToAgent {
			slug, _ := spec["agent_slug"].(string)
			if slug == "" {
				errs = append(errs, "action_spec: dispatch_to_agent requires a non-empty agent_slug")
			}
		} else if row.ActionKind == store.ReflexActionResumeLoopRun {
			loopRunID, _ := spec["loop_run_id"].(string)
			if loopRunID == "" {
				errs = append(errs, "action_spec: resume_loop_run requires a non-empty loop_run_id")
			}
		}
	}
	if row.Status != "" && row.Status != store.ReflexStatusActive && row.Status != store.ReflexStatusPaused && row.Status != store.ReflexStatusExpired {
		errs = append(errs, fmt.Sprintf("invalid status %q", row.Status))
	}
	return errs
}

func definitionNativePolicy(d *agentdef.Definition) (nativePolicyExtension, bool, error) {
	if d == nil || d.Extensions == nil {
		return nativePolicyExtension{}, false, nil
	}
	e, ok := d.Extensions[NativePolicyExtension]
	if !ok {
		return nativePolicyExtension{}, false, nil
	}
	p, err := decodeNativePolicy(e)
	return p, true, err
}

func definitionReflexPolicy(d *agentdef.Definition) (reflexPolicyExtension, bool, error) {
	if d == nil || d.Extensions == nil {
		return reflexPolicyExtension{}, false, nil
	}
	e, ok := d.Extensions[ReflexPolicyExtension]
	if !ok {
		return reflexPolicyExtension{}, false, nil
	}
	p, err := decodeReflexPolicy(e)
	return p, true, err
}
