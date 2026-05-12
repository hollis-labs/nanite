import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import { applyEnvelopePanelEffects, applyPanelSignal } from "@/lib/panel-signal";
import type {
  ApprovalRequest,
  ChatError,
  ChatErrorCode,
  Envelope,
  Message,
  ModeSuggestion,
  PluginEnvelopeItem,
  StreamEvent,
  ToolWarning,
  UserSettings,
} from "@/lib/types";
import {
  useChatStore,
  useCircuitOpen,
  useIsStreaming,
  useSessionTakeover,
  useStatusMessage,
  useStreamingContent,
  useStreamStalled,
} from "@/stores/useChatStore";
import { useLayoutStore } from "@/stores/useLayoutStore";

const PAGE_SIZE = 50;

/** Stall watchdog: flip `streamStalled` true when no SSE event arrives for this
 * long while a stream is active. Intentionally conservative — tool calls can
 * stretch well past the LLM's natural cadence. 60s matches the CW-0043 bug
 * pattern (chat freezes after ~7 tool calls, no events fire). */
const STALL_THRESHOLD_MS = 60_000;
const STALL_CHECK_INTERVAL_MS = 5_000;

/** SSE event type constants — single source of truth for stream event names */
const SSE = {
  DELTA: "delta",
  REPLACE_CONTENT: "replace_content",
  TOOL_CALL: "tool_call",
  TOOL_RESULT: "tool_result",
  TOOL_WARNING: "tool_warning",
  STATUS: "status",
  CIRCUIT_OPEN: "circuit_open",
  SESSION_TAKEOVER: "session_takeover",
  STREAM_END: "stream_end",
  ERROR: "error",
  APPROVAL_REQUEST: "approval_request",
  PLUGIN_ENVELOPE: "plugin_envelope",
  /** J8 v1 (CW-20260426-0006) — agent-driven panel open/close/mode signals. */
  PANEL_SIGNAL: "panel_signal",
  /** B2 (CW-20260428-0010) — non-binding mode-classifier suggestion. */
  MODE_SUGGESTION: "mode_suggestion",
} as const;

function makeChatError(
  code: ChatErrorCode,
  message: string,
  details?: Record<string, unknown>,
  timestamp?: string,
): ChatError {
  return {
    id: `err-${Date.now()}-${crypto.randomUUID().slice(0, 8)}`,
    code,
    message,
    details: details as Record<string, unknown>,
    timestamp: timestamp || new Date().toISOString(),
  };
}

// --- Error persistence helpers (survive page refresh) ---

interface PersistedErrorState {
  errors: ChatError[];
  toolCalls: Array<{
    id: string;
    tool: string;
    status: "running" | "done" | "error";
    summary?: string;
  }>;
  errorMessage?: Message;
}

function storageKey(sessionId: string) {
  return `nanite:errorState:${sessionId}`;
}

function persistErrorState(sessionId: string, state: PersistedErrorState) {
  try {
    localStorage.setItem(storageKey(sessionId), JSON.stringify(state));
  } catch {
    /* quota exceeded — not critical */
  }
}

function clearPersistedErrorState(sessionId: string) {
  try {
    localStorage.removeItem(storageKey(sessionId));
  } catch {
    /* ignore */
  }
}

export function useChat(sessionId: string | null) {
  const [messages, setMessages] = useState<Message[]>([]);
  const [paginationState, setPaginationState] = useState<{
    total: number;
    oldestOffset: number;
  } | null>(null);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const eventSourceRef = useRef<EventSource | null>(null);
  const lastEventAtRef = useRef<number>(0);
  const watchdogRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // CW-20260418-0100: ring-buffer cursor for SSE reconnect. The backend
  // stamps every stream event with a monotonic event_id; we track the
  // highest we've seen so reconnectStalledStream can resume from
  // `?from=<lastEventId>` instead of the heavier /retry path. The current
  // message id is captured alongside so the reconnect URL is correct.
  const lastEventIdRef = useRef<number>(0);
  const currentMessageIdRef = useRef<string | null>(null);

  const queryClient = useQueryClient();

  // Read reactive state via per-session selectors (G-FE-SINGLETON).
  const isStreaming = useIsStreaming(sessionId);
  const streamingContent = useStreamingContent(sessionId);
  const statusMessage = useStatusMessage(sessionId);
  const circuitOpen = useCircuitOpen(sessionId);
  const sessionTakeover = useSessionTakeover(sessionId);
  const streamStalled = useStreamStalled(sessionId);

  // Store actions are stable — read via getState() inside callbacks to avoid
  // bloating dependency arrays. This helper gives typed access to all actions.
  const store = () => useChatStore.getState();

  // recordEventId advances the reconnect cursor. Called from every SSE
  // handler that receives a `(e: MessageEvent)` payload — PR #66 review #2:
  // non-delta events also carry event_ids from the ring buffer, and missing
  // them causes tool_call/tool_result replays on reconnect to look like
  // duplicates. Non-event_id-carrying events (synthetic, pre-ring-buffer)
  // are ignored by the try/catch — the cursor only tracks events the
  // server could replay.
  const recordEventId = useCallback((raw: string) => {
    try {
      const evt = JSON.parse(raw) as { event_id?: number };
      if (evt.event_id && evt.event_id > lastEventIdRef.current) {
        lastEventIdRef.current = evt.event_id;
      }
    } catch {
      // ignore — malformed events are already handled by the real handler
    }
  }, []);

  const touchStreamEvent = useCallback(() => {
    lastEventAtRef.current = Date.now();
    if (!sessionId) return;
    const slice = useChatStore.getState().sessions.get(sessionId);
    if (slice?.streamStalled) {
      store().setStreamStalled(sessionId, false);
    }
  }, [sessionId]);

  const stopStallWatchdog = useCallback(() => {
    if (watchdogRef.current !== null) {
      clearInterval(watchdogRef.current);
      watchdogRef.current = null;
    }
  }, []);

  const startStallWatchdog = useCallback(() => {
    stopStallWatchdog();
    if (!sessionId) return;
    lastEventAtRef.current = Date.now();
    watchdogRef.current = setInterval(() => {
      const slice = useChatStore.getState().sessions.get(sessionId);
      if (!slice) return;
      // Only flag a stall if we're actively streaming, not already
      // flagged, and no benign terminal state applies. The circuit
      // breaker + takeover banners own their own paths.
      if (!slice.isStreaming || slice.streamStalled || slice.circuitOpen || slice.sessionTakeover) {
        return;
      }
      if (Date.now() - lastEventAtRef.current >= STALL_THRESHOLD_MS) {
        useChatStore.getState().setStreamStalled(sessionId, true);
      }
    }, STALL_CHECK_INTERVAL_MS);
  }, [sessionId, stopStallWatchdog]);

  // Clean up the watchdog on unmount so it doesn't outlive the hook.
  useEffect(() => {
    return () => {
      stopStallWatchdog();
    };
  }, [stopStallWatchdog]);

  const loadMessages = useCallback(async () => {
    if (!sessionId) {
      setMessages([]);
      setPaginationState(null);
      return;
    }
    try {
      // First request to get total count and first page.
      const firstPage = await api.getMessagePage(sessionId, PAGE_SIZE);

      let backendMessages: Message[];
      if (!firstPage.has_more) {
        // All messages fit in one page — use them directly.
        backendMessages = firstPage.messages ?? [];
        setPaginationState({ total: firstPage.total, oldestOffset: 0 });
      } else {
        // More messages exist — load the LAST page (most recent).
        const lastOffset = Math.max(0, firstPage.total - PAGE_SIZE);
        const lastPage = await api.getMessagePage(sessionId, PAGE_SIZE, lastOffset);
        backendMessages = lastPage.messages ?? [];
        setPaginationState({ total: lastPage.total, oldestOffset: lastOffset });
      }

      // Clear any leftover banner errors from the previous session.
      store().clearChatErrors(sessionId);
      setMessages(backendMessages);
    } catch (err) {
      console.error("Failed to load messages:", err);
    }
  }, [sessionId]);

  const loadOlderMessages = useCallback(async () => {
    if (!sessionId || !paginationState || paginationState.oldestOffset <= 0 || loadingOlder) return;
    setLoadingOlder(true);
    try {
      const newOffset = Math.max(0, paginationState.oldestOffset - PAGE_SIZE);
      const count = paginationState.oldestOffset - newOffset;
      const page = await api.getMessagePage(sessionId, count, newOffset);
      setMessages((prev) => [...(page.messages ?? []), ...prev]);
      setPaginationState((p) => (p ? { ...p, oldestOffset: newOffset } : null));
    } catch (err) {
      console.error("Failed to load older messages:", err);
    } finally {
      setLoadingOlder(false);
    }
  }, [sessionId, paginationState, loadingOlder]);

  const hasOlderMessages = paginationState != null && paginationState.oldestOffset > 0;

  // Jump to a specific message (for search results). Loads a window around it.
  const jumpToMessage = useCallback(async (targetSessionId: string, messageId: string) => {
    if (!targetSessionId) return;
    try {
      const page = await api.getMessagesAround(targetSessionId, messageId);
      setMessages(page.messages ?? []);
      setPaginationState({ total: page.total, oldestOffset: 0 }); // approximate
    } catch (err) {
      console.error("Failed to jump to message:", err);
    }
  }, []);

  // Load messages when sessionId changes.
  //
  // If a pending cross-session jump is already queued for this sessionId when
  // the effect fires, we skip the normal loadMessages path entirely — the
  // jump effect below will fetch the messages-around window instead, so
  // running loadMessages first would be wasted work and would race with the
  // jump fetch (whoever called setMessages last would win).
  useEffect(() => {
    const queuedJump = useChatStore.getState().pendingJump;
    const skipLoad = queuedJump && queuedJump.sessionId === sessionId && !!sessionId;
    if (!skipLoad) {
      void loadMessages();
    }
    // Session-switch side effects — always run regardless of jump state.
    if (sessionId) {
      store().ensureSession(sessionId);
      const settings = queryClient.getQueryData<UserSettings>(["settings"]);
      const retention = settings ? settings.tool_drawer_retention : -1;
      // Prune stale tool-call / envelope buffers across slices per the user's
      // tool_drawer_retention setting (replaces the legacy loadSessionToolCalls /
      // loadSessionPluginEnvelopes flow — slices are already keyed correctly).
      store().pruneToolCallRetention(retention);
      store().pruneEnvelopeRetention(retention);
      // Auto-show during streaming was previously implemented as a separate
      // ToolCallDrawer state machine. The redesign replaces that with a
      // running-pip on the Tools tab in ChatPrimaryDrawer (Wave 2), so no
      // per-session reset is required here.
      store().setTextOnlyMode(sessionId, false);
    }
  }, [loadMessages, sessionId, queryClient]);

  // Pending-jump consumer. Subscribes reactively to `pendingJump` so it fires
  // for BOTH cross-session jumps (sessionId also changes) and same-session
  // jumps (only pendingJump changes). On trigger, fetches the messages-around
  // window and signals ChatTranscript to scroll + highlight.
  const pendingJump = useChatStore((s) => s.pendingJump);
  useEffect(() => {
    if (!pendingJump || !sessionId || pendingJump.sessionId !== sessionId) return;
    let cancelled = false;
    (async () => {
      try {
        const page = await api.getMessagesAround(sessionId, pendingJump.messageId);
        if (cancelled) return;
        setMessages(page.messages ?? []);
        // Explicitly set paginationState to null — the messages-around endpoint
        // returns a window from the middle of the session, and we don't know
        // its oldest offset. Setting oldestOffset: 0 would lie about being at
        // the start of history and incorrectly disable the "load older" button.
        // `hasOlderMessages` will be false until the user navigates back to a
        // normal load. See frontend.md §Known Gaps for the full window-mode
        // pagination story.
        setPaginationState(null);
        useChatStore.getState().setScrollToMessageId(pendingJump.messageId);
      } catch (err) {
        console.error("Failed to load messages around jump target:", err);
      }
      // Clear the pending jump (success or failure) unless this run was cancelled
      // or the store now holds a different jump. Done outside the catch to
      // avoid a return-in-finally pattern.
      if (cancelled) return;
      const current = useChatStore.getState().pendingJump;
      if (
        current &&
        current.messageId === pendingJump.messageId &&
        current.sessionId === pendingJump.sessionId
      ) {
        useChatStore.getState().setPendingJump(null);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [pendingJump, sessionId]);

  const sendMessage = useCallback(
    async (content: string) => {
      if (!sessionId || !content.trim()) return;

      // Reset takeover state — user is actively using this tab now.
      store().setSessionTakeover(sessionId, false);

      // Optimistically add user message
      const tempUserMsg: Message = {
        id: "temp-" + Date.now(),
        session_id: sessionId,
        agent_id: "",
        role: "user",
        content,
        envelope: null,
        metadata: "{}",
        created_at: new Date().toISOString(),
      };
      setMessages((prev) => [...prev, tempUserMsg]);
      store().ensureSession(sessionId);
      store().setStreaming(sessionId, true);
      store().clearToolCalls(sessionId);
      store().clearToolWarnings(sessionId);
      store().clearPendingApprovals(sessionId);
      store().clearChatErrors(sessionId);
      clearPersistedErrorState(sessionId);
      // J8 v1 (CW-20260426-0006) — dismiss-reset trigger. The v1 simplification
      // is "any new user-message turn resets all dismiss state for all panels"
      // so the agent can re-open dismissed drawers on the next turn. Smarter
      // classified-trigger version captured as
      // followups_j8_classified_dismiss_reset.
      useLayoutStore.getState().clearAllPanelDismissed();
      console.log("[useChat] streaming=true, sending message...");

      try {
        // F1 (CW-20260420-0014): read active effort from this session's slice
        // and pass it to the API so the budget multiplier + reasoning config
        // are applied.
        const slice = useChatStore.getState().sessions.get(sessionId);
        const activeEffort = slice?.activeEffort ?? "normal";
        const { message_id } = await api.sendMessage({
          session_id: sessionId,
          content,
          ...(activeEffort && activeEffort !== "normal" ? { effort: activeEffort } : {}),
        });

        // Connect to SSE stream
        // CW-20260418-0100: track the message id + reset cursor so
        // reconnectStalledStream can re-subscribe with ?from=<lastEventId>.
        currentMessageIdRef.current = message_id;
        lastEventIdRef.current = 0;
        const es = new EventSource(`/api/stream/${message_id}`);
        eventSourceRef.current = es;
        let accumulated = "";
        startStallWatchdog();

        es.addEventListener(SSE.DELTA, (e: MessageEvent) => {
          touchStreamEvent();
          recordEventId(e.data as string);
          const data: StreamEvent = JSON.parse(e.data as string);
          if (data.content) {
            // F4 (CW-20260419-0029) + F3 (CW-20260420-0023): route by phase.
            // "narration" → thinking strip (not accumulated as the answer).
            // "thinking"  → thinking strip (F3 interleaved thinking block).
            // "final"     → answer bubble (accumulated for persistence).
            // No phase (pre-F4 or legacy streams) → treat as final (old behaviour).
            if (data.phase === "narration") {
              store().appendStreamNarration(sessionId, data.content);
            } else if (data.phase === "thinking") {
              // F3: interleaved thinking block content — shown in "Working…" strip,
              // not accumulated into the answer bubble.
              store().appendStreamThinking(sessionId, data.content);
            } else {
              // "final" or absent — goes into the answer accumulator.
              accumulated += data.content;
              store().appendStreamFinal(sessionId, data.content);
            }
            // Clear any transient status message when content starts flowing.
            store().setStatusMessage(sessionId, null);
          }
        });

        es.addEventListener(SSE.REPLACE_CONTENT, (e: MessageEvent) => {
          touchStreamEvent();
          recordEventId(e.data as string);
          const data: StreamEvent = JSON.parse(e.data as string);
          if (data.content != null) {
            accumulated = data.content;
            store().replaceStreamContent(sessionId, data.content);
            store().setStatusMessage(sessionId, null);
          }
        });

        es.addEventListener(SSE.TOOL_CALL, (e: MessageEvent) => {
          touchStreamEvent();
          recordEventId(e.data as string);
          const data = JSON.parse(e.data as string) as StreamEvent & {
            tool_id?: string;
            detail?: string;
          };
          if (data.tool) {
            store().addToolCall(sessionId, {
              id: data.tool_id || data.message_id || `tc-${Date.now()}`,
              tool: data.tool,
              status: "running",
              detail: data.detail,
            });

            // UI-trigger tools: open frontend modals/panels when the agent calls them.
            if (data.tool === "nanite_open_sprint_planning") {
              window.dispatchEvent(
                new CustomEvent("plugin-action", { detail: { id: "sprint-planning" } }),
              );
            }
          }
        });

        es.addEventListener(SSE.TOOL_RESULT, (e: MessageEvent) => {
          touchStreamEvent();
          recordEventId(e.data as string);
          const data = JSON.parse(e.data as string) as StreamEvent & { tool_id?: string };
          const toolId = data.tool_id || data.message_id;
          if (toolId) {
            store().updateToolCall(sessionId, toolId, {
              status: data.error ? "error" : "done",
              summary: (data.summary ?? data.error ?? "") as string,
            });
          }
        });

        es.addEventListener(SSE.TOOL_WARNING, (e: MessageEvent) => {
          touchStreamEvent();
          recordEventId(e.data as string);
          const data: StreamEvent = JSON.parse(e.data as string);
          if (data.data) {
            try {
              const warning = JSON.parse(data.data) as ToolWarning;
              store().addToolWarning(sessionId, warning);
              // Set persistent text-only mode when agent has no MCP tools
              if (warning.level === "critical" && warning.error.includes("no MCP tools")) {
                store().setTextOnlyMode(sessionId, true);
              }
            } catch {
              console.warn("[useChat] Failed to parse tool_warning data:", data.data);
            }
          }
        });

        es.addEventListener(SSE.PLUGIN_ENVELOPE, (e: MessageEvent) => {
          touchStreamEvent();
          recordEventId(e.data as string);
          try {
            const evt: StreamEvent = JSON.parse(e.data as string);
            if (!evt.envelope) return;
            const envelope = JSON.parse(evt.envelope) as Envelope;
            const item: PluginEnvelopeItem = {
              id: `penv-${Date.now()}-${crypto.randomUUID().slice(0, 8)}`,
              pluginId: evt.plugin_id ?? "",
              envelope,
              receivedAt: Date.now(),
            };
            store().addPluginEnvelope(sessionId, item);
            // J8 v1 — declarative drawer routing. When the envelope carries a
            // target field, route the open/render through the layout store with
            // source='agent' so the dismiss machine gates correctly.
            applyEnvelopePanelEffects(envelope);
            if (import.meta.env?.DEV) {
              console.debug("[useChat] plugin_envelope", item);
            }
          } catch (err) {
            console.warn("[useChat] Failed to parse plugin_envelope event:", e.data, err);
          }
        });

        es.addEventListener(SSE.PANEL_SIGNAL, (e: MessageEvent) => {
          touchStreamEvent();
          recordEventId(e.data as string);
          try {
            const evt: StreamEvent = JSON.parse(e.data as string);
            if (!evt.envelope) return;
            const sig = JSON.parse(evt.envelope) as {
              action: "open" | "close" | "mode";
              panel_id?: string;
              mode?: string;
              source?: "agent" | "user";
            };
            applyPanelSignal(sig);
            if (import.meta.env?.DEV) {
              console.debug("[useChat] panel_signal", sig);
            }
          } catch (err) {
            console.warn("[useChat] Failed to parse panel_signal event:", e.data, err);
          }
        });

        es.addEventListener(SSE.MODE_SUGGESTION, (e: MessageEvent) => {
          // B2 (CW-20260428-0010): non-binding classifier signal. We stage
          // the payload in the chat store for B3 to consume; B2 itself
          // performs no UI action beyond a dev-mode debug log.
          touchStreamEvent();
          recordEventId(e.data as string);
          try {
            const evt: StreamEvent = JSON.parse(e.data as string);
            if (!evt.data) return;
            const payload = JSON.parse(evt.data) as ModeSuggestion;
            store().setPendingModeSuggestion(sessionId, payload);
            if (import.meta.env?.DEV) {
              console.debug("[useChat] mode_suggestion", payload);
            }
          } catch (err) {
            console.warn("[useChat] Failed to parse mode_suggestion event:", e.data, err);
          }
        });

        es.addEventListener(SSE.APPROVAL_REQUEST, (e: MessageEvent) => {
          touchStreamEvent();
          recordEventId(e.data as string);
          try {
            const evt: StreamEvent = JSON.parse(e.data as string);
            if (evt.data) {
              const approval = JSON.parse(evt.data) as ApprovalRequest;
              store().addPendingApproval(sessionId, {
                ...approval,
                receivedAt: Date.now(),
              });
            }
          } catch (err) {
            console.warn("[useChat] Failed to parse approval_request event:", e.data, err);
          }
        });

        es.addEventListener(SSE.STATUS, (e: MessageEvent) => {
          touchStreamEvent();
          recordEventId(e.data as string);
          const data: StreamEvent = JSON.parse(e.data as string);
          if (data.content) {
            store().setStatusMessage(sessionId, data.content);
          }
        });

        es.addEventListener(SSE.CIRCUIT_OPEN, () => {
          touchStreamEvent();
          store().setCircuitOpen(sessionId, true);
          // Do NOT close the EventSource — keep it open for potential retry.
        });

        es.addEventListener(SSE.SESSION_TAKEOVER, () => {
          stopStallWatchdog();
          // Another tab opened this session — stop streaming and show banner.
          console.warn("[useChat] Session takeover — another tab is now active");
          store().setSessionTakeover(sessionId, true);
          // Save partial content if any.
          if (accumulated) {
            const partialMsg: Message = {
              id: message_id,
              session_id: sessionId,
              agent_id: "",
              role: "assistant",
              content: accumulated,
              envelope: null,
              metadata: "{}",
              created_at: new Date().toISOString(),
            };
            setMessages((prev) => [...prev, partialMsg]);
          }
          store().clearStreaming(sessionId);
          es.close();
          eventSourceRef.current = null;
          // Do NOT reconnect — that would cause a takeover loop.
        });

        es.addEventListener(SSE.STREAM_END, (e: MessageEvent) => {
          touchStreamEvent();
          recordEventId(e.data as string);
          stopStallWatchdog();
          const data: StreamEvent = JSON.parse(e.data as string);
          // Add the complete assistant message
          // Parse envelope from stream_end event if present.
          let envelope: string | null = null;
          if (data.envelope) {
            envelope =
              typeof data.envelope === "string" ? data.envelope : JSON.stringify(data.envelope);
          }
          const assistantMsg: Message = {
            id: message_id,
            session_id: sessionId,
            agent_id: data.agent_id || "",
            role: "assistant",
            content: accumulated,
            envelope,
            metadata: JSON.stringify(data.usage || {}),
            created_at: new Date().toISOString(),
          };
          setMessages((prev) => [...prev, assistantMsg]);
          store().clearStreaming(sessionId);
          store().clearChatErrors(sessionId);
          clearPersistedErrorState(sessionId);
          es.close();
          eventSourceRef.current = null;

          // Refresh widgets that depend on session usage data
          void queryClient.invalidateQueries({ queryKey: ["session-usage", sessionId] });
          void queryClient.invalidateQueries({ queryKey: ["session", sessionId] });
        });

        es.addEventListener(SSE.ERROR, (e: MessageEvent) => {
          touchStreamEvent();
          recordEventId(e.data as string);
          stopStallWatchdog();
          // Custom SSE error event from the backend (has data).
          if (e.data) {
            try {
              const data: StreamEvent = JSON.parse(e.data as string);

              // Handle structured error from backend
              if (data.structured_error) {
                const se = data.structured_error;
                store().addChatError(
                  sessionId,
                  makeChatError(se.code, se.message, se.details, se.timestamp),
                );
              } else {
                // Fallback for unstructured errors (no envelope from backend)
                const errMsg = data.error || "Unknown streaming error";
                console.error("Stream error from backend:", errMsg);
                store().addChatError(sessionId, makeChatError("internal_error", errMsg));
              }
            } catch {
              console.error("Stream error (unparseable):", e.data);
            }
          }
          // Finalize the stream with whatever we have.
          const errorMsg: Message | undefined = accumulated
            ? {
                id: message_id,
                session_id: sessionId,
                agent_id: "",
                role: "assistant",
                content: accumulated,
                envelope: null,
                metadata: JSON.stringify({ had_error: true }),
                created_at: new Date().toISOString(),
              }
            : undefined;
          if (errorMsg) {
            setMessages((prev) => [...prev, errorMsg]);
          }

          // Persist error state so it survives page refresh.
          const slice = useChatStore.getState().sessions.get(sessionId);
          persistErrorState(sessionId, {
            errors: slice?.chatErrors ?? [],
            toolCalls: slice?.toolCalls ?? [],
            errorMessage: errorMsg,
          });

          store().clearStreaming(sessionId);
          es.close();
          eventSourceRef.current = null;
        });

        // Handle native EventSource connection errors (no data).
        es.onerror = () => {
          // Only handle if the custom error listener above didn't already fire.
          if (eventSourceRef.current) {
            stopStallWatchdog();
            console.error("SSE connection lost");
            if (accumulated) {
              const assistantMsg: Message = {
                id: message_id,
                session_id: sessionId,
                agent_id: "",
                role: "assistant",
                content: accumulated,
                envelope: null,
                metadata: "{}",
                created_at: new Date().toISOString(),
              };
              setMessages((prev) => [...prev, assistantMsg]);
            }
            store().clearStreaming(sessionId);
            es.close();
            eventSourceRef.current = null;
          }
        };
      } catch (err) {
        console.error("Send failed:", err);
        stopStallWatchdog();
        store().clearStreaming(sessionId);
      }
    },
    [sessionId, queryClient, startStallWatchdog, stopStallWatchdog, touchStreamEvent],
  );

  const stopStreaming = useCallback(() => {
    stopStallWatchdog();
    if (eventSourceRef.current) {
      eventSourceRef.current.close();
      eventSourceRef.current = null;
    }
    if (sessionId) {
      store().clearStreaming(sessionId);
      // CW-20260512-0006: tell the BE to cancel the in-flight LLM
      // stream + tool work, not just close the FE SSE. Without this,
      // removing the 5-minute parent wall-clock deadline would let
      // generation keep burning tokens after the user pressed stop.
      // Fire-and-forget: 404 means there was nothing to cancel (race
      // with stream end), network errors are non-fatal — the FE has
      // already closed its EventSource so the worst case is the BE
      // finishes the current turn on its own.
      void api.cancelChatStream(sessionId);
    }
  }, [sessionId, stopStallWatchdog]);

  const retryStream = useCallback(async () => {
    if (!sessionId) return;
    store().setCircuitOpen(sessionId, false);
    store().setStreamStalled(sessionId, false);
    store().clearToolCalls(sessionId);
    store().clearToolWarnings(sessionId);

    try {
      const { message_id } = await api.retryStream(sessionId);

      // Close old event source if still open.
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
      }

      // Open a new SSE connection for the retry.
      // CW-20260418-0100: fresh message id ⇒ reset cursor.
      currentMessageIdRef.current = message_id;
      lastEventIdRef.current = 0;
      const es = new EventSource(`/api/stream/${message_id}`);
      eventSourceRef.current = es;
      store().setStreaming(sessionId, true);
      startStallWatchdog();

      es.addEventListener(SSE.DELTA, (e: MessageEvent) => {
        touchStreamEvent();
        recordEventId(e.data as string);
        const data: StreamEvent = JSON.parse(e.data as string);
        if (data.content) {
          store().appendStreamContent(sessionId, data.content);
          store().setStatusMessage(sessionId, null);
        }
      });

      es.addEventListener(SSE.REPLACE_CONTENT, (e: MessageEvent) => {
        touchStreamEvent();
        recordEventId(e.data as string);
        const data: StreamEvent = JSON.parse(e.data as string);
        if (data.content != null) {
          store().replaceStreamContent(sessionId, data.content);
          store().setStatusMessage(sessionId, null);
        }
      });

      es.addEventListener(SSE.STREAM_END, (e: MessageEvent) => {
        touchStreamEvent();
        recordEventId(e.data as string);
        stopStallWatchdog();
        const data: StreamEvent = JSON.parse(e.data as string);
        const slice = useChatStore.getState().sessions.get(sessionId);
        const assistantMsg: Message = {
          id: message_id,
          session_id: sessionId,
          agent_id: data.agent_id || "",
          role: "assistant",
          content: slice?.streamingContent ?? "",
          envelope: null,
          metadata: JSON.stringify(data.usage || {}),
          created_at: new Date().toISOString(),
        };
        setMessages((prev) => [...prev, assistantMsg]);
        store().clearStreaming(sessionId);
        es.close();
        eventSourceRef.current = null;
      });

      es.addEventListener(SSE.CIRCUIT_OPEN, () => {
        touchStreamEvent();
        store().setCircuitOpen(sessionId, true);
      });

      es.addEventListener(SSE.SESSION_TAKEOVER, () => {
        stopStallWatchdog();
        console.warn("[useChat] Session takeover during retry — another tab is now active");
        store().setSessionTakeover(sessionId, true);
        store().clearStreaming(sessionId);
        es.close();
        eventSourceRef.current = null;
      });

      es.addEventListener(SSE.ERROR, () => {
        stopStallWatchdog();
        store().clearStreaming(sessionId);
        es.close();
        eventSourceRef.current = null;
      });

      es.onerror = () => {
        if (eventSourceRef.current) {
          stopStallWatchdog();
          store().clearStreaming(sessionId);
          es.close();
          eventSourceRef.current = null;
        }
      };
    } catch (err) {
      console.error("Retry failed:", err);
      stopStallWatchdog();
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
        eventSourceRef.current = null;
      }
      store().clearStreaming(sessionId);
    }
  }, [sessionId, startStallWatchdog, stopStallWatchdog, touchStreamEvent]);

  const dismissCircuit = useCallback(() => {
    stopStallWatchdog();
    if (!sessionId) return;
    store().setCircuitOpen(sessionId, false);
    if (eventSourceRef.current) {
      eventSourceRef.current.close();
      eventSourceRef.current = null;
    }
    // Save partial content with interruption note.
    const slice = useChatStore.getState().sessions.get(sessionId);
    const partial = slice?.streamingContent ?? "";
    if (partial) {
      const msg: Message = {
        id: `partial-${Date.now()}`,
        session_id: sessionId,
        agent_id: "",
        role: "assistant",
        content: partial + "\n\n_(Response interrupted: provider rate limited)_",
        envelope: null,
        metadata: "{}",
        created_at: new Date().toISOString(),
      };
      setMessages((prev) => [...prev, msg]);
    }
    store().clearStreaming(sessionId);
  }, [sessionId, stopStallWatchdog]);

  const reconnectStalledStream = useCallback(async () => {
    // User-triggered from the stall banner. Today this still delegates to
    // the heavier /retry path (new message_id). CW-20260418-0100's backend
    // ring buffer + Subscribe-with-cursor are wired on the server and the
    // cursor is tracked on the client via lastEventIdRef, but the
    // "reconnect to the SAME message with ?from=<cursor>" frontend path
    // is deferred to a follow-up that extracts the big block of
    // addEventListener registrations into a reusable helper. See
    // CW-20260418-0103.
    stopStallWatchdog();
    if (sessionId) store().setStreamStalled(sessionId, false);
    if (eventSourceRef.current) {
      eventSourceRef.current.close();
      eventSourceRef.current = null;
    }
    await retryStream();
  }, [sessionId, retryStream, stopStallWatchdog]);

  return {
    messages,
    isStreaming,
    streamingContent,
    statusMessage,
    circuitOpen,
    sessionTakeover,
    streamStalled,
    sendMessage,
    loadMessages,
    stopStreaming,
    retryStream,
    dismissCircuit,
    reconnectStalledStream,
    loadOlderMessages,
    hasOlderMessages,
    loadingOlder,
    jumpToMessage,
  };
}
