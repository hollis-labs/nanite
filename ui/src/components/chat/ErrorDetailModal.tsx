import { useCallback, useState } from 'react'
import { Copy, Check } from 'lucide-react'
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
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

  return (
    <Dialog open={true} onOpenChange={() => onClose()}>
      <DialogContent className="sm:max-w-lg max-h-[80vh] flex flex-col">
        <DialogHeader className="px-4 pt-4 pb-3">
          <DialogTitle>Error Details</DialogTitle>
          <DialogDescription className="sr-only">Detailed error information</DialogDescription>
        </DialogHeader>

        {/* Body */}
        <div className="flex-1 overflow-y-auto px-4 py-3 space-y-4">
          {/* Error Code */}
          <div>
            <dt className="font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted mb-1">
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
            <dt className="font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted mb-1">
              Message
            </dt>
            <dd className="text-sm text-fg">{error.message}</dd>
          </div>

          {/* Timestamp */}
          <div>
            <dt className="font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted mb-1">
              Timestamp
            </dt>
            <dd className="text-sm text-fg-secondary font-mono text-xs">
              {error.timestamp}
            </dd>
          </div>

          {/* Details */}
          {error.details && Object.keys(error.details).length > 0 && (
            <div>
              <dt className="font-mono text-[11px] font-semibold uppercase tracking-[0.04em] text-fg-muted mb-1">
                Raw Details
              </dt>
              <dd>
                <pre className="text-xs text-fg-secondary bg-surface rounded-[6px] p-3 overflow-x-auto font-mono whitespace-pre-wrap break-all">
                  {JSON.stringify(error.details, null, 2)}
                </pre>
              </dd>
            </div>
          )}
        </div>

        <DialogFooter className="px-4">
          <Button variant="outline" size="sm" onClick={handleCopy} className="gap-1.5">
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
          </Button>
          <Button variant="outline" size="sm" onClick={onClose}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
