# Migrate session construction/lifecycle onto go-agent-wrapper's wrapper.Wrapper

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`)
**Status:** not-started — **blocked on `05a`. See `TASKS/ESCALATIONS.md` (2026-08-21, "Task
`06` (migrate session lifecycle to `wrapper.Wrapper`)…" + its Orchestrator resolution).
`wrapper.Wrapper.Run`, as shipped at the time this task was first attempted, was structurally
non-functional for all three of go-agent-wrapper's own real adapters
(`adapters/claude`/`codex`/`opencode`) — its hardcoded internal `agentsessions.StartOptions{}`
construction never set `WorkspaceDir`/`LogPath`, which every one of the
streaming-stdio/jsonrpc-stdio/serve-http runtime kinds hard-requires before spawning anything
(empirically reproduced, not just read). It also exposed no seam for
`SessionIDPreset`/`OnSessionID`/`AutoFireFirstTurn`/`FirstTurnPayload`, three more genuinely
load-bearing pieces of Nanite's current session lifecycle. **Operator-confirmed resolution**:
a new prerequisite task, `TASKS/agent-host-acp/05a-extend-wrapper-config-for-real-adapters.md`,
lands the missing `Config` seams in the sibling `go-agent-wrapper` repo. Once `05a` is
implemented, reviewed, and Nanite's `go.mod` is bumped to its new tag, this task resumes
**exactly as originally scoped below** — no other changes to this task's own brief.**
**Depends on:** `02` (Descriptor split), `04` (Planter migration), `05` (closed — see
correction below, its finding is folded into this task), `05a` (new prerequisite — must land
before this task can be re-attempted)
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

## Work Log (2026-08-21)

**Read before writing any code**, per this task's own risk profile (largest, highest-risk
task in the batch): `docs/engineering/architecture/16-agent-host.md`, this batch's
`README.md`, `TASKS/ESCALATIONS.md`'s task-05 entry (the sandbox-profile correction folded
into this task's Context above), all of `internal/runtime/agent/{agent.go,factory.go,
manager.go,deps.go}`, and — critically — `go-agent-wrapper`'s `wrapper/wrapper.go`,
`adapters/{adapter.go,runtime_adapter.go,claude/claude.go,codex/codex.go,opencode/opencode.go}`,
`plant/plant.go`, and the relevant parts of `agentkit/agentsessions` (`types.go`'s full
`StartOptions` field list, `streaming_stdio_session.go`, `jsonrpc_stdio_session.go`,
`serve_http_session.go`, `from_adapter.go`, `from_provider.go`) and `go-providers/provider`'s
real `BuildArgs` implementations for claude/codex/opencode.

**Verification method**: traced every field Nanite's current `agent.go:525-541` sets on
`agentsessions.StartOptions` against what `wrapper.Wrapper.Run`'s own hardcoded internal
`agentsessions.StartOptions{}` construction (`wrapper.go:342-381`) actually forwards, then, for
every field found missing, checked whether it's genuinely load-bearing today by grepping for
real (non-test) call sites in Nanite and reading the actual `provider.CLIAdapter.BuildArgs`
implementations each field feeds into — not just whether the field exists, but whether losing
it would change real behavior.

**Finding, in full, with exact evidence**: logged in `TASKS/ESCALATIONS.md`
(2026-08-21, "Task `06` (migrate session lifecycle to `wrapper.Wrapper`)…"). Summary:

1. `wrapper.Wrapper.Run` is **structurally non-functional for all three of go-agent-wrapper's
   own real shipped adapters** (`adapters/claude`, `adapters/codex`, `adapters/opencode`).
   Its hardcoded `agentsessions.StartOptions{}` literal never sets `WorkspaceDir` or
   `LogPath`; every real runtime kind those three adapters' `Describe()` implementations map
   to (streaming-stdio, jsonrpc-stdio, serve-http) hard-errors before spawning anything when
   both are empty. **Empirically confirmed**, not just read: wrote a throwaway test
   (`go-agent-wrapper/wrapper/zz_repro_logpath_test.go`, deleted immediately after use, never
   committed — `git status --short` clean in that repo before and after) using the wrapper's
   own fake-CLI-script test harness but with a real `adapters.ProtocolClaudeStreamJSON`/
   `adapters.TransportStdio` Descriptor pair (matching `adapters/claude`'s actual `Describe()`)
   instead of the empty pair every one of go-agent-wrapper's own integration tests uses. `Run`
   failed exactly as predicted: `wrapper: runtime.Start: agentsessions: streaming-stdio
   runtime requires StartOptions.LogPath or StartOptions.WorkspaceDir`. Root cause of why
   go-agent-wrapper's own test suite never caught this: every integration test uses a fake
   adapter with Protocol/Transport left unset, which routes to a fourth, different agentkit
   runtime kind (`from_adapter.go`'s plain per-turn adapter runtime) that has zero
   `WorkspaceDir`/`LogPath` references anywhere — none of go-agent-wrapper's shipped tests
   exercise streaming-stdio/jsonrpc-stdio/serve-http at all.
2. Three more real, load-bearing gaps confirmed against live Nanite call sites (not
   hypothetical): `SessionIDPreset` (Claude's post-host-restart `--resume` flow, live caller
   `internal/service/chat_boot_drive.go:159`), `OnSessionID` (the sole write path for
   `RuntimeStore.SetProviderSessionID`, which the above resume flow reads back from), and
   `AutoFireFirstTurn`/`FirstTurnPayload` (kickoff delivery for every `ModeOneShot`/
   `ModeSubagent`/`ModeBackground` boot). None have a `wrapper.Config` field or any other
   Nanite-reachable seam.
3. By contrast, confirmed two other omissions are **not** load-bearing, so not part of the
   blocking finding: `BootPrompt`/`BootMode` (already vestigial since task 04's Planter
   migration — Nanite's own code force-zeroes it for Claude, and Codex/OpenCode's real
   `BuildArgs` implementations explicitly ignore both params in the modes Nanite uses) and
   `ExtraArgs`/`Supervisor` (no live caller for the former since the boot-profile catalog's
   retirement; the latter is dead code today since `shouldUsePTY` always returns `false`).

**Given the task's own explicit Done-means bullet** ("Session construction... routes through
`wrapper.Wrapper.Run`/`SendInput`/`Stop`") **and its explicit Touches/Repo restriction**
("Touches: four Nanite files... Repo: Nanite") **are jointly unsatisfiable** — finding 1 has
no Nanite-side workaround at all (the missing values must be present inside `wrapper.go`'s own
hardcoded `StartOptions{}` literal, in the sibling repo, outside this task's authorization) —
this is exactly the "task file's own instruction is genuinely ambiguous/internally
contradictory once verified against the real code" category this project's escalation
criteria names as a real stop-and-escalate trigger, not a "correct the rationale, do the task
anyway" situation: the task's *decided action* itself (route through `Wrapper.Run`) is not
achievable as scoped, not just its supporting rationale.

**No code changes made.** `internal/runtime/agent/{agent.go,factory.go,manager.go,deps.go}`
are byte-identical to `main` — confirmed via `git status --short` (no diff). The throwaway
`go-agent-wrapper` repro test was deleted immediately after producing its evidence, confirmed
via `git status --short` in that repo (clean, "main ahead 4" only — no working-tree diff)
both before writing it and after removing it. `go build ./cmd/nanite/`, `go vet ./...`,
`go test ./...` were not re-run as part of this task's own verification since nothing in this
repo changed; the pre-existing baseline (established by tasks `01`-`05`) is unaffected by a
read-only investigation.

**Escalation logged:** `TASKS/ESCALATIONS.md`, entry dated 2026-08-21, "Task `06` (migrate
session lifecycle to `wrapper.Wrapper`)…" — includes the full evidence above plus three
concrete recommended paths for the Orchestrator (extend `go-agent-wrapper`'s `Config`/`Run`
surface as new cross-repo scope; descope this task to keep direct `StartOptions` construction
while adopting only the `adapters.RuntimeAdapter` resolve half; or land the library extension
as a new, small prerequisite task ahead of re-dispatching this one).

**Per this task's own instructions and this project's escalation discipline, stopping
here — not marking this task `implemented`.** Status left as `not-started — escalated` above.
Task `07` (dogfeed validation) depends on this task; the Orchestrator should decide the
recommended path before either `06` is re-dispatched or `07` is touched.
