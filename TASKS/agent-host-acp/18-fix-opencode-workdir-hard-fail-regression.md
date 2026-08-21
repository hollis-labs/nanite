# Fix: every real OpenCode CLI session hard-fails with "Config.Workdir is required"

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`), found during task `07`'s
real-provider dogfeed validation.
**Status:** reviewed
**Depends on:** none (task `06`, the migration that introduced this, is already landed/reviewed —
this is a follow-up fix, not a blocker on anything upstream).
**Touches:** `internal/runtime/agent/agent.go` (the `wrapper.Config.Workdir` wiring) and/or
`internal/runtime/agent/bootdir_opencode.go` (`opencodeLayout.SpawnWorkdir`) — exact fix location
is this task's own call, see "What to do" below. Repo: Nanite.

## Context

Found live during `TASKS/agent-host-acp/07`'s real-provider dogfeed (real `opencode` CLI binary,
not a mock) — **every real OpenCode CLI-hosted chat session fails outright, 100% reproducibly,
on the very first turn**, with:

```
driveBootSession: boot: agent.Boot: wrapper.Run: wrapper: Config.Workdir is required
```

**Root cause, traced end to end:**

1. `wrapper.Wrapper.Run` (`libs/go-agent-wrapper/wrapper/wrapper.go:272-273`) hard-validates
   `Config.Workdir` is non-empty before doing anything else:
   ```go
   if w.cfg.Workdir == "" {
       return errors.New("wrapper: Config.Workdir is required")
   }
   ```
2. `internal/runtime/agent/agent.go:404` computes `spawnWorkdir :=
   layout.SpawnWorkdir(bootDir, opts.Workdir)` and feeds it directly into
   `wrapper.Config.Workdir` (`agent.go:532`).
3. `opencodeLayout.SpawnWorkdir` (`bootdir_opencode.go:190`) is `func (opencodeLayout)
   SpawnWorkdir(_, projectDir string) string { return projectDir }` — it returns `opts.Workdir`
   **verbatim, with zero fallback**. (Compare `claudeLayout`/`codexLayout`'s versions, both
   `func(bootDir, _ string) string { return bootDir }` — they always return the boot dir, which
   is never empty, so they never hit this.)
4. `internal/service/chat_boot_drive.go:591-594` — `bootSessionWorkdir(session *store.Session)
   string` — is a **documented, intentional, pre-existing stub** that always returns `""`:
   > "The chat-harness today binds claude / codex to the per-session sandbox dir (cwd = sandbox
   > dir, no project dir threaded). The new boot-dir-based path preserves that shape: empty
   > Workdir lets the layout's SpawnWorkdir use the boot dir as cwd. Project-repo workdir
   > threading is a follow-up — chat sessions don't carry a resolved project path through to
   > this layer today."

**This is a real, migration-caused regression, not merely a pre-existing gap surfacing for the
first time.** Pre-migration, `opts.Workdir=""` flowed into `agentsessions.StartOptions.Workdir`
directly, which — for OpenCode's `exec.Cmd.Dir` — Go interprets as "inherit the calling
process's own cwd" (i.e. the Nanite daemon's own working directory). That was already a real,
separate, silent "wrong cwd" bug for OpenCode chat sessions (a session would silently run
wherever `nanite serve` happened to be launched from) — but it did not *crash*. Post-migration,
`wrapper.Wrapper.Run`'s new, hard `Config.Workdir == ""` check (a validation that did not exist
anywhere pre-migration) turns that same empty value into an unconditional hard failure. Every
real OpenCode CLI session that used to at least attempt to run (in the wrong directory) now
cannot run at all.

**Confirmed via real dogfeed, not a guess**: built a scratch server (`go build -o
/tmp/nanite-dogfeed ./cmd/nanite/`), ran it from a scratch CWD against a scratch DB, created a
real managed agent with `default_provider='pty-opencode'`, and sent one real chat turn through
`POST /api/harness/v1/sessions/{id}/turns` against the real, authenticated `opencode` binary on
this machine. The SSE stream returned exactly the error above, `details.raw` matching the exact
error string quoted; the `agent_runtime` row's `failure_reason` column recorded the identical
string. No workaround attempted (config, env, or otherwise) — the failure is unconditional
because `bootSessionWorkdir` never returns anything but `""` for any session, by design, today.

**Test-coverage gap that let this through**: task `06`'s own new tests
(`wrapper_lifecycle_test.go`) cover Claude (`TestBoot_WrapperLifecycle_Claude`,
`TestBoot_WrapperLifecycle_Stop`) and Codex (`TestBoot_WrapperLifecycle_Codex_EnvParity`) via
real fake-subprocess scripts, but **contain zero OpenCode-specific coverage** — despite task
`04`'s own Context explicitly flagging OpenCode's `SpawnWorkdir` behavior as "notably different"
and task `06`'s Context explicitly naming it as "the provider where the task's 'don't force this
into Planter' instruction matters most concretely." The one provider whose `SpawnWorkdir`
actually depends on `opts.Workdir` being non-empty was never driven through an end-to-end
Boot → `wrapper.Wrapper.Run` test by either task.

## What to do

1. Decide, and document the decision, on the fix shape — two credible directions, not mutually
   exclusive:
   - **Defend at the wiring site** (`agent.go`): when `spawnWorkdir == ""`, fall back to `bootDir`
     before feeding `wrapper.Config.Workdir` — matching what Claude/Codex already get for free,
     and at minimum turning today's hard crash back into "runs, but in the boot dir" (closer to,
     though not identical to, pre-migration's "runs in the daemon's own cwd" — arguably an
     *improvement* over the pre-migration silent-wrong-cwd behavior, since the boot dir is at
     least a real, scoped, per-session directory, not the daemon's own unrelated cwd).
   - **Defend at the Layout level** (`bootdir_opencode.go`): give `opencodeLayout.SpawnWorkdir`
     its own explicit fallback (e.g. `if projectDir == "" { return bootDir }`) so the same
     protection applies to any other caller of `LayoutFor("opencode").SpawnWorkdir`, not just this
     one call site in `agent.go`.
   - Either way, **this task does not need to solve "project-repo workdir threading"** (the
     larger, explicitly-deferred follow-up `bootSessionWorkdir`'s own comment names) — it only
     needs to stop the empty-Workdir case from hard-crashing every OpenCode session. If the real
     fix is judged to require the larger workdir-threading work instead, escalate that finding
     rather than silently scoping down.
2. Add real regression coverage mirroring task `06`'s own pattern: a
   `TestBoot_WrapperLifecycle_OpenCode` (or equivalent) in `internal/runtime/agent` that drives a
   real fake `opencode` HTTP+SSE server end to end through `agent.Boot` with `Options.Workdir`
   left empty (the exact real-world shape `driveBootSession` always produces today) — the missing
   coverage that let this regression ship.
3. Confirm the fix against a **real** `opencode` binary too, not just the fake-server unit test —
   this class of gap (a hard-required field silently empty in the one real, non-mocked path) is
   exactly what fake-adapter unit tests miss; don't close this out on unit-test-green alone. A
   scratch-server dogfeed identical in shape to `07`'s (see that task's Work Log for the exact
   recipe: scratch CWD, scratch XDG dirs, scratch `-db`, real `opencode` binary, a managed agent
   with `default_provider='pty-opencode'`, one turn via `POST
   /api/harness/v1/sessions/{id}/turns`) is sufficient.

## Done means

- A real OpenCode CLI-hosted chat turn completes successfully end-to-end (verified against the
  real `opencode` binary, not just a fake), with no `Config.Workdir is required` error.
- New test coverage exists pinning the previously-missing OpenCode Boot→`wrapper.Wrapper.Run`
  path (matching Claude/Codex's existing coverage shape).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` clean.
- Work Log documents which fix direction was taken and why, and whether the pre-migration
  "silently wrong cwd" behavior for OpenCode is now: (a) fixed for real (workdir threading
  landed), (b) still present but no longer crashing (boot-dir fallback), or (c) something else —
  don't leave this ambiguous for the next reader.

## Work Log (2026-08-21)

Read this task file in full, `docs/engineering/architecture/16-agent-host.md`, this batch's
`README.md`, and task `07`'s Work Log (for the exact real-binary dogfeed recipe — scratch CWD,
scratch XDG env vars, scratch `-db`, real `$HOME` for CLI auth) before touching anything.

### Fix direction chosen: Layout level (`bootdir_opencode.go`), not the `agent.go` wiring site

Both directions the task file names are functionally identical **at the one call site that
exists today** — `agent.go:404`'s `spawnWorkdir := layout.SpawnWorkdir(bootDir, opts.Workdir)`
is the only caller of `SpawnWorkdir` anywhere in the codebase (confirmed via `grep`; `internal/
recovery`'s `BootDirOps` resolves a `Layout` but never calls `SpawnWorkdir` on it), and
`spawnWorkdir`'s value feeds both `wrapper.Config.Workdir` and the persisted `RuntimeRow.Workdir`
either way. Chose the **Layout level** fix — `opencodeLayout.SpawnWorkdir` now falls back to
`bootDir` when `projectDir == ""` — for two reasons:

1. It makes the `Layout` interface's own contract actually hold. `bootdir.go`'s `Layout.
   SpawnWorkdir` doc comment (and `chat_boot_drive.go`'s `bootSessionWorkdir` doc comment) already
   *claimed* "empty Workdir lets the layout's SpawnWorkdir use the boot dir as cwd" as the
   intended, general design — true for `claudeLayout`/`codexLayout` (both unconditionally return
   `bootDir`), false for `opencodeLayout` before this fix. Fixing at the Layout level makes that
   existing, already-written claim actually true for all three layouts, rather than true for two
   out of three with a wiring-site patch bolted on beside it.
2. It protects any *future* caller of `LayoutFor("opencode").SpawnWorkdir` (there's only one
   today, but the interface is exported and `LayoutFor` is a public composition-root seam per
   `bootdir.go`'s own doc comment) rather than only the one call site `agent.go` happens to have.

Also strengthened the `Layout.SpawnWorkdir` interface doc comment (`bootdir.go`) to state the
invariant explicitly: "MUST NOT return \"\" — the caller feeds this straight into `wrapper.
Config.Workdir`, which `wrapper.Wrapper.Run` hard-requires to be non-empty." Left `unsupportedLayout.
SpawnWorkdir` (`bootdir.go:322`, also returns `projectDir` verbatim) untouched — it's genuinely
unreachable from `agent.Boot`: `Setup` always fails first for an unsupported provider (`agent.
go:383-389` returns before `SpawnWorkdir` is ever called), so there's no live crash risk there,
and touching it would be unrelated scope creep.

Did **not** additionally patch `agent.go`'s wiring site — the Layout-level fix already covers the
one real call site identically; adding a second, redundant fallback there would just be dead
defensive code with no caller that could ever exercise it.

### Fix

```go
func (opencodeLayout) SpawnWorkdir(bootDir, projectDir string) string {
	if projectDir == "" {
		return bootDir
	}
	return projectDir
}
```

(`internal/runtime/agent/bootdir_opencode.go`), with an expanded doc comment tracing the
pre-migration/post-migration behavioral history described in this file's own Context section, so
a future reader doesn't need to re-derive it from git blame.

### Pre-migration "silently wrong cwd" verdict: **(b) — still present, no longer crashing**

Explicitly answering this task's own Done-means question: OpenCode chat sessions still do **not**
run in a real, resolved project directory — `bootSessionWorkdir` (`chat_boot_drive.go:591-594`)
is untouched by this fix and still always returns `""` for every session, by design (its own
doc comment names "project-repo workdir threading" as a separate, larger, still-deferred
follow-up this task was explicitly told it does not need to solve). What changed: the process
now runs with `cwd = bootDir` (a real, scoped, per-session temp directory this session's own
`.mcp.json`/`opencode.json`/`agents.json` already live in) instead of either crashing outright
(post-migration, pre-this-fix) or silently inheriting the Nanite daemon's own unrelated cwd
(pre-migration). This is not "fixed for real" in the sense of running in the user's actual
project directory, but it is a strict improvement over both prior states, and it is honestly a
different directory than pre-migration's behavior (`bootDir`, not the daemon's cwd) — flagging
that explicitly rather than conflating the two.

### Regression coverage added

- `internal/runtime/agent/bootdir_opencode_test.go`:
  `TestOpencodeLayout_SpawnWorkdir_EmptyProjectDirFallsBackToBootDir` — direct unit pin on the
  fixed method (`SpawnWorkdir("/tmp/boot", "")` → `"/tmp/boot"`), alongside the existing
  non-empty-projectDir case (renamed/clarified, unchanged behavior).
- `internal/runtime/agent/wrapper_lifecycle_test.go`: `TestBoot_WrapperLifecycle_OpenCode` — a
  real fake-subprocess integration test mirroring `TestBoot_WrapperLifecycle_Codex_EnvParity`'s
  own shape exactly (real `provider.NewOpencodeAdapter()`, a real fake shell script standing in
  for the `opencode` binary via `OPENCODE_CLI_PATH`, driven through the real `agent.Boot` →
  `wrapper.Wrapper.Run` path), with **`Options.Workdir` left empty** — the exact real-world shape
  `driveBootSession` always produces. Asserts: `Boot` succeeds (pre-fix this failed with `wrapper:
  Config.Workdir is required`), the fake process's observed `OPENCODE_CONFIG_DIR` equals the real
  boot dir (proving `wrapEnvForSpawn`'s env-wrapper-script mechanism still propagates
  `opencodeLayout.AmendEnv`'s redirect correctly with the new cwd), a real `EventDelta` reaches
  the fanout channel, and the persisted `agent_runtime` row's `Workdir` column equals the boot
  dir (not empty) — pinning both the `wrapper.Config.Workdir` and the `RuntimeRow.Workdir` side of
  the fix in one test.

  **Deviation from the task file's literal wording, noted for the record:** the task file's own
  "What to do" step 2 describes the fake as an "HTTP+SSE server." That does not match how Nanite
  actually drives OpenCode — `internal/service/container.go:1005` registers `provider.
  NewOpencodeAdapter()` with `Mode=""` ("run" mode, subprocess-per-turn, plain stdout text), not
  `NewOpencodeAdapterServeHTTP()` (`Mode="serve-http"`, opencode's own native HTTP+SSE API) —
  confirmed directly in `wrapper_adapter.go`'s own doc comment, which states adopting the
  HTTP+SSE-shaped shipped adapter package "would silently change... OpenCode's spawn shape" and
  is deliberately out of scope for the whole migration. `runtimeConfigForAdapter` sets no
  `ServeHTTP`/`JsonRpcStdio` capability for OpenCode today — it runs through the exact same
  "adapter runtime" (subprocess-per-turn) shape as Codex. The new test therefore mirrors the
  Codex test's fake-subprocess shape (a fake shell script behind `OPENCODE_CLI_PATH`), not an
  HTTP+SSE fake server, since that's what the real, compiled code path actually is. This is a
  correction to the task brief's stated mechanism, not a scope change — the coverage still drives
  the exact same real `agent.Boot` → `wrapper.Wrapper.Run` → real-fake-subprocess path task `06`'s
  Claude/Codex tests already establish the pattern for.

### Real-binary dogfeed verification

Followed task `07`'s exact recipe (scratch CWD, scratch `XDG_DATA_HOME`/`XDG_STATE_HOME`/
`XDG_CONFIG_HOME`/`XDG_CACHE_HOME`, scratch `-db`, real `$HOME` for CLI auth). Built a scratch
binary (`go build -o .../scratchpad/nanite-dogfeed18 ./cmd/nanite/`), ran it on port 8099 (the
real Cerberus-managed service holds 8090) from a scratch CWD against a scratch SQLite DB. Created
a real managed agent via `POST /api/agents`, then — matching task `07`'s own noted gap ("the
Phase-0-era file-discovered agent silently drops DB-only columns") — set `default_provider=
'pty-opencode'`/`runtime_kind='cli'` via a direct, scoped `sqlite3 UPDATE` on the scratch DB's own
`agent_profiles` row (confirmed this round-trips via `GET /api/agents/{id}`). Also had to seed a
`providers` row for `provider_type='pty-opencode'` with a non-empty `default_model` (`opencode/
grok-code`) — none existed pre-seeded, matching task `07`'s own one-time scratch-DB setup note;
without it the turn fails cleanly with a distinct, unrelated `"No default model configured"`
error, not the bug this task is about.

Sent real turns via the real `POST /api/harness/v1/sessions` → `POST .../turns` → `GET
.../events` (SSE) HTTP surface — the actual production path, not an internal-package test.
**Zero occurrences of `Config.Workdir is required`** across three separate real turns sent during
this dogfeed (confirmed by grepping the full scratch-server log and every SSE response
transcript). Positive, direct confirmation the fix works:

- The `agent_runtime` row for a live OpenCode session showed `state='running'`,
  `provider='opencode'`, and **`workdir=` the real boot dir path** (not empty) — i.e. the fixed
  `SpawnWorkdir` fallback is genuinely exercised in the real, compiled, running binary, not just
  in a unit test.
- Inspected the real, on-disk generated `.wrapper-exec/opencode.sh` script for that session:
  `OPENCODE_CONFIG_DIR` correctly set to the boot dir, real `$HOME`/`PATH` preserved — confirming
  task `06`'s env-wrapper-script workaround still composes correctly with this fix's new cwd.
- Confirmed via `ps` that the real `opencode` binary genuinely spawns (`opencode run --agent
  <prompt text>`) and — this is the key confirmation the crash is gone — **runs to completion and
  exits cleanly** rather than exiting immediately with the `Config.Workdir is required` error
  path (which never spawns a subprocess at all, per `wrapper.Wrapper.Run`'s own validation
  ordering — the crash fires *before* `runtime.Start`/`Prepare` is ever reached).
- Independently reproduced the exact same wrapper script by hand, outside Nanite, from the boot
  dir as cwd: it read `boot.md`, produced a real, coherent LLM response, and exited 0 — confirming
  the fixed cwd is a genuinely usable working directory for the real CLI, not just "doesn't
  crash."

`git status --short` was run before, during (repeatedly), and after this dogfeed — clean at
every check; the scratch server/DB/CWD were fully isolated under this session's scratchpad
directory. Shut the scratch server down cleanly (`kill -TERM`); confirmed via `ps` that no
scratch-related `nanite-dogfeed18`/`opencode` processes remained afterward.

### A second, separate, real bug found while chasing this task's own "completes successfully
end-to-end" Done-means bar — escalated as `TASKS/agent-host-acp/22`, not fixed here

This task's own Done-means bar #1 asks for "a real OpenCode CLI-hosted chat turn completes
successfully end-to-end." The crash this task is about is fully gone (see above). But pushing a
real turn all the way through the actual interactive chat harness (not just `agent.Boot`'s
`ModeOneShot`/`AutoFireFirstTurn` path, which is what this task's own new unit test uses and does
*not* exhibit the next bug) surfaced a **second, independent, real, 100%-reproducible bug**:
the real `opencode` subprocess spawns, runs, produces real output, and exits cleanly — confirmed
directly via `ps` and a byte-for-byte manual reproduction of the same generated wrapper script —
but the chat harness's own SSE stream never advances past `stream_start`. It hangs indefinitely
(observed for several minutes across two independent real turns; did not wait out the full 900s
idle timeout logged by the chat loop).

Traced this to a structural gap in `agentkit/agentsessions/from_adapter.go`'s `adapterSession.
handleRunnerEvent`: `OpencodeAdapter.ParseLine` (`Mode=""`, the only Mode Nanite wires up) never
emits a terminal `EventDone`/`EventUsage`/`EventError` — by its own documented design, "opencode
emits plain text on stdout with no structured completion event" — and nothing in the
subprocess-per-turn "adapter runtime" synthesizes one on process exit. `wrapper/event_translator.
go`'s `translateStreamEvent` only maps `EventUsage`/`EventDone` to `runtimeevents.
KindTurnCompleted`, so that event kind is never emitted for a real OpenCode turn, and the chat
harness's streamLoop has nothing to key turn-completion off of. Confirmed this is OpenCode-
specific (not a general adapter-runtime gap): Codex's own `ParseLine` does emit `EventDone` on
its own `"turn.completed"` line, so Codex turns should complete correctly through this exact same
plumbing once task `19`'s unrelated `--skip-git-repo-check` gap is fixed. Also confirmed the
`OpencodeAdapter` doc comment's own claim ("the bridge synthesizes llmtypes.EventDone on clean
process exit") does not match any code in `agentkit/agentsessions/from_adapter.go` — the only
synthesis-on-a-native-completion-signal that exists anywhere in `agentkit` is `serve_http_session.
go`'s `markTurnDone()`, which is opencode's *other*, unused-by-Nanite `serve-http` Mode, keyed off
its own native `session.idle` event, not process exit.

This is a genuinely different bug from the one this task is scoped to (a different repo,
`agentkit`, not Nanite; a different mechanism, turn-completion signaling, not workdir
validation) that happened to be reachable for the first time only because this task's own fix
cleared the crash in front of it — the same shape as task `07` surfacing tasks `18`-`21`. Per
this project's fix-as-new-worker-task discipline (`EXECUTION-PROCESS.md`, and this exact batch's
own precedent at task `07`), wrote it up as `TASKS/agent-host-acp/22-fix-opencode-turn-never-
completes-chat-harness.md` with the full root-cause trace, rather than expanding this task's own
scope to fix a different repo's completion-signaling contract inline.

**Net honest status against this task's own Done-means bar #1:** the specific error this task
targets (`Config.Workdir is required`) is fully, verifiably gone — confirmed via both the new
fake-subprocess unit test and a real-binary dogfeed showing the real process spawn/run/exit
cleanly with the correct cwd. A real interactive OpenCode chat turn does **not** yet complete
end-to-end through the full chat harness, but the reason it doesn't is the separate,
newly-discovered bug in task `22`, not anything in this task's own scope or fix. Flagging this
explicitly rather than silently marking the literal Done-means checkbox complete.

### Build/test status

- `go build ./cmd/nanite/` — clean.
- `go vet ./...` — clean except the same two pre-existing, unrelated `internal/service/
  container.go` findings (`stopReaper`/`stopRuntimeReaper` "not used on all paths") every other
  task in this batch has already noted — confirmed via `git blame` these lines predate this batch
  entirely (commit `76df826a3`, 2026-05-11, well before `TASKS/agent-host-acp` started). Not
  touched by this task.
- `go test ./...` (fresh, from this worktree) — clean across every package, including the two new
  tests in `internal/runtime/agent`.

### Commit

Committed to this worktree's branch, `agent-host-acp/task-18-opencode-workdir-fix` — see git log
for the SHA (Orchestrator merges).

## Review notes

Orchestrator-verified (2026-08-21): independently confirmed the diff, re-ran build/vet/test,
and re-confirmed live against the final, fully-patched build during the section-level Phase 2
re-verification dogfeed. Full verification record: `TASKS/ESCALATIONS.md`'s 2026-08-21 entries
for this task, and the whole-section fresh review (also 2026-08-21, PASS) that closed Phase 2.
