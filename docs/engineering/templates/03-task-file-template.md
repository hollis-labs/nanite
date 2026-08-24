# Task file template

`docs/engineering/EXECUTION-PROCESS.md`'s own "Task file format" section
defines the base skeleton — this file doesn't replace it, it annotates it with
what real usage across a dozen batches has taught about what actually needs to
go in each section. Read `EXECUTION-PROCESS.md`'s version first; this is the
same shape with guidance filled in.

```markdown
# <task title — specific enough that a reader knows what it does without opening the file>

**Phase:** <N — phase name, matching the batch's own phase table>
**Status:** not-started | in-progress | implemented | validated | reviewed | done
**Depends on:** <other task file(s) in this batch, or "none">. If the
dependency is a *sequencing* choice rather than a real file/type dependency
(this happens often — task B doesn't technically need task A's code to exist
to compile, but running B before A is functionally meaningless, or creates
unnecessary rework risk), say so explicitly: "sequencing only — see below,"
with the real reason stated in Context. This distinction matters to an
Orchestrator deciding whether a "soft" dependency can be relaxed under time
pressure.
**Touches:** <files/tables/packages this task modifies — the parallelization-
safety check depends entirely on this being accurate and complete. If this
task lands in a different repo than the batch's primary one (a sibling
`libs/*` repo), say so explicitly: "Repo: <name>, not <primary repo>.">

## Context

<Why this task exists — pull from the design doc and, if this batch has one,
`docs/architecture-decision-log-*.md`. A worker with zero memory of the design
review should understand *why*, not just *what*. This is also where any real,
load-bearing correction the Planner's own research found against live code
belongs — with a file:line citation, not just a claim. If the task's own
scope is bigger or smaller than an outer framing assumed (a design doc, a
scoping brief, an illustrative sketch), say so explicitly and why — this has
been a real, common pattern (a 3-task illustrative sketch that became 4 real
tasks once a function's actual complexity was traced; a task assumed to need
no schema change that turned out to need one, once a *different*, more-recently-landed
batch's schema changes were accounted for).

If a design decision this task implements is flagged as adjustable/provisional
rather than locked (a naming choice, a default value, a config shape), say so
explicitly — a worker deviating from it should document their own call in the
Work Log, not be told they got it wrong later for a decision that was never
actually locked.>

## What to do

<Concrete, specific. File paths, function names, table names where known —
but also: tell the worker explicitly where to verify rather than assume, any
place this task's own citations might be stale (a function that's moved, a
line number that's shifted since this task was authored, a file that's had
substantial unrelated churn from a different task landing in between). "Find
the real X before assuming this task's own guess at its location" is a
legitimate, common instruction when a task depends on ground that's been
moving.>

## Done means

<Concrete acceptance criteria — testable, not just descriptive. Prefer "a test
that does X and asserts Y" over "should handle X correctly." If a specific
edge case or regression class matters (a worker rebuilding a marker/parser
that had a known prior bug, a boundary condition that's the whole reason this
task exists), name the specific test that proves it's actually fixed, not just
that the happy path works.>

## Work log

<Worker fills this in as it goes: what was actually done, any deviation from
plan and why, anything escalated, which design call was made if the task's
own Context flagged something as adjustable.>

## Review notes

<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.
**A defect you find and correctly decline to fix goes to
`TASKS/ESCALATIONS.md`, not only here** — a Review-notes entry is found only by
someone already reading this task file, which is nobody once the task closes.
See `05-escalation-entry-template.md`'s "Where a finding belongs".
A strong review note names what was independently re-verified (re-ran the
regression test, re-traced the logic by hand, re-grepped for a claimed-absent
pattern) rather than just restating the worker's own claims.>
```

> **See `docs/engineering/failure-modes.md` — required reading.** It covers
> this section's material plus three classes it doesn't: stale premises,
> documents being read as ground truth rather than best-effort snapshots, and
> rule scope inflation. Every example there is real, from this batch.

## Numeric claims — the most reliable source of drift in this process

Five separate count errors surfaced across Waves 0-2, in task files, kickoffs,
handoffs, and summaries alike. The lists were correct every time; only the
prose summarizing them drifted. They split into two kinds, and the two need
different defenses.

### Kind 1 — wrong at the source

The number faithfully reports a bad command. Re-reading the prose never catches
this, because the prose is accurate about a measurement that was wrong.

- A task file's completeness grep used `'^func \(s \*Store\) [A-Z][A-Za-z0-9]*\('`
  — the pattern ends at the opening paren, so it counted **all 371** exported
  methods rather than the **237** lacking a context. Everything downstream
  inherited it, including a fabricated "target: 505".
- The same file's oracle used `grep -rhoE '\.Exec\(' … | grep -v _test`.
  With `-h -o`, filenames are stripped *before* the filter, so `grep -v _test`
  matched against `".Exec("` and excluded nothing. Use
  `--include`/`--exclude` instead.

- A planning doc reported the audit's rescued evidence as "~16 MB, 30 files"
  from `du -sh` on the source directory; the real figure was **8.0 MB, 29
  files**. `du` reports allocated disk blocks, not summed file sizes, and the
  directory listing included `.`/`..`. A different flavor of the same fault:
  the command ran fine and answered a slightly different question than the one
  being asked.

**Defense: ship the command next to the number**, so verifying is a paste
rather than an investigation — and sanity-check the command itself against a
case whose answer you already know. In the first example above, the number that
caught the error was found by an executing agent *because the command was
printed in the task file*. That is the mechanism working.

### Kind 2 — right once, then copied

A correct count restated in prose in several places, where one restatement gets
reframed and the rest inherit it.

- A kickoff said "twelve tasks" in four places while listing thirteen. The
  off-by-one entered through a framing choice elsewhere in the same file
  ("`06/03` is a thirteenth, out-of-wave task"), which implied the in-wave set
  was twelve.
- The same kickoff said the sweep rewrote "six of the eight" files it touches;
  the real figure was seven of nine.

Restating a number reads as emphasis, not as an independent unverified claim —
which is exactly why it does not feel like duplication while writing it.

**Defense: state a count once and reference it thereafter.** Put it in the
table that is derived from the list, and elsewhere say "the task list above"
rather than repeating the figure. Where a number must appear twice, mark one
authoritative in the text — *"count from this list; if any other number
appears below, this line wins."*

### The short version

Derive every number from a command at the moment you write it. Never recall one
from earlier in the same document, and never carry one across documents.

*(This section's first draft said "five separate count errors" while listing
four — written by the same author who had just spent a session finding the
other five. The pull toward a round summarizing number is strong enough to
survive knowing about it, which is the argument for deriving rather than
resolving to be careful.)*

## Patterns worth carrying forward, observed repeatedly

- **A task whose scope turns out bigger than expected doesn't get silently
  narrowed back down** — if tracing the real code shows the removal/change is
  bigger than a design doc assumed, the task's scope expands to cover the real
  thing, with the correction logged, not quietly limited to the doc's original
  (now-known-wrong) estimate.
- **A schema-migration task always gets tested against a real backup copy of
  the database**, never just an empty fixture — this is a hard requirement in
  every batch that's touched schema, not a suggestion.
- **A task landing in a sibling repo needs its own explicit note on
  cross-repo collision risk** if that repo has any other concurrently-active
  work — check the sibling repo's own `git log` immediately before merging,
  not just at task-authoring time.
- **A task that's explicitly not ready for mechanical dispatch** (a genuinely
  open design question the Planner couldn't resolve — a library choice, an
  auth model, anything the design doc itself flags as deliberately
  undecided) needs a visible banner saying so, both in the task file and in
  the batch's `TASKS/INDEX.md` row, so an Orchestrator doesn't dispatch it like
  routine work. See `05-escalation-entry-template.md` for how this gets logged.
