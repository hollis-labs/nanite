import { ChevronRight } from 'lucide-react'
import { Button } from '@/components/ui/button'

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
  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 p-4">
      {data.title && (
        <h4 className="text-sm font-medium text-fg mb-3">{data.title}</h4>
      )}
      <div className="space-y-2">
        {data.items.map((item, i) => (
          <div key={`item-${i}`} className="flex items-start gap-3">
            <span className="text-xs text-fg-muted mt-0.5 w-5 shrink-0 text-right font-mono">
              {data.ordered ? `${i + 1}.` : '\u2022'}
            </span>
            <div className="flex-1 min-w-0">
              <span className="text-sm text-fg">{item.label}</span>
              {item.description && (
                <p className="text-xs text-fg-muted mt-0.5">{item.description}</p>
              )}
            </div>
            {item.action && onSendMessage && (
              <Button
                size="sm"
                variant="outline"
                className="text-xs h-6 px-2 shrink-0"
                onClick={() => onSendMessage(item.action!.type)}
              >
                <ChevronRight className="mr-1 h-3 w-3" />
                {item.action.label}
              </Button>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
