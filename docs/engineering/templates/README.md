# Multi-Agent Engineering Process — templates and bootstrap guide

This directory is a self-contained reference for Nanite's multi-agent engineering
process: how a design idea becomes a reviewed, merged batch of code, who (or what
role) does each step, and — the piece that was never written down anywhere before
this directory existed — **what the "kickoff-prompt author" role actually does**,
since that's the role this document's author (an assistant session working
alongside the operator) has been performing across roughly a dozen real batches.

If you're trying to revive this whole process from scratch — a new repo, a wiped
`TASKS/` directory, a session with no memory of any of this — start with
**"Bootstrapping from zero"** near the bottom. If you already have the process
running and just need a template for the next batch, jump straight to the file
index.

## The role chain

Five distinct roles produce one landed batch of work. Each one is a separate
session/dispatch with **no shared memory** of the others — every handoff between
roles happens entirely through files in the repo, never through conversation
context. This is deliberate, not an accident of tooling: it's what makes the
process resumable after a compaction, a crashed session, or a multi-day gap.

```
Architect  →  Planner  →  Kickoff-prompt author  →  Orchestrator  →  Worker / Reviewer /
(design)      (task       (this doc's role)         (a booted        Research-Auditor /
              breakdown)                             session)         Doc-Writer (subagents)
```

1. **Architect** — a design/research session (booted however your workflow boots
   design sessions; not covered by this directory) that produces one file under
   `docs/engineering/architecture/NN-name.md`. Its job ends with an explicit
   **`## Status`** section stating the design is approved for implementation, by
   the operator, with a date. See `01-architecture-doc-status-block.md` — this is
   the single most load-bearing convention in the whole pipeline, and the one
   most often skipped by an architect session that doesn't know to include it.

2. **Planner** — reads the design doc (and, for the *original* numbered-phase
   flow, `docs/engineering/TASKS.md` and the decision log — see "Two pipeline
   variants" below) and produces a `TASKS/<batch-name>/` folder: a `README.md`
   (sibling-batch flow only) plus one file per task, each self-contained enough
   that a worker with zero conversation memory can execute it. The Planner
   **never executes anything** — no worker dispatch, no application-code edits,
   ever. It stops after presenting the plan. See `02-batch-readme-template.md`
   and `03-task-file-template.md`. A real prior example of a planner's own
   kickoff prompt: `docs/engineering/PLANNER-KICKOFF-PROMPT.md` (written for the
   original Phase 1-6 planning pass — the pattern generalizes to a sibling
   batch's planning session too, just swap `TASKS.md`'s authority for the
   design doc's).

3. **Kickoff-prompt author** — this role. See "My role" below in full detail.

4. **Orchestrator** — a session booted with the kickoff prompt from step 3 pasted
   directly into a plain session (see "How Orchestrator sessions are actually
   booted" below — this is a real, confirmed operational detail, not an
   assumption). It verifies the Planner's claims, then dispatches the four leaf
   subagent types below to actually execute the batch, tracking status in
   `TASKS/INDEX.md` as it goes and logging real findings to
   `TASKS/ESCALATIONS.md`. At the end it dispatches **doc-writer** once for a
   handoff doc, then stops.

5. **Worker / Reviewer / Research-Auditor / Doc-Writer** — four Claude Code
   subagent types (`.claude/agents/{worker,reviewer,research-auditor,doc-writer}.md`)
   the Orchestrator dispatches. None of them can dispatch further agents — that
   capability was deliberately removed after repeated incidents of a research
   dispatch believing itself the coordinator and self-authorizing more work (see
   `docs/engineering/EXECUTION-PROCESS.md`'s **Log integrity** section for the
   real incident this rule exists because of). Their contracts are defined in
   their own files, not duplicated here — read them directly, they're short.

## Two pipeline variants — know which one you're in

**A. The original, numbered-phase flow** (`docs/engineering/TASKS.md` +
`docs/architecture-decision-log-2026-08-17.md` as the source of truth,
`docs/engineering/EXECUTION-PROCESS.md` as the operating procedure,
`docs/engineering/ORCHESTRATOR-KICKOFF-TEMPLATE.md` as the kickoff template,
`TASKS/phase-N/` as the task folders). This is for the pre-planned Phase 0-9
sequence. Use this variant's own template
(`../ORCHESTRATOR-KICKOFF-TEMPLATE.md`), not this directory's
`06-orchestrator-kickoff-template-sibling-batch.md`, when the batch is a
numbered phase.

**B. The sibling-batch flow** (this directory's actual subject — the pattern
that now produces most real work). A single architecture doc with its own
`## Status` sign-off is the source of truth instead of `TASKS.md`; the batch
lives in its own top-level `TASKS/<batch-name>/` folder (a sibling to
`TASKS/phase-N/`, hence the name) with its own `README.md`; everything else —
`EXECUTION-PROCESS.md`'s Phase A/B mechanics, the four-subagent roster, the
escalation/review/log-integrity discipline — is identical to variant A. **This
variant was never formally templated anywhere before this directory** — it grew
organically across `reflex-taxonomy`, `harness-reactive-self-tools`,
`scheduling`, `teams`, `agent-host-acp`, `filesystem-snapshots`,
`plugin-system`, `skills`, `loops`, `turn-vs-run`, `feedback-carrying-denial`,
and `code-mode`. This directory's templates encode what that pattern actually
looks like in practice, not a theoretical ideal.

Both variants share the same underlying engine: `docs/engineering/EXECUTION-PROCESS.md`.
Read it in full regardless of which variant you're running — it defines the
Orchestrator/Worker/Reviewer roles, the escalation rules, the review discipline,
and the two hard-won safety rules (no repo-global `git stash` across worktrees;
live-verification writes must target an explicit scratch path) that this
project has learned the hard way, twice each.

## How Orchestrator sessions are actually booted — confirmed, not assumed

`.claude/agents/orchestrator.md` exists as a real Claude Code custom subagent
type. **It is not used that way in this project's real workflow.** Confirmed
directly by the operator (2026-08-21-era sessions): every Orchestrator is
booted as a **plain session with only the kickoff-prompt text pasted in** — no
`subagent_type: "orchestrator"` selection, no system-prompt attachment. This
matters enormously for how a kickoff prompt must be written: it cannot assume
`.claude/agents/orchestrator.md`'s roster/guardrails are already loaded as
system configuration, because they aren't. The kickoff prompt itself must tell
the fresh session to go read that file as its own literal first action, and
must inline the essential roster directly in the message text so it isn't
solely dependent on that read succeeding.

This was learned the hard way: the first real dispatch of this pattern produced
a **nested Orchestrator** — the booted session spawned another `orchestrator`
subagent to do the actual work, instead of dispatching the four leaf types
itself, because its kickoff prompt assumed a system prompt was already
attached. Every template in this directory bakes in the fix. Do not remove the
anti-recursion paragraph from a kickoff prompt to save space — it is not
boilerplate, it is a fix for a real, previously-observed failure.

## My role — the kickoff-prompt author

This is the role most likely to be missing if this process is revived by
someone (or something) that hasn't seen it done before, since it was never
written down as its own role anywhere until this directory. In practice, here
is the checklist this role follows for every batch:

1. **Locate the batch.** Find `TASKS/<batch-name>/` and its `README.md` (variant
   B) or, if none exists (this has happened — a batch got handed off mid-flight
   without one), fall back to `TASKS/INDEX.md`'s own section for that batch as
   the read-first source, and say so explicitly in the kickoff prompt.
2. **Read the design doc(s) in full.** Check specifically for a `## Status`
   section with real "approved for implementation" / "operator-signed-off"
   language (`01-architecture-doc-status-block.md`). **If it's missing, that is
   a real, load-bearing gap, not a formality to overlook** — write a mandatory
   pre-flight paragraph into the kickoff telling the Orchestrator to confirm
   approval with the operator directly before dispatching anything, rather than
   treating "the design doc exists and is thorough" as equivalent to sign-off.
   This has happened at least once (the Skills batch) and the fix pattern is in
   `06-orchestrator-kickoff-template-sibling-batch.md`.
3. **Read every task file in the batch.** Note dependencies, `Touches` lists (for
   parallelization), any task explicitly flagged as escalation-gated
   (not ready for mechanical dispatch), and any real corrections the Planner's
   own research logged against live code.
4. **Cross-check `TASKS/INDEX.md`'s section for the batch** against the README
   and task files — task counts, statuses, and dependency chains should match
   exactly. If they've drifted, that's worth a note in the kickoff.
5. **Check `TASKS/ESCALATIONS.md`** for the batch's own planning-pass entry and
   any load-bearing corrections/decisions logged there — these get restated in
   the kickoff, not just referenced, since the Orchestrator shouldn't have to
   go hunting for them.
6. **Check current repo state directly — never trust a doc's claim alone.** Run
   `git log`/`git status`, `ls internal/store/migrations/` (or this project's
   equivalent) for the real current migration ceiling, and check whether any
   *other* concurrently-planned sibling batch has an overlapping migration
   claim or touches the same files. This has caught at least one real,
   already-baked-in collision between two sibling batches' own provisional
   migration numbers before either was ever dispatched — see the pattern in
   `06-orchestrator-kickoff-template-sibling-batch.md`'s migration-numbering
   section.
7. **Check `GLOSSARY.md`** for any new terms the batch introduces, and confirm
   they're either already added or explicitly flagged as the batch's own job to
   add.
8. **Write the kickoff prompt** from `06-orchestrator-kickoff-template-sibling-batch.md`
   (or `../ORCHESTRATOR-KICKOFF-TEMPLATE.md` for a numbered phase), filling
   every placeholder with real, specific, verified content — a file path, a
   line number, a real quoted finding. Generic filler is a sign the batch
   wasn't actually read closely enough yet.
9. **Elevate anything genuinely unresolved into an explicit, mandatory pre-flight
   gate**, not a soft "watch out for this" aside — an entity-relationship
   ambiguity, an unresolved library choice, a missing sign-off. A gate should
   name exactly what to verify, exactly who dispatches what to verify it, and
   exactly what to do (stop and escalate, or fix and proceed) depending on the
   result.
10. **Report back plainly, including anything that contradicts the operator's
    own belief about the batch's status.** If they think a batch is done and
    it's one-third done, say that clearly and early, with the real evidence —
    don't soften it or bury it in the middle of a longer answer.
11. **Never dispatch, never commit, without being asked.** Writing the kickoff
    and updating tracking docs are staged work. Actually running the
    Orchestrator is always the operator's own action, in a separate session.
    Committing the kickoff prompt (and whatever else is staged) happens only
    when the operator says so.

## Tracking discipline — the root `HANDOFF.md`

Alongside writing kickoff prompts, this role keeps one file current: a root
`HANDOFF.md` (repo root, sibling to `CLAUDE.md`) — a running snapshot for
picking the whole effort back up after a compaction or a long gap. It should
always answer, as of "right now": what's landed and how (PR vs. direct commit,
pushed or not), what's staged/uncommitted and why, what's queued next, any real
known risk (a migration collision, a stale blocker, a Torque/tooling bug), and
a concrete "what's next" list. Refresh it whenever a batch completes, whenever
enough new batches get planned that the last version reads as stale, or when
explicitly asked. Don't let it silently drift — a wrong `HANDOFF.md` is worse
than a missing one, because it gets trusted without verification.

## File index

| File | What it's for |
|---|---|
| `01-architecture-doc-status-block.md` | The `## Status` section every design doc needs, so a batch's approval state is machine/agent-checkable, not something to infer. |
| `02-batch-readme-template.md` | `TASKS/<batch-name>/README.md` — the Planner's main deliverable for a sibling batch. |
| `03-task-file-template.md` | One task file — extends `EXECUTION-PROCESS.md`'s minimal skeleton with the annotated guidance real usage has taught. |
| `04-index-section-template.md` | The `TASKS/INDEX.md` section a new batch adds. |
| `05-escalation-entry-template.md` | One `TASKS/ESCALATIONS.md` entry — for either a genuine stop-and-escalate or a logged-and-resolved planning-pass finding. |
| `../failure-modes.md` | **Required reading before any of the below.** How measurements and documents mislead — tool edges, derive-don't-store, stale premises, docs-as-ground-truth, rule scope inflation. All examples real. |
| `../tracking-integrity.md` | Designated sources for duplicated tracking data, and the eight checks that catch drift between them. Spec for a checker; every assertion caught real drift. |
| `06-orchestrator-kickoff-template-sibling-batch.md` | The kickoff prompt template for a sibling batch — this role's main deliverable. |

Not duplicated here, referenced instead (single source of truth):
`../EXECUTION-PROCESS.md` (the operating procedure both variants share),
`../ORCHESTRATOR-KICKOFF-TEMPLATE.md` (the numbered-phase kickoff variant),
`../PLANNER-KICKOFF-PROMPT.md` / `../ORCHESTRATOR-KICKOFF-PROMPT.md` (the
original Phase 0-9 bootstrapping prompts — a real worked example of a
from-scratch Planner/Orchestrator kickoff pair), `../GLOSSARY.md`,
`../../../.claude/agents/{orchestrator,worker,reviewer,research-auditor,doc-writer,planner}.md`.

## Bootstrapping from zero

If none of this exists yet — a brand-new repo, or reviving the process after
everything above has been lost — build it in this order, since each layer
depends on the one before it:

1. **`docs/engineering/EXECUTION-PROCESS.md`** — the operating procedure. Write
   this first; everything else assumes it exists and is stable. Adapt the
   version in this repo rather than starting from nothing — the roles,
   escalation discipline, review discipline, and the two hard-won safety rules
   (worktree-scoped stash only; scratch-path-only live-verification writes)
   are the load-bearing parts, not the specific `TASKS.md` references.
2. **`.claude/agents/{orchestrator,worker,reviewer,research-auditor,doc-writer}.md`**
   — the four leaf subagent types plus the Orchestrator's own type definition
   (even if, per "How Orchestrator sessions are actually booted" above, the
   Orchestrator type itself ends up used only as a reference doc a plain
   session reads, not as a literal `subagent_type` selection). Without these,
   there is nothing for a kickoff prompt to dispatch.
3. **`docs/engineering/GLOSSARY.md`** and **`TASKS/ESCALATIONS.md`** — can start
   empty/skeleton, but must exist as real files before the first batch, since
   every task file and kickoff prompt references them.
4. **`TASKS/INDEX.md`** — can start as an empty table with a header row.
5. **One real design doc**, with a real `## Status` sign-off block
   (`01-architecture-doc-status-block.md`) — this is the first real content the
   whole pipeline runs against.
6. **A Planner pass** producing that batch's `TASKS/<batch-name>/README.md` +
   task files (`02-batch-readme-template.md`, `03-task-file-template.md`),
   plus a `TASKS/INDEX.md` section (`04-index-section-template.md`) and any
   real findings logged to `TASKS/ESCALATIONS.md`
   (`05-escalation-entry-template.md`).
7. **A kickoff-prompt-author pass** (this role) producing the Orchestrator
   kickoff (`06-orchestrator-kickoff-template-sibling-batch.md`), following the
   11-step checklist above.
8. **Operator review**, then **boot a plain session with the kickoff prompt
   pasted in** — confirmed as the real, working boot mechanism for this
   process, not a subagent-type selection.
9. Once the batch lands, refresh (or create) the root `HANDOFF.md` per
   "Tracking discipline" above, and start the cycle again for the next batch.
