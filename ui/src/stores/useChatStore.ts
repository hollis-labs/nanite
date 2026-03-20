import { create } from 'zustand'
import type { ToolCall, ToolCallDisplayMode, ToolWarning, AgentMode, ChatError, ActiveStreamInfo, PendingToolInfo } from '@/lib/types'

interface ChatState {
  // Streaming
  isStreaming: boolean
  streamingContent: string
  streamingSessionId: string | null
  setStreaming: (streaming: boolean) => void
  setStreamingSessionId: (id: string | null) => void
  appendStreamContent: (content: string) => void
  clearStream: () => void

  // Status messages (transient, e.g. retry notifications)
  statusMessage: string | null
  setStatusMessage: (msg: string | null) => void

  // Tool calls
  toolCalls: ToolCall[]
  addToolCall: (tc: ToolCall) => void
  updateToolCall: (id: string, update: Partial<ToolCall>) => void
  clearToolCalls: () => void

  // Tool warnings
  toolWarnings: ToolWarning[]
  addToolWarning: (warning: ToolWarning) => void
  clearToolWarnings: () => void

  // Text-only mode (agent has 0 MCP tools)
  textOnlyMode: boolean
  setTextOnlyMode: (enabled: boolean) => void

  // Chat errors
  chatErrors: ChatError[]
  addChatError: (error: ChatError) => void
  dismissChatError: (id: string) => void
  clearChatErrors: () => void

  // Circuit breaker
  circuitOpen: boolean
  setCircuitOpen: (open: boolean) => void

  // Session takeover (another tab took this session's SSE connection)
  sessionTakeover: boolean
  setSessionTakeover: (taken: boolean) => void

  // Tool call display mode
  toolCallDisplayMode: ToolCallDisplayMode
  setToolCallDisplayMode: (mode: ToolCallDisplayMode) => void

  // Mode
  activeMode: AgentMode
  setActiveMode: (mode: AgentMode) => void

  // Model
  activeModel: string
  setActiveModel: (model: string) => void

  // Presence
  activeStreams: Map<string, ActiveStreamInfo>
  pendingTools: Map<string, PendingToolInfo>
  setActiveStream: (sessionId: string, info: ActiveStreamInfo) => void
  removeActiveStream: (sessionId: string) => void
  setPendingTool: (sessionId: string, info: PendingToolInfo) => void
  removePendingTool: (sessionId: string) => void
}

export const useChatStore = create<ChatState>((set) => ({
  // Streaming
  isStreaming: false,
  streamingContent: '',
  streamingSessionId: null,
  setStreaming: (streaming) => set({ isStreaming: streaming }),
  setStreamingSessionId: (id) => set({ streamingSessionId: id }),
  appendStreamContent: (content) =>
    set((state) => ({ streamingContent: state.streamingContent + content })),
  clearStream: () => set({ streamingContent: '', isStreaming: false, streamingSessionId: null, statusMessage: null }),

  // Status messages
  statusMessage: null,
  setStatusMessage: (msg) => set({ statusMessage: msg }),

  // Tool calls
  toolCalls: [],
  addToolCall: (tc) => set((state) => ({ toolCalls: [...state.toolCalls, tc] })),
  updateToolCall: (id, update) =>
    set((state) => ({
      toolCalls: state.toolCalls.map((tc) => (tc.id === id ? { ...tc, ...update } : tc)),
    })),
  clearToolCalls: () => set({ toolCalls: [] }),

  // Tool warnings
  toolWarnings: [],
  addToolWarning: (warning: ToolWarning) =>
    set((state: ChatState) => ({ toolWarnings: [...state.toolWarnings, warning] })),
  clearToolWarnings: () => set({ toolWarnings: [] }),

  // Text-only mode
  textOnlyMode: false,
  setTextOnlyMode: (enabled: boolean) => set({ textOnlyMode: enabled }),

  // Chat errors
  chatErrors: [],
  addChatError: (error: ChatError) =>
    set((state: ChatState) => ({ chatErrors: [...state.chatErrors, error] })),
  dismissChatError: (id: string) =>
    set((state: ChatState) => ({
      chatErrors: state.chatErrors.map((e: ChatError) =>
        e.id === id ? { ...e, dismissed: true } : e
      ),
    })),
  clearChatErrors: () => set({ chatErrors: [] }),

  // Circuit breaker
  circuitOpen: false,
  setCircuitOpen: (open: boolean) => set({ circuitOpen: open }),

  // Session takeover
  sessionTakeover: false,
  setSessionTakeover: (taken: boolean) => set({ sessionTakeover: taken }),

  // Tool call display mode
  toolCallDisplayMode: (typeof window !== 'undefined'
    ? localStorage.getItem('conduit:toolCallDisplayMode') as ToolCallDisplayMode
    : null) || 'minimal',
  setToolCallDisplayMode: (mode: ToolCallDisplayMode) => {
    localStorage.setItem('conduit:toolCallDisplayMode', mode)
    set({ toolCallDisplayMode: mode })
  },

  // Mode
  activeMode: 'default' as AgentMode,
  setActiveMode: (mode: AgentMode) => set({ activeMode: mode }),

  // Model
  activeModel: 'claude-sonnet-4-20250514',
  setActiveModel: (model: string) => set({ activeModel: model }),

  // Presence
  activeStreams: new Map(),
  pendingTools: new Map(),
  setActiveStream: (sessionId, info) =>
    set((state) => {
      const next = new Map(state.activeStreams)
      next.set(sessionId, info)
      return { activeStreams: next }
    }),
  removeActiveStream: (sessionId) =>
    set((state) => {
      const next = new Map(state.activeStreams)
      next.delete(sessionId)
      return { activeStreams: next }
    }),
  setPendingTool: (sessionId, info) =>
    set((state) => {
      const next = new Map(state.pendingTools)
      next.set(sessionId, info)
      return { pendingTools: next }
    }),
  removePendingTool: (sessionId) =>
    set((state) => {
      const next = new Map(state.pendingTools)
      next.delete(sessionId)
      return { pendingTools: next }
    }),
}))
