# Fix durable-agent wake `CallerType` mistagging (`CallerChat` → `CallerBackground`)

**Phase:** 0
**Status:** implemented
**Depends on:** none
**Touches:** `internal/service/chat.go` (`HandleMessage`, ~line 727-775), `internal/service/durable_agent_runtime_controller.go` (`SendMessage`, ~line 57-63), `internal/service/durable_agents.go` (`deliverWakePrompt`, ~line 485-494), `internal/dispatcher/dispatcher.go` (`CallerType` definitions, ~line 41-79 — read-only reference), `internal/api/harness_v1.go` (~line 339 — read-only, do not change), `internal/api/messages.go` (~line 38 — read-only, do not change)

## Context

TASKS.md Phase 0 item 3: "Fix durable-agent wake `CallerType` mistagging (`CallerChat` → `CallerBackground`). **Blocks** any future caller-type-aware steering narrowing."

`internal/dispatcher/dispatcher.go` defines the `CallerType` enum (`CallerChat`, `CallerSubagent`, `CallerBackground`) that every generation dispatch is required to carry — the harness architecture doc (`docs/engineering/architecture/04-harness.md`) describes this as the shared mechanism by which "interactive chat, LLM-triggered subagent, REST-triggered delegation, or a durable agent's scheduled wake all converge" on the same turn-loop, distinguished by this tag. `internal/dispatcher/dispatcher.go` documents `CallerChat` as "the user → chat-agent `generateResponse` path" and `CallerBackground` as "the agent-flavored background-job path." A durable agent's scheduled wake is background work by definition — not a user typing into chat — so it must carry `CallerBackground`, not `CallerChat`.

**Verified mistagging path, traced end to end:**

1. A durable agent wake delivers its prompt via `internal/service/durable_agents.go`'s `deliverWakePrompt` (~line 485), which calls `s.runtime.SendMessage(ctx, sessionID, prompt)`.
2. The production `DurableAgentRuntimeController` implementation is `chatDurableAgentRuntimeController` in `internal/service/durable_agent_runtime_controller.go`. Its `SendMessage` (line 57-63) is:
   ```go
   func (c chatDurableAgentRuntimeController) SendMessage(ctx context.Context, sessionID, content string) error {
   	if c.chat == nil {
   		return nil
   	}
   	_, err := c.chat.HandleMessage(ctx, sessionID, content)
   	return err
   }
   ```
   It forwards straight into `ChatService.HandleMessage` — the same method real end-user chat messages go through.
3. `internal/service/chat.go`'s `HandleMessage` (line 727) persists the user message, then at line 775 calls:
   ```go
   s.launchGeneration("handleMessage.generateResponse", sessionID, assistantMsgID, content, ch, dispatcher.CallerChat)
   ```
   `dispatcher.CallerChat` is a **hardcoded literal** at this call site — not derived from context, not parameterized.

`HandleMessage` has exactly 3 call sites in the whole codebase (verified via grep, non-test): `internal/api/harness_v1.go:339` and `internal/api/messages.go:38` (both real end-user-typed-a-message HTTP handlers — correctly `CallerChat`), and `internal/service/durable_agent_runtime_controller.go:61` (the durable-agent wake-prompt delivery path — incorrectly inheriting `CallerChat` because `HandleMessage` hardcodes it regardless of who called it).

**This is not a one-line find-and-replace.** `HandleMessage` is genuinely shared between two different real callers with two different correct `CallerType` values — a blind `s/CallerChat/CallerBackground/` at line 775 would break real user chat (mistagging it as background instead). The fix has to distinguish "a real user hit the chat API" from "the durable-agent runtime controller is delivering a wake prompt" at or before the point `HandleMessage` decides the caller type. For comparison, the already-correct background paths (`triggerHarnessTurn`, `internal/service/chat.go:918`, and `triggerMessageWake`, `internal/service/chat.go:977`) don't go through `HandleMessage` at all — they call `s.runGeneration(..., dispatcher.CallerBackground, ...)` directly with an explicit literal, because those call sites are single-purpose (never shared with a real-user path). `HandleMessage` is the odd one out precisely because it's reused by the durable-agent wake delivery instead of getting its own background-flavored send path.

Why this matters beyond correctness bookkeeping: decision log's steering discussion (§10, §11) and `docs/engineering/GLOSSARY.md`'s "Reflexes" entry describe caller-type-aware behavior as a real future need — reflexes/steering narrowing by caller type. If durable-agent wakes are indistinguishable from real user chat at the dispatcher level, that narrowing is structurally impossible to build later without first fixing this.

## What to do

Give `chatDurableAgentRuntimeController.SendMessage` a way to make its call to the chat layer identify itself as `CallerBackground`, without changing what real end-user messages (`harness_v1.go`, `messages.go`) get tagged as. Two reasonable shapes — pick whichever fits the existing `ChatService` interface most cleanly; if neither fits cleanly, escalate rather than force one:

1. **Context-carried caller type**: `dispatcher.go` already exports `WithCallerType`/`CallerTypeFromContext` (used elsewhere, e.g. `chat_generate.go:696` and `:1056`, to read an ambient caller type off `ctx`). Have `chatDurableAgentRuntimeController.SendMessage` stamp `ctx = dispatcher.WithCallerType(ctx, dispatcher.CallerBackground)` before calling `c.chat.HandleMessage(ctx, sessionID, content)`, and change `HandleMessage`'s call to `launchGeneration` (line 775) to prefer `dispatcher.CallerTypeFromContext(ctx)` when present/valid, falling back to `dispatcher.CallerChat` for the two real HTTP-handler callers (which don't stamp anything today, so they'd still resolve to the correct default). Read `chat_generate.go`'s existing pattern at line 1042-1056 first — there's already an established "read `CallerType` off ctx with a documented unknown-fallback" convention in this codebase; reuse it rather than inventing a new one.
2. **Explicit parameter**: add a caller-type-aware variant (e.g. `HandleMessageAs(ctx, sessionID, content, callerType)`) that `HandleMessage` becomes a thin `CallerChat`-defaulting wrapper around, and have the runtime controller call the explicit variant. Only worth it if approach 1's ctx-based convention doesn't cleanly apply here — check first.

Whichever shape is used, confirm the two real end-user call sites (`internal/api/harness_v1.go:339`, `internal/api/messages.go:38`) are unaffected — they must keep producing `CallerChat`.

Also check `internal/dispatcher/dispatcher.go`'s `Dispatcher.Run` (~line 176) — it validates `req.CallerType.Valid()` and rejects unknown values with an error; make sure whatever fallback logic you add can't accidentally produce an empty/invalid `CallerType` for the real-user path (an empty ctx value read via `CallerTypeFromContext` returns `""`, which is NOT `.Valid()` — the fallback-to-`CallerChat` default must be explicit, not implicit).

## Done means

- A durable-agent scheduled wake's delivered prompt reaches `Dispatcher.Run` tagged `dispatcher.CallerBackground`, not `dispatcher.CallerChat`.
- Real end-user chat messages via `POST` to the harness-v1 and legacy messages endpoints are still tagged `dispatcher.CallerChat` — unchanged behavior, verified not just assumed.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Add or extend a test that exercises `chatDurableAgentRuntimeController.SendMessage` (or the durable-agent wake-prompt-delivery path end to end) and asserts the resulting dispatch carries `CallerBackground` — a regression here is easy to reintroduce silently since `HandleMessage`'s hardcoded literal was exactly this kind of silent bug.
- If request-build slog attribution (mentioned in `dispatcher.go`'s doc comments as the mechanism proving identical structural shape across `CallerTypes`) is exercised by an existing test, confirm it now shows `background` for a wake-delivered turn.

## Work log

**2026-08-18 — worker report:** Implemented shape 1 (context-carried caller type). `chatDurableAgentRuntimeController.SendMessage` stamps `dispatcher.WithCallerType(ctx, dispatcher.CallerBackground)` before calling `HandleMessage`; `HandleMessage` defaults to `CallerChat` but honors a stamped ctx value when present/valid. Verified the two real HTTP callers (`harness_v1.go`, `messages.go`) stamp nothing and still resolve to `CallerChat`. Added regression tests exercising the ctx-stamping directly and end-to-end through `Dispatcher.Run`. `go build`/`go vet` (1 pre-existing unrelated finding)/`go test` all pass. No escalations.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
