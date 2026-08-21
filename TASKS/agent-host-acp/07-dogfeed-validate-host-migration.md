# Dogfeed-validate the full host migration

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`)
**Status:** implemented — real dogfeed run in full against all three real CLI binaries; found
**four real, previously-unvalidated bugs** (three of them 100%-reproducible hard failures that
block real usage of the migrated path for two of the three providers, plus a real gap in
`internal/recovery/broker`'s own named top risk), each written up as its own new task file
(`18`-`21`) per the fix-as-new-worker-task discipline. **Not marking Phase 2 `validated` in
`TASKS/INDEX.md`** (that edit is the Orchestrator's, not this task's, per this task's own
instructions) — given the findings below, that section should stay `implemented`/under-repair
until `18`-`21` land and this dogfeed is re-run clean. See Work Log for the full run, evidence,
and findings.
**Depends on:** `04`, `05`, `06`
**Touches:** none (validation-only task — no code changes expected unless it finds a real
bug, in which case follow the fix-as-new-worker-task discipline per `EXECUTION-PROCESS.md`
rather than patching inline). Repo: Nanite.

## Context

16-agent-host.md names "zero production mileage" as a real, named risk of adopting
go-agent-wrapper: "`Wrapper.Run`'s dispatch path, the activity bridge, and TurnID tracking are
unvalidated against real session volume or against `internal/recovery/broker`'s existing
agentkit-based classifier/dispatch logic." Tasks `04`-`06` migrate the code; this task is the
Orchestrator-level "actually exercise the feature" checkpoint `EXECUTION-PROCESS.md`'s
Validation checkpoints section requires before a section can be marked `reviewed` — this
batch is the **first real production exercise of `go-agent-wrapper` anywhere in the
portfolio** (confirmed zero adopters at planning time), so this checkpoint carries more
weight than the same step does in most other batches.

Per `docs/engineering/standards/testing.md`'s "dogfeed it, don't just trust a green test
suite" discipline (the same discipline every other batch in `TASKS/` has followed at its own
Phase-boundary validation step).

## What to do

1. Launch a real session for each of Claude, Codex, and OpenCode through the migrated
   `wrapper.Wrapper`-based path (task `06`) — a real subprocess, not a mock — and confirm: the
   boot directory is planted correctly (task `04`'s migration — diff the actual planted files
   against pre-migration output for at least one provider), the sandbox profile applies
   correctly (task `05`'s migration), a real turn completes and activity/streaming events
   reach the chat surface correctly, and `Session.Stop()` cleanly terminates the process
   (confirming the carried-forward SIGTERM/SIGKILL-only behavior still works, per task `06`'s
   Context — not attempting to prove mid-turn interrupt, which doesn't exist).
2. Force a real session failure (e.g. an idle timeout, or a deliberate process kill) and
   confirm `internal/recovery/broker`'s classifier/remediator/dispatch chain still correctly
   classifies the exit and (if configured to) dispatches a replacement session — this is the
   direct check on 16-agent-host.md's named broker risk.
3. Confirm no regression against pre-migration behavior for anything not in scope for this
   batch — a full `go test ./...` pass plus at least one full chat session exercised through
   the running app (not just an isolated unit test), matching this project's own
   `cerberus_resource_deploy`/`cerberus_resource_reload` real-deployment verification pattern
   used by other batches' Validation checkpoints.
4. If this surfaces a real bug: do not patch it inline. Write it up as a new task file (fix-
   as-new-worker-task discipline, matching every other batch's precedent — e.g.
   `TASKS/phase-4/09`, `TASKS/reflex-taxonomy/08`/`09`) and dispatch it before continuing to
   Phase 3.

## Done means

- Real (not mocked) end-to-end session launch/activity/stop verified for all three providers.
- Real forced-failure classification/remediation verified against `internal/recovery/broker`.
- Full `go test ./...` clean; no unexplained regression against pre-migration behavior.
- Any real bug found is written up as its own task file, not silently patched.
- This section (Phase 2) is marked `validated` in `TASKS/INDEX.md` once this passes, ready for
  a fresh Reviewer dispatch before Phase 3 starts.

## Work Log (2026-08-21)

Read this task file in full, `docs/engineering/architecture/16-agent-host.md`, this batch's
`README.md`, `EXECUTION-PROCESS.md`, `docs/engineering/standards/testing.md`,
`TASKS/ESCALATIONS.md`'s task-05/06 entries, `TASKS/INDEX.md`'s agent-host-acp section, and
tasks `04`/`05`/`05a`/`06`'s full Work Logs (including the pre-migration boot-dir diff
methodology `04` already ran) before touching anything, per this task's own weight.

### Safety setup

Confirmed all three CLI binaries resolve (`claude 2.1.238`, `codex-cli 0.147.0`, `opencode
1.15.6`). Built a scratch binary (`go build -o
/private/tmp/.../scratchpad/nanite-dogfeed ./cmd/nanite/`) and ran it **from a scratch CWD**
(`.../scratchpad/dogfeed-cwd`) with `XDG_DATA_HOME`/`XDG_STATE_HOME`/`XDG_CONFIG_HOME`/
`XDG_CACHE_HOME` all redirected to scratch subdirectories and an explicit `-db` pointed at a
scratch SQLite file — per this task's process notes, never touching any real tracked path. Left
`$HOME` as the real environment's `$HOME` deliberately (after an initial attempt with a scratch
`$HOME` broke real CLI auth — see below): boot directories are always `os.MkdirTemp("",...)`-
rooted (`bootdir_common.go`'s `makeBootDir`, confirmed unrelated to `$HOME`/CWD), Nanite's own
app-level state is fully redirected via the XDG vars + explicit `-db`, and `ManagedConfigRoot`
defaults to `filepath.Join(workingDir, ".nanite")` off the process's real CWD (confirmed
directly in `internal/service/container.go`) — so running from a scratch CWD is the actual load-
bearing isolation, independent of `$HOME`. Verified this empirically before trusting it: created
three managed agents via the real `POST /api/agents` HTTP API and confirmed via `find` that their
`.nanite/agents/*.md` files landed only under the scratch CWD, and via `git status --short` in
this repo that nothing changed. Ran `git status --short` repeatedly throughout the session, not
just at the end — confirmed clean (zero diff caused by this work) at every check. One unrelated,
pre-existing untracked file, `docs/engineering/architecture/19-api-cli-runtime-parity.md`,
appeared in `git status` partway through this session — **not created by this task** (this task
never wrote to `docs/` at any point); left untouched, evidently a concurrent, unrelated session
working in the same checkout.

Seeded three managed (DB+file-backed) test agents via the real HTTP API
(`dogfeed-claude`/`dogfeed-codex`/`dogfeed-opencode`), each pointed at a real CLI provider via a
direct, scoped `sqlite3` UPDATE on the scratch DB's own `agent_profiles.default_provider`/
`runtime_kind` columns (`pty`/`pty-codex`/`pty-opencode`, `cli`) — confirmed this round-trips
correctly through `GET /api/agents/{id}` (i.e. the Phase-0-era "file-discovered agent silently
drops DB-only columns" gap, already fixed elsewhere, is not a live concern here). Seeded
`providers.default_model` rows for each (`sonnet`/`gpt-5-codex`/`opencode/grok-code`) — none
existed pre-seeded for `pty`/`pty-opencode`, and `pty-codex`'s existing row had no default model,
both a one-time scratch-DB setup step, not a product finding.

### Step 1 — real session launch/activity/stop, all three providers, via the real running app

Used the real running scratch server's `/api/harness/v1/sessions` + `/api/harness/v1/sessions/
{id}/turns` + `/api/harness/v1/sessions/{id}/events` (SSE) endpoints — the actual production
HTTP surface, not a direct internal-package test — for every provider, satisfying both this
task's step 1 (activity/streaming reaching "the chat surface") and step 3 ("at least one full
chat session exercised through the running app").

**Claude — full success.** Real `claude -p --input-format stream-json --output-format
stream-json --verbose` subprocess, spawned through the fully-migrated `wrapper.Wrapper.Run`
path. Inspected the real planted boot dir on disk
(`/var/folders/.../nanite-boot-claude-<sessionID>-.../`) and confirmed it matches exactly the
shape task `04`'s own (already-reviewed) pre/post-migration diff established: `CLAUDE.md`,
`.claude/settings.json` (`{"permissions":{"defaultMode":"acceptEdits"}}`), `.mcp.json` (real,
correctly pointing back at this scratch binary + scratch DB + this session's ID), `.sandbox/
agent-context.md`, `.sandbox/envelope-schema.md`, `boot.md`, plus task `06`'s new
`.wrapper-exec/claude.sh` (the `Env` workaround — inspected its content directly: a real
`exec env -i HOME=... PATH=... TMPDIR=... /Users/chrispian/.local/bin/claude "$@"` script,
confirming task `06`'s own described mechanism is real, not just described). Sent a real turn
("What is 17 + 25?") end-to-end: SSE stream carried a real `stream_start` → `delta` (`"42"`) →
`stream_end` (real token usage) sequence — a correct answer from a real Claude subprocess,
confirming activity/streaming reaches the chat surface correctly for the fully-migrated path.
Separately confirmed **`Session.Stop()` cleanly terminates the process** via the real, explicit
Stop() code path (not an external kill): booted a fresh Claude session, completed one real turn,
captured the live subprocess PID via `ps`, called `POST /api/sessions/{id}/agent/reboot` (which
calls `RebootSessionAgent` → `Session.Stop()`), and confirmed both that the real PID no longer
exists afterward and that the boot dir was cleaned up from disk — a clean, real, positive
confirmation of task `06`'s carried-forward SIGTERM/SIGKILL-only Stop() behavior.

**Codex — boot dir/sandbox correct, but every real turn hard-fails (see finding C below).**
Inspected the real planted boot dir: `AGENTS.md`, `.mcp.json`, `config.toml`
(`approval_policy = "never"`, `sandbox_mode = "workspace-write"` — the sandbox profile
correctly applied), `auth.json` (real, correctly-populated OAuth tokens, 0600 mode, confirming
task `04`'s codex-specific two-file provider-settings handling is real), plus
`.wrapper-exec/codex.sh` (correctly sets `CODEX_HOME` to the boot dir per task `06`'s `Env`
workaround). A real turn against the real `codex` binary failed with `runner: process exited 1`
both on the initial attempt and on the broker's own automatic retry — root-caused (see task
`19`) to a missing `--skip-git-repo-check` flag in `go-providers`' `CodexAdapter.BuildArgs`, a
pre-existing gap in a different sibling repo, not this migration's own code.

**OpenCode — boot dir correct, but every real turn hard-fails before ever spawning a process**
(see finding D below, the most severe finding of this dogfeed). Inspected the real planted boot
dir before the failure: `agents/dogfeed-opencode.md`, `opencode.json`, `agents.json`, `.mcp.json`
all present and well-formed. The real turn never got as far as spawning `opencode` at all —
`wrapper.Wrapper.Run` itself hard-errors with `wrapper: Config.Workdir is required` before any
subprocess is spawned. Root-caused to a genuine migration-introduced regression (task `18`):
`opencodeLayout.SpawnWorkdir` returns `opts.Workdir` verbatim (a pre-existing, documented,
always-empty stub — `bootSessionWorkdir` — for chat sessions), which pre-migration silently fell
back to the daemon's own cwd (a separate, real, pre-existing "wrong cwd" bug in its own right,
not previously known to hard-fail), but post-migration hits `wrapper.Wrapper.Run`'s own new,
hard `Config.Workdir == ""` validation and crashes outright, 100% of the time, for every real
OpenCode CLI session.

### Step 2 — forced real session failure vs. `internal/recovery/broker`

This is where the dogfeed's most architecturally significant finding came from (task `20`). Two
distinct forced-failure scenarios were exercised against real, live subprocesses:

1. **Mid-turn provider-level error** (an authentication failure surfaced mid-stream from a real
   claude subprocess, encountered organically while iterating on the scratch-server env setup):
   the broker *did* engage — `"recovery: http chat stream error — invoking broker"` →
   `"recovery: terminal exit observed"` → a retry dispatch attempt, all logged. This path
   (`notifyRecoveryBrokerForHTTPStreamError`) is chat-generation-loop-driven, independent of
   whether the underlying process is actually still alive.
2. **A deliberate, real process kill** (`kill -9` on a live, healthy claude subprocess spawned
   through the fully-migrated path) — exactly what this task's own step 2 asks for. **The broker
   did not engage at all.** No classification log line, no remediation, no replacement
   dispatch — the `agent_runtime` row silently flipped to `state='done'` with **no
   `failure_reason`**, as if the process had exited cleanly. Traced this to the exact line
   (`agentkit/agentsessions/streaming_stdio_session.go`'s `spawnWaiterLegacy`, the only waiter
   path any real Nanite CLI session exercises, since no session configures a `Supervisor`) and
   confirmed the traced source is byte-identical to the exact `agentkit@v0.3.0` module-cache copy
   Nanite's own build actually compiles (`diff` clean) — not a theoretical read, the real
   compiled behavior. Full detail, including why this is confirmed pre-existing in `agentkit`
   itself (not caused by this migration) and a candidate fix shape, is in task `20`.

Net: **task step 2's own explicit ask — "confirm the classifier/remediator/dispatch chain still
correctly classifies the exit" for a forced real failure — does not hold** for a genuine process
kill, the scenario the task itself names as an example forcing mechanism. It does hold for the
chat-layer-synthesized error path. This is the direct, confirmed check on 16-agent-host.md's own
named top risk, and it found the risk was real.

While investigating scenario 2's context, also found (task `21`, lower confidence — see that
task's own caveat) a real, live claude subprocess (+ its MCP sidecar) still running 9+ minutes
after its `agent_runtime` row was marked `done` following a broker-dispatched replacement —
traced to a candidate root cause (`adoptReplacementSession` overwriting `activeSessions` without
stopping what it replaces, an invariant gap between two call paths that reach the same broker
entrypoint) but not independently re-isolated with a clean, single-variable repro; documented as
such in task `21` with an explicit "verify first" instruction for its own worker. Killed both
leaked PIDs by hand after confirming them (`ps` showed genuinely live, scheduled processes, not
zombies) — no lasting resource impact from this dogfeed run itself.

### Step 3 — regression / full-app verification

`go build ./cmd/nanite/`: clean. `go vet ./...`: clean except the same two pre-existing,
unrelated `internal/service/container.go` findings every other task in this batch has already
noted (confirmed via `git log` that file predates this batch entirely). `go test ./...`
(fresh, `-count=1`, run from this actual working tree, not the scratch server): clean across
every package, no regression. At least one full chat session was exercised through the real
running app end-to-end (the Claude session above, and the explicit Stop()-via-reboot session) —
satisfies this task's "not just an isolated unit test" bar. Did not additionally exercise the
real Cerberus-managed `nanite-api-service` — per this task's own process notes, the scratch-
server approach was judged sufficient for the bulk of verification, and given the volume and
severity of real findings from the scratch run, a real Cerberus deploy/reload was judged not to
add meaningful additional confidence before those findings are fixed (would just reproduce the
same Codex/OpenCode failures against the real service). Recommend a real Cerberus
deploy/reload dogfeed as part of `18`/`19`'s own re-verification once those fixes land, matching
this project's standard real-deployment verification pattern.

### Cleanup

Shut the scratch server down cleanly (`kill -TERM`) and confirmed a graceful shutdown log
sequence. Confirmed via `ps` that no scratch-related `claude -p`/`codex`/`opencode`/
`nanite-dogfeed mcp` processes remained afterward. Final `git status --short`: clean except the
one pre-existing, not-mine `docs/engineering/architecture/19-api-cli-runtime-parity.md` noted
above.

### Findings summary — four real bugs, all written up as new task files (fix-as-new-worker-task
discipline, not patched inline)

- **`TASKS/agent-host-acp/18-fix-opencode-workdir-hard-fail-regression.md`** — highest severity:
  a genuine migration-caused regression, 100% blocking every real OpenCode CLI session.
- **`TASKS/agent-host-acp/19-fix-codex-missing-skip-git-repo-check.md`** — 100% blocking every
  real Codex CLI session; a pre-existing `go-providers` gap, not migration-caused, but a hard
  functional break this dogfeed is the first thing to have actually caught.
- **`TASKS/agent-host-acp/20-fix-agentkit-legacy-waiter-swallows-exit-errors.md`** — the direct,
  confirmed hit on 16-agent-host.md's own named top risk: `internal/recovery/broker`'s
  real-process-exit classification path is dead code for every real Nanite CLI session today, a
  pre-existing `agentkit` gap.
- **`TASKS/agent-host-acp/21-fix-broker-replacement-session-orphans-old-process.md`** — lower
  confidence, needs its own clean repro first: a candidate real leak in the broker's
  replacement-session adoption path for the mid-stream-error (non-process-exit) trigger.

### Verdict against this task's own Done-means, stated plainly

- "Real end-to-end session launch/activity/stop verified for all three providers" — **Claude:
  yes, fully.** Codex/OpenCode: boot dir + sandbox verified real and correct, but the actual
  turn does not complete for either (two separate, real, blocking bugs, neither one this task
  patched inline).
- "Real forced-failure classification/remediation verified against `internal/recovery/broker`"
  — **partially**: the chat-layer-synthesized path works; the real-process-exit path (the
  scenario the task itself suggests) does not.
- "Full `go test ./...` clean" — yes.
- "Any real bug found is written up as its own task file" — yes, four.
- Given the above, **this dogfeed run itself is complete and its own job is done**, but the
  *subject* of the validation (the Phase 2 host migration) is not clean — real, blocking,
  previously-unvalidated gaps exist. Not claiming Phase 2 `validated` in `TASKS/INDEX.md`
  (an edit outside this task's own authorization regardless); reporting this verdict to the
  Orchestrator for that decision.

## Work Log addendum (2026-08-21, re-run) — re-verification against the fully-patched build

This is a **second, independent dogfeed run**, dispatched after tasks `18`-`22` all landed and
after the Orchestrator's own discovery (and fix, commit `83100a50`) that Go `replace` directives
are not transitive — every sibling-repo fix had verified clean in isolation, but Nanite's own
compiled binary was still silently running the old, buggy `agentkit v0.3.0` until that commit
bumped `go.mod` to pull `agentkit v0.5.0`/`go-providers v0.24.0`/`go-agent-wrapper v0.4.0` with
zero local `replace` directives for any of the three. This addendum documents the real,
binary-level re-verification that those fixes actually take effect — not a repeat of the original
run's methodology description (see above for the full scratch-server recipe rationale; this
addendum assumes it and just applies it), just its results.

**Appended, not rewriting, the original Work Log above** — the original run's own findings and
verdict stand as the historical record of what was found and why. This addendum reports what
changed since.

### Pre-flight: dependency versions and baseline git status

Confirmed via `go list -m` (from this repo's own `go.mod`, no `replace` line for any of the
three): `github.com/hollis-labs/agentkit v0.5.0`, `github.com/hollis-labs/go-providers v0.24.0`,
`github.com/hollis-labs/go-agent-wrapper v0.4.0` — exactly the versions the dispatch expected,
with no `=>` annotation. Confirmed HEAD is `83100a50f2093b13e4455223107f4cc2a6b219dd` and `git
status --short` baseline before touching anything: five pre-existing modified/untracked files
(`TASKS/agent-host-acp/22-...md`, `docs/engineering/GLOSSARY.md`,
`docs/engineering/architecture/00-overview.md`/`16-agent-host.md`/`17-acp.md`, plus untracked
`docs/engineering/architecture/19-api-cli-runtime-parity.md` and `docs/launch-site/`) — none of
these were touched by this run at any point; per the original run's own precedent (its Safety
setup section noted an identical kind of pre-existing, not-mine untracked file appearing
mid-session), treated as a concurrent, unrelated session's in-progress work in the same checkout
and left alone throughout. `git status --short` was re-checked repeatedly (after the build, after
each provider's turn, after the broker kill test, after shutdown, and finally after the full test
run) and was byte-identical to this baseline at every check.

Built a fresh scratch binary (`go build -o .../scratchpad/nanite-dogfeed07b ./cmd/nanite/`) —
clean, confirming the new dependency versions compile in. Ran it from a fresh scratch CWD
(`.../scratchpad/dogfeed07b-cwd`) on port 8099, with `XDG_DATA_HOME`/`XDG_STATE_HOME`/
`XDG_CONFIG_HOME`/`XDG_CACHE_HOME` redirected to scratch subdirectories, an explicit `-db` pointed
at a scratch SQLite file, and real `$HOME` left alone for CLI auth — the identical recipe the
original run above established and verified safe. Seeded three fresh managed test agents
(`dogfeed07b-claude`/`-codex`/`-opencode`) via the real `POST /api/agents` HTTP API, then set each
one's `default_provider`/`runtime_kind` (`pty`/`pty-codex`/`pty-opencode`, `cli`) via a scoped
`sqlite3 UPDATE` on the scratch DB, confirmed to round-trip via `GET /api/agents/{id}`. Seeded/
patched `providers.default_model` rows for `pty`/`pty-codex`/`pty-opencode`
(`sonnet`/`gpt-5-codex`/`opencode/grok-code`) — `pty-opencode` had no row at all in this fresh
scratch DB and needed a full `INSERT`, matching the original run's own one-time setup note that
this is scratch-DB bootstrapping, not a product finding.

### Check 1 — Claude: real turn + `Session.Stop()`, no regression

Created a harness session (`provider=pty`, `model=sonnet`), sent a real turn ("What is 17 + 25?
Reply with only the number.") via `POST .../turns` and read the real SSE stream via `GET
.../events`: `tool_warning` → `stream_start` → `delta` (`"42"`) → `stream_end` (real usage) — a
correct answer from a real, live `claude -p --input-format stream-json --output-format
stream-json --verbose` subprocess. Confirmed the assistant message persisted non-empty in the
scratch DB (`{"v":1,"text":"42",...}`).

Captured the live PID via `ps` (`22748`), then called `POST /api/sessions/{id}/agent/reboot`
(→ `RebootSessionAgent` → `Session.Stop()`) and confirmed both that PID `22748` no longer exists
afterward and that the server log shows the clean sequence `"recovery: session exited via
intentional reboot — skipping broker"` → `"reboot: session agent stopped; next turn will
cold-boot"`. **No regression** — identical behavior to the original run's own Step 1 finding for
Claude.

### Check 2 — Codex (task `19`): real turn now completes end-to-end

Created a harness session (`provider=pty-codex`, `model=gpt-5-codex`), sent a real turn ("What is
2+2? Reply with only the number.") against the real `codex-cli 0.147.0` binary. SSE stream:
`tool_warning` → `stream_start` → `delta` (`"4"`) → `stream_end` (real usage), completing in
~5s. The server log shows a clean `request_build` → `chat-loop-diag: provider stream closed`
sequence with **zero** occurrences of `"not inside a trusted directory"` or `"process exited 1"`
— the exact 100%-reproducible failure the original run documented is gone. Assistant message
persisted non-empty in the scratch DB (`{"v":1,"text":"4",...}`). **Task `19`'s fix confirmed
working live**, against the real binary, through the real production HTTP surface.

### Check 3 — OpenCode (tasks `18` + `22` together): real turn now completes end-to-end, for the first time

Created a harness session (`provider=pty-opencode`, `model=opencode/grok-code`), sent a real turn
("What is 3+3? Reply with only the number.") against the real `opencode 1.15.6` binary. SSE
stream: `tool_warning` → `stream_start` → `delta` (`"6\n"`) → `stream_end`, completing in ~7s —
**no `Config.Workdir is required` crash** (task `18`'s fix) **and** the stream actually advances
past `stream_start` to a real `delta`/`stream_end` (task `22`'s fix) rather than hanging forever,
which is exactly what neither task `07`'s original run nor task `18`'s own re-verification (which
stopped at confirming the crash was gone, and explicitly deferred this exact end-to-end check to
task `22`/the Orchestrator's centralized pass) had ever exercised together against a real
`opencode` binary before this run. Confirmed in the scratch DB: `agent_runtime.workdir` for this
session is the real boot dir (not empty) — task `18`'s fix genuinely exercised, not just not-
crashing by accident — and the assistant message persisted non-empty
(`{"v":1,"text":"6\n",...}`). Confirmed via `ps` before and after that no `opencode` process was
left running once the turn completed (subprocess-per-turn shape working as designed). **Both
fixes confirmed working together, live, for the first time.**

### Check 4 — Broker real-process-kill classification (task `20`): the single most important check, now confirmed working

Repeated the original run's exact forced-failure scenario. Booted a **second**, fresh Claude
session, sent one real turn to bring up a live subprocess, confirmed via `ps` it stayed alive
after the turn completed (PID `23244`), then ran `kill -9 23244` externally (not via any Nanite
API) against that real, live, healthy process. Within ~3 seconds the server log showed:

```
level=WARN msg="recovery: session exited with error — invoking broker" session_id=2f00d187-... cause="" code=-1 signal=9
level=INFO msg="recovery: terminal exit observed" session_id=2f00d187-... attempt=1 cause="" code=-1 signal=9
```

— the exact classification log line shape the original run's task-`20`-adjacent finding named as
what *should* happen but previously never did for a real process kill (only the chat-layer-
synthesized `http_stream` path produced this before). The scratch DB's `agent_runtime` row for
that session went to `state='running'`, `failure_reason='broker retry attempt 1'` — **not**
silently flipping to `state='done'` with no `failure_reason`, which is exactly what the original
run documented as the broken pre-fix behavior. Confirmed via `ps` a **new** live `claude`
subprocess (PID `23299`, a different PID than the killed one) was spawned — the broker's
dispatched replacement session, not just a classification log line with no real effect. Sent a
second real turn ("Say the word recovered and nothing else.") to that same session ID and got a
real, correct response (`"recovered"`) back through a full `stream_start`/`delta`/`stream_end`
sequence — proving the replacement session isn't just alive but genuinely usable end to end.
Cleanly stopped it afterward via `POST .../agent/reboot`; confirmed no leftover process via `ps`.

**This is the direct, positive, live confirmation task `20`'s fix works** — the single check this
re-run's dispatch explicitly named as most important, and it now holds.

### Check 5 — Broker replacement-session leak (task `21`): lighter-touch confirmation, per this task's own allowance

Judgment call, documented per this task's own "your judgment call, document what you did either
way" instruction: reproducing task `21`'s own specific trigger sequence live (a mid-stream
provider-level error reaching `notifyRecoveryBrokerForHTTPStreamError` without an underlying
process exit) is not something the real HTTP API surface can force cleanly on demand — the
original run itself only hit it by accident, via test-session messiness the original Work Log
explicitly flagged as not independently re-isolated. Given that (a) task `21`'s own Work Log
already built and passed a clean, single-variable repro using the *real* production call chain
(a real `*runtimeagent.Session`, a real `*broker.Broker`, a real `notifyRecoveryBrokerForHTTPStreamError`
call — not a shallow mock) rather than task `07`'s messier organic trigger, and (b) the specific
concern motivating this whole re-run (fixes verifying clean in isolation but not actually
compiled into Nanite's real binary) is exactly what a fresh `go test` run under the newly-bumped,
correctly-resolved dependency graph checks for, re-ran task `21`'s own regression tests directly
against this repo's current module graph:

```
$ go test ./internal/service/... -race -run 'TestAdoptReplacementSession_' -v -count=1
...
recovery: displaced session stopped by adoptReplacementSession — skipping broker session_id=sess-orphan-repro-2
ok  	github.com/hollis-labs/nanite/internal/service	15.954s
```

Both `TestAdoptReplacementSession_MidStreamErrorPath_StopsDisplacedSession` and
`TestAdoptReplacementSession_ObserveSessionForRecoveryPath_NoDoubleNotify` pass clean under
`-race`, now compiled against `agentkit v0.5.0`/`go-agent-wrapper v0.4.0` (not the versions task
`21` was originally verified against before the pin bump) — a real, non-trivial re-verification
given this exact re-run's own premise that isolated-repo-clean does not guarantee
compiled-into-Nanite-clean. This is the lighter-touch confirmation this task's own instructions
explicitly allow in lieu of a fresh live repro; a fresh live single-variable repro of the mid-
stream-error trigger itself was judged out of proportion for this re-verification pass given
task `21`'s already-strong existing coverage.

### Check 6 — full regression

- `go build ./cmd/nanite/` — clean.
- `go vet ./...` — clean except the same two pre-existing `internal/service/container.go`
  findings (`stopReaper`/`stopRuntimeReaper`) every task in this batch has already noted as
  predating the batch entirely.
- `go test ./... -count=1` — clean across all 90 packages, no regression.
- `git status --short` — identical to the pre-run baseline at every check throughout this run
  (confirmed repeatedly: after the build, after each provider's turn, after the broker-kill test,
  after shutdown, and after the full test run) — this run touched no tracked files.

### Cleanup

Shut the scratch server down cleanly (`kill -TERM`); server log shows the same graceful
`"shutting down"` → reaper-stop sequence the original run observed. Confirmed via `ps` that no
scratch-related `claude -p`/`codex exec`/`opencode run`/`nanite-dogfeed07b` processes remained
afterward, and via `ls` that none of this run's own session-specific boot dirs (matched by
session ID) remained under `/var/folders/.../T/` (a large number of *older*, unrelated stale boot
dirs from prior, separate sessions on this machine do still exist there — pre-existing
accumulation, not created or left behind by this run, and out of this task's scope to clean up).

### Verdict

All three previously-broken/unvalidated paths this re-run was dispatched to confirm are now
**verifiably working against real binaries, through the real production HTTP surface**:

- Codex (task `19`): real turn completes end-to-end — **confirmed**.
- OpenCode (tasks `18`+`22` together): real turn completes end-to-end for the first time —
  **confirmed**.
- Broker real-process-kill classification (task `20`): the single most important check in this
  re-run — **confirmed**, including a working end-to-end replacement-session dispatch, not just a
  log line.
- Broker replacement-session leak (task `21`): confirmed via lighter-touch re-verification
  (existing regression tests, re-run clean against the now-correctly-resolved dependency graph),
  per this task's own explicit allowance for that approach.
- Claude (regression check): still fully working, including `Session.Stop()` — **no regression**.
- `go test ./...`: clean. `git status --short`: clean throughout, identical to baseline.

No new bugs found during this re-run. Reporting this verdict to the Orchestrator; per this task's
own standing instruction, not editing `TASKS/INDEX.md` — that Phase-2-`validated` determination is
the Orchestrator's to make.
