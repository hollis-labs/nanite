# Chat System Architecture

> **Status:** current as of 2026-05-07 (post SP-20260501-0003 / Glass sprint close-out, Phase 3 chat surface, soft-`max_turns`).
> **Scope:** the runtime that turns a user message into an LLM response, with all directly-coupled subsystems.
> **Audience:** planners, testers, contributors. Not a tutorial.

The chat system is a **session-scoped, slot-assembled, tool-augmented loop** that streams to the UI over SSE. A single session is the unit of: history, path-grant scope, model/agent selection, mode, slot cache, and SSE stream. Subagents are child sessions; CLI/PTY agents are still session-scoped — the harness owns the loop, the CLI is a provider adapter.

This index is the entry point. Each subsystem doc is self-contained and cross-links into the others.

---

## End-to-End Message Flow

User message → assistant response, 14 steps. Numbered components are clickable into the relevant subsystem doc.

1. **UI** `ChatComposer.sendMessage(content)` (`ui/src/hooks/useChat.ts:300`). Optimistic user-message added; `useChatStore.isStreaming = true` (global — see [09](09-session-and-slot-management.md) on cross-session bleed).
2. **REST** `POST /api/messages` → `chatServiceImpl.HandleMessage` (`internal/service/chat.go:394`). Path-grant scan: `pathGrants.RegisterFromUserMessage(sessionID, content)` registers `~/`, `/`, `./` literals + parents into the session bucket ([06](06-permission-and-authority.md)).
3. **Persist + launch** `HandleMessage` writes the user `*store.Message`, allocates `assistantMsgID`, calls `launchGeneration`. Prior in-flight generation for the same session is canceled (CW-20260418-0043) ([04](04-chat-harness-and-loop-orchestration.md)).
4. **Resolve** `generateResponse` (`internal/service/chat_generate.go:121`) loads session, resolves agent via `agents.ResolveForSession`, parses `AgentConstraints`, resolves model + provider via `resolveProvider` → `chat.InferProvider` ([07](07-provider-routing.md)).
5. **Tool selection** `s.tools.SelectForAgent` returns `ToolSelection{Tools, Progressive, Catalog, OverrideBlock}`. Mode `tool_overrides` applied. `composeExtraSystemPrefix` builds the per-turn dynamic prefix ([02](02-tool-invocation-and-authority.md)).
6. **Plugin filters** `FilterSystemPrompt`, `FilterUserMessage` mutate the per-turn prefix and user content.
7. **Classify** `classify.ClassifyMode(userContent)`. If confidence ≥ 0.7 and disagrees with current session mode → emit non-binding `mode_suggestion` SSE ([08](08-classification-and-intent.md)).
8. **Slot assembly** `assembleTurnContext` builds 11-slot `SlotResult.Window` (System, Memory, Agent, Mode, Rules, Tools, Session, Context, UserContext, Handoff, Conversation). Reminders engine may inject into `SlotUserContext`. SHA-256 cache key per slot ([09](09-session-and-slot-management.md), [10](10-context-window-management.md)).
9. **Stream open** `stream_start` SSE; `streams.BroadcastPresence` ([03](03-sse-envelope-and-interaction-protocol.md)).
10. **Pre-loop budget** `enforceBudgetOrCompact` (drop enrichment → summarize oldest → strip tool blocks). `newLoopState`. Strategy planner produces soft `MaxTurns`; reflex hints / scope tier classified ([04](04-chat-harness-and-loop-orchestration.md), [10](10-context-window-management.md)).
11. **Tool-use loop** (`chat_generate.go:624`):
    - per-iter: budget enforcement → `provider.StreamChat(ChatRequest{SystemPrompt, SlotBlocks, Messages, Model, Tools})`
    - consume `delta` / `tool_use` / `usage` / `thinking` / `error` events
    - mid-stream context-overflow recovery (synchronous compaction + retry, max 2)
12. **Tool execution** if `stop_reason == "tool_use"`:
    - append assistant block
    - `preCheckTools` (permissions + concurrency + path-grants gates)
    - `executeToolBatch` (parallel where safe, serial otherwise)
    - `postProcessToolResults` (artifacts, envelopes, stuck-loop detection)
    - tool results appended as a synthetic user message; loop continues
    - if `stop_reason != "tool_use"` (typically `end_turn`): break
13. **Post-process** `outputFilter` + `FilterAssistantResponse` → `chat.ParseEnvelopes` extracts `nanite-envelope` fenced blocks → `WrapResponse` produces structured JSON. Save assistant `*store.Message`. Record token usage + execution metrics ([07](07-provider-routing.md)).
14. **Stream close** `stream_end` SSE (envelope JSON + final usage). Auto-title + auto-tags goroutines fire. UI `useChat` STREAM_END appends assistant message, clears stream state, invalidates session-usage queries.

---

## Subsystem Index

| # | Doc | What it covers |
|---|---|---|
| 01 | [Chat Stream Messaging](01-chat-stream-messaging.md) | The user↔assistant message stream — types, persistence, multi-turn assembly, F4 narration/final split, F3 thinking blocks |
| 02 | [Tool Invocation & Authority](02-tool-invocation-and-authority.md) | Tool registration, dispatch, in-process tool-broker, what a chat agent actually sees, override blocks, strict-mode-default-off |
| 03 | [SSE Envelope & Interaction Protocol](03-sse-envelope-and-interaction-protocol.md) | The wire protocol: SSE event taxonomy, in-band envelope kinds, devmode gating, reconnection, ring buffer |
| 04 | [Chat Harness & Loop Orchestration](04-chat-harness-and-loop-orchestration.md) | The loop itself: terminators, soft `max_turns`, recoverable errors, telemetry emission points |
| 05 | [External Agent Execution](05-external-agent-execution.md) | Five spawn types: Nanite Harness, inline parallel, async PTY/CLI, chat-session parallel, background. PTY Claude Code flow. |
| 06 | [Permission & Authority](06-permission-and-authority.md) | Path grants, profile permissions, lineage walk, `resolveAllowed`, sandbox, devmode gates, notify-pause |
| 07 | [Provider Routing](07-provider-routing.md) | Provider abstraction, model resolution, capabilities matrix, models.dev / provider envelope sources, prompt cache |
| 08 | [Classification & Intent](08-classification-and-intent.md) | `ClassifyMode`, scope tier + execution pattern, strategy hints, reflex matcher, role assignment |
| 09 | [Session & Slot Management](09-session-and-slot-management.md) | Session lifecycle, 11-slot model, hot-swap state, Glass-3/Glass-4 handoff, **cross-session FE singleton bleed (root cause)** |
| 10 | [Context Window Management](10-context-window-management.md) | Slot trim, cache hints + race, prompt-cache boundary, `EnforceTokenBudget`, overflow recovery |
| 11 | [Memory & Knowledge Integration](11-memory-and-knowledge-integration.md) | Vanta surface during chat, tool description overrides, capture-to-vanta, embedding warning |
| 12 | [Inbox & Internal Messaging](12-inbox-and-internal-messaging.md) | `agent_messages` (user inbox + agent inboxes), message types, agent-to-agent, CLI/PTY messaging via MCP |
| 13 | [Notification & Card Surface](13-notification-and-card-surface.md) | Exhaustive enumeration of every card/banner/toast/error: trigger, surface, lifetime, interactions |
| — | [Gaps](gaps.md) | Consolidated index of known gaps prioritized for planning + testing |
| — | [Future Work](future-work.md) | Locked directional decisions (long-lived PTY default, per-tool CLI SSE, in-process broker), activity-events catalog, open planning follow-ups |

---

## How the subsystems compose

```
                   ┌──────────────────────────────────────────────┐
                   │  UI (ChatComposer / useChatStore singleton)  │
                   └──────────────────────────────────────────────┘
                                       │  REST POST /api/messages
                                       ▼
   ┌────────────────────────────────────────────────────────────┐
   │ HandleMessage  →  generateResponse  (chat-harness loop) ── 04 │
   │                                                            │
   │   ┌─ classify ── 08 ──┐    ┌─ slot assemble ── 09, 10 ──┐  │
   │   ├─ provider resolve ─ 07 ├─ tool select ── 02         │  │
   │   ├─ permission gate ─ 06 ─┤                            │  │
   │   └────────────────────────┴── plugin filters ──────────┘  │
   │                                                            │
   │   tool-use loop  →  provider.StreamChat ── 07               │
   │     │                                                      │
   │     ├─ tool_use → executeToolBatch ── 02, 06               │
   │     ├─ subagent  → child session ── 05                     │
   │     ├─ delta/thinking → chat-stream ── 01                  │
   │     ├─ envelopes → notification surface ── 03, 13          │
   │     └─ error → recoverable-error / circuit ── 04           │
   │                                                            │
   │   inbox writes/reads  ── 12                                │
   │   memory tool-calls   ── 11                                │
   └────────────────────────────────────────────────────────────┘
                                       │  SSE  ── 03
                                       ▼
                   ┌──────────────────────────────────────────────┐
                   │  UI consumes events  →  cards / drawers / 13 │
                   └──────────────────────────────────────────────┘
```

---

## Where existing docs slot in

The pre-existing `docs/architecture/` files are point-in-time design docs. This new set is the current-state reference; the older docs remain useful for design rationale.

- `agent-context-architecture.md` — lessons doc, decision rules, anti-patterns (referenced from boot prompts; complementary to all of these)
- `envelope-pipeline.md` — design-era backstory for [03](03-sse-envelope-and-interaction-protocol.md)
- `tool-broker-design.md`, `tool-discovery-and-enforcement.md`, `tool-first-architecture.md` — design-era backstory for [02](02-tool-invocation-and-authority.md)
- `tool-block-hot-swap-design.md` — design intent for the hot-swap primitive ([09](09-session-and-slot-management.md), [10](10-context-window-management.md)) — note the gap: plumbing landed but no production caller activates it
- `recoverable-errors.md`, `agentic-error-recovery.md` — design backstory for [04](04-chat-harness-and-loop-orchestration.md)
- `agent-panels.md`, `agent-reminders-pins.md` — design backstory for [13](13-notification-and-card-surface.md) and the Reminders engine ([09](09-session-and-slot-management.md))
- `plugin-*.md` — plugin-system docs; touch the chat surface via `FilterSystemPrompt` / `FilterUserMessage` / `FilterAssistantResponse` and via `plugin_envelope` SSE

---

## Conventions used in these docs

- **Path:line** anchors throughout (`internal/service/chat_generate.go:121`). Where a function spans, range form `:121-150`.
- **Subsystem refs** as `[NN](NN-name.md)`.
- **Gaps** are flagged inline under `## Current gaps`, and aggregated in [gaps.md](gaps.md). Each gap has a stable short-id (`G-FE-SINGLETON`, `G-HOT-SWAP-DEAD`, etc.) used in both places.
- **Test surface** lists what would need mocking/fixturing to test the subsystem in isolation. Useful for planning a test harness, not a comprehensive test list.
- The five spawn types from [05](05-external-agent-execution.md) are referenced by name throughout: **Nanite Harness**, **Inline Parallel**, **Async PTY/CLI**, **Chat-Session Parallel**, **Background**.
