import { useCallback, useMemo, useState } from 'react'
import { ChevronDown, ChevronRight, ListTodo } from 'lucide-react'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Skeleton } from '@/components/ui/skeleton'
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from '@/components/ui/empty'
import { useAppStore } from '@/stores/useAppStore'
import { useWorkStore } from '@/stores/useWorkStore'
import { useTodos, useCreateTodo, useToggleTodo, useUpdateTodo, useUpdateTodoScope } from '@/hooks/useTodos'
import { usePlans, useCreatePlan, useUpdatePlan, useTogglePlanStep } from '@/hooks/usePlans'
import { useReminders, useDeleteReminder, useUpdateReminderScope } from '@/hooks/useReminders'
import { useWorkSync } from '@/hooks/useWorkSync'
import { TodoList } from './TodoList'
import { PlanCard } from './PlanCard'
import { AddItemInput } from './AddItemInput'
import { ScopeFilterChip } from './ScopeChip'
import { ReminderItem } from './ReminderItem'
import { arrayMove } from '@dnd-kit/sortable'
import type { Todo } from '@/lib/types'

/**
 * Work panel — D2 (CW-20260428-0015).
 *
 * Surface:
 *   - Scope filter chip: All / Session / Project.
 *   - "All" view renders both "This Session" and "This Project" sections
 *     for todos and reminders side by side, each with scope chips on rows.
 *   - "Session" / "Project" filters render only the matching section.
 *   - Each todo / reminder row exposes promote-to-project and
 *     demote-to-session inline actions (visible on hover).
 */
export function WorkTab() {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const activeProjectId = useAppStore((s) => s.activeProjectId)
  const scope = useWorkStore((s) => s.scope)
  const setScope = useWorkStore((s) => s.setScope)
  const { dirty, changeCount, flush } = useWorkSync()

  const showSessionSection = scope === 'all' || scope === 'session'
  const showProjectSection = scope === 'all' || scope === 'project'

  // Session-scoped todos / plans (if the section is visible).
  const { data: sessionTodos = [], isLoading: sessionTodosLoading } = useTodos({
    scope: 'session',
    scope_id: activeSessionId ?? undefined,
  })
  const { data: sessionPlans = [], isLoading: sessionPlansLoading } = usePlans({
    scope: 'session',
    scope_id: activeSessionId ?? undefined,
  })

  // Project-scoped todos / plans (if the section is visible).
  const { data: projectTodos = [], isLoading: projectTodosLoading } = useTodos({
    scope: 'project',
    scope_id: activeProjectId ?? undefined,
  })
  const { data: projectPlans = [], isLoading: projectPlansLoading } = usePlans({
    scope: 'project',
    scope_id: activeProjectId ?? undefined,
  })

  // Reminders — listed once for the active session; the API auto-includes
  // project-scoped reminders for the session's project.
  const { data: reminders = [] } = useReminders(activeSessionId)
  const sessionReminders = reminders.filter((r) => r.scope !== 'project')
  const projectReminders = reminders.filter((r) => r.scope === 'project')

  const createTodo = useCreateTodo()
  const createPlan = useCreatePlan()
  const updatePlan = useUpdatePlan()
  const toggleTodo = useToggleTodo()
  const updateTodo = useUpdateTodo()
  const updateTodoScope = useUpdateTodoScope()
  const togglePlanStep = useTogglePlanStep()
  const deleteReminder = useDeleteReminder(activeSessionId)
  const updateReminderScope = useUpdateReminderScope(activeSessionId)

  const [todosOpen, setTodosOpen] = useState(true)
  const [plansOpen, setPlansOpen] = useState(true)
  const [remindersOpen, setRemindersOpen] = useState(true)

  // Stable sort by metadata.sort_order then created_at.
  const sortTodos = (todos: Todo[]) =>
    [...todos].sort((a, b) => {
      const aOrder = typeof a.metadata?.sort_order === 'number' ? a.metadata.sort_order : Infinity
      const bOrder = typeof b.metadata?.sort_order === 'number' ? b.metadata.sort_order : Infinity
      if (aOrder !== bOrder) return aOrder - bOrder
      return new Date(a.created_at).getTime() - new Date(b.created_at).getTime()
    })
  const sortedSessionTodos = useMemo(() => sortTodos(sessionTodos), [sessionTodos])
  const sortedProjectTodos = useMemo(() => sortTodos(projectTodos), [projectTodos])

  const handleReorderFor = useCallback(
    (todos: Todo[]) => (activeId: string, overId: string) => {
      const oldIndex = todos.findIndex((t) => t.id === activeId)
      const newIndex = todos.findIndex((t) => t.id === overId)
      if (oldIndex === -1 || newIndex === -1) return
      const reordered = arrayMove(todos, oldIndex, newIndex)
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
    [updateTodo],
  )

  const handleAddSessionTodo = useCallback(
    (title: string) => {
      if (!activeSessionId) return
      createTodo.mutate({
        title,
        scope: 'session',
        scope_id: activeSessionId,
        priority: 'medium',
      })
    },
    [activeSessionId, createTodo],
  )

  const handleAddProjectTodo = useCallback(
    (title: string) => {
      if (!activeProjectId) return
      createTodo.mutate({
        title,
        scope: 'project',
        scope_id: activeProjectId,
        priority: 'medium',
      })
    },
    [activeProjectId, createTodo],
  )

  const handleAddSessionPlan = useCallback(
    (title: string) => {
      if (!activeSessionId) return
      createPlan.mutate({ title, scope: 'session', scope_id: activeSessionId })
    },
    [activeSessionId, createPlan],
  )

  const handleAddProjectPlan = useCallback(
    (title: string) => {
      if (!activeProjectId) return
      createPlan.mutate({ title, scope: 'project', scope_id: activeProjectId })
    },
    [activeProjectId, createPlan],
  )

  const handleAddPlanStep = useCallback(
    (planId: string, title: string) => {
      const plan = [...sessionPlans, ...projectPlans].find((p) => p.id === planId)
      if (!plan) return
      const newStep = {
        id: `step-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
        title,
        status: 'pending' as const,
        depends_on: [],
      }
      updatePlan.mutate({ id: planId, updates: { steps: [...plan.steps, newStep] } })
    },
    [sessionPlans, projectPlans, updatePlan],
  )

  // Promote/demote handlers for both todos and reminders. We use mutateAsync
  // (not mutate) so the row components can await the mutation lifecycle and
  // keep their `busy` flag set until the request actually settles — prevents
  // double-submits on slow networks. We swallow errors here because the
  // mutation hooks already surface them via toast/onError.
  const promoteTodo = useCallback(
    async (id: string, projectId: string) => {
      try {
        await updateTodoScope.mutateAsync({ id, scope: 'project', scopeId: projectId, projectId })
      } catch {
        /* surfaced via mutation onError */
      }
    },
    [updateTodoScope],
  )
  const demoteTodo = useCallback(
    async (id: string, sessionId: string) => {
      try {
        await updateTodoScope.mutateAsync({ id, scope: 'session', scopeId: sessionId, projectId: '' })
      } catch {
        /* surfaced via mutation onError */
      }
    },
    [updateTodoScope],
  )
  const promoteReminder = useCallback(
    async (id: string, projectId: string) => {
      try {
        await updateReminderScope.mutateAsync({ id, scope: 'project', projectId })
      } catch {
        /* surfaced via mutation onError */
      }
    },
    [updateReminderScope],
  )
  const demoteReminder = useCallback(
    async (id: string) => {
      try {
        await updateReminderScope.mutateAsync({ id, scope: 'session', projectId: '' })
      } catch {
        /* surfaced via mutation onError */
      }
    },
    [updateReminderScope],
  )

  const isLoading =
    (showSessionSection && (sessionTodosLoading || sessionPlansLoading)) ||
    (showProjectSection && (projectTodosLoading || projectPlansLoading))

  const isEmpty =
    !isLoading &&
    sessionTodos.length === 0 &&
    sessionPlans.length === 0 &&
    sessionReminders.length === 0 &&
    projectTodos.length === 0 &&
    projectPlans.length === 0 &&
    projectReminders.length === 0

  const scopeActions = {
    activeProjectId,
    activeSessionId,
    onPromote: promoteTodo,
    onDemote: demoteTodo,
  }

  return (
    <ScrollArea className="flex-1 min-h-0">
      <div className="p-3 space-y-3">
        {/* Scope filter chip */}
        <div className="flex items-center justify-between">
          <span className="text-sm font-semibold text-fg">Work</span>
          <ScopeFilterChip filter={scope} onChange={(f) => setScope(f)} />
        </div>

        {/* No project banner — only when project section is visible and there's no project. */}
        {showProjectSection && !activeProjectId && (
          <div className="text-xs text-fg-muted text-center py-2 border border-dashed border-border-subtle rounded-md">
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

        {/* Todos block — split into session + project sections per filter. */}
        {!isLoading && (
          <SectionToggle title="Todos" open={todosOpen} onToggle={() => setTodosOpen((v) => !v)} count={sortedSessionTodos.length + sortedProjectTodos.length}>
            {showSessionSection && (
              <ScopeSection label="This Session">
                <TodoList
                  todos={sortedSessionTodos}
                  onCheck={toggleTodo.check}
                  onUncheck={toggleTodo.uncheck}
                  onReorder={handleReorderFor(sortedSessionTodos)}
                  scopeActions={scopeActions}
                  showScope={scope === 'all'}
                />
                <div className="mt-1.5">
                  <AddItemInput
                    placeholder="Add session todo..."
                    onAdd={handleAddSessionTodo}
                    disabled={!activeSessionId}
                  />
                </div>
              </ScopeSection>
            )}
            {showProjectSection && (
              <ScopeSection label="This Project">
                {activeProjectId ? (
                  <>
                    <TodoList
                      todos={sortedProjectTodos}
                      onCheck={toggleTodo.check}
                      onUncheck={toggleTodo.uncheck}
                      onReorder={handleReorderFor(sortedProjectTodos)}
                      scopeActions={scopeActions}
                      showScope={scope === 'all'}
                    />
                    <div className="mt-1.5">
                      <AddItemInput
                        placeholder="Add project todo..."
                        onAdd={handleAddProjectTodo}
                        disabled={!activeProjectId}
                      />
                    </div>
                  </>
                ) : (
                  <p className="text-xs text-fg-faint italic">No project context.</p>
                )}
              </ScopeSection>
            )}
          </SectionToggle>
        )}

        {/* Reminders block — always rendered but split by scope per filter. */}
        {!isLoading && reminders.length > 0 && (
          <SectionToggle title="Reminders" open={remindersOpen} onToggle={() => setRemindersOpen((v) => !v)} count={reminders.length}>
            {showSessionSection && sessionReminders.length > 0 && (
              <ScopeSection label="This Session">
                <div className="space-y-1">
                  {sessionReminders.map((r) => (
                    <ReminderItem
                      key={r.id}
                      reminder={r}
                      activeProjectId={activeProjectId}
                      onDelete={(id) => deleteReminder.mutate(id)}
                      onPromote={promoteReminder}
                      onDemote={(id) => demoteReminder(id)}
                    />
                  ))}
                </div>
              </ScopeSection>
            )}
            {showProjectSection && projectReminders.length > 0 && (
              <ScopeSection label="This Project">
                <div className="space-y-1">
                  {projectReminders.map((r) => (
                    <ReminderItem
                      key={r.id}
                      reminder={r}
                      activeProjectId={activeProjectId}
                      onDelete={(id) => deleteReminder.mutate(id)}
                      onPromote={promoteReminder}
                      onDemote={(id) => demoteReminder(id)}
                    />
                  ))}
                </div>
              </ScopeSection>
            )}
          </SectionToggle>
        )}

        {/* Plans block — also split. */}
        {!isLoading && (sessionPlans.length > 0 || projectPlans.length > 0) && (
          <SectionToggle title="Plans" open={plansOpen} onToggle={() => setPlansOpen((v) => !v)} count={sessionPlans.length + projectPlans.length}>
            {showSessionSection && (
              <ScopeSection label="This Session">
                <div className="space-y-2">
                  {sessionPlans.map((plan) => (
                    <PlanCard
                      key={plan.id}
                      plan={plan}
                      onStepCheck={togglePlanStep.check}
                      onStepUncheck={togglePlanStep.uncheck}
                      onAddStep={handleAddPlanStep}
                    />
                  ))}
                  <AddItemInput
                    placeholder="Add session plan..."
                    onAdd={handleAddSessionPlan}
                    disabled={!activeSessionId}
                  />
                </div>
              </ScopeSection>
            )}
            {showProjectSection && activeProjectId && (
              <ScopeSection label="This Project">
                <div className="space-y-2">
                  {projectPlans.map((plan) => (
                    <PlanCard
                      key={plan.id}
                      plan={plan}
                      onStepCheck={togglePlanStep.check}
                      onStepUncheck={togglePlanStep.uncheck}
                      onAddStep={handleAddPlanStep}
                    />
                  ))}
                  <AddItemInput
                    placeholder="Add project plan..."
                    onAdd={handleAddProjectPlan}
                    disabled={!activeProjectId}
                  />
                </div>
              </ScopeSection>
            )}
          </SectionToggle>
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

interface SectionToggleProps {
  title: string
  open: boolean
  onToggle: () => void
  count: number
  children: React.ReactNode
}

function SectionToggle({ title, open, onToggle, count, children }: SectionToggleProps) {
  return (
    <div>
      <button type="button" onClick={onToggle} className="flex items-center gap-1.5 mb-1.5">
        {open ? (
          <ChevronDown className="w-2.5 h-2.5 text-fg-faint" />
        ) : (
          <ChevronRight className="w-2.5 h-2.5 text-fg-faint" />
        )}
        <span className="text-[11px] text-fg-muted uppercase tracking-wider font-semibold">{title}</span>
        <span className="text-[10px] text-fg-faint bg-bg-elevated px-1.5 rounded-full">{count}</span>
      </button>
      {open && <div className="space-y-3">{children}</div>}
    </div>
  )
}

function ScopeSection({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-[10px] text-fg-faint uppercase tracking-wider mb-1">{label}</div>
      {children}
    </div>
  )
}
