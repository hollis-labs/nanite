import { useState } from 'react'
import { Check, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import type { Proposal } from '@/lib/types'

interface ProposalCardProps {
  proposal: Proposal
}

export function ProposalCard({ proposal }: ProposalCardProps) {
  const [state, setState] = useState<'pending' | 'applied' | 'dismissed'>('pending')
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
      <div className="rounded-sm border border-border bg-bg-elevated/30 p-3 opacity-50">
        <div className="flex items-center gap-2 text-xs text-fg-muted">
          <X className="w-3.5 h-3.5" />
          <span>Dismissed: {proposal.type}</span>
        </div>
      </div>
    )
  }

  const isApplied = state === 'applied'

  return (
    <div className={`rounded-sm border p-4 ${
      isApplied
        ? 'border-success/30 bg-success/5'
        : 'border-border-subtle bg-bg-elevated/50'
    }`}>
      {/* Header */}
      <div className="flex items-center justify-between mb-3">
        <h4 className="text-sm font-medium text-fg">{proposal.type}</h4>
        {isApplied && (
          <div className="flex items-center gap-1 text-success text-xs">
            <Check className="w-3.5 h-3.5" />
            <span>Applied</span>
          </div>
        )}
      </div>

      {/* Fields */}
      <div className="space-y-3">
        {Object.entries(fields).map(([key, value]) => {
          const schema = proposal.schema?.[key]
          const fieldType = schema?.type || 'text'
          const label = schema?.label || key

          return (
            <div key={key}>
              <label className="block text-xs font-medium text-fg-secondary mb-1">
                {label}
                {schema?.required && <span className="text-red-400 ml-0.5">*</span>}
              </label>
              {fieldType === 'textarea' ? (
                <textarea
                  value={String(value ?? '')}
                  onChange={(e) => setFields((f) => ({ ...f, [key]: e.target.value }))}
                  disabled={isApplied}
                  rows={3}
                  className="w-full bg-surface border border-border-subtle rounded-md px-2.5 py-1.5 text-sm text-fg outline-none focus:border-accent disabled:opacity-50 disabled:cursor-not-allowed resize-none"
                />
              ) : fieldType === 'select' && schema?.options ? (
                <select
                  value={String(value ?? '')}
                  onChange={(e) => setFields((f) => ({ ...f, [key]: e.target.value }))}
                  disabled={isApplied}
                  className="w-full bg-surface border border-border-subtle rounded-md px-2.5 py-1.5 text-sm text-fg outline-none focus:border-accent disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  {schema.options.map((opt) => (
                    <option key={opt} value={opt}>{opt}</option>
                  ))}
                </select>
              ) : (
                <input
                  type={fieldType === 'number' ? 'number' : 'text'}
                  value={String(value ?? '')}
                  onChange={(e) => setFields((f) => ({ ...f, [key]: e.target.value }))}
                  disabled={isApplied}
                  className="w-full bg-surface border border-border-subtle rounded-md px-2.5 py-1.5 text-sm text-fg outline-none focus:border-accent disabled:opacity-50 disabled:cursor-not-allowed"
                />
              )}
            </div>
          )
        })}
      </div>

      {/* Actions */}
      {!isApplied && (
        <div className="flex items-center gap-2 mt-4">
          <Button
            size="sm"
            className="bg-success hover:bg-success/80 text-white text-xs px-3 py-1 h-7"
            onClick={handleApply}
          >
            Apply
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="text-fg-secondary hover:text-fg text-xs px-3 py-1 h-7"
            onClick={handleDismiss}
          >
            Dismiss
          </Button>
        </div>
      )}
    </div>
  )
}
