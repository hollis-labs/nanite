import { AlertTriangle, AlertCircle } from 'lucide-react'
import type { ToolWarning } from '@/lib/types'
import { Envelope, EnvelopeHeader, StatusPill } from './envelopes/primitives'

/**
 * POLISHED — tool warning banner.
 *
 * Changes vs. original:
 *  - Was ad-hoc `rounded-md bg-warning/10 border border-warning/20` block
 *    where the headline text itself was warning-colored — made the whole
 *    banner feel unrecoverable. Now uses the Envelope primitive with a
 *    semantic accent stripe; headline is fg, meta is muted, tone lives in
 *    the stripe + icon.
 *  - Critical count is now a StatusPill (danger tone) in the header's `meta`
 *    slot — glanceable, consistent with every other count chip.
 *  - Body truncates to 200 chars (not 120) because warnings are technical and
 *    the extra context earns its space.
 */

interface ToolWarningBannerProps {
  warnings: ToolWarning[]
}

export function ToolWarningBanner({ warnings }: ToolWarningBannerProps) {
  if (warnings.length === 0) return null

  const hasCritical = warnings.some((w) => w.level === 'critical')
  const latestWarning = warnings[warnings.length - 1]
  const tone = hasCritical ? 'danger' : 'warning'
  const Icon = hasCritical ? AlertCircle : AlertTriangle
  const label = hasCritical ? 'Tool failures' : 'Tool warning'

  const body =
    latestWarning.error.length > 200
      ? latestWarning.error.substring(0, 200) + '…'
      : latestWarning.error

  return (
    <Envelope accent={tone}>
      <EnvelopeHeader
        icon={Icon}
        label={label}
        tone={tone}
        meta={latestWarning.tool_name}
        action={
          hasCritical && warnings.length > 1 ? (
            <StatusPill tone="danger">{warnings.length} fails</StatusPill>
          ) : null
        }
      />
      <div className="px-4 py-3">
        <p className="text-[13px] leading-relaxed text-fg">
          {hasCritical
            ? 'Multiple tool calls failed in this turn. The response may be incomplete.'
            : `Tool "${latestWarning.tool_name || 'unknown'}" raised a warning.`}
        </p>
        <pre className="mt-2 max-h-32 overflow-y-auto whitespace-pre-wrap break-words rounded-[4px] border border-border-subtle bg-surface px-2 py-1.5 font-mono text-[11px] leading-relaxed text-fg-secondary">
          {body}
        </pre>
      </div>
    </Envelope>
  )
}
