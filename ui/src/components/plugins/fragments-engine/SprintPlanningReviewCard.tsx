import { useState, useMemo } from 'react'
import {
  LayoutList, ChevronDown, ChevronRight, Check, ArrowRightLeft,
  ChevronLeft, ChevronsRight, Send,
} from 'lucide-react'
import { Button } from '@/components/ui/button'

interface SprintOption {
  id: string
  name: string
}

interface PlanningTask {
  id: string
  title: string
  summary?: string
  suggested_sprint: string
  priority: string
  status?: string
}

interface SprintPlanningReviewData {
  title: string
  description?: string
  sprints: SprintOption[]
  tasks: PlanningTask[]
  page_size?: number
}

interface SprintPlanningReviewCardProps {
  data: SprintPlanningReviewData
  onSendMessage?: (content: string) => void
}

const PRIORITY_DOT: Record<string, string> = {
  P1: 'bg-primary',
  P2: 'bg-warning',
  P3: 'bg-success',
}

type TaskAssignment = {
  sprint_id: string
  confirmed: boolean
}

export function SprintPlanningReviewCard({ data, onSendMessage }: SprintPlanningReviewCardProps) {
  const pageSize = data.page_size || 10
  const [page, setPage] = useState(0)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const [assignments, setAssignments] = useState<Record<string, TaskAssignment>>(() => {
    const init: Record<string, TaskAssignment> = {}
    for (const t of data.tasks) {
      init[t.id] = { sprint_id: t.suggested_sprint, confirmed: false }
    }
    return init
  })
  const [finished, setFinished] = useState(false)

  const totalPages = Math.ceil(data.tasks.length / pageSize)
  const pageTasks = useMemo(
    () => data.tasks.slice(page * pageSize, (page + 1) * pageSize),
    [data.tasks, page, pageSize],
  )

  const sprintNames = useMemo(() => {
    const m: Record<string, string> = {}
    for (const s of data.sprints) m[s.id] = s.name
    return m
  }, [data.sprints])

  const confirmedCount = Object.values(assignments).filter(a => a.confirmed).length

  // Confirm a task locally — no message sent yet
  const confirmTask = (taskId: string) => {
    setAssignments(prev => {
      const existing = prev[taskId]
      if (!existing || existing.confirmed) return prev
      return { ...prev, [taskId]: { sprint_id: existing.sprint_id, confirmed: true } }
    })
  }

  // Change sprint assignment
  const changeSprint = (taskId: string, newSprintId: string) => {
    setAssignments(prev => {
      const existing = prev[taskId]
      if (!existing) return prev
      return { ...prev, [taskId]: { sprint_id: newSprintId, confirmed: false } }
    })
  }

  // Accept all on current page — local only
  const confirmAllOnPage = () => {
    setAssignments(prev => {
      const next = { ...prev }
      for (const t of pageTasks) {
        const existing = next[t.id]
        if (existing && !existing.confirmed) {
          next[t.id] = { sprint_id: existing.sprint_id, confirmed: true }
        }
      }
      return next
    })
  }

  // Accept all remaining — local only
  const confirmAllRemaining = () => {
    setAssignments(prev => {
      const next = { ...prev }
      for (const t of data.tasks) {
        const existing = next[t.id]
        if (existing && !existing.confirmed) {
          next[t.id] = { sprint_id: existing.sprint_id, confirmed: true }
        }
      }
      return next
    })
  }

  // FINISH — send one batch message with all assignments
  const handleFinish = () => {
    const parts: string[] = []
    for (const t of data.tasks) {
      const a = assignments[t.id]
      if (a) {
        parts.push(`${t.id}=${a.sprint_id}`)
      }
    }
    if (parts.length > 0 && onSendMessage) {
      onSendMessage(`SPRINT_ASSIGN: ${parts.join(', ')}`)
    }
    setFinished(true)
  }

  return (
    <div className="space-y-3 animate-in fade-in duration-300">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 text-fg-secondary">
          <LayoutList className="h-4 w-4 shrink-0" />
          <span className="text-xs font-medium">{data.title}</span>
        </div>
        <div className="flex items-center gap-3 text-xs text-fg-muted">
          <span>
            <span className="text-success font-medium">{confirmedCount}</span>/{data.tasks.length} confirmed
          </span>
          {/* Sprint legend */}
          <div className="flex items-center gap-1.5">
            {data.sprints.map(s => (
              <span
                key={s.id}
                className="inline-block rounded bg-surface px-1.5 py-0.5 text-[10px] text-fg-secondary border border-border-subtle"
                title={s.name}
              >
                {s.name.length > 20 ? s.name.slice(0, 20) + '…' : s.name}
              </span>
            ))}
          </div>
        </div>
      </div>

      {data.description && (
        <p className="text-sm text-fg-secondary">{data.description}</p>
      )}

      {/* Task list */}
      <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 overflow-hidden divide-y divide-border">
        {pageTasks.map(task => {
          const a = assignments[task.id]
          const isExpanded = expanded[task.id] || false
          const assignedName = a ? (sprintNames[a.sprint_id] || a.sprint_id) : ''
          const isOriginal = a?.sprint_id === task.suggested_sprint

          return (
            <div key={task.id} className="group">
              {/* Task row */}
              <div className="flex items-center gap-3 px-4 py-2.5">
                {/* Expand toggle */}
                <button
                  type="button"
                  onClick={() => setExpanded(prev => ({ ...prev, [task.id]: !prev[task.id] }))}
                  className="shrink-0 text-fg-muted hover:text-fg-secondary transition-colors"
                >
                  {isExpanded ? (
                    <ChevronDown className="h-4 w-4" />
                  ) : (
                    <ChevronRight className="h-4 w-4" />
                  )}
                </button>

                {/* Priority dot */}
                <span
                  className={`h-2 w-2 rounded-full shrink-0 ${PRIORITY_DOT[task.priority] || PRIORITY_DOT['P3']}`}
                  title={task.priority}
                />

                {/* Title + ID */}
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="text-[10px] font-mono text-fg-faint">{task.id}</span>
                    <span className="text-sm text-fg truncate">{task.title}</span>
                  </div>
                </div>

                {/* Sprint assignment + action */}
                <div className="flex items-center gap-2 shrink-0">
                  {a?.confirmed ? (
                    <div className="flex items-center gap-1.5">
                      <Check className="h-3.5 w-3.5 text-success" />
                      <span className="text-xs text-fg-secondary">{assignedName}</span>
                    </div>
                  ) : (
                    <>
                      <select
                        value={a?.sprint_id || task.suggested_sprint}
                        onChange={e => changeSprint(task.id, e.target.value)}
                        disabled={finished}
                        className="rounded border border-border-subtle bg-surface px-1.5 py-1 text-[11px] text-fg-secondary focus:border-border-subtle focus:outline-none min-w-[130px]"
                      >
                        {data.sprints.map(s => (
                          <option key={s.id} value={s.id}>
                            {s.id === task.suggested_sprint ? `★ ${s.name}` : s.name}
                          </option>
                        ))}
                      </select>

                      <Button
                        size="sm"
                        onClick={() => confirmTask(task.id)}
                        disabled={finished}
                        className={`h-7 px-2.5 text-xs ${
                          isOriginal
                            ? 'bg-success hover:bg-success/80 text-white'
                            : 'bg-warning hover:bg-amber-500 text-white'
                        }`}
                      >
                        {isOriginal ? (
                          <>
                            <Check className="mr-1 h-3 w-3" />
                            Add
                          </>
                        ) : (
                          <>
                            <ArrowRightLeft className="mr-1 h-3 w-3" />
                            Move
                          </>
                        )}
                      </Button>
                    </>
                  )}
                </div>
              </div>

              {/* Expanded summary */}
              {isExpanded && task.summary && (
                <div className="px-4 pb-3 pl-12 border-t border-border/50">
                  <p className="text-xs text-fg-secondary mt-2 leading-relaxed whitespace-pre-wrap">
                    {task.summary.length > 300 ? task.summary.slice(0, 300) + '…' : task.summary}
                  </p>
                </div>
              )}
            </div>
          )
        })}
      </div>

      {/* Pagination + actions */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            variant="ghost"
            disabled={page === 0}
            onClick={() => setPage(p => p - 1)}
            className="h-7 px-2 text-xs"
          >
            <ChevronLeft className="h-3.5 w-3.5" />
          </Button>
          <span className="text-xs text-fg-muted">
            Page {page + 1} of {totalPages}
          </span>
          <Button
            size="sm"
            variant="ghost"
            disabled={page >= totalPages - 1}
            onClick={() => setPage(p => p + 1)}
            className="h-7 px-2 text-xs"
          >
            <ChevronsRight className="h-3.5 w-3.5" />
          </Button>
        </div>

        <div className="flex items-center gap-2">
          {!finished && (
            <>
              <Button
                size="sm"
                variant="ghost"
                onClick={confirmAllOnPage}
                className="h-7 text-xs text-fg-secondary"
              >
                Accept Page
              </Button>
              {confirmedCount < data.tasks.length && (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={confirmAllRemaining}
                  className="h-7 text-xs"
                >
                  <Check className="mr-1 h-3 w-3" />
                  Accept All
                </Button>
              )}
              <Button
                size="sm"
                onClick={handleFinish}
                disabled={confirmedCount === 0}
                className="bg-success hover:bg-success/80 text-white h-7 text-xs px-4"
              >
                <Send className="mr-1.5 h-3 w-3" />
                Finish ({confirmedCount})
              </Button>
            </>
          )}
        </div>
      </div>

      {/* Completion state */}
      {finished && (
        <div className="rounded-sm border border-success/30 bg-success/5 p-3">
          <div className="flex items-center gap-2">
            <Check className="h-4 w-4 text-success" />
            <span className="text-sm text-success">
              {confirmedCount} tasks assigned to sprints — agent is updating Engine.
            </span>
          </div>
        </div>
      )}
    </div>
  )
}
