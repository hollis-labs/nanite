# [Low] oEmbed and Giphy plugins make outbound HTTP when triggered

**Scope:** Built-in plugins
**Topic:** Outbound network — plugin features
**Date:** 2026-04-11
**Status, 2026-08-18:** Superseded. Both the oEmbed and Giphy plugin registrations have been cut from the codebase (`TASKS/phase-0/15a-cut-giphy.md`, `TASKS/phase-0/15b-cut-oembed.md`) — neither plugin ships or is registered any longer, so this finding no longer applies to current code. Left in place as an historical audit record; not updated in the index's severity/topic tables.

**Update, 2026-08-18 (`TASKS/phase-0/15a-cut-giphy.md`):** The Giphy self-tool
and plugin registration described below were cut in full — the finding no
longer applies to Giphy. Left in place as a historical record; the oEmbed
finding is unaffected by this task (tracked separately in
`TASKS/phase-0/15b-cut-oembed.md`).

## Problem

Two built-in plugins make outbound HTTP requests to external services when their features are triggered:

1. **oEmbed plugin** — fetches metadata from oEmbed provider endpoints (YouTube, Spotify, etc.) when a URL is detected in a message.
2. **Giphy plugin** (removed 2026-08-18, see update above) — called `api.giphy.com` when the Giphy tool was invoked with a search query. Fell back to static demo URLs when no API key was configured.

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

### Giphy (removed — see update above)

`internal/plugin/builtin/giphy/giphy.go:L103-105` (this evidence path predates
the tool's later move to `internal/mcp/self_tools_giphy.go`, since deleted):
```go
func searchLive(ctx context.Context, apiKey, query string) (*GiphyResult, error) {
    reqURL := fmt.Sprintf("https://api.giphy.com/v1/gifs/search?api_key=%s&q=%s&limit=1&rating=g",
        apiKey, url.QueryEscape(query))
```

Sent the user's search query to Giphy's API. Only fired when the Giphy tool was explicitly invoked (by the LLM or user). Without an API key, returned static demo results with no network call. No longer applicable — the tool and its registration are gone.

## Impact

- **oEmbed:** Triggered by URL detection in messages. The oEmbed provider endpoint learns which URLs the user discusses. This is inherent to oEmbed — the whole point is to fetch metadata from the provider. Users who paste YouTube links expect YouTube to know about the fetch.
- **Giphy:** No longer applicable — the plugin and self-tool were removed in full.
- oEmbed is a built-in plugin that cannot currently be disabled individually (it is compiled into the binary and loaded by the plugin host).

## Recommendation

1. Add per-plugin enable/disable configuration so users can turn off oEmbed if they don't want the outbound calls. (No longer applicable to Giphy — removed.)
2. Document that oEmbed makes outbound requests when triggered.

## References

- `internal/plugin/builtin/oembed/fetch.go:L46-95`
- ~~`internal/plugin/builtin/giphy/giphy.go:L55-130`~~ (removed 2026-08-18)
