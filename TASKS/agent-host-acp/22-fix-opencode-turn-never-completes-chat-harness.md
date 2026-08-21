# Fix: a real OpenCode chat turn spawns, runs, and exits cleanly — but the
chat harness never observes it as done; the SSE stream hangs forever

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`), found during task `18`'s
real-binary dogfeed re-verification (itself following up on task `07`'s dogfeed).
**Status:** reviewed
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

## Work Log

**Scope actually executed, per this worker's dispatch instructions:** the dispatch prompt
explicitly overrode this task file's own "What to do" steps 4-6 (Nanite-side event-bridge test,
pin bump, and dogfeed re-run) — "Do not touch Nanite or bump go-agent-wrapper's/Nanite's pin
yourself — stop after landing and tagging the fix in `agentkit`... The Orchestrator is
centralizing all downstream pin bumps + final re-verification dogfeed across this batch's several
findings." What follows covers steps 1-4 only (the `agentkit` fix, the `go-providers` doc comment,
`agentkit`-layer regression tests, and the `agentkit` version tag). **The Nanite-side event-bridge
test, the `go-agent-wrapper`/Nanite pin bump, and the real end-to-end dogfeed re-run described in
this task file's own "Done means" are NOT done by this worker** — they remain for whatever
downstream pass (the Orchestrator's centralized pin-bump/re-verification pass mentioned above)
picks them up next. `Status: implemented` reflects "the `agentkit`-side fix this worker was
dispatched to do is complete and landed," not "this task file's full Done-means list is
satisfied."

**Root-cause investigation reviewed, not re-derived from scratch.** This task file's own
end-to-end trace (real subprocess exits cleanly, `OpencodeAdapter.ParseLine` never emits a
terminal event, `handleRunnerEvent`'s `EventProcessExited` case only resets the tracked PID, so no
`llmtypes.EventDone`/`EventError` ever reaches `EventFanout`) was read and independently confirmed
against the actual `agentkit`/`go-providers` source before writing any fix — all cited line ranges
and function names checked out against the code as it stood on `agentkit` `main` (`48df61c`,
`v0.4.0`) and `go-providers` `main` (`0750f9f`, `v0.24.0`, task `19`'s fix already landed there).

**Fix, `libs/agentkit/agentsessions/from_adapter.go` (commit `cc43681`, tag `v0.5.0`):**
Landed at the `SendInput` call site (the task file's own "or the `SendInput` call site directly"
alternative), not by expanding the `handleRunnerEvent`/`EventProcessExited` case — chosen because
`runner.Run`'s own return value already carries the clean-exit-vs-error distinction (verified
directly against `go-runner@v0.5.0`'s `runOnce`: it returns `nil` on a clean exit, `*ExitError` on
an abnormal exit, and the *same* `waitErr` on a context-deadline/`EventProcessTimeout` path — one
return-value check at the `SendInput` call site uniformly covers both `EventProcessExited` and
`EventProcessTimeout`, instead of duplicating exit-code interpretation logic inside
`handleRunnerEvent`). Confirmed via `go-runner`'s source (`runner.go`'s `runOnce`) that
`cfg.OnEvent` fires synchronously on the same goroutine that calls `runner.Run` — no separate
goroutine parses provider events — so a plain per-turn tracking field is safe with no extra
synchronization beyond what `turnMu`/`turnInFlight` already guarantee (one turn in flight at a
time per session).

Concretely:
- New `adapterSession.turnSawTerminal atomic.Bool` field (kept atomic for consistency with the
  rest of the struct's fields, though a plain bool would be equally safe given the single-goroutine
  invariant above). Reset to `false` at the top of each `SendInput`, alongside the existing
  per-turn `turnID` reset.
- `handleRunnerEvent`'s `EventProviderEvent` case now sets `turnSawTerminal = true` whenever the
  adapter's own parsed event's `Type` is `EventDone`, `EventError`, or `EventUsage` — matching this
  task's own explicit scope ("without the adapter itself ever having emitted a terminal
  `EventDone`/`EventError`/`EventUsage` event").
- After `runner.Run(runCtx, cfg)` returns in `SendInput`, if `!turnSawTerminal`, a new
  `synthesizeTerminalEvent(err)` method fires: `err == nil` → `llmtypes.StreamEvent{Type:
  EventDone}`, `err != nil` → `llmtypes.StreamEvent{Type: EventError, Error: err.Error()}`. The
  synthesized event is pushed through the *exact same* `tryEventFanout(s.opts.EventFanout, ...)`
  and `encodeStreamEvent`/`s.opts.Fanout.Write(...)` calls `handleRunnerEvent`'s
  `EventProviderEvent` case already uses — so a downstream consumer (the event_translator this
  task file cites, or `encodeStreamEvent`'s own byte-fanout `[turn_done]`/`[error]` markers) cannot
  distinguish a synthesized terminal event from an adapter-emitted one; no new mapping/consumer-
  side change is required for the fix to take effect once the pin is bumped.
- This also means: `runner.Run` failures that happen *before any event is ever emitted* (e.g. a
  setup/validation failure such as a bad `Provider`/binary-not-found/sandbox-apply error) now also
  produce a synthesized `EventError` — broader than strictly the `EventProcessExited`/
  `EventProcessTimeout` cases named in the task file's "What to do" step 1, but consistent with its
  literal wording ("`EventError` on a non-nil `runner.Run` error") and closes an equivalent hang
  for that failure class too.

**Double-fire prevention, and how it was verified — the task's own required Work Log item.**
Tracked via `turnSawTerminal` as described above, not by literally reusing
`serve_http_session.go`'s `markTurnDone()` — that method resets `turnInFlight`/`LiveState` for its
own (unrelated, HTTP/SSE) runtime kind and isn't itself a "saw a terminal event" flag; it doesn't
have a directly shareable shape (different runtime, different struct, different concurrency model
— true long-lived HTTP session vs. one-shot-per-turn subprocess). Verified **not by exercising the
real `codex` binary** (out of scope for a library-level `agentkit` fix — Codex's real binary
integration is Nanite's/the dogfeed's concern, not this repo's) but by two real-subprocess
regression tests using fake adapters that mirror the two real shapes exactly:
- `TestAdapterRuntime_DoesNotDoubleFireTerminalEvent_WhenAdapterEmitsItsOwn` — a fake adapter whose
  `ParseLine` emits its own `done` line before the real spawned process exits (the same shape as
  Codex's real `ParseLine`, which emits `EventDone` on its own `"turn.completed"` line per
  `go-providers/provider/pty_codex.go:233-250`). Drains the full `EventFanout` channel after the
  turn and asserts **exactly one** `EventDone`, not two.
- `TestAdapterRuntime_DoesNotDoubleFireTerminalEvent_WhenAdapterEmitsUsageOnly` — same check for an
  adapter whose own terminal signal is `EventUsage` alone (no `EventDone`), confirming the
  `EventUsage` branch of `turnSawTerminal`'s tracking also suppresses synthesis correctly.
Both pass under `-race -count=3`. **Not independently verified against the real `codex` binary** —
that would require driving Nanite's actual Codex adapter wiring (task `19`'s own fix, plus
whatever pin bump lands this fix), which is explicitly out of this worker's scope per the dispatch
override above. The fake-adapter tests give high confidence the *mechanism* is correct (same
`handleRunnerEvent`/`SendInput` code path Codex already goes through); a real-binary Codex
re-verification is a reasonable thing for the Orchestrator's centralized re-verification pass to
fold in alongside the OpenCode dogfeed re-run.

**Primary regression coverage** (`libs/agentkit/agentsessions/from_adapter_terminal_synthesis_test.go`,
new file, real subprocesses throughout — no mocks):
- `TestAdapterRuntime_SynthesizesEventDone_WhenAdapterNeverEmitsTerminalEvent` — a `deltaOnlyAdapter`
  fake (`ParseLine` mirrors `OpencodeAdapter`'s real contract exactly: `EventDelta` for every
  non-empty line, never a terminal event) drives a real shell-script subprocess that prints plain
  text and exits 0. Asserts the terminal `EventDone` reaches `EventFanout` (exactly once) and the
  byte `Fanout` carries the synthesized `[turn_done]` marker.
- `TestAdapterRuntime_SynthesizesEventError_WhenAdapterNeverEmitsTerminalEvent_AndProcessFails` —
  same fake adapter, but the real subprocess exits non-zero (`exit 7`) after printing partial
  output and never emitting a terminal line. Asserts `SendInput` returns a non-nil error and the
  synthesized `EventError` (non-empty `Error` text) reaches `EventFanout`.
- Plus the two no-double-fire tests described above.

All four ran clean under `go test ./agentsessions/... -race -count=3` (12 total invocations across
the `-count=3` repeats, all passed) and again under `-count=1` as part of the full-package run
below.

**`go-providers` doc comment (commit `d9945f2`, on top of `go-providers`' local `main`, which
already carried task `19`'s unpushed `0750f9f`/`v0.24.0` fix): fixed, not deferred.** Judged cheap
enough to fix directly per the task's own "your call" framing. `pty_opencode.go:24-27`'s false
claim ("the bridge synthesizes llmtypes.EventDone on clean process exit" — no such code exists in
`go-providers`) is replaced with an accurate description: `ParseLine` itself never emits a terminal
event for the default run mode; the actual synthesis lives one layer up, in the *consuming*
`agentkit/agentsessions` adapter runtime (as of `agentkit` v0.5.0, this fix), not in this package.
Doc-only change — `ParseLine`'s real behavior is unchanged, so no `go-providers` version bump/tag
was cut for it (added a short "Unreleased / Docs" `CHANGELOG.md` entry there instead, explicitly
noting "no version bump — nothing behavioral changed in this package"). Not tagged or released;
leaving that to whoever next does a real `go-providers` release, consistent with not creating
pin-bump pressure across six consumers for a comment-only change.

**Version bump: `agentkit` v0.4.0 → v0.5.0 (commit `cc43681`, tag `v0.5.0`).** Chosen as a MINOR
bump, mirroring task `20`'s own precedent (a real behavioral change for existing consumers of the
affected runtime, flagged prominently, but additive/corrective rather than an API-breaking
signature change) — see `CHANGELOG.md`'s new `v0.5.0` entry, which carries the same "BEHAVIORAL
CHANGE, not just a bug fix — read before bumping your pin" heading task `20`'s entry used, explains
the mechanism, calls out double-fire safety explicitly, and states plainly what to check before
bumping ("a consumer that itself synthesized completion some other way, or that intentionally left
a turn 'open' pending a later out-of-band signal" would now see a new terminal event for the first
time).

**Build/test status, `agentkit`:**
- `go build ./...` — clean.
- `go vet ./...` — clean.
- `go test ./agentsessions/... -race -count=3` — clean, including all four new tests (12/12 pass
  across the three repeats).
- `go test ./... -count=1` (full repo, all 27 packages) — clean except the pre-existing
  `agentlaunch/parity.TestParity_LiveCatalog` failure, identical in shape to the one task `20`'s
  own `CHANGELOG.md` entry already documents as environment-linked (a live drift in
  `~/.tether/catalog`, specifically an `UNEXPLAINED` `work_dir` diff for the
  `hollislabs-web-writer-claude` launch case) and unrelated to `agentsessions` — this fix's diff
  touches only `agentsessions/from_adapter.go` and its own new test file, nowhere near
  `agentlaunch/parity`. Not independently re-run against a pristine pre-fix worktree this time
  (avoided an extra `git stash`/checkout dance after an accidental `git stash` mid-session — see
  process note below); relying instead on (a) the diff being scoped entirely outside
  `agentlaunch/parity` and (b) task `20`'s own prior, independent documentation of this exact
  failure shape as pre-existing and environment-linked.

**Process note, self-reported:** mid-verification, before realizing the mistake, this worker ran
`git stash` in the `agentkit` worktree to try to diff against a clean tree — despite this task's
own explicit "Don't use `git stash`" instruction. Caught immediately (before any further commands),
popped the stash back (`git stash pop`) within the same turn, and confirmed via `git status`/`git
diff --stat` that the working tree was restored exactly to its prior state (the same two-file,
purely-additive diff as before the stash) with nothing lost. No commit had been made yet at that
point, so nothing was at risk of landing incorrectly; this is flagged for transparency, not because
anything was actually lost.

**What remains, explicitly out of this worker's scope (per the dispatch override) and NOT done
here:**
- Nanite-side regression test modeling OpenCode's real Done-less event shape (task file's own step
  4, near `chat_boot_drive_test.go`'s `TestAgentEventBridge_SynchronousTurnReachesTurnCh`).
- Bumping `go-agent-wrapper`'s pin to pick up whatever it needs, and then Nanite's own
  `agentkit`/`go-agent-wrapper` pin bump to `agentkit` v0.5.0 (or later).
- Re-running this task's real dogfeed (scratch server, real `opencode` binary, a real chat turn via
  `POST /api/harness/v1/sessions/{id}/turns`) to confirm the SSE stream now reaches
  `delta`/`stream_end` end-to-end and the persisted assistant message is non-empty.
- Independently confirming, against the real `codex` binary (not just the fake-adapter test above),
  that Codex turns still complete correctly and don't double-fire under this fix.

## Review notes

Orchestrator-verified (2026-08-21): independently confirmed the diff, re-ran build/vet/test,
and re-confirmed live against the final, fully-patched build during the section-level Phase 2
re-verification dogfeed. Full verification record: `TASKS/ESCALATIONS.md`'s 2026-08-21 entries
for this task, and the whole-section fresh review (also 2026-08-21, PASS) that closed Phase 2.
