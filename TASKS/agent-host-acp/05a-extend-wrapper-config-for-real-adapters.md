# Extend go-agent-wrapper's wrapper.Config for real (non-empty-Descriptor) adapters

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`), landing in the sibling
`libs/go-agent-wrapper` repo — same cross-repo shape as Phase 1's `01`/`02`, inserted as a
prerequisite once task `06` discovered it needed this.
**Status:** not-started
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

## Work log

<Worker fills this in as it goes.>

## Review notes

<Reviewer fills this in.>
