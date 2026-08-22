package api

import (
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// Request types for API handlers.
// Organized by resource. Each type replaces an anonymous struct
// that was previously defined inline in a handler function.

// --- Sessions ---

type CreateSessionRequest struct {
	ProjectID string `json:"project_id"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	AgentID   string `json:"agent_id"`
}

// Phase 0 item 21 ("Cut Modes, in full") removed this file's ModeID field
// from ForkSessionRequest, plus SwitchSessionModeRequest, SetSessionModeRequest,
// SetSessionAutoSwitchRequest, and SessionAutoSwitchResponse — Session Mode
// and Legacy Agent Mode are both gone.
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

// --- Messages ---

type SendMessageRequest struct {
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	// CycleKind annotates durable-agent lifecycle turns. Empty/default is
	// "request"; monitor drivers use "tick"; notify-and-wake uses "wake".
	CycleKind string `json:"cycle_kind,omitempty"`
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
	// ParentDispatchAllowlist (CW-20260512-0107): JSON array of role
	// slugs this agent may dispatch via task_execute. Surfaced into
	// task_execute's per-call description by the Tool Broker Describe
	// hook. Default '[]' (no dispatch).
	ParentDispatchAllowlist string `json:"parent_dispatch_allowlist"`
	RoleTools               string `json:"role_tools"`
	RoleSkills              string `json:"role_skills"`
	ContextPolicy           string `json:"context_policy"`
	Durable                 bool   `json:"durable"`
	ActivationMode          string `json:"activation_mode"`
	Class                   string `json:"class"`
	DefaultState            string `json:"default_state"`
	// RoleID/ConsumerID/ModelID (Phase 5 item 01,
	// TASKS/phase-5/01-build-assignment-api.md) -- the composition-model
	// FKs architecture/01-agent-construction.md names (agents.role_id ->
	// roles(id), agents.consumer_id -> consumers(id), agents.model_id ->
	// models(id)). These are DB-only columns with zero frontmatter
	// representation at all -- the handler
	// writes them via store.UpdateAgentComposition, a direct-DB step
	// separate from AgentConfigService.Create's file-based write, not
	// through this struct's other fields' usual store.AgentProfile path.
	RoleID     string `json:"role_id"`
	ConsumerID string `json:"consumer_id"`
	ModelID    string `json:"model_id"`
	// Protocol/Transport (TASKS/agent-host-acp/11-nanite-per-agent-protocol-
	// transport-config.md) select which wire protocol/transport pairing
	// this agent's CLI process launches through -- same "DB-only, zero
	// frontmatter representation" shape as RoleID/ConsumerID/ModelID above,
	// written the same way (store.UpdateAgentACPConfig, a direct-DB step
	// separate from AgentConfigService.Create's file-based write). Empty
	// means "use this provider's existing native protocol" (the default,
	// preserving every pre-existing agent's behavior unchanged).
	Protocol  string `json:"protocol"`
	Transport string `json:"transport"`
}

type UpdateAgentRequest struct {
	Name            *string `json:"name"`
	Slug            *string `json:"slug"`
	Avatar          *string `json:"avatar"`
	SystemPrompt    *string `json:"system_prompt"`
	Description     *string `json:"description"`
	Modes           *string `json:"modes"`
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
	// CW-20260512-0107 — see CreateAgentRequest.
	ParentDispatchAllowlist *string `json:"parent_dispatch_allowlist"`
	RoleTools               *string `json:"role_tools"`
	RoleSkills              *string `json:"role_skills"`
	ContextPolicy           *string `json:"context_policy"`
	Durable                 *bool   `json:"durable"`
	ActivationMode          *string `json:"activation_mode"`
	Class                   *string `json:"class"`
	DefaultState            *string `json:"default_state"`
	// RoleID/ConsumerID/ModelID -- see CreateAgentRequest's doc comment.
	// Pointer semantics match every other field on this partial-update
	// struct: nil leaves the column untouched, a pointer to "" clears it
	// (nulls the FK), a pointer to a non-empty value sets/reassigns it.
	RoleID     *string `json:"role_id"`
	ConsumerID *string `json:"consumer_id"`
	ModelID    *string `json:"model_id"`
	// Protocol/Transport -- see CreateAgentRequest's doc comment. Pointer
	// semantics match RoleID/ConsumerID/ModelID immediately above: nil
	// leaves the column untouched, a pointer to "" clears it back to
	// "native protocol," a pointer to a non-empty value sets it.
	Protocol  *string `json:"protocol"`
	Transport *string `json:"transport"`
	// Revision is the optimistic-concurrency token the client loaded with the
	// agent (the managed file's content hash). When set, the update is
	// rejected with 409 if the on-disk file changed underneath. Empty skips
	// the guard (back-compat).
	Revision string `json:"revision"`
}

// AgentProfileView wraps a store.AgentProfile with the computed management
// metadata the GUI needs to decide editability and run the optimistic-
// concurrency guard. The embedded profile flattens into the same JSON shape
// existing consumers expect; the extra fields are additive.
type AgentProfileView struct {
	store.AgentProfile
	// ManageClass is one of: managed, internal, plugin, external.
	ManageClass string `json:"manage_class"`
	// Editable reports whether this agent can be edited/deleted in place.
	Editable bool `json:"editable"`
	// CopyToManaged reports whether a read-only agent can be forked into the
	// managed layer ("make editable").
	CopyToManaged bool `json:"copy_to_managed"`
	// Revision is the current optimistic-concurrency token (file hash).
	Revision string `json:"revision"`
	// Persisted reports whether a real agent_profiles DB row backs this
	// profile. False means the source file was discovered and parsed but
	// failed to ingest into the database (see AutoIngestAgents / server
	// startup logs) — the agent shows up here but isn't actually usable
	// (e.g. durable-agent apply will fail to resolve it). CW-20260815-0009.
	Persisted bool `json:"persisted"`
}

type AddSessionAgentRequest struct {
	AgentID string `json:"agent_id"`
	Role    string `json:"role"`
}

type AddAgentProjectRequest struct {
	ProjectID string `json:"project_id"`
}

type AgentKnownToolUpsertRequest struct {
	AgentID    string `json:"agent_id"`
	ToolName   string `json:"tool_name"`
	Pinned     bool   `json:"pinned"`
	SortOrder  int64  `json:"sort_order"`
	TTLSeconds int64  `json:"ttl_seconds"`
	Reason     string `json:"reason"`
}

type AgentKnownSkillUpsertRequest struct {
	AgentID    string `json:"agent_id"`
	SkillName  string `json:"skill_name"`
	Pinned     bool   `json:"pinned"`
	TTLSeconds int64  `json:"ttl_seconds"`
	Reason     string `json:"reason"`
}

type AgentProcedureUpsertRequest struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Body    string `json:"body"`
	Scope   string `json:"scope"`
}

type AgentKnowledgeSeedUpsertRequest struct {
	AgentID   string   `json:"agent_id"`
	SeedKey   string   `json:"seed_key"`
	Namespace string   `json:"namespace"`
	Body      string   `json:"body"`
	TagsJSON  string   `json:"tags_json"`
	Tags      []string `json:"tags"`
}

type AgentBuilderProfileInput struct {
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	Slug                    string `json:"slug"`
	Avatar                  string `json:"avatar"`
	Icon                    string `json:"icon"`
	SystemPrompt            string `json:"system_prompt"`
	Description             string `json:"description"`
	DefaultModel            string `json:"default_model"`
	MCPServers              string `json:"mcp_servers"`
	ToolPermissions         string `json:"tool_permissions"`
	Settings                string `json:"settings"`
	Tools                   string `json:"tools"`
	Directories             string `json:"directories"`
	Constraints             string `json:"constraints"`
	Tags                    string `json:"tags"`
	Status                  string `json:"status"`
	Source                  string `json:"source"`
	SourceRef               string `json:"source_ref"`
	ParentDispatchAllowlist string `json:"parent_dispatch_allowlist"`
	RoleTools               string `json:"role_tools"`
	RoleSkills              string `json:"role_skills"`
	ContextPolicy           string `json:"context_policy"`
	ActivationMode          string `json:"activation_mode"`
	Class                   string `json:"class"`
	DefaultState            string `json:"default_state"`
	CanExecute              *bool  `json:"can_execute,omitempty"`
	Durable                 *bool  `json:"durable,omitempty"`
}

type AgentBuilderCapabilitiesInput struct {
	AssignedSkillIDs   []string                          `json:"assigned_skill_ids"`
	AssignedSkillSlugs []string                          `json:"assigned_skill_slugs"`
	KnownTools         []AgentKnownToolUpsertRequest     `json:"known_tools"`
	KnownSkills        []AgentKnownSkillUpsertRequest    `json:"known_skills"`
	Procedures         []AgentProcedureUpsertRequest     `json:"procedures"`
	KnowledgeSeeds     []AgentKnowledgeSeedUpsertRequest `json:"knowledge_seeds"`
	ReflexSuggestions  []string                          `json:"reflex_suggestions"`
}

type AgentBuilderDurableInstanceInput struct {
	Create         bool              `json:"create"`
	RecipeID       string            `json:"recipe_id"`
	LifecycleClass string            `json:"lifecycle_class"`
	Provider       string            `json:"provider"`
	Model          string            `json:"model"`
	RuntimeKind    string            `json:"runtime_kind"`
	WorkRoot       string            `json:"work_root"`
	ProjectID      string            `json:"project_id"`
	Start          bool              `json:"start"`
	Metadata       map[string]string `json:"metadata"`
}

type AgentBuilderOperatorNotificationInput struct {
	TargetKind   string `json:"target_kind"`
	TargetID     string `json:"target_id"`
	IncludeLinks bool   `json:"include_links"`
}

type AgentBuilderCapabilityOperation struct {
	Area   string `json:"area"`
	Action string `json:"action"`
	Target string `json:"target"`
	Count  int    `json:"count"`
	Detail string `json:"detail,omitempty"`
}

type AgentBuilderLaunchPlanPreview struct {
	LifecycleClass     string                         `json:"lifecycle_class"`
	SessionPolicy      string                         `json:"session_policy"`
	AttachmentRelation string                         `json:"attachment_relation"`
	Provider           string                         `json:"provider"`
	Model              string                         `json:"model"`
	RuntimeKind        string                         `json:"runtime_kind"`
	WorkRoot           string                         `json:"work_root"`
	WouldCreateSession bool                           `json:"would_create_session"`
	WakePayload        DurableAgentWakePayloadRequest `json:"wake_payload"`
}

type AgentBuilderNotificationResource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type AgentBuilderDeepLink struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Label string `json:"label"`
}

type AgentBuilderReadyNotificationPreview struct {
	TargetKind      string                           `json:"target_kind"`
	TargetID        string                           `json:"target_id"`
	Profile         AgentBuilderNotificationResource `json:"profile"`
	DurableInstance AgentBuilderNotificationResource `json:"durable_instance"`
	Session         AgentBuilderNotificationResource `json:"session"`
	Links           []AgentBuilderDeepLink           `json:"links"`
	Warnings        []string                         `json:"warnings"`
	FollowUps       []string                         `json:"followups"`
}

type AgentBuilderDryRunRequest struct {
	SchemaVersion        int                                   `json:"schema_version"`
	Mode                 string                                `json:"mode"`
	Profile              AgentBuilderProfileInput              `json:"profile"`
	Capabilities         AgentBuilderCapabilitiesInput         `json:"capabilities"`
	DurableInstance      AgentBuilderDurableInstanceInput      `json:"durable_instance"`
	OperatorNotification AgentBuilderOperatorNotificationInput `json:"operator_notification"`
}

type AgentBuilderDryRunResponse struct {
	SchemaVersion            int                                  `json:"schema_version"`
	Valid                    bool                                 `json:"valid"`
	Errors                   []string                             `json:"errors"`
	Warnings                 []string                             `json:"warnings"`
	UnsupportedFields        []string                             `json:"unsupported_fields"`
	NormalizedProfilePayload AgentBuilderProfileInput             `json:"normalized_profile_payload"`
	CapabilityOperations     []AgentBuilderCapabilityOperation    `json:"capability_operations"`
	DurableRecipePlan        *service.DurableAgentRecipePlan      `json:"durable_recipe_plan,omitempty"`
	LaunchPlanPreview        *AgentBuilderLaunchPlanPreview       `json:"launch_plan_preview,omitempty"`
	NotificationPreview      AgentBuilderReadyNotificationPreview `json:"notification_preview"`
}

type AgentBuilderDraftRequest struct {
	SchemaVersion           int    `json:"schema_version"`
	IntakeText              string `json:"intake_text"`
	Name                    string `json:"name"`
	Slug                    string `json:"slug"`
	Description             string `json:"description"`
	ProjectContext          string `json:"project_context"`
	WorkRoot                string `json:"work_root"`
	PreferredProvider       string `json:"preferred_provider"`
	PreferredModel          string `json:"preferred_model"`
	PreferredRuntimeKind    string `json:"preferred_runtime_kind"`
	RequestedLifecycleClass string `json:"requested_lifecycle_class"`
}

type AgentBuilderDraftEnvelope struct {
	Mode                 string                                `json:"mode"`
	Profile              AgentBuilderProfileInput              `json:"profile"`
	Capabilities         AgentBuilderCapabilitiesInput         `json:"capabilities"`
	DurableInstance      AgentBuilderDurableInstanceInput      `json:"durable_instance"`
	OperatorNotification AgentBuilderOperatorNotificationInput `json:"operator_notification"`
}

type AgentBuilderDraftResponse struct {
	SchemaVersion       int                       `json:"schema_version"`
	Draft               AgentBuilderDraftEnvelope `json:"draft"`
	Questions           []string                  `json:"questions"`
	Warnings            []string                  `json:"warnings"`
	UnsupportedRequests []string                  `json:"unsupported_requests"`
	Confidence          float64                   `json:"confidence"`
}

type AgentBuilderReviewRequest struct {
	SchemaVersion        int                       `json:"schema_version"`
	CurrentDraft         AgentBuilderDraftEnvelope `json:"current_draft"`
	PreviousBuilderNotes []string                  `json:"previous_builder_notes"`
}

type AgentBuilderPatchOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value,omitempty"`
	Note  string `json:"note,omitempty"`
}

type AgentBuilderReviewResponse struct {
	SchemaVersion            int                          `json:"schema_version"`
	Accepted                 bool                         `json:"accepted"`
	Questions                []string                     `json:"questions"`
	Warnings                 []string                     `json:"warnings"`
	SuggestedPatchOperations []AgentBuilderPatchOperation `json:"suggested_patch_operations"`
	MaxRoundsRecommended     int                          `json:"max_rounds_recommended"`
}

type CreateDurableAgentRequest struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Slug             string `json:"slug"`
	ProfileID        string `json:"profile_id"`
	LifecycleClass   string `json:"lifecycle_class"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	RuntimeKind      string `json:"runtime_kind"`
	LaunchSourceType string `json:"launch_source_type"`
	LaunchSourceID   string `json:"launch_source_id"`
	WorkRoot         string `json:"work_root"`
	MetadataJSON     string `json:"metadata_json"`
}

type UpdateDurableAgentRequest struct {
	Name         *string `json:"name"`
	Slug         *string `json:"slug"`
	WorkRoot     *string `json:"work_root"`
	MetadataJSON *string `json:"metadata_json"`
}

type AttachDurableAgentSessionRequest struct {
	SessionID string `json:"session_id"`
	Relation  string `json:"relation"`
}

type DurableAgentWakePayloadRequest struct {
	Reason   string            `json:"reason"`
	Prompt   string            `json:"prompt,omitempty"`
	Facts    map[string]string `json:"facts,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type DurableAgentStartRequest struct {
	ProjectID   string                         `json:"project_id"`
	WakePayload DurableAgentWakePayloadRequest `json:"wake_payload"`
}

type DurableAgentRecipeRequest struct {
	Name        string                         `json:"name"`
	Slug        string                         `json:"slug"`
	ProfileID   string                         `json:"profile_id"`
	Provider    string                         `json:"provider"`
	Model       string                         `json:"model"`
	RuntimeKind string                         `json:"runtime_kind"`
	WorkRoot    string                         `json:"work_root"`
	ProjectID   string                         `json:"project_id"`
	WakePayload DurableAgentWakePayloadRequest `json:"wake_payload"`
	Metadata    map[string]string              `json:"metadata"`
	Start       bool                           `json:"start"`
}

// --- Roles ---
//
// Phase 1 item 01 (TASKS/phase-1/01-add-roles-table-and-cascade-resolution.md).
// See internal/store/roles.go for the Role struct these requests map onto.

type CreateRoleRequest struct {
	Slug               string `json:"slug"`
	Name               string `json:"name"`
	SystemPrompt       string `json:"system_prompt"`
	DefaultClass       string `json:"default_class"`
	DefaultModel       string `json:"default_model"`
	DefaultProvider    string `json:"default_provider"`
	DefaultTools       string `json:"default_tools"`
	DefaultSkills      string `json:"default_skills"`
	DefaultPermissions string `json:"default_permissions"`
}

type UpdateRoleRequest struct {
	Slug               *string `json:"slug"`
	Name               *string `json:"name"`
	SystemPrompt       *string `json:"system_prompt"`
	DefaultClass       *string `json:"default_class"`
	DefaultModel       *string `json:"default_model"`
	DefaultProvider    *string `json:"default_provider"`
	DefaultTools       *string `json:"default_tools"`
	DefaultSkills      *string `json:"default_skills"`
	DefaultPermissions *string `json:"default_permissions"`
}

// --- Consumers ---
//
// Phase 5 item 01 (TASKS/phase-5/01-build-assignment-api.md). Phase 1 item
// 03 (TASKS/phase-1/03-add-consumers-table.md) built the store-layer CRUD
// (internal/store/consumers.go) and deliberately deferred the REST layer
// here. See that file's store.Consumer struct these requests map onto, and
// GLOSSARY.md's Consumer entry.

type CreateConsumerRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type UpdateConsumerRequest struct {
	Slug *string `json:"slug"`
	Name *string `json:"name"`
}

// --- Agent tools (grant/revoke) ---
//
// Phase 5 item 01 (TASKS/phase-5/01-build-assignment-api.md). Phase 1 item
// 04 (TASKS/phase-1/04-add-known-tools-and-agent-tools-fk.md) built the
// store-layer grant/revoke functions (internal/store/agent_tools.go) and a
// list-only REST endpoint; this is the write side.

// GrantAgentToolRequest grants a known_tools row (by ID) to the agent in
// the URL path. GrantedVia is an optional provenance tag ("explicit" |
// "role_seed" | "legacy_backfill" -- see agent_tools.granted_via's doc
// comment); empty defaults to "explicit", matching store.GrantAgentTool's
// own default.
type GrantAgentToolRequest struct {
	ToolID     string `json:"tool_id"`
	GrantedVia string `json:"granted_via"`
}

// --- Agent context resolvers ---
//
// Phase 2 item 02 (TASKS/phase-2/02-port-forward-dynamic-resolver.md).
// See internal/store/agent_context_resolvers.go for the
// AgentContextResolver struct these requests map onto.

type CreateAgentContextResolverRequest struct {
	SlotName       string `json:"slot_name"`
	Kind           string `json:"kind"`
	Run            string `json:"run"`
	CWD            string `json:"cwd"`
	Timeout        string `json:"timeout"`
	URL            string `json:"url"`
	HeadersJSON    string `json:"headers_json"`
	ResponseFormat string `json:"response_format"`
	JSONPath       string `json:"json_path"`
	Enabled        *bool  `json:"enabled"`
}

type UpdateAgentContextResolverRequest struct {
	SlotName       *string `json:"slot_name"`
	Kind           *string `json:"kind"`
	Run            *string `json:"run"`
	CWD            *string `json:"cwd"`
	Timeout        *string `json:"timeout"`
	URL            *string `json:"url"`
	HeadersJSON    *string `json:"headers_json"`
	ResponseFormat *string `json:"response_format"`
	JSONPath       *string `json:"json_path"`
	Enabled        *bool   `json:"enabled"`
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

// --- Projects ---
// Phase 0 item 20 (retire workspaces): the in-app `workspaces` table (and
// CreateWorkspaceRequest/UpdateWorkspaceRequest, its REST DTOs) is retired
// in full. `projects` is flat now — no more workspace nesting.

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

// --- Skills ---
//
// TASKS/skills/02: request shapes follow store.Skill's redesigned,
// index-only shape (docs/engineering/architecture/20-skills.md's "The
// model: DB is an index, a vendored store is content") — ToolBindings and
// Settings are dropped (no field on store.Skill to map them onto anymore).
// This is still the bare admin-CRUD surface over the index row, not the
// real authored-package install/sync path (tasks 04/05 own that) or the
// remaining REST surface (list/grants/preview/uninstall, task 12) — the
// full redesign of this handler's contract is explicitly later work.

type CreateSkillRequest struct {
	Name                 string `json:"name"`
	Slug                 string `json:"slug"`
	Description          string `json:"description"`
	Category             string `json:"category"`
	Icon                 string `json:"icon"`
	InputSchema          string `json:"input_schema"`
	SourceTier           string `json:"source_tier"`
	ContentHash          string `json:"content_hash"`
	DeclaredDependencies string `json:"declared_dependencies"`
	Enabled              *bool  `json:"enabled"`
}

type UpdateSkillRequest struct {
	Name                 *string `json:"name"`
	Slug                 *string `json:"slug"`
	Description          *string `json:"description"`
	Category             *string `json:"category"`
	Icon                 *string `json:"icon"`
	InputSchema          *string `json:"input_schema"`
	SourceTier           *string `json:"source_tier"`
	ContentHash          *string `json:"content_hash"`
	DeclaredDependencies *string `json:"declared_dependencies"`
	Enabled              *bool   `json:"enabled"`
}

type AssignAgentSkillRequest struct {
	SkillID string `json:"skill_id"`
	Config  string `json:"config"`
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
