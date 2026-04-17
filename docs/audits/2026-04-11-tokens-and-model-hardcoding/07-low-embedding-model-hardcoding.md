# [Low] Embedding model names and dimensions hardcoded per-provider

**Scope:** embedding subsystem
**Topic:** Antipatterns
**Date:** 2026-04-11

## Problem

Each provider that supports embeddings hardcodes its default embedding model name and dimension lookup in two places: `Capabilities().DefaultEmbeddingModel` and `EmbeddingDimensions()` switch statement. Additionally, `internal/service/container.go` hardcodes the specific embedding model choices for the memory system.

## Evidence

**Provider defaults (Capabilities + Embed fallbacks):**

| Provider | Default model | File:Line |
|---|---|---|
| OpenAI | `text-embedding-3-small` | `openai.go:257`, `:297` |
| Ollama | `nomic-embed-text` | `ollama.go:227`, `:257` |
| Gemini | `text-embedding-004` | `gemini.go:290`, `:331` |
| Mistral | `mistral-embed` | `mistral.go:234` |
| Azure OpenAI | `text-embedding-3-small` | `azure_openai.go:250` |

**Dimension lookups (EmbeddingDimensions switch):**

| Provider | Models known | File:Line |
|---|---|---|
| OpenAI | `text-embedding-3-small` (1536), `text-embedding-3-large` (3072), `text-embedding-ada-002` (1536) | `openai.go:L351-361` |
| Azure OpenAI | Same three | `azure_openai.go:L353-363` |
| Ollama | `nomic-embed-text` (768), `all-minilm` (384), `mxbai-embed-large` (1024) | `ollama.go:L321-331` |
| Gemini | `text-embedding-004` (768) | `gemini.go:L390-396` |
| Mistral | `mistral-embed` (1024) | `mistral.go:L329-335` |

**Service container (memory system):**

```go
// internal/service/container.go:258
embeddingModel = "text-embedding-3-large"

// internal/service/container.go:264-266
if _, testErr := ollamaProvider.Embed(probeCtx, "test", "nomic-embed-text"); testErr == nil {
    embeddingModel = "nomic-embed-text"
}
```

Note: the memory system uses `text-embedding-3-large` (3072 dims) while the OpenAI provider's default is `text-embedding-3-small` (1536 dims). This is intentional (memory benefits from higher-dimensional embeddings) but the choice is buried in container wiring, not in a config constant.

## Impact

Low. Embedding model names are stable (OpenAI hasn't deprecated text-embedding-3-* yet; Ollama models are user-installed). The duplication between `Capabilities().DefaultEmbeddingModel` and the `Embed()` method's fallback is minor since they always agree within the same provider file.

The dimension lookup functions return 0 for unknown models, which callers handle (memory system checks dimensions before storing). No crash risk.

## Recommendation

Consider moving embedding model metadata to seed data alongside chat models. This would allow the memory system to read embedding model capabilities from the database rather than hardcoding them in provider code and container wiring.

Low priority. No functional impact today.

## References

- Provider files: `openai.go`, `ollama.go`, `gemini.go`, `mistral.go`, `azure_openai.go`
- `internal/service/container.go:L247-276`
