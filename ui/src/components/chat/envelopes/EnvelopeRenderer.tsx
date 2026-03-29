import { Suspense } from 'react'
import type { Envelope } from '@/lib/types'
import { getEnvelopeComponent } from '@/generated/plugin-envelopes'
import { useSettings } from '@/hooks/useSettings'
import { ProposalCard } from './ProposalCard'
import { QuestionForm } from './QuestionForm'
import { ApprovalCard } from './ApprovalCard'

interface EnvelopeRendererProps {
  envelope: Envelope
  onSendMessage?: (content: string) => void
}

export function EnvelopeRenderer({ envelope, onSendMessage }: EnvelopeRendererProps) {
  const { data: settings } = useSettings()
  const recoverMode = settings?.recover_mode ?? false

  // Single registry lookup — recover mode filters to core-only via source field
  const PluginComponent = getEnvelopeComponent(envelope.type, recoverMode)
  if (PluginComponent && envelope.data) {
    return (
      <Suspense fallback={<div className="animate-pulse p-4 text-sm text-fg-secondary">Loading...</div>}>
        <PluginComponent data={envelope.data} {...(onSendMessage ? { onSendMessage } : {})} />
      </Suspense>
    )
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
