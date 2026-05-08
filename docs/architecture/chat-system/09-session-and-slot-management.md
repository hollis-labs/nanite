# 09 · Session & Slot Management

> **Scope:** the session lifecycle and the 11-slot context model. Hot-swap state, Glass-3/Glass-4 handoff, and the **root cause of multi-chat-session bleed** (FE singleton store).
>
> **Related:** [10 Context Window](10-context-window-management.md), [04 Harness](04-chat-harness-and-loop-orchestration.md), [01 Chat Stream](01-chat-stream-messaging.md), [03 SSE](03-sse-envelope-and-interaction-protocol.md).

## Purpose

Bind a conversation to a single mutable bucket of state: history, agent, mode, slot cache, path grants, in-flight generation, SSE stream. Make slot assembly deterministic and cache-friendly.

## Key files

**Backend:**
- `internal/store/sessions.go` — `*store.Session`, persistence
- `internal/context/window.go` — slot Window assembly, `SlotFlags`, `LoadHint`
- `internal/context/slot.go:47` — `SlotResult.Window`, `SlotOrder`, slot cache key (SHA-256)
- `internal/context/handoff.go`, `internal/context/handoff_envelope.go` — Glass-3/Glass-4 handoff
- `internal/service/chat.go:281-360` — `inFlightGen` registry

**Frontend (root of bleed):**
- `ui/src/stores/useChatStore.ts:165-200` — Zustand singleton

## Session lifecycle

1. **Create** — `POST /api/sessions` writes `*store.Session`; UI sets `useAppStore.activeSessionId`.
2. **Attach** — UI subscribes to SSE for the session's stream.
3. **Resume** — `loadMessages(sessionID)` paginates history into the transcript.
4. **Stop** — `stopStreaming` closes EventSource. Backend keeps the session row.
5. **Cancel in-flight** — sending a new message to a session with an in-flight generation cancels the prior one (CW-20260418-0043, see [04](04-chat-harness-and-loop-orchestration.md)).

## The 11-slot model

`SlotOrder` (`internal/context/slot.go:47`):

| # | Slot | Compactable | Purpose |
|---|---|---|---|
| 1 | System | NO | Harness system prompt |
| 2 | Memory | yes | Memory recall results |
| 3 | Agent | NO | Agent profile prompt |
| 4 | Mode | NO | Active mode addendum |
| 5 | Rules | NO | Composed rules |
| 6 | Tools | yes | Tool catalog (post-trim per Glass-7) |
| 7 | Session | yes | Per-session context |
| 8 | Context | yes | Workspace context |
| 9 | UserContext | NO | Reminders / system-reminder injection |
| 10 | Handoff | NO | Glass-3/Glass-4 handoff content |
| 11 | Conversation | yes | Message history |

Each `Slot{Content, TokenCount, CacheKey, Compactable, Flags}`. SHA-256 `CacheKey` lets the provider place `cache_control` markers at slot boundaries ([07](07-provider-routing.md), [10](10-context-window-management.md)).

**Compactable=false slots** (System, Agent, Mode, Rules, UserContext, Handoff) are never trimmed by compaction — guarantees identity, tools, mode, rules, reminders, and handoff context survive overflow recovery.

## Hot-swap (Glass-5, CW-20260502-0012) — plumbing only

`SlotFlags.LazyLoad` + `LoadHint` shipped in `context/window.go:151`. When set, the slot ships only the `LoadHint` pointer string instead of full content; cache invalidates on toggle.

**`grep "SetFlags.*LazyLoad" internal/service/`** returns 0 hits — **no production code path activates LazyLoad on a slot.** The primitive is wired in slot Window assembly but no caller toggles it.

`SkillEssentialCap=25` (`internal/chat/context.go:333`) governs the inline-rendered skill list, but that's a catalog partition, not LazyLoad.

> **G-HOT-SWAP-DEAD** — see [gaps.md](gaps.md#g-hot-swap-dead).

## Glass-3 + Glass-4 handoff

`SlotHandoff` is non-compactable, `AutoInject=true`, capped at `SlotHandoffMaxTokens=1500`.

`ensureGlass4HandoffPreCompact` (`chat_generate.go:1813, 2082`) runs **only for long-running sessions** (`IsLongRunning(sess)` checks the `session.intent` column added by migration 052).

- **Long-running** (intent=long-running) → Glass-4 handoff path; slot pre/post-compaction injection
- **Per-turn / ephemeral / unclassified** → legacy P7 stash path

Glass-3 added `intent` column + `SlotHandoff` foundation; Glass-4 wires the pre/post-compaction injection. `_, err := s.ensureGlass4HandoffPreCompact(...)` is called in production — **Glass-4 IS wired** for long-running sessions.

The deferred ticket **CW-20260504-0004** is the production-shape verification (Glass-3 + Glass-4 smoke), human-in-the-loop.

## Cross-session bleed — the actual user problem

Backend is correctly session-scoped:
- separate `sessionID`s
- separate `inFlightGen` slots (`chat.go:281-360`)
- separate `generateResponse` goroutines
- separate SSE streams
- separate path-grants buckets

**The bleed is on the FE Zustand store** (`ui/src/stores/useChatStore.ts:165-200`):

| Type | Scope | Examples |
|---|---|---|
| **Globals (BLEED)** | one value across all sessions | `isStreaming`, `streamingContent`, `streamingNarration`, `streamingFinal`, `streamingThinking`, `streamingSessionId`, `statusMessage`, `circuitOpen`, `sessionTakeover`, `streamStalled`, `chatErrors`, `pendingApprovals`, `toolWarnings`, `pendingModeSuggestion`, `chatToast`, `activeMode`, `activeModel`, `activeEffort` |
| **Session-keyed (CORRECT)** | `Map<sessionID, ...>` | `toolCallsBySession`, `pluginEnvelopesBySession`, `autoSwitchSessionOverrides` |

User-visible symptoms:

- **Typing indicator follows you** — `isStreaming` + `streamingContent` are global. Switch tabs while session A streams; the typing indicator/narration appears in session B's view.
- **Drawer contents bleed** — `circuitOpen`, `sessionTakeover`, `streamStalled` drive the working-drawer banners, all global.
- **Errors / warnings cross sessions** — `chatErrors` (banner stack), `toolWarnings`, `pendingApprovals` are global.
- **Mode dial bleeds** — `activeMode`, `activeModel`, `activeEffort` are session-independent globals.

**Tool-call drawer pip is correctly per-session** via `toolCallsBySession`. Plugin envelopes are correctly per-session.

> **G-FE-SINGLETON** — see [gaps.md](gaps.md#g-fe-singleton). This is the user-reported issue. Fix shape: refactor `useChatStore` to keep a `Map<sessionID, ChatSessionState>` and select against the active session (or have one store instance per session).

## Logic gates

- **Slot order is fixed.** Provider sees System → Memory → Agent → Mode → Rules → Tools → Session → Context → UserContext → Handoff → Conversation. Cache markers placed at slot boundaries.
- **Compactable=false guarantees** — overflow recovery cannot drop these slots, only the others. Used as the load-bearing identity surface.
- **`ensureGlass4HandoffPreCompact` only fires for long-running.** Per-turn / ephemeral sessions still use the legacy P7 stash path. Migration of older sessions to long-running-by-default has not happened.

## Current gaps

- **G-FE-SINGLETON** (P0) — see above. Direct user-visible bug.
- **G-HOT-SWAP-DEAD** — `SlotFlags.LazyLoad` plumbing complete; no production caller activates it. The `tool-block-hot-swap-design.md` doc describes the design intent.
- **G-HANDOFF-CLASSIFY** — sessions without `intent` set never hit the Glass-4 path. Migration 052 added the column; sessions classified after creation may be missed.

## Test surface

- Slot assembly deterministic test: identical inputs → identical Window + cache_keys.
- Cache invalidation: change Memory slot content; assert all later slots' cache_keys unchanged but their `cache_control` placement shifts.
- Handoff Glass-4: long-running session crosses compaction threshold, assert Handoff slot populated pre-compaction and survives.
- FE singleton repro: open two chat sessions in the same browser, send a message in session A, switch to session B mid-stream → reproduce typing-indicator bleed (G-FE-SINGLETON).
- LazyLoad smoke (when activated): toggle a slot to LazyLoad; assert provider receives `LoadHint` not full content; cache_key changes.
