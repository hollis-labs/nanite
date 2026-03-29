import { BarChart3, Clock, ChevronRight } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { MessageContent } from '../MessageContent'

interface Metric {
  label: string
  value: string
  percent?: number
  color?: string
}

interface ReportAction {
  label: string
  action: string
  id?: string
}

interface ReportCardData {
  title: string
  generated_at?: string
  metrics: Metric[]
  summary?: string
  actions?: ReportAction[]
}

interface ReportCardProps {
  data: ReportCardData
  onSendMessage?: (content: string) => void
}

const METRIC_COLORS: Record<string, { bg: string; bar: string; text: string }> = {
  emerald: { bg: 'bg-success/10', bar: 'bg-success', text: 'text-success' },
  green:   { bg: 'bg-success/10', bar: 'bg-success', text: 'text-success' },
  amber:   { bg: 'bg-amber-500/10',   bar: 'bg-amber-500',   text: 'text-amber-400' },
  red:     { bg: 'bg-accent/10',      bar: 'bg-accent',      text: 'text-accent' },
  blue:    { bg: 'bg-success/10',     bar: 'bg-success',     text: 'text-success' },
  violet:  { bg: 'bg-violet-500/10',  bar: 'bg-violet-500',  text: 'text-violet-400' },
}

function MetricCard({ metric }: { metric: Metric }) {
  const fallback = { bg: 'bg-blue-500/10', bar: 'bg-blue-500', text: 'text-blue-400' }
  const colors = METRIC_COLORS[metric.color || 'blue'] ?? fallback

  return (
    <div className={`rounded-sm border border-border-subtle/50 ${colors.bg} p-3`}>
      <div className="text-xs text-fg-secondary mb-1">{metric.label}</div>
      <div className={`text-lg font-semibold font-mono ${colors.text}`}>
        {metric.value}
      </div>
      {metric.percent != null && (
        <div className="mt-2 h-1.5 rounded-full bg-surface overflow-hidden">
          <div
            className={`h-full rounded-full ${colors.bar} transition-all duration-700 ease-out`}
            style={{ width: `${Math.min(100, Math.max(0, metric.percent))}%` }}
          />
        </div>
      )}
    </div>
  )
}

function formatTimestamp(iso: string): string {
  try {
    const d = new Date(iso)
    return d.toLocaleString('en-US', {
      month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
    })
  } catch {
    return iso
  }
}

export function ReportCard({ data, onSendMessage }: ReportCardProps) {
  return (
    <div className="animate-in fade-in duration-300 space-y-3">
      {/* Header */}
      <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 overflow-hidden">
        <div className="flex items-center justify-between px-4 py-3 border-b border-border">
          <div className="flex items-center gap-2">
            <BarChart3 className="h-4 w-4 text-fg-secondary shrink-0" />
            <span className="text-sm font-medium text-fg">{data.title}</span>
          </div>
          {data.generated_at && (
            <div className="flex items-center gap-1 text-xs text-fg-muted">
              <Clock className="h-3 w-3" />
              <span>{formatTimestamp(data.generated_at)}</span>
            </div>
          )}
        </div>

        {/* Metrics grid */}
        <div className="grid grid-cols-2 gap-2 p-3">
          {data.metrics.map((metric, i) => (
            <MetricCard key={`metric-${i}`} metric={metric} />
          ))}
        </div>

        {/* Summary */}
        {data.summary && (
          <div className="px-4 pb-3 border-t border-border/50 pt-3">
            <MessageContent content={data.summary} role="assistant" />
          </div>
        )}

        {/* Action buttons */}
        {data.actions && data.actions.length > 0 && (
          <div className="flex items-center gap-2 px-4 pb-3">
            {data.actions.map((action, i) => (
              <Button
                key={`action-${i}`}
                size="sm"
                variant="outline"
                onClick={() => {
                  if (onSendMessage) {
                    const msg = action.id
                      ? `${action.action}:${action.id}`
                      : action.action
                    onSendMessage(msg)
                  }
                }}
                className="text-xs h-7"
              >
                <ChevronRight className="mr-1 h-3 w-3" />
                {action.label}
              </Button>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
