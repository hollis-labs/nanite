import { CheckCircle, Circle, Loader2 } from 'lucide-react'
import { Envelope, EnvelopeHeader, EnvelopeBody } from './Envelope'

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
    <Envelope>
      <EnvelopeHeader
        icon={Loader2}
        label="Progress"
        meta={<span className="font-mono tabular-nums">{pct}%</span>}
      />
      <EnvelopeBody>
        <h3 className="text-[14px] font-semibold leading-snug text-fg">{data.title}</h3>

        {data.status && (
          <p className="mt-1 text-[12px] text-fg-secondary">{data.status}</p>
        )}

        {/* Progress bar */}
        <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-surface">
          <div
            className="h-full rounded-full bg-info transition-all duration-500 ease-out"
            style={{ width: `${pct}%` }}
          />
        </div>

        {data.description && (
          <p className="mt-2 text-[12px] text-fg-muted">{data.description}</p>
        )}

        {/* Step checklist */}
        {data.steps && data.steps.length > 0 && (
          <div className="mt-3 space-y-1.5 border-t border-border-subtle pt-3">
            {data.steps.map((step, i) => (
              <div key={`step-${i}`} className="flex items-center gap-2">
                {step.done ? (
                  <CheckCircle className="h-3.5 w-3.5 shrink-0 text-success" />
                ) : (
                  <Circle className="h-3.5 w-3.5 shrink-0 text-fg-muted" />
                )}
                <span
                  className={`text-[12px] ${
                    step.done ? 'text-fg-muted line-through' : 'text-fg'
                  }`}
                >
                  {step.label}
                </span>
              </div>
            ))}
          </div>
        )}
      </EnvelopeBody>
    </Envelope>
  )
}
