import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import { shouldRenderStandalonePluginEnvelope } from "@/lib/envelope-lane";
import { applyEnvelopePanelEffects, applyPanelSignal } from "@/lib/panel-signal";
import type {
  ApprovalRequest,
  ChatError,
  ChatErrorCode,
  Envelope,
  Message,
  PluginEnvelopeItem,
  StreamEvent,
  ToolWarning,
  UserSettings,
} from "@/lib/types";
import {
  useChatStore,
  useCircuitOpen,
  useInterruptedTurn,
  useIsStreaming,
  useSessionTakeover,
  useStatusMessage,
  useStreamingContent,
} from "@/stores/useChatStore";
import { useLayoutStore } from "@/stores/useLayoutStore";

const PAGE_SIZE = 50;
const STALLED_STREAM_IDLE_MS = 4000;
const STALLED_STREAM_POLL_MS = 1000;

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

const store = () => useChatStore.getState();

export function useChat(sessionId: string | null) {
  const [messages, setMessages] = useState<Message[]>([]);
  const [paginationState, setPaginationState] = useState<{
    total: number;
    oldestOffset: number;
  } | null>(null);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const eventSourceRef = useRef<EventSource | null>(null);
  const lastStreamActivityAtRef = useRef(0);
  const reconcileInFlightRef = useRef(false);
  const generationRef = useRef(0);
  const sendPendingRef = useRef(false);
  const connectStreamRef = useRef<(id: string) => void>(() => {});
  const [loadedSessionId, setLoadedSessionId] = useState<string | null>(null);
  // biome-ignore lint/correctness/useExhaustiveDependencies: session identity starts a new async generation.
  useLayoutEffect(() => {
    generationRef.current++;
    return () => {
      generationRef.current++;
    };
  }, [sessionId]);

  // Cursor and accumulated content are retained together in the session slice.
  const lastEventIdRef = useRef<number>(0);
  const currentMessageIdRef = useRef<string | null>(null);

  const queryClient = useQueryClient();

  // Read reactive state via per-session selectors (G-FE-SINGLETON).
  const isStreaming = useIsStreaming(sessionId);
  const streamingContent = useStreamingContent(sessionId);
  const statusMessage = useStatusMessage(sessionId);
  const circuitOpen = useCircuitOpen(sessionId);
  const sessionTakeover = useSessionTakeover(sessionId);
  const interruptedTurn = useInterruptedTurn(sessionId);

  const recordEventId = useCallback(
    (raw: string) => {
      try {
        const evt = JSON.parse(raw) as { event_id?: number };
        if (evt.event_id) {
          if (evt.event_id <= lastEventIdRef.current) return false;
          lastEventIdRef.current = evt.event_id;
          if (sessionId && currentMessageIdRef.current) {
            store().setStreamCursor(sessionId, currentMessageIdRef.current, evt.event_id);
          }
        }
      } catch {
        // Native transport errors carry no data and do not move the cursor.
      }
      return true;
    },
    [sessionId],
  );

  const markStreamActivity = useCallback(() => {
    lastStreamActivityAtRef.current = Date.now();
  }, []);

  const loadLatestMessages = useCallback(async () => {
    if (!sessionId) {
      return { messages: [] as Message[], total: 0, oldestOffset: 0 };
    }

    const firstPage = await api.getMessagePage(sessionId, PAGE_SIZE);
    if (!firstPage.has_more) {
      return {
        messages: firstPage.messages ?? [],
        total: firstPage.total,
        oldestOffset: 0,
      };
    }

    const saved = store().getTranscriptPosition(sessionId);
    const lastOffset = Math.min(
      Math.max(0, firstPage.total - PAGE_SIZE),
      saved?.oldestOffset ?? Infinity,
    );
    const lastPage = await api.getMessagePage(sessionId, firstPage.total - lastOffset, lastOffset);
    return {
      messages: lastPage.messages ?? [],
      total: lastPage.total,
      oldestOffset: lastOffset,
    };
  }, [sessionId]);

  const reconcileStreamingState = useCallback(
    async (assistantMessageID: string) => {
      if (!sessionId || reconcileInFlightRef.current) return;

      const generation = generationRef.current;
      reconcileInFlightRef.current = true;
      try {
        const latest = await loadLatestMessages();
        if (
          generation !== generationRef.current ||
          currentMessageIdRef.current !== assistantMessageID
        )
          return;
        if (!latest.messages.some((msg) => msg.id === assistantMessageID)) {
          // The assistant message never landed. Two cases:
          //   - The turn is genuinely still generating somewhere — leave the
          //     spinner up, the poll loop will retry.
          //   - CW-20260518-0084: a service restart killed the turn's backend
          //     agent. The backend reports `interrupted_turn` on the session
          //     GET when there is no live stream and the last persisted
          //     message is an unanswered user turn. In that case, stop the
          //     endless spinner and surface the interrupted indicator.
          try {
            const session = await api.getSession(sessionId);
            if (
              generation !== generationRef.current ||
              currentMessageIdRef.current !== assistantMessageID
            )
              return;
            if (session.interrupted_turn?.interrupted === true) {
              store().setInterruptedTurn(sessionId, true);
              store().clearStreaming(sessionId);
              currentMessageIdRef.current = null;
              if (eventSourceRef.current) {
                eventSourceRef.current.close();
                eventSourceRef.current = null;
              }
            }
          } catch (err) {
            console.warn("[useChat] interrupted-turn reconcile probe failed:", err);
          }
          return;
        }

        setMessages((prev) => {
          const merged = new Map<string, Message>();
          for (const msg of prev)
            if (msg.session_id === sessionId && !msg.id.startsWith("temp-"))
              merged.set(msg.id, msg);
          for (const msg of latest.messages) merged.set(msg.id, msg);
          return Array.from(merged.values()).sort((a, b) =>
            a.created_at.localeCompare(b.created_at),
          );
        });
        setPaginationState({ total: latest.total, oldestOffset: latest.oldestOffset });

        const standaloneEnvelopes = await api
          .getSessionPluginEnvelopes(sessionId)
          .catch(() => null);
        if (
          generation !== generationRef.current ||
          currentMessageIdRef.current !== assistantMessageID
        )
          return;
        if (standaloneEnvelopes)
          store().setPluginEnvelopes(
            sessionId,
            standaloneEnvelopes.map((envelope, index) => ({
              id: `rehydrated-${envelope.id ?? index}`,
              pluginId: "",
              envelope,
              receivedAt: Date.now(),
            })),
          );

        store().clearStreaming(sessionId);
        store().clearChatErrors(sessionId);
        clearPersistedErrorState(sessionId);
        currentMessageIdRef.current = null;
        if (eventSourceRef.current) {
          eventSourceRef.current.close();
          eventSourceRef.current = null;
        }

        void queryClient.invalidateQueries({ queryKey: ["session-usage", sessionId] });
        void queryClient.invalidateQueries({ queryKey: ["session", sessionId] });
      } catch (err) {
        console.warn("[useChat] stalled-stream reconcile failed:", err);
      } finally {
        if (generation === generationRef.current) reconcileInFlightRef.current = false;
      }
    },
    [loadLatestMessages, queryClient, sessionId],
  );

  const loadMessages = useCallback(async () => {
    if (!sessionId) {
      setMessages([]);
      setPaginationState(null);
      return;
    }
    const generation = generationRef.current;
    try {
      const [latest, session, envelopes] = await Promise.all([
        loadLatestMessages(),
        api.getSession(sessionId).catch(() => null),
        api.getSessionPluginEnvelopes(sessionId).catch(() => null),
      ]);
      if (generation !== generationRef.current) return;
      const live = !!eventSourceRef.current || sendPendingRef.current;
      setMessages((previous) => {
        const merged = new Map<string, Message>();
        // Preserve a turn started while the history request was pending.
        if (live)
          for (const msg of previous) if (msg.session_id === sessionId) merged.set(msg.id, msg);
        for (const msg of latest.messages) merged.set(msg.id, msg);
        // The turn may finish between the history fetch and the session probe.
        if (!live && !session?.active_message_id) {
          for (const msg of session?.messages ?? []) merged.set(msg.id, msg);
        }
        return [...merged.values()].sort((a, b) => a.created_at.localeCompare(b.created_at));
      });
      setLoadedSessionId(sessionId);
      setPaginationState({ total: latest.total, oldestOffset: latest.oldestOffset });
      if (!live && envelopes) {
        store().setPluginEnvelopes(
          sessionId,
          envelopes.map((envelope, index) => ({
            id: `rehydrated-${envelope.id ?? index}`,
            pluginId: "",
            envelope,
            receivedAt: Date.now(),
          })),
        );
      }
      if (session && !live) {
        store().clearChatErrors(sessionId);
        if (session.active_message_id) connectStreamRef.current(session.active_message_id);
        else store().clearStreaming(sessionId);
        store().setInterruptedTurn(
          sessionId,
          session.interrupted_turn?.interrupted === true && !session.active_message_id,
        );
      }
    } catch (err) {
      console.error("Failed to load messages:", err);
    }
  }, [loadLatestMessages, sessionId]);

  const loadOlderMessages = useCallback(async () => {
    if (!sessionId || !paginationState || paginationState.oldestOffset <= 0 || loadingOlder) return;
    const generation = generationRef.current;
    setLoadingOlder(true);
    try {
      const newOffset = Math.max(0, paginationState.oldestOffset - PAGE_SIZE);
      const count = paginationState.oldestOffset - newOffset;
      const page = await api.getMessagePage(sessionId, count, newOffset);
      if (generation !== generationRef.current) return;
      setMessages((prev) => [...(page.messages ?? []), ...prev]);
      setPaginationState((p) => (p ? { ...p, oldestOffset: newOffset } : null));
    } catch (err) {
      console.error("Failed to load older messages:", err);
    } finally {
      if (generation === generationRef.current) setLoadingOlder(false);
    }
  }, [sessionId, paginationState, loadingOlder]);

  const hasOlderMessages = paginationState != null && paginationState.oldestOffset > 0;

  // Jump to a specific message (for search results). Loads a window around it.
  const jumpToMessage = useCallback(async (targetSessionId: string, messageId: string) => {
    if (!targetSessionId || targetSessionId !== sessionId) return;
    const generation = generationRef.current;
    try {
      const page = await api.getMessagesAround(targetSessionId, messageId);
      if (generation !== generationRef.current) return;
      setLoadedSessionId(targetSessionId);
      setMessages(page.messages ?? []);
      setPaginationState({ total: page.total, oldestOffset: 0 }); // approximate
    } catch (err) {
      console.error("Failed to jump to message:", err);
    }
  }, [sessionId]);

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
    setLoadingOlder(false);
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

  useEffect(() => {
    return () => {
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
        eventSourceRef.current = null;
      }
      currentMessageIdRef.current = null;
      // Keep partial content and its cursor together for a later attachment.
      if (sessionId) store().setStreaming(sessionId, false);
      reconcileInFlightRef.current = false;
      sendPendingRef.current = false;
    };
  }, [sessionId]);

  useEffect(() => {
    if (!sessionId || !isStreaming) return;
    const timer = window.setInterval(() => {
      const assistantMessageID = currentMessageIdRef.current;
      if (!assistantMessageID) return;
      if (Date.now() - lastStreamActivityAtRef.current < STALLED_STREAM_IDLE_MS) {
        return;
      }
      void reconcileStreamingState(assistantMessageID);
    }, STALLED_STREAM_POLL_MS);
    return () => window.clearInterval(timer);
  }, [isStreaming, reconcileStreamingState, sessionId]);

  // Pending-jump consumer. Subscribes reactively to `pendingJump` so it fires
  // for BOTH cross-session jumps (sessionId also changes) and same-session
  // jumps (only pendingJump changes). On trigger, fetches the messages-around
  // window and signals ChatTranscript to scroll + highlight.
  const pendingJump = useChatStore((s) => s.pendingJump);
  useEffect(() => {
    if (!pendingJump || !sessionId || pendingJump.sessionId !== sessionId) return;
    let canceled = false;
    (async () => {
      try {
        const [page, session] = await Promise.all([
          api.getMessagesAround(sessionId, pendingJump.messageId),
          api.getSession(sessionId).catch(() => null),
        ]);
        if (canceled) return;
        setMessages(page.messages ?? []);
        setLoadedSessionId(sessionId);
        // Explicitly set paginationState to null — the messages-around endpoint
        // returns a window from the middle of the session, and we don't know
        // its oldest offset. Setting oldestOffset: 0 would lie about being at
        // the start of history and incorrectly disable the "load older" button.
        // `hasOlderMessages` will be false until the user navigates back to a
        // normal load. See frontend.md §Known Gaps for the full window-mode
        // pagination story.
        setPaginationState(null);
        useChatStore.getState().setScrollToMessageId(pendingJump.messageId);
        if (session?.active_message_id && !eventSourceRef.current && !sendPendingRef.current) {
          connectStreamRef.current(session.active_message_id);
        }
      } catch (err) {
        console.error("Failed to load messages around jump target:", err);
      }
      // Clear the pending jump (success or failure) unless this run was canceled
      // or the store now holds a different jump. Done outside the catch to
      // avoid a return-in-finally pattern.
      if (canceled) return;
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
      canceled = true;
    };
  }, [pendingJump, sessionId]);

  const connectStream = useCallback(
    (message_id: string) => {
      if (!sessionId) return;
      eventSourceRef.current?.close();
      const saved = store().sessions.get(sessionId);
      const resume = saved?.streamMessageId === message_id;
      if (!resume) {
        store().clearStreaming(sessionId);
        store().clearToolCalls(sessionId);
        store().clearToolWarnings(sessionId);
        store().clearPendingApprovals(sessionId);
      }
      currentMessageIdRef.current = message_id;
      lastEventIdRef.current = resume ? saved.streamCursor : 0;
      store().setSessionTakeover(sessionId, false);
      store().setStreamCursor(sessionId, message_id, lastEventIdRef.current);
      store().setStreaming(sessionId, true);
      markStreamActivity();
      const cursor = lastEventIdRef.current;
      const es = new EventSource(`/api/stream/${message_id}${cursor ? `?from=${cursor}` : ""}`);
      eventSourceRef.current = es;
      let accumulated = resume ? saved.streamingFinal : "";
      const listen = (type: string, handler: (event: MessageEvent) => void) => {
        es.addEventListener(type, (event) => {
          if (eventSourceRef.current === es) handler(event as MessageEvent);
        });
      };

      listen(SSE.DELTA, (e: MessageEvent) => {
        markStreamActivity();
        if (!recordEventId(e.data as string)) return;
        const data: StreamEvent = JSON.parse(e.data as string);
        if (data.content) {
          // F4 (CW-20260419-0029) + F3 (CW-20260420-0023): route by phase.
          // "narration" → thinking strip (not accumulated as the answer).
          // "thinking"  → thinking strip (F3 interleaved thinking block).
          // "final"     → answer bubble (accumulated for persistence).
          // No phase (pre-F4 or legacy streams) → treat as final (old behavior).
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

      listen(SSE.REPLACE_CONTENT, (e: MessageEvent) => {
        markStreamActivity();
        if (!recordEventId(e.data as string)) return;
        const data: StreamEvent = JSON.parse(e.data as string);
        if (data.content != null) {
          accumulated = data.content;
          store().replaceStreamContent(sessionId, data.content);
          store().setStatusMessage(sessionId, null);
        }
      });

      listen(SSE.TOOL_CALL, (e: MessageEvent) => {
        markStreamActivity();
        if (!recordEventId(e.data as string)) return;
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

      listen(SSE.TOOL_RESULT, (e: MessageEvent) => {
        markStreamActivity();
        if (!recordEventId(e.data as string)) return;
        const data = JSON.parse(e.data as string) as StreamEvent & { tool_id?: string };
        const toolId = data.tool_id || data.message_id;
        if (toolId) {
          store().updateToolCall(sessionId, toolId, {
            status: data.error ? "error" : "done",
            summary: (data.summary ?? data.error ?? "") as string,
          });
        }
      });

      listen(SSE.TOOL_WARNING, (e: MessageEvent) => {
        markStreamActivity();
        if (!recordEventId(e.data as string)) return;
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

      listen(SSE.PLUGIN_ENVELOPE, (e: MessageEvent) => {
        markStreamActivity();
        if (!recordEventId(e.data as string)) return;
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
          if (shouldRenderStandalonePluginEnvelope(envelope)) {
            store().addPluginEnvelope(sessionId, item);
          }
          // J8 v1 — declarative drawer routing. When the envelope carries a
          // target field, route the open/render through the layout store with
          // source='agent' so the dismiss machine gates correctly.
          applyEnvelopePanelEffects(envelope, sessionId);
          if (import.meta.env?.DEV) {
            console.debug("[useChat] plugin_envelope", item);
          }
        } catch (err) {
          console.warn("[useChat] Failed to parse plugin_envelope event:", e.data, err);
        }
      });

      listen(SSE.PANEL_SIGNAL, (e: MessageEvent) => {
        markStreamActivity();
        if (!recordEventId(e.data as string)) return;
        try {
          const evt: StreamEvent = JSON.parse(e.data as string);
          if (!evt.envelope) return;
          const sig = JSON.parse(evt.envelope) as {
            action: "open" | "close" | "mode";
            panel_id?: string;
            mode?: string;
            source?: "agent" | "user";
          };
          applyPanelSignal(sig, sessionId);
          if (import.meta.env?.DEV) {
            console.debug("[useChat] panel_signal", sig);
          }
        } catch (err) {
          console.warn("[useChat] Failed to parse panel_signal event:", e.data, err);
        }
      });

      listen(SSE.APPROVAL_REQUEST, (e: MessageEvent) => {
        markStreamActivity();
        if (!recordEventId(e.data as string)) return;
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

      listen(SSE.STATUS, (e: MessageEvent) => {
        markStreamActivity();
        if (!recordEventId(e.data as string)) return;
        const data: StreamEvent = JSON.parse(e.data as string);
        if (data.content) {
          store().setStatusMessage(sessionId, data.content);
        }
      });

      listen(SSE.CIRCUIT_OPEN, (e: MessageEvent) => {
        if (!recordEventId(e.data as string)) return;
        markStreamActivity();
        store().setCircuitOpen(sessionId, true);
        // Do NOT close the EventSource — keep it open for potential retry.
      });

      listen(SSE.SESSION_TAKEOVER, () => {
        markStreamActivity();
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
        currentMessageIdRef.current = null;
        es.close();
        eventSourceRef.current = null;
        // Do NOT reconnect — that would cause a takeover loop.
      });

      listen(SSE.STREAM_END, (e: MessageEvent) => {
        markStreamActivity();
        if (!recordEventId(e.data as string)) return;
        es.close();
        if (eventSourceRef.current === es) eventSourceRef.current = null;
        // A bounded replay may omit older deltas. Fetch the persisted answer
        // rather than treating the replay window as a complete message.
        void reconcileStreamingState(message_id);
      });

      listen(SSE.ERROR, (e: MessageEvent) => {
        if (!e.data) return;
        markStreamActivity();
        if (!recordEventId(e.data as string)) return;
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
        currentMessageIdRef.current = null;
        es.close();
        eventSourceRef.current = null;
      });

      // Leave transport failures reconnectable. Persisted-message reconciliation
      // covers a completed/evicted stream or a restarted backend.
      es.onerror = () => {
        if (eventSourceRef.current === es) void reconcileStreamingState(message_id);
      };
    },
    [markStreamActivity, recordEventId, reconcileStreamingState, sessionId],
  );
  useLayoutEffect(() => {
    connectStreamRef.current = connectStream;
  }, [connectStream]);

  const sendMessage = useCallback(
    async (content: string) => {
      if (!sessionId || !content.trim()) return;

      const generation = generationRef.current;
      sendPendingRef.current = true;
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
      store().clearStreaming(sessionId);
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

        if (generation !== generationRef.current) return;
        sendPendingRef.current = false;
        connectStream(message_id);
      } catch (err) {
        if (generation !== generationRef.current) return;
        sendPendingRef.current = false;
        console.error("Send failed:", err);
        store().clearStreaming(sessionId);
        currentMessageIdRef.current = null;
      }
    },
    [connectStream, sessionId],
  );

  const stopStreaming = useCallback(() => {
    if (eventSourceRef.current) {
      eventSourceRef.current.close();
      eventSourceRef.current = null;
    }
    currentMessageIdRef.current = null;
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
  }, [sessionId]);

  const retryStream = useCallback(async () => {
    if (!sessionId) return;
    const generation = generationRef.current;
    store().setCircuitOpen(sessionId, false);
    store().clearToolCalls(sessionId);
    store().clearToolWarnings(sessionId);

    try {
      const { message_id } = await api.retryStream(sessionId);

      if (generation !== generationRef.current) return;
      connectStream(message_id);
    } catch (err) {
      if (generation !== generationRef.current) return;
      console.error("Retry failed:", err);
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
        eventSourceRef.current = null;
      }
      store().clearStreaming(sessionId);
      currentMessageIdRef.current = null;
    }
  }, [connectStream, sessionId]);

  // CW-20260518-0084: manual dismissal of the interrupted-turn banner. The
  // banner also clears automatically when the user sends a new message
  // (setStreaming(true) resets the flag — that's the send-to-resume path).
  const dismissInterruptedTurn = useCallback(() => {
    if (!sessionId) return;
    store().setInterruptedTurn(sessionId, false);
  }, [sessionId]);

  const dismissCircuit = useCallback(() => {
    if (!sessionId) return;
    store().setCircuitOpen(sessionId, false);
    if (eventSourceRef.current) {
      eventSourceRef.current.close();
      eventSourceRef.current = null;
    }
    currentMessageIdRef.current = null;
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
  }, [sessionId]);

  return {
    messages: loadedSessionId === sessionId ? messages : [],
    messagesReady: loadedSessionId === sessionId,
    oldestOffset: paginationState?.oldestOffset ?? 0,
    isStreaming,
    streamingContent,
    statusMessage,
    circuitOpen,
    sessionTakeover,
    interruptedTurn,
    sendMessage,
    loadMessages,
    stopStreaming,
    retryStream,
    dismissCircuit,
    dismissInterruptedTurn,
    loadOlderMessages,
    hasOlderMessages,
    loadingOlder,
    jumpToMessage,
  };
}
