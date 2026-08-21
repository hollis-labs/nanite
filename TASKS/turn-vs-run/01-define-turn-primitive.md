# Define the Turn primitive

**Phase:** 1 — Define the primitive; fix the name (`TASKS/turn-vs-run`)
**Status:** not-started
**Depends on:** none
**Touches:** new file, `internal/service/` package (recommended `internal/service/turn.go` +
`internal/service/turn_test.go` — same package as `chat_generate.go` and
`workflow_step_executor.go` so both can call it with no new import; see "What to do" item 1
for why a new top-level package is not recommended). No existing file is modified by this
task — `chat_generate.go` and `workflow_step_executor.go` start consuming the new primitive
in tasks `03`/`04`, not here.

## Context

Implements `docs/engineering/architecture/22-turn-vs-run.md`'s "Target design": *"The harness
should expose **Turn** as a real, independently-callable primitive — not just an anonymous
loop iteration inside `generateResponse`."* Per `docs/engineering/GLOSSARY.md`'s **Turn** vs.
**Run** entry: *"a **Turn** is one model invocation + stream + tool-call production + return
control... a **Run** is repeated Turns + tool settlement + continuation policy + completion."*

**This task defines and unit-tests the primitive in isolation. It does not wire it into
`generateResponse` or `ExecuteLLMStep` — that's tasks `03` and `04`.** Splitting it this way
lets a reviewer sign off on the primitive's *contract* before the higher-risk rewire of
`generateResponse`'s ~970-line loop body is attempted (see `README.md`'s scoping-deviation
note, item 5).

**Why the boundary is drawn exactly here — confirmed against real code this planning
session, not assumed from doc 22's one-paragraph description:**

`generateResponse`'s tool-settling loop (`internal/service/chat_generate.go:757-1726`) does
five genuinely different jobs per iteration, only one of which is "one Turn":

1. **Build and start the provider call** (`:1056-1067`) — either `prov.StreamChat(provCtx,
   llmtypes.ChatRequest{...})` or, for a CLI-hosted agent, `s.driveBootSession(provCtx,
   ...)`. Both return `(<-chan llmtypes.StreamEvent, error)` — the exact same shape.
2. **Consume the resulting stream** (`:1309-1475`, the `streamLoop:` label) — a `select`
   between the event channel and an inactivity-timeout timer
   (`streamInactivityWindow := ls.limits.idleTimeout`), accumulating text, `tool_use` blocks,
   thinking blocks, and usage, with incremental side effects fired *during* consumption:
   `ch <- chat.StreamEvent{...}` for each thinking block (immediately, not buffered),
   PTY `tool_pending`/`tool_resolved` presence broadcasts and `s.maybeCreateAutoArtifact` on
   each `tool_use` event (only for CLI providers), and delta text buffered into
   `iterDeltaBuf` (flushed with a phase tag only once `stopReason` is known, *after* the
   stream closes — a chat-specific UI decision, not a Turn concern).
3. **Classify and recover from a failed/stalled stream** (`:1068-1260` for a stream-start
   error, `:1395-1421` for a mid-stream `"error"` event, `:1485-1526` for the inactivity
   watchdog firing) — `ctxpkg.IsCompactRecoverable`, `IsContextOverflowMessage`,
   `errors.Is(err, llmcontracts.ErrRequestExceedsRateBudget)`, and the resulting compaction/
   rate-budget-pause retries that decrement `ls.iteration` and `continue` the outer loop.
   **This is Run-level continuation policy, not Turn's job** — a Turn either produces a
   result or fails; deciding *why* it failed and whether to retry with modified input
   belongs to the caller.
4. **Build the assistant message and settle tools** (`:1601-1725`) — appending
   `tool_use`/`text`/`thinking` content blocks to `chatMessages`, `preCheckTools` (permission/
   blocked/concurrency-safety gating), `executeToolBatch`, `postProcessToolResults`, and
   appending tool results as the next `user` message. **Explicitly out of scope for the Turn
   primitive** — see `README.md`'s "What this batch does NOT do."
5. **Loop-level bookkeeping** — `ls.iteration` advancement, `ls.continueWith`, debug
   snapshots, the circuit-breaker check. Stays in `generateResponse`.

Only job 1 + job 2 (plus enough of job 3 to *detect and report* a stall or mid-stream error,
without classifying or recovering from it) is "one Turn." This task builds exactly that, no
more.

**Corroborating precedent, confirmed this session**: `libs/go-llm-types/types.go:101-104`
already defines `func IsTurnComplete(ev StreamEvent) bool` — *"reports whether ev is a
terminal event marking the end of a turn."* Both `generateResponse` and `ExecuteLLMStep`
already depend on `go-llm-types`; that library already uses "Turn" for exactly this unit
(one streamed model response). This is real, existing, upstream vocabulary this task aligns
with — not new terminology invented for this batch.

**Why not a new top-level package** (e.g. `internal/turn`, mirroring `internal/loop`'s
precedent from `TASKS/loops`): both consumers (`chatServiceImpl` in `chat_generate.go`,
`workflowStepExecutor` in `workflow_step_executor.go`) already live in `package service`, and
the primitive's signature needs no type `internal/service` doesn't already import
(`llmtypes.ChatRequest`/`StreamEvent`/`ToolUseBlock`/`ThinkingBlock`/`Usage`, already imported
by both files). A new package would add an import for zero benefit and risks a future
import-cycle if the primitive ever needs a `service`-internal type. `internal/loop` is a
different case — a genuinely new subsystem with its own doc.go disambiguation needs (see
`TASKS/loops/07`). This is a shared internal helper, not a new subsystem; keep it in
`internal/service`.

## What to do

1. Add `internal/service/turn.go` (exact filename is your call; document if you deviate)
   defining:

   ```go
   // TurnStreamStarter begins a single provider stream for one Turn. The
   // caller supplies this closure so ExecuteTurn stays agnostic to which
   // provider or request shape is behind it — chat_generate.go's closure
   // may route to prov.StreamChat or driveBootSession (the CLI-hosted-agent
   // branch); workflow_step_executor.go's closure always uses
   // prov.StreamChat with its own narrower, capability-restricted
   // llmtypes.ChatRequest.
   type TurnStreamStarter func(ctx context.Context) (<-chan llmtypes.StreamEvent, error)

   // TurnSink receives incremental events as a Turn's stream is consumed,
   // in stream order, synchronously, before ExecuteTurn returns. All
   // fields are optional (nil-safe) — a caller that doesn't need
   // incremental side effects (e.g. workflow_step_executor.go) passes a
   // zero-value TurnSink.
   type TurnSink struct {
       OnDelta    func(content string)
       OnToolUse  func(tu llmtypes.ToolUseBlock)
       OnThinking func(tb llmtypes.ThinkingBlock)
   }

   // TurnRequest holds the inputs for exactly one Turn.
   type TurnRequest struct {
       Stream TurnStreamStarter
       // IdleTimeout bounds the gap between provider stream events (not
       // the whole Turn's wall-clock duration) — mirrors
       // chat_generate.go's existing streamInactivityWindow /
       // ls.limits.idleTimeout watchdog exactly. Required; callers pass
       // whatever value their own Run-level policy already resolved.
       IdleTimeout time.Duration
       Sink        TurnSink
   }

   // TurnResult is what one Turn produced.
   type TurnResult struct {
       Text           string
       ToolUseBlocks  []llmtypes.ToolUseBlock
       ThinkingBlocks []llmtypes.ThinkingBlock
       Usage          *llmtypes.Usage
       StopReason     string
       // Stalled is true when IdleTimeout fired — no provider event
       // arrived for the full window. Mirrors chat_generate.go's existing
       // streamStalled flag. err is also non-nil in this case (see below);
       // Stalled lets a caller distinguish this from an ordinary
       // mid-stream provider error without string-matching the error.
       Stalled bool
   }

   // ExecuteTurn runs exactly one model invocation: starts the stream via
   // req.Stream, consumes it with the inactivity watchdog, and returns the
   // accumulated result. It does not execute tools, does not classify or
   // recover from a compaction/rate-budget-exhaustion failure, and does
   // not decide whether to retry — those are Run-level (caller)
   // responsibilities. A non-nil error distinguishes three cases the
   // caller must be able to tell apart (chat_generate.go's existing three
   // branches each have different recovery/messaging):
   //   - req.Stream(ctx) itself returned an error (the call never started)
   //   - a mid-stream "error" event arrived (the call started, then failed)
   //   - the inactivity watchdog fired (TurnResult.Stalled == true)
   // Exact error typing (sentinel errors, a typed *TurnError, or plain
   // wrapping) is your call — document the choice and make sure a caller
   // can distinguish all three without parsing message text for anything
   // it needs to branch on (chat_generate.go's ctxpkg.IsCompactRecoverable/
   // IsContextOverflowMessage checks already work by inspecting the
   // underlying error via errors.Is/errors.As-style matching — preserve
   // that; don't flatten the underlying error into a string before it
   // reaches the caller in task 03).
   func ExecuteTurn(ctx context.Context, req TurnRequest) (TurnResult, error) {
       ...
   }
   ```

   Treat the exact field/function names above as illustrative, not gospel — if implementation
   surfaces a cleaner shape (e.g. `Turn` as a struct with a method instead of a package-level
   function), that's a legitimate call; document the deviation and why, same discipline every
   sibling batch's task files use for their own illustrative code.

2. **`ExecuteTurn`'s own stall/cancellation handling**: create its own cancelable child
   context internally (mirroring `chat_generate.go:880-882`'s
   `provCtx, provStreamCancel = context.WithCancel(provCtx)` / `defer provStreamCancel()`
   pattern) rather than requiring the caller to pre-wrap `ctx` — this keeps the
   inactivity-watchdog wiring (timer reset on every event, cancel-on-stall,
   stop-then-drain-then-reset on the timer) fully encapsulated in the primitive, matching
   `chat_generate.go:1306-1336`'s exact race-free idiom. Copy that idiom verbatim; don't
   invent a new one.

3. **Leave OTel span creation and Run-level bookkeeping to the caller.** `ExecuteTurn` should
   not create its own `feotel.StartSpan` — the existing `nanite.provider.call` span's
   attributes (`nanite.model`, `nanite.iteration`, `nanite.tools.count`,
   `nanite.messages.count`, `nanite.tokens.total`/`.ceiling`) are all Run-level bookkeeping
   the caller already has and the primitive shouldn't need to know about. Task `03` wraps its
   `ExecuteTurn` call with the existing span, unchanged.

4. **Preserve exact event-type handling from the current `streamLoop` switch** (`:1338-1474`):
   `delta` → accumulate + `OnDelta`; `tool_use` → accumulate + `OnToolUse` (fired once per
   block, in arrival order — callers needing PTY presence / auto-artifact behavior get it via
   this hook, they don't need special-casing inside `ExecuteTurn`); `usage` → accumulate into
   the returned `Usage` exactly as `chat_generate.go`'s existing summing logic does
   (`InputTokens +=`, `OutputTokens +=`, etc. — note today's code *sums* across multiple
   `usage` events within one iteration, it doesn't just take the last one; preserve that);
   `error` → stop consuming, return the error (do not attempt any
   `IsContextOverflowMessage`/recovery classification inside `ExecuteTurn` — that stays in
   task `03`'s caller code, unchanged); `session_id` and `done` → no-op, matching today's
   behavior exactly (`chat_generate.go:1452-1474` — both are already inert today; don't add
   new handling that doesn't exist yet, even though it would be easy to).

5. **Unit tests** (`internal/service/turn_test.go`), with a fake `TurnStreamStarter` (a
   channel you control from the test, no real provider) covering: normal completion
   (`stop_reason=end_turn`, no tool_use); a `tool_use`-producing iteration; a mid-stream
   `error` event surfaced as a distinguishable error; the inactivity watchdog firing when the
   fake starter's channel goes silent past `IdleTimeout` (use a short timeout in the test, not
   the real 900s/300s defaults); usage summed correctly across multiple `usage` events in one
   stream; and `TurnSink`'s three hooks each firing in the right order relative to the
   returned result (assert call order, not just call count). No real LLM call, no DB, no
   `chatServiceImpl`/`workflowStepExecutor` involved — this is a pure, isolated unit test of
   the new file only.

## Done means

- `internal/service/turn.go` (or your chosen filename) exists, compiles, and is not yet
  referenced by `chat_generate.go` or `workflow_step_executor.go` (that's tasks `03`/`04` —
  keep this task's diff isolated to the new file(s) only).
- The primitive's contract matches item 1's shape (or a documented, reasoned deviation): it
  owns provider-call-start + stream-consumption + inactivity-watchdog + incremental sink
  callbacks; it does NOT own tool execution/settlement, compaction/rate-budget recovery
  classification, retry decisions, or OTel span creation.
- Unit tests from item 5 all pass, with zero real provider/DB/network dependency.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- `docs/engineering/GLOSSARY.md`'s **Turn** vs. **Run** entry is left unmodified by this task
  (already accurate — no code citation in it points at a file this task changes) — confirm
  this remains true before marking done; if `ExecuteTurn`'s final shape changes any file:line
  citation the Glossary entry makes, note it in the Work Log (it currently cites
  `ls.iteration` in `chat_loop_state.go` and `generateResponse` as a whole, neither of which
  this task touches).

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
