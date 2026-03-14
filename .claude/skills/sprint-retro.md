# sprint-retro

Interactive sprint retrospective — analyze sprint velocity and patterns, present keep/drop/try items for voting, execute adopted items as tasks, and persist retro to Cortex.

## Usage

`/sprint-retro <sprint_id> [--project <project_id>]`

**sprint_id**: The Volon sprint ID to analyze
**--project**: Project ID (default: from agentrc.yaml)

## Instructions

### 1. Gather and analyze sprint data

- Use `volon_sprint_get` with the sprint ID
- Use `volon_tasks_list` filtered by sprint_id to get all tasks
- For each task, note: status, priority, title, created_at, updated_at, tags

**Calculate velocity metrics:**
- Total tasks, completed, open, blocked, paused
- Completion rate: `done / total * 100`
- By priority: A/B/C distribution and completion rates
- By effort: small/medium/large distribution if tags available

**Analyze patterns:**
- Order of completion (quick wins first?)
- Blocked duration (how long were blocked tasks stuck?)
- Scope creep (tasks added after sprint start)

**Cross-reference git:**
```bash
git log --oneline --since="<sprint_start>" --format="%h %s" 2>/dev/null
```

### 2. Show retro summary header

```
=== SPRINT RETROSPECTIVE ===
Sprint: <sprint_id>
Project: <project_id>

VELOCITY:
  Total: <N> | Done: <N> (<percent>%) | Open: <N> | Blocked: <N>
  By priority: A=<done>/<total> B=<done>/<total> C=<done>/<total>

SCOPE:
  Original: <N> tasks | Added mid-sprint: <N> | Change: <+/- N>
```

### 3. Present keep/drop/try — interactive dialog

Based on the analysis, generate insights and present them as votable items.

**Question 1: Keep items** (multiSelect) — what worked well
- **header**: "Keep"
- **question**: `Which practices should we keep doing?`
- **options** (up to 4, generated from analysis):
  - e.g., "Quick wins first — small tasks completed early, kept momentum"
  - e.g., "Parallel agents — 3 agents ran without conflicts"
  - e.g., "ADR-before-code — decisions documented before implementation"
- **multiSelect**: true

**Question 2: Drop items** (multiSelect) — what didn't work
- **header**: "Drop"
- **question**: `Which practices should we stop or change?`
- **options** (up to 4, generated from analysis):
  - e.g., "Scope creep — 5 tasks added mid-sprint, diluted focus"
  - e.g., "Missing acceptance criteria — 3 tasks had no clear done state"
- **multiSelect**: true

**Question 3: Try items** (multiSelect) — experiments for next sprint
- **header**: "Try"
- **question**: `Which experiments should we try next sprint?`
- **options** (up to 4, generated from patterns):
  - e.g., "Sprint size cap — limit to 10 tasks max"
  - e.g., "Daily triage — review unassigned tasks each session"
  - e.g., "Blocker SLA — escalate blocked tasks after 24h"
- **multiSelect**: true

### 4. Execute adopted items

For confirmed **Try** items:
- Create a Volon task for each in the next sprint (or backlog if no next sprint exists)
- Tag with `retro`, `experiment`

For confirmed **Keep** items:
- Note in the retro record (no action needed beyond documentation)

For confirmed **Drop** items:
- Note in the retro record
- If a drop item maps to a process change, create a task for it

### 5. Persist retro to Cortex

Use `context_write` to store the retro record:
- Namespace: "retros"
- Key: sprint ID
- Content: full retro summary with velocity, keep/drop/try selections, and action items

### 6. Show results summary

```
=== RETRO COMPLETE ===
Sprint: <sprint_id>

KEEP (confirmed):
  - <item>

DROP (confirmed):
  - <item>

TRY (adopted — tasks created):
  - TASK-<id>: <experiment description>

VELOCITY SNAPSHOT:
  Completion: <percent>%
  Scope change: <+/- N>

Retro persisted to Cortex: retros/<sprint_id>
Next: Run /sprint-review to handle remaining tasks, or /epic-plan to plan ahead.
=== END RETRO ===
```

## When to Use

- When a sprint is completed or nearly completed
- During planning for the next sprint (review the last one first)
- When the user asks for a sprint summary or retrospective
- At the end of an iteration

## Invariants

- Always show velocity metrics before interactive voting
- Generate insights from data — don't present empty categories
- Adopted "Try" items must become real Volon tasks
- Always persist to Cortex for historical tracking
- If sprint has no completed tasks, skip velocity analysis and note it

$ARGUMENTS
