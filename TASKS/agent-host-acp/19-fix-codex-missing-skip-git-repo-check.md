# Fix: every real Codex CLI session hard-fails — missing `--skip-git-repo-check`

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`), found during task `07`'s
real-provider dogfeed validation.
**Status:** reviewed
**Depends on:** none.
**Touches:** `libs/go-providers/provider/pty_codex.go` (`CodexAdapter.BuildArgs`'s exec-mode
argv). Sibling repo, NOT Nanite — a `go.mod` `require` bump (no `replace` needed; Nanite already
carries a local `replace` for `go-providers`? check at dispatch time) may be needed on the Nanite
side afterward to pick up the fix, mirroring task `01`'s/`05a`'s precedent for landing a fix in a
sibling repo and bumping Nanite's pin.

## Context

Found live during `TASKS/agent-host-acp/07`'s real-provider dogfeed (real `codex` CLI binary —
`codex-cli 0.147.0` on this machine — not a mock) — **every real Codex CLI-hosted chat session
fails outright, 100% reproducibly, on the very first turn**, with:

```
driveBootSession: send input: runner: process exited 1
```

(and, identically, on the recovery broker's own automatic retry-dispatch attempt: `agent.Boot:
wrapper.Run: wrapper: runtime.Start: agentsessions: auto-fire first turn: runner: process exited
1`).

**Root cause, confirmed directly, not guessed**: `internal/runtime/agent`'s planted Codex boot
dir is a fresh, non-git-repo temp directory (`os.MkdirTemp`-rooted, per `bootdir_common.go`'s
`makeBootDir` — unchanged by this migration, confirmed byte-identical pre/post by task `04`'s own
boot-dir diff). Reproduced the exact failure by hand, outside Nanite entirely: ran the real
planted boot dir's own `.wrapper-exec/codex.sh` (task `06`'s env-wrapper script, carrying the
exact env Nanite composed) directly with the exact argv `go-providers`'
`CodexAdapter.BuildArgs`'s exec-mode branch constructs (`["exec", prompt, "--json"]`,
`pty_codex.go:116-118`):

```
$ sh .wrapper-exec/codex.sh exec "What is 2+2? Reply with only the number." --json
Reading additional input from stdin...
Not inside a trusted directory and --skip-git-repo-check was not specified.
```

Adding the one missing flag makes it work correctly, first try:

```
$ sh .wrapper-exec/codex.sh exec "What is 2+2? Reply with only the number." --json --skip-git-repo-check
{"type":"thread.started","thread_id":"..."}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"4"}}
{"type":"turn.completed","usage":{...}}
```

`CodexAdapter.BuildArgs`'s exec-mode branch (`pty_codex.go:116-118`) is:

```go
// Exec mode (default): single-turn `codex exec <prompt> --json`.
// System prompt is file-based (AGENTS.md in sandbox dir), not a flag.
return []string{"exec", prompt, "--json"}
```

— it never passes `--skip-git-repo-check`, and since Nanite's boot dirs are never git
repositories (by design — they're throwaway per-session temp dirs), the real `codex` CLI's own
trust-gate refuses to run non-interactively every single time.

**Not caused by this migration.** `BuildArgs`'s argv construction lives in `go-providers` (a
separate sibling repo from both `go-agent-wrapper` and `agentkit`) and is untouched by task `06`
(confirmed: task `06`'s own Context states `provider.CLIAdapter.BuildArgs` implementations are
called identically pre/post migration, only the call *shape* around `StartOptions` changed).
This is very likely a standing, previously-undiscovered functional gap for Codex CLI sessions in
Nanite generally — plausibly caused by a `codex-cli` version bump on the operator's own machine
that added this trust gate sometime after Codex CLI support was originally built/tested in
Nanite, not by anything in this batch. Flagged here (rather than a separate, non-`agent-host-acp`
task) because this batch's own dogfeed (task `07`) is what surfaced it — this is the first real
exercise of a live `codex` binary through Nanite's CLI-agent path that anyone has done.

## What to do

1. Add `--skip-git-repo-check` to `CodexAdapter.BuildArgs`'s exec-mode return value
   (`pty_codex.go:118`) — before making the change, read the current `codex exec --help` output
   on a representative `codex-cli` version (or check its source/docs) to confirm this is the
   correct, stable flag name and that it's safe unconditionally (i.e. it doesn't relax any
   security-relevant behavior beyond "don't refuse to run outside a git repo" — the sandbox
   (`approval_policy`/`sandbox_mode` in the planted `config.toml`) is a separate, already-correct
   mechanism this flag doesn't touch).
2. Confirm app-server mode (`CodexAdapter.BuildArgs`'s other branch, `Mode == "app-server"`) is
   unaffected — Nanite doesn't use app-server mode today (per task `06`'s Work Log: "Nanite
   doesn't use codex app-server mode"), but check whether that mode has the identical trust-gate
   issue for any other consumer of this shared library before deciding whether it needs the same
   fix or is out of scope.
3. Add or update a `pty_codex_test.go` case pinning `--skip-git-repo-check` in the exec-mode argv
   (a plain `BuildArgs` string-slice assertion — no process spawn needed for this part).
4. Bump Nanite's `go.mod` `go-providers` pin (or whatever the correct sibling-repo landing +
   version-bump precedent is per task `01`'s/`05a`'s example) once the fix lands and is
   reviewed, and re-verify against a **real** `codex` binary (not just the unit test) — a
   scratch-server dogfeed identical in shape to `07`'s (see that task's Work Log: scratch CWD,
   scratch XDG dirs, scratch `-db`, a managed agent with `default_provider='pty-codex'`, one turn
   via `POST /api/harness/v1/sessions/{id}/turns`) is sufficient to confirm.

## Done means

- A real Codex CLI-hosted chat turn completes successfully end-to-end against the real `codex`
  binary, with no "not inside a trusted directory" failure.
- `go build ./...` / `go test ./...` clean in `go-providers`.
- Nanite's pin bumped and re-verified via a real dogfeed turn, not just a unit test.
- Work Log documents whether app-server mode needed the same fix or was confirmed out of scope,
  and why.

## Work Log

**Scope correction vs. the task file above.** The dispatch prompt for this run explicitly
overrode step 4 / the last "Done means" bullet: the Orchestrator is centralizing all
Nanite-side `go.mod` pin bumps across this dogfeed's fixes to avoid concurrent edits, so this
run stopped after landing + tagging the fix in `go-providers` and deliberately did **not** touch
Nanite's `go.mod`, `go.sum`, or attempt a Nanite-side dogfeed re-verification. That is the
Orchestrator's follow-on step, not this task's.

**Fix.** `CodexAdapter.BuildArgs`'s exec-mode branch
(`libs/go-providers/provider/pty_codex.go`) now returns
`["exec", prompt, "--json", "--skip-git-repo-check"]` (previously missing the last flag). Checked
`codex exec --help` on the real `codex-cli 0.147.0` binary on this machine first — the flag's
own help text is `--skip-git-repo-check  Allow running Codex outside a Git repository` — confirms
it is the correct, stable flag name and that it is scoped purely to "which directories codex is
willing to start `exec` in." It does not intersect with `approval_policy`/`sandbox_mode` (a
separate, already-correct mechanism plumbed through the planted `config.toml`), so it's safe to
pass unconditionally on every exec-mode invocation.

**App-server mode: confirmed out of scope, not just "assumed."** Checked three independent ways,
all on the real `codex-cli 0.147.0` binary:
1. `codex app-server --help` — no `--skip-git-repo-check` (or any git/trust-related) flag exists
   in that subcommand's own CLI surface at all.
2. Generated the app-server JSON-RPC protocol's TypeScript bindings
   (`codex app-server generate-ts --out ...`) and grepped the full output for
   `trust`/`skip_git`/`GitRepo`/similar — the `thread/start` request's param type
   (`ThreadStartParams`) has no git-repo-check-equivalent field; nothing in the protocol
   references this concept.
3. Direct empirical confirmation: hand-rolled a newline-delimited JSON-RPC client, drove a real
   `codex app-server` process (`initialize` → `thread/start` → `turn/start`) against a fresh
   non-git tempdir cwd, and it succeeded end-to-end with no trust-gate error — `thread/start`
   returned a live thread (`gitInfo: null`, as expected for a non-git cwd) and `turn/start`
   returned `status: "inProgress"` cleanly.

   Conclusion: app-server mode's trust model is structurally different from `exec` mode's
   CLI-level non-interactive guard — it has no equivalent gate to work around, so
   `CodexAdapter.BuildArgs`'s `Mode == "app-server"` branch (`return []string{"app-server"}`) is
   unchanged. This lines up with task `06`'s Work Log note that Nanite doesn't use app-server mode
   today — moot for Nanite either way, but confirmed rather than assumed, per the task's own
   instruction.

**Real-binary confirmation (exact repro from this task's Context, re-run against the fix).** In a
fresh non-git tempdir:
```
$ codex exec "What is 2+2? Reply with only the number." --json
Reading additional input from stdin...
Not inside a trusted directory and --skip-git-repo-check was not specified.
[exit 1]

$ codex exec "What is 2+2? Reply with only the number." --json --skip-git-repo-check
{"type":"thread.started","thread_id":"..."}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"4"}}
{"type":"turn.completed","usage":{...}}
[exit 0]
```
Went one step further than the task's own repro for full code-path fidelity: wrote a throwaway Go
program that imports `go-providers`, calls the real (fixed) `CodexAdapter.BuildArgs(...)` to get
its actual argv, and `exec.Command("codex", args...)`s it directly against the real binary in a
fresh non-git tempdir — `argv: [exec What is 2+2? Reply with only the number. --json
--skip-git-repo-check]` — succeeded, returned `"4"`, exit 0. This exercises the literal
adapter code, not just a hand-typed equivalent command.

**Tests.** Updated `provider/pty_codex_test.go`: `TestCodexAdapter_BuildArgs` now asserts
`args[3] == "--skip-git-repo-check"`; added `TestCodexAdapter_BuildArgs_ExecMode_SkipsGitRepoCheck`
as a dedicated pin; extended `TestCodexAdapter_AppServer_BuildArgs_IgnoresAllParams`'s blacklist to
also fail if `--skip-git-repo-check` ever leaks into app-server-mode argv (it must stay
exec-mode-only, since app-server has no such flag to accept).

**Build/test status.** `go build ./...`, `go vet ./...`, `go test ./...` all clean in
`go-providers` after the change (`go test`: `ok github.com/hollis-labs/go-providers/provider`).

**Landed in `go-providers`, not Nanite.** Commit `0750f9f` on top of `main` (`fix(codex): add
--skip-git-repo-check to exec-mode argv`), directly on the pre-existing clean `main` (no worktree,
per this task's process notes) at prior HEAD `696ba50`. Touched files: `provider/pty_codex.go`,
`provider/pty_codex_test.go`, `CHANGELOG.md`.

**Version tag.** Checked the actual latest tag first (`git tag --sort=-v:refname`) rather than
trusting the task file's "`v0.23.0` was Nanite's pin at dispatch time" — `v0.23.0` was indeed
already the latest tag in this repo (an unrelated `feat(opencode): serve-http adapter` landed
after it, at the `main` HEAD I started from, with no `CHANGELOG.md` entry added for `v0.23.0`
either — a pre-existing gap, not introduced or backfilled by this task). Added a `## v0.24.0 —
2026-08-21` `CHANGELOG.md` entry under `### Fixed`, following the file's existing format, and cut
annotated tag `v0.24.0` on the fix commit, following the tag-message style of prior releases
(`v0.20.0`/`v0.21.0`).

**Not done (deliberately, per this run's explicit dispatch instruction, overriding the task
file's step 4 and last "Done means" bullet):** Nanite's `go.mod` `go-providers` pin was not
touched, and no Nanite-side dogfeed re-verification was attempted. The Orchestrator owns that
follow-on step centrally across this dogfeed's fixes.

## Review notes

Orchestrator-verified (2026-08-21): independently confirmed the diff, re-ran build/vet/test,
and re-confirmed live against the final, fully-patched build during the section-level Phase 2
re-verification dogfeed. Full verification record: `TASKS/ESCALATIONS.md`'s 2026-08-21 entries
for this task, and the whole-section fresh review (also 2026-08-21, PASS) that closed Phase 2.
