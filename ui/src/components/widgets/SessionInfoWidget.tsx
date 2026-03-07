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

  const { data: session } = useQuery({
    queryKey: ['session', activeSessionId],
    queryFn: () => api.getSession(activeSessionId!),
    enabled: !!activeSessionId,
  })

  if (!session) {
    return (
      <Widget id="session-info" title="Session Info" icon={Info}>
        <p className="text-xs text-zinc-500 italic">No session selected</p>
      </Widget>
    )
  }

  const rows = [
    { label: 'Title', value: session.custom_name || session.title || 'Untitled' },
    { label: 'Short Code', value: `#${session.short_code}` },
    { label: 'Workspace', value: session.workspace_id?.slice(0, 8) || '-' },
    { label: 'Messages', value: String(session.message_count) },
    { label: 'Created', value: formatDate(session.created_at) },
    { label: 'Last Activity', value: formatRelativeTime(session.last_activity) },
  ]

  return (
    <Widget id="session-info" title="Session Info" icon={Info}>
      <div className="space-y-1.5">
        {rows.map(({ label, value }) => (
          <div key={label} className="flex justify-between text-xs">
            <span className="text-zinc-500">{label}</span>
            <span className="text-zinc-300 truncate max-w-[60%] text-right">{value}</span>
          </div>
        ))}
      </div>
    </Widget>
  )
}
