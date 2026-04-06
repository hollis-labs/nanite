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
  try {
    const d = new Date(iso)
    return d.toLocaleString('en-US', {
      month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
    })
  } catch {
    return iso
  }
}

export function TimelineCard({ data }: TimelineCardProps) {
  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 p-4">
      {data.title && (
        <h4 className="text-sm font-medium text-fg mb-3">{data.title}</h4>
      )}
      <div className="relative">
        {data.events.map((event, i) => {
          const status = event.status || 'pending'
          const dotColor = STATUS_DOT[status] ?? STATUS_DOT.pending
          const isLast = i === data.events.length - 1

          return (
            <div key={`event-${i}`} className="relative flex gap-3 pb-4 last:pb-0">
              {/* Vertical line */}
              {!isLast && (
                <div className="absolute left-[5px] top-3 bottom-0 w-px bg-border" />
              )}
              {/* Dot */}
              <div className={`w-[11px] h-[11px] rounded-full mt-1 shrink-0 ${dotColor} ring-2 ring-bg-elevated/50`} />
              {/* Content */}
              <div className="min-w-0 flex-1">
                <div className="flex items-baseline gap-2">
                  <span className="text-sm font-medium text-fg">{event.label}</span>
                  <span className="text-[11px] text-fg-muted">{formatTimestamp(event.timestamp)}</span>
                </div>
                {event.description && (
                  <p className="text-xs text-fg-secondary mt-0.5">{event.description}</p>
                )}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
