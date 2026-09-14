import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, Bot, Info, Loader2, Sparkles } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ScrollArea } from "@/components/ui/scroll-area";
import { useTranscriptScroll } from "@/hooks/useTranscriptScroll";
import { api } from "@/lib/api";
import { shouldRenderStandalonePluginEnvelope } from "@/lib/envelope-lane";
import { messageProviderFailure } from "@/lib/provider-failure";
import type { Message } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import {
  useChatErrors,
  useChatStore,
  usePluginEnvelopes,
  useStreamingFinal,
  useStreamingNarration,
  useStreamingThinking,
  useTextOnlyMode,
  useToolCalls,
  useToolWarnings,
} from "@/stores/useChatStore";
import { cn } from "@/lib/utils";
import { ChatMessage } from "./ChatMessage";
import { CompactionDivider } from "./CompactionDivider";
import { ErrorBanner } from "./ErrorBanner";
import { EnvelopeRenderer } from "./envelopes/EnvelopeRenderer";
import { Envelope, EnvelopeHeader } from "./envelopes/primitives";
import { MessageContent } from "./MessageContent";
import { ThinkingIndicator } from "./ThinkingIndicator";
import { ToolWarningBanner } from "./ToolWarningBanner";

/**
 * POLISHED — chat transcript.
 *
 * Changes vs. original:
 *  - Avatar chip: 7×7 bg-mode/15 rounded-md became 8×8 bg-mode/10 rounded-[8px].
 *    Slightly larger for better optical balance with the polished envelope
 *    cards, and a lighter alpha so the saturated mode-colored icon inside
 *    leads instead of the background fighting it.
 *  - Assistant name label is now uppercase mono tracking-wide — matches the
 *    EnvelopeHeader grammar. Consistency between a standalone assistant
 *    message and a message containing an envelope.
 *  - Transcript vertical rhythm tightened: messages had space-y-6 (24px),
 *    now space-y-5 (20px). Reads tighter next to the denser envelope cards
 *    without feeling cramped.
 *  - Text-only mode banner moved to the Envelope primitive (neutral accent,
 *    info icon). Same info, system-consistent shape.
 *  - Empty state redesigned: Bot in a soft rounded-[14px] surface chip with a
 *    small Sparkles mark, mono "New session" caption, headline in fg-primary,
 *    subcopy in fg-muted. Still minimal; reads like part of the system not a
 *    placeholder left over from framework-days.
 *  - Scroll-to-bottom FAB: primary fill → surface-elevated with a 1px border
 *    and subtle shadow, matching the polished chrome. Still high-contrast
 *    with the arrow icon but doesn't shout from the corner.
 *  - Stream-stalled indicator still renders under streamed content, but now
 *    uses the new ThinkingIndicator shape (three dots, mono "thinking" label).
 */

interface ChatTranscriptProps {
  sessionId?: string;
  messagesReady?: boolean;
  oldestOffset?: number;
  messages: Message[];
  isStreaming: boolean;
  streamingContent: string;
  onSendMessage?: (content: string) => void;
  onRetry?: () => void;
  onLoadOlder?: () => void;
  hasOlderMessages?: boolean;
  loadingOlder?: boolean;
}

export function ChatTranscript({
  sessionId,
  messagesReady = true,
  oldestOffset = 0,
  messages,
  isStreaming,
  streamingContent,
  onSendMessage,
  onRetry,
  onLoadOlder,
  hasOlderMessages,
  loadingOlder,
}: ChatTranscriptProps) {
  const toolWarnings = useToolWarnings();
  const textOnlyMode = useTextOnlyMode();
  const scrollToMessageId = useChatStore((s) => s.scrollToMessageId);
  const setScrollToMessageId = useChatStore((s) => s.setScrollToMessageId);
  const chatErrors = useChatErrors();
  const dismissChatError = useChatStore((s) => s.dismissChatError);
  const activeSessionId = useAppStore((s) => s.activeSessionId);
  const modelUpdatePending = useIsMutating({ mutationKey: ["chat-model", sessionId ?? activeSessionId] }) > 0;
  const pluginEnvelopes = usePluginEnvelopes(activeSessionId);
  const queryClient = useQueryClient();
  const [dismissedFailures, setDismissedFailures] = useState<Set<string>>(() => new Set());
  const chooseModel = () => window.dispatchEvent(new CustomEvent("open-chat-model-picker", {
    detail: { sessionId: sessionId ?? activeSessionId },
  }));

  const currentSessionId = sessionId ?? activeSessionId;
  const toolCalls = useToolCalls(currentSessionId);
  const pendingTools = useChatStore((s) => s.pendingTools);
  const pendingTool = currentSessionId ? pendingTools.get(currentSessionId) : undefined;
  const runningToolCalls = useMemo(
    () => toolCalls.filter((tc) => tc.status === "running"),
    [toolCalls],
  );

  // F4 (CW-20260419-0029) — narration strip + collapse-pill.
  // F3 (CW-20260420-0023) — thinking strip.
  const streamingNarration = useStreamingNarration();
  const streamingFinal = useStreamingFinal();
  const streamingThinking = useStreamingThinking();

  const { scrollRef, bottomRef, userHasScrolled, scrollToBottom, detach } = useTranscriptScroll(
    sessionId ?? activeSessionId,
    messagesReady && (messages.length > 0 || isStreaming),
    oldestOffset,
    `${messages.length}:${streamingContent}:${toolWarnings.length}:${chatErrors.length}`,
    !!scrollToMessageId,
    messages.at(-1)?.id.startsWith("temp-") ? messages.at(-1)!.id : null,
  );

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
    if (streamingContent !== lastContentRef.current) {
      lastContentRef.current = streamingContent;
      setStreamStalled(false);
      if (stallTimerRef.current) clearTimeout(stallTimerRef.current);
      if (streamingContent) {
        stallTimerRef.current = setTimeout(() => setStreamStalled(true), 2000);
      }
    }
  }, [isStreaming, streamingContent]);

  const topSentinelRef = useRef<HTMLDivElement>(null);
  const prevMessagesLengthRef = useRef(messages.length);

  // biome-ignore lint/correctness/useExhaustiveDependencies: scrollRef is a stable viewport ref returned by the scroll hook.
  useEffect(() => {
    const el = topSentinelRef.current;
    if (!el || !hasOlderMessages || !onLoadOlder) return;
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting && !loadingOlder) onLoadOlder();
      },
      { root: scrollRef.current, rootMargin: "200px" },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [hasOlderMessages, onLoadOlder, loadingOlder]);

  useLayoutEffect(() => {
    prevMessagesLengthRef.current = messages.length;
  }, [messages.length]);

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

  // biome-ignore lint/correctness/useExhaustiveDependencies: the stable viewport ref is read when the jump runs.
  useEffect(() => {
    if (!scrollToMessageId) return;
    const HIGHLIGHT_CLASSES = [
      "ring-2",
      "ring-primary/60",
      "rounded-[10px]",
      "transition-shadow",
    ] as const;
    let removeTimer: ReturnType<typeof setTimeout> | null = null;
    let rafId: number | null = null;
    let target: HTMLElement | null = null;
    const deadline = Date.now() + 1000;

    const giveUp = () => {
      console.warn(
        `[ChatTranscript] Jump target ${scrollToMessageId} not found within 1000ms — clearing pending scroll.`,
      );
      setScrollToMessageId(null);
    };

    const tryLocate = () => {
      const scrollElement = scrollRef.current;
      if (!scrollElement) {
        if (Date.now() < deadline) rafId = requestAnimationFrame(tryLocate);
        else giveUp();
        return;
      }
      target = scrollElement.querySelector<HTMLElement>(`[data-message-id="${scrollToMessageId}"]`);
      if (!target) {
        if (Date.now() < deadline) rafId = requestAnimationFrame(tryLocate);
        else giveUp();
        return;
      }
      detach();
      target.scrollIntoView({ behavior: "smooth", block: "center" });
      target.classList.add(...HIGHLIGHT_CLASSES);

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
  }, [scrollToMessageId, setScrollToMessageId, detach]);

  const userMessageCount = messages.filter((m) => m.role === "user").length;

  /* ─────────────────── Empty state ─────────────────── */
  if (!messagesReady) return <div className="flex-1" role="status" aria-label="Loading conversation" />;

  if (messages.length === 0 && !isStreaming) {
    return (
      <div className="flex flex-1 items-center justify-center px-6">
        <div className="flex max-w-sm flex-col items-center text-center">
          <div className="relative mb-5 flex h-16 w-16 items-center justify-center rounded-[14px] border border-border-subtle bg-surface">
            <Bot className="h-7 w-7 text-fg-secondary" />
            <span className="absolute -right-1.5 -top-1.5 flex h-5 w-5 items-center justify-center rounded-full bg-primary text-primary-foreground">
              <Sparkles className="h-3 w-3" />
            </span>
          </div>
          <span className="mb-1 font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
            New session
          </span>
          <h2 className="text-base font-semibold text-fg">Start a conversation with Nanite</h2>
          <p className="mt-1 text-[13px] leading-relaxed text-fg-muted">
            Type a message below, use{" "}
            <code className="rounded-[4px] bg-surface px-1 py-0.5 font-mono text-[11px] text-fg-secondary">
              /
            </code>{" "}
            for commands, or{" "}
            <code className="rounded-[4px] bg-surface px-1 py-0.5 font-mono text-[11px] text-fg-secondary">
              @
            </code>{" "}
            to reference files.
          </p>
        </div>
      </div>
    );
  }

  return (
    <ScrollArea className="relative flex-1 overflow-x-hidden overscroll-x-none no-scrollbar px-4 py-6" ref={scrollRef}>
      <div className="mx-auto max-w-3xl w-full min-w-0 space-y-5">
        {/* Sentinel for loading older messages */}
        {hasOlderMessages && (
          <div ref={topSentinelRef} className="flex items-center justify-center py-2">
            {loadingOlder ? (
              <div className="flex items-center gap-2 font-mono text-[11px] uppercase tracking-wide text-fg-muted">
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                Loading older messages
              </div>
            ) : (
              <span className="font-mono text-[10px] uppercase tracking-wide text-fg-faint">
                Scroll up for older messages
              </span>
            )}
          </div>
        )}

        {/* Text-only mode banner — system-consistent envelope style */}
        {textOnlyMode && (
          <Envelope accent="neutral">
            <EnvelopeHeader icon={Info} label="Text-only mode" meta="No tools" />
            <div className="px-4 py-3 text-[13px] leading-relaxed text-fg-secondary">
              This agent has no tools configured. Use{" "}
              <code className="rounded-[4px] border border-border-subtle bg-surface px-1 py-0.5 font-mono text-[11px] text-fg-secondary">
                /
              </code>{" "}
              for commands or{" "}
              <code className="rounded-[4px] border border-border-subtle bg-surface px-1 py-0.5 font-mono text-[11px] text-fg-secondary">
                @
              </code>{" "}
              to reference files in the prompt.
            </div>
          </Envelope>
        )}

        {messages.map((msg, idx) => {
          const failure = messageProviderFailure(msg);
          const canRecover = idx === messages.length - 1;
          let showCompactionDivider = false;
          if (idx > 0) {
            try {
              const prevMeta = JSON.parse(messages[idx - 1].metadata || "{}");
              const currMeta = JSON.parse(msg.metadata || "{}");
              if (prevMeta.compacted && !currMeta.compacted) showCompactionDivider = true;
            } catch {
              /* ignore */
            }
          }
          return (
            <div key={msg.id} {...(failure ? { "data-message-id": msg.id } : {})}>
              {showCompactionDivider && <CompactionDivider />}
              {(!failure || failure.partial) && <ChatMessage
                message={msg}
                isBookmarked={bookmarkedMessageIds.has(msg.id)}
                onToggleBookmark={handleToggleBookmark}
                {...(onSendMessage && { onSendMessage })}
                userMessageCount={userMessageCount}
              />}
              {failure && !dismissedFailures.has(msg.id) && <ErrorBanner
                error={failure.error}
                onDismiss={(id) => setDismissedFailures((previous) => new Set([...previous, id]))}
                onRetry={canRecover ? onRetry : undefined}
                onChooseModel={canRecover ? chooseModel : undefined}
                busy={isStreaming || modelUpdatePending}
              />}
            </div>
          );
        })}

        {/* LOAD-BEARING — do not remove without explicit direction.
         *
         * Feeds standalone `plugin_envelope` SSE emissions into EnvelopeRenderer.
         * Five BE producers depend on it: chat_loop_budget_soft_warning,
         * chat_loop_terminated, ApprovalEmitterImpl (subagent-spawn-approval +
         * elicitation-prompt), recovery_envelope_sink, plugin-subprocess Deliver.
         *
         * Dropped in commit 585bc47 (2026-04-25 design-reference-final polish);
         * silently invisible for 14 days until restored by Phase 9 W2A (PR #133).
         * During that window, gated subagent spawns and MCP elicitation prompts
         * had no UI consumer.
         *
         * Locked by:
         *   ui/src/__tests__/chat-transcript-plugin-envelope-render.test.tsx
         *
         * Source incident: Tesseract `followups.nanite.dropped_plugin_envelopes_render_block`
         * (rev 01KR89NWKFY52QW17R8PPC8V5S). Skip-when-render_target branch routes
         * to drawer/panel inbox via panel-signal.ts (not the chat thread). */}
        {pluginEnvelopes.map((item) =>
          shouldRenderStandalonePluginEnvelope(item.envelope) ? (
            <div key={item.id} data-plugin-envelope-id={item.id} data-plugin-id={item.pluginId}>
              <EnvelopeRenderer
                envelope={item.envelope}
                {...(onSendMessage && { onSendMessage })}
                userMessageCount={userMessageCount}
              />
            </div>
          ) : null,
        )}

        {isStreaming && toolWarnings.length > 0 && <ToolWarningBanner warnings={toolWarnings} />}

        {/* Streaming message — F4 (CW-20260419-0029) narration strip + answer bubble */}
        {isStreaming && (
          <div className="flex gap-3">
            <div className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-[8px] bg-mode-default/10 text-mode-default">
              <Bot className="h-4 w-4" />
            </div>
            <div className="min-w-0 flex-1">
              <div className="mb-1 flex items-center gap-2 font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
                <span>Nanite</span>
                <span className="inline-flex items-center gap-1.5 text-primary text-[10px] font-normal normal-case">
                  <span className="h-1.5 w-1.5 rounded-full bg-primary animate-pulse" />
                  {runningToolCalls.length > 0
                    ? runningToolCalls.some((t) => t.tool.includes("subagent"))
                      ? "running subagent…"
                      : "running tools…"
                    : "generating…"}
                </span>
              </div>

              {/* Working strip — live while narration or thinking is arriving */}
              {(streamingNarration || streamingThinking) && (
                <div className="mb-2 rounded-[6px] border border-border-subtle bg-surface px-3 py-2 min-w-0">
                  <div className="font-mono text-[10px] uppercase tracking-wide text-fg-faint mb-1">
                    Working…
                  </div>
                  {/* F3: thinking content shown with italic muted styling to distinguish from narration */}
                  {streamingThinking && (
                    <div className="text-[12px] leading-relaxed text-fg-muted/70 italic line-clamp-2 mb-1 break-words [overflow-wrap:anywhere]">
                      {streamingThinking}
                    </div>
                  )}
                  {streamingNarration && (
                    <div className="text-[12px] leading-relaxed text-fg-muted line-clamp-3 break-words [overflow-wrap:anywhere]">
                      {streamingNarration}
                    </div>
                  )}
                </div>
              )}

              {/* Final answer area — renders as it arrives */}
              {streamingFinal && (
                <div className="min-w-0 max-w-full w-full">
                  <MessageContent content={streamingFinal} role="assistant" />
                </div>
              )}

              {/* Active / awaited child work (tools / subagents) */}
              {runningToolCalls.length > 0 && (
                <div className="mt-2 flex flex-col gap-1.5">
                  {runningToolCalls.map((tc) => {
                    const isSubagent = tc.tool.includes("subagent");
                    return (
                      <div
                        key={tc.id}
                        className={cn(
                          "flex items-center gap-2 rounded-[6px] border px-3 py-2 text-[12px] min-w-0",
                          isSubagent
                            ? "border-primary/30 bg-primary/5 text-fg-secondary"
                            : "border-border-subtle bg-surface text-fg-muted",
                        )}
                      >
                        <Loader2 className={cn("h-3.5 w-3.5 animate-spin shrink-0", isSubagent ? "text-primary" : "text-warning")} />
                        <span className={cn("font-mono text-[10px] uppercase font-semibold shrink-0", isSubagent ? "text-primary" : "text-fg-secondary")}>
                          {isSubagent ? "Subagent" : "Tool"}
                        </span>
                        <code className="font-mono text-[11px] text-fg shrink-0">
                          {tc.tool}
                        </code>
                        {tc.detail && (
                          <span className="text-[11px] text-fg-muted truncate font-mono min-w-0" title={tc.detail}>
                            {tc.detail}
                          </span>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}

              {/* Presence fallback when pendingTool is set but runningToolCalls is empty */}
              {runningToolCalls.length === 0 && pendingTool && (
                <div className="mt-2 flex items-center gap-2 rounded-[6px] border border-border-subtle bg-surface px-3 py-2 text-[12px] text-fg-muted min-w-0">
                  <Loader2 className="h-3.5 w-3.5 animate-spin text-warning shrink-0" />
                  <span className="font-mono text-[10px] uppercase font-semibold text-fg-secondary shrink-0">
                    {pendingTool.toolName.includes("subagent") ? "Subagent" : "Tool"}
                  </span>
                  <code className="font-mono text-[11px] text-fg shrink-0">
                    {pendingTool.toolName}
                  </code>
                </div>
              )}

              {/* Baseline thinking dots when no child work card is active */}
              {runningToolCalls.length === 0 && !pendingTool && (
                <div className={streamingFinal ? "mt-2" : ""}>
                  <ThinkingIndicator />
                </div>
              )}

              {streamStalled && runningToolCalls.length > 0 && <ThinkingIndicator />}
            </div>
          </div>
        )}

        {chatErrors
          .filter((e) => !e.dismissed && (!e.details?.message_id || !messages.some((msg) =>
            messageProviderFailure(msg)?.error.id === e.details?.message_id)))
          .map((error) => (
            <ErrorBanner
              key={error.id}
              error={error}
              onDismiss={(id) => activeSessionId && dismissChatError(activeSessionId, id)}
              onRetry={onRetry}
              onChooseModel={chooseModel}
              busy={isStreaming || modelUpdatePending}
            />
          ))}

        <div ref={bottomRef} />
      </div>

      {/* Scroll-to-bottom FAB — quieter than the original primary-fill */}
      {userHasScrolled && (
        <button
          type="button"
          onClick={scrollToBottom}
          className="absolute bottom-4 right-4 flex h-9 w-9 items-center justify-center rounded-[10px] border border-border-subtle bg-bg-elevated text-fg-secondary shadow-lg transition-all duration-200 hover:scale-105 hover:text-fg"
          aria-label="Scroll to bottom"
        >
          <ArrowDown className="h-4 w-4" />
        </button>
      )}
    </ScrollArea>
  );
}
