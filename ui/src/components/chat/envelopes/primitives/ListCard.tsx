import { useQuery } from '@tanstack/react-query'
import { Check, List, ChevronRight } from 'lucide-react'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { useTogglePlanStep } from '@/hooks/usePlans'
import type { PlanStepStatus } from '@/lib/types'
import { Envelope, EnvelopeHeader, EnvelopeBody } from './Envelope'
import { StatusPill } from './StatusPill'

interface ListItem {
  label: string
  description?: string
  icon?: string
  /** Stable item id — required for a `plans`-sourced item to be toggleable. */
  id?: string
  /** Checkbox/done-state. Todos use pending/done; plan steps use all four. */
  status?: PlanStepStatus
  action?: { label: string; type: string }
}

interface ListCardDataSource {
  /**
   * "plans" re-fetches a single plan's steps via `plan_id` and enables
   * per-step toggle through the same mutation the old standalone
   * `plan-review` envelope used (useTogglePlanStep). "todos" is handled
   * by the sibling todo-list composition — see
   * TASKS/phase-6/01-rebuild-todo-list-as-composition.md.
   */
  kind: 'todos' | 'plans'
  scope?: string
  scope_id?: string
  plan_id?: string
}

interface ListCardData {
  title?: string
  items: ListItem[]
  ordered?: boolean
  data_source?: ListCardDataSource
}

interface ListCardProps {
  data: ListCardData
  onSendMessage?: (content: string) => void
}

export function ListCard({ data, onSendMessage }: ListCardProps) {
  // Live plan-steps composition (TASKS/phase-6/02-rebuild-plan-review-as-
  // composition.md) — the standalone `plan-review` type is retired; a
  // `list-card` with `data_source.kind === "plans"` renders the same live,
  // toggleable step list PlanReviewCard used to.
  if (data.data_source?.kind === 'plans' && data.data_source.plan_id) {
    return <PlanStepsListCard data={data} planId={data.data_source.plan_id} />
  }

  // `items` is required by the type, but a partially-loaded envelope can
  // arrive without it — normalize so the card degrades gracefully.
  const items = data.items ?? []
  return (
    <Envelope>
      <EnvelopeHeader
        icon={List}
        label={data.ordered ? 'Ordered list' : 'List'}
        meta={`${items.length} item${items.length === 1 ? '' : 's'}`}
      />
      <EnvelopeBody title={data.title}>
        <div className="space-y-2">
          {items.map((item, i) => (
            <div key={`item-${i}`} className="flex items-start gap-3">
              <span className="mt-0.5 w-5 shrink-0 text-right font-mono text-[11px] text-fg-muted">
                {data.ordered ? `${i + 1}.` : '•'}
              </span>
              <div className="min-w-0 flex-1">
                <span className="text-[13px] text-fg">{item.label}</span>
                {item.description && (
                  <p className="mt-0.5 text-[12px] text-fg-muted">{item.description}</p>
                )}
              </div>
              {item.action && onSendMessage && (
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-6 shrink-0 px-2 text-xs"
                  onClick={() => onSendMessage(item.action!.type)}
                >
                  {item.action.label}
                  <ChevronRight className="ml-1 h-3 w-3" />
                </Button>
              )}
            </div>
          ))}
        </div>
      </EnvelopeBody>
    </Envelope>
  )
}

interface LiveStep {
  id: string
  title: string
  status: PlanStepStatus
}

/**
 * Live plan-steps rendering — the `list-card` half of the `plan-review`
 * composition. Re-fetches the plan via `useQuery(['plans', plan_id])`
 * (the same key PlanReviewCard used) so it stays in sync with the
 * `confirmation-card` half and with any other actor touching the plan,
 * and toggles steps via `useTogglePlanStep` — an identical mutation path
 * to the retired PlanReviewCard's StepRow.
 */
function PlanStepsListCard({ data, planId }: { data: ListCardData; planId: string }) {
  const { data: plan } = useQuery({
    queryKey: ['plans', planId],
    queryFn: () => api.getPlan(planId),
  })
  const toggleStep = useTogglePlanStep()

  // Fall back to the static snapshot in `items` until the live fetch
  // resolves (or if it never does) — mirrors PlanReviewCard's own
  // `plan?.steps ?? data.steps` graceful-degradation guard.
  const steps: LiveStep[] =
    plan?.steps.map((s) => ({ id: s.id, title: s.title, status: s.status })) ??
    (data.items ?? []).map((item) => ({
      id: item.id ?? '',
      title: item.label,
      status: item.status ?? 'pending',
    }))

  const doneCount = steps.filter((s) => s.status === 'done').length
  const totalCount = steps.length
  const progressPct = totalCount > 0 ? (doneCount / totalCount) * 100 : 0

  return (
    <Envelope>
      <EnvelopeHeader
        icon={List}
        label={data.title || plan?.title || 'Plan steps'}
        meta={totalCount > 0 ? `${doneCount}/${totalCount} complete` : undefined}
      />
      <EnvelopeBody>
        {totalCount === 0 ? (
          <span className="text-[13px] text-fg-faint">No steps</span>
        ) : (
          <>
            <div className="mb-3.5 space-y-0.5">
              {steps.map((step) => (
                <PlanStepRow
                  key={step.id}
                  step={step}
                  onCheck={() => toggleStep.check(planId, step.id)}
                  onUncheck={() => toggleStep.uncheck(planId, step.id)}
                />
              ))}
            </div>
            <div className="flex items-center gap-2.5">
              <div className="h-1 flex-1 overflow-hidden rounded-full bg-surface">
                <div
                  className="h-full rounded-full bg-success transition-all duration-300"
                  style={{ width: `${progressPct}%` }}
                />
              </div>
              <span className="font-mono text-[10px] text-fg-muted">
                {Math.round(progressPct)}%
              </span>
            </div>
          </>
        )}
      </EnvelopeBody>
    </Envelope>
  )
}

/** Lifted from the retired PlanReviewCard's StepRow — same markup/behavior. */
function PlanStepRow({
  step,
  onCheck,
  onUncheck,
}: {
  step: LiveStep
  onCheck: () => void
  onUncheck: () => void
}) {
  const isDone = step.status === 'done'
  const isActive = step.status === 'in_progress'
  const isSkipped = step.status === 'skipped'

  return (
    <div
      className={`flex items-center gap-2.5 rounded-[6px] px-2 py-1.5 ${
        isActive ? 'bg-primary-muted/80' : 'bg-transparent'
      }`}
    >
      <button
        type="button"
        onClick={() => {
          if (isDone) onUncheck()
          else if (!isSkipped) onCheck()
        }}
        disabled={isSkipped}
        className={`flex h-4 w-4 shrink-0 items-center justify-center rounded-[4px] transition-colors ${
          isDone
            ? 'bg-success text-success-fg'
            : isActive
              ? 'border border-primary text-primary'
              : isSkipped
                ? 'border border-border-subtle opacity-40'
                : 'border border-border-subtle text-transparent hover:border-primary'
        }`}
        aria-label={isDone ? 'Mark incomplete' : 'Mark complete'}
      >
        {isDone && <Check className="h-2.5 w-2.5" strokeWidth={3} />}
        {isActive && <span className="h-1.5 w-1.5 rounded-full bg-primary" />}
      </button>

      <span
        className={`min-w-0 flex-1 text-[13px] ${
          isDone
            ? 'text-fg-muted line-through'
            : isSkipped
              ? 'text-fg-faint line-through'
              : isActive
                ? 'font-medium text-fg'
                : 'text-fg-secondary'
        }`}
      >
        {step.title}
      </span>

      {isActive && <StatusPill tone="primary">In progress</StatusPill>}
    </div>
  )
}
