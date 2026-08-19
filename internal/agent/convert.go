package agent

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

const fileIDPrefix = "file-"

// IsFileBasedID returns true if the agent ID was generated from a file-based definition.
func IsFileBasedID(id string) bool {
	return len(id) > len(fileIDPrefix) && id[:len(fileIDPrefix)] == fileIDPrefix
}

// SlugFromFileID extracts the slug from a file-based agent ID.
func SlugFromFileID(id string) string {
	if !IsFileBasedID(id) {
		return ""
	}
	return id[len(fileIDPrefix):]
}

// CanonicalID returns the identity used for the DB projection row and all
// FK children. A managed file stamped with a UUID (`id:` frontmatter) owns
// that UUID; embedded/unstamped definitions fall back to the deterministic
// "file-<slug>" runtime identity that the harness hard-codes in several
// places (e.g. the file-default chat-role-harness template binding). Keeping
// unstamped agents on "file-<slug>" is deliberate — only managed-writable
// files get a real UUID written back.
func (d *Definition) CanonicalID() string {
	if id := strings.TrimSpace(d.ID); id != "" {
		return id
	}
	return fileIDPrefix + d.Slug
}

// ToProfile converts a Definition to a store.AgentProfile.
// JSON array fields are marshaled from typed Go slices.
// The ID is deterministic: "file-{slug}".
func (d *Definition) ToProfile() *store.AgentProfile {
	now := time.Now().UTC().Format(time.RFC3339)

	p := &store.AgentProfile{
		ID:           d.CanonicalID(),
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

	// ToolPermissions — frontmatter wins; fall back to deriving an allow_list
	// from Tools so existing agents keep their implicit allowlist behavior.
	switch {
	case d.ToolPermissions != nil:
		p.ToolPermissions = marshalJSONOr(d.ToolPermissions, "{}")
	case len(d.Tools) > 0:
		tp := map[string]any{"allow_list": d.Tools}
		p.ToolPermissions = marshalJSONOr(tp, "{}")
	default:
		p.ToolPermissions = "{}"
	}

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
