import { CheckCircle, Lightbulb, MinusCircle } from 'lucide-react'
import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { ResponseStatus } from '@/lib/envelope-response'
import type { EnvelopeResponder } from './EnvelopeRenderer'
import { Envelope, EnvelopeHeader, EnvelopeFooter } from './primitives/Envelope'

interface ResolutionCaptureData {
  ticket_id?: string
  issue_summary?: string
  categories: string[]
}

interface ResolutionCaptureCardProps {
  data: ResolutionCaptureData
  onSendMessage?: (content: string) => void
  onRespond?: EnvelopeResponder
}

type CardState = 'idle' | 'submitted' | 'skipped'

const TIME_OPTIONS = ['< 15 min', '15-30 min', '30-60 min', '1-2 hours', '2+ hours']

const INPUT_CLS =
  'w-full rounded-[6px] border border-border-subtle bg-surface px-2.5 py-1.5 text-[13px] text-fg outline-none transition-colors placeholder:text-fg-faint focus:border-primary'

export function ResolutionCaptureCard({
  data,
  onSendMessage,
  onRespond,
}: ResolutionCaptureCardProps) {
  const [cardState, setCardState] = useState<CardState>('idle')
  const [whatFixedIt, setWhatFixedIt] = useState('')
  const [category, setCategory] = useState('')
  const [timeSpent, setTimeSpent] = useState('')
  const [relatedKB, setRelatedKB] = useState('')
  const [createArticle, setCreateArticle] = useState(true)
  const [submitError, setSubmitError] = useState<string | null>(null)

  const handleSubmit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    if (!whatFixedIt.trim()) return

    setCardState('submitted')
    setSubmitError(null)

    if (onSendMessage) {
      const ticketLabel = data.ticket_id || 'this issue'
      const fixPreview =
        whatFixedIt.trim().length > 100
          ? whatFixedIt.trim().slice(0, 100) + '...'
          : whatFixedIt.trim()

      onSendMessage(
        `Resolution captured for ${ticketLabel}:\n` +
          `- Fix: ${fixPreview}\n` +
          `- Category: ${category || 'none'}\n` +
          `- Time spent: ${timeSpent || 'not specified'}\n` +
          `- Create KB article: ${createArticle ? 'yes' : 'no'}`,
      )
    }

    if (!onRespond) return
    try {
      await onRespond({
        status: ResponseStatus.Submitted,
        data: {
          ticket_id: data.ticket_id,
          what_fixed_it: whatFixedIt.trim(),
          category: category || null,
          time_spent: timeSpent || null,
          related_kb: relatedKB
            ? relatedKB
                .split(',')
                .map((s) => s.trim())
                .filter(Boolean)
            : [],
          create_kb_article: createArticle,
        },
      })
    } catch (err) {
      setCardState('idle')
      setSubmitError(err instanceof Error ? err.message : 'Failed to submit resolution')
    }
  }

  const handleSkip = async () => {
    setSubmitError(null)
    if (onRespond) {
      try {
        await onRespond({ status: ResponseStatus.Cancelled, data: {} })
      } catch (err) {
        setSubmitError(err instanceof Error ? err.message : 'Failed to record skip')
        return
      }
    }
    setCardState('skipped')
  }

  if (cardState === 'submitted') {
    return (
      <Envelope accent="success" muted>
        <div className="flex items-center gap-2 px-4 py-2.5">
          <CheckCircle className="h-4 w-4 text-success" />
          <span className="text-[13px] text-fg">Resolution captured — thank you!</span>
        </div>
      </Envelope>
    )
  }

  if (cardState === 'skipped') {
    return (
      <Envelope muted>
        <div className="flex items-center gap-2 px-4 py-2.5 text-fg-muted">
          <MinusCircle className="h-4 w-4" />
          <span className="text-[13px]">Resolution capture skipped</span>
        </div>
      </Envelope>
    )
  }

  return (
    <Envelope>
      <form onSubmit={(e) => void handleSubmit(e)}>
        <EnvelopeHeader icon={Lightbulb} label="Capture resolution" />

        <div className="space-y-3 px-4 py-3">
          <p className="text-[12px] text-fg-muted">
            Help us improve the knowledge base — document what resolved this issue.
          </p>

          {data.issue_summary && (
            <div className="rounded-[6px] border border-border-subtle bg-surface px-3 py-2">
              <span className="font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
                Issue
              </span>
              <p className="mt-0.5 text-[13px] text-fg-secondary">{data.issue_summary}</p>
            </div>
          )}

          {submitError && (
            <p className="text-[12px] text-danger" role="alert">
              {submitError}
            </p>
          )}

          <FieldGroup label="What fixed it" required>
            <textarea
              className={`${INPUT_CLS} min-h-[80px] resize-y`}
              value={whatFixedIt}
              onChange={(e) => setWhatFixedIt(e.target.value)}
              placeholder="Describe the resolution steps"
              rows={3}
              required
            />
          </FieldGroup>

          {data.categories.length > 0 && (
            <FieldGroup label="Issue category">
              <select
                className={INPUT_CLS}
                value={category}
                onChange={(e) => setCategory(e.target.value)}
              >
                <option value="">Select category…</option>
                {data.categories.map((cat) => (
                  <option key={cat} value={cat}>
                    {cat.charAt(0).toUpperCase() + cat.slice(1)}
                  </option>
                ))}
              </select>
            </FieldGroup>
          )}

          <FieldGroup label="Time spent">
            <select
              className={INPUT_CLS}
              value={timeSpent}
              onChange={(e) => setTimeSpent(e.target.value)}
            >
              <option value="">Select time range…</option>
              {TIME_OPTIONS.map((opt) => (
                <option key={opt} value={opt}>
                  {opt}
                </option>
              ))}
            </select>
          </FieldGroup>

          <FieldGroup label="Related KB articles">
            <input
              type="text"
              className={INPUT_CLS}
              value={relatedKB}
              onChange={(e) => setRelatedKB(e.target.value)}
              placeholder="Comma-separated KB IDs (optional)"
            />
          </FieldGroup>

          <label className="flex cursor-pointer items-center gap-2">
            <input
              type="checkbox"
              checked={createArticle}
              onChange={(e) => setCreateArticle(e.target.checked)}
              className="accent-primary"
            />
            <span className="text-[13px] text-fg-secondary">
              Should this become a new KB article?
            </span>
          </label>
        </div>

        <EnvelopeFooter>
          <Button type="submit" size="sm" disabled={!whatFixedIt.trim()}>
            Submit resolution
          </Button>
          <Button type="button" size="sm" variant="ghost" onClick={() => void handleSkip()}>
            Skip
          </Button>
        </EnvelopeFooter>
      </form>
    </Envelope>
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
