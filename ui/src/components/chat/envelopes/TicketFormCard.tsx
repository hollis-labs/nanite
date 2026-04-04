import { useState } from 'react'
import { Ticket, Loader2, CheckCircle, AlertCircle, Download, Info } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { buildTicketDataMarker, buildTicketMessage } from './ticket-utils'

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

      const result = await res.json() as Record<string, unknown>
      setFormState('success')
      setTicketId((result['ticket_id'] || result['id'] || '') as string)
      setTicketResult(result)

      if (onSendMessage) {
        const tid = (result['ticket_id'] || result['id'] || 'unknown') as string
        const rt = (result['routing'] || 'auto') as string
        const msg = buildTicketMessage(tid, title.trim(), category, priority, rt)
        const marker = buildTicketDataMarker({
          id: tid, title: title.trim(), description: description.trim(),
          category, priority, routing: rt,
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
      <div className="rounded-sm border border-success/30 bg-bg-elevated/50 overflow-hidden">
        {/* Header */}
        <div className="flex items-center justify-between bg-success/5 px-4 py-3 border-b border-success/20">
          <div className="flex items-center gap-2">
            <CheckCircle className="h-4 w-4 text-success" />
            <span className="text-sm font-medium text-fg">{ticketId}</span>
            <span className="text-sm text-fg-secondary">&mdash;</span>
            <span className="text-sm text-fg-secondary truncate">{title}</span>
          </div>
          <span className="inline-block rounded-full bg-success/15 border border-success/25 px-2 py-0.5 text-xs text-success">
            open
          </span>
        </div>

        <div className="px-4 py-3 space-y-3">
          {/* Details grid */}
          <div className="grid grid-cols-3 gap-3">
            <div>
              <span className="block text-xs font-medium text-fg-secondary mb-0.5">Category</span>
              <span className="text-sm text-fg capitalize">{category}</span>
            </div>
            <div>
              <span className="block text-xs font-medium text-fg-secondary mb-0.5">Priority</span>
              <span className="text-sm text-fg capitalize">{priority}</span>
            </div>
            <div>
              <span className="block text-xs font-medium text-fg-secondary mb-0.5">Routing</span>
              <span className="text-sm text-fg">{routing}</span>
            </div>
          </div>

          {/* Actions */}
          <div className="flex items-center gap-3 pt-1">
            <Button
              size="sm"
              className="bg-surface hover:bg-surface-hover text-fg text-xs px-3 py-1 h-7"
              onClick={() => window.open(`/api/plugins/ui/support-ticket-download?ticket_id=${encodeURIComponent(ticketId)}`, '_blank')}
            >
              <Download className="mr-1.5 h-3 w-3" />
              Download Ticket
            </Button>
          </div>

          {/* Production note */}
          <div className="flex items-start gap-2 rounded-md border border-border-subtle bg-surface/50 px-3 py-2">
            <Info className="mt-0.5 h-3 w-3 shrink-0 text-fg-muted" />
            <span className="text-xs text-fg-muted">
              In production, this ticket would be automatically created in BMC Helix ITSM and routed to {routing}
            </span>
          </div>
        </div>
      </div>
    )
  }

  const inputCls =
    'w-full bg-surface border border-border-subtle rounded-md px-2.5 py-1.5 text-sm text-fg outline-none focus:border-primary placeholder:text-fg-faint'

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 p-4">
      {/* Header */}
      <div className="mb-4 flex items-center gap-2">
        <Ticket className="h-4 w-4 text-fg-secondary" />
        <h4 className="text-sm font-medium text-fg">Create Support Ticket</h4>
      </div>

      <form onSubmit={handleSubmit} className="space-y-3">
        {/* Issue Summary */}
        <div>
          <label className="mb-1 block text-xs font-medium text-fg-secondary">
            Issue Summary <span className="text-danger">*</span>
          </label>
          <input
            type="text"
            className={inputCls}
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="Brief summary of the issue"
            required
          />
        </div>

        {/* Category */}
        <div>
          <label className="mb-1 block text-xs font-medium text-fg-secondary">
            Category <span className="text-danger">*</span>
          </label>
          <select
            className={inputCls}
            value={category}
            onChange={(e) => setCategory(e.target.value)}
            required
          >
            <option value="">Select category...</option>
            {data.categories.map((cat) => (
              <option key={cat} value={cat}>
                {cat.charAt(0).toUpperCase() + cat.slice(1)}
              </option>
            ))}
          </select>
        </div>

        {/* Priority */}
        <div>
          <label className="mb-1 block text-xs font-medium text-fg-secondary">Priority</label>
          <div className="flex gap-3">
            {(['low', 'medium', 'high', 'critical'] as const).map((p) => (
              <label key={p} className="flex items-center gap-1.5 cursor-pointer">
                <input
                  type="radio"
                  name="priority"
                  value={p}
                  checked={priority === p}
                  onChange={() => setPriority(p)}
                  className="accent-accent"
                />
                <span className="text-xs text-fg-secondary capitalize">{p}</span>
              </label>
            ))}
          </div>
        </div>

        {/* Description */}
        <div>
          <label className="mb-1 block text-xs font-medium text-fg-secondary">
            Description <span className="text-danger">*</span>
          </label>
          <textarea
            className={`${inputCls} min-h-[80px] resize-y`}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Describe the issue in detail"
            rows={3}
            required
          />
        </div>

        {/* Steps Already Tried */}
        <div>
          <label className="mb-1 block text-xs font-medium text-fg-secondary">
            Steps Already Tried
          </label>
          <textarea
            className={`${inputCls} min-h-[60px] resize-y`}
            value={stepsTried}
            onChange={(e) => setStepsTried(e.target.value)}
            placeholder="What have you already tried?"
            rows={2}
          />
        </div>

        {/* Error message */}
        {formState === 'error' && (
          <div className="flex items-center gap-2 rounded-md border border-danger/30 bg-danger/5 px-3 py-2">
            <AlertCircle className="h-4 w-4 shrink-0 text-danger" />
            <span className="text-xs text-danger">{errorMsg}</span>
          </div>
        )}

        {/* Submit */}
        <Button
          type="submit"
          size="sm"
          className="bg-primary hover:bg-primary-hover text-white text-xs px-4 py-1 h-8"
          disabled={formState === 'submitting' || !title.trim() || !category || !description.trim()}
        >
          {formState === 'submitting' ? (
            <>
              <Loader2 className="mr-1.5 h-3 w-3 animate-spin" />
              Creating...
            </>
          ) : (
            'Create Ticket'
          )}
        </Button>
      </form>
    </div>
  )
}
