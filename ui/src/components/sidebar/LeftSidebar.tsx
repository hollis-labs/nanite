import { useState, useMemo, type ReactNode } from 'react'
import { Plus, Pin, PinOff, Loader2, MessageSquare } from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/Button'
import { ScrollArea } from '@/components/ui/ScrollArea'
import { AdapterBadge } from '@/components/chat/AdapterBadge'
import { ProjectDropdown } from './ProjectDropdown'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
import { useChatStore } from '@/stores/useChatStore'
import { useSettings } from '@/hooks/useSettings'
import { api } from '@/lib/api'
import type { Session } from '@/lib/types'

function formatRelativeTime(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffMins = Math.floor(diffMs / 1000 / 60)
  const diffHours = Math.floor(diffMins / 60)
  const diffDays = Math.floor(diffHours / 24)

  if (diffMins < 1) return 'now'
  if (diffMins < 60) return `${diffMins}m`
  if (diffHours < 24) return `${diffHours}h`
  if (diffDays < 7) return `${diffDays}d`
  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

function sortByActivity(a: Session, b: Session): number {
  return new Date(b.last_activity).getTime() - new Date(a.last_activity).getTime()
}

export function LeftSidebar() {
  const open = useLayoutStore((s) => s.leftSidebarOpen)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)
  const activeProjectId = useAppStore((s) => s.activeProjectId)
  const setActiveSession = useAppStore((s) => s.setActiveSession)
  const queryClient = useQueryClient()
  const activeStreams = useChatStore((s) => s.activeStreams)
  const pendingTools = useChatStore((s) => s.pendingTools)
  const cliActiveSessions = useChatStore((s) => s.cliActiveSessions)

  const { data: userSettings } = useSettings()

  const { data: sessions = [], isLoading } = useQuery({
    queryKey: ['sessions', activeWorkspaceId],
    queryFn: () => api.listSessions(activeWorkspaceId ?? undefined),
    enabled: !!activeWorkspaceId,
  })

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
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
      setActiveSession(newSession.id)
    },
  })

  const pinMutation = useMutation({
    mutationFn: ({ id, pinned }: { id: string; pinned: boolean }) => api.pinSession(id, pinned),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
    },
  })

  // Filter by active project (client-side)
  const filteredSessions = useMemo(() => {
    if (!activeProjectId) return sessions
    return sessions.filter((s: Session) => s.project_id === activeProjectId)
  }, [sessions, activeProjectId])

  const pinned = useMemo(
    () => filteredSessions.filter((s: Session) => s.is_pinned).sort(sortByActivity),
    [filteredSessions],
  )

  const unpinned = useMemo(
    () => filteredSessions.filter((s: Session) => !s.is_pinned).sort(sortByActivity),
    [filteredSessions],
  )

  return (
    <aside
      className={`h-full bg-bg border-r border-border flex flex-col transition-all duration-200 ease-in-out overflow-hidden ${
        open ? 'w-68' : 'w-0'
      }`}
    >
      <div className="min-w-68 flex flex-col h-full">
        {/* Header */}
        <div className="flex items-center justify-between px-4 h-12 border-b border-border shrink-0">
          <h2 className="text-sm font-semibold text-fg">Chats</h2>
          <Button
            variant="ghost"
            size="icon"
            className="w-7 h-7 text-fg-secondary hover:text-fg"
            onClick={() => createMutation.mutate()}
            disabled={!activeWorkspaceId || createMutation.isPending}
            title="New chat"
          >
            {createMutation.isPending ? (
              <Loader2 className="w-4 h-4 animate-spin" />
            ) : (
              <Plus className="w-4 h-4" />
            )}
          </Button>
        </div>

        {/* Project selector */}
        {activeWorkspaceId && <ProjectDropdown workspaceId={activeWorkspaceId} />}

        {/* Chat list */}
        <ScrollArea className="flex-1 min-h-0">
          <div className="px-2 py-2">
            {isLoading && (
              <div className="flex items-center justify-center py-8">
                <Loader2 className="w-5 h-5 text-fg-muted animate-spin" />
              </div>
            )}

            {!isLoading && filteredSessions.length === 0 && (
              <div className="px-2 py-8 text-center">
                <p className="text-xs text-fg-muted">No chats yet</p>
                <p className="text-xs text-fg-faint mt-1">Click + to start a chat</p>
              </div>
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
                    onTogglePin={() => pinMutation.mutate({ id: session.id, pinned: !session.is_pinned })}
                    statusIndicator={getPresenceIndicator(session.id, activeStreams, pendingTools, cliActiveSessions)}
                  />
                ))}
              </>
            )}

            {/* Recent */}
            {unpinned.length > 0 && (
              <>
                {pinned.length > 0 && <ZoneHeader label="Recent" />}
                {unpinned.map((session: Session) => (
                  <ChatItem
                    key={session.id}
                    session={session}
                    isActive={session.id === activeSessionId}
                    onClick={() => setActiveSession(session.id)}
                    onTogglePin={() => pinMutation.mutate({ id: session.id, pinned: !session.is_pinned })}
                    statusIndicator={getPresenceIndicator(session.id, activeStreams, pendingTools, cliActiveSessions)}
                  />
                ))}
              </>
            )}
          </div>
        </ScrollArea>
      </div>
    </aside>
  )
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function PresenceDot({ variant }: { variant: 'streaming' | 'tool-pending' | 'cli-active' }) {
  if (variant === 'streaming') {
    return <span className="w-2 h-2 rounded-full bg-emerald-500 shrink-0 animate-pulse" title="Streaming" />
  }
  if (variant === 'cli-active') {
    return <span className="w-2 h-2 rounded-full bg-cyan-500 shrink-0 animate-pulse" title="CLI active" />
  }
  return <span className="w-2 h-2 rounded-full bg-amber-500 shrink-0" title="Tool approval pending" />
}

function getPresenceIndicator(
  sessionId: string,
  activeStreams: Map<string, unknown>,
  pendingTools: Map<string, unknown>,
  cliActiveSessions: Map<string, unknown>,
): ReactNode | undefined {
  if (pendingTools.has(sessionId)) return <PresenceDot variant="tool-pending" />
  if (activeStreams.has(sessionId)) return <PresenceDot variant="streaming" />
  if (cliActiveSessions.has(sessionId)) return <PresenceDot variant="cli-active" />
  return undefined
}

function ZoneHeader({ label, icon }: { label: string; icon?: ReactNode }) {
  return (
    <div className="px-2 pt-3 pb-1 text-xs font-medium text-fg-muted uppercase tracking-wider flex items-center gap-1">
      {icon}
      {label}
    </div>
  )
}

function ChatItem({
  session,
  isActive,
  onClick,
  onTogglePin,
  statusIndicator,
}: {
  session: Session
  isActive: boolean
  onClick: () => void
  onTogglePin: () => void
  statusIndicator?: ReactNode
}) {
  const [hovered, setHovered] = useState(false)
  const displayTitle = session.custom_name || session.title || `Chat ${session.short_code}`

  return (
    <button
      onClick={onClick}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      className={`w-full flex items-start gap-2 px-2 py-0.5 rounded-md text-left transition-colors group ${
        isActive
          ? 'bg-surface/60 text-fg'
          : 'text-fg-secondary hover:bg-surface/40 hover:text-fg'
      }`}
    >
      {/* Icon column — message count centered in icon */}
      <div className="relative shrink-0">
        {statusIndicator && <div className="absolute -top-1 -left-1 z-10">{statusIndicator}</div>}
        <div className="relative w-7 h-7 flex items-center justify-center">
          <MessageSquare className="w-5 h-5 opacity-25" />
          {session.message_count > 0 && (
            <span className="absolute inset-x-0 top-[5px] bottom-1.5 flex items-center justify-center text-[9px] font-bold text-fg-muted tabular-nums leading-none">
              {session.message_count > 99 ? '99' : session.message_count}
            </span>
          )}
        </div>
      </div>

      {/* Content — mt aligns title baseline with icon center */}
      <div className="flex-1 min-w-0 mt-[3px]">
        {/* Line 1: title + time/pin */}
        <div className="flex items-center gap-1">
          <span className="text-sm truncate flex-1 leading-snug">{displayTitle}</span>
          {hovered ? (
            <span
              role="button"
              onClick={(e) => { e.stopPropagation(); onTogglePin() }}
              className="p-0.5 rounded text-fg-muted hover:text-fg hover:bg-surface-hover transition-colors shrink-0"
              aria-label={session.is_pinned ? 'Unpin' : 'Pin'}
            >
              {session.is_pinned ? <PinOff className="w-3 h-3" /> : <Pin className="w-3 h-3" />}
            </span>
          ) : (
            <>
              {session.is_pinned && <Pin className="w-3 h-3 text-fg-faint shrink-0" />}
              <span className="text-[11px] text-fg-faint shrink-0">{formatRelativeTime(session.last_activity)}</span>
            </>
          )}
        </div>
        {/* Line 2: #short_code · adapter badge */}
        <div className="flex items-center gap-1.5 mt-0.5">
          <span className="text-[11px] text-fg-faint font-mono">#{session.short_code}</span>
          <AdapterBadge provider={session.provider || 'api'} size="sm" />
        </div>
      </div>
    </button>
  )
}
