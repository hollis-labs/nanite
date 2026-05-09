import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Archive,
  ArchiveRestore,
  EyeOff,
  MessageSquare,
  Pencil,
  Pin,
  PinOff,
  Trash2,
  User,
} from "lucide-react";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import {
  LeftRail,
  LeftRailFooter,
  LeftRailSectionHeader,
  LeftRailSessionRow,
} from "@/components/chat/LeftRail";
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
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip } from "@/components/ui/tooltip";
import { updateSettingsHash } from "@/hooks/useHashRoute";
import { usePluginAction } from "@/hooks/usePluginAction";
import { usePluginSlots } from "@/hooks/usePluginSlots";
import { useSettings } from "@/hooks/useSettings";
import { api } from "@/lib/api";
import { resolveIcon } from "@/lib/icons";
import type { Session } from "@/lib/types";
import { useAppStore } from "@/stores/useAppStore";
import { useChatStore } from "@/stores/useChatStore";
import { useLayoutStore } from "@/stores/useLayoutStore";
import { useNavigationStore } from "@/stores/useNavigationStore";
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
  const showWorkspace = useLayoutStore((s) => s.leftRailWorkspaceVisible);
  const showNewChat = useLayoutStore((s) => s.leftRailNewChatVisible);
  const showSearch = useLayoutStore((s) => s.leftRailSearchVisible);
  const setCurrentPage = useLayoutStore((s) => s.setCurrentPage);
  const activeSessionId = useAppStore((s) => s.activeSessionId);
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId);
  const activeProjectId = useAppStore((s) => s.activeProjectId);
  const setActiveSession = useAppStore((s) => s.setActiveSession);
  const navPush = useNavigationStore((s) => s.push);
  const queryClient = useQueryClient();
  const activeStreams = useChatStore((s) => s.activeStreams);
  const pendingTools = useChatStore((s) => s.pendingTools);
  const cliActiveSessions = useChatStore((s) => s.cliActiveSessions);
  const removeSessionState = useChatStore((s) => s.removeSession);
  const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null);
  const [showArchived, setShowArchived] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);

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
      if (archived) {
        // Drop the per-session chat-store slice so an archived session does
        // not retain streaming/error state in memory until the next page load.
        removeSessionState(id);
        if (activeSessionId === id) setActiveSession("");
      }
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
      removeSessionState(id);
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

  const renameMutation = useMutation({
    mutationFn: ({ id, name }: { id: string; name: string }) =>
      api.updateSession(id, { custom_name: name } as Partial<Session>),
    onMutate: async ({ id, name }) => {
      await queryClient.cancelQueries({ queryKey: ["sessions", activeWorkspaceId] });
      const prev = queryClient.getQueryData(["sessions", activeWorkspaceId]);
      queryClient.setQueryData(["sessions", activeWorkspaceId], (old: Session[] | undefined) =>
        old?.map((s) => (s.id === id ? { ...s, custom_name: name } : s)),
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

  const displayName =
    (userSettings?.ext_settings?.display_name as string) ||
    (userSettings?.ext_settings?.email as string) ||
    "Profile";
  const avatarUrl = (userSettings?.ext_settings?.avatar_url as string) || "";
  const openSearch = () => {
    window.dispatchEvent(new CustomEvent("open-search"));
  };

  const openProfile = () => {
    setCurrentPage("settings");
    updateSettingsHash("profile");
    navPush({ view: "settings/profile", label: "Profile" });
  };

  const openSettings = () => {
    setCurrentPage("settings");
    window.location.hash = "#settings";
  };

  return (
    <>
      <LeftRail
        open={open}
        workspaceHeader={
          activeWorkspaceId ? (
            <ScopeSelector workspaceId={activeWorkspaceId} />
          ) : (
            <div className="flex h-full items-center px-1 text-[13px] font-semibold text-fg">
              Chats
            </div>
          )
        }
        onNewChat={() => createMutation.mutate()}
        onSearch={openSearch}
        showWorkspace={showWorkspace}
        showNewChat={showNewChat}
        showSearch={showSearch}
        newChatDisabled={!activeWorkspaceId || createMutation.isPending}
        newChatPending={createMutation.isPending}
        emptyState={
          isLoading ? (
            <div className="px-3 py-3">
              <div className="flex flex-col gap-2">
                {Array.from({ length: 7 }).map((_, index) => (
                  <div key={index} className="flex items-center gap-2 px-1">
                    <Skeleton className="h-2.5 w-8 rounded-sm" />
                    <Skeleton className="h-3 w-full rounded-sm" />
                  </div>
                ))}
              </div>
            </div>
          ) : filteredSessions.length === 0 ? (
            <Empty className="px-4 py-12">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <MessageSquare />
                </EmptyMedia>
                <EmptyTitle className="text-sm">No chats yet</EmptyTitle>
                <EmptyDescription className="text-xs">
                  Use New chat to start a conversation.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <>
              {pinned.length > 0 && (
                <>
                  <LeftRailSectionHeader icon="pinned" label="Pinned" count={pinned.length} />
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
                      editing={editingId === session.id}
                      onStartEdit={() => setEditingId(session.id)}
                      onCommitEdit={(value) => {
                        setEditingId(null);
                        renameMutation.mutate({ id: session.id, name: value });
                      }}
                      onCancelEdit={() => setEditingId(null)}
                    />
                  ))}
                </>
              )}
              {visibleUnpinned.length > 0 && (
                <>
                  <LeftRailSectionHeader
                    icon="recent"
                    label="Recent"
                    count={filteredSessions.length - pinned.length}
                  />
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
                      editing={editingId === session.id}
                      onStartEdit={() => setEditingId(session.id)}
                      onCommitEdit={(value) => {
                        setEditingId(null);
                        renameMutation.mutate({ id: session.id, name: value });
                      }}
                      onCancelEdit={() => setEditingId(null)}
                    />
                  ))}
                </>
              )}
              {hasMore && (
                <div ref={sentinelRef} className="px-3 pt-3">
                  <div className="font-mono text-[10px] uppercase tracking-[0.14em] text-fg-faint">
                    Loading more...
                  </div>
                </div>
              )}
            </>
          )
        }
        footer={
          <LeftRailFooter
            avatar={
              avatarUrl ? (
                <img src={avatarUrl} alt="" className="size-5 rounded-[4px] object-cover" />
              ) : (
                <span className="flex size-5 items-center justify-center rounded-[4px] bg-surface text-fg-secondary">
                  <User className="size-3" />
                </span>
              )
            }
            name={displayName}
            onProfile={openProfile}
            onSettings={openSettings}
            archiveToggle={
              archivedCount > 0 ? (
                <Tooltip
                  content={showArchived ? "Hide archived" : `Show archived (${archivedCount})`}
                  side="top"
                >
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-6 rounded-[6px] text-fg-muted hover:bg-surface hover:text-fg"
                    onClick={() => setShowArchived((value) => !value)}
                  >
                    {showArchived ? (
                      <EyeOff className="size-3.5" />
                    ) : (
                      <Archive className="size-3.5" />
                    )}
                  </Button>
                </Tooltip>
              ) : undefined
            }
          />
        }
      />

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
    </>
  );
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function PresenceDot({ variant }: { variant: "streaming" | "tool-pending" | "cli-active" }) {
  if (variant === "streaming") {
    return (
      <span className="w-2 h-2 rounded-full bg-success shrink-0 animate-pulse" title="Streaming" />
    );
  }
  if (variant === "cli-active") {
    return (
      <span className="w-2 h-2 rounded-full bg-info shrink-0 animate-pulse" title="CLI active" />
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

function ChatItem({
  session,
  isActive,
  onClick,
  onTogglePin,
  statusIndicator,
  onDelete,
  onArchive,
  editing,
  onStartEdit,
  onCommitEdit,
  onCancelEdit,
}: {
  session: Session;
  isActive: boolean;
  onClick: () => void;
  onTogglePin: () => void;
  statusIndicator?: ReactNode;
  onDelete?: () => void;
  onArchive: () => void;
  editing: boolean;
  onStartEdit: () => void;
  onCommitEdit: (value: string) => void;
  onCancelEdit: () => void;
}) {
  const displayTitle = session.custom_name || session.title || `Chat ${session.short_code}`;
  const isArchived = session.status === "archived";
  const sidebarSlots = usePluginSlots("session-sidebar");
  const sessionContextMenuSlots = usePluginSlots("context-menu:session");
  const handlePluginAction = usePluginAction();

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div>
          <LeftRailSessionRow
            title={displayTitle}
            timeLabel={formatRelativeTime(session.last_activity)}
            active={isActive}
            archived={isArchived}
            statusIndicator={statusIndicator}
            onClick={onClick}
            onTogglePin={onTogglePin}
            onToggleArchive={onArchive}
            pinned={session.is_pinned}
            editing={editing}
            onStartEdit={onStartEdit}
            onCommitEdit={onCommitEdit}
            onCancelEdit={onCancelEdit}
            pluginBadges={
              sidebarSlots.length > 0 ? (
                <>
                  {sidebarSlots.map((entry) => {
                    const PluginIcon = resolveIcon(entry.icon);
                    return (
                      <button
                        type="button"
                        key={entry.id}
                        className="inline-flex size-4 items-center justify-center rounded-[4px] text-fg-faint transition-colors hover:bg-surface hover:text-fg-muted"
                        onClick={(event) => {
                          event.stopPropagation();
                          handlePluginAction(entry);
                        }}
                        title={entry.label}
                      >
                        <PluginIcon className="size-2.5" />
                      </button>
                    );
                  })}
                </>
              ) : undefined
            }
          />
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem onSelect={onStartEdit} className="gap-2 text-xs">
          <Pencil className="size-3.5" />
          Rename chat
        </ContextMenuItem>
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
