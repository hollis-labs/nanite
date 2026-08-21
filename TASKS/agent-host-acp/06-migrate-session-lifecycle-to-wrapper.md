# Migrate session construction/lifecycle onto go-agent-wrapper's wrapper.Wrapper

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`)
**Status:** implemented — re-dispatched after `05a` landed and reviewed PASS; see the fresh
Work Log entry dated 2026-08-21 (second entry) at the bottom of this file for the actual
implementation, including one more real, in-repo-workaroundable gap found beyond `05a`'s scope
(`wrapper.Config` has no `Env` seam — worked around via a per-session shell-script wrapper, no
`libs/go-agent-wrapper` change needed). The banner below is preserved as history from the first
attempt, which correctly stopped rather than forcing a workaround for the `WorkspaceDir`/
`LogPath`/`SessionIDPreset`/`OnSessionID`/`AutoFireFirstTurn` gaps `05a` then fixed.
**Status (superseded, kept for history):** not-started — **blocked on `05a`. See `TASKS/ESCALATIONS.md` (2026-08-21, "Task
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

## Work Log (2026-08-21, re-dispatch after `05a` — actual implementation)

Read this task's own brief in full (with the `05`/`05a` corrections already folded in),
`docs/engineering/architecture/16-agent-host.md`, `TASKS/agent-host-acp/README.md`, the `05a`
Work Log + Review notes, `TASKS/ESCALATIONS.md`'s task-05/06 entries, `docs/engineering/
GLOSSARY.md` (no new vocabulary needed), and — line by line — `internal/runtime/agent/
{agent.go,factory.go,manager.go,deps.go}`, `go-agent-wrapper`'s `wrapper/wrapper.go`, `wrapper/
runtime_dispatch.go`, `wrapper/event_translator.go`, `adapters/{adapter.go,runtime_adapter.go,
claude,codex,opencode}`, `plant/plant.go`, `activity/bridge.go`, `go-runtime-events/
runtimeevents/{schema.go,kinds.go,sink.go,emitter.go}`, and the relevant `agentkit/
agentsessions` internals (`manager.go`, `streaming_stdio_session.go`, `from_adapter.go`,
`types.go`) and `go-providers/provider/{pty_claude.go,pty_codex.go,pty_opencode.go,
cli_adapter.go}`.

### 0. go.mod bump

`require github.com/hollis-labs/go-agent-wrapper` bumped `v0.2.0` → `v0.3.0`. `go mod tidy`
resolved cleanly via the existing local `replace` — no `go.sum` change (replace-resolved, no
checksum needed). Two new *transitive* deps this task's own new code now imports directly
(`go-runtime-events`, `go-harness-filters`, both already go-agent-wrapper deps) needed their
own local `replace` directives added to Nanite's `go.mod`, mirroring the existing go-agent-
wrapper/go-envelopes/go-modelsdev precedent: their module-proxy-published `v0.1.0` tags are
stale relative to the local sibling checkouts go-agent-wrapper was actually built against —
confirmed empirically, not assumed: building without the replace failed with `undefined:
hrepair.Chain` inside `go-agent-wrapper/filters`, a symbol present in the local `libs/go-
harness-filters` checkout but absent from the proxy-published tag. Documented inline in
`go.mod` with the exact repro.

### 1. `adapters.RuntimeAdapter`/`Adapter` wiring — Nanite-side, not the shipped packages

Verified directly (not assumed) that go-agent-wrapper's shipped `adapters/{claude,codex,
opencode}` packages are **not** usable as-is for two of the three providers, so built a
Nanite-side `adapters.Adapter`/`RuntimeAdapter` implementation instead
(`internal/runtime/agent/wrapper_adapter.go`, new file, `nativeAdapter`):

- **Claude**: the shipped `adapters/claude.Adapter.CLIAdapter()` always constructs a fresh
  `provider.NewClaudeAdapterStreamingStdio()` — losing `cmd/nanite`'s dev-mode
  `provider.NewClaudeAdapterDevStreamingStdio()` (`SkipPermissions=true`) variant, which
  `Dependencies.ProviderAdapter` already resolves correctly per-process. `nativeAdapter`
  wraps whatever `provider.CLIAdapter` `deps.ProviderAdapter(providerName)` actually
  resolved, preserving this.
- **Codex/OpenCode**: the shipped `adapters/codex`/`adapters/opencode` packages declare
  `Descriptor{Protocol: ProtocolCodexAppServer/ProtocolOpenCodeNative, Transport: TransportStdio/
  TransportHTTPSSE}`, which `wrapper.runtimeCaps` maps to `Capabilities{JsonRpcStdio:true}` /
  `Capabilities{ServeHTTP:true}` — a long-lived app-server / HTTP+SSE runtime shape. `cmd/
  nanite` registers `provider.NewCodexAdapter()`/`provider.NewOpencodeAdapter()` (`Mode=""`,
  i.e. exec mode), and `factory.go`'s `runtimeConfigForAdapter` sets no lifecycle `Caps` flag
  for either provider today (`shouldUseStreamingStdio` only matches claude, `shouldUsePTY`
  always false) — i.e. Nanite runs Codex/OpenCode as agentkit's subprocess-per-turn "adapter
  runtime" today, a materially different runtime shape than the shipped adapters would select.
  Adopting the shipped packages as-is would have silently changed Codex/OpenCode's spawn shape
  — explicitly out of this task's scope (`factory.go`'s decision sites, Mode/lifecycle policy,
  are product-owned and untouched by this task).

`nativeAdapter`'s `Descriptor.Protocol`/`Transport` are derived from `runtimeConfigForAdapter`'s
own `Capabilities` output (`protocolTransportFromCaps`) — i.e. `agent.go` still calls the
exact same, byte-identical `runtimeConfigForAdapter(adapter, providerName, opts.Mode)` it
always did (just no longer feeds the result into `agentsessions.NewFromAdapter` directly),
so there is zero drift risk between the "which runtime shape" decision and the wrapper's own
Protocol/Transport selection — one source of truth, not two.

### 2. A genuine third gap beyond `05a`'s four, found and worked around entirely in this repo

Before wiring `wrapper.Config`, traced every field the pre-migration `StartOptions` literal
set (`agent.go:525-541`, per this task's own Context) against what `Wrapper.Run`'s hardcoded
`StartOptions{}` literal forwards **after `05a`'s fix** (not before) — the same verification
method the original escalation used, applied a second time against the *post-05a* code, not
just re-trusting `05a`'s own "four gaps, all now closed" framing. Found `Env` is **not** among
the fields `05a` added, and confirmed it is genuinely load-bearing:

- `agentsessions.StartOptions.Env` empty → every runtime kind falls back to
  `cmd.Env = os.Environ()` (confirmed directly in `agentkit/agentsessions/
  streaming_stdio_session.go:228-231` and the identical fallback in `from_adapter.go`) — the
  spawned child inherits the **Nanite daemon's own process environment**, not the per-session
  composed env `composeEnv` + `Layout.AmendEnv` builds.
- Traced each provider's `AmendEnv`: `claudeLayout.AmendEnv` is a no-op (Claude's planted-file
  discovery is cwd-based — `SpawnWorkdir` returns `bootDir`, harmless to lose). But
  `codexLayout.AmendEnv` sets `CODEX_HOME=<bootDir>` and `opencodeLayout.AmendEnv` sets
  `OPENCODE_CONFIG_DIR=<bootDir>` — the **sole** mechanism redirecting those CLIs at their
  planted, sandboxed `config.toml`/`opencode.json` (`approval_policy`, `sandbox_mode`,
  `writable_roots`, `.mcp.json`, hooks). Without it, a wrapper-driven Codex/OpenCode session
  would silently fall back to the **operator's real, global** `~/.codex`/`~/.config/opencode`
  config — a sandbox-restriction bypass, not merely a missing feature.
- Confirmed `wrapper.Config` has no `Env` field at all (`wrapper.go`'s full `Config` struct,
  re-read line by line) and that `adapters.Adapter.Resolve`'s returned `Spec.Env` is
  explicitly discarded by `Wrapper.Run` ("informational... this path", the doc comment on
  `Run` itself) — there is no existing seam anywhere in `wrapper.Config`/`Wrapper.Run` for
  this, at `v0.3.0`.

This is structurally the same shape as the original escalation (a real library gap this
task's own `Touches` doesn't authorize fixing in `libs/go-agent-wrapper`) — but unlike the
original `WorkspaceDir`/`LogPath` gap (which failed *inside* agentkit before any Nanite-
controlled code got a chance to run), this one has a genuine, safe, in-scope workaround, so it
did not rise to the "stop and escalate" bar. Two workarounds were considered and rejected
first:

- `os.Setenv` on the Nanite daemon process itself — unsafe: Nanite spawns concurrent sessions
  with different boot dirs from multiple goroutines; a process-wide env mutation races across
  sessions and can leak one session's `CODEX_HOME` into another's spawn.
- Threading env through `provider.CLIAdapter.BuildArgs` as a CLI flag — `provider.CLIAdapter`
  has no env-contribution method, and Codex/OpenCode's config-dir selection is env-var-only,
  not a documented per-invocation flag.

**Workaround implemented**: `wrapEnvForSpawn`/`writeEnvWrapperScript`/`envWrappedCLIAdapter`
(`wrapper_adapter.go`). `agent.Boot` writes a small per-session POSIX `sh` script under
`<bootDir>/.wrapper-exec/<provider>.sh` (0o700 — the script embeds the composed env verbatim,
which may include provider credentials) that does `exec env -i KEY='val' ... /real/binary
"$@"` — clearing whatever `os.Environ()` fallback agentkit hands it, then re-exec'ing the real
binary with exactly the composed env, byte-identical semantics to the pre-migration direct
`StartOptions.Env` path. A wrapping `provider.CLIAdapter` (`envWrappedCLIAdapter`, embeds the
real adapter, overrides only `Detect()`) points agentkit's own binary resolution at the script
instead of the real binary — confirmed directly against `agentkit/agentsessions/
streaming_stdio_session.go:51,204-225` that `Detect()` is genuinely the *only* seam agentkit's
Prepare-preflight and real `exec.Command` call both go through, so this interception is
complete. Falls back to the unwrapped adapter (no script written) when the real `Detect()`
itself fails, preserving the pre-migration "binary not found" failure shape. Applied uniformly
to all three providers (not special-cased to Codex/OpenCode) for one code path and byte-exact
env parity across the board, including Claude.

`TestBoot_WrapperLifecycle_Codex_EnvParity` (new) proves this end-to-end against a real fake
`codex` subprocess: asserts the value the *spawned child itself observed* for `$CODEX_HOME`
equals the session's real boot dir exactly, not just that the script exists.

### 3. `SessionIDPreset`/`OnSessionID` and `AutoFireFirstTurn`/`FirstTurnPayload` — genuinely wired, not just compiled

`wrapper.Config.SessionIDPreset` ← the same `sessionIDPreset` local `agent.go` already
computed (ModeResume checkpoint / `ResumeProviderSessionID` precedence, byte-identical logic,
untouched). `wrapper.Config.OnSessionID` ← the exact same `onSessionID` closure that used to
be `StartOptions.OnSessionID`, still calling `deps.Store.SetProviderSessionID` — `05a`'s own
unconditional `Process.ProviderSessionID` rebind covers the three previously-uneven per-
adapter delivery paths (Claude/OpenCode fire it; Codex's app-server-only gap doesn't apply
since Nanite doesn't use codex app-server mode). `wrapper.Config.AutoFireFirstTurn`/
`FirstTurnPayload` ← `shouldAutoFireFirstTurn(opts.Mode)` (untouched) and the same NDJSON-
framed-for-streaming-stdio `firstTurnPayload` byte slice the pre-migration code built (CW-
20260516-0007's framing logic preserved verbatim; only the two BootPrompt/BootMode
suppression steps that comment used to also describe are dropped as genuinely moot — see
`04` below). `TestBoot_WrapperLifecycle_Claude` proves `SessionIDPreset`/`OnSessionID`
end-to-end (asserts `store.provIDs[sess.ID] == "claude-fake-session-1"` from a real fake-
claude `system/init` frame) and `AutoFireFirstTurn`/`FirstTurnPayload` end-to-end for both
Claude (implicitly — the kickoff has to reach the fake script for it to produce any output at
all) and Codex (`TestBoot_WrapperLifecycle_Codex_EnvParity`, same reasoning).

### 4. Fields confirmed non-load-bearing, dropped (widening the original escalation's own finding-3 list)

`BootPrompt`/`BootMode`/`ExtraArgs`/`Supervisor` were already confirmed non-load-bearing by
the original escalation (finding 3) — `wrapper.Config` has no fields for any of them, and
nothing downstream needed them dropped. Independently re-confirmed `Supervisor` is dead code
today (`opts.Mode == ModeLongLived && runtimeCfg.Caps.PTY` is never true — `shouldUsePTY`
always returns false) before deleting its construction block (kept `deps.Telemetry`/
`deps.Recovery.OnRestart` themselves untouched — genuinely unused seams for a future PTY
adapter, not this task's concern). Additionally grepped for `StartOptions.AttachEnabled`'s
only real Nanite consumer (`Manager.Attach`/`AttachWith`) and found **zero** call sites
anywhere in the repo — safe to drop alongside the others (same "audited via grep for real
call sites" method the original escalation used, extended to one more field it didn't check).
`metaToStringMap`/`StartRequest.SessionMeta` also confirmed dead (only ever fed
`agentsessions.Manager`'s in-memory `SessionInfo.Meta`, itself never read anywhere in Nanite)
— removed; `RuntimeRow.Meta` (the actually-read, DB-persisted meta bag) is untouched.

### 5. Activity/event translation — reverse-mapped onto the exact pre-migration surfaces

`wrapper.Wrapper.Run` consumes the raw `llmtypes.StreamEvent`/`provider/events.Event` streams
*itself* internally and re-emits a normalized `runtimeevents.Event` stream via
`Config.Activity` — Nanite never sees the raw streams when going through `Wrapper.Run`. Built
`runtimeEventSink` (`wrapper_sink.go`, new file), a `runtimeevents.Sink` implementation that
reverse-translates each event `Kind` back onto whichever pre-migration surface actually
produced the equivalent chat SSE, not necessarily the same raw wire surface the data
originated from:

- `KindAgentDelta` (payload has `content` xor `thinking`) → `EventFanout` as
  `llmtypes.EventDelta`/`EventThinking`.
- `KindAgentToolUse` → **`TypedEventCallback`** as `events.ToolUse` (not `EventFanout` as
  `llmtypes.EventToolUse`) — this is the one place the reverse mapping deliberately does *not*
  mirror wrapper's own forward mapping 1:1: `wrapper.translateStreamEvent` produces
  `KindAgentToolUse` from `EventFanout`'s `llmtypes.EventToolUse`, but Nanite's own
  `agentEventBridge.translateStreamEvent` (unmodified, `internal/service/agent_deps.go`)
  *deliberately drops* `llmtypes.EventToolUse` on the `EventFanout` side ("also surface via
  TypedEventCallback... skip here to avoid double-emission") — replaying it back onto
  `EventFanout` would have silently dropped the tool_call SSE entirely, a real regression.
  Routing it onto `TypedEventCallback` instead is what makes tool-use tracking actually work
  post-migration. `TestBoot_WrapperLifecycle_Claude` is the direct regression test for this
  (asserts a real `events.ToolUse{ID:"tu_1",Name:"Read",...}` reaches the typed callback from
  a real fake-claude `assistant`/`tool_use` content block, not a mock).
- `KindAgentToolResult`/`KindAgentSubagentSpawn` → `TypedEventCallback` as
  `events.ToolResult`/`events.SubagentSpawn` (mirrors `wrapper.translateProviderEvent`'s own
  forward mapping — these two never had the double-emission concern above).
- `KindTurnCompleted` (payload has `usage` or is empty) → `EventFanout` as
  `llmtypes.EventUsage`/`EventDone`.
- `KindTurnFailed` → `EventFanout` as `llmtypes.EventError`.
- Everything else (session/process lifecycle, plant, sandbox, policy, raw IO) — no-op; none of
  these had an `EventFanout`/`TypedEventCallback` equivalent pre-migration either.

Doubles as the Boot-readiness signal: `wrapper.Wrapper.Run` sets its internal session handle
immediately before unconditionally emitting `KindSessionReady` (every runtime kind), so
observing that `Kind` in the Sink (`onReady` callback, `sync.Once`-guarded) is exactly the
point `SendInput`/`Stop` become safe — `Boot` blocks on it (`readyCh`) before ever returning a
`*Session`.

**Known, pre-existing, out-of-scope gap noted but not fixed** (confirmed via code, not
assumed): `wrapper.translateProviderEvent` has no case for `provider/events.Thinking` — if a
provider only emitted that (not the paired `llmtypes.EventThinking`), it would be silently
dropped by the wrapper itself before ever reaching this Sink. Not something a Nanite-side
`runtimeevents.Sink` can fix (nothing to observe) and not exercised by any real Nanite call
path today (both Claude paths this task tested emit `llmtypes.EventThinking`, not the provider-
events variant).

### 6. `Session` construction/lifecycle — the actual rewire, plus three cascading corrections it forced

`wrapper.Wrapper.Run(ctx)` is fully synchronous and blocks for the session's *entire* lifetime
(constructs the agentkit runtime, starts it, translates events, blocks on `session.Wait()`,
only then returns) — a materially different call shape than `SessionsManager.Start`, which
returned once the runtime was merely registered. `agent.Boot` now runs `wr.Run(runCtx)` on a
background goroutine (detached from Boot's own request-scoped `ctx` — a `ModeLongLived` chat
session must outlive it), signals readiness via the Sink's `onReady`/`readyCh`, and blocks on a
three-way `select` (ready / run-finished-before-ready / Boot's own ctx cancelled) before ever
returning a `*Session`. `Session` gained three new **unexported** fields (`wr *wrapper.Wrapper`,
`runDone chan struct{}`, `runErr error`) — public shape (`ID`/`Mode`/`Provider`/`BootDir`/
`WorkspaceDir`) is byte-identical, confirmed via diff, not just tests. `manager.go`'s
`SendInput`/`Stop`/`Wait`/`Checkpoint` now call `s.wr.SendInput`/`s.wr.Stop`/(select on
`s.runDone`)/(unchanged stub) instead of `Dependencies.SessionsManager`, with identical nil-
guard error strings so `TestSession_NilGuards` needed zero changes.

`Session.Wait`'s error propagation is the load-bearing seam for broker compatibility:
`agentsessions.Session.Wait()` (confirmed directly, e.g. `streaming_stdio_session.go:659-667`)
genuinely returns a `*agentsessions.ExitError` on an abnormal exit; `Wrapper.Run` wraps it
with `%w` ("wrapper: session exited with error: %w"), never replacing it; `Session.Wait` here
returns that same error chain untouched. `errors.As(err, &xe)` at
`internal/service/chat_boot_drive.go:467` (the Wait-observer goroutine that feeds
`internal/recovery/broker`) still unwraps correctly through the `%w` chain — verified by
reading the unwrap chain end-to-end, not assumed.

**Real bug found and fixed during this task's own testing, not by the reviewer**: the first
`Session.Stop` implementation called the background goroutine's `runCancel()` immediately
after `wr.Stop(ctx)` returned, as a "belt-and-suspenders" safety net. `TestBoot_WrapperLifecycle_
Codex_EnvParity` caught this converting an otherwise-clean cooperative stop into a spurious
`context.Canceled` `Wait` error: `agentsessions.adapterSession.Wait()` (Codex's runtime kind)
always returns a **nil** error on its own, so `Wrapper.Run`'s tail end falls back to checking
`ctx.Err()` as its result whenever the underlying `Wait()` reports nil — cancelling `runCtx`
from `Stop` raced directly into that fallback check, corrupting the exit result on every clean
stop, not just Codex's. Fixed by moving `runCancel` to fire only via `defer` inside the
background goroutine itself (strictly after `wr.Run` has already returned and computed
`sess.runErr` — never racing the check that produced it) and removing it from `Session.Stop`
entirely — `wr.Stop`'s own `ctx` parameter (caller-bounded) is the correct, sufficient
interrupt mechanism on its own; the extra cancel was actively harmful, not just redundant.
Confirmed the fix against all three lifecycle tests, `-race` clean.

**Also discovered while implementing, and fixed as necessary corollary work (not originally
named in this task's own Done-means, but squarely "session lifecycle" — this task's own
title) — `Wrapper.Run` bypasses `agentsessions.Manager` entirely** (constructs its agentkit
runtime directly via `agentsessions.NewFromAdapter`, never calls `Manager.Start`). Traced the
concrete consequences against real, live-mileage consumers before deciding these needed
fixing, not just noting them:

- **`agent_runtime.state` would freeze at `"launching"` forever.** Pre-migration,
  `agentsessions.Manager`'s `StateSink` (`agentRuntimeStateSink` → `store.SetAgentRuntimeState`)
  drove every `launching → running → done|failed` transition automatically from
  `Manager.Start`/`Manager.watch`. Bypassing the Manager means nothing calls it anymore.
  **Fix**: added `RuntimeStore.UpdateState(runtimeID, state string, pid int) error` to the
  `agent.RuntimeStore` interface (`deps.go`), implemented in `internal/service/agent_deps.go`'s
  `agentRuntimeStore` (delegates to the same pre-existing `store.SetAgentRuntimeState`), and
  called directly from `agent.Boot`/`Session`'s own completion path at the same two points the
  Manager used to (`"running"` once `readyCh` fires, `"done"`/`"failed"` once `wr.Run`
  returns) — `pid` is persisted as `0` uniformly (see next bullet for why this is safe, not a
  gap).
- **`orphansweep`'s `PID==0` liveness fallback (`Dependencies.LiveSessions`) would go blind for
  every wrapper-driven session, not just Codex/OpenCode's usual pid=0 case.** Verified directly
  against `classifyForReconciliation` (`internal/recovery/orphansweep/orphan_sweep.go`): for
  `PID>0` rows it signal-0-probes the OS directly (unaffected by this migration at all — this
  is why persisting `pid=0` uniformly above is safe, not a real loss: every row now takes the
  `PID==0` branch, which is the one this fix actually covers), but for `PID==0` rows (Codex/
  OpenCode's normal case, and now every provider's case) it falls back to
  `LiveSessionChecker.IsLive`, which was wired against `*agentsessions.Manager.Get` — always
  empty post-migration, since nothing registers with it anymore. Left unfixed, `orphansweep`'s
  30-second periodic reaper (`internal/recovery/orphansweep`'s `RuntimeReaper`, confirmed to
  run continuously inside the live daemon, not just at bootstrap) would have falsely marked
  every genuinely-live Codex/OpenCode `ModeLongLived` chat session `"orphaned"` after its
  5-minute pid-zero grace window elapsed. **Fix**: `agent.Dependencies` now implements
  `LiveSessionChecker` itself (`IsLive`, backed by a new unexported `liveSessions sync.Map`
  field populated by `Boot`/cleared by the session's own completion goroutine); the
  composition root (`internal/service/agent_deps.go`) wires `deps.LiveSessions = deps` (self-
  referential, set post-construction) in place of the now-dead `managerLiveSessions{manager}`
  adapter (removed).
- **Daemon-shutdown drain would silently stop doing anything.** `chatServiceImpl.Shutdown`
  called `agentSessionsManager.Shutdown(ctx)` to gracefully stop every live session at daemon
  shutdown — a no-op once nothing is registered with the Manager, meaning CLI child processes
  would no longer be asked to terminate cooperatively on shutdown. **Fix**: added
  `Dependencies.StopAllLiveSessions(ctx) error` (iterates the same `liveSessions` registry,
  calling `Session.Stop` on each), wired into `chatServiceImpl.Shutdown` (`internal/service/
  chat.go`) alongside (not replacing) the now-inert-but-harmless `agentSessionsManager.Shutdown`
  call.

`Dependencies.SessionsManager *agentsessions.Manager` itself is **kept**, still required-non-
nil in `Boot`'s validation, still constructed by the composition root — `agent.Boot` no longer
calls any method on it, but it remains a legitimate, independent dependency for other,
unaffected uses (`AgentDepsBundle.Manager`). Doc comments on both the field and the
`RuntimeStore`/`LiveSessionChecker` interfaces updated to describe the post-migration
ownership accurately rather than leave stale "Boot calls this" claims in place.

### 7. `internal/recovery/broker` — genuinely zero changes

`git diff --stat -- internal/recovery/broker/` is empty. All 8 cited call sites (this task's
own Context section) compile and pass unchanged: `FailureEvent.Exit`, `OnRestart`,
`OnSessionExit`, `buildFailureEvent`, the five `classifier.go` `Cause*` switches, `envelope.go`'s
message copy, `deps.go`'s `AgentBoot.Boot(ctx, agent.Options) (*agent.Session, error)` contract,
`broker.go`'s `DispatchRetry`/`agent.HasBootdirLayout` call — none needed touching because
`agent.Options`, `Session`'s public shape, and `HasBootdirLayout`'s signature are all
byte-identical to before (see `06`'s own `agent.Mode` verification below for the diff-based
method).

### 8. `agent.Mode` and every cited lifecycle-decision site — verified byte-identical, not just "tests pass"

`git diff --stat -- internal/runtime/agent/factory.go` is **empty** — `shouldUsePTY`,
`shouldUseStreamingStdio`, `shouldAutoFireFirstTurn`, `runtimeConfigForAdapter` are literally
untouched. In `agent.go`, `Options.Validate()` (mode-specific validation, lines 204-217
pre-migration) and the `ModeSubagent` path-grant lineage block are outside every diff hunk
(first hunk starts at the `Session` struct addition, well after `Validate`; confirmed by
grepping the current file for the exact pre-migration `"ModeSubagent requires..."`/
`RegisterLineage(sessID` strings — both present, unmoved, unedited). The session-ID-preset-
by-mode switch, the `ModeOneShot`-first-turn-override, and the auto-fire-first-turn logic are
all still driven by the exact same `opts.Mode`/`shouldAutoFireFirstTurn(opts.Mode)` calls,
just feeding a `wrapper.Config` field instead of a `StartOptions` field.

### 9. Test coverage — real fake-subprocess integration tests, not mocks

Per this task's own Done-means allowance ("this task's own tests can use fakes/mocks... should
not claim live-dogfeed verification" — that's task `07`'s job), added
`internal/runtime/agent/wrapper_lifecycle_test.go` (new), three tests, each driving a **real**
fake CLI subprocess (not a mock `CLIAdapter`, not a mock `runtimeevents.Sink`) through the full
`Boot` → `wrapper.Wrapper.Run` → activity-translation → exit/stop path:

- `TestBoot_WrapperLifecycle_Claude` — full lifecycle (start → activity events → exit) against
  a real fake-claude stream-json script driven by the real `provider.NewClaudeAdapterStreamingStdio()`
  adapter. Asserts: `deps.IsLive` true immediately after Boot and false after Wait;
  `EventFanout` receives real `EventDelta`/`EventUsage`/`EventDone`; `TypedEventCallback`
  receives a real `events.ToolUse` from a genuine Claude `tool_use` content block;
  `store.provIDs` captures the real fake session id (`OnSessionID`); `store.states` captures
  both the `"running"` and `"done"` `UpdateState` transitions.
- `TestBoot_WrapperLifecycle_Stop` — the "stop" half of the Done-means bar against a
  still-running (not naturally-exited) long-lived fake-claude script: `Session.Stop` on a live
  child, asserts prompt completion (not a 30s hang), boot-dir cleanup, and `IsLive` flipping
  back to false.
- `TestBoot_WrapperLifecycle_Codex_EnvParity` — the dedicated regression test for §2's `Env`
  workaround: a real fake `codex` subprocess writes the `$CODEX_HOME` value it actually
  observed to a probe file (itself only reachable if the env-wrapper-script chain correctly
  propagated `NANITE_TEST_PROBE_FILE` too), asserted to equal the session's real boot dir
  exactly. Also caught the `runCancel` race bug in §6 during development.

All three pass under `go test -race` (ran repeatedly, both isolated to
`internal/runtime/agent` and combined with the rest of the suite). Existing tests in this
package (`boot_test.go`, `manager_test.go`, `agent_test.go`, etc.) needed **zero** behavioral
changes — only `fakes_test.go`'s `fakeRuntimeStore` gained the new `UpdateState` method (plus
the same addition to `internal/recovery/orphansweep/orphan_sweep_test.go`'s independent fake,
the only other `agent.RuntimeStore` implementation in the repo — grepped to confirm there are
exactly three: production, and these two fakes).

### Files touched (beyond this task's own listed `Touches`, with reasons)

- `internal/runtime/agent/{agent.go,factory.go,manager.go,deps.go}` — as scoped.
  `factory.go` ended up with **zero** diff (see §8).
- `internal/runtime/agent/wrapper_adapter.go`, `wrapper_sink.go`, `wrapper_lifecycle_test.go`
  — new files, the actual migration mechanics (§§1,2,5,9).
- `internal/runtime/agent/fakes_test.go`,
  `internal/recovery/orphansweep/orphan_sweep_test.go` — mechanical `UpdateState` additions to
  the two `agent.RuntimeStore` test fakes (§6).
- `internal/service/agent_deps.go` — `agentRuntimeStore.UpdateState`, `deps.LiveSessions =
  deps` self-reference, removed the now-dead `managerLiveSessions` adapter (§6). Not in this
  task's `Touches` list, but the actual `agentEventBridge`/`agentRuntimeStore`/
  `managerLiveSessions` implementations this migration's corollary fixes needed to touch live
  here, not in `internal/runtime/agent` — `agentEventBridge` itself (the chat-SSE translation
  consumer) needed **zero** changes, confirming the reverse-translation-in-the-Sink design
  (§5) correctly isolated the blast radius to this one file.
- `internal/service/chat.go` — added the `StopAllLiveSessions` call in `Shutdown` (§6). Not in
  `Touches`, same corollary-fix category.
- `go.mod` — the required version bump (§0) plus two new local `replace` directives for
  transitive deps this task's own new code now imports directly.

### `internal/recovery/broker` — restated per Done-means's own explicit checklist item

Zero changes, genuinely required by nothing this task did — see §7.

### Verification

`go build ./cmd/nanite/`: clean. `go vet ./...`: clean except two pre-existing, unrelated
warnings in `internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` "not used on all
paths" — confirmed via `git diff --stat -- internal/service/container.go` returning empty,
i.e. a file this task never touched; not investigated further, out of scope). `go test ./...`
(fresh, `-count=1`, no cache): clean across every package in the repo, including
`internal/runtime/agent`, `internal/service`, `internal/recovery/{broker,orphansweep}`.
`go test ./internal/runtime/agent/... -race -count=1`: clean, run repeatedly (including once
right after the `runCancel` race fix in §6, to confirm the fix itself is race-clean, not just
functionally correct). A combined `-race` run across `internal/runtime/agent` +
`internal/service` + `internal/recovery` together hit the suite's 10-minute test timeout
inside unrelated `internal/service` `TestBootDirAdapter_*` tests (BootDir remediation, a
completely different subsystem from session construction/lifecycle, zero code-path overlap
with anything this task touched) — not reproducible when `internal/runtime/agent` is run in
isolation under `-race`, and `internal/service`'s own full suite passes cleanly without `-race`
in ~74s; read as a pre-existing `-race`-instrumentation/`t.Parallel()`/SQLite-connection-pool
timing interaction under the combined-package invocation, not a defect in this task's own
changes, and out of this task's Done-means bar (which specifies plain `go test ./...`, not a
combined `-race` run) — noted here rather than silently ignored.

Per this task's own Done-means, this task's tests use fakes (real fake-subprocess scripts,
not live provider binaries) and make no live-dogfeed claim — task `07` is real end-to-end
verification against live claude/codex/opencode binaries.

**Commit**: pending — this Work Log entry is written before the commit step; see the final
report for the actual SHA once committed.

**Final commit**: `1f947c55` on `main`.

## Review notes

Fresh Reviewer (no shared context with this task's worker), 2026-08-21 — **PASS**, against
commit `1f947c55`. Independently confirmed both hard constraints against source, not diff
summaries: `internal/recovery/broker` and `factory.go` have zero diff; separately confirmed
`go.mod`'s diff does not touch `agentkit`'s version (only `go-agent-wrapper`/`go-runtime-events`/
`go-harness-filters` moved), so the shared `agentsessions.ExitError` type broker depends on is
untouched at the source, not just diff-empty. `agent.Mode`/lifecycle-decision sites verified
present verbatim in the same relative order. Verified the `nativeAdapter` design decision
against the actual shipped `adapters/{claude,codex,opencode}` source — confirmed the shipped
Claude adapter genuinely has no way to inject the dev-mode `SkipPermissions` variant, and
Codex/OpenCode's shipped `Descriptor`s genuinely map to a different `Capabilities` shape than
Nanite's current subprocess-per-turn — a real, correct justification. Independently verified
the `Env`-wrapper workaround's security properties (`bootDir`'s `MkdirTemp` `0700` mode makes
the script's own `0o700` redundant-but-correct; fresh script per `Boot()` call, no stale-env
risk; no cleanup-race — `RemoveAll` only runs post-ready, after the script has already `exec`'d
into the real binary). Verified `wrapper_sink.go`'s `KindAgentToolUse`-routing claim directly
against `agentEventBridge.translateStreamEvent`'s actual drop-tool_use-on-EventFanout code.
Verified the `runCancel` race fix is real and correctly shaped. Traced all three corollary
fixes (`RuntimeStore.UpdateState`, `liveSessions` registry, `StopAllLiveSessions`) — no leak
found on any exit path. Ran the new `WrapperLifecycle` tests directly — genuine, not vacuous.
Independently reproduced clean `go build`/`go vet` (same 2 pre-existing, unrelated findings)/
`go test ./...`/`go test ./internal/runtime/agent/... -race`/`go test ./internal/recovery/... -race`.

Two real, non-blocking findings (full detail in `TASKS/ESCALATIONS.md`'s 2026-08-21 "Task `06`
re-attempt landed and reviewed PASS" entry): (A) `Options.ExtraArgs` is now a silent no-op with
a stale, actively-misleading doc comment — the migration dropped the only forwarding line and
`wrapper.Config` has no equivalent field, but the field/comment weren't removed alongside
`Supervisor`/`AttachEnabled` (which were correctly removed for the identical reason). (B) one
new abort path in `Boot`'s 3-way select (caller `ctx` cancelled while waiting for `ready`) only
calls `UpdateState(..., "failed", 0)` instead of `MarkRuntimeFailed` (state + reason) — state
still lands correctly on `"failed"`, just without a recorded reason for this one edge case.
Neither touches a hard constraint; both fixed as a small, targeted follow-up per the reviewer's
own recommendation rather than left as debt (see this file's own Work Log addendum below, if
present, or `TASKS/ESCALATIONS.md` for the fix's landing record).

## Work Log addendum (2026-08-21) — small follow-up for the reviewer's two findings

Read `TASKS/ESCALATIONS.md`'s 2026-08-21 "Task `06` re-attempt landed and reviewed PASS
(commit `1f947c55`) — two real, non-blocking findings, fixed as a small follow-up" entry and
this file's own Review notes section above before starting, per the dispatch instructions.
Both findings verified independently against the current `main` source (not just trusted from
the review summary) before fixing.

**Finding A (`Options.ExtraArgs` silent no-op)**: grepped every reference to `ExtraArgs` across
the repo. Confirmed `internal/runtime/agent/agent.go`'s `Options.ExtraArgs` field had zero
setters and zero readers anywhere — `driveBootSession`/`chat_boot_drive.go` never sets it
despite `launch_spec_options_test.go`'s file-level comment claiming it does (that comment only
ever exercised `BootPromptOverride` in its actual test bodies). The only other `ExtraArgs`
symbols in the repo (`internal/workflowrunner/launch.go`,
`internal/service/workflow_external_engine.go`) are a completely unrelated, live, actively-used
mechanism (external workflow script argv) — different package, different struct, not touched.
No live caller found for `agent.Options.ExtraArgs`, matching the reviewer's own research.
Removed the field and its stale doc comment from `Options` (`agent.go`), matching how
`Supervisor`/`AttachEnabled` were already removed in task `06`'s own migration. Corrected the
now-inaccurate file-level comment in `launch_spec_options_test.go` (previously claimed
`ExtraArgs` was one of two Options fields under that file's regression coverage) to describe
the removal instead of continuing to reference a field that no longer exists.

**Finding B (`ctx.Done()` abort path records no failure reason)**: read `Boot`'s 3-way `select`
(`agent.go`, `readyCh`/`sess.runDone`/`ctx.Done()` cases) and confirmed the exact asymmetry:
the `<-sess.runDone:` branch calls `deps.Store.MarkRuntimeFailed(sessID, failErr.Error())`
directly in the select-branch body, while the `<-ctx.Done():` branch had no equivalent call —
the only store write on that path was the shared background goroutine's own unconditional,
branch-agnostic `deps.Store.UpdateState(sessID, state, 0)` (state only, no reason), which runs
for every exit of that goroutine regardless of which select branch triggered it.

Verified `MarkRuntimeFailed`'s real signature and behavior before assuming it's a drop-in
replacement, not guessing: `RuntimeStore.MarkRuntimeFailed(runtimeID, reason string) error`
(`deps.go`) → `agentRuntimeStore.MarkRuntimeFailed` (`internal/service/agent_deps.go`) →
`store.MarkAgentRuntimeFailed` (`internal/store/agent_runtime.go`), whose SQL is `UPDATE
agent_runtime SET state = 'failed', failure_reason = ?, updated_at = ? WHERE id = ?` — it
already sets `state='failed'` as part of persisting the reason, so it's the correct call to add
in the `ctx.Done()` branch itself (mirroring the `runDone` branch's own shape exactly: a single
`MarkRuntimeFailed` call, no companion `UpdateState` call in that branch), not something that
needs to run alongside a separate `UpdateState` call. (`MarkRuntimeFailed` doesn't touch the
`pid` column, unlike `UpdateState` — irrelevant here since every write on this path already
persists `pid=0` uniformly, per this task's own original Work Log §6 corollary-fix note on
`orphansweep`'s `PID==0` fallback.)

Added the call after the branch's existing `<-sess.runDone` block-until-goroutine-exits and
lineage-clear steps, with a reason string derived from `ctx.Err()`
(`"agent.Boot: caller ctx cancelled: " + ctx.Err().Error()`) — prefixed `"agent.Boot: "` to
match this file's existing reason-string convention (e.g. the sibling `runDone` branch's
`"agent.Boot: wrapper.Run exited before session became ready"`). This call runs after the
background goroutine's own bare `UpdateState` write and supersedes it (last write wins on the
same row), landing on the correct final `state="failed"` + reason regardless of whatever
transient state the background goroutine wrote first.

No test previously exercised this specific abort path (grepped `boot_test.go`,
`agent_test.go`, `wrapper_lifecycle_test.go`, `bootdir_alias_test.go` for
`context.WithCancel`/`context.WithTimeout` near a `Boot` call — none found), so no existing
assertion depended on the old, reason-less behavior; no test needed changing.

**Verification**: `go build ./cmd/nanite/` clean. `go vet ./...` clean except the same two
pre-existing, unrelated `container.go` warnings already noted in this task's original Work Log
(file untouched by this follow-up — confirmed via `git status --short` showing no diff to
`internal/service/container.go`). `go test ./...` (fresh, `-count=1`): clean across every
package. `go test ./internal/runtime/agent/... -race -count=1`: clean.

**Files touched**: `internal/runtime/agent/agent.go` (both fixes),
`internal/runtime/agent/launch_spec_options_test.go` (stale comment correction only, no
behavioral test change).

**Commit**: `a55f239c` on `main`.
