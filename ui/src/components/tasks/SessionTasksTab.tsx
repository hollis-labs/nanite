import { useState, useCallback, useMemo } from 'react'
import { Plus, Play, Check, X, RotateCcw, ChevronDown, ChevronRight, ListTodo } from 'lucide-react'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Tooltip } from '@/components/ui/tooltip'
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { useAppStore } from '@/stores/useAppStore'
import { useSessionTasks, useCreateSessionTask, useTransitionSessionTask } from '@/hooks/useSessionTasks'
import { TaskStatusBadge } from './TaskStatusBadge'
import type { SessionTask, SessionTaskStatus } from '@/lib/types'

export function SessionTasksTab() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const { data: tasks = [], isLoading } = useSessionTasks(activeSessionId)
  const createMutation = useCreateSessionTask()
  const transitionMutation = useTransitionSessionTask()

  const [newTitle, setNewTitle] = useState('')
  const [showCompleted, setShowCompleted] = useState(false)

  const handleCreate = useCallback(() => {
    if (!newTitle.trim() || !activeSessionId) return
    createMutation.mutate(
      { title: newTitle.trim(), session_id: activeSessionId },
      { onSuccess: () => setNewTitle('') },
    )
  }, [newTitle, activeSessionId, createMutation])

  const handleTransition = useCallback(
    (id: string, status: SessionTaskStatus) => {
      transitionMutation.mutate({ id, status })
    },
    [transitionMutation],
  )

  const { active, completed } = useMemo(() => {
    const active: SessionTask[] = []
    const completed: SessionTask[] = []
    for (const t of tasks) {
      if (t.status === 'completed' || t.status === 'failed' || t.status === 'cancelled') {
        completed.push(t)
      } else {
        active.push(t)
      }
    }
    // Sort: in_progress first, then pending
    active.sort((a, b) => {
      if (a.status === 'in_progress' && b.status !== 'in_progress') return -1
      if (a.status !== 'in_progress' && b.status === 'in_progress') return 1
      return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
    })
    completed.sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime())
    return { active, completed }
  }, [tasks])

  return (
    <ScrollArea className="flex-1 min-h-0">
      <div className="p-3 space-y-3">
        {/* Quick-add input */}
        <div className="flex items-center gap-2">
          <div className="relative flex-1">
            <input
              type="text"
              value={newTitle}
              onChange={(e) => setNewTitle(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') handleCreate() }}
              placeholder="Add a task..."
              className="w-full bg-surface/50 border border-border rounded-md pl-8 pr-3 py-1.5 text-xs text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary"
              disabled={createMutation.isPending || !activeSessionId}
            />
            <Plus className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-fg-faint pointer-events-none" />
          </div>
        </div>

        {/* Loading */}
        {isLoading && (
          <div className="space-y-2">
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className="h-10 w-full rounded-md" />
            ))}
          </div>
        )}

        {/* Empty state */}
        {!isLoading && tasks.length === 0 && (
          <Empty className="py-12">
            <EmptyHeader>
              <EmptyMedia variant="icon"><ListTodo /></EmptyMedia>
              <EmptyTitle className="text-sm">No tasks yet</EmptyTitle>
              <EmptyDescription className="text-xs">Add a task to track work in this session</EmptyDescription>
            </EmptyHeader>
          </Empty>
        )}

        {/* Active tasks */}
        {active.length > 0 && (
          <div className="rounded-lg border border-border-subtle overflow-hidden divide-y divide-border/50">
            {active.map((task) => (
              <TaskRow key={task.id} task={task} onTransition={handleTransition} />
            ))}
          </div>
        )}

        {/* Completed/failed/cancelled */}
        {completed.length > 0 && (
          <div>
            <button
              onClick={() => setShowCompleted((v) => !v)}
              className="flex items-center gap-1.5 text-xs text-fg-muted hover:text-fg-secondary transition-colors mb-2"
            >
              {showCompleted ? (
                <ChevronDown className="w-3 h-3" />
              ) : (
                <ChevronRight className="w-3 h-3" />
              )}
              Completed ({completed.length})
            </button>
            {showCompleted && (
              <div className="rounded-lg border border-border-subtle overflow-hidden divide-y divide-border/50 opacity-60">
                {completed.map((task) => (
                  <TaskRow key={task.id} task={task} onTransition={handleTransition} />
                ))}
              </div>
            )}
          </div>
        )}
      </div>
    </ScrollArea>
  )
}

interface TaskRowProps {
  task: SessionTask
  onTransition: (id: string, status: SessionTaskStatus) => void
}

function TaskRow({ task, onTransition }: TaskRowProps) {
  return (
    <div className="flex items-center gap-2 px-3 py-2 bg-bg-elevated/60 hover:bg-bg-elevated transition-colors">
      <div className="flex-1 min-w-0">
        <span className={`text-sm text-fg truncate block ${task.status === 'completed' ? 'line-through text-fg-muted' : ''}`}>
          {task.title}
        </span>
        <TaskStatusBadge status={task.status} className="mt-0.5" />
      </div>
      <div className="flex items-center gap-0.5 shrink-0">
        {task.status === 'pending' && (
          <>
            <Tooltip content="Start" side="bottom">
              <button
                onClick={() => onTransition(task.id, 'in_progress')}
                className="p-1 rounded text-fg-faint hover:text-info hover:bg-surface transition-colors"
              >
                <Play className="w-3.5 h-3.5" />
              </button>
            </Tooltip>
            <Tooltip content="Cancel" side="bottom">
              <button
                onClick={() => onTransition(task.id, 'cancelled')}
                className="p-1 rounded text-fg-faint hover:text-danger hover:bg-surface transition-colors"
              >
                <X className="w-3.5 h-3.5" />
              </button>
            </Tooltip>
          </>
        )}
        {task.status === 'in_progress' && (
          <>
            <Tooltip content="Complete" side="bottom">
              <button
                onClick={() => onTransition(task.id, 'completed')}
                className="p-1 rounded text-fg-faint hover:text-success hover:bg-surface transition-colors"
              >
                <Check className="w-3.5 h-3.5" />
              </button>
            </Tooltip>
            <Tooltip content="Cancel" side="bottom">
              <button
                onClick={() => onTransition(task.id, 'cancelled')}
                className="p-1 rounded text-fg-faint hover:text-danger hover:bg-surface transition-colors"
              >
                <X className="w-3.5 h-3.5" />
              </button>
            </Tooltip>
          </>
        )}
        {task.status === 'failed' && (
          <Tooltip content="Retry" side="bottom">
            <button
              onClick={() => onTransition(task.id, 'pending')}
              className="p-1 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface transition-colors"
            >
              <RotateCcw className="w-3.5 h-3.5" />
            </button>
          </Tooltip>
        )}
      </div>
    </div>
  )
}
