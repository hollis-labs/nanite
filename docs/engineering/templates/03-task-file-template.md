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
A strong review note names what was independently re-verified (re-ran the
regression test, re-traced the logic by hand, re-grepped for a claimed-absent
pattern) rather than just restating the worker's own claims.>
```

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
