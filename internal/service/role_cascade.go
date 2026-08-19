package service

import (
	"encoding/json"

	"github.com/hollis-labs/nanite/internal/agent/override"
	"github.com/hollis-labs/nanite/internal/store"
)

// RoleOverrideConfig maps a store.Role's default-hint columns into the
// broadest ("base") layer of the role -> agent -> task cascade described
// in architecture/01-agent-construction.md and decision log §6. A nil
// role (no role bound -- agent_profiles.role_id doesn't exist until
// 02-add-agents-composition-columns.md adds and backfills it) returns the
// zero value, contributing nothing to the merge.
func RoleOverrideConfig(role *store.Role) override.OverrideConfig {
	if role == nil {
		return override.OverrideConfig{}
	}
	cfg := override.OverrideConfig{
		SystemPrompt: role.SystemPrompt,
		Class:        role.DefaultClass,
		Model:        role.DefaultModel,
		Provider:     role.DefaultProvider,
		Tools:        decodeJSONStringArray(role.DefaultTools),
		Skills:       decodeJSONStringArray(role.DefaultSkills),
		Permissions:  decodeJSONObject(role.DefaultPermissions),
	}
	return cfg
}

// AgentOverrideConfig maps an already-loaded agent composition's own field
// values into the middle ("agent"/composition-scope) layer of the cascade
// -- between the role's broadest defaults and any task/invocation-level
// override. A nil profile returns the zero value.
func AgentOverrideConfig(profile *store.AgentProfile) override.OverrideConfig {
	if profile == nil {
		return override.OverrideConfig{}
	}
	cfg := override.OverrideConfig{
		SystemPrompt: profile.SystemPrompt,
		Class:        profile.Class,
		Model:        profile.DefaultModel,
		Provider:     profile.DefaultProvider,
		Tools:        decodeJSONStringArray(profile.Tools),
		Skills:       decodeJSONStringArray(profile.RoleSkills),
		Permissions:  decodeJSONObject(profile.ToolPermissions),
	}
	return cfg
}

// ResolveAgentCascade implements the role -> agent -> task closest-wins
// cascade (architecture/01-agent-construction.md, decision log §6:
// "Cascading override resolution, closest wins... Role (broadest
// defaults) -> Agent/composition (scope-specific settings) -> Task/
// invocation (narrowest, most specific)"), reusing internal/agent/
// override's existing three-layer merge engine rather than a second,
// parallel implementation. role and taskOverride may both be nil:
//   - role is nil for every agent_profiles row today, since role_id
//     doesn't exist until 02-add-agents-composition-columns.md lands.
//   - taskOverride is nil whenever no caller supplies a per-invocation
//     override (nothing does yet -- ResolveForSession's signature has no
//     override parameter; a future task-level dispatch caller can pass
//     one once it exists, without this function's shape changing).
//
// class follows the same three-tier cascade as every other field, per
// decision log §6 -- not special-cased.
func ResolveAgentCascade(role *store.Role, profile *store.AgentProfile, taskOverride *override.OverrideConfig) override.OverrideConfig {
	roleLayer := RoleOverrideConfig(role)
	agentLayer := AgentOverrideConfig(profile)
	return override.Resolve(roleLayer, &agentLayer, taskOverride)
}

func decodeJSONStringArray(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func decodeJSONObject(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}
