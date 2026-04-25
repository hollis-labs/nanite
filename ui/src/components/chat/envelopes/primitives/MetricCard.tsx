import { TrendingUp, TrendingDown, Minus, Activity } from 'lucide-react'
import { Envelope, EnvelopeHeader, EnvelopeBody } from './Envelope'

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
  up:   { icon: TrendingUp,   color: 'text-success'  },
  down: { icon: TrendingDown, color: 'text-danger'   },
  flat: { icon: Minus,        color: 'text-fg-muted' },
}

export function MetricCard({ data }: MetricCardProps) {
  const trend = data.trend ? TREND_CONFIG[data.trend] : null
  const TrendIcon = trend?.icon

  return (
    <Envelope>
      <EnvelopeHeader icon={Activity} label="Metric" />
      <EnvelopeBody>
        <div className="font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
          {data.label}
        </div>
        <div className="mt-1 flex items-baseline gap-2">
          <span className="font-mono text-[24px] font-semibold leading-none text-fg">
            {data.value}
          </span>
          {data.unit && (
            <span className="text-[13px] text-fg-muted">{data.unit}</span>
          )}
          {TrendIcon && trend && (
            <TrendIcon className={`h-4 w-4 ${trend.color}`} />
          )}
        </div>
        {data.previous != null && (
          <div className="mt-1 text-[12px] text-fg-muted">
            Previously: {data.previous}{data.unit ? ` ${data.unit}` : ''}
          </div>
        )}
        {data.description && (
          <p className="mt-2 text-[12px] text-fg-secondary">{data.description}</p>
        )}
      </EnvelopeBody>
    </Envelope>
  )
}
