package agent

import (
	"encoding/json"
	"fmt"
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

// ToProfile converts a Definition to a store.AgentProfile.
// JSON array fields are marshaled from typed Go slices.
// The ID is deterministic: "file-{slug}".
func (d *Definition) ToProfile() *store.AgentProfile {
	now := time.Now().UTC().Format(time.RFC3339)

	p := &store.AgentProfile{
		ID:           fileIDPrefix + d.Slug,
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

	// Modes as JSON array of slug strings (for the modes column).
	// Note: the profile no longer carries a `default_mode` field — the
	// active mode is a *session* attribute (sessions.current_mode_id), not
	// an agent attribute. See migration 063 + CW-20260512-0115.
	if len(d.Modes) > 0 {
		slugs := make([]string, len(d.Modes))
		for i, m := range d.Modes {
			slugs[i] = m.Slug
		}
		p.Modes = marshalJSONOr(slugs, "[]")
	} else {
		p.Modes = "[]"
	}

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

	p.Settings = "{}"
	return p
}

// ToModes converts the inline mode definitions to store.AgentMode slices.
// Each mode gets a deterministic ID: "file-{agentSlug}-{modeSlug}".
func (d *Definition) ToModes() []store.AgentMode {
	modes := make([]store.AgentMode, len(d.Modes))
	agentID := fileIDPrefix + d.Slug

	for i, m := range d.Modes {
		modes[i] = store.AgentMode{
			ID:             fmt.Sprintf("%s%s-%s", fileIDPrefix, d.Slug, m.Slug),
			AgentID:        agentID,
			Slug:           m.Slug,
			Name:           m.Name,
			PromptAddendum: m.PromptAddendum,
			ToolOverrides:  marshalJSONOr(m.ToolOverrides, "{}"),
			Settings:       "{}",
		}
	}
	return modes
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
