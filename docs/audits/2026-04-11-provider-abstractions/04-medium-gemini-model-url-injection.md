# [Medium] Gemini model parameter interpolated into URL without validation

**Scope:** Provider request construction — Gemini
**Topic:** Security
**Date:** 2026-04-11

## Problem

The Gemini provider interpolates the `model` parameter directly into a URL path segment without any validation or sanitization. If a malformed model name reaches this code, it could alter the request URL.

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

A `model` value like `../../v1/other-endpoint?ignore=` would change the URL path. A model value containing `#` would truncate the fragment. A model value containing `?foo=bar&` would inject additional query parameters.

The `model` parameter originates from the database (`models` table, set by admin seeding or user configuration). It is not directly user-input from chat, but it does pass through the API layer at session creation time. The blast radius is contained by the fact that `geminiAPI` is a hardcoded constant (`https://generativelanguage.googleapis.com/v1beta/models`), so path traversal can only redirect within the Google API domain.

The Azure OpenAI provider has a similar pattern at `azure_openai.go:L49` where `deployment` is interpolated into the URL — but that value comes from `AZURE_OPENAI_DEPLOYMENT` env var, which is admin-controlled and lower risk.

## Impact

- **URL path manipulation within Google's API domain.** A crafted model name could redirect requests to unintended Gemini API endpoints, potentially triggering different billing or data-handling behavior.
- **API key exposure amplification.** Combined with finding 01 (key in URL), a model name containing `&` could split the key across parameters in unpredictable ways.
- **Low practical exploitability.** The model name comes from a trusted source (database seed or admin settings), not from untrusted user input. The risk is real but requires a compromised admin path.

## Recommendation

Validate the model name before URL interpolation. A simple alphanumeric + hyphen + dot + slash check is sufficient:

```go
import "regexp"

var validModelName = regexp.MustCompile(`^[a-zA-Z0-9._/-]+$`)

func validateModelName(model string) error {
    if !validModelName.MatchString(model) {
        return fmt.Errorf("invalid model name: %q", model)
    }
    return nil
}
```

Apply at the provider interface boundary (each `StreamChat` / `Complete` entry point) or in the registry's `Get` method.

Alternatively, use `url.PathEscape(model)` to ensure the model name is safely encoded for URL path segments. This is simpler but doesn't reject bad input — it just encodes it.

## References

- CWE-20: Improper Input Validation.
- `pkg/provider/gemini.go:L70`, `:L239`, `:L350` — all three URL construction sites.
- Finding 01 in this audit (API key in URL) — the two findings compound.
