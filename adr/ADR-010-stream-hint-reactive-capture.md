# ADR-010: Stream Hint Reactive Capture

## Status: Accepted

## Date: 2026-03-12

## Context

Agents operating within chat sessions face two recurring inefficiencies: (1) querying service status or health data pulls raw API responses into the main context window, consuming tokens and introducing drift; (2) architectural decisions, blog ideas, tasks, and notes surface naturally during conversation but require manual effort to capture before the session ends or context is lost.

The system needs a mechanism that intercepts these moments reactively — without burdening the participant writing or reviewing the conversation. Stream shortcodes (hints) embedded inline in agent or user output provide a lightweight vocabulary for signaling intent. The question is how those hints are detected and acted upon at different urgency levels.

Three distinct scenarios require different dispatch strategies:

- **Immediate query** (e.g., `:qstatus`, `:qhealth`): needs a fast, context-clean response within the current session.
- **Async capture** (e.g., `:adr`, `:blg`, `:note`, `:tsk`): can be deferred but must survive session death.
- **Zero-effort background capture**: hints detected automatically from stream output without any explicit invocation.

A2A (agent-to-agent) side-channel processing is required so hint dispatch does not pollute the main chat stream.

## Decision

Stream hints are processed through a **three-level reactive capture system**, layered by urgency and infrastructure availability.

### Hint Vocabulary

| Hint | Meaning |
|------|---------|
| `:adr` | Capture an Architecture Decision Record |
| `:blg` | Capture a blog post draft or idea |
| `:tsk` | Create a Volon task |
| `:note` | Save a freeform note |
| `:todo` | Lightweight action item |
| `:draft` | Save a draft artifact |
| `:qstatus` | Query current project/sprint status |
| `:qhealth` | Query Tiamat service health |

### Level 1 — Sub-Agent (Immediate, Context-Clean)

Used for **query hints** (`:qstatus`, `:qhealth`).

A sub-agent is launched within the session with an isolated context window. It fetches data via MCP, formats a summary, and returns it to the main conversation. The main context window receives only the digest — not the raw API payload. Token cost is bounded; drift is eliminated.

### Level 2 — Volon Task Dispatch via Skill (Async, Session-Resilient)

Used for **capture hints** (`:adr`, `:blg`, `:note`, `:tsk`, `:todo`, `:draft`).

When a hint is detected, the corresponding skill is invoked (e.g., `adr`, `nanite`, `task-create`). The skill either executes inline or dispatches a Volon task that persists beyond session death. The artifact is written to the appropriate store (Cortex, Volon, Nanite, or the local ADR file) and confirmed back to the session.

Level 2 is the default capture path today. It requires explicit hint detection (manual or hook-triggered) but does not require stream infrastructure.

### Level 3 — Hook-Based Two-Stage Observer/Specialist Dispatch (Zero-Effort, Fully Reactive)

Used for **all hints** once stream infrastructure (ADR-009) is operational.

**Stage 1 — Observer**: A lightweight hook monitors the output stream for hint shortcodes. The observer does not process content — it pattern-matches the stream and emits a structured event when a hint is detected.

**Stage 2 — Specialist**: The event is routed to a specialist agent scoped to the hint type. The specialist receives only the hint payload (surrounding context window, not the full session), executes the appropriate capture or query action, and writes the result to the target service.

The main chat stream receives no interruption. Hint processing runs on an A2A side-channel. This stage requires ADR-009 unified messaging streams as a prerequisite.

### A2A Side-Channel

All hint processing — at every level — is isolated from the main chat stream. Sub-agents, skills, and specialist dispatches communicate results back as structured messages, not inline text. This keeps the main conversation legible and prevents recursive hint detection loops.

### Layering by Urgency

```
:qstatus / :qhealth  →  Level 1 (immediate sub-agent)
:adr / :blg / :note  →  Level 2 now; Level 3 when stream infrastructure lands
:tsk / :todo         →  Level 2 now; Level 3 optional
:draft               →  Level 2 now; Level 3 optional
```

Each level is independently useful and can be shipped incrementally without waiting for the others.

## Consequences

- **Skills are the unit of delivery**: each hint type maps to a skill (or skill variant). Skills for `:adr`, `:blg`, `:note`, `:tsk`, `:qstatus`, and `:qhealth` are the primary build targets.
- **Level 3 depends on ADR-009**: hook-based stream detection cannot be built until the unified messaging stream is operational. Levels 1 and 2 do not have this dependency.
- **Token savings**: Level 1 sub-agent isolation prevents raw API payloads from entering the main context window. Estimated savings are significant for status/health queries run multiple times per session.
- **Hook infrastructure needed for Level 3**: post-message hooks must be capable of stream scanning and structured event emission. This is a non-trivial infrastructure investment.
- **Design capture becomes ambient**: once Level 3 is operational, architectural decisions, ideas, and tasks surface automatically without any participant remembering to invoke a command.
- **A2A side-channel discipline**: hint specialists must not write back to the main stream in ways that trigger further hint detection. Loop prevention is a required design constraint.
- **Incremental rollout**: Level 1 and Level 2 can ship in the current sprint. Level 3 is a future milestone gated on ADR-009 delivery.

## References

- ADR-009: Unified Messaging Streams (`adr/ADR-009-unified-messaging-streams.md`)
- `volon/docs/special-agent-patterns.md`
- `volon/docs/design-philosophy.md`
- BLG-20260312-057 (token optimization via sub-agent isolation)
- BLG-20260312-058 (structured objects in stream output)
