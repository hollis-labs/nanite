import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Archive,
  ArchiveRestore,
  EyeOff,
  Loader2,
  MessageSquare,
  Pin,
  PinOff,
  Plus,
  Trash2,
} from "lucide-react";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip } from "@/components/ui/tooltip";
import { useSettings } from "@/hooks/useSettings";
import { usePluginSlots } from "@/hooks/usePluginSlots";
import { usePluginAction } from "@/hooks/usePluginAction";
import { resolveIcon } from "@/lib/icons";
import { api } from "@/lib/api";
import type { Session } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { useChatStore } from "@/stores/useChatStore";
import { useLayoutStore } from "@/stores/useLayoutStore";
import { ScopeSelector } from "./ScopeSelector";

function formatRelativeTime(dateStr: string): string {
  const date = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 1000 / 60);
  const diffHours = Math.floor(diffMins / 60);
  const diffDays = Math.floor(diffHours / 24);

  if (diffMins < 1) return "now";
  if (diffMins < 60) return `${diffMins}m`;
  if (diffHours < 24) return `${diffHours}h`;
  if (diffDays < 7) return `${diffDays}d`;
  return date.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function sortByActivity(a: Session, b: Session): number {
  return new Date(b.last_activity).getTime() - new Date(a.last_activity).getTime();
}

export function LeftSidebar() {
  const open = useLayoutStore((s) => s.leftSidebarOpen);
  const activeSessionId = useAppStore((s) => s.activeSessionId);
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId);
  const activeProjectId = useAppStore((s) => s.activeProjectId);
  const setActiveSession = useAppStore((s) => s.setActiveSession);
  const queryClient = useQueryClient();
  const activeStreams = useChatStore((s) => s.activeStreams);
  const pendingTools = useChatStore((s) => s.pendingTools);
  const cliActiveSessions = useChatStore((s) => s.cliActiveSessions);
  const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null);
  const [showArchived, setShowArchived] = useState(false);

  const archiveMutation = useMutation({
    mutationFn: ({ id, archived }: { id: string; archived: boolean }) =>
      api.updateSession(id, { status: archived ? "archived" : "active" } as Partial<Session>),
    onMutate: async ({ id, archived }) => {
      await queryClient.cancelQueries({ queryKey: ["sessions", activeWorkspaceId] });
      const prev = queryClient.getQueryData(["sessions", activeWorkspaceId]);
      queryClient.setQueryData(["sessions", activeWorkspaceId], (old: Session[] | undefined) =>
        old?.map((s) => (s.id === id ? { ...s, status: archived ? "archived" : "active" } : s)),
      );
      return { prev };
    },
    onSuccess: (_data, { id, archived }) => {
      if (archived && activeSessionId === id) setActiveSession("");
    },
    onError: (_err, _vars, context) => {
      if (context?.prev) queryClient.setQueryData(["sessions", activeWorkspaceId], context.prev);
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ["sessions"] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.deleteSession(id),
    onMutate: async (id) => {
      await queryClient.cancelQueries({ queryKey: ["sessions", activeWorkspaceId] });
      const prev = queryClient.getQueryData(["sessions", activeWorkspaceId]);
      queryClient.setQueryData(["sessions", activeWorkspaceId], (old: Session[] | undefined) =>
        old?.filter((s) => s.id !== id),
      );
      return { prev };
    },
    onSuccess: (_data, id) => {
      if (activeSessionId === id) setActiveSession("");
      setDeleteConfirmId(null);
    },
    onError: (_err, _id, context) => {
      if (context?.prev) queryClient.setQueryData(["sessions", activeWorkspaceId], context.prev);
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ["sessions"] });
    },
  });

  const { data: userSettings } = useSettings();

  const { data: sessions = [], isLoading } = useQuery({
    queryKey: ["sessions", activeWorkspaceId],
    queryFn: () => api.listSessions(activeWorkspaceId ?? undefined),
    enabled: !!activeWorkspaceId,
  });

  const createMutation = useMutation({
    mutationFn: () =>
      api.createSession({
        workspace_id: activeWorkspaceId!,
        project_id: activeProjectId ?? undefined,
        provider: userSettings?.default_provider || undefined,
        model: userSettings?.default_model || undefined,
        agent_id: userSettings?.default_agent || undefined,
      }),
    onSuccess: (newSession) => {
      void queryClient.invalidateQueries({ queryKey: ["sessions"] });
      setActiveSession(newSession.id);
    },
  });

  const pinMutation = useMutation({
    mutationFn: ({ id, pinned }: { id: string; pinned: boolean }) => api.pinSession(id, pinned),
    onMutate: async ({ id, pinned }) => {
      await queryClient.cancelQueries({ queryKey: ["sessions", activeWorkspaceId] });
      const prev = queryClient.getQueryData(["sessions", activeWorkspaceId]);
      queryClient.setQueryData(["sessions", activeWorkspaceId], (old: Session[] | undefined) =>
        old?.map((s) => (s.id === id ? { ...s, is_pinned: pinned } : s)),
      );
      return { prev };
    },
    onError: (_err, _vars, context) => {
      if (context?.prev) queryClient.setQueryData(["sessions", activeWorkspaceId], context.prev);
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ["sessions"] });
    },
  });

  // Filter by active project and archive status (client-side)
  const filteredSessions = useMemo(() => {
    let list = sessions;
    if (!showArchived) {
      list = list.filter((s: Session) => s.status !== "archived");
    }
    if (activeProjectId) {
      list = list.filter((s: Session) => s.project_id === activeProjectId);
    }
    return list;
  }, [sessions, activeProjectId, showArchived]);

  const archivedCount = useMemo(
    () => sessions.filter((s: Session) => s.status === "archived").length,
    [sessions],
  );

  const pinned = useMemo(
    () => filteredSessions.filter((s: Session) => s.is_pinned).sort(sortByActivity),
    [filteredSessions],
  );

  const unpinned = useMemo(
    () => filteredSessions.filter((s: Session) => !s.is_pinned).sort(sortByActivity),
    [filteredSessions],
  );

  // Progressive reveal — show 30 initially, load more on scroll
  const PAGE_SIZE = 30;
  const [visibleCount, setVisibleCount] = useState(PAGE_SIZE);
  const sentinelRef = useRef<HTMLDivElement>(null);

  // Reset visible count when project filter changes
  useEffect(() => {
    setVisibleCount(PAGE_SIZE);
  }, [activeProjectId]);

  const visibleUnpinned = useMemo(
    () => unpinned.slice(0, Math.max(0, visibleCount - pinned.length)),
    [unpinned, visibleCount, pinned.length],
  );
  const hasMore = pinned.length + visibleUnpinned.length < filteredSessions.length;

  // Intersection observer to load more
  useEffect(() => {
    const el = sentinelRef.current;
    if (!el || !hasMore) return;
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting) {
          setVisibleCount((c) => c + PAGE_SIZE);
        }
      },
      { rootMargin: "100px" },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [hasMore]);

  return (
    <aside
      className={`h-full bg-bg border-r border-border flex flex-col transition-all duration-200 ease-in-out overflow-hidden ${
        open ? "w-68" : "w-0"
      }`}
    >
      <div className="min-w-68 flex flex-col h-full">
        {/* Header — project dropdown + actions */}
        <div className="flex items-center gap-1 px-2 h-12 border-b border-border shrink-0">
          <div className="flex-1 min-w-0">
            {activeWorkspaceId ? (
              <ScopeSelector workspaceId={activeWorkspaceId} />
            ) : (
              <span className="text-sm font-semibold text-fg px-2">Chats</span>
            )}
          </div>
          <div className="flex items-center gap-0.5 shrink-0">
            {archivedCount > 0 && (
              <Tooltip content={showArchived ? "Hide archived" : `Show archived (${archivedCount})`} side="bottom">
                <Button
                  variant="ghost"
                  size="icon"
                  className="w-7 h-7 text-fg-secondary hover:text-fg"
                  onClick={() => setShowArchived((v) => !v)}
                >
                  {showArchived ? (
                    <EyeOff className="w-3.5 h-3.5" />
                  ) : (
                    <Archive className="w-3.5 h-3.5" />
                  )}
                </Button>
              </Tooltip>
            )}
            <Tooltip content="New chat" side="bottom">
              <Button
                variant="ghost"
                size="icon"
                className="w-7 h-7 text-fg-secondary hover:text-fg"
                onClick={() => createMutation.mutate()}
                disabled={!activeWorkspaceId || createMutation.isPending}
              >
                {createMutation.isPending ? (
                  <Loader2 className="w-4 h-4 animate-spin" />
                ) : (
                  <Plus className="w-4 h-4" />
                )}
              </Button>
            </Tooltip>
          </div>
        </div>

        {/* Chat list */}
        <ScrollArea className="flex-1 min-h-0">
          <div className="px-2 py-2">
            {isLoading && (
              <div className="flex flex-col gap-1 px-2">
                {Array.from({ length: 5 }).map((_, i) => (
                  <div key={i} className="flex items-center gap-2 px-2 py-1">
                    <Skeleton className="w-3.5 h-3.5 rounded" />
                    <Skeleton className="h-3 w-3/4 flex-1" />
                  </div>
                ))}
              </div>
            )}

            {!isLoading && filteredSessions.length === 0 && (
              <Empty className="py-8">
                <EmptyHeader>
                  <EmptyMedia variant="icon">
                    <MessageSquare />
                  </EmptyMedia>
                  <EmptyTitle className="text-sm">No chats yet</EmptyTitle>
                  <EmptyDescription className="text-xs">
                    Click + to start a conversation
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            )}

            {/* Pinned */}
            {pinned.length > 0 && (
              <>
                <ZoneHeader icon={<Pin className="w-3 h-3" />} label="Pinned" />
                {pinned.map((session: Session) => (
                  <ChatItem
                    key={session.id}
                    session={session}
                    isActive={session.id === activeSessionId}
                    onClick={() => setActiveSession(session.id)}
                    onTogglePin={() =>
                      pinMutation.mutate({ id: session.id, pinned: !session.is_pinned })
                    }
                    statusIndicator={getPresenceIndicator(
                      session.id,
                      activeStreams,
                      pendingTools,
                      cliActiveSessions,
                    )}
                    onDelete={() => setDeleteConfirmId(session.id)}
                    onArchive={() =>
                      archiveMutation.mutate({
                        id: session.id,
                        archived: session.status !== "archived",
                      })
                    }
                  />
                ))}
              </>
            )}

            {/* Recent */}
            {visibleUnpinned.length > 0 && (
              <>
                {pinned.length > 0 && <ZoneHeader label="Recent" />}
                {visibleUnpinned.map((session: Session) => (
                  <ChatItem
                    key={session.id}
                    session={session}
                    isActive={session.id === activeSessionId}
                    onClick={() => setActiveSession(session.id)}
                    onTogglePin={() =>
                      pinMutation.mutate({ id: session.id, pinned: !session.is_pinned })
                    }
                    statusIndicator={getPresenceIndicator(
                      session.id,
                      activeStreams,
                      pendingTools,
                      cliActiveSessions,
                    )}
                    onDelete={() => setDeleteConfirmId(session.id)}
                    onArchive={() =>
                      archiveMutation.mutate({
                        id: session.id,
                        archived: session.status !== "archived",
                      })
                    }
                  />
                ))}
              </>
            )}

            {/* Sentinel for infinite scroll */}
            {hasMore && (
              <div ref={sentinelRef} className="flex items-center justify-center py-3">
                <span className="text-[10px] text-fg-faint">Loading more...</span>
              </div>
            )}
          </div>
        </ScrollArea>
      </div>

      {/* Delete confirmation */}
      <AlertDialog
        open={!!deleteConfirmId}
        onOpenChange={(open) => {
          if (!open) setDeleteConfirmId(null);
        }}
      >
        <AlertDialogContent className="sm:max-w-md">
          <AlertDialogHeader>
            <AlertDialogTitle>Delete chat?</AlertDialogTitle>
            <AlertDialogDescription>
              This will permanently delete this chat and all its messages. This action cannot be
              undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => deleteConfirmId && deleteMutation.mutate(deleteConfirmId)}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </aside>
  );
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function PresenceDot({ variant }: { variant: "streaming" | "tool-pending" | "cli-active" }) {
  if (variant === "streaming") {
    return (
      <span
        className="w-2 h-2 rounded-full bg-success shrink-0 animate-pulse"
        title="Streaming"
      />
    );
  }
  if (variant === "cli-active") {
    return (
      <span
        className="w-2 h-2 rounded-full bg-info shrink-0 animate-pulse"
        title="CLI active"
      />
    );
  }
  return (
    <span className="w-2 h-2 rounded-full bg-warning shrink-0" title="Tool approval pending" />
  );
}

function getPresenceIndicator(
  sessionId: string,
  activeStreams: Map<string, unknown>,
  pendingTools: Map<string, unknown>,
  cliActiveSessions: Map<string, unknown>,
): ReactNode | undefined {
  if (pendingTools.has(sessionId)) return <PresenceDot variant="tool-pending" />;
  if (activeStreams.has(sessionId)) return <PresenceDot variant="streaming" />;
  if (cliActiveSessions.has(sessionId)) return <PresenceDot variant="cli-active" />;
  return undefined;
}

function ZoneHeader({ label, icon }: { label: string; icon?: ReactNode }) {
  return (
    <div className="px-2 pt-2 pb-0.5 text-xs font-medium text-fg-muted uppercase tracking-wider flex items-center gap-1">
      {icon}
      {label}
    </div>
  );
}

function ChatItem({
  session,
  isActive,
  onClick,
  onTogglePin,
  statusIndicator,
  onDelete,
  onArchive,
}: {
  session: Session;
  isActive: boolean;
  onClick: () => void;
  onTogglePin: () => void;
  statusIndicator?: ReactNode;
  onDelete?: () => void;
  onArchive?: () => void;
}) {
  const [hovered, setHovered] = useState(false);
  const displayTitle = session.custom_name || session.title || `Chat ${session.short_code}`;
  const isArchived = session.status === "archived";
  const sidebarSlots = usePluginSlots("session-sidebar");
  const sessionContextMenuSlots = usePluginSlots("context-menu:session");
  const handlePluginAction = usePluginAction();

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <button
          onClick={onClick}
          onMouseEnter={() => setHovered(true)}
          onMouseLeave={() => setHovered(false)}
          className={`relative w-full flex items-start gap-1.5 px-2 py-1 rounded-md text-left transition-colors group ${
            isActive
              ? "bg-surface/60 text-fg"
              : "text-fg-secondary hover:bg-surface/40 hover:text-fg"
          } ${isArchived ? "opacity-50" : ""}`}
        >
          {/* Session icon + presence */}
          <div className="relative shrink-0 mt-[5px]">
            {statusIndicator && (
              <div className="absolute -top-1 -left-1 z-10">{statusIndicator}</div>
            )}
            <div className="w-2.5 h-2.5 rounded-[2px] bg-fg-faint/40 border border-fg-faint/60" />
          </div>

          {/* Content */}
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-1">
              <span className="text-[13px] font-medium truncate flex-1 leading-snug">{displayTitle}</span>
              {session.is_pinned && <Pin className="w-3 h-3 text-fg-faint shrink-0" />}
              <span className={`text-[11px] text-fg-faint shrink-0 ${hovered ? "invisible" : ""}`}>
                {formatRelativeTime(session.last_activity)}
              </span>
              <span
                role="button"
                onClick={(e) => {
                  e.stopPropagation();
                  onTogglePin();
                }}
                className={`absolute right-2 p-0.5 rounded text-fg-muted hover:text-fg hover:bg-surface-hover transition-colors ${hovered ? "opacity-100" : "opacity-0 pointer-events-none"}`}
                aria-label={session.is_pinned ? "Unpin" : "Pin"}
              >
                {session.is_pinned ? <PinOff className="w-3 h-3" /> : <Pin className="w-3 h-3" />}
              </span>
            </div>
            <span className="block mt-px text-[11px] text-fg-faint font-mono leading-none">#{session.short_code}</span>
            {/* session-sidebar slot — plugin badges */}
            {sidebarSlots.length > 0 && (
              <div className="flex items-center gap-0.5 mt-0.5">
                {sidebarSlots.map((entry) => {
                  const PluginIcon = resolveIcon(entry.icon);
                  return (
                    <button
                      type="button"
                      key={entry.id}
                      className="appearance-none border-0 cursor-pointer inline-flex items-center gap-0.5 px-1 py-px text-[10px] text-fg-faint hover:text-fg-muted rounded bg-surface-hover/40 transition-colors"
                      onClick={(e) => {
                        e.stopPropagation();
                        handlePluginAction(entry);
                      }}
                      title={entry.label}
                    >
                      <PluginIcon className="w-2.5 h-2.5" />
                    </button>
                  );
                })}
              </div>
            )}
          </div>
        </button>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem onSelect={onTogglePin} className="gap-2 text-xs">
          {session.is_pinned ? <PinOff className="size-3.5" /> : <Pin className="size-3.5" />}
          {session.is_pinned ? "Unpin" : "Pin"}
        </ContextMenuItem>
        <ContextMenuItem onSelect={onArchive} className="gap-2 text-xs">
          {session.status === "archived" ? (
            <>
              <ArchiveRestore className="size-3.5" />
              Unarchive
            </>
          ) : (
            <>
              <Archive className="size-3.5" />
              Archive
            </>
          )}
        </ContextMenuItem>
        {/* context-menu:session slot — plugin items */}
        {sessionContextMenuSlots.length > 0 && (
          <>
            <ContextMenuSeparator />
            {sessionContextMenuSlots.map((entry) => {
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
          </>
        )}
        <ContextMenuSeparator />
        <ContextMenuItem
          onSelect={onDelete}
          className="gap-2 text-xs text-danger focus:text-danger"
        >
          <Trash2 className="size-3.5" />
          Delete
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}
