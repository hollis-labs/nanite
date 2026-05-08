# 13 · Notification & Card Surface

> **Scope:** every card, banner, toast, error, warning, inline alert, and envelope-rendered card the chat surface presents. What event triggers it, who emits it, where it renders, how long it lives, what user interactions exist.
>
> **Related:** [03 SSE Envelope & Interaction Protocol](03-sse-envelope-and-interaction-protocol.md) (the wire), [09 Session/Slot](09-session-and-slot-management.md) (the FE singleton bleed root cause), [04 Harness](04-chat-harness-and-loop-orchestration.md) (most emitters).

## Purpose

Provide a single reference for the entire UI notification surface. Useful for: planning new card types without duplicating, testing surfacing rules, mapping events to UI states for QA scripts.

## Conventions

- **Surface** locations: `Transcript inline` / `Working drawer` / `Tools drawer` / `Composer` / `Toast` / `Modal` / `Inline above bubble` / `Backend-only`.
- **Lifetime:** how long the UI element is visible. `Persisted` = saved with the message; `Session` = lives in store until session change/refresh; `Stream` = until `stream_end`; `Manual dismiss` = user must close.
- Backend signals that emit but FE doesn't subscribe are marked **Backend-only** with a link to [gaps.md](gaps.md).

## Cards & banners — the full table

| Surface | Trigger | Backend emitter | FE consumer | Display location | Lifetime | Interactions |
|---|---|---|---|---|---|---|
| **Streaming text bubble** | SSE `delta` (final phase) | `chat_generate.go:1195` | `useChat.ts:354-378` `appendStreamFinal` | Assistant message in transcript | Until `stream_end` | Stop button on composer |
| **Thinking strip / Working pill** | SSE `delta` (narration/thinking) | `chat_generate.go:1171, 1195` | `appendStreamNarration/Thinking` | Above current bubble during stream → collapses to pill post-stream | Stream + persisted in `metadata.thinking` | Click pill to expand |
| **ThinkingIndicator** | `streamStalled === true` | client watchdog flips after 60s no event | `ChatTranscript.tsx:445` | Inline above bubble | Until next event | None |
| **Status message** | SSE `status` | many sites: retry notice, "Stopped", "Response was cut short" | `useChat.ts:524` → `setStatusMessage` | Tiny warning text below transcript (`ChatMain.tsx:59`) | Until next content delta or `stream_end` | None |
| **Tool call running pip** | SSE `tool_call` | `chat_tool_executor.go` per tool exec | `useChat.ts:391` `addToolCall` | Tools tab in `ChatPrimaryDrawer` (top drawer) | Per-session (`toolCallsBySession`); retention per `tool_drawer_retention` setting | Tab toggle, expand for detail |
| **Tool call result** | SSE `tool_result` | `chat_tool_executor.go` post-exec | `useChat.ts:410` `updateToolCall` | Same Tools tab; status flips done/error | Same retention | Click to view summary |
| **ToolWarningBanner** | SSE `tool_warning` (level=warning/critical) | `chat_loop_state.recordToolCall` consec failures, no-tools warning | `useChat.ts:423` → `addToolWarning` | Inline banner during stream (`ChatTranscript.tsx:403`) | Stream + `toolWarnings` array (in-memory) | Auto-dismiss on `stream_end` |
| **Text-only mode** | TOOL_WARNING `level=critical` + "no MCP tools" | `chat_generate.go:319-326` | `useChat.ts:432` → `setTextOnlyMode(true)` | Composer indicator | Persists until manually re-enabled or session switch | None automatic |
| **ModeSuggestionCard** | SSE `mode_suggestion` | `chat_generate.go:404` | `useChat.ts:488` → `setPendingModeSuggestion` | `ModeSuggestionCard.tsx` confirm card | Until user confirms / dismisses | accept / dismiss buttons |
| **CircuitOpen banner** | SSE `circuit_open` | provider rate-limit retries (`chat_generate.go:243-249`) or chat-loop circuit (line 1372-1376) | `useChat.ts:533` → `setCircuitOpen(true)` | `ChatWorkingDrawer` (`ChatMain.tsx:67`) | Until user retries / dismisses | Retry / Dismiss |
| **SessionTakeover banner** | SSE `session_takeover` | another browser tab connected to same session | `useChat.ts:539` → `setSessionTakeover(true)` | ChatWorkingDrawer | Until refresh / new send | None (informational) |
| **StreamStalled banner** | watchdog interval (FE only) | client-side `setInterval` (`useChat.ts:152-167`) sets `streamStalled` | — | ChatWorkingDrawer | Until `reconnectStalledStream` or new event | Reconnect button |
| **ErrorBanner (chat error)** | SSE `error` w/ `structured_error` | many: provider error, recovery refused, context-overflow-failed, timeout | `useChat.ts:598` → `addChatError` | Stack of banners after transcript (`ChatTranscript.tsx:450-453`) | Persisted in localStorage per session (CW-20260418-0100) | Dismiss per-error |
| **ApprovalCard envelope** | `approval_request` SSE OR `approval-card` envelope | subagent dispatch approval, plugin-permission approval | `useChat.ts:507` → `addPendingApproval` (SSE); `EnvelopeRenderer` (envelope) | Inline in transcript | Until resolved | Approve / Deny |
| **ChatLoopTerminatedCard** | `chat-loop-terminated` envelope | `emitChatLoopTerminated` on runaway / idle / hardCeiling / retry-exhausted | Envelope renderer | Inline in transcript replacing truncated bubble | Persists with message | None |
| **ElicitationPromptCard** | `elicitation-prompt` envelope (MCP `elicitation/create`) | `internal/elicitation` | Envelope renderer | Inline | Until response | text input or accept/decline |
| **ErrorCard / `error-report` envelope** | LLM-emitted `error-report` envelope | agent prompt | Envelope renderer | Inline | Persists | None |
| **SubagentSpawnApprovalCard** | `subagent-spawn-approval` envelope | dispatch H1 trust gate refusal | Envelope renderer | Inline | Until approve | Approve / Deny |
| **Tool-failure footer** | `maybeAppendFailureFooter` | harness-side fallback when LLM doesn't ack errors | text in message body | Inline | Persisted in message | None |
| **Plugin envelope cards** (proposal, info, list, metric, progress, report, ticket-form, …) | `plugin_envelope` SSE OR inline `nanite-envelope` markers | LLM emits via `nanite_show_card` tool | `useChat.ts:441` (SSE) + Envelope renderer | Inline in message OR routed by `target` / `render_target` to drawer/panel via `applyEnvelopePanelEffects` | Persisted with message | per-card |
| **Inbox toast (chatToast)** | Mode B3 switch | `setActiveMode` callsites | `ChatTranscript.tsx:117` `showChatToast` | Bottom toast | 3s auto-dismiss | None |
| **Notify-pause** | `dev_*` tool exec (skip dev_grep/glob) | `chat_notify_pause.go:111` SSE `notify_pause` | **Backend-only** — see [G-SSE-UNSUBSCRIBED](gaps.md#g-sse-unsubscribed) | (planned) inline before tool runs | 1.5s pause | (planned) cancel |
| **Rate-budget pause** | `provider.ErrRequestExceedsRateBudget` after compaction refused | `chat_rate_budget_pause.go:104` SSE `rate_budget_pause` | **Backend-only** — see [G-SSE-UNSUBSCRIBED](gaps.md#g-sse-unsubscribed) | (planned) toast/banner | until auto-retry / user_action | (planned) compact / split |
| **Soft max-turns warning** | iter ≥ resolvedMaxTurns | `chat_loop_budget_soft_warning.go` SSE `chat-loop-budget-soft-warning` | **Backend-only (devmode)** — see [G-SSE-UNSUBSCRIBED](gaps.md#g-sse-unsubscribed) | (planned) inline | one-shot per gen | None |
| **Slot-changed envelope** | mode classifier transitions Tools slot | `chat.slot_changed.go` | Envelope renderer | inline notification card | persists | "What is this?" link to `/help/hot-swap` |
| **CompactionDivider** | `slot_changed` for Conversation slot post-compaction | compaction pipeline | `CompactionDivider.tsx` | divider in transcript | persisted | None |
| **IterationLimitWarning** | `tool_warning` critical at consecutiveFailCap soft cap | `chat_loop_state` | `IterationLimitWarning.tsx` | inline | until `stream_end` | None |
| **ArtifactMiniCard** | `artifact-mini` envelope | `maybeCreateAutoArtifact` (tool result with file write) | Envelope renderer | bottom_chat_drawer | transient (auto-dismiss after download) | Download / Dismiss |

## Cross-session bleed (the user-reported symptom)

Most surfaces above consume *globals* in `useChatStore` rather than session-keyed state. From [09](09-session-and-slot-management.md):

- **Bleed (global):** ThinkingIndicator, Streaming bubble (text/narration/thinking), Status message, ToolWarningBanner, ModeSuggestionCard, CircuitOpen, SessionTakeover, StreamStalled, ErrorBanner, ApprovalCard (SSE path), ChatToast.
- **Correctly session-keyed:** Tool call pip + result (`toolCallsBySession`), plugin envelope cards (`pluginEnvelopesBySession`).

> **G-FE-SINGLETON** is the consolidated gap for this. Fix shape in [gaps.md](gaps.md#g-fe-singleton).

## Logic gates

- **Inline transcript vs drawer routing** — driven by envelope `target` / `render_target` via `applyEnvelopePanelEffects`. Default is inline.
- **Persistence** — anything stored in the message row persists; in-memory store state (errors, warnings, toolCalls) is session-state and lost on refresh except `chatErrors` which is mirrored to localStorage (CW-20260418-0100).
- **Card kind ↔ component** — 1:1 via `getEnvelopeComponent(envelope.type, recoverMode)` (`EnvelopeRenderer.tsx:157`). Adding a kind requires the schema, the generated TS type, and the component map entry.
- **Auto-dismiss vs manual** — Stream banners auto-dismiss on `stream_end`; chat errors persist until user dismisses; toasts are 3s.

## Current gaps

- **G-FE-SINGLETON** — most surfaces consume globals; root cause of cross-session bleed.
- **G-SSE-UNSUBSCRIBED** — three backend signals emit cleanly, no FE listener: `notify_pause`, `rate_budget_pause`, `chat-loop-budget-soft-warning`. Three card-types planned, none rendered.
- **G-PTY-NO-TOOL-EVENTS** — CLI/PTY children don't surface per-tool `tool_call` events; the Tools drawer pip doesn't update during a CLI turn (only one running pip with no detail). See [05](05-external-agent-execution.md), [gaps.md](gaps.md#g-pty-no-tool-events).

## Test surface

- Each card type: synthetic SSE / envelope payload + assertion the right component mounts in the right surface with the right interactions.
- Reconnect within window: emit a card-bearing event → disconnect → reconnect via `?from=<lastEventId>` → assert the card replays exactly once.
- Persistence: emit a chat error → refresh page → assert error banner restored from localStorage.
- Tool drawer retention: emit 5 tool_call/result pairs → switch session → switch back → assert pips visible per `tool_drawer_retention`.
- Cross-session repro (G-FE-SINGLETON): two sessions, send in A, watch B inherit indicators.
