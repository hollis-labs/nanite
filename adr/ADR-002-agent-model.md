# ADR-002: Agent Model — Cognitive Agent with Delegation

**Date:** 2026-03-07
**Status:** Accepted
**Decision Makers:** Chrispian, Mentat

## Context

Mentat Chat needs a clear model for how agents operate. The primary agent (Mentat) should be a cognitive partner — planning, managing context, delegating — not an executor. Other agents (workers) should handle tool calls and direct work.

## Decision

### Agent Identity
- An agent has: name, slug, avatar, system prompt, modes, MCP bindings, tool permissions, `can_execute` flag.
- Mentat's `can_execute` is FALSE. Worker agents' `can_execute` is TRUE.

### Modes (not agent switching)
- Modes modify behavior within the same agent identity.
- A mode adds a prompt addendum and may adjust tool availability.
- Context remains bound to the SESSION, not the mode.
- Slash command: `/mode architect`, `/mode planner`, `/mode writer`.

### Multi-Agent Sessions
- A session can have 1+ agents (via `session_agents` join table).
- Each agent has its own system prompt assembled independently.
- Messages are attributed to a specific agent via `agent_id`.
- Turn-taking: directed (`@agent`), round-robin, or free-form.

### Agent-to-Agent Communication
- Mentat spawns new sessions with worker agents programmatically.
- Worker sessions can run without user participation.
- Mentat monitors worker sessions and reports results.
- User can join any session at any time.

### Delegation Flow
1. User asks Mentat to do something requiring tool execution.
2. Mentat creates a delegation envelope (task description, context).
3. Chat engine spawns a worker session with the appropriate agent.
4. Worker executes (tool calls, file operations, etc.).
5. Worker reports results back to the originating session.
6. Mentat summarizes results for the user.

## Rationale

- Separating cognitive and execution roles keeps Mentat's context clean — no tool results polluting planning conversations.
- Modes are cheaper than agent switching — no context loss, no session fragmentation.
- Multi-agent sessions enable richer collaboration (user + Mentat + specialist discussing architecture).
- Agent-to-agent communication enables autonomous workflows without user in the loop.

## Consequences

- Need `session_agents` join table for multi-agent support.
- Need `agent_id` on every message for attribution.
- Need session spawning mechanism for delegation.
- Need monitoring/notification system for worker session completion.
- Mentat's tool permissions must be carefully scoped (read-only context tools only, no execution).
