import type { ResponseV1 } from "@/lib/envelope-response";

export interface Workspace {
  id: string;
  name: string;
  description: string;
  icon: string;
  sort_order: number;
}

export interface Project {
  id: string;
  workspace_id: string;
  name: string;
  description: string;
  repo_path: string;
  settings: string;
  sort_order: number;
  created_at: string;
  updated_at: string;
}

export interface Session {
  id: string;
  short_code: string;
  title: string;
  custom_name: string;
  workspace_id: string;
  project_id: string;
  context_type: string | null;
  context_id: string | null;
  provider: string;
  model: string;
  status: string;
  is_pinned: boolean;
  sort_order: number;
  message_count: number;
  tags: string;
  last_activity: string;
  created_at: string;
  metadata?: string;
  halted_at?: string | null;
  halted_reason?: string | null;
  runtime_state?: string | null;
  parent_session_id?: string | null;
  root_session_id?: string | null;
  relation?: string | null;
  depth?: number | null;
  // B1 (CW-20260428-0009): session-level mode pointer.
  // Null = fall back to agent-assigned legacy AgentMode.
  current_mode_id?: string | null;
  // F2 (CW-20260429-0002): per-session auto-mode-switch override. Tri-state
  // — null/undefined inherits user_settings.mode_auto_switch_pref; true =
  // force ON for this session (does NOT bypass first-use prompt); false =
  // force OFF (suppress all auto-switches even when user pref permits).
  auto_switch_override?: boolean | null;
}

export const DURABLE_AGENT_LIFECYCLE_CLASSES = [
  "advisor",
  "process",
  "template",
  "harness",
] as const;
export type DurableAgentLifecycleClass =
  (typeof DURABLE_AGENT_LIFECYCLE_CLASSES)[number];

export const DURABLE_AGENT_STATUSES = [
  "sleeping",
  "starting",
  "active",
  "paused",
  "stopped",
  "start_requested",
  "stop_requested",
  "resume_requested",
  "failed",
  "archived",
] as const;
export type DurableAgentStatus = (typeof DURABLE_AGENT_STATUSES)[number];

export const DURABLE_AGENT_LAUNCH_SOURCES = [
  "api_chat",
  "cli_harness",
  "boot_profile",
  "durable_advisor",
  "process_tick",
  "task_template_run",
] as const;
export type DurableAgentLaunchSource =
  (typeof DURABLE_AGENT_LAUNCH_SOURCES)[number];

export const DURABLE_AGENT_ATTACHMENT_RELATIONS = [
  "primary",
  "wake",
  "run",
  "harness",
  "owned",
  "attached",
  "spawned",
] as const;
export type DurableAgentAttachmentRelation =
  (typeof DURABLE_AGENT_ATTACHMENT_RELATIONS)[number];

export const RUNTIME_KINDS = [
  "api",
  "streaming-stdio",
  "subprocess",
  "jsonrpc-stdio",
  "serve-http",
  "pty",
  "pty-debug",
] as const;
export type RuntimeKind = (typeof RUNTIME_KINDS)[number];

export const DURABLE_AGENT_RECIPE_KINDS = [
  "project_advisor",
  "managed_cli_harness",
  "process_monitor",
  "template_worker",
] as const;
export type DurableAgentRecipeKind =
  (typeof DURABLE_AGENT_RECIPE_KINDS)[number];

export const DURABLE_AGENT_RECIPE_INPUT_TYPES = [
  "string",
  "textarea",
  "boolean",
  "select",
  "path",
  "profile",
  "provider",
  "model",
  "runtime_kind",
] as const;
export type DurableAgentRecipeInputType =
  (typeof DURABLE_AGENT_RECIPE_INPUT_TYPES)[number];

export const DURABLE_AGENT_WAKE_REASONS = [
  "manual",
  "lifecycle_start",
  "lifecycle_resume",
  "process_tick",
  "scheduled_wake",
  "external_message",
] as const;
export type DurableAgentWakeReason =
  (typeof DURABLE_AGENT_WAKE_REASONS)[number];

export const DURABLE_AGENT_SESSION_POLICIES = [
  "reuse_latest_or_create",
  "fresh_per_wake",
  "fresh_one_shot",
  "reuse_managed",
] as const;
export type DurableAgentSessionPolicy =
  (typeof DURABLE_AGENT_SESSION_POLICIES)[number];

export const DURABLE_AGENT_EVENT_TYPES = [
  "created",
  "updated",
  "archived",
  "session_attached",
  "start_requested",
  "start_succeeded",
  "start_failed",
  "resume_requested",
  "resume_succeeded",
  "resume_failed",
  "pause_requested",
  "pause_succeeded",
  "stop_requested",
  "runtime_stop_succeeded",
  "stop_succeeded",
  "stop_failed",
  "wake_requested",
  "wake_started",
  "wake_skipped",
  "wake_failed",
  "wake_completed",
] as const;
export type DurableAgentEventType = (typeof DURABLE_AGENT_EVENT_TYPES)[number];

export const DURABLE_AGENT_EVENT_SOURCES = ["api", "runtime"] as const;
export type DurableAgentEventSource =
  (typeof DURABLE_AGENT_EVENT_SOURCES)[number];

export type SessionBootSource =
  | "api_default"
  | "legacy_cli"
  | "boot_profile"
  | "durable_agent"
  | "unknown";

export type ImmutableStartField =
  | "provider"
  | "model"
  | "runtime_kind"
  | "boot_profile"
  | "recipe"
  | "lifecycle_class"
  | "work_root";

export interface EnumOption<T extends string = string> {
  value: T;
  label: string;
  description?: string;
  legacy?: boolean;
}

export interface RuntimeKindOption extends EnumOption<RuntimeKind> {
  managed_automation: boolean;
  product_supported: boolean;
}

export interface BootProfileOption {
  id: string;
  label: string;
  provider: string;
  work_root?: string;
}

export interface WorkRootHint {
  id: string;
  label: string;
  path?: string;
  description?: string;
}

export interface MetaHarness {
  id: string;
  display_name: string;
  launch: string;
  ui_label: string;
  provider: string;
  provider_alias?: string;
  workdir: string;
  boot_mode?: string;
  args: string[];
  env: Record<string, string>;
  role?: string;
  project?: string;
  work_root?: string;
  tracking_root?: string;
  mcp_servers: string[];
  profile_path: string;
  launch_path: string;
}

export interface MetaHarnessInput {
  id?: string;
  display_name?: string;
  ui_label?: string;
  provider?: string;
  workdir?: string;
  boot_mode?: string;
  args?: string[];
  env?: Record<string, string>;
  role?: string;
  project?: string;
  work_root?: string;
  tracking_root?: string;
  mcp_servers?: string[];
}

export interface DurableAgentWakePayload {
  reason: DurableAgentWakeReason | string;
  prompt?: string;
  facts?: Record<string, string>;
  metadata?: Record<string, string>;
}

export interface DurableAgentInstance {
  id: string;
  name: string;
  slug: string;
  profile_id: string;
  lifecycle_class: DurableAgentLifecycleClass | string;
  provider: string;
  model: string;
  runtime_kind: RuntimeKind | string;
  launch_source_type: DurableAgentLaunchSource | string;
  launch_source_id: string;
  work_root: string;
  status: DurableAgentStatus | string;
  current_session_id: string;
  failure_reason: string;
  metadata_json: string;
  created_at: string;
  updated_at: string;
  archived_at?: string | null;
}

export interface CreateDurableAgentRequest {
  id?: string;
  name: string;
  slug: string;
  profile_id: string;
  lifecycle_class?: DurableAgentLifecycleClass | string;
  provider?: string;
  model?: string;
  runtime_kind?: RuntimeKind | string;
  launch_source_type?: DurableAgentLaunchSource | string;
  launch_source_id?: string;
  work_root?: string;
  metadata_json?: string;
}

export type UpdateDurableAgentRequest = Partial<
  Pick<DurableAgentInstance, "name" | "slug" | "work_root" | "metadata_json">
>;

export interface DurableAgentSessionAttachment {
  instance_id: string;
  session_id: string;
  relation: DurableAgentAttachmentRelation | string;
  attached_at: string;
  detached_at?: string | null;
}

export interface DurableAgentSessionAttachmentState
  extends DurableAgentSessionAttachment {
  session_status: string;
  provider: string;
  model: string;
  runtime_state: string;
  runtime_failure_reason?: string;
  halted_at?: string | null;
  halted_reason?: string | null;
}

export interface DurableAgentEvent {
  id: string;
  instance_id: string;
  event_type: DurableAgentEventType | string;
  status_before: DurableAgentStatus | string;
  status_after: DurableAgentStatus | string;
  session_id: string;
  source: DurableAgentEventSource | string;
  message: string;
  metadata_json: string;
  created_at: string;
}

export interface AttachDurableAgentSessionRequest {
  session_id: string;
  relation: DurableAgentAttachmentRelation | string;
}

export interface DurableAgentLaunchPlan {
  instance_id: string;
  lifecycle_class: DurableAgentLifecycleClass | string;
  session_policy: DurableAgentSessionPolicy | string;
  launch_source_type: DurableAgentLaunchSource | string;
  provider: string;
  model: string;
  runtime_kind: RuntimeKind | string;
  work_root: string;
  attachment_relation: DurableAgentAttachmentRelation | string;
  wake_payload: DurableAgentWakePayload;
}

export interface DurableAgentStartRequest {
  workspace_id?: string;
  project_id?: string;
  wake_payload?: DurableAgentWakePayload;
}

export interface DurableAgentLaunchResult {
  instance: DurableAgentInstance;
  policy: DurableAgentLaunchPlan;
  session?: Session;
  created_session: boolean;
  reused_session: boolean;
}

export interface AgentSchedule {
  id: string;
  agent_id: string;
  session_id: string;
  name: string;
  schedule_kind: string;
  schedule_spec: string;
  body: string;
  priority: number;
  status: string;
  expires_at: string;
  fired_count: number;
  last_fired_at: string;
  created_at: string;
  created_by: string;
}

export interface DurableAgentWakeDueItem {
  instance_id: string;
  instance_name: string;
  lifecycle_class: string;
  current_session_id: string;
  schedule: AgentSchedule;
  wake_reason: string;
  due: boolean;
  skip_reason?: string;
  workspace_id?: string;
  project_id?: string;
}

export interface DurableAgentWakeResult {
  instance_id: string;
  schedule_id?: string;
  wake_reason: string;
  skipped: boolean;
  skip_reason?: string;
  launch_result?: DurableAgentLaunchResult;
  failure_reason?: string;
}

export interface DurableAgentWakeRunResult {
  now: string;
  dry_run: boolean;
  results: DurableAgentWakeResult[];
}

export interface RecipeInjectionPlan {
  id: string;
  kind: string;
  target: string;
  description: string;
  secret: boolean;
}

export interface DurableAgentRecipeInputOption {
  value: string;
  label: string;
}

export interface DurableAgentRecipeInput {
  id: string;
  label: string;
  description?: string;
  help?: string;
  type: DurableAgentRecipeInputType | string;
  required?: boolean;
  default?: unknown;
  placeholder?: string;
  options?: DurableAgentRecipeInputOption[];
  secret?: boolean;
  maps_to?: string;
}

export interface DurableAgentRecipe {
  id: string;
  schema_version: number;
  kind: DurableAgentRecipeKind | string;
  name: string;
  description: string;
  lifecycle_class: DurableAgentLifecycleClass | string;
  profile_id?: string;
  profile_rule?: string;
  provider: string;
  model: string;
  runtime_kind: RuntimeKind | string;
  launch_source_type: DurableAgentLaunchSource | string;
  launch_source_id?: string;
  work_root?: string;
  wake_defaults: DurableAgentWakePayload;
  metadata?: Record<string, string>;
  tags?: string[];
  injections?: RecipeInjectionPlan[];
  inputs?: DurableAgentRecipeInput[];
}

export interface DurableAgentRecipeRequest {
  name?: string;
  slug?: string;
  profile_id?: string;
  provider?: string;
  model?: string;
  runtime_kind?: RuntimeKind | string;
  work_root?: string;
  workspace_id?: string;
  project_id?: string;
  wake_payload?: DurableAgentWakePayload;
  metadata?: Record<string, string>;
  start?: boolean;
}

export interface DurableAgentRecipePlan {
  recipe_id: string;
  recipe_schema_version: number;
  instance: DurableAgentInstance;
  launch_policy: DurableAgentLaunchPlan;
  wake_payload: DurableAgentWakePayload;
  session_policy: DurableAgentSessionPolicy | string;
  would_create_session: boolean;
  would_reuse_session: boolean;
  missing_requirements?: string[];
  unsupported?: string[];
  injections?: RecipeInjectionPlan[];
  ready: boolean;
}

export interface DurableAgentRecipeApplyResult {
  plan: DurableAgentRecipePlan;
  instance: DurableAgentInstance;
  launch_result?: DurableAgentLaunchResult;
}

export interface StartSurfaceCapabilitiesResponse {
  schema_version: number;
  lifecycle_classes: EnumOption<DurableAgentLifecycleClass>[];
  durable_statuses: EnumOption<DurableAgentStatus>[];
  launch_sources: EnumOption<DurableAgentLaunchSource>[];
  attachment_relations: EnumOption<DurableAgentAttachmentRelation>[];
  runtime_kinds: RuntimeKindOption[];
  recipe_kinds: EnumOption<DurableAgentRecipeKind>[];
  wake_reasons: EnumOption<DurableAgentWakeReason>[];
  session_policies: EnumOption<DurableAgentSessionPolicy>[];
  recipes: DurableAgentRecipe[];
  durable_agents: DurableAgentInstance[];
  profiles: AgentProfile[];
  providers: ProviderConfig[];
  models: ModelRecord[];
  boot_profiles: BootProfileOption[];
  work_root_hints: WorkRootHint[];
}

export interface SessionRuntimeDetail {
  state: "none" | "starting" | "running" | "stopped" | "failed" | string;
  runtime_id?: string;
  runtime_kind?: RuntimeKind | string;
  provider?: string;
  mode?: string;
  pid?: number;
  boot_dir?: string;
  workspace_dir?: string;
  provider_session_id?: string;
  failure_reason?: string;
  started_at?: string;
  updated_at?: string;
}

export interface CheckpointDetail {
  status: string;
}

export interface SessionHaltDetail {
  is_halted: boolean;
  halted_at?: string;
  halted_reason?: string;
}

export interface SessionDetailsResponse {
  session: Session;
  mode?: Mode | null;
  primary_agent?: AgentProfile | null;
  durable_attachments: DurableAgentSessionAttachmentState[];
  current_durable_agent?: DurableAgentInstance | null;
  activity_state: string;
  last_activity_at?: string;
  last_useful_activity_at?: string;
  halt: SessionHaltDetail;
  usage?: SessionUsageSummary | null;
  recent_durable_events: DurableAgentEvent[];
  runtime: SessionRuntimeDetail;
  boot_source: SessionBootSource | string;
  immutable_start_fields: (ImmutableStartField | string)[];
  checkpoint: CheckpointDetail;
}

export interface HarnessPermissionSupport {
  support_level: string;
  approval_response_route?: string;
  notes?: string[];
}

export interface HarnessRouteHints {
  capabilities: string;
  sessions: string;
  session_events: string;
  session_cancel: string;
  session_approvals: string;
  durable_agents: string;
  durable_agent_start: string;
  durable_agent_wake: string;
}

export interface HarnessInitializeResponse {
  schema_version: number;
  protocol_version: string;
  route_prefix: string;
  app: {
    id: string;
    name: string;
    version: string;
  };
  operations: string[];
  stream_transports: string[];
  supported_event_types: string[];
  permission_requests: HarnessPermissionSupport;
  route_hints: HarnessRouteHints;
  unsupported: string[];
}

export interface HarnessFieldSupport {
  supported: string[];
  unsupported: string[];
}

export interface HarnessCapabilitiesResponse {
  schema_version: number;
  protocol_version: string;
  route_prefix: string;
  app: {
    id: string;
    name: string;
    version: string;
  };
  operations: string[];
  stream_transports: string[];
  runtime_kinds: RuntimeKindOption[];
  lifecycle_classes: EnumOption<DurableAgentLifecycleClass>[];
  durable_statuses: EnumOption<DurableAgentStatus>[];
  attachment_relations: EnumOption<DurableAgentAttachmentRelation>[];
  wake_reasons: EnumOption<DurableAgentWakeReason>[];
  session_activity_states: EnumOption[];
  supported_event_types: EnumOption[];
  session_create_fields: HarnessFieldSupport;
  turn_send_fields: HarnessFieldSupport;
  permission_requests: HarnessPermissionSupport;
  route_hints: HarnessRouteHints;
}

export interface HarnessSessionRoutes {
  self: string;
  events: string;
  cancel: string;
  approvals: string;
}

export interface HarnessSessionResponse {
  session: Session;
  details: SessionDetailsResponse;
  stream_transport: string;
  route_hints: HarnessSessionRoutes;
}

export interface HarnessCreateSessionRequest {
  workspace_id: string;
  project_id?: string;
  provider?: string;
  model?: string;
  agent_id?: string;
  title?: string;
  metadata?: Record<string, unknown>;
  mode_id?: string;
  runtime_kind?: string;
  work_root?: string;
  boot_profile_id?: string;
  durable_agent_id?: string;
}

export interface HarnessTurnRequest {
  content: string;
  cycle_kind?: string;
  effort?: string;
}

export interface HarnessTurnResponse {
  session_id: string;
  message_id: string;
  stream_url: string;
  raw_stream_url: string;
  event_transport: string;
  initial_activity_state: string;
}

export interface HarnessCancelResponse {
  session_id: string;
  status: "cancelled" | "idle" | string;
}

export interface StartSurfacePrefill {
  path?: "chat" | "harness" | "durable" | "recipe";
  provider?: string;
  model?: string;
  agent_id?: string;
  boot_profile_id?: string;
  durable_agent_id?: string;
  durable_prompt?: string;
}

export interface ForkSessionRequest {
  include_messages?: boolean;
  provider?: string;
  model?: string;
  mode_id?: string;
}

/**
 * CW-20260518-0084 — interrupted-turn signal returned alongside a session GET.
 * Non-null only when the session has an in-flight turn whose backend agent is
 * gone (e.g. a service restart killed it mid-generation). The FE uses this to
 * replace an endless "generating" spinner with a clear interrupted state.
 */
export interface InterruptedTurn {
  interrupted: boolean;
  /** Machine-readable cause — currently always "service_restart". */
  reason: string;
  /** Message id of the unanswered user turn that was interrupted. */
  last_message_id: string;
  /** created_at of that message. */
  last_activity_at: string;
}

export interface SessionWithMessages extends Session {
  messages: Message[];
  /** Present only when the backend detected an interrupted in-flight turn. */
  interrupted_turn?: InterruptedTurn | null;
}

export interface MessagePage {
  messages: Message[];
  total: number;
  has_more: boolean;
}

export interface SearchResult {
  session_id: string;
  message_id: string;
  role: string;
  snippet: string;
  created_at: string;
  session_title: string;
  session_short_code: string;
}

export interface Message {
  id: string;
  session_id: string;
  agent_id: string;
  role: "user" | "assistant" | "system" | "tool";
  content: string;
  envelope: string | null;
  metadata: string;
  created_at: string;
}

export interface Agent {
  id: string;
  name: string;
  slug: string;
  avatar: string;
  description: string;
  can_execute: boolean;
  status: string;
  source: string;
  tags: string;
}

export interface AgentProfile {
  id: string;
  name: string;
  slug: string;
  avatar: string;
  icon: string;
  system_prompt: string;
  description: string;
  modes: string;
  default_model: string;
  mcp_servers: string;
  tool_permissions: string;
  can_execute: boolean;
  settings: string;
  created_at: string;
  updated_at: string;
  agent_hash: string;
  version: number;
  tools: string;
  directories: string;
  constraints: string;
  tags: string;
  status: string;
  source: string;
  source_ref: string;
  parent_dispatch_allowlist?: string;
  role_tools?: string;
  role_skills?: string;
  context_policy?: string;
  durable?: boolean;
  urn?: string;
  urn_aliases?: string;
  activation_mode?: string;
  class?: string;
  default_state?: string;
  // Managed file-backed editability contract (additive backend fields).
  // manage_class: one of "managed" | "internal" | "plugin" | "external".
  // editable: true only when manage_class === "managed" — single source of
  //   truth for whether the GUI may edit/delete in place.
  // copy_to_managed: true for plugin/external (offer "Make editable" fork);
  //   false for internal and managed.
  // revision: optimistic-concurrency token (file content hash); may be "".
  manage_class?: string;
  editable?: boolean;
  copy_to_managed?: boolean;
  revision?: string;
}

export type CreateAgentProfileRequest = Pick<
  AgentProfile,
  "name" | "slug" | "system_prompt"
> &
  Partial<
    Pick<
      AgentProfile,
      | "avatar"
      | "icon"
      | "description"
      | "modes"
      | "default_model"
      | "mcp_servers"
      | "tool_permissions"
      | "can_execute"
      | "settings"
      | "tools"
      | "directories"
      | "constraints"
      | "tags"
      | "status"
      | "source"
      | "source_ref"
      | "parent_dispatch_allowlist"
      | "role_tools"
      | "role_skills"
      | "context_policy"
      | "durable"
      | "activation_mode"
      | "class"
      | "default_state"
    >
  >;

export type UpdateAgentProfileRequest = Partial<
  Pick<
    AgentProfile,
    | "name"
    | "slug"
    | "avatar"
    | "icon"
    | "system_prompt"
    | "description"
    | "modes"
    | "default_model"
    | "mcp_servers"
    | "tool_permissions"
    | "can_execute"
    | "settings"
    | "tools"
    | "directories"
    | "constraints"
    | "tags"
    | "status"
    | "parent_dispatch_allowlist"
    | "role_tools"
    | "role_skills"
    | "context_policy"
    | "durable"
    | "activation_mode"
    | "class"
    | "default_state"
  >
>;

export interface AgentBuilderProfileInput {
  id?: string;
  name: string;
  slug: string;
  avatar?: string;
  icon?: string;
  system_prompt: string;
  description?: string;
  default_model?: string;
  mcp_servers?: string;
  tool_permissions?: string;
  settings?: string;
  tools?: string;
  directories?: string;
  constraints?: string;
  tags?: string;
  status?: string;
  source?: string;
  source_ref?: string;
  parent_dispatch_allowlist?: string;
  role_tools?: string;
  role_skills?: string;
  context_policy?: string;
  activation_mode?: string;
  class?: string;
  default_state?: string;
  can_execute?: boolean;
  durable?: boolean;
}

export interface AgentBuilderCapabilitiesInput {
  assigned_skill_ids?: string[];
  assigned_skill_slugs?: string[];
  prompt_template_ids?: string[];
  known_tools?: AgentKnownToolUpsertRequest[];
  known_skills?: AgentKnownSkillUpsertRequest[];
  procedures?: AgentProcedureUpsertRequest[];
  knowledge_seeds?: AgentKnowledgeSeedUpsertRequest[];
  reflex_suggestions?: string[];
}

export interface AgentBuilderDurableInstanceInput {
  create?: boolean;
  recipe_id?: string;
  lifecycle_class?: DurableAgentLifecycleClass | string;
  provider?: string;
  model?: string;
  runtime_kind?: RuntimeKind | string;
  work_root?: string;
  workspace_id?: string;
  project_id?: string;
  start?: boolean;
  metadata?: Record<string, string>;
}

export interface AgentBuilderOperatorNotificationInput {
  target_kind?: string;
  target_id?: string;
  include_links?: boolean;
}

export interface AgentBuilderCapabilityOperation {
  area: string;
  action: string;
  target: string;
  count: number;
  detail?: string;
}

export interface AgentBuilderLaunchPlanPreview {
  lifecycle_class: DurableAgentLifecycleClass | string;
  session_policy: DurableAgentSessionPolicy | string;
  attachment_relation: DurableAgentAttachmentRelation | string;
  provider: string;
  model: string;
  runtime_kind: RuntimeKind | string;
  work_root: string;
  would_create_session: boolean;
  wake_payload: DurableAgentWakePayload;
}

export interface AgentBuilderNotificationResource {
  id: string;
  name: string;
  slug: string;
}

export interface AgentBuilderDeepLink {
  kind: string;
  path: string;
  label: string;
}

export interface AgentBuilderReadyNotificationPreview {
  target_kind: string;
  target_id: string;
  profile: AgentBuilderNotificationResource;
  durable_instance: AgentBuilderNotificationResource;
  session: AgentBuilderNotificationResource;
  links: AgentBuilderDeepLink[];
  warnings: string[];
  followups: string[];
}

export interface AgentBuilderDryRunRequest {
  schema_version: number;
  mode: "create_profile" | "create_profile_and_instance" | "update_profile";
  profile: AgentBuilderProfileInput;
  capabilities?: AgentBuilderCapabilitiesInput;
  durable_instance?: AgentBuilderDurableInstanceInput;
  operator_notification?: AgentBuilderOperatorNotificationInput;
}

export interface AgentBuilderDryRunResponse {
  schema_version: number;
  valid: boolean;
  errors: string[];
  warnings: string[];
  unsupported_fields: string[];
  normalized_profile_payload: AgentBuilderProfileInput;
  capability_operations: AgentBuilderCapabilityOperation[];
  durable_recipe_plan?: DurableAgentRecipePlan;
  launch_plan_preview?: AgentBuilderLaunchPlanPreview;
  notification_preview: AgentBuilderReadyNotificationPreview;
}

export interface AgentBuilderDraft {
  mode:
    | "create_profile"
    | "create_profile_and_instance"
    | "update_profile"
    | string;
  profile: AgentBuilderProfileInput;
  capabilities: AgentBuilderCapabilitiesInput;
  durable_instance: AgentBuilderDurableInstanceInput;
  operator_notification: AgentBuilderOperatorNotificationInput;
}

export interface AgentBuilderDraftRequest {
  schema_version: number;
  intake_text: string;
  name?: string;
  slug?: string;
  description?: string;
  project_context?: string;
  work_root?: string;
  preferred_provider?: string;
  preferred_model?: string;
  preferred_runtime_kind?: RuntimeKind | string;
  requested_lifecycle_class?: DurableAgentLifecycleClass | string;
}

export interface AgentBuilderDraftResponse {
  schema_version: number;
  draft: AgentBuilderDraft;
  questions: string[];
  warnings: string[];
  unsupported_requests: string[];
  confidence: number;
}

export interface AgentBuilderReviewRequest {
  schema_version: number;
  current_draft: AgentBuilderDraft;
  previous_builder_notes?: string[];
}

export interface AgentBuilderPatchOperation {
  op: string;
  path: string;
  value?: string;
  note?: string;
}

export interface AgentBuilderReviewResponse {
  schema_version: number;
  accepted: boolean;
  questions: string[];
  warnings: string[];
  suggested_patch_operations: AgentBuilderPatchOperation[];
  max_rounds_recommended: number;
}

export type AgentReflexStatus = "active" | "paused" | "expired" | string;
export type AgentReflexTriggerKind =
  | "predicate"
  | "event"
  | "interval"
  | string;
export type AgentReflexActionKind =
  | "inject_reminder"
  | "halt_session"
  | "force_tool_choice"
  | "send_message"
  | "add_schedule"
  | string;

export interface AgentReflexRowBase {
  id: string;
  name: string;
  trigger_kind: AgentReflexTriggerKind;
  trigger_spec: string;
  action_kind: AgentReflexActionKind;
  action_spec: string;
  status: AgentReflexStatus;
  priority: number;
  fired_count: number;
  last_fired_at: string;
  created_at: string;
  created_by: string;
}

export interface InheritedAgentReflexRow extends AgentReflexRowBase {
  agent_id: "";
  class_tag: string;
}

export interface ScopedAgentReflexRow extends AgentReflexRowBase {
  agent_id: string;
  class_tag: string;
}

export type AgentReflexRow = InheritedAgentReflexRow | ScopedAgentReflexRow;

export interface PendingReflexRow {
  id: string;
  proposed_by: string;
  proposed_at: string;
  target_agent_id: string;
  name: string;
  trigger_kind: AgentReflexTriggerKind;
  trigger_spec: string;
  action_kind: AgentReflexActionKind;
  action_spec: string;
  rationale: string;
  status: "pending" | "approved" | "rejected" | string;
  reviewed_at: string;
  reviewed_by: string;
}

export interface CreateAgentReflexRequest {
  name: string;
  trigger_kind: AgentReflexTriggerKind;
  trigger_spec: string;
  action_kind: AgentReflexActionKind;
  action_spec: string;
  priority: number;
}

export interface PatchAgentReflexRequest {
  name?: string;
  trigger_kind?: AgentReflexTriggerKind;
  trigger_spec?: string;
  action_kind?: AgentReflexActionKind;
  action_spec?: string;
  status?: AgentReflexStatus;
  priority?: number;
  fired_count?: number;
  last_fired_at?: string;
}

export interface ValidateReflexRequest {
  trigger_kind: AgentReflexTriggerKind;
  trigger_spec: string;
  action_kind: AgentReflexActionKind;
  action_spec: string;
  session_id?: string;
  agent_id?: string;
  agent_class?: string;
  state?: Record<string, unknown>;
}

export interface ValidateReflexResponse {
  valid: boolean;
  errors: string[];
  fired: boolean;
  state_source: "empty" | "request" | "store" | string;
  state_summary: {
    session_id?: string;
    agent_id?: string;
    agent_class?: string;
    messages: number;
    user_messages: number;
    events: number;
    mail_unread_count: number;
    tick_n: number;
    prefix_tokens: number;
  };
}

export interface PendingReflexApproveRequest {
  reviewed_by?: string;
}

export interface PendingReflexRejectRequest {
  reviewed_by?: string;
  reason?: string;
}

export interface PendingReflexRejectResponse {
  id: string;
  status: "rejected" | string;
}

export interface AgentKnownTool {
  agent_id: string;
  tool_name: string;
  pinned: boolean;
  sort_order: number;
  activation_count: number;
  last_used_at: string;
  added_at: string;
  ttl_seconds: number;
  reason: string;
}

export interface AgentKnownSkill {
  agent_id: string;
  skill_name: string;
  pinned: boolean;
  activation_count: number;
  last_used_at: string;
  added_at: string;
  ttl_seconds: number;
  reason: string;
}

export interface AgentProcedure {
  agent_id: string;
  name: string;
  body: string;
  scope: string;
  created_at: string;
  updated_at: string;
}

export interface AgentKnowledgeSeed {
  agent_id: string;
  seed_key: string;
  namespace: string;
  body: string;
  tags_json: string;
  applied_at: string;
  created_at: string;
}

export interface AgentKnownToolUpsertRequest {
  agent_id?: string;
  tool_name?: string;
  pinned: boolean;
  sort_order: number;
  ttl_seconds: number;
  reason: string;
}

export interface AgentKnownSkillUpsertRequest {
  agent_id?: string;
  skill_name?: string;
  pinned: boolean;
  ttl_seconds: number;
  reason: string;
}

export interface AgentProcedureUpsertRequest {
  agent_id?: string;
  name?: string;
  body: string;
  scope?: string;
}

export interface AgentKnowledgeSeedUpsertRequest {
  agent_id?: string;
  seed_key?: string;
  namespace: string;
  body: string;
  tags_json?: string;
  tags?: string[];
}

export interface AgentModeProfile {
  id: string;
  agent_id: string;
  slug: string;
  name: string;
  prompt_addendum: string;
  tool_overrides: string;
  settings: string;
}

// First-class reusable Mode (B1, CW-20260428-0009).
// Mirrors store.Mode in internal/store/modes.go.
export interface Mode {
  id: string;
  slug: string;
  name: string;
  prompt_addendum: string;
  tool_overrides: string;
  settings: string;
  is_builtin: boolean;
  created_at: string;
  updated_at: string;
}

// --- Chat Errors ---

export type ChatErrorCode =
  | "rate_limit"
  | "tool_error"
  | "provider_error"
  | "internal_error";

export interface ChatError {
  id: string;
  code: ChatErrorCode;
  message: string;
  details?: Record<string, unknown>;
  timestamp: string;
  dismissed?: boolean;
}

/**
 * B2 (CW-20260428-0010): non-binding mode-classifier signal emitted by the
 * backend when the deterministic classifier disagrees with the session's
 * current mode at high confidence. The FE stores this for B3 to consume
 * (confirm-card / auto-apply); B2 itself does not act on it.
 */
export interface ModeSuggestion {
  current: string;
  suggested: string;
  confidence: number;
  signals: string[];
}

export interface StreamEvent {
  type:
    | "stream_start"
    | "delta"
    | "replace_content"
    | "stream_end"
    | "error"
    | "tool_call"
    | "tool_result"
    | "tool_warning"
    | "status"
    | "circuit_open"
    | "session_takeover"
    | "approval_request"
    | "plugin_envelope"
    | "mode_suggestion";
  /**
   * Phase classifies delta events by their narrative role (F4 / CW-20260419-0029).
   * "narration" — inter-iteration prose emitted between tool_use blocks.
   * "final"     — post-end_turn text that forms the assistant's answer.
   * "thinking"  — F3 (CW-20260420-0023) interleaved thinking block content.
   * Absent on pre-F4 streams and on non-delta event types.
   */
  phase?: "narration" | "final" | "thinking";
  content?: string;
  message_id?: string;
  agent_id?: string;
  usage?: { input_tokens: number; output_tokens: number; stop_reason: string };
  error?: string;
  structured_error?: {
    code: ChatErrorCode;
    message: string;
    details?: Record<string, unknown>;
    timestamp: string;
  };
  tool?: string;
  tool_id?: string;
  detail?: string;
  summary?: string;
  envelope?: string;
  data?: string;
  plugin_id?: string;
}

/**
 * A plugin-emitted envelope delivered as a standalone chat item (not appended
 * to the current assistant message). Emitted by subprocess plugin event hooks
 * via the backend `plugin_envelope` StreamEvent (BLG-20260413-012).
 */
export interface PluginEnvelopeItem {
  id: string;
  pluginId: string;
  envelope: Envelope;
  receivedAt: number;
}

// --- Context Breakdown ---

export interface MessageTokenDetail {
  id: string;
  role: string;
  content_preview: string;
  tokens: number;
  is_compacted: boolean;
}

export interface ToolTokenDetail {
  name: string;
  tokens: number;
}

export interface ContextBreakdown {
  system_prompt_tokens: number;
  system_prompt_preview: string;
  messages: MessageTokenDetail[];
  message_tokens_total: number;
  tools: ToolTokenDetail[];
  tool_tokens_total: number;
  tools_available: number;
  total: number;
  ceiling: number;
  estimated_cost_usd: number;
}

// --- Token Usage ---

export interface SessionUsageSummary {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  tool_input_tokens: number;
  cache_creation_tokens: number;
  cache_read_tokens: number;
  estimated_cost_usd: number;
  message_count: number;
}

export interface ModelUsage {
  model: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  estimated_cost_usd: number;
}

export interface GlobalUsageSummary {
  total_input: number;
  total_output: number;
  total_tokens: number;
  total_cost: number;
  by_model: ModelUsage[];
}

// --- Execution Metrics ---

export interface ExecutionMetrics {
  id: number;
  session_id: string;
  message_id: string;
  provider: string;
  adapter: string;
  model: string;
  agent_id: string;
  agent_slug: string;
  mode: string;
  duration_ms: number;
  context_messages: number;
  context_tokens: number;
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens: number;
  cache_read_tokens: number;
  estimated_cost_usd: number;
  tool_iterations: number;
  tool_calls: number;
  is_utility: boolean;
  stop_reason: string;
  error: string;
  debug_snapshots: string;
  created_at: string;
}

export interface UtilityCallSummary {
  provider: string;
  model: string;
  call_type: string;
  call_count: number;
  avg_duration_ms: number;
  min_duration_ms: number;
  max_duration_ms: number;
  error_count: number;
  total_cost_usd: number;
}

// --- Process Health ---

export interface ProcessHealthEntry {
  session_id: string;
  pid: number;
  uptime: number;
  idle_duration: number;
  is_stale: boolean;
}

export interface ProcessHealthResponse {
  processes: ProcessHealthEntry[];
  total: number;
  stale_threshold: string;
}

// --- Agent Modes ---

export const AGENT_MODES = [
  "default",
  "architect",
  "planner",
  "writer",
] as const;
export type AgentMode = (typeof AGENT_MODES)[number];

export const MODE_COLORS: Record<AgentMode, string> = {
  default: "blue",
  architect: "red",
  planner: "green",
  writer: "amber",
};

// --- Models ---

export interface ModelOption {
  id: string;
  label: string;
  provider?: string;
}

export interface Provider {
  id: string;
  name: string;
  models: ModelOption[];
}

export interface ModelRecord {
  id: string;
  provider_id: string;
  model_id: string;
  display_name: string;
  context_window: number;
  max_output: number;
  supports_tools: boolean;
  supports_vision: boolean;
  is_enabled: boolean;
  pricing: string;
  sort_order: number;
  provider_type: string;
}

// --- User Settings ---

export interface UserSettings {
  default_provider: string;
  default_model: string;
  default_agent: string;
  utility_provider: string;
  utility_model: string;
  tool_call_display_mode: ToolCallDisplayMode;
  tool_stream_behavior: ToolStreamBehavior;
  tool_drawer_retention: number;
  provider_fallback_chain: string[];
  developer_mode: boolean;
  recover_mode: boolean;
  ext_settings: Record<string, unknown> & {
    widget_visibility?: Record<string, boolean>;
    widget_order?: string[];
    display_name?: string;
    avatar_url?: string;
    email?: string;
    timezone?: string;
    language?: string;
    theme_preference?: "system" | "light" | "dark";
    user_context?: string;
  };
  // Memory embedding (S2a). Server validates provider against a 5-item enum.
  embedding_provider: string;
  embedding_model: string;
  embedding_mode: "disabled" | "explicit";
  // Computed server-side; not persisted. Reflects live credential / reachability.
  embedding_status?:
    | "active"
    | "disabled"
    | "missing_credentials"
    | "unreachable";
  // B3 (CW-20260428-0011): user-level preference for auto-applying classifier
  // mode suggestions. "" = unset (triggers first-use prompt).
  mode_auto_switch_pref?: "" | "always" | "ask" | "never";
}

// B3 (CW-20260428-0011): per-session override for auto-mode-switching.
// Stored only in the FE chat store (not persisted) — resets on full reload.
export type ModeAutoSwitchOverride = "on" | "off";

// B3 (CW-20260428-0011): the resolved effective behavior for a session,
// computed from the global pref + per-session override. Returned by
// useChatStore.getAutoSwitchEffective.
export type ModeAutoSwitchEffective = "auto" | "ask" | "off" | "firstUse";

export interface EmbeddingProviderInfo {
  id: string;
  name: string;
  default_models: string[];
}

export interface ProviderConfig {
  id: string;
  name: string;
  provider_type: string;
  is_enabled: boolean;
  settings: string;
  created_at: string;
  updated_at: string;
}

export interface ProviderStatus extends ProviderConfig {
  has_api_key: boolean;
  registered: boolean;
}

export interface CLIDetectionResult {
  name: string;
  provider_type: string;
  detected: boolean;
  path: string;
  env_var: string;
}

// --- Session Agents ---

export interface SessionAgent {
  id: string;
  agent_id: string;
  session_id: string;
  name: string;
  slug: string;
  avatar: string;
  role: "primary" | "participant";
  status: "active" | "idle" | "offline";
}

// --- Permission & Approval ---

export type PermissionMode = "default" | "accept-edits" | "plan" | "yolo";

export type ShellMode = "ask" | "session" | "yolo";

export type ApprovalDecision = "allow" | "deny";
export type ApprovalScope = "once" | "session";

export interface ApprovalRequest {
  request_id: string;
  tool: string;
  input: Record<string, unknown>;
  reason: string;
}

export interface PendingApproval extends ApprovalRequest {
  receivedAt: number; // Date.now() when SSE event arrived
  resolved?: {
    decision: ApprovalDecision;
    scope?: ApprovalScope;
  };
}

// --- Broker Decisions ---

export interface BrokerDecision {
  id: number;
  session_id: string;
  intent: string;
  layer_reached: string;
  selected_tools: string[];
  signals: string;
  created_at: string;
}

// --- Turn Snapshots ---

export interface TurnSnapshot {
  site: string;
  reason: string;
  tool_calls: TurnSnapshotToolCall[];
  iteration: number;
  max_turns: number;
  timestamp: string;
}

export interface TurnSnapshotToolCall {
  name: string;
  duration_ms: number;
  duration_ns?: number;
  parallel: boolean;
  success?: boolean;
}

// --- Inspector (I1, CW-20260426-0004) ---

export interface InspectorSlotSnapshot {
  name: string;
  tokens: number;
  cached: boolean;
  cache_key?: string;
  sensitive: boolean;
  content: string;
  traffic_light: "green" | "yellow" | "red";
}

export interface InspectorLLMMessageRecord {
  role: string;
  content: string;
  tokens: number;
  classification?: string;
}

export interface InspectorBrokerDecision {
  intent: string;
  outcome: string;
  selected_tools: string[];
  layer_reached: string;
  consecutive_empty: number;
  total_calls: number;
  loaded_count: number;
  reflection_query?: string;
  signals?: string;
}

export interface InspectorToolCallRecord {
  tool_id: string;
  name: string;
  arguments: string;
  result: string;
  is_error: boolean;
  latency_ms: number;
  cache_state: string;
}

export interface InspectorReminderItem {
  id: string;
  text: string;
  trigger_json: string;
  /** D2 — scope tag rendered next to the reminder ID. */
  scope?: AgentStateScope;
}

export interface InspectorRemindersRecord {
  set_this_turn?: InspectorReminderItem[];
  fired_this_turn?: InspectorReminderItem[];
}

export interface InspectorTurnSnapshot {
  session_id: string;
  turn_id: string;
  started_at: string;
  slots: InspectorSlotSnapshot[];
  llm_messages: InspectorLLMMessageRecord[];
  broker_decisions: InspectorBrokerDecision[];
  tool_calls: InspectorToolCallRecord[];
  scope_tier?: string;
  playbook?: { name: string; steps?: string[] };
  memory_hits?: { source: string; content: string; score?: number }[];
  loop_status?: { detected: boolean; reason?: string };
  /** Reminders set and fired this turn (J11, CW-20260426-0009) */
  reminders?: InspectorRemindersRecord;
}

export interface InspectorTurnsResponse {
  session_id: string;
  turns: InspectorTurnSnapshot[];
  count: number;
}

// --- Envelopes ---

export interface Envelope {
  kind: string;
  version: number;
  type: string;
  id?: string;
  title?: string; // agent-defined card title
  subtitle?: string; // agent-defined subheading
  display_class?: "content" | "alert" | "action-required";
  prior_response?: ResponseV1; // set by backend if already answered
  proposals?: Proposal[];
  approval?: EnvelopeApprovalRequest;
  status?: { phase: string; progress: number };
  data?: Record<string, unknown>;
  /**
   * J8 v1 — visibility hint (CW-20260426-0006). The optional panel ID to
   * OPEN when this envelope arrives. Does NOT control where the envelope
   * renders. Known v1 IDs: `bottom_chat_drawer`, `work`, `workflows`.
   * Plugin-shipped panel IDs are accepted for trusted callers. Omit to
   * skip the visibility signal. For routing the card itself, see
   * `render_target` (A2). Both fields can be set independently.
   */
  target?: string;
  /**
   * A2 — placement hint (CW-20260428-0008). The optional panel ID where
   * this envelope should RENDER. When set and the FE dismiss-machine
   * permits, the envelope is pushed into the panel's inbox slot
   * (`useLayoutStore.panelEnvelopes[render_target]`) and the chat shows a
   * stub link instead of the full envelope. Empty / undefined = inline
   * render (the historical default). The backend stamps this from the
   * per-type schema's `default_render_target` when the agent did not
   * provide one; explicit agent override wins.
   */
  render_target?: string;
  /**
   * A2 — debug indicator (CW-20260428-0008). When the backend rejected an
   * explicit render_target at the trust gate, this carries a short reason
   * code (`untrusted_plugin_panel`, `unknown_panel`, `untrusted`). The FE
   * surfaces it as a small inline pill so the agent's blocked intent is
   * visible. The envelope renders inline as a fallback.
   */
  render_target_blocked?: string;
  /**
   * J8 v1 — mode/status signal carried alongside the envelope
   * (CW-20260426-0006). When set, the FE resolves the mode against the
   * preset map at `ui/src/lib/panel-modes.ts` and opens the associated
   * panels using the agent_opened state. Independent of `target` — both
   * can be set on the same envelope. v1 vocabulary: `planning`. Unknown
   * modes are silently ignored.
   */
  mode?: string;
  /**
   * Phase 9 (CW-20260510-0017 / W1D + W2A): wrap-level opaque cancel
   * token emitted by the recovery broker for in-flight retry envelopes.
   * Lives at wrap level (sibling of id/type/data), NOT inside `data`,
   * because the info-card schema sets `additionalProperties: false`.
   *
   * When set, the FE renders a [Cancel retry] button that POSTs the
   * token verbatim to `POST /api/sessions/{sessionID}/recovery/cancel`
   * with body `{"token": "<token>"}`. On success, the card transitions
   * to a cancelled visual state.
   *
   * Recovery-broker-only today; if a third caller appears the field
   * may consolidate with `EnvelopeRouting` per W1D's follow-up note.
   */
  cancel_token?: string;
  /**
   * CW-20260517-0008 — wrap-level marker for envelopes that are ONLY emitted
   * when developer mode is enabled (today: `chat-loop-budget-soft-warning`,
   * gated behind `devModeEnabled()` in the backend). When true, the FE renders
   * a small "DEV" badge so operators recognize the card as dev-mode telemetry
   * rather than a real alert. Lives at wrap level (sibling of id/type/data),
   * stamped by `buildPluginEnvelopeWrap`. Omitted (falsy) for normal
   * envelopes. Intentionally type-agnostic — any future dev-only envelope
   * type gets the badge for free.
   */
  dev_mode_only?: boolean;
}

export interface Proposal {
  type: string;
  payload: Record<string, unknown>;
  schema?: Record<string, SchemaField>;
}

export interface SchemaField {
  type: "text" | "textarea" | "select" | "number";
  label?: string;
  options?: string[];
  required?: boolean;
}

export interface EnvelopeApprovalRequest {
  description: string;
  risk_level?: "low" | "medium" | "high";
  details?: string;
}

// --- Agent messages (messaging subsystem) ---
//
// Shape matches the backend messaging.Message struct introduced in
// S7. Addressing is session-scoped — every message has both a
// session_id and agent_id on each end of the tuple. Channel +
// kind + payload_json came in S7 T3/T4.

export type AgentMessageType =
  | "message"
  | "help_request"
  | "directive"
  | "status_update"
  | "handoff";
export type AgentMessageStatus =
  | "unread"
  | "read"
  | "acknowledged"
  | "resolved";
export type AgentMessageChannel = "chat" | "inbox" | "alert";
export type AgentMessageKind = "request" | "reply" | "notification" | "handoff";

export interface AgentMessage {
  id: string;
  from_session_id: string;
  from_agent_id: string;
  to_session_id: string;
  to_agent_id: string;
  thread_id: string;
  reply_to: string;
  type: AgentMessageType;
  subject: string;
  body: string;
  metadata: string;
  priority: number;
  status: AgentMessageStatus;
  channel: AgentMessageChannel;
  kind: AgentMessageKind;
  payload_json: string;
  created_at: string;
  read_at: string | null;
  resolved_at: string | null;
}

// --- Presence ---

export interface PresenceEvent {
  type:
    | "stream_start"
    | "stream_end"
    | "tool_pending"
    | "tool_resolved"
    | "cli_active"
    | "session_archived"
    | "work_changed"
    /** F1 (CW-20260429-0001): cross-tab session-mode sync. */
    | "session_mode_changed";
  session_id: string;
  agent_id?: string;
  tool_name?: string;
  timestamp: string;
  /** Populated for session_mode_changed; empty when the mode pointer is cleared. */
  mode_id?: string;
  /** Populated for session_mode_changed; empty when the mode pointer is cleared. */
  mode_slug?: string;
}

export interface ActiveStreamInfo {
  agentId: string;
  startedAt: string;
}

export interface PendingToolInfo {
  toolName: string;
}

export interface CLIActiveInfo {
  lastSeen: string;
}

// --- Bookmarks ---

export interface Bookmark {
  id: string;
  message_id: string;
  session_id: string;
  note: string;
  tags: string[];
  created_at: string;
}

// --- Artifacts ---

export interface Artifact {
  id: string;
  session_id: string;
  name: string;
  mime_type: string;
  /** Server-side field: bytes. F4 (CW-20260429-0004) — name corrected from
   * `size` (which the BE never emits) to match the JSON shape. */
  size_bytes: number;
  created_at: string;
}

// --- Documents (J10, CW-20260426-0008) ---

export interface Document {
  id: string;
  session_id: string;
  name: string;
  mime_type: string;
  content: string;
  size_bytes: number;
  /** Include document in agent context */
  included: boolean;
  /** true = send full content; false = send pointer (name + summary) */
  full_content: boolean;
  summary: string;
  created_at: string;
  updated_at: string;
}

// --- Pinned content (J11, CW-20260426-0009; D1, CW-20260428-0014) ---

export type AgentStateScope = "turn" | "session" | "project";

export interface PinnedContent {
  id: string;
  session_id?: string | null;
  scope: AgentStateScope;
  /** Project ID — populated when scope='project'. */
  project_id?: string;
  content: string;
  agent_id: string;
  created_at: string;
  updated_at: string;
}

// --- Bottom drawer pinned cards (C1, CW-20260428-0012) ---
//
// User-pinned cards in the bottom chat drawer. Distinct from PinnedContent
// (J11) which is the agent-context slot pin feature. card_type is one of
// 'markdown' | 'diff' | 'image' | 'scratchpad' | 'artifact-mini' |
// 'agent-envelope' (forward-compat strings tolerated). content_ref is a
// type-specific pointer the FE resolves (envelope_id, artifact_id, etc.).
// payload carries the renderable snapshot (e.g. JSON envelope) so pins survive
// reload even when the source row has been GC'd.

export type DrawerCardType =
  | "markdown"
  | "diff"
  | "image"
  | "scratchpad"
  | "artifact-mini"
  | "agent-envelope"
  | (string & {});

export interface DrawerPinnedCard {
  id: string;
  session_id: string;
  card_type: DrawerCardType;
  content_ref: string;
  title: string;
  payload: string;
  position: number;
  created_at: string;
}

/**
 * Per-tab data for transient card-tabs in ChatWorkingDrawer.
 *
 * Each agent-emitted envelope routed via `render_target=bottom_chat_drawer`
 * becomes one of these tabs. `pinned: true` promotes via the existing
 * POST /drawer-cards endpoint and survives session reload as a DB-backed
 * pinned card; transient (pinned=false) tabs live only in the layout store
 * for the session.
 */
export interface DynamicCardTab {
  /** Stable ID. Format: `card:<uuid>`. */
  id: string;
  /** Display label. Derived from envelope.title when present;
   *  fallback = envelope-type display name + short timestamp. */
  label: string;
  /** The full envelope payload — render via EnvelopeRenderer. */
  payload: Envelope;
  /** Agent-emitted defaults true. When true, the tab promotes to active
   *  on the next drawer-open. Manual user selection overrides until the
   *  next focused-true arrival. */
  focused: boolean;
  /** When true, has been promoted to a DB-backed pinned card via the
   *  existing API. Transient tabs default false. */
  pinned: boolean;
  /** Epoch ms. Used for stable sort order in the tab strip. */
  createdAt: number;
}

// --- Reminders (J11, CW-20260426-0009; D1, CW-20260428-0014) ---

export interface Reminder {
  id: string;
  session_id: string;
  scope: AgentStateScope;
  /** Project ID — populated when scope='project'. */
  project_id?: string;
  text: string;
  /** Raw JSON trigger blob — see internal/reminders.Trigger. */
  trigger_json: string;
  fired_at?: string | null;
  created_at: string;
  updated_at: string;
}

// --- Tool Call Display ---

export type ToolCallDisplayMode = "indicator" | "minimal" | "compact" | "full";
export type ToolStreamBehavior = "streaming" | "persist" | "hidden";

// --- Tool Calls ---

export interface ToolCall {
  id: string;
  tool: string;
  status: "running" | "done" | "error";
  summary?: string;
  detail?: string;
}

export interface ToolWarning {
  tool_name: string;
  error: string;
  iteration: number;
  consecutive_errors: number;
  level: "warning" | "critical";
}

// --- Tool Management ---

export interface ToolDefinition {
  name: string;
  description: string;
  input_schema: Record<string, unknown>;
}

export interface ServerInfo {
  name: string;
  tool_count: number;
  connected: boolean;
}

export interface DiscoveryDiff {
  added: string[];
  removed: string[];
  total: number;
}

export interface ToolSelection {
  name: string;
  description: string;
  server?: string;
}

export interface MCPServerConfig {
  id: string;
  name: string;
  transport_type: "stdio" | "sse";
  command: string;
  url: string;
  args: string;
  env: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

// --- Tool Load Preferences ---

export interface ToolLoadItem {
  name: string;
  description: string;
  load_type: "auto" | "opt-in";
  load_type_source: string;
  enabled: boolean;
}

export type ToolLoadPreferences = Record<string, string>;

// --- Engine (Sprint Planning) ---

export interface FragmentsSprint {
  id: string;
  project_id: string;
  name: string;
  goal: string;
  status: string;
  start_date: string;
  end_date: string;
  created_at: string;
  updated_at: string;
}

export interface FragmentsTask {
  id: string;
  sprint_id: string;
  project_id: string;
  title: string;
  body: string;
  priority: string;
  status: string;
  tags: string[];
  created_at: string;
  updated_at: string;
}

export interface FragmentsBacklogItem {
  id: string;
  project_id: string;
  title: string;
  body: string;
  priority: string;
  tags: string[];
  created_at: string;
}

// --- Todos & Plans ---

export type TodoStatus = "pending" | "in_progress" | "done" | "blocked";
export type TodoPriority = "low" | "medium" | "high" | "critical";
export type PlanStatus =
  | "proposed"
  | "approved"
  | "in_progress"
  | "complete"
  | "abandoned";
export type PlanStepStatus = "pending" | "in_progress" | "done" | "skipped";

export interface Todo {
  id: string;
  /** D1 (CW-20260428-0014): workspace dropped, turn added. */
  scope: AgentStateScope;
  scope_id: string;
  /** Project pointer — populated when scope='project'. */
  project_id?: string;
  parent_id?: string;
  title: string;
  description: string;
  status: TodoStatus;
  priority: TodoPriority;
  labels: string[];
  metadata: Record<string, unknown>;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface TodoFilter {
  scope?: string;
  scope_id?: string;
  /** D1 — convenience filter on project_id directly. */
  project_id?: string;
  status?: TodoStatus;
  priority?: TodoPriority;
  parent_id?: string;
  labels?: string[];
}

export interface PlanStep {
  id: string;
  title: string;
  status: PlanStepStatus;
  todo_id?: string;
  depends_on: string[];
  acceptance?: string;
  notes?: string;
}

export interface Plan {
  id: string;
  scope: "workspace" | "project" | "session";
  scope_id: string;
  title: string;
  description: string;
  status: PlanStatus;
  steps: PlanStep[];
  metadata: Record<string, unknown>;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface PlanFilter {
  scope?: string;
  scope_id?: string;
  status?: PlanStatus;
}

export interface WorkDiff {
  todos_checked: string[];
  todos_unchecked: Array<{ id: string; reason?: string }>;
  todos_added: string[];
  todos_reordered: boolean;
  plan_steps_checked: Array<{ plan_id: string; step_id: string }>;
  plan_steps_unchecked: Array<{
    plan_id: string;
    step_id: string;
    reason?: string;
  }>;
  plans_approved: string[];
  plans_rejected: string[];
}

// --- Workers (background orchestration) ---

export type WorkerType = "full" | "light";
export type WorkerStatus =
  | "spawning"
  | "running"
  | "completed"
  | "failed"
  | "cancelled";

export interface Worker {
  id: string;
  type: WorkerType;
  parent_session_id: string;
  session_id?: string;
  task_id?: string;
  agent_id: string;
  status: WorkerStatus;
  worktree_path?: string;
  created_at: string;
}

// --- Plugin Keybindings ---

export interface PluginKeybinding {
  id: string;
  key: string;
  action: string;
  action_value: string;
  label: string;
  description: string;
}

// --- Slash Command Args ---

export interface CommandArg {
  name: string;
  description?: string;
  required?: boolean;
  type?: string;
  options?: string[];
}

export interface SlashCommandDef {
  name: string;
  description: string;
  category: string;
  source: string;
  args?: CommandArg[];
  required_permission?: string;
}

// --- UI Slots ---

export type UISlotName =
  | "nav-rail"
  | "settings-tab"
  | "right-rail-tab"
  | "composer-toolbar"
  | "chat-header-action"
  | "composer-above"
  | "composer-below"
  | "message-actions"
  | "message-header"
  | "session-sidebar"
  | "modal"
  | "context-menu:message"
  | "context-menu:session"
  | "command-palette";

export interface UISlotEntry {
  id: string;
  plugin_id: string;
  slot: UISlotName;
  label: string;
  icon?: string;
  priority?: number;
  component?: string;
  action?: string;
  props?: Record<string, unknown>;
}

// --- Plugins ---

export interface PluginInfo {
  name: string;
  // Optional human-friendly display name. Backend-yaml schema for this lives
  // in the rest of BLG-20260414-013 (separate session) — frontend is a
  // pure pass-through with `name` as the fallback.
  display_name?: string;
  version: string;
  description: string;
  short_desc: string;
  author: string;
  url: string;
  status: "active" | "disabled" | "available" | "no-binary";
  type: "core" | "user";
  installed: boolean;
  // Install-time signature-verify outcome. Populated by the backend from the
  // devmode build-tag gate: production builds always emit "signed" for
  // installed plugins (unsigned archives refuse to install); devmode builds
  // may emit "unsigned". "untrusted" is reserved for future per-install
  // records and should not appear in practice.
  trust_tier?: "signed" | "unsigned" | "untrusted";
  // Runtime opt-outs a subprocess plugin declined at load time.
  // Wire shape (from Go /api/plugins/managed):
  //   { kind: string; id: string; reason: string }[]
  // Only populated for loaded subprocess plugins; absent for core/available.
  skipped_registrations?: SkippedRegistration[];
}

export interface SkippedRegistration {
  kind: string;
  id: string;
  reason: string;
}

// --- Plugin Catalog ---

export interface CatalogSource {
  id: string;
  name: string;
  url: string;
  type: "official" | "custom";
  enabled: boolean;
  priority: number;
  public_key: string;
  created_at: string;
  updated_at: string;
}

export interface CatalogBrowseEntry {
  name: string;
  version: string;
  description: string;
  author?: string;
  repo?: string;
  archive_url: string;
  checksum?: string;
  signature?: string;
  compat?: string;
  runtime?: string;
  tags?: string[];
  source_id: string;
  source_name: string;
  installed: boolean;
  installed_version?: string;
  update_available?: boolean;
}

export interface ConfigField {
  key: string;
  type: "string" | "bool" | "int" | "select" | "secret";
  label: string;
  description?: string;
  default?: unknown;
  required?: boolean;
  options?: string[];
  component?: string;
}

export interface PluginConfig {
  plugin_id: string;
  settings: Record<string, unknown>;
  schema: ConfigField[];
  updated_at?: string;
}

export interface PluginUIComponent {
  id: string;
  type: "widget" | "envelope" | "action" | "workflow" | "view";
  name: string;
  description: string;
  props?: Record<string, unknown>;
  plugin_id?: string;
}

// --- Skills ---

export interface ToolBinding {
  server: string;
  tool: string;
}

export interface Skill {
  id: string;
  name: string;
  slug: string;
  category: string;
  description: string;
  icon: string;
  tool_bindings: string;
  input_schema: string;
  is_builtin: boolean;
  settings: string;
  prompt?: string; // markdown body; present for file-based skills
  created_at: string;
  updated_at: string;
  // J7 ingestion metadata.
  source?: string; // "builtin", "user", "project", "plugin", "claude"
  imported_at?: string;
  origin_system?: string;
  format?: string;
  version?: number;
  // E2 (CW-20260428-0017): mode binding. JSON string array of mode IDs;
  // empty / "[]" / undefined means "available in every mode".
  mode_ids?: string;
}

// --- Prompt Templates ---

export interface TemplateVariable {
  name: string;
  type: "text" | "textarea" | "number" | "boolean" | "select";
  required: boolean;
  default?: string | number | boolean;
  description?: string;
  options?: string[];
}

export interface PromptTemplate {
  id: string;
  name: string;
  slug: string;
  scope: "system" | "mode" | "skill" | "context";
  template: string;
  variables: string;
  priority: number;
  icon: string;
  is_builtin: boolean;
  created_at: string;
  updated_at: string;
}

// --- Workflow / Pipeline ---

export interface PipelineInfo {
  id: string;
  name: string;
  description: string;
  step_count: number;
}

export type RunStatus =
  | "pending"
  | "running"
  | "completed"
  | "failed"
  | "cancelled";
export type StepStatus =
  | "pending"
  | "running"
  | "completed"
  | "failed"
  | "skipped"
  | "cancelled";

export interface StepState {
  step_id: string;
  status: StepStatus;
  attempts: number;
  started_at: string;
  completed_at: string;
  error: string;
  skip_reason: string;
}

export interface RunState {
  pipeline_id: string;
  run_id: string;
  status: RunStatus;
  step_states: Record<string, StepState>;
  started_at: string;
  completed_at: string;
  error: string;
}

export interface WorkflowEvent {
  type: string;
  pipeline_id: string;
  run_id: string;
  step_id: string;
  data: Record<string, unknown>;
  timestamp: string;
}

export interface WorkflowRun {
  pipeline: PipelineInfo;
  run: RunState;
  events: WorkflowEvent[];
}

// --- Memory ---

export type MemoryOrigin =
  | "user"
  | "feedback"
  | "project"
  | "reference"
  | "observation";
export type MemoryStatus = "draft" | "reviewed" | "canonical" | "deprecated";
export type MemoryScope = "session" | "project" | "user";

export interface Memory {
  memory_key: string;
  namespace: string;
  summary: string;
  body: string;
  origin: MemoryOrigin;
  trigger: string;
  confidence: number;
  tags: string[];
  scope: MemoryScope;
  session_id: string;
  revision_id: string;
  status: MemoryStatus;
  created_at: string;
  updated_at: string;
}

export interface MemoryListResponse {
  memories: Memory[];
  total: number;
}

export interface MemoryCreateRequest {
  summary: string;
  body?: string;
  origin: MemoryOrigin;
  confidence: number;
  scope: MemoryScope;
  tags: string[];
}

export interface MemoryUpdateRequest {
  summary: string;
  body?: string;
  origin: MemoryOrigin;
  confidence: number;
  tags: string[];
}

// --- Role Trust (H1 CW-20260421-0014) ---

export type TrustTier = "untrusted" | "normal" | "trusted";

// WorkspaceRoleTrustOverride is a single row from workspace_role_trust
// listing the explicit override for one agent profile in a workspace.
export interface WorkspaceRoleTrustOverride {
  workspace_id: string;
  agent_profile_id: string;
  trust_tier: TrustTier;
  promoted_at: string;
  promoted_by: string;
}
