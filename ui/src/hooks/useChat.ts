import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import type { ApprovalRequest, ChatError, ChatErrorCode, Message, StreamEvent, ToolWarning, UserSettings } from "@/lib/types";
import { useChatStore } from "@/stores/useChatStore";
import { useLayoutStore } from "@/stores/useLayoutStore";

const PAGE_SIZE = 50;

/** SSE event type constants — single source of truth for stream event names */
const SSE = {
  DELTA: "delta",
  TOOL_CALL: "tool_call",
  TOOL_RESULT: "tool_result",
  TOOL_WARNING: "tool_warning",
  STATUS: "status",
  CIRCUIT_OPEN: "circuit_open",
  SESSION_TAKEOVER: "session_takeover",
  STREAM_END: "stream_end",
  ERROR: "error",
  APPROVAL_REQUEST: "approval_request",
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

function loadPersistedErrorState(sessionId: string): PersistedErrorState | null {
  try {
    const raw = localStorage.getItem(storageKey(sessionId));
    if (!raw) return null;
    return JSON.parse(raw) as PersistedErrorState;
  } catch {
    return null;
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

  const queryClient = useQueryClient();

  // Read reactive state via selectors (triggers re-renders)
  const isStreaming = useChatStore((s) => s.isStreaming);
  const streamingContent = useChatStore((s) => s.streamingContent);
  const statusMessage = useChatStore((s) => s.statusMessage);
  const circuitOpen = useChatStore((s) => s.circuitOpen);
  const sessionTakeover = useChatStore((s) => s.sessionTakeover);

  // Store actions are stable — read via getState() inside callbacks to avoid
  // bloating dependency arrays. This helper gives typed access to all actions.
  const store = () => useChatStore.getState();

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

      // Restore persisted error state (errors not saved by backend).
      const persisted = loadPersistedErrorState(sessionId);
      if (persisted) {
        if (persisted.errorMessage) {
          const exists = backendMessages.some((m) => m.id === persisted.errorMessage!.id);
          if (!exists) {
            backendMessages.push(persisted.errorMessage);
          }
        }
        for (const err of persisted.errors) {
          store().addChatError(err);
        }
        for (const tc of persisted.toolCalls) {
          store().addToolCall(tc);
        }
      }

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

  // Load messages when sessionId changes
  useEffect(() => {
    void loadMessages();
    // Load retained tool calls — skip prune if settings not yet cached (avoids
    // pruning with wrong default when user configured -1). Re-runs when settings load
    // via the separate effect below.
    const settings = queryClient.getQueryData<UserSettings>(['settings'])
    const retention = settings ? settings.tool_drawer_retention : -1
    store().loadSessionToolCalls(sessionId, retention);
    // Close the tool drawer on session switch — user opens as needed
    useLayoutStore.getState().setToolDrawerState('closed');
    // Clear text-only mode on session switch — it will be re-set if the new
    // session's agent also has 0 MCP tools.
    store().setTextOnlyMode(false);
  }, [loadMessages, sessionId]);

  const sendMessage = useCallback(
    async (content: string) => {
      if (!sessionId || !content.trim()) return;

      // Reset takeover state — user is actively using this tab now.
      store().setSessionTakeover(false);

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
      store().setStreaming(true);
      store().setStreamingSessionId(sessionId);
      store().clearToolCalls();
      store().clearToolWarnings();
      store().clearPendingApprovals();
      clearPersistedErrorState(sessionId);
      console.log("[useChat] streaming=true, sending message...");

      try {
        const { message_id } = await api.sendMessage({ session_id: sessionId, content });

        // Connect to SSE stream
        const es = new EventSource(`/api/stream/${message_id}`);
        eventSourceRef.current = es;
        let accumulated = "";

        es.addEventListener(SSE.DELTA, (e: MessageEvent) => {
          const data: StreamEvent = JSON.parse(e.data as string);
          if (data.content) {
            accumulated += data.content;
            store().appendStreamContent(data.content);
            // Clear any transient status message when content starts flowing.
            store().setStatusMessage(null);
          }
        });

        es.addEventListener(SSE.TOOL_CALL, (e: MessageEvent) => {
          const data = JSON.parse(e.data as string) as StreamEvent & { tool_id?: string; detail?: string };
          if (data.tool) {
            store().addToolCall({
              id: data.tool_id || data.message_id || `tc-${Date.now()}`,
              tool: data.tool,
              status: "running",
              detail: data.detail,
            });

            // UI-trigger tools: open frontend modals/panels when the agent calls them.
            if (data.tool === "nanite_open_sprint_planning") {
              window.dispatchEvent(new CustomEvent('plugin-action', { detail: { id: 'sprint-planning' } }));
            }
          }
        });

        es.addEventListener(SSE.TOOL_RESULT, (e: MessageEvent) => {
          const data = JSON.parse(e.data as string) as StreamEvent & { tool_id?: string };
          const toolId = data.tool_id || data.message_id;
          if (toolId) {
            store().updateToolCall(toolId, {
              status: data.error ? "error" : "done",
              summary: (data.summary ?? data.error ?? "") as string,
            });
          }
        });

        es.addEventListener(SSE.TOOL_WARNING, (e: MessageEvent) => {
          const data: StreamEvent = JSON.parse(e.data as string);
          if (data.data) {
            try {
              const warning = JSON.parse(data.data) as ToolWarning;
              store().addToolWarning(warning);
              // Set persistent text-only mode when agent has no MCP tools
              if (warning.level === "critical" && warning.error.includes("no MCP tools")) {
                store().setTextOnlyMode(true);
              }
            } catch {
              console.warn("[useChat] Failed to parse tool_warning data:", data.data);
            }
          }
        });

        es.addEventListener(SSE.APPROVAL_REQUEST, (e: MessageEvent) => {
          try {
            const evt: StreamEvent = JSON.parse(e.data as string);
            if (evt.data) {
              const approval = JSON.parse(evt.data) as ApprovalRequest;
              store().addPendingApproval({
                ...approval,
                receivedAt: Date.now(),
              });
            }
          } catch (err) {
            console.warn("[useChat] Failed to parse approval_request event:", e.data, err);
          }
        });

        es.addEventListener(SSE.STATUS, (e: MessageEvent) => {
          const data: StreamEvent = JSON.parse(e.data as string);
          if (data.content) {
            store().setStatusMessage(data.content);
          }
        });

        es.addEventListener(SSE.CIRCUIT_OPEN, () => {
          store().setCircuitOpen(true);
          // Do NOT close the EventSource — keep it open for potential retry.
        });

        es.addEventListener(SSE.SESSION_TAKEOVER, () => {
          // Another tab opened this session — stop streaming and show banner.
          console.warn("[useChat] Session takeover — another tab is now active");
          store().setSessionTakeover(true);
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
          store().clearStream();
          es.close();
          eventSourceRef.current = null;
          // Do NOT reconnect — that would cause a takeover loop.
        });

        es.addEventListener(SSE.STREAM_END, (e: MessageEvent) => {
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
          store().clearStream();
          clearPersistedErrorState(sessionId);
          es.close();
          eventSourceRef.current = null;

          // Refresh widgets that depend on session usage data
          void queryClient.invalidateQueries({ queryKey: ["session-usage", sessionId] });
          void queryClient.invalidateQueries({ queryKey: ["session", sessionId] });
        });

        es.addEventListener(SSE.ERROR, (e: MessageEvent) => {
          // Custom SSE error event from the backend (has data).
          if (e.data) {
            try {
              const data: StreamEvent = JSON.parse(e.data as string);

              // Handle structured error from backend
              if (data.structured_error) {
                const se = data.structured_error;
                store().addChatError(makeChatError(se.code, se.message, se.details, se.timestamp));
              } else {
                // Fallback for unstructured errors (no envelope from backend)
                const errMsg = data.error || "Unknown streaming error";
                console.error("Stream error from backend:", errMsg);
                store().addChatError(makeChatError("internal_error", errMsg));
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
          const { chatErrors, toolCalls } = useChatStore.getState();
          persistErrorState(sessionId, {
            errors: chatErrors,
            toolCalls,
            errorMessage: errorMsg,
          });

          store().clearStream();
          es.close();
          eventSourceRef.current = null;
        });

        // Handle native EventSource connection errors (no data).
        es.onerror = () => {
          // Only handle if the custom error listener above didn't already fire.
          if (eventSourceRef.current) {
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
            store().clearStream();
            es.close();
            eventSourceRef.current = null;
          }
        };
      } catch (err) {
        console.error("Send failed:", err);
        store().clearStream();
      }
    },
    [sessionId, queryClient],
  );

  const stopStreaming = useCallback(() => {
    if (eventSourceRef.current) {
      eventSourceRef.current.close();
      eventSourceRef.current = null;
    }
    store().clearStream();
  }, []);

  const retryStream = useCallback(async () => {
    if (!sessionId) return;
    store().setCircuitOpen(false);
    store().clearToolCalls();
    store().clearToolWarnings();

    try {
      const { message_id } = await api.retryStream(sessionId);

      // Close old event source if still open.
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
      }

      // Open a new SSE connection for the retry.
      const es = new EventSource(`/api/stream/${message_id}`);
      eventSourceRef.current = es;
      store().setStreaming(true);

      es.addEventListener(SSE.DELTA, (e: MessageEvent) => {
        const data: StreamEvent = JSON.parse(e.data as string);
        if (data.content) {
          store().appendStreamContent(data.content);
          store().setStatusMessage(null);
        }
      });

      es.addEventListener(SSE.STREAM_END, (e: MessageEvent) => {
        const data: StreamEvent = JSON.parse(e.data as string);
        const assistantMsg: Message = {
          id: message_id,
          session_id: sessionId,
          agent_id: data.agent_id || "",
          role: "assistant",
          content: useChatStore.getState().streamingContent,
          envelope: null,
          metadata: JSON.stringify(data.usage || {}),
          created_at: new Date().toISOString(),
        };
        setMessages((prev) => [...prev, assistantMsg]);
        store().clearStream();
        es.close();
        eventSourceRef.current = null;
      });

      es.addEventListener(SSE.CIRCUIT_OPEN, () => {
        store().setCircuitOpen(true);
      });

      es.addEventListener(SSE.SESSION_TAKEOVER, () => {
        console.warn("[useChat] Session takeover during retry — another tab is now active");
        store().setSessionTakeover(true);
        store().clearStream();
        es.close();
        eventSourceRef.current = null;
      });

      es.addEventListener(SSE.ERROR, () => {
        store().clearStream();
        es.close();
        eventSourceRef.current = null;
      });

      es.onerror = () => {
        if (eventSourceRef.current) {
          store().clearStream();
          es.close();
          eventSourceRef.current = null;
        }
      };
    } catch (err) {
      console.error("Retry failed:", err);
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
        eventSourceRef.current = null;
      }
      store().clearStream();
    }
  }, [sessionId]);

  const dismissCircuit = useCallback(() => {
    store().setCircuitOpen(false);
    if (eventSourceRef.current) {
      eventSourceRef.current.close();
      eventSourceRef.current = null;
    }
    // Save partial content with interruption note.
    const partial = useChatStore.getState().streamingContent;
    if (partial && sessionId) {
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
    store().clearStream();
  }, [sessionId]);

  return {
    messages,
    isStreaming,
    streamingContent,
    statusMessage,
    circuitOpen,
    sessionTakeover,
    sendMessage,
    loadMessages,
    stopStreaming,
    retryStream,
    dismissCircuit,
    loadOlderMessages,
    hasOlderMessages,
    loadingOlder,
    jumpToMessage,
  };
}
