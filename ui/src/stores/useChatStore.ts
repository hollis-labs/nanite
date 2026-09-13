import { create } from "zustand";
import {
  readTranscriptPosition,
  type TranscriptPosition,
  writeTranscriptPosition,
} from "@/lib/transcript-position";
import type {
  ActiveStreamInfo,
  ChatError,
  CLIActiveInfo,
  PendingApproval,
  PendingToolInfo,
  PluginEnvelopeItem,
  ToolCall,
  ToolCallDisplayMode,
  ToolWarning,
} from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import {
  type ChatSessionState,
  EMPTY_CHAT_SESSION_STATE,
  emptyChatSessionState,
  MAX_RETAINED_SESSIONS,
} from "./chatSessionState";

function safeLocalStorageGet(key: string): string | null {
  if (typeof localStorage === "undefined") return null;
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function safeLocalStorageSet(key: string, value: string) {
  if (typeof localStorage === "undefined") return;
  try {
    localStorage.setItem(key, value);
  } catch {
    // Ignore storage failures in non-browser / restricted contexts.
  }
}

interface ChatStore {
  /** Per-session state slices (G-FE-SINGLETON). */
  sessions: Map<string, ChatSessionState>;

  // ── Lifecycle ──
  ensureSession: (sessionID: string) => void;
  removeSession: (sessionID: string) => void;
  clearStreaming: (sessionID: string) => void;

  // ── Streaming writers ──
  setStreaming: (sessionID: string, streaming: boolean) => void;
  setStreamCursor: (sessionID: string, messageId: string, cursor: number) => void;
  appendStreamContent: (sessionID: string, content: string) => void;
  appendStreamNarration: (sessionID: string, content: string) => void;
  appendStreamFinal: (sessionID: string, content: string) => void;
  appendStreamThinking: (sessionID: string, content: string) => void;
  replaceStreamContent: (sessionID: string, content: string) => void;

  // ── Status / banners ──
  setStatusMessage: (sessionID: string, msg: string | null) => void;
  setCircuitOpen: (sessionID: string, open: boolean) => void;
  setSessionTakeover: (sessionID: string, taken: boolean) => void;
  setTextOnlyMode: (sessionID: string, enabled: boolean) => void;
  /** CW-20260518-0084 — raise/clear the restart-killed-turn indicator. */
  setInterruptedTurn: (sessionID: string, interrupted: boolean) => void;

  // ── Tool calls ──
  addToolCall: (sessionID: string, tc: ToolCall) => void;
  updateToolCall: (sessionID: string, id: string, update: Partial<ToolCall>) => void;
  clearToolCalls: (sessionID: string) => void;
  /** Prune stale per-session tool-call lists by retention TTL (in minutes). Negative disables pruning. */
  pruneToolCallRetention: (retentionMinutes: number) => void;

  // ── Tool warnings ──
  addToolWarning: (sessionID: string, warning: ToolWarning) => void;
  clearToolWarnings: (sessionID: string) => void;

  // ── Pending approvals ──
  addPendingApproval: (sessionID: string, approval: PendingApproval) => void;
  resolvePendingApproval: (
    sessionID: string,
    requestId: string,
    decision: PendingApproval["resolved"],
  ) => void;
  clearPendingApprovals: (sessionID: string) => void;

  // ── Plugin envelopes ──
  addPluginEnvelope: (sessionID: string, item: PluginEnvelopeItem) => void;
  setPluginEnvelopes: (sessionID: string, items: PluginEnvelopeItem[]) => void;
  clearPluginEnvelopes: (sessionID: string) => void;
  pruneEnvelopeRetention: (retentionMinutes: number) => void;

  // ── Errors ──
  addChatError: (sessionID: string, error: ChatError) => void;
  dismissChatError: (sessionID: string, id: string) => void;
  clearChatErrors: (sessionID: string) => void;

  // ── Per-session dials ──
  setActiveModel: (sessionID: string, model: string) => void;
  setActiveEffort: (sessionID: string, effort: string) => void;

  // ── Composer ──
  setComposerDraft: (sessionID: string, draft: string) => void;
  clearComposerDraft: (sessionID: string) => void;
  getTranscriptPosition: (sessionID: string) => TranscriptPosition | null;
  saveTranscriptPosition: (sessionID: string, position: TranscriptPosition) => void;

  // ── Genuinely cross-session state (stays global) ──
  /** Tool-call display preference. User-level pref with optional per-session override (localStorage-backed). */
  toolCallDisplayMode: ToolCallDisplayMode;
  setToolCallDisplayMode: (mode: ToolCallDisplayMode) => void;
  loadToolCallDisplayMode: (sessionID: string | null) => void;
  saveToolCallDisplayMode: (sessionID: string | null, mode: ToolCallDisplayMode) => void;

  /** Multi-session presence — sidebar surfaces across all sessions. Keyed by sessionID. */
  activeStreams: Map<string, ActiveStreamInfo>;
  pendingTools: Map<string, PendingToolInfo>;
  cliActiveSessions: Map<string, CLIActiveInfo>;
  setActiveStream: (sessionID: string, info: ActiveStreamInfo) => void;
  removeActiveStream: (sessionID: string) => void;
  setPendingTool: (sessionID: string, info: PendingToolInfo) => void;
  removePendingTool: (sessionID: string) => void;
  setCLIActive: (sessionID: string, info: CLIActiveInfo) => void;
  removeCLIActive: (sessionID: string) => void;

  /** Cross-session jump-to-message (search result navigation). */
  pendingJump: { sessionId: string; messageId: string } | null;
  setPendingJump: (jump: { sessionId: string; messageId: string } | null) => void;
  scrollToMessageId: string | null;
  setScrollToMessageId: (id: string | null) => void;

  /**
   * Chat-scoped toast (e.g. "Switched to plan mode").
   *
   * Intentional cross-session global per the locked decision in the
   * fe-singleton-refactor implementer prompt: a 3s auto-dismiss toast that the
   * user will visually consume before any session switch matters. Double-routing
   * to the wrong session is a non-issue given the fleeting lifetime, and the
   * alternative (one toast per session) loses the simple "show one thing at the
   * bottom" UX.
   */
  chatToast: { message: string; tone: "success" | "info" } | null;
  showChatToast: (message: string, tone?: "success" | "info") => void;
  dismissChatToast: () => void;
}

// ── Internal helpers ──

function evictLRUIfNeeded(sessions: Map<string, ChatSessionState>): Map<string, ChatSessionState> {
  if (sessions.size <= MAX_RETAINED_SESSIONS) return sessions;
  const sorted = Array.from(sessions.entries()).sort(
    ([, a], [, b]) => a.lastActivityAt - b.lastActivityAt,
  );
  const evictCount = sessions.size - MAX_RETAINED_SESSIONS;
  const next = new Map(sessions);
  for (let i = 0; i < evictCount; i++) {
    const entry = sorted[i];
    if (entry) next.delete(entry[0]);
  }
  return next;
}

/**
 * Apply a partial update to the slice for `sessionID`. Auto-creates the slice
 * if missing — writers always have a real sessionID, and forcing every caller
 * to ensureSession first would be noisy. Does NOT enforce the LRU cap on
 * mutation paths: that runs only on explicit ensureSession to avoid evicting
 * an actively-mounted session while writes flow.
 */
function applyToSession(
  sessions: Map<string, ChatSessionState>,
  sessionID: string,
  patch: Partial<ChatSessionState>,
): Map<string, ChatSessionState> {
  const current = sessions.get(sessionID) ?? emptyChatSessionState();
  const updated: ChatSessionState = {
    ...current,
    ...patch,
    lastActivityAt: Date.now(),
  };
  const next = new Map(sessions);
  next.set(sessionID, updated);
  return next;
}

export const useChatStore = create<ChatStore>((set, get) => ({
  sessions: new Map(),
  getTranscriptPosition: (sessionID) =>
    get().sessions.get(sessionID)?.transcriptPosition ?? readTranscriptPosition(sessionID),
  saveTranscriptPosition: (sessionID, position) => {
    writeTranscriptPosition(sessionID, position);
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, { transcriptPosition: position }),
    }));
  },

  // ── Lifecycle ──
  ensureSession: (sessionID) =>
    set((state) => {
      if (state.sessions.has(sessionID)) return {};
      const next = new Map(state.sessions);
      next.set(sessionID, emptyChatSessionState());
      return { sessions: evictLRUIfNeeded(next) };
    }),

  removeSession: (sessionID) =>
    set((state) => {
      if (!state.sessions.has(sessionID)) return {};
      const next = new Map(state.sessions);
      next.delete(sessionID);
      return { sessions: next };
    }),

  clearStreaming: (sessionID) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, {
        isStreaming: false,
        streamingContent: "",
        streamingNarration: "",
        streamingFinal: "",
        streamingThinking: "",
        streamMessageId: null,
        streamCursor: 0,
        statusMessage: null,
      }),
    })),

  // ── Streaming writers ──
  setStreamCursor: (sessionID, messageId, cursor) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, {
        streamMessageId: messageId,
        streamCursor: cursor,
      }),
    })),
  setStreaming: (sessionID, streaming) =>
    set((state) => ({
      // CW-20260518-0084: starting a fresh turn is the "send-to-resume" action
      // — it cold-boots a new agent, so any prior interrupted-turn indicator
      // is now stale and must be cleared.
      sessions: applyToSession(state.sessions, sessionID, {
        isStreaming: streaming,
        ...(streaming ? { interruptedTurn: false } : {}),
      }),
    })),

  appendStreamContent: (sessionID, content) =>
    set((state) => {
      const slice = state.sessions.get(sessionID) ?? emptyChatSessionState();
      return {
        sessions: applyToSession(state.sessions, sessionID, {
          streamingContent: slice.streamingContent + content,
        }),
      };
    }),

  appendStreamNarration: (sessionID, content) =>
    set((state) => {
      const slice = state.sessions.get(sessionID) ?? emptyChatSessionState();
      return {
        sessions: applyToSession(state.sessions, sessionID, {
          streamingNarration: slice.streamingNarration + content,
        }),
      };
    }),

  appendStreamFinal: (sessionID, content) =>
    set((state) => {
      const slice = state.sessions.get(sessionID) ?? emptyChatSessionState();
      const nextFinal = slice.streamingFinal + content;
      // Keep streamingContent in sync with final text so consumers reading
      // streamingContent (e.g. ChatTranscript) render the answer.
      return {
        sessions: applyToSession(state.sessions, sessionID, {
          streamingFinal: nextFinal,
          streamingContent: nextFinal,
        }),
      };
    }),

  appendStreamThinking: (sessionID, content) =>
    set((state) => {
      const slice = state.sessions.get(sessionID) ?? emptyChatSessionState();
      return {
        sessions: applyToSession(state.sessions, sessionID, {
          streamingThinking: slice.streamingThinking + content,
        }),
      };
    }),

  replaceStreamContent: (sessionID, content) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, {
        streamingContent: content,
        streamingFinal: content,
      }),
    })),

  // ── Status / banners ──
  setStatusMessage: (sessionID, msg) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, { statusMessage: msg }),
    })),
  setCircuitOpen: (sessionID, open) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, { circuitOpen: open }),
    })),
  setSessionTakeover: (sessionID, taken) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, { sessionTakeover: taken }),
    })),
  setTextOnlyMode: (sessionID, enabled) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, { textOnlyMode: enabled }),
    })),
  setInterruptedTurn: (sessionID, interrupted) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, { interruptedTurn: interrupted }),
    })),

  // ── Tool calls ──
  addToolCall: (sessionID, tc) =>
    set((state) => {
      const slice = state.sessions.get(sessionID) ?? emptyChatSessionState();
      // CW-20260419-0014: upsert by id. Defense-in-depth for SSE replay on
      // reconnect — same id means update, not append.
      const existingIdx = slice.toolCalls.findIndex((x) => x.id === tc.id);
      const updated =
        existingIdx >= 0
          ? slice.toolCalls.map((x, i) => (i === existingIdx ? { ...x, ...tc } : x)).slice(-50)
          : [...slice.toolCalls, tc].slice(-50);
      return {
        sessions: applyToSession(state.sessions, sessionID, {
          toolCalls: updated,
          toolCallsLastActivity: Date.now(),
        }),
      };
    }),

  updateToolCall: (sessionID, id, update) =>
    set((state) => {
      const slice = state.sessions.get(sessionID) ?? emptyChatSessionState();
      const updated = slice.toolCalls.map((tc) => (tc.id === id ? { ...tc, ...update } : tc));
      return {
        sessions: applyToSession(state.sessions, sessionID, {
          toolCalls: updated,
          toolCallsLastActivity: Date.now(),
        }),
      };
    }),

  clearToolCalls: (sessionID) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, {
        toolCalls: [],
        toolCallsLastActivity: Date.now(),
      }),
    })),

  pruneToolCallRetention: (retentionMinutes) =>
    set((state) => {
      if (retentionMinutes < 0) return {};
      const cutoff = Date.now() - retentionMinutes * 60 * 1000;
      let mutated = false;
      const next = new Map(state.sessions);
      for (const [id, slice] of next) {
        if (slice.toolCallsLastActivity < cutoff && slice.toolCalls.length > 0) {
          next.set(id, { ...slice, toolCalls: [] });
          mutated = true;
        }
      }
      return mutated ? { sessions: next } : {};
    }),

  // ── Tool warnings ──
  addToolWarning: (sessionID, warning) =>
    set((state) => {
      const slice = state.sessions.get(sessionID) ?? emptyChatSessionState();
      return {
        sessions: applyToSession(state.sessions, sessionID, {
          toolWarnings: [...slice.toolWarnings, warning],
        }),
      };
    }),
  clearToolWarnings: (sessionID) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, { toolWarnings: [] }),
    })),

  // ── Pending approvals ──
  addPendingApproval: (sessionID, approval) =>
    set((state) => {
      const slice = state.sessions.get(sessionID) ?? emptyChatSessionState();
      return {
        sessions: applyToSession(state.sessions, sessionID, {
          pendingApprovals: [...slice.pendingApprovals, approval],
        }),
      };
    }),
  resolvePendingApproval: (sessionID, requestId, decision) =>
    set((state) => {
      const slice = state.sessions.get(sessionID) ?? emptyChatSessionState();
      return {
        sessions: applyToSession(state.sessions, sessionID, {
          pendingApprovals: slice.pendingApprovals.map((a) =>
            a.request_id === requestId ? { ...a, resolved: decision } : a,
          ),
        }),
      };
    }),
  clearPendingApprovals: (sessionID) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, { pendingApprovals: [] }),
    })),

  // ── Plugin envelopes ──
  addPluginEnvelope: (sessionID, item) =>
    set((state) => {
      const slice = state.sessions.get(sessionID) ?? emptyChatSessionState();
      const updated = [...slice.pluginEnvelopes, item].slice(-50);
      return {
        sessions: applyToSession(state.sessions, sessionID, {
          pluginEnvelopes: updated,
          pluginEnvelopesLastActivity: Date.now(),
        }),
      };
    }),
  setPluginEnvelopes: (sessionID, items) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, {
        pluginEnvelopes: items.slice(-50),
        pluginEnvelopesLastActivity: Date.now(),
      }),
    })),
  clearPluginEnvelopes: (sessionID) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, {
        pluginEnvelopes: [],
        pluginEnvelopesLastActivity: Date.now(),
      }),
    })),
  pruneEnvelopeRetention: (retentionMinutes) =>
    set((state) => {
      if (retentionMinutes < 0) return {};
      const cutoff = Date.now() - retentionMinutes * 60 * 1000;
      let mutated = false;
      const next = new Map(state.sessions);
      for (const [id, slice] of next) {
        if (slice.pluginEnvelopesLastActivity < cutoff && slice.pluginEnvelopes.length > 0) {
          next.set(id, { ...slice, pluginEnvelopes: [] });
          mutated = true;
        }
      }
      return mutated ? { sessions: next } : {};
    }),

  // ── Errors ──
  addChatError: (sessionID, error) =>
    set((state) => {
      const slice = state.sessions.get(sessionID) ?? emptyChatSessionState();
      return {
        sessions: applyToSession(state.sessions, sessionID, {
          chatErrors: [...slice.chatErrors, error],
        }),
      };
    }),
  dismissChatError: (sessionID, id) =>
    set((state) => {
      const slice = state.sessions.get(sessionID) ?? emptyChatSessionState();
      return {
        sessions: applyToSession(state.sessions, sessionID, {
          chatErrors: slice.chatErrors.map((e) => (e.id === id ? { ...e, dismissed: true } : e)),
        }),
      };
    }),
  clearChatErrors: (sessionID) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, { chatErrors: [] }),
    })),

  // ── Per-session dials ──
  setActiveModel: (sessionID, model) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, { activeModel: model }),
    })),
  setActiveEffort: (sessionID, effort) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, { activeEffort: effort }),
    })),

  // ── Composer ──
  setComposerDraft: (sessionID, draft) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, { composerDraft: draft }),
    })),
  clearComposerDraft: (sessionID) =>
    set((state) => ({
      sessions: applyToSession(state.sessions, sessionID, { composerDraft: "" }),
    })),

  // ── Tool-call display preference (cross-session) ──
  toolCallDisplayMode:
    (safeLocalStorageGet("nanite:toolCallDisplayMode") as ToolCallDisplayMode | null) || "minimal",
  setToolCallDisplayMode: (mode) => {
    safeLocalStorageSet("nanite:toolCallDisplayMode", mode);
    set({ toolCallDisplayMode: mode });
  },
  loadToolCallDisplayMode: (sessionID) => {
    if (!sessionID) return;
    const sessionMode = safeLocalStorageGet(
      `nanite:tcMode:${sessionID}`,
    ) as ToolCallDisplayMode | null;
    const globalMode = safeLocalStorageGet(
      "nanite:toolCallDisplayMode",
    ) as ToolCallDisplayMode | null;
    set({ toolCallDisplayMode: sessionMode || globalMode || "minimal" });
  },
  saveToolCallDisplayMode: (sessionID, mode) => {
    safeLocalStorageSet("nanite:toolCallDisplayMode", mode);
    if (sessionID) {
      safeLocalStorageSet(`nanite:tcMode:${sessionID}`, mode);
    }
    set({ toolCallDisplayMode: mode });
  },

  // ── Multi-session presence (cross-session) ──
  activeStreams: new Map(),
  pendingTools: new Map(),
  cliActiveSessions: new Map(),
  setActiveStream: (sessionID, info) =>
    set((state) => {
      const next = new Map(state.activeStreams);
      next.set(sessionID, info);
      return { activeStreams: next };
    }),
  removeActiveStream: (sessionID) =>
    set((state) => {
      const next = new Map(state.activeStreams);
      next.delete(sessionID);
      return { activeStreams: next };
    }),
  setPendingTool: (sessionID, info) =>
    set((state) => {
      const next = new Map(state.pendingTools);
      next.set(sessionID, info);
      return { pendingTools: next };
    }),
  removePendingTool: (sessionID) =>
    set((state) => {
      const next = new Map(state.pendingTools);
      next.delete(sessionID);
      return { pendingTools: next };
    }),
  setCLIActive: (sessionID, info) =>
    set((state) => {
      const next = new Map(state.cliActiveSessions);
      next.set(sessionID, info);
      return { cliActiveSessions: next };
    }),
  removeCLIActive: (sessionID) =>
    set((state) => {
      const next = new Map(state.cliActiveSessions);
      next.delete(sessionID);
      return { cliActiveSessions: next };
    }),

  // ── Cross-session jump-to-message ──
  pendingJump: null,
  setPendingJump: (jump) => set({ pendingJump: jump }),
  scrollToMessageId: null,
  setScrollToMessageId: (id) => set({ scrollToMessageId: id }),

  // ── Chat toast (intentional global) ──
  chatToast: null,
  showChatToast: (message, tone = "success") => set({ chatToast: { message, tone } }),
  dismissChatToast: () => set({ chatToast: null }),
}));

// ──────────────────────────────────────────────────────────────────────────────
// Selector hooks (Phase 3 of the singleton refactor).
//
// Components consume per-session state through these instead of reaching into
// the store directly. `useActiveChatSession()` follows `useAppStore.activeSessionId`
// and is the right default for chat-surface UI; `useChatSession(id)` is for
// callers that need a specific session (e.g. presence-list rendering one row).
// Convenience selectors return individual fields and only re-render on that
// field's change.
// ──────────────────────────────────────────────────────────────────────────────

/** Slice for the explicitly-named session (or the EMPTY fallback if absent). */
export function useChatSession(sessionID: string | null | undefined): ChatSessionState {
  return useChatStore((s) =>
    sessionID ? (s.sessions.get(sessionID) ?? EMPTY_CHAT_SESSION_STATE) : EMPTY_CHAT_SESSION_STATE,
  );
}

/** Slice for `useAppStore.activeSessionId`. */
export function useActiveChatSession(): ChatSessionState {
  const activeSessionId = useAppStore((s) => s.activeSessionId);
  return useChatSession(activeSessionId);
}

function selectField<K extends keyof ChatSessionState>(
  sessionID: string | null | undefined,
  field: K,
): (s: { sessions: Map<string, ChatSessionState> }) => ChatSessionState[K] {
  return (s) => {
    if (!sessionID) return EMPTY_CHAT_SESSION_STATE[field];
    const slice = s.sessions.get(sessionID);
    return slice ? slice[field] : EMPTY_CHAT_SESSION_STATE[field];
  };
}

function useActiveSliceField<K extends keyof ChatSessionState>(
  sessionID: string | null | undefined,
  field: K,
): ChatSessionState[K] {
  const activeSessionId = useAppStore((s) => s.activeSessionId);
  const id = sessionID ?? activeSessionId;
  return useChatStore(selectField(id, field));
}

export function useIsStreaming(sessionID?: string | null): boolean {
  return useActiveSliceField(sessionID, "isStreaming");
}
export function useStreamingContent(sessionID?: string | null): string {
  return useActiveSliceField(sessionID, "streamingContent");
}
export function useStreamingNarration(sessionID?: string | null): string {
  return useActiveSliceField(sessionID, "streamingNarration");
}
export function useStreamingFinal(sessionID?: string | null): string {
  return useActiveSliceField(sessionID, "streamingFinal");
}
export function useStreamingThinking(sessionID?: string | null): string {
  return useActiveSliceField(sessionID, "streamingThinking");
}
export function useStatusMessage(sessionID?: string | null): string | null {
  return useActiveSliceField(sessionID, "statusMessage");
}
export function useCircuitOpen(sessionID?: string | null): boolean {
  return useActiveSliceField(sessionID, "circuitOpen");
}
export function useSessionTakeover(sessionID?: string | null): boolean {
  return useActiveSliceField(sessionID, "sessionTakeover");
}
export function useInterruptedTurn(sessionID?: string | null): boolean {
  return useActiveSliceField(sessionID, "interruptedTurn");
}
export function useTextOnlyMode(sessionID?: string | null): boolean {
  return useActiveSliceField(sessionID, "textOnlyMode");
}
export function useChatErrors(sessionID?: string | null): ChatError[] {
  return useActiveSliceField(sessionID, "chatErrors");
}
export function usePendingApprovals(sessionID?: string | null): PendingApproval[] {
  return useActiveSliceField(sessionID, "pendingApprovals");
}
export function useToolWarnings(sessionID?: string | null): ToolWarning[] {
  return useActiveSliceField(sessionID, "toolWarnings");
}
export function useActiveModel(sessionID?: string | null): string {
  return useActiveSliceField(sessionID, "activeModel");
}
export function useActiveEffort(sessionID?: string | null): string {
  return useActiveSliceField(sessionID, "activeEffort");
}
export function useComposerDraft(sessionID?: string | null): string {
  return useActiveSliceField(sessionID, "composerDraft");
}
export function useToolCalls(sessionID?: string | null): ToolCall[] {
  return useActiveSliceField(sessionID, "toolCalls");
}
export function usePluginEnvelopes(sessionID?: string | null): PluginEnvelopeItem[] {
  return useActiveSliceField(sessionID, "pluginEnvelopes");
}
