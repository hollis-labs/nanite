# [Medium] web_fetch tool leaks default Go User-Agent; brand.UserAgent is defined but unused

**Scope:** Built-in tools
**Topic:** Outbound network — metadata leakage
**Date:** 2026-04-11

## Problem

The `web_fetch` built-in tool makes outbound HTTP GET requests to arbitrary user/LLM-specified URLs using `http.Client.Get()`. This uses the default Go User-Agent header (`Go-http-client/1.1`), which reveals the technology stack. Meanwhile, `brand.UserAgent` (`Nanite/1.0`) is defined in `internal/brand/brand.go:L28` but is never used anywhere in the codebase — no HTTP client sets it.

Additionally, the `web_fetch` tool has no domain allowlist. The LLM can trigger fetches to any URL, including internal network addresses (SSRF). This is a known concern documented in the reviewer context (`general_tools.go` SSRF risk), but it intersects the privacy scope because the outbound request itself reveals that a nanite user is requesting that URL.

## Evidence

`internal/mcp/general_tools.go:L175-197`:
```go
func (g *GeneralToolsTransport) callWebFetch(args map[string]any) (*ToolResult, error) {
    url, _ := args["url"].(string)
    if url == "" {
        return errorResult("url is required"), nil
    }
    client := &http.Client{Timeout: 10 * time.Second}
    resp, err := client.Get(url)
    // ...
}
```

No User-Agent is set. Go's default `Go-http-client/1.1` will be sent.

`internal/brand/brand.go:L27-28`:
```go
// UserAgent is the HTTP User-Agent header value.
UserAgent = "Nanite/1.0"
```

Grep for `brand.UserAgent` across the entire codebase returns zero non-definition results.

## Impact

- Every `web_fetch` call reveals Go runtime version in the User-Agent to the target server.
- The `brand.UserAgent` constant is dead code from a privacy perspective — it was defined for use but never wired.
- The absence of a User-Agent policy means each outbound HTTP client (providers, MCP, catalog fetcher, oEmbed) sends the default Go UA.

## Recommendation

1. Set `brand.UserAgent` on all outbound HTTP requests, or use a shared `http.Client` with a custom transport that injects it.
2. For `web_fetch` specifically, consider a `User-Agent` like `Nanite/1.0 (web_fetch tool)` so the target knows the request is tool-initiated.
3. The SSRF concern in `web_fetch` is out of scope for this audit (it is a security concern, not a privacy concern), but the cross-reference is noted.

## References

- `internal/mcp/general_tools.go:L175-197`
- `internal/brand/brand.go:L27-28`
- Prior audit: `2026-04-10-dev-tools-input-validation/` (may cover SSRF)
