# Project Tiamat — Roadmap

> Ported from mentat CLI. Last major update: 2026-03-06.

## Vision

Project Tiamat is a full-stack agent framework where each component has a distinct role. The end state is a self-managing development platform: Mentat orchestrates, Volon tracks, Hadron automates, Cortex remembers, Nanite captures, and Carrier processes — all connected via MCP and OTel.

## Milestones

### M1: Context Completeness
**Goal:** Mentat owns all context — PCC is current, Cortex is queryable, project docs are complete.

### M2: Releasable Core
**Goal:** Volon, Hadron, and Cortex each have a v1.0 release candidate with binary artifacts.

### M3: Release Pipeline
**Goal:** Automated build-test-release pipeline across all projects.

### M4: Autonomous Maintenance Loop
**Goal:** Mentat runs autonomously on schedule — health checks, PCC sync, drift detection, auto-remediation.

### M5: Integration Layer
**Goal:** Projects communicate seamlessly — events, shared state, cross-project workflows.

### M6: Public Launch
**Goal:** Open-source release with documentation, examples, and getting-started guides.

## Principles

1. **Local-first**: Everything works on a single machine, no cloud dependency
2. **MIT licensed**: Free for personal and commercial use
3. **Composable**: Each project is useful standalone, better together
4. **Observable**: OTel instrumentation across all projects
5. **Agent-native**: Built for AI agent consumption (MCP, structured context, deterministic state)
6. **Self-managing**: Tiamat should increasingly manage itself via Mentat
