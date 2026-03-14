---
version: 1
type: bootstrap
iteration: 4
updated_at: 2026-03-14T02:35:00Z
---

# Mentat Bootstrap

## Current state
- Iteration 4 (post-preflight execution)
- Status: Preflight epic COMPLETE (27 tasks, 3 phases, 8 repos). Platform stabilized.
- Tasks: Check Volon with project_id="mentat" (and project_id="volon" for stabilization)
- Boot profiles: meta-agent, worker, analyst, architect, reviewer, ops-tester
- /reorient skill: use for orientation. /git-cleanup skill: use for worktree maintenance.
- Cortex MCP: WORKING. contextd binary fixed, seeded with 12 records.
- Module renames: tiamat-otel→otel, tiamat-mcp-helpers→mcp-helpers, tiamat-tool-broker→tool-broker

## What happened (iteration 4 — 2026-03-14)
- Executed Preflight epic (EPIC-20260314-11561) across 3 phases
- Phase 1: Scheduler races fixed (FOR UPDATE SKIP LOCKED), worktree lifecycle hardened, agent sessions wired, conventions documented
- Phase 2: DB reconnection backoff, E2E test infrastructure rebuilt, schema audited (7 indexes), Cortex MCP fixed and seeded, NO EMOJI output filters, control surface centralized
- Phase 3: Code format standardized (short codes in GUI), Fragments Engine rebrand (7 repos), Cerberus hardened, 4 GUI pages fixed (Postgres boolean scan), CoW worktree creation, E2E safety infrastructure (TestMain, orphan sweep, pre-built binaries, process group kills)
- Created 8 new tasks: Volon hooks, epic-to-Cortex auto-sync, drift detection, Hadron blueprints, CoW worktrees, Cerberus hardening, E2E validation
- Cleaned 36 orphan E2E test databases
- DB backup at /tmp/volon-db-backup-preflight-20260314-022332.sql

## Active epics (priority order)
1. EPIC-20260314-65311: Demo-Ready Conduit (Docker, delegation, Cortex, compaction)
2. EPIC-20260313-08947: Conduit Separation (rename, central agentrc)
3. EPIC-20260314-80737: Fragments Engine Rebrand & Portfolio Reorganization (folder reorg remaining)
4. EPIC-20260313-31488: Unified Session Context (Profile/Mode/Scope/Lens)
5. EPIC-20260313-86433: Agent Taxonomy & Specification
6. EPIC-20260313-90324: Context Capture Pipeline (Active + Passive)
7. EPIC-20260314-61212: Plugin System & Agent-Native UI

## Follow-ups from Preflight
- Scheduler autopick bug — ClaimNextTodo query returns QUEUE_EMPTY in E2E
- ~/bin/frag launcher — needs creation (~/bin/tiamat is broken symlink)
- Folder reorg (TASK-080) — deferred post-preflight
- Schema: duplicate migration 0043, tags redesign, missing Postgres task_artifacts table

## Next steps
- Pick next epic (Demo-Ready Conduit or Conduit Separation recommended)
- Investigate scheduler autopick bug before next E2E run
- Create ~/bin/frag launcher script
