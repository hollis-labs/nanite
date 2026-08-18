# Provider Abstraction & the LLM Round-Trip

> **Correction (2026-08-17, post-review):** this document originally described the CLI-wrapped provider family as "PTY"/"the CLI/PTY bridge" throughout, implying a real pseudo-terminal is allocated and its output parsed. That's wrong for what actually runs today. Verified directly against source during a later review pass:
>
> - `internal/runtime/agent/factory.go`'s `shouldUsePTY` returns **`false` for every provider, unconditionally** — its own doc comment states this outright and explains why: an earlier design *did* route Claude's long-lived sessions through a real PTY running Claude's interactive TUI, whose output is ANSI/screen-redraw codes with no structured content to parse — "sessions ran forever with zero assistant deltas surfaced" (ticket c202). That design was abandoned as a formal architecture decision (`decisions.nanite.architecture.cli_pty_long_lived_default`).
> - What's live in production (`cmd/nanite/main.go:800`) is `provider.NewClaudeAdapterStreamingStdio()` — Claude's own "Streaming Input Mode": a long-lived subprocess exchanging **NDJSON over plain stdin/stdout pipes**, no pseudo-terminal device involved, parsed by a real JSON parser (`parseClaudeStreamLine`). Codex and OpenCode use a different runtime kind — a fresh subprocess per turn, same structured stream-json output, also no PTY.
> - The `pty`/`pty-claude`/`pty-codex`/`pty-opencode` provider-name strings that appear throughout this document (and in the live DB, e.g. `sessions.provider`) are a **naming convention only** — `internal/chat/engine.go`'s `NormalizeCLIProvider` strips the `pty-` prefix before any runtime decision is made. The label survives from before the pivot away from real PTY; it does not describe what happens at runtime.
>
> Every "PTY" reference below should be read as "the CLI-wrapped subprocess path" — a long-lived or per-turn subprocess exchanging structured stream-json, not a terminal-emulation mechanism. The distinction matters for anyone reasoning about that path's reliability: there is no TTY-attach/detach lifecycle or ANSI-parsing fragility in play, only ordinary subprocess/pipe management. Left uncorrected below as originally written, for audit-trail purposes — treat "PTY" everywhere in this document as this note describes.

## 1. Purpose

This document traces the literal wire-level path a chat turn takes between
"the prompt is assembled" and "the final response is persisted": the call
into an LLM provider, the streamed (or non-streamed) response coming back,
detection of `tool_use` blocks, the handoff to tool execution, and the loop
back into the provider with `tool_result` blocks appended — repeating until
the model stops requesting tools. It also covers where that loop diverges
for the CLI/PTY-bridge provider family, which does not talk to an HTTP API
at all. Prompt *assembly* (slots, system prompt, cache markers) is the
Context Broker's concern (sibling doc `05-context-broker-slot-system.md`);
this doc picks up once a `llmtypes.ChatRequest` is ready to send and stops
once the SSE stream to the frontend and the DB rows are written.

## 2. Key entry points/files

- `internal/service/chat_generate.go` (`generateResponse`, line 124) — the chat-harness function that owns the whole turn, including the outer tool-use loop (`for ls.iteration = 0; ; ls.iteration++`, line 802) and the per-iteration stream-consumption loop (`streamLoop:`, line 1351).
- `internal/service/chat_tool_executor.go` — `preCheckTools`, `executeToolBatch`, `postProcessToolResults`: the tool-execution handoff invoked when a streamed turn ends with `stop_reason == "tool_use"`.
- `internal/service/chat.go` (`resolveProvider`, line 1122; `tryProviderCandidate`, line 1105) — walks session → agent → `user_settings.default_provider` → fallback chain → model-inferred provider to pick which `llmcontracts.Provider` (or CLI alias) handles the turn.
- `internal/service/chat_rate_budget_pause.go` — the rate-budget pause/auto-retry side-loop that wraps `StreamChat` calls when the request would exceed the observed per-minute token budget.
- `github.com/hollis-labs/go-llm-contracts` (checked out at `/Users/chrispian/dev/hollis-labs/libs/go-llm-contracts`) — defines the `Provider` interface (`StreamChat`, `Complete`, `Capabilities`) plus optional capability interfaces (`RateLimited`, `Cacheable`, `CacheableProvider`) that adapters may also implement.
- `github.com/hollis-labs/go-llm-types` (`/Users/chrispian/dev/hollis-labs/libs/go-llm-types/types.go`) — the provider-agnostic wire vocabulary: `ChatRequest`, `ChatMessage`, `ContentBlock`, `StreamEvent`, `EventType` (`delta`, `tool_use`, `usage`, `error`, `done`, `session_id`, `thinking`), `Usage`.
- `internal/llm/anthropic/` — SDK-backed (`anthropic-sdk-go`) wrapper implementing `Provider` for the Anthropic Messages API: `client.go` (construction, capabilities, cache-hint plumbing), `stream.go` (`StreamChat` + SSE event translation), `complete.go` (non-streaming `Complete`), `middleware.go` (rate-limit header calibration + circuit breaker), `cache_plan.go` (prompt-cache marker placement), `errors.go` (error → sentinel translation).
- `internal/llm/openai/` — SDK-backed (`openai-go`) wrapper implementing `Provider` for OpenAI's Chat Completions API: `client.go`, `stream.go`, `params.go`, `tools.go`, `errors.go`. No rate tracker or circuit breaker.
- `cmd/nanite/main.go` (`initProviders`, line 705) — the actual, current provider registration: **only `anthropic` and `openai` are constructed and registered** as `llmcontracts.Provider` today. A code comment records that the catalog was reduced from a larger set (gemini, mistral, azure-openai, openrouter, openzen, ollama) in "Step 6.5 (SP-20260508-0001)"; those adapters were deleted from `go-providers` and never re-implemented behind the new `internal/llm` seam.
- `internal/runtime/agent/agent.go` (`Boot`, line 276) and `internal/service/chat_boot_drive.go` (`driveBootSession`, line 73) — the separate CLI/PTY-bridge path. CLI-provider turns never call `Provider.StreamChat`; they spawn/attach a long-lived or per-turn CLI subprocess and translate its own stream-json/PTY output into the same `<-chan llmtypes.StreamEvent` shape so the rest of `generateResponse` can treat both paths uniformly.
- `github.com/hollis-labs/go-providers` (checked out at `/Users/chrispian/dev/hollis-labs/libs/go-providers`) — the library `internal/runtime/agent` uses to actually spawn and parse the Claude/Codex/OpenCode CLIs (`pty_claude.go`, `pty_codex.go`, `bootdir_*.go`, `subprocess.go`). It is no longer used for the `llmcontracts.Provider` registry path (that registration was deleted in "Phase 4c.6 / CW-20260508-0002").
- `internal/tool/cache.go` (`ResultCache.StoreResult`) — the tool-result cache-and-pointer mechanism (Phase 3 S4a) that plugs into `postProcessToolResults` before a tool result is appended back into the conversation.
- `internal/mcp/validate.go` — the MCP trust-tier validator (Phase 3 S4b) enforced at tool-discovery and tool-execution time, upstream of the result cache. See `docs/mcp-trust-model.md`.
- `internal/service/stream.go` (`StreamManager`, `messageStream`) and `internal/api/messages.go` / `internal/api/sse.go` — the SSE fan-out layer that turns `chat.StreamEvent` values pushed onto the per-generation channel into `text/event-stream` output, with a ring buffer for reconnect-and-replay via `Last-Event-ID`.

**How a provider is selected today:** `chat.go:resolveProvider` walks session-explicit provider → agent-profile default provider → `user_settings.default_provider` → `user_settings.ProviderFallbackChain` → `chat.InferProvider(model)` (prefix-matching on the model string). At every step, a name that is not a registered `llmcontracts.Provider` but matches the CLI-alias shape (`pty`, `pty-*`, `sub-*`) is returned as `(name, nil)` and routed to the CLI bypass; any other unregistered name falls through to the next candidate, and a fully unconfigured installation surfaces a configuration error rather than silently defaulting to Anthropic (`chat.go:1076`, "the unconditional... catch-all... has been removed").

## 3. Flow

### 3.1 Narrative

1. **Request assembly** (upstream of this doc — see the Context Broker doc) produces `chatMessages []llmtypes.ChatMessage`, `tools []llmtypes.ToolDefinition`, a system prompt / slot blocks, and `cacheStrategy []llmcontracts.CacheHint`. `generateResponse` also resolves `model` and `providerName` per-iteration (`chat_generate.go:892-917` re-runs `EnforceTokenBudget` every iteration, since tool results can grow the context between iterations).
2. **Provider dispatch** (`chat_generate.go:1097-1108`): if `chat.IsCLIProvider(providerName)`, the turn goes to `s.driveBootSession(...)`; otherwise `prov.StreamChat(provCtx, llmtypes.ChatRequest{...})` is called directly. Both return `(<-chan llmtypes.StreamEvent, error)` — the unifying seam between the two architecturally different paths.
3. **Streaming consumption** (`streamLoop:`, `chat_generate.go:1351-1518`): a `select` over the event channel and an inactivity timer (`ls.limits.idleTimeout`) that cancels the per-iteration stream context if the provider goes silent for too long (a stall, not a legitimate long tool call — tool execution happens *between* iterations, after this loop has already exited). Each event is switched on `evt.Type`:
   - `delta` — appended to the running text buffer and buffered for a later phase-tagged flush to the frontend (narration vs. final, decided once `stop_reason` is known).
   - `tool_use` — appended to `toolUseBlocks`; for CLI providers this also drives `tool_pending`/`tool_resolved` presence broadcasts to the UI.
   - `usage` — accumulated into `finalUsage` (input/output/cache-creation/cache-read tokens) and captures `stop_reason` off `Usage.StopReason`.
   - `thinking` — a completed interleaved-thinking block (Anthropic-specific; see §4), persisted verbatim for round-trip signature validation and streamed to the frontend as a `PhaseThinking` delta.
   - `error` — mid-stream provider failure. If the message matches a context-overflow or rate-budget-exceeded shape, the compaction-recovery path retries; otherwise the turn ends with an error envelope and a partial-assistant-message persist.
   - `done` — no-op in the switch; the loop simply exits when the channel closes.
4. **Post-stream decision** (`chat_generate.go:1628`): if `stop_reason != "tool_use"` or no tool_use blocks were collected, the outer loop `break`s — the turn is finished.
5. **Tool-use round trip** (only when `stop_reason == "tool_use"`):
   - The assistant's turn (thinking blocks, narration text, `tool_use` blocks) is appended to `chatMessages` as one `ChatMessage{Role: "assistant"}` (`chat_generate.go:1644-1670`).
   - `request_tools` meta-tool calls are split out and handled inline (progressive tool disclosure — see the tool-calling/MCP sibling doc); the rest go through `preCheckTools` (permission/blocked/concurrency-safety classification) → `executeToolBatch` (concurrent-safe tools run in parallel via a `sync.WaitGroup`, the rest serially) → `postProcessToolResults`.
   - Inside `postProcessToolResults`, each result is routed through stuck-loop detection, then the **cache-and-pointer gate** (`internal/tool/cache.go`): results over `DefaultSoftTruncBytes` get stored in `tool_result_cache` and replaced with a truncated view plus a `tool_result://<id>` footer; results under the threshold, errors, scratchpad tools, and a small exempt-tool list pass through untouched. Anything the cache didn't already size gets a final `truncate.OutputForModel` pass.
   - Results become `llmtypes.ContentBlock{Type: "tool_result", ToolUseID, Content, IsError}` and are appended as one `ChatMessage{Role: "user"}` (`chat_generate.go:1719-1721`).
   - The outer `for` loop iterates (`ls.iteration++`), re-runs budget enforcement, and calls the provider again with the now-longer `chatMessages` — this is the loop-back.
6. **Termination** happens on: no more tool_use (`break` at 1641), a layered iteration-limit hit (`ls.shouldStop()`, checked at the top of every iteration — soft `max_turns` warning, consecutive-failure cap, runaway-tool-failure hard breaker, idle timeout), a circuit-breaker trip (Anthropic only — see §4), `max_tokens` truncation, or a subagent literal `directReturn`.
7. **Persistence** (`chat_generate.go:2004-2065`, after the loop): the assistant message (structured content + envelope JSON + metadata, including narration and signed thinking blocks) is written via `s.store.CreateMessage`; token usage is written via `s.store.RecordUsage` (only when `finalUsage` carries nonzero tokens — CLI providers that never emit a `usage` event leave this table row absent); and one `execution_metrics` row is written per turn with `adapter` classified as `http`, `pty`, or `sub`.
8. **SSE delivery** happens continuously throughout, not just at the end: every `ch <- chat.StreamEvent{...}` send in the above steps lands on a per-generation channel created by `StreamManager.CreateStream`; `internal/api/messages.go`'s SSE handler subscribes to that stream (`Services.Streams.Subscribe`) and writes each event as `event: <type>\ndata: <json>\n\n`, tagging it with an `id:` line from a ring buffer so a reconnecting `EventSource` can replay via `Last-Event-ID`.

### 3.2 Sequence diagram (one turn, including the tool-use round-trip)

```mermaid
sequenceDiagram
    participant FE as Frontend (SSE client)
    participant SM as StreamManager
    participant Loop as generateResponse<br/>(outer tool-use loop)
    participant Prov as llmcontracts.Provider<br/>(Anthropic/OpenAI adapter)
    participant API as Provider HTTP API
    participant Exec as Tool executor<br/>(preCheck/executeBatch/postProcess)
    participant MCP as MCP Manager<br/>(trust-tier validator)
    participant Cache as ResultCache<br/>(tool_result_cache)
    participant DB as Store (SQLite)

    Loop->>Loop: EnforceTokenBudget(chatMessages, tools)
    Loop->>Prov: StreamChat(ctx, ChatRequest{messages, tools, cacheHints})
    Prov->>API: POST (streaming), rate-budget preflight + pacing
    activate API
    loop SSE chunks
        API-->>Prov: text_delta / input_json_delta / usage / message_stop
        Prov-->>Loop: llmtypes.StreamEvent{delta|tool_use|usage|thinking|done}
        Loop-->>SM: chat.StreamEvent{delta/tool_call/...}
        SM-->>FE: event: delta\ndata: {...}
    end
    deactivate API
    Note over Loop: channel closes -> stop_reason known

    alt stop_reason == "tool_use"
        Loop->>Loop: append assistant ChatMessage{tool_use blocks}
        Loop->>Exec: preCheckTools + executeToolBatch(plans)
        Exec->>MCP: CallTool (validated: block types, ANSI-strip, size cap by trust tier)
        MCP-->>Exec: ToolContent (capped at tier's MaxResultBytes)
        Exec->>Cache: StoreResult(sessionID, toolCallID, body)
        alt body > soft threshold
            Cache-->>Exec: truncated view + "tool_result://<id>" footer
        else under threshold
            Cache-->>Exec: body unchanged
        end
        Exec-->>SM: chat.StreamEvent{tool_call/tool_result}
        SM-->>FE: event: tool_call / tool_result
        Loop->>Loop: append user ChatMessage{tool_result blocks}
        Loop->>Prov: StreamChat(ctx, ChatRequest{...grown messages...})
        Note over Loop,Prov: loop back — repeat until stop_reason != tool_use<br/>or an iteration limit / circuit breaker fires
    else stop_reason != "tool_use"
        Loop->>DB: CreateMessage(assistantMsg)
        Loop->>DB: RecordUsage(token_usage) [if usage was emitted]
        Loop->>DB: RecordExecutionMetrics(execution_metrics)
        Loop-->>SM: chat.StreamEvent{stream_end}
        SM-->>FE: event: stream_end
    end
```

### 3.3 The CLI/PTY-bridge variant of step 2

For `chat.IsCLIProvider(providerName)` sessions, step 2 above is replaced
entirely: `driveBootSession` calls `runtimeagent.Boot` (which spawns or
attaches to a long-lived subprocess for `pty-claude`, or a fresh
subprocess-per-turn for `sub-codex`/`sub-opencode`), binds a per-turn event
router *before* calling `sess.SendInput([]byte(payload))` (ordering is
load-bearing — some CLI adapters run the turn to completion synchronously
inside `SendInput`), and fans the CLI's own stream-json/PTY output into the
same `llmtypes.StreamEvent` channel shape. See §4 for what is and is not
equivalent between the two paths.

### 3.4 Non-streaming `Complete`

`Provider.Complete` is a separate, simpler call used for one-shot utility
calls that don't need streaming or tools — the only production call site
found is `internal/service/container.go:1315`, wiring memory extraction
(`memory.UtilityCallFunc`). It is not part of the tool-use loop.

## 4. Provider divergence

- **Registered today vs. mentioned in comments.** Only `anthropic` and
  `openai` are constructed and registered as `llmcontracts.Provider` in
  `cmd/nanite/main.go:initProviders`. `pkg/models/registry.go` confirms this
  explicitly: `ProviderDefaults` only has entries for `anthropic` and
  `openai`, with a comment noting gemini/mistral/azure-openai/openrouter/
  openzen/ollama defaults were removed "alongside their adapters." Several
  other places in the codebase still narrate a larger set as if live —
  `chat_generate.go:1093-1094`'s comment lists "anthropic / openai /
  gemini-api / mistral / openrouter / openzen / azure-openai / ollama" as
  providers that "keep going through `provider.StreamChat`," and
  `chat.InferProvider` still prefix-routes `llama*`/`gemma*`/`mistral-7b*`/
  `*:*` model names to a provider literally named `"ollama"` — but no
  `"ollama"` entry is ever registered, so that inference path currently
  terminates in `resolveProvider`'s no-catch-all fallthrough (a
  "provider not available" error) rather than reaching a real adapter.
- **Rate limiting / circuit breaking is Anthropic-only.** `internal/llm/anthropic/client.go` carries a `TokenRateTracker` and `CircuitBreaker`, fed by `rateAwareMiddleware` (parses `x-ratelimit-*` response headers, records success/failure by status code) and consulted both as a pre-flight estimate in `StreamChat` and as a mid-loop check in `chat_generate.go:1758` (`if ap, ok := prov.(*nllmanthropic.Client); ok && ap.CircuitBreaker...`). `internal/llm/openai/client.go`'s doc comment states plainly it "does NOT implement `llmcontracts.RateLimited` and does NOT carry a `TokenRateTracker`/`CircuitBreaker`" — release-parity was explicitly deferred (`followups.nanite.cw_20260508_0012.openai_rate_budget_parity`). `chat_rate_budget_pause.go:providerRateTracker` type-asserts to `*nllmanthropic.Client` specifically, so an OpenAI 429 never enters the `rate_budget_pause` auto-retry SSE event path.
- **Error-sentinel translation differs.** Anthropic's `errors.go:translateError` maps 429s to `llmcontracts.ErrRequestExceedsRateBudget` (the sentinel that gates the whole rate-budget-pause / compaction-recovery machinery). OpenAI's `errors.go:translateError` only annotates the message with the HTTP status/code — it never produces that sentinel, so OpenAI 429s surface as an opaque wrapped error rather than triggering the same recovery UX.
- **Prompt caching is Anthropic-only.** `ChatRequest.CacheHints` flows into Anthropic's `cache_plan.go` (`planCacheMarkersWithHints`) to place `cache_control` markers; `internal/llm/openai/params.go`'s `buildChatParams` never references `CacheHints`. `Capabilities()` reflects this: Anthropic reports `SupportsSystemPromptCaching: true`, OpenAI does not set it.
- **Interleaved thinking is Anthropic-only** (`shouldEnableInterleavedThinking`, gated on both a reasoning-config context value and a model-name-pattern check for `claude-{opus|sonnet|haiku}-4...-<date>=20250514`). OpenAI's stream translator has no `thinking` event case.
- **The CLI/PTY bridge is not an HTTP client at all.** `driveBootSession` doesn't build an HTTP request — it manages a subprocess (persistent PTY for `pty-claude`, spawn-per-turn for `sub-codex`/`sub-opencode`) via `internal/runtime/agent.Boot`, using `go-providers`' `pty_claude.go`/`bootdir_*.go` underneath. The CLI's own permission system and its own built-in tools (Bash, Read, Edit, etc., configured via a planted `.claude/settings.json` / bootdir) are what actually execute tool calls in that mode — not nanite's MCP toolset. `tool_use` events surfaced from the CLI's stream-json are consumed by `streamLoop` for UI presence signaling (`tool_pending`/`tool_resolved`) and auto-artifact creation; whether the CLI ever reports a turn-ending `stop_reason == "tool_use"` that would route into nanite's own `preCheckTools`/`executeToolBatch`/`ResultCache` pipeline was not confirmed in this pass (see §8).
- **Two CLI-bridge flavors exist**, distinguished by `chat.IsPTYProvider` vs. the bare `sub-` prefix: PTY (`pty-*` — a long-lived process nanite talks to over its stdio, e.g. `pty-claude`) and per-turn subprocess (`sub-*` — a fresh process spawned for each turn, e.g. `sub-codex`, `sub-opencode`, per the `--input-format stream-json` / `exec` mode comments in `main.go`).
- **`Complete` behavior differs slightly.** Anthropic's `Complete` returns an error when the response has no text blocks (distinguishing "no content" from "tool_use only"); OpenAI's `Complete` returns the first choice's message content directly (or `errEmptyResponse` if there are no choices) and does not surface tool calls at all.

## 5. Data model touched

| Table | Written by | What |
|---|---|---|
| `messages` | `chat_generate.go` (`s.store.CreateMessage`) | One row per assistant turn: `content` (structured/enveloped JSON via `chat.WrapResponse`), `envelope` (parsed envelope JSON), `metadata` (narration text under `thinking`, signed interleaved-thinking blocks under `thinking_blocks`). |
| `token_usage` | `chat_generate.go` (`s.store.RecordUsage`) | Input/output/cache-creation/cache-read tokens, per assistant message, per model — only written when `finalUsage` accumulated nonzero tokens during the stream loop (a provider/path that never emits a `usage` `StreamEvent` leaves no row). |
| `execution_metrics` | `chat_generate.go` (`s.store.RecordExecutionMetrics`) | One row per turn: `provider`, `adapter` (`http`/`pty`/`sub`), `model`, duration, `context_messages`, `tool_iterations` (= final `ls.iteration`), `tool_calls`, `stop_reason`, optional debug snapshots. |
| `tool_result_cache` | `internal/tool/cache.go` (`ResultCache.StoreResult`), invoked from `postProcessToolResults` | `id` (ULID), `session_id`, `tool_name`, `tool_call_id`, `byte_size`, `was_truncated`, `body` (nullable — null once `byte_size` exceeds the hard cap, metadata-only). Read back by the `fetch_tool_result` / `search_tool_result` meta-tools. |
| `providers` / `models` | Seeded by `internal/store/seed.go` from `pkg/models.AllSeeded()`; read via `store.ResolveProviderAndModel` / admin API (`internal/api/provider_manage.go`) | Catalog/metadata rows (display name, context window, pricing, `is_enabled`) used for default-resolution and the UI dropdown — **not** the live source that constructs the two registered `llmcontracts.Provider` instances (see §6). |
| `broker_decisions`, `event_log` | Various `s.store.LogEvent` calls scattered through the loop (`provider_error`, `provider_stream_stalled`, `max_tokens_truncation`, `envelope_error`, `envelope_retry`) | Operational/audit trail of loop-level events, not the turn content itself. |

## 6. Configuration & manual-setup points

- **Registering a new HTTP-API provider is a code change, not a config
  change.** It requires: (1) a new `internal/llm/<name>/` package
  implementing `llmcontracts.Provider` (`StreamChat`, `Complete`,
  `Capabilities`); (2) a new literal entry in the `apiProviders` slice inside
  `cmd/nanite/main.go:initProviders` (name, display name, catalog row ID,
  constructor, `setKey` closure); (3) a `ProviderDefaults` entry in
  `pkg/models/registry.go` if per-provider capability defaults are wanted;
  (4) `allModels` entries for any model IDs that should resolve through
  `models.ProviderFor`/`ModelByID` without falling back to prefix-matching
  or the seedcatalog floor. None of this can be done from the admin UI or a
  DB row alone.
- **The `providers` DB table's `base_url` and `api_key` columns are not
  consumed by request construction today.** `store.ProviderConfig.BaseURL`
  is read and written only inside `internal/store/providers.go`'s CRUD
  methods (list/get/update) and the admin API surface
  (`internal/api/provider_manage.go`); no code path in `internal/llm/anthropic`
  or `internal/llm/openai` reads a DB-stored base URL when constructing the
  SDK client. The two registered clients use fixed default endpoints (or
  `OPENAI_API_KEY`/keychain-resolved keys) set once at `initProviders` time.
- **API keys** are resolved from the OS keychain via `secrets.Get(secrets.ProviderKeyName(providerID))` at `initProviders` time (using the catalog row IDs `"anthropic-001"` / `"openai-001"`), not read per-request from the `providers` table.
- **Model metadata can be refreshed at runtime without a rebuild, within limits.** `container.go` wires a `modelsdev.Client` (`github.com/hollis-labs/go-modelsdev`, the models.dev catalog) with `WithOnRefresh(syncCatalogToRegistry)`; `syncCatalogToRegistry` calls `models.SyncFromCatalog`, which overlays context-window/max-output/pricing data onto `pkg/models`' in-memory registry for any model ID models.dev knows about. This updates *numbers* for existing or catalog-only model IDs; it does not add a new `Provider` binding or route a wholly new provider name.
- **Default provider/model resolution** is centralized in `store.ResolveProviderAndModel` (`internal/store/defaults.go:74`) — the walk is explicit args → `user_settings.default_model`/`default_provider` → `providers.default_model` — and is described in-code as the SSOT specifically to avoid the earlier bug class where a hardcoded Go literal model string silently masked misconfiguration.
- **Adding a CLI/PTY provider** goes through a different surface entirely — `internal/runtime/agent`'s adapter index and `go-providers`' `CLIAdapter`/bootdir types, gated by `factory.shouldUsePTY`; a comment in `main.go` notes several previously-registered CLI adapters (gemini, copilot, aider, junie, kiro, qwen) were pruned because `factory.shouldUsePTY` never matched their shapes in production.

## 7. Cross-references

- `05-context-broker-slot-system.md` — owns everything upstream of this doc: how `chatMessages`, `SlotBlocks`, the system prompt, and `CacheHints` are assembled before the first `StreamChat` call of a turn.
- `07-tool-calling-mcp.md` — owns the tool-execution internals this doc only skims: `preCheckTools`/`executeToolBatch` permission and concurrency logic, MCP server discovery/naming/collision resolution, and the full trust-tier validator behavior (this doc only shows where it sits in the round-trip, between the provider's `tool_use` event and the `ResultCache`).
- `02-boot-process.md` / `09-durable-agents-runtime.md` — likely owners of the `internal/runtime/agent.Boot` / CLI-session internals that `driveBootSession` delegates to; this doc treats that as a boundary and only describes the `llmtypes.StreamEvent` shape crossing it.
- `docs/mcp-trust-model.md` — authoritative reference for the trust-tier ceilings (`builtin` 2 MiB / `plugin_stdio` 512 KiB / `plugin_http` 256 KiB / `third_party_http` 128 KiB) enforced before a tool result ever reaches `ResultCache.StoreResult`.

## 8. Open questions

- **CLAUDE.md's stated tool-result-cache threshold (64 KiB) does not match the code.** The project-level CLAUDE.md describes Phase 3 S4a as caching "results over 64 KiB"; `internal/tool/cache.go`'s `DefaultSoftTruncBytes` is `2 * 1024` (2 KiB) today. Code comments document the same 65536→2048 drop from two angles: the Go constant's comment (CW-20260419-0004 Part 1) says 64 KiB was too high because "nothing in practical use ever hit the cache path"; migration `021_lower_soft_trunc_default.sql` (CW-20260419-0018, "UAT c17") says an 89 KiB result bypassed the cache-pointer gate and blew the per-minute rate budget. Whichever event CLAUDE.md's "64 KiB" line was originally written against, the value has since moved and the doc wasn't updated to track it.
- **Whether CLI-bridge `tool_use` events can ever drive nanite's own tool executor is unconfirmed.** The post-stream branch in `chat_generate.go` (`if stop_reason != "tool_use" ... break`) is provider-agnostic — it would run `preCheckTools`/`executeToolBatch`/`ResultCache` for a CLI-provider turn exactly as for an HTTP-provider turn if the CLI's translated `Usage.StopReason` were ever `"tool_use"`. Given the CLI runs its own permission-gated tool execution internally before signaling turn completion, this branch may be effectively dead for CLI providers in practice — but nothing in the code makes that guarantee explicit, and this pass did not trace a live CLI turn to confirm either way.
- **Several code comments describe a wider provider roster (gemini-api, mistral, openrouter, openzen, azure-openai, ollama) as if currently live**, while `main.go:initProviders` and `pkg/models/registry.go` both state explicitly that only Anthropic and OpenAI remain after "Step 6.5." `chat.InferProvider` still special-cases Ollama-shaped model names (`llama*`, `gemma*`, `model:tag`) and routes them to a provider literally named `"ollama"` that has no registration — meaning a user selecting a locally-hosted Ollama-style model name today would resolve to `(name="ollama", provider=nil)` and hit the non-CLI-shaped fallthrough in `resolveProvider`, surfacing a configuration error rather than a working local-model path. Whether this is intentional (Ollama support removed, comment/routing not yet cleaned up) or an in-progress gap was not determined.
- **The `providers` table's `base_url` column appears to model a capability the runtime doesn't currently use** (per §6) — it's exposed through the full CRUD/admin-API surface but has no observed effect on where a request is sent. Whether this is vestigial from a pre-"Step 6.5" architecture where more providers (and presumably per-row endpoint configuration) were live, or scaffolding for a not-yet-wired feature, wasn't determined from the code alone.
- **The rate-budget preflight estimate is a byte-count heuristic** (`len(payload) / 4` minus an estimated cacheable-prefix byte count, in `anthropic/stream.go:StreamChat`), not a real tokenizer call — noted here as observed behavior, not a defect judgment.
- **`Complete` and `StreamChat` disagree on empty-response handling** across providers (Anthropic errors on zero text blocks; OpenAI returns `errEmptyResponse` only on zero choices, but would return an empty string for a zero-length message on a present choice) — a caller switching between the two non-streaming call sites would see different failure shapes for what might be the same underlying condition.
