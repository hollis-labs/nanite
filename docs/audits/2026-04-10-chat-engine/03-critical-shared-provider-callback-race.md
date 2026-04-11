# [Critical] Data race on shared `*provider.Anthropic` fields — concurrent sessions clobber each other's callbacks and cache hints

**Scope:** chat-engine
**Topic:** Concurrency correctness / Security
**Date:** 2026-04-10

## Problem

Providers are singletons in the `provider.Registry`. `generateResponse` mutates mutable fields on the **shared** provider instance — `OnStatus`, `OnCircuitOpen`, and `cacheHints` — without any synchronization. When two chat sessions run concurrently, the second session overwrites the first session's callbacks before the first session's provider call returns. Status events meant for session A are delivered into session B's SSE channel, and circuit-breaker notifications can land in the wrong session.

This is a data race on fields that determine correctness, not just liveness: the callback captures a session-specific `ch chan chat.StreamEvent`, and the wrong channel means wrong destination for user-facing events. It is also a session-boundary information-leak surface.

## Evidence

`generateResponse` writes to the provider's fields directly after resolving it from the registry:

```go
// internal/service/chat_generate.go:L132-L148
// Wire status callback for retry notifications and circuit breaker.
if ap, ok := prov.(*provider.Anthropic); ok {
    ap.OnStatus = func(message string) {
        ch <- chat.StreamEvent{Type: "status", Content: message}
    }
    ap.OnCircuitOpen = func() {
        ch <- chat.StreamEvent{
            Type:    "circuit_open",
            Content: "Provider rate limited after multiple retries. Would you like to keep trying?",
        }
    }
}

// Apply cache hints.
if cacheable, ok := prov.(provider.CacheableProvider); ok {
    cacheable.SetCacheHints(provider.DefaultCacheStrategy())
}
```

`prov` comes from `s.providers.Get(providerName)` at `resolveProvider` (`internal/service/chat.go:L313-L365`), which returns the singleton held by the registry — there is no per-session instantiation.

The Anthropic struct stores these as plain fields, no mutex:

```go
// pkg/provider/anthropic.go:L25-L34
type Anthropic struct {
    apiKey         string
    client         *http.Client
    Retry          RetryConfig
    OnStatus       StatusCallback // optional; called during retries to report status
    CircuitBreaker *CircuitBreaker
    OnCircuitOpen  func() // called when the circuit breaker trips
    RateTracker    *TokenRateTracker
    cacheHints     []CacheHint // set via SetCacheHints before each request
}
```

And inside the provider's retry path, the callbacks are read back on the receiver:

```go
// pkg/provider/anthropic.go:L327-L328
if a.OnStatus != nil {
    a.OnStatus(fmt.Sprintf("Waiting %ds for rate limit budget...", int(wait.Seconds()+0.5)))
}
```

```go
// pkg/provider/anthropic.go:L382-L400
if a.OnCircuitOpen != nil {
    a.OnCircuitOpen()
}
...
if a.OnStatus != nil {
    a.OnStatus(fmt.Sprintf("Rate limited, retrying in %s... (attempt %d/%d)", ...))
```

No mutex, no atomic, no per-call parameter, no context-scoped callback table. Two goroutines racing on `ap.OnStatus = ...` have no happens-before relationship. `grep -n "mu\|Lock\|atomic" pkg/provider/anthropic.go` returns nothing relevant to these fields.

### Race scenario (concrete)

1. Session A calls `HandleMessage`. A new goroutine starts `generateResponse`. It assigns `ap.OnStatus = func(...){ ch <- ...A channel... }`.
2. Before session A's `prov.StreamChatWithTools` returns, session B calls `HandleMessage`. A new goroutine starts `generateResponse`. It overwrites `ap.OnStatus = func(...){ ch <- ...B channel... }`.
3. Session A's retry loop hits a rate-limit wait and calls `a.OnStatus("Waiting ...")`. The current value of `OnStatus` is now session B's closure. The status event is pushed to session B's channel.
4. Session B's SSE consumer sees a "Waiting ..." status event it has no context for. Session A's consumer never sees the event it should have received.

The same applies to `OnCircuitOpen` — session A can trigger the circuit breaker and session B gets the "would you like to keep trying?" notification and retry prompt UI.

The `cacheHints` field is written via `SetCacheHints` (L146-L148 of chat_generate.go) — same pattern, same race. If session A uses an agent with one cache strategy and session B uses a different one, whichever write wins determines the cache control markers on **both** sessions' next request.

## Impact

- **Correctness:** The provider is the system's throughput-critical shared resource. `go test -race` would flag this instantly if a test exercised two `generateResponse` calls concurrently against the same provider — I did not run tooling per the scoped-review rule, but the race is obvious from the source.
- **Cross-session leakage:** A status event or circuit-open notification destined for session A can land in session B's SSE stream. The notification content (`"Waiting %ds for rate limit budget..."`) is benign, but the pattern is a session-boundary crossing. Any future OnStatus message that includes context-specific detail (e.g., the session ID or agent name) becomes a cross-tenant leak.
- **UX corruption:** If session A's provider call triggers a circuit open, the `circuit_open` SSE event goes to session B. Session B's user sees a "retry?" prompt they didn't trigger, and the real owner (session A) sees nothing.
- **Reproducibility:** The race fires any time two chat sessions use the same provider name concurrently — which is the normal case for any multi-tab or multi-session Nanite instance. Not a corner case.
- **Related fields:** `RateTracker`, `CircuitBreaker` — these have their own internal synchronization (assumed; out of scope here) but the wrapping `Anthropic` struct's field assignments are unsynchronized.

Severity Critical: data race on fields that determine correctness, not just liveness.

## Recommendation

The root cause is in `pkg/provider/anthropic.go` (not in scope for this audit), but the **chat engine is the caller that breaks the invariant**. Two options:

**Option A — fix at the call site (chat-engine, in scope).** Stop mutating shared provider state. Pass per-request callbacks through context values, and have the provider read them from context on every callback invocation:

```go
// internal/service/chat_generate.go — replace L132-L148 with:
provCtx = provider.WithStatusCallback(provCtx, func(msg string) {
    ch <- chat.StreamEvent{Type: "status", Content: msg}
})
provCtx = provider.WithCircuitOpenCallback(provCtx, func() {
    ch <- chat.StreamEvent{Type: "circuit_open", Content: "..."}
})
provCtx = provider.WithCacheHints(provCtx, provider.DefaultCacheStrategy())
```

The provider side then reads callbacks via `provider.StatusCallbackFromContext(ctx)` inside its retry loop. This is the same pattern already used for `WithSandboxDir`, `WithCLISessionID`, `WithProcessCallback`, `WithActivityCallback` (`chat_generate.go:L900, L907, L914, L921`), so it's idiomatic within this codebase. The context is per-call, so there is no shared state.

**Option B — fix at the struct (provider audit scope).** Add a `sync.RWMutex` to `Anthropic` and guard every read/write of `OnStatus`, `OnCircuitOpen`, and `cacheHints`. This is heavier-handed and doesn't address the fundamental design issue (shared mutable state on a singleton).

**Recommended: Option A.** It's more work in the chat engine but it's the correct fix, matches the existing context pattern in this codebase, and eliminates the class of bug rather than patching the symptom. It also fits the chat engine's reviewer mandate — the chat engine is the component that decides callbacks are per-request state, so the chat engine is the right place to express that.

Either way, this should be fixed before any concurrent-session testing. The `worker` package supports concurrent spawns (per `.nanite/agents/reviewer-backend.md:L158-L166`), and `DelegateAndAggregate` already spawns multiple workers concurrently (`internal/service/delegation.go:L263-L283`) — all of which call `generateResponse`, all of which will race on the same provider if they share one.

## References

- `internal/service/chat_generate.go:L132-L148` — the unsynchronized writes
- `internal/service/chat.go:L313-L365` — `resolveProvider` returns registry singletons
- `pkg/provider/anthropic.go:L25-L45` — shared struct without mutex
- `pkg/provider/anthropic.go:L327-L400` — the retry path that reads the racy fields
- `internal/service/delegation.go:L263-L283` — `DelegateAndAggregate` explicitly spawns concurrent `generateResponse` goroutines, which will race on the provider
- Reviewer-backend context `.nanite/agents/reviewer-backend.md:L158-L166` — "concurrent full-worker spawns are explicitly supported" — this finding says: not safely, not yet, not with this provider plumbing
- Cross-reference: `provider-abstractions` audit (queued) should verify no other provider-specific adapter has the same pattern
