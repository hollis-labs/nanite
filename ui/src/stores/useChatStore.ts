import { create } from 'zustand'
import type { ToolCall, ToolCallDisplayMode, ToolWarning, AgentMode, ChatError, ActiveStreamInfo, PendingToolInfo, CLIActiveInfo, PendingApproval, PluginEnvelopeItem } from '@/lib/types'

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

  // Tool calls (session-scoped retention)
  toolCalls: ToolCall[]
  toolCallsBySession: Map<string, { calls: ToolCall[]; lastActivity: number }>
  addToolCall: (tc: ToolCall, sessionId?: string) => void
  updateToolCall: (id: string, update: Partial<ToolCall>, sessionId?: string) => void
  clearToolCalls: () => void
  loadSessionToolCalls: (sessionId: string | null, retentionMinutes?: number) => void

  // Tool warnings
  toolWarnings: ToolWarning[]
  addToolWarning: (warning: ToolWarning) => void
  clearToolWarnings: () => void

  // Pending approvals (vNext permission system)
  pendingApprovals: PendingApproval[]
  addPendingApproval: (approval: PendingApproval) => void
  resolvePendingApproval: (requestId: string, decision: PendingApproval['resolved']) => void
  clearPendingApprovals: () => void

  // Plugin envelopes — standalone cards emitted by plugin event hooks
  // (BLG-20260413-012 / BLG-20260414-010). Session-scoped so switching
  // sessions does not mix envelopes from different conversations.
  pluginEnvelopes: PluginEnvelopeItem[]
  pluginEnvelopesBySession: Map<string, { items: PluginEnvelopeItem[]; lastActivity: number }>
  addPluginEnvelope: (item: PluginEnvelopeItem, sessionId?: string) => void
  clearPluginEnvelopes: () => void
  loadSessionPluginEnvelopes: (sessionId: string | null, retentionMinutes?: number) => void

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

  // Stream stalled watchdog — flipped when the SSE connection delivers no
  // events for an extended period while streaming is still marked active.
  streamStalled: boolean
  setStreamStalled: (stalled: boolean) => void

  // Tool call display mode (per-session override)
  toolCallDisplayMode: ToolCallDisplayMode
  setToolCallDisplayMode: (mode: ToolCallDisplayMode) => void
  loadToolCallDisplayMode: (sessionId: string | null) => void
  saveToolCallDisplayMode: (sessionId: string | null, mode: ToolCallDisplayMode) => void

  // Mode
  activeMode: AgentMode
  setActiveMode: (mode: AgentMode) => void

  // Model
  activeModel: string
  setActiveModel: (model: string) => void

  // Presence
  activeStreams: Map<string, ActiveStreamInfo>
  pendingTools: Map<string, PendingToolInfo>
  cliActiveSessions: Map<string, CLIActiveInfo>
  setActiveStream: (sessionId: string, info: ActiveStreamInfo) => void
  removeActiveStream: (sessionId: string) => void
  setPendingTool: (sessionId: string, info: PendingToolInfo) => void
  removePendingTool: (sessionId: string) => void
  setCLIActive: (sessionId: string, info: CLIActiveInfo) => void
  removeCLIActive: (sessionId: string) => void

  // Cross-session jump-to-message (search result navigation)
  pendingJump: { sessionId: string; messageId: string } | null
  setPendingJump: (jump: { sessionId: string; messageId: string } | null) => void
  scrollToMessageId: string | null
  setScrollToMessageId: (id: string | null) => void
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
  clearStream: () => set({ streamingContent: '', isStreaming: false, streamingSessionId: null, statusMessage: null, streamStalled: false }),

  // Status messages
  statusMessage: null,
  setStatusMessage: (msg) => set({ statusMessage: msg }),

  // Tool calls (session-scoped retention)
  toolCalls: [],
  toolCallsBySession: new Map(),
  addToolCall: (tc, sessionId?) =>
    set((state) => {
      const targetSession = sessionId ?? state.streamingSessionId
      const next = new Map(state.toolCallsBySession)
      const existing = targetSession ? next.get(targetSession)?.calls ?? [] : state.toolCalls
      // CW-20260419-0014: upsert by id. SSE replay on reconnect (ring
      // buffer + Last-Event-ID) should prevent duplicates at the wire
      // level, but this is defense-in-depth — same id means update the
      // existing entry, not append. Prevents the 14→28 doubling we saw
      // in UAT c13/c14 when the browser auto-reconnected without a
      // cursor.
      const existingIdx = existing.findIndex((x) => x.id === tc.id)
      const updated =
        existingIdx >= 0
          ? existing.map((x, i) => (i === existingIdx ? { ...x, ...tc } : x)).slice(-50)
          : [...existing, tc].slice(-50)
      if (targetSession) {
        next.set(targetSession, { calls: updated, lastActivity: Date.now() })
      }
      // Only update the displayed toolCalls if this is the active session
      const displayUpdate = targetSession === state.streamingSessionId ? { toolCalls: updated } : {}
      return { ...displayUpdate, toolCallsBySession: next }
    }),
  updateToolCall: (id, update, sessionId?) =>
    set((state) => {
      const targetSession = sessionId ?? state.streamingSessionId
      const next = new Map(state.toolCallsBySession)
      const existing = targetSession ? next.get(targetSession)?.calls ?? [] : state.toolCalls
      const updated = existing.map((tc) => (tc.id === id ? { ...tc, ...update } : tc))
      if (targetSession) {
        next.set(targetSession, { calls: updated, lastActivity: Date.now() })
      }
      const displayUpdate = targetSession === state.streamingSessionId ? { toolCalls: updated } : {}
      return { ...displayUpdate, toolCallsBySession: next }
    }),
  clearToolCalls: () =>
    set((state) => {
      const sessionId = state.streamingSessionId
      if (sessionId) {
        const next = new Map(state.toolCallsBySession)
        next.set(sessionId, { calls: [], lastActivity: Date.now() })
        return { toolCalls: [], toolCallsBySession: next }
      }
      return { toolCalls: [] }
    }),
  loadSessionToolCalls: (sessionId, retentionMinutes = 15) =>
    set((state) => {
      if (!sessionId) return { toolCalls: [] }
      const next = new Map(state.toolCallsBySession)
      // Prune stale entries only when retention is non-negative; negative values keep until refresh
      if (retentionMinutes >= 0) {
        const cutoff = Date.now() - retentionMinutes * 60 * 1000
        for (const [id, entry] of next) {
          if (entry.lastActivity < cutoff) next.delete(id)
        }
      }
      const entry = next.get(sessionId)
      return {
        toolCalls: entry?.calls ?? [],
        toolCallsBySession: next,
      }
    }),

  // Tool warnings
  toolWarnings: [],
  addToolWarning: (warning: ToolWarning) =>
    set((state: ChatState) => ({ toolWarnings: [...state.toolWarnings, warning] })),
  clearToolWarnings: () => set({ toolWarnings: [] }),

  // Pending approvals
  pendingApprovals: [],
  addPendingApproval: (approval: PendingApproval) =>
    set((state: ChatState) => ({ pendingApprovals: [...state.pendingApprovals, approval] })),
  resolvePendingApproval: (requestId: string, decision: PendingApproval['resolved']) =>
    set((state: ChatState) => ({
      pendingApprovals: state.pendingApprovals.map((a) =>
        a.request_id === requestId ? { ...a, resolved: decision } : a
      ),
    })),
  clearPendingApprovals: () => set({ pendingApprovals: [] }),

  // Plugin envelopes (session-scoped, mirrors toolCallsBySession retention)
  pluginEnvelopes: [],
  pluginEnvelopesBySession: new Map(),
  addPluginEnvelope: (item, sessionId) =>
    set((state) => {
      const targetSession = sessionId ?? state.streamingSessionId
      if (!targetSession) return {}
      const next = new Map(state.pluginEnvelopesBySession)
      const existing = next.get(targetSession)?.items ?? []
      const updated = [...existing, item].slice(-50)
      next.set(targetSession, { items: updated, lastActivity: Date.now() })
      const displayUpdate =
        targetSession === state.streamingSessionId ? { pluginEnvelopes: updated } : {}
      return { ...displayUpdate, pluginEnvelopesBySession: next }
    }),
  clearPluginEnvelopes: () =>
    set((state) => {
      const sessionId = state.streamingSessionId
      if (sessionId) {
        const next = new Map(state.pluginEnvelopesBySession)
        next.set(sessionId, { items: [], lastActivity: Date.now() })
        return { pluginEnvelopes: [], pluginEnvelopesBySession: next }
      }
      return { pluginEnvelopes: [] }
    }),
  loadSessionPluginEnvelopes: (sessionId, retentionMinutes = 15) =>
    set((state) => {
      if (!sessionId) return { pluginEnvelopes: [] }
      const next = new Map(state.pluginEnvelopesBySession)
      // Prune stale entries (mirrors loadSessionToolCalls). Negative
      // retention disables pruning (keep until refresh).
      if (retentionMinutes >= 0) {
        const cutoff = Date.now() - retentionMinutes * 60 * 1000
        for (const [id, entry] of next) {
          if (entry.lastActivity < cutoff) next.delete(id)
        }
      }
      const entry = next.get(sessionId)
      return {
        pluginEnvelopes: entry?.items ?? [],
        pluginEnvelopesBySession: next,
      }
    }),

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

  // Stream stalled watchdog
  streamStalled: false,
  setStreamStalled: (stalled: boolean) => set({ streamStalled: stalled }),

  // Tool call display mode — per-session override stored in localStorage
  toolCallDisplayMode: (typeof window !== 'undefined'
    ? localStorage.getItem('nanite:toolCallDisplayMode') as ToolCallDisplayMode
    : null) || 'minimal',
  setToolCallDisplayMode: (mode: ToolCallDisplayMode) => {
    if (typeof window !== 'undefined') {
      localStorage.setItem('nanite:toolCallDisplayMode', mode)
    }
    set({ toolCallDisplayMode: mode })
  },
  loadToolCallDisplayMode: (sessionId: string | null) => {
    if (!sessionId || typeof window === 'undefined') return
    const sessionMode = localStorage.getItem(`nanite:tcMode:${sessionId}`) as ToolCallDisplayMode | null
    const globalMode = localStorage.getItem('nanite:toolCallDisplayMode') as ToolCallDisplayMode | null
    set({ toolCallDisplayMode: sessionMode || globalMode || 'minimal' })
  },
  saveToolCallDisplayMode: (sessionId: string | null, mode: ToolCallDisplayMode) => {
    if (typeof window !== 'undefined') {
      localStorage.setItem('nanite:toolCallDisplayMode', mode)
      if (sessionId) {
        localStorage.setItem(`nanite:tcMode:${sessionId}`, mode)
      }
    }
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
  cliActiveSessions: new Map(),
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
  setCLIActive: (sessionId, info) =>
    set((state) => {
      const next = new Map(state.cliActiveSessions)
      next.set(sessionId, info)
      return { cliActiveSessions: next }
    }),
  removeCLIActive: (sessionId) =>
    set((state) => {
      const next = new Map(state.cliActiveSessions)
      next.delete(sessionId)
      return { cliActiveSessions: next }
    }),

  // Cross-session jump-to-message
  pendingJump: null,
  setPendingJump: (jump) => set({ pendingJump: jump }),
  scrollToMessageId: null,
  setScrollToMessageId: (id) => set({ scrollToMessageId: id }),
}))
