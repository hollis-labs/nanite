import { Activity } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Widget } from './Widget'
import { api } from '@/lib/api'

function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${(ms / 60000).toFixed(1)}m`
}

function formatCost(usd: number): string {
  if (usd === 0) return '$0.00'
  if (usd < 0.01) return `$${usd.toFixed(4)}`
  return `$${usd.toFixed(2)}`
}

function MetricRow({ label, value, color }: { label: string; value: string; color: string }) {
  return (
    <div className="flex justify-between items-center text-xs">
      <span className="text-fg-muted">{label}</span>
      <span className={`${color} font-mono tabular-nums`}>{value}</span>
    </div>
  )
}

export function ObservabilityWidget() {
  const { data: executions = [] } = useQuery({
    queryKey: ['metrics', 'executions', 10],
    queryFn: () => api.getRecentExecutions(10),
    staleTime: 15_000,
    refetchInterval: 30_000,
  })

  const total = executions.length
  const avgDuration = total > 0
    ? executions.reduce((sum, m) => sum + m.duration_ms, 0) / total
    : 0
  const totalCost = executions.reduce((sum, m) => sum + m.estimated_cost_usd, 0)
  const errorCount = executions.filter((m) => m.error !== '').length
  const last = executions[0]

  return (
    <Widget id="observability" title="Observability" icon={Activity}>
      <div className="space-y-1.5">
        {last ? (
          <>
            <MetricRow label="Last exec" value={`${formatDuration(last.duration_ms)} · ${last.model}`} color="text-fg-secondary" />
            <MetricRow label="Avg duration" value={formatDuration(avgDuration)} color="text-blue-400" />
            <MetricRow label={`Cost (last ${total})`} value={formatCost(totalCost)} color="text-status-warn" />
            {errorCount > 0 && (
              <MetricRow label="Errors" value={String(errorCount)} color="text-status-danger" />
            )}
          </>
        ) : (
          <p className="text-xs text-fg-faint italic">No executions yet</p>
        )}
        <div className="pt-1 border-t border-border">
          <p className="text-[10px] text-fg-faint">
            Settings → Observability for full dashboard
          </p>
        </div>
      </div>
    </Widget>
  )
}
