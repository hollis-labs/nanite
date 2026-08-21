# Migrate buildSandboxProfile onto go-agent-wrapper's sandbox.Applier

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`)
**Status:** not-started — **escalated, no code changes made. See `TASKS/ESCALATIONS.md`
(2026-08-21, "Task `05` (sandbox.Applier migration)…"). `Applier.Apply(ctx, pid)` is
confirmed a true post-spawn attach-by-pid mechanism (option (b) in this file's own Context),
incompatible with `buildSandboxProfile`'s pre-spawn model — do not dispatch a follow-up worker
against this file's original scope without an Orchestrator/operator decision on the
escalation's recommended path first.**
**Depends on:** `03` (dependency wired)
**Touches:** `internal/runtime/agent/sandbox_profile.go`. Repo: Nanite.

## Context

`docs/engineering/architecture/16-agent-host.md` names `buildSandboxProfile` as composing
`go-sandbox.Profile` directly — "the same primitive `go-agent-wrapper/sandbox` wraps, just
not through it."

**Verified directly.** `buildSandboxProfile(base sandbox.Profile, opts Options, workspaceDir,
bootDir string) sandbox.Profile`, `sandbox_profile.go:24-47`: sets `AllowLoopback=true`
(`:30`, required for the `.mcp.json` subprocess RPC) and appends `opts.Workdir`,
`workspaceDir`, `bootDir`, and `naniteHomeDir()` (`$HOME/.nanite`) to `p.FS.Write`
(`:38-44`), dedup'd via a `seen` map. Short-circuits to a zero-value `sandbox.Profile{}` when
`Mode == ModeBackground && WideOpen` (`:25-27`, called out in the function's own comment as
"the legacy privileged primitive"). Invoked from `agent.go`'s Boot flow, feeding
`agentsessions.StartOptions.Profile` at `agent.go:532`.

`go-agent-wrapper`'s `sandbox` package (`sandbox/sandbox.go`): `Applier interface { Apply(ctx,
pid int) (Result, error) }`, `Result{Profile string, Applied bool, Notes []string}`,
`NoOpApplier{}` reference implementation. Composes `go-sandbox` (v0.2.1+) profiles — same
underlying library Nanite already uses.

**Real open question this task must resolve, not guess past**: `Applier.Apply` takes a `pid
int` — a signature that implies applying a sandbox to an *already-running* process. Nanite's
current flow is the opposite: `buildSandboxProfile` constructs a `go-sandbox.Profile` value
*before* spawn, passed into `agentsessions.StartOptions.Profile`, which presumably applies it
as part of (or immediately around) the exec call itself, not after the fact via a pid handle.
Neither research pass for this planning session read `go-agent-wrapper/sandbox/sandbox.go`'s
full implementation or its actual callers deeply enough to know whether `Apply(ctx, pid)` is:
(a) a genuine post-fork-pre-exec hook (common for Landlock/seccomp-style restrictions applied
to a child after `fork()` but before `exec()`, which would still fit Nanite's pre-spawn
profile-construction model), or (b) a true post-spawn attach-by-pid mechanism that would
require restructuring how/when Nanite calls this relative to process start.

## What to do

1. **Before writing any migration code**, read `go-agent-wrapper/sandbox/sandbox.go` in full,
   plus its real caller inside `wrapper.Wrapper.Run` (where/when `Applier.Apply` is actually
   invoked relative to process spawn), plus enough of `go-sandbox`'s own `Apply`/`Profile`
   semantics to know definitively which of the two shapes above is real. Do not assume either
   answer from this task's Context — verify against the actual code, since this planning
   session's own research did not resolve it.
2. If `Apply(ctx, pid)` is a genuine pre-exec-compatible hook (option (a) above): implement it
   as a thin wrapper around the existing `buildSandboxProfile` logic — same profile
   construction, called at whatever point `wrapper.Wrapper` actually invokes `Applier.Apply`.
3. If it's a true post-spawn attach mechanism incompatible with Nanite's current pre-spawn
   `StartOptions.Profile` flow (option (b)): **this is a real, concrete mismatch — stop and
   escalate rather than forcing a fit.** Per `EXECUTION-PROCESS.md`'s escalation rule, this
   would be "the task file's own instruction being genuinely ambiguous" territory once you've
   done the verification step 1 asks for and the two models are provably incompatible; log it
   in `TASKS/ESCALATIONS.md` with your findings and a recommended path (e.g., go-agent-wrapper
   needs a pre-spawn sandbox hook added, which would itself be new go-agent-wrapper scope) —
   do not silently pick a workaround.
4. Preserve the `ModeBackground && WideOpen` short-circuit behavior (`sandbox_profile.go:25-
   27`) exactly — this stays Nanite-owned logic (it's a `Mode`-driven decision, not something
   the host should encode), just relocated to wherever the migrated call site lives.
5. Preserve the exact `FS.Write` path set (`opts.Workdir`, `workspaceDir`, `bootDir`,
   `naniteHomeDir()`, deduped) and `AllowLoopback=true`.

## Done means

- Either: sandbox profile construction routes through `sandbox.Applier` with identical
  resulting `go-sandbox.Profile` semantics to before this migration (verified by a direct
  before/after profile comparison in a test, not just "it compiles"), and the
  `ModeBackground && WideOpen` short-circuit is preserved — OR — a documented, evidence-
  backed escalation exists in `TASKS/ESCALATIONS.md` explaining exactly why the migration
  can't proceed as scoped, with no code changes made that paper over the mismatch.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` clean (existing
  `sandbox_profile_test.go` passes or is updated with equivalent coverage).

## Work Log (2026-08-21)

**Verification performed (step 1), against real code, not the doc's characterization:**

1. Read `go-agent-wrapper/sandbox/sandbox.go` in full (`Applier`, `Result`, `NoOpApplier`) and
   `sandbox/doc.go`. The interface doc comment is internally inconsistent — sentence 1 says
   "applies a sandbox profile to a child-process context before exec"; sentence 2 says "the
   wrapper calls Apply once after spawning the child but before any agent-visible work
   begins." Not load-bearing on its own; resolved by the real caller below.
2. Read `go-agent-wrapper/wrapper/wrapper.go`'s `Config.Sandbox` field doc (lines 92-102),
   which resolves the ambiguity explicitly and unambiguously: `Apply` runs "against the
   session's child PID after `Runtime.Start` returns," with an explicit note that "for the
   adapter runtime (subprocess-per-turn) the PID is zero between turns — the Applier's
   pre-spawn enforcement story belongs in `agentsessions.StartOptions.Profile` for that path.
   The wrapper's Sandbox is the right hook for runtimes where a long-lived child has a stable
   PID (PTY, streaming-stdio, jsonrpc-stdio)."
3. Read the real call site, `Wrapper.Run` (`wrapper.go:197-453`): `runtime.Start(ctx,
   agentsessions.StartOptions{..., Profile: w.cfg.SandboxProfile, ...})` at line 342-347
   (note: a *separate* `Config.SandboxProfile` field, not `Config.Sandbox`, feeds
   `StartOptions.Profile` here) returns a live `session`; `session.ready`/`process.started`
   events fire with the live PID at lines 389-393; only *after* that does `runSandbox`
   (`wrapper.go:592-619`) call `w.cfg.Sandbox.Apply(ctx, session.Health().PID)` at line 395.
   The child process is already running, already has a PID, and `session.ready` has already
   fired, by the time `Applier.Apply` is invoked.
4. `wrapper_integration_test.go`'s `TestRunInvokesSandbox` independently confirms this
   ordering as a tested invariant: it explicitly asserts `session.ready`'s index in the
   emitted event stream precedes `sandbox.applied`'s index.
5. Read `go-sandbox`'s own `Apply` across all three build-tag variants
   (`apply_darwin.go:212`, `apply_linux.go:217`, `apply_unsupported.go:12`) — identical
   signature on every platform: `Apply(cmd *exec.Cmd, p Profile, workspace string) (cleanup
   func(), err error)`. It mutates an *unstarted* `*exec.Cmd` (rewrites `cmd.Path`/`cmd.Args`
   to wrap execution in `sandbox-exec`/`bwrap`) before `cmd.Start()` is ever called — no PID
   exists yet at that point. Confirmed this is exactly what `agentkit/agentsessions` already
   calls today for every runtime kind Nanite uses (`streaming_stdio_session.go:246-252`,
   `pty_session.go:304-310`, `serve_http_session.go:226-232`,
   `jsonrpc_stdio_session.go:249-255`, all pre-`cmd.Start()`) when given
   `StartOptions.Profile` — which is exactly the field `buildSandboxProfile`'s return value
   feeds today (`agent.go:466,532`). There is no PID-attach implementation of `sandbox.Apply`
   anywhere in `go-sandbox`, on any platform, and the only shipped `sandbox.Applier`
   implementation in `go-agent-wrapper` is `NoOpApplier`.

**Finding: definitively option (b).** `sandbox.Applier.Apply(ctx, pid)` is a true post-spawn
attach-by-pid mechanism, confirmed by four independent, mutually-reinforcing sources (the
`Config.Sandbox` doc comment, the actual call ordering in `Wrapper.Run`, the integration
test's own ordering assertion, and `go-sandbox`'s `Apply` signature having no PID-based
variant on any platform). It is not a pre-fork-pre-exec hook and does not fit
`buildSandboxProfile`'s pre-spawn `go-sandbox.Profile`-construction model. Per this task's own
step 3, this is a real, concrete mismatch — no migration onto `sandbox.Applier` was
implemented, and no workaround was forced.

**Additional finding beyond the task's own step-3 example** ("go-agent-wrapper needs a
pre-spawn sandbox hook added, which would itself be new go-agent-wrapper scope"): no new
`go-agent-wrapper` scope is actually needed. `wrapper.Config` already carries a second,
distinct field next to `Sandbox`: `SandboxProfile sandboxprofile.Profile` (`wrapper.go:105-
108`), forwarded directly into `agentsessions.StartOptions.Profile` at `wrapper.go:347` — the
same pre-spawn seam Nanite already uses today, just reached through `wrapper.Config` instead
of `agentsessions.StartOptions` directly. `buildSandboxProfile`'s existing logic needs no
behavioral change to route through it; only the call site (task `06`'s scope, which already
replaces `agent.go:525-554`'s direct `StartOptions`/`SessionsManager.Start` construction with
`wrapper.Wrapper.Run`) needs to plug the same `sandbox.Profile` return value into
`Config.SandboxProfile` instead of `StartOptions.Profile` directly.

**No code changes made.** `internal/runtime/agent/sandbox_profile.go` and
`sandbox_profile_test.go` are untouched — confirmed via `git status --short` (no diff against
`main`). `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` were not re-run as part of
this task's own verification since nothing in the repo changed; the pre-existing baseline is
unaffected by a read-only investigation.

**Escalation logged:** `TASKS/ESCALATIONS.md`, entry dated 2026-08-21, "Task `05`
(sandbox.Applier migration): `Applier.Apply(ctx, pid)` confirmed to be a true post-spawn
attach mechanism…" — includes the full evidence above plus a recommended path for the
Orchestrator (fold the mechanical call-site rewire into task `06`'s existing scope rather than
treating it as a standalone migration, since there is no separate `Applier`-shaped seam to
migrate onto).

**Per this task's own instructions, stopping here — not marking this task `implemented`.**
Status left as `not-started — escalated` above. `06` depends on this task; the Orchestrator
should decide the recommended path before `06` is dispatched.
