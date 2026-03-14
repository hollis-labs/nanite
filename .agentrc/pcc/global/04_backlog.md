---
intent: pcc_global
project: mentat
updated_at: "2026-03-13"
---

# Backlog

## Active Epics

### EPIC-20260313-31488: Unified Session Context
Profile, Mode, Scope, and Lens system for agent sessions. 4 sprints planned, 21 tasks. Establishes how agents configure their operating context per session.

### EPIC-20260313-86433: Agent Taxonomy & Specification
Formal classification of agent types (Special Agents, System Agents, Primary/Secondary). 2 sprints, 7 tasks. Defines agent capabilities, responsibilities, and interaction contracts.

### EPIC-20260313-90324: Context Capture Pipeline
Active capture (Mentat + user during sessions) and passive capture (Carrier post-processes transcripts). 3 sprints, 12 tasks. Stream hint markers (`:carrier`, `:adr`, `:blg`, etc.) per ADR-010.

## Active Sprints

- **MNT-CONTEXT-GATES**: Context gating and session setup
- **MNT-CHAT-UI-POLISH**: Frontend refinements and UX improvements

## Roadmap Milestones

- **M1**: Context Completeness — PCC current, Cortex queryable, docs complete
- **M2**: Releasable Core — v1.0 release candidates for Volon, Hadron, Cortex
- **M3**: Release Pipeline — automated build-test-release
- **M4**: Autonomous Maintenance Loop — scheduled health, sync, remediation
- **M5**: Integration Layer — cross-project events and workflows
- **M6**: Public Launch — open-source release

## Approximate Task Counts

- Todo/backlog: ~125 tasks
- Completed: ~130 tasks

## Evidence
- Last refreshed: 2026-03-13
- Sources: Volon MCP queries, docs/roadmap.md, memory
