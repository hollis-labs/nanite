---
version: 1
type: bootstrap
iteration: 6
updated_at: 2026-03-14T23:45:00Z
---

# Mentat Bootstrap

## Current state
- Iteration 6 (Interactive Agent UX complete)
- Status: 9 interactive dialog skills built, 8 slash commands registered, pattern + anatomy docs written, EPIC-41370 done
- A2A messaging system operational — tested with 5 agents, zero conflicts
- Tasks: Check Volon with project_id="mentat" (and project_id="volon")
- Boot profiles updated with: inbox checking, task completion checklist, tag enforcement

## What happened (iteration 5 — 2026-03-14)
### Planning & Architecture
- 6 ADRs written (016-021): Carrier absorption, Nanite RAG, Sigil classification, Volon chat convergence, Hadron blueprint surfacing, quality gates
- 7 new epics created, portfolio taxonomy established
- Full portfolio cleanup: 187 backlog deleted, 62 promoted, 425 tasks enriched

### Iteration 1: Lock the Gates
- golangci-lint + lefthook on all 11 Go projects
- Biome + @tsconfig/strictest on all 4 frontends
- Task flow enforcement: sprint pre-close validation, post-task auto-close cascade, TryAutoCloseEpic
- Cerberus go install fix + MCP binary path corrections

### Iteration 2: Name Things Right
- Cross-project naming cleanup: Hadron Task→Step, Mentat Broker→Client, Cerberus Service→ManagedService, Cortex Broker→Planner, Volon Service→CompletionService
- Scheduler autopick bug fixed (flat YAML key)
- Naming conventions documented

### Iteration 3: Architecture (parallel agents)
- Universal ToolBroker (TASK-165) — 2 consumers, integration guide
- Universal ContextBroker (TASK-168) — 8 intents, 4 source adapters, wired into engine
- Demo-Ready Conduit (EPIC-65311) — 5/5 tasks by conduit-agent

### Iteration 4: Volon UX + Tools
- 8 GUI bug fixes + mark complete/archive workflow
- Task completion workflow + tag taxonomy documented
- 21 skills/workflows/blueprints by tools-agent (EPIC-11418 S2+S3)

### Agent Context Architecture (S1)
- Auto-boot imperative added
- MEMORY.md redesigned (200→46 lines of directives)
- Boot hash generation (FRAG_BOOT_HASH)
- Volon executor MEMORY_DIR env var
- Session-inject.sh slimmed (100→8 lines, added FRAG_ROLE env)

### Iteration 6: Interactive Agent UX (EPIC-41370 — DONE)
- Interactive dialog pattern doc + skill/command anatomy doc
- 9 skills upgraded with AskUserQuestion dialogs: sprint-review, task-triage, drift-resolve, code-review, epic-plan, session-handoff, memory-audit, git-cleanup, sprint-retro
- 8 new slash commands registered (session-handoff is agent-only)
- Both sprints closed (S1: 5/5, S2: 5/5), epic done

## Active epics (priority order)
1. EPIC-20260314-21360: Agent Context Architecture (S1 done, S2+S3 remain — GATED)
2. EPIC-20260314-64269: Quality Gates (S1 4/5 done, S2+S3 remain)
3. EPIC-20260314-11418: Portfolio Tools (S1 MCP tools remain, S2+S3 done)
4. EPIC-20260314-83904: Volon GUI Polish (8 done, TASK-105 Triage UI deferred)
5. EPIC-20260313-08947: Conduit Separation
6. EPIC-20260314-51504: Core Library Consolidation
7. EPIC-20260314-14784: Frag CLI & Agent Integration (B priority)

## Completed epics
- EPIC-20260314-41370: Interactive Agent UX — Structured Dialogs (10/10 tasks, 2026-03-14)

## Next steps
- Execute EPIC-21360 S2 (hooks & enforcement) when ready
- S3 cross-project rollout GATED — requires lead+owner review session
- Remaining: TASK-174 (go-playground/validator), EPIC-11418 S1 (MCP tools)
- Consider: Conduit Separation or Core Consolidation for next major iteration
