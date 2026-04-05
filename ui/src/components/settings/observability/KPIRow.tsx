import { Activity, Clock, DollarSign, AlertTriangle } from 'lucide-react'
import { StatCard } from './StatCard'
import type { ExecutionMetrics } from '@/lib/types'

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

interface KPIRowProps {
  data: ExecutionMetrics[]
}

export function KPIRow({ data }: KPIRowProps) {
  const total = data.length
  const avgDuration = total > 0
    ? data.reduce((sum, m) => sum + m.duration_ms, 0) / total
    : 0
  const totalCost = data.reduce((sum, m) => sum + m.estimated_cost_usd, 0)
  const errorCount = data.filter((m) => m.error !== '').length
  const errorRate = total > 0 ? (errorCount / total) * 100 : 0

  return (
    <div className="grid grid-cols-4 gap-3">
      <StatCard
        label="Executions"
        value={String(total)}
        icon={Activity}
        color="text-fg"
      />
      <StatCard
        label="Avg Duration"
        value={formatDuration(avgDuration)}
        icon={Clock}
        color="text-info"
      />
      <StatCard
        label="Total Cost"
        value={formatCost(totalCost)}
        icon={DollarSign}
        color="text-warning"
      />
      <StatCard
        label="Error Rate"
        value={`${errorRate.toFixed(1)}%`}
        subValue={errorCount > 0 ? `${errorCount} errors` : undefined}
        icon={AlertTriangle}
        color={errorRate > 5 ? 'text-danger' : 'text-success'}
      />
    </div>
  )
}
