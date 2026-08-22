# Sandbox + capability-policy gate for skill script/materializer execution

**Phase:** 5 — Security: sandbox + policy gate (`TASKS/skills`)
**Status:** in-progress — review found a real secret-leakage bug, fix required (see "Fix required" section below)
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

**Scope/file choice.** Built as a new file, `internal/skill/gate.go`, per the task's own primary
option (task `08`'s `exec.go` was already landed/reviewed on `main` and is large; keeping the
gate as its own file reads more naturally and keeps `exec.go`'s existing "no direct subprocess
spawn" static-AST test scoped to exactly the file it was written for). `internal/skill/gate_test.go`
holds all new tests. `internal/runtime/agent/sandbox_profile.go` was read only, not modified, per
the task's own Touches line.

**Pre-flight.** Confirmed task `02` (grant-state columns) and task `08` (`GatedExecutor`
interface, `ExecRequest`/`ExecResult`, `ExecKind`) are both landed and reviewed on `main` in this
worktree — read `internal/skill/exec.go` in full, confirmed `ExecRequest` deliberately has no
`ExecProfile`/sandbox-profile field (per that task's own review), and did not add one. Confirmed
`go-sandbox` is pinned at `v0.2.1` (`go.mod`) and read `sandbox/profile.go`,
`sandbox/apply_darwin.go`, `sandbox/apply_linux.go`, `sandbox/apply_unsupported.go`,
`sandbox/doc.go`, and `sandbox/integration_test.go` from the module cache directly before
designing anything, per the task's own "check apply_{darwin,linux,unsupported}.go" instruction.

**Real, load-bearing finding beyond what the task file's own Context section flagged — investigated
directly against `go-sandbox@v0.2.1`'s source, not assumed.** The task's Context section already
correctly flags that `policy.Engine`/`policy.Store` is dormant in Nanite (confirmed independently:
zero hits for `wrapper.Config.Policy` being set anywhere). Beyond that, this task's own design
work surfaced a second, more specific gap in the *sandbox* primitive itself (not the policy
mechanism): **`go-sandbox@v0.2.1`'s two backends enforce a `Profile` very differently, and neither
platform has a `Profile` field that is both meaningful and robustly enforced for "block a write to
an unlisted path" on its own**:
  - **macOS (`sandbox-exec`/SBPL)**: `doc.go` states the posture as "default-allow with selective
    denies," and `apply_darwin.go`'s `BuildSBPL` confirms this literally — it always emits
    `(allow default)`, and `FS.Write`/`FS.Read` entries are either redundant with that default-allow
    (`Write`) or genuinely inert (`Read` — the code's own comment: "Read paths are no-ops under
    default-allow"). The *only* filesystem-access restriction this backend can actually express is
    `FS.Deny` (denies both read+write together for listed paths). An **empty `FS.Write` list does
    NOT, by itself, block a write to some other, unlisted path** on this platform — confirmed
    empirically in this task's own test suite (see below), not just by reading the source.
  - **Linux (bubblewrap)**: the opposite, genuinely default-deny posture — only a narrow, hardcoded
    read-only interpreter/TLS allowlist plus `workspace`/`FS.Write` (`--bind`, writable) and
    `FS.Read` (`--ro-bind-try`, real read-only) are visible inside the sandboxed mount namespace at
    all; everything else genuinely does not exist there. But `FS.Deny` is a **silent no-op** on this
    backend — `buildBwrapArgs` never references it. `Subprocess` is also explicitly documented as
    unenforced on Linux.
  - **Both backends treat `Apply`'s third parameter (`workspace`) as unconditionally writable**
    regardless of `Profile` contents (macOS's own comment: "Workspace is always writable, regardless
    of FS.Write contents"; Linux's `buildBwrapArgs` does an unconditional `--bind absWS absWS`).
    Passing `req.WorkDir` (a skill's own vendored package directory, for a script) as `workspace`
    would therefore make it writable no matter what a grant says — violating
    `internal/skillvendor.Store.Path`'s own "treat the returned directory as read-only" contract.

  This is a real, pre-existing limitation of the vendored `go-sandbox` library (its own `doc.go`:
  "Tightening to default-deny requires a well-tested per-OS allowlist and is intentionally left to
  a future sprint") — not something this task's own scope covers fixing (this task is a consumer of
  `sandbox.Apply`, not its implementer), and not a reason to stop (nothing here contradicts what
  this task was asked to build; it changes *how* the gate must compose a profile to get real
  enforcement, not *whether* to build it). **Resolution, applied directly in `gate.go`'s design**:
  (1) `composeProfile` always creates a fresh, disposable, per-execution scratch directory as the
  `workspace` argument to `sandbox.Apply` — never `req.WorkDir` — so a grant with no fs-write
  capability never accidentally makes the skill's own read-only vendored directory writable through
  this side channel; `req.WorkDir` is instead added to `FS.Read` (never `FS.Write`), structurally,
  regardless of grant, purely so the command can find and read its own files. (2) The `Capabilities`
  vocabulary's `FS` field forwards a grant's `Deny` entries unconditionally on every platform
  (harmless no-op on Linux, load-bearing on macOS) rather than relying on bare omission from `Write`
  to mean "blocked" — bare omission is not, on macOS, sufficient proof of real enforcement, and this
  task's own Done-means explicitly requires proving the sandbox *actually* blocks the forbidden
  operation, not just that the gate "would have" refused it.

**1. Capability vocabulary (What-to-do item 1).** Defined in `gate.go`: `Capabilities{FS
*FSCapability, Network *NetworkCapability, SubprocessSpawn *bool}`, where `FSCapability{Read,
Write, Deny []string}` mirrors `sandbox.FSSpec` directly (combining the architecture doc's
illustrative "fs-read"/"fs-write" resource kinds into one structure, since `sandbox.Profile` itself
expresses filesystem access as one `FSSpec`, not two independent gates — splitting them into two
top-level JSON keys would just require immediately recombining them with no enforcement benefit),
and `NetworkCapability{Allow, AllowLoopback bool}` maps onto `Profile.Net`/`Profile.AllowLoopback`.
**"environment-secret-access" (the fifth resource kind `20-skills.md`'s illustrative list names) is
deliberately omitted** — confirmed by reading `sandbox.Profile`'s full field set directly that it has
no field expressing environment-variable scoping at all, so there is nothing for a grant of this
kind to actually back, per the task's own explicit "don't invent capabilities the sandbox can't
back" instruction. A sandboxed command's environment is inherited from the gate's own process
unmodified (Go's `exec.Cmd` "nil `Env` means inherit" default) — a known, documented limitation, not
an oversight. This is this task's own JSON shape for `agent_known_skills.capabilities_granted`
(task `02`'s column, left loose/JSON deliberately for this task to specify) — parsed via the new
`ParseCapabilities(raw string) (Capabilities, error)`, where an empty string (the common case: a
grant with no elevated capabilities beyond the default posture) returns the zero value, not an
error.

**2. Default posture (What-to-do item 2).** `composeProfile` gives a skill's script/marker execution
`FS.Write: nil` (empty) and `Net: false`/`AllowLoopback: false` by default, exactly per the task's
own instruction, unless `capabilities_granted` explicitly widens either. Verified against task `08`'s
actual `exec.go` (already landed) that neither `ResolveInlineMarkers` nor `ExecuteScript` themselves
assume or require any write capability — both only ever capture stdout/stderr — so no
scratch/output-only write path needed to be carved out by default; a skill that genuinely needs to
write something declares an explicit `fs.write` path in its grant.

**`SubprocessSpawn` default (a design-latitude call, documented here rather than left implicit).**
Defaults to `true` (not gated) when the grant's `SubprocessSpawn` field is `nil`. Reasoning, and why
this isn't a stop-and-escalate case: `20-skills.md`'s own default-posture description enumerates
only `FS.Write` and `Net` as fields a grant must explicitly widen — it does not name `Subprocess`.
Defaulting `Subprocess` to `false` would make the "default posture" this task builds unable to run
the Agent-Skills-spec's own canonical example (`` !`git diff HEAD` ``, which has to fork/exec a real
`git` binary), which would contradict "read/compute/materialize" being a genuinely usable default.
This is also safe, not a hole in the other gates: both `go-sandbox` backends apply enforcement to
the whole sandboxed process tree, not just the initial binary, so a forked child remains just as
FS/Net-constrained as its parent. `SubprocessSpawn` is still a real, grantable `*bool` field for the
rare skill that must never fork/exec at all (explicit `false` revokes it).

**3. Gate entry point (What-to-do item 3).** `Gate.ExecuteGated(ctx, req) (ExecResult, error)`
implements `exec.go`'s `GatedExecutor` interface exactly (`var _ GatedExecutor = (*Gate)(nil)`
compile-time assertion). Internally: `authorize` looks up the skill's current catalog row
(`SkillIndexStore.GetSkillBySlug` — reused `resolver.go`'s existing interface rather than declaring
a duplicate one) for its current `ContentHash`, looks up the agent's grant row
(`AgentKnownSkillStore.GetAgentKnownSkill`, a new narrow interface matching this package's existing
DI-for-testability convention), and refuses via a typed `*ReapprovalRequiredError` (carrying both
hashes) when `grant.ApprovedContentHash != sk.ContentHash` — never a silent fallback to the old
approval. On a match, `ParseCapabilities(grant.CapabilitiesGranted)` feeds `composeProfile`, which
`Gate.run` hands to a real `sandbox.Apply` call against a prepared `*exec.Cmd` built from
`req.Command`/`req.WorkDir`.

**4. No-grant-row resolution (What-to-do item 4— the flagged ambiguity).** Read
`20-skills.md`'s "Security, sandboxing, and trust" section directly, in full, before deciding (not
just the task file's own paraphrase of it), per the task's explicit instruction. Found it **not**
genuinely ambiguous: the section frames the entire default posture in terms of *approved* execution
("Approval is granted against a specific hash..."), and nothing anywhere in the doc describes an
ungranted skill/agent pair executing under an ambient default posture. Read plainly, "default
posture is read/compute/materialize" describes the *capability breadth once a grant exists* (i.e.,
what a grant gets by default, absent an elevated `capabilities_granted`) — it is not a statement
that no grant is needed at all. **Resolution applied**: `Gate` refuses execution outright — a typed
`*GrantRequiredError` — whenever no `agent_known_skills` row exists for `(AgentID, SkillSlug)` at
all (`Reason: "no grant row exists"`), *or* a row exists but has never been through an explicit
approval step (`ApprovedContentHash == ""` — e.g. a bare row `AssignSkillToAgent` left behind,
exactly `store.AgentKnownSkill.IsBareAssignment`'s shape, `Reason: "grant row exists but has never
been approved"`). Both cases are treated identically (refuse) because neither is an approved grant;
distinguished only in the error's `Reason` string for clearer operator-facing messages. **Not
escalated** — per `EXECUTION-PROCESS.md`'s own framing, this was a case of "verifying the instruction
against the primary source directly," which resolved cleanly, not a genuine unknown requiring a
stop. No new `TASKS/ESCALATIONS.md` entry was needed; the 2026-08-21 entry this task's own file cites
for the `policy.Engine`/`policy.Store` dormancy finding already exists on `main`.

**5. Decision logging (What-to-do item 5).** `GateDecision{Mode DecisionMode, SkillSlug, AgentID,
Message}` — `DecisionMode` constants (`observe`/`nudge`/`rewrite`/`block`/`approval`) mirror
`go-agent-wrapper@v0.8.1`'s `policy.Mode` taxonomy exactly (read the real package,
`policy/policy.go`, to confirm the four field names/values before choosing these) for the
future-compatibility reason the task's Context section names, but as this package's own type — no
import of `go-agent-wrapper/policy`. Since `ExecuteGated`'s signature is fixed by task `08`'s
already-reviewed `GatedExecutor` interface (no room to return a decision alongside `ExecResult`),
the decision is logged unconditionally via `slog` (this codebase's standard package-level logging
convention, e.g. `internal/service/container.go`) on every call, and — for a refusal — is also
carried inside the returned error's own typed fields (`GrantRequiredError`/
`ReapprovalRequiredError`), so a caller can act on structured detail via `errors.As` without needing
the log line.

**Real sandbox verification — not mocked, and honest about platform-dependence.** Ran on this
worktree's actual machine: darwin/arm64, `/usr/bin/sandbox-exec` present (confirmed via
`which sandbox-exec`) — the real, non-stub `apply_darwin.go` backend, not the `apply_unsupported.go`
no-op stub. `requireSandboxTool` (mirroring `go-sandbox`'s own `sandbox/integration_test.go`
convention exactly) skips — not fails — on a host missing `sandbox-exec`/`bwrap`, so these tests
degrade safely on a CI machine lacking either tool without ever silently treating "skipped" as
"verified"; on *this* machine, every one of them ran for real and passed:
  - `TestExecuteGated_WriteWithinGrantSucceeds_OutsideGrantBlocked` — one real `sandbox.Apply`
    call, one real `/bin/sh -c` subprocess, granting write to one directory and explicitly denying
    another (via the grant's `fs.deny`, per the darwin default-allow finding above — bare omission
    from `fs.write` alone is not, on this platform, sufficient to prove enforcement). Verified via
    **real filesystem state after execution** (`os.Stat`), not just captured stdout text: the
    granted-path write file exists on disk; the denied-path write's target file does **not** exist
    on disk. This is the task's own literal Done-means requirement ("attempts an operation outside
    the granted bounds... and confirms the sandbox actually blocks it, not just that the gate 'would
    have' refused it") satisfied with a real, checked, on-disk negative result.
  - `TestExecuteGated_NetworkBlockedOutsideGrant_AllowedWithGrant` — a real local HTTP listener,
    real `curl` subprocess dialing it from inside the sandbox: fails with no network capability
    granted (`Net: false, AllowLoopback: false`, the default), succeeds when
    `{"network":{"allow_loopback":true}}` is granted. Network is the one `Profile` field both
    `go-sandbox` backends robustly, symmetrically enforce (SBPL `deny network*` on macOS;
    network-namespace unshare on Linux) — used here as an additional, maximally-portable proof
    alongside the FS-bounds test above, not a replacement for addressing the task's own
    filesystem-write example directly.
  - `TestExecuteGated_HashMismatch_ReapprovalRequired` — a real end-to-end re-install: installs a
    fixture skill via a real `ParsePackageDir` → `skillvendor.Store.Write` → `store.CreateSkill`
    pipeline (mirroring `resolver_test.go`'s own `installFixture` convention — no test-only
    cross-package dependency on `internal/skillinstall`, matching that file's precedent exactly),
    grants an agent against the resulting hash, confirms `ExecuteGated` succeeds, then genuinely
    re-installs the *same* slug with different body content (`reinstallGateFixture`, mirroring
    `internal/skillinstall`'s own re-sync `upsertIndex` shape: same row, new address, bumped
    version) — confirms the resulting hash actually differs, confirms `ExecuteGated` now refuses
    with `*ReapprovalRequiredError` carrying both the stale-approved and current hashes, then
    confirms re-approving (updating the grant's `ApprovedContentHash` to the new hash) restores
    execution.
  - `TestExecuteGated_NoGrantRow_Refused` / `TestExecuteGated_BareAssignmentGrant_Refused` — both
    of the no-grant-row resolution's two cases (item 4, above), each asserting the specific typed
    `*GrantRequiredError.Reason`.
  - `TestGate_ExactlyOneSandboxApplyCallInPackage` — an AST-based static check (parses every
    non-`_test.go` file in `internal/skill/`, counts `sandbox.Apply` call expressions) confirming
    exactly one call site exists in the package — this task's own Done-means grep requirement, made
    into a real, permanent regression test rather than a one-off manual grep.

**`go build ./cmd/nanite/`, `go vet ./...`, `go test ./...`** all pass — full suite, run twice
(once scoped to `internal/skill/...` during development, once the full repo-wide suite at the end).
`go vet`'s only findings are the same two pre-existing, unrelated `stopReaper`/`stopRuntimeReaper`
findings in `internal/service/container.go` that task `02`'s own Work Log already confirmed
pre-existing via `git blame`.

**GLOSSARY.md.** Added a **"Skill capability gate"** entry immediately after the existing "Skill
Materializer" entry (which already forward-referenced "`TASKS/skills/09`'s policy/sandbox layer"),
following this file's established disambiguation-entry pattern (what it is, and what it is
explicitly NOT — distinguished from the dormant `go-agent-wrapper` `policy.Engine`/`policy.Store`
mechanism). Checked the file for collisions on every new Go identifier (`Gate`, `Capabilities`,
`FSCapability`, `NetworkCapability`, `GateDecision`, `DecisionMode`, `GrantRequiredError`,
`ReapprovalRequiredError`) before writing anything — none found; "gate" appears elsewhere only in
unrelated Team/Workflow prose ("phase/gate sequence," `StepKindGate`), not as a colliding Go type
name.

**No schema migration needed.** This task operates entirely on task `02`'s already-landed
`agent_known_skills` grant columns and `skills.content_hash` — confirmed no new migration was
required, matching the task file's own expectation.

**Process notes followed.** No `git stash` used anywhere in this session. All live verification
(the real `store.New`/`skillvendor.New` calls in tests) used `t.TempDir()`-rooted absolute scratch
paths exclusively — no relative path or real tracked directory was ever touched; `git status
--short` confirms only this task's two new files (`internal/skill/gate.go`,
`internal/skill/gate_test.go`) plus the `GLOSSARY.md` edit and this task file's own edits are
present.

## Fix required (fresh reviewer, 2026-08-21 — see `TASKS/ESCALATIONS.md`'s matching entry)

**Bug, reproduced directly: sandboxed skill execution leaks the full, unfiltered host process
environment — a real secret-exfiltration path.** `Gate.run` (`internal/skill/gate.go`) never sets
`cmd.Env`, so Go's `exec.Cmd` inherits the current process's environment verbatim — including
whatever real secrets the running Nanite server process holds. A skill granted the tightest
possible default posture (no `FS`, no `Network`, nothing elevated) can run `` !`env` `` or
`` !`printenv` `` as an ordinary "compute" marker and get the full host environment back verbatim
in `ExecResult.Stdout` — which flows straight into model-visible materialized content. No
capability grant elevation is needed; it's unconditional. `go-sandbox`'s own Linux backend
provides no safety net either (its loopback-helper path explicitly re-inherits the full
unfiltered `os.Environ()`).

**Why this is this gate's own responsibility, not a library limitation to work around:**
environment filtering is a plain Go-level `cmd.Env` decision, entirely orthogonal to
`sandbox.Profile`/`sandbox.Apply` — nothing about `go-sandbox`'s own design needs to back this.
This codebase already has a working, actively-used implementation of exactly this control for the
identical problem class (agent-triggered subprocess execution): `internal/sandbox/exec.go`'s
`filterSecrets(environ []string) []string` (strips any env var whose name contains
`KEY`/`SECRET`/`TOKEN`/`PASSWORD`/`CREDENTIAL`/`AUTH`, via `isSecretKey`) — already used by that
same file's own `UserExec` path (`env := filterSecrets(os.Environ())`). That file also has a
stricter `buildAgentEnv` (minimal allowlist: `HOME`/`USER`/`LANG`/`TERM` plus a restricted `PATH`,
used for MCP dev-tool execution), but the reviewer's recommendation — and this fix's required
floor — is the `filterSecrets` blocklist approach: unconditional secret-stripping of the full
inherited environment, not a switch to an allowlist (which risks breaking legitimate skill
scripts that need ordinary env vars like `HOME`/`LANG`/`PATH` beyond that narrow allowlist).

**What to do:**

1. In `Gate.run`, before calling `sandbox.Apply`, set `cmd.Env` to a secret-filtered copy of the
   inherited environment — apply the equivalent of `internal/sandbox.filterSecrets(os.Environ())`
   unconditionally, independent of any capability grant (this is a floor, not something a grant
   can opt out of). Decide, and document your reasoning in the Work Log: reuse
   `internal/sandbox`'s `filterSecrets`/`isSecretKey` directly (requires exporting them from that
   package, since they're currently unexported — check whether that's a reasonable, narrow export
   or whether a package-local equivalent in `internal/skill` reads cleaner, matching this batch's
   own established precedent of adapting a pattern rather than always importing across packages
   when the two call sites' needs might diverge over time).
2. Add a regression test: a granted skill (any capability posture, including the bare default) run
   with a marker or script that reads its own environment (e.g. `` !`env` `` or an equivalent
   `printenv`-style command) must NOT see a secret-shaped environment variable your test
   deliberately sets before running it (e.g. set `NANITE_TEST_SECRET_TOKEN=should-not-leak` in the
   test process's own environment via `t.Setenv`, then confirm the sandboxed command's captured
   stdout does not contain `should-not-leak`). This must be a real, unmocked test exercising the
   actual `Gate.run`/`sandbox.Apply` path, matching this task's own established real-sandbox-test
   discipline — not a unit test against `filterSecrets` in isolation only (that alone doesn't prove
   `Gate.run` actually applies it).
3. Confirm non-secret environment variables (e.g. `HOME`, `PATH`, `LANG`) still pass through
   correctly after filtering, so a real skill script relying on ordinary environment context isn't
   broken by this fix — add or extend a test confirming this.
4. Re-verify `go build`/`go vet`/`go test ./internal/skill/... -race -count=1` clean when done.

**One minor, non-blocking doc-precision nit from the review, not required to fix:** the code's
`ApprovedContentHash == ""` check comment describes matching `store.AgentKnownSkill.IsBareAssignment()`'s
"exact shape," but it's actually broader/more conservative (doesn't also require
`Pinned`/`ActivationCount`/etc. all zero) — the actual security behavior is correct and arguably
stricter, just an imprecise comment. Optional polish, not required as part of this fix.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
