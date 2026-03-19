import type { Envelope } from '@/lib/types'
import { ProposalCard } from './ProposalCard'
import { QuestionForm } from './QuestionForm'
import { ApprovalCard } from './ApprovalCard'
import { KBResultCard } from './KBResultCard'
import { TicketFormCard } from './TicketFormCard'
import { TicketConfirmationCard } from './TicketConfirmationCard'
import { ResolutionCaptureCard } from './ResolutionCaptureCard'

interface EnvelopeRendererProps {
  envelope: Envelope
  onSendMessage?: (content: string) => void
}

export function EnvelopeRenderer({ envelope, onSendMessage }: EnvelopeRendererProps) {
  // Custom plugin envelope types — render only the custom component
  if (envelope.type === 'kb-result' && envelope.data) {
    return <KBResultCard data={envelope.data as { results?: Array<{ id: string; title: string; category: string; severity: string; body?: string; source?: string; tags?: string[]; related?: string[]; rank?: number; confidence?: string }>; query: string }} {...(onSendMessage ? { onSendMessage } : {})} />
  }

  if (envelope.type === 'ticket-form' && envelope.data) {
    return <TicketFormCard data={envelope.data as { prefilled?: { title?: string; description?: string; category?: string; priority?: string; steps_tried?: string }; categories: string[] }} {...(onSendMessage ? { onSendMessage } : {})} />
  }

  if (envelope.type === 'ticket-confirmation' && envelope.data) {
    return <TicketConfirmationCard data={envelope.data as { ticket: { id: string; title: string; description: string; category: string; priority: string; status: string; requester: string; routing: string; created_at: string } }} />
  }

  if (envelope.type === 'resolution-capture' && envelope.data) {
    return <ResolutionCaptureCard data={envelope.data as { ticket_id?: string; issue_summary?: string; categories: string[] }} {...(onSendMessage ? { onSendMessage } : {})} />
  }

  // Default envelope rendering — proposals, questions, approval
  return (
    <div className="space-y-3">
      {envelope.proposals?.map((proposal, i) => (
        <ProposalCard key={`proposal-${i}`} proposal={proposal} />
      ))}

      {envelope.questions && envelope.questions.length > 0 && (
        <QuestionForm questions={envelope.questions} {...(onSendMessage ? { onSubmit: onSendMessage } : {})} />
      )}

      {envelope.approval && (
        <ApprovalCard approval={envelope.approval} />
      )}
    </div>
  )
}
