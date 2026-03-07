import { CheckCircle, XCircle, Loader2, Circle } from 'lucide-react'
import type { WorkflowResult, StepResult } from '@/lib/types'

interface WorkflowResultCardProps {
  result: WorkflowResult
}

const STATUS_CONFIG: Record<StepResult['status'], { icon: typeof CheckCircle; color: string }> = {
  pending: { icon: Circle, color: 'text-zinc-500' },
  running: { icon: Loader2, color: 'text-blue-400' },
  done: { icon: CheckCircle, color: 'text-green-400' },
  error: { icon: XCircle, color: 'text-red-400' },
}

export function WorkflowResultCard({ result }: WorkflowResultCardProps) {
  const overallDone = result.status === 'done'
  const overallError = result.status === 'error'

  return (
    <div className="space-y-3">
      {/* Overall status */}
      <div className="flex items-center gap-2">
        {overallDone && <CheckCircle className="w-4 h-4 text-green-400" />}
        {overallError && <XCircle className="w-4 h-4 text-red-400" />}
        {!overallDone && !overallError && <Loader2 className="w-4 h-4 text-blue-400 animate-spin" />}
        <span className="text-sm font-medium text-zinc-200">
          {result.workflow}
        </span>
        <span className={`text-xs ${overallDone ? 'text-green-400' : overallError ? 'text-red-400' : 'text-blue-400'}`}>
          {result.status}
        </span>
      </div>

      {/* Steps */}
      {(result.steps ?? []).length > 0 && (
        <div className="space-y-1 pl-1">
          {(result.steps ?? []).map((step, i) => {
            const config = STATUS_CONFIG[step.status]
            const Icon = config.icon
            return (
              <div key={i} className="flex items-start gap-2 py-1">
                <Icon className={`w-3.5 h-3.5 mt-0.5 shrink-0 ${config.color} ${step.status === 'running' ? 'animate-spin' : ''}`} />
                <div className="flex-1 min-w-0">
                  <span className="text-sm text-zinc-300">{step.name}</span>
                  {step.output && (
                    <pre className="mt-1 text-xs text-zinc-500 bg-zinc-800/50 rounded p-2 overflow-x-auto whitespace-pre-wrap">
                      {step.output}
                    </pre>
                  )}
                  {step.error && (
                    <p className="mt-1 text-xs text-red-400">{step.error}</p>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* Overall output */}
      {result.output && (
        <pre className="text-xs text-zinc-400 bg-zinc-800/50 rounded-lg p-3 overflow-x-auto whitespace-pre-wrap border border-zinc-700/50">
          {result.output}
        </pre>
      )}
    </div>
  )
}
