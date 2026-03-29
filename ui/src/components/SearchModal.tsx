import { useState, useCallback, useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { MessageSquare, Hash } from 'lucide-react'
import {
  CommandDialog,
  CommandInput,
  CommandList,
  CommandEmpty,
  CommandGroup,
  CommandItem,
} from '@/components/ui/command'
import { useAppStore } from '@/stores/useAppStore'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { api } from '@/lib/api'

interface SearchModalProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

function formatDate(dateStr: string): string {
  const d = new Date(dateStr)
  const now = new Date()
  const diff = now.getTime() - d.getTime()
  if (diff < 60_000) return 'now'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`
  if (diff < 604_800_000) return `${Math.floor(diff / 86_400_000)}d ago`
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

export function SearchModal({ open, onOpenChange }: SearchModalProps) {
  const [scope, setScope] = useState<'all' | 'workspace' | 'project'>('workspace')
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)
  const activeProjectId = useAppStore((s) => s.activeProjectId)
  const setActiveSession = useAppStore((s) => s.setActiveSession)
  const setCurrentPage = useLayoutStore((s) => s.setCurrentPage)

  const { data: sessions = [] } = useQuery({
    queryKey: ['sessions', activeWorkspaceId],
    queryFn: () => api.listSessions(activeWorkspaceId ?? undefined),
    enabled: open,
  })

  const { data: projects = [] } = useQuery({
    queryKey: ['projects', activeWorkspaceId],
    queryFn: () => api.listProjects(activeWorkspaceId!),
    enabled: open && !!activeWorkspaceId,
  })

  const filteredSessions = useMemo(() => {
    let list = sessions
    if (scope === 'project' && activeProjectId) {
      list = list.filter((s) => s.project_id === activeProjectId)
    }
    return [...list].sort(
      (a, b) => new Date(b.last_activity).getTime() - new Date(a.last_activity).getTime()
    )
  }, [sessions, scope, activeProjectId])

  const activeProjectName = useMemo(() => {
    if (!activeProjectId) return null
    return projects.find((p) => p.id === activeProjectId)?.name ?? null
  }, [activeProjectId, projects])

  const selectSession = useCallback((sessionId: string) => {
    setActiveSession(sessionId)
    setCurrentPage('chat')
    window.location.hash = '#chat'
    onOpenChange(false)
  }, [setActiveSession, setCurrentPage, onOpenChange])

  return (
    <CommandDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Search Chats"
      description="Find and navigate to chat sessions"
      showCloseButton={false}
    >
      <CommandInput placeholder="Search chats by title or short code..." />

      {/* Scope filters */}
      <div className="flex items-center gap-1 px-3 py-1.5 border-b border-border">
        <ScopeButton active={scope === 'all'} onClick={() => setScope('all')}>
          All
        </ScopeButton>
        <ScopeButton active={scope === 'workspace'} onClick={() => setScope('workspace')}>
          Workspace
        </ScopeButton>
        {activeProjectId && (
          <ScopeButton active={scope === 'project'} onClick={() => setScope('project')}>
            {activeProjectName || 'Project'}
          </ScopeButton>
        )}
      </div>

      <CommandList className="max-h-[400px]">
        <CommandEmpty className="text-fg-muted">No chats found.</CommandEmpty>

        <CommandGroup heading={`${filteredSessions.length} chat${filteredSessions.length === 1 ? '' : 's'}`}>
          {filteredSessions.map((session) => (
            <CommandItem key={session.id} onSelect={() => selectSession(session.id)}>
              <MessageSquare className="text-fg-faint" />
              <div className="flex flex-col min-w-0 flex-1">
                <span className="truncate text-sm">
                  {session.title || 'Untitled'}
                </span>
                <span className="text-xs text-fg-faint flex items-center gap-1.5">
                  <Hash className="size-2.5" />
                  {session.short_code}
                  <span className="mx-0.5">·</span>
                  {session.message_count} messages
                </span>
              </div>
              <span className="text-xs text-fg-faint shrink-0">{formatDate(session.last_activity)}</span>
            </CommandItem>
          ))}
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  )
}

function ScopeButton({ active, onClick, children }: {
  active: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`px-2 py-0.5 rounded text-xs font-medium transition-colors ${
        active
          ? 'bg-surface text-fg'
          : 'text-fg-muted hover:text-fg-secondary'
      }`}
    >
      {children}
    </button>
  )
}
