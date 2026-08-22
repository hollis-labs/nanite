# Where we are (2026-08-21, audit-remediation planning session)

Picking this up after compaction: read this whole file first, then the
pointers it names. This replaces the earlier 2026-08-21 version — everything
below is current as of a fresh check just before this doc was written.

## Headline: repo-wide dev freeze is IN EFFECT; audit remediation is the only authorized work

> **🛑 ALL tasks in all batches are frozen (AD-24, decided 2026-08-21).**
> Not scoped to audited packages. Every `TASKS/` folder — Phase 0-9 and every
> sibling batch — is frozen. **`TASKS/audit-remediation/` is priority #1 and
> the only authorized work.** Exceptions need explicit operator authorization,
> case by case; the operator has said one is unlikely, and an agent must never
> self-authorize. **The operator is the gate for resuming** — not a wave
> boundary, not "all critical/high closed," not a green test run. No derived
> trigger exists. In-flight work at freeze time finished; nothing new starts.
>
> Enforced where an agent will actually hit it: a banner at the top of
> `TASKS/INDEX.md`, and a `DO NOT BOOT THIS` banner on **all 18 files in
> `docs/engineering/orchestrator-kickoffs/`** — those are the real boot
> artifacts, since a kickoff pasted into a plain session is how a batch starts.
> Both must be removed when the freeze lifts.

**`TASKS/audit-remediation/` is now a fully sequenced batch** — 63 task files,
113 findings, 9 waves, dispatched as **eleven units** rather than one (at 63
tasks it is 5x the largest batch this process has ever run). Planned this
session; **nothing dispatched, nothing committed to production code.**

### What blocks dispatch now

**AD-23 and AD-24 are both decided.** What remains:

1. **Wave 0** (`00/01`, `00/02`) — 40 commits / 156 files / +24,891 lines
   landed between the audited commit and HEAD. All 113 findings still carry
   `disposition: remediate`, a placeholder rather than a judgment. Wave 0 sets
   real dispositions and refreshes the count-bearing measurements. Nothing in
   `01/`-`13/` dispatches until it closes.
2. **AD-01 through AD-04, now Wave 0 decisions** (moved from Wave 1 by
   operator direction). `00/01` must deliver an **interim critical/high report
   before finishing the full 113-finding sweep** — the operator decides these
   four from it, against revalidated evidence rather than 40-commit-stale
   audit-era evidence. `GO-SEC4-005` gets pulled forward out of severity order
   because AD-03 needs it. Wave 1 dispatches only once all four are `decided`.
3. **Commit everything** — see "Uncommitted right now." The evidence rescue in
   particular is not complete until it is in git history.

**AD-23 is done.** The evidence is at `docs/audits/2026-08-21-go-quality/raw/`
(29 files, 8.0 MB — a full copy, `diff`-verified against the source), with a
narrow `.gitignore` negation at `.gitignore:88-91` without which 21 of the 29
files would still be silently skipped by the global `*.log`/`*.out` rules. All
14 `raw/` paths cited by `REPORT.md` and the task files resolve.

### What this session produced

All under `TASKS/audit-remediation/`, all uncommitted:

- **`README.md`** — rewritten from an inventory note into a real batch README
  (template 02 shape): `## Status` sign-off block, wave/dependency tables for
  all 63 tasks, parallelization plan cross-checked against every task's real
  `Touches` list, migration-numbering statement, scope fence.
- **`ARCHITECT-DECISIONS.md`** — new. 24 decisions (guide §9's ten, the six
  island wire/defer/retire calls, plus nine surfaced by task files and this
  pass). 44 of 113 findings carry `requires_architect_decision: true` and had
  no queue anywhere before this.
- **`PREVENTION.md`** — new. The guide's output-format D. Its headline: **the
  lint rule that would have caught the batch's most severe finding already
  exists and was scoped away from the package where the bug lived.**
  `.golangci.yml:88-103` forbids `filepath.Join` in favour of
  `pathsafe.ResolveUnder`; `.golangci.yml:180-184` silences it outside
  `sandbox|mcp|service/install`. `GO-PLUGIN-002` (critical) is a bare
  `filepath.Join` at `internal/api/catalog.go:301`. Same class:
  `GO-STORE-003` (high) was already in `nilerr` output — invisible because the
  pre-commit hook runs `golangci-lint run --new`.
- **`00-revalidate-baseline/`** — new folder, 2 task files, the batch gate.
- **61 task files** — each gained a "Planner sequencing" block (wave, dispatch
  unit, cross-folder depends/blocks, parallel-safety, `AD-NN` gate,
  security-review and regression-test flags).
- **`docs/audits/2026-08-21-go-quality/REMEDIATION-GUIDE.md`** — the advisor's
  guide, vendored into the repo. It previously lived only at
  `~/dev/chrispian/inbox/`, unreadable by a context-free Orchestrator, while
  being a primary source of truth for the batch.
- **`TASKS/INDEX.md`** section and **four `TASKS/ESCALATIONS.md`** entries.

**Kickoff prompts are not written** — that is the kickoff-prompt author's
role, one per dispatch unit, as each becomes dispatchable.

## Headline: Skills is executing right now; six more batches are planned behind it

**`TASKS/skills/` is actively in progress** (kicked off by the operator right
before this doc was written) — 4 of 12 tasks `reviewed` (Phase 1 + Phase 2:
`01`-`04`), two real bugs already found and fixed mid-flight (`02`'s
`agent_skills`/`agent_known_skills` collision, `04`'s `file-<slug>` ID colliding
with the retired file-based-skill sentinel — both re-reviewed clean). Next up
in the batch: `05` (install/sync REST API + CLI). Migrations `136`/`137`
landed as planned; `135` (Plugin System's claim) is still unclaimed — Plugin
System hasn't been dispatched yet. **Don't assume Skills is done — check
`TASKS/INDEX.md`'s "Skills" section for real current status before reporting
on it.**

**Filesystem Snapshots is queued to run right after Skills finishes** — the
operator's own plan, stated directly. Real status, corrected from an earlier
mistaken belief it was already done: **1 of 3 tasks complete.** `01` (host
mechanism, `libs/go-agent-wrapper`) is `implemented`. `02` (Nanite capture
policy/wiring) and `03` (diff/preview/restore API) are `not-started` — they
were paused mid-batch to avoid a real merge collision with
`agent-host-acp/06`'s rewrite of `agent.go`; that blocker is now cleared
(agent-host-acp is fully done), but `02`/`03` were never actually resumed.
Kickoff prompt is ready and makes this 1/3 status impossible to miss:
`docs/engineering/orchestrator-kickoffs/filesystem-snapshots.md`.

**Six more sibling batches are fully planned, not yet dispatched**: Plugin
System, Loops, Turn vs. Run, Feedback-Carrying Denial, Code Mode (kickoffs
written for all five), plus Filesystem Snapshots above. All in
`TASKS/INDEX.md`, all `not-started` except where noted.

## New this session: the whole process is now documented as a reusable template package

`docs/engineering/templates/` — **not yet committed**, 7 files. Written because
the sibling-batch pattern (now 11 of the last 13 real batches) was never
formally templated anywhere — only the original numbered-phase flow had
templates (`EXECUTION-PROCESS.md`, `ORCHESTRATOR-KICKOFF-TEMPLATE.md`,
`PLANNER-KICKOFF-PROMPT.md`). Contents: `README.md` (the full five-role chain,
both pipeline variants, the confirmed "plain session, no subagent_type" boot
mechanism, **this role's own process documented as an explicit 11-step
checklist for the first time**, tracking discipline, a numbered
bootstrap-from-zero sequence), plus one template file each for an
architecture doc's `## Status` block, a batch `README.md`, a task file, an
`INDEX.md` section, an `ESCALATIONS.md` entry, and the general-purpose
sibling-batch kickoff prompt (extracted from all dozen real ones written so
far). Read `docs/engineering/templates/README.md` first if picking this back
up — it's the anchor document.

## Landed and complete

| Batch | Design doc | Landed | How |
|---|---|---|---|
| Reflex Action Taxonomy | `10-reflex-action-taxonomy.md` | ✅ | PR #263, merged |
| Harness-Reactive Self-Tools | `11-harness-reactive-self-tools.md` | ✅ | PR #264, merged |
| Scheduling | `12-scheduling.md` | ✅ | direct commits to `main`, no PR |
| Teams | `15-teams.md` | ✅ | direct commits to `main`, no PR |
| Agent Host + ACP | `16-agent-host.md` + `17-acp.md` | ✅ | 24 tasks (grew from 17), direct commits, pushed |
| Phase 6 — Envelopes & Cards | (numbered phase, `08-cards.md`) | ✅ | 6 tasks, direct commits — first real use of the numbered-phase kickoff template since it was fixed |

The kickoff-prompt pattern has now held clean across six real runs in a row
with no repeat of the original nested-orchestrator bug.

## In progress / queued

| Batch | Status | Kickoff ready? |
|---|---|---|
| Skills | **executing now**, 4/12 reviewed | used already |
| Filesystem Snapshots | 1/3 done, paused — queued next | ✅ `filesystem-snapshots.md` |
| Plugin System | not-started | ✅ `plugin-system.md` |
| Loops | not-started | ✅ `loops.md` (has a mandatory `LoopRun`-vs-`WorkflowRun` pre-flight gate) |
| Turn vs. Run | not-started | ✅ `turn-vs-run.md` |
| Feedback-Carrying Denial | not-started | ✅ `feedback-carrying-denial.md` |
| Code Mode | not-started | ✅ `code-mode.md` (real migration-`144` collision with Loops flagged) |

## Torque

`CW-20260819-0004` — **confirmed `done`.** The earlier blocking Torque server
bug (`SQL logic error: no such column: depends_on` on any status transition)
is fixed — operator reported it, Torque team fixed it, MCP reconnected and
verified working again same day. No outstanding Torque blockers right now.

`CW-20260820-0001` through `-0008` (Memory & Knowledge Tools follow-ups) and
the rest of the `CW-20260819-*` series remain `todo`/`manual`, untouched since
filing.

## Uncommitted right now

`docs/engineering/templates/` (7 files, from the prior session),
`docs/engineering/orchestrator-kickoffs/filesystem-snapshots.md`, and **this
session's entire audit-remediation planning output** (see the headline
section): the rewritten batch `README.md`, `ARCHITECT-DECISIONS.md`,
`PREVENTION.md`, `00-revalidate-baseline/` (2 files), sequencing blocks on 61
task files, `REMEDIATION-GUIDE.md`, plus the `TASKS/INDEX.md` section and four
`TASKS/ESCALATIONS.md` entries. **No production code touched.**

Everything else staged before (the six other kickoff prompts, six new `TASKS/`
folders, ~9 new/modified architecture docs, `phase-6` work) landed in a single
`Doc sync` commit (`54015aed`) two handoffs ago.

## Headline status — Phase 0-9 (the original architecture-review sequence)

Unchanged since the last handoff — not re-verified this session, carried
forward as-is:

- **Phase 0, Phase 1**: done, merged to `main`.
- **Phases 2-5**: executed and `reviewed` (closed), except `phase-5/06`
  (superseded by `plugin-system/07`).
- **Phase 6**: done (see table above).
- **Still open, still need your review**: `TASKS/phase-2/07-audit-agent-roster.md`
  — `not-started`.
- **CLI-vs-API**: resolved — keep both, app default CLI, overridable
  system-wide and per-agent. `TASKS/phase-8/01-set-default-runtime-kind.md`.
- **Phases 7, 9**: not started, unchanged.
- **Phase 8**: `01` re-scoped; `02`-`05` not started; `06`/`07` planned, not
  started.

## A real migration-number collision, still not yet hit

`TASKS/loops` (task `09`) and `TASKS/code-mode` (task `03`) both provisionally
claim migration `144` — a genuine arithmetic drift between two sibling
planning docs. Both kickoffs already flag this explicitly. Real current
ceiling on disk: `137` (Skills' own `02`). `135` (Plugin System) and `138`-`144`
(Loops) are the next real claims in sequence if those batches run before
Code Mode.

## Key pointers

- `TASKS/INDEX.md` — live status tracker, always re-read fresh.
- `TASKS/ESCALATIONS.md` — escalation log; latest real entries are each
  batch's own 2026-08-21 planning-pass findings, plus Skills' two mid-flight
  bug-fix entries.
- `docs/engineering/orchestrator-kickoffs/` — thirteen files now: six for
  landed/executing batches (kept for reference/pattern — includes `skills.md`,
  already used), plus `filesystem-snapshots.md`, `plugin-system.md`,
  `loops.md`, `turn-vs-run.md`, `feedback-carrying-denial.md`, `code-mode.md`
  for what's queued next.
- `docs/engineering/templates/` — the process-documentation package, still
  not committed. Start here if reviving this process cold.
- `TASKS/audit-remediation/README.md` — the batch's authoritative wave,
  dependency, and parallelization tables. Its `## Status` block carries the
  operator sign-off gate; an unchecked box is a hard stop.
- `TASKS/audit-remediation/ARCHITECT-DECISIONS.md` — 24 open decisions. A task
  whose `Gated on` decision is still `open` must not be dispatched.
- Torque: project `PRJ-20260417-0002`, no outstanding tool blockers.

## What's next

Most concrete open items, roughly in likely order:
1. **Commit this session's work**, evidence rescue first and as its own
   commit — until it is in history the rescue has not actually happened.
2. **Dispatch Wave 0** (`00/01` + `00/02`). The freeze is in effect and the
   in-flight batches are done, so the precondition is met.
3. **Decide AD-01 through AD-04** off `00/01`'s interim critical/high report,
   then Wave 1 becomes dispatchable.
4. ~~Filesystem Snapshots~~ — **frozen**, along with every other batch. Its
   kickoff carries a `DO NOT BOOT THIS` banner. Re-slot only when the operator
   lifts the freeze.
5. **Commit `docs/engineering/templates/`** whenever convenient — nothing blocks this, just hasn't been asked
   for yet.
6. ~~**Pick the order for the remaining planned batches**~~ — moot until the
   freeze lifts; kept for when it does. (Plugin System,
   Loops, Turn vs. Run, Feedback-Carrying Denial, Code Mode) — Plugin System
   should probably run before Loops/Code Mode given the migration sequencing
   above, but Turn vs. Run and Feedback-Carrying Denial need no migrations at
   all and are free to run anytime.
7. `TASKS/phase-2/07-audit-agent-roster.md` — still open, still needs review.
8. Review the architecture docs never looked at yet
   (`19-api-cli-runtime-parity.md`, `25-plugin-conformance-harness.md`,
   `26-architecture-enforcement-tests.md` — note this one is now cited by
   `PREVENTION.md` as the source of the in-repo enforceable-rule precedent) and
   `docs/engineering/engineering-principles-draft.md`/`docs/launch-site/`
   whenever wanted — still untouched.
