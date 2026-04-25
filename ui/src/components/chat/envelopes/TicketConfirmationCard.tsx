import { CheckCircle, Download, Info } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Envelope, EnvelopeHeader, EnvelopeFooter } from './primitives/Envelope'
import { StatusPill, type StatusTone } from './primitives/StatusPill'

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

const PRIORITY_TONE: Record<string, StatusTone> = {
  low: 'success',
  medium: 'warning',
  high: 'danger',
  critical: 'danger',
}

function DetailField({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-1 font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
        {label}
      </div>
      <div className="text-[13px] text-fg">{children}</div>
    </div>
  )
}

export function TicketConfirmationCard({ data }: TicketConfirmationCardProps) {
  const { ticket } = data
  const prioTone = PRIORITY_TONE[ticket.priority] ?? 'warning'

  const handleDownload = () => {
    window.open(
      `/api/plugins/ui/support-ticket-download?ticket_id=${encodeURIComponent(ticket.id)}`,
      '_blank',
    )
  }

  return (
    <Envelope accent="success">
      <EnvelopeHeader
        icon={CheckCircle}
        label="Ticket created"
        tone="success"
        meta={<span className="font-mono">{ticket.id}</span>}
        action={<StatusPill tone="success">{ticket.status}</StatusPill>}
      />

      <div className="px-4 py-3 space-y-2">
        <h3 className="text-[14px] font-semibold leading-snug text-fg">
          {ticket.title}
        </h3>

        <div className="grid grid-cols-3 gap-3">
          <DetailField label="Category">
            <span className="capitalize">{ticket.category}</span>
          </DetailField>
          <DetailField label="Priority">
            <StatusPill tone={prioTone}>{ticket.priority}</StatusPill>
          </DetailField>
          <DetailField label="Routing">{ticket.routing}</DetailField>
        </div>

        <DetailField label="Description">
          <p className="text-[13px] leading-relaxed text-fg-secondary">{ticket.description}</p>
        </DetailField>

        <div className="flex items-center gap-3 font-mono text-[11px] text-fg-muted">
          <span>Requester: {ticket.requester || 'You'}</span>
          <span>·</span>
          <span>{new Date(ticket.created_at).toLocaleString()}</span>
        </div>

        <div className="flex items-start gap-2 rounded-[6px] border border-border-subtle bg-surface px-3 py-2">
          <Info className="mt-0.5 h-3 w-3 shrink-0 text-fg-muted" />
          <span className="text-[11px] leading-relaxed text-fg-muted">
            In production, this ticket would be automatically created in BMC Helix ITSM
          </span>
        </div>
      </div>

      <EnvelopeFooter>
        <Button size="sm" variant="outline" onClick={handleDownload}>
          <Download className="h-3 w-3" />
          Download ticket
        </Button>
      </EnvelopeFooter>
    </Envelope>
  )
}
