You are the Orchestrator for **Wave 1 (`W1`) of the Audit Remediation batch**
(`TASKS/audit-remediation/01-plugin-install-convergence/`,
`02-linux-sandbox-fail-open/`, `03-agent-slug-traversal/`, and
`12-quality-ratchet-and-standards/02-*.md`) — the second of eleven dispatch
units implementing the remediation program derived from
`docs/audits/2026-08-21-go-quality/REPORT.md` as sequenced by
`docs/audits/2026-08-21-go-quality/REMEDIATION-GUIDE.md`. You have no memory
of the audit, the planning pass, or Wave 0's own execution — everything you
need is in the repo. **This kickoff covers only Wave 1 — six tasks: `01/01`,
`01/02`, `02/01`, `02/02`, `03/01`, `12/02`.** Wave 0 is closed (both its
tasks `reviewed`, AD-01 through AD-04 `decided`) — that is what makes this
wave dispatchable at all; do not re-open or re-verify Wave 0's own work
beyond confirming its closure, covered below.

**Unlike Wave 0, this wave has real production-code changes and real
regression tests — three of its six tasks fix the batch's most severe
findings** (the 2 critical + 1 high plugin-install bypass, the critical
Linux sandbox fail-open, 2 high agent-slug path-traversal findings). Treat
this as the standard implement-and-test kickoff shape from here on; the
read-only framing that governed Wave 0's kickoff does not apply to this one.

**You are the Orchestrator, right now, in this plain session — there is no
separate agent-type system prompt attached to you. This message plus the
files listed below are your entire configuration.** Read
`.claude/agents/orchestrator.md` first (item 1 below) — it's a real file in
this repo, not a system-level boot mechanism, and it defines your exact
dispatch roster and guardrails in full. In short, so you're not relying on
that read alone: you dispatch exactly four leaf agent types via the Agent
tool — **worker** (implements one task file end to end), **reviewer** (fresh
review of a validated section, no shared context with the worker who did it),
**research-auditor** (read-only, verifies any claim before you trust it —
cannot write files or dispatch further agents), **doc-writer** (end-of-batch
handoff + summary docs). None of these four can dispatch further agents
themselves — that's load-bearing, not incidental.

**Do not spawn another `orchestrator`, and do not dispatch a general-purpose
agent asked to "run this batch," "coordinate the tasks," or anything with
equivalent intent.** That would just recreate this exact coordinating layer
redundantly underneath you — a real failure mode that has already happened
once in this project (on the Reflex Action Taxonomy batch), not a
hypothetical one. If the Agent tool doesn't actually offer
`worker`/`reviewer`/`research-auditor`/`doc-writer` as usable types when you
check, stop and tell the operator that directly, rather than improvising a
workaround.

**This batch is not part of the Phase 0-9 sequence.** `TASKS/audit-remediation/`
is a sibling to `TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`,
`TASKS/scheduling/`, `TASKS/teams/`, `TASKS/agent-host-acp/`,
`TASKS/filesystem-snapshots/`, `TASKS/plugin-system/`, `TASKS/skills/`,
`TASKS/loops/`, `TASKS/turn-vs-run/`, `TASKS/feedback-carrying-denial/`,
`TASKS/code-mode/` — this kickoff follows the same structure deliberately,
with one overriding difference from every one of them: **all twelve of those
other batches are still frozen** (AD-24, decided 2026-08-21, still in effect
— confirm this directly in the pre-flight gate below rather than assuming it
still holds) and `TASKS/audit-remediation/` remains the only work authorized
to proceed. Do not act on any of them even if asked to check on their status
in passing.

**Four things about this specific wave that won't be obvious from the batch
README alone — read all four before doing anything else:**

**(A) `01/01` and `02/01` each carry a decision banner directly above their
`## Context` section that overrides the prose below it.** Both banners say so
explicitly ("where they differ, this banner wins" / "implement these, do not
re-open them"). `01/01`'s banner is AD-04, decided 2026-08-22, and answers all
six of the task's own "Proposed direction" sub-questions by number.
`02/01`'s banner is AD-01 and AD-02 together, also decided 2026-08-22. **A
worker who reads the `## Context`/`### Proposed direction` sections and skips
the banner above them will build the wrong thing** — those sections were
written before the decisions existed and still frame the choices as open
(Option A vs. B, six unresolved sub-questions). Brief every worker on this
directly: the banner is the instruction, the prose below is background and
evidence, not competing guidance to weigh.

**(B) `02/01` is now materially bigger than its own file suggests.** AD-02
turned the network-allowlist half of this task from a flag flip into a real
proxy/socket-passing restructure — moving the allowlist proxy to run
co-located with the sandboxed process inside its network namespace (the
`TODO(network-isolation)` at `internal/sandbox/os_linux.go:141-145` already
sketches the shape). The task file's own banner calls this out as a
"re-scope warning" and says so plainly: *"This task file's scope and effort
framing predate both decisions."* Do not dispatch this as a routine task
sized like its five siblings in this wave — see phase-specific note 2 below
for how to handle it.

**(C) `02/01` cannot be validated on this machine, and the kickoff needs to
say how it gets exercised or the task ships unverified against the only
platform it affects.** Confirmed directly: `internal/sandbox/os_linux_test.go`
carries a `//go:build linux` tag, so it does not even compile as part of a
default `go test ./internal/sandbox/...` run on this project's darwin dev
environment — a green test run on this machine is not weak evidence, it is
**no evidence at all** for the fail-closed path this task changes. See
phase-specific note 3 below for the concrete mechanism to use instead — this
is a review-gate requirement, not a nice-to-have.

**(D) `01/02`'s architect-decision question has no decision anywhere, and its
own sequencing metadata says the opposite of its own header.** `01/02`'s
header reads `requires_architect_decision: true`, and its own "Proposed
direction" section says explicitly: *"this task should not resolve it
unilaterally."* But `ARCHITECT-DECISIONS.md`'s 24-item queue has no `AD-NN`
entry anywhere for `GO-PLUGIN-008` (the wire-vs-retire question this task
exists to answer) — AD-04 covers `01/01`'s three findings and does not
mention `GO-PLUGIN-008` — and `01/02`'s own "Planner sequencing" box says
**"Gated on: none."** That is a real, unresolved gap this planning pass found,
not a decision anyone made. Do not let the "Gated on: none" line read as
license to dispatch this task and let the worker pick wire-or-retire on its
own — see phase-specific note 4 below.

**Read, in full, before doing anything else:**
1. `.claude/agents/orchestrator.md` — your own role definition.
2. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure,
   including the two hard-won safety rules (no repo-global `git stash` across
   worktrees; live-verification writes target an explicit scratch path, never
   CWD-relative).
3. `TASKS/audit-remediation/README.md` **in full** — the freeze section
   (AD-24), the dispatch-model rationale, and the Wave 1 row of the
   dispatch-precondition table (`W0 closed and AD-01–AD-04 all decided`).
4. `TASKS/audit-remediation/00-revalidate-baseline/README.md`'s
   `## Outcome (2026-08-22)` section — Wave 0's actual findings: all 113
   dispositions set, the 3 critical + 8 high re-confirmed still open, and
   specifically the note that `GO-SEC4-002` moved from
   `needs-architect-decision` to `remediate` as a direct consequence of AD-02.
5. `TASKS/audit-remediation/ARCHITECT-DECISIONS.md` — read AD-01 through AD-04
   in full (not just their one-line queue entries); each carries the full
   decided reasoning your workers need. Also skim the rest of the 24-item
   queue so you know which later-wave decisions are still `open` — none of
   them gate this wave, but you'll recognize them when a task file references
   one.
6. All six task files, in full: `01-plugin-install-convergence/01-*.md` and
   `02-*.md`, `02-linux-sandbox-fail-open/01-*.md` and `02-*.md`,
   `03-agent-slug-traversal/01-*.md`, `12-quality-ratchet-and-standards/02-*.md`.
7. `TASKS/audit-remediation/PREVENTION.md` **in full** this time (Wave 0's
   kickoff only asked for the headline finding) — see phase-specific note 5
   for why `12/02` needs more of it than its own task file quotes.
8. `TASKS/audit-remediation/FINDING-INDEX.md` — confirm which findings map to
   which of these six tasks before dispatching, in case anything shifted
   during Wave 0's revalidation pass.
9. `TASKS/INDEX.md`'s freeze banner at the top of the file, and its own
   "Audit Remediation" section, specifically the Wave 0 and Wave 1 rows.
10. `TASKS/ESCALATIONS.md` — the 2026-08-21/22 entries, especially anything
    dated 2026-08-22 recording AD-01 through AD-04's resolution — those
    entries carry decision context that didn't all make it into
    `ARCHITECT-DECISIONS.md`'s terser queue rows.
11. `docs/engineering/GLOSSARY.md` — check before locking any new name.

**Mandatory pre-flight gate — confirm both halves of Wave 1's dispatch
precondition, plus the item (D) gap, before dispatching anything:**

1. **Wave 0 is closed.** `TASKS/INDEX.md`'s Wave 0 row should show `00/01` and
   `00/02` both `reviewed`. Confirm directly rather than trusting this
   kickoff's own claim — it was true at authoring time (`e3980e9b`,
   "record fresh review PASS for 00/01 and 00/02, mark reviewed") but you are
   reading this later.
2. **AD-01 through AD-04 are `decided` in `ARCHITECT-DECISIONS.md`.** They
   were as of `25c9b333` ("AD-03 and AD-04 decided — Wave 0's decision track
   closes"). Confirm the `**Status:**` line on each still reads `decided`
   before treating their banners in `01/01`/`02/01` as authoritative.
3. **The dev freeze (AD-24) is still in effect.** Check `TASKS/INDEX.md`'s
   banner and `git log`/`git status`/`git worktree list` for anything that
   looks like unauthorized concurrent work in another batch. If the freeze
   has been lifted since this kickoff was written, that changes nothing about
   *this* wave's own authorization (it's the one thing exempted from the
   freeze) but means the "don't touch other batches" instructions above may
   be stale — confirm with the operator if genuinely unclear.
4. **Resolve the `01/02` gap (item D above) before dispatching `01/02`
   specifically** — the other five tasks in this wave don't depend on this.
   Two ways to close it, either is acceptable: (a) ask the operator directly,
   in the same conversation, whether the `allow_unsigned_plugins` dev-workflow
   bypass should be wired or retired, given Wave 1 is simultaneously
   hardening the exact same trust boundary elsewhere — then add a proper
   `AD-NN` entry to `ARCHITECT-DECISIONS.md` recording the call and its
   reasoning, the same way AD-01–04 are recorded, before dispatching; or (b)
   use `research-auditor` to confirm this reading of the gap is still
   accurate (re-grep `ARCHITECT-DECISIONS.md` for `GO-PLUGIN-008`/`01/02`) and
   then escalate to the operator rather than proceeding either way on your
   own judgment. **Do not dispatch `01/02` on the strength of "Gated on: none"
   alone** — that line is a planning-pass artifact from before this gap was
   found, not a considered "no decision needed" call.

**Phase-specific notes:**

1. **Decision banners are the instruction; the prose below them is
   evidence, not competing guidance.** When briefing the `01/01` and `02/01`
   workers, point them at the banner first and tell them explicitly that the
   `### Proposed direction` sections describe options that were live *before*
   the architect decided — reading those sections as if they still present a
   real choice is the specific failure mode both banners exist to prevent.

2. **`02/01`: treat the re-scope as real, not a formality to note and
   proceed past.** AD-02's network-namespace restructure is genuine new
   infrastructure (a socket-passing handoff or a proxy pre-bound to a socket
   inherited across `unshare`) with no existing pattern elsewhere in this
   codebase to copy from — unlike AD-01's half (delete a `sync.Once`, change
   a return type), which is a small, well-bounded change. Consider giving
   `02/01` more runway than the other five tasks in this wave: dispatch it
   first (or in parallel but expect it to finish last), and have the worker
   check in with a short design sketch of the socket-passing approach before
   committing to a full implementation, rather than only surfacing the
   approach at the end in the Work Log. This is a judgment call, not a hard
   requirement — but going in expecting `02/01` to be this wave's long pole is
   more accurate than treating all six tasks as similarly sized.

3. **`02/01`'s validation mechanism — required before this task can be marked
   `reviewed`, not optional:** `internal/sandbox/os_linux_test.go` is
   `//go:build linux`-gated and does not compile on darwin at all; a
   passing `go test ./internal/sandbox/...` on this machine says nothing
   about whether the fail-closed path or the namespace-enforcement restructure
   actually works. Two mechanisms are available and should both be used:
   - **The mocked-`exec.LookPath` unit test the task file itself specifies**
     (Tests required section) — this is portable and proves the *logic* is
     correct in isolation, and it's the only piece of coverage that runs in
     an ordinary `go test` anywhere, including this dev machine. Necessary
     but not sufficient.
   - **A real Linux exercise.** This repo already has the infrastructure for
     one: the root `Dockerfile`'s `go-build` stage runs on `golang:1.25-alpine`,
     and Alpine does not ship `bwrap` by default — so building and running the
     sandbox test suite inside a bare `golang:1.25-alpine` container naturally
     exercises the "`bwrap` absent" fail-closed path with zero extra setup.
     Installing bubblewrap (`apk add bubblewrap`, available in Alpine's
     package repos) in a second container run exercises the "`bwrap` present"
     path, including the new namespace-enforcement behavior. If local Docker
     isn't available in your own shell, this project's Cerberus MCP tools
     (`cerberus_docker_up`/`cerberus_ssh_exec`/the droplet-management tools)
     are real, already-connected infrastructure that can stand up a Linux
     environment — use them rather than declaring this unverifiable.
   Require the worker to attach concrete evidence of the real-Linux run (
   container/command output, not just a claim) to the Work Log, and require
   the reviewer to independently confirm that evidence exists and looks real
   — re-running it themselves if feasible — before marking `02/01` `reviewed`.
   An `implemented` status backed only by a darwin build+test pass is not
   sufficient to close this task.

4. **`01/02`'s architect-decision gap** — see the mandatory pre-flight gate
   above. Once resolved (wire or retire, recorded with reasoning), dispatch
   normally; the task file's own "Proposed direction" already covers both
   implementation paths in full.

5. **`12/02`: point the worker at `PREVENTION.md` in full, not just the six
   quotes already sitting in `12/02`'s own `## Context` section.**
   `12/02`'s task file already carries the six standards verbatim and is
   sufficient to complete the task as scoped — but `PREVENTION.md`'s "Defect
   classes and their preventions" section (items 1-6, matching the six
   standards one-to-one) has materially richer per-class writeups than what
   `12/02`'s own condensed cross-references quote, and `PREVENTION.md`'s
   closing "In-repo precedents worth copying rather than inventing" section
   has content `12/02`'s task file doesn't mention at all. `PREVENTION.md`'s
   own intro states plainly that it *is* the specification for this task
   ("This file is their specification; they should not have to re-derive
   it.") — treat it as the richer source to draw the "why" lines from, while
   keeping the six standards' own quoted wording locked exactly as `12/02`
   specifies (verbatim from the guide, no paraphrasing the normative text
   itself).

6. **Parallelization — cross-checked against each task's own `Touches` list,
   matches the batch README's Wave 1 row exactly.** `01/01` ∥ `02/01` ∥
   `02/02` ∥ `03/01` ∥ `12/02` are worktree-parallel. `01/01` and `03/01` both
   touch `internal/api/` but disjoint files (`catalog.go`/`plugins.go` vs.
   `agents.go`/`durable_agents.go`) — safe. `01/02` is sequenced **after**
   `01/01`, not run in parallel with it — not a compile dependency, but both
   edit the same `SignatureVerifier` construction site, and `01/02`'s own
   Dependencies section explains landing them in one pass avoids two
   conflicting edits to that construction code. Given note 4 above, `01/02`
   likely dispatches later than the other four parallel-safe tasks regardless
   of the sequencing rule, once its own gap is resolved.

7. **Review discipline — this wave has real code and real regression tests,
   the standard applies in full.** A fresh reviewer (no shared context with
   the worker) independently re-verifies, not just re-reads the Work Log:
   - `01/01` — re-run the "All production callers" `grep -rn "HandleFunc"`
     sweep the task file itself specifies as its own re-confirmation step;
     line numbers will have shifted since this task was authored.
   - `02/01` — confirm the real-Linux evidence per note 3, don't take the
     Work Log's word for it.
   - `03/01` — confirm the pre-implementation auth-boundary verification step
     (what middleware actually gates `/api/agents`/`/api/durable-agents`) was
     actually done and recorded, not skipped — the task file is explicit that
     this is a required verification step, not optional context.
   - `12/02` — confirm all six standards' quoted text is byte-exact against
     the guide content already present in `PREVENTION.md`/this task file, not
     paraphrased.

8. **Scope fences — restate per task, don't let any of these drift:** `01/01`
   does not redesign the signature/trust model itself (already healthy per
   the audit) and does not expand to re-plumb `handleInstall`/
   `handleInstallLocal`/`handleInstallArchive` unless AD-04's banner said so
   (it didn't). `02/01` does not touch `internal/permission`, `internal/secrets`,
   `internal/pathsafe`, `internal/fsutil`, or `internal/safego` (reviewed
   healthy in the same audit section) and does not fix the macOS
   `sandbox-exec`-missing gap (a separate, out-of-scope finding). `02/02`
   does **not** change `internal/sandbox/os_darwin.go` — AD-03 decided
   disclosure-only, code does not change. `03/01` does not build a generic
   path-safety framework (wires two existing, already-proven primitives —
   `pathsafe.ResolveUnder` and the existing slug regex — into new call
   sites) and does not touch the rest of `GO-SEC-003`'s 68-site list (a
   separate task, `08/03`, not in this wave). `12/02` does not invent
   additional standards beyond the six, and does not add any new lint rule or
   CI check (documentation only).

Work through `01/01` ∥ `02/01` ∥ `02/02` ∥ `03/01` ∥ `12/02` per note 6,
resolve `01/02`'s gap per the pre-flight gate and note 4 before dispatching
it, get each reviewed by a fresh reviewer per note 7, and land them. Post a
short update when each task lands and when review completes, not after every
file edit — except `02/01`, where a mid-task design-sketch check-in (note 2)
is worth an extra update given its size. Use `research-auditor` liberally,
particularly to verify the `01/02` gap (note 4/pre-flight item 4) and to spot
check `01/01`'s production-caller table before trusting a worker's
re-confirmation of it. When all six tasks are `reviewed`, dispatch doc-writer
for the end-of-wave handoff and summary docs — this wave closes the batch's
most severe findings and is worth a real synthesized record for whoever
authors Wave 2's kickoff — then stop; the operator reviews before deciding
what's next.
