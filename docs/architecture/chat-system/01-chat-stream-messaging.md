# 01 · Chat Stream Messaging

> **Scope:** the user↔assistant message stream — how a user message becomes a persisted row, gets assembled into a multi-turn conversation for the provider, and how the assistant's response is split between narration, thinking, and final text. This doc covers **only** the chat stream. Inbox/email-style internal messaging lives in [12](12-inbox-and-internal-messaging.md).
>
> **Related:** [04 Harness](04-chat-harness-and-loop-orchestration.md), [09 Session/Slot](09-session-and-slot-management.md), [07 Provider](07-provider-routing.md).

## Purpose

Capture user input, persist it, build the multi-turn structure the provider sees, persist the assistant's response back, and keep the F4 narration/final split + F3 signed thinking blocks correctly round-tripped.

## Key files

- `internal/store/messages.go` — message persistence (table: `messages`)
- `internal/service/chat.go:394-650` — `HandleMessage` entry point + history hookup
- `internal/service/chat_generate.go:1268-1279` — message-list build into `provider.ChatMessage`
- `internal/service/chat_generate.go:1576-1610` — assistant persistence
- `internal/service/chat_generate.go:677,715,977,1021,1027,1033,1152,1157` — `persistPartialAssistant` paths
- `pkg/provider/.../provider.go:204` — `ChatRequest{SystemPrompt, SlotBlocks, Messages, Model, Tools}` shape

## Entry points

- **REST** `POST /api/messages` → `chatServiceImpl.HandleMessage(ctx, sessionID, content)`
- **History load** `ContextService.LoadHistory(sessionID)` during `assembleTurnContext` (`chat_generate.go`)

## Happy path

1. Persist user message: `store.CreateMessage(*Message{Role:"user", Content:content, SessionID:sessionID})`.
2. Allocate `assistantMsgID` up front so SSE can stream against it.
3. `assembleTurnContext` calls `LoadHistory` — returns ordered `[]*store.Message` for the session.
4. Build provider input list (`chat_generate.go:1268`): each row → `provider.ChatMessage{Role, Content/ContentBlocks}`. Tool calls + tool results round-tripped as `ContentBlocks` of type `tool_use` / `tool_result`. F3 thinking blocks round-tripped as `provider.ContentBlock{Type:"thinking", Signature:...}`.
5. Provider sees `ChatRequest{SystemPrompt:<dynamic per-turn prefix>, SlotBlocks:[]SlotBlock{Name,Content,CacheKey}, Messages:<this list>, Model, Tools}`.
6. Stream events accumulate into the assistant message:
   - **Narration** (text emitted before `end_turn`) → `metadata.thinking`
   - **Final** (text emitted at `end_turn`) → `message.Content`
   - **Thinking blocks** (F3, signed) → `metadata.thinking_blocks`
   - **Tool use blocks** → appended as `ContentBlocks` on the assistant message
   - **Tool result blocks** → appended as a synthetic `Role:"user"` message with `tool_result` content blocks
7. Post-loop: `outputFilter` + `FilterAssistantResponse` → `chat.ParseEnvelopes` → `WrapResponse` → final structured JSON.
8. Persist assistant `Message{Role:"assistant", Content:<finalJSON>, Envelope:<envelopeJSON>, Metadata:{thinking, thinking_blocks}}`.

## Sad path

- **Empty user content** → rejected at `HandleMessage`.
- **`Failed to load session` / `Failed to resolve agent`** → SSE `error` event + return.
- **Persist failure on user message** → SSE `error` + return; no assistant row created.
- **Mid-stream context cancel / provider error** → `persistPartialAssistant` writes whatever has accumulated to the row marked truncated.
- **Provider stream closed before `end_turn`** → recoverable-error logic ([04](04-chat-harness-and-loop-orchestration.md)) decides retry vs persist-partial.

## Logic gates

- **F4 narration vs final split** — text emitted *before* `end_turn` is narration (lives in `metadata.thinking`); text emitted *at* `end_turn` is final (becomes `message.Content`). The provider's stop_reason drives this.
- **F3 thinking signature round-trip** — Anthropic-style signed thinking blocks must be sent back on subsequent turns to keep the signature valid. Stored in `metadata.thinking_blocks`, replayed in step 4.
- **Tool result placement** — appended as a *new* `user`-role message after the assistant's tool_use turn, not merged into the assistant turn. The loop expects this for the next provider call.
- **System prompt vs slot blocks** — `SystemPrompt` is the per-turn dynamic prefix only (no-tools warning + progressive catalog + `nativeToolGuide` + override block). Persistent system content lives in `SlotBlocks` so it can be cached at slot boundaries.

## Current gaps

- **G-FE-SINGLETON** — the FE `useChatStore` is a Zustand singleton. `streamingContent`, `streamingNarration`, `streamingFinal`, `streamingThinking`, `streamingSessionId` are global. Switching active session while a stream is in flight causes the typing/narration UI to follow the active session, not stay bound to the originating session. Backend is correct; FE needs session-keyed maps for these. See [09](09-session-and-slot-management.md) and [gaps.md](gaps.md#g-fe-singleton).

## Test surface

- `provider.Provider` interface (mock `StreamChat`)
- `ContextService.LoadHistory`
- `store.CreateMessage`
- Slot Window assembly (`assembleTurnContext`)
- F3 thinking-block round-trip: a fixture conversation with a signed thinking block on turn N must reach the provider unchanged on turn N+1.
- F4 split: a single stream with narration → tool_use → final must persist narration to `metadata.thinking` and only the final to `Content`.
