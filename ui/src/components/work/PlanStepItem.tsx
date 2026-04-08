import { useState, useRef, useCallback } from 'react'
import { Check } from 'lucide-react'
import type { PlanStep } from '@/lib/types'

interface PlanStepItemProps {
  step: PlanStep
  onCheck: (stepId: string) => void
  onUncheck: (stepId: string, reason?: string) => void
}

export function PlanStepItem({ step, onCheck, onUncheck }: PlanStepItemProps) {
  const isDone = step.status === 'done'
  const isSkipped = step.status === 'skipped'
  const [reopenFeedback, setReopenFeedback] = useState<string | null>(null)
  const feedbackRef = useRef<HTMLInputElement>(null)

  const handleToggle = useCallback(() => {
    if (isDone) {
      setReopenFeedback('')
      requestAnimationFrame(() => feedbackRef.current?.focus())
    } else if (!isSkipped) {
      onCheck(step.id)
    }
  }, [isDone, isSkipped, step.id, onCheck])

  const handleFeedbackSubmit = useCallback(() => {
    const reason = reopenFeedback?.trim() || undefined
    onUncheck(step.id, reason)
    setReopenFeedback(null)
  }, [reopenFeedback, step.id, onUncheck])

  const handleFeedbackKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter') {
        e.preventDefault()
        handleFeedbackSubmit()
      } else if (e.key === 'Escape') {
        onUncheck(step.id)
        setReopenFeedback(null)
      }
    },
    [handleFeedbackSubmit, onUncheck, step.id],
  )

  const isReopening = reopenFeedback !== null

  return (
    <div>
      <div className="flex items-center gap-1.5 py-0.5">
        <button
          type="button"
          onClick={handleToggle}
          disabled={isSkipped}
          className={`w-2.5 h-2.5 rounded-sm border-2 flex items-center justify-center shrink-0 transition-colors ${
            isDone
              ? 'bg-primary border-primary'
              : isSkipped
                ? 'border-fg-faint/30 cursor-not-allowed'
                : 'border-border-subtle hover:border-primary/60'
          }`}
        >
          {isDone && <Check className="w-2 h-2 text-white" />}
        </button>
        <span
          className={`text-[11px] ${
            isDone
              ? 'text-fg-muted line-through'
              : isSkipped
                ? 'text-fg-faint line-through'
                : step.status === 'in_progress'
                  ? 'text-fg'
                  : 'text-fg-secondary'
          }`}
        >
          {step.title}
        </span>
        {isReopening && (
          <span className="text-[9px] text-warning ml-auto">reopened</span>
        )}
      </div>
      {isReopening && (
        <div className="flex gap-1.5 ml-4 mt-0.5 mb-1">
          <input
            ref={feedbackRef}
            type="text"
            value={reopenFeedback}
            onChange={(e) => setReopenFeedback(e.target.value)}
            onKeyDown={handleFeedbackKeyDown}
            placeholder="Why?"
            className="flex-1 bg-bg border border-border rounded px-1.5 py-0.5 text-[10px] text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary"
          />
          <button
            type="button"
            onClick={handleFeedbackSubmit}
            className="px-1.5 py-0.5 bg-primary text-white text-[10px] rounded hover:bg-primary/80"
          >
            OK
          </button>
        </div>
      )}
    </div>
  )
}
