package store

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// AgentProfile represents an agent profile record.
type AgentProfile struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	Avatar          string `json:"avatar"`
	SystemPrompt    string `json:"system_prompt"`
	Description     string `json:"description"`
	Modes           string `json:"modes"`
	DefaultMode     string `json:"default_mode"`
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
}

// agentColumns is the canonical SELECT column list for agent_profiles.
const agentColumns = `id, name, slug, COALESCE(avatar,''), system_prompt, COALESCE(description,''),
        modes, default_mode, COALESCE(default_model,''), COALESCE(default_provider,''),
        mcp_servers, tool_permissions, can_execute, settings, created_at, updated_at,
        agent_hash, version, tools, directories, constraints, tags, status, source, source_ref,
        COALESCE(icon,''),
        kind, capabilities_json, limits_json, model_strategy,
        COALESCE(imported_at,''), COALESCE(origin_system,''), COALESCE(format,'markdown'),
        COALESCE(parent_dispatch_allowlist,'[]')`

// scanAgent scans a row into an AgentProfile using the canonical column order.
func scanAgent(scanner interface{ Scan(...any) error }, a *AgentProfile) error {
	return scanner.Scan(
		&a.ID, &a.Name, &a.Slug, &a.Avatar, &a.SystemPrompt, &a.Description,
		&a.Modes, &a.DefaultMode, &a.DefaultModel, &a.DefaultProvider,
		&a.MCPServers, &a.ToolPermissions, &a.CanExecute, &a.Settings, &a.CreatedAt, &a.UpdatedAt,
		&a.AgentHash, &a.Version, &a.Tools, &a.Directories, &a.Constraints, &a.Tags,
		&a.Status, &a.Source, &a.SourceRef, &a.Icon,
		&a.Kind, &a.CapabilitiesJSON, &a.LimitsJSON, &a.ModelStrategy,
		&a.ImportedAt, &a.OriginSystem, &a.Format,
		&a.ParentDispatchAllowlist,
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
	if a.DefaultMode == "" {
		a.DefaultMode = "default"
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

	_, err := s.DB.Exec(
		`INSERT INTO agent_profiles (id, name, slug, avatar, system_prompt, description,
		                              modes, default_mode, default_model, default_provider,
		                              mcp_servers, tool_permissions, can_execute, settings,
		                              created_at, updated_at,
		                              agent_hash, version, tools, directories, constraints,
		                              tags, status, source, source_ref, icon,
		                              kind, capabilities_json, limits_json, model_strategy,
		                              imported_at, origin_system, format,
		                              parent_dispatch_allowlist)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Slug, nullIfEmpty(a.Avatar), a.SystemPrompt, nullIfEmpty(a.Description),
		a.Modes, a.DefaultMode, nullIfEmpty(a.DefaultModel), a.DefaultProvider,
		a.MCPServers, a.ToolPermissions, a.CanExecute, a.Settings,
		now, now,
		a.AgentHash, a.Version, a.Tools, a.Directories, a.Constraints,
		a.Tags, a.Status, a.Source, a.SourceRef, nullIfEmpty(a.Icon),
		a.Kind, a.CapabilitiesJSON, a.LimitsJSON, a.ModelStrategy,
		a.ImportedAt, a.OriginSystem, a.Format,
		a.ParentDispatchAllowlist,
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

	// CW-20260512-0111: include `source` and `source_ref` in the UPDATE so
	// that file-source-of-truth boot sync can flip an already-deployed row
	// from source='builtin' (or any prior value) to source='internal' when
	// the file profile is now the canonical source. Without this, the
	// Wave 2 cleanup (CW-20260512-0112: DELETE WHERE source != 'internal')
	// would wipe rows whose body was re-synced from internal/agent/builtin/
	// profiles/*.md but whose source column never flipped.
	_, err := s.DB.Exec(
		`UPDATE agent_profiles SET name = ?, slug = ?, avatar = ?, system_prompt = ?, description = ?,
		        modes = ?, default_mode = ?, default_model = ?, default_provider = ?,
		        mcp_servers = ?, tool_permissions = ?, can_execute = ?, settings = ?,
		        updated_at = ?,
		        agent_hash = ?, version = ?, tools = ?, directories = ?, constraints = ?,
		        tags = ?, status = ?, source = ?, source_ref = ?, icon = ?,
		        imported_at = ?, origin_system = ?, format = ?,
		        parent_dispatch_allowlist = ?
		 WHERE id = ?`,
		a.Name, a.Slug, nullIfEmpty(a.Avatar), a.SystemPrompt, nullIfEmpty(a.Description),
		a.Modes, a.DefaultMode, nullIfEmpty(a.DefaultModel), a.DefaultProvider,
		a.MCPServers, a.ToolPermissions, a.CanExecute, a.Settings,
		now,
		a.AgentHash, a.Version, a.Tools, a.Directories, a.Constraints,
		a.Tags, a.Status, a.Source, a.SourceRef, nullIfEmpty(a.Icon),
		a.ImportedAt, a.OriginSystem, a.Format,
		a.ParentDispatchAllowlist,
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
	return s.UpdateAgent(a)
}
