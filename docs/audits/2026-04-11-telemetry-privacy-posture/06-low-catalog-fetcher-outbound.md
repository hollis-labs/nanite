# [Low] Plugin catalog fetcher makes outbound HTTP to configured source URLs

**Scope:** Plugin system
**Topic:** Outbound network — catalog
**Date:** 2026-04-11

## Problem

The `CatalogFetcher` makes outbound HTTP GET requests to configured catalog source URLs to retrieve plugin listings. These requests reveal the user's IP to the catalog server. The catalog source URLs are user-configured (stored in DB), so this is opt-in by definition — you cannot have catalog sources without explicitly adding them.

## Evidence

`internal/plugin/catalog.go:L140-171`:
```go
func (cf *CatalogFetcher) fetchSource(ctx context.Context, src CatalogSource) (*CatalogFile, error) {
    req, err := http.NewRequestWithContext(ctx, "GET", src.URL, nil)
    // ...
    resp, err := cf.client.Do(req)
    // ...
}
```

No User-Agent or custom headers are sent beyond Go defaults. The request sends no nanite-specific metadata — just a plain HTTP GET.

Catalog sources are stored in the database and managed via the API. No default catalog sources are pre-configured.

## Impact

Minimal. This is purely opt-in. Users who add catalog sources know they are fetching from those URLs. The only concern is that the requests use the default Go User-Agent, which reveals the technology stack.

## Recommendation

1. Set `brand.UserAgent` on catalog fetch requests.
2. No other action needed — the opt-in nature of catalog sources makes this a non-issue for privacy.

## References

- `internal/plugin/catalog.go:L60-171`
