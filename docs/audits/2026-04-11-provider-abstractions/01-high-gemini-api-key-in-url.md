# [High] Gemini API key passed in URL query string

**Scope:** Provider request construction — Gemini
**Topic:** Security
**Date:** 2026-04-11

## Problem

The Gemini provider embeds the API key directly in the URL query parameter (`key=...`). All other providers pass credentials via HTTP headers (Anthropic: `x-api-key`, OpenAI/OpenRouter/OpenZen/Mistral: `Authorization: Bearer`, Azure: `api-key`). The Gemini path is the sole exception.

## Evidence

`pkg/provider/gemini.go:L70`
```go
url := fmt.Sprintf("%s/%s:streamGenerateContent?alt=sse&key=%s", geminiAPI, model, g.apiKey)
```

`pkg/provider/gemini.go:L239`
```go
url := fmt.Sprintf("%s/%s:generateContent?key=%s", geminiAPI, model, g.apiKey)
```

`pkg/provider/gemini.go:L350`
```go
url := fmt.Sprintf("%s/%s:batchEmbedContents?key=%s", geminiAPI, model, g.apiKey)
```

Three call sites. All three pass `g.apiKey` as a URL query parameter.

## Impact

URL query strings are logged in many default configurations:

1. **HTTP access logs / proxy logs.** Any corporate proxy, debug proxy, or transparent proxy that logs request URLs will capture the API key.
2. **OTel HTTP client instrumentation.** If `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` is wired (it isn't currently, but the project uses OTel throughout), the full URL including query string is recorded as `http.url` span attribute by default.
3. **Error messages.** The `fmt.Errorf("create request: %w", err)` path could embed the URL in an error string that propagates to logs or API error responses.
4. **HTTP Referer headers.** Not directly applicable to POST requests, but if the URL is ever used in a redirect chain, the key leaks via Referer.
5. **Browser history / developer tools.** Not applicable for server-to-server, but relevant if the URL is ever exposed in a debug endpoint.

The key grants full access to the user's Google Cloud AI resources. Exposure is a billing liability and a data-access risk.

Note: Google's Gemini API does support the `key=` query parameter pattern as the documented auth method. However, header-based auth (`x-goog-api-key` header) is equally supported and avoids all of the above exposure vectors.

## Recommendation

Move the API key from the URL to an HTTP header. Google's Gemini API accepts `x-goog-api-key` as a request header.

```go
// Before:
url := fmt.Sprintf("%s/%s:streamGenerateContent?alt=sse&key=%s", geminiAPI, model, g.apiKey)

// After:
url := fmt.Sprintf("%s/%s:streamGenerateContent?alt=sse", geminiAPI, model)
req.Header.Set("x-goog-api-key", g.apiKey)
```

Apply to all three call sites (`:70`, `:239`, `:350`).

## References

- Google Gemini API docs: API key authentication supports both query parameter and `x-goog-api-key` header.
- OWASP: "Sensitive Data in URL" — API keys in query strings are a recognized antipattern.
- All other providers in this package use header-based auth. This is the sole outlier.
