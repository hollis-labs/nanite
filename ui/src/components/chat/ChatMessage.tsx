import { BookmarkCheck, Bot, ChevronDown, ChevronRight, User } from "lucide-react";
import { useMemo, useState } from "react";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { usePluginAction } from "@/hooks/usePluginAction";
import { usePluginSlots } from "@/hooks/usePluginSlots";
import { useSettings } from "@/hooks/useSettings";
import { resolveIcon } from "@/lib/icons";
import type { Envelope, Message } from "@/lib/types";
import { useLayoutStore } from "@/stores/useLayoutStore";
import { ContentActions } from "./ContentActions";
import { EnvelopeRenderer } from "./envelopes/EnvelopeRenderer";
import { MessageContent } from "./MessageContent";
import { ShellMessage } from "./ShellMessage";

interface StructuredMessage {
  v: number;
  text: string;
  tier: string;
  hash?: string;
  envelopes?: Array<{ type: string; data: unknown }>;
  tool_calls?: Array<{ id: string; name: string; status: string; has_envelope?: boolean }>;
  flags: {
    truncated?: boolean;
    has_error?: boolean;
    provisional?: boolean;
  };
}

function parseStructuredContent(content: string): { text: string; structured?: StructuredMessage } {
  try {
    const parsed = JSON.parse(content);
    if (parsed && typeof parsed === "object" && parsed.v === 1) {
      return { text: parsed.text, structured: parsed as StructuredMessage };
    }
  } catch {
    // Not JSON — legacy raw text
  }
  return { text: content };
}

function formatRelativeTime(dateStr: string): string {
  const date = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffSecs = Math.floor(diffMs / 1000);
  const diffMins = Math.floor(diffSecs / 60);
  const diffHours = Math.floor(diffMins / 60);
  const diffDays = Math.floor(diffHours / 24);

  if (diffSecs < 60) return "just now";
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  if (diffDays < 7) return `${diffDays}d ago`;
  return date.toLocaleDateString();
}

// Default (non-user) message avatar style. Phase 0 item 21 ("Cut Modes, in
// full") removed the mode-indexed MODE_AVATAR_STYLES / MODE_LABEL_STYLES —
// the per-session `activeMode` dial they were keyed on had no real backend
// setter and always resolved to "default" in production; this is that
// same "default" style, now a plain constant instead of a lookup table.
const DEFAULT_AVATAR_STYLE = { bg: "bg-mode-default/15", text: "text-mode-default" };

// Agent colors for multi-agent sessions — deterministic by agent_id.
// These are identity markers (not semantic), so they stay as distinct hues.
const AGENT_COLORS = [
  { border: "ring-primary", badge: "bg-primary/15 text-primary" },
  { border: "ring-violet-500", badge: "bg-violet-500/15 text-violet-400" },
  { border: "ring-orange-500", badge: "bg-orange-500/15 text-warning" },
  { border: "ring-pink-500", badge: "bg-pink-500/15 text-pink-400" },
  { border: "ring-brand", badge: "bg-brand-muted text-brand" },
];

function agentColorIndex(agentId: string): number {
  let hash = 0;
  for (let i = 0; i < agentId.length; i++) {
    hash = (hash * 31 + agentId.charCodeAt(i)) | 0;
  }
  return Math.abs(hash) % AGENT_COLORS.length;
}

interface ChatMessageProps {
  message: Message;
  isBookmarked?: boolean;
  onToggleBookmark?: (messageId: string) => void;
  onSendMessage?: (content: string) => void;
  agentName?: string;
  isMultiAgent?: boolean;
  userMessageCount?: number;
}

export function ChatMessage({
  message,
  isBookmarked = false,
  onToggleBookmark,
  onSendMessage,
  agentName,
  isMultiAgent = false,
  userMessageCount,
}: ChatMessageProps) {
  const [hovered, setHovered] = useState(false);
  const { data: settings } = useSettings();
  const userAvatarUrl = (settings?.ext_settings?.avatar_url as string) || "";
  const userDisplayName = (settings?.ext_settings?.display_name as string) || "";
  const messageHeaderSlots = usePluginSlots("message-header");
  const messageActionSlots = usePluginSlots("message-actions");
  const contextMenuSlots = usePluginSlots("context-menu:message");
  const handlePluginAction = usePluginAction();

  // F4 (CW-20260419-0029) — narration collapse-pill.
  // dev-mode: default expanded; otherwise collapsed.
  const developerMode = settings?.developer_mode ?? false;
  const [narrationExpanded, setNarrationExpanded] = useState(developerMode);

  // Parse structured message format (v=1) or fall through to legacy raw text.
  const { text: displayText, structured } = useMemo(
    () => parseStructuredContent(message.content),
    [message.content],
  );

  // Detect shell_exec messages from metadata
  const shellMeta = useMemo(() => {
    if (message.role !== "user" || !message.metadata) return null;
    try {
      const meta =
        typeof message.metadata === "string" ? JSON.parse(message.metadata) : message.metadata;
      if (meta?.type === "shell_exec" && meta.shell_exec) {
        return meta.shell_exec as {
          command: string;
          exit_code: number;
          duration_ms: number;
          truncated: boolean;
        };
      }
    } catch {
      /* not JSON */
    }
    return null;
  }, [message.role, message.metadata]);

  // F4 (CW-20260419-0029) — narration thinking stored in metadata.thinking.
  // Old messages without narration render as-is; new messages show the pill.
  const narrationThinking = useMemo(() => {
    if (message.role !== "assistant" || !message.metadata) return null;
    try {
      const meta =
        typeof message.metadata === "string" ? JSON.parse(message.metadata) : message.metadata;
      return typeof meta?.thinking === "string" && meta.thinking.length > 0
        ? (meta.thinking as string)
        : null;
    } catch {
      /* not JSON */
    }
    return null;
  }, [message.role, message.metadata]);

  // F3 (CW-20260420-0023) — signed thinking blocks stored in metadata.thinking_blocks.
  // Round-tripped from the Anthropic interleaved-thinking beta. Rendered with
  // a distinct "thinking" badge inside the expanded pill.
  const thinkingBlocks = useMemo(() => {
    if (message.role !== "assistant" || !message.metadata) return null;
    try {
      const meta =
        typeof message.metadata === "string" ? JSON.parse(message.metadata) : message.metadata;
      if (!Array.isArray(meta?.thinking_blocks) || meta.thinking_blocks.length === 0) return null;
      return meta.thinking_blocks as Array<{ thinking: string; signature: string }>;
    } catch {
      /* not JSON */
    }
    return null;
  }, [message.role, message.metadata]);

  // Rough step count: split by newlines as narration is stored paragraph-per-iteration.
  const narrationStepCount = useMemo(() => {
    if (!narrationThinking) return 0;
    return narrationThinking.split("\n").filter((l: string) => l.trim().length > 0).length;
  }, [narrationThinking]);

  const isShellExec = shellMeta !== null;
  const isUser = message.role === "user";
  const avatarStyle = isUser ? { bg: "bg-surface", text: "text-fg-secondary" } : DEFAULT_AVATAR_STYLE;

  // Parse envelope — from saved envelope field or from streaming content.
  const envelope = useMemo<Envelope | null>(() => {
    // Helper: merge an array of envelopes into one.
    const mergeEnvelopes = (arr: Envelope[]): Envelope => {
      const merged: Envelope = { kind: arr[0].kind, version: arr[0].version, type: arr[0].type };
      for (const env of arr) {
        if (env.proposals) merged.proposals = [...(merged.proposals ?? []), ...env.proposals];
        if (env.approval && !merged.approval) merged.approval = env.approval;
        if (env.status && !merged.status) merged.status = env.status;
        if (env.data && !merged.data) {
          merged.data = env.data;
          merged.type = env.type;
        }
      }
      return merged;
    };

    // 1. Try the saved envelope field (set after message is persisted).
    if (message.envelope) {
      try {
        const raw =
          typeof message.envelope === "string" ? JSON.parse(message.envelope) : message.envelope;
        if (Array.isArray(raw) && raw.length > 0) return mergeEnvelopes(raw as Envelope[]);
        if (raw && typeof raw === "object" && !Array.isArray(raw)) return raw as Envelope;
      } catch {
        /* ignore */
      }
    }

    // 2. During streaming, extract from content (envelope field not set yet).
    if (message.content) {
      const pattern = /```nanite-envelope\s*\n([\s\S]*?)```/g;
      const envelopes: Envelope[] = [];
      let match;
      while ((match = pattern.exec(message.content)) !== null) {
        try {
          envelopes.push(JSON.parse(match[1].trim()));
        } catch {
          /* incomplete JSON during streaming — skip */
        }
      }
      if (envelopes.length > 0) return mergeEnvelopes(envelopes);
    }

    return null;
  }, [message.envelope, message.content]);

  // Shell exec messages get a distinct full-width layout — no avatar, centered to match composer width
  if (isShellExec && shellMeta) {
    return (
      <div
        className="w-full group"
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
        data-message-id={message.id}
      >
        <div className="flex justify-end mb-1">
          <span className="text-xs text-fg-faint">
            {userDisplayName || "You"}
            {hovered && ` · ${formatRelativeTime(message.created_at)}`}
          </span>
        </div>
        <ShellMessage content={displayText} meta={shellMeta} />
      </div>
    );
  }

  const messageBody = (
    <div
      className={`flex gap-3 group ${isUser ? "flex-row-reverse" : ""}`}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      data-message-id={message.id}
    >
      {/* Avatar */}
      <div
        className={`w-7 h-7 rounded-md flex items-center justify-center shrink-0 mt-0.5 overflow-hidden ${avatarStyle.bg} ${avatarStyle.text} ${
          isMultiAgent && !isUser && message.agent_id
            ? `ring-2 ${AGENT_COLORS[agentColorIndex(message.agent_id)].border}`
            : ""
        }`}
      >
        {isUser ? (
          userAvatarUrl ? (
            <img
              src={userAvatarUrl}
              alt={userDisplayName || "You"}
              className="w-full h-full object-cover"
            />
          ) : (
            <User className="w-4 h-4" />
          )
        ) : (
          <Bot className="w-4 h-4" />
        )}
      </div>

      {/* Content */}
      <div className={`flex-1 min-w-0 ${isUser ? "flex flex-col items-end" : ""}`}>
        <div className="flex items-center gap-2 mb-1">
          <span className="text-xs font-medium text-fg-muted">
            {isUser ? userDisplayName || "You" : agentName || "Nanite"}
          </span>
          {isMultiAgent && !isUser && message.agent_id && (
            <span
              className={`text-xs px-1.5 py-0.5 rounded-full ${AGENT_COLORS[agentColorIndex(message.agent_id)].badge}`}
            >
              agent
            </span>
          )}
          {hovered && (
            <span className="text-xs text-fg-faint">{formatRelativeTime(message.created_at)}</span>
          )}
          {/* Persistent bookmark indicator */}
          {isBookmarked && !hovered && <BookmarkCheck className="w-3.5 h-3.5 text-warning" />}
          {/* message-header slot — plugin badges/tags */}
          {messageHeaderSlots.length > 0 &&
            messageHeaderSlots.map((entry) => {
              const PluginIcon = resolveIcon(entry.icon);
              return (
                <button
                  key={entry.id}
                  type="button"
                  className="flex items-center gap-0.5 px-1.5 py-0.5 text-[11px] text-fg-muted hover:text-fg rounded-full bg-surface-hover/50 hover:bg-surface-hover transition-colors"
                  onClick={() => handlePluginAction(entry)}
                >
                  <PluginIcon className="w-3 h-3" />
                  <span>{entry.label}</span>
                </button>
              );
            })}
        </div>
        {/* F4 (CW-20260419-0029) + F3 (CW-20260420-0023) — collapse-pill for agent process */}
        {!isUser && (narrationThinking || thinkingBlocks) && (
          <div className="mb-2">
            <button
              type="button"
              onClick={() => setNarrationExpanded((v) => !v)}
              className="flex items-center gap-1.5 rounded-[6px] border border-border-subtle bg-surface px-2.5 py-1.5 text-[11px] font-mono uppercase tracking-wide text-fg-faint hover:text-fg-muted hover:bg-surface-hover transition-colors"
              aria-expanded={narrationExpanded}
            >
              {narrationExpanded ? (
                <ChevronDown className="w-3 h-3" />
              ) : (
                <ChevronRight className="w-3 h-3" />
              )}
              {narrationExpanded
                ? "Hide agent process"
                : `Agent worked through ${narrationStepCount} step${narrationStepCount !== 1 ? "s" : ""}`}
            </button>
            {narrationExpanded && (
              <div className="mt-1.5 rounded-[6px] border border-border-subtle bg-surface px-3 py-2.5 text-[12px] leading-relaxed text-fg-muted whitespace-pre-wrap">
                {/* F3: thinking blocks rendered with a "thinking" badge, italicized */}
                {thinkingBlocks &&
                  thinkingBlocks.map((tb, idx) => (
                    <div key={idx} className="mb-2">
                      <span className="inline-block mb-1 font-mono text-[10px] uppercase tracking-wide text-fg-faint border border-border-subtle rounded px-1 py-0.5">
                        thinking
                      </span>
                      <p className="text-fg-muted/80 italic">{tb.thinking}</p>
                    </div>
                  ))}
                {narrationThinking && <div>{narrationThinking}</div>}
              </div>
            )}
          </div>
        )}
        {
          <div
            className={`${
              isUser ? "bg-surface rounded-2xl rounded-tr-sm px-4 py-2.5 max-w-[80%]" : "max-w-full"
            }`}
          >
            <MessageContent content={displayText} role={message.role} />
          </div>
        }

        {/* Truncation banner for structured messages */}
        {structured?.flags?.truncated && (
          <div className="mt-2 px-3 py-1.5 text-xs text-warning border border-warning/50 rounded bg-warning/10">
            Response was cut short due to length limits
          </div>
        )}

        {/* Envelope rendering — stop pointer propagation so interactive envelope
            elements (checkboxes, buttons) aren't swallowed by ContextMenuTrigger.
            A2 — when render_target is set and was not blocked, render a stub
            link instead of the full envelope; the card itself lands in the
            named panel's inbox slot via applyEnvelopePanelEffects. Blocked
            routings fall through to inline so the user still sees the
            content, with a debug pill noting the block reason. */}
        {envelope && !isUser && (
          <div className="mt-3" onPointerDownCapture={(e) => e.stopPropagation()}>
            {envelope.render_target && !envelope.render_target_blocked ? (
              <RenderTargetStub envelope={envelope} />
            ) : (
              <>
                <EnvelopeRenderer
                  envelope={envelope}
                  onSendMessage={onSendMessage}
                  userMessageCount={userMessageCount}
                />
                {envelope.render_target_blocked && (
                  <RenderTargetBlockedPill
                    reason={envelope.render_target_blocked}
                    attemptedTarget={envelope.render_target}
                  />
                )}
              </>
            )}
          </div>
        )}

        {/* Actions — always rendered to avoid layout shift, opacity toggles on hover */}
        {!isUser && (
          <ContentActions
            content={displayText}
            messageId={message.id}
            isBookmarked={isBookmarked}
            onToggleBookmark={onToggleBookmark}
            visible={hovered}
            className="mt-1"
          />
        )}
        {/* message-actions slot — plugin action buttons per message */}
        {messageActionSlots.length > 0 && (
          <div
            className={`flex items-center gap-0.5 mt-0.5 transition-opacity ${hovered ? "opacity-100" : "opacity-0"}`}
          >
            {messageActionSlots.map((entry) => {
              const PluginIcon = resolveIcon(entry.icon);
              return (
                <button
                  key={entry.id}
                  type="button"
                  className="flex items-center gap-1 px-1.5 py-0.5 text-xs text-fg-muted hover:text-fg hover:bg-surface rounded transition-colors"
                  onClick={() => handlePluginAction(entry)}
                  title={entry.label}
                >
                  <PluginIcon className="w-3.5 h-3.5" />
                  <span>{entry.label}</span>
                </button>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );

  // Wrap in context menu if plugin items are registered
  if (contextMenuSlots.length === 0) return messageBody;

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>{messageBody}</ContextMenuTrigger>
      <ContextMenuContent>
        {contextMenuSlots.map((entry) => {
          const PluginIcon = resolveIcon(entry.icon);
          return (
            <ContextMenuItem
              key={entry.id}
              onSelect={() => handlePluginAction(entry)}
              className="gap-2 text-xs"
            >
              <PluginIcon className="size-3.5" />
              {entry.label}
            </ContextMenuItem>
          );
        })}
      </ContextMenuContent>
    </ContextMenu>
  );
}

// A2 — CW-20260428-0008. RenderTargetStub stands in for the full envelope
// in chat when the card has been routed to a panel inbox slot. Click
// re-opens the panel so the user can find the routed card.
function RenderTargetStub({ envelope }: { envelope: Envelope }) {
  const setChatWorkingDrawer = useLayoutStore((s) => s.setChatWorkingDrawer);
  const setPanelOpen = useLayoutStore((s) => s.setPanelOpen);
  const target = envelope.render_target ?? "";
  const label = envelope.title ? `${envelope.type}: ${envelope.title}` : envelope.type;
  const handleClick = () => {
    if (target === "bottom_chat_drawer") {
      // The chat working drawer is the new home for `render_target=bottom_chat_drawer`
      // payloads (per spec — routing key kept for back-compat). The envelope
      // has already been appended as a `card:<id>` dynamic tab; focus it if we
      // can, otherwise just open the drawer.
      const activeTab = envelope.id ? `card:${envelope.id}` : undefined;
      setChatWorkingDrawer({ open: true, ...(activeTab ? { activeTab } : {}) });
    } else if (target) {
      setPanelOpen(target, "user");
    }
  };
  return (
    <button
      type="button"
      onClick={handleClick}
      className="flex items-center gap-2 px-3 py-2 rounded-md border border-border bg-surface text-xs text-fg-muted hover:text-fg hover:bg-surface/80 transition-colors"
      title={`Open ${target} to see this card`}
    >
      <span aria-hidden>📎</span>
      <span className="truncate">
        Sent <span className="font-medium text-fg">{label}</span> to{" "}
        <span className="font-mono text-fg-faint">{target}</span>
      </span>
    </button>
  );
}

// A2 — debug pill rendered next to an inline envelope when the backend
// rejected an explicit render_target at the trust gate. Surfaces the
// blocked-reason so the agent's intent is visible without requiring the
// inspector view.
function RenderTargetBlockedPill({
  reason,
  attemptedTarget,
}: {
  reason: string;
  attemptedTarget?: string;
}) {
  return (
    <div className="mt-2 inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-[11px] text-warning border border-warning/40 bg-warning/10">
      <span aria-hidden>⚠️</span>
      <span>
        Routing to <span className="font-mono">{attemptedTarget || "unknown"}</span> blocked:{" "}
        {reason}
      </span>
    </div>
  );
}
