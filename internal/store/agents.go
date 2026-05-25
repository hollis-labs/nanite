package store

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
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
	ToolPermissions string `json:"tool_permissions"`
	CanExecute      bool   `json:"can_execute"`
	Settings        string `json:"settings"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	// Schema v2 fields
	AgentHash   string `json:"agent_hash"`
	Version     int    `json:"version"`
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
	// one of advisor/process/template; ActivationMode is singleton or
	// instance; DefaultState is sleeping or active. Defaults track
	// migration 070. Enum-shape fields are enforced at the Go layer (see
	// validateAgentMultiAgentFields) since SQLite ALTER TABLE ADD COLUMN
	// can't carry an idempotent CHECK after the column exists.
	URN            string `json:"urn"`
	URNAliases     string `json:"urn_aliases"`
	ActivationMode string `json:"activation_mode"`
	Class          string `json:"class"`
	DefaultState   string `json:"default_state"`
}

// validateAgentMultiAgentFields enforces the enum constraints that
// migration 070 deliberately did not encode at the column level. FU-28.
func validateAgentMultiAgentFields(a *AgentProfile) error {
	switch a.ActivationMode {
	case "", "singleton", "instance":
	default:
		return fmt.Errorf("activation_mode %q invalid: must be 'singleton' or 'instance'", a.ActivationMode)
	}
	switch a.Class {
	case "", "advisor", "process", "template":
	default:
		return fmt.Errorf("class %q invalid: must be 'advisor', 'process', or 'template'", a.Class)
	}
	switch a.DefaultState {
	case "", "sleeping", "active":
	default:
		return fmt.Errorf("default_state %q invalid: must be 'sleeping' or 'active'", a.DefaultState)
	}
	return nil
}

// applyMultiAgentDefaults applies the FU-28 column defaults (activation_mode,
// class, default_state, urn_aliases) and mints a URN if none is set.
// When minting, the slug-form URN is pushed into urn_aliases so legacy
// routing keeps resolving. FU-28.
func applyMultiAgentDefaults(a *AgentProfile) {
	if a.ActivationMode == "" {
		a.ActivationMode = "singleton"
	}
	if a.Class == "" {
		a.Class = "advisor"
	}
	if a.DefaultState == "" {
		a.DefaultState = "sleeping"
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
        COALESCE(default_state,'sleeping')`

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
	)
}

// AgentMode represents a mode configuration for an agent.
type AgentMode struct {
	ID             string `json:"id"`
	AgentID        string `json:"agent_id"`
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	PromptAddendum string `json:"prompt_addendum"`
	ToolOverrides  string `json:"tool_overrides"`
	Settings       string `json:"settings"`
}

// GetAgentBySlug returns an agent profile by its slug.
func (s *Store) GetAgentBySlug(slug string) (*AgentProfile, error) {
	var a AgentProfile
	row := s.DB.QueryRow(`SELECT `+agentColumns+` FROM agent_profiles WHERE slug = ?`, slug)
	if err := scanAgent(row, &a); err != nil {
		return nil, fmt.Errorf("get agent by slug %s: %w", slug, err)
	}
	return &a, nil
}

// GetAgentMode returns a specific mode for an agent.
func (s *Store) GetAgentMode(agentID, modeSlug string) (*AgentMode, error) {
	var m AgentMode
	err := s.DB.QueryRow(
		`SELECT id, agent_id, slug, name, prompt_addendum, tool_overrides, settings
		 FROM agent_modes WHERE agent_id = ? AND slug = ?`, agentID, modeSlug,
	).Scan(&m.ID, &m.AgentID, &m.Slug, &m.Name, &m.PromptAddendum, &m.ToolOverrides, &m.Settings)
	if err != nil {
		return nil, fmt.Errorf("get agent mode %s/%s: %w", agentID, modeSlug, err)
	}
	return &m, nil
}

// GetAgent returns an agent profile by ID.
func (s *Store) GetAgent(id string) (*AgentProfile, error) {
	var a AgentProfile
	row := s.DB.QueryRow(`SELECT `+agentColumns+` FROM agent_profiles WHERE id = ?`, id)
	if err := scanAgent(row, &a); err != nil {
		return nil, fmt.Errorf("get agent %s: %w", id, err)
	}
	return &a, nil
}

// CreateAgent inserts a new agent profile.
func (s *Store) CreateAgent(a *AgentProfile) error {
	if a.Slug == "user" {
		return fmt.Errorf("agent slug %q is reserved (messaging user sentinel)", a.Slug)
	}
	if a.ID == "user" {
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

	_, err := s.DB.Exec(
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
		                              activation_mode, class, default_state)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
	)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	a.CreatedAt = now
	a.UpdatedAt = now
	return nil
}

// DeleteAgent removes an agent profile by slug, including related records
// (modes, skills, session associations). Returns nil if the agent doesn't exist.
//
// All deletes run inside a single transaction — no PRAGMA toggling. Junction
// tables (agent_modes, agent_skills, agent_prompt_templates,
// agent_mode_assignments, session_agents) intentionally have no FK back to
// agent_profiles (they may reference file-based agents), so deleting them
// explicitly is both correct and FK-safe. Messages have their agent_id
// nullified to preserve user data.
func (s *Store) DeleteAgent(slug string) error {
	agent, err := s.GetAgentBySlug(slug)
	if err != nil {
		return nil // agent doesn't exist — nothing to delete
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	cleanups := []string{
		"DELETE FROM session_agents WHERE agent_id = ?",
		"DELETE FROM agent_modes WHERE agent_id = ?",
		"DELETE FROM agent_skills WHERE agent_id = ?",
		"DELETE FROM agent_prompt_templates WHERE agent_id = ?",
		"DELETE FROM agent_mode_assignments WHERE agent_id = ?",
		// Per-agent capability/runtime children (migrations 068/070/074/078/085).
		// These declare FKs to agent_profiles(id); clean them explicitly so a
		// managed-agent delete leaves no orphaned reflexes, known tools/skills,
		// procedures, knowledge seeds, schedules, cycles, or boot plan.
		"DELETE FROM agent_known_tools WHERE agent_id = ?",
		"DELETE FROM agent_known_skills WHERE agent_id = ?",
		"DELETE FROM agent_procedures WHERE agent_id = ?",
		"DELETE FROM agent_knowledge_seed WHERE agent_id = ?",
		"DELETE FROM agent_log WHERE agent_id = ?",
		"DELETE FROM agent_cycles WHERE agent_id = ?",
		"DELETE FROM agent_schedules WHERE agent_id = ?",
		"DELETE FROM agent_reflexes WHERE agent_id = ?",
		"DELETE FROM agent_boot_plans WHERE agent_id = ?",
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
		if _, err := tx.Exec(q, agent.ID); err != nil {
			return fmt.Errorf("cleanup agent references (%s): %w", q, err)
		}
	}

	// Nullify agent_id on messages (preserve messages, just unlink the agent).
	if _, err := tx.Exec("UPDATE messages SET agent_id = NULL WHERE agent_id = ?", agent.ID); err != nil {
		return fmt.Errorf("nullify messages for agent %s: %w", slug, err)
	}

	if _, err := tx.Exec("DELETE FROM agent_profiles WHERE id = ?", agent.ID); err != nil {
		return fmt.Errorf("delete agent %s: %w", slug, err)
	}

	return tx.Commit()
}

// UpdateAgent updates mutable fields on an agent profile.
// It recomputes agent_hash and bumps version if content fields changed.
func (s *Store) UpdateAgent(a *AgentProfile) error {
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
	_, err := s.DB.Exec(
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
		        activation_mode = ?, class = ?, default_state = ?
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
		a.ID,
	)
	if err != nil {
		return fmt.Errorf("update agent: %w", err)
	}
	a.UpdatedAt = now
	return nil
}

// ListAgentModes returns all modes for a given agent.
func (s *Store) ListAgentModes(agentID string) ([]AgentMode, error) {
	rows, err := s.DB.Query(
		`SELECT id, agent_id, slug, name, prompt_addendum, tool_overrides, settings
		 FROM agent_modes WHERE agent_id = ? ORDER BY slug`, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list agent modes: %w", err)
	}
	defer rows.Close()

	out := make([]AgentMode, 0)
	for rows.Next() {
		var m AgentMode
		if err := rows.Scan(&m.ID, &m.AgentID, &m.Slug, &m.Name, &m.PromptAddendum, &m.ToolOverrides, &m.Settings); err != nil {
			return nil, fmt.Errorf("scan agent mode: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CreateAgentMode inserts a new mode for an agent.
func (s *Store) CreateAgentMode(m *AgentMode) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	if m.ToolOverrides == "" {
		m.ToolOverrides = "{}"
	}
	if m.Settings == "" {
		m.Settings = "{}"
	}

	_, err := s.DB.Exec(
		`INSERT INTO agent_modes (id, agent_id, slug, name, prompt_addendum, tool_overrides, settings)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.AgentID, m.Slug, m.Name, m.PromptAddendum, m.ToolOverrides, m.Settings,
	)
	if err != nil {
		return fmt.Errorf("create agent mode: %w", err)
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
func (s *Store) GetSessionPrimaryAgent(sessionID string) (*SessionAgent, error) {
	var sa SessionAgent
	err := s.DB.QueryRow(
		`SELECT session_id, agent_id, mode, joined_at, is_primary
		 FROM session_agents WHERE session_id = ? AND is_primary = TRUE`, sessionID,
	).Scan(&sa.SessionID, &sa.AgentID, &sa.Mode, &sa.JoinedAt, &sa.IsPrimary)
	if err != nil {
		return nil, fmt.Errorf("get session primary agent for %s: %w", sessionID, err)
	}
	return &sa, nil
}

// SetSessionAgentMode updates the mode for a specific agent in a session.
func (s *Store) SetSessionAgentMode(sessionID, agentID, mode string) error {
	_, err := s.DB.Exec(
		`UPDATE session_agents SET mode = ? WHERE session_id = ? AND agent_id = ?`,
		mode, sessionID, agentID,
	)
	if err != nil {
		return fmt.Errorf("set session agent mode: %w", err)
	}
	return nil
}

// EnsureSessionAgent upserts a session_agents record.
func (s *Store) EnsureSessionAgent(sessionID, agentID, mode string, isPrimary bool) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(
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
func (s *Store) ListSessionAgents(sessionID string) ([]SessionAgent, error) {
	rows, err := s.DB.Query(
		`SELECT session_id, agent_id, mode, joined_at, is_primary
		 FROM session_agents WHERE session_id = ?
		 ORDER BY joined_at`, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list session agents: %w", err)
	}
	defer rows.Close()

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
func (s *Store) DeleteSessionAgent(sessionID, agentID string) error {
	res, err := s.DB.Exec(
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
func (s *Store) ListAgents() ([]AgentProfile, error) {
	rows, err := s.DB.Query(`SELECT ` + agentColumns + ` FROM agent_profiles ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

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
func (s *Store) ListAgentsBySource(source string) ([]AgentProfile, error) {
	rows, err := s.DB.Query(`SELECT `+agentColumns+` FROM agent_profiles WHERE source = ? ORDER BY name`, source)
	if err != nil {
		return nil, fmt.Errorf("list agents by source: %w", err)
	}
	defer rows.Close()

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

// UpsertAgentBySlug inserts or updates an agent profile by slug.
// Used by framework sync plugins (agentrc, etc.) to keep DB in sync with config.
func (s *Store) UpsertAgentBySlug(a *AgentProfile) error {
	existing, err := s.GetAgentBySlug(a.Slug)
	if err != nil {
		// Not found — create.
		return s.CreateAgent(a)
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
	return s.UpdateAgent(a)
}

// GetAgentByURN resolves an URN against agent_profiles, checking both
// the primary urn column AND the urn_aliases JSON array. Returns
// (agent, true) on hit, (nil, false) on miss. FU-28.
func (s *Store) GetAgentByURN(urn string) (*AgentProfile, bool, error) {
	if urn == "" {
		return nil, false, nil
	}
	// First try the primary urn column (indexed by migration 070).
	var a AgentProfile
	row := s.DB.QueryRow(`SELECT `+agentColumns+` FROM agent_profiles WHERE urn = ?`, urn)
	if err := scanAgent(row, &a); err == nil {
		return &a, true, nil
	}
	// Fall back to alias lookup. The urn_aliases column is a JSON array
	// of strings; SQLite's LIKE pattern matches the quoted form.
	pattern := `%"` + urn + `"%`
	rows, err := s.DB.Query(`SELECT `+agentColumns+` FROM agent_profiles WHERE urn_aliases LIKE ?`, pattern)
	if err != nil {
		return nil, false, fmt.Errorf("get agent by urn alias %s: %w", urn, err)
	}
	defer rows.Close()
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
func (s *Store) ListAgentsFilter(class, activationMode, status string, tagsAny []string) ([]AgentProfile, error) {
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
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list agents filter: %w", err)
	}
	defer rows.Close()
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
// (slug-based) but for direct-ID callers. Returns nil if the row
// didn't exist. Tombstones for durable=1 rows are deferred future work;
// today a DELETE wipes the row regardless of durable. FU-28.
func (s *Store) DeleteAgentByID(id string) error {
	a, err := s.GetAgent(id)
	if err != nil {
		return nil
	}
	return s.DeleteAgent(a.Slug)
}

// CloneAgent copies an existing agent profile under a new slug + name,
// mints a fresh URN, resets agent_hash + version=1, and returns the new
// AgentProfile. Tags, role_tools, system_prompt are copied verbatim.
// FU-28.
func (s *Store) CloneAgent(srcID, newSlug, newName string) (*AgentProfile, error) {
	src, err := s.GetAgent(srcID)
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
	if err := s.CreateAgent(&clone); err != nil {
		return nil, fmt.Errorf("clone agent: create: %w", err)
	}
	return &clone, nil
}

// ListSessionsByAgentID returns all sessions linked to an agent via
// the session_agents junction table, ordered by joined_at DESC. FU-28.
func (s *Store) ListSessionsByAgentID(agentID string) ([]SessionAgent, error) {
	rows, err := s.DB.Query(
		`SELECT sa.session_id, sa.agent_id, sa.mode, sa.joined_at, sa.is_primary
		 FROM session_agents sa
		 WHERE sa.agent_id = ?
		 ORDER BY sa.joined_at DESC`, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions by agent: %w", err)
	}
	defer rows.Close()
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
func (s *Store) SetSessionStatusByAgentID(agentID, status string) (int64, error) {
	res, err := s.DB.Exec(
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
