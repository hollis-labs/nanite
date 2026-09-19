# Chat stream pipeline

How a model's streamed output becomes what an `/api/harness/v1` (or legacy) subscriber receives, what is stored on the way, and where it differs from the public provider streaming specifications: Anthropic Messages streaming, OpenAI Chat Completions streaming, OpenAI Responses streaming, and WHATWG Server-Sent Events.

Citations are `symbol` in `path` (Nanite-repo-relative; libraries by module name). "Verify" commands at the end re-find the load-bearing claims.

## 1. Hops

```
 provider ──A──▶ adapter ──B──▶ generation loop ──C──▶ StreamManager ──D──▶ SSE subscriber
 (HTTPS SSE,     (llmtypes.       (chat.StreamEvent      (ring + 1             (harness v1 or
  stdio, RPC)     StreamEvent)     on `produce`)          subscriber)           legacy route)
                                        └──▶ SQLite (assistant row, usage) at end of turn
```

| Hop | From → to | Transport | Vocabulary | Defined in |
|---|---|---|---|---|
| A | provider → adapter | HTTPS SSE (Anthropic, OpenAI); stdio NDJSON (Claude CLI, Codex CLI); stdout text (OpenCode CLI); JSON-RPC over stdio (ACP) | provider-native | `internal/llm/anthropic`, `internal/llm/openai`; go-providers, go-agent-wrapper |
| B | adapter → loop | Go channel `<-chan llmtypes.StreamEvent` | 7 types + 1 Nanite-local (§2.2) | go-llm-types, go-llm-contracts |
| C | loop → StreamManager | Go channel `produce` (capacity 128) | `chat.StreamEvent` | `chat.StreamEvent` in `internal/chat/engine.go` |
| D | StreamManager → subscriber | HTTP SSE | `chat.StreamEvent` as JSON | `streamMessageEvents` in `internal/api/messages.go` |

Channel 1 (LLM ↔ Nanite) = A + B (§2). Channel 2 (Nanite → subscribers) = C + D (§3). A separate SSE feed, `host_runtime.v1`, is documented in `docs/architecture/host-runtime-feed.md`.

Nanite's internal message object is Anthropic-shaped: `llmtypes.ContentBlock` types `text | tool_use | tool_result | thinking`; tools carry `input_schema`; stop reasons use Anthropic's vocabulary (OpenAI finish reasons are mapped into it).

## 2. Channel 1: provider → Nanite

### 2.1 Adapters

| Provider | Transport / upstream protocol | Code |
|---|---|---|
| Anthropic API | HTTPS SSE, Messages streaming | `internal/llm/anthropic` (anthropic-sdk-go) |
| OpenAI Chat Completions | HTTPS SSE chunks | `internal/llm/openai` (openai-go) |
| OpenAI Responses | HTTPS SSE | `internal/llm/openai` (`reasoning.go` picks Responses vs Chat per model) |
| Claude Code CLI | one long-lived process, stdio NDJSON (`--output-format stream-json`) | go-providers, go-agent-wrapper, agentkit |
| Codex CLI | one process per turn, stdout JSONL (`exec --json`) | go-providers, go-runner |
| OpenCode CLI | one process per turn, plain-text stdout | go-providers, go-runner |
| ACP agents | JSON-RPC 2.0 over stdio; only when the agent profile's protocol is `acp` | go-agent-wrapper |

- API providers register only when a key resolves (keychain, then env). No Google, Ollama, OpenRouter, Mistral or Azure adapter exists.
- SDK-level retries are disabled in both HTTP clients.

### 2.2 Internal provider event (`llmtypes.StreamEvent`)

| Type | Fields |
|---|---|
| `delta` | `Content` |
| `tool_use` | `ToolUse{ID, Name, Input}` |
| `usage` | `Usage{InputTokens, OutputTokens, CacheCreationTokens, CacheReadTokens, StopReason}` |
| `thinking` | `ThinkingBlock{Thinking, Signature}` |
| `error` | `Error` |
| `done` | none |
| `session_id` | `SessionID` |
| `openai_response_output` (Nanite-local) | `Content` = JSON array of raw Responses output items |

No reasoning-token field. End of stream = channel close. The consumer (`consumeProviderIteration` in `internal/service/chat_generation_actions.go`) sums `usage` per field, treats `done` and `session_id` as no-ops, and has no default case.

### 2.3 Mapping

**Anthropic**

| Upstream | Internal |
|---|---|
| `message_start.usage` (input, cache creation, cache read) | `usage` immediately |
| `text_delta` | `delta` |
| `tool_use` block: `input_json_delta` fragments | appended to one accumulator (block index unused); at `content_block_stop` parsed into one `tool_use` (malformed → `{"_raw": …}`, empty → `{}`) |
| `thinking` block: `thinking_delta`, `signature_delta` | one `thinking{Thinking, Signature}` at block stop; only when interleaved thinking is enabled (§2.6) |
| `message_delta.usage.output_tokens`, `delta.stop_reason` | `usage{OutputTokens, StopReason}` (stop reason raw) |
| `message_stop` | `done` |
| SSE error, decode error, network error | `error` |

Not consumed: `ping`, `citations_delta`, `server_tool_use` and `web_search_tool_result` blocks, `redacted_thinking`, `message_start` `output_tokens`, `message_delta` input/cache/server-tool fields, `stop_sequence`. A clean EOF before `message_stop` emits neither `done` nor `error`.

**OpenAI Chat Completions**

| Upstream | Internal |
|---|---|
| `choices[].delta.content` | `delta` |
| `delta.tool_calls[]` | accumulated per `index`; emitted after stream end (entries missing id or name dropped) |
| final usage chunk (`include_usage` always set): prompt, completion, `cached_tokens`, `cache_write_tokens` | one `usage`, after tool_use, before `done` |
| `finish_reason` | `tool_calls`→`tool_use`, `stop`→`end_turn`, `length`→`max_tokens`, `content_filter` passthrough |
| stream end | `done` |

Not consumed: `refusal`, `logprobs`, annotations, `role`, `function_call`, `completion_tokens_details` (reasoning tokens), non-final usage. A stream that ends without `[DONE]` is not detected (the SDK reports no error); `tool_use`, `usage`, `done` are still emitted. Thinking blocks are dropped from requests.

**OpenAI Responses**

| Upstream | Internal |
|---|---|
| `response.output_text.delta`, `response.refusal.delta` | `delta` |
| `response.reasoning_summary_text.delta` | `thinking` (unsigned), one per delta |
| `response.completed` | `openai_response_output`; `tool_use` per `function_call` item (args from the final item); `usage` (stop reason `tool_use` if any call, else `end_turn`); `done` |
| `response.failed`, `response.incomplete` | `usage` (`max_tokens` if `max_output_tokens`, else `error`), then `error` |
| `error` | `error` |

Not consumed: every other event (`created`, `output_item.*`, `content_part.*`, `function_call_arguments.*`, `reasoning_text`, annotations, tool-search events), reasoning token counts. A stream that ends before `response.completed` → `error`. Requests set `include: reasoning.encrypted_content` and `store: false`; the raw output items are kept for the tool loop and replayed verbatim; they are never rendered.

**CLI and ACP**

| Source | Internal | Not consumed |
|---|---|---|
| Claude CLI (`stream-json`) | assistant text blocks (whole) → `delta`; `tool_use` → `tool_use`; `result` → `usage` (only if present) + `done`, or `error` | thinking blocks, `rate_limit_event`, unknown types; parse errors ignored |
| Codex CLI | `agent_message` → `delta`; `turn.completed` → `usage` (no stop reason) + `done`; failures → `error` | reasoning and command items (no `tool_use`); `reasoning_output_tokens` |
| OpenCode CLI | each stdout line → `delta` | everything else; `done`/`error` synthesized from process exit |
| ACP | `agent_message_chunk` → `delta`; `agent_thought_chunk` → `thinking` (unsigned); `tool_call`, `tool_call_update` → runtime tool events; prompt-result `usage` → `usage` + `done` | `stopReason`, plan/mode/command updates |

The CLI runtime-to-loop path sends non-blocking and drops events when its buffer is full. The Claude CLI reader has a 1 MiB line limit and does not check the scanner error.

### 2.4 Tool arguments, usage, stop reason, reasoning by provider

| | Anthropic | OpenAI Chat | OpenAI Responses | CLI / ACP |
|---|---|---|---|---|
| Tool arguments | assembled from fragments; one `tool_use` at block stop | assembled by index; emitted after stream end | taken from the final item at `response.completed` | complete (Claude CLI, ACP); none (Codex, OpenCode) |
| Usage | input+cache at start; output at `message_delta` | one final chunk | `response.completed` | at turn end, when present |
| Stop reason | raw | mapped to Anthropic vocabulary | derived | Claude CLI `stop_reason` else `end_turn`; Codex none; ACP not read |
| Reasoning | consumed only if interleaved thinking enabled; signature kept | none in the API; tokens dropped | summary → unsigned `thinking`; encrypted items opaque | ACP thoughts → unsigned; Claude CLI and Codex dropped |

### 2.5 Failure handling

| Concern | Behavior |
|---|---|
| Rate budget (Anthropic only) | Pre-flight estimate (payload chars / 4 minus cacheable prefix / 4) against a tracked tokens-per-minute limit. Over limit → `ErrRequestExceedsRateBudget`; otherwise a paced wait with a `status` event every 10 s. Limit seeded at 50000, recalibrated from the `anthropic-ratelimit-input-tokens-limit` response header; env `NANITE_PROVIDER_RATE_BUDGET_TPM` overrides the seed. `Retry-After` is not read. OpenAI has no tracker. |
| Circuit breaker (Anthropic only) | 3 consecutive failures (429, ≥500, transport) open it for 30 s, then half-open. `RetryLastMessage` resets it. The `OnCircuitOpen` field is assigned but never called. |
| Error classification | Anthropic: typed sentinel errors (`translateError`). OpenAI: formatted string. The consumer classifies by substring (`ClassifyError` in `internal/chat/errors.go`). |
| Context overflow | A matching error triggers compaction and a retry of the iteration, up to 2 attempts; requires the `ContextOverflowRecovery` setting and a summarizer. |
| Recovery broker | HTTP stream errors are classified transient or permanent; transient → `RetryLastMessage` after 1 s, 2 s, 4 s… (cap 10 s), at most 3. |
| Inactivity watchdog | Consumer-side; reset by every provider event; 900 s (chat) / 300 s (subagent); on expiry cancels the provider context and emits a `stalled` error. Not present inside the adapters. |
| Cancellation | The provider context is passed to the SDK request. Anthropic emits `error` "context canceled". OpenAI and CLI paths close the channel with no error event. CLI stop: SIGTERM then SIGKILL after 5 s. ACP: `session/cancel`. |
| Truncated stream | Anthropic clean EOF before `message_stop`: no `done`, no `error`; the consumer sees an empty stop reason, which is not `tool_use`, so buffered text flushes as `final` and the turn completes normally. OpenAI Chat without `[DONE]`: same. Responses before `response.completed`: `error`. |

### 2.6 Request side

- **Anthropic.** `max_tokens` = request value or 16384 (the chat service passes none). System, tools, messages map to the Messages schema; tool_result is sent as text. Header `anthropic-beta: prompt-caching-2024-07-31` is always sent. `cache_control` markers: at most 4 (system prefix, last tool, last two user messages), default ephemeral TTL. `temperature`, `top_p`, `tool_choice`, stop sequences are not set.
- **Anthropic thinking.** `{type: enabled, budget_tokens}` is sent only for effort `high` (8000) or `max` (20000), and only when `modelSupportsInterleavedThinking` (in `internal/llm/anthropic/client.go`) accepts the model ID: `claude-{opus|sonnet|haiku}-4[-minor]-YYYYMMDD` with a date ≥ 20250514. The function returns false for any ID without an 8-digit date suffix, including the alias `claude-sonnet-4-5` and IDs of the form `claude-sonnet-5`; no thinking is requested for them.
- **OpenAI Chat.** `include_usage` true; `reasoning_effort` `none` when tools are present on the gpt-5.1 to 5.6 range. No cache hints.
- **OpenAI Responses.** `instructions`, `max_output_tokens`, `reasoning{effort, summary: auto}`, `include: reasoning.encrypted_content`, `store: false`, function tools with explicit `strict`.

## 3. Channel 2: generation loop → subscribers

### 3.1 Turn lifecycle

1. Handler (`handleSendMessage` legacy, `handleHarnessV1SendTurn` harness) validates, calls `HandleMessage`, and returns 202 `{message_id, stream_url}` before generation starts.
2. `HandleMessage` writes the user message row; `CreateStream` allocates `produce` and starts the pump goroutine; `launchGeneration` starts the generation goroutine on a context detached from the HTTP request and cancels any earlier generation on the session.
3. Generation emits `stream_start`, then loops: provider iteration → consume → tool settle.
4. `finalizeRun`: output filters, envelope parse, one `CreateMessage` INSERT, usage and metrics rows, then `stream_end`.
5. Deferred on return: `close(produce)`, `ScheduleCleanup` (60 s), presence `stream_end`.

### 3.2 Channels and buffers

| Channel | Capacity | Writer | Reader | When full |
|---|---|---|---|---|
| provider adapter → loop | Anthropic 64, OpenAI 16 | adapter | loop | adapter blocks |
| `produce` | 128 | generation goroutine (blocking); broadcast helpers (non-blocking) | pump | generation blocks; broadcast dropped |
| ring buffer | `ringBufferCapacity` = 256 events | pump | `subscribe` (replay) | oldest evicted, no marker |
| subscriber | max(128, ring length at subscribe) | pump (non-blocking) | SSE handler | subscriber closed (§3.7) |

### 3.3 Event vocabulary (`chat.StreamEvent`)

No `StreamEvent` is persisted.

| `type` | Emitted | Fields |
|---|---|---|
| `stream_start` | start of generation, after any prepare-turn events | `message_id`, `agent_id` |
| `delta` narration/final | phased mode: after each provider iteration closes, one event per buffered fragment; `narration` if stop reason is `tool_use`, else `final` (empty stop reason → `final`) | `content`, `phase` |
| `delta` untagged | live mode: as each provider text delta arrives | `content` |
| `delta` thinking | on each provider `thinking` event, both modes (Anthropic: whole block at block stop) | `content`, `phase: thinking` |
| `delta` Nanite-authored | plugin and envelope blocks, synthesis; phase `final`. Error-envelope text has no phase | `content`, `phase` |
| `replace_content` | direct-return literal; promissory-preamble recovery (content `""`, then the same text is re-sent as narration) | `content` |
| `tool_call` | when a tool starts; also for blocked, denied or invalid calls | `tool`, `tool_id`, `detail` |
| `tool_result` | after the whole tool batch has executed, in plan order; blocked/denied results during pre-check | `tool`, `tool_id`, `summary` (≤500 B, then `... (truncated)`), `is_error` |
| `approval_request` | permission check returns ask; the generation goroutine then blocks | `data` = JSON string `{request_id, tool, input, reason}` (double-encoded; `input` untruncated) |
| `notify_pause` | before `dev_*` tools except grep and glob, followed by a fixed delay (`notifyPauseDefaultDelay`, 1.5 s) | `tool`, `tool_id`, `data{path, detail, delay_ms, cancel_hint}` |
| `tool_warning` | no tools available; tool error; approval-denial cap | `data{tool_name, error, iteration, consecutive_errors, level}` |
| `status` | stop and cancel notices, `max_tokens` notice, pacing heartbeat | `content` or `detail` |
| `circuit_open`, `rate_budget_pause`, `slot_changed`, `handoff_loaded` | breaker open at tool settle; rate-budget refusal; context-slot change; handoff load | `content` / `data` / `envelope` |
| `plugin_envelope`, `panel_signal`, `message_received`, `subagent_run_status_changed` | plugin, reflex, route, card and messaging sinks | `envelope`, `plugin_id` |
| `error` | provider, stall, and save failures; terminal for the generation loop (no `stream_end` follows); CLI `error` events are not terminal | `error`, `structured_error{code, message, details, timestamp}` |
| `stream_end` | success path only, after the assistant row is written; one emit site | `message_id`, `agent_id`, `usage` (summed over iterations; `stop_reason` = last non-empty), `envelope` |
| `session_takeover` | written by the SSE handler when another subscriber replaces this one; no `id:`, not in the ring | `content` |

Declared or advertised with no emitter: `mode_suggestion`, `subordinate_delta`, `subordinate_tool_use`, `subordinate_done`.

### 3.4 Delta modes

| | phased (default) | live (`delta_mode: "live"` on the turn request) |
|---|---|---|
| Text delivery | buffered per iteration in `iterDeltaBuf` (unbounded), flushed when the provider channel closes | each provider delta sent immediately |
| Phase tag | `narration` / `final` | none (Nanite-authored text keeps `final`) |
| Thinking | immediate | immediate |
| Context-overflow retry | buffer discarded | text already sent is not retracted |
| Error or stall before the flush | buffered text never streamed; persisted as partial content | text already sent |
| CLI turns | the provider channel closes only at turn end, so all text is held until then; tool events stream live by broadcast | same consume loop; each delta sent as it arrives |
| Persistence | narration → `metadata.thinking`, final → `content.text` | same |

Retry, agent-message, harness-trigger and wake turns are always phased. Unknown `delta_mode` values return 400; the field is advertised in `turn_send_fields`.

### 3.5 Tool handling in the loop

- A `tool_use` is acted on only when the stop reason is `tool_use` and at least one block exists; otherwise the blocks are discarded and the turn ends.
- Order per tool: metadata, result cache, blocked/exhausted, permission, plugin hook, execution rules, argument validation.
- Approvals: `WaitForApproval` blocks the generation goroutine. Default timeout 5 minutes → deny with `TimedOut`. Context cancel → deny recorded as "user denied". Scopes `once` and `session`; `project` returns 400.
- Execution: concurrency-safe tools run first, one goroutine each (no cap), then the rest serially. All executed `tool_result` events are emitted afterward in plan order (in `postProcessToolResults`), not per tool.
- Model-visible result is truncated to a per-model budget (4000–32000 B; 512 B once cumulative output passes 24 KiB in a turn); over-budget bodies go to `tool_result_cache`. The subscriber receives `summary` (500 B).
- Tool input reaches subscribers only via `approval_request.data.input`, `notify_pause.data`, and `detail` (first line, ≤120 B, only for `dev_bash`, read, write, edit, grep, glob, `web_fetch`, web search, `subagent_spawn`, `python_run`).
- Limits: 200 iterations hard ceiling; 10 consecutive failed calls end the run; idle 900 s (300 s subagent), checked at iteration top; 1 s sleep per iteration after the first.

### 3.6 Text sent to the model but not to subscribers

Promissory-preamble nudge (iteration 0, once, non-CLI: synthetic assistant and user messages appended to the model request); `request_tools` reflection; repeat and per-tool-cap notes; reminders, reflex and subagent results inserted into the user-context slot; failure footer (persisted only).

### 3.7 StreamManager

- The pump assigns `EventID` (from 1, per message), appends to the ring, and does a non-blocking send to the single subscriber. If the subscriber channel is full, the subscriber is cleared and its channel closed: the SSE handler sees EOF and the client is expected to reconnect with its cursor; the ring keeps the events.
- One subscriber per message; one SSE connection per session (a second connection on the session closes the first with `session_takeover`).
- Replay: cursor = larger of `?from` and `Last-Event-ID` (malformed `?from` → 400; malformed header ignored). `subscribe` replays ring events with `EventID` > cursor, then goes live. Evicted events leave no gap marker.
- Completed stream: `subscribe` returns replay plus a closed channel, but `SubscribeSSE` still registers the session's SSE, so replaying a finished message takes over the session's live subscriber.
- Cleanup: `ScheduleCleanup` removes the stream 60 s (`defaultPostCompletionGrace`) after the producer closes; afterwards the events routes return 404 `stream not found`.
- There is no unsubscribe: after a client disconnect the subscriber stays registered until its channel fills and the pump closes it.

### 3.8 SSE framing (`streamMessageEvents`)

- Headers: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`; the write deadline is cleared (`clearSSEWriteDeadline`).
- Frame: `id: N` (omitted when `EventID` is 0), `event: <type>`, `data: <StreamEvent JSON>` (the JSON also carries `event_id`), blank line. Flush per event.
- No `retry:` field, no comment lines, no heartbeat: the stream is silent during approvals (up to 5 minutes), rate-budget waits and tool runs.
- Marshal, write and flush errors are ignored; a dead peer is noticed only when the request context ends.
- On channel close the handler returns with no terminal marker.

### 3.9 Routes: legacy messages API vs harness v1

Both write through the same `streamMessageEvents`; harness v1 does not filter or transform events.

| | Legacy | Harness v1 (`/api/harness/v1`) |
|---|---|---|
| Create session | `POST /api/sessions` `{project_id, model, provider, agent_id}` → 201 Session; default agent from user settings, then slug `default` | `POST /sessions` `{project_id, provider, model, agent_id, title, metadata}` → 201 `{session, details, stream_transport, route_hints}`; no default agent; 422 for `runtime_kind`, `work_root`, `durable_agent_id` |
| Send | `POST /api/messages` `{session_id, content, cycle_kind, effort, delta_mode}` → 202 `{message_id, stream_url}` | `POST /sessions/{id}/turns` `{content, cycle_kind, effort, delta_mode}` → 202 `{session_id, message_id, stream_url, raw_stream_url, event_transport, initial_activity_state}`; 404 before body decode |
| Stream | `GET /api/stream/{messageID}` | `GET /sessions/{id}/events?message_id=` (required); 404 if the message belongs to another session |
| Resume | `?from=` / `Last-Event-ID` | same code; `stream_url` carries no cursor |
| Approval | `POST /api/sessions/{id}/approvals/{requestId}` `{decision, scope}` | same handler under the harness prefix |
| Cancel | `POST …/chat/cancel` (404 when idle) | `POST …/cancel` (200 `idle` when idle) |
| Recover | `/recover`, `/agent/reboot`, `/recovery/cancel` | `/recover` |
| Retry, list, history, fork, delete, `active_message_id`, `interrupted_turn` | present | absent |
| Discovery | `GET /api/start-surface/capabilities` | `GET /initialize`, `GET /capabilities` (operations, event types, field support) |
| Durable agents | full | list, get, start, resume, wake |
| Auth | Basic auth when `NANITE_AUTH_USER` or `NANITE_AUTH_PASSWORD` is set | same middleware |

`cycle_kind` is accepted and advertised on both and read by no code. Unknown JSON fields are ignored on both.

### 3.10 Advertised vs emitted event types

`harnessV1EventTypes` (in `internal/api/harness_v1.go`) advertises: `stream_start`, `delta`, `replace_content`, `tool_call`, `tool_result`, `approval_request`, `tool_warning`, `notify_pause`, `plugin_envelope`, `message_received`, `subagent_run_status_changed`, `mode_suggestion`, `status`, `error`, `stream_end`, `session_takeover`.
Emitted but not advertised: `circuit_open`, `rate_budget_pause`, `handoff_loaded`, `panel_signal`, `slot_changed`. Advertised with no emitter: `mode_suggestion`.

### 3.11 Client behaviors that matter to the stream

| Client | Surface | Reconnect / cursor |
|---|---|---|
| `nanite chat` (CLI) | harness v1 only | connection retries at establishment only; no cursor; a mid-stream failure becomes an `error` event |
| Browser GUI (extracted GUI repo; the copy of `ui/` still tracked in Nanite) | legacy messages + `/api/stream/{id}?from=cursor`; harness client methods exist with no non-test caller | cursor per message, de-dup by `event_id`, native `EventSource` reconnect, reattach via `active_message_id`; 13 event types handled |
| Other HTTP clients | vary | native `EventSource` or none |

## 4. Persistence

### 4.1 Storage

SQLite (single connection, WAL, goose migrations in `internal/store/migrations`). Non-SQLite: `~/.nanite/tool-output/*.txt` (fallback tool-output files, 7-day retention), artifact files, boot-dir `recovery.md`.

| Table | Holds | Written |
|---|---|---|
| `messages` | id, session_id, agent_id, role, `content`, `envelope`, `metadata`, parent_id, is_compacted, created_at (1 s resolution) | user row at request time; assistant row once at end of turn; partial row on error or cancel |
| `token_usage` | per message: input, output, cache_creation, cache_read, tool_input tokens; estimated cost | end of turn, only if input or output > 0; not on error or cancel |
| `execution_metrics` | per message: provider, adapter, model, duration, `stop_reason`, tool_iterations, tool_calls, `debug_snapshots` (tool name, duration, success, parallel) | end of turn |
| `event_log` | event_type, category, detail, metadata (`tool_call`, `tool_error`, `provider_error`, `provider_stream_stalled`, `max_tokens_truncation`, `preamble_stall_recovery`, …); write errors swallowed | during the turn; no HTTP reader |
| `tool_result_cache` | over-budget tool output bodies | during the turn; never deleted; body NULL over 1 MiB |
| `artifacts` | pointer rows (path from tool input) for write and edit tools | during the turn |
| `envelope_instances` | approval and elicitation envelopes | during the turn |
| `subagent_runs` | prompt, inputs, result, error, attempts | during the turn |
| `host_runtime_feed_*` | CLI-wrapper runtime feed (512 events per session) | during the turn |

No `role='tool'` rows are written. Sessions are archived, never deleted. No retention job exists for `messages`, `token_usage`, `execution_metrics`, `event_log` or `artifacts`.

### 4.2 Assistant `messages.content` (served as a JSON string)

`StructuredMessage` in `internal/chat/structured.go`: `{v, text, tier, hash, envelopes[], tool_calls[], flags}`.

| Field | Content |
|---|---|
| `v` | always 1; readers accept any `v` > 0 |
| `text` | final text after output filters, envelope-block stripping and the failure footer |
| `tier` | `tool` if any tool-call ref exists, else `default` |
| `hash` | sha256 of `text` |
| `tool_calls[]` | `{id, name, status, has_envelope?, error_reason?}`; status `success`, `error`, `denied`, `blocked`, `canceled` |
| `flags` | `truncated`, `has_error`, `provisional` (declared, no writer) |

`messages.metadata` keys: `thinking` (narration text of tool iterations), `thinking_blocks` (`[{thinking, signature}]`, last iteration only), `had_error`, `provider_error`, `partial_output`. Error and cancel rows carry an empty `tool_calls`.

### 4.3 What is kept, by item

| Item | Kept? | Where and when | Retrievable via |
|---|---|---|---|
| User text | yes | `messages.content`, plain, at request time | messages endpoint |
| Assistant text | yes | `content.text`, end of turn (partial on error or cancel) | messages endpoint |
| Narration vs final | yes, as two fields | narration → `metadata.thinking`; final → `content.text`; per-delta phase not stored | messages endpoint |
| Reasoning text and signature | partial | Only if thinking was requested (§2.6). Signed blocks reset after every tool iteration; only the last iteration's blocks reach `metadata.thinking_blocks`. History replay is text-only, so stored blocks are not read back. OpenAI reasoning summaries are unsigned. | messages endpoint (`metadata`) |
| Tool call name, id, status | yes | `content.tool_calls[]`, end of turn; `event_log` at execution (name, result length) | messages endpoint |
| Tool call input | no durable copy | in-memory inspector, SSE `detail` and `approval_request.input` only | inspector endpoint (memory only) |
| Tool result, full | only over the preview budget | `tool_result_cache.body`, during the turn | model tools `fetch_tool_result` and `search_tool_result`; no HTTP route |
| Tool result shown to the model | no | in-memory turn state; inspector | inspector endpoint |
| Usage | yes, end of turn | `token_usage`: cache tokens stored; cost = input × input price + output × output price (cache tokens excluded; unknown model → 0) | `/usage`, `/details`, `/metrics` |
| Reasoning tokens | no | not carried by any provider mapping | none |
| Stop reason | yes | `execution_metrics.stop_reason`; `stream_end.usage.stop_reason` | `/metrics` |
| Provider and model per message | yes | `execution_metrics`; `messages.agent_id` | `/metrics`, `/details` |
| SSE events | no | ring buffer, 256 events, 60 s after completion | events routes |
| Approvals | no | pending requests and session grants in memory; outcome only as `tool_calls` status `denied` | approvals route (write) |
| Errors | yes | `metadata.provider_error` (code, message, details, timestamp); `event_log`; `structured_error` on SSE not stored | messages endpoint |

### 4.4 Memory-only

Inspector `TurnSnapshot` ring (50 turns per session; exists only when `developer_mode` was on at boot; holds slots, model messages, and per-tool `arguments`, `result`, `visible_result`); stream ring buffers; producer channel; per-turn `chatMessages` (tool_use, tool_result, signed thinking); pending approvals; loop-detector windows. A restart mid-turn leaves the user row with no assistant row.

### 4.5 Tool calls: where inputs and outputs live

Calls: inline `{id, name, status}` refs in the assistant row. Inputs: no durable copy. Outputs: durable only above the preview budget (`tool_result_cache`, no HTTP route). Separate channels, none of which stores both durably:

| Channel | Carries | Durable |
|---|---|---|
| SSE `tool_call` / `tool_result` | `detail` ≤120 B; `summary` ≤500 B | no |
| Inspector endpoint | full arguments and results | no (memory, dev mode) |
| `event_log` | name, result length | yes; no HTTP route |
| `execution_metrics.debug_snapshots` | name, duration, success | yes |
| `host_runtime_feed_events` (`/runtime-events`) | tool id, name, status, is_error; arguments and results redacted | yes; CLI-wrapper sessions only |

## 5. Drops and silent failures

| Where | Mechanism | Effect |
|---|---|---|
| `pump` in `internal/service/stream.go` | non-blocking send; full subscriber channel closed | subscriber gets EOF with no terminal event; events stay in the ring |
| `pump` | ring evicts oldest at 256 | no gap marker on replay |
| `SubscribeSSE` | replaying a finished message registers the session's SSE | closes the session's live subscriber |
| `ScheduleCleanup` | stream removed 60 s after completion | later resume returns 404 though the message row exists |
| `streamMessageEvents` | marshal, write, flush errors ignored; no heartbeat | dead peer undetected until context ends; silent during approvals and long tool runs |
| `streamMessageEvents` | malformed `Last-Event-ID` ignored | replay from `?from` or 0; duplicates possible |
| `trySendEnvelope`, broadcast helpers | non-blocking send, recover | dropped when `produce` is full or closed; only plugin envelopes are counted and logged |
| iteration buffer (phased) | discarded on context-overflow retry; not flushed on error or stall returns | text not streamed (persisted as partial content on error paths) |
| `consumeProviderIteration` | `tool_use` blocks used only when stop reason is `tool_use` | tool calls silently discarded otherwise |
| provider consumers | truncated stream (Anthropic clean EOF, OpenAI Chat without `[DONE]`) | treated as a normal completion; partial text persisted as the answer |
| `finalizeRun` | `CreateMessage` failure emits `error` and returns | no `stream_end`, no assistant row |
| cancel mid-stream (OpenAI, CLI) | channel closes with no error | treated as a normal end; by code reading, `CreateMessage` then runs on a canceled context and fails with "Failed to save response" (not exercised) |
| Anthropic adapter | thinking only when the model-ID gate passes; `redacted_thinking` never captured | see §2.6 |
| `thinking_blocks` | written to `messages.metadata`, no reader | never replayed from storage |
| CLI runtime path | non-blocking send drops events when full; parse errors ignored | missing deltas or usage |
| CLI runtime turns | only `delta`, `thinking`, `usage`, `done`, `error` reach the generation loop | tool events reach subscribers by broadcast only; `content.tool_calls` is expected to be empty (code reading, not exercised) |
| `LogEvent`, usage and metrics writers | errors swallowed or logged only | missing rows |
| tool `summary` and `detail` | byte slicing at 500 / 300 / 120 | can split a UTF-8 sequence |
| `tool_result` | duplicate emit for the same id (notify-cancel path, scratchpad) | two results per id |
| `OnCircuitOpen`, `mode_suggestion`, `subordinate_*`, `cycle_kind` | declared, advertised or parsed; never invoked, emitted or read | no effect |
| subagent-caused errors | error delta, error event and partial row suppressed | client sees stream close only |
| cost estimate | cache tokens excluded from cost | estimated cost below billed cost |

## 6. Comparison with the provider specs

Specifications compared: [Anthropic Messages streaming](https://platform.claude.com/docs/en/build-with-claude/streaming), [OpenAI Chat Completions streaming events](https://developers.openai.com/api/reference/resources/chat/subresources/completions/streaming-events), [OpenAI Responses streaming events](https://developers.openai.com/api/reference/resources/responses/streaming-events), [WHATWG Server-Sent Events](https://html.spec.whatwg.org/multipage/server-sent-events.html). "Δ" marks the direction of Nanite's difference: improvement, regression, or neutral.

| Topic | Provider specs | Nanite | Δ |
|---|---|---|---|
| Framing | SSE. Anthropic and Responses: `event:` + JSON `type`. Chat: `data:` only + `[DONE]`. Responses adds `sequence_number` | `id:` + `event:` + `data:` (JSON with `type` and `event_id`); no `retry:` | neutral |
| Terminal marker | Anthropic `message_stop`; Chat `[DONE]`; Responses `response.completed` / `failed` / `incomplete` | `stream_end` on the success path only; error, cancel, save failure and subscriber eviction end without it | regression |
| Keepalive | Anthropic `ping`; SSE spec advises a comment line about every 15 s; Chat and Responses: none documented | none on the chat stream | regression |
| Resume | SSE defines `Last-Event-ID`, replay left to the server; Anthropic and Chat: none documented; Responses: `starting_after` cursor, background mode | `id:` per event, `?from` / `Last-Event-ID`, 256-event ring for 60 s after completion | improvement (bounded; no gap marker) |
| Text timing | streamed as generated | phased default: per iteration; live: as generated | regression (phased) / neutral (live) |
| Tool-call arguments | streamed as partial JSON (Anthropic `input_json_delta`; Chat and Responses argument deltas) | assembled in the adapter; subscribers get `tool_call` with a `detail` label; full input only in `approval_request` | regression for clients needing arguments |
| Tool results | supplied by the client (server tools aside) | executed server-side; streamed as `tool_result` summary | neutral (different layer) |
| Reasoning | Anthropic `thinking_delta` + `signature_delta`; Responses summary/text events + encrypted content; Chat: none in deltas | `delta` with `phase: thinking`, no signature; Anthropic thinking gated by a dated model-ID pattern; signed blocks stored for the last iteration only and never replayed | regression |
| Usage | Anthropic: start + cumulative `message_delta`; Chat: final chunk, opt-in; Responses: `response.completed` | one `stream_end.usage` summed over iterations; no reasoning tokens; not incremental | regression (reasoning tokens) / neutral (aggregation) |
| Stop reasons | Anthropic: `end_turn`, `max_tokens`, `stop_sequence`, `tool_use`, `pause_turn`, `refusal`, `model_context_window_exceeded`; Chat: `stop`, `length`, `content_filter`, `tool_calls`; Responses: status + `incomplete_details.reason` | special handling for `tool_use` and `max_tokens`; every other value, including `refusal`, is a normal end | regression |
| Unknown events | Anthropic: handle unknown event types gracefully | adapters ignore unknown events; the subscriber stream emits types it does not advertise | neutral (A) / regression (D) |
| Content kinds | Anthropic citations and server-tool blocks; Chat refusals and logprobs; Responses annotations and built-in tool events | not consumed | regression (data dropped) |
| In-stream errors | Anthropic `error` event after HTTP 200; Responses `error` and `response.failed` | `error` event with `structured_error`; terminal | neutral |
| Internal message model | Anthropic: content blocks; OpenAI: `messages` with `tool_calls` | Anthropic-shaped; OpenAI adapters convert both ways | neutral |
| Agent-level events (tool loop, approvals, runs) | not covered by the provider specs; covered by application protocols (AG-UI, ACP, the Vercel AI SDK UI message stream protocol) | own vocabulary (`chat.StreamEvent`); ACP consumed at hop A; no AG-UI or AI SDK stream emitter in the repo | neutral |

## Verify

```
grep -rn 'Type: "stream_end"' internal/service/                    # one emit site (success path)
grep -n 'ringBufferCapacity\|defaultPostCompletionGrace' internal/service/stream.go
grep -n 'func harnessV1EventTypes' internal/api/harness_v1.go      # advertised set
grep -rn 'OnCircuitOpen' internal/ | grep -v _test                 # declared and assigned, not called
grep -rn 'thinking_blocks' internal/ | grep -v _test               # write sites only
grep -n 'func modelSupportsInterleavedThinking' internal/llm/anthropic/client.go
```
