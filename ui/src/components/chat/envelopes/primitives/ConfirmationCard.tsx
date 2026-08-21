import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle, XCircle } from 'lucide-react'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { useApprovePlan, useRejectPlan } from '@/hooks/usePlans'
import { ResponseStatus } from '@/lib/envelope-response'
import type { Envelope as EnvelopeType, PlanStatus } from '@/lib/types'
import type { EnvelopeResponder } from '../EnvelopeRenderer'
import { Envelope, EnvelopeHeader, EnvelopeFooter } from './Envelope'
import { StatusPill, type StatusTone } from './StatusPill'

interface ConfirmationCardDataSource {
  /**
   * "plan_approval" re-fetches the plan via `plan_id` and routes
   * confirm/cancel through the same approve/reject mutation the old
   * standalone `plan-review` envelope used (useApprovePlan/useRejectPlan)
   * instead of the generic typed-response POST.
   */
  kind: 'plan_approval'
  plan_id: string
}

interface ConfirmationCardData {
  title: string
  message: string
  confirm_label?: string
  cancel_label?: string
  request_changes_label?: string
  risk?: 'low' | 'medium' | 'high'
  data_source?: ConfirmationCardDataSource
}

const PLAN_STATUS_TONE: Record<PlanStatus, StatusTone> = {
  proposed: 'warning',
  approved: 'success',
  in_progress: 'info',
  complete: 'neutral',
  abandoned: 'neutral',
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

  // Live plan-approval composition (TASKS/phase-6/02-rebuild-plan-review-
  // as-composition.md) — the standalone `plan-review` type is retired; a
  // `confirmation-card` with `data_source.kind === "plan_approval"` renders
  // the same live approve/reject/request-changes footer PlanReviewCard
  // used to, driven by the plan's own live status instead of the generic
  // `prior_response` hydration path below.
  if (data.data_source?.kind === 'plan_approval' && data.data_source.plan_id) {
    return (
      <PlanApprovalConfirmationCard
        data={data}
        planId={data.data_source.plan_id}
        onSendMessage={onSendMessage}
      />
    )
  }

  return <GenericConfirmationCard envelope={envelope} data={data} onRespond={onRespond} onSendMessage={onSendMessage} />
}

function GenericConfirmationCard({
  envelope,
  data,
  onRespond,
  onSendMessage,
}: {
  envelope: EnvelopeType
  data: ConfirmationCardData
  onRespond?: EnvelopeResponder
  onSendMessage?: (content: string) => void
}) {
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

/**
 * Live plan-approval rendering — the `confirmation-card` half of the
 * `plan-review` composition. Re-fetches the plan via
 * `useQuery(['plans', plan_id])` (the same key PlanReviewCard and the
 * paired `list-card` half use) so the footer reflects the real current
 * status even if the plan was approved/rejected through another path
 * (e.g. a chat command), and drives Approve/Reject through the exact
 * mutations PlanReviewCard used (useApprovePlan/useRejectPlan) instead of
 * the generic typed-response POST — preserving identical behavior rather
 * than routing through `onRespond`.
 */
function PlanApprovalConfirmationCard({
  data,
  planId,
  onSendMessage,
}: {
  data: ConfirmationCardData
  planId: string
  onSendMessage?: (content: string) => void
}) {
  const { data: plan } = useQuery({
    queryKey: ['plans', planId],
    queryFn: () => api.getPlan(planId),
  })
  const approvePlan = useApprovePlan()
  const { reject } = useRejectPlan()
  const [acted, setActed] = useState(false)

  const currentStatus: PlanStatus = plan?.status ?? 'proposed'
  const canAct = !acted && currentStatus === 'proposed'
  const confirmLabel = data.confirm_label || 'Approve plan'
  const cancelLabel = data.cancel_label || 'Reject'
  const requestChangesLabel = data.request_changes_label || 'Request changes'
  const title = plan?.title ?? data.title

  const handleApprove = () => {
    approvePlan.mutate(
      { id: planId, createTodos: true },
      { onSuccess: () => setActed(true) },
    )
  }

  const handleReject = () => {
    reject(planId)
    setActed(true)
  }

  return (
    <Envelope>
      <EnvelopeHeader
        icon={CheckCircle}
        label="Approve plan"
        tone={
          currentStatus === 'proposed' ? 'warning' : currentStatus === 'approved' ? 'success' : 'neutral'
        }
        action={<StatusPill tone={PLAN_STATUS_TONE[currentStatus]}>{currentStatus.replace('_', ' ')}</StatusPill>}
      />

      <div className="px-4 py-3">
        <h4 className="mb-1.5 text-[14px] font-semibold leading-snug text-fg">{title}</h4>
        <p className="text-[13px] leading-relaxed text-fg-secondary">{data.message}</p>
      </div>

      {canAct && (
        <EnvelopeFooter className="gap-2.5">
          <Button size="sm" onClick={handleApprove} disabled={approvePlan.isPending}>
            {confirmLabel}
          </Button>
          {onSendMessage && (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => onSendMessage(`Please revise the proposed plan "${title}".`)}
            >
              {requestChangesLabel}
            </Button>
          )}
          <div className="flex-1" />
          <Button size="sm" variant="ghost" onClick={handleReject}>
            {cancelLabel}
          </Button>
        </EnvelopeFooter>
      )}
    </Envelope>
  )
}
