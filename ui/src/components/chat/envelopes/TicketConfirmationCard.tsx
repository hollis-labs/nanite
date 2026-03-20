import { CheckCircle, Download, Info } from 'lucide-react'
import { Button } from '@/components/ui/Button'

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
  low: { bg: 'bg-green-500/15', text: 'text-green-400', border: 'border-green-500/25' },
  medium: { bg: 'bg-amber-500/15', text: 'text-amber-400', border: 'border-amber-500/25' },
  high: { bg: 'bg-red-500/15', text: 'text-red-400', border: 'border-red-500/25' },
  critical: { bg: 'bg-red-600/20', text: 'text-red-300', border: 'border-red-600/30' },
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
    <div className="rounded-lg border border-green-500/30 bg-zinc-900/50 overflow-hidden">
      {/* Header */}
      <div className="flex items-center justify-between bg-green-500/5 px-4 py-3 border-b border-green-500/20">
        <div className="flex items-center gap-2">
          <CheckCircle className="h-4 w-4 text-green-400" />
          <span className="text-sm font-medium text-zinc-200">{ticket.id}</span>
          <span className="text-sm text-zinc-400">&mdash;</span>
          <span className="text-sm text-zinc-300 truncate">{ticket.title}</span>
        </div>
        <span className="inline-block rounded-full bg-green-500/15 border border-green-500/25 px-2 py-0.5 text-xs text-green-400 capitalize">
          {ticket.status}
        </span>
      </div>

      {/* Details grid */}
      <div className="px-4 py-3 space-y-3">
        <div className="grid grid-cols-3 gap-3">
          {/* Category */}
          <div>
            <span className="block text-xs font-medium text-zinc-400 mb-0.5">Category</span>
            <span className="text-sm text-zinc-200 capitalize">{ticket.category}</span>
          </div>

          {/* Priority */}
          <div>
            <span className="block text-xs font-medium text-zinc-400 mb-0.5">Priority</span>
            <span
              className={`inline-block rounded-full px-2 py-0.5 text-xs capitalize border ${prioStyle?.bg} ${prioStyle?.text} ${prioStyle?.border}`}
            >
              {ticket.priority}
            </span>
          </div>

          {/* Routing */}
          <div>
            <span className="block text-xs font-medium text-zinc-400 mb-0.5">Routing</span>
            <span className="text-sm text-zinc-200">{ticket.routing}</span>
          </div>
        </div>

        {/* Description */}
        <div>
          <span className="block text-xs font-medium text-zinc-400 mb-0.5">Description</span>
          <p className="text-sm text-zinc-300">{ticket.description}</p>
        </div>

        {/* Requester & Date */}
        <div className="flex items-center gap-4 text-xs text-zinc-500">
          <span>Requester: {ticket.requester || 'You'}</span>
          <span>Created: {new Date(ticket.created_at).toLocaleString()}</span>
        </div>

        {/* Actions */}
        <div className="flex items-center gap-3 pt-1">
          <Button
            size="sm"
            className="bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-xs px-3 py-1 h-7"
            onClick={handleDownload}
          >
            <Download className="mr-1.5 h-3 w-3" />
            Download Ticket
          </Button>
        </div>

        {/* Production note */}
        <div className="flex items-start gap-2 rounded-md border border-zinc-700 bg-zinc-800/50 px-3 py-2">
          <Info className="mt-0.5 h-3 w-3 shrink-0 text-zinc-500" />
          <span className="text-xs text-zinc-500">
            In production, this ticket would be automatically created in BMC Helix ITSM
          </span>
        </div>
      </div>
    </div>
  )
}
