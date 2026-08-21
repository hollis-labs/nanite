# Migrate session construction/lifecycle onto go-agent-wrapper's wrapper.Wrapper

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`)
**Status:** not-started
**Depends on:** `02` (Descriptor split), `04` (Planter migration), `05` (closed — see
correction below, its finding is folded into this task)
**Touches:** `internal/runtime/agent/agent.go`, `factory.go`, `manager.go`, `deps.go`. Repo:
Nanite. **Does not touch `internal/recovery/broker/*` directly — preserving its exact
contract is a hard constraint of this task, see Context and Done means.**

## Context

This is the largest task in the batch — the actual swap from Nanite's bespoke
`agentsessions.StartOptions` construction to go-agent-wrapper's `Adapter`→`Resolve`→
`wrapper.Wrapper.Run` flow.

**What Nanite does today, verified directly:**

`deps.go:34` — `Dependencies.SessionsManager *agentsessions.Manager` is the sole agentkit
session-manager handle. **No `LaunchPlan`/`CompiledLaunch`/`PreparedLaunch` type appears
anywhere in `internal/runtime/agent`** (grep across `agent.go`, `factory.go`, `deps.go`,
`manager.go` found none) — those are go-agent-wrapper/agentkit-internal abstractions Nanite's
current code doesn't use. What Nanite actually constructs: `agentsessions.StartOptions{...}`
directly (`agent.go:525-541` — `Workdir`, `WorkspaceDir`, `LogPath`, `BootPrompt`, `BootMode`,
`Env`, `Profile`, `SessionIDPreset`, `OnSessionID`, `Supervisor`, `AutoFireFirstTurn`,
`FirstTurnPayload`, `AttachEnabled`, `ExtraArgs`, `EventFanout`, `TypedEventCallback`), then
`deps.SessionsManager.Start(ctx, agentsessions.StartRequest{ID, Runtime, Options,
SessionMeta})` (`agent.go:550-554`). On success it builds Nanite's own `&Session{ID, Mode,
Provider, BootDir, WorkspaceDir, deps, startedAt, hadLineage}` (`agent.go:563-572`) — this
`agent.Session` type is Nanite's own, not agentkit's. `manager.go`'s methods on it are thin
wrappers: `SendInput` (`:21-33`) → `SessionsManager.SendInput`; `Stop` (`:39-60`) →
`SessionsManager.Stop`; `Wait` (`:64-72`) → `SessionsManager.WaitSession`; `Checkpoint`
(`:80-86`) is a stub (the lib doesn't expose `CheckpointHints` at the Manager level).

**What go-agent-wrapper does instead**: `wrapper.Wrapper.Run(ctx) error` (`wrapper.go:197-
453`) type-asserts a `RuntimeAdapter`, maps the (now-split, per task `02`)
`Descriptor.Protocol`/`Transport` → `agentsessions.Capabilities`, calls
`agentsessions.NewFromAdapter` → `Prepare` → `Start` (lines 244-384), fans
`llmtypes.StreamEvent` through translation (`wrapper/event_translator.go`, invoked at
`wrapper.go:415`), and blocks on `session.Wait()` internally. `SendInput`/`Stop` are thin
wrappers (`wrapper.go:460-475`, `482-491`) that emit `runtimeevents` activity around the
underlying `agentkit` session call. This is a materially different call shape than Nanite's
direct `StartOptions` construction — the migration is a real rewire of the call path, not a
drop-in swap.

**Hard constraint: `internal/recovery/broker`'s contract must not change shape.** Verified
directly: broker's coupling to this code is narrow but real —
- `types.go:37` — `FailureEvent.Exit *agentsessions.ExitError`.
- `broker.go:138` — `OnRestart(sessionID string, attempt int, prevExit *agentsessions.ExitError)`.
- `orchestration.go:44` — `OnSessionExit(sessionID string, exit *agentsessions.ExitError, meta map[string]any)`.
- `orchestration.go:285` — `buildFailureEvent(sessionID string, exit *agentsessions.ExitError, ...)`.
- `classifier.go:51,57,63,69,75` — switches directly on `xe.Cause` against
  `agentsessions.CauseIdleTimeout`/`CauseWatchdogKill`/`CauseOOMKill`/
  `CauseRestartExhausted`/`CauseResourceLimit`; also reads plain `xe.Code`/`xe.Signal` ints.
- `envelope.go:84,86,115` — same `Cause*` constants, chosen message copy.
- `deps.go:75` — broker's dispatch path calls `AgentBoot.Boot(ctx, agent.Options) (*agent.Session, error)` — Nanite's own types, not agentkit's directly.
- `broker.go:327` — `DispatchRetry` constructs `agent.Options{...}` and calls
  `b.deps.AgentBoot.Boot(ctx, ...)`.
- `broker.go:342` — `agent.HasBootdirLayout(ev.Provider)`.

Broker never calls an agentkit method directly and never constructs `LaunchPlan`/session
types itself — every real coupling point is either (a) the shape of `*agentsessions.ExitError`
(a plain data struct with `Cause`/`Code`/`Signal` fields — unaffected by this task, since this
migration doesn't touch how exits are classified, only how sessions are started/stopped) or
(b) Nanite's own `agent.Options`/`agent.Session`/`agent.HasBootdirLayout` surface. **As long
as `agent.Options`, `agent.Session`'s public shape, and `agent.HasBootdirLayout`'s signature
are unchanged after this migration, broker requires zero changes.** If this task's
implementation genuinely can't preserve that surface, expanding scope to update broker's 8
cited call sites is acceptable (per `EXECUTION-PROCESS.md` worker step 7 — correction, not
escalation, if the underlying decision still holds) — but changing broker without a documented
reason is a signal something went wrong, not a normal part of this task.

**Mode stays entirely product-owned, unchanged.** `agent.Mode` (`agent.go:23`, values at
`:29,34,39,44,50`) and every lifecycle decision it drives —
`agent.go:204-210` (validation: `ModeSubagent` requires `ParentSessionID`, `ModeResume`
requires `ResumeFromCheckpoint`), `:361` (path-grant lineage, `ModeSubagent`-only), `:430-446`
(session-ID presets/PTY caps by mode), `:485-486` (`ModeOneShot` first-turn override), `:577-
589` (auto-fire-first-turn logic); `factory.go:72` (`shouldUsePTY`), `:114`
(`shouldUseStreamingStdio`), `:133` (`shouldAutoFireFirstTurn`) — are not touched by this
task. These decisions continue to determine *which* adapter/`StartOptions`-equivalent gets
built; only the mechanism that turns that decision into a running session moves onto
`wrapper.Wrapper`.

**No mid-turn interrupt exists today, in either the current code or go-agent-wrapper — this
is a known, carried-forward limitation, not something this task needs to fix.**
`internal/service/agent_deps.go:772-776` already has an explicit TODO: "claude-code's PTY
surface doesn't expose a mid-turn interrupt today... Phase 5+ adds `Session.Interrupt(ctx)`
once go-agent-sessions surfaces a non-blocking interrupt." Per task `02`'s findings,
go-agent-wrapper's `Stop()` has the identical ceiling for Claude/Codex today. Migrating onto
`wrapper.Wrapper` does not regress this — `Session.Stop()`'s current SIGTERM/SIGKILL-only
behavior (`manager.go:39-60`) carries forward with equivalent semantics via
`wrapper.Wrapper.Stop`.

**This is also the first real, end-to-end consumer of `go-runtime-events` inside any app** —
16-agent-host.md's own text says the vocabulary "shipped 2026-05-26 and nothing today consumes
it end-to-end inside an app." Wiring `wrapper.Wrapper`'s activity translation
(`wrapper/event_translator.go`) into Nanite's existing event/telemetry consumption is real,
novel integration work, not a mechanical swap — budget real attention for it, and flag
anywhere Nanite's existing event handling assumes a shape `go-runtime-events` doesn't provide.

**Correction from task `05`'s escalation (`TASKS/ESCALATIONS.md`, 2026-08-21) — read before
step 2 below.** Task `05` found `sandbox.Applier.Apply(ctx, pid)` is a true post-spawn
attach-by-pid mechanism (confirmed against `wrapper.go`'s real call ordering: `Config.Sandbox`
only runs via `runSandbox` *after* `runtime.Start` returns) — it does **not** fit
`buildSandboxProfile`'s pre-spawn model, and task `05` was closed with no migration and no
code changes. There is a *separate*, already-existing `wrapper.Config` field,
`SandboxProfile sandboxprofile.Profile` (`wrapper.go:105-108`), that *is* the correct pre-spawn
seam — it's forwarded directly into `agentsessions.StartOptions.Profile` inside
`Wrapper.Run` (`wrapper.go:347`), the same semantic Nanite already relies on today via
`StartOptions.Profile` directly. **`buildSandboxProfile`'s own logic needs zero changes** —
this task's own job (building `wrapper.Config` in place of `StartOptions`) already covers
routing its return value into `Config.SandboxProfile` instead of `StartOptions.Profile`
directly; that's a one-field rewire, not a new migration surface. Do not attempt to route
sandboxing through `Config.Sandbox`/`sandbox.Applier` — that's the wrong seam per `05`'s
finding.

## What to do

1. Implement `adapters.RuntimeAdapter`/`Adapter` for Claude/Codex/OpenCode on the Nanite side
   (or confirm the shipped `go-agent-wrapper/adapters/{claude,codex,opencode}` packages are
   usable as-is — check whether they need Nanite-specific `ResolveContext` wiring or can be
   used directly).
2. Replace `agent.go:525-554`'s direct `agentsessions.StartOptions` construction +
   `SessionsManager.Start` call with a `wrapper.Wrapper.Run` invocation, feeding it whatever
   `ResolveContext` the chosen `RuntimeAdapter` needs (boot dir, env, workdir — reusing task
   `04`'s migrated planting; sandbox profile — `buildSandboxProfile`'s existing, unmigrated
   return value fed into `wrapper.Config.SandboxProfile`, per the correction above, not
   `Config.Sandbox`).
3. Replace `manager.go`'s `SendInput`/`Stop`/`Wait` implementations to call
   `wrapper.Wrapper.SendInput`/`Stop`/(internal `Run`-blocking equivalent to `Wait`) instead
   of `SessionsManager` directly, while keeping `agent.Session`'s own public method
   signatures unchanged for every existing caller (broker included).
4. Wire `wrapper.Wrapper`'s activity/event translation into whatever Nanite already does with
   session activity (SSE streaming to chat, tool-use tracking, etc.) — trace the existing
   `EventFanout`/`TypedEventCallback` consumers in the current `StartOptions` construction
   (`agent.go:525-541`) to find every place that needs rewiring.
5. Do not touch `internal/recovery/broker/*` unless step 2's or 3's implementation genuinely
   changes `agent.Options`/`agent.Session`'s public shape or `agent.HasBootdirLayout`'s
   signature — if it does, update broker's 8 cited call sites and document exactly why in
   your Work Log.
6. Leave `Mode` and every cited lifecycle-decision call site completely unchanged.

## Done means

- Session construction for Claude/Codex/OpenCode routes through `wrapper.Wrapper.Run`/
  `SendInput`/`Stop` instead of direct `agentsessions.StartOptions`/`SessionsManager` calls.
- `agent.Mode` and every cited lifecycle-decision site is byte-identical to before this task
  (verify via diff, not just "tests pass").
- `internal/recovery/broker`'s 8 cited call sites either compile unchanged against
  `agent.Options`/`agent.Session`/`agent.HasBootdirLayout`, or (if genuinely required) are
  updated with an explicit, documented reason in this task's Work Log.
- Activity/event data (chat streaming, tool-use tracking) flows correctly through the new
  `wrapper.Wrapper`-mediated path — covered by a real test exercising at least one full
  session lifecycle (start → activity events → stop/exit), not just a compile check.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` clean.
- Real end-to-end verification is task `07`'s job, not this one's — this task's own tests can
  use fakes/mocks per existing convention (`fakes_test.go`), but should not claim live-dogfeed
  verification in its Work Log.
