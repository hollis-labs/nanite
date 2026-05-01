import {
  Archive,
  ArchiveRestore,
  Bookmark,
  Clock3,
  Pencil,
  Pin,
  PinOff,
  Plus,
  Search,
  Settings,
} from "lucide-react";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";

interface LeftRailProps {
  open: boolean;
  workspaceHeader: ReactNode;
  onNewChat: () => void;
  onSearch: () => void;
  newChatDisabled?: boolean;
  newChatPending?: boolean;
  emptyState?: ReactNode;
  footer: ReactNode;
  showWorkspace?: boolean;
  showNewChat?: boolean;
  showSearch?: boolean;
}

interface LeftRailSectionHeaderProps {
  icon: "pinned" | "recent";
  label: string;
  count?: number;
}

interface LeftRailSessionRowProps {
  title: string;
  timeLabel: string;
  active?: boolean;
  archived?: boolean;
  statusIndicator?: ReactNode;
  pluginBadges?: ReactNode;
  onClick: () => void;
  onTogglePin: () => void;
  onToggleArchive: () => void;
  pinned?: boolean;
  editing?: boolean;
  onStartEdit: () => void;
  onCommitEdit: (value: string) => void;
  onCancelEdit: () => void;
}

interface LeftRailFooterProps {
  avatar: ReactNode;
  name: string;
  onProfile: () => void;
  onSettings: () => void;
  archiveToggle?: ReactNode;
}

export function LeftRail({
  open,
  workspaceHeader,
  onNewChat,
  onSearch,
  newChatDisabled = false,
  newChatPending = false,
  emptyState,
  footer,
  showWorkspace = true,
  showNewChat = true,
  showSearch = true,
}: LeftRailProps) {
  const showActionBar = showNewChat || showSearch;

  return (
    <aside
      className={cn(
        "h-full overflow-hidden border-r border-border bg-bg transition-all duration-200 ease-in-out",
        open ? "w-64" : "w-0",
      )}
    >
      <div className="flex h-full min-w-64 flex-col">
        {showWorkspace && (
          <div className="h-[52px] shrink-0 border-b border-border px-3">{workspaceHeader}</div>
        )}

        {showActionBar && (
          <div className="shrink-0 border-b border-border px-3 py-3">
            <div className="flex flex-col gap-2">
              {showNewChat && (
                <button
                  type="button"
                  onClick={onNewChat}
                  disabled={newChatDisabled}
                  className={cn(
                    "flex h-8 w-full items-center justify-center gap-2 rounded-[6px] bg-primary px-3 text-[12px] font-semibold text-primary-foreground transition-colors",
                    "disabled:cursor-not-allowed disabled:opacity-60",
                    !newChatDisabled && "hover:bg-primary-hover",
                  )}
                >
                  {newChatPending ? (
                    <span className="size-3.5 animate-spin rounded-full border border-primary-foreground/35 border-t-transparent" />
                  ) : (
                    <Plus className="size-3.5" strokeWidth={2.1} />
                  )}
                  <span>New chat</span>
                  <span className="rounded-[4px] bg-primary-foreground/18 px-1.5 py-px font-mono text-[10px] font-medium tracking-wide text-primary-foreground/90">
                    ⌘N
                  </span>
                </button>
              )}

              {showSearch && (
                <button
                  type="button"
                  onClick={onSearch}
                  className="flex h-8 w-full items-center gap-2 rounded-[6px] border border-border-subtle bg-bg-elevated px-3 text-left transition-colors hover:bg-surface"
                >
                  <Search className="size-3.5 text-fg-muted" strokeWidth={2} />
                  <span className="flex-1 truncate text-[12px] text-fg-faint">Search chats</span>
                  <span className="font-mono text-[10px] tracking-wide text-fg-faint">⌘K</span>
                </button>
              )}
            </div>
          </div>
        )}

        <div className="chat-scroll no-scrollbar min-h-0 flex-1 overflow-y-auto pb-3">
          {emptyState}
        </div>

        <div className="shrink-0 border-t border-border">{footer}</div>
      </div>
    </aside>
  );
}

export function LeftRailSectionHeader({ icon, label, count }: LeftRailSectionHeaderProps) {
  const Icon = icon === "pinned" ? Bookmark : Clock3;

  return (
    <div className="flex items-center gap-1.5 px-3 pt-3 pb-1.5">
      <Icon className="size-3 text-fg-muted" strokeWidth={1.9} />
      <span className="font-mono text-[10px] font-semibold uppercase tracking-[0.16em] text-fg-muted">
        {label}
      </span>
      {typeof count === "number" && (
        <span className="font-mono text-[10px] text-fg-faint">{count}</span>
      )}
      <div className="ml-1 h-px flex-1 bg-border" />
    </div>
  );
}

export function LeftRailSessionRow({
  title,
  timeLabel,
  active = false,
  archived = false,
  statusIndicator,
  pluginBadges,
  onClick,
  onTogglePin,
  onToggleArchive,
  pinned = false,
  editing = false,
  onStartEdit,
  onCommitEdit,
  onCancelEdit,
}: LeftRailSessionRowProps) {
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={() => {
        if (editing) return;
        onClick();
      }}
      onKeyDown={(event) => {
        if (editing) return;
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onClick();
        }
      }}
      className={cn(
        "group relative block w-full py-1.5 pl-7 pr-14 text-left transition-colors",
        archived && "opacity-55",
      )}
    >
      {active && <span className="absolute inset-y-0 left-0 w-0.5 bg-primary" />}
      {statusIndicator && <span className="absolute top-2.5 left-2">{statusIndicator}</span>}

      {!editing && !statusIndicator && (
        <button
          type="button"
          onClick={(event) => {
            event.stopPropagation();
            onStartEdit();
          }}
          className="absolute top-1/2 left-2 flex size-4 -translate-y-1/2 items-center justify-center rounded-[4px] text-fg-muted opacity-0 transition-opacity hover:bg-surface hover:text-fg group-hover:opacity-100"
          aria-label="Rename chat"
        >
          <Pencil className="size-3" strokeWidth={2} />
        </button>
      )}

      {editing ? (
        <SessionRowInlineEdit initial={title} onCommit={onCommitEdit} onCancel={onCancelEdit} />
      ) : (
        <div className="flex items-baseline">
          <span
            className={cn(
              "min-w-0 flex-1 truncate text-[12.5px] leading-[1.35] text-fg-secondary",
              active && "font-semibold text-fg",
            )}
          >
            {title}
          </span>
          <span className="ml-2 shrink-0 font-mono text-[10px] text-fg-faint transition-opacity group-hover:opacity-0">
            {timeLabel}
          </span>
        </div>
      )}

      {pluginBadges ? <div className="ml-1 mt-1 flex flex-wrap gap-1">{pluginBadges}</div> : null}

      {!editing && (
        <span className="absolute top-1/2 right-2 flex -translate-y-1/2 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
          <button
            type="button"
            onClick={(event) => {
              event.stopPropagation();
              onToggleArchive();
            }}
            className="flex size-5 items-center justify-center rounded-[4px] text-fg-muted transition-colors hover:bg-surface hover:text-fg"
            aria-label={archived ? "Unarchive" : "Archive"}
          >
            {archived ? (
              <ArchiveRestore className="size-3" strokeWidth={2} />
            ) : (
              <Archive className="size-3" strokeWidth={2} />
            )}
          </button>
          <button
            type="button"
            onClick={(event) => {
              event.stopPropagation();
              onTogglePin();
            }}
            className="flex size-5 items-center justify-center rounded-[4px] text-fg-muted transition-colors hover:bg-surface hover:text-fg"
            aria-label={pinned ? "Unpin" : "Pin"}
          >
            {pinned ? (
              <PinOff className="size-3" strokeWidth={2} />
            ) : (
              <Pin className="size-3" strokeWidth={2} />
            )}
          </button>
        </span>
      )}
    </div>
  );
}

function SessionRowInlineEdit({
  initial,
  onCommit,
  onCancel,
}: {
  initial: string;
  onCommit: (value: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState(initial);
  const inputRef = useRef<HTMLInputElement>(null);
  const committedRef = useRef(false);

  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);

  const commit = () => {
    if (committedRef.current) return;
    committedRef.current = true;
    const trimmed = value.trim();
    if (trimmed === initial.trim()) {
      onCancel();
      return;
    }
    onCommit(trimmed);
  };

  const cancel = () => {
    if (committedRef.current) return;
    committedRef.current = true;
    onCancel();
  };

  return (
    <input
      ref={inputRef}
      value={value}
      onChange={(e) => setValue(e.target.value)}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.key === "Enter") {
          e.preventDefault();
          commit();
        } else if (e.key === "Escape") {
          e.preventDefault();
          cancel();
        }
      }}
      onBlur={commit}
      className="w-full rounded-[4px] border border-border-subtle bg-bg-elevated px-1.5 py-0.5 text-[12.5px] leading-[1.35] text-fg outline-none focus:border-primary focus:ring-1 focus:ring-primary"
    />
  );
}

export function LeftRailFooter({
  avatar,
  name,
  onProfile,
  onSettings,
  archiveToggle,
}: LeftRailFooterProps) {
  return (
    <div className="flex items-center gap-2 px-2.5 py-2">
      <button
        type="button"
        onClick={onProfile}
        className="flex min-w-0 flex-1 items-center gap-2 rounded-[6px] px-0.5 py-0.5 text-left transition-colors hover:bg-surface"
      >
        {avatar}
        <span className="truncate text-[12px] text-fg-secondary">{name}</span>
      </button>

      {archiveToggle}

      <FooterIconButton onClick={onSettings} label="Settings">
        <Settings className="size-3.5" strokeWidth={2} />
      </FooterIconButton>
    </div>
  );
}

function FooterIconButton({
  children,
  onClick,
  label,
}: {
  children: ReactNode;
  onClick: () => void;
  label: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={label}
      aria-label={label}
      className="flex size-6 items-center justify-center rounded-[6px] text-fg-muted transition-colors hover:bg-surface hover:text-fg"
    >
      {children}
    </button>
  );
}
