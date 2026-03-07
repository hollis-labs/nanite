import { useState } from 'react'
import { Button } from '@/components/ui/Button'
import type { Question } from '@/lib/types'

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
      <div className="rounded-lg border border-green-500/30 bg-green-500/5 p-4">
        <p className="text-sm text-green-400">Answers submitted</p>
      </div>
    )
  }

  return (
    <div className="rounded-lg border border-zinc-700 bg-zinc-900/50 p-4 space-y-4">
      {questions.map((q, i) => (
        <div key={i}>
          <label className="block text-sm font-medium text-zinc-300 mb-1.5">
            {q.prompt}
            {q.required && <span className="text-red-400 ml-0.5">*</span>}
          </label>

          {q.type === 'textarea' && (
            <textarea
              value={String(answers[i] ?? '')}
              onChange={(e) => setAnswers((a) => ({ ...a, [i]: e.target.value }))}
              rows={3}
              className="w-full bg-zinc-800 border border-zinc-700 rounded-md px-2.5 py-1.5 text-sm text-zinc-200 outline-none focus:border-indigo-500 resize-none"
            />
          )}

          {q.type === 'text' && (
            <input
              type="text"
              value={String(answers[i] ?? '')}
              onChange={(e) => setAnswers((a) => ({ ...a, [i]: e.target.value }))}
              className="w-full bg-zinc-800 border border-zinc-700 rounded-md px-2.5 py-1.5 text-sm text-zinc-200 outline-none focus:border-indigo-500"
            />
          )}

          {q.type === 'select' && q.options && (
            <select
              value={String(answers[i] ?? '')}
              onChange={(e) => setAnswers((a) => ({ ...a, [i]: e.target.value }))}
              className="w-full bg-zinc-800 border border-zinc-700 rounded-md px-2.5 py-1.5 text-sm text-zinc-200 outline-none focus:border-indigo-500"
            >
              <option value="">Select...</option>
              {q.options.map((opt) => (
                <option key={opt} value={opt}>{opt}</option>
              ))}
            </select>
          )}

          {q.type === 'radio' && q.options && (
            <div className="space-y-1.5">
              {q.options.map((opt) => (
                <label key={opt} className="flex items-center gap-2 text-sm text-zinc-300 cursor-pointer">
                  <input
                    type="radio"
                    name={`question-${i}`}
                    value={opt}
                    checked={answers[i] === opt}
                    onChange={() => setAnswers((a) => ({ ...a, [i]: opt }))}
                    className="accent-indigo-500"
                  />
                  {opt}
                </label>
              ))}
            </div>
          )}

          {q.type === 'checkbox' && q.options && (
            <div className="space-y-1.5">
              {q.options.map((opt) => {
                const selected = Array.isArray(answers[i]) ? answers[i] as string[] : []
                return (
                  <label key={opt} className="flex items-center gap-2 text-sm text-zinc-300 cursor-pointer">
                    <input
                      type="checkbox"
                      value={opt}
                      checked={selected.includes(opt)}
                      onChange={(e) => {
                        setAnswers((a) => {
                          const current = Array.isArray(a[i]) ? [...(a[i] as string[])] : []
                          if (e.target.checked) {
                            return { ...a, [i]: [...current, opt] }
                          } else {
                            return { ...a, [i]: current.filter((v) => v !== opt) }
                          }
                        })
                      }}
                      className="accent-indigo-500"
                    />
                    {opt}
                  </label>
                )
              })}
            </div>
          )}
        </div>
      ))}

      <Button
        size="sm"
        className="bg-indigo-600 hover:bg-indigo-500 text-white text-xs px-4 py-1 h-7"
        onClick={handleSubmit}
      >
        Submit Answers
      </Button>
    </div>
  )
}
