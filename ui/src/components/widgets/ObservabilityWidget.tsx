import { Activity } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Widget, WidgetRow } from './Widget'
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
    <Widget id="observability" title="Observability" icon={Activity} defaultOpen={false}>
      <div className="flex flex-col gap-1.5">
        {last ? (
          <>
            <WidgetRow label="Last exec" mono>{formatDuration(last.duration_ms)} · {last.model}</WidgetRow>
            <WidgetRow label="Avg duration">
              <span className="font-mono text-[11px] text-info">{formatDuration(avgDuration)}</span>
            </WidgetRow>
            <WidgetRow label={`Cost (last ${total})`}>
              <span className="font-mono text-[11px] text-warning">{formatCost(totalCost)}</span>
            </WidgetRow>
            <WidgetRow label="Errors">
              <span className={`font-mono text-[11px] ${errorCount > 0 ? 'text-danger' : 'text-fg-muted'}`}>
                {errorCount}
              </span>
            </WidgetRow>
          </>
        ) : (
          <p className="text-[12px] text-fg-faint italic">No executions yet</p>
        )}
        <div className="pt-1.5 mt-0.5 border-t border-divider">
          <p className="text-[10px] text-fg-faint">Settings → Observability for full dashboard</p>
        </div>
      </div>
    </Widget>
  )
}
