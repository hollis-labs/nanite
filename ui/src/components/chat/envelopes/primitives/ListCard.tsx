import { List, ChevronRight } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Envelope, EnvelopeHeader, EnvelopeBody } from './Envelope'

interface ListItem {
  label: string
  description?: string
  icon?: string
  action?: { label: string; type: string }
}

interface ListCardData {
  title?: string
  items: ListItem[]
  ordered?: boolean
}

interface ListCardProps {
  data: ListCardData
  onSendMessage?: (content: string) => void
}

export function ListCard({ data, onSendMessage }: ListCardProps) {
  // `items` is required by the type, but a partially-loaded envelope can
  // arrive without it — normalize so the card degrades gracefully.
  const items = data.items ?? []
  return (
    <Envelope>
      <EnvelopeHeader
        icon={List}
        label={data.ordered ? 'Ordered list' : 'List'}
        meta={`${items.length} item${items.length === 1 ? '' : 's'}`}
      />
      <EnvelopeBody title={data.title}>
        <div className="space-y-2">
          {items.map((item, i) => (
            <div key={`item-${i}`} className="flex items-start gap-3">
              <span className="mt-0.5 w-5 shrink-0 text-right font-mono text-[11px] text-fg-muted">
                {data.ordered ? `${i + 1}.` : '\u2022'}
              </span>
              <div className="min-w-0 flex-1">
                <span className="text-[13px] text-fg">{item.label}</span>
                {item.description && (
                  <p className="mt-0.5 text-[12px] text-fg-muted">{item.description}</p>
                )}
              </div>
              {item.action && onSendMessage && (
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-6 shrink-0 px-2 text-xs"
                  onClick={() => onSendMessage(item.action!.type)}
                >
                  {item.action.label}
                  <ChevronRight className="ml-1 h-3 w-3" />
                </Button>
              )}
            </div>
          ))}
        </div>
      </EnvelopeBody>
    </Envelope>
  )
}
