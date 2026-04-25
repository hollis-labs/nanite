import { AlertTriangle } from 'lucide-react'
import { Envelope, EnvelopeHeader } from './envelopes/primitives'

/**
 * POLISHED — iteration limit warning.
 *
 * Changes vs. original:
 *  - Was a standalone colored pill bar with mixed primary/warning tones
 *    depending on severity. Now uses the Envelope primitive with an accent
 *    stripe that escalates tone (warning → danger) as the limit approaches.
 *  - Adds a small progress meter in the body — the headline tells you the
 *    ratio, but the meter makes "how close" scannable at a glance.
 *  - Header meta shows the percentage in mono, matching the ReportCard /
 *    MetricCard grammar for numeric readouts.
 */

interface IterationLimitWarningProps {
  current: number
  max: number
}

export function IterationLimitWarning({ current, max }: IterationLimitWarningProps) {
  if (max <= 0 || current / max < 0.8) return null

  const percent = Math.round((current / max) * 100)
  const critical = percent >= 95
  const tone = critical ? 'danger' : 'warning'

  return (
    <Envelope accent={tone}>
      <EnvelopeHeader
        icon={AlertTriangle}
        label={critical ? 'Turn limit critical' : 'Approaching turn limit'}
        tone={tone}
        meta={`${current} / ${max}`}
      />
      <div className="px-4 py-3">
        <p className="text-[13px] leading-relaxed text-fg">
          {critical
            ? 'The response may be truncated — consider forking the session to continue.'
            : 'You are approaching the per-session turn limit for this agent.'}
        </p>
        <div
          role="progressbar"
          aria-valuenow={percent}
          aria-valuemin={0}
          aria-valuemax={100}
          className="mt-3 h-1 w-full overflow-hidden rounded-full bg-surface"
        >
          <div
            className={`h-full transition-all ${critical ? 'bg-danger' : 'bg-warning'}`}
            style={{ width: `${Math.min(percent, 100)}%` }}
          />
        </div>
      </div>
    </Envelope>
  )
}
