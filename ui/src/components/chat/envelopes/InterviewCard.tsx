import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { X, Check } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { type Answer, ResponseStatus } from '@/lib/envelope-response'
import type { Envelope as EnvelopeType, Question } from '@/lib/types'
import type { EnvelopeResponder } from './EnvelopeRenderer'
import { Envelope, EnvelopeHeader, EnvelopeFooter } from './primitives/Envelope'
import { StatusPill } from './primitives/StatusPill'

function normalizeOption(raw: unknown): { value: string; label: string; description?: string } {
  if (typeof raw === 'string') return { value: raw, label: raw }
  if (raw && typeof raw === 'object' && 'value' in raw) {
    const o = raw as { value: string; label?: string; description?: string }
    return { value: o.value, label: o.label ?? o.value, description: o.description }
  }
  return { value: String(raw), label: String(raw) }
}

interface InterviewCardProps {
  envelope: EnvelopeType
  onRespond?: EnvelopeResponder
  userMessageCount?: number
}

export function InterviewCard({ envelope, onRespond, userMessageCount }: InterviewCardProps) {
  const questions = useMemo(() => envelope.questions ?? [], [envelope.questions])
  const alreadyAnswered = envelope.prior_response != null

  const [answers, setAnswers] = useState<Record<number, string | string[]>>(() => {
    const initial: Record<number, string | string[]> = {}
    questions.forEach((q, i) => {
      if (q.type === 'checkbox') initial[i] = q.default ? [q.default] : []
      else initial[i] = q.default ?? ''
    })
    return initial
  })
  const [errors, setErrors] = useState<Record<number, string>>({})
  const [dismissed, setDismissed] = useState(false)
  const [submitted, setSubmitted] = useState(alreadyAnswered)
  const [submitError, setSubmitError] = useState<string | null>(null)

  const mountCountRef = useRef(userMessageCount)
  useEffect(() => {
    if (userMessageCount !== undefined && mountCountRef.current !== undefined) {
      if (userMessageCount > mountCountRef.current && !submitted) setDismissed(true)
    }
  }, [userMessageCount, submitted])

  const validate = useCallback((): boolean => {
    const next: Record<number, string> = {}
    questions.forEach((q, i) => {
      if (!q.required) return
      const val = answers[i]
      const empty = Array.isArray(val) ? val.length === 0 : !val
      if (empty) next[i] = 'Required'
    })
    setErrors(next)
    return Object.keys(next).length === 0
  }, [questions, answers])

  const buildAnswers = useCallback(
    (overrideWithDefaults = false, markAccepted = false): Answer[] =>
      questions.map((q, i) => {
        const val = overrideWithDefaults
          ? answers[i] !== undefined && answers[i] !== ''
            ? answers[i]
            : q.default ?? ''
          : answers[i] ?? ''
        return {
          questionId: `q-${i}`,
          value: val,
          ...(markAccepted ? { acceptedSuggestion: true } : {}),
        }
      }),
    [questions, answers],
  )

  const handleSubmit = useCallback(async () => {
    if (!validate()) return
    setSubmitError(null)
    const typed = buildAnswers()
    setSubmitted(true)
    if (!onRespond) return
    try {
      await onRespond({ status: ResponseStatus.Submitted, answers: typed })
    } catch (err) {
      setSubmitted(false)
      setSubmitError(err instanceof Error ? err.message : 'Failed to submit')
    }
  }, [validate, buildAnswers, onRespond])

  const handleAcceptSuggested = useCallback(async () => {
    setSubmitError(null)
    const typed = buildAnswers(true, true)
    setSubmitted(true)
    if (!onRespond) return
    try {
      await onRespond({ status: ResponseStatus.Submitted, answers: typed })
    } catch (err) {
      setSubmitted(false)
      setSubmitError(err instanceof Error ? err.message : 'Failed to submit')
    }
  }, [buildAnswers, onRespond])

  const hasDefaults = questions.some((q) => q.default != null && q.default !== '')
  const allRequiredHaveDefaults = questions
    .filter((q) => q.required)
    .every((q) => q.default != null && q.default !== '')

  if (dismissed) return null

  if (submitted) {
    return (
      <Envelope accent="success" muted>
        <div className="flex items-center gap-2 px-4 py-2.5">
          <Check className="h-4 w-4 text-success" />
          <span className="text-[13px] text-fg">Answers submitted</span>
        </div>
      </Envelope>
    )
  }

  return (
    <Envelope>
      <EnvelopeHeader
        label={envelope.title || 'Interview'}
        meta={envelope.subtitle ? <span className="normal-case">{envelope.subtitle}</span> : undefined}
        action={
          <button
            type="button"
            onClick={() => setDismissed(true)}
            className="rounded-[4px] p-1 text-fg-faint transition-colors hover:bg-surface-hover hover:text-fg-muted"
            aria-label="Dismiss"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        }
      />

      <div className="space-y-4 px-4 py-3">
        {questions.map((q, i) => (
          <QuestionInput
            key={i}
            question={q}
            answer={answers[i] ?? ''}
            error={errors[i]}
            onChange={(val) => {
              setAnswers((a) => ({ ...a, [i]: val }))
              if (errors[i]) {
                setErrors((e) => {
                  const n = { ...e }
                  delete n[i]
                  return n
                })
              }
            }}
          />
        ))}

        {submitError && (
          <p className="text-[12px] text-danger" role="alert">
            {submitError}
          </p>
        )}
      </div>

      <EnvelopeFooter>
        <Button size="sm" onClick={() => void handleSubmit()}>
          Submit
        </Button>
        {hasDefaults && allRequiredHaveDefaults && (
          <Button
            size="sm"
            variant="ghost"
            onClick={() => void handleAcceptSuggested()}
          >
            Accept suggested
          </Button>
        )}
      </EnvelopeFooter>
    </Envelope>
  )
}

interface QuestionInputProps {
  question: Question
  answer: string | string[]
  error?: string
  onChange: (val: string | string[]) => void
}

const FIELD_INPUT =
  'w-full rounded-[6px] border border-border-subtle bg-surface px-2.5 py-1.5 text-[13px] text-fg outline-none transition-colors placeholder:text-fg-faint focus:border-primary'

function QuestionInput({ question, answer, error, onChange }: QuestionInputProps) {
  const isCard = question.display_style === 'card'

  return (
    <div>
      <label className="mb-1 block font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
        {question.prompt}
        {question.required && <span className="ml-0.5 text-danger">*</span>}
      </label>

      {question.description && (
        <p className="mb-2 text-[12px] leading-relaxed text-fg-muted">{question.description}</p>
      )}

      {question.type === 'text' && (
        <input
          type="text"
          value={String(answer ?? '')}
          onChange={(e) => onChange(e.target.value)}
          className={FIELD_INPUT}
        />
      )}

      {question.type === 'textarea' && (
        <textarea
          value={String(answer ?? '')}
          onChange={(e) => onChange(e.target.value)}
          rows={3}
          className={`${FIELD_INPUT} resize-none`}
        />
      )}

      {question.type === 'select' && question.options && (
        <select
          value={String(answer ?? '')}
          onChange={(e) => onChange(e.target.value)}
          className={FIELD_INPUT}
        >
          <option value="">Select…</option>
          {question.options.map((raw) => {
            const opt = normalizeOption(raw)
            return (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            )
          })}
        </select>
      )}

      {question.type === 'radio' &&
        question.options &&
        (isCard ? (
          <CardOptions
            options={question.options}
            selected={String(answer ?? '')}
            defaultValue={question.default}
            multi={false}
            onChange={(v) => onChange(v as string)}
          />
        ) : (
          <CompactRadioOptions
            options={question.options}
            selected={String(answer ?? '')}
            defaultValue={question.default}
            onChange={(v) => onChange(v)}
          />
        ))}

      {question.type === 'checkbox' &&
        question.options &&
        (isCard ? (
          <CardOptions
            options={question.options}
            selected={Array.isArray(answer) ? answer : []}
            defaultValue={question.default}
            multi={true}
            onChange={(v) => onChange(v as string[])}
          />
        ) : (
          <CompactCheckboxOptions
            options={question.options}
            selected={Array.isArray(answer) ? answer : []}
            defaultValue={question.default}
            onChange={(v) => onChange(v)}
          />
        ))}

      {error && <p className="mt-1 text-[12px] text-danger">{error}</p>}
    </div>
  )
}

function CompactRadioOptions({
  options = [],
  selected,
  defaultValue,
  onChange,
}: {
  options: Question['options']
  selected: string
  defaultValue?: string
  onChange: (v: string) => void
}) {
  return (
    <div className="space-y-1.5">
      {options.map((raw) => {
        const opt = normalizeOption(raw)
        const isSelected = selected === opt.value
        const isDefault = defaultValue === opt.value
        return (
          <button
            key={opt.value}
            type="button"
            onClick={() => onChange(opt.value)}
            className="group flex w-full items-center gap-2 text-left"
          >
            <span
              className={`flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-full border-2 transition-colors ${
                isSelected
                  ? 'border-primary bg-primary'
                  : 'border-border-subtle group-hover:border-primary/60'
              }`}
            >
              {isSelected && <span className="h-1.5 w-1.5 rounded-full bg-primary-foreground" />}
            </span>
            <span className={`flex-1 text-[13px] ${isSelected ? 'text-fg' : 'text-fg-secondary'}`}>
              {opt.label}
            </span>
            {isDefault && !isSelected && <StatusPill tone="info">Suggested</StatusPill>}
          </button>
        )
      })}
    </div>
  )
}

function CompactCheckboxOptions({
  options = [],
  selected,
  defaultValue,
  onChange,
}: {
  options: Question['options']
  selected: string[]
  defaultValue?: string
  onChange: (v: string[]) => void
}) {
  return (
    <div className="space-y-1.5">
      {options.map((raw) => {
        const opt = normalizeOption(raw)
        const isSelected = selected.includes(opt.value)
        const isDefault = defaultValue === opt.value
        return (
          <button
            key={opt.value}
            type="button"
            onClick={() => {
              onChange(
                isSelected ? selected.filter((v) => v !== opt.value) : [...selected, opt.value],
              )
            }}
            className="group flex w-full items-center gap-2 text-left"
          >
            <span
              className={`flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-[4px] border-2 transition-colors ${
                isSelected
                  ? 'border-primary bg-primary'
                  : 'border-border-subtle group-hover:border-primary/60'
              }`}
            >
              {isSelected && <Check className="h-2.5 w-2.5 text-primary-foreground" />}
            </span>
            <span className={`flex-1 text-[13px] ${isSelected ? 'text-fg' : 'text-fg-secondary'}`}>
              {opt.label}
            </span>
            {isDefault && !isSelected && <StatusPill tone="info">Suggested</StatusPill>}
          </button>
        )
      })}
    </div>
  )
}

function CardOptions({
  options = [],
  selected,
  defaultValue,
  multi,
  onChange,
}: {
  options: Question['options']
  selected: string | string[]
  defaultValue?: string
  multi: boolean
  onChange: (v: string | string[]) => void
}) {
  const selectedArr = Array.isArray(selected) ? selected : [selected]
  return (
    <div className="space-y-2">
      {options.map((raw) => {
        const opt = normalizeOption(raw)
        const isSelected = selectedArr.includes(opt.value)
        const isDefault = defaultValue === opt.value

        const handleClick = () => {
          if (multi) {
            const arr = selectedArr.includes(opt.value)
              ? selectedArr.filter((v) => v !== opt.value)
              : [...selectedArr, opt.value]
            onChange(arr)
          } else {
            onChange(opt.value)
          }
        }

        return (
          <button
            key={opt.value}
            type="button"
            onClick={handleClick}
            className={`w-full rounded-[6px] border-2 px-3 py-2.5 text-left transition-colors ${
              isSelected
                ? 'border-primary bg-primary/5'
                : 'border-border-subtle hover:border-primary/40'
            }`}
          >
            <div className="flex items-start justify-between gap-2">
              <span
                className={`text-[13px] font-medium ${
                  isSelected ? 'text-fg' : 'text-fg-secondary'
                }`}
              >
                {opt.label}
              </span>
              <div className="flex shrink-0 items-center gap-1.5">
                {isDefault && !isSelected && <StatusPill tone="info">Suggested</StatusPill>}
                {multi ? (
                  <span
                    className={`flex h-3.5 w-3.5 items-center justify-center rounded-[4px] border-2 transition-colors ${
                      isSelected ? 'border-primary bg-primary' : 'border-border-subtle'
                    }`}
                  >
                    {isSelected && <Check className="h-2.5 w-2.5 text-primary-foreground" />}
                  </span>
                ) : (
                  <span
                    className={`flex h-3.5 w-3.5 items-center justify-center rounded-full border-2 transition-colors ${
                      isSelected ? 'border-primary bg-primary' : 'border-border-subtle'
                    }`}
                  >
                    {isSelected && (
                      <span className="h-1.5 w-1.5 rounded-full bg-primary-foreground" />
                    )}
                  </span>
                )}
              </div>
            </div>
            {opt.description && (
              <p className="mt-1 text-[12px] leading-relaxed text-fg-muted">{opt.description}</p>
            )}
          </button>
        )
      })}
    </div>
  )
}
