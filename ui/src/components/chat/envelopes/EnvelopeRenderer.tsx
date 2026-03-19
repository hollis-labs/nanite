import { Suspense } from 'react'
import type { Envelope } from '@/lib/types'
import { PLUGIN_ENVELOPE_REGISTRY } from '@/generated/plugin-envelopes'
import { ProposalCard } from './ProposalCard'
import { QuestionForm } from './QuestionForm'
import { ApprovalCard } from './ApprovalCard'

interface EnvelopeRendererProps {
  envelope: Envelope
  onSendMessage?: (content: string) => void
}

export function EnvelopeRenderer({ envelope, onSendMessage }: EnvelopeRendererProps) {
  // Plugin envelope types — resolved from the generated registry
  const PluginComponent = PLUGIN_ENVELOPE_REGISTRY[envelope.type]
  if (PluginComponent && envelope.data) {
    return (
      <Suspense fallback={<div className="animate-pulse p-4 text-sm text-zinc-400">Loading...</div>}>
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
