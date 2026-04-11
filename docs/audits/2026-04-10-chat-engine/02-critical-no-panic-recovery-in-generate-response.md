# [Critical] No panic recovery in `generateResponse` — user-reachable panic crashes the server

**Scope:** chat-engine
**Topic:** Error Handling / Security
**Date:** 2026-04-10

## Problem

`chatServiceImpl.generateResponse` is launched in a new goroutine from `HandleMessage`, `RetryLastMessage`, `SendAgentMessage`, and `DelegateTask`. None of these call sites wrap the goroutine in a `defer recover()`, and `generateResponse` itself has no top-level recover. The HTTP router's `recoverMiddleware` protects the request goroutine but has no effect on the detached generation goroutine.

A panic anywhere in the generate path — plugin filter handlers, envelope parsing, JSON marshalling of an unexpected shape, a nil-dereference in a helper, a race inside the provider — crashes the entire Nanite server process. Several of these panic sources are reachable by normal user input.

## Evidence

`HandleMessage` spawns `generateResponse` in a detached goroutine with `context.WithoutCancel`, which deliberately survives the HTTP request's lifetime but also means the request handler's recover has nothing to catch if the goroutine panics:

```go
// internal/service/chat.go:L162-L168
// Start async generation with a detached context. The HTTP request context
// is cancelled when the handler returns (202 Accepted), but generateResponse
// runs in the background and must not be tied to the request lifecycle.
bgCtx := context.WithoutCancel(ctx)
go s.generateResponse(bgCtx, sessionID, assistantMsgID, content, ch)

return assistantMsgID, nil
```

`generateResponse` itself only defers stream cleanup — no recover:

```go
// internal/service/chat_generate.go:L46-L71
func (s *chatServiceImpl) generateResponse(ctx context.Context, sessionID, assistantMsgID, userContent string, ch chan chat.StreamEvent) {
    startTime := time.Now()

    ctx, cancel := context.WithTimeout(ctx, generateResponseTimeout)
    defer cancel()

    ctx, span := feotel.StartSpan(ctx, "nanite.service.generateResponse")
    ...
    defer span.End()

    defer func() {
        close(ch)
        s.streams.CloseStream(assistantMsgID)

        // Broadcast presence: stream ended.
        s.streams.ClearActivePresence(sessionID)
        s.streams.BroadcastPresence(chat.PresenceEvent{
            Type:      "stream_end",
            ...
```

Nothing catches a panic. The router's recover is scoped only to HTTP handler goroutines:

```go
// internal/server/server.go:L123-L133
func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if err := recover(); err != nil {
                log.Printf("PANIC: %v", err)
                http.Error(w, "internal server error", http.StatusInternalServerError)
            }
        }()
        next.ServeHTTP(w, r)
    })
}
```

`grep -n "recover" internal/service/chat*.go internal/service/delegation.go internal/service/stream.go` returns only TODO comments and log strings — no actual `recover()` calls in any chat-service source file.

### Reachable panic sources inside `generateResponse`

**1. Plugin filter panic (highest-impact).** `s.pluginHost.ApplyFilter` is called inline four times in `generateResponse`: on `FilterSystemPrompt` (L185), `FilterUserMessage` (L194), `FilterContextWindow` (L326), and `FilterAssistantResponse` (L583), plus `FilterEnvelopeData` (L645) and `FilterToolResult` in `chat_tool_executor.go` (L316). The filter registry's Apply method has **no recover** around handler invocation:

```go
// internal/plugin/filter.go:L131-L155
func (r *FilterRegistry) Apply(name string, data interface{}, ctx FilterContext) (interface{}, error) {
    ...
    for _, entry := range handlers {
        visible := stripForView(current, entry.View)
        result, err := entry.Fn(visible, ctx)    // ← direct call, no recover
        if err != nil {
            return nil, fmt.Errorf("filter %q (plugin %q, priority %d): %w", ...)
        }
        current = result
    }
    return current, nil
}
```

A panic inside any filter handler propagates up through `Apply` → `ApplyFilter` → `generateResponse` → crash. A single bad plugin (or a plugin that panics on specific user input shapes) takes down the whole server on every matching message.

**2. `ParseAgentConstraints` silently ignores JSON errors but returns a zero-value struct the rest of the loop reads without nil checks.** `internal/chat/engine.go:L31` does `json.Unmarshal([]byte(raw), &c)` and discards the error. If the unmarshal partially populates the struct with garbage (e.g., a negative `MaxTurns`), downstream math in `chat_loop_state.go` runs on poisoned input. Not directly a panic source but contributes to unexpected state.

**3. Envelope parsing on a crafted tool result.** `ParseEnvelopes` at `internal/chat/envelope.go:L163` walks regex match index pairs and indexes into the content. The regex `envelopePattern` uses `(?s)` dotall, but the index arithmetic on `match[2]:match[3]` is trusted without bounds checking against the input length; in practice `regexp` guarantees valid indices, but any future change to the pattern or a migration to a different regex engine could introduce an out-of-bounds read.

**4. `decomposer.DecomposeTask` JSON unmarshal.** Handled (returns a default), but downstream in `orchestrator.Aggregate` (`internal/chat/orchestrator.go:L99-L145`), `plan` is dereferenced without a nil-check for the nested `Decomposer` if ever called in the wrong order.

**5. Process tracker nil deref.** `setupCLIContext` at `chat_generate.go:L881` guards `s.processTracker != nil` but proceeds to dereference `s.appConfig.Artifacts` unconditionally at `maybeCreateAutoArtifact` L947-L948 after a single nil check for `s.appConfig`. Reachable during normal CLI flow.

The main concern is **#1** — plugin filters run on every message, operate on user-controlled content, and have no isolation.

## Impact

- Nanite is "preparing its first beta for developer friends." A single crashing plugin filter — including one a developer friend installs to experiment — kills every session in the host for everyone until someone restarts the binary. The user experience is "chat worked, I installed a plugin, now the server crashes on every message."
- Because the generation goroutine is spawned with `context.WithoutCancel`, a retried message (`RetryLastMessage`) re-runs the same path and re-triggers the same panic, producing a crash loop.
- The `envelope_retry` path (L620-L632 and L976-L1044) re-invokes provider streaming for envelope correction; a panic there runs outside the already-closed original generateResponse defer chain, potentially leaking the already-closed channel as well.
- Plugin authors who write filters have no defensive expectation — a filter that dereferences a nil map, type-asserts on an unexpected input, or performs a byte-slice on a non-UTF-8 string will panic. Without recover, every plugin becomes a potential server-wide DoS.

This is "panic in a code path triggered by normal user input" — the Critical rubric exactly.

## Recommendation

Add a top-level recover in `generateResponse` that converts panics into a structured error event on the stream, logs the panic + stack, and returns cleanly:

```go
// internal/service/chat_generate.go — add at the top of generateResponse, before the existing defers
defer func() {
    if r := recover(); r != nil {
        stack := debug.Stack()
        log.Printf("PANIC in generateResponse (session=%s msg=%s): %v\n%s",
            sessionID, assistantMsgID, r, stack)
        // Best-effort error emission — the existing stream-close defer will run after this.
        select {
        case ch <- chat.ErrorEvent(chat.ErrorCodeInternal,
            "Internal error during response generation",
            map[string]interface{}{"raw": fmt.Sprintf("%v", r)}):
        default:
            // Channel full; ignore. The existing defer will close() the channel.
        }
    }
}()
```

Order the recover **before** the existing `defer close(ch)` so that:
1. The panic is caught.
2. The error event is attempted (non-blocking).
3. The existing defers run: `close(ch)`, `CloseStream`, presence cleanup, span.End().

**Additionally**, wrap `FilterRegistry.Apply` at `internal/plugin/filter.go:L147` with a per-handler recover so that a bad plugin fails that filter pass without crashing the caller:

```go
// internal/plugin/filter.go:L144-L155 — sketch
for _, entry := range handlers {
    visible := stripForView(current, entry.View)
    var result interface{}
    err := func() (panicErr error) {
        defer func() {
            if r := recover(); r != nil {
                panicErr = fmt.Errorf("filter handler panic: %v", r)
            }
        }()
        var fnErr error
        result, fnErr = entry.Fn(visible, ctx)
        return fnErr
    }()
    if err != nil {
        return nil, fmt.Errorf("filter %q (plugin %q, priority %d): %w",
            name, entry.PluginID, entry.Priority, err)
    }
    current = result
}
```

This filter-level recover is in-scope for this audit because filter calls are an integral part of the chat engine's message path — but the actual change belongs to the plugin package. Reference it in the finding and let the plugin audit track the edit.

The `generateResponse` top-level recover is the minimum fix. The filter-level recover is defense-in-depth.

## References

- `internal/service/chat.go:L162-L168` — detached goroutine spawn (also L209-L210, L240-L241)
- `internal/service/chat_generate.go:L46-L71` — generateResponse defers (no recover)
- `internal/server/server.go:L123-L133` — HTTP recover (does not cover detached goroutines)
- `internal/plugin/filter.go:L131-L155` — filter Apply without per-handler recover
- Plugin audit (`docs/audits/2026-04-10-plugin-system-plan-eval/`) finding 04 — `Host.EmitEvent` missing panic recovery; this finding is the chat-engine side of the same class of bug, but the chat-service goroutine recover is a separate fix from the plugin host's event-hook recover.
