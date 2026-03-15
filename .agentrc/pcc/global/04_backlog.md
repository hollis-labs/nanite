---
intent: pcc_global
project: mentat
updated_at: "2026-03-14"
---

# Backlog

## Active Epics (20 total: 4 done, 16 open)

### Priority A (open)

- **EPIC-20260314-21360**: Agent Context Architecture Implementation (ADR-024) — five-layer context: MEMORY.md directives, Cortex memory, hooks, auto-boot, boot hash
- **EPIC-20260314-65311**: Demo-Ready: Conduit End-to-End Polish — delegation flow, context continuity, Docker deployment, scripted demo
- **EPIC-20260314-64269**: Automated Quality Gates (ADR-021) — golangci-lint, govulncheck, Lefthook, Biome across 11 Go projects + 4 frontends
- **EPIC-20260314-19068**: CLI Tooling Integration — tokf filtering, gitleaks secrets detection, mise task runner, controlled execution
- **EPIC-20260314-11418**: Portfolio Tools, Skills & Automation Expansion — new MCP tools, skills, commands, workflows for gap coverage
- **EPIC-20260313-08947**: Conduit Separation (ADR-013) — extract chat app from Mentat agent identity, central agent directory

### Priority A (done)

- **EPIC-20260314-41370**: Interactive Agent UX — Structured Dialogs (9 interactive skills built)
- **EPIC-20260314-11561**: Preflight: Stabilization, Cleanup & Foundation
- **EPIC-20260314-80737**: Fragments Engine Rebrand & Portfolio Reorganization

### Priority B (open)

- **EPIC-20260314-14784**: Frag CLI & Special Agent Deep Integration (ADR-022)
- **EPIC-20260314-08295**: Unified Tool Pipeline (ADR-023) — extend ToolBroker with gating, filtering, CLI
- **EPIC-20260314-61212**: Conduit Plugin System & Agent-Native UI
- **EPIC-20260314-85672**: Agent Infrastructure & Developer Tooling
- **EPIC-20260314-48213**: Carrier Absorption (ADR-016) — app to Special Agent + shared ingest library
- **EPIC-20260314-34219**: Event-Driven Architecture — cross-project pub/sub & reactive wiring
- **EPIC-20260314-51504**: Core Library Consolidation — unify shared packages into core
- **EPIC-20260313-86433**: Agent Taxonomy & Specification System
- **EPIC-20260313-90324**: Context Capture Pipeline — active, passive, autocapture & analysis
- **EPIC-20260313-31488**: Unified Session Context — profiles, modes, scope & lens

### Priority B (done)

- **EPIC-20260314-02338**: High-ROI Portfolio Skills (merged into EPIC-11418)

## Task Counts (from Volon)

- Todo: 312 tasks
- In progress: 0 tasks
- Done: 188 tasks
- Total: ~500 tasks

## Top Priority Todo Tasks

1. Deploy hooks to all portfolio projects — shared gates, discovery, mirroring (A)
2. Create shared MEMORY.md directive template — deploy to all portfolio projects (A)
3. Strengthen deterministic gates — enforce project_id, validate MCP params (A)
4. Build memory-to-Cortex mirroring hook — auto-persist MEMORY.md writes (A)
5. Build progressive discovery hook — suggest tools when agent uses manual approach (A)
6. Replace Hadron ValidateCommand() with ToolBroker GateCommand() (A)
7. Build toolbroker CLI binary with gate and filter subcommands (A)
8. Create thin shell adapters for Claude Code PreToolUse and PostToolUse hooks (A)
9. Add Layer field and gate/filter action types to broker Rule struct (A)
10. Synthesize agent integration audit — cross-project patterns, gaps, migration plan (A)

## Roadmap Milestones

- **M1**: Context Completeness — PCC current, Cortex queryable, docs complete
- **M2**: Releasable Core — v1.0 release candidates for Volon, Hadron, Cortex
- **M3**: Release Pipeline — automated build-test-release
- **M4**: Autonomous Maintenance Loop — scheduled health, sync, remediation
- **M5**: Integration Layer — cross-project events and workflows
- **M6**: Public Launch — open-source release

## Evidence
- Last refreshed: 2026-03-14
- Sources: Volon MCP (volon_epics_list, volon_tasks_list), docs/roadmap.md
