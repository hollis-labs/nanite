import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Check, ListChecks } from 'lucide-react'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { useApprovePlan, useRejectPlan, useTogglePlanStep } from '@/hooks/usePlans'
import type { PlanStatus, PlanStep } from '@/lib/types'
import { Envelope, EnvelopeHeader, EnvelopeBody, EnvelopeFooter } from './primitives/Envelope'
import { StatusPill, type StatusTone } from './primitives/StatusPill'

const STATUS_TONE: Record<PlanStatus, StatusTone> = {
  proposed: 'warning',
  approved: 'success',
  in_progress: 'info',
  complete: 'neutral',
  abandoned: 'neutral',
}

interface PlanReviewCardData {
  plan_id: string
  title: string
  description?: string
  status: PlanStatus
  steps: Array<{ id: string; title: string }>
}

interface PlanReviewCardProps {
  data: PlanReviewCardData
  onSendMessage?: (content: string) => void
}

function StepRow({
  step,
  onCheck,
  onUncheck,
}: {
  step: PlanStep
  onCheck: (stepId: string) => void
  onUncheck: (stepId: string, reason?: string) => void
}) {
  const isDone = step.status === 'done'
  const isActive = step.status === 'in_progress'
  const isSkipped = step.status === 'skipped'

  return (
    <div
      className={`flex items-center gap-2.5 rounded-[6px] px-2 py-1.5 ${
        isActive ? 'bg-primary-muted/80' : 'bg-transparent'
      }`}
    >
      <button
        type="button"
        onClick={() => {
          if (isDone) onUncheck(step.id)
          else if (!isSkipped) onCheck(step.id)
        }}
        disabled={isSkipped}
        className={`flex h-4 w-4 shrink-0 items-center justify-center rounded-[4px] transition-colors ${
          isDone
            ? 'bg-success text-success-fg'
            : isActive
              ? 'border border-primary text-primary'
              : isSkipped
                ? 'border border-border-subtle opacity-40'
                : 'border border-border-subtle text-transparent hover:border-primary'
        }`}
        aria-label={isDone ? 'Mark incomplete' : 'Mark complete'}
      >
        {isDone && <Check className="h-2.5 w-2.5" strokeWidth={3} />}
        {isActive && <span className="h-1.5 w-1.5 rounded-full bg-primary" />}
      </button>

      <span
        className={`min-w-0 flex-1 text-[13px] ${
          isDone
            ? 'text-fg-muted line-through'
            : isSkipped
              ? 'text-fg-faint line-through'
              : isActive
                ? 'font-medium text-fg'
                : 'text-fg-secondary'
        }`}
      >
        {step.title}
      </span>

      {isActive && <StatusPill tone="primary">In progress</StatusPill>}
    </div>
  )
}

export function PlanReviewCard({ data, onSendMessage }: PlanReviewCardProps) {
  const { data: plan } = useQuery({
    queryKey: ['plans', data.plan_id],
    queryFn: () => api.getPlan(data.plan_id),
    initialData: undefined,
  })
  const approvePlan = useApprovePlan()
  const { reject } = useRejectPlan()
  const toggleStep = useTogglePlanStep()
  const [acted, setActed] = useState(false)

  const currentStatus = plan?.status ?? data.status
  // `steps` is required by the type, but a partially-loaded envelope can
  // arrive without it — normalize before `.map` so the card degrades
  // gracefully instead of throwing.
  const steps =
    plan?.steps ??
    (data.steps ?? []).map((s) => ({ ...s, status: 'pending' as const, depends_on: [] as string[] }))
  const doneCount = steps.filter((s) => s.status === 'done').length
  const totalCount = steps.length
  const progressPct = totalCount > 0 ? (doneCount / totalCount) * 100 : 0
  const canAct = !acted && currentStatus === 'proposed'

  const handleApprove = () => {
    approvePlan.mutate(
      { id: data.plan_id, createTodos: true },
      { onSuccess: () => setActed(true) },
    )
  }

  const handleReject = () => {
    reject(data.plan_id)
    setActed(true)
  }

  return (
    <Envelope className="my-2">
      <EnvelopeHeader
        icon={ListChecks}
        label="Plan review"
        tone={currentStatus === 'proposed' ? 'warning' : currentStatus === 'approved' ? 'success' : 'neutral'}
        meta={totalCount > 0 ? `${doneCount}/${totalCount} complete` : undefined}
      />

      <EnvelopeBody>
        <div className="mb-1 flex items-start justify-between gap-3">
          <h3 className="text-[15px] font-semibold leading-snug text-fg">
            {plan?.title ?? data.title}
          </h3>
          <StatusPill tone={STATUS_TONE[currentStatus]}>
            {currentStatus.replace('_', ' ')}
          </StatusPill>
        </div>

        {(plan?.description || data.description) && (
          <p className="mb-3.5 text-[13px] leading-relaxed text-fg-secondary">
            {plan?.description || data.description}
          </p>
        )}

        {totalCount > 0 && (
          <div className="mb-3.5 space-y-0.5">
            {steps.map((step) => (
              <StepRow
                key={step.id}
                step={{ ...step, status: step.status || 'pending' }}
                onCheck={(stepId) => toggleStep.check(data.plan_id, stepId)}
                onUncheck={(stepId, reason) =>
                  toggleStep.uncheck(data.plan_id, stepId, reason)
                }
              />
            ))}
          </div>
        )}

        {totalCount > 0 && (
          <div className="flex items-center gap-2.5">
            <div className="h-1 flex-1 overflow-hidden rounded-full bg-surface">
              <div
                className="h-full rounded-full bg-success transition-all duration-300"
                style={{ width: `${progressPct}%` }}
              />
            </div>
            <span className="font-mono text-[10px] text-fg-muted">
              {Math.round(progressPct)}%
            </span>
          </div>
        )}
      </EnvelopeBody>

      {canAct && (
        <EnvelopeFooter className="gap-2.5">
          <Button
            size="sm"
            onClick={handleApprove}
            disabled={approvePlan.isPending}
          >
            Approve plan
          </Button>
          {onSendMessage && (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => onSendMessage(`Please revise the proposed plan "${plan?.title ?? data.title}".`)}
            >
              Request changes
            </Button>
          )}
          <div className="flex-1" />
          <Button size="sm" variant="ghost" onClick={handleReject}>
            Reject
          </Button>
        </EnvelopeFooter>
      )}
    </Envelope>
  )
}
