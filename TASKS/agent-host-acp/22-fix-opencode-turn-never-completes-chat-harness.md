# Fix: a real OpenCode chat turn spawns, runs, and exits cleanly — but the
chat harness never observes it as done; the SSE stream hangs forever

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`), found during task `18`'s
real-binary dogfeed re-verification (itself following up on task `07`'s dogfeed).
**Status:** not-started
**Depends on:** none functionally, but was unreachable until task `18` landed — every real
OpenCode CLI session hard-crashed on `Config.Workdir is required` before this point (task `18`'s
own finding) was ever reachable.
**Touches:** `libs/agentkit/agentsessions/from_adapter.go` (`adapterSession.handleRunnerEvent`,
the `runner.EventProcessExited` case) most likely, possibly also
`libs/go-providers/provider/pty_opencode.go` (`OpencodeAdapter`'s doc comment, which currently
describes a synthesis contract that does not exist in the code it describes) — exact fix
location and shape is this task's own call. Sibling repo (`agentkit`), same package task `20`
already touched for an adjacent bug in the same waiter/completion-signaling family — worth
checking whether the two fixes compose cleanly or should land together.

## Context

Task `18` fixed a hard-crash (`wrapper: Config.Workdir is required`) that made 100% of real
OpenCode CLI sessions fail on their very first turn. Once that crash was fixed and re-verified
against the real `opencode` binary via a scratch-server dogfeed (real managed agent,
`default_provider='pty-opencode'`, `POST /api/harness/v1/sessions/{id}/turns`), a **second, real,
100%-reproducible bug** surfaced immediately behind it — never previously reachable because the
Workdir crash always fired first.

**Symptom, confirmed twice independently, live, against the real `opencode` binary:** a real chat
turn is sent, the SSE event stream (`GET /api/harness/v1/sessions/{id}/events?message_id=...`)
emits `tool_warning` then `stream_start` — and then **nothing else, ever**. No `delta`, no
`stream_end`, no error. Reconnecting to the same `message_id` minutes later (the event log is
buffered/replayed by event id, not a live-only tail — confirmed by the `tool_warning`/
`stream_start` pair replaying identically on every reconnect) still shows only those same two
events. Confirmed via `ps` that the real `opencode` subprocess itself spawns, runs, and **exits
cleanly within ~8 seconds**, having produced real output (independently reproduced by invoking
the exact same generated `.wrapper-exec/opencode.sh` script by hand outside Nanite — it printed a
correct, complete response and exited 0 in well under 15s). The server's own log shows
`chat-loop-diag: iter start` → `request_build` and then **nothing further** for that
`session_id`/`msg_id` — no `chat-loop-diag: provider stream closed`, unlike every other working
turn in the same log (Claude via the default agent, and the earlier Claude real-CLI turn logged
in task `07`'s Work Log, both show that log line within milliseconds of `request_build`).

**Root cause, traced end to end:**

1. Real interactive chat turns for `ModeLongLived` CLI sessions go through
   `internal/service/chat_boot_drive.go`'s `driveBootSession`, which calls
   `runtimeagent.Session.SendInput` (`internal/runtime/agent/manager.go`) once per turn — NOT
   through `agent.Boot`'s `AutoFireFirstTurn` path (that only fires the very first kickoff message
   for `ModeOneShot`/`ModeSubagent`/`ModeBackground`, never for `ModeLongLived` chat sessions).
2. `Session.SendInput` routes through `wr.SendInput` (`go-agent-wrapper`'s
   `Wrapper.SendInput`, `wrapper.go:552`), which calls the underlying `agentsessions.Session.
   SendInput` directly — for OpenCode's runtime shape (`Caps{}` all false → `NewFromAdapter`'s
   default case, `agentkit/agentsessions/from_adapter.go`'s `adapterSession`, the
   subprocess-per-turn "adapter runtime"), this is `adapterSession.SendInput`
   (`from_adapter.go:236-289`), which blocks on a real `runner.Run(...)` call for the whole turn.
3. `runner.Run`'s `cfg.OnEvent` is wired to `adapterSession.handleRunnerEvent`
   (`from_adapter.go:295-322`). Every `runner.EventProviderEvent` (i.e. every
   `OpencodeAdapter.ParseLine`-parsed line) is forwarded to `s.opts.EventFanout` — the same
   `fanout` channel `wrapper.Wrapper.Run`'s translator goroutine drains
   (`wrapper.go:494-513`) into `translateStreamEvent` (`wrapper/event_translator.go:23-`).
4. **`translateStreamEvent` only maps two `llmtypes.EventType`s to `runtimeevents.
   KindTurnCompleted`: `EventUsage` and `EventDone`** (`event_translator.go:37-48`).
5. `OpencodeAdapter.ParseLine` (`go-providers/provider/pty_opencode.go:79-94`, `Mode=""` — the
   *only* Mode Nanite's composition root wires up, confirmed at `internal/service/container.go:
   1005`, `provider.NewOpencodeAdapter()`, no Mode override anywhere in Nanite) **only ever emits
   `EventDelta`** — never `EventUsage`, never `EventDone`, by design (its own doc comment: "opencode
   emits plain text on stdout with no structured completion event").
6. `adapterSession.handleRunnerEvent`'s `case runner.EventProcessExited:` (`from_adapter.go:317-
   318`) does **nothing but `s.pid.Store(0)`** — it does not synthesize a terminal
   `EventDone`/`EventUsage` event when the adapter itself never emitted one.
7. Net: for a real OpenCode turn, `KindTurnCompleted` (or `KindTurnFailed`) is **never emitted**,
   at any layer, for any reason. `internal/service`'s `chat_generate.go` streamLoop (which
   persists the assistant message and closes the per-turn SSE stream) has nothing to key off of
   and simply never advances past `stream_start` — the hang this dogfeed observed.

**The `OpencodeAdapter` doc comment's own claim is false for the code path Nanite actually
uses.** `pty_opencode.go:24-27` states: "ParseLine emits llmtypes.EventDelta per non-empty line
and never emits llmtypes.EventDone/llmtypes.EventError directly; **the bridge synthesizes
llmtypes.EventDone on clean process exit**." No such synthesis exists anywhere in
`agentkit/agentsessions/from_adapter.go`. The *only* place in `agentkit` that does anything like
this is `serve_http_session.go`'s `markTurnDone()`/`session.idle` handling (`serve_http_session.
go:472-476`) — but that's OpenCode's *other* Mode (`"serve-http"`, opencode's own native HTTP+SSE
API, keyed off `session.idle`, not process exit), which **Nanite does not use** (confirmed:
`wrapper_adapter.go`'s own doc comment states plainly that adopting the shipped `ProtocolOpenCode
Native`/`TransportHTTPSSE` descriptor "would silently change... OpenCode's spawn shape" and is
explicitly out of scope for that migration; `runtimeConfigForAdapter` sets no `ServeHTTP` cap for
opencode today). Either the doc comment describes an intended contract that was never actually
implemented for the subprocess-per-turn shape, or it conflates the two Modes' completion
semantics. Whichever it is, the doc comment does not match the compiled behavior of the code path
this task's own dogfeed exercised.

**Confirmed OpenCode-specific, not a general adapter-runtime gap.** Codex's own `ParseLine`
(`go-providers/provider/pty_codex.go:233-250`) *does* emit `EventDone` on its own `"turn.
completed"` line — so once task `19`'s separate, unrelated `--skip-git-repo-check` gap is fixed,
Codex turns should complete correctly through this exact same `adapterSession`/`handleRunnerEvent`
plumbing. OpenCode is the only provider Nanite drives through the adapter-runtime shape whose
`ParseLine` deliberately never emits a terminal event.

**Confirmed pre-existing in shape, not introduced by task `18`'s fix.** Task `18` only changed
`opencodeLayout.SpawnWorkdir`'s fallback value (bootDir vs. empty string) — nothing about turn
completion signaling. This gap has existed in `agentkit`/`go-providers` since the adapter-runtime
shape was built; it was simply unreachable for OpenCode until task `18` cleared the crash in
front of it. (Task `07`'s own dogfeed never got this far for OpenCode either, for the same
reason.)

**Not exhaustively bisected against the full 900s idle timeout.** The dogfeed observed the hang
persist for several minutes (well past the ~8s the real subprocess itself took to exit) across
two independent real turns, and traced the root cause structurally (per the code path above) with
high confidence — but did not wait out the full `idle_timeout_s=900` (15 minutes) logged by
`chat-loop-diag: loop start` to confirm whether some outer watchdog eventually force-closes the
turn as failed. Even if it does, that would not satisfy "completes successfully" — a 15-minute
stall before a synthetic failure is still the bug this task is about.

## What to do

1. Fix `agentkit/agentsessions/from_adapter.go`'s `adapterSession.handleRunnerEvent` (or the
   `SendInput` call site directly) so that when a turn's `runner.Run` call completes without the
   adapter itself ever having emitted a terminal `EventDone`/`EventError`/`EventUsage`, the
   session synthesizes one — `EventDone` on a clean exit (`runner.EventProcessExited` with a nil
   `runner.Run` error), `EventError` on a non-nil `runner.Run` error (mirroring
   `encodeStreamEvent`'s existing `EventError`/`EventDone` framing at the bottom of the same
   file). This must not double-fire for adapters (like Codex) whose `ParseLine` already emits its
   own terminal event for the turn — track "did this turn already see a terminal event" the same
   way `serve_http_session.go`'s `markTurnDone()` already does for its own runtime kind, or reuse/
   adapt that mechanism if it's reasonably shareable.
2. Correct or clarify `OpencodeAdapter`'s doc comment (`pty_opencode.go:24-27`) to describe the
   actual, fixed contract accurately — do not leave a doc comment describing behavior the code
   doesn't implement.
3. Add a real, non-mocked regression test in `agentkit/agentsessions` exercising the exact gap:
   an `adapterRuntime`-backed session driven by a fake `CLIAdapter` whose `ParseLine` only ever
   emits `EventDelta` (mirroring `OpencodeAdapter`'s real contract) and never a terminal event —
   assert that a real spawned-and-exited subprocess still results in a terminal event reaching
   `EventFanout`. A second test should confirm an adapter that *does* emit its own terminal event
   (mirroring Codex) is not double-fired.
4. Add or extend a Nanite-side regression test (`internal/service`, near
   `chat_boot_drive_test.go`'s existing `TestAgentEventBridge_SynchronousTurnReachesTurnCh`) that
   models OpenCode's real event shape — a burst of `EventDelta`s with **no trailing `EventDone`**
   — and confirms the harness still closes the turn (once the `agentkit` fix lands and Nanite's
   pin is bumped) rather than assuming, as the existing test's own burst does today, that
   `EventDone` is always present.
5. Once fixed in `agentkit`, bump `go-agent-wrapper`'s pin and then Nanite's own consumption of
   it (mirroring task `20`'s and task `01`'s precedent — confirm with the Orchestrator whether pin
   bumps are being centralized across this batch's findings before bumping unilaterally, per task
   `20`'s own Work Log noting that centralization).
6. Re-run this task's exact dogfeed repro (scratch server, real `opencode` binary, a real chat
   turn via `POST /api/harness/v1/sessions/{id}/turns`) and confirm the SSE stream now reaches
   `delta`/`stream_end` for a real OpenCode response, and that the persisted assistant message row
   is non-empty on reload.

## Done means

- A real OpenCode CLI-hosted chat turn (`ModeLongLived`, the real interactive chat harness path —
  not just `agent.Boot`'s `ModeOneShot`/`AutoFireFirstTurn` path, which does not exhibit this bug)
  completes end-to-end against the real `opencode` binary: SSE stream reaches `delta` and
  `stream_end`, and the assistant message persists non-empty.
- New regression coverage exists at both the `agentkit` layer (fake adapter that never emits a
  terminal event, real subprocess) and the Nanite layer (event-bridge test modeling OpenCode's
  real Done-less event shape) pinning this gap.
- `OpencodeAdapter`'s doc comment accurately describes the actual, fixed behavior.
- `go build ./...` / `go test ./...` clean in `agentkit`; `go build ./cmd/nanite/`, `go vet
  ./...`, `go test ./...` clean in Nanite after the pin bump.
- Work Log documents whether Codex's own already-terminal-event-emitting `ParseLine` was
  independently verified not to double-fire under the fix.
