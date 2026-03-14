# Planning Session — Structured Strategic Work Session

Run a structured planning session with the user. This encodes the process demonstrated on 2026-03-13/14 that produced 8 epics and 101 tasks in a single session.

## When to use

- At the start of a planning day/session
- When the user says "let's plan", "planning session", "let's review and plan"
- When multiple topics need to be discussed, decided, and turned into work items

## Procedure

### Step 1: Orient (/reorient)

Run /reorient first. This establishes current state: services, tasks, epics, recent activity, drift.

### Step 2: Topic Loop

For each topic the user brings up:

1. **Research** — Launch sub-agents to gather relevant context (existing tasks, code, docs, Cortex records). Don't discuss until you have data.

2. **Analyze** — Present findings. Identify gaps, opportunities, issues. Give your recommendation.

3. **Align** — Discuss with user. Ask clarifying questions. Reach agreement on approach.

4. **Decide** — If an architectural decision: write ADR. If a naming/direction change: update memory + Cortex.

5. **Plan** — Create epic, sprints, and tasks in Volon. Include:
   - Rich descriptions with USE CASES
   - Acceptance criteria
   - File pointers
   - Proper priority and tags

6. **Persist** — Update: Cortex (decision record), Memory (if durable), bootstrap (if state changed). Push Nanite note if content-worthy.

### Step 3: Between Topics

At natural breaks:
- Check: did we make any decisions worth an ADR?
- Check: is memory still current?
- Check: should we capture anything for Carrier? (`:carrier` markers)

### Step 4: Wrap

At session end:
- Update Cortex session record with full output
- Update bootstrap with current state
- Update memory index if new sections added
- Commit any local file changes if requested

## Patterns to Recognize

These patterns emerge repeatedly in planning sessions. Use the right skill for each:

| Pattern | Skill/Action |
|---------|-------------|
| "Review project X" | /deep-review X |
| "What's our status?" | /reorient or /qstatus |
| "Generate a UI for X" | /sigil-ui X |
| Quick backlog capture | /blg "description" |
| Architectural decision | /adr "description" |
| Service health check | /qhealth |
| Task cleanup needed | Batch close via volon_task_transition |
| New feature area | Create epic → sprints → tasks |
| Cross-project concern | Check all projects, create tasks in appropriate project_ids |

## Invariants

- Always research before recommending
- Always align before creating tasks
- Always persist decisions (ADR + Cortex + Memory)
- Use sub-agents for data gathering to keep main context clean
- Create tasks with enough context that an agent can execute without asking questions
