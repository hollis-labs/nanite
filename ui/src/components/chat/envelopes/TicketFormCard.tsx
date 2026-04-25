import { useState } from 'react'
import { Ticket, Loader2, CheckCircle, AlertCircle, Download, Info } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { buildTicketDataMarker, buildTicketMessage } from './ticket-utils'
import { Envelope, EnvelopeHeader, EnvelopeFooter } from './primitives/Envelope'
import { StatusPill, type StatusTone } from './primitives/StatusPill'

interface TicketFormData {
  prefilled?: {
    title?: string
    description?: string
    category?: string
    priority?: string
    steps_tried?: string
  }
  categories: string[]
}

interface TicketFormCardProps {
  data: TicketFormData
  onSendMessage?: (content: string) => void
}

type FormState = 'idle' | 'submitting' | 'success' | 'error'

const PRIORITY_TONE: Record<string, StatusTone> = {
  low: 'success',
  medium: 'warning',
  high: 'danger',
  critical: 'danger',
}

const INPUT_CLS =
  'w-full rounded-[6px] border border-border-subtle bg-surface px-2.5 py-1.5 text-[13px] text-fg outline-none transition-colors placeholder:text-fg-faint focus:border-primary disabled:opacity-50'

export function TicketFormCard({ data, onSendMessage }: TicketFormCardProps) {
  const [title, setTitle] = useState(data.prefilled?.title || '')
  const [category, setCategory] = useState(data.prefilled?.category || '')
  const [priority, setPriority] = useState(data.prefilled?.priority || 'medium')
  const [description, setDescription] = useState(data.prefilled?.description || '')
  const [stepsTried, setStepsTried] = useState(data.prefilled?.steps_tried || '')
  const [formState, setFormState] = useState<FormState>('idle')
  const [errorMsg, setErrorMsg] = useState('')
  const [ticketId, setTicketId] = useState('')
  const [ticketResult, setTicketResult] = useState<Record<string, unknown> | null>(null)

  const handleSubmit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    if (!title.trim() || !category || !description.trim()) return

    setFormState('submitting')
    setErrorMsg('')

    try {
      const res = await fetch('/api/plugins/tickets', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: title.trim(),
          category,
          priority,
          description: description.trim(),
          steps_tried: stepsTried.trim() || undefined,
        }),
      })

      if (!res.ok) {
        const body = await res.text()
        throw new Error(body || `HTTP ${res.status}`)
      }

      const result = (await res.json()) as Record<string, unknown>
      setFormState('success')
      setTicketId((result['ticket_id'] || result['id'] || '') as string)
      setTicketResult(result)

      if (onSendMessage) {
        const tid = (result['ticket_id'] || result['id'] || 'unknown') as string
        const rt = (result['routing'] || 'auto') as string
        const msg = buildTicketMessage(tid, title.trim(), category, priority, rt)
        const marker = buildTicketDataMarker({
          id: tid,
          title: title.trim(),
          description: description.trim(),
          category,
          priority,
          routing: rt,
        })
        onSendMessage(msg + marker)
      }
    } catch (err) {
      setFormState('error')
      setErrorMsg(err instanceof Error ? err.message : 'Failed to create ticket')
    }
  }

  if (formState === 'success' && ticketResult) {
    const routing = (ticketResult['routing'] || 'IT Service Desk — Triage') as string
    return (
      <Envelope accent="success">
        <EnvelopeHeader
          icon={CheckCircle}
          label="Ticket created"
          tone="success"
          meta={<span className="font-mono">{ticketId}</span>}
          action={<StatusPill tone="success">open</StatusPill>}
        />

        <div className="space-y-3 px-4 py-3">
          <h3 className="text-[14px] font-semibold leading-snug text-fg">{title}</h3>

          <div className="grid grid-cols-3 gap-3">
            <Field label="Category">
              <span className="capitalize">{category}</span>
            </Field>
            <Field label="Priority">
              <StatusPill tone={PRIORITY_TONE[priority] ?? 'warning'}>{priority}</StatusPill>
            </Field>
            <Field label="Routing">
              <span className="text-[13px]">{routing}</span>
            </Field>
          </div>

          <div className="flex items-start gap-2 rounded-[6px] border border-border-subtle bg-surface px-3 py-2">
            <Info className="mt-0.5 h-3 w-3 shrink-0 text-fg-muted" />
            <span className="text-[11px] leading-relaxed text-fg-muted">
              In production, this ticket would be automatically created in BMC Helix ITSM and routed
              to {routing}
            </span>
          </div>
        </div>

        <EnvelopeFooter>
          <Button
            size="sm"
            variant="outline"
            onClick={() =>
              window.open(
                `/api/plugins/ui/support-ticket-download?ticket_id=${encodeURIComponent(ticketId)}`,
                '_blank',
              )
            }
          >
            <Download className="h-3 w-3" />
            Download ticket
          </Button>
        </EnvelopeFooter>
      </Envelope>
    )
  }

  return (
    <Envelope>
      <form onSubmit={handleSubmit}>
        <EnvelopeHeader icon={Ticket} label="Create support ticket" />

        <div className="space-y-3 px-4 py-3">
        <FieldGroup label="Issue summary" required>
          <input
            type="text"
            className={INPUT_CLS}
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="Brief summary of the issue"
            required
          />
        </FieldGroup>

        <FieldGroup label="Category" required>
          <select
            className={INPUT_CLS}
            value={category}
            onChange={(e) => setCategory(e.target.value)}
            required
          >
            <option value="">Select category…</option>
            {data.categories.map((cat) => (
              <option key={cat} value={cat}>
                {cat.charAt(0).toUpperCase() + cat.slice(1)}
              </option>
            ))}
          </select>
        </FieldGroup>

        <FieldGroup label="Priority">
          <div className="flex flex-wrap gap-2">
            {(['low', 'medium', 'high', 'critical'] as const).map((p) => {
              const active = priority === p
              return (
                <button
                  key={p}
                  type="button"
                  onClick={() => setPriority(p)}
                  className={`rounded-[4px] border px-2.5 py-1 text-[12px] capitalize transition-colors ${
                    active
                      ? 'border-primary bg-primary/10 text-fg'
                      : 'border-border-subtle bg-surface text-fg-secondary hover:border-border'
                  }`}
                >
                  {p}
                </button>
              )
            })}
          </div>
        </FieldGroup>

        <FieldGroup label="Description" required>
          <textarea
            className={`${INPUT_CLS} min-h-[80px] resize-y`}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Describe the issue in detail"
            rows={3}
            required
          />
        </FieldGroup>

        <FieldGroup label="Steps already tried">
          <textarea
            className={`${INPUT_CLS} min-h-[60px] resize-y`}
            value={stepsTried}
            onChange={(e) => setStepsTried(e.target.value)}
            placeholder="What have you already tried?"
            rows={2}
          />
        </FieldGroup>

        {formState === 'error' && (
          <div className="flex items-center gap-2 rounded-[6px] border border-danger/30 bg-danger/5 px-3 py-2">
            <AlertCircle className="h-4 w-4 shrink-0 text-danger" />
            <span className="text-[12px] text-danger">{errorMsg}</span>
          </div>
        )}
        </div>

        <EnvelopeFooter>
          <Button
            type="submit"
            size="sm"
            disabled={
              formState === 'submitting' || !title.trim() || !category || !description.trim()
            }
          >
            {formState === 'submitting' ? (
              <>
                <Loader2 className="h-3 w-3 animate-spin" />
                Creating…
              </>
            ) : (
              'Create ticket'
            )}
          </Button>
        </EnvelopeFooter>
      </form>
    </Envelope>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-1 font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
        {label}
      </div>
      <div className="text-[13px] text-fg">{children}</div>
    </div>
  )
}

function FieldGroup({
  label,
  required,
  children,
}: {
  label: string
  required?: boolean
  children: React.ReactNode
}) {
  return (
    <div>
      <label className="mb-1 block font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
        {label}
        {required && <span className="ml-0.5 text-danger">*</span>}
      </label>
      {children}
    </div>
  )
}
