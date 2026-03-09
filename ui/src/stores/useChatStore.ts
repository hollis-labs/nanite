import { create } from 'zustand'
import type { ToolCall, AgentMode, ChatError } from '@/lib/types'

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

  // Chat errors
  chatErrors: ChatError[]
  addChatError: (error: ChatError) => void
  dismissChatError: (id: string) => void
  clearChatErrors: () => void

  // Circuit breaker
  circuitOpen: boolean
  setCircuitOpen: (open: boolean) => void

  // Mode
  activeMode: AgentMode
  setActiveMode: (mode: AgentMode) => void

  // Model
  activeModel: string
  setActiveModel: (model: string) => void
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

  // Mode
  activeMode: 'default' as AgentMode,
  setActiveMode: (mode: AgentMode) => set({ activeMode: mode }),

  // Model
  activeModel: 'claude-sonnet-4-20250514',
  setActiveModel: (model: string) => set({ activeModel: model }),
}))
