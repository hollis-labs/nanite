package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
)

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
	ForkSession(sourceID string, overrides *store.Session, copyMessages bool) (*store.Session, error)
	CopyMessages(sourceSessionID, targetSessionID string) error
}

// AgentReader provides read access to agents, skills, and session-agent bindings.
type AgentReader interface {
	GetAgent(id string) (*store.AgentProfile, error)
	GetAgentBySlug(slug string) (*store.AgentProfile, error)
	ListAgents() ([]store.AgentProfile, error)
	ListAgentsBySource(source string) ([]store.AgentProfile, error)
	GetSessionPrimaryAgent(sessionID string) (*store.SessionAgent, error)
	ListSessionAgents(sessionID string) ([]store.SessionAgent, error)
	ListAgentSkills(agentID string) ([]store.Skill, error)
	ListAgentProjects(agentID string) ([]store.Project, error)
	ListProjectAgents(projectID string) ([]store.AgentProfile, error)
}

// AgentWriter provides write access to agents and session-agent bindings.
type AgentWriter interface {
	CreateAgent(a *store.AgentProfile) error
	UpdateAgent(a *store.AgentProfile) error
	DeleteAgent(slug string) error
	UpsertAgentBySlug(a *store.AgentProfile) error
	EnsureSessionAgent(sessionID, agentID, mode string, isPrimary bool) error
	SetSessionAgentMode(sessionID, agentID, mode string) error
	DeleteSessionAgent(sessionID, agentID string) error
	AssignSkillToAgent(agentID, skillID, config string) error
	RemoveSkillFromAgent(agentID, skillID string) error
	AddAgentProject(agentID, projectID string) error
	RemoveAgentProject(agentID, projectID string) error
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

	// InsertAgentBrokerDecision appends a row to agent_broker_decisions.
	// CW-20260509-0046: the upstream agent-broker call site in
	// chat_generate.go writes one row per turn (dispatch or chat-direct).
	// Distinct from the tool-broker's broker_decisions log
	// (BrokerDecisionLogger above) — naming history captured at
	// internal/store/broker_decisions.go.
	InsertAgentBrokerDecision(row *store.AgentBrokerDecision) error
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
	// ListArtifactsByProject returns artifacts whose owning session belongs
	// to the given project. F4 (CW-20260429-0004): backs the right-rail
	// "This Project" inherited-artifacts section. excludeSessionID, when
	// non-empty, excludes artifacts owned by that session from the result.
	ListArtifactsByProject(projectID, excludeSessionID string) ([]store.Artifact, error)
	CreateArtifact(a *store.Artifact) error
	GetArtifact(id string) (*store.Artifact, error)
}

// TemplateStore provides access to prompt templates.
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

// TodoStore provides CRUD access to internal todos.
type TodoStore interface {
	CreateTodo(t *store.Todo) error
	GetTodo(id string) (*store.Todo, error)
	ListTodos(f store.TodoFilter) ([]store.Todo, error)
	UpdateTodo(t *store.Todo) error
	UpdateTodoScope(id, scope, scopeID, projectID string) error
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

// DefaultResolver is the minimal interface exposed by store.Store for
// "what provider+model should this call use?" resolution. Service
// helpers that need only the resolver (e.g. BuildSummarizer) accept this
// interface instead of the full Store so they remain easy to fake in
// tests. CW-20260526-0003.
type DefaultResolver interface {
	ResolveProviderAndModel(explicitProvider, explicitModel string) (string, string, error)
	DefaultModelForProvider(providerType string) (string, error)
}

// ProviderStore provides access to provider and model configuration.
type ProviderStore interface {
	ListProviders() ([]store.ProviderConfig, error)
	GetProvider(id string) (*store.ProviderConfig, error)
	ListModels() ([]store.Model, error)
	UpdateProvider(id string, u store.ProviderUpdate) error

	// DefaultModelForProvider returns providers.default_model for the
	// given provider_type, or an ErrNoDefaultModel-wrapped error when
	// no row supplies one (CW-20260526-0003).
	DefaultModelForProvider(providerType string) (string, error)

	// ResolveProviderAndModel walks explicit args →
	// user_settings.default_{provider,model} → providers.default_model
	// for the resolved provider_type, returning ErrNoDefaultModel when
	// the chain is dry (CW-20260526-0003). This is the SSOT for
	// "what provider+model should this call use?"
	ResolveProviderAndModel(explicitProvider, explicitModel string) (string, string, error)
}

// HandoffStashStore covers session handoff stash persistence + retrieval
// (P7, CW-20260420-0024 — write; Glass-4, CW-20260502-0015 — read).
type HandoffStashStore interface {
	UpsertHandoffStash(stash store.HandoffStash) error
	GetHandoffStash(sessionID, stashID string) (store.HandoffStash, error)
	GetLatestStashForSession(sessionID string) (store.HandoffStash, error)
}

// ReminderStore covers reminder persistence (J11, CW-20260426-0009; D1, CW-20260428-0014).
type ReminderStore interface {
	CreateReminder(r store.Reminder) error
	GetReminder(id string) (store.Reminder, error)
	ListUnfiredReminders(sessionID string) ([]store.Reminder, error)
	MarkReminderFired(id string) error
	UpdateReminderScope(id, scope, projectID string) error
	DeleteReminder(id string) error
}

// PinnedContentStore covers pinned content persistence (J11, CW-20260426-0009; D1, CW-20260428-0014).
type PinnedContentStore interface {
	CreatePinnedContent(p store.PinnedContent) error
	ListPinnedContent(sessionID string) ([]store.PinnedContent, error)
	DeletePinnedContent(id string) error
	UpdatePinScope(id, scope, projectID string) error
	ClearSessionPins(sessionID string) error
}

// CompactionEventStore covers structured compaction-event persistence and
// retrieval (P8 CompactionContract — write side CW-20260420-0027 Part C,
// read side CW-20260420-0025 Part A disclosure injection).
type CompactionEventStore interface {
	WriteCompactionEvent(ctx context.Context, event store.CompactionEvent) error
	GetLatestCompactionEvent(ctx context.Context, sessionID string) (*store.CompactionEvent, error)
	ListCompactionEventsBySession(ctx context.Context, sessionID string, limit int) ([]store.CompactionEvent, error)
}

// Store is the composite interface satisfied by *store.Store.
// Services that need the full surface (e.g. the Container constructor) use this.
// EnvelopeStore covers persistence for envelope instances emitted during a chat turn.
type EnvelopeStore interface {
	CreateEnvelopeInstance(inst *store.EnvelopeInstance) error
	GetEnvelopeInstance(id string) (*store.EnvelopeInstance, error)
}

// SubagentRunsReader exposes the narrow query the chat loop needs to
// classify "is the current pause caused by a hung subagent" — used to
// suppress the FE-visible `[generation interrupted]` placeholder and
// `ErrorCodeInternal` event when a subagent dispatch is blocking the
// parent turn (CW-20260512-0002 subtodo (d)). Returns the active row's
// id + role + child_session_id for structured logging, or empty values
// when there is no active subagent for the parent session.
type SubagentRunsReader interface {
	ActiveSubagentRunForParent(parentSessionID string) (id, role, childSessionID string, ok bool, err error)
}

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
	ProviderStore
	TodoStore
	PlanStore
	HandoffStashStore
	CompactionEventStore
	EnvelopeStore
	ReminderStore
	PinnedContentStore
	SubagentRunsReader

	// AgentRuntimeProviderSessionID returns the captured provider session id
	// for a chat session's runtime row, or "" when none. CW-20260525-0001
	// Slice 3 — resume a CLI provider session after a host restart.
	AgentRuntimeProviderSessionID(id string) (string, error)

	// SetAgentRuntimeProviderSessionID overwrites (or clears with "") the
	// captured provider_session_id on a chat session's runtime row.
	// CW-20260525-0001 Slice 3 follow-up — used by stale-resume detection
	// to clear an expired id after a fast-exit-after-resume so the next
	// turn cold-boots without --resume.
	SetAgentRuntimeProviderSessionID(id, providerSessionID string) error
}

// Compile-time verification that *store.Store satisfies the composite interface.
var _ Store = (*store.Store)(nil)
