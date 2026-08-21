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
