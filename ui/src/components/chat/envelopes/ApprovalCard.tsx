import { ChevronDown, ChevronRight, ShieldAlert, ShieldCheck, ShieldX, type LucideIcon } from 'lucide-react'
import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { ResponseStatus } from '@/lib/envelope-response'
import type { EnvelopeApprovalRequest, Envelope as EnvelopeType } from '@/lib/types'
import { Envelope, EnvelopeHeader, EnvelopeBody, EnvelopeFooter } from './primitives/Envelope'
import { StatusPill, type StatusTone } from './primitives/StatusPill'
import type { EnvelopeResponder } from './EnvelopeRenderer'

interface ApprovalCardProps {
  envelope: EnvelopeType
  onRespond?: EnvelopeResponder
}

/**
 * `subagent-spawn-approval`'s wire type. Phase 6 (TASKS/phase-6/03) folds
 * this card's UI into `ApprovalCard` as a composed second flavor rather
 * than the old standalone `SubagentSpawnApprovalCard.tsx` sibling — see
 * docs/engineering/architecture/08-cards.md: "subagent-spawn-approval ->
 * approval-card + subagent-specific data via the existing props
 * discriminator mechanism." The manifest keeps the `subagent-spawn-approval`
 * wire type (backend emitter / response-handler registration / the
 * sessions.go recovery-rehydration gate all key off this literal string
 * unchanged) but now points its `component`/`export` at this file. Both
 * flavors ride through EnvelopeRenderer with `props: "envelope"`
 * (CORE_OVERRIDES in scripts/generate-plugin-imports.mjs) so this
 * component always receives the full envelope wrapper, not just `data` —
 * required to read `prior_response` for post-reload hydration.
 */
const SUBAGENT_SPAWN_APPROVAL_TYPE = 'subagent-spawn-approval'

/** `subagent-spawn-approval`'s `data` shape (the subagent-specific flavor). */
export type SubagentApprovalData = {
  run_id: string
  role: string
  prompt: string
  mode: 'sync' | 'async' | 'api' | 'interactive'
  parent_agent_id?: string
  timeout_seconds?: number
  inputs_json?: string
  risk_level?: 'low' | 'medium' | 'high'
}

const PROMPT_TRUNCATE_LEN = 200

/**
 * Hydrate the decision from a persisted response. The backend injects
 * `prior_response` into the envelope when the card was already answered.
 * The generic flavor's `respond()` always posts status=Submitted and
 * encodes the decision in `data.approved`; the subagent flavor posts
 * status=Submitted on approve and status=Cancelled (with a `reason`) on
 * reject — both read correctly through the same status-first check.
 * CW-20260517-0006.
 */
function hydrateDecision(prior: EnvelopeType['prior_response']): 'pending' | 'approved' | 'rejected' {
  if (!prior) return 'pending'
  if (prior.status === ResponseStatus.Cancelled) return 'rejected'
  return prior.data?.approved === false ? 'rejected' : 'approved'
}

const RISK_META: Record<
  'low' | 'medium' | 'high',
  { tone: StatusTone; icon: LucideIcon; label: string }
> = {
  low:    { tone: 'success', icon: ShieldCheck, label: 'Low risk' },
  medium: { tone: 'warning', icon: ShieldAlert, label: 'Medium risk' },
  high:   { tone: 'danger',  icon: ShieldX,     label: 'High risk' },
}

export function ApprovalCard({ envelope, onRespond }: ApprovalCardProps) {
  const isSubagentSpawn = envelope.type === SUBAGENT_SPAWN_APPROVAL_TYPE

  const approval: EnvelopeApprovalRequest =
    (envelope.approval ?? (envelope.data as EnvelopeApprovalRequest | undefined)) ??
    ({ description: '' } as EnvelopeApprovalRequest)
  const subagent = envelope.data as unknown as SubagentApprovalData

  const [decision, setDecision] = useState<'pending' | 'approved' | 'rejected'>(() =>
    hydrateDecision(envelope.prior_response),
  )
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [reason, setReason] = useState('')
  const [showFullPrompt, setShowFullPrompt] = useState(false)
  const [showAdvanced, setShowAdvanced] = useState(false)

  const risk = (isSubagentSpawn ? subagent.risk_level : approval.risk_level) || 'low'
  const meta = RISK_META[risk]
  const RiskIcon = meta.icon

  const respond = async (approved: boolean) => {
    setSubmitError(null)

    if (isSubagentSpawn) {
      // internal/chat/envelope_response_subagent.go's registered handler
      // switches on resp.Status: Submitted -> Approve, Cancelled -> Reject
      // (reading the reject reason off resp.Data["reason"]). Preserve that
      // exact wire contract — do not collapse to the generic flavor's
      // always-Submitted-with-data.approved shape.
      if (!onRespond) {
        setDecision(approved ? 'approved' : 'rejected')
        return
      }
      setSubmitting(true)
      try {
        if (approved) {
          await onRespond({ status: ResponseStatus.Submitted })
        } else {
          await onRespond({ status: ResponseStatus.Cancelled, data: { reason } })
        }
        setDecision(approved ? 'approved' : 'rejected')
      } catch (err) {
        setSubmitError(err instanceof Error ? err.message : 'Failed to submit')
      } finally {
        setSubmitting(false)
      }
      return
    }

    setDecision(approved ? 'approved' : 'rejected')
    if (!onRespond) return
    try {
      await onRespond({
        status: ResponseStatus.Submitted,
        data: { approved, description: approval.description },
      })
    } catch (err) {
      setDecision('pending')
      setSubmitError(err instanceof Error ? err.message : 'Failed to submit approval')
    }
  }

  // Terminal states — dialed-back shell, same primitive for both flavors.
  if (decision === 'approved') {
    return (
      <Envelope accent="success" muted>
        <EnvelopeBody>
          <div className="flex items-center gap-2">
            <ShieldCheck className="h-4 w-4 text-success" />
            {isSubagentSpawn ? (
              <span className="text-[13px] text-fg-secondary">
                Subagent run <code className="font-mono text-fg">{subagent.run_id}</code> — approved.
              </span>
            ) : (
              <>
                <span className="text-[13px] text-fg">Approved —</span>
                <span className="text-[13px] text-fg-secondary">{approval.description}</span>
              </>
            )}
          </div>
        </EnvelopeBody>
      </Envelope>
    )
  }

  if (decision === 'rejected') {
    return (
      <Envelope accent="neutral" muted>
        <EnvelopeBody>
          <div className="flex items-center gap-2">
            <ShieldX className="h-4 w-4 text-fg-muted" />
            {isSubagentSpawn ? (
              <span className="text-[13px] text-fg-secondary">
                Subagent run <code className="font-mono text-fg">{subagent.run_id}</code> — rejected.
              </span>
            ) : (
              <>
                <span className="text-[13px] text-fg">Rejected —</span>
                <span className="text-[13px] text-fg-secondary">{approval.description}</span>
              </>
            )}
          </div>
        </EnvelopeBody>
      </Envelope>
    )
  }

  if (isSubagentSpawn) {
    const isLongPrompt = subagent.prompt.length > PROMPT_TRUNCATE_LEN
    const displayedPrompt =
      isLongPrompt && !showFullPrompt
        ? `${subagent.prompt.slice(0, PROMPT_TRUNCATE_LEN)}…`
        : subagent.prompt
    const hasAdvanced = !!(subagent.parent_agent_id || subagent.inputs_json)

    return (
      <Envelope accent={meta.tone}>
        <EnvelopeHeader
          icon={RiskIcon}
          label="Subagent spawn approval"
          tone={meta.tone}
          meta={<span className="font-mono">{subagent.run_id}</span>}
          action={<StatusPill tone={meta.tone}>{meta.label}</StatusPill>}
        />

        <div className="space-y-3 px-4 py-3">
          <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-[12px]">
            <MetaItem label="Role" value={subagent.role} mono />
            <MetaItem label="Mode" value={subagent.mode} mono />
            {subagent.timeout_seconds != null && (
              <MetaItem label="Timeout" value={`${subagent.timeout_seconds}s`} mono />
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
                  {subagent.parent_agent_id && (
                    <div>
                      <span className="text-fg-muted">Parent agent ID:</span>{' '}
                      <code className="font-mono text-fg">{subagent.parent_agent_id}</code>
                    </div>
                  )}
                  {subagent.inputs_json && (
                    <div>
                      <div className="mb-1 text-fg-muted">Inputs JSON:</div>
                      <pre className="whitespace-pre-wrap break-words rounded-[6px] border border-border-subtle bg-surface px-2.5 py-1.5 font-mono text-[11px] text-fg">
                        {subagent.inputs_json}
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

          {submitError && (
            <p className="text-[12px] text-danger" role="alert">
              {submitError}
            </p>
          )}
        </div>

        <EnvelopeFooter>
          <Button size="sm" onClick={() => void respond(true)} disabled={submitting}>
            Approve
          </Button>
          <Button size="sm" variant="ghost" onClick={() => void respond(false)} disabled={submitting}>
            Reject
          </Button>
        </EnvelopeFooter>
      </Envelope>
    )
  }

  return (
    <Envelope accent={meta.tone}>
      <EnvelopeHeader
        icon={RiskIcon}
        label="Approval required"
        tone={meta.tone}
        action={<StatusPill tone={meta.tone}>{meta.label}</StatusPill>}
      />

      <EnvelopeBody title={approval.description} description={approval.details} />

      {submitError && (
        <div
          role="alert"
          className="mx-4 mb-3 rounded-md border border-danger/30 bg-danger/5 px-3 py-2 text-[12px] text-danger"
        >
          {submitError}
        </div>
      )}

      <EnvelopeFooter>
        <Button size="sm" onClick={() => void respond(true)}>
          Approve
        </Button>
        <Button
          size="sm"
          variant="outline"
          className="text-danger hover:bg-danger/10 hover:text-danger"
          onClick={() => void respond(false)}
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
