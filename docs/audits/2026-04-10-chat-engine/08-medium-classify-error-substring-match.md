# [Medium] `ClassifyError` misclassifies any error containing "rate" as a rate-limit error

**Scope:** chat-engine
**Topic:** Error handling
**Date:** 2026-04-10

## Problem

`ClassifyError` in `internal/chat/errors.go` classifies errors by substring-matching against the word `"rate"`. Many error messages that have nothing to do with rate limits contain that substring — "generate", "aggregate", "separate", "rate limit" (the real one), "narrate", "saturate". All of them classify as `ErrorCodeRateLimit`, which triggers rate-limit-specific UX (retry prompts, circuit breaker activation, provider fallback event emission) regardless of the actual error.

## Evidence

```go
// internal/chat/errors.go:L46-L53
// ClassifyError inspects an error string and returns the appropriate ErrorCode.
func ClassifyError(err error) ErrorCode {
    msg := err.Error()
    lower := strings.ToLower(msg)
    if strings.Contains(lower, "429") || strings.Contains(lower, "rate") {
        return ErrorCodeRateLimit
    }
    return ErrorCodeProviderError
}
```

The substring test `strings.Contains(lower, "rate")` matches:
- `"failed to generate response"` → classified as rate limit
- `"failed to aggregate sub-tasks"` (`DelegateAndAggregate` error path) → rate limit
- `"saturated provider pool"` → rate limit
- `"generate request failed"` → rate limit

### Call sites that act on the classification

```go
// internal/service/chat_generate.go:L349-L355
errCode := chat.ClassifyError(err)
s.events.EmitError(ctx, sessionID, "provider_error", err.Error())
if errCode == chat.ErrorCodeRateLimit {
    s.events.EmitRateLimitHit(ctx, sessionID, "anthropic", 0)
}
```

A provider error with "generate" in its message now emits a `rate_limit_hit` activity event and the associated UI treatment.

```go
// internal/service/chat_generate.go:L367-L369
ch <- chat.ErrorEnvelopeDelta(chat.ClassifyError(err), "Provider streaming failed", errDetails)
ch <- chat.ErrorEvent(chat.ClassifyError(err), "Provider streaming failed", errDetails)
```

The envelope delivered to the frontend labels the error as `rate_limit` instead of `provider_error`, potentially driving the wrong UI (retry-after prompt vs. generic error display).

## Impact

- **Wrong UX.** Users see rate-limit language ("Waiting ... for rate limit budget", "Would you like to keep trying?") for errors that are not rate limits. When a real provider issue occurs, the user is told to retry — which does not fix the underlying problem.
- **Wrong events.** `EmitRateLimitHit` is called for non-rate-limit errors, polluting activity metrics and any downstream alerting built on them.
- **Circuit-breaker misfire.** Circuit breaker state changes are driven by classified errors. A stream of "failed to generate content" errors (real-world: malformed model output) fires rate-limit logic and opens the breaker, preventing legitimate retries.
- **Contained blast radius** — the impact is UX and metrics correctness, not data loss or security. Hence Medium, not High. But it's easy to hit in practice once a non-rate-limit error path is exercised.

## Recommendation

Classify errors by type, not by substring:

```go
func ClassifyError(err error) ErrorCode {
    if err == nil {
        return ErrorCodeInternal
    }

    // Check for wrapped typed errors first.
    var rateErr *provider.RateLimitError
    if errors.As(err, &rateErr) {
        return ErrorCodeRateLimit
    }
    var toolErr *ToolError
    if errors.As(err, &toolErr) {
        return ErrorCodeToolError
    }

    // HTTP status codes as a secondary signal.
    msg := err.Error()
    if strings.Contains(msg, "429") {
        return ErrorCodeRateLimit
    }

    return ErrorCodeProviderError
}
```

This requires the provider to emit typed errors (`RateLimitError`, `CircuitOpenError`, etc.) which is a change in `pkg/provider/` — call out as a dependency. In the interim, tighten the substring match to word-boundary:

```go
var ratePattern = regexp.MustCompile(`\brate[\s_-]*limit\b`)
if ratePattern.MatchString(lower) || strings.Contains(lower, "429") {
    return ErrorCodeRateLimit
}
```

Word-boundary + "limit" avoids false positives on `generate`, `aggregate`, etc., while still matching "rate limit", "rate-limit", "rate_limit". Cheap interim fix until typed errors are available.

## References

- `internal/chat/errors.go:L46-L53`
- `internal/service/chat_generate.go:L349-L355` — classification-driven event emission
- `internal/service/chat_generate.go:L367-L369` — error envelope labeling
- `provider-abstractions` queued audit (INDEX.md §7) — the right long-term home for typed provider errors
