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
	GetSession(ctx context.Context, id string) (*store.Session, error)
	ListSessions(ctx context.Context, includeArchived ...bool) ([]store.Session, error)
	ListMessages(ctx context.Context, sessionID string, limit int) ([]store.Message, error)
	ListMessagesPaginated(ctx context.Context, sessionID string, limit, offset int) (*store.MessagePage, error)
	ListMessagesAroundID(ctx context.Context, sessionID, messageID string, before, after int) (*store.MessagePage, error)
	GetMessage(ctx context.Context, id string) (*store.Message, error)
	SearchMessages(ctx context.Context, query, projectID string, limit int) ([]store.SearchResult, error)
}

// SessionWriter provides write access to sessions and messages.
type SessionWriter interface {
	CreateSession(ctx context.Context, sess *store.Session) error
	UpdateSession(ctx context.Context, sess *store.Session) error
	UpdateSessionTags(ctx context.Context, id, tagsJSON string) error
	UpdateSessionMetadata(ctx context.Context, id, metadataJSON string) error
	ArchiveSession(ctx context.Context, id string) error
	NextShortCode(ctx context.Context) (string, error)
	CreateMessage(ctx context.Context, msg *store.Message) error
	UpdateMessageContent(ctx context.Context, id, content string, isCompacted bool) error
	ForkSession(ctx context.Context, sourceID string, overrides *store.Session, copyMessages bool) (*store.Session, error)
	CopyMessages(ctx context.Context, sourceSessionID, targetSessionID string) error
}

// AgentReader provides read access to agents, skills, and session-agent bindings.
type AgentReader interface {
	GetAgent(ctx context.Context, id string) (*store.AgentProfile, error)
	GetAgentBySlug(ctx context.Context, slug string) (*store.AgentProfile, error)
	ListAgents(ctx context.Context) ([]store.AgentProfile, error)
	ListAgentsBySource(ctx context.Context, source string) ([]store.AgentProfile, error)
	GetSessionPrimaryAgent(ctx context.Context, sessionID string) (*store.SessionAgent, error)
	ListSessionAgents(ctx context.Context, sessionID string) ([]store.SessionAgent, error)
	ListAgentSkills(ctx context.Context, agentID string) ([]store.Skill, error)
	ListAgentProjects(ctx context.Context, agentID string) ([]store.Project, error)
	ListProjectAgents(ctx context.Context, projectID string) ([]store.AgentProfile, error)
	// GetRole backs roleForProfile's role -> agent -> task cascade lookup
	// (02-add-agents-composition-columns.md). Returns (nil, nil) on a
	// miss, matching store.Store.GetRole's own contract.
	GetRole(ctx context.Context, id string) (*store.Role, error)
}

// AgentWriter provides write access to agents and session-agent bindings.
type AgentWriter interface {
	CreateAgent(ctx context.Context, a *store.AgentProfile) error
	UpdateAgent(ctx context.Context, a *store.AgentProfile) error
	DeleteAgent(ctx context.Context, slug string) error
	UpsertAgentBySlug(ctx context.Context, a *store.AgentProfile) error
	EnsureSessionAgent(ctx context.Context, sessionID, agentID, mode string, isPrimary bool) error
	SetSessionAgentMode(ctx context.Context, sessionID, agentID, mode string) error
	DeleteSessionAgent(ctx context.Context, sessionID, agentID string) error
	AssignSkillToAgent(ctx context.Context, agentID, skillID, config string) error
	RemoveSkillFromAgent(ctx context.Context, agentID, skillID string) error
	AddAgentProject(ctx context.Context, agentID, projectID string) error
	RemoveAgentProject(ctx context.Context, agentID, projectID string) error
}

// ToolStore provides access to MCP server configs and the catalog.
type ToolStore interface {
	ListMCPServers(ctx context.Context) ([]store.MCPServerConfig, error)
	GetMCPServer(ctx context.Context, name string) (*store.MCPServerConfig, error)
	CreateMCPServer(ctx context.Context, cfg *store.MCPServerConfig) error
	UpdateMCPServer(ctx context.Context, cfg *store.MCPServerConfig) error
	DeleteMCPServer(ctx context.Context, name string) error
	ListCatalogSources(ctx context.Context) ([]store.CatalogSource, error)
	GetCatalogSource(ctx context.Context, id string) (*store.CatalogSource, error)
	CreateCatalogSource(ctx context.Context, name, url, sourceType string, priority int) (*store.CatalogSource, error)
	UpdateCatalogSource(ctx context.Context, id, name, url string, enabled bool, priority int) error
	SetCatalogSourcePublicKey(ctx context.Context, id, publicKey string) error
	DeleteCatalogSource(ctx context.Context, id string) error
}

// UsageStore provides access to token usage, execution metrics, and event logs.
type UsageStore interface {
	RecordUsage(ctx context.Context, sessionID, messageID, model string, inputTokens, outputTokens, toolInputTokens, cacheCreationTokens, cacheReadTokens int) error
	GetSessionUsage(ctx context.Context, sessionID string) (*store.SessionUsageSummary, error)
	GetUsageSummary(ctx context.Context) (*store.UsageSummary, error)
	RecordExecutionMetrics(ctx context.Context, m *store.ExecutionMetrics) error
	GetSessionExecutionMetrics(ctx context.Context, sessionID string) ([]store.ExecutionMetrics, error)
	GetRecentExecutionMetrics(ctx context.Context, limit int) ([]store.ExecutionMetrics, error)
	GetUtilityCallSummary(ctx context.Context) ([]store.UtilityCallSummary, error)
	GetUtilityCallLog(ctx context.Context, limit int) ([]store.ExecutionMetrics, error)
	LogEvent(ctx context.Context, sessionID, eventType, category, detail, metadata string)
	ListEvents(ctx context.Context, category string, limit int) ([]store.EventLog, error)
	CountSessionToolCalls(ctx context.Context, sessionID string) int
}

// SettingsStore provides access to user settings and plugin settings.
type SettingsStore interface {
	GetUserSettings(ctx context.Context) (*store.UserSettings, error)
	UpdateUserSettings(ctx context.Context, us *store.UserSettings) error
	GetPluginSettings(ctx context.Context, pluginID string) (*store.PluginSettings, error)
	UpsertPluginSettings(ctx context.Context, pluginID string, settings map[string]any) error
	UpsertPluginSchema(ctx context.Context, pluginID string, schema []store.ConfigField) error
	ListPluginSettings(ctx context.Context) ([]*store.PluginSettings, error)
	UpdatePluginIcon(ctx context.Context, pluginID, icon string) error
	GetPluginSettingValue(ctx context.Context, pluginID, key string) (string, error)
}

// ProjectStore provides access to projects. Formerly WorkspaceStore —
// renamed when the in-app `workspaces` table (and its nesting of projects
// under a workspace_id) was retired in full (Phase 0 item 20,
// TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md).
type ProjectStore interface {
	ListProjects(ctx context.Context) ([]store.Project, error)
	GetProject(ctx context.Context, id string) (*store.Project, error)
	CreateProject(ctx context.Context, p *store.Project) error
	UpdateProject(ctx context.Context, p *store.Project) error
	DeleteProject(ctx context.Context, id string) error
}

// BookmarkStore provides access to message bookmarks.
type BookmarkStore interface {
	ListBookmarks(ctx context.Context, sessionID string) ([]store.Bookmark, error)
	GetBookmark(ctx context.Context, id string) (*store.Bookmark, error)
	GetBookmarkByMessage(ctx context.Context, messageID string) (*store.Bookmark, error)
	CreateBookmark(ctx context.Context, b *store.Bookmark) error
	DeleteBookmark(ctx context.Context, id string) error
	UpdateBookmarkNote(ctx context.Context, id, note string) error
}

// ArtifactStore provides access to session artifacts.
type ArtifactStore interface {
	ListArtifacts(ctx context.Context, sessionID string) ([]store.Artifact, error)
	ListArtifactsByOrigin(ctx context.Context, sessionID, origin string) ([]store.Artifact, error)
	// ListArtifactsByProject returns artifacts whose owning session belongs
	// to the given project. F4 (CW-20260429-0004): backs the right-rail
	// "This Project" inherited-artifacts section. excludeSessionID, when
	// non-empty, excludes artifacts owned by that session from the result.
	ListArtifactsByProject(ctx context.Context, projectID, excludeSessionID string) ([]store.Artifact, error)
	CreateArtifact(ctx context.Context, a *store.Artifact) error
	GetArtifact(ctx context.Context, id string) (*store.Artifact, error)
}

// SkillStore provides CRUD access to skills (independent of agent bindings).
type SkillStore interface {
	ListSkills(ctx context.Context) ([]store.Skill, error)
	GetSkill(ctx context.Context, id string) (*store.Skill, error)
	GetSkillBySlug(ctx context.Context, slug string) (*store.Skill, error)
	CreateSkill(ctx context.Context, sk *store.Skill) error
	UpdateSkill(ctx context.Context, sk *store.Skill) error
	DeleteSkill(ctx context.Context, id string) error
}

// TodoStore provides CRUD access to internal todos.
type TodoStore interface {
	CreateTodo(ctx context.Context, t *store.Todo) error
	GetTodo(ctx context.Context, id string) (*store.Todo, error)
	ListTodos(ctx context.Context, f store.TodoFilter) ([]store.Todo, error)
	UpdateTodo(ctx context.Context, t *store.Todo) error
	UpdateTodoScope(ctx context.Context, id, scope, scopeID, projectID string) error
	DeleteTodo(ctx context.Context, id string) error
	ListTodoChildren(ctx context.Context, parentID string) ([]store.Todo, error)
}

// PlanStore provides CRUD access to internal plans.
type PlanStore interface {
	CreatePlan(ctx context.Context, p *store.Plan) error
	GetPlan(ctx context.Context, id string) (*store.Plan, error)
	ListPlans(ctx context.Context, f store.PlanFilter) ([]store.Plan, error)
	UpdatePlan(ctx context.Context, p *store.Plan) error
	UpdatePlanStep(ctx context.Context, planID, stepID string, updates store.PlanStep) error
	DeletePlan(ctx context.Context, id string) error
}

// DefaultResolver is the minimal interface exposed by store.Store for
// "what provider+model should this call use?" resolution. Service
// helpers that need only the resolver (e.g. BuildSummarizer) accept this
// interface instead of the full Store so they remain easy to fake in
// tests. CW-20260526-0003.
type DefaultResolver interface {
	ResolveProviderAndModel(ctx context.Context, explicitProvider, explicitModel string) (string, string, error)
	DefaultModelForProvider(ctx context.Context, providerType string) (string, error)
}

// ProviderStore provides access to provider and model configuration.
type ProviderStore interface {
	ListProviders(ctx context.Context) ([]store.ProviderConfig, error)
	GetProvider(ctx context.Context, id string) (*store.ProviderConfig, error)
	ListModels(ctx context.Context) ([]store.Model, error)
	UpdateProvider(ctx context.Context, id string, u store.ProviderUpdate) error

	// DefaultModelForProvider returns providers.default_model for the
	// given provider_type, or an ErrNoDefaultModel-wrapped error when
	// no row supplies one (CW-20260526-0003).
	DefaultModelForProvider(ctx context.Context, providerType string) (string, error)

	// ResolveProviderAndModel walks explicit args →
	// user_settings.default_{provider,model} → providers.default_model
	// for the resolved provider_type, returning ErrNoDefaultModel when
	// the chain is dry (CW-20260526-0003). This is the SSOT for
	// "what provider+model should this call use?"
	ResolveProviderAndModel(ctx context.Context, explicitProvider, explicitModel string) (string, string, error)
}

// HandoffStashStore covers session handoff stash persistence + retrieval
// (P7, CW-20260420-0024 — write; Glass-4, CW-20260502-0015 — read).
type HandoffStashStore interface {
	UpsertHandoffStash(ctx context.Context, stash store.HandoffStash) error
	GetHandoffStash(ctx context.Context, sessionID, stashID string) (store.HandoffStash, error)
	GetLatestStashForSession(ctx context.Context, sessionID string) (store.HandoffStash, error)
}

// ReminderStore covers reminder persistence (J11, CW-20260426-0009; D1, CW-20260428-0014).
type ReminderStore interface {
	CreateReminder(ctx context.Context, r store.Reminder) error
	GetReminder(ctx context.Context, id string) (store.Reminder, error)
	ListUnfiredReminders(ctx context.Context, sessionID string) ([]store.Reminder, error)
	MarkReminderFired(ctx context.Context, id string) error
	UpdateReminderScope(ctx context.Context, id, scope, projectID string) error
	DeleteReminder(ctx context.Context, id string) error
}

// PinnedContentStore covers pinned content persistence (J11, CW-20260426-0009; D1, CW-20260428-0014).
type PinnedContentStore interface {
	CreatePinnedContent(ctx context.Context, p store.PinnedContent) error
	ListPinnedContent(ctx context.Context, sessionID string) ([]store.PinnedContent, error)
	DeletePinnedContent(ctx context.Context, id string) error
	UpdatePinScope(ctx context.Context, id, scope, projectID string) error
	ClearSessionPins(ctx context.Context, sessionID string) error
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
	CreateEnvelopeInstance(ctx context.Context, inst *store.EnvelopeInstance) error
	GetEnvelopeInstance(ctx context.Context, id string) (*store.EnvelopeInstance, error)
}

// SubagentRunsReader exposes the narrow query the chat loop needs to
// classify "is the current pause caused by a hung subagent" — used to
// suppress the FE-visible `[generation interrupted]` placeholder and
// `ErrorCodeInternal` event when a subagent dispatch is blocking the
// parent turn (CW-20260512-0002 subtodo (d)). Returns the active row's
// id + role + child_session_id for structured logging, or empty values
// when there is no active subagent for the parent session.
type SubagentRunsReader interface {
	ActiveSubagentRunForParent(ctx context.Context, parentSessionID string) (id, role, childSessionID string, ok bool, err error)
}

type Store interface {
	SessionReader
	SessionWriter
	AgentReader
	AgentWriter
	ToolStore
	UsageStore
	SettingsStore
	ProjectStore
	BookmarkStore
	ArtifactStore
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
	AgentRuntimeProviderSessionID(ctx context.Context, id string) (string, error)

	// SetAgentRuntimeProviderSessionID overwrites (or clears with "") the
	// captured provider_session_id on a chat session's runtime row.
	// CW-20260525-0001 Slice 3 follow-up — used by stale-resume detection
	// to clear an expired id after a fast-exit-after-resume so the next
	// turn cold-boots without --resume.
	SetAgentRuntimeProviderSessionID(ctx context.Context, id, providerSessionID string) error

	// ListEnabledAgentContextResolvers returns an agent's enabled
	// cmd/http dynamic context resolvers (Phase 2 item 02,
	// TASKS/phase-2/02-port-forward-dynamic-resolver.md). Used by
	// chat_boot_drive.go's resolveAgentContextForBoot at launch time.
	ListEnabledAgentContextResolvers(ctx context.Context, agentID string) ([]store.AgentContextResolver, error)

	// ListAgentToolNames and ListAlwaysIncludedKnownTools (Phase 5 item 01,
	// TASKS/phase-5/01-build-assignment-api.md) back
	// chatServiceImpl.enforceExecutionRules' agent_tools-authoritative
	// execution-time re-check -- mirroring filterToolsByAgentTools' and
	// resolveAlwaysIncludedTools' selection-time reads (internal/service/
	// tool.go, TASKS/phase-4/05) so a grant made via the agent_tools API
	// isn't rejected one turn later purely because this separate re-check
	// didn't know about the new table. Deliberately added directly here
	// rather than to AgentReader/ToolStore above -- those narrower
	// interfaces are also used by toolServiceImpl/agentServiceImpl via
	// hand-rolled test doubles that have no reason to grow these two
	// agent_tools-specific methods; scoping the addition to the one
	// composite interface that actually needs them (Store, used by
	// chatServiceImpl) keeps the blast radius to this interface's own
	// fakes (chat_test.go's minimalStore).
	ListAgentToolNames(ctx context.Context, agentID string) ([]string, error)
	ListAlwaysIncludedKnownTools(ctx context.Context) ([]store.KnownTool, error)
}

// Compile-time verification that *store.Store satisfies the composite interface.
var _ Store = (*store.Store)(nil)
