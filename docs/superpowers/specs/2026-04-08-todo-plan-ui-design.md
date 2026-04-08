# Todo/Plan Collaborative UI — Design Spec

**Date:** 2026-04-08
**Status:** Approved
**Scope:** Frontend only (backend APIs already complete)

## Summary

Replace the existing "Tasks" RightRail tab with a "Work" tab providing interactive todo and plan management. Users can check/uncheck items, drag to reorder, add new items, and provide feedback when reopening completed items. Changes sync to agents via batched diffs and backend events, reducing token churn on review cycles.

Three interaction layers:

1. **RightRail "Work" tab** — persistent, deterministic, zero-token interaction
2. **Envelope cards in chat** — agent-initiated, inline, interactive
3. **Backend events** — `todo.updated`, `plan.updated`, `plan.approved` for agent/plugin hooks

## Layout

**Collapsible sections** (not sub-tabs) — Todos and Plans visible at once in the Work tab. Each section has a collapsible header with item count. Scope switcher at the top toggles between Session (default) and Project.

### Work Tab Structure

```
[Work]                          [Session ▾ | Project]
─────────────────────────────────────────────────────
▼ Todos (3)
  ⠿ ☐ Implement auth middleware              [high]
  ⠿ ☐ Write API integration tests            [med]
  ⠿ ☑ Set up database schema (strikethrough)
  [+ Add todo...]

▼ Plans (1)
  ┌─ Auth System Rollout              [approved]
  │  ☑ Design schema (strikethrough)
  │  ☐ Implement middleware
  │  ☐ Write tests
  │  ━━━━━━━━━━━━━━━━━░░░░░░░  1/3
  └─
  [+ Add plan...]

                              [Send Changes] (inactive)
```

## Interaction Model

### Checkbox Toggle (done/undone)

- **Check (mark done):** Immediate optimistic update. Item grays out with strikethrough. Marks dirty.
- **Uncheck (reopen):** Item highlights amber with "reopened" label. Inline feedback input expands below the item:
  - Text input with placeholder: `"Why? (e.g., tests are failing)"`
  - Enter or OK button submits the reason
  - Escape collapses without reason (reopen still happens, feedback is optional but encouraged)
  - Feedback stored in `todo.metadata.reopen_reason` (or `plan step` equivalent)
  - Marks dirty

### Drag-and-Drop Reorder

- Library: `@dnd-kit/sortable` (lightweight, accessible, matches existing patterns)
- Grip handle (`⠿`) on left of each todo item
- Plans themselves are reorderable; steps within a plan are independently reorderable
- Reorder persists via `todo.metadata.sort_order` (integer) on each item's update endpoint. No backend schema change needed.
- Marks dirty

### Adding Items

- "+" row at bottom of each section. Click expands an inline text input.
- Enter creates the item, Escape cancels.
- New todos: `status: "pending"`, `priority: "medium"`, scoped to current scope.
- New plan steps: appended to the step list of the currently expanded plan.
- Marks dirty.

### Send Changes Flow

1. All mutations (check, uncheck, reorder, add, edit) save to the backend immediately via React Query mutations (optimistic updates).
2. `useWorkSync` hook tracks a "dirty since last sync" flag. Any mutation sets it.
3. "Send Changes" button at bottom of WorkTab:
   - **Inactive** (grayed out) when no pending changes
   - **Active** (indigo, shows change count badge) when dirty
4. Clicking "Send Changes":
   - Collects a batched diff of all changes since last sync
   - Posts `WorkDiff` to `POST /api/work/sync` (new endpoint — emits events, returns confirmation). This is a small backend addition needed.
   - Clears dirty flag
   - Shows toast in composer chrome
5. **Auto-inject on next message:** If user sends a chat message while dirty, the system automatically injects the diff as a system context item alongside the user's message. Clears dirty flag. Shows toast.
6. **Toast:** Renders in the existing composer chrome area (same location as `ShellInfoDrawer`, shell approval bar, and "Running command..." pulse). Pattern: conditional render above the TipTap editor, `px-3 py-1.5 text-xs border-b` styling. Indigo check icon, text: `"Tasks updated — agent notified"`. Auto-dismiss after 3 seconds. Manual close button.

## Scoping

- **Session (default in chat):** Todos/plans for the active session. Filtered by `scope=session&scope_id={sessionId}`.
- **Project:** Todos/plans for the project associated with the working directory. Filtered by `scope=project&scope_id={projectId}`.
- **No project exists:** When user switches to Project scope and no project is found, show a prompt: "No project found for this directory. Create one?" with a single-click create action that creates the project and associates the working directory path(s).
- **Workspace:** Not in v1. Can be added if there is demand.

## Envelope Cards

Two new envelope types registered in `generated/plugin-envelopes.ts`:

### `todo-list` Envelope

Agent sends this to show current session todos inline in chat. Renders as a card with:
- Checkable items (same toggle behavior as the tab, including undo-with-feedback)
- Changes feed into the same `useWorkSync` dirty tracking
- Compact display (no drag handles — reorder is tab-only)

### `plan-review` Envelope

Agent sends this when proposing a plan. Renders as a card with:
- Plan title, status badge, description
- Step list with numbering
- Action buttons:
  - **Approve** — calls `POST /api/plans/{id}/approve` with `{ create_todos: true }`. Creates linked todos, updates status to `approved`.
  - **Edit Steps** — expands inline step editor (add/remove/reorder before approving)
  - **Reject** — sets status to `abandoned`

## Backend Events

Emitted on the existing event bus for agent/plugin hooks:

- `todo.created` — new todo added
- `todo.updated` — status change, reorder, metadata update (includes reopen_reason)
- `todo.deleted` — todo removed
- `plan.created` — new plan added
- `plan.updated` — status change, step modifications
- `plan.approved` — plan approved (with or without linked todo creation)
- `plan.abandoned` — plan rejected

## New Files

| File | Purpose |
|------|---------|
| `components/work/WorkTab.tsx` | Top-level tab: scope switcher, collapsible Todos + Plans sections, "Send Changes" button |
| `components/work/TodoItem.tsx` | Single todo row: drag handle, checkbox, title, priority badge, inline feedback expansion |
| `components/work/TodoList.tsx` | Sortable todo list container (`@dnd-kit/sortable`) |
| `components/work/PlanCard.tsx` | Plan header + collapsible step list with progress bar, status badge |
| `components/work/PlanStepItem.tsx` | Single plan step: checkbox, title, dependency indicator |
| `components/work/AddItemInput.tsx` | Inline "+" input for adding todos or plan steps |
| `hooks/useTodos.ts` | React Query: `useTodos`, `useCreateTodo`, `useUpdateTodo`, `useDeleteTodo` |
| `hooks/usePlans.ts` | React Query: `usePlans`, `useCreatePlan`, `useUpdatePlan`, `useApprovePlan` |
| `hooks/useWorkSync.ts` | Dirty tracking, batched diff generation, auto-inject on next message |
| `stores/useWorkStore.ts` | Zustand: pending change queue, dirty flag, scope selection |
| `chat/envelopes/TodoListCard.tsx` | Envelope: interactive todo checklist in chat |
| `chat/envelopes/PlanReviewCard.tsx` | Envelope: plan with Approve/Edit/Reject actions |

## Modified Files

| File | Change |
|------|--------|
| `components/RightRail.tsx` | Replace `tasks` tab entry with `work`, render `WorkTab` |
| `components/chat/ChatComposer.tsx` | Add toast conditional render in composer chrome area |
| `generated/plugin-envelopes.ts` | Register `todo-list` and `plan-review` envelope types |
| `lib/api.ts` | Add 13 API client functions for todo/plan endpoints |
| `lib/types.ts` | Add `Todo`, `TodoFilter`, `Plan`, `PlanStep`, `PlanFilter`, `WorkDiff` interfaces |

## Deleted Files (superseded)

| File | Reason |
|------|--------|
| `components/tasks/SessionTasksTab.tsx` | Replaced by `WorkTab` |
| `components/tasks/TaskStatusBadge.tsx` | Replaced by inline status rendering in `TodoItem` / `PlanStepItem` |
| `hooks/useSessionTasks.ts` | Replaced by `useTodos.ts` / `usePlans.ts` |
| `components/chat/envelopes/SessionTaskCard.tsx` | Replaced by `TodoListCard` / `PlanReviewCard` envelopes |

Delete the `components/tasks/` directory entirely (no remaining files after removal).

Additionally, remove from `lib/types.ts`:
- `SessionTask` interface
- `SessionTaskStatus` type

Remove from `lib/api.ts`:
- `listSessionTasks()`
- `createSessionTask()`
- `updateSessionTask()`
- `transitionSessionTask()`

Remove from `RightRail.tsx`:
- `tasks` tab definition and `SessionTasksTab` import

## API Client Functions (additions to `lib/api.ts`)

### Todos
```typescript
listTodos(filter?: TodoFilter): Promise<Todo[]>
  // GET /api/todos?scope=...&scope_id=...&status=...&priority=...
createTodo(data: { title: string; scope: string; scope_id?: string; priority?: string; description?: string }): Promise<Todo>
  // POST /api/todos
getTodo(id: string): Promise<Todo>
  // GET /api/todos/{id}
updateTodo(id: string, updates: Partial<Pick<Todo, 'title' | 'description' | 'status' | 'priority' | 'labels' | 'metadata'>>): Promise<Todo>
  // PUT /api/todos/{id}
deleteTodo(id: string): Promise<void>
  // DELETE /api/todos/{id}
listTodoChildren(id: string): Promise<Todo[]>
  // GET /api/todos/{id}/children
```

### Plans
```typescript
listPlans(filter?: PlanFilter): Promise<Plan[]>
  // GET /api/plans?scope=...&scope_id=...&status=...
createPlan(data: { title: string; scope: string; scope_id?: string; description?: string; steps?: PlanStep[] }): Promise<Plan>
  // POST /api/plans
getPlan(id: string): Promise<Plan>
  // GET /api/plans/{id}
updatePlan(id: string, updates: Partial<Pick<Plan, 'title' | 'description' | 'status' | 'steps' | 'metadata'>>): Promise<Plan>
  // PUT /api/plans/{id}
updatePlanStep(planId: string, stepId: string, updates: Partial<Pick<PlanStep, 'title' | 'status' | 'notes'>>): Promise<Plan>
  // PUT /api/plans/{id}/steps/{stepId}
deletePlan(id: string): Promise<void>
  // DELETE /api/plans/{id}
approvePlan(id: string, createTodos?: boolean): Promise<Plan>
  // POST /api/plans/{id}/approve { create_todos: true }
```

## TypeScript Interfaces (additions to `lib/types.ts`)

```typescript
type TodoStatus = 'pending' | 'in_progress' | 'done' | 'blocked'
type TodoPriority = 'low' | 'medium' | 'high' | 'critical'
type PlanStatus = 'proposed' | 'approved' | 'in_progress' | 'complete' | 'abandoned'
type PlanStepStatus = 'pending' | 'in_progress' | 'done' | 'skipped'

interface Todo {
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

interface TodoFilter {
  scope?: string
  scope_id?: string
  status?: TodoStatus
  priority?: TodoPriority
  parent_id?: string
  labels?: string[]
}

interface PlanStep {
  id: string
  title: string
  status: PlanStepStatus
  todo_id?: string
  depends_on: string[]
  acceptance?: string
  notes?: string
}

interface Plan {
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

interface PlanFilter {
  scope?: string
  scope_id?: string
  status?: PlanStatus
}

interface WorkDiff {
  todos_checked: string[]      // IDs marked done
  todos_unchecked: Array<{ id: string; reason?: string }>  // IDs reopened with optional feedback
  todos_added: string[]        // IDs of new todos
  todos_reordered: boolean     // whether sort order changed
  plan_steps_checked: Array<{ plan_id: string; step_id: string }>
  plan_steps_unchecked: Array<{ plan_id: string; step_id: string; reason?: string }>
  plans_approved: string[]     // IDs of approved plans
  plans_rejected: string[]     // IDs of rejected plans
}
```

## Dependencies

New npm package:
- `@dnd-kit/core` + `@dnd-kit/sortable` + `@dnd-kit/utilities` — drag-and-drop

## Workflow & Memory (placeholder — future specs)

### Workflow Progress (future)

Reserved: a "Workflows" collapsible section in WorkTab, collapsed by default, hidden when empty. Blocked on HTTP API endpoints being exposed from the internal workflow engine (`internal/workflow/`). Design will follow the same collapsible section pattern with pipeline name as header and steps as a vertical progress indicator with status icons.

### Memory Viewer (future)

Reserved: a future RightRail tab or widget. Will show recalled memories for the current session, extraction history, confidence scores, namespace hierarchy. Blocked on HTTP API for memory recall/list being exposed from `internal/memory/`.
