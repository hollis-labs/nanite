# Fix: `adoptReplacementSession` overwrites `activeSessions` without stopping the session it
replaces — a real orphaned-process leak on the mid-stream-error broker path

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`), found during task `07`'s
real-provider dogfeed validation.
**Status:** implemented
**Depends on:** none. Independent of task `18`/`19`/`20` — different code, different repo
(Nanite, not a sibling), can land in any order relative to them.
**Touches:** `internal/service/chat_boot_drive.go` (`adoptReplacementSession`,
`observeSessionForRecovery`) and/or `internal/service/chat_http_broker_notify.go`
(`notifyRecoveryBrokerForHTTPStreamError`). Repo: Nanite.

## Context

Found live during `TASKS/agent-host-acp/07`'s real-provider dogfeed. **Caveat up front, stated
plainly so the dispatched worker doesn't over-trust the exact trigger sequence**: the specific
run that surfaced this involved some of task `07`'s own test-session messiness (a mid-dogfeed
server restart, a stale `--resume` provider-session-id from an earlier broken-auth attempt) —
the *symptom* (a real, live subprocess left running long after its `agent_runtime` row was
marked `done`) is solid and directly observed, but the *exact* sequence that triggered it was not
independently re-isolated with a clean, single-variable repro before this write-up. The code-level
root cause below is real and independently verifiable by reading; confirming it against a clean
repro should be this task's own first step, not assumed from task `07`'s messier one.

**What was directly observed**: a real `claude` CLI subprocess (plus its `.mcp.json`-spawned
Nanite self-tools MCP sidecar process) remained alive and running for 9+ minutes after the
`agent_runtime` row for that session was marked `state='done'`, `failure_reason='broker retry
attempt 1'` — i.e. after the recovery broker had already classified a failure and dispatched a
replacement session for that same chat session. Killing both PIDs by hand confirmed they were
genuinely still alive, not zombies (`ps` showed `SN`, actively scheduled).

**Root cause candidate, from direct code reading** (`internal/service/chat_boot_drive.go`):

There are two independent call paths that can reach `internal/recovery/broker`'s
`OnSessionExit` → `DispatchRetry` → (on success) the replacement-session hook
(`adoptReplacementSession`):

1. **`observeSessionForRecovery`** (the real `Session.Wait()`-driven path) — calls
   `s.activeSessions.Delete(sessionID)` *before* calling `s.agentDeps.Recovery.OnSessionExit(...)`
   (see the code's own comment: "Per-session state cleanup happens BEFORE OnSessionExit. The
   broker may dispatch a replacement session... Cleaning up after OnSessionExit returns would
   race-clobber the freshly stored replacement."). This path is internally consistent: by the
   time `OnSessionExit` fires, the old entry is already gone, so `adoptReplacementSession`'s own
   `s.activeSessions.Store(sessionID, sess)` is a clean write into an empty slot, not an
   overwrite of something live.
2. **`notifyRecoveryBrokerForHTTPStreamError`** (`chat_http_broker_notify.go`) — called directly
   from `chat_generate.go`'s stream-consuming loop whenever a mid-turn stream error is observed
   (a chat-harness-level condition, entirely independent of whether the underlying process is
   still alive), and reaches the *same* `broker.OnSessionExit(sessionID, exit, meta)` entrypoint
   (`chat_http_broker_notify.go:221`) with a synthesized `cause="http_stream"` `ExitError`. **This
   file has zero `activeSessions.Delete` calls anywhere** (confirmed by grep) — it never clears
   the old session from the cache before notifying the broker.

`adoptReplacementSession`'s own doc comment states an invariant that is only actually true for
path 1: "the prior session's Delete(sessionID) ran before adoptReplacementSession is invoked (see
observeSessionForRecovery's call ordering)". When the broker is instead reached via path 2, that
invariant does not hold: `adoptReplacementSession`'s `s.activeSessions.Store(sessionID, sess)`
silently **overwrites** whatever live session was still cached under `sessionID` — with no
`.Stop()` call on the thing it's replacing anywhere in either function. The old session's own
real subprocess (and its still-live `observeSessionForRecovery` goroutine, separately blocked on
that old session's own `Wait()`) is left running, un-tracked by `activeSessions`, until/unless it
happens to exit on its own.

**Two independent risks this creates, if confirmed**:
- A real resource leak — an orphaned CLI subprocess (plus its MCP sidecar) running indefinitely,
  consuming a real provider session/quota, for every mid-stream-error-triggered broker retry.
- A double-notification / clobber race: if the orphaned old session's process *does* eventually
  exit on its own, its own still-running `observeSessionForRecovery` goroutine will fire a
  *second* `activeSessions.Delete(sessionID)` + a *second* `OnSessionExit` call for the same
  `sessionID` — at that point potentially deleting/clobbering whatever the *replacement* session
  had since stored there, and dispatching an unwanted second-tier retry against a session that
  might already be healthy.

## What to do

1. **First**, build a clean, single-variable repro before touching any code — don't fix blind
   against task `07`'s messier observation. Suggested shape: drive a real (or realistic fake)
   session through `driveBootSession` to a live, cached `activeSessions` entry, then force
   exactly one `notifyRecoveryBrokerForHTTPStreamError` call (a mid-stream error) *without* an
   underlying process exit, with the broker configured to actually dispatch a retry — confirm
   whether the pre-existing session is left un-stopped, cached-and-orphaned exactly as described
   above.
2. If confirmed: fix `adoptReplacementSession` (or the call site immediately before it) to
   `.Stop()` whatever session it's about to overwrite, if any and if still live, before storing
   the replacement — mirroring what `observeSessionForRecovery`'s own Delete-then-notify ordering
   already achieves for path 1, but made safe for path 2 too. Consider whether the fix belongs in
   `adoptReplacementSession` itself (defensive, covers any future third call path) rather than in
   `notifyRecoveryBrokerForHTTPStreamError` specifically (narrower, only covers this one path) —
   your call, document the reasoning.
3. Re-check the double-notification/clobber race named above once the primary fix lands — confirm
   whether stopping the old session as part of the fix also naturally prevents its
   `observeSessionForRecovery` goroutine from firing a stale second notification (e.g. because
   `Stop()` causes a clean-exit `Wait()` return that the "clean exit — nothing for the broker to
   recover" branch already handles safely), or whether a second, explicit guard is needed.
4. Add a regression test reproducing the confirmed scenario from step 1, asserting the old
   session is stopped (not merely dropped) when a replacement is adopted via the
   mid-stream-error path specifically (not just the already-covered `observeSessionForRecovery`
   path).

## Done means

- Step 1's clean repro is documented in the Work Log, confirming or revising this task's own
  root-cause theory against real evidence (not task `07`'s messier one).
- If confirmed, a real fix lands: no live session is ever silently overwritten in
  `activeSessions` without being stopped first, regardless of which call path reached the broker.
- The double-notification/clobber risk is explicitly checked and its outcome documented, fixed if
  still present after the primary fix.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` clean.
- New regression test coverage for the mid-stream-error replacement-adoption path specifically.

## Work Log

**Step 1 — clean repro (confirmed the theory, against real evidence, before touching any code).**

Built `internal/service/chat_replacement_session_orphan_test.go`, exercising the REAL production
call chain end to end — no fakes stand in for the parts that matter:

- A real, live `*runtimeagent.Session` (`bootRealLongLivedSession`, via the real `runtimeagent.Boot`
  against the `codex` bootdir layout with a fake CLI adapter — the exact pattern
  `internal/runtime/agent`'s own `TestBoot_LongLived_HappyPath`/`TestSession_StopHappyPath` use;
  `ModeLongLived` + a subprocess-per-turn adapter with no `AutoFireFirstTurn` never actually forks a
  process at Boot time, so this is fast/deterministic while still giving a genuinely live
  `wr`/`runDone` — `Wait()` really blocks until `Stop()` or a crash closes it) is stored into a real
  `chatServiceImpl.activeSessions`.
- A real `*broker.Broker` (`internal/recovery/broker`), wired with a scripted `AgentBoot` fake that
  returns a fixed replacement session, and `SetReplacementSessionHook(svc.adoptReplacementSession)`.
- Exactly one call to `svc.notifyRecoveryBrokerForHTTPStreamError(...)` — path 2, the mid-stream
  chat-loop error notify — with **no process exit anywhere in the chain**: `oldSess.Wait()` is still
  genuinely blocked the entire time. `exit.Code=-1`, `Attempt=1` classifies `Transient` (the
  classifier's default "unclassified non-zero exit, first occurrence" rule), so the broker actually
  dispatches a CLI replacement (provider `"codex"` has a real bootdir `Layout`, so `DispatchRetry`
  takes the `AgentBoot.Boot` branch, not the HTTP-retry branch) and invokes the replacement hook.

Result **pre-fix**: `adoptReplacementSession`'s bare `activeSessions.Store` silently overwrote the
live old session; `oldSess.Wait()` with a 2s-bounded context returned `context.DeadlineExceeded` —
i.e. the old session was never stopped, orphaned exactly as the task's root-cause theory predicted.
This is now confirmed against a clean, single-variable repro, not task `07`'s messier dogfeed
observation. The theory is correct: **fix confirmed as needed**, not a false alarm.

One correction to the task's own framing worth logging: the task file frames path 2
(`notifyRecoveryBrokerForHTTPStreamError`) as if it only matters for genuinely HTTP-provider
sessions. Reading `chat_generate.go`'s stream-consuming loop shows this isn't quite right — the
SAME loop consumes `provCh` regardless of whether it came from `driveBootSession` (CLI/PTY path,
`prov == nil`) or a real HTTP `provider.StreamChat` call, and every one of its mid-stream `"error"`/
stall branches funnels through `persistPartialAssistantAndNotifyBroker(PreClassified)` →
`notifyRecoveryBrokerForHTTPStreamError` unconditionally. So path 2 is reachable for a CLI-driven,
`activeSessions`-tracked session too (e.g. a mid-turn EventError surfaced through the agent event
bridge, or a stream-inactivity stall) — not just literal HTTP-provider chat turns. This makes the
bug's blast radius the same CLI sessions `driveBootSession` manages, which is what the repro above
deliberately targets (provider `"codex"`, a real bootdir-backed CLI session) rather than a plain
HTTP provider.

**Fix (step 2).** `adoptReplacementSession` (`internal/service/chat_boot_drive.go`) now uses
`activeSessions.Swap` (Go 1.20+ `sync.Map` primitive) instead of a bare `Store`, atomically
capturing whatever was previously cached under `sessionID`. If a previous session existed and
differs from the incoming replacement, `stopDisplacedSession` cooperatively tears it down: flags
the exact session pointer in a new `displacedSessions sync.Map` (`chat.go`), then calls `.Stop()` on
`safego.Go` (async, bounded by the existing `stopRebootGrace` = 15s constant) so the broker's own
synchronous orchestration loop is never blocked on a cooperative SIGTERM-then-SIGKILL escalation.

Landed in `adoptReplacementSession` itself (not narrowly in `notifyRecoveryBrokerForHTTPStreamError`)
per the task's own framing of that tradeoff — this is the one seam every replacement-adoption call
path funnels through, so it's defensive against any future third caller reaching the broker without
its own pre-clear, not just today's two paths.

**Double-notification/clobber race (step 3) — checked, and a second explicit guard WAS needed.**
`Session.Stop()`'s own doc comment (and `observeSessionForRecovery`'s existing `rebootingSessions`
handling) already establishes that a cooperative `Stop()` can surface as a real
`*agentsessions.ExitError` (SIGTERM/SIGKILL), not a clean nil exit — so simply calling `.Stop()`
would NOT, on its own, make the displaced session's still-running `observeSessionForRecovery`
goroutine treat the resulting exit as anything other than a fresh crash. Without a guard, that
goroutine would route a second `OnSessionExit` to the broker and (via a bare `Delete`) risk
clobbering the replacement `adoptReplacementSession` just stored.

Considered reusing the existing `rebootingSessions` map (keyed by chat session id, used by
`RebootSessionAgent`) for this — rejected: for a short window, the OLD (being-stopped) and NEW
(just-adopted replacement) sessions are BOTH live under the same `sessionID`, each with its own
`observeSessionForRecovery` goroutine racing on its own `Wait()`. A flag keyed only by `sessionID`
can't tell which generation's exit it's meant for — a fast-crashing replacement could steal a flag
meant for the old session, incorrectly self-deleting from `activeSessions` and skipping its own
genuine-crash broker notification. Instead added `displacedSessions sync.Map`, keyed by the exact
`*runtimeagent.Session` **pointer** being stopped (see `chat.go`'s field doc for the full
reasoning). `observeSessionForRecovery` checks this first (before the pre-existing
`rebootingSessions` check): if the pointer it's watching was flagged, it `CompareAndDelete`s
`activeSessions` (a no-op once the slot holds the replacement — this is what actually closes the
clobber race), clears the per-session slot/tool-partition state, and returns WITHOUT calling the
broker. Deliberately does NOT call `broker.ClearSession(sessionID)` the way the `rebootingSessions`
branch does — the broker's per-session attempt-count classifier state legitimately belongs to the
recovery sequence that just dispatched the replacement, and clearing it here would silently reset
the hard-cap retry counter on every mid-stream-error-triggered replacement, undermining the
"escalate to permanent after N attempts" guard.

**Step 4 — regression tests**, in `chat_replacement_session_orphan_test.go`:
- `TestAdoptReplacementSession_MidStreamErrorPath_StopsDisplacedSession` — the step-1 repro itself,
  now asserting the post-fix outcome (`oldSess.Wait()` returns promptly, not
  `context.DeadlineExceeded`), plus the replacement is correctly live in `activeSessions`.
- `TestAdoptReplacementSession_ObserveSessionForRecoveryPath_NoDoubleNotify` — spawns a real second
  `observeSessionForRecovery` goroutine watching the old session (mirroring the goroutine
  `driveBootSession` would have spawned at its original boot), drives the same mid-stream-error
  notify, and asserts exactly one envelope is emitted by the broker (a second envelope would mean
  the old session's stop-induced exit was misrouted to the broker as a fresh crash) and that
  `activeSessions` still holds the replacement afterward (not clobbered).

Both replacement sessions in these tests are also REAL booted sessions (not bare
`&runtimeagent.Session{}` shells) — `adoptReplacementSession` re-arms a fresh
`observeSessionForRecovery` for whatever it adopts, and a nil-`wr` shell's `Wait()` returns
immediately as a "clean exit," which would make that re-armed observer itself bare-`Delete` the
replacement out of `activeSessions` — a test-fixture artifact unrelated to the race actually being
pinned, discovered and fixed while building the repro.

**Note on `-race` test cost (unrelated to this fix).** The two new tests use a real SQLite-backed
`store.New` (`newConfigTestStore`, the same helper `orphan_sweep_smoke_test.go` /
`agent_bootdir_adapter_test.go` already use) to get genuinely live sessions. Confirmed by direct
isolated measurement (`newConfigTestStore` alone, no Boot/wrapper involved) that a single real-store
construction costs several real seconds under `go test -race` (vs. sub-millisecond without `-race`)
— a pre-existing property of the store package's migration path under race instrumentation, not
something this fix introduces. `internal/runtime/agent`'s own much larger Boot-heavy test suite
stays fast under `-race` because it uses an in-memory fake store, not a real one. Refactored the two
new tests to share one real store per test (`bootRealDeps`) instead of one per booted session,
halving this cost; `go test ./internal/service/... -race -run 'TestAdoptReplacementSession_'` is
~16s (down from ~35s), `go test ./internal/service/...` (no `-race`) is ~1s for these two tests.
Noted here for transparency, not fixed further — it's outside this task's scope and doesn't affect
the required (non-`-race`) baseline.

**Verification.**
- `go build ./cmd/nanite/` — clean.
- `go vet ./...` — clean except four pre-existing findings in `internal/service/container.go`
  (`stopReaper`/`stopRuntimeReaper` possible-context-leak vet warnings), confirmed via `git log` to
  predate this task and untouched by this change.
- `go test ./...` — full suite green.
- `go test ./internal/service/... -race -run 'TestAdoptReplacementSession_'` — green (repeatable,
  ~16s per the note above).
- Both new tests re-run 3x each (`-count=3`) with and without `-race` — deterministic pass every
  time.

**Files touched:** `internal/service/chat.go` (new `displacedSessions` field),
`internal/service/chat_boot_drive.go` (`adoptReplacementSession` fix + new `stopDisplacedSession` +
`observeSessionForRecovery` guard), `internal/service/chat_replacement_session_orphan_test.go` (new
— clean repro + regression coverage).
