import { Info } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Widget } from './Widget'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'

function formatDate(dateStr: string): string {
  return new Date(dateStr).toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function formatRelativeTime(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffMins = Math.floor(diffMs / 1000 / 60)
  const diffHours = Math.floor(diffMins / 60)
  const diffDays = Math.floor(diffHours / 24)

  if (diffMins < 1) return 'just now'
  if (diffMins < 60) return `${diffMins}m ago`
  if (diffHours < 24) return `${diffHours}h ago`
  if (diffDays < 7) return `${diffDays}d ago`
  return formatDate(dateStr)
}

export function SessionInfoWidget() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const activeWorkspaceId = useAppStore((s) => s.activeWorkspaceId)

  const { data: session } = useQuery({
    queryKey: ['session', activeSessionId],
    queryFn: () => api.getSession(activeSessionId!),
    enabled: !!activeSessionId,
  })

  // Fetch projects to resolve project name
  const { data: projects = [] } = useQuery({
    queryKey: ['projects', activeWorkspaceId],
    queryFn: () => api.listProjects(activeWorkspaceId!),
    enabled: !!activeWorkspaceId,
  })

  // Fetch session agents for primary agent name
  const { data: sessionAgents = [] } = useQuery({
    queryKey: ['session-agents', activeSessionId],
    queryFn: () => api.listSessionAgents(activeSessionId!),
    enabled: !!activeSessionId,
  })

  if (!activeSessionId) {
    return (
      <Widget id="session-info" title="Session Info" icon={Info}>
        <p className="text-xs text-fg-muted italic">No session selected</p>
      </Widget>
    )
  }

  if (!session) {
    return (
      <Widget id="session-info" title="Session Info" icon={Info}>
        <div className="space-y-1.5">
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i} className="flex justify-between">
              <div className="h-3 w-16 rounded bg-surface animate-pulse" />
              <div className="h-3 w-24 rounded bg-surface animate-pulse" />
            </div>
          ))}
        </div>
      </Widget>
    )
  }

  const projectName = session.project_id
    ? projects.find((p) => p.id === session.project_id)?.name
    : null

  const primaryAgent = sessionAgents.find(
    (a) => a.role === 'primary'
  )

  const rows: { label: string; value: string }[] = [
    { label: 'Title', value: session.custom_name || session.title || 'Untitled' },
    { label: 'Short Code', value: `#${session.short_code}` },
    ...(projectName ? [{ label: 'Project', value: projectName }] : []),
    ...(primaryAgent ? [{ label: 'Agent', value: primaryAgent.name }] : []),
    { label: 'Provider', value: session.provider || '-' },
    { label: 'Model', value: session.model || '-' },
    { label: 'Messages', value: String(session.message_count) },
    { label: 'Created', value: formatDate(session.created_at) },
    { label: 'Last Activity', value: formatRelativeTime(session.last_activity) },
  ]

  return (
    <Widget id="session-info" title="Session Info" icon={Info}>
      <div className="space-y-1.5">
        {rows.map(({ label, value }) => (
          <div key={label} className="flex justify-between text-xs gap-2">
            <span className="text-fg-muted shrink-0">{label}</span>
            <span className="text-fg-secondary truncate text-right font-mono text-[11px]">{value}</span>
          </div>
        ))}
      </div>
    </Widget>
  )
}
