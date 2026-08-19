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

// OverlayDBFields overlays db's DB-authoritative field values onto p (a
// profile already produced by ToProfile()), when db is non-nil. Call this
// whenever a file-backed Definition's ToProfile() result also has a real
// agent_profiles DB row — agentServiceImpl.Get/GetBySlug/List
// (TASKS/phase-1/12-fix-agent-service-get-drops-new-db-only-columns.md) do
// this for every lookup that resolves through an in-memory Definition.
// Returns p unchanged when db is nil (the "file-backed but no DB row yet"
// case — a genuinely new, not-yet-ingested file — where p's DB-only fields
// are already left at ToProfile()'s sensible zero-value default).
//
// This is the read-side analogue of the "DB is authoritative once a row
// exists" convention 08-kill-file-reingest-on-boot-pattern.md established
// for the boot-time write path: once ingested, a row's DB values can
// diverge from whatever a fresh parse of the file would produce — a direct
// API edit to a field the frontmatter can't represent at all, or a
// hand-edited file whose content a frozen boot-time reingest never synced
// back into the row — so ToProfile()'s file-derived guess must not be
// returned as-is once that DB row exists.
//
// role_id / model_id / runtime_kind / consumer_id (Phase 1 items 02/03)
// have zero frontmatter representation at all — ToProfile() can only ever
// return their empty zero-value for a file-backed def, so db's value is
// the only place any of these four is ever populated for one.
//
// activation_mode / class / default_state DO have frontmatter fields (see
// managedFileFrontmatter and ToProfile()'s own partial handling of them),
// so at first glance they look like they need no DB-wins treatment. That
// partial handling ("file wins when non-empty") is still correct for the
// write/ingest path (upsertAgentDef: a fresh row, or an explicit reimport
// immediately after a managed-agent write, both of which keep the file and
// the row in lockstep). It's insufficient on the read path once a row
// already exists, for exactly the same reason as the DB-only four above:
// nothing keeps a hand-edited file's frontmatter and the DB row in
// lockstep outside of an explicit reimport (and, post-08, a boot-time
// reingest of an already-ingested row is deliberately frozen and will
// never re-sync a file-side edit into the DB), so a stale/diverged file
// value could otherwise shadow the row's real, current value on every
// read. DB wins here too.
func OverlayDBFields(p, db *store.AgentProfile) *store.AgentProfile {
	if p == nil || db == nil {
		return p
	}
	p.RoleID = db.RoleID
	p.ModelID = db.ModelID
	p.RuntimeKind = db.RuntimeKind
	p.ConsumerID = db.ConsumerID
	p.ActivationMode = db.ActivationMode
	p.Class = db.Class
	p.DefaultState = db.DefaultState
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
