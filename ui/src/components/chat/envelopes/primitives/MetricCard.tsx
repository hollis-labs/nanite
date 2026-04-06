import { TrendingUp, TrendingDown, Minus } from 'lucide-react'

interface MetricCardData {
  label: string
  value: string | number
  unit?: string
  trend?: 'up' | 'down' | 'flat'
  previous?: string | number
  description?: string
}

interface MetricCardProps {
  data: MetricCardData
}

const TREND_CONFIG = {
  up:   { icon: TrendingUp,   color: 'text-success' },
  down: { icon: TrendingDown, color: 'text-danger' },
  flat: { icon: Minus,        color: 'text-fg-muted' },
}

export function MetricCard({ data }: MetricCardProps) {
  const trend = data.trend ? TREND_CONFIG[data.trend] : null
  const TrendIcon = trend?.icon

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 p-4">
      <div className="text-xs text-fg-secondary mb-1">{data.label}</div>
      <div className="flex items-baseline gap-2">
        <span className="text-2xl font-semibold font-mono text-fg">
          {data.value}
        </span>
        {data.unit && (
          <span className="text-sm text-fg-muted">{data.unit}</span>
        )}
        {TrendIcon && trend && (
          <TrendIcon className={`w-4 h-4 ${trend.color}`} />
        )}
      </div>
      {data.previous != null && (
        <div className="text-xs text-fg-muted mt-1">
          Previously: {data.previous}{data.unit ? ` ${data.unit}` : ''}
        </div>
      )}
      {data.description && (
        <p className="text-xs text-fg-secondary mt-2">{data.description}</p>
      )}
    </div>
  )
}
