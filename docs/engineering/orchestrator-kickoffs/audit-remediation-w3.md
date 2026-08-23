You are the Orchestrator for **Wave 3 of the Audit Remediation batch** —
dispatch unit `W3`, `TASKS/audit-remediation/08-remaining-security-hardening/`
— the fifth of eleven dispatch units implementing the remediation program
derived from `docs/audits/2026-08-21-go-quality/REPORT.md` as sequenced by
`docs/audits/2026-08-21-go-quality/REMEDIATION-GUIDE.md`. You have no memory
of the audit, the planning pass, or Waves 0-2's own execution — everything
you need is in the repo. **This kickoff covers ten tasks: `08/01` through
`08/10`.** Wave 0, Wave 1, and Wave 2 (both `W2a` and `W2b`, all thirteen of
their tasks) are all closed — that is what makes this wave dispatchable at
all; do not re-open or re-verify their work beyond confirming closure,
covered below.

**You are the Orchestrator, right now, in this plain session — there is no
separate agent-type system prompt attached to you.** Read
`.claude/agents/orchestrator.md` first (item 1 below); it defines your exact
dispatch roster in full. In short: you dispatch exactly four leaf agent types
via the Agent tool — **worker** (implements one task file end to end),
**reviewer** (fresh review of a validated section, no shared context with the
worker who did it), **research-auditor** (read-only, verifies any claim
before you trust it — cannot write files or dispatch further agents),
**doc-writer** (end-of-wave handoff + summary docs). None of these four can
dispatch further agents themselves.

**Do not spawn another `orchestrator`, and do not dispatch a general-purpose
agent asked to "run this batch" or "coordinate the tasks."** That would
recreate this coordinating layer redundantly underneath you — a real failure
mode that has already happened once in this project. If the Agent tool
doesn't actually offer `worker`/`reviewer`/`research-auditor`/`doc-writer` as
usable types when you check, stop and tell the operator directly.

**The repo-wide dev freeze (AD-24) is still in effect** — confirm this
directly (`TASKS/INDEX.md`'s banner) rather than assuming. `TASKS/audit-remediation/`
remains the only work authorized to proceed; the other frozen sibling
batches (Filesystem Snapshots, Plugin System, Loops, Turn vs. Run,
Feedback-Carrying Denial, Code Mode) stay frozen. Do not act on any of them
even if asked to check on their status in passing.

**This is the largest single wave dispatched so far by architect-decision
count**: five decisions attach to it — AD-15, AD-16, AD-27, AD-28 (all
`decided`, all gate at least one task directly), plus AD-05 (`open`, attached
to `01/01` as a non-blocking follow-up, not a Wave 3 gate). Unlike Wave 1's
`01/02`/AD-25 gap and Wave 2's `04/04`/AD-26 gap — both found by the kickoff
author, mid-write — **every decision this wave needs was already found and
resolved by a dedicated Wave 3 pre-flight cross-check on 2026-08-22**, before
this kickoff was written. Confirm that's still true (pre-flight gate below),
but go in expecting a genuinely clean dispatch, not another gap hunt.

---

## Things about this specific wave that won't be obvious from the batch README alone — read all of these before doing anything else

**(A) `08/07` is a breaking change with an operator-facing migration
obligation baked into its own Done-means — treat this as the wave's headline
task, not a routine one.** AD-15 (decided 2026-08-22) changes Nanite's
default bind address from all-interfaces to loopback-only, with an explicit
opt-in required to bind wide. **This silently breaks every current
bind-all-reliant deployment on upgrade** — Docker port mapping, LAN access,
remote dev, a reverse proxy aimed at a non-loopback interface — and the
failure mode is silent from the operator's side: the service starts
normally and simply stops being reachable, with no crash and no error to
grep for. `08/07`'s own task file already carries the full AD-15 decision
banner and states plainly: *"the migration note is part of this task, not a
follow-up."* Concretely, `08/07`'s Done-means is not complete without:
  - the new bind-address/bind-all opt-in option named explicitly (it does not
    exist in `internal/config` today — this task adds it, not just consumes
    it),
  - a startup log line that always states auth status and warns when auth is
    unconfigured (same silent-degradation class AD-01 already closed
    elsewhere in this batch),
  - **operator-facing release-note text** — the symptom ("starts fine but is
    no longer reachable from other hosts") and the one-line fix (the new
    opt-in flag) — as an actual artifact, not just a Work Log mention.
  TLS is explicitly out of scope for this task (AD-15 rejected it — loopback
  makes it ceremony, a reverse proxy is the right answer for the wide case).
  Brief the `08/07` worker on the release-note requirement directly; it is
  easy to read this as "just" a `server.go:163` one-line default change and
  miss that the task's actual Done-means is broader.

**(B) `08/09` grew a third finding after this wave was originally scoped and
is now the wave's biggest task — five findings in one file, not the four its
own title suggests.** Its title lists four ("Autocomplete repo_path
exposure, artifact write-path confinement, catalog fetch timeout, plugin-UI
symlink gap" — `GO-API-001`, `GO-API-002`, `GO-API-003`, `GO-API-008`). The
task file's own "⚠ RE-BASELINED 2026-08-22" banner adds a fifth,
**`GO-SEC4-007`**, reassigned here after `11/07` (Wave 6a, its original
`task_file` pointer) turned out to explicitly disclaim being an
implementation task. Two of the five now carry full decision banners:
  - **`GO-API-001` (AD-27):** validate `repo_path` at *write* time, in
    `handleCreateProject`/`handleUpdateProject` — **not** by confining the
    autocomplete walk itself. Suggested policy (confirm, don't assume):
    reject `/`, the home directory *itself* (subdirectories stay allowed),
    and system dirs (`/etc`, `/usr`, `/var`, `/System`); require an existing
    directory. The worker must decide and record whether existing `projects`
    rows get swept or are validated only on next update.
  - **`GO-API-003` + `GO-SEC4-007` (AD-28):** block private/loopback/link-local
    archive-fetch destinations (including cloud IMDS `169.254.169.254`), and
    — this is the part easiest to miss — **extract the SSRF CIDR denylist
    that currently exists twice** (`internal/mcp/general_tools.go:61-68`,
    `internal/sandbox/proxy.go:37+`, identical, no parity enforcement) **into
    one shared location** rather than writing a third copy. AD-28's own text
    is explicit that a naive implementation would make `GO-SEC4-007` worse
    while closing `GO-API-003`. See gotcha (C) below — this extraction has a
    real coordination dependency on `08/01` that neither task file names.
  `GO-API-002` (artifact write-path confinement) and `GO-API-008`
  (plugin-UI symlink gap) are the original, unchanged, non-gated pair — both
  straightforward `pathsafe.ResolveUnder` adoptions.

**(C) `08/01` and `08/09` both need the same SSRF CIDR-denylist logic, and
neither task file cross-references the other — a real coordination gap, not
just a documentation nicety.** `08/01`'s own task file independently
identifies the same two existing duplicate copies AD-28 names and says
*"reuse one of them here rather than writing a third independent copy"* —
written before AD-28 existed, so it has no way to know a shared-location
extraction is now mandated elsewhere in this same wave. If both are dispatched
independently without coordination: `08/01` might import (or copy) one of the
two pre-extraction duplicates just as `08/09` is deleting it in favor of a
new shared location, or the two could land on different shared-location
shapes. **Recommended sequencing:** land `08/09`'s `GO-SEC4-007` extraction
first (or explicitly checkpoint it early, since `08/09` is this wave's
largest task and likely to run longest regardless), then brief `08/01`'s
worker to import the resulting shared location as a fourth consumer rather
than reusing either pre-extraction copy. If `08/01` lands first for
scheduling reasons instead, have it note in its Work Log that its import site
needs a follow-up update once `08/09`'s extraction lands, and flag that
explicitly to `08/09`'s worker rather than leaving it to be discovered in
review.

**(D) `08/06` and `08/09` both touch `internal/sandbox/proxy.go` — same file,
disjoint concerns, low real conflict risk, but sequence rather than run
truly concurrent worktrees against it.** `08/06` adds one field
(`ReadHeaderTimeout`) to the proxy's `http.Server` construction — literally
the smallest task in this folder, by its own description. `08/09`'s
`GO-SEC4-007` half (gotcha C) touches the same file to import the new shared
CIDR location in place of the existing inline denylist. Recommend landing
`08/06` first (trivial, fast) so `08/09` doesn't have to rebase its own
change around an unrelated one-line diff.

**(E) `08/03` is a triage/investigation task with an unknown downstream
footprint — do not treat its "parallel-safe" status as settled until its own
Step 1 output exists.** Its Touches field says so explicitly: *"repo-wide
read-only triage first; downstream code touches are not yet known — they
depend entirely on Step 1's filtered list."* Steps 1-3 (regenerate the full
70-site G304 list from `raw/golangci-baseline.json`, exclude the two
already-triaged subsets, classify the remainder by trust boundary, trace
each above-CLI-trust site) are read-only against production code and can run
immediately, in parallel with everything else in this wave. **Step 4**
(applying `pathsafe.ResolveUnder` to confirmed sites) has a real,
currently-unknowable file footprint — it could touch any package containing
one of the ~50-plus un-sampled sites. Do not schedule Step 4 until Step 1's
list exists; re-check it against whatever else has landed in this wave by
that point before treating it as safe to run alongside anything specific.
Also: this task's architect-decision gate applies to **Step 4's site
selection**, not to dispatching the task at all — brief the worker that
Steps 1-3 proceed now, and Step 4 needs an architect sign-off checkpoint on
the classified list before any fix lands, matching this task's own explicit
`requires_architect_decision: true`.

**(F) Ctx-sweep drift (the mechanical rewrite from `06/03`/`06/04`, `fe16e138`,
that dominated Wave 2's kickoff) touched three of this wave's files, smaller
in scope than Wave 2's but still real.** Independently re-verified against
current `HEAD`: `internal/service/a2a_push_notifier.go` (1 marker),
`internal/service/a2a_task_manager.go` (2 markers) — both `08/01`'s targets,
and its task file predates the sweep with no self-flag, unlike `08/09`
below — and `internal/api/autocomplete.go` (3 markers), one of `08/09`'s
five targets, whose own re-baselined banner already says *"citations in this
file predate both `01/01` and the ctx sweep — re-locate before editing"* (so
that one is already self-aware; `08/01` is not). Every other file this wave
touches (`internal/mcp/dev_tools.go`, `internal/sandbox/exec.go` and
`proxy.go`, all three `internal/server/*.go` files, `internal/api/projects.go`/
`artifacts.go`/`catalog.go`/`plugins.go`/`schedules.go`/`settings.go`/
`memories.go`, `internal/permission/engine.go`) shows **zero** markers —
genuinely clean, still worth a quick `grep -n` before editing but no specific
reason to expect drift. Brief `08/01`'s worker specifically: re-derive
current line numbers in both `a2a_*.go` files before trusting the task
file's citations.

---

## Read, in full, before doing anything else

1. `.claude/agents/orchestrator.md` — your own role definition.
2. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure,
   including the two hard-won safety rules (no repo-global `git stash` across
   worktrees; live-verification writes target an explicit scratch path).
3. `TASKS/audit-remediation/README.md` **in full** — the freeze section
   (AD-24), the dispatch-model rationale, and the Wave 3 row of the
   dispatch-precondition table.
4. `TASKS/audit-remediation/ARCHITECT-DECISIONS.md` — read AD-15, AD-16,
   AD-27, and AD-28 in full (not just the one-line queue entries); all four
   are `decided` and gate this wave. Also read AD-05 (`open`, non-blocking
   follow-up on `01/01`, already landed) so you recognize it if `08/09`'s
   worker cross-references it. Skim the rest of the 28-item queue — several
   later-wave decisions remain `open` (Wave 4's six island calls AD-06
   through AD-11, plus AD-12/13/19/20/21/22) — none gate this wave.
5. All ten task files, in full: `08-remaining-security-hardening/01-*.md`
   through `10-*.md`.
6. `TASKS/audit-remediation/WAVE-1-HANDOFF.md` and (once it exists)
   `WAVE-2-HANDOFF.md` — prior waves' worked records; read as examples of
   this project's kickoff/review discipline if you haven't dispatched a wave
   before.
7. `TASKS/INDEX.md`'s freeze banner at the top, and its own "Audit
   Remediation" section — confirm Wave 0, Wave 1, Wave 2a, and Wave 2b rows
   all show every task `reviewed`/`validated`, and the Wave 3 row (just
   synced to reflect `08/09`'s AD-27/AD-28 gates and its now-five-finding
   scope).
8. `TASKS/ESCALATIONS.md` — the 2026-08-22 entries, for context on what
   Waves 1-2 actually found (the leftover-archive-file hygiene bug, the
   `06/03` cancellation-safety regression) — nothing here blocks Wave 3, but
   useful background.
9. `docs/engineering/GLOSSARY.md` — check before locking any new name.

## Mandatory pre-flight gate — confirm all of the following before dispatching anything

1. **Wave 0, Wave 1, and Wave 2 (both units) are all closed.** `TASKS/INDEX.md`'s
   rows for `00/01`-`00/02`, `01/01`-`03/01`, `12/02`, and all thirteen of
   `04/01`-`07/05` (plus `06/03`/`06/04`, out-of-wave but load-bearing) should
   show `reviewed` or `validated`. Confirm directly.
2. **AD-15, AD-16, AD-27, and AD-28 are all `decided` in
   `ARCHITECT-DECISIONS.md`.** They were as of the Wave 3 pre-flight
   cross-check, 2026-08-22. Confirm each `**Status:**` line still reads
   `decided` before treating their banners in `08/04`/`08/07`/`08/09` as
   authoritative.
3. **The dev freeze (AD-24) is still in effect.** Check `TASKS/INDEX.md`'s
   banner. If it has lifted since this kickoff was written, that changes
   nothing about *this* wave's own authorization but means the "don't touch
   other batches" instructions above may be stale — confirm with the operator
   if genuinely unclear.
4. **Resolve the `08/01`↔`08/09` CIDR-consolidation coordination (gotcha C)
   before both are in flight simultaneously** — either sequence them, or
   brief both workers explicitly on the shared-location plan so neither
   independently commits to a pre-extraction copy without knowing the other
   task is about to remove it.

## Dispatch plan

**Fully parallel, file-disjoint, no coordination needed:** `08/02`, `08/04`,
`08/05`, `08/10` — verified against each task's own `Touches` list with no
overlap against each other or against the coordination pairs below.

**`08/08` runs alone**, per its own explicit instruction — it rewrites
`go.mod`/`go.sum`, which every other open worktree also carries, and a
parallel run risks a conflict in every branch. Dispatch it either first
(clean baseline for everything else to build against) or last (avoid making
every other in-flight worktree rebase around a dependency bump) — either is
defensible; picking first is slightly safer since a bad toolchain/dependency
bump would then surface before nine other tasks build on top of it.

**`08/03`'s Steps 1-3 run immediately, in parallel with everything else**
(read-only). Hold Step 4 until the filtered list exists and has been
cross-checked against whatever else has landed by then (gotcha E).

**`08/06` → `08/09`, sequenced** (gotcha D, same file
`internal/sandbox/proxy.go`) — land the trivial `08/06` fix first.

**`08/01` and `08/09`, coordinated per gotcha (C)** — either sequence
`08/09`'s `GO-SEC4-007` extraction before `08/01` starts its own SSRF-CIDR
work, or dispatch both with explicit cross-briefing so neither commits to a
soon-to-be-obsolete duplicate copy independently.

**`08/07`** has no file-overlap with anything else in this wave (its files —
`internal/server/*.go`, `internal/config`, `cmd/nanite/main.go`'s composition
root — are untouched by the other nine) and can run fully parallel to
everything above. Given gotcha (A)'s scope (new config option + startup log
+ release-note text), budget it more runway than the folder's smaller tasks,
similar to how Wave 1's kickoff flagged `02/01` as a likely long pole.

## Review discipline

A fresh reviewer (no shared context with the worker) independently
re-verifies every task, not just re-reads the Work Log. Specific things worth
an independent check given this wave's gotchas:

- `08/01` — confirm the mandatory auth-boundary pre-step was actually traced
  (not skipped) and recorded in the Work Log, and confirm the CIDR-source
  decision (gotcha C) was actually coordinated with `08/09`, not landed blind.
- `08/03` — confirm the regenerated list is genuinely complete (drawn fresh
  from `raw/golangci-baseline.json`, not just the audit's original 10-site
  sample), and confirm an actual architect sign-off exists for Step 4's site
  selection before any `pathsafe.ResolveUnder` fix is treated as authorized.
- `08/04` — confirm the AD-16 premise was actually verified (`Check()`
  genuinely consulting `PathGrants` on the write path) before trusting the
  doc-comment-only fix; if the worker found the premise false and re-opened
  AD-16 instead, confirm that escalation actually happened rather than being
  silently absorbed.
- `08/07` — confirm all three Done-means items landed: the new opt-in option,
  the always-on startup auth-status line, and the actual release-note text
  as a real artifact (not a Work Log paragraph standing in for it).
- `08/09` — confirm all five findings closed, not just the four the title
  names; confirm the CIDR extraction actually lands in one shared location
  (not a fourth near-duplicate); confirm the `repo_path` write-time
  validation policy decision (sweep-existing vs. validate-on-next-update) is
  explicitly recorded, not left implicit.
- `08/10` — confirm GO-API-004's "three producers still equivalent" check
  was actually done before consolidation, not assumed from the in-repo
  comment's age; confirm GO-API-005's regression test actually seeds enough
  records to prove the previous under-return bug, not just a small fixture
  that happens to pass either way.

## Scope fences — restate per task, don't let any of these drift

`08/01` does not build a general-purpose outbound-URL policy engine, does
not touch A2A task-routing logic beyond the webhook URL. `08/02` does not
change `resolveAllowed`'s already-correct top-level validation, does not add
a general filesystem-sandboxing layer. `08/03` does not mechanically apply
`pathsafe.ResolveUnder` to all ~70 sites without individual trust-chain
verification, and does not touch the `internal/agent/managed_*` subset
(folder 03's territory) or re-flag the four already-triaged `cmd/nanite`
files. `08/04` does not redesign the permission-mode system or `PathGrants`'
broader architecture. `08/05` does not build a general secrets-scanning
system, does not change UserExec's default behavior. `08/06` does not add
other `http.Server` hardening beyond `ReadHeaderTimeout`. `08/07` does not
add TLS (AD-15 explicitly rejected it) and does not build a full
role-based-authorization system (AD-15's scope is bind/auth-visibility only).
`08/08` does not perform a broader dependency-audit sweep beyond the two
named findings. `08/09` does not unify its five findings into a new shared
abstraction beyond `pathsafe.ResolveUnder` itself (already existing) and the
CIDR-denylist extraction (already mandated by AD-28) — no other new
primitive. `08/10` does not build a general cross-cutting validation
framework (GO-API-004) and does not redesign `memory.RecallOpts`/`Recall`'s
broader API (GO-API-005) beyond the filter-then-paginate ordering fix.

## At the end

When all ten tasks are `reviewed`, dispatch `doc-writer` for the end-of-wave
`WAVE-3-HANDOFF.md` and `WAVE-3-SUMMARY.md`, mirroring Wave 1's and Wave 2's
pairs — written for whoever authors Wave 4's kickoff. Ask it to specifically
record: how the `08/01`↔`08/09` CIDR-consolidation coordination (gotcha C)
was actually resolved (sequencing chosen, shared-location shape landed), and
`08/07`'s migration/release-note artifact's actual location for Wave 4's
author to point operators at if asked. Then stop; the operator reviews before
deciding what's next.
