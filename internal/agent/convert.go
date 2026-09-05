package agent

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// ToProfile converts a Definition to a store.AgentProfile.
// JSON array fields are marshaled from typed Go slices.
//
// The file-based agent runtime (TASKS/adhoc/01-eliminate-file-based-agent-
// runtime.md) is eliminated: there is no more deterministic "file-<slug>"
// fallback identity. d.ID is either an explicitly imported identity or empty
// (for example, an internal builtin seed profile not yet in the DB) -- the standard agent-creation path
// (store.CreateAgent, called from upsertAgentDef) mints a fresh UUID for the
// empty case exactly the way it does for any other newly created agent.
func (d *Definition) ToProfile() *store.AgentProfile {
	now := time.Now().UTC().Format(time.RFC3339)

	p := &store.AgentProfile{
		ID:           strings.TrimSpace(d.ID),
		Name:         d.Name,
		Slug:         d.Slug,
		Avatar:       d.Avatar,
		SystemPrompt: d.SystemPrompt,
		Description:  d.Description,
		DefaultModel: d.Model,
		CanExecute:   d.PermissionMode == "yolo",
		Status:       "active",
		Source:       d.Source,
		SourceRef:    d.SourceRef,
		Icon:         d.Icon,
		Version:      1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// Marshal JSON array fields. Nil slices are normalized to empty arrays.
	p.Tools = marshalSlice(d.Tools)
	p.Tags = marshalSlice(d.Tags)
	p.MCPServers = marshalSlice(d.MCPServers)
	p.Directories = marshalSlice(d.Directories)

	// Constraints as JSON object.
	if d.Constraints != (AgentConstraints{}) {
		p.Constraints = marshalJSONOr(d.Constraints, "{}")
	} else {
		p.Constraints = "{}"
	}

	// agent_profiles.modes is a legacy denormalized column (pre-dates
	// Phase 0 item 21, "Cut Modes, in full"). It always carries "[]" now
	// — frontmatter no longer declares inline agent modes (ModeDefinition
	// / Definition.Modes were deleted with the rest of Legacy Agent Mode;
	// see TASKS/phase-0/21-cut-modes.md). The column itself is left in
	// place (out of that task's scope — it's not one of the
	// modes/agent_modes/agent_mode_assignments tables or the
	// sessions.current_mode_id column the task enumerates) but is
	// permanently inert.
	p.Modes = "[]"

	// ToolPermissions -- TASKS/adhoc/02-remove-tool-permissions-collapse-to-
	// agent-tools.md retired the tool_permissions/CheckPermission
	// enforcement machinery and the toolPermissions: frontmatter field
	// entirely; agent_tools is now the sole tool-selection gate for every
	// agent, ingested or not. The agent_profiles.tool_permissions column
	// itself is left in place (that task's schema decision) but always
	// written as an inert "{}" now — there is no more frontmatter input to
	// derive it from.
	p.ToolPermissions = "{}"

	// ParentDispatchAllowlist — CW-20260512-0107 (SP-20260512-0008 W2A).
	// JSON array of role slugs this agent may dispatch via task_execute;
	// rendered into the task_execute description by the Tool Broker
	// Describe hook. Default '[]' (no dispatch) matches the migration
	// 059 column default for legacy / non-parent profiles.
	p.ParentDispatchAllowlist = marshalSlice(d.ParentDispatchAllowlist)

	p.RoleTools = marshalSlice(d.RoleTools)

	if len(d.ContextPolicy) > 0 {
		p.ContextPolicy = marshalJSONOr(d.ContextPolicy, "{}")
	} else {
		p.ContextPolicy = "{}"
	}

	p.Durable = d.Durable
	if d.ActivationMode != "" {
		p.ActivationMode = d.ActivationMode
	}
	if d.Class != "" {
		p.Class = d.Class
	}
	if d.DefaultState != "" {
		p.DefaultState = d.DefaultState
	}

	p.Settings = "{}"
	return p
}

// marshalSlice marshals a string slice to JSON, normalizing nil to "[]".
func marshalSlice(v []string) string {
	if v == nil {
		return "[]"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// marshalJSONOr marshals v to JSON, returning fallback on error or nil input.
func marshalJSONOr(v any, fallback string) string {
	if v == nil {
		return fallback
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fallback
	}
	return string(b)
}
