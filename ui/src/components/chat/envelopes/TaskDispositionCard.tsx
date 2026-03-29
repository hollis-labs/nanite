import { useState } from 'react'
import { ClipboardList, Loader2, CheckCircle2, MessageSquare } from 'lucide-react'
import { Button } from '@/components/ui/Button'

interface TaskItem {
  id: string
  title: string
  status: string
  priority: string
}

interface TaskDispositionData {
  title: string
  description?: string
  tasks: TaskItem[]
  actions?: string[]
}

interface TaskDispositionCardProps {
  data: TaskDispositionData
  onSendMessage?: (content: string) => void
}

const DEFAULT_ACTIONS = ['Approve', 'Done', 'Request Changes', 'Defer', 'Skip']

const PRIORITY_COLOR: Record<string, string> = {
  P1: 'bg-red-500/20 text-red-400 border-red-500/25',
  P2: 'bg-amber-500/20 text-amber-400 border-amber-500/25',
  P3: 'bg-blue-500/20 text-blue-400 border-blue-500/25',
}

const STATUS_COLOR: Record<string, string> = {
  todo: 'bg-surface/50 text-fg-secondary',
  doing: 'bg-blue-500/20 text-blue-400',
  blocked: 'bg-red-500/20 text-red-400',
  done: 'bg-emerald-500/20 text-emerald-400',
  queued: 'bg-violet-500/20 text-violet-400',
}

const ACTION_HINT: Record<string, string> = {
  'Approve': '→ queued',
  'Done': '→ done',
  'Request Changes': '→ todo + comment',
  'Defer': '→ backlog',
  'Skip': 'no action',
}

export function TaskDispositionCard({ data, onSendMessage }: TaskDispositionCardProps) {
  const actions = data.actions && data.actions.length > 0 ? data.actions : DEFAULT_ACTIONS
  const hasRequestChanges = actions.includes('Request Changes')

  const [decisions, setDecisions] = useState<Record<string, string>>(() => {
    const init: Record<string, string> = {}
    for (const t of data.tasks) init[t.id] = 'Skip'
    return init
  })
  const [comments, setComments] = useState<Record<string, string>>({})
  const [submitted, setSubmitted] = useState(false)
  const [submitting, setSubmitting] = useState(false)

  const handleSubmit = () => {
    setSubmitting(true)
    const parts: string[] = []
    for (const t of data.tasks) {
      const d = decisions[t.id]
      if (d === 'Skip') continue
      if (d === 'Request Changes' && comments[t.id]) {
        parts.push(`${t.id}=RequestChanges[${comments[t.id]}]`)
      } else {
        parts.push(`${t.id}=${d}`)
      }
    }
    if (parts.length === 0) {
      setSubmitting(false)
      return
    }
    if (onSendMessage) {
      onSendMessage(`DISPOSITION: ${parts.join(', ')}`)
    }
    setSubmitted(true)
    setSubmitting(false)
  }

  const nonSkipCount = Object.values(decisions).filter(d => d !== 'Skip').length

  return (
    <div className="space-y-3 animate-in fade-in duration-300">
      {/* Header */}
      <div className="flex items-center gap-2 text-fg-secondary">
        <ClipboardList className="h-4 w-4 shrink-0" />
        <span className="text-xs font-medium">{data.title}</span>
      </div>

      {data.description && (
        <p className="text-sm text-fg-secondary">{data.description}</p>
      )}

      {/* Task rows */}
      <div className="rounded-lg border border-border-subtle bg-bg-elevated/50 overflow-hidden divide-y divide-border">
        {data.tasks.map(task => {
          const decision = decisions[task.id]
          const showComment = hasRequestChanges && decision === 'Request Changes'

          return (
            <div key={task.id} className="px-4 py-3">
              <div className="flex items-center gap-3">
                {/* Task info */}
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="inline-block rounded bg-surface/60 px-1.5 py-0.5 text-xs font-mono text-fg-secondary">
                      {task.id}
                    </span>
                    <span className={`inline-block rounded px-1.5 py-0.5 text-xs font-medium border ${PRIORITY_COLOR[task.priority] || PRIORITY_COLOR['P3']}`}>
                      {task.priority}
                    </span>
                    <span className={`inline-block rounded-full px-2 py-0.5 text-xs ${STATUS_COLOR[task.status] || STATUS_COLOR['todo']}`}>
                      {task.status}
                    </span>
                  </div>
                  <p className="mt-1 text-sm text-fg truncate">{task.title}</p>
                </div>

                {/* Action dropdown */}
                {!submitted ? (
                  <div className="flex flex-col items-end gap-1">
                    <select
                      value={decision}
                      onChange={e => setDecisions(prev => ({ ...prev, [task.id]: e.target.value }))}
                      className="rounded-md border border-border-subtle bg-surface px-2 py-1.5 text-xs text-fg focus:border-border-subtle focus:outline-none focus:ring-1 focus:ring-border-subtle min-w-[140px]"
                    >
                      {actions.map(action => (
                        <option key={action} value={action}>
                          {action}{ACTION_HINT[action] ? ` (${ACTION_HINT[action]})` : ''}
                        </option>
                      ))}
                    </select>
                  </div>
                ) : (
                  <span className={`text-xs ${decision === 'Skip' ? 'text-fg-faint' : 'text-fg-secondary font-medium'}`}>
                    {decision === 'Skip' ? '—' : decision}
                  </span>
                )}
              </div>

              {/* Comment input for Request Changes */}
              {showComment && !submitted && (
                <div className="mt-2 ml-0 flex items-start gap-2">
                  <MessageSquare className="h-3.5 w-3.5 text-amber-400 mt-1.5 shrink-0" />
                  <input
                    type="text"
                    placeholder="What changes are needed?"
                    value={comments[task.id] || ''}
                    onChange={e => setComments(prev => ({ ...prev, [task.id]: e.target.value }))}
                    className="flex-1 rounded-md border border-amber-500/30 bg-surface/50 px-2.5 py-1.5 text-xs text-fg placeholder:text-fg-faint focus:border-amber-500/50 focus:outline-none focus:ring-1 focus:ring-amber-500/30"
                  />
                </div>
              )}
            </div>
          )
        })}
      </div>

      {/* Submit */}
      {!submitted ? (
        <div className="flex items-center justify-between">
          <span className="text-xs text-fg-muted">
            {nonSkipCount} of {data.tasks.length} tasks will be actioned
          </span>
          <Button
            size="sm"
            disabled={submitting || nonSkipCount === 0}
            onClick={handleSubmit}
            className="bg-emerald-600 hover:bg-emerald-500 text-white text-xs px-4 py-1 h-7"
          >
            {submitting ? (
              <Loader2 className="mr-1.5 h-3 w-3 animate-spin" />
            ) : (
              <CheckCircle2 className="mr-1.5 h-3 w-3" />
            )}
            Submit All
          </Button>
        </div>
      ) : (
        <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-3">
          <div className="flex items-center gap-2">
            <CheckCircle2 className="h-4 w-4 text-emerald-400" />
            <span className="text-sm text-emerald-400">
              Dispositions submitted — agent is processing transitions.
            </span>
          </div>
        </div>
      )}
    </div>
  )
}
