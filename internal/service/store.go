package service

import "github.com/hollis-labs/nanite/internal/store"

// Domain-scoped sub-interfaces carved from store.Store's methods.
// Each service depends only on the slice it needs. The concrete
// *store.Store satisfies all of them implicitly — no adapter code required.

// SessionReader provides read access to sessions and messages.
type SessionReader interface {
	GetSession(id string) (*store.Session, error)
	ListSessions(workspaceID string, includeArchived ...bool) ([]store.Session, error)
	ListMessages(sessionID string, limit int) ([]store.Message, error)
	ListMessagesPaginated(sessionID string, limit, offset int) (*store.MessagePage, error)
	ListMessagesAroundID(sessionID, messageID string, before, after int) (*store.MessagePage, error)
	GetMessage(id string) (*store.Message, error)
	SearchMessages(query, workspaceID, projectID string, limit int) ([]store.SearchResult, error)
}

// SessionWriter provides write access to sessions and messages.
type SessionWriter interface {
	CreateSession(sess *store.Session) error
	UpdateSession(sess *store.Session) error
	UpdateSessionTags(id, tagsJSON string) error
	UpdateSessionMetadata(id, metadataJSON string) error
	ArchiveSession(id string) error
	NextShortCode() (string, error)
	CreateMessage(msg *store.Message) error
	UpdateMessageContent(id, content string, isCompacted bool) error
	UpdateSessionCompaction(id, summary string) error
	ForkSession(sourceID string, overrides *store.Session, copyMessages bool) (*store.Session, error)
	CopyMessages(sourceSessionID, targetSessionID string) error
}

// AgentReader provides read access to agents, modes, skills, and session-agent bindings.
type AgentReader interface {
	GetAgent(id string) (*store.AgentProfile, error)
	GetAgentBySlug(slug string) (*store.AgentProfile, error)
	ListAgents() ([]store.AgentProfile, error)
	ListAgentsBySource(source string) ([]store.AgentProfile, error)
	GetAgentMode(agentID, modeSlug string) (*store.AgentMode, error)
	ListAgentModes(agentID string) ([]store.AgentMode, error)
	GetSessionPrimaryAgent(sessionID string) (*store.SessionAgent, error)
	ListSessionAgents(sessionID string) ([]store.SessionAgent, error)
	ListAgentSkills(agentID string) ([]store.Skill, error)
	ListAgentProjects(agentID string) ([]store.Project, error)
	ListProjectAgents(projectID string) ([]store.AgentProfile, error)
	GetAgentAssignedModes(agentID string) ([]store.Mode, error)
}

// AgentWriter provides write access to agents and session-agent bindings.
type AgentWriter interface {
	CreateAgent(a *store.AgentProfile) error
	UpdateAgent(a *store.AgentProfile) error
	DeleteAgent(slug string) error
	UpsertAgentBySlug(a *store.AgentProfile) error
	CreateAgentMode(m *store.AgentMode) error
	EnsureSessionAgent(sessionID, agentID, mode string, isPrimary bool) error
	SetSessionAgentMode(sessionID, agentID, mode string) error
	DeleteSessionAgent(sessionID, agentID string) error
	AssignSkillToAgent(agentID, skillID, config string) error
	RemoveSkillFromAgent(agentID, skillID string) error
	AddAgentProject(agentID, projectID string) error
	RemoveAgentProject(agentID, projectID string) error
	AssignModeToAgent(agentID, modeID string) error
	UnassignModeFromAgent(agentID, modeID string) error
}

// ToolStore provides access to MCP server configs and the catalog.
type ToolStore interface {
	ListMCPServers() ([]store.MCPServerConfig, error)
	GetMCPServer(name string) (*store.MCPServerConfig, error)
	CreateMCPServer(cfg *store.MCPServerConfig) error
	UpdateMCPServer(cfg *store.MCPServerConfig) error
	DeleteMCPServer(name string) error
	ListCatalogSources() ([]store.CatalogSource, error)
	GetCatalogSource(id string) (*store.CatalogSource, error)
	CreateCatalogSource(name, url, sourceType string, priority int) (*store.CatalogSource, error)
	UpdateCatalogSource(id, name, url string, enabled bool, priority int) error
	SetCatalogSourcePublicKey(id, publicKey string) error
	DeleteCatalogSource(id string) error
}

// UsageStore provides access to token usage, execution metrics, and event logs.
type UsageStore interface {
	RecordUsage(sessionID, messageID, model string, inputTokens, outputTokens, toolInputTokens, cacheCreationTokens, cacheReadTokens int) error
	GetSessionUsage(sessionID string) (*store.SessionUsageSummary, error)
	GetUsageSummary() (*store.UsageSummary, error)
	RecordExecutionMetrics(m *store.ExecutionMetrics) error
	GetSessionExecutionMetrics(sessionID string) ([]store.ExecutionMetrics, error)
	GetRecentExecutionMetrics(limit int) ([]store.ExecutionMetrics, error)
	GetUtilityCallSummary() ([]store.UtilityCallSummary, error)
	GetUtilityCallLog(limit int) ([]store.ExecutionMetrics, error)
	LogEvent(sessionID, eventType, category, detail, metadata string)
	ListEvents(category string, limit int) ([]store.EventLog, error)
	CountSessionToolCalls(sessionID string) int
}

// SettingsStore provides access to user settings and plugin settings.
type SettingsStore interface {
	GetUserSettings() (*store.UserSettings, error)
	UpdateUserSettings(us *store.UserSettings) error
	GetPluginSettings(pluginID string) (*store.PluginSettings, error)
	UpsertPluginSettings(pluginID string, settings map[string]any) error
	UpsertPluginSchema(pluginID string, schema []store.ConfigField) error
	ListPluginSettings() ([]*store.PluginSettings, error)
	UpdatePluginIcon(pluginID, icon string) error
	GetPluginSettingValue(pluginID, key string) (string, error)
}

// WorkspaceStore provides access to workspaces and projects.
type WorkspaceStore interface {
	ListWorkspaces() ([]store.Workspace, error)
	GetWorkspace(id string) (*store.Workspace, error)
	CreateWorkspace(w *store.Workspace) error
	UpdateWorkspace(w *store.Workspace) error
	DeleteWorkspace(id string) error
	ListProjects(workspaceID string) ([]store.Project, error)
	GetProject(id string) (*store.Project, error)
	CreateProject(p *store.Project) error
	UpdateProject(p *store.Project) error
	DeleteProject(id string) error
}

// BookmarkStore provides access to message bookmarks.
type BookmarkStore interface {
	ListBookmarks(sessionID string) ([]store.Bookmark, error)
	GetBookmark(id string) (*store.Bookmark, error)
	GetBookmarkByMessage(messageID string) (*store.Bookmark, error)
	CreateBookmark(b *store.Bookmark) error
	DeleteBookmark(id string) error
	UpdateBookmarkNote(id, note string) error
}

// ArtifactStore provides access to session artifacts.
type ArtifactStore interface {
	ListArtifacts(sessionID string) ([]store.Artifact, error)
	ListArtifactsByOrigin(sessionID, origin string) ([]store.Artifact, error)
	CreateArtifact(a *store.Artifact) error
	GetArtifact(id string) (*store.Artifact, error)
}

// TemplateStore provides access to prompt templates and output templates.
type TemplateStore interface {
	// Prompt templates
	ListPromptTemplates() ([]store.PromptTemplate, error)
	GetPromptTemplate(id string) (*store.PromptTemplate, error)
	GetPromptTemplateBySlug(slug string) (*store.PromptTemplate, error)
	CreatePromptTemplate(pt *store.PromptTemplate) error
	UpdatePromptTemplate(pt *store.PromptTemplate) error
	DeletePromptTemplate(id string) error
	ListPromptTemplatesForAgent(agentID string) ([]store.PromptTemplate, error)
	AssignPromptTemplateToAgent(agentID, templateID string) error
	RemovePromptTemplateFromAgent(agentID, templateID string) error
	ComposePromptForAgent(agentID string, variables map[string]string) (string, error)
	// Output templates
	ListTemplates() ([]store.Template, error)
	GetTemplate(name string) (*store.Template, error)
	CreateTemplate(t *store.Template) error
	UpdateTemplate(name, templateText string) error
	DeleteTemplate(name string) error
}

// SkillStore provides CRUD access to skills (independent of agent bindings).
type SkillStore interface {
	ListSkills() ([]store.Skill, error)
	GetSkill(id string) (*store.Skill, error)
	GetSkillBySlug(slug string) (*store.Skill, error)
	CreateSkill(sk *store.Skill) error
	UpdateSkill(sk *store.Skill) error
	DeleteSkill(id string) error
}

// ModeStore provides CRUD access to modes (independent of agent bindings).
type ModeStore interface {
	CreateMode(m *store.Mode) error
	GetMode(id string) (*store.Mode, error)
	GetModeBySlug(slug string) (*store.Mode, error)
	ListModes() ([]store.Mode, error)
	UpdateMode(m *store.Mode) error
	DeleteMode(id string) error
}

// CustomActionStore provides access to custom actions.
type CustomActionStore interface {
	CreateCustomAction(action *store.CustomAction) error
	GetCustomAction(id string) (*store.CustomAction, error)
	UpdateCustomAction(action *store.CustomAction) error
	DeleteCustomAction(id string) error
	ListCustomActions() ([]store.CustomAction, error)
	ListCustomActionsByTrigger(trigger string) ([]store.CustomAction, error)
}

// TriggerRuleStore provides access to trigger rules.
type TriggerRuleStore interface {
	CreateTriggerRule(rule *store.TriggerRule) error
	GetTriggerRule(id string) (*store.TriggerRule, error)
	UpdateTriggerRule(rule *store.TriggerRule) error
	DeleteTriggerRule(id string) error
	ListTriggerRules(pluginID string) ([]store.TriggerRule, error)
	ListTriggerRulesByEvent(eventType string) ([]store.TriggerRule, error)
	DeleteTriggerRulesByPlugin(pluginID string) error
}

// TodoStore provides CRUD access to internal todos.
type TodoStore interface {
	CreateTodo(t *store.Todo) error
	GetTodo(id string) (*store.Todo, error)
	ListTodos(f store.TodoFilter) ([]store.Todo, error)
	UpdateTodo(t *store.Todo) error
	DeleteTodo(id string) error
	ListTodoChildren(parentID string) ([]store.Todo, error)
}

// PlanStore provides CRUD access to internal plans.
type PlanStore interface {
	CreatePlan(p *store.Plan) error
	GetPlan(id string) (*store.Plan, error)
	ListPlans(f store.PlanFilter) ([]store.Plan, error)
	UpdatePlan(p *store.Plan) error
	UpdatePlanStep(planID, stepID string, updates store.PlanStep) error
	DeletePlan(id string) error
}

// ProviderStore provides access to provider and model configuration.
type ProviderStore interface {
	ListProviders() ([]store.ProviderConfig, error)
	GetProvider(id string) (*store.ProviderConfig, error)
	ListModels() ([]store.Model, error)
	UpdateProvider(id string, u store.ProviderUpdate) error
	SetProviderAPIKey(id, apiKey string) error
	HasProviderAPIKey(id string) (bool, error)
}

// Store is the composite interface satisfied by *store.Store.
// Services that need the full surface (e.g. the Container constructor) use this.
type Store interface {
	SessionReader
	SessionWriter
	AgentReader
	AgentWriter
	ToolStore
	UsageStore
	SettingsStore
	WorkspaceStore
	BookmarkStore
	ArtifactStore
	TemplateStore
	SkillStore
	ModeStore
	CustomActionStore
	TriggerRuleStore
	ProviderStore
	TodoStore
	PlanStore
}

// Compile-time verification that *store.Store satisfies the composite interface.
var _ Store = (*store.Store)(nil)
