import { useCallback } from 'react'
import { X, Copy, Check } from 'lucide-react'
import { useState } from 'react'
import type { ChatError } from '@/lib/types'

interface ErrorDetailModalProps {
  error: ChatError
  onClose: () => void
}

const CODE_LABELS: Record<string, string> = {
  rate_limit: 'Rate Limit',
  tool_error: 'Tool Error',
  provider_error: 'Provider Error',
  internal_error: 'Internal Error',
}

export function ErrorDetailModal({ error, onClose }: ErrorDetailModalProps) {
  const [copied, setCopied] = useState(false)

  const fullPayload = JSON.stringify(error, null, 2)

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(fullPayload)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // Clipboard API not available
    }
  }, [fullPayload])

  const handleBackdropClick = useCallback(
    (e: React.MouseEvent<HTMLDivElement>) => {
      if (e.target === e.currentTarget) {
        onClose()
      }
    },
    [onClose]
  )

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm"
      onClick={handleBackdropClick}
    >
      <div className="bg-bg-elevated border border-border-subtle rounded-lg shadow-2xl w-full max-w-lg mx-4 max-h-[80vh] flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-border">
          <h3 className="text-sm font-medium text-fg">Error Details</h3>
          <button
            onClick={onClose}
            className="text-fg-muted hover:text-fg-secondary transition-colors"
            aria-label="Close"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto px-4 py-3 space-y-4">
          {/* Error Code */}
          <div>
            <dt className="text-xs font-medium text-fg-muted uppercase tracking-wider mb-1">
              Error Code
            </dt>
            <dd className="text-sm text-fg">
              <span className="inline-block px-2 py-0.5 rounded bg-surface text-fg-secondary font-mono text-xs">
                {error.code}
              </span>
              <span className="ml-2 text-fg-secondary text-xs">
                {CODE_LABELS[error.code] || error.code}
              </span>
            </dd>
          </div>

          {/* Message */}
          <div>
            <dt className="text-xs font-medium text-fg-muted uppercase tracking-wider mb-1">
              Message
            </dt>
            <dd className="text-sm text-fg">{error.message}</dd>
          </div>

          {/* Timestamp */}
          <div>
            <dt className="text-xs font-medium text-fg-muted uppercase tracking-wider mb-1">
              Timestamp
            </dt>
            <dd className="text-sm text-fg-secondary font-mono text-xs">
              {error.timestamp}
            </dd>
          </div>

          {/* Details */}
          {error.details && Object.keys(error.details).length > 0 && (
            <div>
              <dt className="text-xs font-medium text-fg-muted uppercase tracking-wider mb-1">
                Raw Details
              </dt>
              <dd>
                <pre className="text-xs text-fg-secondary bg-surface/80 rounded-md p-3 overflow-x-auto font-mono whitespace-pre-wrap break-all">
                  {JSON.stringify(error.details, null, 2)}
                </pre>
              </dd>
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex justify-end gap-2 px-4 py-3 border-t border-border">
          <button
            onClick={handleCopy}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-fg-secondary bg-surface hover:bg-surface-hover rounded-md border border-border-subtle transition-colors"
          >
            {copied ? (
              <>
                <Check className="w-3.5 h-3.5 text-success" />
                Copied
              </>
            ) : (
              <>
                <Copy className="w-3.5 h-3.5" />
                Copy Payload
              </>
            )}
          </button>
          <button
            onClick={onClose}
            className="px-3 py-1.5 text-xs font-medium text-fg-secondary bg-surface hover:bg-surface-hover rounded-md border border-border-subtle transition-colors"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  )
}
