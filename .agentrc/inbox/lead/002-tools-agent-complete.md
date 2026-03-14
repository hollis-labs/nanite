---
from: tools-agent
to: lead
type: info
priority: medium
timestamp: 2026-03-14T16:45:00Z
subject: TOOLS-S2 COMPLETE — 12/12 tasks done
---

Sprint TOOLS-S2-SKILLS-AND-COMMANDS is fully complete. All 12 tasks done in Volon.

## Deliverables

### Skills (10 new files in .claude/skills/):
1. **discover-tools** (TASK-151) — Query all MCP servers, categorize tools by domain
2. **vault-search** (TASK-153) — Search Nanite vaults from any project
3. **session-handoff** (TASK-299) — Package session state for next agent
4. **project-onboard** (TASK-157) — Full new-project setup (Volon + PCC + agentrc + Cortex)
5. **code-review** (TASK-304) — Structured review with ADR-021 quality gate checklist
6. **sprint-retro** (TASK-302) — Sprint analysis with velocity and insights
7. **blueprint-builder** (TASK-305) — Generate Hadron blueprints from description
8. **task-triage** (TASK-301) — Batch classify and prioritize unassigned tasks
9. **epic-summary** (TASK-303) — Full epic status with trajectory projection
10. **cross-impact** (TASK-306) — Analyze how changes affect other projects

### Config (1 file changed):
11. **Tool broker rules** (TASK-162) — Replaced blanket hadron_bp_* exclusion with 12 intent-based inclusion rules per ADR-020. Categories: test/lint, build, drift, backup, audit, release, docker, health, reports, cleanup, search, game.

### File: ~/Projects-apps/tiamat-tool-broker/broker/default-rules.yaml

## Notes
- All skills follow the established format (matching send-message, check-inbox, delegate patterns)
- Code-review skill integrates the full ADR-021 checklist (Go, TS/React, portfolio-aware, security)
- Tool broker rules use priority 15 to override the priority 10 blanket exclusion
- SUDS game blueprints only surface on explicit "play_game" intent (per ADR-020)
- No Go source files modified — all markdown + 1 YAML config as scoped

## Uncommitted changes
- 10 new .claude/skills/*.md files in mentat repo
- 1 modified default-rules.yaml in tiamat-tool-broker repo
- Ready for commit when you're ready
