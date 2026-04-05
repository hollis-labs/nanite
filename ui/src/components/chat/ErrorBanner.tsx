import { useState } from 'react'
import { AlertTriangle, AlertCircle, XCircle, X, ChevronDown } from 'lucide-react'
import type { ChatError, ChatErrorCode } from '@/lib/types'
import { ErrorDetailModal } from './ErrorDetailModal'

const ERROR_STYLES: Record<ChatErrorCode, { bg: string; border: string; icon: string; IconComponent: typeof AlertTriangle }> = {
  rate_limit: {
    bg: 'bg-warning/10',
    border: 'border-warning/30',
    icon: 'text-warning',
    IconComponent: AlertTriangle,
  },
  tool_error: {
    bg: 'bg-danger/10',
    border: 'border-danger/30',
    icon: 'text-danger',
    IconComponent: XCircle,
  },
  provider_error: {
    bg: 'bg-danger/10',
    border: 'border-danger/30',
    icon: 'text-danger',
    IconComponent: AlertCircle,
  },
  internal_error: {
    bg: 'bg-warning/10',
    border: 'border-warning/30',
    icon: 'text-warning',
    IconComponent: AlertCircle,
  },
}

interface ErrorBannerProps {
  error: ChatError
  onDismiss: (id: string) => void
}

export function ErrorBanner({ error, onDismiss }: ErrorBannerProps) {
  const [showModal, setShowModal] = useState(false)

  if (error.dismissed) return null

  const style = ERROR_STYLES[error.code] || ERROR_STYLES.internal_error
  const { IconComponent } = style

  return (
    <>
      <div
        className={`flex items-start gap-2 px-3 py-2 rounded-md border text-sm ${style.bg} ${style.border}`}
      >
        <IconComponent className={`w-4 h-4 shrink-0 mt-0.5 ${style.icon}`} />
        <div className="flex-1 min-w-0">
          <p className="text-fg text-xs leading-relaxed">{error.message}</p>
          <button
            onClick={() => setShowModal(true)}
            className="inline-flex items-center gap-1 text-xs text-fg-secondary hover:text-fg mt-1 transition-colors"
          >
            <ChevronDown className="w-3 h-3" />
            View Details
          </button>
        </div>
        <button
          onClick={() => onDismiss(error.id)}
          className="text-fg-muted hover:text-fg-secondary shrink-0 transition-colors"
          aria-label="Dismiss error"
        >
          <X className="w-3.5 h-3.5" />
        </button>
      </div>

      {showModal && (
        <ErrorDetailModal error={error} onClose={() => setShowModal(false)} />
      )}
    </>
  )
}
