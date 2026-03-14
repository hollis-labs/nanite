# Deep Review — Project/System Assessment Skill

Structured deep-dive assessment of a project, system, or feature area. Produces a status report, identifies issues, and creates actionable tasks.

## When to use

- When evaluating a project for integration, adoption, or retirement
- When assessing MVP/demo readiness
- When auditing system health before a milestone
- At the start of a planning cycle to understand current state
- When the user says "review X", "assess X", "what's the state of X"

## Arguments

- `$ARGUMENTS` — The target to review (project name, system area, feature, or "full" for portfolio-wide)

## Procedure

### Step 1: Gather (parallel sub-agents)

Launch 3-5 sub-agents depending on scope:

**Agent 1 — Code Assessment** (subagent_type: general-purpose)
- Does it build? Run build commands.
- Do tests pass? Run test suite.
- Lines of code, file count, language breakdown.
- Key dependencies and their state.
- Last commit date, recent activity.

**Agent 2 — Volon State** (subagent_type: general-purpose)
- Epics, sprints, tasks for the project_id
- Task counts by status (todo/doing/done/blocked)
- Any stale/orphaned tasks or sprints
- Active work and blockers

**Agent 3 — Documentation & Context** (subagent_type: general-purpose)
- Read README, CLAUDE.md, docs/ directory
- Check Cortex for records about this project
- Read architecture docs, ADRs
- Check PCC if it exists

**Agent 4 — Integration Points** (subagent_type: general-purpose, optional)
- How does it connect to other portfolio projects?
- MCP tools available?
- Shared modules or dependencies?
- Config references from other projects

### Step 2: Synthesize

Combine agent results into a structured report:

```
=== DEEP REVIEW — <TARGET> ===

STATUS: <one-line summary>

BUILD & TESTS:
  Build: PASS/FAIL
  Tests: X/Y pass, Z fail
  Lines: N across M files

VOLON STATE:
  Epics: N open
  Tasks: X todo / Y done / Z blocked
  Active sprint: <name or "none">

KEY FINDINGS:
  ✓ <what's working well>
  ⚠ <what needs attention>
  ✗ <what's broken>

INTEGRATION:
  <how it connects to the portfolio>

RECOMMENDATIONS:
  1. <highest priority action>
  2. <second priority>
  3. <third priority>

=== END REVIEW ===
```

### Step 3: Create tasks

For each concrete recommendation, offer to create a Volon task with proper description, acceptance criteria, and pointers. Ask the user which recommendations to act on.

## Invariants

- ALWAYS run via sub-agents for data gathering
- Read-only during assessment — no state changes
- Present findings before creating tasks (user confirms)
- Include evidence (file paths, test output, task IDs) for every finding
