# [Medium] InferProvider uses model-name prefix matching to select providers

**Scope:** model-to-provider routing
**Topic:** Antipatterns
**Date:** 2026-04-11

## Problem

`InferProvider()` in `internal/chat/engine.go:110-125` maps model names to provider names using prefix matching and exact string comparisons. This is the primary model-name-based behavior switch in the codebase and will break silently when new models are added that don't match the existing prefix rules.

## Evidence

```go
// internal/chat/engine.go:110-125
func InferProvider(model string) string {
    switch {
    case model == "claude-cli":
        return "pty"
    case model == "codex-cli":
        return "pty-codex"
    case model == "gemini-cli":
        return "pty-gemini"
    case strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "o1-") || strings.HasPrefix(model, "o3-"):
        return "openai"
    case strings.HasPrefix(model, "llama") || strings.HasPrefix(model, "mistral") || strings.HasPrefix(model, "gemma"):
        return "ollama"
    default:
        return "anthropic"
    }
}
```

Issues:

1. **`o1-` prefix is matched but `o4-mini` is not** — `o4-mini` is seeded as an OpenAI model in `seed.go:293` but has no prefix match, so `InferProvider("o4-mini")` returns `"anthropic"` (the default). The `o3-` prefix catches `o3` but not `o4-mini`.

2. **Gemini API models fall through to Anthropic** — `gemini-2.5-flash` (seeded as `gemini-api-001` provider) would match `InferProvider` as... `"anthropic"` (no `gemini-` prefix rule for non-CLI models).

3. **Mistral models routed to Ollama** — `mistral-large-latest` matches `strings.HasPrefix(model, "mistral")` and returns `"ollama"` instead of `"mistral"`. This is incorrect for the Mistral API provider.

4. **OpenRouter/OpenZen models** — models prefixed with `anthropic/`, `openai/`, `google/` (OpenRouter format) or standard model IDs (OpenZen format) have no matching rules.

5. **No coverage for `codestral-latest`** — Mistral's Codestral model doesn't match any prefix.

## Impact

Any session that lacks an explicit provider assignment and uses a non-Anthropic, non-GPT, non-CLI model will be routed to the wrong provider. The `o4-mini` misroute and the Mistral/Gemini misroutes are real: if a user selects these models from the model picker (which reads from seed data) without setting a provider, the request goes to the wrong API endpoint and fails.

In practice, sessions usually carry a provider from the model picker, so this is a fallback path. But delegation (`internal/service/delegation.go`) and auto-inference paths rely on it.

## Recommendation

Replace prefix matching with a database lookup. The `models` table already has `provider_id` for every seeded model. `InferProvider` should query `SELECT provider_type FROM providers WHERE id = (SELECT provider_id FROM models WHERE model_id = ?)` or maintain an in-memory cache of model-to-provider mappings loaded at boot from seed data.

Short-term fix: add missing prefixes (`o4-`, `gemini-` for API models) and fix the `mistral` prefix to differentiate Ollama's mistral models from Mistral API models (use full model ID matching for `-latest` suffix models).

## References

- `internal/chat/engine.go:L110-125`
- `internal/store/seed.go:L284-328` (model-to-provider mappings)
- `internal/chat/engine_test.go:L69` (`o1-preview` test case)
- `internal/service/chat_test.go:L331` (same test case)
