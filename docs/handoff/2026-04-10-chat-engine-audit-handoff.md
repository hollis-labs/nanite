# Chat Engine Audit Handoff — 2026-04-10

## Mission

Act on the chat engine deep-review audit: 3 Critical, 4 High, 3 Medium, 1 grouped Low. **Finding 01 (`scope_guard.go` is dead code) is significant enough that the receiving agent must bring it to the user before taking any action.** Everything else is actionable on normal backend judgment.

## Inputs

- **Audit:** `docs/audits/2026-04-10-chat-engine/` — `index.md` plus `01`–`12`.
- **Audit index:** `docs/audits/INDEX.md` — cross-cutting themes plus the new "dead code claiming to be defense" entry finding 01 created.
- **Reviewer context:** `.nanite/agents/reviewer-backend.md` — trust boundaries, "DO NOT re-flag" list, and the original framing of `scope_guard.go` as the prompt-injection defense that finding 01 overturns.

## Framing corrections

No time estimates. No release-readiness gating. Severity reflects technical risk only. Execution is autonomous parallel agents; human-team sequencing does not apply.

## HEADLINE — `scope_guard.go` is dead code (finding 01)

**Stop and read this finding before any other work.** The reviewer-backend context names `pkg/provider/scope_guard.go` as the chat engine's prompt-injection containment layer. The claim is false on every axis:

- `NewScopeGuard` and `NewEventReactionPipeline` have **only test callers** (two hits, both in `pkg/provider/event_pipeline_test.go`).
- The chat engine calls providers directly at `internal/service/chat_generate.go:L337-L339` — `prov.StreamChatWithTools` straight into the loop, no pipeline wrap.
- The entire `pkg/provider/event_pipeline.go` wrapper is dead code — not just `ScopeGuard`, also the cost monitor and progress tracker it wraps.
- Even if wired, implementation is `strings.Contains` keyword matching with `DefaultEventReactionConfig()` setting `AllowedScopes: ["*"]` and `ScopeViolationMode: "log"` (never terminate).

**Do not unilaterally pick a fix.** Multiple paths are defensible with different downstream implications:

- **(a)** Wire the existing implementation as-is and accept its weakness.
- **(b)** Replace with a real design — token-level tool-arg validation at dispatch, integration with `internal/permission/`, default-deny.
- **(c)** Delete `scope_guard.go`, `event_pipeline.go`, their tests, and update `.nanite/agents/reviewer-backend.md:L63-L65` to document that enforcement lives in `internal/permission/` and `internal/sandbox/`.
- **(d)** Something else.

This changes how Nanite describes its security posture in docs, the reviewer-backend card, and any threat model referencing the file. **Bring this to the user before acting.** Detail: `01-critical-scope-guard-dead-code.md`.

## Other Critical findings

- **02 — No panic recovery in `generateResponse`.** `HandleMessage`, `RetryLastMessage`, `SendAgentMessage`, `DelegateTask` spawn `generateResponse` detached under `context.WithoutCancel`. The router's `recoverMiddleware` (`server.go:L123-L133`) does not cover it, and `generateResponse` has no top-level recover (`chat_generate.go:L46-L71`). `FilterRegistry.Apply` (`internal/plugin/filter.go:L131-L155`) has no per-handler recover — any filter panic crashes the server. Fix: top-level recover emitting an `ErrorEvent`, plus per-handler recover at `filter.go:L144-L155`. Detail: `02-critical-no-panic-recovery-in-generate-response.md`.
- **03 — Shared `*provider.Anthropic` callback race.** `generateResponse` writes `OnStatus`, `OnCircuitOpen`, `cacheHints` directly on the registry singleton (`chat_generate.go:L132-L148`; `pkg/provider/anthropic.go:L25-L34`). Concurrent sessions overwrite each other's callbacks with no happens-before — events land on the wrong SSE channel. Fix at the call site via context-scoped callbacks (`WithStatusCallback`, `WithCircuitOpenCallback`, `WithCacheHints`) — same idiom already used for `WithSandboxDir` / `WithCLISessionID` at `chat_generate.go:L900-L921`. Detail: `03-critical-shared-provider-callback-race.md`.

## High findings

- **04 — Stream channel backpressure leaks goroutines.** Every `ch <- event` in `chat_generate.go` / `chat_tool_executor.go` is a blocking send ignoring `ctx.Done()`. Disconnected SSE clients leak goroutines, provider streams, and tool subprocesses for the full timeout. `executeToolBatch` holds `sync.Mutex` around a blocking send, deadlocking concurrent tool batches. Fix: context-aware `sendOrDrop` helper plus drain in `handleStream`. Detail: `04-high-stream-channel-backpressure-goroutine-leak.md`.
- **05 — User-forged envelope injection.** `generateResponse` scans `userContent` for `<!--TICKET_DATA:...:TICKET_DATA-->` (`chat_generate.go:L597-L609`) and injects an assistant-attributed envelope, persisted and streamed. Same class in `captureEnvelopeData` for tool results (`L862-L878`). Cross-session vectors via `SendAgentMessage` (`chat.go:L216-L244`) and `DelegateTask` title/description (`delegation.go:L103-L105`). Detail: `05-high-user-forged-envelope-injection.md`.
- **06 — Context broker unsanitized injection.** `enrichWithContextBroker` appends raw `FormatPacket` output to the system prompt with no delimiter hardening (`context_client.go:L302-L348`; `internal/contextbroker/broker.go:L270-L299`). `FilterUserMessage` scrubs only the in-memory copy; the DB holds raw, re-feeding the broker via session source. Detail: `06-high-context-broker-unsanitized-injection.md`.
- **07 — `DelegateAndAggregate` unbounded goroutines.** One `go func` per sub-task, no `errgroup` limit, no per-goroutine recover, no post-LLM cap on `len(decomposition.SubTasks)`, no per-sub-task timeout (`delegation.go:L253-L290`). Decomposer's "2-6 items" is LLM-enforced only. Also byte-slice title truncation at `L229-L233`. Detail: `07-high-delegate-and-aggregate-unbounded-goroutines.md`.

## Medium findings

- **08** — `ClassifyError` substring-matches "rate", misclassifies any error containing the word.
- **09** — `ParseAgentConstraints` silently swallows `json.Unmarshal` errors at `internal/chat/engine.go:L31`.
- **10** — Scope mismatch: `internal/chat/` is a DTO grab-bag; the real engine lives in `internal/service/chat_generate.go` + `chat_tool_executor.go` + `delegation.go`.

## Cross-audit connections

- **02 ↔ plugin audit 04.** Both panic-recovery gaps, **separate fixes.** Plugin covers `Host.EmitEvent` and trigger dispatch; this covers the chat-service goroutine and `FilterRegistry.Apply`. Do not merge.
- **03 ↔ queued `provider-abstractions`.** Root cause is the mutable-field design in `pkg/provider/anthropic.go`; queued scope should audit OpenAI/Gemini/Ollama/Mistral.
- **04 ↔ sandbox PTY concerns.** Disconnected SSE plus long-running `pty-*` means the PTY output buffer also fills, cascading the block.
- **05 ↔ queued `envelope-system-and-plugin-rendering` (INDEX.md §16-17).** Frontend must verify envelope-initiated actions aren't privileged at render time; backend-only fix is insufficient.
- **07 ↔ queued `worker-lifecycle-regression` (INDEX.md §8).** Flags the parent's unbounded spawn; queued scope covers worker-package concurrent-spawn safety.

## Praise to preserve

From `12-info-praise-and-notes.md`. Do NOT regress while fixing the bugs around them: `loopState` + typed `ContinueSite` constants in `chat_loop_state.go`; the three-phase tool executor split (`preCheckTools` → `executeToolBatch` → result assembly); `EnforceTokenBudget` cascade with `TokenBreakdown`; real-SQLite test harness in `engine_test.go` / `context_client_test.go`; flat-struct DTO discipline (`chat.StreamEvent`, `SubTaskResult`, `SubTask`); config-struct constructor for `chatServiceImpl` (new deps extend the config, not the signature).

## Things to verify

- **Run `go test -race` against `internal/chat/...` and `internal/service/...`** with a synthetic test calling `HandleMessage` twice concurrently against a fake provider that invokes `OnStatus` mid-stream. Should fire finding 03 immediately; if not, the race model is wrong.
- Panic-injection test for finding 02: register a filter handler that panics, confirm the server stays up.
- Confirm `FilterUserMessage` contract before finding 06 option (a) — persisting filter output is a behavior change.
- Walk every `ch <-` in `chat_generate.go`, `chat_tool_executor.go`, `stream.go`, `delegation.go` before finding 04 lands.
- Grep `pkg/provider/` for other adapters with the same `OnStatus` / `OnCircuitOpen` mutable-field pattern before shipping 03.
- `go.mod` check for `golang.org/x/sync/errgroup` before 07.

## What's NOT in this handoff

- Time estimates, release-window judgments, "fits in" analysis, human-team sequencing.
- Tooling sweep — queued `chat-engine-tooling-and-tests`.
- Per-source context broker audit — queued `context-broker-sources`.
- `internal/memory/` and `internal/contextbroker/` internals — queued `memory-and-context-broker`.

## Suggested first move

**First, read `01-critical-scope-guard-dead-code.md` and bring it to the user.** Present the four options from the HEADLINE section and wait for a decision. Do not edit `scope_guard.go`, `event_pipeline.go`, or the reviewer-backend card until the decision lands. **Then** start with finding 02 (panic recovery) — smallest fix, no cross-cutting dependencies. **Then** finding 03 (provider callback race) — race-detector confirmable, and the context-callback pattern is already idiomatic in the codebase. **Treat finding 05 as a frontend-coordinated fix, not backend-only** — a backend `TICKET_DATA` deletion alone leaves `captureEnvelopeData` and the delegation title carrier open; coordinate with the queued `envelope-system-and-plugin-rendering` scope before landing the backend change.
