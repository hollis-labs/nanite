# Consolidate workflow_step_executor.go's ExecuteLLMStep onto the Turn primitive

**Phase:** 2 — Consume the primitive (`TASKS/turn-vs-run`)
**Status:** not-started
**Depends on:** `01-define-turn-primitive.md`
**Touches:** `internal/service/workflow_step_executor.go` (`ExecuteLLMStep`, lines 123-254 at
planning time). Does not touch `internal/service/chat_generate.go`/`chat_loop_state.go`
(task `03`, parallel-safe, disjoint file) or `internal/agentworkflow/interfaces.go` (the
`StepExecutor` interface itself is unchanged — this task is an internal implementation
consolidation, not an API change).

## Context

Implements doc 22's "Target design," the second half: *"Once that primitive exists,
`workflow_step_executor.go`'s duplicate loop becomes unnecessary — a workflow step requests
either a single Turn (bounded, capability-restricted, matching what its doc comment already
claims to do) or a full Run, from the one shared implementation, instead of maintaining its
own."* And the real-cost framing from doc 22's "The real cost" section: *"`StepExecutor.
ExecuteLLMStep` is documented as running 'one capability-restricted agent turn,' but its
actual implementation... is a completely separate, independently-maintained tool-use loop...
It has the same one-word-two-meanings problem baked into its own doc comment."*

**What this task does and does not consolidate — confirmed by direct comparison this
session, not assumed:**

`ExecuteLLMStep` (`workflow_step_executor.go:123-254`) does two genuinely different jobs
inside its own `for iter := 0; ; iter++` loop:

1. **The model-call-and-stream-consumption unit** (`:166-213`) — build a
   `llmtypes.ChatRequest{Model, SystemPrompt, Messages, Tools}`, call `prov.StreamChat(ctx,
   chatReq)`, consume the returned channel accumulating `textBuf`/`pendingToolUses`/
   `stopReason`/`lastUsage` via a `switch ev.Type` over exactly `EventDelta`/`EventToolUse`/
   `EventUsage`/`EventError` (no `thinking`/`session_id`/`done` handling — narrower than
   `chat_generate.go`'s switch, which is fine, `TurnSink`'s hooks are all optional). **This is
   the actual duplicate** — mechanically the same job as `chat_generate.go`'s
   streamLoop (task `01`/`03`), just narrower (no SSE, no thinking, no PTY, no watchdog
   today). This task replaces it with a call to `ExecuteTurn`.
2. **Capability-restricted tool settlement** (`:213-252`, inside the same loop, after the
   model-call unit returns with `pendingToolUses` non-empty) — appends the assistant message,
   then for each `tool_use` block: refuses (with a typed error result, not a panic or silent
   drop) any tool name outside `allowed` (the capability-restriction backstop — "the model was
   only ever offered `toolDefs` built from `req.Tools`, so this should be unreachable in
   practice... if a provider adapter ever echoes back a tool_use outside what was offered,
   refuse to execute it"), otherwise calls `e.tools.Execute(ctx, req.AgentID, tu.Name,
   tu.Input)` **directly — no `preCheckTools` permission/blocked/concurrency-safety gating,
   no per-tool caps, no `loopState` at all.** This is deliberately, structurally different
   from `chat_generate.go`'s tool settlement (`internal/agentworkflow/interfaces.go:26-30`'s
   own doc comment states this is load-bearing: *"Capability restriction... and tool steps
   never touching model inference are load-bearing invariants of this interface, not
   implementation details left to callers"*). **This task does NOT touch this block, does NOT
   route it through `preCheckTools`/`executeToolBatch`/`postProcessToolResults`, and does NOT
   give it permission checks or per-tool caps it doesn't have today.** Doing so would be a
   real behavior/security-model change (loosening or altering capability restriction),
   explicitly out of scope — see `README.md`'s "What this batch does NOT do."

`ExecuteLLMStep`'s own doc comment (`:117-122`, *"runs one capability-restricted agent
turn"*) is itself an instance of the naming conflation doc 22 describes: the real
implementation, unchanged by this task, is a bounded (`maxIter`, default
`DefaultMaxToolIterations = 10`) **capability-restricted Run** — repeated Turns (job 1) +
capability-restricted tool settlement (job 2) — not a single Turn. Correct this doc comment
as part of this task (see "What to do" item 3) since it's a direct, one-line fix now that the
vocabulary exists to state it accurately, and leaving it wrong after this task lands would be
worse than before (a stale doc comment sitting directly above code that now visibly composes
Turns, for anyone who reads both together).

**Also confirmed this session**: `Verify`'s `mode: agent` path (`verifyAgent`, `:355-378`)
calls `e.ExecuteLLMStep` itself (a nested call, "a reviewer step is a second, independent
`ExecuteLLMStep` call" per the interface's own doc comment) — this task's consolidation
applies transparently to that path too, with zero code change needed in `verifyAgent`/
`composeReviewerPrompt`/`parseReviewerVerdict` (they only ever call the public
`ExecuteLLMStep` method, never touch its internals).

## What to do

1. Re-verify `workflow_step_executor.go`'s current line numbers before editing (task `01`
   will have landed by the time this task starts; this file itself may also have moved).
2. Inside `ExecuteLLMStep`'s `for iter := 0; ; iter++` loop, replace the model-call-and-
   stream-consumption block (`:166-213` at planning time — everything from building `chatReq`
   through `return agentworkflow.LLMStepResult{...}, nil` for the no-more-tool-uses case)
   with a call to task `01`'s `ExecuteTurn`:
   - `TurnRequest.Stream`: a closure calling `prov.StreamChat(ctx, llmtypes.ChatRequest{
     Model: req.Model, SystemPrompt: systemPrompt, Messages: messages, Tools: toolDefs})` —
     identical request shape to today, just wrapped.
   - `TurnRequest.IdleTimeout`: this loop has no existing inactivity watchdog today (unlike
     `chat_generate.go`) — decide a reasonable default (document your reasoning; a generous
     fixed value or a new `agentworkflow.LLMStepRequest` field, your call — don't silently
     inherit a chat-specific constant like `defaultIdleTimeoutSeconds` without considering
     whether a workflow step's expected duration profile is actually the same shape as an
     interactive chat turn's). Adding a watchdog here is a real, new (mildly) behavioral
     change — a currently-nonexistent termination condition — flag it plainly in the Work Log
     as a deliberate consequence of adopting the shared primitive, not a silent side effect.
   - `TurnRequest.Sink`: not needed here (zero-value `TurnSink` — no SSE, no thinking, no PTY
     concerns in this call site) — confirm `ExecuteTurn` behaves correctly with an
     all-nil-callback sink (task `01`'s unit tests should already cover this; if they don't,
     that's a gap to flag back, not silently work around here).
   - Map `TurnResult.Text`/`.ToolUseBlocks`/`.Usage`/`.StopReason` onto the existing
     `textBuf`/`pendingToolUses`/`lastUsage`/`stopReason` locals (or restructure the loop
     variables to use `TurnResult` fields directly — your call).
   - Map a returned error (including a stalled `TurnResult` if you added the watchdog) onto
     `ExecuteLLMStep`'s existing `return agentworkflow.LLMStepResult{}, fmt.Errorf(...)`
     error-wrapping convention — preserve the `"workflow: llm step stream error: %w"`-style
     wrapping so callers see the same error shape/classification they do today.
3. Leave the capability-restricted tool-settlement block (`:213-252`) completely untouched —
   same `allowed` map check, same direct `e.tools.Execute` call, same
   `agentworkflow.ToolCallRecord` construction, same message-append shape.
4. Correct `ExecuteLLMStep`'s own doc comment (`:117-122`) to state accurately that it
   composes a bounded, capability-restricted sequence of Turns (a capability-restricted Run),
   not a single Turn — cite `docs/engineering/architecture/22-turn-vs-run.md` and
   `GLOSSARY.md`'s Turn/Run entry. Do not change `internal/agentworkflow/interfaces.go`'s
   `StepExecutor.ExecuteLLMStep` interface doc comment (a one-line comment, "ExecuteLLMStep
   runs one capability-restricted agent turn.", immediately above the method signature inside
   the `StepExecutor` interface block) without checking whether it needs the same correction —
   if it does, that's a legitimate small addition to this task's scope (same file family, same
   fix); note it in the Work Log either way.
5. Run `workflow_step_executor_test.go` (or wherever its tests live) and confirm all existing
   behavior — `ExecuteLLMStep`'s max-iteration error, the capability-restriction refusal for
   an out-of-surface tool name, `Verify`'s both modes, `verifyAgent`'s nested call — is
   unchanged. Add a test asserting the model-call unit specifically goes through
   `ExecuteTurn` if that's mechanically checkable (e.g. via a fake provider and asserting the
   same event-consumption behavior task `01`'s own unit tests already establish), rather than
   just re-testing `ExecuteTurn` itself here (already covered in task `01`).

## Done means

- `ExecuteLLMStep`'s model-call-and-stream-consumption logic is `ExecuteTurn` calls, not a
  second hand-written copy of the same switch-over-event-types logic.
- Capability-restricted tool settlement (the `allowed` map check, direct `e.tools.Execute`,
  no permission/cap gating) is unchanged, byte-for-byte, from before this task.
- `ExecuteLLMStep`'s own doc comment accurately describes what it does under the new
  vocabulary (a capability-restricted Run composed of Turns, not "one turn").
- Any new behavior this task's shared-primitive adoption introduces (most likely: an
  inactivity watchdog that didn't exist before) is a deliberate, documented, called-out
  decision in the Work Log — not a silent side effect discovered later.
- `Verify`'s both modes (`engine`, `agent`) and `verifyAgent`'s nested `ExecuteLLMStep` call
  continue to work with zero code change to either.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- `docs/engineering/architecture/00-overview.md:55`'s "Turn as a callable harness primitive,
  and the `workflow_step_executor.go` consolidation it would enable... target architecture
  only, no implementation plan" bullet is updated — this task is the one that actually
  finishes the consolidation half of that sentence; correct or remove the bullet.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
