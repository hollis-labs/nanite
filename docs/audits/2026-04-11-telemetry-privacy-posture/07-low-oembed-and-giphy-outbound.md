# [Low] oEmbed and Giphy plugins make outbound HTTP when triggered

**Scope:** Built-in plugins
**Topic:** Outbound network — plugin features
**Date:** 2026-04-11
**Status, 2026-08-18:** Superseded. Both the oEmbed and Giphy plugin registrations have been cut from the codebase (`TASKS/phase-0/15a-cut-giphy.md`, `TASKS/phase-0/15b-cut-oembed.md`) — neither plugin ships or is registered any longer, so this finding no longer applies to current code. Left in place as an historical audit record; not updated in the index's severity/topic tables.

## Problem

Two built-in plugins make outbound HTTP requests to external services when their features are triggered:

1. **oEmbed plugin** — fetches metadata from oEmbed provider endpoints (YouTube, Spotify, etc.) when a URL is detected in a message.
2. **Giphy plugin** — calls `api.giphy.com` when the Giphy tool is invoked with a search query. Falls back to static demo URLs when no API key is configured.

Both plugins are loaded as built-in plugins. They are part of the binary but only fire on specific triggers.

## Evidence

### oEmbed

`internal/plugin/builtin/oembed/fetch.go:L46-58`:
```go
func FetchOEmbed(ctx context.Context, provider *Provider, rawURL string) (*OEmbedResult, error) {
    endpoint := strings.Replace(provider.Endpoint, "{url}", url.QueryEscape(rawURL), 1)
    // ...
    resp, err := http.DefaultClient.Do(req)
```

Sends the target URL (from user message content) as a query parameter to the oEmbed provider endpoint. The provider knows which URLs the user is discussing.

### Giphy

`internal/plugin/builtin/giphy/giphy.go:L103-105`:
```go
func searchLive(ctx context.Context, apiKey, query string) (*GiphyResult, error) {
    reqURL := fmt.Sprintf("https://api.giphy.com/v1/gifs/search?api_key=%s&q=%s&limit=1&rating=g",
        apiKey, url.QueryEscape(query))
```

Sends the user's search query to Giphy's API. Only fires when the Giphy tool is explicitly invoked (by the LLM or user). Without an API key, returns static demo results with no network call.

## Impact

- **oEmbed:** Triggered by URL detection in messages. The oEmbed provider endpoint learns which URLs the user discusses. This is inherent to oEmbed — the whole point is to fetch metadata from the provider. Users who paste YouTube links expect YouTube to know about the fetch.
- **Giphy:** Only fires on explicit tool invocation. Search queries are sent to Giphy. Without an API key, no network call is made.
- Both are built-in plugins that cannot currently be disabled individually (they are compiled into the binary and loaded by the plugin host).

## Recommendation

1. Add per-plugin enable/disable configuration so users can turn off oEmbed and Giphy if they don't want the outbound calls.
2. Document that these plugins make outbound requests when triggered.

## References

- `internal/plugin/builtin/oembed/fetch.go:L46-95`
- `internal/plugin/builtin/giphy/giphy.go:L55-130`
