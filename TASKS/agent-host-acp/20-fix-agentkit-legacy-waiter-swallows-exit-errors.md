# Fix: agentkit's unsupervised streaming-stdio waiter never surfaces a real
`*agentsessions.ExitError` — `internal/recovery/broker`'s real-process-exit classification path
is dead code for every Nanite CLI session today

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`), found during task `07`'s
real-provider dogfeed validation — this is the direct, confirmed check on 16-agent-host.md's
own named top risk ("`internal/recovery/broker`'s existing agentkit-based classifier/dispatch
logic... needs auditing first").
**Status:** reviewed
**Depends on:** none.
**Touches:** `libs/agentkit/agentsessions/streaming_stdio_session.go` (`spawnWaiterLegacy`).
Sibling repo, NOT Nanite (nor `go-agent-wrapper` — this bug is one layer further down, in the
library `go-agent-wrapper` itself wraps, and it predates `go-agent-wrapper`'s existence). A
`go.mod` version bump on both `go-agent-wrapper` and Nanite's own direct `agentkit` import will
be needed afterward, mirroring task `01`'s precedent.

## Context

16-agent-host.md names, as the single largest named risk of adopting `go-agent-wrapper`: "the
activity bridge, and TurnID tracking are unvalidated against real session volume or against
`internal/recovery/broker`'s existing agentkit-based classifier/dispatch logic." Task `07`'s own
brief explicitly asks to "force a real session failure... and confirm
`internal/recovery/broker`'s classifier/remediator/dispatch chain still correctly classifies the
exit" — this is the direct result of doing exactly that.

**Finding: it does not.** A deliberate `kill -9` of a real, live, healthy `claude` CLI subprocess
(spawned through the fully-migrated `wrapper.Wrapper.Run` path, mid-`ModeLongLived` chat session)
produced **zero broker activity at all** — no classification log line, no remediation, no
replacement dispatch. The only observable effect was the `agent_runtime` row's `state` column
silently flipping to `done` with **no `failure_reason`**, as if the process had exited cleanly.

**Root cause, traced to the exact line, and confirmed against the exact code actually compiled
into Nanite's own build** (not just the working-tree checkout — `diff`'d byte-identical against
`$(go env GOMODCACHE)/github.com/hollis-labs/agentkit@v0.3.0`, the tagged version both Nanite's
own direct `agentkit` import and `go-agent-wrapper`'s replace-resolved local checkout share):

`agentkit/agentsessions/streaming_stdio_session.go`'s `spawnWaiterLegacy` (the waiter used
whenever `agentsessions.StartOptions.Supervisor == nil` — confirmed, per task `06`'s own Work
Log, that **no Nanite CLI session configures a Supervisor today**, so this is the *only* waiter
path any real Nanite CLI provider — Claude, Codex, or OpenCode — ever exercises):

```go
s.waitOnce.Do(func() {
    switch {
    case err == nil:
        s.waitCode.Store(0)
    default:
        var ee *exec.ExitError
        if errors.As(err, &ee) {
            s.waitCode.Store(int32(ee.ExitCode()))   // <-- code stored, waitErr NEVER set
        } else {
            s.waitCode.Store(-1)
            s.waitErr.Store(err)                      // <-- raw stdlib error, not *ExitError
        }
    }
    s.done <- err
    close(s.done)
})
```

and the exported `Wait()`:

```go
func (s *streamingStdioSession) Wait() (int, error) {
    <-s.done
    code := int(s.waitCode.Load())
    var err error
    if v := s.waitErr.Load(); v != nil {
        err, _ = v.(error)
    }
    return code, err
}
```

Go's `cmd.Wait()` returns a `*exec.ExitError` for **any** abnormal exit — a non-zero exit code
*and* a signal-based death (SIGKILL included) both take the `errors.As(err, &ee)` branch. That
branch stores only the numeric code and **never calls `s.waitErr.Store(...)`**, so `Wait()`
returns `(code, nil)` — a **nil error** — for the overwhelming majority of real-world abnormal
exits. The `else` branch (genuinely non-`*exec.ExitError`-shaped errors, e.g. a `wait4` syscall
failure) does store the raw error, but it's still a plain stdlib error, never an
`agentkit`-typed `*agentsessions.ExitError` — so it *also* fails to satisfy
`errors.As(err, &xe)` for any `*agentsessions.ExitError`-typed variable.

**Net effect: nowhere in `spawnWaiterLegacy` is an `*agentsessions.ExitError` ever constructed.**
`Session.Wait()` can return a nil error for a genuinely-killed process, or a raw, untyped error
for a narrower class of failures — but it can never return the one typed error shape
`internal/recovery/broker`'s entire real-process-exit classification path is built around
(`internal/service/chat_boot_drive.go`'s `observeSessionForRecovery`: `if !errors.As(err, &xe) {
// Clean exit — nothing for the broker to recover. ... return }`).

**Confirmed pre-existing, not caused by this migration.** Traced the pre-migration call chain
too: `agentsessions.Manager.WaitSession` → `Manager.watch` (`manager.go:292-322`) just calls
`sess.Wait()` directly and stores whatever it returns, with zero additional classification —
identical shape to `wrapper.Wrapper.Run`'s own handling (`wrapper.go:527-539`, which wraps
`session.Wait()`'s return with `%w` and nothing more). This bug lives entirely inside
`agentkit`, below both the pre-migration and post-migration Nanite call sites — it would have
produced the exact same silent-swallow behavior before this migration too. This is squarely why
16-agent-host.md called this path out as "unvalidated against real session volume" — no one had
actually killed a real provider process and watched the broker (not) react, until this dogfeed.

**Confirmed this is not narrowly scoped to Claude/streaming-stdio.** `runSupervised` (the
*supervised* waiter, used only when `Supervisor != nil`) *does* correctly construct
`&ExitError{...}` values in its own crash-handling paths — but since no Nanite session ever sets
`Supervisor`, that code is unreachable from Nanite today regardless of correctness. Whether
`jsonrpc_stdio_session.go` and `serve_http_session.go` (Codex/OpenCode's own runtime kinds) have
an equivalent legacy-waiter gap was not exhaustively checked as part of this finding — the
dispatched worker should check both before considering this fully closed, not assume the fix is
`streaming_stdio_session.go`-only.

## What to do

1. Fix `spawnWaiterLegacy` (and audit the equivalent unsupervised waiter, if a distinct one
   exists, in `jsonrpc_stdio_session.go` / `serve_http_session.go` — check whether they share this
   helper or have their own copy of the same bug) to construct a real `*agentsessions.ExitError`
   whenever `cmd.Wait()` returns *any* non-nil error — not just the non-`*exec.ExitError`-shaped
   ones. At minimum this needs:
   - `Code` populated from the real exit code (or `-1` for a signal death, matching
     `exec.ExitError.ExitCode()`'s own convention).
   - `Signal` populated when the process died by signal (available via
     `ee.ProcessState.Sys().(syscall.WaitStatus).Signal()` on Unix — check the existing
     `runSupervised`/other `agentkit` code for the idiomatic way this library already extracts a
     signal elsewhere, and reuse it rather than reinventing it).
   - `Cause` — decide what the correct value is for "process died, no supervisor configured, no
     other cause detected." The existing `Cause*` constants (`CauseIdleTimeout`,
     `CauseWatchdogKill`, `CauseOOMKill`, `CauseRestartExhausted`, `CauseResourceLimit`) are all
     supervisor-specific and none obviously fit an externally-caused or plain non-zero-exit
     death under the *unsupervised* waiter — this may need a new, more generic cause (e.g.
     `CauseProcessExit` or similar), or an empty/unset `Cause` may be acceptable if
     `internal/recovery/broker`'s classifier already has a sane default for "code != 0, no
     recognized cause" (task `06`'s own Context notes: "The classifier doesn't branch on these
     directly; they fall through to the default Code != 0 path" for Nanite's own synthesized
     `http_stream_*` causes — check whether the same default-path behavior is what's wanted here
     too before inventing a new constant unnecessarily).
2. Add a real, non-mocked regression test: spawn a real subprocess via the unsupervised waiter
   path, kill it externally (SIGKILL, matching this finding's own repro), and assert `Wait()`
   returns a non-nil `*agentsessions.ExitError` with the correct code/signal. A `cmd.Wait()`-level
   fake is not sufficient here — this bug is specifically about the `errors.As` branching logic
   dropping data that a fake could accidentally paper over; use a real `os/exec` child process
   (e.g. `sh -c 'kill -9 $$'` or spawn-then-external-kill, matching this task's own repro
   methodology) the way `wrapper_real_adapters_test.go` (task `05a`) already established the
   precedent for using real subprocesses over fakes when the fake would hide the exact class of
   bug being tested.
3. Once fixed and reviewed, bump `go-agent-wrapper`'s `agentkit` pin (should be a pure version
   bump per task `01`'s established precedent, but re-verify against the new tag, not assumed) and
   then Nanite's own direct `agentkit` import, and re-run this task's exact dogfeed repro (real
   `kill -9` on a real live claude session spawned via the running app) to confirm the broker
   *does* now classify and (per its own configured policy) dispatch a replacement.

## Done means

- `Session.Wait()` returns a real, correctly-populated `*agentsessions.ExitError` for a real,
  externally-killed process under the unsupervised waiter path, verified by a real-subprocess
  test (not a fake).
- The equivalent unsupervised-waiter code paths in `jsonrpc_stdio_session.go` /
  `serve_http_session.go` are either confirmed unaffected (with the evidence for that in the Work
  Log) or fixed identically.
- `go build ./...` / `go test ./...` clean in `agentkit`.
- Both `go-agent-wrapper` and Nanite bumped to the fixed `agentkit` version.
- Re-verified via a real dogfeed: killing a real live claude/codex/opencode subprocess through
  the running Nanite app now visibly reaches `internal/recovery/broker`'s classifier (a
  `"recovery: session exited with error — invoking broker"` log line or equivalent, not silence).

## Work Log

**Scope note (deviation from this file's own header/"What to do" step 3, per explicit
instruction from the launching agent, not a decision made unilaterally here):** this file's own
`Touches`/`Depends on` header and `What to do` step 3 describe bumping `go-agent-wrapper`'s and
Nanite's own `agentkit` pin, plus re-running the dogfeed repro, as part of this task. The
launching agent's brief explicitly narrowed that: land + tag the fix in `agentkit` only, then
stop — the Orchestrator is centralizing all downstream pin bumps and the final re-verification
dogfeed across this batch's findings to avoid concurrent `go.mod` edits across multiple in-flight
tasks. This Work Log covers the `agentkit`-repo work only; the `go-agent-wrapper`/Nanite pin
bumps and the "Done means" dogfeed-reverification bullet above are explicitly **not** done here
and are left for the Orchestrator's centralized pass.

**Root cause confirmed exactly as described in this file's Context section.** Read
`agentsessions/streaming_stdio_session.go` at `libs/agentkit` HEAD (`5b8aaad`, the commit named
in my brief) and confirmed `spawnWaiterLegacy`'s `errors.As(err, &ee)` branch stored only
`ee.ExitCode()` into `waitCode` and never called `s.waitErr.Store(...)` — so `Wait()` returned
`(code, nil)` for the overwhelming majority of abnormal exits (any signal death included, per
Go's own `cmd.Wait()` semantics: a signal-killed process's error also satisfies
`errors.As(err, &*exec.ExitError)`).

**Audited the two named files, plus one more the brief didn't name.** Per the brief's own
instruction to check, not assume, `jsonrpc_stdio_session.go`'s `spawnWaiterLegacy` and
`serve_http_session.go`'s `finishOnProcessExit` were read line-by-line: both are **independent,
copy-pasted duplicates of the identical broken switch statement** (not calls into
`streaming_stdio_session.go`'s implementation — each runtime kind's file has its own private
copy). Neither is "confirmed unaffected"; both carried the exact same bug and both are fixed
identically in this change. While auditing, I also found `pty_session.go`'s own
`spawnWaiterLegacy` carries the identical bug (this file predates the stdio runtimes per its own
doc comments, so it's a third independent occurrence, not inherited). This file wasn't named in
the brief's "What to do" list, but per this task's own guidance to do the full job when a
correction reveals wider scope, and given `libs/agentkit`'s PTY runtime is the pattern
`agent-mux`'s own runtime was lifted from (per `pty_session.go`'s doc comment) — i.e. a real,
separate consumer's own runtime kind — I fixed it too rather than leaving a known-identical bug
unfixed in the same release. Nanite itself doesn't use the PTY runtime (no real pseudo-terminal
in Nanite's current runtime, per this repo's own Glossary), so this doesn't change anything for
Nanite specifically, but it does matter for `agentkit`'s other real consumers.

**The fix.** All four unsupervised waiters (`streaming_stdio_session.go`, `jsonrpc_stdio_session.
go`, `pty_session.go`, `serve_http_session.go`) now call the *same* `buildExitError(ps, waitErr,
cause)` helper (`supervision.go`) the supervised path (`runSupervised`/`waitOnceSupervised`) was
already using — this is exactly the "idiomatic way this library already extracts a signal
elsewhere" the brief pointed at, so nothing new was invented for `Code`/`Signal`/`Killed`
extraction.

**`Cause` decision: left empty (no new constant).** Per the brief's own steer, I checked whether
an empty `Cause` already has a sane default-path meaning for the classifier before inventing a
new constant. `manager.go`'s own pre-existing `WaitSession` godoc already documented this
exact convention for the supervised path: "Cause is empty for ordinary non-zero exits or
Stop/ctx-cancel under supervision (the supervisor did not trigger the termination...)." An
unsupervised exit with no `Supervisor` attached is squarely the same case — nothing about
`internal/recovery/broker`'s classification needs to change on the Nanite side; the `*ExitError`
itself being non-nil (vs. nil, as it was before this fix) is what unblocks the classifier, not a
new `Cause` value. I did not add `CauseProcessExit` or similar. I updated `manager.go`'s
`WaitSession` godoc (previously described the *pre-fix* buggy contract almost verbatim — "The
underlying wait error (typically `*exec.ExitError`) for non-zero exits on non-supervised
sessions" — which is no longer accurate) to describe the corrected, uniform contract and to
explicitly flag the behavioral change in the doc comment itself, not just the CHANGELOG.

**Second, independent bug found and fixed in `serve_http_session.go` (in scope, not
speculative):** while writing the real-subprocess regression test for this runtime kind, I found
`Start()` spawned its `finishOnProcessExit` waiter goroutine **twice** — once immediately after
`spawn()`, once again after `waitHealthy`+`createSession` succeeded. Both goroutines raced to
receive the single value off the buffered, close-once `processDone` channel; the
later-started goroutine would, in this environment, consistently receive the channel's
post-close zero value (nil) instead and win the `sync.Once` race, silently discarding the real
`*ExitError` regardless of the switch-statement fix above — reproduced this concretely
(5-for-5 failures) before the fix, 5-for-5 clean (`-race`) after removing the redundant second
`go finishOnProcessExit()` call. This is squarely the same "equivalent unsupervised waiter" code
path the brief asked me to audit for `serve_http_session.go`, and without this second fix the
primary fix would be unreliable (~50%) specifically for this runtime kind, so it's included as
part of the same change rather than filed separately.

**Real-subprocess regression tests (not fakes), one per runtime kind, in the existing
`agentsessions` in-package test style:**
- `TestStreamingStdioSession_ExternalSigkill_SurfacesExitError`
  (`streaming_stdio_session_test.go`)
- `TestJsonRpcStdioSession_ExternalSigkill_SurfacesExitError` (`jsonrpc_stdio_session_test.go`)
- `TestPTYRuntime_ExternalSigkill_SurfacesExitError` (`pty_session_test.go`)
- `TestServeHTTPRuntime_ExternalSigkill_SurfacesExitError` (`serve_http_session_test.go`)

Each spawns a real child process through the real runtime's `Start()` (no `Supervisor` set, so
the unsupervised legacy waiter path is exercised — the exact path this finding is about), reads
the real PID via `PIDReporter.LivePID()`, sends a real `syscall.Kill(pid, syscall.SIGKILL)`
directly against that PID (matching the finding's own `kill -9` repro against a real, live
process — not a `cmd.Wait()`-level fake, which would have hidden this exact class of bug since
the bug is specifically in how a real `*exec.ExitError` from a real killed child gets translated
by the `errors.As` branching), then calls `Wait()` and asserts: `waitErr != nil`,
`errors.As(waitErr, &xe)` succeeds, `xe.Signal == syscall.SIGKILL`, `xe.Killed == true`,
`xe.Code == -1`, `xe.Cause == ""`, and `code == -1`.

All four passed on first correct run and were re-run 3x with `-race`, 5x for the
`serve_http_session.go` case specifically (to confirm the double-spawn race fix), all clean.

**Build/test status (in `libs/agentkit`, at commit `48df61c`):**
- `go build ./...` — clean.
- `go vet ./...` — clean.
- `gofmt -l` on every touched file — clean (one unrelated, pre-existing gofmt-dirty file,
  `agentlaunch/bootassembly_test.go`, untouched by this change, left as-is).
- `go test -race -count=3 ./agentsessions/...` — clean, including the 4 new tests.
- `go test -race -count=1 ./...` (all 27 packages except `agentlaunch/parity`) — clean.
- `agentlaunch/parity.TestParity_LiveCatalog` — **fails, but confirmed pre-existing and unrelated
  to this change**: it diffs a live catalog at `~/.tether/catalog` against test fixtures and hits
  an `UNEXPLAINED` `work_dir` diff for `hollislabs-web-writer-claude` (looks like a local
  directory rename on this machine, `hollis-labs.com` → `hollislabs-web`, that the fixture/live
  catalog haven't caught up with). Verified via a throwaway `git worktree` at the unmodified
  `5b8aaad` base commit: the exact same failure reproduces there, confirming it predates and is
  unrelated to this change. No files under `agentlaunch/` were touched by this fix.

**Versioning.** Bumped `v0.3.0` → **`v0.4.0`** (minor, not patch) — no exported symbol was
added/removed/renamed, but per this task's own instruction to be deliberate given this is a real
behavioral change for 6 real consumers, a MINOR bump signals "check your assumptions" more
strongly than a PATCH would for a change that alters what error value existing correct-looking
call sites receive. `CHANGELOG.md` v0.4.0 entry explicitly leads with "BEHAVIORAL CHANGE, not
just a bug fix — read before bumping your pin" and spells out exactly what downstream code
(`err == nil` treated as "clean exit, nothing to do") needs re-checking. Annotated tag `v0.4.0`
created and pushed to `origin` (`github.com/hollis-labs/agentkit`), pointing at commit
`48df61c8bffb22e794f72949e759b30e53e9d480` — pushed (along with `main`) because the Orchestrator's
centralized downstream pin-bump pass needs the tag to actually resolve via `go get`.

**Commit:** `48df61c8bffb22e794f72949e759b30e53e9d480` on `libs/agentkit`'s `main`
(`fix(agentsessions): unsupervised legacy waiter never surfaced a real *ExitError`), pushed to
`origin/main`.

**What's explicitly NOT done here (left for the Orchestrator, per the launching agent's
narrowed scope):** `go-agent-wrapper`'s `agentkit` pin, Nanite's own direct `agentkit` import,
and the "Done means" dogfeed re-verification bullet (killing a real live subprocess through the
running Nanite app and confirming the broker now classifies it) all remain outstanding — none of
that was done as part of this task.

## Review notes

Orchestrator-verified (2026-08-21): independently confirmed the diff, re-ran build/vet/test,
and re-confirmed live against the final, fully-patched build during the section-level Phase 2
re-verification dogfeed. Full verification record: `TASKS/ESCALATIONS.md`'s 2026-08-21 entries
for this task, and the whole-section fresh review (also 2026-08-21, PASS) that closed Phase 2.
