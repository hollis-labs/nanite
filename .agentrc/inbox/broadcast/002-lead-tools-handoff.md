---
from: lead
to: tools-agent
type: handoff
priority: high
timestamp: 2026-03-14T16:00:00Z
subject: Portfolio Tools & Skills Sprint — Delegated to tools-agent
---

## Scope

EPIC-20260314-11418: Portfolio Tools, Skills & Automation Expansion
Sprint: TOOLS-S2-SKILLS-AND-COMMANDS (12 tasks, all Priority B)

Build skills (markdown), commands (markdown), and one config update that improve agent efficiency and context continuity across the Fragments Engine portfolio.

## Tasks (suggested execution order — quick wins first)

### Quick Wins (effort-small, can-parallel):
1. **TASK-20260314-151** — /discover-tools skill: query all MCP servers, categorize tools
2. **TASK-20260314-153** — /vault-search skill: search Nanite vaults from any project
3. **TASK-20260314-162** — Update tool broker rules: surface Hadron blueprints by intent (ADR-020)

### Core Skills (effort-medium):
4. **TASK-20260314-299** — session-handoff skill: package session state for next agent
5. **TASK-20260314-157** — /project-onboard skill: full new-project setup in one command
6. **TASK-20260314-304** — code-review skill: structured review with portfolio-aware checklist
7. **TASK-20260314-302** — sprint-retro skill: analyze completed sprint with insights
8. **TASK-20260314-305** — blueprint-builder skill: generate Hadron blueprints from description

### Commands (effort-medium):
9. **TASK-20260314-300** — memory-audit command: cross-ref MEMORY.md against system state
10. **TASK-20260314-301** — task-triage command: batch classify unassigned tasks
11. **TASK-20260314-303** — epic-summary command: full epic status with trajectory

### Cross-Cutting (effort-medium):
12. **TASK-20260314-306** — cross-impact skill: analyze how changes affect other projects

## Key Context

- **Skills are markdown files** in `.claude/skills/`. See existing skills for format examples.
- **Commands are also markdown files** — same directory, similar format.
- **Tool broker rules** are in `~/Projects-apps/tiamat-tool-broker/default-rules.yaml` (will move to core/broker later).
- **Existing skills** to reference: `.claude/skills/send-message.md`, `.claude/skills/check-inbox.md`, `.claude/skills/delegate.md`
- **ADR-020**: `adr/ADR-020-hadron-blueprint-skill-surfacing.md` — intent-based tool broker rules
- **ADR-021**: `adr/ADR-021-automated-quality-gates.md` — quality gate checklist for code-review skill
- **Naming conventions**: `docs/architecture/naming-conventions.md`
- **Epic/task guide**: `docs/process/epic-sprint-task-creation-guide.md`
- **MCP servers available**: Volon, Hadron, Cortex, Cerberus (all in ~/.claude.json)
- **Nanite API**: Running on localhost, check Cerberus for port

## Constraints

- Skills and commands are markdown-only — no Go code changes needed
- TASK-162 (tool broker rules) IS a code/config change — that's the exception
- Do NOT modify any Go source files beyond tiamat-tool-broker/default-rules.yaml
- Do NOT rename anything — naming cleanup is complete and handled by the lead
- For TASK-162: use `go install` not `go build` if you rebuild the tool broker

## Communication

- Your session ID: `tools-agent`
- Project lead inbox: `.agentrc/inbox/lead/`
- Use `/send-message lead info <subject> — <body>` for status updates (after every 2-3 tasks)
- Use `/send-message owner question <subject> — <body>` when you need a decision
- Check `.agentrc/inbox/tools-agent/` and `.agentrc/inbox/broadcast/` for messages

## What's Running in Parallel

- **Lead session**: Executing Iteration 3 (Universal ToolBroker TASK-165, Universal ContextBroker TASK-168) and Iteration 4 (Volon GUI fixes)
- **Conduit agent**: May still be active on remaining Demo-Ready tasks
- No file conflicts expected — you're writing new markdown files, they're modifying Go code
