# [Medium] Non-Anthropic providers lack retry, circuit breaker, and rate tracking

**Scope:** Provider request construction — OpenAI, Ollama, Gemini, Mistral, Azure OpenAI, OpenRouter, OpenZen
**Topic:** Error Handling / Concurrency
**Date:** 2026-04-11

## Problem

Only the Anthropic provider has retry logic, circuit breaker, and rate tracking. The other seven HTTP API providers fail immediately on any retryable status code (429, 529, 5xx). There is no rate pacing, no backoff, and no circuit-open SSE event for the frontend.

## Evidence

Grep for retry/circuit/rate structures across the provider package:

```
rg 'CircuitBreaker|RetryConfig|RateTracker' pkg/provider/*.go --files-with-matches
```

Result: only `anthropic.go` (plus the definition files `circuit.go`, `ratelimit.go`, `retry.go`).

Anthropic's retry loop (`anthropic.go:L342-411`):
```go
for attempt := 0; attempt <= a.Retry.MaxRetries; attempt++ {
    // ... create request, do request ...
    if resp.StatusCode == http.StatusOK { break }
    // ... check RetryableStatusCode, backoff, retry ...
}
```

OpenAI's equivalent (`openai.go:L76-96`):
```go
req, err := http.NewRequestWithContext(ctx, "POST", openaiAPI, bytes.NewReader(payload))
// ...
resp, err := o.client.Do(req)
// ...
if resp.StatusCode != http.StatusOK {
    defer resp.Body.Close()
    errBody, _ := io.ReadAll(resp.Body)
    return nil, fmt.Errorf("openai API error %d: %s", resp.StatusCode, string(errBody))
}
```

All seven non-Anthropic providers follow the same pattern: single attempt, immediate error return on any non-200 status.

## Impact

- **User-facing failures on transient errors.** A single 429 from OpenAI/Gemini/Mistral kills the entire chat turn. The user sees "API error 429: rate limited" with no automatic recovery.
- **No circuit breaker protection.** Under sustained rate limiting from any non-Anthropic provider, the system keeps sending requests at full rate, burning rate limit budget and potentially triggering longer lockouts.
- **Inconsistent behavior.** Users switching between Anthropic (graceful degradation) and any other provider (immediate failure) experience different reliability characteristics from the same application.

This is not a security issue (severity stays Medium) because the providers are talking to trusted first-party APIs, but it is a significant reliability gap. The infrastructure (`circuit.go`, `ratelimit.go`, `retry.go`) already exists in the package — it just isn't wired.

## Recommendation

Extract the retry/circuit/rate pattern from Anthropic into a shared `ResilienceConfig` struct that all HTTP providers embed:

```go
type ResilienceConfig struct {
    Retry          RetryConfig
    CircuitBreaker *CircuitBreaker
    RateTracker    *TokenRateTracker
    OnStatus       StatusCallback
    OnCircuitOpen  func()
}
```

Wire into each provider's constructor with sensible defaults. The `streamChatInternal` pattern from Anthropic can become a shared helper method.

## References

- `pkg/provider/circuit.go`, `ratelimit.go`, `retry.go` — existing infrastructure, unused by 7 of 8 providers.
- `pkg/provider/anthropic.go:L342-411` — reference implementation of the retry loop.
