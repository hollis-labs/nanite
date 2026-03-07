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

export interface StreamEvent {
  type: 'stream_start' | 'delta' | 'stream_end' | 'error' | 'tool_call' | 'tool_result'
  content?: string
  message_id?: string
  agent_id?: string
  usage?: { input_tokens: number; output_tokens: number; stop_reason: string }
  error?: string
  tool?: string
  summary?: string
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
}

export const AVAILABLE_MODELS: ModelOption[] = [
  { id: 'claude-sonnet-4-20250514', label: 'Claude Sonnet 4' },
  { id: 'claude-opus-4-20250514', label: 'Claude Opus 4' },
  { id: 'claude-haiku-35-20241022', label: 'Claude Haiku' },
]

// --- Envelopes ---

export interface Envelope {
  kind: string
  version: number
  type: string
  proposals?: Proposal[]
  questions?: Question[]
  approval?: ApprovalRequest
  status?: { phase: string; progress: number }
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
