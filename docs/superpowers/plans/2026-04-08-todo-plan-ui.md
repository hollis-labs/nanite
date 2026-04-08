# Todo/Plan Collaborative UI — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the RightRail "Tasks" tab with an interactive "Work" tab providing collaborative todo/plan management with drag-and-drop, undo-with-feedback, batched agent sync, and matching envelope cards.

**Architecture:** Collapsible sections (Todos + Plans) in the RightRail, scoped to Session or Project. Mutations save immediately via React Query; a dirty-tracking layer batches changes for agent notification via toast. Two new envelope types provide inline chat interaction.

**Tech Stack:** React 19, TypeScript, Zustand, TanStack React Query, @dnd-kit/sortable, Tailwind CSS (dark zinc theme), shadcn primitives

**Spec:** `docs/superpowers/specs/2026-04-08-todo-plan-ui-design.md`

---

## File Map

| File | Responsibility |
|------|---------------|
| `ui/src/lib/types.ts` | Add Todo, Plan, PlanStep, filter, and WorkDiff types |
| `ui/src/lib/api.ts` | Add 13 todo/plan API client functions |
| `ui/src/stores/useWorkStore.ts` | Zustand store: scope selection, dirty flag, change queue |
| `ui/src/hooks/useTodos.ts` | React Query hooks for todo CRUD |
| `ui/src/hooks/usePlans.ts` | React Query hooks for plan CRUD + approve |
| `ui/src/hooks/useWorkSync.ts` | Dirty tracking, diff builder, auto-inject on send |
| `ui/src/components/work/AddItemInput.tsx` | Inline "+" input for adding todos or plan steps |
| `ui/src/components/work/TodoItem.tsx` | Todo row: drag handle, checkbox, priority, feedback expansion |
| `ui/src/components/work/TodoList.tsx` | Sortable todo list with @dnd-kit |
| `ui/src/components/work/PlanStepItem.tsx` | Plan step row: checkbox, title |
| `ui/src/components/work/PlanCard.tsx` | Plan: header, step list, progress bar |
| `ui/src/components/work/WorkTab.tsx` | Top-level tab: scope switcher, sections, Send Changes button |
| `ui/src/components/RightRail.tsx` | Replace `tasks` tab with `work` tab |
| `ui/src/components/chat/ChatComposer.tsx` | Add WorkSyncToast in composer chrome area |
| `ui/src/components/chat/envelopes/TodoListCard.tsx` | Envelope: interactive todo checklist |
| `ui/src/components/chat/envelopes/PlanReviewCard.tsx` | Envelope: plan with Approve/Edit/Reject |
| `ui/src/generated/plugin-envelopes.ts` | Register `todo-list` and `plan-review` entries |

**Deleted files:**
- `ui/src/components/tasks/SessionTasksTab.tsx`
- `ui/src/components/tasks/TaskStatusBadge.tsx`
- `ui/src/hooks/useSessionTasks.ts`
- `ui/src/components/chat/envelopes/SessionTaskCard.tsx`

---

### Task 1: Types and API Client

**Files:**
- Modify: `ui/src/lib/types.ts:665-685` (replace SessionTask block)
- Modify: `ui/src/lib/api.ts:882-930` (replace session task functions)

- [ ] **Step 1: Remove old SessionTask types from types.ts**

Delete lines 665-685 in `ui/src/lib/types.ts` (the `SessionTaskStatus` type, `SessionTask` interface, and the comment above them). Replace with the new types:

```typescript
// --- Todos & Plans ---

export type TodoStatus = 'pending' | 'in_progress' | 'done' | 'blocked'
export type TodoPriority = 'low' | 'medium' | 'high' | 'critical'
export type PlanStatus = 'proposed' | 'approved' | 'in_progress' | 'complete' | 'abandoned'
export type PlanStepStatus = 'pending' | 'in_progress' | 'done' | 'skipped'

export interface Todo {
  id: string
  scope: 'workspace' | 'project' | 'session'
  scope_id: string
  parent_id?: string
  title: string
  description: string
  status: TodoStatus
  priority: TodoPriority
  labels: string[]
  metadata: Record<string, unknown>
  created_by: string
  created_at: string
  updated_at: string
}

export interface TodoFilter {
  scope?: string
  scope_id?: string
  status?: TodoStatus
  priority?: TodoPriority
  parent_id?: string
  labels?: string[]
}

export interface PlanStep {
  id: string
  title: string
  status: PlanStepStatus
  todo_id?: string
  depends_on: string[]
  acceptance?: string
  notes?: string
}

export interface Plan {
  id: string
  scope: 'workspace' | 'project' | 'session'
  scope_id: string
  title: string
  description: string
  status: PlanStatus
  steps: PlanStep[]
  metadata: Record<string, unknown>
  created_by: string
  created_at: string
  updated_at: string
}

export interface PlanFilter {
  scope?: string
  scope_id?: string
  status?: PlanStatus
}

export interface WorkDiff {
  todos_checked: string[]
  todos_unchecked: Array<{ id: string; reason?: string }>
  todos_added: string[]
  todos_reordered: boolean
  plan_steps_checked: Array<{ plan_id: string; step_id: string }>
  plan_steps_unchecked: Array<{ plan_id: string; step_id: string; reason?: string }>
  plans_approved: string[]
  plans_rejected: string[]
}
```

- [ ] **Step 2: Update the import block at the top of api.ts**

In `ui/src/lib/api.ts`, find the import of `SessionTask` and `SessionTaskStatus` in the type import block (line 1-30 area). Replace them with imports for the new types:

Remove: `SessionTask`, `SessionTaskStatus`
Add: `Todo`, `TodoFilter`, `Plan`, `PlanFilter`, `PlanStep`, `WorkDiff`

- [ ] **Step 3: Replace session task API functions with todo/plan functions**

In `ui/src/lib/api.ts`, delete lines 882-930 (the `// Session Tasks` block: `listSessionTasks`, `createSessionTask`, `updateSessionTask`, `transitionSessionTask`). Replace with:

```typescript
  // --- Todos ---

  listTodos: async (filter?: TodoFilter): Promise<Todo[]> => {
    const params = new URLSearchParams()
    if (filter?.scope) params.set('scope', filter.scope)
    if (filter?.scope_id) params.set('scope_id', filter.scope_id)
    if (filter?.status) params.set('status', filter.status)
    if (filter?.priority) params.set('priority', filter.priority)
    if (filter?.parent_id) params.set('parent_id', filter.parent_id)
    if (filter?.labels?.length) params.set('labels', filter.labels.join(','))
    const qs = params.toString()
    const res = await fetch(`${API_BASE}/todos${qs ? `?${qs}` : ''}`)
    if (!res.ok) throw new Error(`Failed to list todos: ${res.status}`)
    return res.json()
  },

  createTodo: async (data: {
    title: string
    scope: string
    scope_id?: string
    priority?: string
    description?: string
  }): Promise<Todo> => {
    const res = await fetch(`${API_BASE}/todos`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }))
      throw new Error(err.error || `Failed to create todo: ${res.status}`)
    }
    return res.json()
  },

  getTodo: async (id: string): Promise<Todo> => {
    const res = await fetch(`${API_BASE}/todos/${encodeURIComponent(id)}`)
    if (!res.ok) throw new Error(`Failed to get todo: ${res.status}`)
    return res.json()
  },

  updateTodo: async (
    id: string,
    updates: Partial<Pick<Todo, 'title' | 'description' | 'status' | 'priority' | 'labels' | 'metadata'>>,
  ): Promise<Todo> => {
    const res = await fetch(`${API_BASE}/todos/${encodeURIComponent(id)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(updates),
    })
    if (!res.ok) throw new Error(`Failed to update todo: ${res.status}`)
    return res.json()
  },

  deleteTodo: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/todos/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    })
    if (!res.ok) throw new Error(`Failed to delete todo: ${res.status}`)
  },

  listTodoChildren: async (id: string): Promise<Todo[]> => {
    const res = await fetch(`${API_BASE}/todos/${encodeURIComponent(id)}/children`)
    if (!res.ok) throw new Error(`Failed to list todo children: ${res.status}`)
    return res.json()
  },

  // --- Plans ---

  listPlans: async (filter?: PlanFilter): Promise<Plan[]> => {
    const params = new URLSearchParams()
    if (filter?.scope) params.set('scope', filter.scope)
    if (filter?.scope_id) params.set('scope_id', filter.scope_id)
    if (filter?.status) params.set('status', filter.status)
    const qs = params.toString()
    const res = await fetch(`${API_BASE}/plans${qs ? `?${qs}` : ''}`)
    if (!res.ok) throw new Error(`Failed to list plans: ${res.status}`)
    return res.json()
  },

  createPlan: async (data: {
    title: string
    scope: string
    scope_id?: string
    description?: string
    steps?: PlanStep[]
  }): Promise<Plan> => {
    const res = await fetch(`${API_BASE}/plans`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }))
      throw new Error(err.error || `Failed to create plan: ${res.status}`)
    }
    return res.json()
  },

  getPlan: async (id: string): Promise<Plan> => {
    const res = await fetch(`${API_BASE}/plans/${encodeURIComponent(id)}`)
    if (!res.ok) throw new Error(`Failed to get plan: ${res.status}`)
    return res.json()
  },

  updatePlan: async (
    id: string,
    updates: Partial<Pick<Plan, 'title' | 'description' | 'status' | 'steps' | 'metadata'>>,
  ): Promise<Plan> => {
    const res = await fetch(`${API_BASE}/plans/${encodeURIComponent(id)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(updates),
    })
    if (!res.ok) throw new Error(`Failed to update plan: ${res.status}`)
    return res.json()
  },

  updatePlanStep: async (
    planId: string,
    stepId: string,
    updates: Partial<Pick<PlanStep, 'title' | 'status' | 'notes'>>,
  ): Promise<Plan> => {
    const res = await fetch(
      `${API_BASE}/plans/${encodeURIComponent(planId)}/steps/${encodeURIComponent(stepId)}`,
      {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(updates),
      },
    )
    if (!res.ok) throw new Error(`Failed to update plan step: ${res.status}`)
    return res.json()
  },

  deletePlan: async (id: string): Promise<void> => {
    const res = await fetch(`${API_BASE}/plans/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    })
    if (!res.ok) throw new Error(`Failed to delete plan: ${res.status}`)
  },

  approvePlan: async (id: string, createTodos = true): Promise<Plan> => {
    const res = await fetch(`${API_BASE}/plans/${encodeURIComponent(id)}/approve`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ create_todos: createTodos }),
    })
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }))
      throw new Error(err.error || `Failed to approve plan: ${res.status}`)
    }
    return res.json()
  },

  // --- Work Sync ---

  syncWorkChanges: async (diff: WorkDiff): Promise<{ ok: boolean }> => {
    const res = await fetch(`${API_BASE}/work/sync`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(diff),
    })
    if (!res.ok) throw new Error(`Failed to sync work changes: ${res.status}`)
    return res.json()
  },
```

- [ ] **Step 4: Verify TypeScript compiles**

Run: `cd ui && npx tsc --noEmit 2>&1 | head -20`

Expected: Type errors from files that still import `SessionTask`/`SessionTaskStatus` (the old components we'll delete in Task 9). No errors from `types.ts` or `api.ts` themselves.

- [ ] **Step 5: Commit**

```bash
git add ui/src/lib/types.ts ui/src/lib/api.ts
git commit -m "feat(ui): add Todo/Plan types and API client, remove SessionTask types"
```

---

### Task 2: Zustand Store — useWorkStore

**Files:**
- Create: `ui/src/stores/useWorkStore.ts`

- [ ] **Step 1: Create the work store**

Create `ui/src/stores/useWorkStore.ts`:

```typescript
import { create } from 'zustand'
import type { WorkDiff } from '@/lib/types'

type WorkScope = 'session' | 'project'

interface WorkChange {
  type: 'todo_checked' | 'todo_unchecked' | 'todo_added' | 'todo_reordered' |
        'plan_step_checked' | 'plan_step_unchecked' | 'plan_approved' | 'plan_rejected'
  id: string
  planId?: string
  reason?: string
}

interface WorkState {
  scope: WorkScope
  dirty: boolean
  changes: WorkChange[]
  toastMessage: string | null
  setScope: (scope: WorkScope) => void
  recordChange: (change: WorkChange) => void
  buildDiff: () => WorkDiff
  clearChanges: () => void
  showToast: (message: string) => void
  dismissToast: () => void
}

export const useWorkStore = create<WorkState>((set, get) => ({
  scope: 'session',
  dirty: false,
  changes: [],
  toastMessage: null,

  setScope: (scope) => set({ scope }),

  recordChange: (change) =>
    set((state) => ({
      dirty: true,
      changes: [...state.changes, change],
    })),

  buildDiff: () => {
    const { changes } = get()
    const diff: WorkDiff = {
      todos_checked: [],
      todos_unchecked: [],
      todos_added: [],
      todos_reordered: false,
      plan_steps_checked: [],
      plan_steps_unchecked: [],
      plans_approved: [],
      plans_rejected: [],
    }
    for (const c of changes) {
      switch (c.type) {
        case 'todo_checked':
          diff.todos_checked.push(c.id)
          break
        case 'todo_unchecked':
          diff.todos_unchecked.push({ id: c.id, reason: c.reason })
          break
        case 'todo_added':
          diff.todos_added.push(c.id)
          break
        case 'todo_reordered':
          diff.todos_reordered = true
          break
        case 'plan_step_checked':
          diff.plan_steps_checked.push({ plan_id: c.planId!, step_id: c.id })
          break
        case 'plan_step_unchecked':
          diff.plan_steps_unchecked.push({ plan_id: c.planId!, step_id: c.id, reason: c.reason })
          break
        case 'plan_approved':
          diff.plans_approved.push(c.id)
          break
        case 'plan_rejected':
          diff.plans_rejected.push(c.id)
          break
      }
    }
    return diff
  },

  clearChanges: () => set({ dirty: false, changes: [] }),

  showToast: (message) => set({ toastMessage: message }),
  dismissToast: () => set({ toastMessage: null }),
}))
```

- [ ] **Step 2: Verify it compiles**

Run: `cd ui && npx tsc --noEmit --pretty 2>&1 | grep useWorkStore || echo "No errors for useWorkStore"`

Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add ui/src/stores/useWorkStore.ts
git commit -m "feat(ui): add useWorkStore for work tab state and dirty tracking"
```

---

### Task 3: React Query Hooks — useTodos and usePlans

**Files:**
- Create: `ui/src/hooks/useTodos.ts`
- Create: `ui/src/hooks/usePlans.ts`

- [ ] **Step 1: Create useTodos.ts**

Create `ui/src/hooks/useTodos.ts`:

```typescript
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { Todo, TodoFilter, TodoStatus, TodoPriority } from '@/lib/types'
import { useWorkStore } from '@/stores/useWorkStore'

export function useTodos(filter: TodoFilter) {
  return useQuery({
    queryKey: ['todos', filter],
    queryFn: () => api.listTodos(filter),
    enabled: !!filter.scope_id || filter.scope === 'workspace',
  })
}

export function useCreateTodo() {
  const queryClient = useQueryClient()
  const recordChange = useWorkStore((s) => s.recordChange)

  return useMutation({
    mutationFn: (data: {
      title: string
      scope: string
      scope_id?: string
      priority?: string
      description?: string
    }) => api.createTodo(data),
    onSuccess: (todo) => {
      queryClient.invalidateQueries({ queryKey: ['todos'] })
      recordChange({ type: 'todo_added', id: todo.id })
    },
  })
}

export function useUpdateTodo() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({
      id,
      updates,
    }: {
      id: string
      updates: Partial<Pick<Todo, 'title' | 'description' | 'status' | 'priority' | 'labels' | 'metadata'>>
    }) => api.updateTodo(id, updates),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['todos'] })
    },
  })
}

export function useDeleteTodo() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => api.deleteTodo(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['todos'] })
    },
  })
}

export function useToggleTodo() {
  const updateTodo = useUpdateTodo()
  const recordChange = useWorkStore((s) => s.recordChange)

  return {
    check: (id: string) => {
      updateTodo.mutate({ id, updates: { status: 'done' as TodoStatus } })
      recordChange({ type: 'todo_checked', id })
    },
    uncheck: (id: string, reason?: string) => {
      updateTodo.mutate({
        id,
        updates: {
          status: 'pending' as TodoStatus,
          metadata: reason ? { reopen_reason: reason } : undefined,
        },
      })
      recordChange({ type: 'todo_unchecked', id, reason })
    },
  }
}
```

- [ ] **Step 2: Create usePlans.ts**

Create `ui/src/hooks/usePlans.ts`:

```typescript
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { Plan, PlanFilter, PlanStep, PlanStepStatus } from '@/lib/types'
import { useWorkStore } from '@/stores/useWorkStore'

export function usePlans(filter: PlanFilter) {
  return useQuery({
    queryKey: ['plans', filter],
    queryFn: () => api.listPlans(filter),
    enabled: !!filter.scope_id || filter.scope === 'workspace',
  })
}

export function useCreatePlan() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (data: {
      title: string
      scope: string
      scope_id?: string
      description?: string
      steps?: PlanStep[]
    }) => api.createPlan(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['plans'] })
    },
  })
}

export function useUpdatePlan() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({
      id,
      updates,
    }: {
      id: string
      updates: Partial<Pick<Plan, 'title' | 'description' | 'status' | 'steps' | 'metadata'>>
    }) => api.updatePlan(id, updates),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['plans'] })
    },
  })
}

export function useUpdatePlanStep() {
  const queryClient = useQueryClient()
  const recordChange = useWorkStore((s) => s.recordChange)

  return useMutation({
    mutationFn: ({
      planId,
      stepId,
      updates,
    }: {
      planId: string
      stepId: string
      updates: Partial<Pick<PlanStep, 'title' | 'status' | 'notes'>>
    }) => api.updatePlanStep(planId, stepId, updates),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ['plans'] })
      if (variables.updates.status === 'done') {
        recordChange({ type: 'plan_step_checked', id: variables.stepId, planId: variables.planId })
      }
    },
  })
}

export function useApprovePlan() {
  const queryClient = useQueryClient()
  const recordChange = useWorkStore((s) => s.recordChange)

  return useMutation({
    mutationFn: ({ id, createTodos = true }: { id: string; createTodos?: boolean }) =>
      api.approvePlan(id, createTodos),
    onSuccess: (plan) => {
      queryClient.invalidateQueries({ queryKey: ['plans'] })
      queryClient.invalidateQueries({ queryKey: ['todos'] })
      recordChange({ type: 'plan_approved', id: plan.id })
    },
  })
}

export function useRejectPlan() {
  const updatePlan = useUpdatePlan()
  const recordChange = useWorkStore((s) => s.recordChange)

  return {
    reject: (id: string) => {
      updatePlan.mutate({ id, updates: { status: 'abandoned' } })
      recordChange({ type: 'plan_rejected', id })
    },
  }
}

export function useTogglePlanStep() {
  const updateStep = useUpdatePlanStep()
  const recordChange = useWorkStore((s) => s.recordChange)

  return {
    check: (planId: string, stepId: string) => {
      updateStep.mutate({ planId, stepId, updates: { status: 'done' as PlanStepStatus } })
    },
    uncheck: (planId: string, stepId: string, reason?: string) => {
      updateStep.mutate({ planId, stepId, updates: { status: 'pending' as PlanStepStatus } })
      recordChange({ type: 'plan_step_unchecked', id: stepId, planId, reason })
    },
  }
}
```

- [ ] **Step 3: Verify both compile**

Run: `cd ui && npx tsc --noEmit --pretty 2>&1 | grep -E "useTodos|usePlans" || echo "No errors"`

Expected: No errors.

- [ ] **Step 4: Commit**

```bash
git add ui/src/hooks/useTodos.ts ui/src/hooks/usePlans.ts
git commit -m "feat(ui): add useTodos and usePlans React Query hooks"
```

---

### Task 4: useWorkSync Hook

**Files:**
- Create: `ui/src/hooks/useWorkSync.ts`

- [ ] **Step 1: Create useWorkSync.ts**

Create `ui/src/hooks/useWorkSync.ts`:

```typescript
import { useCallback } from 'react'
import { useWorkStore } from '@/stores/useWorkStore'
import { api } from '@/lib/api'

/**
 * Provides the sync action and auto-inject helper.
 * Call `flush()` from the Send Changes button.
 * Call `flushIfDirty()` before sending a chat message to auto-inject.
 */
export function useWorkSync() {
  const dirty = useWorkStore((s) => s.dirty)
  const buildDiff = useWorkStore((s) => s.buildDiff)
  const clearChanges = useWorkStore((s) => s.clearChanges)
  const showToast = useWorkStore((s) => s.showToast)
  const changes = useWorkStore((s) => s.changes)

  const flush = useCallback(async () => {
    if (!dirty) return
    const diff = buildDiff()
    try {
      await api.syncWorkChanges(diff)
      clearChanges()
      showToast('Tasks updated \u2014 agent notified')
    } catch (err) {
      console.error('[useWorkSync] sync failed:', err)
    }
  }, [dirty, buildDiff, clearChanges, showToast])

  const flushIfDirty = useCallback(async () => {
    if (dirty) {
      await flush()
    }
  }, [dirty, flush])

  return {
    dirty,
    changeCount: changes.length,
    flush,
    flushIfDirty,
  }
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd ui && npx tsc --noEmit --pretty 2>&1 | grep useWorkSync || echo "No errors"`

Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add ui/src/hooks/useWorkSync.ts
git commit -m "feat(ui): add useWorkSync hook for batched agent notification"
```

---

### Task 5: Install @dnd-kit

**Files:**
- Modify: `ui/package.json`

- [ ] **Step 1: Install dnd-kit packages**

Run: `cd ui && npm install @dnd-kit/core @dnd-kit/sortable @dnd-kit/utilities`

- [ ] **Step 2: Verify installation**

Run: `cd ui && node -e "require('@dnd-kit/sortable')" && echo "OK"`

Expected: `OK`

- [ ] **Step 3: Commit**

```bash
git add ui/package.json ui/package-lock.json
git commit -m "deps(ui): add @dnd-kit/core, @dnd-kit/sortable, @dnd-kit/utilities"
```

---

### Task 6: AddItemInput Component

**Files:**
- Create: `ui/src/components/work/AddItemInput.tsx`

- [ ] **Step 1: Create AddItemInput.tsx**

Create `ui/src/components/work/AddItemInput.tsx`:

```typescript
import { useState, useRef, useCallback } from 'react'
import { Plus } from 'lucide-react'

interface AddItemInputProps {
  placeholder?: string
  onAdd: (title: string) => void
  disabled?: boolean
}

export function AddItemInput({ placeholder = 'Add item...', onAdd, disabled }: AddItemInputProps) {
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  const handleSubmit = useCallback(() => {
    const trimmed = value.trim()
    if (trimmed) {
      onAdd(trimmed)
    }
    setValue('')
    setEditing(false)
  }, [value, onAdd])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter') {
        e.preventDefault()
        handleSubmit()
      } else if (e.key === 'Escape') {
        setValue('')
        setEditing(false)
      }
    },
    [handleSubmit],
  )

  if (!editing) {
    return (
      <button
        type="button"
        onClick={() => {
          setEditing(true)
          requestAnimationFrame(() => inputRef.current?.focus())
        }}
        disabled={disabled}
        className="flex items-center gap-1.5 w-full px-2 py-1.5 text-xs text-fg-faint hover:text-fg-muted border border-dashed border-border-subtle rounded-md transition-colors"
      >
        <Plus className="w-3 h-3" />
        <span>{placeholder}</span>
      </button>
    )
  }

  return (
    <input
      ref={inputRef}
      type="text"
      value={value}
      onChange={(e) => setValue(e.target.value)}
      onKeyDown={handleKeyDown}
      onBlur={handleSubmit}
      placeholder={placeholder}
      className="w-full bg-surface/50 border border-border rounded-md px-2 py-1.5 text-xs text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary"
    />
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add ui/src/components/work/AddItemInput.tsx
git commit -m "feat(ui): add AddItemInput component for inline todo/plan creation"
```

---

### Task 7: TodoItem Component

**Files:**
- Create: `ui/src/components/work/TodoItem.tsx`

- [ ] **Step 1: Create TodoItem.tsx**

Create `ui/src/components/work/TodoItem.tsx`:

```typescript
import { useState, useRef, useCallback } from 'react'
import { GripVertical, Check } from 'lucide-react'
import { useSortable } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import type { Todo, TodoPriority } from '@/lib/types'

const PRIORITY_STYLE: Record<TodoPriority, string> = {
  critical: 'text-danger bg-danger/10',
  high: 'text-warning bg-warning/10',
  medium: 'text-fg-muted bg-surface',
  low: 'text-fg-faint bg-surface/50',
}

interface TodoItemProps {
  todo: Todo
  onCheck: (id: string) => void
  onUncheck: (id: string, reason?: string) => void
}

export function TodoItem({ todo, onCheck, onUncheck }: TodoItemProps) {
  const isDone = todo.status === 'done'
  const [reopenFeedback, setReopenFeedback] = useState<string | null>(null)
  const feedbackRef = useRef<HTMLInputElement>(null)

  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: todo.id })

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : undefined,
  }

  const handleToggle = useCallback(() => {
    if (isDone) {
      // Reopening — show feedback input
      setReopenFeedback('')
      requestAnimationFrame(() => feedbackRef.current?.focus())
    } else {
      onCheck(todo.id)
    }
  }, [isDone, todo.id, onCheck])

  const handleFeedbackSubmit = useCallback(() => {
    const reason = reopenFeedback?.trim() || undefined
    onUncheck(todo.id, reason)
    setReopenFeedback(null)
  }, [reopenFeedback, todo.id, onUncheck])

  const handleFeedbackKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter') {
        e.preventDefault()
        handleFeedbackSubmit()
      } else if (e.key === 'Escape') {
        // Still reopen, just no reason
        onUncheck(todo.id)
        setReopenFeedback(null)
      }
    },
    [handleFeedbackSubmit, onUncheck, todo.id],
  )

  const isReopening = reopenFeedback !== null

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={`rounded-md border transition-colors ${
        isReopening
          ? 'border-warning/50 bg-bg-elevated'
          : isDone
            ? 'border-border-subtle/50 bg-bg/80'
            : 'border-border-subtle bg-bg-elevated'
      }`}
    >
      <div className="flex items-center gap-1.5 px-1.5 py-1.5">
        {/* Drag handle */}
        <button
          type="button"
          className="p-0.5 text-fg-faint/50 hover:text-fg-muted cursor-grab active:cursor-grabbing touch-none"
          {...attributes}
          {...listeners}
        >
          <GripVertical className="w-3 h-3" />
        </button>

        {/* Checkbox */}
        <button
          type="button"
          onClick={handleToggle}
          className={`w-3.5 h-3.5 rounded-sm border-2 flex items-center justify-center shrink-0 transition-colors ${
            isDone
              ? 'bg-primary border-primary'
              : 'border-primary hover:border-primary/80'
          }`}
        >
          {isDone && <Check className="w-2.5 h-2.5 text-white" />}
        </button>

        {/* Title */}
        <span
          className={`flex-1 text-xs truncate ${
            isDone ? 'text-fg-muted line-through' : 'text-fg'
          }`}
        >
          {todo.title}
        </span>

        {/* Priority badge */}
        {todo.priority !== 'medium' && (
          <span className={`text-[9px] px-1.5 py-0.5 rounded ${PRIORITY_STYLE[todo.priority]}`}>
            {todo.priority}
          </span>
        )}

        {/* Reopened label */}
        {isReopening && (
          <span className="text-[9px] text-warning">reopened</span>
        )}
      </div>

      {/* Feedback input — shown when reopening a done item */}
      {isReopening && (
        <div className="px-2 pb-2 pt-0.5 ml-7">
          <div className="flex gap-1.5">
            <input
              ref={feedbackRef}
              type="text"
              value={reopenFeedback}
              onChange={(e) => setReopenFeedback(e.target.value)}
              onKeyDown={handleFeedbackKeyDown}
              placeholder="Why? (e.g., tests are failing)"
              className="flex-1 bg-bg border border-border rounded px-2 py-1 text-[11px] text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary"
            />
            <button
              type="button"
              onClick={handleFeedbackSubmit}
              className="px-2 py-1 bg-primary text-white text-[11px] rounded hover:bg-primary/80 transition-colors"
            >
              OK
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add ui/src/components/work/TodoItem.tsx
git commit -m "feat(ui): add TodoItem with checkbox, drag handle, and undo-with-feedback"
```

---

### Task 8: TodoList with Drag-and-Drop

**Files:**
- Create: `ui/src/components/work/TodoList.tsx`

- [ ] **Step 1: Create TodoList.tsx**

Create `ui/src/components/work/TodoList.tsx`:

```typescript
import { useCallback } from 'react'
import {
  DndContext,
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core'
import {
  SortableContext,
  sortableKeyboardCoordinates,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable'
import type { Todo } from '@/lib/types'
import { TodoItem } from './TodoItem'
import { useWorkStore } from '@/stores/useWorkStore'

interface TodoListProps {
  todos: Todo[]
  onCheck: (id: string) => void
  onUncheck: (id: string, reason?: string) => void
  onReorder: (activeId: string, overId: string) => void
}

export function TodoList({ todos, onCheck, onUncheck, onReorder }: TodoListProps) {
  const recordChange = useWorkStore((s) => s.recordChange)
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )

  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      const { active, over } = event
      if (over && active.id !== over.id) {
        onReorder(String(active.id), String(over.id))
        recordChange({ type: 'todo_reordered', id: String(active.id) })
      }
    },
    [onReorder, recordChange],
  )

  return (
    <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
      <SortableContext items={todos.map((t) => t.id)} strategy={verticalListSortingStrategy}>
        <div className="space-y-1">
          {todos.map((todo) => (
            <TodoItem key={todo.id} todo={todo} onCheck={onCheck} onUncheck={onUncheck} />
          ))}
        </div>
      </SortableContext>
    </DndContext>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add ui/src/components/work/TodoList.tsx
git commit -m "feat(ui): add TodoList with @dnd-kit sortable drag-and-drop"
```

---

### Task 9: PlanStepItem and PlanCard Components

**Files:**
- Create: `ui/src/components/work/PlanStepItem.tsx`
- Create: `ui/src/components/work/PlanCard.tsx`

- [ ] **Step 1: Create PlanStepItem.tsx**

Create `ui/src/components/work/PlanStepItem.tsx`:

```typescript
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
```

- [ ] **Step 2: Create PlanCard.tsx**

Create `ui/src/components/work/PlanCard.tsx`:

```typescript
import { useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import type { Plan, PlanStatus } from '@/lib/types'
import { PlanStepItem } from './PlanStepItem'

const STATUS_STYLE: Record<PlanStatus, string> = {
  proposed: 'text-warning bg-warning/10',
  approved: 'text-success bg-success/10',
  in_progress: 'text-info bg-info/10',
  complete: 'text-fg-muted bg-surface',
  abandoned: 'text-fg-faint bg-surface/50',
}

interface PlanCardProps {
  plan: Plan
  onStepCheck: (planId: string, stepId: string) => void
  onStepUncheck: (planId: string, stepId: string, reason?: string) => void
}

export function PlanCard({ plan, onStepCheck, onStepUncheck }: PlanCardProps) {
  const [expanded, setExpanded] = useState(true)
  const doneCount = plan.steps.filter((s) => s.status === 'done').length
  const totalCount = plan.steps.length
  const progressPct = totalCount > 0 ? (doneCount / totalCount) * 100 : 0

  return (
    <div className="rounded-md border border-border-subtle bg-bg-elevated overflow-hidden">
      {/* Header */}
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="flex items-center gap-2 w-full px-2 py-1.5 text-left hover:bg-surface/30 transition-colors"
      >
        {expanded ? (
          <ChevronDown className="w-3 h-3 text-fg-faint shrink-0" />
        ) : (
          <ChevronRight className="w-3 h-3 text-fg-faint shrink-0" />
        )}
        <span className="flex-1 text-xs font-medium text-fg truncate">{plan.title}</span>
        <span className={`text-[9px] px-1.5 py-0.5 rounded ${STATUS_STYLE[plan.status]}`}>
          {plan.status}
        </span>
      </button>

      {/* Steps */}
      {expanded && (
        <div className="px-2 pb-2">
          <div className="border-l-2 border-border-subtle pl-2 ml-1 space-y-0.5">
            {plan.steps.map((step) => (
              <PlanStepItem
                key={step.id}
                step={step}
                onCheck={(stepId) => onStepCheck(plan.id, stepId)}
                onUncheck={(stepId, reason) => onStepUncheck(plan.id, stepId, reason)}
              />
            ))}
          </div>

          {/* Progress bar */}
          {totalCount > 0 && (
            <div className="flex items-center gap-1.5 mt-2 pt-1.5 border-t border-border-subtle/50">
              <div className="flex-1 h-1 bg-surface rounded-full overflow-hidden">
                <div
                  className="h-full bg-primary rounded-full transition-all duration-300"
                  style={{ width: `${progressPct}%` }}
                />
              </div>
              <span className="text-[9px] text-fg-muted">{doneCount}/{totalCount}</span>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 3: Verify both compile**

Run: `cd ui && npx tsc --noEmit --pretty 2>&1 | grep -E "PlanCard|PlanStepItem" || echo "No errors"`

Expected: No errors.

- [ ] **Step 4: Commit**

```bash
git add ui/src/components/work/PlanStepItem.tsx ui/src/components/work/PlanCard.tsx
git commit -m "feat(ui): add PlanCard and PlanStepItem with step toggle and progress bar"
```

---

### Task 10: WorkTab — Main Tab Component

**Files:**
- Create: `ui/src/components/work/WorkTab.tsx`

- [ ] **Step 1: Create WorkTab.tsx**

Create `ui/src/components/work/WorkTab.tsx`:

```typescript
import { useCallback, useMemo } from 'react'
import { ChevronDown, ChevronRight, ListTodo, Map } from 'lucide-react'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Skeleton } from '@/components/ui/skeleton'
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from '@/components/ui/empty'
import { useAppStore } from '@/stores/useAppStore'
import { useWorkStore } from '@/stores/useWorkStore'
import { useTodos, useCreateTodo, useToggleTodo, useUpdateTodo } from '@/hooks/useTodos'
import { usePlans, useTogglePlanStep } from '@/hooks/usePlans'
import { useWorkSync } from '@/hooks/useWorkSync'
import { TodoList } from './TodoList'
import { PlanCard } from './PlanCard'
import { AddItemInput } from './AddItemInput'
import { useState } from 'react'
import { arrayMove } from '@dnd-kit/sortable'
import type { Todo } from '@/lib/types'

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
  const toggleTodo = useToggleTodo()
  const updateTodo = useUpdateTodo()
  const togglePlanStep = useTogglePlanStep()

  const [todosOpen, setTodosOpen] = useState(true)
  const [plansOpen, setPlansOpen] = useState(true)

  // Sort todos by metadata.sort_order, falling back to created_at
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
      // Persist sort_order for each item
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
      if (!scopeId && scope !== 'workspace') return
      createTodo.mutate({
        title,
        scope,
        scope_id: scopeId ?? undefined,
        priority: 'medium',
      })
    },
    [scope, scopeId, createTodo],
  )

  const isLoading = todosLoading || plansLoading
  const isEmpty = !isLoading && todos.length === 0 && plans.length === 0

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
        {!isLoading && (todos.length > 0 || (scopeId || scope === 'workspace')) && (
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
                    disabled={!scopeId && scope !== 'workspace'}
                  />
                </div>
              </>
            )}
          </div>
        )}

        {/* Plans section */}
        {!isLoading && (plans.length > 0 || (scopeId || scope === 'workspace')) && (
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
                  />
                ))}
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
```

- [ ] **Step 2: Verify it compiles**

Run: `cd ui && npx tsc --noEmit --pretty 2>&1 | grep WorkTab || echo "No errors"`

Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add ui/src/components/work/WorkTab.tsx
git commit -m "feat(ui): add WorkTab with collapsible Todos/Plans sections and scope switcher"
```

---

### Task 11: Wire WorkTab into RightRail and Delete Old Files

**Files:**
- Modify: `ui/src/components/RightRail.tsx`
- Delete: `ui/src/components/tasks/SessionTasksTab.tsx`
- Delete: `ui/src/components/tasks/TaskStatusBadge.tsx`
- Delete: `ui/src/hooks/useSessionTasks.ts`
- Delete: `ui/src/components/chat/envelopes/SessionTaskCard.tsx`

- [ ] **Step 1: Update RightRail.tsx imports**

In `ui/src/components/RightRail.tsx`:

Replace the import on line 18:
```typescript
import { SessionTasksTab } from './tasks/SessionTasksTab'
```
with:
```typescript
import { WorkTab } from './work/WorkTab'
```

Also replace the `ListTodo` icon import (line 2) — keep it since it's still used. Change the CORE_TABS entry on line 24:

Replace:
```typescript
  { id: 'tasks' as const, icon: ListTodo, label: 'Tasks' },
```
with:
```typescript
  { id: 'work' as const, icon: ListTodo, label: 'Work' },
```

- [ ] **Step 2: Update the tab content rendering**

In `ui/src/components/RightRail.tsx`, replace the block at line 190-192:

```typescript
        {activeTab === 'tasks' && (
          <SessionTasksTab />
        )}
```
with:
```typescript
        {activeTab === 'work' && (
          <WorkTab />
        )}
```

Also update the plugin tab exclusion list at line 203 — replace `'tasks'` with `'work'`:

```typescript
        {!['widgets', 'work', 'artifacts', 'inbox'].includes(activeTab) && (() => {
```

- [ ] **Step 3: Delete old session task files**

Run:
```bash
rm ui/src/components/tasks/SessionTasksTab.tsx
rm ui/src/components/tasks/TaskStatusBadge.tsx
rmdir ui/src/components/tasks
rm ui/src/hooks/useSessionTasks.ts
rm ui/src/components/chat/envelopes/SessionTaskCard.tsx
```

- [ ] **Step 4: Remove SessionTaskCard from envelope registry**

In `ui/src/generated/plugin-envelopes.ts`, delete lines 23-31 (the `"session-task"` CORE_ENTRIES block):

```typescript
  // Task system (core)
  "session-task": {
    component: lazy(() =>
      import("@/components/chat/envelopes/SessionTaskCard").then((m) => ({
        default: m.SessionTaskCard,
      })),
    ),
    source: "core",
  },
```

- [ ] **Step 5: Verify everything compiles**

Run: `cd ui && npx tsc --noEmit --pretty 2>&1 | head -30`

Expected: Clean compile (0 errors). If there are remaining references to `SessionTask` or `useSessionTasks`, grep and fix them:

Run: `cd ui && grep -r "SessionTask\|useSessionTasks\|SessionTasksTab\|TaskStatusBadge" src/ --include="*.ts" --include="*.tsx" | grep -v node_modules`

Expected: No output.

- [ ] **Step 6: Verify the dev server builds**

Run: `cd ui && npm run build 2>&1 | tail -5`

Expected: Build succeeds.

- [ ] **Step 7: Commit**

```bash
git add -A ui/src/components/RightRail.tsx ui/src/generated/plugin-envelopes.ts
git add -A ui/src/components/tasks/ ui/src/hooks/useSessionTasks.ts ui/src/components/chat/envelopes/SessionTaskCard.tsx
git commit -m "feat(ui): replace Tasks tab with Work tab, delete old session-task code"
```

---

### Task 12: Toast in Composer Chrome

**Files:**
- Modify: `ui/src/components/chat/ChatComposer.tsx`

- [ ] **Step 1: Add toast rendering to ChatComposer**

In `ui/src/components/chat/ChatComposer.tsx`, add the import at the top:

```typescript
import { useWorkStore } from '@/stores/useWorkStore'
```

Inside the `ChatComposer` function body (after the existing state declarations around line 65), add:

```typescript
  const workToast = useWorkStore((s) => s.toastMessage)
  const dismissWorkToast = useWorkStore((s) => s.dismissToast)
```

Add a useEffect for auto-dismiss (after existing useEffects):

```typescript
  useEffect(() => {
    if (!workToast) return
    const timer = setTimeout(() => dismissWorkToast(), 3000)
    return () => clearTimeout(timer)
  }, [workToast, dismissWorkToast])
```

In the JSX, add the toast render inside the composer container, right after the `{shellRunning && (...)}` block (around line 411) and before the `{pendingShellCommand && (...)}` block:

```typescript
        {workToast && (
          <div className="px-3 py-1.5 text-xs text-center border-b border-primary/30 bg-primary/5 flex items-center justify-center gap-2">
            <span className="w-3.5 h-3.5 bg-primary rounded-full flex items-center justify-center shrink-0">
              <Check className="w-2 h-2 text-white" />
            </span>
            <span className="text-fg-secondary">{workToast}</span>
            <button
              type="button"
              onClick={dismissWorkToast}
              className="text-fg-faint hover:text-fg-muted ml-1"
            >
              <X className="w-3 h-3" />
            </button>
          </div>
        )}
```

Note: `Check` and `X` are already imported from lucide-react in this file.

- [ ] **Step 2: Wire auto-inject into sendMessage**

In `ui/src/components/chat/ChatComposer.tsx`, add the import:

```typescript
import { useWorkSync } from '@/hooks/useWorkSync'
```

Inside the function body, add:

```typescript
  const { flushIfDirty } = useWorkSync()
```

In the `handleSend` callback (around line 336), add `flushIfDirty()` before `onSend(text)`:

```typescript
  const handleSend = useCallback(() => {
    if (!editor) return
    const text = editor.getText().trim()
    if (!text) return
    pushHistory(text)

    // Detect ! prefix — route to shell exec
    if (text.startsWith('!') && text.length > 1) {
      const command = text.slice(1).trim()
      if (command) {
        editor.commands.clearContent()
        void handleShellExec(command)
        return
      }
    }

    // Auto-sync work changes before sending
    void flushIfDirty()

    onSend(text)
    editor.commands.clearContent()
  }, [editor, onSend, handleShellExec, flushIfDirty])
```

- [ ] **Step 3: Verify it compiles**

Run: `cd ui && npx tsc --noEmit --pretty 2>&1 | head -10`

Expected: Clean compile.

- [ ] **Step 4: Commit**

```bash
git add ui/src/components/chat/ChatComposer.tsx
git commit -m "feat(ui): add work sync toast and auto-inject in composer"
```

---

### Task 13: Envelope Cards — TodoListCard and PlanReviewCard

**Files:**
- Create: `ui/src/components/chat/envelopes/TodoListCard.tsx`
- Create: `ui/src/components/chat/envelopes/PlanReviewCard.tsx`
- Modify: `ui/src/generated/plugin-envelopes.ts`

- [ ] **Step 1: Create TodoListCard.tsx**

Create `ui/src/components/chat/envelopes/TodoListCard.tsx`:

```typescript
import { Check } from 'lucide-react'
import { useToggleTodo } from '@/hooks/useTodos'
import { useTodos } from '@/hooks/useTodos'

interface TodoListCardData {
  scope: string
  scope_id: string
  title?: string
}

interface TodoListCardProps {
  data: TodoListCardData
}

export function TodoListCard({ data }: TodoListCardProps) {
  const { data: todos = [] } = useTodos({
    scope: data.scope,
    scope_id: data.scope_id,
  })
  const toggleTodo = useToggleTodo()

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/60 overflow-hidden my-2">
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-border/50">
        <span className="text-sm font-semibold text-fg">{data.title || 'Todos'}</span>
        <span className="text-[10px] text-fg-muted">{todos.length} items</span>
      </div>
      <div className="px-3 py-1.5 space-y-1">
        {todos.map((todo) => {
          const isDone = todo.status === 'done'
          return (
            <div key={todo.id} className="flex items-center gap-2 py-0.5">
              <button
                type="button"
                onClick={() => {
                  if (isDone) {
                    toggleTodo.uncheck(todo.id)
                  } else {
                    toggleTodo.check(todo.id)
                  }
                }}
                className={`w-3 h-3 rounded-sm border-2 flex items-center justify-center shrink-0 transition-colors ${
                  isDone ? 'bg-primary border-primary' : 'border-primary'
                }`}
              >
                {isDone && <Check className="w-2 h-2 text-white" />}
              </button>
              <span className={`text-xs ${isDone ? 'text-fg-muted line-through' : 'text-fg'}`}>
                {todo.title}
              </span>
            </div>
          )
        })}
        {todos.length === 0 && (
          <span className="text-xs text-fg-faint">No todos</span>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Create PlanReviewCard.tsx**

Create `ui/src/components/chat/envelopes/PlanReviewCard.tsx`:

```typescript
import { useState } from 'react'
import { useApprovePlan, useRejectPlan, useUpdatePlan } from '@/hooks/usePlans'
import type { PlanStatus } from '@/lib/types'

const STATUS_STYLE: Record<PlanStatus, string> = {
  proposed: 'text-warning bg-warning/10',
  approved: 'text-success bg-success/10',
  in_progress: 'text-info bg-info/10',
  complete: 'text-fg-muted bg-surface',
  abandoned: 'text-fg-faint bg-surface/50',
}

interface PlanReviewCardData {
  plan_id: string
  title: string
  description?: string
  status: PlanStatus
  steps: Array<{ id: string; title: string }>
}

interface PlanReviewCardProps {
  data: PlanReviewCardData
}

export function PlanReviewCard({ data }: PlanReviewCardProps) {
  const approvePlan = useApprovePlan()
  const { reject } = useRejectPlan()
  const updatePlan = useUpdatePlan()
  const [acted, setActed] = useState(false)
  const [currentStatus, setCurrentStatus] = useState<PlanStatus>(data.status)
  const [editing, setEditing] = useState(false)
  const [editSteps, setEditSteps] = useState(data.steps)
  const [newStepTitle, setNewStepTitle] = useState('')

  const handleApprove = () => {
    // If user edited steps, save them first
    if (editing) {
      updatePlan.mutate(
        {
          id: data.plan_id,
          updates: {
            steps: editSteps.map((s) => ({
              ...s,
              status: 'pending' as const,
              depends_on: [],
            })),
          },
        },
        {
          onSuccess: () => {
            approvePlan.mutate(
              { id: data.plan_id, createTodos: true },
              {
                onSuccess: () => {
                  setCurrentStatus('approved')
                  setActed(true)
                  setEditing(false)
                },
              },
            )
          },
        },
      )
    } else {
      approvePlan.mutate(
        { id: data.plan_id, createTodos: true },
        {
          onSuccess: () => {
            setCurrentStatus('approved')
            setActed(true)
          },
        },
      )
    }
  }

  const handleReject = () => {
    reject(data.plan_id)
    setCurrentStatus('abandoned')
    setActed(true)
  }

  const handleAddStep = () => {
    const trimmed = newStepTitle.trim()
    if (!trimmed) return
    setEditSteps((prev) => [
      ...prev,
      { id: `new-${Date.now()}`, title: trimmed },
    ])
    setNewStepTitle('')
  }

  const handleRemoveStep = (stepId: string) => {
    setEditSteps((prev) => prev.filter((s) => s.id !== stepId))
  }

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/60 overflow-hidden my-2">
      <div className="px-3 py-2">
        <div className="flex items-center justify-between mb-1">
          <span className="text-sm font-semibold text-fg">{data.title}</span>
          <span className={`text-[9px] px-1.5 py-0.5 rounded ${STATUS_STYLE[currentStatus]}`}>
            {currentStatus}
          </span>
        </div>
        {data.description && (
          <p className="text-[11px] text-fg-muted mb-2">{data.description}</p>
        )}

        {/* Step list — read-only or editable */}
        <div className="border-l-2 border-border-subtle pl-2 ml-1 mb-2 space-y-0.5">
          {(editing ? editSteps : data.steps).map((step, i) => (
            <div key={step.id} className="flex items-center gap-1 text-[11px] text-fg-secondary py-0.5">
              <span>{i + 1}. {step.title}</span>
              {editing && (
                <button
                  type="button"
                  onClick={() => handleRemoveStep(step.id)}
                  className="text-danger/60 hover:text-danger ml-auto text-[10px]"
                >
                  remove
                </button>
              )}
            </div>
          ))}
          {editing && (
            <div className="flex gap-1 mt-1">
              <input
                type="text"
                value={newStepTitle}
                onChange={(e) => setNewStepTitle(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); handleAddStep() } }}
                placeholder="Add step..."
                className="flex-1 bg-bg border border-border rounded px-1.5 py-0.5 text-[10px] text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary"
              />
              <button
                type="button"
                onClick={handleAddStep}
                className="px-1.5 py-0.5 bg-surface text-fg-muted text-[10px] rounded hover:bg-surface-hover"
              >
                Add
              </button>
            </div>
          )}
        </div>

        {!acted && currentStatus === 'proposed' && (
          <div className="flex gap-2">
            <button
              type="button"
              onClick={handleApprove}
              disabled={approvePlan.isPending || updatePlan.isPending}
              className="px-3 py-1 bg-primary text-white text-[11px] rounded hover:bg-primary/80 transition-colors disabled:opacity-50"
            >
              Approve
            </button>
            <button
              type="button"
              onClick={() => setEditing((v) => !v)}
              className="px-3 py-1 bg-surface text-fg-secondary text-[11px] rounded hover:bg-surface-hover transition-colors"
            >
              {editing ? 'Done Editing' : 'Edit Steps'}
            </button>
            <button
              type="button"
              onClick={handleReject}
              className="px-3 py-1 bg-surface text-fg-muted text-[11px] rounded hover:bg-surface-hover transition-colors"
            >
              Reject
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 3: Register envelopes in plugin-envelopes.ts**

In `ui/src/generated/plugin-envelopes.ts`, add two new entries to the `CORE_ENTRIES` object (after the `"question-form"` entry, before the `// Reusable primitives` comment):

```typescript
  // Todo/Plan system (core)
  "todo-list": {
    component: lazy(() =>
      import("@/components/chat/envelopes/TodoListCard").then((m) => ({
        default: m.TodoListCard,
      })),
    ),
    source: "core",
  },
  "plan-review": {
    component: lazy(() =>
      import("@/components/chat/envelopes/PlanReviewCard").then((m) => ({
        default: m.PlanReviewCard,
      })),
    ),
    source: "core",
  },
```

- [ ] **Step 4: Verify everything compiles and builds**

Run: `cd ui && npx tsc --noEmit --pretty 2>&1 | head -10`

Expected: Clean compile.

Run: `cd ui && npm run build 2>&1 | tail -5`

Expected: Build succeeds.

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/chat/envelopes/TodoListCard.tsx ui/src/components/chat/envelopes/PlanReviewCard.tsx ui/src/generated/plugin-envelopes.ts
git commit -m "feat(ui): add TodoListCard and PlanReviewCard envelope types"
```

---

### Task 14: Final Verification

**Files:** None (verification only)

- [ ] **Step 1: Clean build**

Run: `cd ui && rm -rf dist && npm run build 2>&1 | tail -10`

Expected: Build completes with no errors.

- [ ] **Step 2: Lint check**

Run: `cd ui && npx biome check src/ 2>&1 | tail -10`

Expected: No new errors (existing warnings are OK).

- [ ] **Step 3: Verify no dead imports**

Run: `cd ui && grep -r "SessionTask\|useSessionTasks\|SessionTasksTab\|TaskStatusBadge\|SessionTaskCard" src/ --include="*.ts" --include="*.tsx"`

Expected: No output (all references cleaned up).

- [ ] **Step 4: Verify all new files exist**

Run:
```bash
ls -la ui/src/components/work/WorkTab.tsx \
       ui/src/components/work/TodoItem.tsx \
       ui/src/components/work/TodoList.tsx \
       ui/src/components/work/PlanCard.tsx \
       ui/src/components/work/PlanStepItem.tsx \
       ui/src/components/work/AddItemInput.tsx \
       ui/src/hooks/useTodos.ts \
       ui/src/hooks/usePlans.ts \
       ui/src/hooks/useWorkSync.ts \
       ui/src/stores/useWorkStore.ts \
       ui/src/components/chat/envelopes/TodoListCard.tsx \
       ui/src/components/chat/envelopes/PlanReviewCard.tsx
```

Expected: All 12 files listed.

- [ ] **Step 5: Verify old files are gone**

Run:
```bash
ls ui/src/components/tasks/ 2>&1
ls ui/src/hooks/useSessionTasks.ts 2>&1
ls ui/src/components/chat/envelopes/SessionTaskCard.tsx 2>&1
```

Expected: All return "No such file or directory".

- [ ] **Step 6: Commit (if any lint fixes were needed)**

```bash
git add -A ui/src/
git commit -m "chore(ui): lint fixes for todo/plan UI"
```
