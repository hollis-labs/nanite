# Stamp a real session ID into workflow tool-step context

**Phase:** 2 — Non-agentic reach
**Status:** not-started
**Depends on:** `01` (sequencing only — a workflow-triggered `python_run` call is meaningless
until `01`'s dispatcher/perm-checker wiring is real; no file/type overlap with `01`)
**Touches:** `internal/agentworkflow/types.go` (new field on `ToolStepRequest`),
`internal/service/workflow_step_executor.go` (`ExecuteToolStep`),
`internal/service/workflow_engine.go` (`buildToolStepRequest`)

## Context

`agentworkflow.StepExecutor.ExecuteToolStep` (interface: `internal/agentworkflow/interfaces.go:
38`; implementation: `internal/service/workflow_step_executor.go:102-115`) already calls any named
tool generically:

```go
// workflow_step_executor.go:102-115
func (e *workflowStepExecutor) ExecuteToolStep(ctx context.Context, req agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
    ...
    result, err := e.tools.Execute(ctx, req.AgentID, req.Tool, req.Args)
    ...
}
```

This can already reach `python_run` today — no new `StepKind` is needed (`ToolStepRequest`
already carries `Tool`/`Args`/`AgentID`; a workflow step author just names `python_run` as the
tool). **The real gap**: `mcp.WithSessionID`/`WithCallerProfile` are only stamped into `ctx` by the
LLM-turn loop's tool-execution path (`chat_tool_executor.go:354,369`, inside
`executeToolBatch`) — never by `ExecuteToolStep`. A workflow-triggered `python_run` call
therefore reaches `RunPythonSandbox`/`pumpToolCalls` with no session identity in `ctx` at all,
collapsing onto `callRunPython`'s own hardcoded `"ptc-default"` pseudo-session fallback
(`self_tools_python.go:2451-2457`) — not wrong, but not meaningfully attributable to the
`WorkflowRun` that actually triggered it either.

**`ToolStepRequest` has no `SessionID` field to stamp from at all** (`internal/agentworkflow/
types.go:173-182`):

```go
type ToolStepRequest struct {
    WorkflowRunID string `json:"workflow_run_id,omitempty"`
    StepID        string `json:"step_id,omitempty"`
    AgentID string `json:"agent_id,omitempty"`
    Tool string         `json:"tool"`
    Args map[string]any `json:"args,omitempty"`
}
```

Compare `LLMStepRequest`, which already has one (`types.go:103`, `SessionID string
\`json:"session_id,omitempty"\``), populated today only when a workflow step author explicitly
sets it in the step's own YAML/JSON `Config` — `buildLLMStepRequest`
(`internal/service/workflow_engine.go:809-830`) reads `SessionID: configString(cfg,
"session_id")` (line 821). There is no equivalent for tool steps at all today.

**The session-id-minting question this task must resolve** (left open by `docs/engineering/
architecture/27-code-mode.md`'s own text: "mint a per-`WorkflowRun` synthetic session id, or reuse
the `WorkflowRun`'s own id as the session id"): **resolved by this planning session — reuse the
`WorkflowRun`'s own ID.** Investigated what a session ID actually gates downstream before
deciding, so this isn't a guess: (a) `permission.Engine.Check`'s session-scoped grants
(`sessionGrants[sessionID][toolName]`, `internal/permission/engine.go`) — any stable string works,
no requirement that it resolve to a real `sessions` table row; (b) `store.GetSession(sessionID)`
lookups elsewhere (e.g. `resolveProjectIDFromSession`, `self_tools_transport.go:1088-1097`)
degrade softly to `""` on a not-found ID rather than erroring; (c) `event_log.session_id`
(`internal/store/migrations/001_schema.sql:475`) is a bare `TEXT` column with **no FK
constraint** — confirmed directly from the schema, not assumed. The codebase already tolerates a
non-FK'd, non-"real"-session sentinel string in exactly this code path today: `callRunPython`'s
own `"ptc-default"` fallback. Given all three, minting a fresh synthetic ID per `WorkflowRun`
would add an extra ID↔run mapping for no benefit; reusing `workflow_runs.id` directly (already
available as `buildToolStepRequest`'s own `runID` parameter, `workflow_engine.go:832`) gives every
tool call from that run a stable, already-meaningful, immediately-correlatable identifier, with
negligible collision risk against real chat `sessions.id` values (independently-generated ID
spaces, different tables).

## What to do

1. Add a `SessionID` field to `agentworkflow.ToolStepRequest` (`internal/agentworkflow/types.go:
   173-182`), mirroring `LLMStepRequest.SessionID`'s existing doc comment style:
   ```go
   type ToolStepRequest struct {
       WorkflowRunID string `json:"workflow_run_id,omitempty"`
       StepID        string `json:"step_id,omitempty"`

       // SessionID, when non-empty, scopes this tool call's permission checks
       // and downstream tool-side session lookups (e.g. python_run's sandboxed
       // tool_call() attribution). When the step author leaves this empty,
       // ExecuteToolStep falls back to WorkflowRunID — every tool step gets a
       // real, stable identity, not the shared "ptc-default" pseudo-session.
       SessionID string `json:"session_id,omitempty"`

       AgentID string `json:"agent_id,omitempty"`

       Tool string         `json:"tool"`
       Args map[string]any `json:"args,omitempty"`
   }
   ```
2. In `internal/service/workflow_engine.go`'s `buildToolStepRequest` (lines 832-844), read an
   author-time override the same way `buildLLMStepRequest` already does (line 821):
   ```go
   return agentworkflow.ToolStepRequest{
       WorkflowRunID: runID,
       StepID:        step.ID,
       SessionID:     configString(cfg, "session_id"),
       AgentID:       configString(cfg, "agent_id"),
       Tool:          tool,
       Args:          configMap(cfg, "args"),
   }, nil
   ```
3. In `internal/service/workflow_step_executor.go`'s `ExecuteToolStep` (lines 102-115), stamp
   `ctx` before calling `e.tools.Execute`, falling back to `req.WorkflowRunID` when the step author
   didn't set `SessionID` — matching `chat_tool_executor.go:354,369`'s exact stamping convention:
   ```go
   func (e *workflowStepExecutor) ExecuteToolStep(ctx context.Context, req agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
       if e.tools == nil {
           return agentworkflow.ToolStepResult{}, fmt.Errorf("workflow: tool step executor has no ToolService configured")
       }
       if req.Tool == "" {
           return agentworkflow.ToolStepResult{}, fmt.Errorf("workflow: tool step requires a tool name")
       }

       sessionID := req.SessionID
       if sessionID == "" {
           sessionID = req.WorkflowRunID
       }
       if sessionID != "" {
           ctx = mcp.WithSessionID(ctx, sessionID)
       }
       if req.AgentID != "" {
           ctx = mcp.WithCallerProfile(ctx, req.AgentID)
       }

       result, err := e.tools.Execute(ctx, req.AgentID, req.Tool, req.Args)
       if err != nil {
           return agentworkflow.ToolStepResult{}, fmt.Errorf("workflow: tool step %q: %w", req.Tool, err)
       }
       return agentworkflow.ToolStepResult{Output: result.Output, IsError: result.IsError}, nil
   }
   ```
   Add the `"github.com/hollis-labs/nanite/internal/mcp"` import to `workflow_step_executor.go`
   (not currently imported there).
4. Do **not** extend this same stamping fix to `ExecuteLLMStep`'s own internal tool-call loop
   (same file, the `for iter := 0;;` loop calling `e.tools.Execute(ctx, req.AgentID, tu.Name,
   tu.Input)`). It has an analogous gap, but it is out of this task's scope — see the batch
   README's "What this batch does NOT do." Do not silently fix it and do not silently leave it
   unmentioned; if you notice it while editing this file, leave a short code comment pointing at
   this task file so a future reader isn't surprised it wasn't touched.

## Done means

- `agentworkflow.ToolStepRequest` has a `SessionID` field; `buildToolStepRequest` populates it
  from the step's own `Config["session_id"]` when set.
- `ExecuteToolStep` stamps `mcp.WithSessionID` (using `req.SessionID`, falling back to
  `req.WorkflowRunID` when empty) and `mcp.WithCallerProfile` (using `req.AgentID`, when non-empty)
  onto `ctx` before calling `e.tools.Execute`.
- A new test proves a tool step with no author-configured `session_id` results in
  `mcp.SessionIDFromContext(ctx)` returning the step's `WorkflowRunID` inside the tool it calls —
  not `""` and not `"ptc-default"`. A stub `ToolService.Execute` capturing the `ctx` it was called
  with (matching `workflow_step_executor_test.go`'s existing test style) is sufficient; a second
  test confirms an author-configured `session_id` in `Config` overrides the `WorkflowRunID`
  fallback.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
