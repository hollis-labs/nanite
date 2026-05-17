import { FlaskConical } from 'lucide-react'
import { cn } from '@/lib/utils'
import { StatusPill } from './StatusPill'

/**
 * DevBadge — a small "DEV" marker for dev-mode-only envelopes
 * (CW-20260517-0008).
 *
 * Some envelope types are only emitted when developer mode is enabled
 * (today: `chat-loop-budget-soft-warning`, gated behind `devModeEnabled()`
 * in the backend). To an operator these look like normal alerts and get
 * reported as bugs when they are just dev-mode telemetry. This badge marks
 * them at a glance.
 *
 * Type-agnostic: rendered by EnvelopeRenderer for ANY envelope carrying the
 * wrap-level `dev_mode_only` marker, regardless of which render path the
 * envelope takes (component or the unreachable fallback). Any future
 * dev-only envelope type gets the badge for free.
 */
export function DevBadge({ className }: { className?: string }) {
  return (
    <StatusPill
      tone="info"
      className={cn(className)}
      title="Dev-mode-only telemetry — only visible while developer mode is enabled"
    >
      <FlaskConical className="h-3 w-3" />
      DEV
    </StatusPill>
  )
}

/**
 * DevModeEnvelopeWrapper — wraps any envelope's rendered output and floats a
 * DevBadge in the top-right corner so the marker is visible no matter which
 * card (or fallback) the envelope resolved to. EnvelopeRenderer applies this
 * whenever `envelope.dev_mode_only` is set.
 */
export function DevModeEnvelopeWrapper({
  children,
}: {
  children: React.ReactNode
}) {
  return (
    <div data-dev-mode-envelope="true" className="relative">
      <div className="pointer-events-none absolute right-2 top-2 z-10">
        <DevBadge className="pointer-events-auto" />
      </div>
      {children}
    </div>
  )
}
