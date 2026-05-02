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
  // B1 (CW-20260428-0009): session-level mode pointer.
  // Null = fall back to agent-assigned legacy AgentMode.
  current_mode_id?: string | null;
  // F2 (CW-20260429-0002): per-session auto-mode-switch override. Tri-state
  // — null/undefined inherits user_settings.mode_auto_switch_pref; true =
  // force ON for this session (does NOT bypass first-use prompt); false =
  // force OFF (suppress all auto-switches even when user pref permits).
  auto_switch_override?: boolean | null;
}

export interface SessionWithMessages extends Session {
  messages: Message[];
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
  default_mode: string;
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

export type ChatErrorCode = "rate_limit" | "tool_error" | "provider_error" | "internal_error";

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

export const AGENT_MODES = ["default", "architect", "planner", "writer"] as const;
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
    theme_preference?: 'system' | 'light' | 'dark';
    user_context?: string;
  };
  // Memory embedding (S2a). Server validates provider against a 5-item enum.
  embedding_provider: string;
  embedding_model: string;
  embedding_mode: 'disabled' | 'explicit';
  // Computed server-side; not persisted. Reflects live credential / reachability.
  embedding_status?: 'active' | 'disabled' | 'missing_credentials' | 'unreachable';
  // B3 (CW-20260428-0011): user-level preference for auto-applying classifier
  // mode suggestions. "" = unset (triggers first-use prompt).
  mode_auto_switch_pref?: '' | 'always' | 'ask' | 'never';
}

// B3 (CW-20260428-0011): per-session override for auto-mode-switching.
// Stored only in the FE chat store (not persisted) — resets on full reload.
export type ModeAutoSwitchOverride = 'on' | 'off';

// B3 (CW-20260428-0011): the resolved effective behavior for a session,
// computed from the global pref + per-session override. Returned by
// useChatStore.getAutoSwitchEffective.
export type ModeAutoSwitchEffective = 'auto' | 'ask' | 'off' | 'firstUse';

export interface EmbeddingProviderInfo {
  id: string;
  name: string;
  default_models: string[];
}

export interface ProviderConfig {
  id: string;
  name: string;
  provider_type: string;
  base_url: string;
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
  traffic_light: 'green' | 'yellow' | 'red';
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
  strategy?: { reflex_match_id?: string; max_turns: number; reasoning?: string };
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
  prior_response?: ResponseV1; // set by backend if already answered
  proposals?: Proposal[];
  questions?: Question[];
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

export interface Question {
  prompt: string;
  type: "text" | "textarea" | "select" | "radio" | "checkbox";
  options?: (string | { value: string; label: string; description?: string })[];
  required: boolean;
  default?: string;
  description?: string; // paragraph shown in card display
  display_style?: "compact" | "card"; // defaults to "compact"
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

export type AgentMessageType = "message" | "help_request" | "directive" | "status_update" | "handoff";
export type AgentMessageStatus = "unread" | "read" | "acknowledged" | "resolved";
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
  id: string
  session_id: string
  name: string
  mime_type: string
  content: string
  size_bytes: number
  /** Include document in agent context */
  included: boolean
  /** true = send full content; false = send pointer (name + summary) */
  full_content: boolean
  summary: string
  created_at: string
  updated_at: string
}

// --- Pinned content (J11, CW-20260426-0009; D1, CW-20260428-0014) ---

export type AgentStateScope = 'turn' | 'session' | 'project'

export interface PinnedContent {
  id: string
  session_id?: string | null
  scope: AgentStateScope
  /** Project ID — populated when scope='project'. */
  project_id?: string
  content: string
  agent_id: string
  created_at: string
  updated_at: string
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
  | 'markdown'
  | 'diff'
  | 'image'
  | 'scratchpad'
  | 'artifact-mini'
  | 'agent-envelope'
  | (string & {})

export interface DrawerPinnedCard {
  id: string
  session_id: string
  card_type: DrawerCardType
  content_ref: string
  title: string
  payload: string
  position: number
  created_at: string
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
  id: string
  /** Display label. Derived from envelope.title when present;
   *  fallback = envelope-type display name + short timestamp. */
  label: string
  /** The full envelope payload — render via EnvelopeRenderer. */
  payload: Envelope
  /** Agent-emitted defaults true. When true, the tab promotes to active
   *  on the next drawer-open. Manual user selection overrides until the
   *  next focused-true arrival. */
  focused: boolean
  /** When true, has been promoted to a DB-backed pinned card via the
   *  existing API. Transient tabs default false. */
  pinned: boolean
  /** Epoch ms. Used for stable sort order in the tab strip. */
  createdAt: number
}

// --- Reminders (J11, CW-20260426-0009; D1, CW-20260428-0014) ---

export interface Reminder {
  id: string
  session_id: string
  scope: AgentStateScope
  /** Project ID — populated when scope='project'. */
  project_id?: string
  text: string
  /** Raw JSON trigger blob — see internal/reminders.Trigger. */
  trigger_json: string
  fired_at?: string | null
  created_at: string
  updated_at: string
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

export type TodoStatus = 'pending' | 'in_progress' | 'done' | 'blocked'
export type TodoPriority = 'low' | 'medium' | 'high' | 'critical'
export type PlanStatus = 'proposed' | 'approved' | 'in_progress' | 'complete' | 'abandoned'
export type PlanStepStatus = 'pending' | 'in_progress' | 'done' | 'skipped'

export interface Todo {
  id: string
  /** D1 (CW-20260428-0014): workspace dropped, turn added. */
  scope: AgentStateScope
  scope_id: string
  /** Project pointer — populated when scope='project'. */
  project_id?: string
  parent_id?: string
  title: string
  description: string
  status: TodoStatus
  priority: TodoPriority
  labels: string[]
  metadata: Record<string, unknown>
  created_by: string
  created_at: string
  updated_at: string
}

export interface TodoFilter {
  scope?: string
  scope_id?: string
  /** D1 — convenience filter on project_id directly. */
  project_id?: string
  status?: TodoStatus
  priority?: TodoPriority
  parent_id?: string
  labels?: string[]
}

export interface PlanStep {
  id: string
  title: string
  status: PlanStepStatus
  todo_id?: string
  depends_on: string[]
  acceptance?: string
  notes?: string
}

export interface Plan {
  id: string
  scope: 'workspace' | 'project' | 'session'
  scope_id: string
  title: string
  description: string
  status: PlanStatus
  steps: PlanStep[]
  metadata: Record<string, unknown>
  created_by: string
  created_at: string
  updated_at: string
}

export interface PlanFilter {
  scope?: string
  scope_id?: string
  status?: PlanStatus
}

export interface WorkDiff {
  todos_checked: string[]
  todos_unchecked: Array<{ id: string; reason?: string }>
  todos_added: string[]
  todos_reordered: boolean
  plan_steps_checked: Array<{ plan_id: string; step_id: string }>
  plan_steps_unchecked: Array<{ plan_id: string; step_id: string; reason?: string }>
  plans_approved: string[]
  plans_rejected: string[]
}

// --- Workers (background orchestration) ---

export type WorkerType = "full" | "light";
export type WorkerStatus = "spawning" | "running" | "completed" | "failed" | "cancelled";

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

// --- Custom Actions ---

export interface CustomAction {
  id: string;
  name: string;
  description: string;
  keybinding: string;
  command: string;
  slash_command: string;
  auto_triggers: string; // JSON array: ["on_new_session", "on_agent_switch", "on_mode_change"]
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export const AUTO_TRIGGER_OPTIONS = [
  { value: "on_new_session", label: "New Session" },
  { value: "on_agent_switch", label: "Agent Switch" },
  { value: "on_mode_change", label: "Mode Change" },
] as const;

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
  prompt?: string;   // markdown body; present for file-based skills
  created_at: string;
  updated_at: string;
  // J7 ingestion metadata.
  source?: string;        // "builtin", "user", "project", "plugin", "claude"
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

export type RunStatus = "pending" | "running" | "completed" | "failed" | "cancelled";
export type StepStatus = "pending" | "running" | "completed" | "failed" | "skipped" | "cancelled";

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

export type MemoryOrigin = "user" | "feedback" | "project" | "reference" | "observation";
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
