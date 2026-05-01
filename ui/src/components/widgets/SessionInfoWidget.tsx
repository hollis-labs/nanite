import { Check, Copy, Info } from 'lucide-react'
import { type ReactNode, useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Widget, WidgetRow } from './Widget'
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

  const { data: projects = [] } = useQuery({
    queryKey: ['projects', activeWorkspaceId],
    queryFn: () => api.listProjects(activeWorkspaceId!),
    enabled: !!activeWorkspaceId,
  })

  const { data: sessionAgents = [] } = useQuery({
    queryKey: ['session-agents', activeSessionId],
    queryFn: () => api.listSessionAgents(activeSessionId!),
    enabled: !!activeSessionId,
  })

  if (!activeSessionId) {
    return (
      <Widget id="session-info" title="Session" icon={Info} defaultOpen={false}>
        <p className="text-[12px] text-fg-faint italic">No session selected</p>
      </Widget>
    )
  }

  if (!session) {
    return (
      <Widget id="session-info" title="Session" icon={Info} defaultOpen={false}>
        <div className="flex flex-col gap-1.5">
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
  const primaryAgent = sessionAgents.find((a) => a.role === 'primary')

  const shortCode = `#${session.short_code}`

  const rows: { label: string; value: ReactNode }[] = [
    { label: 'Title',         value: session.custom_name || session.title || 'Untitled' },
    { label: 'Short Code',    value: <ShortCodeValue code={shortCode} /> },
    ...(projectName ? [{ label: 'Project', value: projectName }] : []),
    ...(primaryAgent ? [{ label: 'Agent', value: primaryAgent.name }] : []),
    { label: 'Provider',      value: session.provider || '—' },
    { label: 'Model',         value: session.model || '—' },
    { label: 'Messages',      value: String(session.message_count) },
    { label: 'Created',       value: formatDate(session.created_at) },
    { label: 'Last Activity', value: formatRelativeTime(session.last_activity) },
  ]

  return (
    <Widget id="session-info" title="Session" icon={Info} defaultOpen={false}>
      <div className="flex flex-col gap-1.5">
        {rows.map(({ label, value }) => (
          <WidgetRow key={label} label={label} mono>{value}</WidgetRow>
        ))}
      </div>
    </Widget>
  )
}

function ShortCodeValue({ code }: { code: string }) {
  const [copied, setCopied] = useState(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Clear any pending reset-timer if the widget unmounts mid-feedback so we
  // don't setCopied on an unmounted component (session switch, drawer close,
  // etc. can all unmount within the 1.5s window).
  useEffect(() => {
    return () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
      }
    }
  }, [])

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(code)
      setCopied(true)
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
      }
      timerRef.current = setTimeout(() => {
        setCopied(false)
        timerRef.current = null
      }, 1500)
    } catch {
      // ignore — clipboard may be unavailable
    }
  }

  return (
    <span className="inline-flex items-center gap-1.5">
      <span>{code}</span>
      <button
        type="button"
        onClick={handleCopy}
        title={copied ? 'Copied' : 'Copy short code'}
        aria-label={copied ? 'Copied' : 'Copy short code'}
        className="flex size-4 items-center justify-center rounded-[3px] text-fg-muted transition-colors hover:bg-surface hover:text-fg"
      >
        {copied ? (
          <Check className="size-3 text-success" strokeWidth={2.2} />
        ) : (
          <Copy className="size-3" strokeWidth={2} />
        )}
      </button>
    </span>
  )
}
