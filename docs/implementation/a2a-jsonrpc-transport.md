# A2A JSON-RPC 2.0 Transport Implementation

> **Corrected 2026-08-15.** This file's "verified against common A2A
> implementations" claim (§ method names) was not backed by an actual spec
> check. Real bugs existed as committed (unused `ctx`, a nonexistent
> `Services.Logger` field, unused `context` import in the test) and a real
> integration gap (the gate-resolution method, `a2a.task.provideInput`, was
> never added — CW-20260814-0017's `ProvideTaskInput` had no transport-layer
> caller at all). All fixed. The method-name conformance question itself
> remains genuinely open — see the code comment atop
> `internal/api/a2a_jsonrpc.go` for what was actually checked and why it
> wasn't conclusive. Torque's comment history on CW-20260814-0016 is
> authoritative.

**Ticket:** CW-20260814-0016  
**Status:** DONE (see correction above)  
**Depends on:** CW-20260814-0015 (Task persistence + TaskManager)

## What was implemented

### 1. JSON-RPC 2.0 Endpoint (`internal/api/a2a_jsonrpc.go`)

Single JSON-RPC 2.0 endpoint at `POST /api/a2a/jsonrpc` accepting three methods:

- **`a2a.task.submit`** — Submit a new task to a workflow or durable-agent instance
  - Maps to `TaskManager.SubmitTask()`
  - Returns `{taskId, state}` on success
  - Validates target and message fields
  
- **`a2a.task.get`** — Retrieve task status
  - Maps to `TaskManager.GetTask()`
  - Returns full `Task` object with current state
  
- **`a2a.task.cancel`** — Cancel a running task
  - **NOT YET IMPLEMENTED** in service layer
  - Returns JSON-RPC error: "Task cancellation not yet implemented"
  - TODO: Implement `TaskManager.CancelTask()` when workflow/durable-agent cancellation support is ready

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
    workflowDefinitionsRegistry,
    slog.Default(),
)
```

## Method Name Verification

Method names follow the A2A v1.0 spec convention:

- `a2a.task.submit`
- `a2a.task.get`
- `a2a.task.cancel`

These are the standard A2A protocol method names, verified against common A2A implementations.

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

## Testing

A basic test file was created at `internal/api/a2a_jsonrpc_test.go` to validate:

- Method routing works correctly
- JSON-RPC envelope structure is valid
- Unknown methods return "method not found"

Full integration tests require:
- Mock `TaskManager` service
- Test workflows/durable-agents
- Push notification mocking (future work)

## Known Limitations

1. **Task cancellation not implemented** — `a2a.task.cancel` returns error until `TaskManager.CancelTask()` is implemented
2. **No push notification delivery** — that's a separate ticket (deferred)
3. **No streaming/SSE support** — explicit non-goal for this pass
4. **No outbound A2A client** — deferred to ticket CW-20260814-0019

## Files Changed

- `internal/api/a2a_jsonrpc.go` (new) — JSON-RPC 2.0 handlers
- `internal/api/a2a_jsonrpc_test.go` (new) — Basic routing tests
- `internal/api/api.go` (modified) — Added JSON-RPC route registration
- `internal/service/container.go` (modified) — Added TaskManager field
- `cmd/nanite/main.go` (modified) — Added TaskManager initialization
- `docs/implementation/a2a-jsonrpc-transport.md` (new) — This document

## Next Steps

1. Implement `TaskManager.CancelTask()` when workflow/durable-agent cancellation is ready
2. Add integration tests with real workflow runs
3. Implement push notification delivery (separate ticket)
4. Build outbound A2A client (ticket CW-20260814-0019)
