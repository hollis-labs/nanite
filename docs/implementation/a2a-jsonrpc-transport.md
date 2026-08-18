# A2A JSON-RPC 2.0 Transport Implementation

> **Corrected 2026-08-15, re-corrected 2026-08-18.** The 2026-08-15 note
> below said the method-name conformance question "remains genuinely
> open." It's now closed: a real spec-verification pass (see the comment
> atop `internal/api/a2a_jsonrpc.go`, and `TASKS/phase-0/08-a2a-conformance.md`'s
> Work log) confirmed the current A2A spec (github.com/a2aproject/A2A,
> release v1.0.1) uses bare PascalCase JSON-RPC method names —
> `SendMessage`/`GetTask`/`CancelTask` — and this file's method names have
> been renamed to match. `TaskManager.CancelTask` is also now implemented
> (real for the `'instance'` target kind; the `'workflow'` kind returns a
> typed "not supported" error — see `TASKS/ESCALATIONS.md`). The
> 2026-08-15 bug-fix history below (unused `ctx`, nonexistent
> `Services.Logger` field, missing `provideInput` transport wiring) stays
> accurate as a historical record.

**Ticket:** CW-20260814-0016
**Status:** DONE — method names spec-verified 2026-08-18, `CancelTask` implemented (instance target kind)
**Depends on:** CW-20260814-0015 (Task persistence + TaskManager)

## What was implemented

### 1. JSON-RPC 2.0 Endpoint (`internal/api/a2a_jsonrpc.go`)

Single JSON-RPC 2.0 endpoint at `POST /api/a2a/jsonrpc` accepting four methods (three spec methods, one Nanite-specific extension):

- **`SendMessage`** — Submit a new task to a workflow or durable-agent instance
  - Maps to `TaskManager.SubmitTask()`
  - Returns `{taskId, state}` on success
  - Validates target and message fields

- **`GetTask`** — Retrieve task status
  - Maps to `TaskManager.GetTask()`
  - Returns full `Task` object with current state

- **`CancelTask`** — Cancel a running task
  - Maps to `TaskManager.CancelTask()`
  - Real execution path for `target_kind = 'instance'` (reuses `DurableAgentService.RequestStop`)
  - Returns a typed JSON-RPC error (`ErrTaskNotCancelable`, `-32004`) for `target_kind = 'workflow'` — no interrupt primitive exists for an in-flight workflow run yet (see `TASKS/ESCALATIONS.md`)

- **`a2a.task.provideInput`** — Resolve a paused workflow gate (CW-20260814-0017)
  - Nanite-specific extension, deliberately non-spec-shaped — the real A2A spec has no dedicated "provide input" method; it resumes a paused task via a new `SendMessage` on the same `taskId`

### 2. Agent Card Endpoint (Already Existed)

Plain HTTP GET endpoint at `/.well-known/agent-card.json`:

- **Already implemented** in `internal/api/a2a.go` (from CW-20260814-0014)
- Serves the A2A Agent Card for discovery
- Delegates to `AgentCardGenerator.Generate()`
- Returns JSON with skills derived from:
  - Named workflow definitions (Agent Workflows registry)
  - Durable-agent boot profiles (bootprofile.Registry)

**No changes needed** — the agent card endpoint was already wired in ticket CW-20260814-0014.

### 3. HTTP Route Wiring (`internal/api/api.go`)

Routes registered in `RegisterRoutes()`:

```go
mux.HandleFunc("GET /.well-known/agent-card.json", a.handleAgentCard)
mux.HandleFunc("POST /api/a2a/jsonrpc", a.handleA2AJSONRPC)
```

### 4. Service Container Wiring

- Added `TaskManager *TaskManager` field to `service.Container`
- Initialized in `cmd/nanite/main.go` after `AgentCardGenerator`:

```go
container.TaskManager = service.NewTaskManager(
    s,
    workflowLauncher,
    container.DurableWake,
    container.DurableAgents,
    workflowDefinitionsRegistry,
    slog.Default(),
)
```

## Method Name Verification

Spec-verified 2026-08-18 against the official A2A Protocol specification (github.com/a2aproject/A2A, release tag v1.0.1, published 2026-05-26 — the current latest stable release, mirrored at https://a2a-protocol.org/latest/specification/), §5.3 "Method Mapping Reference" and §9.4 "Core Methods":

- `SendMessage`
- `GetTask`
- `CancelTask`

These are bare PascalCase, no prefix/namespace — confirmed via the spec's literal example JSON-RPC request bodies (e.g. `"method": "SendMessage"`), not inferred from the human-readable prose alone. Pre-1.0 drafts (tag v0.3.0) used a different, slash-style convention (`message/send`, `tasks/get`, `tasks/cancel`); v1.0.0 (released 2026-03-12) renamed the whole method set to PascalCase. See the comment block atop `internal/api/a2a_jsonrpc.go` for the full citation and the historical note on why an earlier (2026-08-15) review found "inconsistent" results.

`a2a.task.provideInput` has no spec equivalent and is not force-fit into spec vocabulary — see above.

## Error Handling

JSON-RPC 2.0 error codes used:

- `-32700`: Parse error (invalid JSON)
- `-32600`: Invalid request (missing jsonrpc/method)
- `-32601`: Method not found (unknown method)
- `-32602`: Invalid params (missing required fields)
- `-32603`: Internal error (service layer failures)

A2A-specific error codes (from `internal/a2a/jsonrpc.go`):

- `-32000`: Task not found
- `-32001`: Task rejected (validation failure)
- `-32002`: Target not found (workflow/instance doesn't exist)
- `-32003`: Invalid target (malformed address)
- `-32004`: Task not cancelable (already terminal, or `target_kind` has no cancellation primitive — e.g. `'workflow'`)

## Testing

`internal/api/a2a_jsonrpc_test.go` validates:

- Method routing works correctly (spec-verified method names)
- JSON-RPC envelope structure is valid
- Unknown methods return "method not found"
- `handleTaskCancel` end-to-end against a real `*service.TaskManager` + real store: a successful `'instance'`-target cancel, and a `'workflow'`-target cancel returning `ErrTaskNotCancelable`

`internal/service/a2a_task_manager_test.go` covers `TaskManager.CancelTask` directly: instance-target success (asserting the reused `RequestStop` primitive is actually called), workflow-target `ErrWorkflowCancelUnsupported`, task-not-found, idempotent re-cancel of an already-canceled task, and rejection of canceling an already-terminal (completed) task.

`internal/service/a2a_push_notifier_test.go`'s `TestA2APushNotifier_TickerDrivenPath_EndToEnd` drives the real production enqueue call site (`TaskManager.CancelTask` → `enqueuePushNotification`) rather than calling `EnqueueDelivery` directly, then calls `ProcessPendingDeliveries` — exactly what the `cmd/nanite/main.go` 30s ticker calls — and confirms a real local HTTP listener receives the notification.

## Known Limitations

1. **Workflow-target cancellation not implemented** — `CancelTask` returns `ErrTaskNotCancelable` for `target_kind = 'workflow'`. `WorkflowLauncher.Launch` runs the engine synchronously in-process; its per-run `context.CancelFunc` is local and deferred, never stored anywhere a later, separate `CancelTask` request could reach. Building a real interrupt primitive (an async launch path + a run registry, or a context-checked cooperative-cancellation signal inside the step loop) is a genuine from-scratch gap, escalated in `TASKS/ESCALATIONS.md` rather than half-built.
2. **No streaming/SSE support** — explicit non-goal for this pass.
3. **No outbound A2A client** — deferred to ticket CW-20260814-0019.

Push notification delivery is no longer a limitation — see the ticker-driven end-to-end test above.

## Files Changed

- `internal/api/a2a_jsonrpc.go` — JSON-RPC 2.0 handlers; 2026-08-18: method-name rename, `handleTaskCancel` wired to the real `TaskManager.CancelTask`
- `internal/api/a2a_jsonrpc_test.go` — routing + `handleTaskCancel` end-to-end tests
- `internal/a2a/jsonrpc.go` — 2026-08-18: added `ErrTaskNotCancelable` (-32004)
- `internal/service/a2a_task_manager.go` — 2026-08-18: added `CancelTask`, `ErrWorkflowCancelUnsupported`, `durableAgentCanceller`
- `internal/service/a2a_task_manager_test.go` — `CancelTask` coverage
- `internal/service/a2a_push_notifier_test.go` — 2026-08-18: added the ticker-driven end-to-end test
- `internal/api/api.go` — Added JSON-RPC route registration
- `internal/service/container.go` — Added TaskManager field
- `cmd/nanite/main.go` — TaskManager initialization; 2026-08-18: threaded `container.DurableAgents` through for `CancelTask`
- `docs/implementation/a2a-jsonrpc-transport.md` — this document

## Next Steps

1. Build a real interrupt primitive for workflow-target cancellation (async launch + run registry, or a cooperative-cancellation signal inside the step loop) — see Known Limitations above and `TASKS/ESCALATIONS.md`.
2. Build outbound A2A client (ticket CW-20260814-0019).
