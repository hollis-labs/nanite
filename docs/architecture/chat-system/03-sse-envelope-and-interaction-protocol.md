# 03 · SSE Envelope & Interaction Protocol

> **Scope:** the wire protocol from backend to UI. Two layers: (1) **SSE event taxonomy** — typed control events that drive UI state; (2) **in-band envelope kinds** — structured cards/forms emitted by the LLM inside an assistant message via `nanite_show_card` or `nanite-envelope` fenced blocks.
>
> **Related:** [13 Notification & Card Surface](13-notification-and-card-surface.md) (the UI side — every card type, where it lives, lifetime), [04 Harness](04-chat-harness-and-loop-orchestration.md) (emits most SSE), [09 Session/Slot](09-session-and-slot-management.md) (FE singleton bleed root cause).

## Purpose

Drive the UI state machine over a single SSE stream per session, with a stable event taxonomy and a typed envelope sub-protocol for richer payloads.

## Key files

**Backend:**
- `internal/chat/engine.go:88-120` — `StreamEvent` struct + event-name constants
- `internal/server/server.go` + `streams/` — ring buffer, broadcast, reconnection
- All `chat_*.go` files in `internal/service/` — emitters
- `internal/envelope/schemas/*.schema.json` — envelope kind schemas (25 types)
- `ui/src/generated/envelope-types.generated.ts:310-334` — generated TS types

**Frontend:**
- `ui/src/hooks/useChat.ts:18-36` — SSE event-name constants
- `ui/src/hooks/useChat.ts:354-674` — listeners
- `ui/src/components/EnvelopeRenderer.tsx:157` — `getEnvelopeComponent(envelope.type, recoverMode)`

## Entry points

- **Backend** any callsite with access to `streams.BroadcastSessionStreamEvent(sessionID, event)`.
- **Frontend** `EventSource` opened on stream start; reconnects with `?from=<lastEventId>` after stall.

## SSE event taxonomy

Declared in `engine.go:90`:

| Event | Emitter | Frontend handler |
|---|---|---|
| `stream_start` | `generateResponse` start | begin streaming UI |
| `delta` | provider stream chunk (text) | append to narration / final / thinking |
| `replace_content` | full message replace (rare; recovery) | overwrite current bubble |
| `stream_end` | end of generation | finalize, persist message client-side |
| `error` | many: provider error, recovery refused, timeout, persist fail | `addChatError` |
| `tool_call` | `chat_tool_executor` per exec | `addToolCall` (Tools drawer pip) |
| `tool_result` | `chat_tool_executor` post-exec | `updateToolCall` |
| `status` | retry notice, "Stopped", "Response was cut short" | inline tiny banner |
| `circuit_open` | provider circuit breaker open OR loop circuit | `setCircuitOpen(true)` |
| `session_takeover` | another browser tab connected to same session | `setSessionTakeover(true)` |
| `tool_warning` | consec failures, no-tools-warning, level=critical/warning | `addToolWarning`; critical=text-only mode flip |
| `plugin_envelope` | typed envelope SSE channel | `addPluginEnvelope`; routes to drawer/panel |
| `message_received` | inbox / agent_message arrival | (FE consumes via inbox path, not chat-stream) |
| `subagent_run_status_changed` | subagent run state change | (FE not subscribed currently — gap) |
| `mode_suggestion` | `classify.ClassifyMode` confidence ≥ 0.7 | `setPendingModeSuggestion` → confirm card |
| `notify_pause` | `dev_*` exec (skip dev_grep/glob) | **NOT subscribed in FE — gap** |

Emitted but not in the constant list:

| Event | Emitter | Frontend handler |
|---|---|---|
| `rate_budget_pause` | `chat_rate_budget_pause.go:38,104` | **NOT subscribed in FE — gap** |
| `chat-loop-budget-soft-warning` | `chat_loop_budget_soft_warning.go` | **NOT subscribed in FE — gap** (devmode-gated SSE) |
| `panel_signal` | envelope `target`/`render_target` routing | `applyEnvelopePanelEffects` |
| `approval_request` | subagent dispatch approval, plugin permission approval | `addPendingApproval` |

## In-band envelope kinds (25 types)

Declared in `internal/envelope/schemas/*.schema.json`, generated as TS types. Each kind binds 1:1 with a renderer component via `getEnvelopeComponent(envelope.type, recoverMode)`.

`approval-card`, `artifact-mini`, **`chat-loop-terminated`**, `confirmation-card`, `diff-card`, `document-viewer`, **`elicitation-prompt`**, `error-report`, `giphy-modal`, `info-card`, `kb-result`, `list-card`, `metric-card`, `progress-card`, `proposal-card`, `question-form`, `report-card`, `resolution-capture`, `session-task`, `subagent-spawn-approval`, `table-card`, `ticket-confirmation`, `ticket-form`, `timeline-card`.

Two emission paths:

- **Inline** the LLM emits a ` ```nanite-envelope ... ``` ` fenced block inside an assistant message. `chat.ParseEnvelopes` extracts on stream end.
- **SSE** `plugin_envelope` event with the envelope JSON (used by plugins / mid-stream emitters).

## Reconnection / ring buffer

- Each generation has an `assistantMsgID` keyed ring of events.
- `StreamManager.ScheduleCleanup(msgID, 60s)` retains events for 60s after stream end (reconnect window).
- `EventID uint64` monotonic per stream (`engine.go:119`).
- Frontend tracks `lastEventIdRef`, reconnects with `?from=<lastEventId>` on stall (60s no event triggers `streamStalled = true`).

## Devmode gating

- `chat-loop-budget-soft-warning` emits SSE **only** when `developer_mode || NANITE_DEVMODE=1` (`chat_loop_budget_soft_warning.go:48-52`); always logged regardless.
- This pattern is the template for "backend signal that's UX-pending" — emit gated, log always.

## Logic gates

- **Ordering** `EventID` is per-stream monotonic. UI relies on order of `delta` for narration vs final phase split (driven by stop_reason on the provider side, not by event type).
- **Envelope routing** `target` / `render_target` in the envelope drives `applyEnvelopePanelEffects` to choose drawer vs inline. Default is inline-in-transcript.
- **`tool_warning` levels** `level=critical` flips text-only mode (composer indicator); `level=warning` adds a banner. Both auto-dismiss on `stream_end`.
- **`error` event vs envelope `error-report`** — `error` events are harness-emitted (provider failure, persist failure, recovery refused). `error-report` envelope is LLM-emitted (the agent narrating an error condition in its message). Both surface as banners but persist differently.

## Current gaps

- **G-SSE-UNSUBSCRIBED** — three backend SSE event kinds emit but the FE doesn't subscribe: `notify_pause`, `rate_budget_pause`, `chat-loop-budget-soft-warning`. Backend signals plumbed for upcoming UX work; users currently see no UI for them. See [gaps.md](gaps.md#g-sse-unsubscribed).
- **G-SUBAGENT-STATUS** — `subagent_run_status_changed` listed in the constant table but FE consumption path not verified for the new external-agent flows. Users report PTY-spawned subagent feels silent; this event channel may be the lever ([05](05-external-agent-execution.md), [13](13-notification-and-card-surface.md)).

## Test surface

- `EventSource` mock for FE; backend emits via `streams.BroadcastSessionStreamEvent` interface (mockable per test).
- Replay test: serialize a stream of events with EventIDs, simulate disconnect at event N, reconnect with `?from=N`, assert no duplication or gap.
- Envelope round-trip: emit each of the 25 envelope kinds, assert the renderer maps to the expected component.
