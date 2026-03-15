---
intent: pcc_global
project: mentat
updated_at: "2026-03-14"
---

# Decisions

## Accepted ADRs (21)

| ADR | Title | Date | Summary |
|-----|-------|------|---------|
| 001 | Tech Stack | 2026-03-06 | Go backend + React frontend, SQLite, multi-provider LLM |
| 002 | Agent Model | 2026-03-06 | Agent abstraction with skills, modes, and builders |
| 003 | Context Management | 2026-03-06 | ContextBroker with token budgeting and PCC integration |
| 004 | Backend Pattern Divergence | 2026-03-06 | Mentat's backend diverges from other projects (engine pattern vs. CRUD API) |
| 005 | Rename .volon to .agentrc | 2026-03-06 | Config directory renamed for project-agnostic naming |
| 006 | Single DB Writer | 2026-03-06 | One writer per SQLite database, readers use read-only connections |
| 007 | Vector Search via Cortex | 2026-03-06 | Semantic search delegated to Cortex, not embedded in Mentat |
| 008 | MCP Response Budget | 2026-03-07 | Token budget contract for MCP tool responses to prevent context overflow |
| 009 | Unified Messaging Streams | 2026-03-12 | Single messaging abstraction across task comments, chat, notifications |
| 010 | Stream Hint Reactive Capture | 2026-03-12 | Inline markers (:carrier, :adr, :blg) trigger passive capture pipelines |
| 013 | Conduit Separation | 2026-03-13 | Separate chat app (Conduit) from Mentat agent identity (ADR-013) |
| 015 | Central Agent Filesystem | 2026-03-14 | Centralized agent filesystem to reduce docs scatter across repos |
| 016 | Carrier Absorption | 2026-03-13 | Standalone Carrier app becomes Special Agent + shared ingest library |
| 017 | Nanite RAG Vault | 2026-03-13 | Multi-vault knowledge retrieval with system agents |
| 018 | Sigil Core Infrastructure | 2026-03-13 | Sigil as shared tool infrastructure, not standalone product |
| 019 | Volon GUI Chat Convergence | 2026-03-14 | Deprecate Volon's GUI chat, converge on Conduit |
| 020 | Hadron Blueprint Skill Surfacing | 2026-03-14 | Surface Hadron blueprints as conversational skills via tool broker rules |
| 021 | Automated Quality Gates | 2026-03-14 | Linting, testing, code review tooling across portfolio (Lefthook, golangci-lint, Biome) |
| 022 | Special Agent Integration Architecture | 2026-03-14 | Dual-mode binary pattern: native function calls for own-service, MCP for cross-service |
| 023 | Unified Tool Pipeline | 2026-03-14 | Extend ToolBroker with selection, gating, and filtering layers |
| 024 | Agent Context Architecture | 2026-03-14 | Five-layer context: MEMORY.md directives, Cortex memory, hooks, auto-boot, boot hash |

Note: ADR-011, ADR-012, ADR-014 numbers are unused/skipped.

## Key Architectural Principles

- **Tool-first**: All state writes go through services via MCP/API, never write state files directly
- **Central DB**: Volon Postgres is the source of truth for tasks/sprints across all projects
- **File cache is read-only**: `.agentrc/tasks/` are exported caches, not writable
- **Hadron first**: Attempt blueprint automation before manual work
- **Decompose before executing**: Every prompt produces child tasks before work begins
- **Conduit is the harness, Mentat is the agent**: Chat app (Conduit) is agent-agnostic; Mentat is a Special Agent profile that runs inside it

## Evidence
- Last refreshed: 2026-03-14
- Sources: adr/ directory (21 files), docs/process/00_behavior-rules.md
