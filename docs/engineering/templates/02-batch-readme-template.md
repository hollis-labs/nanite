# `TASKS/<batch-name>/README.md` template

The Planner's main deliverable for a sibling batch (variant B — see this
directory's own `README.md`). Every real batch so far (`reflex-taxonomy`,
`teams`, `scheduling`, `plugin-system`, `skills`, `loops`, `agent-host-acp`,
`turn-vs-run`, `feedback-carrying-denial`, `code-mode`) has one shaped like
this. One real batch (`filesystem-snapshots`) shipped without one, because it
was handed off mid-flight — its kickoff prompt had to point at
`TASKS/INDEX.md`'s own section as a substitute and say so explicitly. Prefer
always writing one; it's cheap relative to what it saves a later reader.

```markdown
# <Batch Name> — implementation

Implements `docs/engineering/architecture/NN-name.md`<'s "Target design"
sections>, the design produced by a dedicated <architecture-alignment |
design> session (<DATE><, operator-signed-off>) that <one sentence on how the
design was reached — reviewed an external proposal against real code, audited
X against Y, reconciled an old audit against current state, etc.>. A sibling
to `TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`,
`TASKS/scheduling/`, `TASKS/teams/`, `TASKS/agent-host-acp/`,
`TASKS/filesystem-snapshots/`, `TASKS/plugin-system/`, `TASKS/skills/`,
`TASKS/loops/`, `TASKS/turn-vs-run/`, `TASKS/feedback-carrying-denial/`, and
`TASKS/code-mode/` — kept in its own top-level `TASKS/` subfolder for the same
reason those are: this work originates from a dedicated architecture-review
pass, not `docs/engineering/TASKS.md`'s original plan.

<If this batch spans more than one repo (a sibling `libs/*` repo, most
commonly), say so here in its own short section — which phases/tasks land
where, and any real collision risk with other concurrently-active work in that
sibling repo. This has been load-bearing more than once (`agent-host-acp`,
`filesystem-snapshots` both landed real work in `libs/go-agent-wrapper`
concurrently).>

## Read before starting any task here

<Numbered list. Always include, in some order: the design doc(s) in full; any
precedent code the batch's own engine/launcher/mechanism is directly modeled
on (cite real files, not just "similar to X"); GLOSSARY.md, with a note on
which new terms this batch adds; EXECUTION-PROCESS.md; and — the single most
valuable item in this section when it exists — an explicit callout of any
task file carrying a real, load-bearing correction the Planner's own research
found against live code, so a reader doesn't have to discover it independently
per task.>

## <Load-bearing corrections / decisions this planning session made> — real findings, not assumed

<For every real correction the Planner's own research found against live code
— not a doc-vs-doc inconsistency, an actual code-vs-doc mismatch — state it
here with a file:line citation and which task it grounds. This section is
what lets a kickoff-prompt author (and later, an Orchestrator) trust the
batch's scoping without re-deriving it from scratch. Real examples from past
batches: "the audit's named unmanaged-schema offender is already gone from
this repo — this task is genuinely new infrastructure, not a fix to a live
offender"; "X's own doc comment says it does one thing, but its actual
implementation does something structurally different"; "a mechanism the design
doc says to plug into is confirmed dormant — zero live call sites — so this
task builds direct enforcement instead.">

## What this batch does NOT do

<A real scope fence, carried forward from the design doc's own explicit
exclusions plus any planning-session scoping calls. Every item here should be
something a worker or reviewer might plausibly think is "obviously in scope"
and isn't — that's the test for whether it belongs in this list. Generic
exclusions ("no frontend work") are fine to include but shouldn't be the only
content; the valuable entries are the non-obvious ones with a real reason
attached.>

## Task sequence

<Flat-numbered `01`-`NN` across however many phases, one table per phase, each
row: Task | Depends on. State the numbering/phase convention explicitly if it
matches a prior batch's, so a reader can pattern-match.>

## Parallelization plan

<Explicit waves — "Wave 1: tasks X, Y, fully independent, worktree-isolated"
etc. — cross-checked against each task's own `Touches` list for real file
overlap, not assumed from the dependency table alone. State the reasoning for
any task that's *file-disjoint but still sequenced* (a real, load-bearing
distinction — see `03-task-file-template.md`'s note on this) rather than
leaving a reader to wonder why it isn't marked parallel-safe.>

## Migration numbering

<If the batch needs any: state the highest existing migration number on disk
at planning time, which number(s) this batch provisionally claims and for
which task(s), and an explicit instruction to re-list the migrations directory
before any actually lands — plus a note on which *other* concurrently-planned
batches might also be claiming numbers in the same range. If the batch needs
none: say so explicitly and why (e.g. "purely in-process Go refactor," "all
state already in-memory," "reuses an existing column") — a reader shouldn't
have to infer "no migration" from silence.>

## Escalations logged during this planning pass

<Point at the specific `TASKS/ESCALATIONS.md` entry/entries this batch's
planning produced (see `05-escalation-entry-template.md`), rather than
repeating their content here.>
```

## Guidance on tone and rigor

The best real READMEs read like a planning session's own case file, not a
spec — every claim about the current code is either cited (file:line) or
flagged as unverified. A README that just restates the design doc's prose in
different words hasn't done the job; the job is grounding the design doc's
claims against what the code actually does *right now*, since design docs are
often written before or during the same period as other, unrelated work that
changes the ground they stand on.
