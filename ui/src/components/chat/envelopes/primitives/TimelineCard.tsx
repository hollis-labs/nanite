import { Clock } from 'lucide-react'
import { Envelope, EnvelopeHeader, EnvelopeBody } from './Envelope'

interface TimelineEvent {
  timestamp: string
  label: string
  description?: string
  icon?: string
  status?: 'completed' | 'active' | 'pending'
}

interface TimelineCardData {
  title?: string
  events: TimelineEvent[]
}

interface TimelineCardProps {
  data: TimelineCardData
}

const STATUS_DOT: Record<string, string> = {
  completed: 'bg-success',
  active:    'bg-info',
  pending:   'bg-fg-muted/40',
}

function formatTimestamp(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) {
    return iso
  }
  return d.toLocaleString('en-US', {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  })
}

export function TimelineCard({ data }: TimelineCardProps) {
  // `events` is required by the type, but a partially-loaded envelope can
  // arrive without it — normalize so the card degrades gracefully.
  const events = data.events ?? []
  return (
    <Envelope>
      <EnvelopeHeader
        icon={Clock}
        label="Timeline"
        meta={`${events.length} event${events.length === 1 ? '' : 's'}`}
      />
      <EnvelopeBody title={data.title}>
        <div className="relative">
          {events.map((event, i) => {
            const status = event.status || 'pending'
            const dotColor = STATUS_DOT[status] ?? STATUS_DOT.pending
            const isLast = i === events.length - 1

            return (
              <div key={`event-${i}`} className="relative flex gap-3 pb-4 last:pb-0">
                {/* Vertical rail */}
                {!isLast && (
                  <div className="absolute bottom-0 left-[5px] top-3 w-px bg-border-subtle" />
                )}
                {/* Dot */}
                <div
                  className={`mt-1 h-[11px] w-[11px] shrink-0 rounded-full ring-2 ring-bg-elevated ${dotColor}`}
                />
                {/* Content */}
                <div className="min-w-0 flex-1">
                  <div className="flex items-baseline gap-2">
                    <span className="text-[13px] font-medium text-fg">{event.label}</span>
                    <span className="font-mono text-[11px] text-fg-muted">
                      {formatTimestamp(event.timestamp)}
                    </span>
                  </div>
                  {event.description && (
                    <p className="mt-0.5 text-[12px] text-fg-secondary">{event.description}</p>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      </EnvelopeBody>
    </Envelope>
  )
}
