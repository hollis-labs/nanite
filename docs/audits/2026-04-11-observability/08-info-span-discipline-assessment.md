# [Info] Span discipline in core paths is sound where used

**Scope:** Observability — span discipline
**Topic:** Span discipline
**Date:** 2026-04-11

## Problem

Not a problem. This finding documents the positive state of span discipline in the paths where tracing exists.

## Evidence

Nanite creates spans in 10 locations across 5 files. Every span that is created follows correct patterns:

**Correct `defer span.End()` usage:**
- `internal/service/chat_generate.go:L52-57` — `nanite.service.generateResponse` span with `defer span.End()`
- `internal/service/delegation.go:L23-28` — `nanite.delegateTask` span with `defer span.End()`
- `internal/chat/context_client.go:L53-54` — `nanite.broker.assembleContext` span with `defer span.End()`
- `internal/mcp/manager.go:L117-118` — `nanite.mcp.discoverTools` span with `defer span.End()`
- `internal/mcp/manager.go:L247-248` — `nanite.mcp.toolCall` (via `ToolCallSpan`) with `defer span.End()`
- `pkg/provider/anthropic.go:L665` — `nanite.provider.anthropic.complete` span with `defer span.End()`
- `internal/service/chat_tool_executor.go:L304,341` — `ToolCallSpan` with explicit `span.End()` after use

**Correct error recording:**
- `provSpan.RecordError(err)` + `provSpan.SetStatus(codes.Error, ...)` on provider errors
- `toolSpan.RecordError(...)` + `toolSpan.SetStatus(codes.Error, ...)` on tool failures

**Correct attribute usage:**
- Session ID, message ID, model, iteration count, tool count, message count, token counts all recorded as span attributes
- No user message content in span attributes (privacy-safe)

**Correct goroutine handoff in Anthropic:**
- `pkg/provider/anthropic.go:L420` passes the span into the SSE reader goroutine
- `readSSEWithTracking` (L445-478) records token counts and calls `span.End()` in a deferred function
- This correctly ends the span when the streaming goroutine finishes, not when the HTTP response is received

**The provSpan lifecycle in generateResponse:**
- Created at L288, ended at L344 on error, L459 on success
- Both paths are covered. The error path at L435-439 (streaming error event) returns without ending provSpan — but this is after the channel has been consumed, and the Anthropic span is ended by the SSE reader goroutine, not by generateResponse. The provSpan and the Anthropic internal span are separate. **However**, the provSpan itself has a leak path: if the `for evt := range provCh` loop exits via the `case "error"` branch at L435-439 which calls `return`, provSpan is not ended. This is a minor span leak.

## Impact

The existing span discipline is correct. The coverage gap (only 5 files out of 69 that log) is the real problem — see findings 01, 02, 03, 04. Where spans exist, they are well-constructed.

The provSpan leak on streaming errors is a minor cleanup.

## Recommendation

Fix the provSpan leak: add `provSpan.End()` before the return on the streaming error path at L435-439:

```go
case "error":
    provSpan.RecordError(fmt.Errorf("%s", evt.Error))
    provSpan.SetStatus(codes.Error, evt.Error)
    provSpan.End()
    // ... existing error handling ...
    return
```

## References

- All 10 span creation sites listed in Evidence section
- `telemetry-privacy-posture` audit finding 01 — confirmed no user content in span attributes
