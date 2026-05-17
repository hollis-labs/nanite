import { useState } from 'react'
import { AlertTriangle, CheckCircle, XCircle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { ResponseStatus } from '@/lib/envelope-response'
import type { Envelope as EnvelopeType } from '@/lib/types'
import type { EnvelopeResponder } from '../EnvelopeRenderer'
import { Envelope, EnvelopeHeader, EnvelopeFooter } from './Envelope'
import { StatusPill } from './StatusPill'

interface ConfirmationCardData {
  title: string
  message: string
  confirm_label?: string
  cancel_label?: string
  risk?: 'low' | 'medium' | 'high'
}

interface ConfirmationCardProps {
  envelope: EnvelopeType
  onRespond?: EnvelopeResponder
  /**
   * @deprecated legacy untyped path. Kept only as a fallback for envelopes
   * with no `id` (pre-Phase-3 emits) where `onRespond` is not wired.
   */
  onSendMessage?: (content: string) => void
}

/**
 * Hydrate the decision from a persisted response so the card keeps its
 * resolved state after a page reload. CW-20260517-0006.
 */
function hydrateDecision(
  prior: EnvelopeType['prior_response'],
): 'pending' | 'confirmed' | 'cancelled' {
  if (!prior) return 'pending'
  return prior.status === ResponseStatus.Cancelled ? 'cancelled' : 'confirmed'
}

export function ConfirmationCard({ envelope, onRespond, onSendMessage }: ConfirmationCardProps) {
  const data = (envelope.data ?? {}) as unknown as ConfirmationCardData
  const [decision, setDecision] = useState<'pending' | 'confirmed' | 'cancelled'>(() =>
    hydrateDecision(envelope.prior_response),
  )

  const risk = data.risk || 'low'
  const confirmLabel = data.confirm_label || 'Confirm'
  const cancelLabel = data.cancel_label || 'Cancel'

  const handleConfirm = () => {
    setDecision('confirmed')
    if (onRespond) {
      void onRespond({ status: ResponseStatus.Submitted, data: { confirmed: true } }).catch(() => {
        setDecision('pending')
      })
    } else {
      // Legacy fallback — only reached when the envelope has no id.
      onSendMessage?.(`confirm:${data.title}`)
    }
  }

  const handleCancel = () => {
    setDecision('cancelled')
    if (onRespond) {
      void onRespond({ status: ResponseStatus.Cancelled, data: { confirmed: false } }).catch(() => {
        setDecision('pending')
      })
    } else {
      // Legacy fallback — only reached when the envelope has no id.
      onSendMessage?.(`cancel:${data.title}`)
    }
  }

  if (decision === 'confirmed') {
    return (
      <Envelope accent="success" muted>
        <div className="flex items-center gap-2 px-4 py-2.5">
          <CheckCircle className="h-4 w-4 text-success" />
          <span className="text-[13px] text-fg">Confirmed: {data.title}</span>
        </div>
      </Envelope>
    )
  }

  if (decision === 'cancelled') {
    return (
      <Envelope muted>
        <div className="flex items-center gap-2 px-4 py-2.5 text-fg-muted">
          <XCircle className="h-4 w-4" />
          <span className="text-[13px]">Cancelled: {data.title}</span>
        </div>
      </Envelope>
    )
  }

  const headerTone = risk === 'high' ? 'danger' : risk === 'medium' ? 'warning' : 'neutral'
  const pillTone = risk === 'high' ? 'danger' : risk === 'medium' ? 'warning' : 'info'

  return (
    <Envelope accent={risk === 'high' ? 'danger' : risk === 'medium' ? 'warning' : undefined}>
      <EnvelopeHeader
        icon={risk === 'high' ? AlertTriangle : undefined}
        label="Confirm action"
        tone={headerTone}
        action={<StatusPill tone={pillTone}>{risk} risk</StatusPill>}
      />

      <div className="px-4 py-3">
        <h4 className="mb-1.5 text-[14px] font-semibold leading-snug text-fg">
          {data.title}
        </h4>
        <p className="text-[13px] leading-relaxed text-fg-secondary">{data.message}</p>
      </div>

      <EnvelopeFooter>
        <Button
          size="sm"
          variant={risk === 'high' ? 'destructive' : 'default'}
          onClick={handleConfirm}
        >
          {confirmLabel}
        </Button>
        <Button size="sm" variant="ghost" onClick={handleCancel}>
          {cancelLabel}
        </Button>
      </EnvelopeFooter>
    </Envelope>
  )
}
