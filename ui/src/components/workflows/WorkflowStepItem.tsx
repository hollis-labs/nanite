import { CheckCircle2, Loader2, Circle, XCircle, Slash } from 'lucide-react'
import type { StepState, StepStatus } from '@/lib/types'

interface WorkflowStepItemProps {
  step: StepState
  isLast: boolean
}

function formatDuration(startedAt: string, completedAt?: string): string {
  const start = new Date(startedAt).getTime()
  const end = completedAt ? new Date(completedAt).getTime() : Date.now()
  const ms = end - start
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

function StatusIcon({ status }: { status: StepStatus }) {
  switch (status) {
    case 'completed':
      return <CheckCircle2 className="w-4 h-4 text-success shrink-0" />
    case 'running':
      return <Loader2 className="w-4 h-4 text-info shrink-0 animate-spin" />
    case 'pending':
      return <Circle className="w-4 h-4 text-fg-muted/40 shrink-0" />
    case 'failed':
      return <XCircle className="w-4 h-4 text-danger shrink-0" />
    case 'skipped':
      return <Slash className="w-4 h-4 text-fg-muted shrink-0" />
    case 'cancelled':
      return <XCircle className="w-4 h-4 text-fg-muted shrink-0" />
  }
}

function lineColor(status: StepStatus): string {
  switch (status) {
    case 'completed':
      return 'bg-success/40'
    case 'running':
      return 'bg-info/40'
    case 'failed':
      return 'bg-danger/40'
    case 'skipped':
    case 'cancelled':
      return 'bg-fg-muted/20'
    default:
      return 'bg-border'
  }
}

export function WorkflowStepItem({ step, isLast }: WorkflowStepItemProps) {
  const hasStarted = !!step.started_at

  return (
    <div className="flex gap-3">
      {/* Icon + connecting line */}
      <div className="flex flex-col items-center">
        <StatusIcon status={step.status} />
        {!isLast && (
          <div className={`w-px flex-1 min-h-3 mt-1 ${lineColor(step.status)}`} />
        )}
      </div>

      {/* Content */}
      <div className={`pb-4 flex-1 min-w-0 ${isLast ? '' : ''}`}>
        <div className="flex items-center justify-between gap-2">
          <span
            className={`text-xs font-medium truncate ${
              step.status === 'pending'
                ? 'text-fg-muted/60'
                : step.status === 'skipped' || step.status === 'cancelled'
                  ? 'text-fg-muted line-through'
                  : 'text-fg'
            }`}
          >
            {step.step_id}
          </span>

          {/* Duration / elapsed */}
          {step.status === 'completed' && hasStarted && (
            <span className="text-[10px] text-fg-faint shrink-0">
              {formatDuration(step.started_at, step.completed_at)}
            </span>
          )}
          {step.status === 'running' && hasStarted && (
            <span className="text-[10px] text-info/70 shrink-0">
              {formatDuration(step.started_at)}
            </span>
          )}
        </div>

        {/* Error */}
        {step.status === 'failed' && step.error && (
          <p className="mt-0.5 text-[11px] text-danger/80 break-words">{step.error}</p>
        )}

        {/* Skip reason */}
        {step.status === 'skipped' && step.skip_reason && (
          <p className="mt-0.5 text-[11px] text-fg-faint">{step.skip_reason}</p>
        )}
      </div>
    </div>
  )
}
