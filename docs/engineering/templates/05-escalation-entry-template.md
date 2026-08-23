# `TASKS/ESCALATIONS.md` entry template

Every genuine unknown, real bug found during review, process incident, or
planning-pass finding gets one entry here — this is the project's single
running log across every batch, numbered-phase and sibling alike. Two shapes
cover almost everything that's ever gone in it.

## Shape A — a genuine stop-and-escalate (rare; most findings are Shape B)

```markdown
## <DATE> — <short title naming the actual question or mismatch>

**Raised by:** <who/what found it — a worker, a reviewer, a Planner's own
research, an Orchestrator's dogfeed>
**Question / mismatch:** <the real ambiguity or contradiction — per
EXECUTION-PROCESS.md's Escalation section, this should be one of: the task
file's own instruction is genuinely ambiguous about what to do; something has
zero coverage anywhere in docs/engineering/*; or completing the task as
written would directly contradict another still-active decision. A
decision-log or architecture-doc passage being factually wrong about the code
is NOT, by itself, grounds for this shape — that's a Shape B correction
instead.>
**Resolution:** <how it was actually resolved — operator input, a documented
default choice with reasoning, or "not resolved — genuinely open, needs a
fresh operator conversation before the gated task can be dispatched.">
**Follow-up:** <what happens next, and who/what is blocked on it.>
```

## Shape B — a finding logged for the record, not blocking anything

The far more common shape. Used for: a real correction to a design doc's or
task file's stated fact, a bug found and fixed during review, a process
incident and its fix, or a design-latitude call the Planner made and wants on
record.

```markdown
## <DATE> — <short title>

**Question / mismatch:** <what was claimed vs. what's actually true, with a
real citation — file:line, a grep result, a direct test run.>
**Resolution:** <what was done about it — corrected the doc in place, fixed as
a new numbered task (name it), or "no fix required — cosmetic/documentation-
accuracy only, not a functional defect.">
**Follow-up:** <anything not resolved here that a future reader should know
about — a named, deliberately-deferred piece of work, a "revisit if X ever
happens" note.>
```


## Where a finding belongs: this log, or the wave handoff?

Three times in one batch, a real finding was written **only** into a
`WAVE-N-HANDOFF.md` and never reached this log. Each time it was a genuine
item somebody would need later; each time it was one wave away from being
invisible.

**A wave handoff is read once — by the next wave's kickoff author — and then
becomes historical.** `ESCALATIONS.md` is the project's single running log
across every batch. They are not interchangeable, and "I wrote it down" is not
the same as "it will be found."

### The test

> **If the next kickoff author never reads this, does it still need to
> survive?**

Yes → `ESCALATIONS.md`. No → the wave handoff. **Both**, when an item is
durable *and* immediately actionable — write it here in full and reference it
from the handoff rather than restating it.

### Goes in `ESCALATIONS.md`, always

- **A real defect found and deliberately not fixed.** Out-of-scope is the
  correct call; leaving it only in a handoff is not.
- **A deferred verification gate** — any task closing below `reviewed`, and
  why. *(Missed once: a deferred `-race` gate on the only task in its wave that
  didn't reach `reviewed`.)*
- **A newly discovered unwired feature, dead subsystem, or island.** *(Missed
  once: an unwired messaging path found while wiring something adjacent.)*
- **A process incident and its fix** — especially anything that touched
  operator data or state outside the repo.
- **A correction to a stated fact** in a task file, design doc, or guide.
- **Anything whose owner is "to be determined."** No owner is exactly why it
  needs a durable home.

### Goes in the wave handoff

Things the next kickoff author needs *right now*, which expire once they've
used them: which citations drifted, what parallelization changed, which tasks
are safe to trust as dependencies, verification commands to re-run.

### The failure is quiet

Nothing breaks when an item lands only in a handoff. It reads as diligent —
the finding *was* recorded. It simply becomes unfindable two waves later, when
whoever needs it has no reason to open a handoff for a wave they weren't part
of.

## What actually goes in this log — patterns from real entries

- **Two unrelated batches' own provisional migration-number claims
  colliding** — logged even before either batch is dispatched, as soon as a
  kickoff-prompt author or Planner notices the arithmetic doesn't reconcile.
  Real precedent: `TASKS/loops`' task `09` and `TASKS/code-mode`'s task `03`
  both provisionally claimed the same migration number, caught before either
  batch started.
- **A design doc's own architecture-review pass finding something the
  original scoping brief assumed wasn't true** — e.g. "assumed this needs no
  schema migration; confirmed it does, because a *different*, more-recently-
  landed batch changed the ground this task stands on."
  This is a correction, not a stop-and-escalate, unless it also makes the task
  genuinely undecidable without operator input.
- **A process incident and its fix** — a rogue sub-dispatch writing files
  outside its read-only scope, a repo-global `git stash` colliding across
  worktrees, a fabricated log entry claiming a review happened that didn't.
  These get logged even when "closed, no lasting damage," because the
  point is building a searchable history of failure modes so they don't
  get rediscovered from scratch by a later session.
- **A task explicitly marked not-ready-for-mechanical-dispatch** — the log
  entry is what a later Orchestrator checks before deciding whether that
  task's gate has actually been resolved yet, rather than trusting the task
  file alone (which may not get updated the moment a decision is made
  elsewhere).

## The rule about closing an escalation with a recommendation

Per `EXECUTION-PROCESS.md`'s "Promote recommendations, don't just log them"
section: closing an entry with "here's what should change going forward" is
not the same as making that change stick. If the recommendation is something
future workers/sessions need to actually see *before* they'd repeat the
mistake, it needs to land somewhere that's actually read at the relevant
moment — `EXECUTION-PROCESS.md` itself, a task-file template, or an
agent-type definition — not just exist as prose in this log that nobody
re-reads until after the fact. This project has been bitten twice by the same
mistake because a recommendation was logged here and never actually
propagated anywhere it would be read in time.
