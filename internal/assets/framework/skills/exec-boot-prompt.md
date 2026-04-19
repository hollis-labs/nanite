# Exec Boot Prompt (:exec-boot-prompt)

Generate a **heavy-weight boot prompt** for an execution session that follows a planning session. Different from the generic `boot-prompt` skill — this one assumes a spec + plan already exist and the next session is an implementation session.

## When to use

- At the end of a planning session (brainstorming → spec → plan), when the user asks for an execution boot prompt
- After writing an implementation plan via `superpowers:writing-plans`
- When the user says "create a boot prompt for the next session to execute this plan"
- When the user asks for a handoff document for a dedicated execution session

**Do not use** for generic "save this for next time" — that's the `boot-prompt` skill (short, ~1k tokens, focused on next actions). Exec boot prompts are 2–4K tokens and include critical state, decisions-already-locked, scope-creep fences, and exit-gate criteria.

## Prerequisites for this skill to fire

The session must have produced (or the user must point at):
1. A **spec doc** at `<project>/docs/superpowers/specs/YYYY-MM-DD-*.md`
2. A **plan doc** at `<project>/docs/superpowers/plans/YYYY-MM-DD-*.md`
3. Committed artifacts (spec + plan) in the project repo

If any of these are missing, stop and ask the user to produce them first (usually via `superpowers:writing-plans`).

## Procedure

1. **Gather inputs** from the current session context:
   - Project slug (e.g., `clockwork-manifold`, `nanite`)
   - Feature / session name (e.g., `task-model-mvp`, `phase-3-s4b-trust-boundary`)
   - Spec file path (absolute, in project repo)
   - Plan file path (absolute, in project repo)
   - Latest `main` commit SHA (short) and description of its role
   - Any sibling / in-flight sessions the executor needs to know about
   - Decisions locked during the planning session (4–7 typically, most important first)
   - Critical state notes — things the executor will get wrong without a warning (migration numbers, deferred items, naming collisions, dep additions, semantic gotchas)
   - Exit gate / MVP success criteria from the spec §8 or equivalent
   - BLGs filed this session (IDs + one-line summaries)

2. **Sample existing exec boot prompts** to match style + depth. Read 1–2 from `agent-workspaces/boot/<project>/` — especially for the nanite project, these are the canonical reference examples.

3. **Write the boot prompt** to `agent-workspaces/boot/<project>/boot-prompt-<feature>.md` with this structure:

```markdown
# Session Boot — <project> (<role>) — <Feature Name>

**Last updated:** <date> after the planner session.
**Scope:** This boot prompt is **focused on <feature> only**. For session-agnostic state use `boot/<project>/boot-prompt.md`.

> **Memory + knowledge:** Vanta-primary (`vanta-primary-since: 2026-04-19`). Recall Vanta first (`memory_recall`/`conduit_lookup`), file-based is legacy fallback. Writes → Vanta only via `capture-to-vanta`. See `~/.claude/CLAUDE.md` for full contract.

## Where we are

- **`main` tip at handoff:** `<sha>` — <description>. Prior track commits: ...
- **Working tree:** clean / dirty. Verify before starting.
- **Open PRs** for this track: <list or "none; you will create `feat/<slug>`">
- **Sibling tracks:** <list or "none active">

## This session's scope — <feature>

**Plan doc (authoritative):** `<path>`
**Spec doc:** `<path>`

**One-line summary:** <one paragraph that captures what "done" looks like>

**Decisions already locked** (do not reopen):
- **D1** <decision + 1-line rationale>
- **D2** ...
- ...

**Work breakdown:** <phase list or task-group summary, 3–6 items>

**Exit gate** (per spec §<X> — must all pass):
1. <criterion>
2. ...

## Critical state notes (read before starting)

<5–12 bullets of gotchas, migration numbers, semantic ambiguities, "don't do X because Y"s>

## Worktrees

- <primary path + branch expectation>
- <branching convention: `git switch -c feat/<slug>`>
- <any per-phase branch guidance>

## Recent foundation (what <feature> builds on)

<3–5 prior PRs / merges and what each contributes to this session's starting state>

## Pacing & guardrails

- TDD / commit-discipline reminders per the plan doc
- Sub-agent verification rule
- Build + vet + test gates
- Any skill-specific guardrails (e.g., "don't scope-creep into <sibling>")

## Out of scope / deferred (don't scope-creep)

**Filed BLGs in Engine** (`project_id=<project>`):
- **BLG-XXX** — <title>
- ...

**Not BLG'd** (KB GAPs): <list with pointers to the KB>

If you hit one of these, capture a BLG immediately and keep moving.

## Backlog discipline

- Engine source of truth: `mcp__engine__engine_backlog_list project_id=<project>`
- Set `project_id` explicitly on capture
- Grep tracking dirs for BLG-IDs before session close; reconcile

## Tracking root for this execution session

`agent-workspaces/execution/<project>/<role>/<YYYY-MM-DD>/`

Two-root contract: code → work_root, tracking → tracking_root.

## Key docs

- **Plan (authoritative):** <path>
- **Spec (authoritative design):** <path>
- **Project oracle:** `agent-workspaces/knowledge/projects/<project>.md`
- <3–5 more project-specific references>

## How to boot this session

1. `cd <work_root>`
2. `git status` — confirm clean, `main` at `<sha>` or later.
3. `git switch -c feat/<slug>`
4. Read the plan doc front-to-back.
5. Read the spec's key sections for the *why*.
6. Start with Task/Phase 1.
7. <feature-specific first-task guidance>
```

4. **Write the file.** Overwrite any existing boot prompt for this feature unless the user asks otherwise.

5. **Confirm the path.** Report: `Exec boot prompt written to agent-workspaces/boot/<project>/boot-prompt-<feature>.md. Boot the execution session with this prompt.`

## What makes an exec boot prompt different from the generic `boot-prompt` skill

| Dimension | Generic `boot-prompt` | Exec boot prompt |
|-----------|----------------------|------------------|
| Length | ≤1K tokens | 2–4K tokens |
| Trigger | "save this for next time" | "create boot prompt for executor" |
| Inputs | Current conversation | Spec + plan + decisions + BLGs |
| Structure | 4 sections (Where/Current/Next/Key) | 10+ sections incl. critical state, worktrees, decisions-locked, exit gate |
| Overwrites | `.nanite/boot-prompt.md` (session-agnostic) | `boot/<project>/boot-prompt-<feature>.md` (feature-specific) |
| Decision locks | Not typical | Explicit — "do not reopen" |
| Audience | Same-project next session | Dedicated implementer agent |

## Naming conventions

- Project-agnostic session-boot: `boot/<project>/boot-prompt.md`
- Feature-specific exec boot: `boot/<project>/boot-prompt-<feature>.md`
- Feature slugs: lowercase, hyphenated, descriptive (`task-model-mvp`, `phase-3-s4b`, `executor-wiring`)
- Archives: move superseded feature boot prompts to `boot/<project>/archive/` when the feature lands

## Invariants

- **Never fire automatically.** User must explicitly ask.
- **Spec + plan must exist first.** If they don't, redirect to `superpowers:brainstorming` + `superpowers:writing-plans`.
- **Decisions-locked section is the highest-leverage part.** Extract from the planning-session conversation — these are the "don't revisit" calls that prevent the executor from relitigating already-settled questions.
- **Critical state notes catch executor-traps.** If you know something will trip the executor (stale migration numbers, nil vs empty, semantic collisions, new deps) — write it down.
- **Out-of-scope section is a fence, not a wish list.** Every deferred item should either be a BLG or a KB GAP with a pointer.
- **Two-root contract** must be stated — code → work_root, tracking → tracking_root — so the executor doesn't mix artifacts.
- **Exit gate must be copy-paste from the spec.** Don't invent new criteria in the boot prompt.
- **Vanta-first anchor is required.** Insert the blockquote between the `**Scope:**` line and the first `##` section, verbatim from this skill's template. This ensures direct-boot sessions (`Boot @path`) still honor the Vanta-primary memory contract even when nanite agent resolution is skipped.

## Adjacent skills + docs

- `boot-prompt` — session-agnostic, short, generic. Different purpose.
- `boot-remote` — for booting an agent whose config lives in a different repo. Composable: a remote-boot session may consume an exec boot prompt.
- `superpowers:writing-plans` — produces the plan this skill references.
- `superpowers:brainstorming` — produces the spec this skill references.
- `knowledge/user-guide/planner-executor-handoff.md` — pattern overview (what this skill encodes in procedure form).

## Example

See `agent-workspaces/boot/clockwork-manifold/boot-prompt-task-model-mvp.md` (2026-04-16) for a worked example produced via this skill.

Also: `agent-workspaces/boot/nanite/boot-prompt-s4b.md` and `boot-prompt-s7.md` — reference examples (not produced via this skill; pre-date it — but match the shape the skill codifies).
