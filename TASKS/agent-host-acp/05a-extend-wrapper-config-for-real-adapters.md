# Extend go-agent-wrapper's wrapper.Config for real (non-empty-Descriptor) adapters

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`), landing in the sibling
`libs/go-agent-wrapper` repo — same cross-repo shape as Phase 1's `01`/`02`, inserted as a
prerequisite once task `06` discovered it needed this.
**Status:** reviewed
**Depends on:** none (Phase 1 already landed and merged; this is independent of any
Nanite-side work).
**Touches:** `libs/go-agent-wrapper/wrapper/wrapper.go`, `wrapper/wrapper_test.go`,
`wrapper/wrapper_integration_test.go`, `CHANGELOG.md`. Repo: `libs/go-agent-wrapper` (sibling,
NOT Nanite).

## Context

Task `06` (`TASKS/agent-host-acp/06-migrate-session-lifecycle-to-wrapper.md`) attempted the
Nanite-side migration onto `wrapper.Wrapper.Run` and found — empirically, via a reproduction
test, not just by reading code — that `Wrapper.Run` is structurally non-functional for all
three of go-agent-wrapper's own real shipped adapters (`adapters/claude`, `adapters/codex`,
`adapters/opencode`). Full finding logged in `TASKS/ESCALATIONS.md`, 2026-08-21 ("Task `06`
... `Wrapper.Run` is non-functional for all three of go-agent-wrapper's own real adapters...").
Summary, verified independently by the Orchestrator against `libs/go-agent-wrapper`'s merged
`main` before this task was written:

**Finding 1 — hard, unconditional blocker.** `Wrapper.Run`'s hardcoded
`agentsessions.StartOptions{}` construction (`wrapper.go:342-381`) sets `Workdir`,
`EventFanout`, `Fanout`, `Stderr`, `Profile`, `TypedEventCallback`, `JsonRpcRequestHook` —
nothing else. `wrapper.Config` has **no field** that maps to `StartOptions.WorkspaceDir` or
`StartOptions.LogPath`. Every one of agentkit's three real runtime kinds Claude/Codex/
OpenCode's real `Describe()` implementations map to (streaming-stdio, jsonrpc-stdio,
serve-http) calls a `resolve<Kind>LogPath(opts)` helper that returns a hard error, before
spawning anything, when both `opts.LogPath` and `opts.WorkspaceDir` are empty — e.g.
`agentkit/agentsessions/streaming_stdio_session.go:130-136`: `if opts.LogPath != "" { return
opts.LogPath, nil }; if opts.WorkspaceDir == "" { return "", errors.New("agentsessions:
streaming-stdio runtime requires StartOptions.LogPath or StartOptions.WorkspaceDir") }`. Since
`Wrapper.Run` never sets either, this error fires deterministically for real Claude/Codex/
OpenCode adapters. Task `06`'s worker empirically confirmed this via a throwaway reproduction
test using the wrapper's own fake-CLI-script test harness with a real
`adapters.ProtocolClaudeStreamJSON`/`adapters.TransportStdio` Descriptor pair (exactly what
`adapters/claude/claude.go` declares) — it failed with the exact predicted error.

**Why go-agent-wrapper's own ~15 integration tests never caught this**: every one uses a fake
adapter with Protocol/Transport left deliberately empty (`wrapper_integration_test.go:87-90`'s
own comment: "Protocol/Transport intentionally left unset — subprocess-per-turn fallback
shape"), which routes via `runtimeCaps`'s `case protocol == "" && transport == "":` branch to
a fourth, different "adapter runtime" session type with zero `WorkspaceDir`/`LogPath`
references anywhere. **None of go-agent-wrapper's shipped tests exercise the streaming-stdio,
jsonrpc-stdio, or serve-http runtime kinds at all** — the exact three kinds its own three real
adapters select.

**Finding 2 — three more real, load-bearing gaps, verified against live Nanite call sites:**
- `SessionIDPreset` — no `Config` field, never forwarded by `Run`. Needed for Claude's
  post-host-restart `--resume` flow (`go-providers/provider/pty_claude.go:311-313` appends
  `--resume <id>` when `cliSessionID != ""`, fed by `StartOptions.SessionIDPreset`; live
  Nanite caller `internal/service/chat_boot_drive.go:159`, CW-20260525-0001 Slice 3).
- `OnSessionID` — no `Config` field, never forwarded. This is the sole write path for
  `RuntimeStore.SetProviderSessionID` (Nanite's `agent.go:468-469`, `deps.go:169-172`), which
  is exactly what the `SessionIDPreset` resume flow above reads back on the next cold boot.
  **May not need a brand-new field** — `Wrapper.Run` already rebinds
  `Process.ProviderSessionID` on the `Activity` bridge's `Emitter` when it observes an
  `EventSessionID` stream frame (`wrapper.go:406-413`). Investigate whether a Nanite-side
  `runtimeevents.Sink` (which task `06` already has to build regardless, per its own "first
  real end-to-end consumer of go-runtime-events" framing) can observe this rebind indirectly
  from a subsequent emitted event's `Process.ProviderSessionID` field, without a new `Config`
  callback — this may be a narrower fix than the other three gaps.
- `AutoFireFirstTurn`/`FirstTurnPayload` — no `Config` field, never forwarded. Needed for
  every `ModeOneShot`/`ModeSubagent`/`ModeBackground` boot (`factory.go:133-140`'s
  `shouldAutoFireFirstTurn` returns `true` for all three) — these modes rely on the runtime
  auto-delivering the kickoff payload as the first turn. Without it, every subagent dispatch,
  background task, and one-shot boot spawns a process that never receives its actual
  instructions.

(Two other omissions from `Wrapper.Run`'s hardcoded `StartOptions` are confirmed
**non-load-bearing** for Nanite today, per task `06`'s own investigation — not in scope here:
`BootPrompt`/`BootMode` and `ExtraArgs`/`Supervisor`.)

## What to do

1. Add `Config.WorkspaceDir string` and/or `Config.LogPath string` (your call on which, or
   both — following the same pattern `agentsessions.StartOptions` itself already uses:
   `LogPath` if explicitly set, else derived from `WorkspaceDir`). If neither is set by the
   caller, consider synthesizing a default the same way `runPlanter` already defaults
   `BootDir` from `Workdir`+`.wrapper-boot/<sessionID>` when empty (e.g.
   `<Workdir>/.wrapper-workspace/<sessionID>`) — your call on whether an implicit default or a
   required-field error is the better shape for this library; document the choice and why.
2. Forward the new field(s) into the `StartOptions{}` literal in `Wrapper.Run`
   (`wrapper.go:342-381`).
3. Add `Config.SessionIDPreset string`, forwarded into `StartOptions.SessionIDPreset`.
4. Investigate whether `OnSessionID` genuinely needs a new `Config` callback field, or whether
   the existing `Process.ProviderSessionID` rebind-on-`EventSessionID` behavior
   (`wrapper.go:406-413`) already gives a caller everything it needs via its own
   `runtimeevents.Sink`. If a new field turns out to be genuinely needed, add
   `Config.OnSessionID func(string)` (or equivalent) and call it at the same point the rebind
   happens; if not, document clearly in your Work Log why the existing mechanism suffices, so
   task `06`'s worker doesn't have to re-derive this when it resumes.
5. Add `Config.AutoFireFirstTurn bool` and `Config.FirstTurnPayload string` (or equivalent),
   forwarded into `StartOptions`.
6. **Add real integration test coverage using each adapter's real, non-empty `Descriptor`**
   (`ProtocolClaudeStreamJSON`/`TransportStdio`, `ProtocolCodexAppServer`/`TransportStdio`,
   `ProtocolOpenCodeNative`/`TransportHTTPSSE`) — not the empty-Protocol/Transport fallback
   pair every existing test in `wrapper_integration_test.go` uses today, which is exactly why
   this gap went undetected in the first place. At minimum, confirm `Wrapper.Run` no longer
   hard-errors on `WorkspaceDir`/`LogPath` for all three real Protocol/Transport pairs.
7. Update `CHANGELOG.md` and cut a new version tag, following task `01`'s precedent — this is
   a real, non-trivial feature addition to `wrapper.Config`, not a patch-level bump. Use your
   judgment on the version number consistent with `v0.1.0`/`v0.2.0`'s own progression and
   document the call (no operator sign-off needed for a version number, per task `01`'s own
   precedent).

## Done means

- `wrapper.Config` has working seams for `WorkspaceDir`/`LogPath`, `SessionIDPreset`, and
  `AutoFireFirstTurn`/`FirstTurnPayload`, all correctly forwarded into `StartOptions`.
- A documented decision exists (in this task's Work Log) on whether `OnSessionID` needed a new
  field or was already covered by the existing rebind-on-event behavior.
- New integration tests exist that exercise `Wrapper.Run` against each of the three real
  shipped adapters' actual `Descriptor` values (not the empty fallback pair) and confirm no
  `WorkspaceDir`/`LogPath` hard-error.
- `go build ./...` / `go test ./...` clean in `libs/go-agent-wrapper`.
- A new version tag is cut, `CHANGELOG.md` updated.
- Task `06` (`TASKS/agent-host-acp/06-migrate-session-lifecycle-to-wrapper.md`) can then be
  re-dispatched exactly as originally scoped, unchanged, once Nanite's `go.mod` is bumped to
  the new tag (that bump is task `06`'s own job when it resumes, not this task's — this task
  does not touch Nanite at all).

## Work log (2026-08-21)

Worked directly in `libs/go-agent-wrapper` on top of `main` at `83524e0` (confirmed via
`git log --oneline -3` before starting — matches the branch/status this task file assumed).
No worktree used, per instructions. Read `wrapper/wrapper.go` in full, `agentkit/agentsessions/
types.go`'s full `StartOptions` field list, `streaming_stdio_session.go`, `jsonrpc_stdio_session.go`,
`serve_http_session.go`, `from_adapter.go`, and `adapters/{claude,codex,opencode}/*.go` and their
underlying `go-providers/provider/pty_{claude,codex,opencode}.go` before writing any code, plus
this batch's `README.md`, `docs/engineering/architecture/16-agent-host.md`, and
`docs/engineering/GLOSSARY.md` (no new vocabulary introduced — no glossary changes needed).

**1. `WorkspaceDir`/`LogPath` — added both, with an implicit default (not a required-field
error).** `Config.WorkspaceDir` and `Config.LogPath` are new optional `string` fields, forwarded
verbatim into `agentsessions.StartOptions.WorkspaceDir`/`LogPath`. When both are left empty,
`Wrapper.Run` synthesizes `<Workdir>/.wrapper-workspace/<SessionID>` as `WorkspaceDir` — mirroring
exactly how `runPlanter` already defaults `BootDir` from `Workdir`+`.wrapper-boot/<sessionID>`.
Chose the implicit default over a required-field error because `Config`'s own doc comment already
states a design contract ("zero values mean 'no planting', 'no sandbox profile'... the wrapper
degrades cleanly into a pure passthrough") that a required-field error would violate, and because
a required-field error would just reintroduce the same "caller must know agentkit internals"
problem this task exists to remove. Pulled the decision into a small pure function,
`resolveWorkspaceLogPath(workdir, sessionID, workspaceDir, logPath string) (string, string)`, so
it's independently unit-tested (`TestResolveWorkspaceLogPath`, 4 subcases) without spawning a
process, and wired that same function into `Run`.

**2. `SessionIDPreset` — added, forwarded as specified.** `Config.SessionIDPreset string` →
`StartOptions.SessionIDPreset`. Verified end-to-end against the *real* `adapters/claude` adapter
in the new integration test: with a real fake-`claude` binary capturing its own argv, confirmed
`--resume <id>` actually appears in front of the real `ClaudeAdapter.BuildArgs` output — not just
that the Config field exists.

**3. `OnSessionID` investigation — outcome revised from the task text's own tentative framing.**
The task's Context suggested `OnSessionID` "may not need a brand new field" since Claude's rebind-
on-`EventSessionID` already covers it. **Traced this per-adapter against the actual agentkit
source (not just Claude) and found the picture is adapter-specific, not uniform:**
  - **Claude (streaming-stdio)**: confirmed — `OnSessionID` and the `EventFanout` `EventSessionID`
    frame fire from the exact same observed line, same loop iteration
    (`streaming_stdio_session.go:360-368`). The pre-existing `Wrapper.Run` rebind (on the
    `EventFanout`-consumer goroutine) is genuinely sufficient here.
  - **Codex (jsonrpc-stdio)**: `CodexAdapter.ParseLine` *always* returns `(nil, nil)` in
    app-server mode by its own doc comment ("This adapter is a pass-through in app-server mode...
    do not 'fix' this by adding a JSON-RPC parser"), and `ParseLineEvents`'s switch never emits a
    session-id-carrying event for JSON-RPC-framed lines either (it's written for `codex exec`'s
    different JSON shape). Net result: **neither `OnSessionID` nor `EventFanout`'s `EventSessionID`
    ever fires for real Codex app-server sessions today, with or without a new `Config` field** —
    this is a pre-existing gap inside `agentkit`/`go-providers`, not something a `wrapper.Config`
    seam can fix, and out of this task's `Touches` (this repo only). Documented here so task `06`'s
    worker doesn't rediscover it from scratch; not fixed as part of this task.
  - **OpenCode (serve-http)**: two independent delivery paths exist.
    `serveHTTPSession.createSession` (the *primary*, first-session-creation path, called
    synchronously inside `Start()` from the initial `POST /session` HTTP response) calls
    `s.opts.OnSessionID(out.ID)` directly and **never** pushes a matching `EventFanout` frame
    (`serve_http_session.go:362-364`). The *secondary* SSE `session.created` path
    (`serve_http_session.go:449-459`) does fire both together, but that's not the path a normal
    first-session boot takes. **Net finding: for OpenCode specifically, the existing rebind
    mechanism was insufficient** — a Sink-only observer would never see the session id at all for
    the common case, because it never reaches `EventFanout`.

  **Given this, added `Config.OnSessionID func(string)` after all**, forwarded into
  `StartOptions.OnSessionID`. Rather than leaving the pre-existing `EventFanout`-driven rebind as
  the only rebind site, `Wrapper.Run`'s own `StartOptions.OnSessionID` implementation now
  unconditionally calls `w.cfg.Activity.Emitter().SetProviderSessionID(id)` (in addition to
  invoking the caller's own `Config.OnSessionID`, if set) — so a caller that only wires a
  `runtimeevents.Sink` (no direct callback) still gets full coverage across all three real
  adapters, including OpenCode's `createSession` path, without having to know about this
  adapter-specific quirk. Verified this exact fix with a precise integration-test assertion: the
  very first `session.ready` event emitted after a fake-OpenCode `Start()` already carries
  `Process.ProviderSessionID == "ses_fake_opencode"` — before this fix that field would have been
  empty at that point for every real OpenCode session, since `createSession` runs before `Start()`
  even returns and its result never touched `EventFanout`.

**4. `AutoFireFirstTurn`/`FirstTurnPayload` — added, forwarded as specified.**
`Config.AutoFireFirstTurn bool` + `Config.FirstTurnPayload string` (string, matching the task
text's own "or equivalent" phrasing and the pattern of `Config.BootPrompt`/`BootDir`; converted to
`[]byte` inline when building `StartOptions`). Verified end-to-end against real Claude and Codex
fake binaries: each captures whatever arrives on its stdin first, and the captured bytes match the
configured `FirstTurnPayload` exactly, proving the whole `Config → StartOptions → agentkit
`SendInput`-on-Start` chain works for real adapters, not just a fake one. Also verified against
real OpenCode: the fake HTTP server's `/session/{id}/prompt_async` handler receives exactly one
call with the configured payload as its `parts[0].text`.

**5. New integration test coverage (`wrapper/wrapper_real_adapters_test.go`, new file).** Added
three tests, one per real shipped adapter, each using the adapter's actual (non-empty)
`Descriptor` — `adapters/claude.New()` (`ProtocolClaudeStreamJSON`/`TransportStdio`),
`adapters/codex.New()` (`ProtocolCodexAppServer`/`TransportStdio`), `adapters/opencode.New()`
(`ProtocolOpenCodeNative`/`TransportHTTPSSE`) — routed at a fake binary via each adapter's real
env-var `Detect()` override (`CLAUDE_CLI_PATH`/`CODEX_CLI_PATH`/`OPENCODE_CLI_PATH`), not the
empty-Protocol/Transport fallback pair every pre-existing test in this repo uses (confirmed via
grep before writing these — genuinely zero prior coverage of streaming-stdio/jsonrpc-stdio/
serve-http). Each test goes beyond the "at minimum, no hard error" bar in the Done-means list:
  - Confirms `Run` returns with **no error at all** (the strongest form of "no hard error"), and
    that the synthesized `<Workdir>/.wrapper-workspace/<SessionID>/logs/session.log` file actually
    exists afterward — proving the default-synthesis path (item 1) works end-to-end against a real
    runtime kind, not just in the pure-function unit test.
  - Claude: asserts `--resume claude-resume-id` reached real argv (item 2), the configured
    first-turn payload reached the child's stdin verbatim (item 4), and the fake `system`/`init`
    session id reached `Process.ProviderSessionID` via the pre-existing rebind path (item 3, the
    "already sufficient for Claude" half of the finding).
  - Codex: asserts no hard error and first-turn stdin delivery (item 4); does **not** assert
    anything about `SessionIDPreset`/argv, since `CodexAdapter.BuildArgs` in app-server mode
    genuinely ignores its `cliSessionID` parameter (real code, not a test gap) — documented in the
    test's own doc comment so a future reader doesn't mistake the omission for an oversight.
  - OpenCode: asserts no hard error, the real `directory` query param and `prompt_async` body
    reached the fake HTTP server correctly (item 4), and — the specific, precise regression test
    for item 3's fix — that `session.ready`'s `Process.ProviderSessionID` is populated from
    `createSession`'s direct callback alone, with no SSE event in play.

  Ran the full suite with `-race` as well (`go test ./wrapper/... -race`) given the new
  `OnSessionID` closure runs on the adapter's own read/HTTP goroutine — clean.

**6. Version tag.** Cut `v0.3.0` (annotated tag, matching the `v0.1.0`→`v0.2.0` precedent's
format — not pushed to `origin`, matching `v0.2.0`'s own state: `git ls-remote --tags origin`
shows only `v0.1.0` on the remote before this work, and Nanite reaches this repo via a local
`go.mod` `replace` pointing at the sibling checkout, not the module proxy, so a local tag is
sufficient for task `06` to resume against once it also bumps the `require` line's version
string). Chose `v0.3.0` over a patch bump per the task's own instruction ("a real, non-trivial
feature addition... not a patch-level bump") and to keep the `v0.1.0`/`v0.2.0` minor-version
progression intact. **While drafting the `CHANGELOG.md` entry, found `HEAD` (`83524e0`) was
already 2 commits ahead of the `v0.2.0` tag** — commit `371c9d0` ("split Descriptor.Runtime into
Protocol/Transport, add InterruptCapability") landed *after* `v0.2.0` was tagged and had never
been changelogged. Followed the exact precedent `v0.2.0`'s own entry set for the same situation
(it backfilled an untagged pre-tag commit, `a248ab4`) and folded `371c9d0`'s changes into this
same `v0.3.0` entry under a dedicated "Changed (backfilled...)" subsection, rather than silently
dropping them from the changelog. Noted explicitly that `Descriptor.Runtime`'s removal (replaced
by `Protocol`+`Transport`) is a real breaking change to `Descriptor`'s literal shape — not
"additive" — acceptable pre-1.0 and moot in practice since `go-agent-wrapper` has zero adopters,
but inaccurate to describe as non-breaking, so corrected that framing rather than reusing
`v0.2.0`'s "no breaking change" language verbatim.

**Verification.** `go build ./...`, `go vet ./...`, `go test ./...` all clean (11 packages, all
passing, `-count=1`). `go test ./wrapper/... -race` also clean. All pre-existing tests
(~15+ integration tests using the empty-Protocol/Transport fallback adapter) pass unchanged — the
new `OnSessionID` closure and `WorkspaceDir`/`LogPath` defaulting are additive and don't alter the
fallback runtime's behavior (confirmed by inspection: `from_adapter.go` doesn't reference
`WorkspaceDir`/`LogPath` at all, and its own pre-existing `OnSessionID` call site is unaffected by
`Wrapper.Run` now supplying a non-nil callback where it previously supplied none).

**Files touched**: `wrapper/wrapper.go` (Config fields + `Run` forwarding + `resolveWorkspaceLogPath`
helper), `wrapper/wrapper_test.go` (`TestResolveWorkspaceLogPath`), `wrapper/wrapper_real_adapters_test.go`
(new — 3 real-adapter integration tests), `CHANGELOG.md` (new `v0.3.0` entry). Did **not** touch
`wrapper/wrapper_integration_test.go` despite it being listed in this task's own `Touches` line —
the new real-adapter tests didn't need anything from that file beyond its already-exported-within-
package helpers (`capturingSink`, `hasKind`, `indexOfKind`), which the new file reuses directly
since both live in `package wrapper`; a genuinely separate file kept the new real-adapter pattern
visually distinct from the existing fake-adapter test suite rather than growing an already-1600-line
file further.

**Commit**: `7c65601` on `libs/go-agent-wrapper`'s `main` (directly on top of `83524e0`, no
worktree/branch, per instructions). **Tag**: `v0.3.0` (annotated, local only — not pushed).

**Task `06` unblocked**: per this task's own Done-means, task `06` can now resume exactly as
originally scoped once it bumps Nanite's `go.mod` `require github.com/hollis-labs/go-agent-wrapper`
line to `v0.3.0` (the local `replace` already resolves to this repo's checkout on disk regardless
of the tag, so no other Nanite-side change is required for the bump itself). The Codex `OnSessionID`
gap documented in item 3 above is a real, separate, `agentkit`/`go-providers`-side limitation that
task `06`'s worker should be aware of but is not blocked by — Codex's provider-session-id capture
was already going to be a `RuntimeStore.SetProviderSessionID` no-op for that adapter either way,
pending a future fix in a different repo.

## Review notes

Fresh Reviewer (no shared context with this task's worker), 2026-08-21 — **PASS**, against
`libs/go-agent-wrapper` commit `7c65601` (tag `v0.3.0`). Independently re-verified against
the actual diff, not the Work Log's narrative: all four new `Config` fields correctly
forwarded into `StartOptions`, including the `resolveWorkspaceLogPath` synthesis helper and
the unconditional `OnSessionID` rebind of `Process.ProviderSessionID` (confirmed mutex-guarded
via `runtimeevents.Emitter.SetProviderSessionID`, safe under `-race`). Confirmed
`wrapper_real_adapters_test.go` genuinely drives `Wrapper.Run` against `claude.New()`/
`codex.New()`/`opencode.New()`'s real, non-empty `Descriptor` values — not the empty-fallback
pair that hid the original bug — and that each test asserts real end-to-end behavior (log file
on disk, real `--resume` argv, real first-turn stdin/HTTP delivery, rebound session ID on a
real event). Independently re-derived the `OnSessionID` investigation's OpenCode claim
directly against `agentkit/agentsessions/serve_http_session.go` (confirmed:
`createSession` calls `OnSessionID` with no adjacent `EventFanout` push) and its Codex claim
against `go-providers/provider/pty_codex.go` (confirmed: `ParseLine` unconditionally returns
nil in app-server mode, so neither mechanism fires — a real, pre-existing, correctly
out-of-scope gap). No scope creep — diff touches exactly what the task's `Touches` list named
plus one new test file reusing already-visible helpers, not `wrapper_integration_test.go`
itself as literally listed (reasonable deviation, not flagged as a problem).

One non-blocking heads-up, logged separately in `TASKS/ESCALATIONS.md`: `go test ./...`
surfaced an intermittent flake in `wrapper_integration_test.go`'s
`TestRunEndToEndAdapterRuntime` (~10-20% failure rate), independently confirmed via isolated
reproduction to predate this task (reproduces at a similar rate on the pre-`05a` base commit,
exercising only the empty-Protocol/Transport fake-adapter path this task never touches). Not
caused by this task; does not change the PASS verdict.

Task `06` is unblocked to re-dispatch as originally scoped.
