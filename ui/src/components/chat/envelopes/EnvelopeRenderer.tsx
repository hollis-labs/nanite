import { Suspense, Component, useSyncExternalStore, type ReactNode } from 'react'
import { AlertTriangle } from 'lucide-react'
import type { Envelope } from '@/lib/types'
import { getEnvelopeComponent } from '@/generated/plugin-envelopes'
import { subscribeRegistry, getRegistryVersion } from '@/lib/plugin-loader'
import { useSettings } from '@/hooks/useSettings'
import { ProposalCard } from './ProposalCard'
import { QuestionForm } from './QuestionForm'
import { ApprovalCard } from './ApprovalCard'

/** Error boundary scoped to a single envelope — prevents a broken plugin from crashing the chat. */
class EnvelopeErrorBoundary extends Component<
  { type: string; children: ReactNode },
  { error: Error | null }
> {
  state: { error: Error | null } = { error: null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  render() {
    if (this.state.error) {
      return (
        <div className="rounded-sm border border-danger/50 bg-danger/10 p-3">
          <div className="flex items-center gap-2 mb-1">
            <AlertTriangle className="w-3.5 h-3.5 text-danger shrink-0" />
            <span className="text-xs font-medium text-danger">
              Envelope failed: {this.props.type}
            </span>
          </div>
          <p className="text-[11px] text-danger/70 leading-relaxed">
            {this.state.error.message}
          </p>
        </div>
      )
    }
    return this.props.children
  }
}

interface EnvelopeRendererProps {
  envelope: Envelope
  onSendMessage?: (content: string) => void
}

export function EnvelopeRenderer({ envelope, onSendMessage }: EnvelopeRendererProps) {
  const { data: settings } = useSettings()
  const recoverMode = settings?.recover_mode ?? false

  // Re-render when dynamic plugins register new envelope components.
  useSyncExternalStore(subscribeRegistry, getRegistryVersion)

  // Single registry lookup — checks build-time first, then dynamic fallback.
  // Recover mode restricts to core-only entries.
  const PluginComponent = getEnvelopeComponent(envelope.type, recoverMode)
  if (PluginComponent && envelope.data) {
    return (
      <EnvelopeErrorBoundary type={envelope.type}>
        <Suspense fallback={<div className="animate-pulse p-4 text-sm text-fg-secondary">Loading...</div>}>
          <PluginComponent data={envelope.data} {...(onSendMessage ? { onSendMessage } : {})} />
        </Suspense>
      </EnvelopeErrorBoundary>
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
