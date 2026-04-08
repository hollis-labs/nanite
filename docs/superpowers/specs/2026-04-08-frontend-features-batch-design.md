# Frontend Features Batch — Design Spec

**Date:** 2026-04-08
**Scope:** 3 features (Worker Status Settings Panel, Workflow Progress Panel, Memory Viewer)

## Overview

Three frontend features for the Nanite chat harness UI. Two require new backend REST APIs; one extends existing UI only.

| Feature | Backend Work | Frontend Work | Effort |
|---------|-------------|---------------|--------|
| Worker Status Settings Panel | None (APIs ready) | New panel in Observability | Low |
| Workflow Progress Panel | New REST + RunStore + SSE | New RightRail tab + components | Medium |
| Memory Viewer | New REST wrapping MemoryService | Modal + CRUD + slash command | Medium-High |

**Already complete (discovered during scoping):**
- Tool Load Preferences UI — fully implemented in `ToolDashboard.tsx`
- Worker Status Widget — exists as `WorkerStatusWidget.tsx` in RightRail

---

## 1. Worker Status Settings Panel

### What

Full-detail worker status table in Settings > Observability, alongside the existing ProcessHealthPanel. Shows all workers (not just active), with timestamps, session links, and cancel actions.

### Existing Infrastructure

- `GET /api/workers` — returns `Worker[]` with id, type, parent_session_id, session_id, task_id, agent_id, status, worktree_path, created_at
- `POST /api/workers/{id}/cancel` — cancels a running worker
- `WorkerStatusWidget.tsx` — compact widget already in RightRail (active workers only, 5s polling)

### Frontend Components

**New file:** `ui/src/components/settings/observability/WorkerStatusPanel.tsx`

Table columns:
- **Agent** — `agent_id`
- **Type** — `full` / `light` badge
- **Status** — colored badge (spawning=amber pulse, running=blue, completed=green, failed=red, cancelled=muted)
- **Parent Session** — truncated ID, clickable to navigate
- **Created** — relative time
- **Worktree** — path if present, monospace truncated
- **Actions** — Cancel button for spawning/running workers

Header summary line: "N workers (M active)" with optional cancel-all for active workers.

Uses `GET /api/workers` with 5s `refetchInterval` via React Query (same pattern as widget).

**Integration:** Add as a new `<Card title="Worker Status">` in `ObservabilityDashboard.tsx`, positioned after Process Health.

### Data Flow

```
useQuery(['workers'], api.listWorkers, { refetchInterval: 5000 })
  → WorkerStatusPanel renders table
  → Cancel button → useMutation(api.cancelWorker) → invalidate ['workers']
```

---

## 2. Workflow Progress Panel

### What

New RightRail tab showing internal pipeline workflow runs with step-level progress. Real-time updates via SSE. List view with run cards, detail view with step timeline.

### Backend Work Required

#### RunStore (in-memory)

The workflow executor (`internal/workflow/executor.go`) runs pipelines in-memory with no persistence. A lightweight RunStore is needed to hold recent runs for API access.

**New file:** `internal/workflow/store.go`

```go
type RunStore struct {
    mu   sync.RWMutex
    runs []*RunRecord    // ring buffer, newest first
    cap  int             // default 50
}

type RunRecord struct {
    Pipeline    PipelineInfo           // id, name, description, step_count
    Run         *RunState              // full run state with step states
    Events      []Event                // event log for this run
}

type PipelineInfo struct {
    ID          string
    Name        string
    Description string
    StepCount   int
}
```

The executor's `EventHandler` writes to the RunStore. On `pipeline.started`, a new RunRecord is created. Step and pipeline events update the record in place and append to the event log.

#### REST API Endpoints

**New file:** `internal/api/workflows.go`

| Endpoint | Method | Response | Notes |
|----------|--------|----------|-------|
| `/api/workflows` | GET | `PipelineInfo[]` | List registered pipelines |
| `/api/workflows/runs` | GET | `RunRecord[]` | List recent runs (from RunStore). Query params: `?status=running&pipeline_id=X` |
| `/api/workflows/runs/{runId}` | GET | `RunRecord` | Single run with all step states |
| `/api/workflows/runs/{runId}/cancel` | POST | `{ status: "cancelled" }` | Cancel via context cancellation |
| `/api/workflows/events` | GET (SSE) | Event stream | Real-time workflow events. Same SSE pattern as `/api/stream/{id}`. Events: `pipeline.started`, `step.started`, `step.completed`, `step.failed`, `step.skipped`, `pipeline.completed`, `pipeline.failed`, `pipeline.cancelled` |

#### SSE Event Format

```json
{
  "type": "step.completed",
  "pipeline_id": "deploy-staging",
  "run_id": "a3f8c...",
  "step_id": "build",
  "data": {},
  "timestamp": "2026-04-08T12:00:00Z"
}
```

The SSE endpoint subscribes to the executor's EventHandler via a channel fan-out. Connection cleanup on client disconnect.

### Frontend Components

#### Types

**Add to `ui/src/lib/types.ts`:**

```typescript
interface PipelineInfo {
  id: string
  name: string
  description: string
  step_count: number
}

interface WorkflowRun {
  pipeline: PipelineInfo
  run: RunState
  events: WorkflowEvent[]
}

interface RunState {
  pipeline_id: string
  run_id: string
  status: 'pending' | 'running' | 'completed' | 'failed' | 'cancelled'
  step_states: Record<string, StepState>
  started_at: string
  completed_at: string
  error: string
}

interface StepState {
  step_id: string
  status: 'pending' | 'running' | 'completed' | 'failed' | 'skipped' | 'cancelled'
  attempts: number
  started_at: string
  completed_at: string
  error: string
  skip_reason: string
}

interface WorkflowEvent {
  type: string
  pipeline_id: string
  run_id: string
  step_id: string
  data: Record<string, any>
  timestamp: string
}
```

#### API Client

**Add to `ui/src/lib/api.ts`:**

```typescript
listPipelines(): Promise<PipelineInfo[]>
listWorkflowRuns(params?: { status?: string; pipeline_id?: string }): Promise<WorkflowRun[]>
getWorkflowRun(runId: string): Promise<WorkflowRun>
cancelWorkflowRun(runId: string): Promise<{ status: string }>
```

SSE connection handled in a custom hook, not in the api client.

#### Components

**New files in `ui/src/components/workflows/`:**

| File | Purpose |
|------|---------|
| `WorkflowTab.tsx` | Main tab component. List/detail view switching. Filter toggle (All/Active). |
| `WorkflowRunCard.tsx` | Run summary card: pipeline name, status badge, progress bar, step pills (completed/running/pending). Click navigates to detail. |
| `WorkflowRunDetail.tsx` | Detail view: back button, run metadata, vertical step timeline, cancel button. |
| `WorkflowStepItem.tsx` | Single step in timeline: status icon (✓/●/○/✗/⊘), name, duration, error/skip reason. Connected by vertical line. |

**New hook:** `ui/src/hooks/useWorkflowEvents.ts`

SSE hook connecting to `/api/workflows/events`. On each event, invalidates `['workflow-runs']` query to refresh the list. For the detail view, merges events into local state for instant UI updates before the query refetches.

```typescript
function useWorkflowEvents() {
  // Connect to SSE
  // On event → queryClient.invalidateQueries(['workflow-runs'])
  // Returns: { connected: boolean }
}
```

#### RightRail Tab Registration

Add to `CORE_TABS` in `RightRail.tsx`:

```typescript
{ id: 'workflows' as const, icon: GitBranch, label: 'Workflows' }
```

Add `activeTab === 'workflows'` branch in the content render section, rendering `<WorkflowTab />`.

Update `useLayoutStore` `rightRailTab` type to include `'workflows'`.

#### Layout Store Update

Add `'workflows'` to the `rightRailTab` union type in `useLayoutStore.ts`.

### UI Behavior

**List view (default):**
- Shows runs from RunStore, newest first
- Active filter (default): only running/pending runs
- All filter: includes completed/failed/cancelled
- Each run card shows: pipeline name, status dot + badge, "Started Xm ago · N/M steps", progress bar, step pills
- Completed runs show reduced opacity
- Empty state: "No workflow runs" with muted icon

**Detail view (click a run):**
- Back arrow returns to list
- Header: pipeline name, status badge, cancel button (if running)
- Meta line: run ID (truncated), start time
- Vertical timeline of steps:
  - Completed: green check, step name, duration
  - Running: blue dot (animated), step name, elapsed time
  - Pending: gray circle, step name, dependency info
  - Failed: red X, step name, error message
  - Skipped: gray slash, step name, skip reason
- Steps connected by vertical lines colored by status

### Data Flow

```
SSE /api/workflows/events → useWorkflowEvents hook → invalidate queries
useQuery(['workflow-runs']) → WorkflowTab → WorkflowRunCard[]
Click run → useQuery(['workflow-run', runId]) → WorkflowRunDetail → WorkflowStepItem[]
Cancel → useMutation(api.cancelWorkflowRun) → invalidate queries
```

---

## 3. Memory Viewer

### What

Full CRUD modal for browsing and managing agent memories. Launched via `/memory` slash command in ChatComposer. Browse with filters, view/edit/create/delete memories, manage status lifecycle.

### Backend Work Required

#### REST API Endpoints

**New file:** `internal/api/memories.go`

These endpoints wrap the existing `memory.Service` which uses Vanta Conduit for storage.

| Endpoint | Method | Request | Response | Notes |
|----------|--------|---------|----------|-------|
| `/api/memories` | GET | Query: `?scope=user&status=canonical&q=search&tags=ui,styling&limit=50&offset=0` | `{ memories: Memory[], total: int }` | Paginated list with filters. Uses `memory.Service.Recall()` with query mapping. |
| `/api/memories` | POST | `{ summary, body, origin, confidence, scope, tags }` | `Memory` | Create new memory. Uses `memory.Service.Save()`. |
| `/api/memories/{key}` | PUT | `{ summary, body, origin, confidence, tags }` | `Memory` | Update existing memory. Scope is immutable after creation. |
| `/api/memories/{key}` | DELETE | — | `{ deleted: true }` | Hard delete from Conduit. |
| `/api/memories/{key}/status` | PUT | `{ status: "draft" \| "reviewed" \| "canonical" \| "deprecated" }` | `Memory` | Status transition. Uses `memory.Service` status methods. |

#### Memory REST Type (JSON response)

```json
{
  "memory_key": "string",
  "namespace": "string",
  "summary": "string",
  "body": "string",
  "origin": "user | feedback | project | reference | observation",
  "trigger": "explicit | post_compact | per_turn | promotion | manual",
  "confidence": 0.95,
  "tags": ["styling", "ui"],
  "scope": "session | project | user",
  "session_id": "string",
  "revision_id": "string",
  "status": "draft | reviewed | canonical | deprecated",
  "created_at": "2026-04-08T12:00:00Z",
  "updated_at": "2026-04-08T12:00:00Z"
}
```

### Frontend Components

#### Types

**Add to `ui/src/lib/types.ts`:**

```typescript
interface Memory {
  memory_key: string
  namespace: string
  summary: string
  body: string
  origin: 'user' | 'feedback' | 'project' | 'reference' | 'observation'
  trigger: string
  confidence: number
  tags: string[]
  scope: 'session' | 'project' | 'user'
  session_id: string
  revision_id: string
  status: 'draft' | 'reviewed' | 'canonical' | 'deprecated'
  created_at: string
  updated_at: string
}

interface MemoryListResponse {
  memories: Memory[]
  total: number
}

interface MemoryCreateRequest {
  summary: string
  body?: string
  origin: Memory['origin']
  confidence: number
  scope: Memory['scope']
  tags: string[]
}

interface MemoryUpdateRequest {
  summary: string
  body?: string
  origin: Memory['origin']
  confidence: number
  tags: string[]
}
```

#### API Client

**Add to `ui/src/lib/api.ts`:**

```typescript
listMemories(params?: { scope?: string; status?: string; q?: string; tags?: string; limit?: number; offset?: number }): Promise<MemoryListResponse>
createMemory(data: MemoryCreateRequest): Promise<Memory>
updateMemory(key: string, data: MemoryUpdateRequest): Promise<Memory>
deleteMemory(key: string): Promise<{ deleted: boolean }>
updateMemoryStatus(key: string, status: string): Promise<Memory>
```

#### Components

**New files in `ui/src/components/memory/`:**

| File | Purpose |
|------|---------|
| `MemoryModal.tsx` | Root modal component. Dialog wrapper. Manages view state (browse/detail/create). |
| `MemoryBrowse.tsx` | Browse view: search bar, scope pills, status filter, paginated memory list. |
| `MemoryCard.tsx` | Single memory row in browse list: summary, body preview, origin badge, confidence score, scope badge, age, status badge. Click opens detail. |
| `MemoryDetail.tsx` | Detail/edit view: form with summary, body (textarea), origin (select), confidence (number input), scope (select, disabled on edit), status (pill selector), tags (editable chips). Delete and Save buttons. |

**State management:** Local `useState` in `MemoryModal` for view state (`browse | detail | create`) and selected memory key. No Zustand store needed — modal is self-contained.

#### Slash Command Integration

The `/memory` command is **client-side only** — it toggles modal visibility without sending a message to the backend.

**Integration point:** `ui/src/components/chat/extensions/SlashCommandExtension.ts`

Add a client command entry:

```typescript
{
  name: 'memory',
  description: 'Browse and manage memories',
  category: 'tools',
  clientAction: () => setMemoryModalOpen(true)
}
```

The `clientAction` pattern may need to be added to the slash command extension if it doesn't exist — currently commands send messages. The simplest approach: the slash command inserts nothing and fires a callback that opens the modal. The callback is passed down from `ChatComposer` or accessed via a ref/store.

**Alternative if client commands are too invasive:** Register `/memory` as a regular command that the backend handles, returning an empty response but setting a flag that the frontend reads to open the modal. This is heavier but doesn't change the slash command extension's contract.

Recommended: client-side approach. It's cleaner and avoids a wasted round-trip.

#### Modal Trigger

`MemoryModal` rendered in `AppShell.tsx` (alongside other modals). Visibility controlled by either:
- A `memoryModalOpen` state in `useLayoutStore`
- A simpler approach: a module-level ref/callback that the slash command extension calls

Using `useLayoutStore` is more consistent with how other modals work (SprintPlanningModal uses `useSprintPlanningStore`).

### UI Behavior

**Browse view (default):**
- Filter bar: search input, scope pills (all/session/project/user), status pills (canonical/draft/reviewed/deprecated)
- Default filter: all scopes, canonical status
- Memory list sorted by updated_at descending
- Each card: summary (bold), body preview (truncated, muted), metadata row (scope badge, origin badge, confidence in amber, relative time)
- Total count badge in header
- "+ New" button opens create form
- Click a card opens detail/edit view

**Detail/edit view:**
- Back arrow returns to browse
- Editable fields: summary (text input), body (textarea), origin (select dropdown), confidence (number input 0-1), tags (chip input with add/remove)
- Scope: shown but disabled on edit (immutable). Editable on create.
- Status: clickable pill selector (draft/reviewed/canonical/deprecated). Clicking a different status immediately calls the status endpoint.
- Delete button with confirmation dialog
- Save button commits summary/body/origin/confidence/tags changes
- Read-only metadata footer: session ID, revision, trigger

**Create view:**
- Same form as edit but all fields empty/default
- Scope is editable (defaults to session)
- Origin defaults to "user" (human-created)
- Confidence defaults to 0.8
- Status defaults to "draft"
- Save creates and returns to browse

### Data Flow

```
/memory slash command → useLayoutStore.setMemoryModalOpen(true) → MemoryModal renders
useQuery(['memories', filters]) → MemoryBrowse → MemoryCard[]
Click card → setSelectedKey(key) → view='detail'
Edit → local form state → Save → useMutation(api.updateMemory) → invalidate ['memories']
Status change → useMutation(api.updateMemoryStatus) → invalidate ['memories']
Delete → confirm dialog → useMutation(api.deleteMemory) → invalidate ['memories'] → view='browse'
Create → useMutation(api.createMemory) → invalidate ['memories'] → view='browse'
```

---

## Build Order

1. **Worker Status Settings Panel** — low effort, no backend, extends existing Observability dashboard
2. **Workflow Progress Panel** — medium effort, needs backend RunStore + API + SSE, then frontend tab + components
3. **Memory Viewer** — medium-high effort, needs backend REST wrapper, then modal + CRUD forms + slash command integration

---

## Error Handling

All features follow the existing pattern:
- API errors throw `Error` with message from response body
- React Query `onError` surfaces errors (toast or inline)
- Mutations show loading state on buttons (disabled + spinner)
- Empty states use the `Empty` component pattern from the design system

## Testing Notes

- Worker Status Panel: verify table renders all statuses, cancel mutation works
- Workflow Panel: verify SSE connection/reconnection, step status transitions render correctly, cancel works
- Memory Viewer: verify CRUD operations, filter combinations, status transitions, slash command opens modal, form validation (summary required, confidence 0-1)
