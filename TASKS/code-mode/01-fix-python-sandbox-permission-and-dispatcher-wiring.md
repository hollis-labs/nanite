# Fix Python sandbox permission-checker and tool-dispatcher wiring

**Phase:** 1 — Production wiring fix
**Status:** not-started
**Depends on:** none
**Touches:** `internal/selftools/self_tools_transport.go` (no change, just the target fields),
new file `internal/service/python_tool_dispatcher.go`, `cmd/nanite/main.go` (the `selfTools.X =
container.Y` wiring block, ~lines 626-698)

## Context

`internal/selftools/self_tools_python.go` implements `python_run` (`RunPythonSandbox`) — an
isolated, resource-capped Python subprocess with a `tool_call(name, args)` helper that's supposed
to bridge every call through the real permission engine and tool dispatcher, no security hole
opened by design (see the tool's own doc comment, `self_tools_python.go:522-558`, and
`docs/engineering/architecture/27-code-mode.md`'s "Confirmed: designed as the real mechanism"
section). It's wired through two fields on `SelfToolsTransport`:

```go
// self_tools_transport.go:198-208
PythonPermChecker PythonPermissionChecker
PythonDispatcher  PythonToolDispatcher
```

Both are documented as "set post-construction." **Neither ever is, anywhere in the production
build path.** Grepped `cmd/nanite/main.go` and `internal/service/container.go` in full during this
planning session — zero assignments to either field outside `self_tools_python_test.go:508-509`
(a test-only stub). The consequence, in `pumpToolCalls` (`self_tools_python.go:414-459`):

```go
// self_tools_python.go:418-434
if perm != nil {
    checkResult := perm.Check(ctx, sessionID, req.Name, req.Args, permission.ToolMeta{...})
    if checkResult.Decision == permission.DecisionDeny { ... }
}
// self_tools_python.go:437-438
if dispatcher == nil {
    resp.Error = "no tool dispatcher configured"
}
```

A nil `perm` **silently skips the permission check entirely** — every `tool_call()` from inside a
running `python_run` script is currently allowed unconditionally, a live, real security gap (not
hypothetical — `python_run` is broadly reachable today, gated only by an agent profile's tool
permissions and a dev-mode flag, per the tool's own doc comment). A nil `dispatcher` makes every
`tool_call()` fail outright with `"no tool dispatcher configured"`. This is the highest-priority
task in this batch — a live correctness/security bug, not new work — and `02`/`03` are pointless
until it's fixed (a workflow- or reflex-triggered `python_run` call would just hit the same
unwired dispatcher and fail or bypass permissions identically).

**The two interfaces to satisfy** (`self_tools_python.go:44-53`):

```go
type PythonPermissionChecker interface {
    Check(ctx context.Context, sessionID, toolName string, input map[string]any, meta permission.ToolMeta) permission.CheckResult
}
type PythonToolDispatcher interface {
    Dispatch(ctx context.Context, sessionID, toolName string, args map[string]any) (any, error)
}
```

**`PythonPermissionChecker` needs no adapter.** `container.Permissions` (`*permission.Engine`,
`internal/service/container.go:900`, `permissions := permission.NewEngine(permission.ModeDefault,
nil)`, exposed as the public `Container.Permissions` field, `container.go:176`) already has a
`Check(ctx context.Context, sessionID, toolName string, input map[string]any, meta ToolMeta)
CheckResult` method (`internal/permission/engine.go:117`) that matches this interface exactly,
structurally — this is the *same* engine instance already enforcing every ordinary chat-turn tool
call (`chat_tool_executor.go:162`, `s.permissions.Check(ctx, sessionID, tu.Name, tu.Input, meta)`).
Wiring it in gives `python_run`'s `tool_call()` real, identical enforcement to every other tool
call in the harness — not a parallel, weaker permission path.

**`PythonToolDispatcher` needs a real, small adapter.** The natural thing to route through is
`ToolService.Execute` (`internal/service/tool.go:59`, `Execute(ctx context.Context, agentID,
toolName string, input map[string]any) (*ToolResult, error)`) — the same "route through
ToolClient/permission checks, falling back to direct MCPManager execution" path every other tool
call in the harness goes through (`toolServiceImpl.Execute`, `internal/service/tool.go:364-381`).
Two shape mismatches need bridging, not re-derivation:

1. **Second parameter.** `PythonToolDispatcher.Dispatch` takes `sessionID`; `ToolService.Execute`
   wants `agentID`. Resolve `agentID` via `mcp.CallerProfileFromContext(ctx)`
   (`internal/mcp/tool_ctx.go:67`) — the same context-stamping convention
   `chat_tool_executor.go:369` (`ctx = mcp.WithCallerProfile(ctx, agentID)`) already establishes
   for every ordinary tool call. `ctx` reaching `Dispatch` already carries this stamp when
   `python_run` is invoked from an ordinary LLM turn (the call chain is
   `executeToolBatch`→`executeSingleTool`→…→`CallTool`→`callRunPython`→`RunPythonSandbox`→
   `pumpToolCalls`→`Dispatch`, and the stamp happens at the top of that chain, `chat_tool_executor.
   go:354,369`, before any of it runs) — nothing new needs stamping for *this* task's target path.
   (Task `02` and task `03` are each responsible for stamping it onto `ctx` for their own,
   different entry points — a workflow tool step and a reflex firing, neither of which passes
   through `chat_tool_executor.go` at all.)
2. **Return shape.** `ToolService.Execute` returns `*ToolResult{Output string, IsError bool}`;
   `Dispatch` must return `(any, error)` — the `any` becomes exactly the JSON dict the sandboxed
   Python's `tool_call()` receives as its return value (`pumpToolCalls`'s `resp.Result =
   toolResult`, decoded Python-side as `resp.get("result", {})`). `python_run`'s own tool
   description promises "Returns a dict, raises RuntimeError on denial or error"
   (`self_tools_python.go:143`'s Python preamble docstring) — but most tool results are
   `mcp.TextResult`-wrapped plain prose, not JSON, and there's no existing convention in this
   codebase for turning an arbitrary `ToolResult.Output` into a dict. **This planning session's
   default** (flagged, not locked — see the batch README's design-call #3): wrap every non-error
   result as `map[string]any{"output": result.Output}` (always a dict, matching the promise, no
   fragile JSON-sniffing), and turn an `IsError` result into a Go `error` (e.g.
   `fmt.Errorf("%s", result.Output)`) so `pumpToolCalls` records it as `Status: "error"` and the
   Python side sees a real `RuntimeError`, matching the tool's own documented contract. If a
   worker finds an existing, better convention for shaping tool output into structured JSON
   elsewhere in the codebase, use it instead — document the deviation in the Work Log.

**Where to wire it.** `SelfToolsTransport` (`selfTools`) is constructed entirely inside `cmd/
nanite/main.go`'s `initMCP` (line ~997), *before* `service.NewContainer` is called (line ~372).
Every other post-construction dependency `selfTools` needs is wired in the block right after
`NewContainer` returns (lines ~626-698, e.g. `selfTools.Messaging = container.Messaging`,
`selfTools.AgentClassifier = container.AgentConfig`, `selfTools.Elicitation =
container.Elicitation`) — add this task's two assignments to that same block, in the same style.
`container.Tools` and `container.Permissions` are both unconditionally constructed inside
`NewContainer` (never nil in production), so neither assignment needs a nil-guard the way some of
the neighboring optional ones do.

## What to do

1. Create `internal/service/python_tool_dispatcher.go`:
   ```go
   package service

   import (
       "context"
       "fmt"

       "github.com/hollis-labs/nanite/internal/mcp"
       "github.com/hollis-labs/nanite/internal/selftools"
   )

   // pythonToolDispatcher adapts ToolService to selftools.PythonToolDispatcher —
   // the bridge python_run's sandboxed tool_call() helper dispatches real tool
   // calls through, matching the same ToolClient/MCPManager path every ordinary
   // chat-turn tool call goes through (toolServiceImpl.Execute).
   type pythonToolDispatcher struct {
       tools ToolService
   }

   // NewPythonToolDispatcher builds the adapter. Called independently from two
   // separate construction sites — cmd/nanite/main.go (wiring SelfToolsTransport,
   // task 01) and internal/service/container.go (wiring the reflexes.Executor's
   // run_python_sandbox hook, task 03) — because SelfToolsTransport and the
   // reflex Engine are built in two different object graphs with no reference
   // to each other (see this batch's README, design-call #4). Both call sites
   // get a behaviorally-identical, independently-constructed, stateless adapter
   // — that duplication is intentional, not a bug to collapse into one shared
   // field.
   func NewPythonToolDispatcher(tools ToolService) selftools.PythonToolDispatcher {
       return &pythonToolDispatcher{tools: tools}
   }

   func (d *pythonToolDispatcher) Dispatch(ctx context.Context, sessionID, toolName string, args map[string]any) (any, error) {
       agentID := mcp.CallerProfileFromContext(ctx)
       result, err := d.tools.Execute(ctx, agentID, toolName, args)
       if err != nil {
           return nil, err
       }
       if result.IsError {
           return nil, fmt.Errorf("%s", result.Output)
       }
       return map[string]any{"output": result.Output}, nil
   }
   ```
   Confirm `selftools.PythonToolDispatcher` really is satisfied structurally by this type (`go
   build` will catch it if not) and adjust the return-shape design call per this task's Context if
   a better existing convention turns up.
2. In `cmd/nanite/main.go`, in the `selfTools.X = container.Y` wiring block (~lines 626-698), add:
   ```go
   selfTools.PythonPermChecker = container.Permissions
   selfTools.PythonDispatcher = service.NewPythonToolDispatcher(container.Tools)
   ```
3. Confirm `internal/selftools`'s `PythonPermissionChecker`/`PythonToolDispatcher` interfaces need
   no changes — this task only supplies real implementations, it doesn't touch the interfaces
   themselves or `RunPythonSandbox`/`pumpToolCalls`'s logic.

## Done means

- `selftools.SelfToolsTransport.PythonPermChecker` and `.PythonDispatcher` are both non-nil on the
  real production construction path (`cmd/nanite/main.go`), confirmed by reading the wired-up
  values, not just the assignment lines compiling.
- A `tool_call()` from inside a `python_run` script invoked through the normal LLM-turn path now
  (a) actually enforces permission checks — a denied tool inside the sandbox surfaces as a
  `RuntimeError` naming the denial reason, not a silent allow — and (b) actually dispatches
  through the real tool broker and gets a real result back, not `"no tool dispatcher configured"`.
- A new automated test exercises the real `pythonToolDispatcher` adapter directly (both the
  success path — result shape is `{"output": ...}` — and the `IsError`/dispatch-error paths
  return a Go `error`), not just a visual confirmation that the two `main.go` assignment lines
  exist. If a full production-container integration test is impractical here, a focused unit test
  on the adapter type plus confirming `cmd/nanite/main.go` compiles with the two new lines wired
  is sufficient — document whichever the worker chooses in the Work Log.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
