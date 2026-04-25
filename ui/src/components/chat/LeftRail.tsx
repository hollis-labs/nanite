import {
  Bookmark,
  Clock3,
  Moon,
  Pin,
  PinOff,
  Plus,
  Search,
  Settings,
  Sun,
} from "lucide-react";
import type { ReactNode } from "react";
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
  shortCode: string;
  timeLabel: string;
  active?: boolean;
  archived?: boolean;
  statusIndicator?: ReactNode;
  pluginBadges?: ReactNode;
  onClick: () => void;
  onTogglePin: () => void;
  pinned?: boolean;
}

interface LeftRailFooterProps {
  avatar: ReactNode;
  name: string;
  onProfile: () => void;
  onThemeToggle: () => void;
  onSettings: () => void;
  archiveToggle?: ReactNode;
  darkMode: boolean;
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
  const showActionBar = showNewChat || showSearch

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

export function LeftRailSectionHeader({
  icon,
  label,
  count,
}: LeftRailSectionHeaderProps) {
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
  shortCode,
  timeLabel,
  active = false,
  archived = false,
  statusIndicator,
  pluginBadges,
  onClick,
  onTogglePin,
  pinned = false,
}: LeftRailSessionRowProps) {
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onClick();
        }
      }}
      className={cn(
        "group relative block w-full px-3 py-1.5 text-left transition-colors",
        archived && "opacity-55",
      )}
    >
      {active && <span className="absolute inset-y-0 left-0 w-0.5 bg-primary" />}
      {statusIndicator && <span className="absolute top-2.5 left-1.5">{statusIndicator}</span>}

      <div className="flex items-baseline gap-2 pr-7">
        <span
          className={cn(
            "w-9 shrink-0 font-mono text-[10px] tracking-wide text-fg-faint",
            active && "font-semibold text-primary",
          )}
        >
          #{shortCode}
        </span>
        <span
          className={cn(
            "min-w-0 flex-1 truncate text-[12.5px] leading-[1.35] text-fg-secondary",
            active && "font-semibold text-fg",
          )}
        >
          {title}
        </span>
        <span className="shrink-0 font-mono text-[10px] text-fg-faint transition-opacity group-hover:opacity-0">
          {timeLabel}
        </span>
      </div>

      {pluginBadges ? <div className="ml-11 mt-1 flex flex-wrap gap-1">{pluginBadges}</div> : null}

      <span className="absolute top-1/2 right-2 -translate-y-1/2 opacity-0 transition-opacity group-hover:opacity-100">
        <button
          type="button"
          onClick={(event) => {
            event.stopPropagation();
            onTogglePin();
          }}
          className="flex size-5 items-center justify-center rounded-[4px] text-fg-muted transition-colors hover:bg-surface hover:text-fg"
          aria-label={pinned ? "Unpin" : "Pin"}
        >
          {pinned ? <PinOff className="size-3" strokeWidth={2} /> : <Pin className="size-3" strokeWidth={2} />}
        </button>
      </span>
    </div>
  );
}

export function LeftRailFooter({
  avatar,
  name,
  onProfile,
  onThemeToggle,
  onSettings,
  archiveToggle,
  darkMode,
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

      <FooterIconButton onClick={onThemeToggle} label={darkMode ? "Light mode" : "Dark mode"}>
        {darkMode ? <Sun className="size-3.5" strokeWidth={2} /> : <Moon className="size-3.5" strokeWidth={2} />}
      </FooterIconButton>

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
