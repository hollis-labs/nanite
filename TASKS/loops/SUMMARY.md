# Loops — Batch Summary

**For:** the operator. This batch is closed — all 13 tasks (`01`-`13`, all 4 phases) are
`reviewed` in `TASKS/INDEX.md`. Implements `docs/engineering/architecture/21-loops.md` in full.
See `TASKS/loops/HANDOFF.md` for the technical handoff to whoever picks up related work next;
this doc is the plain-language, what-shipped-and-what-needs-your-attention version.

**Worktree/branch state:** everything in this batch is committed on branch
`worktree-loops-batch`, in the isolated worktree at
`/Users/chrispian/dev/hollis-labs/apps/nanite/.claude/worktrees/loops-batch`. This was kept
deliberately isolated from `main` and from the concurrent `plugin-system`/`skills` batches
running at the same time, per your own instruction at kickoff. The working tree is currently
clean (nothing uncommitted). **Merging this branch into `main` — and reconciling this batch's
migration numbers against whatever `plugin-system`/`skills` land with in the
meantime — is a decision for you, not something done as part of this batch.**
**Update, 2026-08-22:** `skills` has since landed real `136`/`137` migrations on `main`,
directly colliding with this batch's own (then-)`136`/`137`. Resolved via a uniform `+3`
renumbering of this batch's entire range: `135`-`143` → `138`-`146`. This is the last
blocking step before this branch merges into `main`.

---

## What shipped, by subsystem

### Schema foundation (Phase 1)

Two new first-class entities, persisted from day one: **Goal** (`goals` table — a target state
with its own lifecycle, independent of any one execution) and **LoopRun** (`loop_runs` — a
*new peer* to `WorkflowRun`, not a `WorkflowRun` itself, because a Loop's plan can change shape
between iterations in a way the workflow engine's DAG-only design deliberately can't represent).
`goal_evidence` is a thin pointer table, not a duplicate content store — every row points at
something real (a verify result, a gate resolution, an event-log entry), never free text alone.
`loop_run_iterations` is the append-only per-iteration history. `workflow_runs` gained two
scoping columns so any `WorkflowRun` can answer "which loop, which iteration" directly.
`workflow_run_steps` gained a widened `kind` CHECK (`'loop'`) so a Workflow can later contain a
Loop as one of its steps.

### The runtime engine (Phase 2)

A new, small decision function — `Decide` — is the one genuinely new piece of logic this whole
batch adds: given a goal, an evaluation, history, and a budget, it deterministically decides
`COMPLETE`/`ESCALATE`/`FAIL`/`CONTINUE`, and falls back to a single, isolated LLM call (the same
shape the existing Verify-mode reviewer step already uses) only for the one case that genuinely
needs judgment — a no-progress streak. `LoopEngine` drives a sequence of ordinary `WorkflowRun`s
against that decision, pausing cleanly (no busy-waiting) when a decision needs a human or an
external condition. A Workflow can now contain a Loop (`StepKindLoop`) using the identical
pause/resume plumbing the engine already reused twice before for gates and flex phases, complete
with a real, working push mechanism so the outer workflow genuinely wakes back up when the
contained loop finishes — not just a hope that something else happens to call `Resume` later.

### Three ways to trigger a Loop (Phase 3)

1. **Manual/API** — `POST /api/loops` to launch, `GET` to inspect, `/cancel` and `/resolve` to
   intervene, plus full Goal CRUD under `/api/goals`.
2. **Event/predicate** — a new, named reflex action kind (`resume_loop_run`), reusing the
   existing reflex trigger language (the same predicate/event/interval vocabulary every other
   reflex already uses) rather than inventing a new one.
3. **Scheduled polling** — a new job type (`loop_run_tick`), the fifth alongside the four
   Scheduling already shipped, for a loop waiting on an external condition to periodically
   re-check.

**These three were designed to be independent, and two of them weren't actually connected to
each other until a fix landed — see "What got escalated" below.** This is fixed now and
independently re-verified, but it's worth understanding why it happened, not just that it's
resolved.

### Presets (Phase 4)

A small, Go-coded registry of named `(budget, continuation policy, iteration template)`
bundles — no new database table, since presets are pure configuration with no independent
lifecycle. **`ralph`** — repeat a simple task up to N times until it's done or budget runs
out — is built and tested completely, end to end. The other six named presets from the design
(`test-fix`, `review-fix`, `plan-execute`, `queue-drain`, `durable`, `self-improve`) exist as
named placeholders you can reference by name today, but none of them actually do anything real
yet if launched — that's honest, disclosed follow-up work, not a silent gap.

---

## What got escalated during this batch, and how it resolved

All entries below are in `TASKS/ESCALATIONS.md`. Cited by title so the full detail can be looked
up rather than re-explained here.

- **"Loops planning: `LoopStep` dynamic-YAML reachability, and the flex-trigger 'reuse' claim is
  incomplete"** (2026-08-21, pre-flight planning) — clean audit, no blocking findings. Confirmed
  a legacy, unrelated pipeline primitive (`internal/workflow.LoopStep`) is reachable via an
  existing API but has zero real callers — not touched by this batch, flagged for the record.
  Also found and resolved a real design gap before any code was written: the design doc assumed
  Loop's event/predicate trigger could fully reuse an existing mechanism the way Teams does, but
  that mechanism turned out to be a lazy re-check with nothing to piggyback on for a Loop — task
  `11` built a real, new, legible mechanism instead of assuming reuse would work.
- **"Loops Wave 1 (`01`, `06`): same-batch migration-number collision"** (2026-08-21) — two
  tasks dispatched in parallel each independently claimed the same migration number from their
  own isolated worktrees. Caught and fixed at merge time (renumbered one of them) with zero
  operator involvement needed — the exact kind of collision this batch's own kickoff anticipated,
  just one step earlier than expected.
- **"Task `01` (goals schema) worker ran a repo-global `git stash`"** (2026-08-21, fourth
  documented occurrence of this exact rule violation across this project's recent batches) — no
  data lost, self-recovered, task otherwise implemented cleanly.
- **"Orchestrator's own `go test ./... | tail -N` verification masked a real build failure"**
  (2026-08-21) — a process bug in the Orchestrator's own verification method, not a worker's
  mistake: piping test output through `tail` hid a real compile failure in the merged tree (two
  parallel tasks had each independently added an identically-named test helper). Caught by the
  next reviewer running the command directly, fixed with a one-line dedup. The Orchestrator now
  redirects to a file and checks the exit code directly for the rest of this batch.
- **"Task `07`'s `Decide()` re-implements task `02`'s goal-evidence formula locally"**
  (2026-08-21) — a duplicated formula, judged non-blocking at review time and explicitly flagged
  for the very next task to revisit. It was: task `08` removed the duplicate entirely one task
  later.
- **"Phase 3's two parallel trigger tasks (`11`, `12`) built compatible but disconnected
  mechanisms"** (2026-08-21/22) — **the most instructive finding in this batch.** Tasks `11` and
  `12` were dispatched in parallel as "mutually parallel-safe" work, and each was individually
  correct, individually well-tested, and individually reviewed clean. But nothing connected
  them: the scheduled tick (`12`) called the resume function directly and unconditionally,
  never checking whether an event/predicate reflex (`11`) was attached and had or hadn't fired
  yet. The result was a fully-built, fully-tested trigger mechanism that was completely
  unreachable in the actual running system — neither task's own review process could have caught
  this, since each diff looks correct in isolation; it took someone tracing the real, merged
  call path to find it. Fixed with a small bridging component and re-reviewed sound the next
  day. This is a genuine lesson about this batch's own parallel-dispatch methodology: "safe to
  build in parallel" is not the same guarantee as "will actually be wired together" when two
  parallel tasks are really building two halves of one producer/consumer relationship.
- **"Fifth occurrence of the `git stash` incident, this time from a reviewer, not a worker"**
  (2026-08-22) — during the review of the `11`/`12` fix above, the reviewer (not a worker) hit
  the identical forbidden-`git-stash` pattern already flagged four times before in this project.
  No data lost. This time the underlying gap was actually fixed at the source, rather than
  logged as a sixth thing to watch for: the "never run `git stash`" warning already present in
  the worker agent definition was missing from the reviewer agent definition (both have the same
  tool access) — now both carry it.

**One item investigated and closed out, not left open:** a Work Log detail in task `06`
describes a `git stash` round-trip during its own baseline verification, phrased almost
identically to the pattern flagged as incidents four and five above — never logged as an
incident at the time. Checked directly: task `06` was dispatched in Wave 1, before this batch's
explicit "never run `git stash`" instruction existed in any worker prompt (that instruction only
started appearing from Wave 2 onward, once task `01`'s own incident surfaced the need for it).
This is realistically the first or second chronological occurrence of the pattern in this
batch, not a separate sixth one — and there's no evidence of harm: task `06` was reviewed
clean, merged, and built upon without incident through dozens of subsequent clean build/test
checkpoints. No further action needed — the gap that let it happen (a worker dispatched before
the warning existed) is already closed structurally, since the warning now lives in the shared
agent-type definitions rather than per-dispatch prompt text.

---

## Still flagged, deferred, or needing your attention

1. **The `WAIT`/`ESCALATE` prompt-ambiguity gap** in the reasoning-fallback LLM call
   `Decide` uses. The model is asked to choose between `WAIT` and `ESCALATE` with no criteria
   telling it what each actually means operationally — `WAIT` should mean "safe to auto-retry
   later," `ESCALATE` should mean "stop and get a human." Task `12` is the first (and only) piece
   of this batch to attach a real, automatic behavior to that choice (an unattended re-launch),
   and nothing today makes the model's answer reliable enough to carry that weight safely.
   **Recommend prioritizing this before anyone builds a real `durable` preset on top of Loops** —
   `durable` is explicitly the unattended, long-running, low-supervision use case this ambiguity
   would matter most for, and it's one of the six presets not yet built. Not fixed in this
   batch, deliberately — it's a real behavior change to an already-reviewed file, not something
   to bundle into an unrelated fix.
2. **Six of the seven named presets are stubs.** `ralph` works end-to-end; `test-fix`,
   `review-fix`, `plan-execute`, `queue-drain`, `durable`, `self-improve` are registered by name
   but return a clean "not implemented" error if launched. This was the deliberate, disclosed
   scope for this batch (README: "`ralph` is the one preset this batch builds and tests fully
   end-to-end... the rest... are a real follow-up, not silently dropped").
3. **No frontend/admin surface for Goals or Loops.** Explicit scope fence for this entire batch,
   matching every prior batch's own convention — a separate stream if/when you want one.
4. **Several small, named, non-blocking technical gaps** live in `HANDOFF.md`'s numbered
   follow-up list (item 4: preset registry doesn't survive a process restart mid-paused-run;
   item 6: `loop_runs.status` can't distinguish a `WAIT` pause from an `ESCALATE` pause at the
   database level, only per-iteration history can; item 7: a scheduled poll tick is one-shot, not
   self-rescheduling; item 10: `Cancel` has no precondition check against an already-terminal
   run). None of these block anything this batch claims to ship; all are candidates for whoever
   does the next round of Loop hardening.
5. **Mid-run Goal decomposition** (`parent_goal_id`, one goal recomputing its own subgoals) — the
   database column exists, but no engine reads it yet. Named in the original design doc as a
   real capability, deliberately out of this batch's scope.

---

## Current `TASKS/INDEX.md` state for this batch

All 13 tasks done — 0 in-progress, 0 blocked, 0 not-started.

| Phase | Tasks | Status |
|---|---|---|
| 1 — Schema & storage foundation | `01`, `02`, `03`, `04`, `05`, `06` | all reviewed (real migrations: `138`, `140`, `141`, `142`, `143`, `139` respectively) |
| 2 — Runtime engine | `07`, `08`, `09` | all reviewed (`09`'s migration: `144`); `08` includes one real review-found bug, fixed and re-verified |
| 3 — Trigger surface | `10`, `11`, `12` | all reviewed (`11`'s migration: `145`, `12`'s migration: `146`); `11`/`12` required a dedicated post-merge integration fix, independently re-reviewed sound |
| 4 — Presets | `13` | reviewed |

No task in this batch is open, blocked, or awaiting a decision from you. The one real decision
point ahead of you is prioritization, not a blocker: whether the `WAIT`/`ESCALATE` prompt fix
(item 1 above) should happen before anyone starts building `durable` or another
unattended-running preset on top of this foundation.
