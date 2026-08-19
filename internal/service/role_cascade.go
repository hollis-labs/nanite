package service

import (
	"encoding/json"

	"github.com/hollis-labs/nanite/internal/agent/override"
	"github.com/hollis-labs/nanite/internal/store"
)

// RoleOverrideConfig maps a store.Role's default-hint columns into the
// broadest ("base") layer of the role -> agent -> task cascade described
// in architecture/01-agent-construction.md and decision log §6. A nil
// role (no role bound -- agent_profiles.role_id was nil for every row
// until 02-add-agents-composition-columns.md added and backfilled it)
// returns the zero value, contributing nothing to the merge.
//
// ModelID is deliberately left unset here: roles (migration 114) has no
// model_id-equivalent column, only the free-text DefaultModel/
// DefaultProvider hints already mapped to Model/Provider above -- there is
// no role-level default for the relational FK today.
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
//
// ModelID (02-add-agents-composition-columns.md) is wired here as a plain
// passthrough of the composition's own value -- since RoleOverrideConfig
// never supplies one (see above) and no caller supplies a task-level
// override for it yet, this is a proven no-op today for every row without
// role_id/model_id populated, same shape as Model/Provider before 01
// landed. RuntimeKind is still deliberately NOT included in this cascade:
// Phase 2 item 01 (TASKS/phase-2/01-wire-runtime-kind-routing.md) made
// runtime_kind live for CLI-vs-API routing, but reads it straight off the
// already-resolved *store.AgentProfile ResolveForSession/Get produce
// (agent.RuntimeKind) at the routing call sites themselves
// (service/chat_generate.go, service/chat.go's classifyNilProvider) --
// not through this override-merge cascade. runtime_kind isn't a per-role
// or per-task overridable setting the way system_prompt/model/tools are;
// it's a fixed property of which runtime an agent composition boots
// under, so there is no cascade layer for it to merge across.
func AgentOverrideConfig(profile *store.AgentProfile) override.OverrideConfig {
	if profile == nil {
		return override.OverrideConfig{}
	}
	cfg := override.OverrideConfig{
		SystemPrompt: profile.SystemPrompt,
		Class:        profile.Class,
		Model:        profile.DefaultModel,
		Provider:     profile.DefaultProvider,
		ModelID:      profile.ModelID,
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
