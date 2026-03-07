# ADR-003: Context Management Strategy

**Date:** 2026-03-07
**Status:** Accepted
**Decision Makers:** Chrispian, Mentat

## Context

Context management is the #1 challenge in AI chat applications. Based on analysis of OpenClaw (75% budget enforcement), OpenCode (per-turn pruning at 40K tokens), Pi (LLM-based compaction), and Volon (manual only), we need a hybrid strategy.

Mentat Chat has an additional constraint: the primary agent manages MANY different contexts (Project Tiamat, businesses, writing, personal). Context pollution across domains is a real risk.

## Decision

### Context is Session-Scoped
- Each session has its own context window. No cross-session context bleeding.
- Session knows its workspace + optional project. Context assembly starts from these.

### Three-Layer Context Assembly

**Layer 1: Static Context** (always included)
- Agent system prompt + mode addendum
- Workspace description / context snippet
- Project context (if project-scoped)

**Layer 2: Dynamic Context** (assembled per turn)
- Session message history (with compaction)
- Bookmarked messages from this session
- Active task context (if task-scoped)

**Layer 3: On-Demand Context** (agent requests via MCP)
- Cortex context packets
- Volon task/sprint details
- External data via tool calls

### Budget Enforcement
- Configurable budget ceiling: default 75% of model context window
- Per-turn pruning: after each assistant response, compact old tool results to `"[compacted: <summary>]"`
- Head+tail truncation: individual tool outputs capped, preserving error output at tail
- Full compaction: LLM-based summarization when total context exceeds 85% of budget

### Compaction Strategy
1. **Tool result pruning** (automatic, every turn): Tool results older than 3 turns → compacted
2. **Message summarization** (triggered at 85%): LLM generates summary of old messages, replaces them
3. **Session archival** (manual): User can archive a session, summary preserved for reference

## Rationale

- Session-scoped context prevents the biggest problem: planning context from one domain leaking into another.
- Three-layer assembly is progressive — static context is cheap, dynamic is bounded, on-demand is pay-as-you-go.
- Budget enforcement prevents the silent failure mode where context overflow causes degraded responses.
- Compaction preserves important information while freeing space for new turns.

## Consequences

- Need token counting per message (estimate: chars/4 for English)
- Need compaction service (can use the same AI provider)
- Need `is_compacted` flag on messages
- Need `compaction_summary` on sessions
- Context budget widget in right rail for user visibility
