# Confirm and disclose the macOS seatbelt read/process-inspection boundary tradeoff

**Phase:** Wave 1 — Release-blocking trust boundaries (per remediation guide §4)
**Status:** implemented
**Depends on:** none within this batch. Conceptually pairs with
`01-sandbox-fail-closed-without-bwrap.md` in this same folder (both findings
leave the *read*/network boundary less enforced than a naive reading of
"sandboxed" would suggest — GO-SEC4-002 on Linux network, GO-SEC4-005 on
macOS file reads/IPC), but there is no build-order dependency between them;
they can be picked up independently or by the same implementer.
**Touches:** primarily documentation (a user-facing security-disclosure
doc, if the architect confirms one should exist, plus a correction to
`docs/hardening-phase-plan.md`'s inaccurate Tier 2 description — see
"Current behavior"). `internal/sandbox/os_darwin.go` is explicitly **not**
expected to change code-wise unless the architect decision (see below)
concludes the tradeoff itself should be narrowed, not just disclosed.

```yaml
requires_architect_decision: true
requires_security_review: false
requires_regression_test: false
```

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 1 — release-blocking trust boundaries · **Dispatch unit:** `W1`
> - **Depends on:** `00/01`
> - **Blocks:** none
> - **Parallel-safe with:** all of Wave 1
> - **Gated on:** AD-03 — determines whether this stays docs-only or narrows the tradeoff in code.
> - **requires_security_review:** true · **requires_regression_test:** false

> ## ✅ AD-03 DECIDED (2026-08-22) — disclose; do not narrow the boundary
>
> The unrestricted file reads, mach-IPC, and process inspection permitted by
> `internal/sandbox/os_darwin.go`'s seatbelt profile are an accepted,
> intentional tradeoff. **`os_darwin.go` does not change.** This task's own
> conditional ("not expected to change code-wise unless the architect decision
> concludes the tradeoff itself should be narrowed") resolves to: it does not.
>
> The remediation is the **disclosure gap**, which Wave 0 confirmed is real:
> `docs/hardening-phase-plan.md`'s Tier 2 description claims protections the
> shipped code does not provide. Correcting it to match reality is the whole
> job — that inaccuracy is the live harm, because it tells an operator they
> have a boundary they do not have.
>
> `GO-SEC4-005` is `remediate`, not `accepted-risk`: what was accepted is the
> code posture, not the finding. This task is otherwise correctly scoped as
> written.

## Context

### Finding addressed

- **GO-SEC4-005** (low severity, confidence high, `requires_architect_decision: true`
  in the audit's own finding record) — macOS seatbelt profile stays
  `(allow default)` for file reads, mach-IPC, and process-inspection by
  design. The 2026-08-21 audit frames this as a re-confirmation, not a new
  discovery: "the original finding frames this as an explicit, defensible
  beta-stage tradeoff contingent on user-facing disclosure (not
  independently re-verified here)." (`REPORT.md` §8.12.) The audit
  explicitly notes it did **not** re-verify whether that disclosure
  condition is actually met — this task is that verification.

Source: `docs/audits/2026-08-21-go-quality/REPORT.md` §8.12 and
`docs/audits/2026-08-21-go-quality/findings.json` (`GO-SEC4-005`). The
original, fuller writeup of this tradeoff is
`docs/audits/2026-04-10-sandbox-hardening/09-medium-seatbelt-allow-default-sandbox-escape-surface.md`
— read that file in full before starting; it contains the specific list of
what `(allow default)` leaves open (file reads anywhere the user can read,
process enumeration via `ps`/`sysctl`/mach ports, mach-IPC to arbitrary
bootstrap-registered services, signals to same-user processes, unix-socket
connections, and unrestricted subprocess spawning) and a graduated set of
near-term/medium-term recommendations this task should treat as the starting
menu of options, not a mandate to implement all of them.

### Root cause

This is not a code defect in the sense the other findings in this folder
are — `os_darwin.go`'s own doc comment is explicit and correct about the
choice: `"Strategy: allow default, then deny file writes outside sandbox and
network. This is more practical than deny-default because macOS processes
need many mach ports, sysctls, and IPC operations that are hard to
enumerate."` (`internal/sandbox/os_darwin.go:54-56`.) The 2026-04-10 audit
judged this a defensible engineering tradeoff for a beta product *on the
condition that it's disclosed to end users* — the root cause this task
addresses is that **the disclosure condition was never independently
verified**, and — see below — there is direct evidence current internal
documentation actively describes stronger guarantees than the code
provides, which is a worse state than no documentation at all.

### Current behavior

The seatbelt profile itself, unchanged in substance since the 2026-04-10
audit's finding 09:

```go
// internal/sandbox/os_darwin.go:96-98
var b strings.Builder
b.WriteString("(version 1)\n")
b.WriteString("(allow default)\n\n")
```

...followed by a narrow `(deny file-write* ...)` carve-out
(`os_darwin.go:100-117`, restricted to the sandbox dir / extra write path /
`/tmp` / `/dev/null` / `/dev/tty` / `/dev/fd`) and a network-only deny
(`os_darwin.go:119-130`). Nothing denies `file-read*`, `process-exec*`,
`mach-lookup`, `mach-register`, `ipc-posix-*`, `signal`, or `sysctl-read`.

The current code's own comment is honest about this, in a spot only a
developer reading source would see:

```go
// internal/sandbox/os_darwin.go:66-67
// Reads are unrestricted under (allow default), so this only affects
// the write boundary.
```

**The disclosure gap, found directly during this task's research:**
`docs/hardening-phase-plan.md` — an internal engineering planning doc, not
generated by the audit, found by grepping the repo for existing sandbox
documentation — describes the *intended* Tier 2 (OS-level isolation)
behavior in terms that **do not match what `os_darwin.go` actually
implements**:

```text
docs/hardening-phase-plan.md:136-142
**Tier 2 — OS-level isolation (agent-exec only):**
- macOS: `sandbox-exec` (seatbelt) profile:
  - Read/write within sandbox CWD only
  - Network: proxy-allowlisted domains only (or deny-all for pure compute)
  - No process inspection, no signal sending to non-child processes
- Linux: bubblewrap with equivalent constraints
- Fallback: Tier 1 on unsupported platforms, with a logged warning
```

"Read/write within sandbox CWD only" and "No process inspection, no signal
sending to non-child processes" are **not true** of the shipped
implementation — reads are unrestricted (`allow default`), and nothing
denies `process-exec*`, `signal`, or process/mach introspection. This is a
planning document describing an aspirational Tier 2 design, not a
statement about current behavior, and it does not appear to be dated or
marked as superseded — a reader (engineer or, if this doc or its claims
ever reach user-facing material, a user) encountering it today has no signal
that the "Read/write within sandbox CWD only" line was never implemented and
the code deliberately chose a weaker, `(allow default)`-based design instead.
This is stronger and more concrete evidence for the disclosure question this
task exists to answer than "no doc exists" would be: the risk isn't merely
*absence* of disclosure, it's an *existing* internal document making a
claim the shipped code contradicts.

No dedicated user-facing security-model document (e.g. a `SECURITY.md`, a
"what the sandbox does and doesn't protect against" doc) was found anywhere
under `docs/` at the time this task was written (checked via repo-wide grep
for `seatbelt`, `sandbox-exec`, `allow default` across `docs/*.md` and
`docs/**/*.md`, excluding the audit directories themselves). This task
should re-verify that before concluding — see "What to do."

### Desired invariant

Users are not misled about what the sandbox does and doesn't protect
against, on either platform. This pairs conceptually with GO-SEC4-002 in
`01-sandbox-fail-closed-without-bwrap.md`: both platforms leave the
read/network boundary less enforced than a naive reading of "sandboxed"
would suggest, and both should end up in a state where that's either
(a) tightened, or (b) accurately and visibly documented — not silently
assumed.

### Scope

This task is deliberately lighter-weight than
`01-sandbox-fail-closed-without-bwrap.md` — the "fix" here is primarily a
documentation/disclosure question, not necessarily a code change:

- Confirm (architect decision, see below) whether the `(allow default)`
  tradeoff still holds as intentional design for the current product stage
  (still beta? has the risk calculus changed since 2026-04-10?).
- Correct or annotate `docs/hardening-phase-plan.md`'s Tier 2 description
  (`lines 136-142`) so it stops describing guarantees the shipped code does
  not provide — either by updating it to match actual behavior, or by
  marking it clearly as an unrealized/superseded design intent if the
  Tier-2-as-originally-planned goal is still live but not yet built.
- If the architect confirms user-facing disclosure is warranted and doesn't
  already exist elsewhere (verify first — see "What to do"), add or extend
  a security-model doc stating plainly: the sandbox denies file writes
  outside the sandbox dir/extra write path and denies non-localhost network
  (or all network, depending on mode), but does **not** deny file reads,
  process inspection, mach-IPC, signals to same-user processes, or unix
  socket connections. State this for both platforms, tying in the
  Linux-side equivalent gaps this same audit section found (narrowed
  `--ro-bind` read set on Linux is real hardening already landed post-2026-04-10
  — see `internal/sandbox/os_linux.go:16-37`'s `bwrapRoBindCandidates` — so the
  two platforms are *not* symmetric on file reads specifically; Linux is
  materially better here since the 2026-04-10 audit. Only mach-IPC/process-inspection/signal-style
  gaps are genuinely comparable across platforms, since Linux's namespace
  unsharing (`--unshare-pid --unshare-ipc --unshare-uts`, `os_linux.go:122-130`)
  already closes the process/IPC visibility gap that macOS still has open).
- Code changes to `os_darwin.go` (e.g. adding `file-read*` denies for known
  credential directories, per the 2026-04-10 finding's recommendation #3) are
  **only** in scope if the architect decision concludes the tradeoff should
  be narrowed rather than merely disclosed — see "Non-goals."

### All production callers

Not applicable in the security-migration sense the guide's template
otherwise asks for (this task is not fixing a primitive with multiple
call sites) — `applyOSSandbox` on darwin has the same two production
callers as documented in `01-sandbox-fail-closed-without-bwrap.md`
(`AgentExec` at `internal/sandbox/exec.go:158`, `UserExec` at
`internal/sandbox/exec.go:203`), unaffected by this task unless the
architect decision expands scope to a code change.

### Proposed direction

**This is the architect decision the finding record flags
(`requires_architect_decision: true`) and the remediation guide's §9 item 3
(auth/bind/TLS/warning posture) family of "does the documented intent match
the implementation" questions this audit repeatedly raised.** Two questions,
not mutually exclusive:

1. **Does the `(allow default)` tradeoff still hold?** I.e., is a
   deny-default (or narrower allow-default with explicit credential-directory
   denies, per the 2026-04-10 finding's recommendation #3) seatbelt profile
   now warranted given the product's current stage, or is `(allow default)`
   still the right call for now? If the latter, this task's job is
   disclosure only. If the former, this task's scope grows to include a
   real `os_darwin.go` change and should be re-scoped/escalated rather than
   quietly expanded — flag it back to the planner rather than silently
   absorbing a bigger change into what was specified as a documentation
   task.
2. **Is disclosure adequate?** Given the `docs/hardening-phase-plan.md`
   discrepancy found above, the honest current answer is "no, and there is
   active evidence to the contrary" — an internal doc currently claims a
   stronger guarantee than what ships. At minimum, that specific document
   needs correcting. Whether a *new*, genuinely user-facing security-model
   document should also be created is the architect's call — the guide's own
   Wave 1 framing treats this as worth a decision, not a rubber-stamp.

### Non-goals

- **Not** a full seatbelt profile rewrite to deny-default (the 2026-04-10
  finding's own "Medium term" recommendation #5 — "Switch to deny-default
  after cataloguing the exact mach services, ports, and sysctls needed" — is
  explicitly framed there as multi-day, post-beta work, not this task's
  scope).
- **Not** per-interpreter seatbelt profiles (same finding's recommendation
  #6) — out of scope.
- **Not** re-litigating whether `(allow default)` was originally the right
  call — the 2026-08-21 audit explicitly did not re-verify this in full and
  neither does this task; it confirms the current state and the disclosure
  gap, and queues the "still the right tradeoff?" question to the architect
  rather than answering it here.
- **Not** touching Linux's `bwrap`-present-and-working read-boundary
  (`bwrapRoBindCandidates`) — that side has already been meaningfully
  hardened since 2026-04-10 (see "Scope" above) and is healthy; this task is
  about macOS's still-open gap and, if the architect confirms it's still an
  issue, correspondingly about GO-SEC4-002's Linux network gap for the
  disclosure angle only (the enforcement angle for GO-SEC4-002 is
  `01-sandbox-fail-closed-without-bwrap.md`'s job, not this task's).

### Dependencies

- None within this batch.
- **External dependency: an architect decision is required** on both
  questions above before this task's documentation work is finalized (the
  verification/grep-for-existing-docs legwork and the
  `hardening-phase-plan.md` correction can proceed without waiting, since
  those are true regardless of which way the tradeoff question resolves).

### Tests required

None in the automated-test sense — this is a documentation task by default
scope. If the architect decision expands scope to an `os_darwin.go` change
(see "Proposed direction," question 1), that change would need:

- A regression test against `seatbelt` profile generation confirming the
  new deny rules are present in generated profile text for the relevant
  paths (following the existing pattern of `os_darwin_test.go` if one
  exists — check before assuming; the file wasn't confirmed present during
  this task's authoring).
- A test confirming `validateSeatbeltLiteral`-covered interpolation still
  rejects unsafe bytes in any newly-added interpolated profile section
  (defense-in-depth against the profile-injection class the 2026-08-21 audit
  confirmed already-fixed elsewhere in this file).

But note again: this is conditional on the architect expanding scope, not
this task's default expectation.

### Prevention

- Whatever documentation this task lands (corrected `hardening-phase-plan.md`
  and/or a new security-model doc) should be explicitly cross-referenced
  from `internal/sandbox/os_darwin.go`'s own doc comment (which already
  cross-references the 2026-04-10 audit directory) so future readers of
  either the code or the docs land on the same, current, accurate
  description — closing the gap that let `hardening-phase-plan.md` drift
  from actual behavior undetected.
- Consider (note for `12-quality-ratchet-and-standards/`, not required here)
  whether planning docs like `hardening-phase-plan.md` that describe
  intended-but-not-yet-verified behavior should carry an explicit "design
  intent, not verified current behavior" header to prevent this exact class
  of drift recurring elsewhere in the repo.

### Verification

No build/test commands apply to the default documentation-only scope.
Verification is a manual review checklist:

- [x] Confirmed (via repo-wide search, not just the locations cited above)
      whether any other doc under `docs/` or `ui/` makes claims about
      macOS sandbox read/process-inspection guarantees, and corrected or
      cross-referenced all of them consistently.
- [x] `docs/hardening-phase-plan.md`'s Tier 2 macOS description no longer
      states "Read/write within sandbox CWD only" or "No process
      inspection, no signal sending to non-child processes" as current
      behavior without qualification.
- [x] Architect decision on both questions ("still the right tradeoff?",
      "is disclosure now adequate?") is recorded in this task's Work log,
      including which of the 2026-04-10 finding's recommendation options (if
      any) were selected.
- [x] If scope expanded to an `os_darwin.go` change per the architect
      decision, that change's own build/test/verification is documented
      following `01-sandbox-fail-closed-without-bwrap.md`'s pattern, and this
      task file's scope-expansion is explicitly logged (per this project's
      task-file-template guidance: a task whose real scope grows doesn't get
      silently narrowed back down). — **N/A**, scope did not expand; see
      Work log.

### Risk / rollback

Minimal — documentation-only changes carry no runtime regression risk. If
scope expands to a code change, that change's risk profile should be
assessed at that time following the same pattern as
`01-sandbox-fail-closed-without-bwrap.md` (seatbelt profile changes are
low-blast-radius: a broken profile fails the sandboxed process outright
rather than silently under-enforcing, since `sandbox-exec -f <profile>`
either parses or the process fails to start).

### Done means

- [x] Architect decision recorded for both open questions.
- [x] `docs/hardening-phase-plan.md`'s Tier 2 macOS description corrected or
      clearly marked as unrealized design intent, not current behavior.
- [x] A definitive answer exists (in this task's Work log) to "is the
      read/process-inspection tradeoff currently disclosed to end users
      adequately" — not "not re-verified," an actual yes/no with evidence.
- [x] If the architect concluded disclosure is inadequate, a genuinely
      user-facing (or clearly-scoped internal-only, if that's the
      architect's call) document exists stating the sandbox's actual
      guarantees and gaps on both platforms. — architect (AD-03) explicitly
      scoped the remedy to the internal-doc correction only ("`02/02` is
      already scoped for exactly this and needs no re-scope"); see Work log
      for why no new document was created.
- [x] If the architect concluded the tradeoff itself should narrow, that
      work is re-scoped as its own task rather than silently expanded here,
      and the escalation is recorded. — **N/A**, AD-03 explicitly decided
      the tradeoff does not narrow.

## Work log

**2026-08-22, worker session.**

### What was checked

- Read `docs/engineering/EXECUTION-PROCESS.md`, this task file in full
  (including the `✅ AD-03 DECIDED` banner), `docs/engineering/GLOSSARY.md`,
  and `TASKS/audit-remediation/ARCHITECT-DECISIONS.md`'s AD-03 section
  before starting, per dispatch instructions.
- Re-read `internal/sandbox/os_darwin.go` and `internal/sandbox/os_linux.go`
  directly (not just the task file's quoted excerpts) to confirm current
  shipped behavior before writing any disclosure text. Confirmed:
  - `os_darwin.go`: `(allow default)` + narrow `(deny file-write* ...)`
    (sandbox dir / extra write path / `/tmp` / `/private/tmp` / `/dev/null` /
    `/dev/tty` / `/dev/fd`) + network deny (or allow-localhost-only when a
    proxy allowlist is configured). No `file-read*`, `process-exec*`,
    `mach-lookup`/`mach-register`, `signal`, or `sysctl-*` denies — matches
    the task file's "Current behavior" section exactly.
  - `os_linux.go`: still has the pre-AD-01/AD-02-remediation shape (fail-open
    `sync.Once`-warned fallback when `bwrap` is absent; `--unshare-net` only
    when `networkAllow` is empty) — confirms `02/01` in this same folder has
    not landed yet, which is expected and does not block this task
    (`Depends on: none`, `Parallel-safe with: all of Wave 1`).
  - Read `docs/audits/2026-04-10-sandbox-hardening/09-medium-seatbelt-allow-default-sandbox-escape-surface.md`
    in full, per the task file's instruction, before writing the disclosure
    text.
- Repo-wide search (not just `docs/hardening-phase-plan.md`) for other docs
  under `docs/` or `ui/` making claims about macOS sandbox read/process-
  inspection guarantees:
  - `grep -rniE "seatbelt|sandbox-exec|allow default" docs/ ui/` (excluding
    `docs/audits/`) and a second, narrower pass on
    `"read.?write within|no process inspection|no signal sending|process
    inspection"`.
  - Found and corrected a **second** disclosure gap beyond
    `docs/hardening-phase-plan.md`: `docs/programmatic-tool-calling-safety.md`
    (a landed, real feature doc for `nanite_run_python`) claimed, in two
    places, that "the OS sandbox (sandbox-exec / bwrap) blocks most of
    these [file reads]" and that macOS's seatbelt "already blocks outbound
    network for the sandbox-exec child." Traced the actual code path
    (`internal/selftools/self_tools_python.go`'s `RunPythonSandbox` →
    `exec.CommandContext(...)` + `applySandboxSysProcAttr` which only sets
    `Setpgid`; dispatched from `internal/selftools/self_tools_transport.go`'s
    `callRunPython`) and confirmed `python_run`'s subprocess does **not**
    route through `sandbox.AgentExec`/`applyOSSandbox` at all — no seatbelt,
    no bwrap wraps this process today, on either platform. So this doc's
    claims were doubly wrong: not only does macOS's `(allow default)` not
    restrict reads (the primary finding), but this specific tool doesn't
    even get the OS-level sandbox applied to check that boundary against.
    Corrected all three affected passages (`~52-67`, `~229-230`, `~234-243`)
    to state the actual current behavior and cross-reference the
    `hardening-phase-plan.md` correction.
  - Everything else the grep surfaced (`docs/architecture/chat-system/*`,
    `docs/research/anthropic-digest-cluster-3-4.md`,
    `docs/research/nanite-alignment-matrix.md`,
    `docs/research/gaps-and-opportunities.md`,
    `docs/engineering/architecture/16-agent-host.md`,
    `docs/handoff/2026-04-10-installer-audit-handoff.md`,
    `docs/engineering/orchestrator-kickoffs/*`) either (a) describes a
    sibling app's (Agent Mux) or Anthropic's own (Claude Code) sandbox model
    as external research/comparison material, not a claim about Nanite's
    shipped behavior, (b) mentions "OS sandbox-exec" only as a mechanism
    name without asserting a specific read/write/process-inspection
    guarantee, or (c) is itself an audit/handoff document already citing the
    finding correctly. None of these needed correction.
  - `ui/src` has zero references to `sandbox`/`seatbelt`/`sandbox-exec`
    anywhere (`grep -rniE "sandbox" ui/src` — no output) — no frontend copy
    to correct.
  - `.nanite/agents/reviewer-backend.md` (a developer-persona boot file, not
    under `docs/` or `ui/` and explicitly out of this task's stated search
    scope per **GLOSSARY.md**'s own note distinguishing Nanite's runtime
    agent system from the `.nanite/` developer-boot convention) mentions
    seatbelt/bwrap but makes no specific read/write/process-inspection
    guarantee claim — checked, left untouched, not in scope.
  - Confirmed (again, independently) no `SECURITY.md` or dedicated
    security-model doc exists anywhere in the repo
    (`find . -iname "SECURITY*.md" -o -iname "*security-model*"` —
    no output, worktrees/node_modules excluded), and that no other
    user-facing doc (`docs/beta-known-issues.md`,
    `docs/beta-readiness-install-setup.md`, `docs/dev-mode.md`,
    `docs/developer-mode-gate.md`, `docs/mcp-trust-model.md`) mentions
    sandbox/seatbelt/isolation at all.

### What was corrected

1. **`docs/hardening-phase-plan.md:136-179`** — the Tier 2 macOS bullets
   ("Read/write within sandbox CWD only", "No process inspection, no signal
   sending to non-child processes") are now explicitly marked as this task's
   original 2026-04-07 *design intent*, with a correction block above them
   stating actual shipped behavior (writes scoped to sandbox dir + a fixed
   allowlist; reads/process-inspection/mach-IPC/signals unrestricted under
   `(allow default)`), citing `internal/sandbox/os_darwin.go`, the 2026-04-10
   finding 09 writeup, and AD-03. The disputed lines themselves are
   struck through (`~~...~~`) with an inline correction, rather than
   silently deleted, so the historical design-intent record is preserved
   (per this doc's own nature as a dated planning log) while no longer
   reading as current behavior. Also corrected the Linux comparison to
   accurately state Linux is *not* symmetric with macOS on the read
   boundary (narrower `--ro-bind` set, namespace-unshared PID/IPC/UTS) —
   this matches the task file's own "Scope" section instruction.
2. **`docs/programmatic-tool-calling-safety.md`** (three spots, see above) —
   corrected the "Network block is best-effort" bullet, the "Known
   limitations" network bullet, and the "Known limitations" filesystem-read
   bullet to state that `python_run` does not currently route through the
   OS-level sandbox (seatbelt/bwrap) at all, so the import-level Python
   monkey-patch is, today, the sole defense on every platform for both
   network and (by omission) filesystem reads — cross-referencing the
   corrected `hardening-phase-plan.md` Tier 2 section for what the OS-level
   sandbox does and doesn't cover where it *is* actually applied
   (`sandbox.AgentExec`/`UserExec`).

### Architect decision (both open questions)

Recorded verbatim in `TASKS/audit-remediation/ARCHITECT-DECISIONS.md`'s
AD-03 section, decided 2026-08-22, read and confirmed before writing any
disclosure text:

- **"Does the `(allow default)` tradeoff still hold?"** — **Yes, decided:
  disclose, do not narrow.** The unrestricted reads/mach-IPC/process
  inspection are an accepted, intentional beta-stage tradeoff.
  `internal/sandbox/os_darwin.go` does not change code-wise. This worker did
  not touch that file — confirmed via `git status --short` after all edits
  (only the two docs files above are modified).
- **"Is disclosure now adequate?"** — AD-03's own text: *"the remediation is
  the disclosure gap... Correcting it to match reality is the whole job"*
  and *"`02/02` is already scoped for exactly this and needs no re-scope."*
  No recommendation option from the 2026-04-10 finding's "Near term"/"Medium
  term" list was selected beyond recommendation #1 ("Document the
  allow-default stance in user-facing security docs") — and even that is
  satisfied at the *internal engineering documentation* level, not by
  creating new end-user product copy; recommendations #2-#6 (per-session
  `/tmp`, credential-directory read denies, `process-exec*` restriction,
  deny-default switch, per-interpreter profiles) remain explicitly
  out of scope per this task's own "Non-goals" and AD-03's "the code does
  not change."

### Definitive answer: is the read/process-inspection tradeoff currently disclosed to end users adequately?

**No, but the specific harm this task exists to close is fixed, and no
further work is required by AD-03's own decision.** Breaking this into the
two things the task file's evidence actually supports:

- **The active harm identified by Wave 0 — an internal document actively
  contradicting the shipped code — is fixed.** Both
  `docs/hardening-phase-plan.md` and `docs/programmatic-tool-calling-safety.md`
  now state the real behavior instead of a stronger guarantee than what
  ships. Verified by re-reading both corrected files in full after editing
  and by a fresh repo-wide grep confirming no remaining unguarded
  "Read/write within sandbox CWD only" / "no process inspection" / "OS
  sandbox blocks" style claims exist anywhere under `docs/` (excluding the
  now-struck-through, explicitly-marked-superseded lines) or `ui/`.
- **There is still no genuinely end-user-facing document** (a `SECURITY.md`
  or equivalent an operator would actually read, as opposed to an internal
  engineering planning doc under `docs/`) stating the sandbox's guarantees
  and gaps in product terms. Confirmed absent by direct search (above). This
  gap is real and unresolved by this task.
- **This second gap is not something this task is leaving open by oversight
  or default-to-cut** — it is the direct, explicit consequence of AD-03's
  own decision. AD-03 states the disclosure-doc correction "is already
  scoped for exactly this and needs no re-scope," which is the architect
  affirmatively deciding *not* to require a new user-facing security-model
  document as part of this task's remediation. Per `EXECUTION-PROCESS.md`'s
  "Reasoning vs. instruction" rule, that decision (an instruction, not mere
  Context reasoning) governs; this worker did not create a new document.
  If a genuinely user-facing security posture doc is wanted later, that is
  a follow-up decision for the architect/operator to make explicitly, not
  something to infer from this task's own "Done means" checklist wording.

### Scope

No expansion. `internal/sandbox/os_darwin.go` was read for verification but
not modified — confirmed via `git status --short` (only the two `docs/*.md`
files are changed). The task file's own "Prevention" section suggests
cross-referencing this correction from `os_darwin.go`'s doc comment; that
was deliberately **not** done, because the dispatch instruction for this
task was explicit and unambiguous ("`internal/sandbox/os_darwin.go` does NOT
change code-wise... Do not touch that file's logic") and because that
Prevention-section claim itself turned out to be slightly inaccurate on
inspection — `os_darwin.go` does **not** currently cross-reference the
2026-04-10 audit directory the way `os_linux.go` does (checked via
`grep -n "2026-04-10\|audit" internal/sandbox/os_darwin.go` — no output);
only `os_linux.go` does. Noting this correction for the record rather than
acting on it, per the "distinguish the decision from its rationale"
discipline — the instruction not to touch the file stands regardless of
whether the Prevention section's supporting claim was fully accurate.

### Baseline check

`go build ./cmd/nanite/` — passes. `go vet ./...` — passes except for two
pre-existing, unrelated warnings in `internal/service/container.go` (about
`stopRuntimeReaper`/`stopReaper` context-leak paths); that file was not
touched by this task, so these predate this change and are out of scope.
`git status --short` confirms only the two `docs/*.md` files and this task
file itself are modified — zero Go files touched, so `go test ./...` was not
run (nothing it could regress).

## Review notes

<Reviewer fills this in.>
