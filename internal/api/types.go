package api

// Request types for API handlers.
// Organized by resource. Each type replaces an anonymous struct
// that was previously defined inline in a handler function.

// --- Sessions ---

type CreateSessionRequest struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Model       string `json:"model"`
	Provider    string `json:"provider"`
	AgentID     string `json:"agent_id"`
}

type ForkSessionRequest struct {
	IncludeMessages bool   `json:"include_messages"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
}

type UpdateSessionRequest struct {
	Title      *string `json:"title"`
	CustomName *string `json:"custom_name"`
	IsPinned   *bool   `json:"is_pinned"`
	Model      *string `json:"model"`
	Provider   *string `json:"provider"`
	Status     *string `json:"status"`
}

type SwitchSessionModeRequest struct {
	Mode string `json:"mode"`
}

// --- Messages ---

type SendMessageRequest struct {
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	// Effort biases the token budget multiplier and reasoning-block enablement
	// for this turn. Valid values: "low", "normal" (default), "high", "max".
	// Empty string or omitted → "normal". Unknown values are ignored (treated as normal).
	// See internal/effort for the full mapping (CW-20260420-0014).
	Effort string `json:"effort,omitempty"`
}

type AgentMessageRequest struct {
	FromSessionID string `json:"from_session_id"`
	Content       string `json:"content"`
}

type DelegateTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	AgentID     string `json:"agent_id,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Model       string `json:"model,omitempty"`
}

type DelegateAndAggregateRequest struct {
	Message string `json:"message"`
	Model   string `json:"model,omitempty"`
}

// --- Agents ---

type CreateAgentRequest struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	Avatar          string `json:"avatar"`
	SystemPrompt    string `json:"system_prompt"`
	Description     string `json:"description"`
	Modes           string `json:"modes"`
	DefaultMode     string `json:"default_mode"`
	DefaultModel    string `json:"default_model"`
	MCPServers      string `json:"mcp_servers"`
	ToolPermissions string `json:"tool_permissions"`
	CanExecute      bool   `json:"can_execute"`
	Settings        string `json:"settings"`
	Tools           string `json:"tools"`
	Directories     string `json:"directories"`
	Constraints     string `json:"constraints"`
	Tags            string `json:"tags"`
	Status          string `json:"status"`
	Source          string `json:"source"`
	SourceRef       string `json:"source_ref"`
	Icon            string `json:"icon"`
}

type UpdateAgentRequest struct {
	Name            *string `json:"name"`
	Slug            *string `json:"slug"`
	Avatar          *string `json:"avatar"`
	SystemPrompt    *string `json:"system_prompt"`
	Description     *string `json:"description"`
	Modes           *string `json:"modes"`
	DefaultMode     *string `json:"default_mode"`
	DefaultModel    *string `json:"default_model"`
	MCPServers      *string `json:"mcp_servers"`
	ToolPermissions *string `json:"tool_permissions"`
	CanExecute      *bool   `json:"can_execute"`
	Settings        *string `json:"settings"`
	Tools           *string `json:"tools"`
	Directories     *string `json:"directories"`
	Constraints     *string `json:"constraints"`
	Tags            *string `json:"tags"`
	Status          *string `json:"status"`
	Icon            *string `json:"icon"`
}

type AddSessionAgentRequest struct {
	AgentID string `json:"agent_id"`
	Role    string `json:"role"`
}

type AddAgentProjectRequest struct {
	ProjectID string `json:"project_id"`
}

type CreateAgentModeRequest struct {
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	PromptAddendum string `json:"prompt_addendum"`
	ToolOverrides  string `json:"tool_overrides"`
	Settings       string `json:"settings"`
}

// --- Artifacts ---

type PlaceArtifactRequest struct {
	SessionID   string `json:"session_id"`
	MessageID   string `json:"message_id"`
	Name        string `json:"name"`
	MimeType    string `json:"mime_type"`
	StoragePath string `json:"storage_path"`
	AgentID     string `json:"agent_id"`
	PluginID    string `json:"plugin_id"`
}

// --- Tools ---

type SelectToolsRequest struct {
	Intent string   `json:"intent"`
	Hints  []string `json:"hints"`
}

// --- Templates ---

type CreateTemplateRequest struct {
	Name     string `json:"name"`
	Template string `json:"template"`
}

type UpdateTemplateRequest struct {
	Template string `json:"template"`
}

type ApplyTemplateRequest struct {
	SessionID string `json:"session_id"`
}

// --- Prompt Templates ---

type CreatePromptTemplateRequest struct {
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Scope     string `json:"scope"`
	Template  string `json:"template"`
	Variables string `json:"variables"`
	Priority  int    `json:"priority"`
	Icon      string `json:"icon"`
}

type UpdatePromptTemplateRequest struct {
	Name      *string `json:"name"`
	Slug      *string `json:"slug"`
	Scope     *string `json:"scope"`
	Template  *string `json:"template"`
	Variables *string `json:"variables"`
	Priority  *int    `json:"priority"`
	Icon      *string `json:"icon"`
}

type AssignAgentPromptTemplateRequest struct {
	TemplateID string `json:"template_id"`
}

// --- Bookmarks ---

type CreateBookmarkRequest struct {
	MessageID string `json:"message_id"`
	SessionID string `json:"session_id"`
	Note      string `json:"note"`
}

// --- Plans ---

type ApprovePlanRequest struct {
	CreateTodos bool `json:"create_todos"`
}

// --- Shell ---

type SetShellModeRequest struct {
	Mode string `json:"mode"`
}

type ShellExecRequest struct {
	Command  string `json:"command"`
	Approved bool   `json:"approved"`
}

// --- Workspaces ---

type CreateWorkspaceRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

type UpdateWorkspaceRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Icon        *string `json:"icon"`
}

type CreateProjectRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RepoPath    string `json:"repo_path"`
}

type UpdateProjectRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	RepoPath    *string `json:"repo_path"`
	Settings    *string `json:"settings"`
	SortOrder   *int    `json:"sort_order"`
}

// --- Triggers ---

type CreateTriggerRuleRequest struct {
	PluginID        string `json:"plugin_id"`
	EventType       string `json:"event_type"`
	ConnectorName   string `json:"connector_name"`
	PayloadTemplate string `json:"payload_template"`
	FilterExpr      string `json:"filter_expr"`
	Enabled         *bool  `json:"enabled"`
	Description     string `json:"description"`
}

type UpdateTriggerRuleRequest struct {
	EventType       *string `json:"event_type"`
	ConnectorName   *string `json:"connector_name"`
	PayloadTemplate *string `json:"payload_template"`
	FilterExpr      *string `json:"filter_expr"`
	Enabled         *bool   `json:"enabled"`
	Description     *string `json:"description"`
}

// --- Actions ---

type CreateActionRequest struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Keybinding   string `json:"keybinding"`
	Command      string `json:"command"`
	SlashCommand string `json:"slash_command"`
	AutoTriggers string `json:"auto_triggers"`
	Enabled      *bool  `json:"enabled"`
}

type UpdateActionRequest struct {
	Name         *string `json:"name"`
	Description  *string `json:"description"`
	Keybinding   *string `json:"keybinding"`
	Command      *string `json:"command"`
	SlashCommand *string `json:"slash_command"`
	AutoTriggers *string `json:"auto_triggers"`
	Enabled      *bool   `json:"enabled"`
}

type ExecuteActionRequest struct {
	SessionID string `json:"session_id"`
}

// --- Skills ---

type CreateSkillRequest struct {
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Description  string `json:"description"`
	Category     string `json:"category"`
	ToolBindings string `json:"tool_bindings"`
	InputSchema  string `json:"input_schema"`
	Settings     string `json:"settings"`
	Icon         string `json:"icon"`
}

type UpdateSkillRequest struct {
	Name         *string `json:"name"`
	Slug         *string `json:"slug"`
	Description  *string `json:"description"`
	Category     *string `json:"category"`
	ToolBindings *string `json:"tool_bindings"`
	InputSchema  *string `json:"input_schema"`
	Settings     *string `json:"settings"`
	Icon         *string `json:"icon"`
}

type AssignAgentSkillRequest struct {
	SkillID string `json:"skill_id"`
	Config  string `json:"config"`
}

// --- Modes ---

type CreateModeRequest struct {
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	PromptAddendum string `json:"prompt_addendum"`
	ToolOverrides  string `json:"tool_overrides"`
	Settings       string `json:"settings"`
}

type UpdateModeRequest struct {
	Name           *string `json:"name"`
	Slug           *string `json:"slug"`
	PromptAddendum *string `json:"prompt_addendum"`
	ToolOverrides  *string `json:"tool_overrides"`
	Settings       *string `json:"settings"`
}

type AssignModeToAgentRequest struct {
	ModeID string `json:"mode_id"`
}

// --- Approvals ---

type RespondApprovalRequest struct {
	Decision string `json:"decision"`
	Scope    string `json:"scope"`
}

type SetPermissionModeRequest struct {
	Mode string `json:"mode"`
}

// --- Tasks ---

type TransitionTaskRequest struct {
	Status string `json:"status"`
}

type AssignTaskRequest struct {
	AgentID         string `json:"agent_id"`
	WorkerSessionID string `json:"worker_session_id"`
}

// --- Commands ---

type ExecuteCommandRequest struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	Args      string `json:"args"`
}

// --- Plugins ---

type InstallLocalRequest struct {
	Path string `json:"path"`
}

// --- Catalog ---

type AddSourceRequest struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Priority int    `json:"priority"`
}

type UpdateSourceRequest struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Enabled  *bool  `json:"enabled"`
	Priority *int   `json:"priority"`
}

type SetSourceKeyRequest struct {
	PublicKey string `json:"public_key"`
}

type CatalogInstallRequest struct {
	Name string `json:"name"`
}

// --- Provider Management ---

type SetProviderAPIKeyRequest struct {
	APIKey string `json:"api_key"`
}

// --- Trust (H1, CW-20260421-0014) ---

// SetWorkspaceRoleTrustRequest is the body for
// POST /api/workspaces/{workspace_id}/roles/{agent_profile_id}/trust.
type SetWorkspaceRoleTrustRequest struct {
	Tier       string `json:"tier"`        // "untrusted" | "normal" | "trusted"
	PromotedBy string `json:"promoted_by"` // optional attribution string
}
