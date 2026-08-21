# Sandbox + capability-policy gate for skill script/materializer execution

**Phase:** 5 — Security: sandbox + policy gate (`TASKS/skills`)
**Status:** not-started
**Depends on:** `02` (grant-state columns on `agent_known_skills`), `08` (the caller this gate
serves — task `08` calls into whatever this task builds)
**Touches:** new file `internal/skill/gate.go` (or extend `internal/skill/exec.go` if task `08`
landed first and the split reads more naturally combined — worker's judgment, note the choice in
your Work Log), `internal/runtime/agent/sandbox_profile.go` (read as a pattern reference, not
modified).

## Context

`docs/engineering/architecture/20-skills.md`'s "Security, sandboxing, and trust" section:
*"Script/materializer execution reuses `go-sandbox`'s `Profile`+`Apply`... a short-lived,
sandboxed, read/compute subprocess exec needs none of the full host/wrapper session machinery...
Policy enforcement... plugs into the same `policy.Engine`/`policy.Store` shape the host already
runs for CLI tool-call interception... Default posture is read/compute/materialize... Trust
invalidation is the vendored content hash. Approval is granted against a specific hash; a
changed source requires a new explicit install before it affects anything."*

**A real correction to the "plugs into the same... shape the host already runs" claim, found
during this batch's own planning research (not assumed from the architecture doc) — read this
before designing anything:** `docs/engineering/architecture/16-agent-host.md`'s description of
`go-agent-wrapper`'s `policy/` package (mechanism vs. policy split, `policy.Engine.Decide` /
`policy.Store`) is an accurate description of that *library's* design. But `wrapper.Config.Policy`
is **never set anywhere in Nanite today** — confirmed via repo-wide grep — so no tool-call
interception through this mechanism is actually running in production. There is no live pipeline
for this task to "plug into." Separately, `policy.Rule.Match` (the library's rule shape) is a
bare, store-defined-syntax string with no typed fs/network/subprocess/secrets schema — even if
the mechanism were wired live, there's no existing typed vocabulary to extend.

**Resolution, matching what `TASKS/plugin-system/06` (`capability-enforcement-at-rpc-proxy-layer`)
independently decided for the exact same reason**: build narrow, direct capability enforcement at
this task's own call site — gated on task `02`'s `agent_known_skills` grant-state columns
(`approved_content_hash`, `granted_at`, `granted_by`, `capabilities_granted`) — rather than
depending on wiring the currently-dormant host-level `policy.Engine`/`policy.Store` mechanism as
a prerequisite. This task's rule vocabulary should stay loosely aligned with `policy.Rule`'s
taxonomy (a decision's mode: observe/nudge/rewrite/block/approval) for future compatibility if
that host mechanism ever does get wired live — but no code is shared, because there's no live
code to share yet. See `TASKS/skills/README.md`'s corrections section and
`TASKS/ESCALATIONS.md`'s 2026-08-21 entry for the full record of this finding.

**The sandbox primitive, confirmed real and reusable, but with no existing short-lived-exec call
site in Nanite today**: `go-sandbox@v0.2.1`'s `sandbox.Profile` (`FS{Read,Write,Deny}`, `Net`,
`AllowLoopback`, `LoopbackForwardPorts`, `Subprocess`) and
`sandbox.Apply(cmd *exec.Cmd, p Profile, workspace string) (cleanup func(), err error)`
(`apply_{darwin,linux,unsupported}.go`) — every current Nanite use is threaded through
`agentkit`'s long-lived session runtimes via `wrapper.Config.SandboxProfile`
(`internal/runtime/agent/agent.go:569` ← `buildSandboxProfile`,
`internal/runtime/agent/sandbox_profile.go:24`). Nothing in Nanite calls `sandbox.Apply` directly
against an ad-hoc, short-lived `*exec.Cmd` today — this task is that first, genuinely new call
site. `buildSandboxProfile`'s composition style (compose `FS.Write` from the relevant working
directories, `AllowLoopback: true`) is worth matching for consistency, not literally importing —
a skill-script's profile should be much narrower (default read/compute only, no write access
unless a capability grant says otherwise), not a copy of a long-lived agent session's broader
profile.

## What to do

1. Define the capability-grant vocabulary this task enforces: resource kind (fs-read / fs-write /
   network / subprocess-spawn / environment-secret-access — pick the real set based on what
   `go-sandbox.Profile` can actually express, don't invent capabilities the sandbox can't back),
   stored in `agent_known_skills.capabilities_granted` (task `02`'s column) as a JSON structure
   you define here (this task owns that column's actual shape — task `02` left it loose/JSON
   deliberately for this task to specify).
2. Define the default posture: `read/compute/materialize` only — a skill's script/materializer
   gets a `sandbox.Profile` with `FS.Write` empty (or scoped to a scratch/output-only path, if
   materialization genuinely needs to write anything — verify against task `08`'s actual needs
   before deciding), `Net: false`, unless `capabilities_granted` explicitly authorizes more.
3. Implement the gate's entry point (the interface task `08` calls into): given a skill's ID, the
   invoking agent's ID, and the command/script to run, look up the agent's
   `agent_known_skills` grant row, confirm `approved_content_hash` matches the skill's *current*
   vendored content hash (task `02`/`03`) — **a mismatch means the content changed since
   approval; refuse execution and surface a clear "re-approval required" error, per the trust
   model's own explicit rule**, not a silent fallback to the old approval. If the hash matches,
   compose a `sandbox.Profile` from `capabilities_granted` (narrower than
   `buildSandboxProfile`'s agent-session profile, per the default-posture rule above), and call
   `sandbox.Apply` directly against the prepared `*exec.Cmd`.
4. If no grant row exists at all for this agent/skill pair, refuse execution outright (no
   ambient capability, matching the "anything mutating or higher-risk requires an explicit
   opt-in grant" rule) — the default posture (read/compute/materialize) still requires *some*
   grant row to exist (even a minimal one), it isn't a bypass of the grant system entirely;
   confirm this interpretation against `20-skills.md`'s actual wording before implementing — if
   genuinely ambiguous whether an ungranted skill gets zero execution or default-posture
   execution, treat this as a real escalation-worthy ambiguity per `EXECUTION-PROCESS.md`, don't
   guess silently.
5. Log/return the decision (mode: observe/nudge/rewrite/block/approval-required) in a shape that
   loosely mirrors `policy.Decision`'s fields, for the future-compatibility reason noted in
   Context — but implement it as this package's own type, not an import of the unwired
   `go-agent-wrapper/policy` package.

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- A granted skill with a matching approved-hash executes successfully within its declared
  capability bounds (a real `sandbox.Apply` call, not mocked) — verified via a test that attempts
  an operation *outside* the granted bounds (e.g. a write when only read was granted) and
  confirms the sandbox actually blocks it, not just that the gate "would have" refused it.
- A skill whose vendored content hash no longer matches the approved hash is refused execution
  with a clear "re-approval required" error — verified by re-installing the test fixture with
  changed content (new hash) and confirming the previously-granted agent can no longer execute it
  until re-approved.
- A skill/agent pair with no grant row at all is refused (or executes under whatever the
  resolved default-posture interpretation from step 4 turns out to be — document which,
  concretely, in the Work Log).
- No skill-script execution path anywhere in this batch (task `08`'s callers) reaches
  `sandbox.Apply` except through this task's gate — a test/grep confirms `internal/skill/`
  contains exactly one call to `sandbox.Apply`.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
