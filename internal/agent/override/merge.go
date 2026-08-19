// Package override implements a three-layer config override cascade with
// per-field merge semantics: base → project → session.
package override

import "strings"

// OverrideConfig holds per-field overrides that can be layered on top of a
// base agent config. All fields use omitempty so zero values are omitted from
// serialised output and do not shadow lower-priority layers.
type OverrideConfig struct {
	// Scalars — last non-empty writer wins.
	Model       string `yaml:"model,omitempty"       json:"model,omitempty"`
	Provider    string `yaml:"provider,omitempty"    json:"provider,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// SystemPrompt and Class back the role -> agent -> task cascade
	// (TASKS/phase-1/01-add-roles-table-and-cascade-resolution.md) — see
	// internal/service/role_cascade.go, the first real caller of this
	// package. Same last-non-empty-writer-wins scalar semantics as Model/
	// Provider/Description above.
	SystemPrompt string `yaml:"system_prompt,omitempty" json:"system_prompt,omitempty"`
	Class        string `yaml:"class,omitempty"          json:"class,omitempty"`
	// ModelID backs 02-add-agents-composition-columns.md's relational
	// model_id FK (agent_profiles.model_id -> models(id)), distinct from
	// the free-text Model/Provider scalars above. roles has no model_id-
	// equivalent column today, so the role layer never contributes a
	// value for this field (RoleOverrideConfig leaves it empty) — it's
	// wired into the same three-tier cascade shape as everything else so
	// a future role-level default has a seam to land in without a second,
	// parallel resolution path, per that task's own explicit instruction.
	ModelID string `yaml:"model_id,omitempty" json:"model_id,omitempty"`

	// MessageWakePolicy backs chat.AgentConstraints.MessageWakePolicy
	// (CW-20260816-0065) — same three-value vocabulary
	// (render_and_wait/auto_summarize/batch) as the agent-profile
	// constraints field it mirrors. Added by Phase 3 item 01
	// (TASKS/phase-3/01-collapse-resolveprovider-into-cascade.md), which
	// folded internal/service's resolveMessageWakePolicy into this
	// package's shared merge engine instead of its own bespoke three-step
	// walk. Same "no live role-level source column yet, but wire the seam"
	// precedent as ModelID above: roles has no message_wake_policy-
	// equivalent column, so only the agent (project) and session (task)
	// layers ever populate this in practice today — see
	// internal/service/messaging_reactor.go's resolveMessageWakePolicy,
	// the caller.
	MessageWakePolicy string `yaml:"message_wake_policy,omitempty" json:"message_wake_policy,omitempty"`

	// Lists — union with optional +/- prefix support.
	Tools       []string `yaml:"tools,omitempty"       json:"tools,omitempty"`
	Skills      []string `yaml:"skills,omitempty"      json:"skills,omitempty"`
	MCPServers  []string `yaml:"mcp_servers,omitempty" json:"mcp_servers,omitempty"`
	Tags        []string `yaml:"tags,omitempty"        json:"tags,omitempty"`
	Directories []string `yaml:"directories,omitempty" json:"directories,omitempty"`

	// Maps — shallow deep-merge (overlay keys replace base keys).
	Permissions map[string]any `yaml:"permissions,omitempty"  json:"permissions,omitempty"`
	Settings    map[string]any `yaml:"settings,omitempty"     json:"settings,omitempty"`
	Constraints map[string]any `yaml:"constraints,omitempty"  json:"constraints,omitempty"`
}

// Resolve applies layers in order: base → project → session.
// Nil layers are silently skipped.
func Resolve(base OverrideConfig, project, session *OverrideConfig) OverrideConfig {
	result := base
	if project != nil {
		result = applyLayer(result, *project)
	}
	if session != nil {
		result = applyLayer(result, *session)
	}
	return result
}

// ResolveWithMap applies the wildcard "*" override first, then the
// agent-specific override, and finally the optional session override.
func ResolveWithMap(base OverrideConfig, overrides map[string]OverrideConfig, agentKey string, session *OverrideConfig) OverrideConfig {
	result := base
	if wildcard, ok := overrides["*"]; ok {
		result = applyLayer(result, wildcard)
	}
	if specific, ok := overrides[agentKey]; ok {
		result = applyLayer(result, specific)
	}
	if session != nil {
		result = applyLayer(result, *session)
	}
	return result
}

// applyLayer merges one override layer onto base according to per-field rules.
func applyLayer(base, layer OverrideConfig) OverrideConfig {
	// Scalars: last non-empty writer wins.
	if layer.Model != "" {
		base.Model = layer.Model
	}
	if layer.Provider != "" {
		base.Provider = layer.Provider
	}
	if layer.Description != "" {
		base.Description = layer.Description
	}
	if layer.SystemPrompt != "" {
		base.SystemPrompt = layer.SystemPrompt
	}
	if layer.Class != "" {
		base.Class = layer.Class
	}
	if layer.ModelID != "" {
		base.ModelID = layer.ModelID
	}
	if layer.MessageWakePolicy != "" {
		base.MessageWakePolicy = layer.MessageWakePolicy
	}

	// Lists: union with +/- prefix support.
	base.Tools = mergeList(base.Tools, layer.Tools)
	base.Skills = mergeList(base.Skills, layer.Skills)
	base.MCPServers = mergeList(base.MCPServers, layer.MCPServers)
	base.Tags = mergeList(base.Tags, layer.Tags)
	base.Directories = mergeList(base.Directories, layer.Directories)

	// Maps: shallow deep-merge.
	base.Permissions = mergeMaps(base.Permissions, layer.Permissions)
	base.Settings = mergeMaps(base.Settings, layer.Settings)
	base.Constraints = mergeMaps(base.Constraints, layer.Constraints)

	return base
}

// mergeList computes the union of base and overlay entries, respecting +/-
// prefix semantics in overlay:
//   - "+foo" or "foo" — add "foo" to the result set
//   - "-foo"          — remove "foo" from the result set
func mergeList(base, overlay []string) []string {
	if len(overlay) == 0 {
		return base
	}

	// Seed the ordered set from base.
	order := make([]string, 0, len(base)+len(overlay))
	seen := make(map[string]bool, len(base))
	for _, v := range base {
		if !seen[v] {
			seen[v] = true
			order = append(order, v)
		}
	}

	for _, entry := range overlay {
		switch {
		case strings.HasPrefix(entry, "-"):
			key := entry[1:]
			// Remove from order slice and seen map.
			if seen[key] {
				delete(seen, key)
				filtered := order[:0]
				for _, v := range order {
					if v != key {
						filtered = append(filtered, v)
					}
				}
				order = filtered
			}
		case strings.HasPrefix(entry, "+"):
			key := entry[1:]
			if !seen[key] {
				seen[key] = true
				order = append(order, key)
			}
		default:
			if !seen[entry] {
				seen[entry] = true
				order = append(order, entry)
			}
		}
	}

	return order
}

// mergeMaps performs a shallow deep-merge: overlay keys replace base keys,
// missing keys in the overlay are left unchanged.
func mergeMaps(base, overlay map[string]any) map[string]any {
	if len(overlay) == 0 {
		return base
	}
	result := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range overlay {
		result[k] = v
	}
	return result
}
