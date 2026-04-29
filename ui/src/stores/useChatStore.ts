import { create } from 'zustand'
import type { ToolCall, ToolCallDisplayMode, ToolWarning, AgentMode, ChatError, ActiveStreamInfo, PendingToolInfo, CLIActiveInfo, PendingApproval, PluginEnvelopeItem, ModeSuggestion, ModeAutoSwitchOverride, ModeAutoSwitchEffective } from '@/lib/types'

/**
 * B3 (CW-20260428-0011) — pure resolver for the effective auto-switch
 * behavior. Exported so it can be unit-tested independently of the store.
 *
 * Semantics:
 *   - session override "off"      → "off"  (suppress all auto-switches)
 *   - global pref ""  (unset)     → "firstUse"  (show 5-option card)
 *     even when override is "on"  →  the user must make the choice once
 *   - global pref "never"         → "off"
 *   - global pref "always"        → "auto"
 *   - global pref "ask"           → "ask"
 */
export function getAutoSwitchEffective(
  override: ModeAutoSwitchOverride | undefined,
  pref: '' | 'always' | 'ask' | 'never' | undefined,
): ModeAutoSwitchEffective {
  if (override === 'off') return 'off'
  const p = pref ?? ''
  if (p === '') return 'firstUse'
  if (p === 'never') return 'off'
  if (p === 'always') return 'auto'
  return 'ask'
}

interface ChatState {
  // Streaming
  isStreaming: boolean
  streamingContent: string
  /** F4 (CW-20260419-0029) — inter-iteration narration text. Live during streaming.
   *  Collapses to a pill after stream_end. Empty when the turn had no tool calls. */
  streamingNarration: string
  /** F4 — post-end_turn final answer text. This becomes the assistant bubble. */
  streamingFinal: string
  /** F3 (CW-20260420-0023) — interleaved thinking text. Live during streaming.
   *  Shown in the "Working…" strip alongside narration. Collapses to the pill
   *  post-stream, rendered with a distinct "thinking" badge. */
  streamingThinking: string
  streamingSessionId: string | null
  setStreaming: (streaming: boolean) => void
  setStreamingSessionId: (id: string | null) => void
  appendStreamContent: (content: string) => void
  appendStreamNarration: (content: string) => void
  appendStreamFinal: (content: string) => void
  appendStreamThinking: (content: string) => void
  replaceStreamContent: (content: string) => void
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

  // Effort (F1 / CW-20260420-0014) — per-turn token-budget + reasoning dial.
  // Values: "low" | "normal" | "high" | "max". Default: "normal".
  activeEffort: string
  setActiveEffort: (effort: string) => void

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

  // B2 (CW-20260428-0010) — non-binding mode classifier suggestion from the
  // backend. Populated by the SSE `mode_suggestion` event in useChat.
  // B3 will hook into this to render the confirm-card / auto-apply UX;
  // B2 only stages the value.
  pendingModeSuggestion: ModeSuggestion | null
  setPendingModeSuggestion: (s: ModeSuggestion | null) => void
  clearModeSuggestion: () => void

  // B3 (CW-20260428-0011) — per-session override for auto-mode-switching.
  // Absence = inherit global pref. "off" suppresses all auto-switches for
  // the session; "on" lets the global pref take effect (does NOT bypass
  // first-use prompt). Resets on full page reload by design.
  autoSwitchSessionOverrides: Record<string, ModeAutoSwitchOverride>
  setAutoSwitchOverride: (sessionId: string, override: ModeAutoSwitchOverride | 'inherit') => void

  // B3 (CW-20260428-0011) — lightweight chat-scoped toast (e.g. "Switched
  // to plan mode"). Mirrors useWorkStore.toastMessage shape but separately
  // owned so chat surfaces don't depend on the work store.
  chatToast: { message: string; tone: 'success' | 'info' } | null
  showChatToast: (message: string, tone?: 'success' | 'info') => void
  dismissChatToast: () => void
}

export const useChatStore = create<ChatState>((set) => ({
  // Streaming
  isStreaming: false,
  streamingContent: '',
  streamingNarration: '',
  streamingFinal: '',
  streamingThinking: '',
  streamingSessionId: null,
  setStreaming: (streaming) => set({ isStreaming: streaming }),
  setStreamingSessionId: (id) => set({ streamingSessionId: id }),
  appendStreamContent: (content) =>
    set((state) => ({ streamingContent: state.streamingContent + content })),
  appendStreamNarration: (content) =>
    set((state) => ({ streamingNarration: state.streamingNarration + content })),
  appendStreamFinal: (content) =>
    set((state) => ({
      streamingFinal: state.streamingFinal + content,
      // Keep streamingContent in sync with final text so legacy consumers
      // (e.g. ChatTranscript's streamingContent prop) render the answer.
      streamingContent: state.streamingFinal + content,
    })),
  appendStreamThinking: (content) =>
    set((state) => ({ streamingThinking: state.streamingThinking + content })),
  replaceStreamContent: (content) => set({ streamingContent: content, streamingFinal: content }),
  clearStream: () => set({
    streamingContent: '',
    streamingNarration: '',
    streamingFinal: '',
    streamingThinking: '',
    isStreaming: false,
    streamingSessionId: null,
    statusMessage: null,
    streamStalled: false,
  }),

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

  // Effort
  activeEffort: 'normal',
  setActiveEffort: (effort: string) => set({ activeEffort: effort }),

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

  // B2 — pending mode suggestion (consumed by B3 confirm-card / auto-apply).
  pendingModeSuggestion: null,
  setPendingModeSuggestion: (s) => set({ pendingModeSuggestion: s }),
  clearModeSuggestion: () => set({ pendingModeSuggestion: null }),

  // B3 — per-session override map.
  autoSwitchSessionOverrides: {},
  setAutoSwitchOverride: (sessionId, override) =>
    set((state) => {
      const next = { ...state.autoSwitchSessionOverrides }
      if (override === 'inherit') {
        delete next[sessionId]
      } else {
        next[sessionId] = override
      }
      return { autoSwitchSessionOverrides: next }
    }),

  // B3 — chat toast slice.
  chatToast: null,
  showChatToast: (message, tone = 'success') => set({ chatToast: { message, tone } }),
  dismissChatToast: () => set({ chatToast: null }),
}))
