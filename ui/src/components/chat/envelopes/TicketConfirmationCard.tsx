import { CheckCircle, Download, Info } from 'lucide-react'
import { Button } from '@/components/ui/button'

interface TicketConfirmationData {
  ticket: {
    id: string
    title: string
    description: string
    category: string
    priority: string
    status: string
    requester: string
    routing: string
    created_at: string
  }
}

interface TicketConfirmationCardProps {
  data: TicketConfirmationData
}

const PRIORITY_STYLES: Record<string, { bg: string; text: string; border: string }> = {
  low: { bg: 'bg-success/15', text: 'text-success', border: 'border-success/25' },
  medium: { bg: 'bg-warning/15', text: 'text-warning', border: 'border-warning/25' },
  high: { bg: 'bg-primary/15', text: 'text-primary', border: 'border-primary/25' },
  critical: { bg: 'bg-primary/20', text: 'text-primary', border: 'border-primary/30' },
}

export function TicketConfirmationCard({ data }: TicketConfirmationCardProps) {
  const { ticket } = data
  const prioStyle = PRIORITY_STYLES[ticket.priority] ?? PRIORITY_STYLES['medium']

  const handleDownload = () => {
    window.open(
      `/api/plugins/ui/support-ticket-download?ticket_id=${encodeURIComponent(ticket.id)}`,
      '_blank'
    )
  }

  return (
    <div className="rounded-sm border border-success/30 bg-bg-elevated/50 overflow-hidden">
      {/* Header */}
      <div className="flex items-center justify-between bg-success/5 px-4 py-3 border-b border-success/20">
        <div className="flex items-center gap-2">
          <CheckCircle className="h-4 w-4 text-success" />
          <span className="text-sm font-medium text-fg">{ticket.id}</span>
          <span className="text-sm text-fg-secondary">&mdash;</span>
          <span className="text-sm text-fg-secondary truncate">{ticket.title}</span>
        </div>
        <span className="inline-block rounded-full bg-success/15 border border-success/25 px-2 py-0.5 text-xs text-success capitalize">
          {ticket.status}
        </span>
      </div>

      {/* Details grid */}
      <div className="px-4 py-3 space-y-3">
        <div className="grid grid-cols-3 gap-3">
          {/* Category */}
          <div>
            <span className="block text-xs font-medium text-fg-secondary mb-0.5">Category</span>
            <span className="text-sm text-fg capitalize">{ticket.category}</span>
          </div>

          {/* Priority */}
          <div>
            <span className="block text-xs font-medium text-fg-secondary mb-0.5">Priority</span>
            <span
              className={`inline-block rounded-full px-2 py-0.5 text-xs capitalize border ${prioStyle?.bg} ${prioStyle?.text} ${prioStyle?.border}`}
            >
              {ticket.priority}
            </span>
          </div>

          {/* Routing */}
          <div>
            <span className="block text-xs font-medium text-fg-secondary mb-0.5">Routing</span>
            <span className="text-sm text-fg">{ticket.routing}</span>
          </div>
        </div>

        {/* Description */}
        <div>
          <span className="block text-xs font-medium text-fg-secondary mb-0.5">Description</span>
          <p className="text-sm text-fg-secondary">{ticket.description}</p>
        </div>

        {/* Requester & Date */}
        <div className="flex items-center gap-4 text-xs text-fg-muted">
          <span>Requester: {ticket.requester || 'You'}</span>
          <span>Created: {new Date(ticket.created_at).toLocaleString()}</span>
        </div>

        {/* Actions */}
        <div className="flex items-center gap-3 pt-1">
          <Button
            size="sm"
            className="bg-surface hover:bg-surface-hover text-fg text-xs px-3 py-1 h-7"
            onClick={handleDownload}
          >
            <Download className="mr-1.5 h-3 w-3" />
            Download Ticket
          </Button>
        </div>

        {/* Production note */}
        <div className="flex items-start gap-2 rounded-sm border border-border-subtle bg-surface/50 px-3 py-2">
          <Info className="mt-0.5 h-3 w-3 shrink-0 text-fg-muted" />
          <span className="text-xs text-fg-muted">
            In production, this ticket would be automatically created in BMC Helix ITSM
          </span>
        </div>
      </div>
    </div>
  )
}
