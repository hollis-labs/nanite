# [High] Embedding API calls send raw user content to external providers

**Scope:** Memory / embedding
**Topic:** Outbound network — embedding APIs
**Date:** 2026-04-11

## Problem

When embeddings are enabled (OpenAI or Ollama), raw user conversation content is sent to the embedding provider for vectorization. The memory extraction system sends user message text (up to 1500 chars) to the utility LLM for extraction, and Conduit sends full record payloads — including summaries, descriptions, and body text — to the embedding API. There is no opt-in gate specifically for embedding; the system auto-activates when an API key is present.

## Evidence

### Memory extraction sends user messages to utility LLM

`internal/memory/extraction.go:L159-172`:
```go
prompt := fmt.Sprintf(`Extract a memory from this user message...

User message:
%s
...`, truncateForPrompt(content, 1500))

result, err := e.utilityCall(ctx, prompt)
```

The user's message content (truncated to 1500 chars) is sent as-is in the extraction prompt. This goes to whichever LLM is configured as the utility model.

### Conduit embedding sends record payloads to embedding API

`../vanta-conduit/embed.go:L39-44`:
```go
text := extractTextForEmbedding(rec)
// ...
result, err := c.embedder.Embed(ctx, text, c.embeddingModel)
```

`extractTextForEmbedding` (`../vanta-conduit/embed.go:L110-131`) pulls from JSON fields: `title`, `summary`, `description`, `content`, `body`, `text`, `message`. Falls back to raw payload bytes.

### Memory revision embedding sends summary + body

`../vanta-conduit/internal/memory/embed.go:L49-57`:
```go
func revisionEmbedText(rev Revision) string {
    parts := make([]string, 0, 2)
    if rev.Payload.Summary != "" {
        parts = append(parts, rev.Payload.Summary)
    }
    if rev.Payload.Body != "" {
        parts = append(parts, rev.Payload.Body)
    }
    return strings.Join(parts, "\n")
}
```

### Embedder selection is implicit — no explicit opt-in

`internal/service/container.go:L245-270`:
```go
openaiKey := secrets.Get(secrets.ProviderKeyName("openai-001"))
if openaiKey == "" {
    openaiKey = os.Getenv("OPENAI_API_KEY")
}

if openaiKey != "" {
    oai := provider.NewOpenAI()
    oai.SetAPIKey(openaiKey)
    embedder = oai
    embeddingModel = "text-embedding-3-large"
} else {
    // Falls back to Ollama probe...
}
```

If `OPENAI_API_KEY` is set for LLM chat, embedding auto-activates as a side effect. There is no separate `NANITE_EMBEDDING_ENABLED` toggle.

## Impact

- **With OpenAI embedding:** Memory summaries, memory bodies, context record payloads, and similarity search queries are sent to `api.openai.com`. This can include code snippets, project decisions, user preferences, and potentially secrets the user pasted into chat (which the memory extraction system specifically looks for — phrases like "remember", "always", "never").
- **With Ollama embedding:** Same content is sent to a local Ollama instance (`localhost:11434`). Data stays on-machine.
- **No opt-out:** A user who sets `OPENAI_API_KEY` for chat automatically gets their memories embedded via OpenAI. There is no way to use OpenAI for chat but Ollama for embedding, or to disable embedding entirely while keeping chat.

## Recommendation

1. Add an explicit `NANITE_EMBEDDING_ENABLED` config option (default: `true` for Ollama, `false` for remote providers). Let users opt-in to sending content to remote embedding APIs separately from enabling them for chat.
2. Alternatively, add an `embedding_provider` config field that lets users select `ollama` even when `OPENAI_API_KEY` is set for chat.
3. Document clearly: "When embedding is enabled with a remote provider, memory summaries and context records are sent to that provider's API for vectorization."
4. Consider truncating or summarizing content before embedding to minimize data exposure — memory summaries are already short, but Conduit record payloads can be arbitrarily large.

## References

- `internal/memory/extraction.go:L150-226`
- `internal/service/container.go:L240-290`
- `../vanta-conduit/embed.go:L28-131`
- `../vanta-conduit/internal/memory/embed.go:L18-58`
