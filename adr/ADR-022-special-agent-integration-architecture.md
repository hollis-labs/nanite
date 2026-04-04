# ADR-022: Special Agent Integration Architecture

**Status**: Accepted
**Date**: 2026-03-14

---

## Context

Fragments Engine has five core services (Nanite, Cortex, Volon, Hadron, Beacon), each with an associated Special Agent. As the agent tier matures, communication patterns between agents and services have accumulated inconsistently — some agents use MCP, some call HTTP APIs directly, and some shell out to CLI tools. This creates:

- Performance overhead from unnecessary MCP round-trips when an agent operates on its own service
- No clear protocol boundary for cross-service communication
- No canonical shell interface for humans, scripts, Hadron pipeline stages, and git hooks
- Risk of LLM hallucination causing invalid state changes without a validation layer

The dual-mode binary pattern (single binary serving both CLI and MCP) is already proven in Volon and Hadron. This ADR codifies the three-layer model that extends this pattern across the full portfolio.

---

## Decision

Adopt a **three-layer integration model** for all Special Agents. Each layer has a distinct consumer and protocol.

### Layer 1 — Native Tools (agent's own service)

- Direct Go function calls into the service library (store layer, service layer)
- Injected into the agent as typed tool definitions with rich descriptions and examples in the agent prompt
- **Deterministic gates** (pure Go code) enforce input validation, permission checks, and rate limits before any state change
- Zero MCP round-trip overhead for the agent's primary domain
- Tight coupling between agent binary and service internals is intentional — they ship together

**Example**: The Cortex Special Agent calls `store.Write()` directly when indexing context records. It does not route through the Cortex MCP server.

### Layer 2 — MCP Bridge (cross-service)

- MCP stdio protocol for all agent-to-agent and agent-to-foreign-service communication
- Enforces a clean service boundary — agents never import another service's internal packages
- Enables independent deployment and versioning of each service
- MCP tool schemas serve as the cross-service API contract

**Example**: The Cortex Agent calls `volon_task_get` via MCP when it needs task context to enrich a memory record. It does not import Volon's store package.

### Layer 3 — CLI (humans, scripts, pipelines)

- Universal `frag` CLI binary with per-service subcommands: `frag task`, `frag ctx`, `frag health`, `frag bp`
- Single binary that imports all service libraries directly — a peer to HTTP and MCP, not a wrapper around either
- **Agent-native contract**: `--json` output flag, idempotent operations, structured stderr/stdout separation
- Usable from interactive shell, mise task runners, Hadron pipeline stages, git hooks, and cron jobs
- Agents MAY use CLI as a fallback but MUST prefer native tools when operating in their own domain

---

## Architecture

```
                    ┌─────────────┐
                    │  Go Service  │
                    │   Library    │
                    └──────┬──────┘
              ┌────────────┼────────────┐
              │            │            │
          ┌───▼──┐    ┌────▼───┐   ┌───▼────┐
          │ HTTP │    │  MCP   │   │  CLI   │
          │ API  │    │ stdio  │   │ (frag) │
          └──────┘    └────────┘   └────────┘
              ▲            ▲            ▲
              │            │            │
          GUI/Web    Special Agents  Humans/Scripts
                    (own service)   Pipelines/Hooks
                    Other agents
                    (cross-service)
```

---

## Decision Matrix

| Context | Use |
|---------|-----|
| Agent operating on its own service | Native function calls + deterministic gates |
| Agent calling another service | MCP |
| Human operating any service | `frag` CLI subcommands |
| Scripts / pipelines / git hooks | CLI with `--json` |
| Agent needing shell output or fallback | CLI (fallback only, not primary) |

---

## Consequences

**Positive**

- Native path eliminates MCP round-trip latency for the agent's primary domain
- Deterministic gates provide a hard validation layer that LLM output cannot bypass
- `frag` CLI becomes the single portfolio entry point for all non-agent consumers — one tool to learn
- Clean service boundaries via MCP prevent accidental cross-service coupling at the code level
- Dual-mode binary pattern already proven; this ADR standardizes it across the portfolio

**Negative / Trade-offs**

- Each Special Agent binary has tight coupling to its own service's internal packages — changes to service internals require rebuilding the agent
- Cross-service changes still require MCP contract negotiation between service owners
- New services must expose all three surfaces (HTTP API, MCP stdio, CLI subcommands) from day one — higher initial implementation cost
- The `frag` CLI must be kept in sync as services evolve; drift is a maintenance risk

---

## References

- ADR-013: Nanite Separation — establishes service boundary between Nanite and Mentat Agent
- ADR-019: Volon GUI Chat Convergence — dual-mode binary pattern applied to Volon
- Dual-mode binary pattern: `cmd/volon` and `cmd/gui-server` in the Volon repo
- Agent taxonomy in memory: Special Agents vs System Agents, primary/secondary classification
- `tiamat-tool-broker`: shared library for tool intent analysis used by Nanite
