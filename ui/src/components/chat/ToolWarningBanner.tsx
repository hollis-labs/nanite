import { AlertTriangle, AlertCircle } from 'lucide-react'
import type { ToolWarning } from '@/lib/types'

interface ToolWarningBannerProps {
  warnings: ToolWarning[]
}

export function ToolWarningBanner({ warnings }: ToolWarningBannerProps) {
  if (warnings.length === 0) return null

  const hasCritical = warnings.some((w) => w.level === 'critical')
  const latestWarning = warnings[warnings.length - 1]

  return (
    <div
      className={`flex items-start gap-2 px-3 py-2 rounded-md text-sm ${
        hasCritical
          ? 'bg-danger/10 border border-danger/20 text-danger'
          : 'bg-warning/10 border border-warning/20 text-warning'
      }`}
    >
      {hasCritical ? (
        <AlertCircle className="w-4 h-4 mt-0.5 shrink-0" />
      ) : (
        <AlertTriangle className="w-4 h-4 mt-0.5 shrink-0" />
      )}
      <div>
        <div className="font-medium">
          {hasCritical
            ? `Multiple tool failures (${warnings.length}) — response may be incomplete`
            : `Tool warning: ${latestWarning.tool_name || 'unknown'}`}
        </div>
        <div className="text-xs opacity-75 mt-0.5">
          {latestWarning.error.length > 120
            ? latestWarning.error.substring(0, 120) + '...'
            : latestWarning.error}
        </div>
      </div>
    </div>
  )
}
