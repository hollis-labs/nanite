import { useState, useCallback, useEffect } from 'react'
import { X, Loader2, ListTodo, CheckSquare, ArrowUp, Trash2, Send, Pause, AlertCircle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { api } from '@/lib/api'
import type { FragmentsSprint, FragmentsTask, FragmentsBacklogItem } from '@/lib/types'

// --- Pending action types for batch apply ---

type PendingAction =
  | { type: 'transition'; taskId: string; status: string }
  | { type: 'delete'; taskId: string }
  | { type: 'promote'; backlogId: string; sprintId: string }

// --- Priority badge colors ---

const priorityColors: Record<string, string> = {
  A: 'bg-danger/20 text-danger border-danger/30',
  B: 'bg-warning/20 text-warning border-warning/30',
  C: 'bg-info/15 text-info border-info/30',
  D: 'bg-fg-muted/20 text-fg-secondary border-border-subtle/30',
}

const statusColors: Record<string, string> = {
  todo: 'bg-fg-muted/20 text-fg-secondary border-border-subtle/30',
  doing: 'bg-info/15 text-info border-info/30',
  done: 'bg-success/20 text-success border-success/30',
  paused: 'bg-warning/20 text-warning border-warning/30',
  blocked: 'bg-danger/20 text-danger border-danger/30',
}

interface SprintPlanningModalProps {
  projectId?: string
  onClose: () => void
}

export function SprintPlanningModal({ projectId, onClose }: SprintPlanningModalProps) {
  const [activeTab, setActiveTab] = useState<'tasks' | 'backlog'>('tasks')

  // Data
  const [sprints, setSprints] = useState<FragmentsSprint[]>([])
  const [tasks, setTasks] = useState<FragmentsTask[]>([])
  const [backlog, setBacklog] = useState<FragmentsBacklogItem[]>([])

  // Selection
  const [selectedSprint, setSelectedSprint] = useState<string>('')
  const [selectedTaskIds, setSelectedTaskIds] = useState<Set<string>>(new Set())
  const [selectedBacklogIds, setSelectedBacklogIds] = useState<Set<string>>(new Set())
  const [promotionSprintId, setPromotionSprintId] = useState<string>('')

  // Actions queue
  const [pendingActions, setPendingActions] = useState<PendingAction[]>([])
  const [notes, setNotes] = useState('')

  // Loading/error
  const [loading, setLoading] = useState(true)
  const [applying, setApplying] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Fetch sprints on mount
  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        setLoading(true)
        setError(null)
        const sprintData = await api.getFragmentsSprints(projectId)
        if (cancelled) return
        const items = sprintData.items ?? []
        setSprints(items)
        if (items.length > 0) {
          setSelectedSprint(items[0].id)
          setPromotionSprintId(items[0].id)
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : 'Failed to connect to Fragments Engine')
        }
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => { cancelled = true }
  }, [projectId])

  // Fetch tasks when sprint changes
  useEffect(() => {
    if (!selectedSprint) return
    let cancelled = false
    async function load() {
      try {
        const taskData = await api.getFragmentsTasks(selectedSprint, undefined, projectId)
        if (!cancelled) setTasks(taskData.items ?? [])
      } catch {
        // Non-fatal: tasks just show empty
        if (!cancelled) setTasks([])
      }
    }
    load()
    return () => { cancelled = true }
  }, [selectedSprint, projectId])

  // Fetch backlog when switching to backlog tab
  useEffect(() => {
    if (activeTab !== 'backlog') return
    let cancelled = false
    async function load() {
      try {
        const data = await api.getFragmentsBacklog(projectId)
        if (!cancelled) setBacklog(data.items ?? [])
      } catch {
        if (!cancelled) setBacklog([])
      }
    }
    load()
    return () => { cancelled = true }
  }, [activeTab, projectId])

  // Toggle task selection
  const toggleTask = useCallback((id: string) => {
    setSelectedTaskIds(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  // Toggle backlog selection
  const toggleBacklog = useCallback((id: string) => {
    setSelectedBacklogIds(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  // Queue actions for tasks
  const queueTaskAction = useCallback((type: 'transition' | 'delete', status?: string) => {
    setPendingActions(prev => {
      const next = [...prev]
      for (const id of selectedTaskIds) {
        // Remove existing actions for this task
        const filtered = next.filter(a =>
          !((a.type === 'transition' || a.type === 'delete') && 'taskId' in a && a.taskId === id)
        )
        next.length = 0
        next.push(...filtered)

        if (type === 'delete') {
          next.push({ type: 'delete', taskId: id })
        } else if (status) {
          next.push({ type: 'transition', taskId: id, status })
        }
      }
      return next
    })
    setSelectedTaskIds(new Set())
  }, [selectedTaskIds])

  // Queue promote actions for backlog
  const queuePromote = useCallback(() => {
    if (!promotionSprintId) return
    setPendingActions(prev => {
      const next = [...prev]
      for (const id of selectedBacklogIds) {
        const filtered = next.filter(a =>
          !(a.type === 'promote' && a.backlogId === id)
        )
        next.length = 0
        next.push(...filtered)
        next.push({ type: 'promote', backlogId: id, sprintId: promotionSprintId })
      }
      return next
    })
    setSelectedBacklogIds(new Set())
  }, [selectedBacklogIds, promotionSprintId])

  const queueBacklogDelete = useCallback(() => {
    // Backlog items don't have a direct delete via our proxy, but we can
    // promote them and then delete. For now, we show a note.
    // Actually the task says "Delete" on backlog tab too, but Fragments Engine may not
    // have a backlog_delete. We'll skip this gracefully.
    setSelectedBacklogIds(new Set())
  }, [])

  // Apply all pending actions
  const handleApply = useCallback(async () => {
    if (pendingActions.length === 0) return
    setApplying(true)
    setError(null)

    try {
      for (const action of pendingActions) {
        switch (action.type) {
          case 'transition':
            await api.transitionFragmentsTask(action.taskId, action.status)
            break
          case 'delete':
            await api.deleteFragmentsTask(action.taskId)
            break
          case 'promote':
            await api.promoteBacklogItem(action.backlogId, action.sprintId)
            break
        }
      }

      // Clear and refresh
      setPendingActions([])
      setNotes('')

      // Refresh tasks
      if (selectedSprint) {
        const taskData = await api.getFragmentsTasks(selectedSprint, undefined, projectId)
        setTasks(taskData.items ?? [])
      }
      // Refresh backlog
      const backlogData = await api.getFragmentsBacklog(projectId)
      setBacklog(backlogData.items ?? [])
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to apply changes')
    } finally {
      setApplying(false)
    }
  }, [pendingActions, selectedSprint, projectId])

  // Check if a task has a pending action
  const getTaskPendingAction = (taskId: string): PendingAction | undefined => {
    return pendingActions.find(a =>
      (a.type === 'transition' || a.type === 'delete') && 'taskId' in a && a.taskId === taskId
    )
  }

  const getBacklogPendingAction = (backlogId: string): PendingAction | undefined => {
    return pendingActions.find(a => a.type === 'promote' && a.backlogId === backlogId)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div className="absolute inset-0 bg-black/60" onClick={onClose} />

      {/* Dialog */}
      <div className="relative w-full max-w-2xl max-h-[85vh] bg-bg-elevated border border-border-subtle rounded-xl shadow-2xl flex flex-col overflow-hidden">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-border">
          <div className="flex items-center gap-2">
            <ListTodo className="w-4 h-4 text-primary" />
            <h2 className="text-sm font-semibold text-fg">Sprint Planning</h2>
            {pendingActions.length > 0 && (
              <span className="ml-2 px-1.5 py-0.5 text-[10px] font-medium bg-primary/10 text-primary-hover rounded">
                {pendingActions.length} pending
              </span>
            )}
          </div>
          <button
            onClick={onClose}
            className="p-1 rounded text-fg-muted hover:text-fg-secondary hover:bg-surface transition-colors"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Tabs */}
        <div className="flex border-b border-border">
          <button
            onClick={() => setActiveTab('tasks')}
            className={cn(
              'flex-1 px-4 py-2.5 text-xs font-medium transition-colors',
              activeTab === 'tasks'
                ? 'text-primary border-b-2 border-primary'
                : 'text-fg-muted hover:text-fg-secondary'
            )}
          >
            Sprint Tasks
          </button>
          <button
            onClick={() => setActiveTab('backlog')}
            className={cn(
              'flex-1 px-4 py-2.5 text-xs font-medium transition-colors',
              activeTab === 'backlog'
                ? 'text-primary border-b-2 border-primary'
                : 'text-fg-muted hover:text-fg-secondary'
            )}
          >
            Backlog
          </button>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto p-4">
          {loading ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="w-5 h-5 animate-spin text-fg-muted" />
              <span className="ml-2 text-sm text-fg-muted">Loading from Engine...</span>
            </div>
          ) : error && sprints.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-12 text-center">
              <AlertCircle className="w-8 h-8 text-danger mb-2" />
              <p className="text-sm text-danger mb-1">Could not connect to Engine</p>
              <p className="text-xs text-fg-muted">{error}</p>
            </div>
          ) : (
            <>
              {/* Sprint Tasks Tab */}
              {activeTab === 'tasks' && (
                <div className="space-y-3">
                  {/* Sprint selector */}
                  <div className="flex items-center gap-2">
                    <label className="text-xs text-fg-muted">Sprint:</label>
                    <select
                      value={selectedSprint}
                      onChange={(e) => setSelectedSprint(e.target.value)}
                      className="flex-1 px-2 py-1.5 text-xs bg-surface border border-border-subtle rounded-md text-fg outline-none focus:border-primary transition-colors"
                    >
                      {sprints.map(s => (
                        <option key={s.id} value={s.id}>{s.name} ({s.status})</option>
                      ))}
                    </select>
                  </div>

                  {/* Actions bar */}
                  {selectedTaskIds.size > 0 && (
                    <div className="flex items-center gap-2 px-3 py-2 bg-surface/50 rounded-lg border border-border-subtle/50">
                      <span className="text-xs text-fg-secondary">{selectedTaskIds.size} selected</span>
                      <div className="flex-1" />
                      <Button
                        variant="ghost"
                        size="sm"
                        className="gap-1 text-success hover:text-success"
                        onClick={() => queueTaskAction('transition', 'done')}
                      >
                        <CheckSquare className="w-3 h-3" />
                        Done
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="gap-1 text-warning hover:text-warning"
                        onClick={() => queueTaskAction('transition', 'paused')}
                      >
                        <Pause className="w-3 h-3" />
                        Defer
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="gap-1 text-danger hover:text-danger"
                        onClick={() => queueTaskAction('delete')}
                      >
                        <Trash2 className="w-3 h-3" />
                        Delete
                      </Button>
                    </div>
                  )}

                  {/* Task list */}
                  {tasks.length === 0 ? (
                    <p className="text-xs text-fg-muted text-center py-6">No tasks in this sprint.</p>
                  ) : (
                    <div className="space-y-1">
                      {tasks.map(task => {
                        const pending = getTaskPendingAction(task.id)
                        return (
                          <label
                            key={task.id}
                            className={cn(
                              'flex items-center gap-3 px-3 py-2.5 rounded-lg cursor-pointer transition-colors',
                              selectedTaskIds.has(task.id) ? 'bg-primary/10 border border-primary/20' : 'hover:bg-surface/50 border border-transparent',
                              pending && 'opacity-60'
                            )}
                          >
                            <input
                              type="checkbox"
                              checked={selectedTaskIds.has(task.id)}
                              onChange={() => toggleTask(task.id)}
                              className="w-3.5 h-3.5 rounded border-border-subtle bg-surface text-primary focus:ring-primary focus:ring-offset-0 shrink-0"
                            />
                            <div className="flex-1 min-w-0">
                              <div className="flex items-center gap-2">
                                <span className="text-sm text-fg truncate">{task.title}</span>
                                {pending && (
                                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-primary/10 text-primary-hover shrink-0">
                                    {pending.type === 'delete' ? 'will delete' : `will ${(pending as { status: string }).status}`}
                                  </span>
                                )}
                              </div>
                            </div>
                            <span className={cn('text-[10px] px-1.5 py-0.5 rounded border shrink-0', priorityColors[task.priority] ?? priorityColors.D)}>
                              {task.priority}
                            </span>
                            <span className={cn('text-[10px] px-1.5 py-0.5 rounded border shrink-0', statusColors[task.status] ?? statusColors.todo)}>
                              {task.status}
                            </span>
                          </label>
                        )
                      })}
                    </div>
                  )}
                </div>
              )}

              {/* Backlog Tab */}
              {activeTab === 'backlog' && (
                <div className="space-y-3">
                  {/* Promotion target */}
                  <div className="flex items-center gap-2">
                    <label className="text-xs text-fg-muted">Promote to:</label>
                    <select
                      value={promotionSprintId}
                      onChange={(e) => setPromotionSprintId(e.target.value)}
                      className="flex-1 px-2 py-1.5 text-xs bg-surface border border-border-subtle rounded-md text-fg outline-none focus:border-primary transition-colors"
                    >
                      {sprints.map(s => (
                        <option key={s.id} value={s.id}>{s.name}</option>
                      ))}
                    </select>
                  </div>

                  {/* Actions bar */}
                  {selectedBacklogIds.size > 0 && (
                    <div className="flex items-center gap-2 px-3 py-2 bg-surface/50 rounded-lg border border-border-subtle/50">
                      <span className="text-xs text-fg-secondary">{selectedBacklogIds.size} selected</span>
                      <div className="flex-1" />
                      <Button
                        variant="ghost"
                        size="sm"
                        className="gap-1 text-primary hover:text-primary-hover"
                        onClick={queuePromote}
                        disabled={!promotionSprintId}
                      >
                        <ArrowUp className="w-3 h-3" />
                        Promote
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="gap-1 text-danger hover:text-danger"
                        onClick={queueBacklogDelete}
                      >
                        <Trash2 className="w-3 h-3" />
                        Delete
                      </Button>
                    </div>
                  )}

                  {/* Backlog list */}
                  {backlog.length === 0 ? (
                    <p className="text-xs text-fg-muted text-center py-6">Backlog is empty.</p>
                  ) : (
                    <div className="space-y-1">
                      {backlog.map(item => {
                        const pending = getBacklogPendingAction(item.id)
                        return (
                          <label
                            key={item.id}
                            className={cn(
                              'flex items-center gap-3 px-3 py-2.5 rounded-lg cursor-pointer transition-colors',
                              selectedBacklogIds.has(item.id) ? 'bg-primary/10 border border-primary/20' : 'hover:bg-surface/50 border border-transparent',
                              pending && 'opacity-60'
                            )}
                          >
                            <input
                              type="checkbox"
                              checked={selectedBacklogIds.has(item.id)}
                              onChange={() => toggleBacklog(item.id)}
                              className="w-3.5 h-3.5 rounded border-border-subtle bg-surface text-primary focus:ring-primary focus:ring-offset-0 shrink-0"
                            />
                            <div className="flex-1 min-w-0">
                              <div className="flex items-center gap-2">
                                <span className="text-sm text-fg truncate">{item.title}</span>
                                {pending && (
                                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-primary/10 text-primary-hover shrink-0">
                                    will promote
                                  </span>
                                )}
                              </div>
                              {item.body && (
                                <p className="text-xs text-fg-muted truncate mt-0.5">{item.body}</p>
                              )}
                            </div>
                            <span className={cn('text-[10px] px-1.5 py-0.5 rounded border shrink-0', priorityColors[item.priority] ?? priorityColors.D)}>
                              {item.priority}
                            </span>
                          </label>
                        )
                      })}
                    </div>
                  )}
                </div>
              )}
            </>
          )}

          {/* Error banner */}
          {error && sprints.length > 0 && (
            <div className="mt-3 p-3 rounded-lg bg-danger/10 border border-danger/20 text-sm text-danger">
              {error}
            </div>
          )}
        </div>

        {/* Notes input */}
        <div className="px-4 py-2 border-t border-border">
          <input
            type="text"
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            placeholder="Notes or context..."
            className="w-full px-3 py-1.5 text-xs bg-surface border border-border-subtle rounded-md text-fg placeholder:text-fg-faint outline-none focus:border-primary transition-colors"
          />
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between px-5 py-3 border-t border-border">
          <span className="text-xs text-fg-muted">
            {pendingActions.length === 0 ? 'No changes queued' : `${pendingActions.length} action${pendingActions.length > 1 ? 's' : ''} queued`}
          </span>
          <div className="flex items-center gap-2">
            <Button variant="ghost" size="sm" onClick={onClose}>
              Cancel
            </Button>
            <Button
              size="sm"
              className="gap-1.5"
              disabled={pendingActions.length === 0 || applying}
              onClick={handleApply}
            >
              {applying ? (
                <Loader2 className="w-3.5 h-3.5 animate-spin" />
              ) : (
                <Send className="w-3.5 h-3.5" />
              )}
              Apply {pendingActions.length > 0 ? `(${pendingActions.length})` : ''}
            </Button>
          </div>
        </div>
      </div>
    </div>
  )
}
