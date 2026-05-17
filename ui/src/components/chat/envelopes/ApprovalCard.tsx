import { ShieldAlert, ShieldCheck, ShieldX, type LucideIcon } from 'lucide-react'
import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { ResponseStatus } from '@/lib/envelope-response'
import type { EnvelopeApprovalRequest, Envelope as EnvelopeType } from '@/lib/types'
import { Envelope, EnvelopeHeader, EnvelopeBody, EnvelopeFooter } from './primitives/Envelope'
import { StatusPill, type StatusTone } from './primitives/StatusPill'
import type { EnvelopeResponder } from './EnvelopeRenderer'

interface ApprovalCardProps {
  envelope: EnvelopeType
  onRespond?: EnvelopeResponder
}

/**
 * Hydrate the decision from a persisted response. The backend injects
 * `prior_response` into the envelope when the card was already answered;
 * `respond()` writes `data.approved` (boolean). CW-20260517-0006.
 */
function hydrateDecision(prior: EnvelopeType['prior_response']): 'pending' | 'approved' | 'rejected' {
  if (!prior) return 'pending'
  if (prior.status === ResponseStatus.Cancelled) return 'rejected'
  return prior.data?.approved === false ? 'rejected' : 'approved'
}

const RISK_META: Record<
  'low' | 'medium' | 'high',
  { tone: StatusTone; icon: LucideIcon; label: string }
> = {
  low:    { tone: 'success', icon: ShieldCheck, label: 'Low risk' },
  medium: { tone: 'warning', icon: ShieldAlert, label: 'Medium risk' },
  high:   { tone: 'danger',  icon: ShieldX,     label: 'High risk' },
}

export function ApprovalCard({ envelope, onRespond }: ApprovalCardProps) {
  const approval: EnvelopeApprovalRequest =
    (envelope.approval ?? (envelope.data as EnvelopeApprovalRequest | undefined)) ??
    ({ description: '' } as EnvelopeApprovalRequest)
  const [decision, setDecision] = useState<'pending' | 'approved' | 'rejected'>(() =>
    hydrateDecision(envelope.prior_response),
  )
  const [submitError, setSubmitError] = useState<string | null>(null)

  const risk = approval.risk_level || 'low'
  const meta = RISK_META[risk]
  const RiskIcon = meta.icon

  const respond = async (approved: boolean) => {
    setDecision(approved ? 'approved' : 'rejected')
    setSubmitError(null)
    if (!onRespond) return
    try {
      await onRespond({
        status: ResponseStatus.Submitted,
        data: { approved, description: approval.description },
      })
    } catch (err) {
      setDecision('pending')
      setSubmitError(err instanceof Error ? err.message : 'Failed to submit approval')
    }
  }

  // Terminal states — dialed-back shell, same primitive
  if (decision === 'approved') {
    return (
      <Envelope accent="success" muted>
        <EnvelopeBody>
          <div className="flex items-center gap-2">
            <ShieldCheck className="h-4 w-4 text-success" />
            <span className="text-[13px] text-fg">Approved —</span>
            <span className="text-[13px] text-fg-secondary">{approval.description}</span>
          </div>
        </EnvelopeBody>
      </Envelope>
    )
  }

  if (decision === 'rejected') {
    return (
      <Envelope accent="neutral" muted>
        <EnvelopeBody>
          <div className="flex items-center gap-2">
            <ShieldX className="h-4 w-4 text-fg-muted" />
            <span className="text-[13px] text-fg">Rejected —</span>
            <span className="text-[13px] text-fg-secondary">{approval.description}</span>
          </div>
        </EnvelopeBody>
      </Envelope>
    )
  }

  return (
    <Envelope accent={meta.tone}>
      <EnvelopeHeader
        icon={RiskIcon}
        label="Approval required"
        tone={meta.tone}
        action={<StatusPill tone={meta.tone}>{meta.label}</StatusPill>}
      />

      <EnvelopeBody title={approval.description} description={approval.details} />

      {submitError && (
        <div
          role="alert"
          className="mx-4 mb-3 rounded-md border border-danger/30 bg-danger/5 px-3 py-2 text-[12px] text-danger"
        >
          {submitError}
        </div>
      )}

      <EnvelopeFooter>
        <Button size="sm" onClick={() => void respond(true)}>
          Approve
        </Button>
        <Button
          size="sm"
          variant="outline"
          className="text-danger hover:bg-danger/10 hover:text-danger"
          onClick={() => void respond(false)}
        >
          Reject
        </Button>
      </EnvelopeFooter>
    </Envelope>
  )
}
