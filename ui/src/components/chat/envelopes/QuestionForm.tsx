import { useState } from 'react'
import { Button } from '@/components/ui/Button'
import type { Question } from '@/lib/types'

// Options can be strings or {value, label} objects — normalize to {value, label}.
function normalizeOption(opt: unknown): { value: string; label: string } {
  if (typeof opt === 'string') return { value: opt, label: opt }
  if (opt && typeof opt === 'object' && 'value' in opt) {
    const o = opt as { value: string; label?: string }
    return { value: o.value, label: o.label || o.value }
  }
  return { value: String(opt), label: String(opt) }
}

interface QuestionFormProps {
  questions: Question[]
  onSubmit?: (formatted: string) => void
}

export function QuestionForm({ questions, onSubmit }: QuestionFormProps) {
  const [answers, setAnswers] = useState<Record<number, string | string[]>>(() => {
    const initial: Record<number, string | string[]> = {}
    questions.forEach((q, i) => {
      if (q.type === 'checkbox') {
        initial[i] = q.default ? [q.default] : []
      } else {
        initial[i] = q.default || ''
      }
    })
    return initial
  })
  const [submitted, setSubmitted] = useState(false)

  const handleSubmit = () => {
    const lines = questions.map((q, i) => {
      const answer = answers[i]
      const answerStr = Array.isArray(answer) ? answer.join(', ') : answer
      return `- ${q.prompt}: ${answerStr}`
    })
    const formatted = `Answers:\n${lines.join('\n')}`
    console.log('[QuestionForm] Submitted:', formatted)
    onSubmit?.(formatted)
    setSubmitted(true)
  }

  if (submitted) {
    return (
      <div className="rounded-sm border border-success/30 bg-success/5 p-4">
        <p className="text-sm text-success">Answers submitted</p>
      </div>
    )
  }

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 p-4 space-y-4">
      {questions.map((q, i) => (
        <div key={i}>
          <label className="block text-sm font-medium text-fg-secondary mb-1.5">
            {q.prompt}
            {q.required && <span className="text-red-400 ml-0.5">*</span>}
          </label>

          {q.type === 'textarea' && (
            <textarea
              value={String(answers[i] ?? '')}
              onChange={(e) => setAnswers((a) => ({ ...a, [i]: e.target.value }))}
              rows={3}
              className="w-full bg-surface border border-border-subtle rounded-md px-2.5 py-1.5 text-sm text-fg outline-none focus:border-accent resize-none"
            />
          )}

          {q.type === 'text' && (
            <input
              type="text"
              value={String(answers[i] ?? '')}
              onChange={(e) => setAnswers((a) => ({ ...a, [i]: e.target.value }))}
              className="w-full bg-surface border border-border-subtle rounded-md px-2.5 py-1.5 text-sm text-fg outline-none focus:border-accent"
            />
          )}

          {q.type === 'select' && q.options && (
            <select
              value={String(answers[i] ?? '')}
              onChange={(e) => setAnswers((a) => ({ ...a, [i]: e.target.value }))}
              className="w-full bg-surface border border-border-subtle rounded-md px-2.5 py-1.5 text-sm text-fg outline-none focus:border-accent"
            >
              <option value="">Select...</option>
              {q.options.map((raw) => {
                const opt = normalizeOption(raw)
                return <option key={opt.value} value={opt.value}>{opt.label}</option>
              })}
            </select>
          )}

          {q.type === 'radio' && q.options && (
            <div className="space-y-1.5">
              {q.options.map((raw) => {
                const opt = normalizeOption(raw)
                return (
                  <label key={opt.value} className="flex items-center gap-2 text-sm text-fg-secondary cursor-pointer">
                    <input
                      type="radio"
                      name={`question-${i}`}
                      value={opt.value}
                      checked={answers[i] === opt.value}
                      onChange={() => setAnswers((a) => ({ ...a, [i]: opt.value }))}
                      className="accent-accent"
                    />
                    {opt.label}
                  </label>
                )
              })}
            </div>
          )}

          {q.type === 'checkbox' && q.options && (
            <div className="space-y-1.5">
              {q.options.map((raw) => {
                const opt = normalizeOption(raw)
                const selected = Array.isArray(answers[i]) ? answers[i] as string[] : []
                return (
                  <label key={opt.value} className="flex items-center gap-2 text-sm text-fg-secondary cursor-pointer">
                    <input
                      type="checkbox"
                      value={opt.value}
                      checked={selected.includes(opt.value)}
                      onChange={(e) => {
                        setAnswers((a) => {
                          const current = Array.isArray(a[i]) ? [...(a[i] as string[])] : []
                          if (e.target.checked) {
                            return { ...a, [i]: [...current, opt.value] }
                          } else {
                            return { ...a, [i]: current.filter((v) => v !== opt.value) }
                          }
                        })
                      }}
                      className="accent-accent"
                    />
                    {opt.label}
                  </label>
                )
              })}
            </div>
          )}
        </div>
      ))}

      <Button
        size="sm"
        className="bg-accent hover:bg-accent-hover text-white text-xs px-4 py-1 h-7"
        onClick={handleSubmit}
      >
        Submit Answers
      </Button>
    </div>
  )
}
