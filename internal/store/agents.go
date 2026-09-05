package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/a2a"
)

// urnPrefix mirrors agent.URNPrefix without an import cycle on the
// internal/agent package (which already depends on internal/store). The
// canonical helper lives at internal/agent/urn.go; this is the
// store-side mirror used by CreateAgent/UpdateAgent URN minting. FU-28.
const urnPrefix = "msg://agent/agent-mux/"

// generateAgentURN mirrors agent.GenerateAgentURN to avoid an import
// cycle. FU-28.
func generateAgentURN() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("store: rand.Read failed: " + err.Error())
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
	return urnPrefix + "agt_" + strings.ToLower(enc[:10])
}

// slugAliasURN mirrors agent.SlugAliasURN to avoid an import cycle. FU-28.
func slugAliasURN(slug string) string {
	return urnPrefix + slug
}

// AgentProfile represents an agent profile record.
type AgentProfile struct {
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
	// pre-seeded with at create time. The CreateAgent API handler reads
	// this field, parses the JSON array, and inserts one agent_known_tools
	// row per entry with pinned=1, reason='role_seed'. Empty "[]" means no
	// seed. Added by FU-7a (migration 066).
	//
	// DEPRECATED as of Phase 1 item 04
	// (TASKS/phase-1/04-add-known-tools-and-agent-tools-fk.md): its names
	// now also produce real agent_tools grant rows (granted_via='role_seed',
	// internal/service/ingest.go's seedRoleToolsFromIngest), on top of the
	// agent_known_tools seeding described above, which is unchanged.
	// Existing agents' current values were carried into agent_tools once
	// by this task's backfill (internal/service/known_tools_backfill.go).
	RoleTools string `json:"role_tools"`

	// RoleSkills is a JSON array of skill slugs this agent should be
	// pre-seeded with at create time. The CreateAgent API handler reads
	// this field, parses the JSON array, and inserts one agent_known_skills
	// row per entry with pinned=1, reason='role_seed'. Empty "[]" means no
	// seed. Added by FU-33 (migration 074).
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

	// FU-28 multi-agent foundation. URN is the opaque agt_<n> identity;
	// URNAliases preserves legacy slug-form URNs for routing. Class is
	// one of advisor/process/template/harness; ActivationMode is singleton
	// or instance; DefaultState is sleeping or active. Defaults track
	// migration 070. Enum-shape fields are enforced at the Go layer (see
	// validateAgentMultiAgentFields) since SQLite ALTER TABLE ADD COLUMN
	// can't carry an idempotent CHECK after the column exists.
	URN            string `json:"urn"`
	URNAliases     string `json:"urn_aliases"`
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
// is retired as a valid value; every pre-existing row (and every
// .nanite/agents/*.md file) was migrated to 'fresh-per-wake' in lockstep.
func validateAgentMultiAgentFields(a *AgentProfile) error {
	switch a.ActivationMode {
	case "", "singleton", "fresh-per-wake", "concurrent":
	default:
		return fmt.Errorf("activation_mode %q invalid: must be 'singleton', 'fresh-per-wake', or 'concurrent'", a.ActivationMode)
	}
	switch a.Class {
	case "", "advisor", "process", "template", "harness":
	default:
		return fmt.Errorf("class %q invalid: must be 'advisor', 'process', 'template', or 'harness'", a.Class)
	}
	switch a.DefaultState {
	case "", "sleeping", "active":
	default:
		return fmt.Errorf("default_state %q invalid: must be 'sleeping' or 'active'", a.DefaultState)
	}
	// runtime_kind also carries a real DB-level CHECK (migration 117,
	// unlike the three enums above), but validating it here too gives API
	// callers a clean Go error instead of a raw SQLite CHECK-constraint
	// failure, matching this function's existing job for every other
	// enum-shaped column on this row.
	switch a.RuntimeKind {
	case "", "cli", "api":
	default:
		return fmt.Errorf("runtime_kind %q invalid: must be 'cli' or 'api'", a.RuntimeKind)
	}
	// Protocol/Transport (TASKS/agent-host-acp/11) also carry a real
	// DB-level CHECK (migration 134), mirrored here for the same clean-
	// Go-error-instead-of-raw-CHECK-failure reason as runtime_kind above.
	switch a.Protocol {
	case "", "claude-stream-json", "codex-app-server", "opencode-native", "acp":
	default:
		return fmt.Errorf("protocol %q invalid: must be '', 'claude-stream-json', 'codex-app-server', 'opencode-native', or 'acp'", a.Protocol)
	}
	switch a.Transport {
	case "", "stdio", "tcp":
	default:
		return fmt.Errorf("transport %q invalid: must be '', 'stdio', or 'tcp'", a.Transport)
	}
	if a.Transport != "" && a.Protocol != "acp" {
		return fmt.Errorf("transport %q is only valid when protocol is 'acp' (got protocol %q)", a.Transport, a.Protocol)
	}
	return nil
}

// applyMultiAgentDefaults applies the FU-28 column defaults (activation_mode,
// class, default_state, urn_aliases, runtime_kind) and mints a URN if none
// is set. When minting, the slug-form URN is pushed into urn_aliases so
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
	if a.URNAliases == "" {
		a.URNAliases = "[]"
	}
	if a.URN == "" {
		a.URN = generateAgentURN()
		// Push the slug-form alias so legacy resolvers continue to
		// route msg://agent/agent-mux/<slug> against this row. Only
		// inject when we just minted the URN (preserving operator
		// intent on rows that already have one set).
		if a.Slug != "" {
			alias := slugAliasURN(a.Slug)
			var existing []string
			_ = json.Unmarshal([]byte(a.URNAliases), &existing)
			seen := false
			for _, e := range existing {
				if e == alias {
					seen = true
					break
				}
			}
			if !seen {
				existing = append(existing, alias)
				if b, err := json.Marshal(existing); err == nil {
					a.URNAliases = string(b)
				}
			}
		}
	}
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
// internal/store, so the reverse import would cycle. Same "mirror without
// an import cycle" convention this file already uses for
// urnPrefix/generateAgentURN (see their doc comments above). Kept in
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
        COALESCE(urn,''), COALESCE(urn_aliases,'[]'),
        COALESCE(activation_mode,'singleton'), COALESCE(class,'advisor'),
        COALESCE(default_state,'sleeping'),
        COALESCE(consumer_id,''),
        COALESCE(role_id,''), COALESCE(model_id,''), COALESCE(runtime_kind,'api'),
        COALESCE(protocol,''), COALESCE(transport,''),
        COALESCE(plugin_id,'')`

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
		&a.URN, &a.URNAliases,
		&a.ActivationMode, &a.Class,
		&a.DefaultState,
		&a.ConsumerID,
		&a.RoleID, &a.ModelID, &a.RuntimeKind,
		&a.Protocol, &a.Transport,
		&a.PluginID,
	)
}

// GetAgentBySlug returns an agent profile by its slug.
func (s *Store) GetAgentBySlug(ctx context.Context, slug string) (*AgentProfile, error) {
	var a AgentProfile
	row := s.DB.QueryRowContext(ctx, `SELECT `+agentColumns+` FROM agent_profiles WHERE slug = ?`, slug)
	if err := scanAgent(row, &a); err != nil {
		return nil, fmt.Errorf("get agent by slug %s: %w", slug, err)
	}
	return &a, nil
}

// GetAgent returns an agent profile by ID.
func (s *Store) GetAgent(ctx context.Context, id string) (*AgentProfile, error) {
	var a AgentProfile
	row := s.DB.QueryRowContext(ctx, `SELECT `+agentColumns+` FROM agent_profiles WHERE id = ?`, id)
	if err := scanAgent(row, &a); err != nil {
		return nil, fmt.Errorf("get agent %s: %w", id, err)
	}
	return &a, nil
}

// CreateAgent inserts a new agent profile.
func (s *Store) CreateAgent(ctx context.Context, a *AgentProfile) error {
	if a.Slug == a2a.UserSentinel {
		return fmt.Errorf("agent slug %q is reserved (messaging user sentinel)", a.Slug)
	}
	if a.ID == a2a.UserSentinel {
		return fmt.Errorf("agent id %q is reserved (messaging user sentinel)", a.ID)
	}
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if a.Modes == "" {
		a.Modes = "[]"
	}
	if a.MCPServers == "" {
		a.MCPServers = "[]"
	}
	if a.ToolPermissions == "" {
		a.ToolPermissions = "{}"
	}
	if a.Settings == "" {
		a.Settings = "{}"
	}
	// v2 defaults
	if a.Tools == "" {
		a.Tools = "[]"
	}
	if a.Directories == "" {
		a.Directories = "[]"
	}
	if a.Constraints == "" {
		a.Constraints = "{}"
	}
	if a.Tags == "" {
		a.Tags = "[]"
	}
	if a.Status == "" {
		a.Status = "active"
	}
	if a.Source == "" {
		a.Source = "api"
	}
	if a.Version == 0 {
		a.Version = 1
	}
	if a.AgentHash == "" {
		a.AgentHash = ComputeAgentHash(a.SystemPrompt, a.Tools, a.ToolPermissions)
	}
	// S7 T5 registry defaults — match the CHECK/NOT NULL column
	// defaults so DB insert sees non-empty values for these new
	// columns without forcing every caller to populate them.
	if a.Kind == "" {
		a.Kind = "internal"
	}
	if a.CapabilitiesJSON == "" {
		a.CapabilitiesJSON = "[]"
	}
	if a.LimitsJSON == "" {
		a.LimitsJSON = "{}"
	}

	if a.Format == "" {
		a.Format = "markdown"
	}
	// ParentDispatchAllowlist (CW-20260512-0107): JSON array of role slugs
	// this agent may dispatch via task_execute. Default '[]' matches the
	// migration 059 column default — keeps inserts succeeding without
	// forcing every caller to populate the field.
	if a.ParentDispatchAllowlist == "" {
		a.ParentDispatchAllowlist = "[]"
	}
	// RoleTools (FU-7a): JSON array of tool-name patterns to pre-seed into
	// agent_known_tools. Default '[]' matches the migration 066 column
	// default — keeps inserts succeeding without forcing every caller to
	// populate the field.
	if a.RoleTools == "" {
		a.RoleTools = "[]"
	}
	// RoleSkills (FU-33): JSON array of skill slugs to pre-seed into
	// agent_known_skills. Default '[]' matches the migration 074 column
	// default — keeps inserts succeeding without forcing every caller to
	// populate the field.
	if a.RoleSkills == "" {
		a.RoleSkills = "[]"
	}
	if a.ContextPolicy == "" {
		a.ContextPolicy = "{}"
	}

	// FU-28: multi-agent foundation defaults + enum validation. Mint a
	// URN if absent and inject the slug-form alias so legacy routing
	// resolves. Enum-shape fields are enforced here because migration
	// 070 deliberately skipped per-column CHECKs (idempotency papercut).
	applyMultiAgentDefaults(a)
	if err := validateAgentMultiAgentFields(a); err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO agent_profiles (id, name, slug, avatar, system_prompt, description,
		                              modes, default_model, default_provider,
		                              mcp_servers, tool_permissions, can_execute, settings,
		                              created_at, updated_at,
		                              agent_hash, version, tools, directories, constraints,
		                              tags, status, source, source_ref, icon,
		                              kind, capabilities_json, limits_json, model_strategy,
		                              imported_at, origin_system, format,
		                              parent_dispatch_allowlist,
		                              role_tools,
		                              role_skills,
		                              context_policy,
		                              durable,
		                              urn, urn_aliases,
		                              activation_mode, class, default_state,
		                              consumer_id,
		                              role_id, model_id, runtime_kind,
		                              protocol, transport,
		                              plugin_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Slug, nullIfEmpty(a.Avatar), a.SystemPrompt, nullIfEmpty(a.Description),
		a.Modes, nullIfEmpty(a.DefaultModel), a.DefaultProvider,
		a.MCPServers, a.ToolPermissions, a.CanExecute, a.Settings,
		now, now,
		a.AgentHash, a.Version, a.Tools, a.Directories, a.Constraints,
		a.Tags, a.Status, a.Source, a.SourceRef, nullIfEmpty(a.Icon),
		a.Kind, a.CapabilitiesJSON, a.LimitsJSON, a.ModelStrategy,
		a.ImportedAt, a.OriginSystem, a.Format,
		a.ParentDispatchAllowlist,
		a.RoleTools,
		a.RoleSkills,
		a.ContextPolicy,
		a.Durable,
		a.URN, a.URNAliases,
		a.ActivationMode, a.Class, a.DefaultState,
		nullIfEmpty(a.ConsumerID),
		nullIfEmpty(a.RoleID), nullIfEmpty(a.ModelID), a.RuntimeKind,
		nullIfEmpty(a.Protocol), nullIfEmpty(a.Transport),
		nullIfEmpty(a.PluginID),
	)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	a.CreatedAt = now
	a.UpdatedAt = now
	return nil
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
	agent, err := s.GetAgentBySlug(ctx, slug)
	if err != nil {
		return nil // agent doesn't exist — nothing to delete
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	cleanups := []string{
		"DELETE FROM session_agents WHERE agent_id = ?",
		"DELETE FROM agent_projects WHERE agent_id = ?",
		// Per-agent capability/runtime children (migrations 068/070/074/085).
		// These declare FKs to agent_profiles(id); clean them explicitly so a
		// managed-agent delete leaves no orphaned reflexes, known tools/skills,
		// procedures, knowledge seeds, or schedules.
		"DELETE FROM agent_known_tools WHERE agent_id = ?",
		"DELETE FROM agent_known_skills WHERE agent_id = ?",
		// Phase 1 item 04 (migration 116): agent_tools/
		// agent_dispatch_tool_allowlist both already declare
		// ON DELETE CASCADE agent_profiles(id) FKs, so these two lines are
		// belt-and-suspenders, matching this list's existing style of
		// explicitly clearing agent_projects even though migration 113 gave
		// that a real cascade FK too.
		"DELETE FROM agent_tools WHERE agent_id = ?",
		"DELETE FROM agent_dispatch_tool_allowlist WHERE agent_id = ?",
		"DELETE FROM agent_tools_legacy_backfill WHERE agent_id = ?",
		"DELETE FROM agent_procedures WHERE agent_id = ?",
		"DELETE FROM agent_knowledge_seed WHERE agent_id = ?",
		"DELETE FROM agent_log WHERE agent_id = ?",
		"DELETE FROM agent_schedules WHERE agent_id = ?",
		"DELETE FROM agent_reflexes WHERE agent_id = ?",
		// pending_reflexes.target_agent_id references the profile (migration
		// 074, no cascade) — clear it or the final delete fails under
		// foreign_keys=ON. (pending_reflexes has no agent_id column.)
		"DELETE FROM pending_reflexes WHERE target_agent_id = ?",
		// durable_agent_instances.profile_id references the profile
		// (migration 080, no cascade; 081/082 only add columns). Removing the
		// instances cascades their instance_id children (sessions, events).
		// Deleting the managed profile is a permanent operator action, so its
		// durable instances go with it.
		"DELETE FROM durable_agent_instances WHERE profile_id = ?",
	}
	for _, q := range cleanups {
		if _, err := tx.ExecContext(ctx, q, agent.ID); err != nil {
			return fmt.Errorf("cleanup agent references (%s): %w", q, err)
		}
	}

	// Nullify agent_id on messages (preserve messages, just unlink the agent).
	if _, err := tx.ExecContext(ctx, "UPDATE messages SET agent_id = NULL WHERE agent_id = ?", agent.ID); err != nil {
		return fmt.Errorf("nullify messages for agent %s: %w", slug, err)
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM agent_profiles WHERE id = ?", agent.ID); err != nil {
		return fmt.Errorf("delete agent %s: %w", slug, err)
	}

	return tx.Commit()
}

// UpdateAgent updates mutable fields on an agent profile.
// It recomputes agent_hash and bumps version if content fields changed.
func (s *Store) UpdateAgent(ctx context.Context, a *AgentProfile) error {
	now := time.Now().UTC().Format(time.RFC3339)

	// Recompute hash; bump version if content changed.
	newHash := ComputeAgentHash(a.SystemPrompt, a.Tools, a.ToolPermissions)
	if a.AgentHash != "" && newHash != a.AgentHash {
		a.Version++
	}
	a.AgentHash = newHash

	// ParentDispatchAllowlist (CW-20260512-0107): preserve the column-default
	// '[]' shape if a caller leaves the field empty during an update.
	if a.ParentDispatchAllowlist == "" {
		a.ParentDispatchAllowlist = "[]"
	}
	// RoleTools (FU-7a): preserve the column-default '[]' shape if a caller
	// leaves the field empty during an update.
	if a.RoleTools == "" {
		a.RoleTools = "[]"
	}
	// RoleSkills (FU-33): preserve the column-default '[]' shape if a caller
	// leaves the field empty during an update.
	if a.RoleSkills == "" {
		a.RoleSkills = "[]"
	}
	if a.ContextPolicy == "" {
		a.ContextPolicy = "{}"
	}

	// FU-28: ensure all multi-agent fields are populated and valid.
	// applyMultiAgentDefaults preserves an existing URN; it only mints
	// when URN is empty (e.g. on the first UPDATE after migration 070
	// landed on a previously-deployed row).
	applyMultiAgentDefaults(a)
	if err := validateAgentMultiAgentFields(a); err != nil {
		return fmt.Errorf("update agent: %w", err)
	}

	// CW-20260512-0111: include `source` and `source_ref` in the UPDATE so
	// that file-source-of-truth boot sync can flip an already-deployed row
	// from source='builtin' (or any prior value) to source='internal' when
	// the file profile is now the canonical source. Without this, the
	// Wave 2 cleanup (CW-20260512-0112: DELETE WHERE source != 'internal')
	// would wipe rows whose body was re-synced from internal/agent/builtin/
	// profiles/*.md but whose source column never flipped.
	_, err := s.DB.ExecContext(ctx,
		`UPDATE agent_profiles SET name = ?, slug = ?, avatar = ?, system_prompt = ?, description = ?,
		        modes = ?, default_model = ?, default_provider = ?,
		        mcp_servers = ?, tool_permissions = ?, can_execute = ?, settings = ?,
		        updated_at = ?,
		        agent_hash = ?, version = ?, tools = ?, directories = ?, constraints = ?,
		        tags = ?, status = ?, source = ?, source_ref = ?, icon = ?,
		        imported_at = ?, origin_system = ?, format = ?,
		        parent_dispatch_allowlist = ?,
		        role_tools = ?,
		        role_skills = ?,
		        context_policy = ?,
		        durable = ?,
		        urn = ?, urn_aliases = ?,
		        activation_mode = ?, class = ?, default_state = ?,
		        consumer_id = ?,
		        role_id = ?, model_id = ?, runtime_kind = ?,
		        protocol = ?, transport = ?,
		        plugin_id = ?
		 WHERE id = ?`,
		a.Name, a.Slug, nullIfEmpty(a.Avatar), a.SystemPrompt, nullIfEmpty(a.Description),
		a.Modes, nullIfEmpty(a.DefaultModel), a.DefaultProvider,
		a.MCPServers, a.ToolPermissions, a.CanExecute, a.Settings,
		now,
		a.AgentHash, a.Version, a.Tools, a.Directories, a.Constraints,
		a.Tags, a.Status, a.Source, a.SourceRef, nullIfEmpty(a.Icon),
		a.ImportedAt, a.OriginSystem, a.Format,
		a.ParentDispatchAllowlist,
		a.RoleTools,
		a.RoleSkills,
		a.ContextPolicy,
		a.Durable,
		a.URN, a.URNAliases,
		a.ActivationMode, a.Class, a.DefaultState,
		nullIfEmpty(a.ConsumerID),
		nullIfEmpty(a.RoleID), nullIfEmpty(a.ModelID), a.RuntimeKind,
		nullIfEmpty(a.Protocol), nullIfEmpty(a.Transport),
		nullIfEmpty(a.PluginID),
		a.ID,
	)
	if err != nil {
		return fmt.Errorf("update agent: %w", err)
	}
	a.UpdatedAt = now
	return nil
}

// UpdateAgentComposition directly sets an agent's DB-only composition
// columns (role_id, consumer_id, model_id) -- see the RoleID/ConsumerID/
// ModelID field doc comments above: all three have zero frontmatter
// representation. That matters for writes, not just reads:
// AgentConfigService.Create/Update's managed-agent write pipeline
// (writeManaged -> file write -> file reparse -> IngestAgentDefinition ->
// upsertAgentDef) always reconstructs its store.AgentProfile from a fresh
// file parse, whose Definition has no role_id/consumer_id/model_id fields
// at all -- so routing a composition write through that pipeline would
// silently wipe these columns back to NULL. This is the one legitimate
// direct-DB write path for them (TASKS/phase-5/01-build-assignment-api.md).
//
// roleID/consumerID/modelID are each a *string: nil leaves that column
// untouched; non-nil (including a pointer to "") sets or clears it. A
// non-empty value must reference a real roles/consumers/models row --
// enforced by the column's own FK constraint (this codebase runs with
// PRAGMA foreign_keys=1) and surfaced here as a wrapped error.
func (s *Store) UpdateAgentComposition(ctx context.Context, agentID string, roleID, consumerID, modelID *string) error {
	if agentID == "" {
		return fmt.Errorf("update agent composition: agent_id is required")
	}
	sets := make([]string, 0, 3)
	args := make([]any, 0, 4)
	if roleID != nil {
		sets = append(sets, "role_id = ?")
		args = append(args, nullIfEmpty(*roleID))
	}
	if consumerID != nil {
		sets = append(sets, "consumer_id = ?")
		args = append(args, nullIfEmpty(*consumerID))
	}
	if modelID != nil {
		sets = append(sets, "model_id = ?")
		args = append(args, nullIfEmpty(*modelID))
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, agentID)
	query := "UPDATE agent_profiles SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	res, err := s.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update agent composition: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update agent composition: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("update agent composition: agent %q not found", agentID)
	}
	return nil
}

// UpdateAgentACPConfig directly sets an agent's Protocol/Transport columns
// (TASKS/agent-host-acp/11-nanite-per-agent-protocol-transport-config.md),
// mirroring UpdateAgentComposition's exact shape and for the identical
// reason: like role_id/consumer_id/model_id, Protocol/Transport have zero
// frontmatter representation in internal/agent's markdown Definition
// format, so routing a write through AgentConfigService.Create/Update's
// managed-file pipeline (write -> reparse -> IngestAgentDefinition) would
// silently wipe them back to NULL on every save. This is the one
// legitimate direct-DB write path for them.
//
// protocol/transport are each a *string: nil leaves that column untouched;
// non-nil (including a pointer to "") sets or clears it. Validated against
// the same enum validateAgentMultiAgentFields enforces for a full-struct
// write, so a caller of this narrower path gets the identical rejection
// for an invalid value (or an explicit transport set without protocol=
// "acp") instead of a raw CHECK-constraint failure.
func (s *Store) UpdateAgentACPConfig(ctx context.Context, agentID string, protocol, transport *string) error {
	if agentID == "" {
		return fmt.Errorf("update agent acp config: agent_id is required")
	}
	if protocol != nil || transport != nil {
		existing, err := s.GetAgent(ctx, agentID)
		if err != nil {
			return fmt.Errorf("update agent acp config: %w", err)
		}
		probe := AgentProfile{RuntimeKind: "cli", Protocol: existing.Protocol, Transport: existing.Transport}
		if protocol != nil {
			probe.Protocol = *protocol
		}
		if transport != nil {
			probe.Transport = *transport
		}
		if err := validateAgentMultiAgentFields(&probe); err != nil {
			return fmt.Errorf("update agent acp config: %w", err)
		}
	}

	sets := make([]string, 0, 2)
	args := make([]any, 0, 3)
	if protocol != nil {
		sets = append(sets, "protocol = ?")
		args = append(args, nullIfEmpty(*protocol))
	}
	if transport != nil {
		sets = append(sets, "transport = ?")
		args = append(args, nullIfEmpty(*transport))
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, agentID)
	query := "UPDATE agent_profiles SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	res, err := s.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update agent acp config: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update agent acp config: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("update agent acp config: agent %q not found", agentID)
	}
	return nil
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
		 FROM session_agents WHERE session_id = ? AND is_primary = TRUE`, sessionID,
	).Scan(&sa.SessionID, &sa.AgentID, &sa.Mode, &sa.JoinedAt, &sa.IsPrimary)
	if err != nil {
		return nil, fmt.Errorf("get session primary agent for %s: %w", sessionID, err)
	}
	return &sa, nil
}

// SetSessionAgentMode updates the mode for a specific agent in a session.
func (s *Store) SetSessionAgentMode(ctx context.Context, sessionID, agentID, mode string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE session_agents SET mode = ? WHERE session_id = ? AND agent_id = ?`,
		mode, sessionID, agentID,
	)
	if err != nil {
		return fmt.Errorf("set session agent mode: %w", err)
	}
	return nil
}

// EnsureSessionAgent upserts a session_agents record.
func (s *Store) EnsureSessionAgent(ctx context.Context, sessionID, agentID, mode string, isPrimary bool) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO session_agents (session_id, agent_id, mode, joined_at, is_primary)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(session_id, agent_id) DO UPDATE SET mode = excluded.mode, is_primary = excluded.is_primary`,
		sessionID, agentID, mode, now, isPrimary,
	)
	if err != nil {
		return fmt.Errorf("ensure session agent: %w", err)
	}
	return nil
}

// ListSessionAgents returns all agents in a given session.
func (s *Store) ListSessionAgents(ctx context.Context, sessionID string) ([]SessionAgent, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT session_id, agent_id, mode, joined_at, is_primary
		 FROM session_agents WHERE session_id = ?
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

// DeleteSessionAgent removes an agent from a session.
// Returns an error if the row does not exist.
func (s *Store) DeleteSessionAgent(ctx context.Context, sessionID, agentID string) error {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM session_agents WHERE session_id = ? AND agent_id = ?`,
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
		return fmt.Errorf("session agent not found")
	}
	return nil
}

// ListAgents returns all agent profiles.
func (s *Store) ListAgents(ctx context.Context) ([]AgentProfile, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+agentColumns+` FROM agent_profiles ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer closeRows(rows)

	out := make([]AgentProfile, 0)
	for rows.Next() {
		var a AgentProfile
		if err := scanAgent(rows, &a); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListAgentsBySource returns all agent profiles with the given source.
func (s *Store) ListAgentsBySource(ctx context.Context, source string) ([]AgentProfile, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+agentColumns+` FROM agent_profiles WHERE source = ? ORDER BY name`, source)
	if err != nil {
		return nil, fmt.Errorf("list agents by source: %w", err)
	}
	defer closeRows(rows)

	out := make([]AgentProfile, 0)
	for rows.Next() {
		var a AgentProfile
		if err := scanAgent(rows, &a); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
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
	rows, err := s.DB.QueryContext(ctx, `SELECT `+agentColumns+` FROM agent_profiles WHERE role_id = ? ORDER BY name`, roleID)
	if err != nil {
		return nil, fmt.Errorf("list agents by role_id: %w", err)
	}
	defer closeRows(rows)

	out := make([]AgentProfile, 0)
	for rows.Next() {
		var a AgentProfile
		if err := scanAgent(rows, &a); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
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
	rows, err := s.DB.QueryContext(ctx, `SELECT `+agentColumns+` FROM agent_profiles WHERE plugin_id = ? ORDER BY slug`, pluginID)
	if err != nil {
		return nil, fmt.Errorf("list agents by plugin_id: %w", err)
	}
	defer closeRows(rows)

	out := make([]AgentProfile, 0)
	for rows.Next() {
		var a AgentProfile
		if err := scanAgent(rows, &a); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountAgentsByRoleID returns how many agent_profiles rows currently
// reference roleID. Used before deleting a plugin-owned role during
// plugin.Host.UnloadPlugin's sweep so a role another agent still depends on
// (e.g. an operator or a different plugin bound to a reused, shared role --
// see agent_profiles.go's resolveOrCreatePluginRole) is never removed out
// from under it.
func (s *Store) CountAgentsByRoleID(ctx context.Context, roleID string) (int, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_profiles WHERE role_id = ?`, roleID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count agents by role_id: %w", err)
	}
	return n, nil
}

// UpsertAgentBySlug inserts or updates an agent profile by slug.
// Used by framework sync plugins (agentrc, etc.) to keep DB in sync with config.
func (s *Store) UpsertAgentBySlug(ctx context.Context, a *AgentProfile) error {
	existing, err := s.GetAgentBySlug(ctx, a.Slug)
	if err != nil {
		// Not found — create.
		return s.CreateAgent(ctx, a)
	}
	// Found — update, preserving the ID.
	a.ID = existing.ID
	a.AgentHash = existing.AgentHash
	a.Version = existing.Version
	// Preserve the existing URN + alias list so URN identity is stable
	// across re-ingest. FU-28.
	if a.URN == "" {
		a.URN = existing.URN
	}
	if a.URNAliases == "" || a.URNAliases == "[]" {
		a.URNAliases = existing.URNAliases
	}
	return s.UpdateAgent(ctx, a)
}

// GetAgentByURN resolves an URN against agent_profiles, checking both
// the primary urn column AND the urn_aliases JSON array. Returns
// (agent, true) on hit, (nil, false) on miss. FU-28.
func (s *Store) GetAgentByURN(ctx context.Context, urn string) (*AgentProfile, bool, error) {
	if urn == "" {
		return nil, false, nil
	}
	// First try the primary urn column (indexed by migration 070).
	var a AgentProfile
	row := s.DB.QueryRowContext(ctx, `SELECT `+agentColumns+` FROM agent_profiles WHERE urn = ?`, urn)
	if err := scanAgent(row, &a); err == nil {
		return &a, true, nil
	}
	// Fall back to alias lookup. The urn_aliases column is a JSON array
	// of strings; SQLite's LIKE pattern matches the quoted form.
	pattern := `%"` + urn + `"%`
	rows, err := s.DB.QueryContext(ctx, `SELECT `+agentColumns+` FROM agent_profiles WHERE urn_aliases LIKE ?`, pattern)
	if err != nil {
		return nil, false, fmt.Errorf("get agent by urn alias %s: %w", urn, err)
	}
	defer closeRows(rows)
	for rows.Next() {
		var cand AgentProfile
		if err := scanAgent(rows, &cand); err != nil {
			return nil, false, fmt.Errorf("scan agent by urn alias: %w", err)
		}
		// Confirm the candidate's alias list literally contains the URN
		// (LIKE pattern can false-positive on overlapping substrings).
		var aliases []string
		if err := json.Unmarshal([]byte(cand.URNAliases), &aliases); err != nil {
			continue
		}
		for _, alias := range aliases {
			if alias == urn {
				return &cand, true, nil
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("get agent by urn alias rows: %w", err)
	}
	return nil, false, nil
}

// ListAgentsFilter applies optional class / activation_mode / status / tag
// filters. Empty string means no filter on that axis. tagsAny: empty
// slice means no tag filter; non-empty matches rows that carry ANY of
// the supplied tags. Returns sorted by slug ASC. FU-28.
func (s *Store) ListAgentsFilter(ctx context.Context, class, activationMode, status string, tagsAny []string) ([]AgentProfile, error) {
	var clauses []string
	var args []any
	if class != "" {
		clauses = append(clauses, "class = ?")
		args = append(args, class)
	}
	if activationMode != "" {
		clauses = append(clauses, "activation_mode = ?")
		args = append(args, activationMode)
	}
	if status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, status)
	}
	// tagsAny uses LIKE on the tags JSON column. Each tag becomes a
	// disjunction of LIKE patterns; collectively they OR together to
	// match any row that contains at least one of the requested tags.
	if len(tagsAny) > 0 {
		var tagClauses []string
		for _, t := range tagsAny {
			if t == "" {
				continue
			}
			tagClauses = append(tagClauses, "tags LIKE ?")
			args = append(args, `%"`+t+`"%`)
		}
		if len(tagClauses) > 0 {
			clauses = append(clauses, "("+strings.Join(tagClauses, " OR ")+")")
		}
	}
	query := `SELECT ` + agentColumns + ` FROM agent_profiles`
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY slug ASC"
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list agents filter: %w", err)
	}
	defer closeRows(rows)
	out := make([]AgentProfile, 0)
	for rows.Next() {
		var a AgentProfile
		if err := scanAgent(rows, &a); err != nil {
			return nil, fmt.Errorf("scan agent filter: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAgentByID removes an agent profile by ID. Mirrors DeleteAgent
// (slug-based) but for direct-ID callers. Returns nil only when the row
// genuinely does not exist; all other lookup and delete errors propagate.
// Tombstones for durable=1 rows are deferred future work; today a DELETE
// wipes the row regardless of durable. FU-28.
func (s *Store) DeleteAgentByID(ctx context.Context, id string) error {
	a, err := s.GetAgent(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("delete agent by id %s: %w", id, err)
	}
	return s.DeleteAgent(ctx, a.Slug)
}

// CloneAgent copies an existing agent profile under a new slug + name,
// mints a fresh URN, resets agent_hash + version=1, and returns the new
// AgentProfile. Tags, role_tools, system_prompt are copied verbatim.
// FU-28.
func (s *Store) CloneAgent(ctx context.Context, srcID, newSlug, newName string) (*AgentProfile, error) {
	src, err := s.GetAgent(ctx, srcID)
	if err != nil {
		return nil, fmt.Errorf("clone agent: lookup source %s: %w", srcID, err)
	}
	if newSlug == "" {
		return nil, fmt.Errorf("clone agent: new_slug is required")
	}
	if newName == "" {
		newName = src.Name + " (clone)"
	}
	clone := *src
	clone.ID = ""
	clone.Slug = newSlug
	clone.Name = newName
	clone.URN = ""        // mint fresh
	clone.URNAliases = "" // reset; CreateAgent will inject slug alias
	clone.Version = 1
	clone.AgentHash = ""
	// Reset ingestion provenance so the clone isn't confused with a
	// file-sourced row on the next boot sync.
	clone.Source = "user"
	clone.SourceRef = ""
	clone.ImportedAt = ""
	if err := s.CreateAgent(ctx, &clone); err != nil {
		return nil, fmt.Errorf("clone agent: create: %w", err)
	}
	return &clone, nil
}

// ListSessionsByAgentID returns all sessions linked to an agent via
// the session_agents junction table, ordered by joined_at DESC. FU-28.
func (s *Store) ListSessionsByAgentID(ctx context.Context, agentID string) ([]SessionAgent, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT sa.session_id, sa.agent_id, sa.mode, sa.joined_at, sa.is_primary
		 FROM session_agents sa
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
		 WHERE id IN (SELECT session_id FROM session_agents WHERE agent_id = ?)`,
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
