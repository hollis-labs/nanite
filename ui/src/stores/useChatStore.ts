import { create } from 'zustand'
import type { ToolCall, AgentMode } from '@/lib/types'

interface ChatState {
  // Streaming
  isStreaming: boolean
  streamingContent: string
  streamingSessionId: string | null
  setStreaming: (streaming: boolean) => void
  setStreamingSessionId: (id: string | null) => void
  appendStreamContent: (content: string) => void
  clearStream: () => void

  // Tool calls
  toolCalls: ToolCall[]
  addToolCall: (tc: ToolCall) => void
  updateToolCall: (id: string, update: Partial<ToolCall>) => void
  clearToolCalls: () => void

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
  clearStream: () => set({ streamingContent: '', isStreaming: false, streamingSessionId: null }),

  // Tool calls
  toolCalls: [],
  addToolCall: (tc) => set((state) => ({ toolCalls: [...state.toolCalls, tc] })),
  updateToolCall: (id, update) =>
    set((state) => ({
      toolCalls: state.toolCalls.map((tc) => (tc.id === id ? { ...tc, ...update } : tc)),
    })),
  clearToolCalls: () => set({ toolCalls: [] }),

  // Mode
  activeMode: 'default',
  setActiveMode: (mode) => set({ activeMode: mode }),

  // Model
  activeModel: 'claude-sonnet-4-20250514',
  setActiveModel: (model) => set({ activeModel: model }),
}))
