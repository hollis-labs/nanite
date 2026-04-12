# [Info] Provider API calls send only required data — no extra metadata

**Scope:** Provider adapters
**Topic:** Outbound network — LLM providers
**Date:** 2026-04-11

## Problem

No issue. This is a positive finding.

## Evidence

All 8 HTTP-based provider adapters (Anthropic, OpenAI, Ollama, Gemini, Mistral, Azure OpenAI, OpenRouter, OpenZen) were reviewed for what they send in HTTP requests.

### Headers sent per provider

| Provider | Headers |
|----------|---------|
| Anthropic | `Content-Type`, `x-api-key`, `anthropic-version`, `anthropic-beta` |
| OpenAI | `Content-Type`, `Authorization: Bearer` |
| Ollama | `Content-Type` |
| Gemini | `Content-Type` (API key in URL query param) |
| Mistral | `Content-Type`, `Authorization: Bearer` |
| Azure OpenAI | `Content-Type`, `api-key` |
| OpenRouter | `Content-Type`, `Authorization: Bearer` |
| OpenZen | `Content-Type`, `Authorization: Bearer` |

**No provider sends:**
- Custom User-Agent headers (Go default `Go-http-client/1.1` is sent)
- Session IDs
- Nanite version strings
- Usage telemetry
- Device/OS identifiers
- Any custom tracking headers

### Request body contents

All providers send only what the API requires:
- `model` — selected model name
- `messages` — conversation messages (system prompt + user/assistant messages)
- `tools` — tool definitions (when tool calling is active)
- `stream: true` — streaming flag
- `max_tokens` — output limit

The conversation content itself is necessarily sent to the provider (this is the core function), but no additional metadata is attached.

### brand.UserAgent is unused

`internal/brand/brand.go:L28`: `UserAgent = "Nanite/1.0"` is defined but never set on any HTTP request. This means provider APIs see `Go-http-client/1.1` — which reveals less about the client than `Nanite/1.0` would. From a privacy perspective, this is arguably better (less identifying), though the `brand.UserAgent` constant suggests the intent was to use it.

## Impact

No privacy concern beyond the expected data flow. Users who configure a provider API key expect their conversation content to be sent to that provider.

## Recommendation

No action required for privacy. This is the correct behavior.

Note: whether to set `brand.UserAgent` on provider requests is a product decision — it would let providers distinguish nanite traffic from other Go clients. This has analytics implications (provider dashboards could show nanite-originating traffic volume). Consider leaving it unset for privacy, or setting it for debugging purposes with a config toggle.

## References

- `pkg/provider/anthropic.go:L348-351`
- `pkg/provider/openai.go:L80-81`
- `pkg/provider/ollama.go:L81`
- `pkg/provider/gemini.go:L75`
- `pkg/provider/mistral.go:L67-68`
- `pkg/provider/azure_openai.go:L85-86`
- `pkg/provider/openrouter.go:L65-66`
- `pkg/provider/openzen.go:L72-73`
