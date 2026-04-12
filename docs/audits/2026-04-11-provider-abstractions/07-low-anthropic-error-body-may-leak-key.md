# [Low] Anthropic error body forwarded verbatim — potential indirect key leak

**Scope:** Provider request construction — Anthropic
**Topic:** Security
**Date:** 2026-04-11

## Problem

When the Anthropic API returns an error, the full error body is captured and stored in `APIError.Message`. The `APIError.Error()` method returns this verbatim. If an Anthropic API error ever echoes the request headers (e.g., in a debug/staging environment, or in a future API change), the API key could propagate into logs, OTel spans, or SSE error events.

## Evidence

`pkg/provider/anthropic.go:L368-375`:
```go
errBody, _ := io.ReadAll(resp.Body)
resp.Body.Close()

apiErr := &APIError{
    StatusCode: resp.StatusCode,
    Message:    string(errBody),  // raw API response body
    RetryAfter: ParseRetryAfter(resp.Header.Get("Retry-After")),
}
```

`pkg/provider/retry.go:L43-48`:
```go
func (e *APIError) Error() string {
    if e.RetryAfter > 0 {
        return fmt.Sprintf("API error %d: %s (retry-after: %s)", e.StatusCode, e.Message, e.RetryAfter)
    }
    return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Message)
}
```

The `APIError` is then:
- Logged at `anthropic.go:L396` (`log.Printf`)
- Recorded on an OTel span at `anthropic.go:L387-388` (`span.RecordError(apiErr)`)
- Returned to the caller, which may forward it to the SSE stream

The same pattern exists in all other providers (OpenAI, Gemini, etc.), where `string(errBody)` is used directly in `fmt.Errorf`.

## Impact

Low severity because Anthropic's production API does not currently echo request headers in error responses. The risk is speculative — it depends on a future API behavior change or a misconfigured proxy that injects headers into error responses. However, the pattern of storing untrusted API response bodies verbatim in error messages that flow to logs and spans is worth flagging as a defense-in-depth concern.

## Recommendation

Truncate and sanitize error bodies before storing:

```go
const maxErrMsg = 1024
msg := string(errBody)
if len(msg) > maxErrMsg {
    msg = msg[:maxErrMsg] + "... (truncated)"
}
```

This also protects against the OOM vector from finding 02 (unbounded error body reads).

## References

- Finding 02 in this audit — unbounded response body reads.
- OWASP: Sensitive Data Exposure — error messages should not contain sensitive data.
