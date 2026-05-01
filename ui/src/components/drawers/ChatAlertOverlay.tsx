import { AlertTriangle, RefreshCw, X } from 'lucide-react'

type Severity = 'info' | 'warning' | 'danger'

interface AlertSpec {
  severity: Severity
  title: string
  body: string
  actions: { label: string; onClick: () => void; primary?: boolean }[]
}

interface ChatAlertOverlayProps {
  sessionTakeover: boolean
  streamStalled: boolean
  circuitOpen: boolean
  statusMessage: string | null
  onReconnect: () => void
  onRetry: () => void
  onDismissCircuit: () => void
}

export function ChatAlertOverlay({
  sessionTakeover,
  streamStalled,
  circuitOpen,
  statusMessage,
  onReconnect,
  onRetry,
  onDismissCircuit,
}: ChatAlertOverlayProps) {
  // Resolve the topmost alert (priority: takeover > circuit > stalled > status).
  let alert: AlertSpec | null = null
  if (sessionTakeover) {
    alert = {
      severity: 'info',
      title: 'This session is now active in another tab',
      body: 'The streaming connection moved to a newer tab. Reload to reconnect here.',
      actions: [{ label: 'Reload', onClick: () => window.location.reload(), primary: true }],
    }
  } else if (circuitOpen) {
    alert = {
      severity: 'warning',
      title: 'Provider rate limited after multiple retries',
      body: 'The API provider returned rate limit errors. Retry, or dismiss to keep the partial response.',
      actions: [
        { label: 'Retry', onClick: onRetry, primary: true },
        { label: 'Dismiss', onClick: onDismissCircuit },
      ],
    }
  } else if (streamStalled) {
    alert = {
      severity: 'warning',
      title: 'Connection appears stalled',
      body: 'No activity from the server in the last minute. Reconnect to retry this turn.',
      actions: [{ label: 'Reconnect', onClick: onReconnect, primary: true }],
    }
  } else if (statusMessage) {
    alert = { severity: 'info', title: statusMessage, body: '', actions: [] }
  }

  if (!alert) return null

  const colorBySeverity: Record<Severity, { ring: string; ribbon: string; icon: string }> = {
    info: { ring: 'border-info-muted bg-info-muted', ribbon: 'bg-info', icon: 'text-info' },
    warning: { ring: 'border-warning-muted bg-warning-muted', ribbon: 'bg-warning', icon: 'text-warning' },
    danger: { ring: 'border-danger-muted bg-danger-muted', ribbon: 'bg-danger', icon: 'text-danger' },
  }
  const c = colorBySeverity[alert.severity]

  return (
    <div
      className={`pointer-events-auto absolute left-[7.5%] right-[7.5%] bottom-0 z-30 border ${c.ring} shadow-lg`}
      style={{ borderRadius: '10px 10px 0 0' }}
    >
      <div className={`pointer-events-none absolute inset-x-0 top-0 h-[2px] ${c.ribbon} opacity-75`} />
      <div className="flex items-start gap-3 p-4">
        <AlertTriangle className={`h-5 w-5 shrink-0 mt-0.5 ${c.icon}`} />
        <div className="flex-1 min-w-0">
          <p className={`text-sm font-medium ${c.icon}`}>{alert.title}</p>
          {alert.body && <p className="text-xs text-fg-muted mt-1">{alert.body}</p>}
          {alert.actions.length > 0 && (
            <div className="flex gap-2 mt-3">
              {alert.actions.map((a) => (
                <button
                  key={a.label}
                  type="button"
                  onClick={a.onClick}
                  className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-[6px] text-xs font-medium transition-colors ${
                    a.primary
                      ? 'bg-surface text-fg hover:bg-surface-hover'
                      : 'text-fg-secondary hover:bg-surface-hover'
                  }`}
                >
                  {a.label === 'Reconnect' && <RefreshCw className="w-3.5 h-3.5" />}
                  {a.label === 'Retry' && <RefreshCw className="w-3.5 h-3.5" />}
                  {a.label === 'Dismiss' && <X className="w-3.5 h-3.5" />}
                  {a.label}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
