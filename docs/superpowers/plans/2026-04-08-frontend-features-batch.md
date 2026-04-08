# Frontend Features Batch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Worker Status settings panel, Workflow Progress RightRail tab, and Memory Viewer modal to the Nanite chat harness UI.

**Architecture:** Three independent features built in order of complexity. Worker Status extends existing Observability dashboard with no backend changes. Workflow Progress adds a new in-memory RunStore, REST+SSE API, and RightRail tab. Memory Viewer wraps the existing MemoryService with REST endpoints and a full CRUD modal launched via `/memory` slash command.

**Tech Stack:** Go (backend API), React 19 + TypeScript, Zustand, TanStack React Query, Tailwind 4, lucide-react

---

## File Map

### Feature 1: Worker Status Settings Panel
| Action | File | Responsibility |
|--------|------|---------------|
| Create | `ui/src/components/settings/observability/WorkerStatusPanel.tsx` | Full-detail worker table with cancel actions |
| Modify | `ui/src/components/settings/observability/ObservabilityDashboard.tsx` | Add WorkerStatusPanel card |

### Feature 2: Workflow Progress Panel
| Action | File | Responsibility |
|--------|------|---------------|
| Create | `internal/workflow/store.go` | In-memory ring buffer for recent runs |
| Create | `internal/workflow/store_test.go` | RunStore unit tests |
| Create | `internal/api/workflows.go` | REST + SSE handlers for workflow data |
| Create | `internal/api/workflows_test.go` | API handler tests |
| Modify | `internal/api/routes.go` | Register workflow routes |
| Modify | `internal/workflow/executor.go` | Wire EventHandler to RunStore |
| Modify | `ui/src/lib/types.ts` | Add workflow/pipeline/run types |
| Modify | `ui/src/lib/api.ts` | Add workflow API client methods |
| Create | `ui/src/hooks/useWorkflows.ts` | React Query hooks + SSE event hook |
| Create | `ui/src/components/workflows/WorkflowTab.tsx` | Main tab: list/detail view switching, filter |
| Create | `ui/src/components/workflows/WorkflowRunCard.tsx` | Run summary card with progress bar + step pills |
| Create | `ui/src/components/workflows/WorkflowRunDetail.tsx` | Detail view with step timeline + cancel |
| Create | `ui/src/components/workflows/WorkflowStepItem.tsx` | Single step in vertical timeline |
| Modify | `ui/src/components/RightRail.tsx` | Add workflows tab to CORE_TABS + render branch |
| Modify | `ui/src/stores/useLayoutStore.ts` | Add 'workflows' to RightRailTab type comment |

### Feature 3: Memory Viewer
| Action | File | Responsibility |
|--------|------|---------------|
| Create | `internal/api/memories.go` | REST handlers wrapping memory.Service |
| Create | `internal/api/memories_test.go` | API handler tests |
| Modify | `internal/api/routes.go` | Register memory routes |
| Modify | `ui/src/lib/types.ts` | Add Memory types |
| Modify | `ui/src/lib/api.ts` | Add memory API client methods |
| Create | `ui/src/hooks/useMemories.ts` | React Query hooks for memory CRUD |
| Create | `ui/src/components/memory/MemoryModal.tsx` | Root modal: view state management |
| Create | `ui/src/components/memory/MemoryBrowse.tsx` | Browse view: filters, search, list |
| Create | `ui/src/components/memory/MemoryCard.tsx` | Single memory row in browse list |
| Create | `ui/src/components/memory/MemoryDetail.tsx` | Detail/edit/create form |
| Modify | `ui/src/stores/useLayoutStore.ts` | Add memoryModalOpen state |
| Modify | `ui/src/components/AppShell.tsx` | Render MemoryModal |
| Modify | `ui/src/components/chat/ChatComposer.tsx` | Add /memory case to handleCommand |

---

## Task 1: Worker Status Settings Panel

**Files:**
- Create: `ui/src/components/settings/observability/WorkerStatusPanel.tsx`
- Modify: `ui/src/components/settings/observability/ObservabilityDashboard.tsx`

- [ ] **Step 1: Create WorkerStatusPanel component**

Create `ui/src/components/settings/observability/WorkerStatusPanel.tsx`:

```tsx
import { Cpu, X } from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Tooltip } from '@/components/ui/tooltip'
import { api } from '@/lib/api'
import type { Worker } from '@/lib/types'

const STATUS_STYLES: Record<string, { bg: string; text: string; label: string }> = {
  spawning: { bg: 'bg-warning/20', text: 'text-warning', label: 'spawning' },
  running: { bg: 'bg-info/20', text: 'text-info', label: 'running' },
  completed: { bg: 'bg-success/20', text: 'text-success', label: 'completed' },
  failed: { bg: 'bg-danger/20', text: 'text-danger', label: 'failed' },
  cancelled: { bg: 'bg-fg-muted/20', text: 'text-fg-muted', label: 'cancelled' },
}

function formatRelativeTime(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  return `${Math.floor(h / 24)}d ago`
}

function WorkerRow({ worker, onCancel, cancelling }: {
  worker: Worker
  onCancel: (id: string) => void
  cancelling: boolean
}) {
  const style = STATUS_STYLES[worker.status] ?? STATUS_STYLES.cancelled
  const canCancel = worker.status === 'spawning' || worker.status === 'running'

  return (
    <tr className="border-b border-border/30 hover:bg-surface/20 transition-colors">
      <td className="py-1.5 pr-3 text-fg-secondary text-xs">{worker.agent_id}</td>
      <td className="py-1.5 pr-3">
        <span className="text-[10px] px-1.5 py-0.5 rounded bg-surface/40 text-fg-muted font-medium">
          {worker.type}
        </span>
      </td>
      <td className="py-1.5 pr-3">
        <span className={`text-[10px] px-1.5 py-0.5 rounded font-medium ${style.bg} ${style.text}`}>
          {style.label}
        </span>
      </td>
      <td className="py-1.5 pr-3 font-mono text-fg-secondary text-xs">
        {worker.parent_session_id.slice(0, 8)}
      </td>
      <td className="py-1.5 pr-3 text-right font-mono tabular-nums text-fg-secondary text-xs">
        {formatRelativeTime(worker.created_at)}
      </td>
      <td className="py-1.5 pr-3 font-mono text-fg-muted text-[10px] max-w-[120px] truncate">
        {worker.worktree_path || '—'}
      </td>
      <td className="py-1.5 text-center">
        {canCancel && (
          <Tooltip content="Cancel worker" side="left">
            <button
              onClick={() => onCancel(worker.id)}
              disabled={cancelling}
              className="p-1 rounded text-fg-faint hover:text-danger hover:bg-surface transition-colors disabled:opacity-50"
            >
              <X className="w-3 h-3" />
            </button>
          </Tooltip>
        )}
      </td>
    </tr>
  )
}

export function WorkerStatusPanel() {
  const queryClient = useQueryClient()

  const { data: workers = [], isLoading } = useQuery({
    queryKey: ['workers'],
    queryFn: api.listWorkers,
    refetchInterval: 5000,
  })

  const cancelMutation = useMutation({
    mutationFn: (id: string) => api.cancelWorker(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workers'] })
    },
  })

  const activeCount = workers.filter(
    (w: Worker) => w.status === 'spawning' || w.status === 'running',
  ).length

  if (isLoading) {
    return <p className="text-xs text-fg-faint italic py-2">Loading worker status...</p>
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 text-xs text-fg-secondary">
          <Cpu className="w-3.5 h-3.5" />
          <span>
            {workers.length} worker{workers.length !== 1 && 's'}
            {activeCount > 0 && (
              <span className="text-info ml-1">({activeCount} active)</span>
            )}
          </span>
        </div>
      </div>

      {workers.length === 0 ? (
        <p className="text-xs text-fg-faint italic py-2">No workers</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="text-[10px] uppercase tracking-wider text-fg-muted border-b border-border">
                <th className="text-left py-2 pr-3 font-medium">Agent</th>
                <th className="text-left py-2 pr-3 font-medium">Type</th>
                <th className="text-left py-2 pr-3 font-medium">Status</th>
                <th className="text-left py-2 pr-3 font-medium">Parent</th>
                <th className="text-right py-2 pr-3 font-medium">Created</th>
                <th className="text-left py-2 pr-3 font-medium">Worktree</th>
                <th className="text-center py-2 font-medium w-8"></th>
              </tr>
            </thead>
            <tbody>
              {workers.map((worker: Worker) => (
                <WorkerRow
                  key={worker.id}
                  worker={worker}
                  onCancel={(id) => cancelMutation.mutate(id)}
                  cancelling={cancelMutation.isPending}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Add WorkerStatusPanel to ObservabilityDashboard**

In `ui/src/components/settings/observability/ObservabilityDashboard.tsx`, add the import and card:

Add import at top:
```typescript
import { WorkerStatusPanel } from './WorkerStatusPanel'
```

Add after the Process Health card (after line ~49 `</Card>`):
```tsx
      <Card title="Worker Status">
        <WorkerStatusPanel />
      </Card>
```

- [ ] **Step 3: Build and verify**

Run:
```bash
cd ui && npm run build
```

Expected: Build succeeds with no TypeScript errors.

- [ ] **Step 4: Commit**

```bash
git add ui/src/components/settings/observability/WorkerStatusPanel.tsx ui/src/components/settings/observability/ObservabilityDashboard.tsx
git commit -m "feat(ui): add Worker Status panel to Observability dashboard"
```

---

## Task 2: Workflow RunStore (Backend)

**Files:**
- Create: `internal/workflow/store.go`
- Create: `internal/workflow/store_test.go`

- [ ] **Step 1: Write RunStore tests**

Create `internal/workflow/store_test.go`:

```go
package workflow

import (
	"testing"
	"time"
)

func TestRunStore_AddAndGet(t *testing.T) {
	s := NewRunStore(10)

	run := &RunState{
		PipelineID: "p1",
		RunID:      "r1",
		Status:     RunRunning,
		StepStates: map[string]*StepState{},
		StartedAt:  time.Now(),
	}
	info := PipelineInfo{ID: "p1", Name: "test-pipeline", StepCount: 3}

	s.Add(info, run)

	got, ok := s.Get("r1")
	if !ok {
		t.Fatal("expected to find run r1")
	}
	if got.Run.PipelineID != "p1" {
		t.Errorf("got pipeline_id %q, want %q", got.Run.PipelineID, "p1")
	}
	if got.Pipeline.Name != "test-pipeline" {
		t.Errorf("got name %q, want %q", got.Pipeline.Name, "test-pipeline")
	}
}

func TestRunStore_List(t *testing.T) {
	s := NewRunStore(10)

	for i := range 3 {
		run := &RunState{
			PipelineID: "p1",
			RunID:      "r" + string(rune('0'+i)),
			Status:     RunCompleted,
			StepStates: map[string]*StepState{},
			StartedAt:  time.Now(),
		}
		s.Add(PipelineInfo{ID: "p1"}, run)
	}

	all := s.List("", "")
	if len(all) != 3 {
		t.Fatalf("got %d runs, want 3", len(all))
	}

	// Filter by status
	s.runs[0].Run.Status = RunRunning
	running := s.List("", "running")
	if len(running) != 1 {
		t.Fatalf("got %d running runs, want 1", len(running))
	}
}

func TestRunStore_RingBuffer(t *testing.T) {
	s := NewRunStore(3) // cap of 3

	for i := range 5 {
		run := &RunState{
			RunID:      "r" + string(rune('0'+i)),
			StepStates: map[string]*StepState{},
			StartedAt:  time.Now(),
		}
		s.Add(PipelineInfo{}, run)
	}

	all := s.List("", "")
	if len(all) != 3 {
		t.Fatalf("got %d runs, want 3 (ring buffer cap)", len(all))
	}

	// Oldest should be evicted
	_, ok := s.Get("r0")
	if ok {
		t.Error("expected r0 to be evicted")
	}
}

func TestRunStore_AppendEvent(t *testing.T) {
	s := NewRunStore(10)

	run := &RunState{
		RunID:      "r1",
		StepStates: map[string]*StepState{},
		StartedAt:  time.Now(),
	}
	s.Add(PipelineInfo{}, run)

	s.AppendEvent("r1", Event{
		Type:      "step.started",
		RunID:     "r1",
		StepID:    "s1",
		Timestamp: time.Now(),
	})

	got, _ := s.Get("r1")
	if len(got.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(got.Events))
	}
	if got.Events[0].StepID != "s1" {
		t.Errorf("got step_id %q, want %q", got.Events[0].StepID, "s1")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
go test ./internal/workflow/ -run TestRunStore -v
```

Expected: Compilation errors — `NewRunStore`, `PipelineInfo`, `RunRecord` not defined.

- [ ] **Step 3: Implement RunStore**

Create `internal/workflow/store.go`:

```go
package workflow

import (
	"sync"
)

// PipelineInfo holds metadata about a pipeline for display purposes.
type PipelineInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	StepCount   int    `json:"step_count"`
}

// RunRecord holds a pipeline run and its associated metadata.
type RunRecord struct {
	Pipeline PipelineInfo `json:"pipeline"`
	Run      *RunState    `json:"run"`
	Events   []Event      `json:"events"`
}

// RunStore is a thread-safe, fixed-capacity ring buffer of recent workflow runs.
type RunStore struct {
	mu   sync.RWMutex
	runs []*RunRecord
	cap  int
	idx  map[string]int // runID -> index in runs slice
}

// NewRunStore creates a RunStore with the given capacity.
func NewRunStore(cap int) *RunStore {
	if cap <= 0 {
		cap = 50
	}
	return &RunStore{
		runs: make([]*RunRecord, 0, cap),
		cap:  cap,
		idx:  make(map[string]int, cap),
	}
}

// Add inserts a new run record. If at capacity, the oldest is evicted.
func (s *RunStore) Add(info PipelineInfo, run *RunState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec := &RunRecord{
		Pipeline: info,
		Run:      run,
		Events:   nil,
	}

	if len(s.runs) >= s.cap {
		// Evict oldest (index 0).
		evicted := s.runs[0]
		delete(s.idx, evicted.Run.RunID)
		s.runs = s.runs[1:]
		// Reindex.
		for i, r := range s.runs {
			s.idx[r.Run.RunID] = i
		}
	}

	s.runs = append(s.runs, rec)
	s.idx[run.RunID] = len(s.runs) - 1
}

// Get returns a run record by run ID.
func (s *RunStore) Get(runID string) (*RunRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	i, ok := s.idx[runID]
	if !ok {
		return nil, false
	}
	return s.runs[i], true
}

// List returns runs, optionally filtered by pipeline ID and/or status.
// Returns newest first.
func (s *RunStore) List(pipelineID, status string) []*RunRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*RunRecord
	for i := len(s.runs) - 1; i >= 0; i-- {
		r := s.runs[i]
		if pipelineID != "" && r.Run.PipelineID != pipelineID {
			continue
		}
		if status != "" && string(r.Run.Status) != status {
			continue
		}
		result = append(result, r)
	}
	return result
}

// AppendEvent adds an event to a run's event log.
func (s *RunStore) AppendEvent(runID string, event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	i, ok := s.idx[runID]
	if !ok {
		return
	}
	s.runs[i].Events = append(s.runs[i].Events, event)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go test ./internal/workflow/ -run TestRunStore -v
```

Expected: All 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/workflow/store.go internal/workflow/store_test.go
git commit -m "feat(workflow): add in-memory RunStore ring buffer for recent runs"
```

---

## Task 3: Workflow REST + SSE API (Backend)

**Files:**
- Create: `internal/api/workflows.go`
- Modify: `internal/api/routes.go`
- Modify: `internal/workflow/executor.go`

Before writing this task, read `internal/api/routes.go` to find the router registration pattern and how the server struct provides dependencies. Also read how the existing SSE endpoint (`/api/stream/{id}`) is implemented for the SSE pattern.

- [ ] **Step 1: Read route registration pattern**

Read these files to understand:
- `internal/api/routes.go` — how routes are registered, what `Server` struct fields are available
- `internal/api/handlers.go` or the file containing the `/api/stream/{id}` handler — SSE implementation pattern

Document the router type (chi, stdlib, etc.), how handlers access dependencies (store, executor), and the SSE flush pattern.

- [ ] **Step 2: Wire RunStore into the application**

Modify `internal/workflow/executor.go` — add a method to create an EventHandler that writes to a RunStore:

```go
// RunStoreHandler returns an EventHandler that records events to the given RunStore.
func RunStoreHandler(store *RunStore) EventHandler {
	return func(event Event) {
		switch event.Type {
		case "pipeline.started":
			stepCount, _ := event.Data["step_count"].(int)
			store.Add(PipelineInfo{
				ID:        event.PipelineID,
				Name:      event.PipelineID, // Name populated by caller if available
				StepCount: stepCount,
			}, &RunState{
				PipelineID: event.PipelineID,
				RunID:      event.RunID,
				Status:     RunRunning,
				StepStates: make(map[string]*StepState),
				StartedAt:  event.Timestamp,
			})
		default:
			store.AppendEvent(event.RunID, event)
		}
	}
}
```

Note: The actual wiring into the application's Server struct depends on what you find in Step 1. The RunStore should be created at server startup and passed to both the executor's EventHandler and the API handlers.

- [ ] **Step 3: Create workflow API handlers**

Create `internal/api/workflows.go`. The exact handler signature and routing depends on Step 1 findings, but the logic is:

**`GET /api/workflows/runs`** — list runs:
```go
func (s *Server) handleListWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	pipelineID := r.URL.Query().Get("pipeline_id")
	runs := s.runStore.List(pipelineID, status)
	writeJSON(w, runs)
}
```

**`GET /api/workflows/runs/{runId}`** — get single run:
```go
func (s *Server) handleGetWorkflowRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runId") // or however the router extracts params
	rec, ok := s.runStore.Get(runID)
	if !ok {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	writeJSON(w, rec)
}
```

**`POST /api/workflows/runs/{runId}/cancel`** — cancel run:
```go
func (s *Server) handleCancelWorkflowRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runId")
	rec, ok := s.runStore.Get(runID)
	if !ok {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	// Cancel is done by cancelling the context — implementation depends on
	// how the executor tracks cancel functions. If not tracked, update status directly.
	rec.Run.Status = workflow.RunCancelled
	writeJSON(w, map[string]string{"status": "cancelled"})
}
```

**`GET /api/workflows/events`** — SSE stream:
```go
func (s *Server) handleWorkflowEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Subscribe to events from the RunStore or a fan-out channel.
	// Pattern: create a channel, register it with a broadcaster,
	// deregister on client disconnect.
	ch := s.workflowBroadcaster.Subscribe()
	defer s.workflowBroadcaster.Unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-ch:
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
```

The broadcaster is a simple fan-out: the executor's EventHandler sends events to it, and each SSE connection subscribes. This follows the same pattern as the chat stream SSE.

- [ ] **Step 4: Register routes**

Add to `internal/api/routes.go` in the route setup function:

```go
r.Get("/api/workflows/runs", s.handleListWorkflowRuns)
r.Get("/api/workflows/runs/{runId}", s.handleGetWorkflowRun)
r.Post("/api/workflows/runs/{runId}/cancel", s.handleCancelWorkflowRun)
r.Get("/api/workflows/events", s.handleWorkflowEvents)
```

- [ ] **Step 5: Build and verify**

Run:
```bash
go build ./cmd/nanite/
```

Expected: Compiles cleanly.

- [ ] **Step 6: Commit**

```bash
git add internal/api/workflows.go internal/api/routes.go internal/workflow/executor.go
git commit -m "feat(api): add workflow REST + SSE endpoints"
```

---

## Task 4: Workflow Frontend Types + API Client

**Files:**
- Modify: `ui/src/lib/types.ts`
- Modify: `ui/src/lib/api.ts`

- [ ] **Step 1: Add workflow types**

Add to the end of `ui/src/lib/types.ts`:

```typescript
// --- Workflow / Pipeline ---

export interface PipelineInfo {
  id: string;
  name: string;
  description: string;
  step_count: number;
}

export type RunStatus = "pending" | "running" | "completed" | "failed" | "cancelled";
export type StepStatus = "pending" | "running" | "completed" | "failed" | "skipped" | "cancelled";

export interface StepState {
  step_id: string;
  status: StepStatus;
  attempts: number;
  started_at: string;
  completed_at: string;
  error: string;
  skip_reason: string;
}

export interface RunState {
  pipeline_id: string;
  run_id: string;
  status: RunStatus;
  step_states: Record<string, StepState>;
  started_at: string;
  completed_at: string;
  error: string;
}

export interface WorkflowEvent {
  type: string;
  pipeline_id: string;
  run_id: string;
  step_id: string;
  data: Record<string, unknown>;
  timestamp: string;
}

export interface WorkflowRun {
  pipeline: PipelineInfo;
  run: RunState;
  events: WorkflowEvent[];
}
```

- [ ] **Step 2: Add workflow API methods**

Add to the `api` object in `ui/src/lib/api.ts`:

```typescript
  // Workflow Runs
  listWorkflowRuns: async (params?: { status?: string; pipeline_id?: string }): Promise<WorkflowRun[]> => {
    const qs = new URLSearchParams();
    if (params?.status) qs.set("status", params.status);
    if (params?.pipeline_id) qs.set("pipeline_id", params.pipeline_id);
    const query = qs.toString();
    const res = await fetch(`${API_BASE}/workflows/runs${query ? `?${query}` : ""}`);
    if (!res.ok) throw new Error(`Failed to list workflow runs: ${res.status}`);
    return res.json();
  },

  getWorkflowRun: async (runId: string): Promise<WorkflowRun> => {
    const res = await fetch(`${API_BASE}/workflows/runs/${encodeURIComponent(runId)}`);
    if (!res.ok) throw new Error(`Failed to get workflow run: ${res.status}`);
    return res.json();
  },

  cancelWorkflowRun: async (runId: string): Promise<{ status: string }> => {
    const res = await fetch(`${API_BASE}/workflows/runs/${encodeURIComponent(runId)}/cancel`, {
      method: "POST",
    });
    if (!res.ok) throw new Error(`Failed to cancel workflow run: ${res.status}`);
    return res.json();
  },
```

Add the type import at the top of `api.ts` if not auto-resolved:
```typescript
import type { WorkflowRun } from "./types";
```

- [ ] **Step 3: Build and verify**

Run:
```bash
cd ui && npm run build
```

Expected: Build succeeds.

- [ ] **Step 4: Commit**

```bash
git add ui/src/lib/types.ts ui/src/lib/api.ts
git commit -m "feat(ui): add workflow types and API client methods"
```

---

## Task 5: Workflow React Query Hooks + SSE

**Files:**
- Create: `ui/src/hooks/useWorkflows.ts`

- [ ] **Step 1: Create workflow hooks**

Create `ui/src/hooks/useWorkflows.ts`:

```typescript
import { useEffect, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'

export function useWorkflowRuns(filter?: { status?: string; pipeline_id?: string }) {
  return useQuery({
    queryKey: ['workflow-runs', filter],
    queryFn: () => api.listWorkflowRuns(filter),
    refetchInterval: 10_000,
  })
}

export function useWorkflowRun(runId: string | null) {
  return useQuery({
    queryKey: ['workflow-run', runId],
    queryFn: () => api.getWorkflowRun(runId!),
    enabled: !!runId,
    refetchInterval: 5_000,
  })
}

export function useCancelWorkflowRun() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (runId: string) => api.cancelWorkflowRun(runId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workflow-runs'] })
      queryClient.invalidateQueries({ queryKey: ['workflow-run'] })
    },
  })
}

export function useWorkflowEvents() {
  const queryClient = useQueryClient()
  const esRef = useRef<EventSource | null>(null)

  useEffect(() => {
    const es = new EventSource('/api/workflows/events')
    esRef.current = es

    es.onmessage = () => {
      queryClient.invalidateQueries({ queryKey: ['workflow-runs'] })
      queryClient.invalidateQueries({ queryKey: ['workflow-run'] })
    }

    es.onerror = () => {
      // EventSource auto-reconnects. No action needed.
    }

    return () => {
      es.close()
      esRef.current = null
    }
  }, [queryClient])
}
```

- [ ] **Step 2: Build and verify**

Run:
```bash
cd ui && npm run build
```

Expected: Build succeeds (hooks are tree-shaken if unused, but should compile).

- [ ] **Step 3: Commit**

```bash
git add ui/src/hooks/useWorkflows.ts
git commit -m "feat(ui): add workflow React Query hooks and SSE event hook"
```

---

## Task 6: Workflow UI Components

**Files:**
- Create: `ui/src/components/workflows/WorkflowStepItem.tsx`
- Create: `ui/src/components/workflows/WorkflowRunCard.tsx`
- Create: `ui/src/components/workflows/WorkflowRunDetail.tsx`
- Create: `ui/src/components/workflows/WorkflowTab.tsx`

- [ ] **Step 1: Create WorkflowStepItem**

Create `ui/src/components/workflows/WorkflowStepItem.tsx`:

```tsx
import { CheckCircle2, Circle, Loader2, XCircle, SlashIcon } from 'lucide-react'
import type { StepState } from '@/lib/types'

function formatDuration(startedAt: string, completedAt: string): string {
  if (!startedAt) return ''
  const start = new Date(startedAt).getTime()
  const end = completedAt ? new Date(completedAt).getTime() : Date.now()
  const ms = end - start
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

const STATUS_CONFIG: Record<string, {
  icon: typeof Circle
  color: string
  lineColor: string
}> = {
  completed: { icon: CheckCircle2, color: 'text-success', lineColor: 'bg-success/40' },
  running: { icon: Loader2, color: 'text-info', lineColor: 'bg-info/40' },
  pending: { icon: Circle, color: 'text-fg-muted/40', lineColor: 'bg-border/40' },
  failed: { icon: XCircle, color: 'text-danger', lineColor: 'bg-danger/40' },
  skipped: { icon: SlashIcon, color: 'text-fg-muted', lineColor: 'bg-border/40' },
  cancelled: { icon: XCircle, color: 'text-fg-muted', lineColor: 'bg-border/40' },
}

interface WorkflowStepItemProps {
  step: StepState
  isLast: boolean
}

export function WorkflowStepItem({ step, isLast }: WorkflowStepItemProps) {
  const config = STATUS_CONFIG[step.status] ?? STATUS_CONFIG.pending
  const Icon = config.icon

  return (
    <div className="flex gap-3">
      <div className="flex flex-col items-center">
        <Icon className={`w-4 h-4 shrink-0 ${config.color} ${step.status === 'running' ? 'animate-spin' : ''}`} />
        {!isLast && <div className={`w-px flex-1 min-h-[16px] ${config.lineColor}`} />}
      </div>
      <div className="flex-1 pb-3 min-w-0">
        <div className="flex items-center gap-2">
          <span className="text-xs font-medium text-fg">{step.step_id}</span>
          {step.status === 'running' && (
            <span className="text-[10px] text-info">{formatDuration(step.started_at, '')}</span>
          )}
          {step.status === 'completed' && step.started_at && (
            <span className="text-[10px] text-fg-muted">{formatDuration(step.started_at, step.completed_at)}</span>
          )}
        </div>
        {step.error && (
          <p className="text-[10px] text-danger mt-0.5 truncate">{step.error}</p>
        )}
        {step.skip_reason && (
          <p className="text-[10px] text-fg-muted mt-0.5 truncate">{step.skip_reason}</p>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Create WorkflowRunCard**

Create `ui/src/components/workflows/WorkflowRunCard.tsx`:

```tsx
import type { WorkflowRun } from '@/lib/types'

function formatRelativeTime(dateStr: string): string {
  if (!dateStr) return ''
  const diff = Date.now() - new Date(dateStr).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  return `${Math.floor(h / 24)}d ago`
}

const STATUS_BADGE: Record<string, string> = {
  running: 'bg-success/20 text-success',
  completed: 'bg-success/20 text-success',
  failed: 'bg-danger/20 text-danger',
  cancelled: 'bg-fg-muted/20 text-fg-muted',
  pending: 'bg-warning/20 text-warning',
}

const STATUS_DOT: Record<string, string> = {
  running: 'bg-success animate-pulse',
  completed: 'bg-success',
  failed: 'bg-danger',
  cancelled: 'bg-fg-muted',
  pending: 'bg-warning animate-pulse',
}

interface WorkflowRunCardProps {
  run: WorkflowRun
  onClick: () => void
}

export function WorkflowRunCard({ run, onClick }: WorkflowRunCardProps) {
  const steps = Object.values(run.run.step_states)
  const doneCount = steps.filter((s) => s.status === 'completed').length
  const totalCount = steps.length || run.pipeline.step_count || 1
  const progress = (doneCount / totalCount) * 100
  const status = run.run.status
  const isTerminal = status === 'completed' || status === 'failed' || status === 'cancelled'

  const stepPills = steps.map((s) => {
    const colors: Record<string, string> = {
      completed: 'bg-success/20 text-success',
      running: 'bg-info/20 text-info border border-info/40',
      pending: 'bg-surface/40 text-fg-muted/50',
      failed: 'bg-danger/20 text-danger',
      skipped: 'bg-surface/40 text-fg-muted/40',
      cancelled: 'bg-surface/40 text-fg-muted/40',
    }
    const prefix: Record<string, string> = {
      completed: '✓',
      running: '▸',
      failed: '✗',
      skipped: '⊘',
    }
    return { id: s.step_id, className: colors[s.status] ?? colors.pending, prefix: prefix[s.status] ?? '' }
  })

  return (
    <button
      onClick={onClick}
      className={`w-full text-left px-4 py-3 border-b border-border/20 hover:bg-surface/20 transition-colors ${isTerminal ? 'opacity-70' : ''}`}
    >
      <div className="flex items-center gap-2 mb-1.5">
        <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${STATUS_DOT[status] ?? STATUS_DOT.pending}`} />
        <span className="text-xs font-medium text-fg truncate">{run.pipeline.name || run.run.pipeline_id}</span>
        <span className={`text-[10px] px-1.5 py-0.5 rounded ml-auto shrink-0 ${STATUS_BADGE[status] ?? STATUS_BADGE.pending}`}>
          {status}
        </span>
      </div>
      <div className="text-[11px] text-fg-muted mb-2">
        {status === 'running' ? 'Started' : status === 'completed' ? 'Finished' : status === 'failed' ? 'Failed' : ''}{' '}
        {formatRelativeTime(run.run.started_at)} · {doneCount}/{totalCount} steps
      </div>
      <div className="h-[3px] bg-surface/40 rounded-full overflow-hidden mb-2">
        <div
          className={`h-full rounded-full transition-all duration-300 ${status === 'failed' ? 'bg-danger' : 'bg-success'}`}
          style={{ width: `${progress}%` }}
        />
      </div>
      {stepPills.length > 0 && (
        <div className="flex gap-1 flex-wrap">
          {stepPills.map((pill) => (
            <span key={pill.id} className={`text-[10px] px-1.5 py-0.5 rounded ${pill.className}`}>
              {pill.prefix ? `${pill.prefix} ` : ''}{pill.id}
            </span>
          ))}
        </div>
      )}
    </button>
  )
}
```

- [ ] **Step 3: Create WorkflowRunDetail**

Create `ui/src/components/workflows/WorkflowRunDetail.tsx`:

```tsx
import { ArrowLeft } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useWorkflowRun, useCancelWorkflowRun } from '@/hooks/useWorkflows'
import { WorkflowStepItem } from './WorkflowStepItem'
import type { StepState } from '@/lib/types'

function formatRelativeTime(dateStr: string): string {
  if (!dateStr) return ''
  const diff = Date.now() - new Date(dateStr).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  return h < 24 ? `${h}h ago` : `${Math.floor(h / 24)}d ago`
}

interface WorkflowRunDetailProps {
  runId: string
  onBack: () => void
}

export function WorkflowRunDetail({ runId, onBack }: WorkflowRunDetailProps) {
  const { data: run, isLoading } = useWorkflowRun(runId)
  const cancelMutation = useCancelWorkflowRun()

  if (isLoading || !run) {
    return <p className="text-xs text-fg-faint italic p-4">Loading run...</p>
  }

  const status = run.run.status
  const canCancel = status === 'running' || status === 'pending'

  // Sort steps by started_at (pending last)
  const steps: StepState[] = Object.values(run.run.step_states).sort((a, b) => {
    if (!a.started_at && !b.started_at) return 0
    if (!a.started_at) return 1
    if (!b.started_at) return -1
    return new Date(a.started_at).getTime() - new Date(b.started_at).getTime()
  })

  const STATUS_BADGE: Record<string, string> = {
    running: 'bg-success/20 text-success',
    completed: 'bg-success/20 text-success',
    failed: 'bg-danger/20 text-danger',
    cancelled: 'bg-fg-muted/20 text-fg-muted',
    pending: 'bg-warning/20 text-warning',
  }

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="px-4 py-3 border-b border-border flex items-center gap-2">
        <button onClick={onBack} className="p-1 rounded hover:bg-surface/40 transition-colors text-fg-muted">
          <ArrowLeft className="w-4 h-4" />
        </button>
        <span className="text-sm font-semibold text-fg truncate flex-1">
          {run.pipeline.name || run.run.pipeline_id}
        </span>
        <span className={`text-[10px] px-1.5 py-0.5 rounded ${STATUS_BADGE[status] ?? ''}`}>
          {status}
        </span>
        {canCancel && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => cancelMutation.mutate(runId)}
            disabled={cancelMutation.isPending}
            className="text-[10px] text-danger hover:bg-danger/10"
          >
            Cancel
          </Button>
        )}
      </div>

      {/* Meta */}
      <div className="px-4 py-2 text-[11px] text-fg-muted border-b border-border/20">
        Run <span className="font-mono text-fg-secondary">{runId.slice(0, 8)}</span>
        {run.run.started_at && <> · Started {formatRelativeTime(run.run.started_at)}</>}
      </div>

      {/* Step timeline */}
      <div className="flex-1 overflow-y-auto px-4 py-3">
        {steps.length === 0 ? (
          <p className="text-xs text-fg-faint italic">No steps</p>
        ) : (
          steps.map((step, i) => (
            <WorkflowStepItem key={step.step_id} step={step} isLast={i === steps.length - 1} />
          ))
        )}
      </div>

      {/* Error footer */}
      {run.run.error && (
        <div className="px-4 py-2 border-t border-danger/20 bg-danger/5 text-[11px] text-danger">
          {run.run.error}
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Create WorkflowTab**

Create `ui/src/components/workflows/WorkflowTab.tsx`:

```tsx
import { useState } from 'react'
import { GitBranch } from 'lucide-react'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { useWorkflowRuns, useWorkflowEvents } from '@/hooks/useWorkflows'
import { WorkflowRunCard } from './WorkflowRunCard'
import { WorkflowRunDetail } from './WorkflowRunDetail'

type Filter = 'active' | 'all'

export function WorkflowTab() {
  const [filter, setFilter] = useState<Filter>('active')
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null)

  // Subscribe to SSE events for live updates
  useWorkflowEvents()

  const statusFilter = filter === 'active' ? 'running' : undefined
  const { data: runs = [], isLoading } = useWorkflowRuns(
    statusFilter ? { status: statusFilter } : undefined,
  )

  if (selectedRunId) {
    return (
      <WorkflowRunDetail
        runId={selectedRunId}
        onBack={() => setSelectedRunId(null)}
      />
    )
  }

  return (
    <div className="flex flex-col h-full">
      {/* Filter bar */}
      <div className="px-4 py-2 border-b border-border/50 flex items-center gap-1.5">
        <button
          onClick={() => setFilter('all')}
          className={`text-[10px] px-2 py-1 rounded transition-colors ${
            filter === 'all' ? 'bg-surface/60 text-fg' : 'text-fg-muted hover:text-fg'
          }`}
        >
          All
        </button>
        <button
          onClick={() => setFilter('active')}
          className={`text-[10px] px-2 py-1 rounded transition-colors ${
            filter === 'active' ? 'bg-indigo-500/20 text-indigo-300' : 'text-fg-muted hover:text-fg'
          }`}
        >
          Active
        </button>
      </div>

      {/* Run list */}
      <ScrollArea className="flex-1">
        {isLoading ? (
          <div className="p-4 space-y-3">
            <Skeleton className="h-20 w-full" />
            <Skeleton className="h-20 w-full" />
          </div>
        ) : runs.length === 0 ? (
          <Empty className="py-12">
            <EmptyHeader>
              <EmptyMedia>
                <GitBranch className="w-8 h-8 text-fg-muted/40" />
              </EmptyMedia>
              <EmptyTitle>No workflow runs</EmptyTitle>
              <EmptyDescription>
                {filter === 'active' ? 'No active workflows. Switch to "All" to see recent runs.' : 'No workflow runs recorded yet.'}
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          runs.map((run) => (
            <WorkflowRunCard
              key={run.run.run_id}
              run={run}
              onClick={() => setSelectedRunId(run.run.run_id)}
            />
          ))
        )}
      </ScrollArea>
    </div>
  )
}
```

- [ ] **Step 5: Build and verify**

Run:
```bash
cd ui && npm run build
```

Expected: Build succeeds (components aren't mounted yet but should compile).

- [ ] **Step 6: Commit**

```bash
git add ui/src/components/workflows/
git commit -m "feat(ui): add Workflow tab components — WorkflowTab, RunCard, RunDetail, StepItem"
```

---

## Task 7: Wire Workflow Tab into RightRail

**Files:**
- Modify: `ui/src/components/RightRail.tsx`

- [ ] **Step 1: Add workflows tab and render branch**

In `ui/src/components/RightRail.tsx`:

Add import at top:
```typescript
import { GitBranch } from 'lucide-react'
import { WorkflowTab } from './workflows/WorkflowTab'
```

Add to the `CORE_TABS` array (after the `work` entry):
```typescript
  { id: 'workflows' as const, icon: GitBranch, label: 'Workflows' },
```

Add the render branch in the content area. Find the section where `activeTab` is checked (look for `activeTab === 'work'` rendering `<WorkTab />`). Add after the work branch:

```tsx
{activeTab === 'workflows' && <WorkflowTab />}
```

- [ ] **Step 2: Build and verify**

Run:
```bash
cd ui && npm run build
```

Expected: Build succeeds.

- [ ] **Step 3: Commit**

```bash
git add ui/src/components/RightRail.tsx
git commit -m "feat(ui): register Workflows tab in RightRail"
```

---

## Task 8: Memory REST API (Backend)

**Files:**
- Create: `internal/api/memories.go`
- Modify: `internal/api/routes.go`

Before writing this task, read `internal/memory/service.go` to understand the `Save`, `Recall`, and status methods. Also check how the memory service is accessed from the API server (field name on Server struct).

- [ ] **Step 1: Read memory service interface**

Read these files:
- `internal/memory/service.go` — understand `Save()`, `Recall()`, `Get()`, `Delete()`, `UpdateStatus()` method signatures and types
- `internal/api/routes.go` — find how memory service is available (e.g., `s.memoryService` or `s.memory`)

Document the exact method signatures and how the API server accesses the service.

- [ ] **Step 2: Create memory API handlers**

Create `internal/api/memories.go`. Handlers wrap the memory service:

**`GET /api/memories`** — list with filters:
- Query params: `scope`, `status`, `q` (search query), `tags` (comma-separated), `limit`, `offset`
- Calls `memoryService.Recall()` with appropriate options
- Returns `{ "memories": [...], "total": N }`

**`POST /api/memories`** — create:
- Body: `{ summary, body, origin, confidence, scope, tags }`
- Calls `memoryService.Save()` with the provided fields
- Sets trigger to `"manual"` and status to `"draft"`
- Returns the created memory

**`PUT /api/memories/{key}`** — update:
- Body: `{ summary, body, origin, confidence, tags }`
- Fetches existing memory, updates fields, saves back
- Returns updated memory

**`DELETE /api/memories/{key}`** — delete:
- Calls the appropriate delete method on the service
- Returns `{ "deleted": true }`

**`PUT /api/memories/{key}/status`** — status transition:
- Body: `{ "status": "canonical" }`
- Validates status is one of: draft, reviewed, canonical, deprecated
- Calls the appropriate status method
- Returns updated memory

Exact implementation depends on the memory service API found in Step 1.

- [ ] **Step 3: Register routes**

Add to `internal/api/routes.go`:

```go
r.Get("/api/memories", s.handleListMemories)
r.Post("/api/memories", s.handleCreateMemory)
r.Put("/api/memories/{key}", s.handleUpdateMemory)
r.Delete("/api/memories/{key}", s.handleDeleteMemory)
r.Put("/api/memories/{key}/status", s.handleUpdateMemoryStatus)
```

- [ ] **Step 4: Build and verify**

Run:
```bash
go build ./cmd/nanite/
```

Expected: Compiles cleanly.

- [ ] **Step 5: Commit**

```bash
git add internal/api/memories.go internal/api/routes.go
git commit -m "feat(api): add memory REST endpoints wrapping MemoryService"
```

---

## Task 9: Memory Frontend Types + API Client + Hooks

**Files:**
- Modify: `ui/src/lib/types.ts`
- Modify: `ui/src/lib/api.ts`
- Create: `ui/src/hooks/useMemories.ts`

- [ ] **Step 1: Add memory types**

Add to the end of `ui/src/lib/types.ts`:

```typescript
// --- Memory ---

export type MemoryOrigin = "user" | "feedback" | "project" | "reference" | "observation";
export type MemoryStatus = "draft" | "reviewed" | "canonical" | "deprecated";
export type MemoryScope = "session" | "project" | "user";

export interface Memory {
  memory_key: string;
  namespace: string;
  summary: string;
  body: string;
  origin: MemoryOrigin;
  trigger: string;
  confidence: number;
  tags: string[];
  scope: MemoryScope;
  session_id: string;
  revision_id: string;
  status: MemoryStatus;
  created_at: string;
  updated_at: string;
}

export interface MemoryListResponse {
  memories: Memory[];
  total: number;
}

export interface MemoryCreateRequest {
  summary: string;
  body?: string;
  origin: MemoryOrigin;
  confidence: number;
  scope: MemoryScope;
  tags: string[];
}

export interface MemoryUpdateRequest {
  summary: string;
  body?: string;
  origin: MemoryOrigin;
  confidence: number;
  tags: string[];
}
```

- [ ] **Step 2: Add memory API methods**

Add to the `api` object in `ui/src/lib/api.ts`:

```typescript
  // Memories
  listMemories: async (params?: {
    scope?: string; status?: string; q?: string; tags?: string; limit?: number; offset?: number;
  }): Promise<import("./types").MemoryListResponse> => {
    const qs = new URLSearchParams();
    if (params?.scope) qs.set("scope", params.scope);
    if (params?.status) qs.set("status", params.status);
    if (params?.q) qs.set("q", params.q);
    if (params?.tags) qs.set("tags", params.tags);
    if (params?.limit) qs.set("limit", String(params.limit));
    if (params?.offset) qs.set("offset", String(params.offset));
    const query = qs.toString();
    const res = await fetch(`${API_BASE}/memories${query ? `?${query}` : ""}`);
    if (!res.ok) throw new Error(`Failed to list memories: ${res.status}`);
    return res.json();
  },

  createMemory: async (data: import("./types").MemoryCreateRequest): Promise<import("./types").Memory> => {
    const res = await fetch(`${API_BASE}/memories`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to create memory: ${res.status}`);
    }
    return res.json();
  },

  updateMemory: async (key: string, data: import("./types").MemoryUpdateRequest): Promise<import("./types").Memory> => {
    const res = await fetch(`${API_BASE}/memories/${encodeURIComponent(key)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to update memory: ${res.status}`);
    }
    return res.json();
  },

  deleteMemory: async (key: string): Promise<{ deleted: boolean }> => {
    const res = await fetch(`${API_BASE}/memories/${encodeURIComponent(key)}`, {
      method: "DELETE",
    });
    if (!res.ok) throw new Error(`Failed to delete memory: ${res.status}`);
    return res.json();
  },

  updateMemoryStatus: async (key: string, status: string): Promise<import("./types").Memory> => {
    const res = await fetch(`${API_BASE}/memories/${encodeURIComponent(key)}/status`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ status }),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }));
      throw new Error(err.error || `Failed to update memory status: ${res.status}`);
    }
    return res.json();
  },
```

- [ ] **Step 3: Create memory hooks**

Create `ui/src/hooks/useMemories.ts`:

```typescript
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { MemoryCreateRequest, MemoryUpdateRequest } from '@/lib/types'

interface MemoryFilters {
  scope?: string
  status?: string
  q?: string
  tags?: string
  limit?: number
  offset?: number
}

export function useMemories(filters?: MemoryFilters) {
  return useQuery({
    queryKey: ['memories', filters],
    queryFn: () => api.listMemories(filters),
  })
}

export function useCreateMemory() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: MemoryCreateRequest) => api.createMemory(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['memories'] })
    },
  })
}

export function useUpdateMemory() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ key, data }: { key: string; data: MemoryUpdateRequest }) =>
      api.updateMemory(key, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['memories'] })
    },
  })
}

export function useDeleteMemory() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (key: string) => api.deleteMemory(key),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['memories'] })
    },
  })
}

export function useUpdateMemoryStatus() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ key, status }: { key: string; status: string }) =>
      api.updateMemoryStatus(key, status),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['memories'] })
    },
  })
}
```

- [ ] **Step 4: Build and verify**

Run:
```bash
cd ui && npm run build
```

Expected: Build succeeds.

- [ ] **Step 5: Commit**

```bash
git add ui/src/lib/types.ts ui/src/lib/api.ts ui/src/hooks/useMemories.ts
git commit -m "feat(ui): add memory types, API client, and React Query hooks"
```

---

## Task 10: Memory Modal — Browse View

**Files:**
- Create: `ui/src/components/memory/MemoryCard.tsx`
- Create: `ui/src/components/memory/MemoryBrowse.tsx`

- [ ] **Step 1: Create MemoryCard**

Create `ui/src/components/memory/MemoryCard.tsx`:

```tsx
import type { Memory } from '@/lib/types'

function formatRelativeTime(dateStr: string): string {
  if (!dateStr) return ''
  const diff = Date.now() - new Date(dateStr).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  return h < 24 ? `${h}h ago` : `${Math.floor(h / 24)}d ago`
}

const STATUS_STYLE: Record<string, string> = {
  canonical: 'bg-success/20 text-success',
  reviewed: 'bg-purple-500/20 text-purple-400',
  draft: 'bg-info/20 text-info',
  deprecated: 'bg-fg-muted/20 text-fg-muted',
}

interface MemoryCardProps {
  memory: Memory
  onClick: () => void
}

export function MemoryCard({ memory, onClick }: MemoryCardProps) {
  return (
    <button
      onClick={onClick}
      className={`w-full text-left px-5 py-3 border-b border-border/20 hover:bg-surface/20 transition-colors ${
        memory.status === 'deprecated' ? 'opacity-50' : memory.status === 'draft' ? 'opacity-75' : ''
      }`}
    >
      <div className="flex items-center gap-2 mb-1">
        <span className="text-xs font-medium text-fg truncate flex-1">{memory.summary}</span>
        <span className={`text-[9px] px-1.5 py-0.5 rounded shrink-0 ${STATUS_STYLE[memory.status] ?? ''}`}>
          {memory.status}
        </span>
      </div>
      {memory.body && (
        <p className="text-[11px] text-fg-muted line-clamp-2 mb-1.5 leading-relaxed">{memory.body}</p>
      )}
      <div className="flex items-center gap-1.5">
        <span className="text-[10px] px-1.5 py-0.5 rounded bg-surface/40 text-fg-muted">{memory.scope}</span>
        <span className="text-[10px] px-1.5 py-0.5 rounded bg-surface/40 text-fg-muted">{memory.origin}</span>
        <span className="text-[10px] px-1.5 py-0.5 rounded bg-warning/20 text-warning">{memory.confidence.toFixed(2)}</span>
        <span className="text-[10px] text-fg-muted/50 ml-auto">{formatRelativeTime(memory.updated_at)}</span>
      </div>
    </button>
  )
}
```

- [ ] **Step 2: Create MemoryBrowse**

Create `ui/src/components/memory/MemoryBrowse.tsx`:

```tsx
import { useState } from 'react'
import { Search, Brain } from 'lucide-react'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { useMemories } from '@/hooks/useMemories'
import { MemoryCard } from './MemoryCard'
import type { MemoryScope, MemoryStatus } from '@/lib/types'

const SCOPE_OPTIONS: Array<{ value: string; label: string }> = [
  { value: '', label: 'all' },
  { value: 'session', label: 'session' },
  { value: 'project', label: 'project' },
  { value: 'user', label: 'user' },
]

const STATUS_OPTIONS: Array<{ value: string; label: string }> = [
  { value: '', label: 'all' },
  { value: 'canonical', label: 'canonical' },
  { value: 'draft', label: 'draft' },
  { value: 'reviewed', label: 'reviewed' },
  { value: 'deprecated', label: 'deprecated' },
]

interface MemoryBrowseProps {
  onSelect: (key: string) => void
  onCreate: () => void
}

export function MemoryBrowse({ onSelect, onCreate }: MemoryBrowseProps) {
  const [search, setSearch] = useState('')
  const [scope, setScope] = useState('')
  const [status, setStatus] = useState('')

  const { data, isLoading } = useMemories({
    q: search || undefined,
    scope: scope || undefined,
    status: status || undefined,
    limit: 50,
  })

  const memories = data?.memories ?? []
  const total = data?.total ?? 0

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="px-5 py-3.5 border-b border-border flex items-center justify-between">
        <div className="flex items-center gap-2.5">
          <span className="text-sm font-semibold text-fg">Memories</span>
          <span className="text-[10px] px-2 py-0.5 rounded-full bg-surface/60 text-fg-muted tabular-nums">
            {total}
          </span>
        </div>
        <button
          onClick={onCreate}
          className="text-[11px] px-2.5 py-1 rounded-md bg-indigo-500 text-white hover:bg-indigo-600 transition-colors"
        >
          + New
        </button>
      </div>

      {/* Filters */}
      <div className="px-5 py-2.5 border-b border-border/30 flex items-center gap-2 flex-wrap">
        <div className="flex-1 min-w-[140px] flex items-center gap-2 px-2.5 py-1.5 rounded-md bg-surface/40 border border-border/30">
          <Search className="w-3.5 h-3.5 text-fg-muted" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search memories..."
            className="flex-1 bg-transparent text-xs text-fg placeholder:text-fg-muted/50 outline-none"
          />
        </div>

        <div className="flex gap-1">
          {SCOPE_OPTIONS.map((opt) => (
            <button
              key={opt.value}
              onClick={() => setScope(opt.value)}
              className={`text-[10px] px-2 py-1 rounded transition-colors ${
                scope === opt.value
                  ? 'bg-indigo-500/20 text-indigo-300 border border-indigo-500/40'
                  : 'bg-surface/40 text-fg-muted hover:text-fg'
              }`}
            >
              {opt.label}
            </button>
          ))}
        </div>

        <div className="flex gap-1">
          {STATUS_OPTIONS.map((opt) => (
            <button
              key={opt.value}
              onClick={() => setStatus(opt.value)}
              className={`text-[10px] px-2 py-1 rounded transition-colors ${
                status === opt.value
                  ? 'bg-indigo-500/20 text-indigo-300 border border-indigo-500/40'
                  : 'bg-surface/40 text-fg-muted hover:text-fg'
              }`}
            >
              {opt.label}
            </button>
          ))}
        </div>
      </div>

      {/* List */}
      <ScrollArea className="flex-1">
        {isLoading ? (
          <div className="p-5 space-y-3">
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
          </div>
        ) : memories.length === 0 ? (
          <Empty className="py-12">
            <EmptyHeader>
              <EmptyMedia>
                <Brain className="w-8 h-8 text-fg-muted/40" />
              </EmptyMedia>
              <EmptyTitle>No memories found</EmptyTitle>
              <EmptyDescription>
                {search || scope || status
                  ? 'Try adjusting your filters.'
                  : 'Memories are created by agents during conversations.'}
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          memories.map((mem) => (
            <MemoryCard
              key={mem.memory_key}
              memory={mem}
              onClick={() => onSelect(mem.memory_key)}
            />
          ))
        )}
      </ScrollArea>
    </div>
  )
}
```

- [ ] **Step 3: Build and verify**

Run:
```bash
cd ui && npm run build
```

Expected: Build succeeds.

- [ ] **Step 4: Commit**

```bash
git add ui/src/components/memory/MemoryCard.tsx ui/src/components/memory/MemoryBrowse.tsx
git commit -m "feat(ui): add MemoryCard and MemoryBrowse components"
```

---

## Task 11: Memory Modal — Detail/Edit View

**Files:**
- Create: `ui/src/components/memory/MemoryDetail.tsx`

- [ ] **Step 1: Create MemoryDetail**

Create `ui/src/components/memory/MemoryDetail.tsx`:

```tsx
import { useState, useEffect } from 'react'
import { ArrowLeft } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter,
} from '@/components/ui/dialog'
import { useMemories, useUpdateMemory, useDeleteMemory, useCreateMemory, useUpdateMemoryStatus } from '@/hooks/useMemories'
import type { Memory, MemoryOrigin, MemoryScope, MemoryStatus } from '@/lib/types'

const ORIGIN_OPTIONS: MemoryOrigin[] = ['user', 'feedback', 'project', 'reference', 'observation']
const SCOPE_OPTIONS: MemoryScope[] = ['session', 'project', 'user']
const STATUS_OPTIONS: Array<{ value: MemoryStatus; label: string; style: string }> = [
  { value: 'draft', label: 'draft', style: 'bg-info/20 text-info' },
  { value: 'reviewed', label: 'reviewed', style: 'bg-purple-500/20 text-purple-400' },
  { value: 'canonical', label: 'canonical', style: 'bg-success/20 text-success' },
  { value: 'deprecated', label: 'depr.', style: 'bg-fg-muted/20 text-fg-muted' },
]

interface MemoryDetailProps {
  memoryKey: string | null  // null = create mode
  onBack: () => void
}

export function MemoryDetail({ memoryKey, onBack }: MemoryDetailProps) {
  const isCreate = memoryKey === null
  const { data } = useMemories(isCreate ? undefined : { limit: 200 })
  const memory = isCreate ? null : data?.memories.find((m) => m.memory_key === memoryKey)

  const updateMutation = useUpdateMemory()
  const createMutation = useCreateMemory()
  const deleteMutation = useDeleteMemory()
  const statusMutation = useUpdateMemoryStatus()

  const [summary, setSummary] = useState('')
  const [body, setBody] = useState('')
  const [origin, setOrigin] = useState<MemoryOrigin>('user')
  const [confidence, setConfidence] = useState(0.8)
  const [scope, setScope] = useState<MemoryScope>('session')
  const [tags, setTags] = useState<string[]>([])
  const [tagInput, setTagInput] = useState('')
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false)

  // Populate form when memory loads
  useEffect(() => {
    if (memory) {
      setSummary(memory.summary)
      setBody(memory.body || '')
      setOrigin(memory.origin)
      setConfidence(memory.confidence)
      setScope(memory.scope)
      setTags(memory.tags || [])
    }
  }, [memory])

  const handleSave = () => {
    if (!summary.trim()) return

    if (isCreate) {
      createMutation.mutate(
        { summary: summary.trim(), body: body.trim() || undefined, origin, confidence, scope, tags },
        { onSuccess: onBack },
      )
    } else {
      updateMutation.mutate(
        { key: memoryKey!, data: { summary: summary.trim(), body: body.trim() || undefined, origin, confidence, tags } },
        { onSuccess: onBack },
      )
    }
  }

  const handleDelete = () => {
    if (!memoryKey) return
    deleteMutation.mutate(memoryKey, { onSuccess: onBack })
  }

  const handleStatusChange = (newStatus: MemoryStatus) => {
    if (!memoryKey || memory?.status === newStatus) return
    statusMutation.mutate({ key: memoryKey, status: newStatus })
  }

  const addTag = () => {
    const tag = tagInput.trim().toLowerCase()
    if (tag && !tags.includes(tag)) {
      setTags([...tags, tag])
    }
    setTagInput('')
  }

  const removeTag = (tag: string) => {
    setTags(tags.filter((t) => t !== tag))
  }

  const saving = createMutation.isPending || updateMutation.isPending

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="px-5 py-3.5 border-b border-border flex items-center gap-2">
        <button onClick={onBack} className="p-1 rounded hover:bg-surface/40 transition-colors text-fg-muted">
          <ArrowLeft className="w-4 h-4" />
        </button>
        <span className="text-sm font-semibold text-fg flex-1">
          {isCreate ? 'New Memory' : 'Edit Memory'}
        </span>
        {!isCreate && (
          <button
            onClick={() => setShowDeleteConfirm(true)}
            className="text-[10px] px-2 py-1 rounded bg-danger/10 text-danger hover:bg-danger/20 transition-colors"
          >
            Delete
          </button>
        )}
        <button
          onClick={handleSave}
          disabled={saving || !summary.trim()}
          className="text-[10px] px-2.5 py-1 rounded bg-indigo-500 text-white hover:bg-indigo-600 transition-colors disabled:opacity-50"
        >
          {saving ? 'Saving...' : 'Save'}
        </button>
      </div>

      {/* Form */}
      <div className="flex-1 overflow-y-auto px-5 py-4 space-y-4">
        {/* Summary */}
        <div>
          <label className="text-[10px] uppercase tracking-wider text-fg-muted font-medium block mb-1">Summary</label>
          <input
            type="text"
            value={summary}
            onChange={(e) => setSummary(e.target.value)}
            placeholder="One-line summary..."
            className="w-full px-3 py-2 rounded-md border border-border/50 bg-surface/40 text-xs text-fg placeholder:text-fg-muted/50 outline-none focus:border-indigo-500/50"
          />
        </div>

        {/* Body */}
        <div>
          <label className="text-[10px] uppercase tracking-wider text-fg-muted font-medium block mb-1">Body</label>
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="Detailed description (optional)..."
            rows={3}
            className="w-full px-3 py-2 rounded-md border border-border/50 bg-surface/40 text-xs text-fg placeholder:text-fg-muted/50 outline-none focus:border-indigo-500/50 resize-y"
          />
        </div>

        {/* Origin + Confidence */}
        <div className="flex gap-3">
          <div className="flex-1">
            <label className="text-[10px] uppercase tracking-wider text-fg-muted font-medium block mb-1">Origin</label>
            <select
              value={origin}
              onChange={(e) => setOrigin(e.target.value as MemoryOrigin)}
              className="w-full px-3 py-2 rounded-md border border-border/50 bg-surface/40 text-xs text-fg outline-none"
            >
              {ORIGIN_OPTIONS.map((o) => (
                <option key={o} value={o}>{o}</option>
              ))}
            </select>
          </div>
          <div className="flex-1">
            <label className="text-[10px] uppercase tracking-wider text-fg-muted font-medium block mb-1">Confidence</label>
            <input
              type="number"
              value={confidence}
              onChange={(e) => setConfidence(Math.min(1, Math.max(0, parseFloat(e.target.value) || 0)))}
              step={0.05}
              min={0}
              max={1}
              className="w-full px-3 py-2 rounded-md border border-border/50 bg-surface/40 text-xs text-warning outline-none"
            />
          </div>
        </div>

        {/* Scope + Status */}
        <div className="flex gap-3">
          <div className="flex-1">
            <label className="text-[10px] uppercase tracking-wider text-fg-muted font-medium block mb-1">Scope</label>
            <select
              value={scope}
              onChange={(e) => setScope(e.target.value as MemoryScope)}
              disabled={!isCreate}
              className="w-full px-3 py-2 rounded-md border border-border/50 bg-surface/40 text-xs text-fg outline-none disabled:opacity-50"
            >
              {SCOPE_OPTIONS.map((s) => (
                <option key={s} value={s}>{s}</option>
              ))}
            </select>
          </div>
          {!isCreate && memory && (
            <div className="flex-1">
              <label className="text-[10px] uppercase tracking-wider text-fg-muted font-medium block mb-1">Status</label>
              <div className="flex gap-1">
                {STATUS_OPTIONS.map((opt) => (
                  <button
                    key={opt.value}
                    onClick={() => handleStatusChange(opt.value)}
                    disabled={statusMutation.isPending}
                    className={`text-[10px] px-2 py-1 rounded transition-colors ${
                      memory.status === opt.value
                        ? `${opt.style} border border-current/30`
                        : 'bg-surface/40 text-fg-muted/50 hover:text-fg-muted'
                    }`}
                  >
                    {opt.label}
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>

        {/* Tags */}
        <div>
          <label className="text-[10px] uppercase tracking-wider text-fg-muted font-medium block mb-1">Tags</label>
          <div className="flex gap-1 flex-wrap items-center">
            {tags.map((tag) => (
              <span key={tag} className="text-[10px] px-2 py-0.5 rounded bg-surface/40 text-fg-muted flex items-center gap-1">
                {tag}
                <button onClick={() => removeTag(tag)} className="text-fg-muted/50 hover:text-fg-muted">x</button>
              </span>
            ))}
            <input
              type="text"
              value={tagInput}
              onChange={(e) => setTagInput(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addTag() } }}
              onBlur={addTag}
              placeholder="+ add"
              className="text-[10px] px-2 py-0.5 bg-transparent border border-dashed border-border/50 rounded text-fg-muted/50 outline-none w-16"
            />
          </div>
        </div>

        {/* Read-only metadata */}
        {!isCreate && memory && (
          <div className="pt-3 border-t border-border/30">
            <div className="flex gap-4 text-[10px] text-fg-muted/50">
              <span>Session: <span className="font-mono text-fg-muted">{memory.session_id?.slice(0, 8) || '—'}</span></span>
              <span>Revision: <span className="font-mono text-fg-muted">{memory.revision_id || '—'}</span></span>
              <span>Trigger: <span className="text-fg-muted">{memory.trigger || '—'}</span></span>
            </div>
          </div>
        )}
      </div>

      {/* Delete confirmation */}
      <Dialog open={showDeleteConfirm} onOpenChange={setShowDeleteConfirm}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete memory?</DialogTitle>
            <DialogDescription>
              This will permanently delete &ldquo;{memory?.summary}&rdquo;. This cannot be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setShowDeleteConfirm(false)}>Cancel</Button>
            <Button
              variant="default"
              onClick={handleDelete}
              disabled={deleteMutation.isPending}
              className="bg-danger text-white hover:bg-danger/90"
            >
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
```

- [ ] **Step 2: Build and verify**

Run:
```bash
cd ui && npm run build
```

Expected: Build succeeds.

- [ ] **Step 3: Commit**

```bash
git add ui/src/components/memory/MemoryDetail.tsx
git commit -m "feat(ui): add MemoryDetail component with full CRUD form"
```

---

## Task 12: Memory Modal + Slash Command Integration

**Files:**
- Create: `ui/src/components/memory/MemoryModal.tsx`
- Modify: `ui/src/stores/useLayoutStore.ts`
- Modify: `ui/src/components/AppShell.tsx`
- Modify: `ui/src/components/chat/ChatComposer.tsx`

- [ ] **Step 1: Add memoryModalOpen to layout store**

In `ui/src/stores/useLayoutStore.ts`, add to the interface:
```typescript
  memoryModalOpen: boolean
  setMemoryModalOpen: (open: boolean) => void
```

Add to the store initial state:
```typescript
  memoryModalOpen: false,
  setMemoryModalOpen: (open) => set({ memoryModalOpen: open }),
```

Note: `memoryModalOpen` should NOT be persisted — modal should always start closed. If the store uses `persist` with a `partialize` option, exclude it. If it persists everything, add `memoryModalOpen` to the migration to reset it, or handle it in `onRehydrateStorage`.

- [ ] **Step 2: Create MemoryModal**

Create `ui/src/components/memory/MemoryModal.tsx`:

```tsx
import { useState, useCallback } from 'react'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { useLayoutStore } from '@/stores/useLayoutStore'
import { MemoryBrowse } from './MemoryBrowse'
import { MemoryDetail } from './MemoryDetail'

type View = 'browse' | 'detail' | 'create'

export function MemoryModal() {
  const open = useLayoutStore((s) => s.memoryModalOpen)
  const setOpen = useLayoutStore((s) => s.setMemoryModalOpen)
  const [view, setView] = useState<View>('browse')
  const [selectedKey, setSelectedKey] = useState<string | null>(null)

  const handleClose = useCallback((isOpen: boolean) => {
    if (!isOpen) {
      setOpen(false)
      // Reset view state after close animation
      setTimeout(() => {
        setView('browse')
        setSelectedKey(null)
      }, 200)
    }
  }, [setOpen])

  const handleSelect = useCallback((key: string) => {
    setSelectedKey(key)
    setView('detail')
  }, [])

  const handleCreate = useCallback(() => {
    setSelectedKey(null)
    setView('create')
  }, [])

  const handleBack = useCallback(() => {
    setView('browse')
    setSelectedKey(null)
  }, [])

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className="max-w-2xl h-[600px] p-0 flex flex-col overflow-hidden">
        {view === 'browse' && (
          <MemoryBrowse onSelect={handleSelect} onCreate={handleCreate} />
        )}
        {(view === 'detail' || view === 'create') && (
          <MemoryDetail
            memoryKey={view === 'create' ? null : selectedKey}
            onBack={handleBack}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 3: Render MemoryModal in AppShell**

In `ui/src/components/AppShell.tsx`, add import:
```typescript
import { MemoryModal } from './memory/MemoryModal'
```

Add `<MemoryModal />` alongside other modals (find where `SprintPlanningModal` or similar modals are rendered):
```tsx
<MemoryModal />
```

- [ ] **Step 4: Add /memory slash command handler**

In `ui/src/components/chat/ChatComposer.tsx`, find the `handleCommand` callback's switch statement. Add a new case before the `default`:

```typescript
    case 'memory': {
      useLayoutStore.getState().setMemoryModalOpen(true)
      return
    }
```

- [ ] **Step 5: Build and verify**

Run:
```bash
cd ui && npm run build
```

Expected: Build succeeds.

- [ ] **Step 6: Commit**

```bash
git add ui/src/components/memory/MemoryModal.tsx ui/src/stores/useLayoutStore.ts ui/src/components/AppShell.tsx ui/src/components/chat/ChatComposer.tsx
git commit -m "feat(ui): add MemoryModal with /memory slash command integration"
```

---

## Task 13: Final Build Verification

- [ ] **Step 1: Full backend build**

Run:
```bash
go build ./cmd/nanite/
```

Expected: Compiles cleanly.

- [ ] **Step 2: Backend tests**

Run:
```bash
go test ./internal/workflow/ -v
```

Expected: All RunStore tests pass.

- [ ] **Step 3: Full frontend build**

Run:
```bash
cd ui && npm run build
```

Expected: Build succeeds with no errors.

- [ ] **Step 4: Frontend lint**

Run:
```bash
cd ui && npm run lint
```

Expected: No errors (warnings acceptable).

- [ ] **Step 5: Deploy and verify**

Run:
```bash
cerberus_rebuild nanite-api --reason "add worker status panel, workflow progress tab, memory viewer modal"
```

Then verify via `cerberus_logs nanite-api` that the service starts cleanly.
