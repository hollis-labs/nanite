# Orchestrated Execution Process

How Phase 0 onward (`TASKS.md`) actually gets executed: one Orchestrator session, dispatching Worker and Reviewer subagents, with real safety procedure — not "move fast," derisked on purpose. If you are the Orchestrator: read this whole file before doing anything else. It is the operating procedure, not background reading.

## Roles

- **Orchestrator** (you, if you're reading this to execute) — plans, dispatches, tracks status, escalates, never writes application code directly except to resolve a dispatch/tooling problem.
- **Worker subagent** — implements one task, documents it, tests it, marks it complete.
- **Reviewer subagent** — always a *fresh* dispatch, never the same context as the worker that did the implementation. Reviews against `docs/engineering/*` for alignment, correctness, and bugs. Fixes get dispatched as their own worker task, not patched inline by the reviewer.
- **Human (operator)** — approves the plan before execution starts, is available for escalations, will also run independent Codex/Copilot review passes once real batches of work are ready. Worker and Reviewer subagents should know this last review layer exists — not so they relax, so they understand their job is "catch what you can, escalate what you're unsure of," not "be the only safety net."

## Source of truth

`docs/engineering/TASKS.md` is the decided, authoritative scope — every action item in it (cut / keep / build / rename) is settled, not a proposal up for re-evaluation. `docs/architecture-decision-log-2026-08-17.md` is the reasoning trail behind those decisions — read it for context, cite it in task files, but it is not itself authoritative and it is not internally perfect (it's a two-day chronological trail; some early framing was later corrected and the prose was never fully reconciled). When the decision log's stated rationale for an item turns out to be factually wrong about the code — the table isn't actually dead, the feature isn't actually a demo — `TASKS.md`'s action still stands. That is not a mismatch worth stopping for; see worker step 7.

## Phase A — Planning (do this first, then stop)

1. Read `docs/engineering/TASKS.md`, every file in `docs/engineering/architecture/`, `docs/engineering/GLOSSARY.md`, and `docs/architecture-decision-log-2026-08-17.md` in full before planning anything.
2. Create a `TASKS/` directory at the repo root (sibling to `docs/`, not inside it) — this is the live execution tracker, distinct from `docs/engineering/TASKS.md` (the source plan) and `docs/architecture-decision-log-2026-08-17.md` (the reasoning history).
3. Translate `TASKS.md`'s items into individual task files — see **Task file format**, below. Don't just point a worker at a `TASKS.md` line item; each task file is a complete, self-contained brief (a worker subagent starts with zero memory of this conversation or `TASKS.md`'s surrounding context).
4. Write `TASKS/INDEX.md` — see **Index format**, below.
5. For every pair of tasks you're considering running in parallel, explicitly check they don't touch overlapping files or tables. If there's any doubt, serialize them — don't guess. Note the parallelization plan in the index.
6. **Stop here.** Present the plan (the `TASKS/` folder, the index, the parallelization groupings) to the operator. Do not dispatch any worker until you get explicit go-ahead. This is the one mandatory checkpoint in the whole process — everything after it runs autonomously unless a stop condition below fires.

## Phase B — Execution (after explicit go-ahead)

Work straight through the plan without pausing for per-task approval. Post a short update when a logical section (subsystem or phase, per `TASKS.md`'s own grouping) completes — not after every single task. Keep going. The operator will interrupt if something needs their attention; you don't need to wait for permission to continue between tasks.

### Dispatching a worker

- Give the worker its full task file (see format below) — not a summary, not "go do item 14."
- **Parallel dispatch always uses `isolation: "worktree"`.** This is not optional and not something the worker is asked to remember — it's how the dispatch itself works. Never rely on a prompted instruction to "use a worktree."
- After a set of parallel workers complete, merge their branches back sequentially, reviewing each diff as it merges. Parallel dispatch removes the *collision-while-working* problem; it does not remove the need for a real integration step afterward. Don't assume parallel branches reconcile themselves.
- Serial work (anything with a real dependency, or anything you're not confident is file-disjoint from other in-flight work) just runs directly, no worktree needed.

### What a worker does

1. Read its task file in full, plus whatever `docs/engineering/architecture/*.md` file(s) it points at.
2. **Check `GLOSSARY.md` before introducing any new name.** Catching a naming collision here is free; catching it in review costs a whole review-and-fix cycle.
3. Implement the task.
4. Run the baseline check — `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` (or the frontend equivalent) — after *every* task, not just at phase boundaries. This is cheap and catches basic breakage before the next task (parallel or sequential) builds on a broken foundation. It is not the same as full validation (below) — it's the floor, not the finish line.
5. If the task involves a schema migration, test it against a real copy of the backed-up database (`~/.local/share/nanite/workspaces/default/backups/`), not just an empty fixture.
6. Document the work directly in its task file (see format) and mark status `implemented`.
7. **Distinguish the decision from its rationale.** `TASKS.md`'s stated action for this task (cut / keep / build / rename) is settled — it is not reopened by discovering the decision log's or an architecture doc's *reasoning* for it doesn't hold up. If the table isn't actually dead, the feature isn't actually a demo, the provider isn't actually removed — note the correction in this task's Work Log for the record and execute the action anyway. If the correction means the removal is bigger than expected (a live UI or test suite attached to what looked like a dead table), expand the task's scope to remove all of it — don't stop and ask whether to still do it.
   **What actually warrants stopping:** the task file's own instruction is ambiguous about what action to take; you find something with zero coverage anywhere in `docs/engineering/*` (not even wrongly — genuinely never mentioned); or doing the task as written would directly contradict another still-active `TASKS.md` item (a real item-vs-item conflict, not a decision-log prose inconsistency). Even then, this project's standing policy is aggressive removal when genuinely undecided — default to cut, log it as a heads-up in `TASKS/ESCALATIONS.md`, and keep going, rather than blocking. Reserve an actual stop-and-wait escalation for cases touching something security/trust/data-integrity-sensitive, or where a wrong default guess would be genuinely hard to reverse.

### Validation checkpoints

At the end of each logical section (a subsystem, or a full phase per `TASKS.md`'s grouping) — not after every task — do real validation, not just the baseline build/vet/test: actually exercise the feature the section implements, the way `standards/testing.md` describes (dogfeed it, don't just trust a green test suite). Mark the section `validated` in the index once this passes.

### Review

1. Once a section is implemented and validated, dispatch a **fresh** Reviewer subagent — no shared context with the worker. Give it: the diff/changes for that section, the relevant `docs/engineering/architecture/*.md` file(s), `GLOSSARY.md`, and this process doc's review criteria below. If the `code-review` skill is available to the reviewer, use it as the review mechanism, layered with the "check against `docs/engineering/*` for alignment" instruction on top — don't reinvent review tooling that already exists.
2. Review criteria: correctness/bugs (standard code review), **alignment with `docs/engineering/*`** (does this match the target architecture, not just "is this good code" in the abstract), and **regression into the patterns this whole redesign exists to eliminate** — new naming collisions, silent fail-open, a hardcoded value duplicated across multiple call sites instead of one typed source of truth, a feature that's wired but never actually reachable. `standards/patterns.md` and `standards/code-quality.md` are the checklist for this last category.
3. If the reviewer finds a real issue: it dispatches a **fix** as its own worker task — same task-file format, same document/test/complete discipline as original work, not a quick inline patch. Then re-review.
4. If the reviewer is unsure whether something is actually a problem (not confident either way) — same rule as workers: stop, escalate, don't guess.
5. Mark the section `reviewed` in the index once it passes.

### Escalation

Whenever a worker or reviewer hits a genuine unknown — `TASKS.md`'s own instruction is ambiguous about what to do, something has zero coverage anywhere in `docs/engineering/*`, or completing a task as written would directly contradict another still-active `TASKS.md` item — stop that task, report up to the Orchestrator with the specific question. **A decision-log or architecture-doc passage being factually wrong about the code is not, by itself, grounds to stop.** The decision it supports still stands; correct the record in the task's Work Log and execute (worker step 7 has the full rule, including the default-to-cut fallback for genuinely undocumented cases). The Orchestrator brings true escalations to the operator. **Keep a running log of every escalation and its resolution** (`TASKS/ESCALATIONS.md`) — if the same ambiguity would otherwise resurface in a later task, check this log first instead of re-escalating something already answered.

This is an expected stop for a genuine unknown, not a checkpoint for re-verifying decisions that are already made. The two-day design review this plan comes from was about refusing to guess past a real mismatch between what a doc claimed and what the code does — it was not about leaving every decision open to relitigation for the rest of the project's life. Once `TASKS.md` says what to do, verifying *how big the job is* is real work; verifying *whether to still do it* is not, unless one of the three conditions above is actually true.

## Task file format

`TASKS/<phase>/<NN>-<slug>.md` — one file per task.

```markdown
# <task title>

**Phase:** <matches TASKS.md phase>
**Status:** not-started | in-progress | implemented | validated | reviewed | done
**Depends on:** <other task file(s), or "none">
**Touches:** <files/tables/packages this task modifies — used for the parallelization-safety check>

## Context
<Why this task exists — the real reasoning, not just "TASKS.md says so." Pull from
docs/architecture-decision-log-2026-08-17.md and the relevant architecture doc. A
worker with zero memory of the design review should understand *why*, not just *what*.>

## What to do
<Concrete, specific. File paths, function names, table names where known.>

## Done means
<Concrete acceptance criteria — what "implemented" actually looks like for this task.>

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
```

## Index format

`TASKS/INDEX.md` — one row per task, kept current by the Orchestrator as work completes.

```markdown
| Task | Phase | Status | Depends on | Parallel group |
|---|---|---|---|---|
| 01-fix-model-pinning | 0 | done | none | — |
| 02-fix-request-start | 0 | reviewed | none | A |
| ... | | | | |
```

## Non-goals for the Orchestrator

- Don't make new architecture decisions beyond what `docs/engineering/*` already settled. Ambiguity about *how* to implement something the docs don't specify is an escalation, not something to improvise past.
- Don't reopen a `TASKS.md` action item because the decision log's stated rationale for it turns out to be inaccurate. Correct the rationale, keep the action — see **Source of truth** and worker step 7.
- Don't hesitate to cut things per the standing dead-code policy because you're worried about losing them — git history has it, and the operator has already confirmed no live traffic depends on any of this during execution. Caution here just slows down decided work.
- Don't skip the fresh-reviewer step because a worker seems confident. The whole reason for a *fresh* reviewer is that confidence and correctness aren't the same thing — this design review found real, confidently-stated claims that were simply wrong, more than once.
