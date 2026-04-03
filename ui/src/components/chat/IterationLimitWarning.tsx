import { AlertTriangle } from 'lucide-react'

interface IterationLimitWarningProps {
  current: number
  max: number
}

/**
 * Subtle warning banner when approaching the iteration/turn limit.
 * Shown when usage exceeds 80% of the max turns.
 */
export function IterationLimitWarning({ current, max }: IterationLimitWarningProps) {
  if (max <= 0 || current / max < 0.8) return null

  const percent = Math.round((current / max) * 100)
  const critical = percent >= 95

  return (
    <div className={`flex items-center gap-2 px-3 py-1.5 rounded-md border text-xs ${
      critical
        ? 'bg-accent/10 border-accent/30 text-accent'
        : 'bg-surface border-border-subtle text-fg-secondary'
    }`}>
      <AlertTriangle className="w-3.5 h-3.5 shrink-0" />
      <span>
        Approaching turn limit ({current}/{max})
        {critical && ' — response may be truncated'}
      </span>
    </div>
  )
}
