# Turn vs. Run

A follow-up architecture topic from the harness audit, addressing whether Nanite conflates one reasoning/tool-producing model invocation with the full autonomous tool-settling loop. [19-api-cli-runtime-parity.md](19-api-cli-runtime-parity.md) confirmed the conflation is real and flagged it as a separate topic without resolving it. This doc resolves it.

## Confirmed: two levels, one name

`internal/service/chat_generate.go`'s `generateResponse` drives `for ls.iteration = 0; ; ls.iteration++` (`chat_loop_state.go`), calling the provider, executing any tool calls, feeding results back, and repeating until `stopReason != "tool_use"` or a hard breaker fires (runaway tool failures, idle timeout, iteration ceiling). One `ls.iteration` is one model invocation + stream + tool-call production + return control — call this a **Turn**. The whole loop — repeated Turns + tool settlement + continuation policy + completion — is `generateResponse` itself, and it's the level `chat_loop_state.go`'s own vocabulary (`MaxTurns`, `TerminationMaxTurns`) currently, confusingly, also calls "Turn." Call this outer level a **Run**. There is no name for the inner unit today; the outer loop's own vocabulary squats on the word that should belong to the inner one.

Durable agents don't get their own boundary here either: a wake routes straight through `ChatService.HandleMessage` → `generateResponse` (`durable_agent_runtime_controller.go`'s `SendMessage`), identical to interactive chat — a durable agent gets a full Run every wake, with no way to request just one Turn.

## The real cost: Agent Workflows already hit this and built a second loop

This isn't naming pedantry. `internal/agentworkflow/interfaces.go`'s `StepExecutor.ExecuteLLMStep` is documented as running "one capability-restricted agent turn," but its actual implementation (`internal/service/workflow_step_executor.go`) is a **completely separate, independently-maintained tool-use loop** (`DefaultMaxToolIterations = 10`, its own `StreamChat` call, its own tool-result stitching) that never touches `chat_loop_state.go`/`generateResponse`. It has the same one-word-two-meanings problem baked into its own doc comment. Workflows needed Turn-level control and, lacking a way to ask the shared harness for it, duplicated the whole loop instead — two independently-evolving termination/budget implementations doing the same job.

## Naming: namespaced, not avoided

Generic words are fine reused across this codebase's subsystems as long as usage stays namespaced and any real ambiguity gets an explicit glossary disambiguation — the same discipline `GLOSSARY.md` already applies to "slot" (Team Slot vs. Context Broker slot: both kept, never used bare, one entry explaining the split). Turn/Run get the same treatment rather than inventing fresh words to dodge the collision:

- **`Turn.Cancel`** — cancel just the current model call. Finer-grained than what exists today; not yet built (`internal/loopdetect`'s existing per-Turn-tool-call guard is a different mechanism — runaway-repetition detection, not cancellation).
- **`Run.Cancel`** — cancel the whole tool-settling loop. This is what `Turn.Cancel`/`CancelActiveGeneration` (`internal/service/chat.go`) already does today, under the wrong name — for a CLI-hosted agent it unbinds Nanite's listener for the current generation without touching the underlying process. **Rename `CancelActiveGeneration`'s conceptual name from `Turn.Cancel` to `Run.Cancel`** — same scope, correct name, `GLOSSARY.md` updated in this pass.
- **`Session.Stop`** — unaffected, already correctly scoped and named (terminates the whole runtime process).

**The `WorkflowRun`/`TeamRun`/`LoopRun` overlap** ([21-loops.md](21-loops.md) landed concurrently with this review, establishing `Run` as a suffix for persisted, multi-step orchestration instances) is real but not a blocker: those are compound identifiers naming *a specific kind of persisted execution record*, while bare **Run** here names *the harness's inner tool-settling loop within one `generateResponse` call* — a different altitude, the same way "Team Slot" and "Context Broker slot" are both real, coexisting, and disambiguated rather than merged or renamed away. `GLOSSARY.md`'s Run entry states this explicitly, same pattern as the existing Slot entry.

## Target design

The harness should expose **Turn** as a real, independently-callable primitive — not just an anonymous loop iteration inside `generateResponse`. **Run** composes it: iterate Turns, settle tools, apply continuation policy, stop on completion. Once that primitive exists, `workflow_step_executor.go`'s duplicate loop becomes unnecessary — a workflow step requests either a single Turn (bounded, capability-restricted, matching what its doc comment already claims to do) or a full Run, from the one shared implementation, instead of maintaining its own.

This was target architecture at authoring time. **Approved for implementation, 2026-08-21** — exposing Turn as a callable primitive and the `CancelActiveGeneration` → `Run.Cancel` rename are now real, scoped, sequenced work, tracked under `TASKS/turn-vs-run/` (see that folder's `README.md` for the task breakdown). `workflow_step_executor.go`'s consolidation onto the shared primitive is the natural follow-through this design enables, scoped within that same batch as far as the planning pass judges practical — see the batch README for exactly what landed in this pass versus what's carried forward.

## What's genuinely still open

- `Turn.Cancel` (single-model-call cancellation) is a real future option now that the vocabulary exists for it, not a current requirement — no evidence of pressure for finer-grained cancellation today. Not included in the approved batch.
- Whether durable agents should ever get Turn-level wake granularity (vs. always getting a full Run) — no evidence of real pressure for this yet; noted as a possible future consequence of the primitive existing, not a current requirement. Not included in the approved batch.
