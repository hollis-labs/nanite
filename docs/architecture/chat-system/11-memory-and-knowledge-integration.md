# 11 · Memory & Knowledge Integration

> **Scope:** how the chat surface integrates with Vanta Conduit (the primary memory/knowledge substrate). Tool-surface availability, tool description overrides, capture-to-vanta from chat, embedding warning.
>
> **Related:** [02 Tool Invocation](02-tool-invocation-and-authority.md), [03 SSE](03-sse-envelope-and-interaction-protocol.md), [09 Session/Slot](09-session-and-slot-management.md) (Memory slot).

## Purpose

Make memory recall, knowledge lookup, and capture writes available as first-class tools to the chat agent. Surface a guard rail (embedding warning) when the recall path is non-functional.

## Key files

- `internal/mcp/memory_tools.go` — `nanite_memory_recall` and friends
- `internal/mcp/vanta_descriptions.go:28-84` — Vanta tool description overrides for chat surface
- `internal/service/chat_generate.go:189` — `maybeEmitEmbeddingWarning`
- The Vanta MCP server is registered like any other MCP server; transport auth per-server in MCP config.

## Vanta hooks during chat

Tools the chat agent can call directly:

- `mcp__mux__memory_recall` — primary recall
- `mcp__mux__conduit_lookup` — memory + knowledge union
- `mcp__mux__memory_write`, `memory_get`, `memory_history`, `memory_promote`, `memory_deprecate`, `memory_get_revision`
- `mcp__mux__knowledge_get`, `knowledge_write`, `knowledge_history`
- `mcp__mux__context_*` — namespaces, search, RAG, packs, embeds

After Phase 3 the chat-surface deny-list is empty — these tools reach the chat agent without surface-side filtering. Permission is gated by transport auth at the MCP server level ([06](06-permission-and-authority.md)).

## Tool description overrides

`vanta_descriptions.go:28-84` overrides default Vanta tool descriptions with chat-shaped phrasing for the chat surface. The defaults are SDK-shaped (terse, type-focused); the chat overrides are verb-led, generic-path, no embedded `permission.HomeDir`.

Why: tool descriptions carry authority with the model. SDK-style descriptions (e.g. embedding HomeDir or strict path types) leak into the model's mental model of the tool and lead to malformed calls (c114/c117 lessons).

## Capture-to-vanta from chat

Goes through normal `tool_use` → `mcp.Manager.Call` → Vanta MCP server. The agent calls `memory_write` / `knowledge_write` like any other tool.

Memory-key normalization: Vanta requires `[a-z0-9_]` per segment. The capture skill (`capture-to-vanta`) auto-normalizes hyphens → underscores. Direct `memory_write` calls must pre-normalize.

## Memory slot

The `SlotMemory` slot ([09](09-session-and-slot-management.md)) is the staged-recall surface. `assembleTurnContext` populates it from a recall pass; the slot is `Compactable=true` (drops first under overflow) so it doesn't hard-bind context survival.

There is **no automatic `memory_recall` sweep at chat-loop start** — population relies on the agent calling the tool, or upstream code priming the slot. The current pattern is agent-prompted recall.

## Embedding warning

`maybeEmitEmbeddingWarning` (`chat_generate.go:189`) fires once per session when the memory embedder isn't active.

- Advisory only.
- Fires regardless of whether memory tools are called this turn.
- Surface: a `tool_warning`-style banner in the working drawer.

The point: if recall would silently miss because embeddings aren't active, tell the user once.

## Logic gates

- **Vanta is primary; file-based memory is legacy.** `vanta-primary-since: 2026-04-19`. File-based auto-memory is read-only fallback.
- **Memory keys must be normalized** before write.
- **Tool descriptions are chat-overridden** for Vanta tools; default descriptions are not what the model sees.
- **Vanta supersedes is intra-`memory_id`.** New revisions of the same key form a lineage; a different key creates a different memory_id and breaks supersession.

## Current gaps

- **G-NO-AUTO-RECALL** — no automatic memory_recall at session start. Agent must prompt itself. For a first-class assistant experience, primed-on-session-start recall might be valuable; today it's pull-only.
- **G-MEMORY-SLOT-EMPTY** — the Memory slot is structured but rarely populated by the harness; it's mostly populated by tool-call results during the turn (which then go into Conversation, not Memory).

## Test surface

- Mock MCP Vanta server; assert the chat agent's tool descriptions match the overrides, not the SDK defaults.
- Capture round-trip: chat agent calls `memory_write` with a hyphen in the key; assert the capture skill / direct path normalizes correctly.
- Embedding warning fires once: invoke a chat session with embedder off; assert warning emits on first turn, not subsequent.
- `nanite_memory_recall` callsite: confirm it reaches Vanta with the session context.
