# ADR-009: Unified Messaging Streams

## Status: Accepted

## Date: 2026-03-12

## Context

Fragments Engine has multiple communication patterns emerging independently:
- Task comments in Volon
- Chat sessions in Mentat, Nanite, and Volon
- A2A (agent-to-agent) coordination needs
- A2U (agent-to-user) notifications and alerts
- Cerberus service alerts
- Agent handoff context transfer
- Agent output streaming during task execution

Without a unified model, each becomes its own system with its own storage, API, and UI — leading to fragmentation and duplicated infrastructure.

## Decision

**All communication is modeled as scoped message streams.** One primitive, many views.

### Volon owns the write path
Message streams are tightly coupled to Volon objects (tasks, sprints, sessions, projects). Every Volon object auto-gets a stream. All message writes go through Volon API/MCP.

### Cortex provides the read/discovery path
Cortex indexes messages for cross-project search and context assembly via ContextBroker. Cortex does not own messages — it indexes them.

### Messages are NOT owned by Cortex
Cortex is system-agnostic. Coupling memory to agentic orchestration would violate our design philosophy: tools provide functionality, not prescribed process. Messages are execution artifacts that become context over time.

## Agent Types (formalized)

1. **App Internal / System Agents** — The app is the agent. One point of contact per app. SMEs and gatekeepers.
2. **Process / Special Agents** — Born for a specific job. Handler/Field Agent pattern. Instantiated, work, complete.
3. **General Agents** — Standard worker pattern. Boot, claim, execute.

## Consequences

- Task comments, chat, A2A, A2U, alerts, handoffs, and agent output all use the same `streams` + `messages` tables in Volon
- Mentat Chat, Volon task detail, and future UIs all render from the same primitive
- No new broker service needed (no ChatBroker, MessageBroker)
- Cortex ContextBroker gains a new source for context assembly
- Migration needed: existing task comments → stream messages
- Agent identity and discovery remains a separate concern (AgentBroker, future)

## References

- Design doc: `volon/docs/unified-messaging-architecture.md`
- Related: BLG-20260312-024 (handoff protocol), BLG-20260312-040 (A2A messaging), BLG-20260312-028 (broker system)
