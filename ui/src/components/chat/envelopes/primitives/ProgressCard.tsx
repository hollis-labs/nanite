import { CheckCircle, Circle } from 'lucide-react'

interface ProgressStep {
  label: string
  done: boolean
}

interface ProgressCardData {
  title: string
  progress: number
  status?: string
  description?: string
  steps?: ProgressStep[]
}

interface ProgressCardProps {
  data: ProgressCardData
}

export function ProgressCard({ data }: ProgressCardProps) {
  const pct = Math.min(100, Math.max(0, data.progress))

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 p-4">
      <div className="flex items-center justify-between mb-2">
        <h4 className="text-sm font-medium text-fg">{data.title}</h4>
        <span className="text-xs font-mono text-fg-muted">{pct}%</span>
      </div>

      {data.status && (
        <p className="text-xs text-fg-secondary mb-2">{data.status}</p>
      )}

      {/* Progress bar */}
      <div className="h-2 rounded-full bg-surface overflow-hidden">
        <div
          className="h-full rounded-full bg-info transition-all duration-500 ease-out"
          style={{ width: `${pct}%` }}
        />
      </div>

      {data.description && (
        <p className="text-xs text-fg-muted mt-2">{data.description}</p>
      )}

      {/* Step checklist */}
      {data.steps && data.steps.length > 0 && (
        <div className="mt-3 space-y-1.5 border-t border-border/50 pt-3">
          {data.steps.map((step, i) => (
            <div key={`step-${i}`} className="flex items-center gap-2">
              {step.done ? (
                <CheckCircle className="w-3.5 h-3.5 text-success shrink-0" />
              ) : (
                <Circle className="w-3.5 h-3.5 text-fg-muted shrink-0" />
              )}
              <span className={`text-xs ${step.done ? 'text-fg-secondary line-through' : 'text-fg'}`}>
                {step.label}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
