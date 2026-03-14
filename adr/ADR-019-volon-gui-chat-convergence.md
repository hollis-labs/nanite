# ADR-019: Volon GUI Chat Deprecation — Converge on Conduit

**Status:** Accepted
**Date:** 2026-03-14
**Deciders:** chrispian, Mentat
**Relates to:** ADR-013 (Conduit Separation), ADR-016 (Carrier Absorption)

## Context

During a portfolio-wide sweep, we identified that two chat systems are being built in parallel:

1. **Volon GUI Server** — 67 HTTP endpoints including multi-provider chat (Anthropic, OpenAI, OpenRouter, OpenZen), SSE streaming, agent profiles, prompt templates, voice profiles, workflows, and scheduler control.

2. **Conduit (Mentat)** — Multi-provider chat (Anthropic, OpenAI, Ollama), SSE streaming, MCP tool integration, progressive tool discovery, context brokering, and workflow engine.

Both implement: provider abstractions with retry/circuit-breaker, streaming SSE, agent profiles with modes, prompt template composition, and workflow execution. This is a significant duplication of effort.

ADR-013 established that Conduit is the chat harness. But Volon GUI's chat surface continues to grow.

## Decision

### Volon GUI becomes OPS-only

Volon GUI Server retains:
- Task/sprint/backlog/epic CRUD and management
- Scheduler control (start/stop/tick)
- Activity monitoring and event streaming
- Agent session tracking
- Administrative operations (audit, prune, migrate)

Volon GUI Server sheds:
- Multi-provider chat (LLM calls) → Conduit
- Prompt template composition → Conduit (or shared lib)
- Voice profiles → Conduit (or shared lib)
- Workflow execution → Conduit

### Extract shared capabilities

Before removing from Volon, extract to `fe-core` or standalone packages:
- **Provider abstraction** (Anthropic, OpenAI + circuit breaker, retry, rate tracking) → `fe-core/providers`
- **Voice profiles** (schema, validation, exemplars, rules) → `fe-core/voiceprofile` or Conduit internal
- **Prompt template composition** → Conduit internal (or shared if Hadron needs it)

### Volon GUI embeds Conduit (optional future)

If Volon GUI needs chat capabilities for OPS workflows, it can embed Conduit as an iframe/component rather than maintaining its own chat engine.

## Consequences

### Positive
- Single chat engine to maintain and improve
- Voice profiles, provider resilience, and prompt templates benefit from Conduit's richer context (MCP, tool broker)
- Volon GUI becomes focused and lean (OPS control plane)
- Clear ownership: Conduit = conversation, Volon = orchestration

### Negative
- Migration effort to extract providers and voice profiles
- Volon GUI loses self-contained demo capability (needs Conduit running for chat)
- Users currently using Volon GUI chat need to switch to Conduit

### Risks
- Provider abstraction may have Volon-specific assumptions that don't generalize cleanly
- Voice profiles may be tightly coupled to Volon's DB schema

## Migration Path

1. **Phase 1**: Extract provider abstraction to fe-core (both consume it)
2. **Phase 2**: Migrate voice profiles to Conduit (or fe-core)
3. **Phase 3**: Remove chat endpoints from Volon GUI
4. **Phase 4**: Optional — embed Conduit in Volon GUI for OPS chat needs
