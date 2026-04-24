import { BarChart3, ChevronRight } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { MessageContent } from '../MessageContent'
import { Envelope, EnvelopeHeader, EnvelopeFooter } from './primitives/Envelope'
import { StatusPill } from './primitives/StatusPill'

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

// Metric palette — backend color names map to semantic tokens.
// `violet` stays as identity hue since there's no semantic slot.
const METRIC_TONE: Record<string, { bg: string; bar: string; text: string }> = {
  emerald: { bg: 'bg-success/10', bar: 'bg-success', text: 'text-success' },
  green:   { bg: 'bg-success/10', bar: 'bg-success', text: 'text-success' },
  amber:   { bg: 'bg-warning/10', bar: 'bg-warning', text: 'text-warning' },
  red:     { bg: 'bg-danger/10',  bar: 'bg-danger',  text: 'text-danger'  },
  blue:    { bg: 'bg-info/10',    bar: 'bg-info',    text: 'text-info'    },
  violet:  { bg: 'bg-violet-500/10', bar: 'bg-violet-500', text: 'text-violet-400' },
}

function MetricTile({ metric }: { metric: Metric }) {
  const fallback = METRIC_TONE.blue
  const t = METRIC_TONE[metric.color || 'blue'] ?? fallback

  return (
    <div className="p-3.5">
      <div className="font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
        {metric.label}
      </div>
      <div className={`mt-1.5 font-mono text-[24px] font-semibold leading-none tracking-tight ${t.text}`}>
        {metric.value}
      </div>
      {metric.percent != null && (
        <div className="mt-3 h-0.5 overflow-hidden rounded-full bg-surface">
          <div
            className={`h-full rounded-full ${t.bar} transition-all duration-700 ease-out`}
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
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    })
  } catch {
    return iso
  }
}

export function ReportCard({ data, onSendMessage }: ReportCardProps) {
  const hasActions = data.actions && data.actions.length > 0

  return (
    <Envelope className="animate-in fade-in duration-300">
      <EnvelopeHeader
        icon={BarChart3}
        label={data.title}
        meta={data.generated_at ? `Generated ${formatTimestamp(data.generated_at)}` : undefined}
      />

      {data.metrics.length > 0 && (
        <div className="grid grid-cols-2 divide-x divide-y divide-border-subtle">
          {data.metrics.map((metric, i) => (
            <MetricTile key={`metric-${i}`} metric={metric} />
          ))}
        </div>
      )}

      {(data.summary || hasActions) && (
        <EnvelopeFooter className="items-start">
          {data.summary && (
            <>
              <StatusPill tone="success">Report</StatusPill>
              <div className="min-w-0 flex-1 text-[12px] leading-relaxed text-fg-secondary">
                <MessageContent content={data.summary} role="assistant" />
              </div>
            </>
          )}

          {hasActions && (
            <>
              <div className="flex-1" />
              {data.actions!.map((action, i) => (
                <Button
                  key={`action-${i}`}
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    if (onSendMessage) {
                      const msg = action.id ? `${action.action}:${action.id}` : action.action
                      onSendMessage(msg)
                    }
                  }}
                >
                  {action.label}
                  <ChevronRight className="ml-1 h-3 w-3" />
                </Button>
              ))}
            </>
          )}
        </EnvelopeFooter>
      )}
    </Envelope>
  )
}
