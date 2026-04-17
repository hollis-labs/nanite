# [Info] HTTP MCP transport bounded by http.Client timeout

**Scope:** MCP HTTP transport
**Topic:** Backpressure
**Date:** 2026-04-11

## Problem

No problem. Observation for completeness.

## Evidence

`internal/mcp/http_transport.go:L65-L71`:

```go
func NewHTTPTransport(serverURL string) *HTTPTransport {
    return &HTTPTransport{
        serverURL: serverURL,
        client: &http.Client{
            Timeout: 60 * time.Second,
        },
    }
}
```

The HTTP transport uses request-response semantics (not streaming). The `http.Client.Timeout` bounds the entire request lifecycle at 60 seconds. The response body is read via `json.NewDecoder(resp.Body).Decode()`, which reads only as much as needed to decode one JSON object.

However, the error path at `http_transport.go:103` uses `io.ReadAll(resp.Body)` without a size limit, which is the same unbounded-error-body class flagged in `provider-abstractions` finding 02. The 60s timeout bounds the read duration but not the read size.

## Impact

The timeout provides adequate backpressure for normal operation. The unbounded `io.ReadAll` on error responses is already tracked in the provider-abstractions audit as a systemic pattern.

## Recommendation

None beyond the existing recommendation in `provider-abstractions` finding 02 to wrap error body reads in `io.LimitReader`.

## References

- `internal/mcp/http_transport.go:L65-L117`
- `docs/audits/2026-04-11-provider-abstractions/02-high-unbounded-response-bodies.md`
