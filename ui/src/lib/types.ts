export interface Workspace {
  id: string
  name: string
  description: string
  icon: string
  sort_order: number
}

export interface Session {
  id: string
  short_code: string
  title: string
  custom_name: string
  workspace_id: string
  project_id: string
  status: string
  is_pinned: boolean
  sort_order: number
  message_count: number
  tags: string
  last_activity: string
  created_at: string
}

export interface SessionWithMessages extends Session {
  messages: Message[]
}

export interface Message {
  id: string
  session_id: string
  agent_id: string
  role: 'user' | 'assistant' | 'system' | 'tool'
  content: string
  envelope: string | null
  metadata: string
  created_at: string
}

export interface Agent {
  id: string
  name: string
  slug: string
  avatar: string
  description: string
  can_execute: boolean
}

export interface AgentProfile {
  id: string
  name: string
  slug: string
  avatar: string
  system_prompt: string
  description: string
  modes: string
  default_mode: string
  default_model: string
  mcp_servers: string
  tool_permissions: string
  can_execute: boolean
  settings: string
  created_at: string
  updated_at: string
}

export interface AgentModeProfile {
  id: string
  agent_id: string
  slug: string
  name: string
  prompt_addendum: string
  tool_overrides: string
  settings: string
}

// --- Chat Errors ---

export type ChatErrorCode = 'rate_limit' | 'tool_error' | 'provider_error' | 'internal_error'

export interface ChatError {
  id: string
  code: ChatErrorCode
  message: string
  details?: Record<string, unknown>
  timestamp: string
  dismissed?: boolean
}

export interface StreamEvent {
  type: 'stream_start' | 'delta' | 'stream_end' | 'error' | 'tool_call' | 'tool_result' | 'status' | 'circuit_open'
  content?: string
  message_id?: string
  agent_id?: string
  usage?: { input_tokens: number; output_tokens: number; stop_reason: string }
  error?: string
  structured_error?: {
    code: ChatErrorCode
    message: string
    details?: Record<string, unknown>
    timestamp: string
  }
  tool?: string
  summary?: string
}

// --- Context Breakdown ---

export interface MessageTokenDetail {
  id: string
  role: string
  content_preview: string
  tokens: number
  is_compacted: boolean
}

export interface ToolTokenDetail {
  name: string
  tokens: number
}

export interface ContextBreakdown {
  system_prompt_tokens: number
  system_prompt_preview: string
  messages: MessageTokenDetail[]
  message_tokens_total: number
  tools: ToolTokenDetail[]
  tool_tokens_total: number
  tools_available: number
  total: number
  ceiling: number
  estimated_cost_usd: number
}

// --- Token Usage ---

export interface SessionUsageSummary {
  input_tokens: number
  output_tokens: number
  total_tokens: number
  cache_creation_tokens: number
  cache_read_tokens: number
  estimated_cost_usd: number
  message_count: number
}

export interface ModelUsage {
  model: string
  input_tokens: number
  output_tokens: number
  total_tokens: number
  estimated_cost_usd: number
}

export interface GlobalUsageSummary {
  total_input: number
  total_output: number
  total_tokens: number
  total_cost: number
  by_model: ModelUsage[]
}

// --- Agent Modes ---

export const AGENT_MODES = ['default', 'architect', 'planner', 'writer'] as const
export type AgentMode = (typeof AGENT_MODES)[number]

export const MODE_COLORS: Record<AgentMode, string> = {
  default: 'blue',
  architect: 'purple',
  planner: 'green',
  writer: 'amber',
}

// --- Models ---

export interface ModelOption {
  id: string
  label: string
  provider?: string
}

export interface Provider {
  id: string
  name: string
  models: ModelOption[]
}

export const AVAILABLE_MODELS: ModelOption[] = [
  { id: 'claude-sonnet-4-20250514', label: 'Claude Sonnet 4', provider: 'anthropic' },
  { id: 'claude-opus-4-20250514', label: 'Claude Opus 4', provider: 'anthropic' },
  { id: 'claude-haiku-35-20241022', label: 'Claude Haiku', provider: 'anthropic' },
]

// --- Workflows ---

export interface WorkflowInput {
  name: string
  type: 'text' | 'textarea' | 'select' | 'number' | 'boolean'
  label: string
  required: boolean
  default?: string | number | boolean
  options?: string[]
}

export interface Workflow {
  name: string
  description: string
  inputs: WorkflowInput[]
}

export interface StepResult {
  name: string
  status: 'pending' | 'running' | 'done' | 'error'
  output?: string
  error?: string
}

export interface WorkflowResult {
  workflow: string
  status: 'pending' | 'running' | 'done' | 'error'
  steps: StepResult[]
  output?: string
}

// --- Session Agents ---

export interface SessionAgent {
  id: string
  agent_id: string
  session_id: string
  name: string
  slug: string
  avatar: string
  role: 'primary' | 'participant'
  status: 'active' | 'idle' | 'offline'
}

// --- Envelopes ---

export interface Envelope {
  kind: string
  version: number
  type: string
  proposals?: Proposal[]
  questions?: Question[]
  approval?: ApprovalRequest
  status?: { phase: string; progress: number }
  data?: Record<string, unknown>
}

export interface Proposal {
  type: string
  payload: Record<string, unknown>
  schema?: Record<string, SchemaField>
}

export interface SchemaField {
  type: 'text' | 'textarea' | 'select' | 'number'
  label?: string
  options?: string[]
  required?: boolean
}

export interface Question {
  prompt: string
  type: 'text' | 'textarea' | 'select' | 'radio' | 'checkbox'
  options?: string[]
  required: boolean
  default?: string
}

export interface ApprovalRequest {
  description: string
  risk_level?: 'low' | 'medium' | 'high'
  details?: string
}

// --- Bookmarks ---

export interface Bookmark {
  id: string
  message_id: string
  session_id: string
  note: string
  tags: string[]
  created_at: string
}

// --- Artifacts ---

export interface Artifact {
  id: string
  session_id: string
  name: string
  mime_type: string
  size: number
  created_at: string
}

// --- Tool Calls ---

export interface ToolCall {
  id: string
  tool: string
  status: 'running' | 'done' | 'error'
  summary?: string
}

// --- Tool Management ---

export interface ToolDefinition {
  name: string
  description: string
  input_schema: Record<string, unknown>
}

export interface ServerInfo {
  name: string
  tool_count: number
  connected: boolean
}

export interface DiscoveryDiff {
  added: string[]
  removed: string[]
  total: number
}

export interface ToolSelection {
  name: string
  description: string
  server?: string
}

export interface MCPServerConfig {
  id: string
  name: string
  transport_type: 'stdio' | 'sse'
  command: string
  url: string
  args: string
  env: string
  enabled: boolean
  created_at: string
  updated_at: string
}

// --- Volon (Sprint Planning) ---

export interface VolonSprint {
  id: string
  project_id: string
  name: string
  goal: string
  status: string
  start_date: string
  end_date: string
  created_at: string
  updated_at: string
}

export interface VolonTask {
  id: string
  sprint_id: string
  project_id: string
  title: string
  body: string
  priority: string
  status: string
  tags: string[]
  created_at: string
  updated_at: string
}

export interface VolonBacklogItem {
  id: string
  project_id: string
  title: string
  body: string
  priority: string
  tags: string[]
  created_at: string
}

// --- Plugins ---

export interface PluginInfo {
  name: string
  version: string
  description: string
  short_desc: string
  author: string
  url: string
  status: 'active' | 'disabled' | 'available' | 'no-binary'
  type: 'core' | 'user'
  installed: boolean
}

// --- Skills ---

export interface ToolBinding {
  server: string
  tool: string
}

export interface Skill {
  id: string
  name: string
  slug: string
  category: string
  description: string
  tool_bindings: string
  input_schema: string
  is_builtin: boolean
  settings: string
  created_at: string
  updated_at: string
}

// --- Prompt Templates ---

export interface TemplateVariable {
  name: string
  type: 'text' | 'textarea' | 'number' | 'boolean' | 'select'
  required: boolean
  default?: string | number | boolean
  description?: string
  options?: string[]
}

export interface PromptTemplate {
  id: string
  name: string
  slug: string
  scope: 'system' | 'mode' | 'skill' | 'context'
  template: string
  variables: string
  priority: number
  is_builtin: boolean
  created_at: string
  updated_at: string
}
