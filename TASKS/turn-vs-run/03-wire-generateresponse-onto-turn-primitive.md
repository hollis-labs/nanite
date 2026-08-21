# Wire generateResponse onto the Turn primitive

**Phase:** 2 — Consume the primitive (`TASKS/turn-vs-run`)
**Status:** not-started
**Depends on:** `01-define-turn-primitive.md`
**Touches:** `internal/service/chat_generate.go` (the tool-settling loop body,
`:757-1726` at planning time — re-check line numbers before editing, this file changes
often), `internal/service/chat_loop_state.go` (read-only — `ls.limits.idleTimeout` is passed
into the primitive, nothing in this file is modified). Does not touch
`internal/service/workflow_step_executor.go` (task `04`, parallel-safe, disjoint file).

## Context

This is the actual behavior-preserving rewire doc 22's "Target design" describes:
*"The harness should expose **Turn** as a real, independently-callable primitive... **Run**
composes it: iterate Turns, settle tools, apply continuation policy, stop on completion."*
Task `01` built and unit-tested the primitive in isolation; this task makes
`generateResponse` actually use it, with **zero observable behavior change** — identical SSE
event sequences, identical recovery/retry behavior, identical persisted message/metrics
shape. This task is real risk, not a mechanical find-and-replace — treat every one of the
five distinct branches below as something to individually verify still behaves identically,
not just "the happy path compiles."

**The exact seam, confirmed this session, per line ranges at planning time (re-verify before
editing — `chat_generate.go` is ~3,650 lines and under active concurrent development):**

- `:1056-1067` — the provider-call start (`prov.StreamChat(...)` or `s.driveBootSession(...)`)
  becomes the `TurnStreamStarter` closure passed into `TurnRequest.Stream`.
- `:1309-1475` — the `streamLoop:` consumption block is replaced by the `ExecuteTurn` call.
  The three side-effect points that must move into `TurnSink` callbacks:
  - Thinking blocks (`:1462-1470`, `case "thinking":`) — currently emits
    `ch <- chat.StreamEvent{Type: "delta", Content: evt.ThinkingBlock.Thinking, Phase:
    chat.PhaseThinking}` immediately per block. Wire this exact send into
    `TurnSink.OnThinking`.
  - PTY tool presence + auto-artifact creation (`:1353-1370`, `case "tool_use":`) — the
    `chat.IsCLIProvider(providerName)` branch broadcasting `tool_pending`/`tool_resolved` and
    calling `s.maybeCreateAutoArtifact`. Wire into `TurnSink.OnToolUse`.
  - Delta buffering (`:1339-1351`) — `iterDeltaBuf = append(iterDeltaBuf, evt.Content)`.
    Wire into `TurnSink.OnDelta`; the phase-tagged flush *after* `stopReason` is known
    (`:1536-1546`) stays exactly as-is, operating on the buffer your `OnDelta` callback fills.
- `:1068-1260` (stream-start error), `:1395-1421` (mid-stream error event),
  `:1485-1526` (inactivity watchdog) — all three classification/recovery blocks stay in
  `generateResponse`, now operating on `ExecuteTurn`'s returned error/`TurnResult.Stalled`
  instead of the old inline `err`/`streamStalled` locals. The five distinct retry shapes that
  must keep working exactly as today:
  1. Stream-start compaction-recoverable error → `s.recoverFromContextOverflow` →
     `ls.iteration--; continue` (`:1095-1123`).
  2. Stream-start rate-budget error, first pause → `s.pauseAndMaybeRetryRateBudget` auto-retry
     → `ls.iteration--; continue` (`:1158-1168`).
  3. Stream-start compact-recoverable error, attempts exhausted, rate-budget sub-case →
     second pause path → `ls.iteration--; continue` (`:1227-1231`).
  4. Mid-stream `"error"` event, context-overflow recoverable → recover → discard this
     iteration's partial `TurnResult` entirely, `continue` without touching `chatMessages`
     beyond what recovery already rebuilt (`:1400-1421`, `contextOverflowRecovered` flag,
     handled post-loop at `:1569-1572`).
  5. Inactivity watchdog fires (`TurnResult.Stalled == true`) → today this is terminal (no
     retry) — confirm this stays terminal; don't accidentally add a retry path that doesn't
     exist today.
- `:1601-1725` — assistant-message construction and tool settlement
  (`preCheckTools`/`executeToolBatch`/`postProcessToolResults`) reads from the returned
  `TurnResult` (`.ToolUseBlocks`, `.ThinkingBlocks`, `.Text`/turn content) instead of the old
  inline `toolUseBlocks`/`thinkingBlocks`/`turnContent` locals. **Unchanged otherwise** — this
  whole block is out of the Turn primitive's scope per task `01`'s Context and this batch's
  README.

**What must NOT change**: the OTel span (`provSpan` / `nanite.provider.call`, task `01`'s
Context item 3 — stays in `generateResponse`, wrapping the `ExecuteTurn` call, with all its
existing attributes computed exactly as today); `ls.iteration`/`ls.continueWith`/debug
snapshot bookkeeping; the circuit-breaker check after tool settlement; every
`s.store.LogEvent`/`s.events.Emit*`/`s.pluginHost.Emit*` call currently inside these branches.

## What to do

1. Re-read `internal/service/chat_generate.go`'s current line numbers before starting — this
   file is long and actively edited; the ranges above are planning-time references, not
   guaranteed current.
2. Build the `TurnStreamStarter` closure at the existing `prov == nil` branch point
   (`:1056-1067`), capturing `provCtx`, `sessionID`, `session`, `agent`, `slotResult`,
   `userContent`, `ls.iteration`, `providerName` for the `driveBootSession` branch, and
   `extraSystemPrefix`/`slotBlocksFor(slotResult)`/`chatMessages`/`model`/`tools`/
   `cacheStrategy` for the `prov.StreamChat` branch — identical arguments to today's two call
   sites, just wrapped in a closure instead of called inline.
3. Build the `TurnSink` wiring three call sites described in Context, verbatim.
4. Replace the `streamLoop:` block with the `ExecuteTurn` call, passing
   `ls.limits.idleTimeout` as `IdleTimeout`.
5. Rewire all five retry/recovery branches (Context's numbered list) to operate on
   `ExecuteTurn`'s returned error / `TurnResult.Stalled`, preserving every existing message,
   log line, event emission, and retry decision exactly.
6. Rewire the post-loop assistant-message-construction and tool-settlement block to read from
   `TurnResult` instead of the old inline accumulators.
7. Run the existing chat-generation test suite (`internal/service/chat_generate*_test.go` and
   any integration tests exercising `generateResponse`) and confirm zero behavior change —
   if any existing test asserts on internal implementation details that no longer apply
   post-refactor (rather than on observable behavior — SSE events, persisted message shape,
   metrics), that's expected; update the test to assert the same observable behavior through
   the new shape, don't delete coverage.
8. Real dogfeed verification per `standards/testing.md`'s discipline: exercise at least one
   real chat turn producing tool calls, one producing a plain text answer, and (if
   practically reproducible) one of the five retry shapes — a compaction-recoverable
   context-overflow or a rate-budget pause — confirming the SSE stream and final persisted
   message are unchanged from pre-refactor behavior.

## Done means

- `generateResponse`'s loop body calls `ExecuteTurn` for the provider-call-and-stream-consume
  step; no inline `streamLoop:` remains.
- All five retry/recovery shapes from Context are preserved exactly, verified by test and/or
  dogfeed.
- SSE event sequences (delta/thinking/tool_pending/tool_resolved/status/stream_end/etc.),
  persisted assistant message content, metadata, and execution metrics are byte-for-byte
  equivalent to pre-refactor behavior for at least the three dogfeed scenarios in item 8.
- The OTel span, event/plugin-hook emissions, and loop-level bookkeeping
  (`ls.iteration`/`continueWith`/snapshots/circuit-breaker) are unchanged.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
