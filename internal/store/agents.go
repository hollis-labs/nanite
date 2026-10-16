package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/agentpolicy"
)

// AgentProfile represents an agent profile record.
type AgentProfile struct {
	// DefinitionPolicy is a transient runtime projection of verified immutable
	// content. It is never accepted or persisted as editable profile JSON.
	DefinitionPolicy *agentpolicy.NativePolicy `json:"-"`
	DefinitionReflex *agentpolicy.ReflexBundle `json:"-"`
	NativeHost       *NativeHostSettings       `json:"-"`
	// Revision is an opaque identity for the complete persisted profile and assignments.
	Revision        string `json:"-"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	Avatar          string `json:"avatar"`
	SystemPrompt    string `json:"system_prompt"`
	Description     string `json:"description"`
	Modes           string `json:"modes"`
	DefaultModel    string `json:"default_model"`
	DefaultProvider string `json:"default_provider"`
	MCPServers      string `json:"mcp_servers"`
	// ToolPermissions (allow_list/deny_list JSON) was DEPRECATED as of
	// Phase 1 item 04 (TASKS/phase-1/04-add-known-tools-and-agent-tools-
	// fk.md) in favor of the FK-based agent_tools join
	// (internal/store/agent_tools.go), and its enforcement machinery
	// (toolclient.ToolPermissions/CheckPermission/GetPermissions/
	// ParsePermissions/PermissionResolver) was deleted outright by
	// TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md:
	// agent_tools (+ the known_tools.always_included escape hatch) is the
	// sole tool-selection/execution gate everywhere now, including the
	// deeper ToolClient.CallTool backstop that used to also read this
	// column. The column is left in place (that task's schema decision —
	// reversible, low-risk to defer) but is now permanently inert: nothing
	// reads it for access control, and new agents always get "{}" here
	// (internal/agent's Definition.ToProfile()). Pre-existing rows may
	// still carry real historical JSON from before that task; it is dead
	// data. Its CURRENT value at the time was carried into agent_tools for
	// every existing agent by the earlier one-time backfill
	// (internal/service/known_tools_backfill.go).
	ToolPermissions string `json:"tool_permissions"`
	CanExecute      bool   `json:"can_execute"`
	Settings        string `json:"settings"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	// Schema v2 fields
	AgentHash string `json:"agent_hash"`
	Version   int    `json:"version"`
	// Tools (schema-v2 allowlist of tool-name patterns) is DEPRECATED as of
	// Phase 1 item 04 in favor of agent_tools, same as ToolPermissions
	// above, but — unlike ToolPermissions — is NOT inert: it (unioned with
	// RoleTools) is still the pattern-match input
	// internal/service/known_tools_backfill.go's one-time-per-agent
	// backfill reads to derive a brand-new agent's initial agent_tools
	// grant set. Not read anywhere else (SelectForAgent/
	// enforceExecutionRulesViaAgentTools consult agent_tools directly, not
	// this column, once a grant exists).
	Tools       string `json:"tools"`
	Directories string `json:"directories"`
	Constraints string `json:"constraints"`
	Tags        string `json:"tags"`
	Status      string `json:"status"`
	Source      string `json:"source"`
	SourceRef   string `json:"source_ref"`
	Icon        string `json:"icon"`
	// S7 T5 — registry extension. Kind names the provenance of the
	// agent row (internal = DB/file-based agent, external = auto-
	// registered on first messaging call, cli = CLI caller with a
	// deterministic ID). CapabilitiesJSON / LimitsJSON / ModelStrategy
	// are reserved for the future capability broker; they are
	// read-only placeholders for MVP.
	Kind             string `json:"kind"`
	CapabilitiesJSON string `json:"capabilities_json"`
	LimitsJSON       string `json:"limits_json"`
	ModelStrategy    string `json:"model_strategy"`
	// J7 ingestion metadata (CW-20260421-0011).
	ImportedAt   string `json:"imported_at"`   // RFC3339 timestamp of last ingest; empty for non-file agents
	OriginSystem string `json:"origin_system"` // "nanite", "agentrc", "claude", etc. — free-form provenance
	Format       string `json:"format"`        // "markdown", "yaml"

	// ParentDispatchAllowlist is a JSON array of role slugs this agent
	// (as a *parent*) may dispatch via task_execute. Surfaced into the
	// task_execute description through the Tool Broker Describe hook
	// (CW-20260512-0105 W1B) so the LLM picks intent fit, not role-name
	// memory. Empty "[]" means no dispatch permission — Describer renders
	// the baseline description without role enumeration.
	// Added by CW-20260512-0107 (SP-20260512-0008 W2A — migration 059).
	ParentDispatchAllowlist string `json:"parent_dispatch_allowlist"`

	// RoleTools is a JSON array of tool-name patterns this agent should be
	// pre-seeded with at create time. The agent config transaction parses
	// this field and inserts one agent_known_tools
	// row per entry with pinned=1, reason='role_seed'. Empty "[]" means no
	// seed. Added by FU-7a (migration 066).
	//
	// DEPRECATED as of Phase 1 item 04
	// (TASKS/phase-1/04-add-known-tools-and-agent-tools-fk.md): its names
	// now also produce real agent_tools grant rows (granted_via='role_seed',
	// seedAgentConfig for operator writes, seedRoleToolsFromIngest for ingest),
	// on top of the
	// agent_known_tools seeding described above, which is unchanged.
	// Existing agents' current values were carried into agent_tools once
	// by this task's backfill (internal/service/known_tools_backfill.go).
	RoleTools string `json:"role_tools"`

	// RoleSkills is a JSON array of skill slugs this agent should be
	// pre-seeded with at create time. AgentConfigService.Create/Update seed
	// one agent_known_skills row per entry with pinned=1, reason='role_seed'
	// (seedAgentConfig for config writes). Empty "[]" means no seed.
	//
	// The seeded row is a CATALOG ENTRY, not a capability grant: it carries
	// no ApprovedContentHash, so internal/skill/gate.go still refuses
	// execution until the skill is explicitly approved for this agent. A
	// slug missing from the skills catalog is skipped with a warning rather
	// than failing the write. Added by FU-33 (migration 074).
	RoleSkills string `json:"role_skills"`

	// ContextPolicy is a JSON object describing how this durable agent's
	// live model context should be cycled. It is intentionally policy-only:
	// durable truth stays in session logs, agent DB, Tesseract, Torque, etc.
	ContextPolicy string `json:"context_policy"`

	// Durable, when true, exempts this row from migration 061's eject
	// predicate (source != 'internal' AND durable = 0). Source='internal'
	// rows survive regardless; this flag is the survival opt-in for any
	// other source (typically API-created agents at source='user').
	// Default false matches the column default in migration 069.
	Durable bool `json:"durable"`

	// Class is
	// one of advisor/process/template/harness; ActivationMode is singleton
	// or instance; DefaultState is sleeping or active. Defaults track
	// migration 070. Enum-shape fields are enforced at the Go layer (see
	// validateAgentMultiAgentFields) since SQLite ALTER TABLE ADD COLUMN
	// can't carry an idempotent CHECK after the column exists.
	ActivationMode string `json:"activation_mode"`
	Class          string `json:"class"`
	DefaultState   string `json:"default_state"`

	// ConsumerID is a nullable FK to consumers(id) -- the ownership/tenancy
	// tag identifying which external system this agent belongs to (e.g.
	// Loom owns Curator). Empty string means internal/operator-owned, per
	// decision log Section 4. See internal/store/consumers.go and
	// docs/engineering/architecture/01-agent-construction.md. Added by
	// migration 112 (TASKS/phase-1/03-add-consumers-table.md).
	ConsumerID string `json:"consumer_id"`

	// RoleID is a nullable FK to roles(id) -- the broadest layer of the
	// role -> agent -> task cascade (see internal/service/role_cascade.go
	// and architecture/01-agent-construction.md). Empty string means no
	// role bound yet; nullable during transition per
	// 02-add-agents-composition-columns.md's own text (existing rows have
	// no role until 10-data-migrate-nanite-agents-md.md backfills one,
	// which is out of Phase 1 scope). Added by migration 117.
	RoleID string `json:"role_id"`

	// ModelID is a nullable FK to models(id) -- a relational reference into
	// the DB-authoritative models catalog (06-fix-models-table-sync-
	// target.md), distinct from the pre-existing free-text
	// DefaultModel/DefaultProvider scalars that already participate in the
	// role->agent->task cascade. Empty string means no relational model
	// bound yet. Added by migration 117.
	ModelID string `json:"model_id"`

	// RuntimeKind is 'cli' or 'api' -- see GLOSSARY.md's "Runtime kind"
	// entry. Populated for every row (backfilled by migration 117 from
	// each row's pre-existing default_provider via inferRuntimeKind,
	// mirroring chat.IsCLIProvider; defaulted the same way for every row
	// created afterward by applyMultiAgentDefaults) but deliberately not
	// yet consulted anywhere for CLI-vs-API routing decisions -- wiring it
	// as the actual routing switch is Phase 2's job, not this task's.
	RuntimeKind string `json:"runtime_kind"`

	// Protocol/Transport (TASKS/agent-host-acp/11-nanite-per-agent-protocol-
	// transport-config.md) select which wire protocol/transport pairing this
	// agent's CLI process actually launches through -- orthogonal to
	// RuntimeKind (which decides CLI vs. API substrate; an ACP-configured
	// agent still carries RuntimeKind="cli" unchanged). Empty/unset means
	// "use this provider's existing native protocol" -- 17-acp.md's
	// explicit "additive, not a cutover" framing; no agent silently
	// switches to ACP without an operator explicitly writing
	// Protocol="acp". Transport is only meaningful when Protocol="acp"
	// (task 10's Copilot CLI adapter supports both "stdio" and "tcp"; every
	// native protocol's transport is implied by the protocol itself).
	// Consulted once at launch by internal/runtime/agent/factory.go's
	// useACPProtocol/effectiveACPTransport. Added by migration 134.
	Protocol  string `json:"protocol"`
	Transport string `json:"transport"`

	// PluginID tags this row as created/owned by a plugin's
	// registers.agent_profiles[] registration (Phase 5 item 03,
	// TASKS/phase-5/03-wire-registers-agent-profiles.md) -- the plugin's
	// canonical id (manifest.Identifier() / p.ID()), mirroring the
	// artifacts.source_plugin_id precedent. Empty string means this row is
	// operator/GUI-created (or predates Phase 5 item 03). Read by
	// ListAgentsByPluginID and plugin.Host.UnloadPlugin's unload sweep so a
	// plugin uninstall/unload correctly removes what it registered. Added
	// by migration 122.
	PluginID string `json:"plugin_id"`

	// TetherManaged is the per-agent opt-in for Tether registration.
	// Default false -- registering an agent in Tether's federation
	// directory is an explicit, per-agent decision, not ambient behavior.
	// Added by migration 162.
	TetherManaged bool `json:"tether_managed"`

	// TetherURN holds this agent's Tether directory URN once one exists
	// (minted via tether_registry_register -- an ordinary agent tool call,
	// not something this store package or its callers perform). Empty
	// until minted, regardless of TetherManaged. See
	// internal/runtime/agent/tether_identity.go, which plants this value
	// into a CLI session's boot dir when non-empty, and
	// docs/adding-an-agent.md's "Materialize, then boot" section for why
	// minting happens once, upstream of any boot, rather than as part of
	// this flow. Added by migration 162.
	TetherURN string `json:"tether_urn"`
}

// validateAgentMultiAgentFields enforces the enum constraints that
// migration 070 deliberately did not encode at the column level. FU-28.
//
// activation_mode's valid set was widened from 'singleton'/'instance' to
// 'singleton'/'fresh-per-wake'/'concurrent' by migration 117 (Phase 1 item
// 02, TASKS/phase-1/02-add-agents-composition-columns.md) -- the real
// design decision documented in that task's Work Log: extend this existing
// column to the 3-value instance_mode shape architecture/
// 01-agent-construction.md calls for, rather than add a second, competing
// column, since exhaustive grep confirmed nothing anywhere read the old
// 2-value column for behavior (durable_wake.go's wakeSkipReason -- the
// intended real consumer -- switched on lifecycle_class instead). 'instance'
// is retired as a valid value; every pre-existing row was migrated to
// 'fresh-per-wake' in lockstep.
func validateAgentMultiAgentFields(a *AgentProfile) error {
	if _, err := ValidateAgentBehaviorFields(a); err != nil {
		return err
	}

	// Protocol/Transport (TASKS/agent-host-acp/11) also carry a real
	// DB-level CHECK (migration 134), mirrored here for the same clean-
	// Go-error-instead-of-raw-CHECK-failure reason as runtime_kind above.
	return ValidateAgentACPFields(a.Protocol, a.Transport)
}

// ValidateAgentBehaviorFields validates profile enums and identifies an invalid field.
func ValidateAgentBehaviorFields(a *AgentProfile) (string, error) {
	switch a.ActivationMode {
	case "", "singleton", "fresh-per-wake", "concurrent":
	default:
		return "activation_mode", fmt.Errorf("activation_mode %q invalid: must be 'singleton', 'fresh-per-wake', or 'concurrent'", a.ActivationMode)
	}
	switch a.Class {
	case "", "advisor", "process", "template", "harness":
	default:
		return "class", fmt.Errorf("class %q invalid: must be 'advisor', 'process', 'template', or 'harness'", a.Class)
	}
	switch a.DefaultState {
	case "", "sleeping", "active":
	default:
		return "default_state", fmt.Errorf("default_state %q invalid: must be 'sleeping' or 'active'", a.DefaultState)
	}
	// runtime_kind also carries a real DB-level CHECK (migration 117,
	// unlike the three enums above), but validating it here too gives API
	// callers a clean Go error instead of a raw SQLite CHECK-constraint
	// failure, matching this function's existing job for every other
	// enum-shaped column on this row.
	switch a.RuntimeKind {
	case "", "cli", "api":
	default:
		return "runtime_kind", fmt.Errorf("runtime_kind %q invalid: must be 'cli' or 'api'", a.RuntimeKind)
	}
	return "", nil
}

// ValidateAgentACPFields is the canonical protocol/transport validation used
// by profile and assignment writes before SQL constraints are involved.
func ValidateAgentACPFields(protocol, transport string) error {
	switch protocol {
	case "", "claude-stream-json", "codex-app-server", "opencode-native", "acp":
	default:
		return fmt.Errorf("protocol %q invalid: must be '', 'claude-stream-json', 'codex-app-server', 'opencode-native', or 'acp'", protocol)
	}
	switch transport {
	case "", "stdio", "tcp":
	default:
		return fmt.Errorf("transport %q invalid: must be '', 'stdio', or 'tcp'", transport)
	}
	if transport != "" && protocol != "acp" {
		return fmt.Errorf("transport %q is only valid when protocol is 'acp' (got protocol %q)", transport, protocol)
	}
	return nil
}

// applyMultiAgentDefaults applies the FU-28 column defaults (activation_mode,
// class, default_state, runtime_kind). It no longer mints a URN — see the
// LegacyURN field comment and migration 159. Historically it minted one and
// pushed a slug-form alias so
// legacy routing keeps resolving. FU-28; runtime_kind + the class-aware
// activation_mode default added by migration 117 (Phase 1 item 02).
//
// Class is defaulted *before* ActivationMode here (reordered from this
// function's original shape) because DefaultActivationModeForClass needs
// a's final class value, not whatever it was before applyMultiAgentDefaults
// ran -- a caller that sets Class="process" and leaves ActivationMode empty
// must still land on 'fresh-per-wake', not 'singleton'.
func applyMultiAgentDefaults(a *AgentProfile) {
	if a.Class == "" {
		a.Class = "advisor"
	}
	if a.ActivationMode == "" {
		a.ActivationMode = DefaultActivationModeForClass(a.Class)
	}
	if a.DefaultState == "" {
		a.DefaultState = "sleeping"
	}
	if a.RuntimeKind == "" {
		a.RuntimeKind = inferRuntimeKind(a.DefaultProvider)
	}
	// No URN is minted here, and none is modeled. A profile is a definition,
	// not a recipient — actor identity lives on durable_agent_instances.urn
	// (migration 159, CW-20260912-0017). agent_profiles.legacy_urn and
	// legacy_urn_aliases still hold what the retired minter wrote, as history
	// for reconciling against Tether; no Go code reads or writes them, and
	// re-adding a reader would re-create the address this migration removed.
}

// DefaultActivationModeForClass returns the activation_mode value a newly
// created composition should default to when the caller leaves it unset,
// derived from class. Exported so internal/api/agent_builder.go's draft
// advisor (which pre-fills a suggested ActivationMode for the operator to
// review before create, rather than leaving it empty for
// applyMultiAgentDefaults to fill in later) can share this exact mapping
// instead of hardcoding its own -- avoiding a second, drifting source of
// the same decision.
//
// process and template both default to 'fresh-per-wake': both use a
// fresh/one-shot session policy with no reuse collision for "already
// active" to guard against (durableAgentLaunchPolicyFor's
// SessionPolicyFreshPerWake and SessionPolicyFreshOneShot, respectively --
// see internal/service/durable_agents.go). advisor and harness (or
// anything else, including empty/unrecognized values) default to
// 'singleton', matching durable_wake.go's pre-migration-116 behavior for
// every class other than process.
func DefaultActivationModeForClass(class string) string {
	switch class {
	case "process", "template":
		return "fresh-per-wake"
	default:
		return "singleton"
	}
}

// inferRuntimeKind mirrors chat.IsCLIProvider's exact classification
// (name == "pty" OR has prefix "pty-" OR has prefix "sub-" => cli, else
// api) without importing internal/chat -- internal/chat already imports
// internal/store, so the reverse import would cycle.
//
// This once cited urnPrefix/generateAgentURN in this file as precedent for
// the same convention. Do not restore that citation: those were deleted by
// CW-20260912-0017, and their stated cycle was not real -- the canonical
// helper lived in internal/a2a, which this file already imports. The mirror
// drifted into another product's authority and minted every agent URN wrong
// for the life of the feature. Verify the cycle before mirroring; the claim
// above is about a different import pair and still holds. Kept in
// lockstep with chat.IsCLIProvider and this migration's SQL backfill
// (117_agent_profiles_composition_columns.sql) by hand; chat/engine.go's
// own doc comment lists every other site that same classification must not
// drift from.
func inferRuntimeKind(providerName string) string {
	if providerName == "pty" || strings.HasPrefix(providerName, "pty-") || strings.HasPrefix(providerName, "sub-") {
		return "cli"
	}
	return "api"
}

// agentColumns is the canonical SELECT column list for agent_profiles.
const agentColumns = `id, name, slug, COALESCE(avatar,''), system_prompt, COALESCE(description,''),
        modes, COALESCE(default_model,''), COALESCE(default_provider,''),
        mcp_servers, tool_permissions, can_execute, settings, created_at, updated_at,
        agent_hash, version, tools, directories, constraints, tags, status, source, source_ref,
        COALESCE(icon,''),
        kind, capabilities_json, limits_json, model_strategy,
        COALESCE(imported_at,''), COALESCE(origin_system,''), COALESCE(format,'markdown'),
        COALESCE(parent_dispatch_allowlist,'[]'),
        COALESCE(role_tools,'[]'),
        COALESCE(role_skills,'[]'),
        COALESCE(context_policy,'{}'),
        COALESCE(durable,0),
        COALESCE(activation_mode,'singleton'), COALESCE(class,'advisor'),
        COALESCE(default_state,'sleeping'),
        COALESCE(consumer_id,''),
        COALESCE(role_id,''), COALESCE(model_id,''), COALESCE(runtime_kind,'api'),
        COALESCE(protocol,''), COALESCE(transport,''),
        COALESCE(plugin_id,''),
        COALESCE(tether_managed,0), COALESCE(tether_urn,''), revision`

// scanAgent scans a row into an AgentProfile using the canonical column order.
func scanAgent(scanner interface{ Scan(...any) error }, a *AgentProfile) error {
	return scanner.Scan(
		&a.ID, &a.Name, &a.Slug, &a.Avatar, &a.SystemPrompt, &a.Description,
		&a.Modes, &a.DefaultModel, &a.DefaultProvider,
		&a.MCPServers, &a.ToolPermissions, &a.CanExecute, &a.Settings, &a.CreatedAt, &a.UpdatedAt,
		&a.AgentHash, &a.Version, &a.Tools, &a.Directories, &a.Constraints, &a.Tags,
		&a.Status, &a.Source, &a.SourceRef, &a.Icon,
		&a.Kind, &a.CapabilitiesJSON, &a.LimitsJSON, &a.ModelStrategy,
		&a.ImportedAt, &a.OriginSystem, &a.Format,
		&a.ParentDispatchAllowlist,
		&a.RoleTools,
		&a.RoleSkills,
		&a.ContextPolicy,
		&a.Durable,
		&a.ActivationMode, &a.Class,
		&a.DefaultState,
		&a.ConsumerID,
		&a.RoleID, &a.ModelID, &a.RuntimeKind,
		&a.Protocol, &a.Transport,
		&a.PluginID,
		&a.TetherManaged, &a.TetherURN, &a.Revision,
	)
}

// GetAgentBySlug returns an agent profile by its slug.
func (s *Store) GetAgentBySlug(ctx context.Context, slug string) (*AgentProfile, error) {
	var id string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM agent_host_settings WHERE slug=?`, slug).Scan(&id); err != nil {
		return nil, err
	}
	return s.GetAgent(ctx, id)
}

// GetAgent reads a fresh host projection, never a retained profile fallback.
func (s *Store) GetAgent(ctx context.Context, id string) (*AgentProfile, error) {
	h, err := s.GetAgentHostSettings(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.projectAgentHost(ctx, h)
}

// GetHistoricalAgentProfile is exclusively for retained export/history/retirement.
// It is not a runtime resolution fallback or an actor binding.
func (s *Store) GetHistoricalAgentProfile(ctx context.Context, id string) (*AgentProfile, error) {
	return getHistoricalAgentProfile(ctx, s.DB, id)
}

func getHistoricalAgentProfile(ctx context.Context, db agentConfigDB, id string) (*AgentProfile, error) {
	var a AgentProfile
	row := db.QueryRowContext(ctx, `SELECT `+agentColumns+` FROM agent_profiles WHERE id = ?`, id)
	if err := scanAgent(row, &a); err != nil {
		return nil, fmt.Errorf("get agent %s: %w", id, err)
	}
	return &a, nil
}

// CreateAgent inserts a new agent profile.
func (s *Store) CreateAgent(ctx context.Context, a *AgentProfile) error {
	return s.writeAgentRow(ctx, a, true)
}

func createAgent(ctx context.Context, db agentConfigDB, a *AgentProfile) error {
	if err := checkProfileIngestionRetired(ctx, db, a.ID, a.Slug); err != nil {
		return err
	}
	return ErrImmutableAgentProfile
}

// DeleteAgent removes an agent profile by slug, including related records
// (skills, project links, session associations). Returns nil if the agent
// doesn't exist.
//
// All deletes run inside a single transaction — no PRAGMA toggling.
// session_agents intentionally has no FK back to agent_profiles (it may
// reference file-based agents), so deleting it explicitly is both correct
// and FK-safe. agent_projects DOES carry a real
// `agent_id ... REFERENCES agent_profiles(id) ON DELETE CASCADE` FK
// (migration 113, Phase 1 #05) — the explicit cleanup line for it is no
// longer required for correctness (the CASCADE would handle it on its own),
// but is kept anyway for the same belt-and-suspenders reason the per-agent
// capability/runtime children below are (they run before the final
// agent_profiles delete regardless, so behavior is identical with or
// without the CASCADE). The old, dedicated agent<->skill join table this
// comment used to also cite here is dropped in full by TASKS/skills/02 —
// agent_known_skills is now the sole per-agent skill attachment table, and
// its own cleanup line below already covers it.
// Messages have their agent_id nullified to preserve user data.
func (s *Store) DeleteAgent(ctx context.Context, slug string) error {
	return ErrImmutableAgentProfile
}

// UpdateAgent updates mutable fields on an agent profile.
// It recomputes agent_hash and bumps version if content fields changed.
func (s *Store) UpdateAgent(ctx context.Context, a *AgentProfile) error {
	return s.writeAgentRow(ctx, a, false)
}

func updateAgent(ctx context.Context, db agentConfigDB, a *AgentProfile) error {
	if err := checkProfileIngestionRetired(ctx, db, a.ID, a.Slug); err != nil {
		return err
	}
	return ErrImmutableAgentProfile
}

// UpdateAgentComposition sets an agent's composition
// columns (role_id, consumer_id, model_id) -- see the RoleID/ConsumerID/
// ModelID field doc comments above. This narrower writer provides the pointer
// semantics and FK validation used by the assignment API.
//
// roleID/consumerID/modelID are each a *string: nil leaves that column
// untouched; non-nil (including a pointer to "") sets or clears it. A
// non-empty value must reference a real roles/consumers/models row --
// enforced by the column's own FK constraint (this codebase runs with
// PRAGMA foreign_keys=1) and surfaced here as a wrapped error.
func (s *Store) UpdateAgentComposition(ctx context.Context, agentID string, roleID, consumerID, modelID *string) error {
	return updateAgentComposition(ctx, s.DB, agentID, roleID, consumerID, modelID)
}

func updateAgentComposition(ctx context.Context, db agentConfigDB, agentID string, roleID, consumerID, modelID *string) error {
	return ErrImmutableAgentProfile
}

// UpdateAgentACPConfig sets an agent's Protocol/Transport columns
// (TASKS/agent-host-acp/11-nanite-per-agent-protocol-transport-config.md),
// mirroring UpdateAgentComposition's narrow partial-update shape.
//
// protocol/transport are each a *string: nil leaves that column untouched;
// non-nil (including a pointer to "") sets or clears it. Validated against
// the same enum validateAgentMultiAgentFields enforces for a full-struct
// write, so a caller of this narrower path gets the identical rejection
// for an invalid value (or an explicit transport set without protocol=
// "acp") instead of a raw CHECK-constraint failure.
func (s *Store) UpdateAgentACPConfig(ctx context.Context, agentID string, protocol, transport *string) error {
	return updateAgentACPConfig(ctx, s.DB, agentID, protocol, transport)
}

func updateAgentACPConfig(ctx context.Context, db agentConfigDB, agentID string, protocol, transport *string) error {
	return ErrImmutableAgentProfile
}

// SessionAgent represents a record in the session_agents table.
type SessionAgent struct {
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	Mode      string `json:"mode"`
	JoinedAt  string `json:"joined_at"`
	IsPrimary bool   `json:"is_primary"`
}

// GetSessionPrimaryAgent returns the primary agent for a session.
func (s *Store) GetSessionPrimaryAgent(ctx context.Context, sessionID string) (*SessionAgent, error) {
	var sa SessionAgent
	err := s.DB.QueryRowContext(ctx,
		`SELECT session_id, agent_id, mode, joined_at, is_primary
		 FROM session_actor_bindings WHERE session_id = ? AND is_primary = TRUE`, sessionID,
	).Scan(&sa.SessionID, &sa.AgentID, &sa.Mode, &sa.JoinedAt, &sa.IsPrimary)
	if err != nil {
		return nil, fmt.Errorf("get session primary agent for %s: %w", sessionID, err)
	}
	return &sa, nil
}

// SetSessionAgentMode updates the mode for a specific agent in a session.
func (s *Store) SetSessionAgentMode(ctx context.Context, sessionID, agentID, mode string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE session_actor_bindings SET mode = ? WHERE session_id = ? AND agent_id = ?`,
		mode, sessionID, agentID,
	)
	if err != nil {
		return fmt.Errorf("set session agent mode: %w", err)
	}
	return nil
}

// EnsureSessionAgent upserts a session_agents record.
func (s *Store) EnsureSessionAgent(ctx context.Context, sessionID, agentID, mode string, isPrimary bool) error {
	if _, err := s.GetAgentForActor(ctx, agentID); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if isPrimary {
		if _, err = tx.ExecContext(ctx, `UPDATE session_actor_bindings SET is_primary=FALSE WHERE session_id=? AND agent_id<>? AND is_primary=TRUE`, sessionID, agentID); err != nil {
			return err
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.ExecContext(ctx, `INSERT INTO session_actor_bindings(session_id,agent_id,mode,joined_at,is_primary) VALUES(?,?,?,?,?) ON CONFLICT(session_id,agent_id) DO UPDATE SET mode=excluded.mode,is_primary=excluded.is_primary`, sessionID, agentID, mode, now, isPrimary)
	if err != nil {
		return fmt.Errorf("ensure session actor: %w", err)
	}
	return tx.Commit()

}

// ListSessionAgents returns all agents in a given session.
func (s *Store) ListSessionAgents(ctx context.Context, sessionID string) ([]SessionAgent, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT session_id, agent_id, mode, joined_at, is_primary
		 FROM session_actor_bindings WHERE session_id = ?
		 ORDER BY joined_at`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list session agents: %w", err)
	}
	defer closeRows(rows)

	out := make([]SessionAgent, 0)
	for rows.Next() {
		var sa SessionAgent
		if err := rows.Scan(&sa.SessionID, &sa.AgentID, &sa.Mode, &sa.JoinedAt, &sa.IsPrimary); err != nil {
			return nil, fmt.Errorf("scan session agent: %w", err)
		}
		out = append(out, sa)
	}
	return out, rows.Err()
}

// ErrSessionAgentNotFound denotes an absent session-agent membership.
var ErrSessionAgentNotFound = errors.New("session agent not found")

// DeleteSessionAgent removes an agent from a session.
// Returns an error if the row does not exist.
func (s *Store) DeleteSessionAgent(ctx context.Context, sessionID, agentID string) error {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM session_actor_bindings WHERE session_id = ? AND agent_id = ?`,
		sessionID, agentID,
	)
	if err != nil {
		return fmt.Errorf("delete session agent: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete session agent rows affected: %w", err)
	}
	if n == 0 {
		return ErrSessionAgentNotFound
	}
	return nil
}

// ListAgents returns all agent profiles.
func (s *Store) ListAgents(ctx context.Context) ([]AgentProfile, error) {
	hs, err := s.ListAgentHostSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AgentProfile, 0, len(hs))
	for _, h := range hs {
		p, err := s.projectAgentHost(ctx, h)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, nil
}

// ListAgentsBySource returns all agent profiles with the given source.
func (s *Store) ListAgentsBySource(ctx context.Context, source string) ([]AgentProfile, error) {
	all, err := s.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AgentProfile, 0)
	for _, p := range all {
		if p.Source == source {
			out = append(out, p)
		}
	}
	return out, nil
}

// ListAgentsByRoleID returns every agent_profiles row bound to roleID
// (agent_profiles.role_id), ordered by name for a stable, deterministic
// "first match" pick. TASKS/teams/08-team-run-launcher.md's `resolution:
// fresh` Team Slot construction is the first real caller: given a Team
// Slot's role_slug -> roles.id (GetRoleBySlug), this is how it finds the
// concrete Agent (an already-composed agent_profiles row bound to that
// Role -- see GLOSSARY.md's Agent entry: "a role... bound to a scope...
// Not a flat, standalone definition") to attach a fresh session to,
// mirroring CountAgentsByRoleID's existing role_id lookup shape (used
// today only as a plugin-unload deletion guard) but returning full rows
// instead of a count.
func (s *Store) ListAgentsByRoleID(ctx context.Context, roleID string) ([]AgentProfile, error) {
	return nil, ErrImmutableAgentProfile
}

// ListAgentsByPluginID returns every agent_profiles row tagged with the
// given plugin_id -- the plugin-ownership column added by Phase 5 item 03
// (TASKS/phase-5/03-wire-registers-agent-profiles.md, migration 122)
// alongside registers.agent_profiles[]'s registration path. Used by
// plugin.Host.UnloadPlugin's unload sweep to find rows to remove; DB-
// authoritative rather than an in-memory host-side map so the sweep is
// correct even for a plugin uninstalled while disabled (never loaded into
// the current host process at all).
func (s *Store) ListAgentsByPluginID(ctx context.Context, pluginID string) ([]AgentProfile, error) {
	all, err := s.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AgentProfile, 0)
	for _, p := range all {
		if p.PluginID == pluginID {
			out = append(out, p)
		}
	}
	return out, nil
}

// CountAgentsByRoleID returns how many agent_profiles rows currently
// reference roleID. Used before deleting a plugin-owned role during
// plugin.Host.UnloadPlugin's sweep so a role another agent still depends on
// (e.g. an operator or a different plugin bound to a reused, shared role --
// see agent_profiles.go's resolveOrCreatePluginRole) is never removed out
// from under it.
func (s *Store) CountAgentsByRoleID(ctx context.Context, roleID string) (int, error) {
	return 0, ErrImmutableAgentProfile
}

// UpsertAgentBySlug inserts or updates an agent profile by slug.
// Used by framework sync plugins (agentrc, etc.) to keep DB in sync with config.
func (s *Store) UpsertAgentBySlug(ctx context.Context, a *AgentProfile) error {
	return ErrImmutableAgentProfile
}

func (s *Store) ListAgentsFilter(ctx context.Context, class, activationMode, status string, tagsAny []string) ([]AgentProfile, error) {
	all, err := s.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AgentProfile, 0)
	for _, p := range all {
		if class != "" && p.Class != class {
			continue
		}
		if status != "" && p.Status != status {
			continue
		}
		if activationMode != "" || len(tagsAny) > 0 {
			return nil, ErrImmutableAgentProfile
		}
		out = append(out, p)
	}
	return out, nil
}

// DeleteAgentByID removes an agent profile by ID. Mirrors DeleteAgent
// (slug-based) but for direct-ID callers. Returns nil only when the row
// genuinely does not exist; all other lookup and delete errors propagate.
// Tombstones for durable=1 rows are deferred future work; today a DELETE
// wipes the row regardless of durable. FU-28.
func (s *Store) DeleteAgentByID(ctx context.Context, id string) error {
	return ErrImmutableAgentProfile
}

// CloneAgent copies an existing agent profile under a new slug + name,
// mints a fresh URN, resets agent_hash + version=1, and returns the new
// AgentProfile. Tags, role_tools, system_prompt are copied verbatim.
// FU-28.
func (s *Store) CloneAgent(ctx context.Context, srcID, newSlug, newName string) (*AgentProfile, error) {
	return nil, ErrImmutableAgentProfile
}

// ListSessionsByAgentID returns all sessions linked to an agent via
// the session_agents junction table, ordered by joined_at DESC. FU-28.
func (s *Store) ListSessionsByAgentID(ctx context.Context, agentID string) ([]SessionAgent, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT sa.session_id, sa.agent_id, sa.mode, sa.joined_at, sa.is_primary
		 FROM session_actor_bindings sa
		 WHERE sa.agent_id = ?
		 ORDER BY sa.joined_at DESC`, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions by agent: %w", err)
	}
	defer closeRows(rows)
	out := make([]SessionAgent, 0)
	for rows.Next() {
		var sa SessionAgent
		if err := rows.Scan(&sa.SessionID, &sa.AgentID, &sa.Mode, &sa.JoinedAt, &sa.IsPrimary); err != nil {
			return nil, fmt.Errorf("scan session by agent: %w", err)
		}
		out = append(out, sa)
	}
	return out, rows.Err()
}

// SetSessionStatusByAgentID updates sessions.status for every session
// linked to the given agent. Returns the number of rows updated.
// Spike-scope helper for FU-28 wake/sleep/shutdown handlers. FU-28.
func (s *Store) SetSessionStatusByAgentID(ctx context.Context, agentID, status string) (int64, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE sessions SET status = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id IN (SELECT session_id FROM session_actor_bindings WHERE agent_id = ?)`,
		status, agentID,
	)
	if err != nil {
		return 0, fmt.Errorf("set session status by agent: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}

// SetAgentDefaultTrustTier writes agent_profiles.default_trust_tier for one
// profile — H1's per-profile trust default, and since migration 108 dropped
// workspace_role_trust, the whole of base-tier resolution. See ResolveTrust
// in trust.go for how it is read.
//
// It exists so callers outside this package can set the tier without reaching
// for s.DB directly. The two in-tree callers that predate it
// (service.upsertAgentDef and AgentConfigService.Create) still issue their own
// UPDATE keyed on slug/id respectively; converting them is a separate change,
// not a drive-by.
func (s *Store) SetAgentDefaultTrustTier(ctx context.Context, agentID, tier string) error {
	return ErrImmutableAgentProfile
}
