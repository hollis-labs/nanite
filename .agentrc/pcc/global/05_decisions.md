---
intent: pcc_global
project: mentat
updated_at: "2026-03-13"
---

# Decisions

## Accepted ADRs

| ADR | Title | Date | Summary |
|-----|-------|------|---------|
| 001 | Tech Stack | 2026-03-06 | Go backend + React frontend, SQLite, multi-provider LLM |
| 002 | Agent Model | 2026-03-06 | Agent abstraction with skills, modes, and builders |
| 003 | Context Management | 2026-03-06 | ContextBroker with token budgeting and PCC integration |
| 004 | Backend Pattern Divergence | 2026-03-06 | Accepted that Mentat's backend diverges from other Tiamat projects (engine pattern vs. CRUD API) |
| 005 | Rename .volon to .agentrc | 2026-03-06 | Config directory renamed for project-agnostic naming |
| 006 | Single DB Writer | 2026-03-06 | One writer per SQLite database, readers use read-only connections |
| 007 | Vector Search via Cortex | 2026-03-06 | Semantic search delegated to Cortex, not embedded in Mentat |
| 008 | MCP Response Budget | 2026-03-07 | Token budget contract for MCP tool responses to prevent context overflow |
| 009 | Unified Messaging Streams | 2026-03-12 | Single messaging abstraction across task comments, chat, notifications |
| 010 | Stream Hint Reactive Capture | 2026-03-12 | Inline markers (`:carrier`, `:adr`, `:blg`) trigger passive capture pipelines |

## Key Architectural Principles

- **Tool-first**: All state writes go through services via MCP/API, never write state files directly
- **Central DB**: Volon Postgres is the source of truth for tasks/sprints across all projects
- **File cache is read-only**: `.agentrc/tasks/` are exported caches, not writable
- **Hadron first**: Attempt blueprint automation before manual work
- **Decompose before executing**: Every prompt produces child tasks before work begins

## Pending Decisions

- ADR-011: Unified Session Context (Profile/Mode/Scope/Lens) — draft
- ADR-012: Agent Specification Format — draft

## Evidence
- Last refreshed: 2026-03-13
- Sources: adr/ directory, docs/process/00_behavior-rules.md, memory
