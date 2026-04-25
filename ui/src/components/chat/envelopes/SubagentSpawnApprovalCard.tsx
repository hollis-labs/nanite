import { ChevronDown, ChevronRight, ShieldAlert, ShieldCheck, ShieldX } from 'lucide-react'
import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { ResponseStatus } from '@/lib/envelope-response'
import type { EnvelopeResponder } from './EnvelopeRenderer'
import { Envelope, EnvelopeHeader, EnvelopeFooter } from './primitives/Envelope'
import { StatusPill, type StatusTone } from './primitives/StatusPill'

export type SubagentSpawnApprovalData = {
  run_id: string
  role: string
  prompt: string
  mode: 'sync' | 'async' | 'api' | 'interactive'
  parent_agent_id?: string
  timeout_seconds?: number
  inputs_json?: string
  risk_level?: 'low' | 'medium' | 'high'
}

interface SubagentSpawnApprovalCardProps {
  data: SubagentSpawnApprovalData
  onRespond?: EnvelopeResponder
}

const RISK_CONFIG: Record<
  'low' | 'medium' | 'high',
  { tone: StatusTone; icon: typeof ShieldCheck; label: string }
> = {
  low: { tone: 'success', icon: ShieldCheck, label: 'low risk' },
  medium: { tone: 'warning', icon: ShieldAlert, label: 'medium risk' },
  high: { tone: 'danger', icon: ShieldX, label: 'high risk' },
}

const PROMPT_TRUNCATE_LEN = 200

export function SubagentSpawnApprovalCard({ data, onRespond }: SubagentSpawnApprovalCardProps) {
  const [decided, setDecided] = useState<'approved' | 'rejected' | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [reason, setReason] = useState('')
  const [showFullPrompt, setShowFullPrompt] = useState(false)
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const risk = data.risk_level ?? 'medium'
  const riskConfig = RISK_CONFIG[risk]

  const submit = async (status: 'submitted' | 'cancelled') => {
    if (!onRespond) return
    setSubmitting(true)
    setError(null)
    try {
      if (status === ResponseStatus.Submitted) {
        await onRespond({ status: ResponseStatus.Submitted })
        setDecided('approved')
      } else {
        await onRespond({ status: ResponseStatus.Cancelled, data: { reason } })
        setDecided('rejected')
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to submit')
    } finally {
      setSubmitting(false)
    }
  }

  if (decided) {
    return (
      <Envelope accent={decided === 'approved' ? 'success' : undefined} muted>
        <div className="px-4 py-2.5 text-[13px] text-fg-secondary">
          Subagent run <code className="font-mono text-fg">{data.run_id}</code> — {decided}.
        </div>
      </Envelope>
    )
  }

  const isLongPrompt = data.prompt.length > PROMPT_TRUNCATE_LEN
  const displayedPrompt =
    isLongPrompt && !showFullPrompt
      ? `${data.prompt.slice(0, PROMPT_TRUNCATE_LEN)}…`
      : data.prompt
  const hasAdvanced = !!(data.parent_agent_id || data.inputs_json)

  return (
    <Envelope accent={risk === 'high' ? 'danger' : risk === 'medium' ? 'warning' : undefined}>
      <EnvelopeHeader
        icon={riskConfig.icon}
        label="Subagent spawn approval"
        tone={risk === 'high' ? 'danger' : risk === 'medium' ? 'warning' : 'success'}
        meta={<span className="font-mono">{data.run_id}</span>}
        action={<StatusPill tone={riskConfig.tone}>{riskConfig.label}</StatusPill>}
      />

      <div className="space-y-3 px-4 py-3">
        <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-[12px]">
          <MetaItem label="Role" value={data.role} mono />
          <MetaItem label="Mode" value={data.mode} mono />
          {data.timeout_seconds != null && (
            <MetaItem label="Timeout" value={`${data.timeout_seconds}s`} mono />
          )}
        </div>

        <div>
          <div className="mb-1 font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
            Prompt
          </div>
          <pre className="whitespace-pre-wrap break-words rounded-[6px] border border-border-subtle bg-surface px-3 py-2 font-mono text-[12px] leading-relaxed text-fg">
            {displayedPrompt}
          </pre>
          {isLongPrompt && (
            <button
              type="button"
              onClick={() => setShowFullPrompt((v) => !v)}
              className="mt-1 text-[12px] text-primary hover:underline"
            >
              {showFullPrompt ? 'Collapse' : 'Show full'}
            </button>
          )}
        </div>

        {hasAdvanced && (
          <div>
            <button
              type="button"
              onClick={() => setShowAdvanced((v) => !v)}
              className="flex items-center gap-1 font-mono text-[11px] font-semibold uppercase tracking-wide text-fg-muted hover:text-fg"
            >
              {showAdvanced ? (
                <ChevronDown className="h-3 w-3" />
              ) : (
                <ChevronRight className="h-3 w-3" />
              )}
              Advanced
            </button>
            {showAdvanced && (
              <div className="mt-2 space-y-2 text-[12px] text-fg-secondary">
                {data.parent_agent_id && (
                  <div>
                    <span className="text-fg-muted">Parent agent ID:</span>{' '}
                    <code className="font-mono text-fg">{data.parent_agent_id}</code>
                  </div>
                )}
                {data.inputs_json && (
                  <div>
                    <div className="mb-1 text-fg-muted">Inputs JSON:</div>
                    <pre className="whitespace-pre-wrap break-words rounded-[6px] border border-border-subtle bg-surface px-2.5 py-1.5 font-mono text-[11px] text-fg">
                      {data.inputs_json}
                    </pre>
                  </div>
                )}
              </div>
            )}
          </div>
        )}

        <textarea
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          rows={2}
          placeholder="Reason (optional, shown to the agent if you reject)"
          className="w-full resize-none rounded-[6px] border border-border-subtle bg-surface px-2.5 py-1.5 text-[13px] text-fg outline-none placeholder:text-fg-faint focus:border-primary"
        />

        {error && (
          <p className="text-[12px] text-danger" role="alert">
            {error}
          </p>
        )}
      </div>

      <EnvelopeFooter>
        <Button
          size="sm"
          onClick={() => void submit(ResponseStatus.Submitted)}
          disabled={submitting}
        >
          Approve
        </Button>
        <Button
          size="sm"
          variant="ghost"
          onClick={() => void submit(ResponseStatus.Cancelled)}
          disabled={submitting}
        >
          Reject
        </Button>
      </EnvelopeFooter>
    </Envelope>
  )
}

function MetaItem({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <span>
      <span className="font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
        {label}
      </span>{' '}
      <span className={mono ? 'font-mono text-fg' : 'text-fg'}>{value}</span>
    </span>
  )
}
