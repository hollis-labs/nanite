# Migrate buildSandboxProfile onto go-agent-wrapper's sandbox.Applier

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`)
**Status:** not-started
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
