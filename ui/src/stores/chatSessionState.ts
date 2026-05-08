import type {
  AgentMode,
  ChatError,
  ModeSuggestion,
  PendingApproval,
  PluginEnvelopeItem,
  ToolCall,
  ToolWarning,
} from '@/lib/types'

/**
 * G-FE-SINGLETON: per-session slice for chat state.
 *
 * One slice per `sessionID` lives inside `useChatStore.sessions`. Replaces the
 * previous singleton fields (`isStreaming`, `streamingContent`, `chatErrors`,
 * `activeMode/Model/Effort`, etc.) that bled across concurrent sessions.
 */
export interface ChatSessionState {
  // Streaming
  isStreaming: boolean
  streamingContent: string
  streamingNarration: string
  streamingFinal: string
  streamingThinking: string

  // Status / banners
  statusMessage: string | null
  circuitOpen: boolean
  sessionTakeover: boolean
  streamStalled: boolean
  textOnlyMode: boolean

  // Errors / approvals / warnings
  chatErrors: ChatError[]
  pendingApprovals: PendingApproval[]
  toolWarnings: ToolWarning[]
  pendingModeSuggestion: ModeSuggestion | null

  // Per-session dials
  activeMode: AgentMode
  activeModel: string
  activeEffort: string

  // Already session-keyed; folded into the slice for shape consistency
  toolCalls: ToolCall[]
  pluginEnvelopes: PluginEnvelopeItem[]
  toolCallsLastActivity: number
  pluginEnvelopesLastActivity: number

  // LRU bookkeeping
  lastActivityAt: number
}

export const DEFAULT_ACTIVE_MODE: AgentMode = 'default'
export const DEFAULT_ACTIVE_MODEL = 'claude-sonnet-4-20250514'
export const DEFAULT_ACTIVE_EFFORT = 'normal'

export function emptyChatSessionState(now: number = Date.now()): ChatSessionState {
  return {
    isStreaming: false,
    streamingContent: '',
    streamingNarration: '',
    streamingFinal: '',
    streamingThinking: '',
    statusMessage: null,
    circuitOpen: false,
    sessionTakeover: false,
    streamStalled: false,
    textOnlyMode: false,
    chatErrors: [],
    pendingApprovals: [],
    toolWarnings: [],
    pendingModeSuggestion: null,
    activeMode: DEFAULT_ACTIVE_MODE,
    activeModel: DEFAULT_ACTIVE_MODEL,
    activeEffort: DEFAULT_ACTIVE_EFFORT,
    toolCalls: [],
    pluginEnvelopes: [],
    toolCallsLastActivity: now,
    pluginEnvelopesLastActivity: now,
    lastActivityAt: now,
  }
}

/**
 * Frozen empty slice returned by selectors when the queried session is not
 * present in the map. Lets components consume `useActiveChatSession()` etc.
 * without null checks. Reference-stable to avoid unnecessary re-renders.
 */
export const EMPTY_CHAT_SESSION_STATE: Readonly<ChatSessionState> = Object.freeze({
  ...emptyChatSessionState(0),
})

/**
 * Soft cap on retained per-session slices. Beyond this we LRU-evict on
 * `ensureSession` so a long-running browser tab does not accumulate state for
 * every session it has ever attached. Chat history is loaded from the backend
 * on demand, so eviction does not lose user data — it only clears in-memory
 * UI state for the evicted session.
 */
export const MAX_RETAINED_SESSIONS = 20
