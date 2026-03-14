---
version: 1
type: bootstrap
iteration: 3
updated_at: 2026-03-14T02:30:00Z
---

# Mentat Bootstrap

## Current state
- Iteration 3 (post-deep-planning session)
- Status: 8 epics created, full system audit complete, cleanup done
- Tasks: Check Volon with project_id="mentat" (and project_id="volon" for stabilization)
- Boot profiles: meta-agent, worker, analyst, architect, reviewer, ops-tester
- /reorient skill: BUILT AND TESTED — use it for orientation

## What happened (iteration 3 — 2026-03-13/14)
- Oriented from zero context, built and tested /reorient skill
- Created 8 epics, 22 sprints, 101 tasks across mentat + volon
- Designed: Conduit separation (ADR-013), plugin system, central filesystem (ADR-015)
- Designed: session context (Profile/Mode/Scope/Lens), agent taxonomy, capture pipeline
- Full system audit: 14 projects, 1396 tasks, 201 sprints — identified critical Volon issues
- Cleanup: 117 worktrees (22GB), 326 branches, 22 test artifacts, agentrc sync, PCC creation
- Key decision: chat app = Conduit (harness), Mentat = Special Agent (cognitive assistant)

## Active epics (priority order)
1. EPIC-20260314-56630: Volon Stabilization (claim race, E2E, agent sessions) — VOLON
2. EPIC-20260314-65311: Demo-Ready Conduit (Docker, delegation, Cortex, compaction)
3. EPIC-20260313-08947: Conduit Separation (rename, central agentrc) — via MMA agent
4. EPIC-20260313-31488: Unified Session Context (Profile/Mode/Scope/Lens)
5. EPIC-20260313-86433: Agent Taxonomy & Specification
6. EPIC-20260313-90324: Context Capture Pipeline (Active + Passive)
7. EPIC-20260314-61212: Plugin System & Agent-Native UI (includes A2A messaging)

## Next steps
- Execute Conduit rename via MMA-style parallel agent process
- Activate Volon stabilization sprint (VSTAB-S1-CRITICAL-FIXES)
- Continue planning sessions for remaining topics
- Begin Phase 1 implementation
