import { ArrowLeft } from 'lucide-react'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useWorkflowRun, useCancelWorkflowRun } from '@/hooks/useWorkflows'
import { WorkflowStepItem } from './WorkflowStepItem'
import type { StepState } from '@/lib/types'

interface WorkflowRunDetailProps {
  runId: string
  onBack: () => void
}

function statusBadgeClass(status: string): string {
  switch (status) {
    case 'running':
      return 'bg-success/20 text-success'
    case 'completed':
      return 'bg-success/20 text-success'
    case 'failed':
      return 'bg-danger/20 text-danger'
    case 'cancelled':
      return 'bg-fg-muted/10 text-fg-muted'
    case 'pending':
      return 'bg-warning/20 text-warning'
    default:
      return 'bg-surface/40 text-fg-faint'
  }
}

function formatStarted(dateStr: string): string {
  try {
    return new Date(dateStr).toLocaleString(undefined, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    })
  } catch {
    return dateStr
  }
}

export function WorkflowRunDetail({ runId, onBack }: WorkflowRunDetailProps) {
  const { data: workflowRun, isLoading } = useWorkflowRun(runId)
  const cancelMutation = useCancelWorkflowRun()

  if (isLoading) {
    return (
      <div className="flex flex-col h-full">
        <div className="px-3 py-2 border-b border-border-subtle shrink-0">
          <Skeleton className="h-5 w-40" />
        </div>
        <div className="p-3 space-y-3">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-8 w-full rounded" />
          ))}
        </div>
      </div>
    )
  }

  if (!workflowRun) {
    return (
      <div className="flex flex-col h-full">
        <button
          type="button"
          onClick={onBack}
          className="flex items-center gap-1.5 px-3 py-2 text-xs text-fg-muted hover:text-fg transition-colors border-b border-border-subtle shrink-0"
        >
          <ArrowLeft className="w-3.5 h-3.5" />
          Back
        </button>
        <div className="flex-1 flex items-center justify-center">
          <p className="text-xs text-fg-faint">Run not found.</p>
        </div>
      </div>
    )
  }

  const { pipeline, run } = workflowRun
  const status = run.status
  const isActive = status === 'running' || status === 'pending'

  // Sort steps: started_at ascending, pending last
  const sortedSteps: StepState[] = Object.values(run.step_states ?? {}).sort((a, b) => {
    if (!a.started_at && !b.started_at) return 0
    if (!a.started_at) return 1
    if (!b.started_at) return -1
    return new Date(a.started_at).getTime() - new Date(b.started_at).getTime()
  })

  return (
    <div className="flex flex-col h-full min-h-0">
      {/* Header */}
      <div className="px-3 py-2 border-b border-border-subtle shrink-0 space-y-1.5">
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={onBack}
            className="p-0.5 rounded text-fg-muted hover:text-fg transition-colors shrink-0"
            aria-label="Back to runs"
          >
            <ArrowLeft className="w-3.5 h-3.5" />
          </button>
          <span className="text-xs font-semibold text-fg truncate flex-1">{pipeline.name}</span>
          <span className={`text-[10px] px-1.5 py-0.5 rounded-full font-medium shrink-0 ${statusBadgeClass(status)}`}>
            {status}
          </span>
          {isActive && (
            <Button
              size="sm"
              variant="destructive"
              className="h-5 px-2 text-[10px]"
              disabled={cancelMutation.isPending}
              onClick={() => cancelMutation.mutate(run.run_id)}
            >
              Cancel
            </Button>
          )}
        </div>

        {/* Meta line */}
        <div className="flex items-center gap-2 pl-6 text-[11px] text-fg-faint">
          <span className="font-mono truncate max-w-[120px]" title={run.run_id}>
            {run.run_id.slice(0, 8)}…
          </span>
          {run.started_at && (
            <>
              <span>·</span>
              <span>{formatStarted(run.started_at)}</span>
            </>
          )}
        </div>
      </div>

      {/* Step timeline */}
      <ScrollArea className="flex-1 min-h-0">
        <div className="px-3 pt-3 pb-2">
          {sortedSteps.length === 0 ? (
            <p className="text-xs text-fg-faint text-center py-6">No steps recorded yet.</p>
          ) : (
            sortedSteps.map((step, i) => (
              <WorkflowStepItem
                key={step.step_id}
                step={step}
                isLast={i === sortedSteps.length - 1}
              />
            ))
          )}
        </div>

        {/* Error footer */}
        {run.error && (
          <div className="mx-3 mb-3 px-3 py-2 rounded bg-danger/10 border border-danger/20">
            <p className="text-[11px] text-danger break-words">{run.error}</p>
          </div>
        )}
      </ScrollArea>
    </div>
  )
}
