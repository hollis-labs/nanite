import { Plus, Hash, Pin, Loader2 } from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/Button'
import { ScrollArea } from '@/components/ui/ScrollArea'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { useAppStore } from '@/stores/useAppStore'
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

export function LeftSidebar() {
  const open = useLayoutStore((s) => s.leftSidebarOpen)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)
  const setActiveSession = useAppStore((s) => s.setActiveSession)
  const queryClient = useQueryClient()

  const { data: sessions = [], isLoading } = useQuery({
    queryKey: ['sessions', activeWorkspaceId],
    queryFn: () => api.listSessions(activeWorkspaceId ?? undefined),
    enabled: !!activeWorkspaceId,
  })

  const createMutation = useMutation({
    mutationFn: () => api.createSession({ workspace_id: activeWorkspaceId! }),
    onSuccess: (newSession) => {
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
      setActiveSession(newSession.id)
    },
    onError: (err) => {
      console.error('Failed to create session:', err)
    },
  })

  // Group sessions: pinned first, then by last_activity desc
  const pinned = sessions
    .filter((s: Session) => s.is_pinned)
    .sort((a: Session, b: Session) => new Date(b.last_activity).getTime() - new Date(a.last_activity).getTime())

  const recent = sessions
    .filter((s: Session) => !s.is_pinned)
    .sort((a: Session, b: Session) => new Date(b.last_activity).getTime() - new Date(a.last_activity).getTime())

  return (
    <aside
      className={`h-full bg-zinc-950 border-r border-zinc-800 flex flex-col transition-all duration-200 ease-in-out overflow-hidden ${
        open ? 'w-68' : 'w-0'
      }`}
    >
      <div className="min-w-68 flex flex-col h-full">
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-zinc-800 shrink-0">
          <h2 className="text-sm font-semibold text-zinc-100">Sessions</h2>
          <Button
            variant="ghost"
            size="icon"
            className="w-7 h-7 text-zinc-400 hover:text-zinc-100"
            onClick={() => createMutation.mutate()}
            disabled={!activeWorkspaceId || createMutation.isPending}
          >
            {createMutation.isPending ? (
              <Loader2 className="w-4 h-4 animate-spin" />
            ) : (
              <Plus className="w-4 h-4" />
            )}
          </Button>
        </div>

        {/* Session list */}
        <ScrollArea className="flex-1 min-h-0">
          <div className="px-2 py-2">
            {isLoading && (
              <div className="flex items-center justify-center py-8">
                <Loader2 className="w-5 h-5 text-zinc-500 animate-spin" />
              </div>
            )}

            {!isLoading && sessions.length === 0 && (
              <div className="px-2 py-8 text-center">
                <p className="text-xs text-zinc-500">No sessions yet</p>
                <p className="text-xs text-zinc-600 mt-1">Click + to start a chat</p>
              </div>
            )}

            {/* Pinned */}
            {pinned.length > 0 && (
              <>
                <div className="px-2 pt-1 pb-1 text-xs font-medium text-zinc-500 uppercase tracking-wider flex items-center gap-1">
                  <Pin className="w-3 h-3" />
                  Pinned
                </div>
                {pinned.map((session: Session) => (
                  <SessionItem
                    key={session.id}
                    session={session}
                    isActive={session.id === activeSessionId}
                    onClick={() => setActiveSession(session.id)}
                  />
                ))}
              </>
            )}

            {/* Recent */}
            {recent.length > 0 && (
              <>
                {pinned.length > 0 && (
                  <div className="px-2 pt-3 pb-1 text-xs font-medium text-zinc-500 uppercase tracking-wider">
                    Recent
                  </div>
                )}
                {recent.map((session: Session) => (
                  <SessionItem
                    key={session.id}
                    session={session}
                    isActive={session.id === activeSessionId}
                    onClick={() => setActiveSession(session.id)}
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

function SessionItem({
  session,
  isActive,
  onClick,
}: {
  session: Session
  isActive: boolean
  onClick: () => void
}) {
  const displayTitle = session.custom_name || session.title || `#${session.short_code}`

  return (
    <button
      onClick={onClick}
      className={`w-full flex items-center gap-2 px-2 py-2 rounded-md text-sm text-left transition-colors ${
        isActive
          ? 'bg-zinc-800/60 text-zinc-100'
          : 'text-zinc-400 hover:bg-zinc-800/40 hover:text-zinc-200'
      }`}
    >
      <Hash className="w-3.5 h-3.5 shrink-0 opacity-50" />
      <span className="truncate flex-1">{displayTitle}</span>
      <div className="flex items-center gap-2 shrink-0">
        {session.message_count > 0 && (
          <span className="text-xs text-zinc-600 tabular-nums">{session.message_count}</span>
        )}
        <span className="text-xs text-zinc-600">
          {formatRelativeTime(session.last_activity)}
        </span>
      </div>
    </button>
  )
}
