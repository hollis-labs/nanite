package selftools

import "github.com/hollis-labs/nanite/internal/store"

// selfToolAgentProfileView fixes the JSON shape emitted inside self-tool text results.
type selfToolAgentProfileView struct {
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	Slug                    string `json:"slug"`
	Avatar                  string `json:"avatar"`
	SystemPrompt            string `json:"system_prompt"`
	Description             string `json:"description"`
	Modes                   string `json:"modes"`
	DefaultModel            string `json:"default_model"`
	DefaultProvider         string `json:"default_provider"`
	MCPServers              string `json:"mcp_servers"`
	ToolPermissions         string `json:"tool_permissions"`
	CanExecute              bool   `json:"can_execute"`
	Settings                string `json:"settings"`
	CreatedAt               string `json:"created_at"`
	UpdatedAt               string `json:"updated_at"`
	AgentHash               string `json:"agent_hash"`
	Version                 int    `json:"version"`
	Tools                   string `json:"tools"`
	Directories             string `json:"directories"`
	Constraints             string `json:"constraints"`
	Tags                    string `json:"tags"`
	Status                  string `json:"status"`
	Source                  string `json:"source"`
	SourceRef               string `json:"source_ref"`
	Icon                    string `json:"icon"`
	Kind                    string `json:"kind"`
	CapabilitiesJSON        string `json:"capabilities_json"`
	LimitsJSON              string `json:"limits_json"`
	ModelStrategy           string `json:"model_strategy"`
	ImportedAt              string `json:"imported_at"`   // RFC3339 timestamp of last ingest; empty for non-file agents
	OriginSystem            string `json:"origin_system"` // "nanite", "agentrc", "claude", etc. — free-form provenance
	Format                  string `json:"format"`        // "markdown", "yaml"
	ParentDispatchAllowlist string `json:"parent_dispatch_allowlist"`
	RoleTools               string `json:"role_tools"`
	RoleSkills              string `json:"role_skills"`
	ContextPolicy           string `json:"context_policy"`
	Durable                 bool   `json:"durable"`
	ActivationMode          string `json:"activation_mode"`
	Class                   string `json:"class"`
	DefaultState            string `json:"default_state"`
	ConsumerID              string `json:"consumer_id"`
	RoleID                  string `json:"role_id"`
	ModelID                 string `json:"model_id"`
	RuntimeKind             string `json:"runtime_kind"`
	Protocol                string `json:"protocol"`
	Transport               string `json:"transport"`
	PluginID                string `json:"plugin_id"`
	TetherManaged           bool   `json:"tether_managed"`
	TetherURN               string `json:"tether_urn"`
}

func selfToolAgentProfileToView(r *store.AgentProfile) *selfToolAgentProfileView {
	if r == nil {
		return nil
	}
	return &selfToolAgentProfileView{
		ID:                      r.ID,
		Name:                    r.Name,
		Slug:                    r.Slug,
		Avatar:                  r.Avatar,
		SystemPrompt:            r.SystemPrompt,
		Description:             r.Description,
		Modes:                   r.Modes,
		DefaultModel:            r.DefaultModel,
		DefaultProvider:         r.DefaultProvider,
		MCPServers:              r.MCPServers,
		ToolPermissions:         r.ToolPermissions,
		CanExecute:              r.CanExecute,
		Settings:                r.Settings,
		CreatedAt:               r.CreatedAt,
		UpdatedAt:               r.UpdatedAt,
		AgentHash:               r.AgentHash,
		Version:                 r.Version,
		Tools:                   r.Tools,
		Directories:             r.Directories,
		Constraints:             r.Constraints,
		Tags:                    r.Tags,
		Status:                  r.Status,
		Source:                  r.Source,
		SourceRef:               r.SourceRef,
		Icon:                    r.Icon,
		Kind:                    r.Kind,
		CapabilitiesJSON:        r.CapabilitiesJSON,
		LimitsJSON:              r.LimitsJSON,
		ModelStrategy:           r.ModelStrategy,
		ImportedAt:              r.ImportedAt,
		OriginSystem:            r.OriginSystem,
		Format:                  r.Format,
		ParentDispatchAllowlist: r.ParentDispatchAllowlist,
		RoleTools:               r.RoleTools,
		RoleSkills:              r.RoleSkills,
		ContextPolicy:           r.ContextPolicy,
		Durable:                 r.Durable,
		ActivationMode:          r.ActivationMode,
		Class:                   r.Class,
		DefaultState:            r.DefaultState,
		ConsumerID:              r.ConsumerID,
		RoleID:                  r.RoleID,
		ModelID:                 r.ModelID,
		RuntimeKind:             r.RuntimeKind,
		Protocol:                r.Protocol,
		Transport:               r.Transport,
		PluginID:                r.PluginID,
		TetherManaged:           r.TetherManaged,
		TetherURN:               r.TetherURN,
	}
}

// selfToolTodoView fixes the JSON shape emitted inside self-tool text results.
type selfToolTodoView struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`    // turn, session, project
	ScopeID     string `json:"scope_id"` // session_id (turn/session) or project_id (project)
	ProjectID   string `json:"project_id,omitempty"`
	ParentID    string `json:"parent_id"` // optional parent todo for nesting
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`   // pending, in_progress, done, blocked
	Priority    string `json:"priority"` // low, medium, high, critical
	Labels      string `json:"labels"`   // JSON array of strings
	Metadata    string `json:"metadata"` // JSON object
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func selfToolTodoToView(r *store.Todo) *selfToolTodoView {
	if r == nil {
		return nil
	}
	return &selfToolTodoView{
		ID:          r.ID,
		Scope:       r.Scope,
		ScopeID:     r.ScopeID,
		ProjectID:   r.ProjectID,
		ParentID:    r.ParentID,
		Title:       r.Title,
		Description: r.Description,
		Status:      r.Status,
		Priority:    r.Priority,
		Labels:      r.Labels,
		Metadata:    r.Metadata,
		CreatedBy:   r.CreatedBy,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// selfToolPlanView fixes the JSON shape emitted inside self-tool text results.
type selfToolPlanView struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`    // workspace, project, session
	ScopeID     string `json:"scope_id"` // empty for workspace, project_id or session_id
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"` // proposed, approved, in_progress, complete, abandoned
	Steps       string `json:"steps"`  // JSON array of PlanStep
	Metadata    string `json:"metadata"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func selfToolPlanToView(r *store.Plan) *selfToolPlanView {
	if r == nil {
		return nil
	}
	return &selfToolPlanView{
		ID:          r.ID,
		Scope:       r.Scope,
		ScopeID:     r.ScopeID,
		Title:       r.Title,
		Description: r.Description,
		Status:      r.Status,
		Steps:       r.Steps,
		Metadata:    r.Metadata,
		CreatedBy:   r.CreatedBy,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}
