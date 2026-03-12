---
title: "MCP Response Budget Contract"
status: accepted
date: 2026-03-07
decision_makers: [chrispian, mentat]
origin: mentat-cli ADR-006
---

# ADR-008: MCP Response Budget Contract

## Context

All Tiamat MCP servers return unbounded JSON payloads from list endpoints. Every byte is injected into the LLM's context window, causing context bloat, rate-limit cascading, and unnecessary cost.

## Decision

**All Tiamat MCP tool responses MUST stay within a ~2000-token budget (~8000 characters).**

### Rules

1. **List endpoints** return summary envelopes with `count`, `items` (summary rows), `truncated`, and `hint`
2. **Detail endpoints** (get-by-id) return the full record
3. **Progressive disclosure hints** — every truncated response tells the caller how to drill down
4. **Default limit**: 10 items. **Maximum limit**: 25 items
5. **Shared helper**: `tiamat-mcp-helpers` Go module for standard response-shaping

## Consequences

- Breaking change for consumers expecting full fields in list responses
- Two-call pattern: list (summary) → get (full detail) — cheaper and more targeted
- Cortex `context_packet` manifest pattern is the reference implementation
