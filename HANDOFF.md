# Where we are (2026-08-21, later)

Picking this up after compaction: read this whole file first, then the
pointers it names. This replaces the earlier 2026-08-21 version — everything
below is current as of a fresh check just before this doc was written.

## Headline: Agent Host + ACP is DONE — the biggest sibling batch yet, and seven more are planned behind it

**Agent Host + ACP** (`TASKS/agent-host-acp/`) landed and closed today.
Confirmed via git log and `TASKS/INDEX.md`: what started as a 17-task plan
grew to **24 real tasks** — five extra fix tasks (`18`-`22`) landed during
Phase 2's dogfeed validation, most touching sibling repos, plus one
post-handoff fix (`23`, wiring the Claude/Codex/Pi ACP bridge adapters into
actual dispatch — found after the first HANDOFF/SUMMARY pass, both docs
regenerated). All committed **directly to `main`, no PR** — matching
Scheduling/Teams' pattern — and confirmed **already pushed to `origin/main`**
(`git log origin/main..HEAD` is empty; nothing is stranded locally).

Real cross-repo work, not just Nanite: `libs/go-agent-wrapper` went from
`v0.1.0`-pinned to `v0.7.0` over the course of this batch; `libs/agentkit`
got a real bug fix (legacy waiter swallowing exit errors, tagged `v0.5.0`,
operator-approved cross-portfolio fix); `libs/go-providers` got a real fix
(Codex missing `--skip-git-repo-check`, tagged `v0.24.0`). `go-agent-wrapper`
went from zero adopters anywhere in the portfolio to Nanite's full production
host, with Claude/Codex/Pi driven over ACP via per-provider bridges (`12`'s
operator-approved decision: per-provider bridges over `beyond5959/acp-adapter`),
plus native ACP for OpenCode and Copilot CLI. Full detail:
`TASKS/agent-host-acp/HANDOFF.md`, `TASKS/agent-host-acp/SUMMARY.md`.

**Seven more sibling batches are fully planned and ready for you to review**,
none dispatched yet: **Filesystem Snapshots**, **Plugin System**, **Skills**,
**Loops**, **Turn vs. Run**, **Feedback-Carrying Denial**, **Code Mode** — all
in `TASKS/INDEX.md`, all `not-started`. I've written orchestrator kickoff
prompts for six of these seven (all but Filesystem Snapshots, which hasn't
been asked for yet) plus **Phase 6** (a real numbered Phase 0-9 phase, not a
sibling batch — first real use of the master template since it was fixed).
**None of the seven kickoff prompts are committed yet** — see "Uncommitted
right now" below.

## Uncommitted right now (7 kickoff prompts, 6 new TASKS/ folders, ~9 architecture docs)

Nothing here is lost — just staged in the working tree, not yet in git
history. Confirm with the operator before committing (last time, "commit
everything" was an explicit instruction, not a default).

**Kickoff prompts** (`docs/engineering/orchestrator-kickoffs/`): `phase-6.md`,
`plugin-system.md`, `skills.md` (patched to match standard — see below),
`loops.md` (patched with a mandatory pre-flight gate — see below),
`turn-vs-run.md`, `feedback-carrying-denial.md`, `code-mode.md`.

**New `TASKS/` folders** (all sibling batches, all `not-started`):
`plugin-system/` (7 tasks), `skills/` (12 tasks), `loops/` (13 tasks),
`turn-vs-run/` (4 tasks), `feedback-carrying-denial/` (6 tasks), `code-mode/`
(3 tasks). `filesystem-snapshots/` (3 tasks) also exists, planned earlier,
no kickoff written for it yet — not asked for.

**New/modified architecture docs**: `20-skills.md`, `21-loops.md`,
`22-turn-vs-run.md`, `23-feedback-carrying-denial.md`, `27-code-mode.md` are
new and each carry real "Approved for implementation, 2026-08-21" status
lines (verified directly, not assumed — see per-batch detail below).
`24-typed-corruption-recovery-taxonomy.md` is new but **deliberately
untouched — still correctly documented as "decided to wait,"** not a
batch, not actionable. `19-api-cli-runtime-parity.md`, `25-plugin-conformance-harness.md`,
`26-architecture-enforcement-tests.md` are also new — **I have not reviewed
these three or `docs/engineering/engineering-principles-draft.md`/
`docs/launch-site/` at all**; flagging their existence, not their content.
`09-plugin-system.md`, `16-agent-host.md`, `17-acp.md`,
`06-session-lifecycle-and-recovery.md`, `00-overview.md`, `GLOSSARY.md` all
have small in-place corrections from various planning passes.
`TASKS/phase-5/06-make-http-middleware-plugin-extensible.md` is modified —
marked superseded by `TASKS/plugin-system/07`.

## A real, already-known migration-number collision (flagged, not yet hit)

`TASKS/loops` (task `09`, its `RunStatusWaitingOnLoop` status-CHECK) and
`TASKS/code-mode` (task `03`, its new reflex action kind) **both provisionally
claim migration `144`** — a genuine arithmetic drift between two sibling
planning docs, not a hypothetical future race. Code Mode's kickoff prompt
flags this explicitly and tells its orchestrator to expect `144` is already
Loops' and land at `145`+ instead. Current real highest migration on disk is
still `134` (landed by Agent Host + ACP's own task `11`) — nothing else has
landed since, so `plugin-system` (`135`), `skills` (`136`-`137`), and `loops`
(`138`-`144`) are all still open claims, in that order, if dispatched in
that order.

## The `skills.md` and `loops.md` kickoffs got real corrections after first-draft review

- **`skills.md`**: was originally drafted by a planner session (not me),
  matched my pattern closely but was missing the standard "entire
  configuration" sentence, the anti-recursion paragraph's trailing clause,
  and — the real gap — the "verify before trusting" pre-flight paragraph.
  Traced why: `20-skills.md` genuinely has no `## Status` section and no
  "operator-signed-off" language anywhere, unlike every sibling's design
  doc. Patched in a paragraph requiring the orchestrator to confirm approval
  with the operator directly before dispatching anything, rather than
  treating "the design doc exists" as equivalent to sign-off.
- **`loops.md`**: at the operator's explicit request, the `LoopRun`-vs-
  `WorkflowRun` entity-relationship distinction (a new peer entity, the
  opposite of Teams' `TeamRun`-IS-a-`WorkflowRun` collapse, even though
  Loop's engine/launcher reuses Teams' code as its structural template) is
  now a **mandatory pre-flight gate** the orchestrator must resolve — via a
  research-auditor cross-check of the design doc against the real Teams
  precedent code — before dispatching any Phase 1 worker, not a note to
  notice mid-implementation.

## Batch detail — everything landed so far

| Batch | Design doc | Landed | How |
|---|---|---|---|
| Reflex Action Taxonomy | `10-reflex-action-taxonomy.md` | ✅ | PR #263, merged |
| Harness-Reactive Self-Tools | `11-harness-reactive-self-tools.md` | ✅ | PR #264, merged |
| Scheduling | `12-scheduling.md` | ✅ | direct commits to `main`, no PR |
| Teams | `15-teams.md` | ✅ | direct commits to `main`, no PR |
| Agent Host + ACP | `16-agent-host.md` + `17-acp.md` | ✅ (just now) | direct commits to `main`, no PR, pushed |

The kickoff-prompt pattern has now held clean across five real runs in a
row with no repeat of the original nested-orchestrator bug.

## Headline status — Phase 0-9 (the original architecture-review sequence)

Unchanged since the last handoff — not re-verified this session, carried
forward as-is:

- **Phase 0, Phase 1**: done, merged to `main`.
- **Phases 2-5**: executed and `reviewed` (closed), except `phase-5/06`
  (now superseded by `plugin-system/07`, see above).
- **Still open, still need your review**: `TASKS/phase-2/07-audit-agent-roster.md`
  — `not-started`.
- **CLI-vs-API**: resolved — keep both, app default CLI, overridable
  system-wide and per-agent. `TASKS/phase-8/01-set-default-runtime-kind.md`.
- **Phase 6**: kickoff written (`docs/engineering/orchestrator-kickoffs/phase-6.md`),
  not dispatched. **Phases 7, 9**: not started, unchanged.
- **Phase 8**: `01` re-scoped; `02`-`05` not started; `06`/`07` planned, not
  started.

## Torque — one real blocker, status unknown

`CW-20260819-0004` ("build a scheduler," superseded by the landed Scheduling
batch): I added a comment documenting the supersession, but every attempt to
transition its status failed with a genuine Torque server bug
(`SQL logic error: no such column: depends_on`) — reproduced on both single
and bulk transition, and on `todo→doing` as well as `todo→done`, so it's not
FSM-specific. You said Torque pushed an update the same day and you'd report
the bug and have them fix it — **not re-verified since**; retry the
transition once you confirm the fix landed.

`CW-20260820-0001` through `-0008` (Memory & Knowledge Tools follow-ups) and
the rest of the `CW-20260819-*` series remain `todo`/`manual`, untouched
since filing.

## Key pointers

- `TASKS/INDEX.md` — live status tracker, always re-read fresh; has full
  per-task detail for all five completed sibling batches plus all seven
  planned-not-dispatched ones.
- `TASKS/ESCALATIONS.md` — escalation log; latest real entries are each new
  batch's own planning-pass findings (2026-08-21 entries for Loops, Skills,
  Plugin System, Turn vs. Run, Feedback-Carrying Denial, Code Mode).
- `docs/engineering/orchestrator-kickoffs/` — nine files now: the five for
  landed batches (kept for reference/pattern), plus `phase-6.md`,
  `plugin-system.md`, `skills.md`, `loops.md`, `turn-vs-run.md`,
  `feedback-carrying-denial.md`, `code-mode.md` for what's queued next.
  `ORCHESTRATOR-KICKOFF-TEMPLATE.md` is the Phase 0-9-specific variant,
  used for real for the first time on `phase-6.md`.
- Torque: project `PRJ-20260417-0002`.

## What's next

Most concrete open items, roughly in likely order:
1. **Decide whether to commit the seven uncommitted kickoff prompts + six
   new `TASKS/` folders + new architecture docs** — everything is staged and
   ready, just waiting on the word, same as agent-host-acp's planning
   baseline was before it got committed and dispatched.
2. **Pick which of the seven planned batches to dispatch next**, and in what
   order — `plugin-system`/`skills`/`loops` have real sequential migration
   claims (`135`, `136`-`137`, `138`-`144`) that get simpler to reason about
   if run in that order rather than interleaved; `turn-vs-run` and
   `feedback-carrying-denial` need no migrations at all, so they're free to
   run anytime without numbering coordination.
3. **Confirm the Torque transition bug is fixed**, then close out
   `CW-20260819-0004` for real.
4. `TASKS/phase-2/07-audit-agent-roster.md` — still open, still needs your
   review.
5. Review the three unreviewed-by-me new architecture docs
   (`19-api-cli-runtime-parity.md`, `25-plugin-conformance-harness.md`,
   `26-architecture-enforcement-tests.md`) and `engineering-principles-draft.md`/
   `docs/launch-site/` whenever you want them looked at — nothing done on
   these yet.
