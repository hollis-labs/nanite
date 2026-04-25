import { useCallback, useMemo, useState } from 'react'
import { ChevronDown, ChevronRight, ListTodo } from 'lucide-react'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Skeleton } from '@/components/ui/skeleton'
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from '@/components/ui/empty'
import { useAppStore } from '@/stores/useAppStore'
import { useWorkStore } from '@/stores/useWorkStore'
import { useTodos, useCreateTodo, useToggleTodo, useUpdateTodo } from '@/hooks/useTodos'
import { usePlans, useCreatePlan, useUpdatePlan, useTogglePlanStep } from '@/hooks/usePlans'
import { useWorkSync } from '@/hooks/useWorkSync'
import { TodoList } from './TodoList'
import { PlanCard } from './PlanCard'
import { AddItemInput } from './AddItemInput'
import { arrayMove } from '@dnd-kit/sortable'

export function WorkTab() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const activeProjectId = useAppStore((s) => s.activeProjectId)
  const scope = useWorkStore((s) => s.scope)
  const setScope = useWorkStore((s) => s.setScope)
  const { dirty, changeCount, flush } = useWorkSync()

  const scopeId = scope === 'session' ? activeSessionId : activeProjectId

  const { data: todos = [], isLoading: todosLoading } = useTodos({
    scope,
    scope_id: scopeId ?? undefined,
  })
  const { data: plans = [], isLoading: plansLoading } = usePlans({
    scope,
    scope_id: scopeId ?? undefined,
  })

  const createTodo = useCreateTodo()
  const createPlan = useCreatePlan()
  const updatePlan = useUpdatePlan()
  const toggleTodo = useToggleTodo()
  const updateTodo = useUpdateTodo()
  const togglePlanStep = useTogglePlanStep()

  const [todosOpen, setTodosOpen] = useState(true)
  const [plansOpen, setPlansOpen] = useState(true)

  const sortedTodos = useMemo(() => {
    return [...todos].sort((a, b) => {
      const aOrder = typeof a.metadata?.sort_order === 'number' ? a.metadata.sort_order : Infinity
      const bOrder = typeof b.metadata?.sort_order === 'number' ? b.metadata.sort_order : Infinity
      if (aOrder !== bOrder) return aOrder - bOrder
      return new Date(a.created_at).getTime() - new Date(b.created_at).getTime()
    })
  }, [todos])

  const handleReorder = useCallback(
    (activeId: string, overId: string) => {
      const oldIndex = sortedTodos.findIndex((t) => t.id === activeId)
      const newIndex = sortedTodos.findIndex((t) => t.id === overId)
      if (oldIndex === -1 || newIndex === -1) return
      const reordered = arrayMove(sortedTodos, oldIndex, newIndex)
      for (let i = 0; i < reordered.length; i++) {
        const todo = reordered[i]
        const currentOrder = typeof todo.metadata?.sort_order === 'number' ? todo.metadata.sort_order : -1
        if (currentOrder !== i) {
          updateTodo.mutate({
            id: todo.id,
            updates: { metadata: { ...todo.metadata, sort_order: i } },
          })
        }
      }
    },
    [sortedTodos, updateTodo],
  )

  const handleAddTodo = useCallback(
    (title: string) => {
      if (!scopeId) return
      createTodo.mutate({
        title,
        scope,
        scope_id: scopeId ?? undefined,
        priority: 'medium',
      })
    },
    [scope, scopeId, createTodo],
  )

  const handleAddPlan = useCallback(
    (title: string) => {
      if (!scopeId) return
      createPlan.mutate({ title, scope, scope_id: scopeId ?? undefined })
    },
    [scope, scopeId, createPlan],
  )

  const handleAddPlanStep = useCallback(
    (planId: string, title: string) => {
      const plan = plans.find((p) => p.id === planId)
      if (!plan) return
      const newStep = {
        id: `step-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
        title,
        status: 'pending' as const,
        depends_on: [],
      }
      updatePlan.mutate({ id: planId, updates: { steps: [...plan.steps, newStep] } })
    },
    [plans, updatePlan],
  )

  const isLoading = todosLoading || plansLoading
  const isEmpty = !isLoading && !!scopeId && todos.length === 0 && plans.length === 0

  const activeTodos = sortedTodos.filter((t) => t.status !== 'done')
  const doneTodos = sortedTodos.filter((t) => t.status === 'done')

  return (
    <ScrollArea className="flex-1 min-h-0">
      <div className="p-3 space-y-3">
        {/* Scope switcher */}
        <div className="flex items-center justify-between">
          <span className="text-sm font-semibold text-fg">Work</span>
          <div className="flex bg-bg-elevated rounded p-0.5 gap-0.5">
            <button
              type="button"
              onClick={() => setScope('session')}
              className={`text-[10px] px-2 py-0.5 rounded transition-colors ${
                scope === 'session' ? 'bg-surface text-fg' : 'text-fg-faint hover:text-fg-muted'
              }`}
            >
              Session
            </button>
            <button
              type="button"
              onClick={() => setScope('project')}
              className={`text-[10px] px-2 py-0.5 rounded transition-colors ${
                scope === 'project' ? 'bg-surface text-fg' : 'text-fg-faint hover:text-fg-muted'
              }`}
            >
              Project
            </button>
          </div>
        </div>

        {/* No project prompt */}
        {scope === 'project' && !activeProjectId && (
          <div className="text-xs text-fg-muted text-center py-4 border border-dashed border-border-subtle rounded-md">
            No project found for this directory.
          </div>
        )}

        {isLoading && (
          <div className="space-y-2">
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className="h-8 w-full rounded-md" />
            ))}
          </div>
        )}

        {isEmpty && (
          <Empty className="py-12">
            <EmptyHeader>
              <EmptyMedia variant="icon"><ListTodo /></EmptyMedia>
              <EmptyTitle className="text-sm">No work items</EmptyTitle>
              <EmptyDescription className="text-xs">Add a todo or let the agent create a plan</EmptyDescription>
            </EmptyHeader>
          </Empty>
        )}

        {/* Todos section */}
        {!isLoading && (
          <div>
            <button
              type="button"
              onClick={() => setTodosOpen((v) => !v)}
              className="flex items-center gap-1.5 mb-1.5"
            >
              {todosOpen ? (
                <ChevronDown className="w-2.5 h-2.5 text-fg-faint" />
              ) : (
                <ChevronRight className="w-2.5 h-2.5 text-fg-faint" />
              )}
              <span className="text-[11px] text-fg-muted uppercase tracking-wider font-semibold">
                Todos
              </span>
              <span className="text-[10px] text-fg-faint bg-bg-elevated px-1.5 rounded-full">
                {activeTodos.length}
              </span>
            </button>
            {todosOpen && (
              <>
                <TodoList
                  todos={[...activeTodos, ...doneTodos]}
                  onCheck={toggleTodo.check}
                  onUncheck={toggleTodo.uncheck}
                  onReorder={handleReorder}
                />
                <div className="mt-1.5">
                  <AddItemInput
                    placeholder="Add todo..."
                    onAdd={handleAddTodo}
                    disabled={!scopeId}
                  />
                </div>
              </>
            )}
          </div>
        )}

        {/* Plans section */}
        {!isLoading && (
          <div>
            <button
              type="button"
              onClick={() => setPlansOpen((v) => !v)}
              className="flex items-center gap-1.5 mb-1.5"
            >
              {plansOpen ? (
                <ChevronDown className="w-2.5 h-2.5 text-fg-faint" />
              ) : (
                <ChevronRight className="w-2.5 h-2.5 text-fg-faint" />
              )}
              <span className="text-[11px] text-fg-muted uppercase tracking-wider font-semibold">
                Plans
              </span>
              <span className="text-[10px] text-fg-faint bg-bg-elevated px-1.5 rounded-full">
                {plans.length}
              </span>
            </button>
            {plansOpen && (
              <div className="space-y-2">
                {plans.map((plan) => (
                  <PlanCard
                    key={plan.id}
                    plan={plan}
                    onStepCheck={togglePlanStep.check}
                    onStepUncheck={togglePlanStep.uncheck}
                    onAddStep={handleAddPlanStep}
                  />
                ))}
                <AddItemInput
                  placeholder="Add plan..."
                  onAdd={handleAddPlan}
                  disabled={!scopeId}
                />
              </div>
            )}
          </div>
        )}

        {/* Send Changes button */}
        <div className="pt-2 border-t border-border-subtle/50 flex justify-end">
          <button
            type="button"
            onClick={() => void flush()}
            disabled={!dirty}
            className={`px-3 py-1 text-[11px] font-medium rounded transition-colors ${
              dirty
                ? 'bg-primary text-white hover:bg-primary/80'
                : 'bg-surface text-fg-faint cursor-default'
            }`}
          >
            Send Changes
            {dirty && changeCount > 0 && (
              <span className="ml-1 opacity-70">({changeCount})</span>
            )}
          </button>
        </div>
      </div>
    </ScrollArea>
  )
}
