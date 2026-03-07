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
  type: 'stream_start' | 'delta' | 'stream_end' | 'error'
  content?: string
  message_id?: string
  agent_id?: string
  usage?: { input_tokens: number; output_tokens: number; stop_reason: string }
  error?: string
}
