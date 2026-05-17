import { useState } from 'react'
import { Check, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { ResponseStatus } from '@/lib/envelope-response'
import type { Proposal, Envelope as EnvelopeType } from '@/lib/types'
import { Envelope, EnvelopeHeader, EnvelopeFooter } from './primitives/Envelope'
import { StatusPill } from './primitives/StatusPill'

interface ProposalCardProps {
  envelope: EnvelopeType
}

const FIELD_INPUT =
  'w-full rounded-[6px] border border-border-subtle bg-surface px-2.5 py-1.5 text-[13px] text-fg outline-none transition-colors focus:border-primary disabled:cursor-not-allowed disabled:opacity-50'

/**
 * Hydrate the proposal state from a persisted response so the card does not
 * reset to 'pending' after a page reload. CW-20260517-0006.
 */
function hydrateState(
  prior: EnvelopeType['prior_response'],
): 'pending' | 'applied' | 'dismissed' {
  if (!prior) return 'pending'
  return prior.status === ResponseStatus.Cancelled ? 'dismissed' : 'applied'
}

export function ProposalCard({ envelope }: ProposalCardProps) {
  const proposal: Proposal =
    (envelope.data as Proposal | undefined) ??
    envelope.proposals?.[0] ??
    ({ type: '', payload: {} } as Proposal)
  const [state, setState] = useState<'pending' | 'applied' | 'dismissed'>(() =>
    hydrateState(envelope.prior_response),
  )
  const [fields, setFields] = useState<Record<string, unknown>>({ ...proposal.payload })

  const handleApply = () => {
    console.log('[ProposalCard] Applied:', { type: proposal.type, payload: fields })
    setState('applied')
  }

  const handleDismiss = () => {
    console.log('[ProposalCard] Dismissed:', proposal.type)
    setState('dismissed')
  }

  if (state === 'dismissed') {
    return (
      <Envelope muted>
        <div className="flex items-center gap-2 px-4 py-2.5 text-fg-muted">
          <X className="h-3.5 w-3.5" />
          <span className="text-[13px]">Dismissed: {proposal.type}</span>
        </div>
      </Envelope>
    )
  }

  const isApplied = state === 'applied'

  return (
    <Envelope accent={isApplied ? 'success' : undefined}>
      <EnvelopeHeader
        label="Proposal"
        meta={<span className="font-mono normal-case">{proposal.type}</span>}
        action={
          isApplied ? (
            <StatusPill tone="success">
              <Check className="h-3 w-3" />
              Applied
            </StatusPill>
          ) : (
            <StatusPill tone="info">Review</StatusPill>
          )
        }
      />

      <div className="space-y-3 px-4 py-3">
        {Object.entries(fields).map(([key, value]) => {
          const schema = proposal.schema?.[key]
          const fieldType = schema?.type || 'text'
          const label = schema?.label || key

          return (
            <div key={key}>
              <label className="mb-1 block font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
                {label}
                {schema?.required && <span className="ml-0.5 text-danger">*</span>}
              </label>
              {fieldType === 'textarea' ? (
                <textarea
                  value={String(value ?? '')}
                  onChange={(e) => setFields((f) => ({ ...f, [key]: e.target.value }))}
                  disabled={isApplied}
                  rows={3}
                  className={`${FIELD_INPUT} resize-none`}
                />
              ) : fieldType === 'select' && schema?.options ? (
                <select
                  value={String(value ?? '')}
                  onChange={(e) => setFields((f) => ({ ...f, [key]: e.target.value }))}
                  disabled={isApplied}
                  className={FIELD_INPUT}
                >
                  {schema.options.map((opt) => (
                    <option key={opt} value={opt}>
                      {opt}
                    </option>
                  ))}
                </select>
              ) : (
                <input
                  type={fieldType === 'number' ? 'number' : 'text'}
                  value={String(value ?? '')}
                  onChange={(e) => setFields((f) => ({ ...f, [key]: e.target.value }))}
                  disabled={isApplied}
                  className={FIELD_INPUT}
                />
              )}
            </div>
          )
        })}
      </div>

      {!isApplied && (
        <EnvelopeFooter>
          <Button size="sm" onClick={handleApply}>
            <Check className="h-3 w-3" />
            Apply
          </Button>
          <Button variant="ghost" size="sm" onClick={handleDismiss}>
            Dismiss
          </Button>
        </EnvelopeFooter>
      )}
    </Envelope>
  )
}
