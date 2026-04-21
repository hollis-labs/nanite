import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, Bot, Info, Loader2 } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ScrollArea } from "@/components/ui/scroll-area";
import { api } from "@/lib/api";
import type { AgentMode, Message } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { useChatStore } from "@/stores/useChatStore";
import { useSettings } from "@/hooks/useSettings";
import { ApprovalCard } from "./ApprovalCard";
import { ChatMessage } from "./ChatMessage";
import { EnvelopeRenderer } from "./envelopes/EnvelopeRenderer";
import { CompactionDivider } from "./CompactionDivider";
import { ErrorBanner } from "./ErrorBanner";
import { MessageContent } from "./MessageContent";
import { ThinkingIndicator } from "./ThinkingIndicator";
import { ToolCallDisplay } from "./ToolCallDisplay";
import { ToolWarningBanner } from "./ToolWarningBanner";

const MODE_AVATAR_STYLES: Record<AgentMode, { bg: string; text: string }> = {
  default: { bg: "bg-mode-default/15", text: "text-mode-default" },
  architect: { bg: "bg-mode-architect/15", text: "text-mode-architect" },
  planner: { bg: "bg-mode-planner/15", text: "text-mode-planner" },
  writer: { bg: "bg-mode-writer/15", text: "text-mode-writer" },
};

interface ChatTranscriptProps {
  messages: Message[];
  isStreaming: boolean;
  streamingContent: string;
  onSendMessage?: (content: string) => void;
  onLoadOlder?: () => void;
  hasOlderMessages?: boolean;
  loadingOlder?: boolean;
}

export function ChatTranscript({
  messages,
  isStreaming,
  streamingContent,
  onSendMessage,
  onLoadOlder,
  hasOlderMessages,
  loadingOlder,
}: ChatTranscriptProps) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const activeMode = useChatStore((s) => s.activeMode);
  const toolCalls = useChatStore((s) => s.toolCalls);
  const toolWarnings = useChatStore((s) => s.toolWarnings);
  const textOnlyMode = useChatStore((s) => s.textOnlyMode);
  const toolCallDisplayMode = useChatStore((s) => s.toolCallDisplayMode);
  const saveToolCallDisplayMode = useChatStore((s) => s.saveToolCallDisplayMode);
  const loadToolCallDisplayMode = useChatStore((s) => s.loadToolCallDisplayMode);
  const pendingApprovals = useChatStore((s) => s.pendingApprovals);
  const pluginEnvelopes = useChatStore((s) => s.pluginEnvelopes);
  const loadSessionPluginEnvelopes = useChatStore((s) => s.loadSessionPluginEnvelopes);
  const scrollToMessageId = useChatStore((s) => s.scrollToMessageId);
  const setScrollToMessageId = useChatStore((s) => s.setScrollToMessageId);
  const { data: userSettings } = useSettings();
  const toolStreamBehavior = userSettings?.tool_stream_behavior ?? 'streaming';
  const chatErrors = useChatStore((s) => s.chatErrors);
  const dismissChatError = useChatStore((s) => s.dismissChatError);
  const activeSessionId = useAppStore((s) => s.activeSessionId);
  const queryClient = useQueryClient();

  // Load per-session tool call display mode when session changes.
  useEffect(() => {
    loadToolCallDisplayMode(activeSessionId ?? null);
  }, [activeSessionId, loadToolCallDisplayMode]);

  // Load per-session plugin envelopes when session changes. Plugin envelopes
  // are delivered by subprocess event hooks via the `plugin_envelope`
  // StreamEvent (BLG-20260413-012 / BLG-20260414-010) — session-scoped so
  // envelopes from one session don't bleed into another.
  useEffect(() => {
    loadSessionPluginEnvelopes(activeSessionId ?? null);
  }, [activeSessionId, loadSessionPluginEnvelopes]);

  // Track if user is scrolled to bottom - auto-scroll only when at bottom
  const [isAtBottom, setIsAtBottom] = useState(true);
  const [userHasScrolled, setUserHasScrolled] = useState(false);

  // Detect stalled stream — content stopped flowing but still streaming (tool calls, LLM thinking)
  const [streamStalled, setStreamStalled] = useState(false);
  const lastContentRef = useRef(streamingContent);
  const stallTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (!isStreaming) {
      setStreamStalled(false);
      if (stallTimerRef.current) {
        clearTimeout(stallTimerRef.current);
        stallTimerRef.current = null;
      }
      lastContentRef.current = "";
      return;
    }
    // Content changed — reset stall detection
    if (streamingContent !== lastContentRef.current) {
      lastContentRef.current = streamingContent;
      setStreamStalled(false);
      if (stallTimerRef.current) clearTimeout(stallTimerRef.current);
      // Start new stall timer — if no new content for 2s, show indicator
      if (streamingContent) {
        stallTimerRef.current = setTimeout(() => setStreamStalled(true), 2000);
      }
    }
  }, [isStreaming, streamingContent]);

  const cycleToolCallDisplayMode = useCallback(() => {
    const modes = ["indicator", "minimal", "compact", "full"] as const;
    const idx = modes.indexOf(toolCallDisplayMode);
    const nextIdx = idx === -1 ? 1 : (idx + 1) % modes.length;
    saveToolCallDisplayMode(activeSessionId ?? null, modes[nextIdx]);
  }, [toolCallDisplayMode, saveToolCallDisplayMode, activeSessionId]);

  const avatarStyle = MODE_AVATAR_STYLES[activeMode];

  // Check if user is near the bottom of the scroll area
  const checkScrollPosition = useCallback(() => {
    const scrollElement = scrollRef.current;
    if (!scrollElement) return;

    const { scrollTop, scrollHeight, clientHeight } = scrollElement;
    const distanceFromBottom = scrollHeight - scrollTop - clientHeight;

    // Use threshold: >100px = pause auto-scroll, <50px = resume auto-scroll
    if (distanceFromBottom > 100) {
      setIsAtBottom(false);
      setUserHasScrolled(true);
    } else if (distanceFromBottom < 50) {
      setIsAtBottom(true);
      setUserHasScrolled(false);
    }
  }, []);

  // Add scroll event listener
  useEffect(() => {
    const scrollElement = scrollRef.current;
    if (!scrollElement) return;

    scrollElement.addEventListener("scroll", checkScrollPosition);
    return () => {
      scrollElement.removeEventListener("scroll", checkScrollPosition);
    };
  }, [checkScrollPosition]);

  // Also check scroll position when content changes (not just user scroll)
  useLayoutEffect(() => {
    checkScrollPosition();
  }, [
    messages.length,
    streamingContent,
    toolCalls.length,
    toolWarnings.length,
    pendingApprovals.length,
    pluginEnvelopes.length,
    chatErrors.length,
    checkScrollPosition,
  ]);

  // Scroll-to-top detection for loading older messages
  const topSentinelRef = useRef<HTMLDivElement>(null);
  const prevMessagesLengthRef = useRef(messages.length);

  useEffect(() => {
    const el = topSentinelRef.current;
    if (!el || !hasOlderMessages || !onLoadOlder) return;
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting && !loadingOlder) {
          onLoadOlder();
        }
      },
      { rootMargin: "200px" },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [hasOlderMessages, onLoadOlder, loadingOlder]);

  // Preserve scroll position when prepending older messages
  useLayoutEffect(() => {
    const scrollElement = scrollRef.current;
    if (!scrollElement) return;
    const prevLen = prevMessagesLengthRef.current;
    const currLen = messages.length;
    if (currLen > prevLen && prevLen > 0) {
      // Messages were prepended if the first message ID changed
      // Maintain relative scroll position from the bottom
    }
    prevMessagesLengthRef.current = currLen;
  }, [messages.length]);

  // Fetch bookmarks for the active session
  const { data: bookmarks = [] } = useQuery({
    queryKey: ["bookmarks", activeSessionId],
    queryFn: () => api.listBookmarks(activeSessionId!),
    enabled: !!activeSessionId,
  });

  const bookmarkedMessageIds = useMemo(
    () => new Set(bookmarks.map((b) => b.message_id)),
    [bookmarks],
  );

  const toggleBookmarkMutation = useMutation({
    mutationFn: (messageId: string) => api.toggleBookmark(messageId, activeSessionId!),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["bookmarks", activeSessionId] });
    },
  });

  const handleToggleBookmark = useCallback(
    (messageId: string) => {
      if (!activeSessionId) return;
      toggleBookmarkMutation.mutate(messageId);
    },
    [activeSessionId, toggleBookmarkMutation],
  );

  // Scroll to bottom function
  const scrollToBottom = useCallback(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
    setIsAtBottom(true);
    setUserHasScrolled(false);
  }, []);

  // Scroll a specific message into view when a cross-session jump lands.
  // Notes on the design:
  //   - Uses requestAnimationFrame-based polling (max ~1s) because the effect
  //     fires as soon as `scrollToMessageId` is set, which can race ahead of
  //     the DOM commit for the messages fetched by useChat's pending-jump
  //     consumer. We retry until the target element exists.
  //   - The store state is cleared INSIDE the setTimeout (not eagerly) because
  //     this effect subscribes to `scrollToMessageId` — clearing it eagerly
  //     would re-run the effect, running the previous cleanup which would
  //     cancel the highlight-removal timer and leave the ring on-screen.
  //   - `suppressAutoScrollRef` prevents the auto-scroll-to-bottom effect
  //     from stealing focus on the same render.
  const suppressAutoScrollRef = useRef(false);
  useEffect(() => {
    if (!scrollToMessageId) return;
    const HIGHLIGHT_CLASSES = ["ring-2", "ring-primary/60", "rounded-md", "transition-shadow"] as const;
    let removeTimer: ReturnType<typeof setTimeout> | null = null;
    let rafId: number | null = null;
    let target: HTMLElement | null = null;
    const deadline = Date.now() + 1000; // give up polling after 1s

    const giveUp = () => {
      // Target never arrived. Clear the store state so the next jump can
      // retrigger and the app doesn't sit in "pending scroll" forever.
      console.warn(
        `[ChatTranscript] Jump target ${scrollToMessageId} not found within ${1000}ms — clearing pending scroll.`,
      );
      setScrollToMessageId(null);
    };

    const tryLocate = () => {
      const scrollElement = scrollRef.current;
      if (!scrollElement) {
        if (Date.now() < deadline) {
          rafId = requestAnimationFrame(tryLocate);
        } else {
          giveUp();
        }
        return;
      }
      target = scrollElement.querySelector<HTMLElement>(
        `[data-message-id="${scrollToMessageId}"]`,
      );
      if (!target) {
        if (Date.now() < deadline) {
          rafId = requestAnimationFrame(tryLocate);
        } else {
          giveUp();
        }
        return;
      }
      // Found it — scroll + highlight + schedule cleanup.
      suppressAutoScrollRef.current = true;
      target.scrollIntoView({ behavior: "smooth", block: "center" });
      target.classList.add(...HIGHLIGHT_CLASSES);
      setIsAtBottom(false);
      setUserHasScrolled(true);
      removeTimer = setTimeout(() => {
        target?.classList.remove(...HIGHLIGHT_CLASSES);
        setScrollToMessageId(null);
      }, 1500);
    };

    rafId = requestAnimationFrame(tryLocate);

    return () => {
      if (rafId !== null) cancelAnimationFrame(rafId);
      if (removeTimer !== null) clearTimeout(removeTimer);
      target?.classList.remove(...HIGHLIGHT_CLASSES);
    };
  }, [scrollToMessageId, setScrollToMessageId]);

  // Auto-scroll to bottom on new messages or streaming updates
  // Only when user is at bottom AND hasn't manually scrolled up
  useEffect(() => {
    if (suppressAutoScrollRef.current) {
      suppressAutoScrollRef.current = false;
      return;
    }
    if (isAtBottom && !userHasScrolled) {
      bottomRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [
    messages.length,
    streamingContent,
    toolCalls.length,
    toolWarnings.length,
    pendingApprovals.length,
    pluginEnvelopes.length,
    chatErrors.length,
    isAtBottom,
    userHasScrolled,
  ]);

  const userMessageCount = messages.filter((m) => m.role === "user").length;

  if (messages.length === 0 && !isStreaming) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <div className="text-center">
          <Bot className="w-16 h-16 text-fg-faint mx-auto mb-4" />
          <h2 className="text-lg font-medium text-fg-secondary mb-1">
            Start a conversation with Nanite
          </h2>
          <p className="text-xs text-fg-faint mt-1">Type a message below to begin</p>
        </div>
      </div>
    );
  }

  return (
    <ScrollArea className="flex-1 px-4 py-6 relative" ref={scrollRef}>
      <div className="max-w-3xl mx-auto space-y-6">
        {/* Sentinel for loading older messages */}
        {hasOlderMessages && (
          <div ref={topSentinelRef} className="flex items-center justify-center py-2">
            {loadingOlder ? (
              <div className="flex items-center gap-2 text-xs text-fg-muted">
                <Loader2 className="w-3.5 h-3.5 animate-spin" />
                Loading older messages...
              </div>
            ) : (
              <span className="text-[10px] text-fg-faint">Scroll up for older messages</span>
            )}
          </div>
        )}

        {/* Persistent text-only mode banner (agent has 0 MCP tools) */}
        {textOnlyMode && (
          <div className="flex items-center gap-2 px-3 py-2 rounded-md text-xs bg-surface border border-border-subtle text-fg-secondary">
            <Info className="w-3.5 h-3.5 shrink-0" />
            <span>This agent has no tools configured — responses are text-only</span>
          </div>
        )}

        {messages.map((msg, idx) => {
          // Show compaction divider before the first non-compacted message
          // when earlier messages were compacted
          let showCompactionDivider = false;
          if (idx > 0) {
            try {
              const prevMeta = JSON.parse(messages[idx - 1].metadata || '{}');
              const currMeta = JSON.parse(msg.metadata || '{}');
              if (prevMeta.compacted && !currMeta.compacted) {
                showCompactionDivider = true;
              }
            } catch { /* ignore parse errors */ }
          }

          return (
            <div key={msg.id}>
              {showCompactionDivider && <CompactionDivider />}
              <ChatMessage
                message={msg}
                isBookmarked={bookmarkedMessageIds.has(msg.id)}
                onToggleBookmark={handleToggleBookmark}
                {...(onSendMessage && { onSendMessage })}
              />
            </div>
          );
        })}

        {/* Tool call indicators — visibility controlled by tool_stream_behavior setting */}
        {toolStreamBehavior !== 'hidden' && toolCalls.length > 0 && (toolStreamBehavior === 'persist' || isStreaming) && (
          <div className="flex gap-3">
            <div
              className={`w-7 h-7 rounded-md flex items-center justify-center shrink-0 mt-0.5 ${avatarStyle.bg} ${avatarStyle.text}`}
            >
              <Bot className="w-4 h-4" />
            </div>
            <div className="flex-1 min-w-0">
              <ToolCallDisplay
                toolCalls={toolCalls}
                displayMode={toolCallDisplayMode}
                onCycleMode={cycleToolCallDisplayMode}
              />
            </div>
          </div>
        )}

        {/* Tool warning banner during streaming */}
        {isStreaming && toolWarnings.length > 0 && <ToolWarningBanner warnings={toolWarnings} />}

        {/* Approval cards — inline in the message stream */}
        {pendingApprovals.map((approval) => (
          <ApprovalCard key={approval.request_id} approval={approval} />
        ))}

        {/* Plugin-emitted envelopes (BLG-20260414-010) — standalone cards,
            not appended to any assistant message content. */}
        {pluginEnvelopes.map((item) => (
          <div
            key={item.id}
            data-plugin-envelope-id={item.id}
            data-plugin-id={item.pluginId}
          >
            <EnvelopeRenderer envelope={item.envelope} {...(onSendMessage && { onSendMessage })} userMessageCount={userMessageCount} />
          </div>
        ))}

        {/* Streaming message */}
        {isStreaming && streamingContent && (
          <div className="flex gap-3">
            <div
              className={`w-7 h-7 rounded-md flex items-center justify-center shrink-0 mt-0.5 ${avatarStyle.bg} ${avatarStyle.text}`}
            >
              <Bot className="w-4 h-4" />
            </div>
            <div className="flex-1 min-w-0">
              <div className="text-xs font-medium text-fg-muted mb-1">Nanite</div>
              <MessageContent content={streamingContent} role="assistant" />
              {streamStalled && <ThinkingIndicator />}
            </div>
          </div>
        )}

        {/* Thinking indicator — shown while streaming, before content arrives */}
        {isStreaming && !streamingContent && (
          <div className="flex gap-3">
            <div
              className={`w-7 h-7 rounded-md flex items-center justify-center shrink-0 mt-0.5 ${avatarStyle.bg} ${avatarStyle.text}`}
            >
              <Bot className="w-4 h-4" />
            </div>
            <div className="flex-1 min-w-0">
              <div className="text-xs font-medium text-fg-muted mb-1">Nanite</div>
              <ThinkingIndicator />
            </div>
          </div>
        )}

        {/* Error banners */}
        {chatErrors
          .filter((e) => !e.dismissed)
          .map((error) => (
            <ErrorBanner key={error.id} error={error} onDismiss={dismissChatError} />
          ))}

        <div ref={bottomRef} />
      </div>

      {/* Scroll to bottom button - shown when user has scrolled up */}
      {userHasScrolled && !isAtBottom && (
        <button
          onClick={scrollToBottom}
          className="absolute bottom-4 right-4 bg-primary hover:bg-primary-hover text-primary-foreground rounded-full p-3 shadow-lg transition-all duration-200 hover:scale-105"
          aria-label="Scroll to bottom"
        >
          <ArrowDown className="w-5 h-5" />
        </button>
      )}
    </ScrollArea>
  );
}
