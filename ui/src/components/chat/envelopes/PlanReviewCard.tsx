import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { useApprovePlan, useRejectPlan, useTogglePlanStep } from '@/hooks/usePlans'
import type { PlanStatus } from '@/lib/types'
import { PlanStepItem } from '@/components/work/PlanStepItem'

const STATUS_STYLE: Record<PlanStatus, string> = {
  proposed: 'text-warning bg-warning/10',
  approved: 'text-success bg-success/10',
  in_progress: 'text-info bg-info/10',
  complete: 'text-fg-muted bg-surface',
  abandoned: 'text-fg-faint bg-surface/50',
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
}

export function PlanReviewCard({ data }: PlanReviewCardProps) {
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
  const steps = plan?.steps ?? data.steps.map((s) => ({ ...s, status: 'pending' as const, depends_on: [] as string[] }))
  const doneCount = steps.filter((s) => s.status === 'done').length
  const totalCount = steps.length
  const progressPct = totalCount > 0 ? (doneCount / totalCount) * 100 : 0

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
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/60 overflow-hidden my-2">
      <div className="px-3 py-2">
        <div className="flex items-center justify-between mb-1">
          <span className="text-sm font-semibold text-fg">{plan?.title ?? data.title}</span>
          <span className={`text-[9px] px-1.5 py-0.5 rounded ${STATUS_STYLE[currentStatus]}`}>
            {currentStatus}
          </span>
        </div>
        {(plan?.description || data.description) && (
          <p className="text-[11px] text-fg-muted mb-2">{plan?.description || data.description}</p>
        )}

        <div className="border-l-2 border-border-subtle pl-2 ml-1 mb-2 space-y-0.5">
          {steps.map((step) => (
            <PlanStepItem
              key={step.id}
              step={{ ...step, status: step.status || 'pending' }}
              onCheck={(stepId) => toggleStep.check(data.plan_id, stepId)}
              onUncheck={(stepId, reason) => toggleStep.uncheck(data.plan_id, stepId, reason)}
            />
          ))}
        </div>

        {totalCount > 0 && (
          <div className="flex items-center gap-1.5 mb-2">
            <div className="flex-1 h-1 bg-surface rounded-full overflow-hidden">
              <div
                className="h-full bg-primary rounded-full transition-all duration-300"
                style={{ width: `${progressPct}%` }}
              />
            </div>
            <span className="text-[9px] text-fg-muted">{doneCount}/{totalCount}</span>
          </div>
        )}

        {!acted && currentStatus === 'proposed' && (
          <div className="flex gap-2">
            <button
              type="button"
              onClick={handleApprove}
              disabled={approvePlan.isPending}
              className="px-3 py-1 bg-primary text-white text-[11px] rounded hover:bg-primary/80 transition-colors disabled:opacity-50"
            >
              Approve
            </button>
            <button
              type="button"
              onClick={handleReject}
              className="px-3 py-1 bg-surface text-fg-muted text-[11px] rounded hover:bg-surface-hover transition-colors"
            >
              Reject
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
