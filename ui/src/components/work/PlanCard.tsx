import { useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import type { Plan, PlanStatus } from '@/lib/types'
import { PlanStepItem } from './PlanStepItem'
import { AddItemInput } from './AddItemInput'

const STATUS_STYLE: Record<PlanStatus, string> = {
  proposed: 'text-warning bg-warning/10',
  approved: 'text-success bg-success/10',
  in_progress: 'text-info bg-info/10',
  complete: 'text-fg-muted bg-surface',
  abandoned: 'text-fg-faint bg-surface/50',
}

interface PlanCardProps {
  plan: Plan
  onStepCheck: (planId: string, stepId: string) => void
  onStepUncheck: (planId: string, stepId: string, reason?: string) => void
  onAddStep?: (planId: string, title: string) => void
}

export function PlanCard({ plan, onStepCheck, onStepUncheck, onAddStep }: PlanCardProps) {
  const [expanded, setExpanded] = useState(true)
  const doneCount = plan.steps.filter((s) => s.status === 'done').length
  const totalCount = plan.steps.length
  const progressPct = totalCount > 0 ? (doneCount / totalCount) * 100 : 0

  return (
    <div className="rounded-md border border-border-subtle bg-bg-elevated overflow-hidden">
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="flex items-center gap-2 w-full px-2 py-1.5 text-left hover:bg-surface/30 transition-colors"
      >
        {expanded ? (
          <ChevronDown className="w-3 h-3 text-fg-faint shrink-0" />
        ) : (
          <ChevronRight className="w-3 h-3 text-fg-faint shrink-0" />
        )}
        <span className="flex-1 text-xs font-medium text-fg truncate">{plan.title}</span>
        <span className={`text-[9px] px-1.5 py-0.5 rounded ${STATUS_STYLE[plan.status]}`}>
          {plan.status}
        </span>
      </button>

      {expanded && (
        <div className="px-2 pb-2">
          <div className="border-l-2 border-border-subtle pl-2 ml-1 space-y-0.5">
            {plan.steps.map((step) => (
              <PlanStepItem
                key={step.id}
                step={step}
                onCheck={(stepId) => onStepCheck(plan.id, stepId)}
                onUncheck={(stepId, reason) => onStepUncheck(plan.id, stepId, reason)}
              />
            ))}
            {onAddStep && (
              <div className="pt-0.5">
                <AddItemInput
                  placeholder="Add step..."
                  onAdd={(title) => onAddStep(plan.id, title)}
                />
              </div>
            )}
          </div>

          {totalCount > 0 && (
            <div className="flex items-center gap-1.5 mt-2 pt-1.5 border-t border-border-subtle/50">
              <div className="flex-1 h-1 bg-surface rounded-full overflow-hidden">
                <div
                  className="h-full bg-primary rounded-full transition-all duration-300"
                  style={{ width: `${progressPct}%` }}
                />
              </div>
              <span className="text-[9px] text-fg-muted">{doneCount}/{totalCount}</span>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
