# ADR-016: Carrier Absorption — Standalone App to Special Agent + Shared Library

**Status:** Accepted
**Date:** 2026-03-13
**Deciders:** chrispian, Mentat
**Relates to:** ADR-013 (Conduit Separation), ADR-010 (Stream Hint Reactive Capture)

## Context

Carrier is a Python 3.9+ content operations pipeline with 8 ingestion sources, blueprint-driven generation, opportunity extraction, a full REST API, React UI, and 260 tests. It is the only Python project in an otherwise Go-based portfolio.

During strategic review, we identified that Carrier's value decomposes into three distinct layers:

1. **Ingestion infrastructure** — Multi-source artifact aggregation (git, Claude logs, ChatGPT, Nanite vaults, RSS, media, etc.), deduplication, sessionization, redaction. This is reusable plumbing.
2. **Editorial intelligence** — Blueprint selection, lens application, voice profiles, opportunity extraction, content lifecycle management. This is agent behavior.
3. **UI surfaces** — Blueprint management, opportunity inbox, output review. These are views, not an app.

Carrier has no standalone value without the rest of the ecosystem — it needs Nanite for vaults, Claude for generation, and Hadron for scheduling. It was born integrated.

## Decision

Absorb Carrier into Fragments Engine as three components:

### 1. Shared Ingest Library (`fragments-ingest` or similar)
- Extract ingestion sources as a reusable package (Go or Python, TBD)
- Artifact normalization, deduplication, sessionization
- Redaction pipeline as a shared security utility
- Cron evaluator (pure Python, no deps — port to Go or keep as utility)
- Any agent or workflow can use these, not just Carrier

### 2. Carrier Special Agent
- The editorial intelligence becomes a Special Agent with:
  - Boot profile defining its persona, domain, and skills
  - Skills for blueprint execution, opportunity extraction, lens application
  - Access to shared ingest library for artifact retrieval
  - Runs inside Conduit (GUI) or via CLI (agentrc boot profile)
- Keeps the "Carrier" brand as the agent name
- Passive capture (`:carrier` stream hints, ADR-010) remains unchanged

### 3. Conduit Plugins
- Blueprint management → Conduit plugin
- Opportunity inbox → Conduit plugin
- Output review → Conduit plugin
- These replace the standalone React UI

### Scheduling
- Carrier already uses Hadron blueprints for scheduling. Lean fully into Hadron as the scheduler.
- No separate launchd agents or Python cron evaluator needed.

## Consequences

### Positive
- One fewer app to build, test, deploy, and maintain
- Eliminates the Python/Go split in the portfolio
- Ingestion sources become available to any agent, not just Carrier
- Conduit becomes the single UI entry point
- Carrier's 260 tests become integration tests for the ingest library
- Simpler mental model: 6 projects → 5 projects + 1 shared lib

### Negative
- Migration effort to extract and rewrite/port ingestion sources
- Loss of standalone Carrier app (minor — it has no standalone value)
- Python expertise required during migration, then can sunset Python dependency

### Risks
- Ingest library scope creep — keep it focused on artifact normalization, not content generation
- Agent behavior may be harder to test outside of a Conduit session — ensure CLI-mode testing works

## Migration Path

1. **Phase 1**: Extract shared ingest library (artifact types, source interfaces, dedup, sessionization)
2. **Phase 2**: Create Carrier Special Agent boot profile and skills
3. **Phase 3**: Build Conduit plugins for blueprint/opportunity/output management
4. **Phase 4**: Retire standalone Carrier app, redirect to Fragments Engine
