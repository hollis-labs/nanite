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

The `SlotMemory` slot ([09](09-session-and-slot-management.md)) is the staged-recall surface. The chat-harness populates it per turn via the ContextBroker pipeline; the slot is `Compactable=true` (drops first under overflow) so it doesn't hard-bind context survival.

**Per-turn auto-recall is on by default.** `internal/contextbroker/source_memory.go` (`MemorySource`) is registered with the broker whenever `memorySvc != nil` at container startup (`internal/service/container.go:491`). Each turn:

1. `chat.AssembleSlotSources` (`internal/chat/context_client.go`) derives an Intent from the session's last user message.
2. The chat-harness reads the agent profile's auto-recall settings (`ResolveAutoRecallConfig`, `internal/chat/auto_recall_settings.go`) and plumbs them onto the Intent: `AutoRecall *bool`, `AutoRecallLimit`, `AutoRecallMinConfidence`, `AutoRecallTimeout`.
3. `Broker.Fetch` calls every registered source in parallel; `MemorySource.Fetch` honors the intent's auto-recall fields — short-circuits when `AutoRecall=false`, applies the override floors/caps, and wraps the Vanta call in a `context.WithTimeout` (default 2s) so a slow recall can't stall the chat loop.
4. Items with `Source=="memory"` flow through `formatPacketItemsBySource` and into `cw.SetContent(ctxpkg.SlotMemory, ...)`.

**Per-agent control via `agent_profiles.settings` JSON.** The same blob that already carries `debug` carries the auto-recall dials:

```json
{ "auto_recall": false, "auto_recall_limit": 10, "auto_recall_min_confidence": 0.5 }
```

Defaults: `auto_recall=true`, `limit=30`, `min_confidence=0.4`, `timeout=2s`. Profiles that don't pin the keys keep the prior behavior unchanged.

**Telemetry.** `MemorySource.Fetch` emits one structured `slog.Info` per turn:

```
contextbroker/memory: auto-recall ok session_id=… agent_id=… hit_count=… items_kept=… tokens_estimate=… latency_ms=… query_len=…
```

Variants for the empty-result path (`auto-recall hit_count=0`), the disabled path (`auto-recall disabled by intent`, debug level), and the timeout path (`auto-recall timed out`).

**When recall isn't useful.** If an agent's task-context arrives via prompt rather than memory (e.g. a focused executor agent), pin `auto_recall: false` on its profile. The Memory slot will stay empty for that agent's sessions, saving the per-turn Vanta round-trip without affecting the rest of the broker pipeline.

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

- ~~**G-NO-AUTO-RECALL**~~ — ✓ Closed 2026-05-08. Per-turn auto-recall has been live since `phase-3 S2b`; per-agent gating + observability landed on `feat/memory-auto-recall`. See the Memory-slot section above for the current contract.
- ~~**G-MEMORY-SLOT-EMPTY**~~ — ✓ Closed 2026-05-08 (framing was stale). `MemorySource` has been registered and the slot populated per turn since the broker landed.

## Test surface

- Mock MCP Vanta server; assert the chat agent's tool descriptions match the overrides, not the SDK defaults.
- Capture round-trip: chat agent calls `memory_write` with a hyphen in the key; assert the capture skill / direct path normalizes correctly.
- Embedding warning fires once: invoke a chat session with embedder off; assert warning emits on first turn, not subsequent.
- `nanite_memory_recall` callsite: confirm it reaches Vanta with the session context.
