# Provider SDK Adapter Pattern

> Reference: `pkg/provider/anthropic.go` (Phase 0.4.1, swapped to `anthropic-sdk-go`).
> Follow this pattern when swapping `openai.go`, `ollama.go`, `gemini.go` to their official SDKs.

This document captures the conventions we landed on while wrapping the official
Anthropic SDK behind nanite's existing `Provider` interface. Sub-agents doing the
parallel 0.4.2–0.4.5 swaps should copy the shape, not the details.

---

## Goals of the swap

1. Replace hand-rolled HTTP / SSE code with the official SDK's transport.
2. Keep nanite's `Provider` interface (`pkg/provider/provider.go`) unchanged.
3. Keep the decorator chain transparent — `retry.go`, `circuit.go`, `cost_monitor.go`,
   `cache.go` still wrap the adapter. Nothing downstream changes.
4. Keep existing tests green. Some test expectations are semantic (cache hints on
   system blocks, on the last tool, on the last N user messages) — preserve them.

---

## Non-goals

- **Do not** change `Provider`, `Embedder`, `CacheableProvider`, `APIKeySetter`.
- **Do not** touch `pty_*.go`, `cli_*.go`, `subprocess_bridge.go`.
- **Do not** rewrite `registry.go`, `retry.go`, `circuit.go`, `cost_monitor.go`.
- **Do not** extend the public API surface of the adapter beyond what exists.

---

## File layout

```
pkg/provider/
  anthropic.go           # SDK-backed adapter + helpers, in one file
  anthropic_test.go      # Semantic tests (cache hints, capabilities, request shape)
  sdk_imports.go         # Blank imports keep go.mod require block stable across waves
  ADAPTER_PATTERN.md     # This doc
```

One file per adapter is the convention. Split into `<provider>_adapter.go` only
if the file exceeds ~1000 lines.

---

## Step-by-step: wrap an official SDK behind the Provider interface

### 1. Add the SDK dependency

The four SDKs are already in `go.mod` (see `sdk_imports.go`). When you start
wiring the real adapter, delete the corresponding blank import line. Example in
`anthropic.go`:

```go
import "github.com/anthropics/anthropic-sdk-go"
```

…and `sdk_imports.go` no longer blank-imports `anthropic-sdk-go` (already the
case — the Anthropic line has been removed there).

### 2. Preserve the adapter struct shape

The existing struct fields (`apiKey`, `Retry`, `CircuitBreaker`, `RateTracker`,
`OnStatus`, `OnCircuitOpen`, `cacheHints`) are wired by `main.go`, tests, and
the decorator layer. **Keep them.** Add an SDK client field and initialize it
lazily when the API key is set:

```go
type Anthropic struct {
    apiKey     string
    httpClient *http.Client
    client     *anthropic.Client // lazy
    Retry          RetryConfig
    CircuitBreaker *CircuitBreaker
    RateTracker    *TokenRateTracker
    OnStatus       StatusCallback
    OnCircuitOpen  func()
    cacheHints     []CacheHint
}

func (a *Anthropic) ensureClient() {
    if a.client != nil || a.apiKey == "" { return }
    c := anthropic.NewClient(
        option.WithAPIKey(a.apiKey),
        option.WithHTTPClient(a.httpClient),
        option.WithMaxRetries(0), // our retry decorator owns this
    )
    a.client = &c
}
```

**Rule:** **disable the SDK's built-in retry** (`WithMaxRetries(0)` or
equivalent). The `retry.go` / `circuit.go` decorator chain owns that policy;
doubling it up causes long latency tails and the circuit breaker will miscount.

### 3. Build request params from nanite's types

Write pure mappers from `ChatMessage` / `ToolDefinition` / `CacheHint` to the
SDK's param types:

```go
func (a *Anthropic) buildSDKSystem(prompt string) []anthropic.TextBlockParam
func (a *Anthropic) buildSDKTools(tools []ToolDefinition) []anthropic.ToolUnionParam
func (a *Anthropic) buildSDKMessages(msgs []ChatMessage) []anthropic.MessageParam
```

Keep these **pure** — no I/O, no logging. Tests call them directly.

**Gotcha:** The Anthropic SDK's `CacheControlEphemeralParam` zero value marshals
to `omitzero` (absent). You must set at least one field (`TTL` defaults to 5m).
See `ephemeralCache()` in `anthropic.go`. Check the equivalent behaviour in
OpenAI/Ollama/Gemini SDK param types before assuming zero-struct == present.

### 4. Streaming bridge

The SDK exposes a streaming iterator (`s.Next() / s.Current() / s.Err()` for
Anthropic). Bridge it into nanite's `chan StreamEvent` in a goroutine:

```go
ch := make(chan StreamEvent, 64)
go a.bridgeStream(ctx, stream, ch, span)
return ch, nil
```

Inside `bridgeStream`:
- `defer close(ch)` — always.
- `defer stream.Close()` — always.
- `defer span.End()` — always, with token-count attributes.
- **Peek the first event before returning** so start-time API errors flow
  through the retry loop. See `peekStream` in `anthropic.go`. OpenAI / Gemini /
  Ollama SDKs each expose iterators with different start-error semantics —
  check whether the first `Next()` triggers the HTTP round-trip.
- Map SDK event variants to nanite `StreamEvent`s:
  - `MessageStartEvent` → `StreamEvent{Type:"usage", Usage:{InputTokens…, CacheCreation…, CacheRead…}}`
  - `ContentBlockDeltaEvent(text_delta)` → `StreamEvent{Type:"delta", Content:…}`
  - `ContentBlockDeltaEvent(input_json_delta)` → accumulate per-block, emit on stop
  - `ContentBlockStopEvent` (when a tool_use block closes) → `StreamEvent{Type:"tool_use", ToolUse:…}`
  - `MessageDeltaEvent` → `StreamEvent{Type:"usage", Usage:{OutputTokens…, StopReason:…}}`
  - `MessageStopEvent` → `StreamEvent{Type:"done"}`
- Benign termination (context cancelled, io.EOF) must not emit an `error`
  event. See `isStreamClosedErr`.

### 5. Tool-use conversion

Map **both directions**:

**nanite → SDK (request):** `ToolDefinition` → `ToolUnionParam{OfTool:…}` with
`InputSchema: ToolInputSchemaParam{Properties: <your schema>}`. `ContentBlock`
with `Type:"tool_use"` → `ContentBlockParamUnion{OfToolUse: &ToolUseBlockParam{…}}`.
`Type:"tool_result"` → `OfToolResult: &ToolResultBlockParam{…}`. See
`chatBlockToSDK`.

**SDK → nanite (response):** tool_use blocks stream as a
`ContentBlockStartEvent` with `ContentBlock.Type=="tool_use"` carrying
`ID`/`Name`, followed by `input_json_delta` chunks. Accumulate the chunks per
block index (map keyed by `ev.Index`), `json.Unmarshal` on
`ContentBlockStopEvent`, emit a `StreamEvent{Type:"tool_use", ToolUse: &ToolUseBlock{…}}`.

If the JSON unmarshal fails, wrap the raw string as `{"_raw": raw}` — never
drop the tool call silently.

### 6. Error mapping

Map SDK errors to `*APIError` (defined in `retry.go`):

```go
func classifyAnthropicError(err error) *APIError {
    var sdkErr *anthropic.Error
    if errors.As(err, &sdkErr) {
        raw := sdkErr.RawJSON()
        if len(raw) > maxAnthropicErrBody { raw = raw[:maxAnthropicErrBody] }
        return &APIError{
            StatusCode: sdkErr.StatusCode,
            Message:    raw,
            RetryAfter: ParseRetryAfter(sdkErr.Response.Header.Get("Retry-After")),
        }
    }
    return &APIError{StatusCode: 0, Message: err.Error()}
}
```

**StatusCode 0** (not an SDK API error, e.g. transport failure) is
**non-retryable** per `RetryableStatusCode(0) == false`. This is intentional —
dial failures shouldn't be silently retried by the adapter; let the caller
decide.

**Per-provider:** OpenAI's SDK exposes `openai.Error`, Gemini's SDK exposes
`genai.APIError`, Ollama's SDK returns unstructured errors. Check the exact
typed-error shape before writing `errors.As`.

### 7. Usage accounting

Two sources of usage events in a single request:
- `message_start` → input tokens, cache creation, cache read (fire on entry).
- `message_delta` (final one) → output tokens, stop reason.

**Always** record input tokens into the `RateTracker` as soon as `message_start`
arrives, not at the end — otherwise the next request starts pacing with stale
state.

### 8. Response-body cap handling — **AUDIT ANCHOR**

The Anthropic SDK uses `io.ReadAll(res.Body)` internally (see
`anthropic-sdk-go/internal/requestconfig/requestconfig.go:498,529`). It does
**not** apply `io.LimitReader` to the response body. The
`pkg/provider/02-high-unbounded-response-bodies.md` finding is therefore **not
closed by the SDK** — only partially mitigated because the SDK at least
streams error bodies rather than buffering unbounded streaming content.

**What we do in the adapter:** cap the forwarded error message ourselves via
`maxAnthropicErrBody = 1 << 20` inside `classifyAnthropicError`. This prevents
an OOM from a hostile error response being propagated up as an `*APIError`.

**What the other adapters must do:** same cap (`const maxErrBody = 1 << 20`)
in their error classifier. Streaming bodies still need a separate review — the
SDKs do their own stream parsing, but an infinite stream with no terminator
will still tie up a goroutine indefinitely. Flag that as a follow-up; do not
paper over it.

### 9. Cache-hint backwards compat

Existing tests (`anthropic_test.go`, `cache_test.go`) call package-level
helpers (`buildSystemBlocks`, `buildToolsWithCacheControl`, `marshalMessages`)
and methods on the adapter (`a.buildSystemBlocks`, etc.) that return
`map[string]any` shapes. These tests check the **semantics** of cache hints.

Keep these pure helpers — they're cheap and they anchor the semantic invariant
independently of SDK param types. The live request path goes through
`buildSDKSystem` / `buildSDKTools` / `buildSDKMessages`, which encode the same
semantics onto SDK types. **Any new semantic tests should target the SDK
builders; don't remove the map-based tests until we retire `pkg/provider` into
`go-providers`.**

---

## Testing checklist

- [ ] `go build ./...` clean.
- [ ] `go test -race ./pkg/provider/...` green.
- [ ] No change to the existing `Provider` interface signatures.
- [ ] Decorator chain still wires (smoke test: `NewAnthropic()` works through
      `Retry`, `CircuitBreaker`, `RateTracker`).
- [ ] Cache hints produce SDK params with the correct `cache_control` markers.
- [ ] Stream events produce the full canonical set: usage → delta* → tool_use*
      → usage(stop) → done.
- [ ] Error response with retryable status triggers the retry loop, not the
      SDK's built-in retry (which we disabled).
- [ ] Benign stream termination (ctx cancel, EOF) does not emit a spurious
      `error` event.

---

## Anti-patterns — things not to do

- ❌ Leaving the SDK's default retry enabled. Our decorator chain will not
  know about SDK-level retries, causing duplicate counting, worse backoff, and
  circuit-breaker miscalibration.
- ❌ Using `time.Sleep` in the bridge goroutine. Always `select` on
  `ctx.Done()`.
- ❌ Blocking on the channel send when the consumer has gone away. Use buffered
  channels (size 64 matches Anthropic) and `select` with `ctx.Done()`.
- ❌ Forwarding the full SDK error message verbatim. Cap it; it may carry
  request echo that contains the API key. See finding 07 in the
  provider-abstractions audit.
- ❌ Changing the `Provider` interface to be more convenient. Don't. Fix the
  adapter; leave the interface alone.

---

## Questions the parallel sub-agents should answer in their reports

1. Does their SDK retry by default? How did they disable it?
2. Does their SDK expose a typed `APIError` with `StatusCode`? If not, how is
   retry-on-429 detected?
3. Does their SDK impose a response body cap? (Answer for Anthropic: **no**.)
4. Does their SDK handle streaming tool-use as a single terminal call, or as
   per-chunk deltas?
5. Are there tool-use or content-block shape differences between their SDK's
   types and nanite's `ToolDefinition`/`ContentBlock` that require loss or
   mapping tables?
