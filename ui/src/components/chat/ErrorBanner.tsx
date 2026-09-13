import type { ChatError, ChatErrorCode } from '@/lib/types'
import type { LucideIcon } from 'lucide-react'
import { AlertCircle, AlertTriangle, ChevronDown, X, XCircle } from 'lucide-react'
import { useState } from 'react'
import { ErrorDetailModal } from './ErrorDetailModal'
import { Envelope, EnvelopeHeader } from './envelopes/primitives'

const ERROR_CONFIG: Record<
  ChatErrorCode,
  { tone: 'danger' | 'warning'; icon: LucideIcon; label: string }
> = {
  rate_limit:     { tone: 'warning', icon: AlertTriangle, label: 'Rate limit' },
  tool_error:     { tone: 'danger',  icon: XCircle,       label: 'Tool error' },
  provider_error: { tone: 'danger',  icon: AlertCircle,   label: 'Provider error' },
  internal_error: { tone: 'warning', icon: AlertCircle,   label: 'Internal error' },
}

interface ErrorBannerProps {
  error: ChatError
  onDismiss: (id: string) => void
  onRetry?: () => void
  onChooseModel?: () => void
  busy?: boolean
}

export function ErrorBanner({ error, onDismiss, onRetry, onChooseModel, busy = false }: ErrorBannerProps) {
  const [showModal, setShowModal] = useState(false)

  if (error.dismissed) return null

  const config = ERROR_CONFIG[error.code] || ERROR_CONFIG.internal_error

  return (
    <>
      <Envelope accent={config.tone}>
        <EnvelopeHeader
          icon={config.icon}
          label={error.details?.source === 'nanite' ? 'Nanite · Request paused' : config.label}
          tone={config.tone}
          action={
            <div className="flex items-center gap-1">
              <button
                type="button"
                onClick={() => setShowModal(true)}
                className="inline-flex items-center gap-1 rounded-[4px] px-1.5 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted transition-colors hover:bg-surface hover:text-fg-secondary"
              >
                <ChevronDown className="h-3 w-3" />
                Details
              </button>
              <button
                type="button"
                onClick={() => onDismiss(error.id)}
                className="rounded-[4px] p-1 text-fg-faint transition-colors hover:bg-surface hover:text-fg-secondary"
                aria-label="Dismiss error"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            </div>
          }
        />
        <div className="px-4 py-3">
          <p className="text-[13px] leading-relaxed text-fg">{error.message}</p>
          {(onRetry || onChooseModel) && (
            <div className="mt-3 space-y-2">
              <p className="text-xs text-fg-muted">How would you like to continue?</p>
              <div className="flex flex-wrap gap-2">
                {onChooseModel && <button type="button" disabled={busy} onClick={onChooseModel}
                  className="rounded-md border border-border-subtle px-3 py-1.5 text-xs disabled:opacity-50">Choose another model</button>}
                {onRetry && <button type="button" disabled={busy} onClick={onRetry}
                  className="rounded-md border border-border-subtle px-3 py-1.5 text-xs disabled:opacity-50">Retry request</button>}
              </div>
              <p className="text-xs text-fg-muted">Choosing a model does not resend your request. Use Retry when you’re ready.</p>
            </div>
          )}
        </div>
      </Envelope>

      {showModal && (
        <ErrorDetailModal error={error} onClose={() => setShowModal(false)} />
      )}
    </>
  )
}
