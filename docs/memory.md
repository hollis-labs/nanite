# Memory Embeddings

Nanite stores long-term memory via an embedded Vanta Conduit instance. Memory
revisions are persisted regardless of configuration; the embedder only affects
whether **similarity-based recall** is available during context assembly.

## Configuration (Settings → Memory)

| Field | Values |
|-------|--------|
| Mode | `disabled` (default) · `explicit` |
| Provider | `openai` · `azure_openai` · `ollama` · `gemini` · `mistral` |
| Model | provider-specific embedding model ID |

Status (`active` · `disabled` · `missing_credentials` · `unreachable`) is
computed on each settings read and shown as a badge. When status is not
`active`, the first turn of a new session emits a dismissible warning.

## Defaults

Fresh installs start with `embedding_mode = disabled` — no embedding provider
is contacted until the user explicitly chooses one. Similarity recall is
unavailable in this state; activation-based and chronological recall still work.

## Supported providers

| Provider | Kind | Credential (keychain key · env fallback) | Default models |
|----------|------|------------------------------------------|----------------|
| OpenAI | cloud | `provider-api-key:openai-001` · `OPENAI_API_KEY` | `text-embedding-3-large`, `text-embedding-3-small` |
| Azure OpenAI | cloud | `provider-api-key:azure_openai-001` · `AZURE_OPENAI_API_KEY` (also needs `AZURE_OPENAI_ENDPOINT`, deployment) | `text-embedding-3-large`, `text-embedding-3-small` |
| Ollama | local | none (probed via 3s embed call) | `nomic-embed-text`, `mxbai-embed-large` |
| Gemini | cloud | `provider-api-key:gemini-001` · `GEMINI_API_KEY` | `text-embedding-004` |
| Mistral | cloud | `provider-api-key:mistral-001` · `MISTRAL_API_KEY` | `mistral-embed` |

Anthropic and OpenRouter are chat-only on the `go-providers` side and are not
surfaced in the Memory panel.

## Privacy posture

Embedding providers that run in the cloud (OpenAI, Azure OpenAI, Gemini,
Mistral) **send the memory's summary and body text to the provider** on every
write and every similarity recall. Ollama runs locally and keeps content on the
host.

If privacy is a hard requirement, keep Memory mode set to `disabled` or use
Ollama. The rest of Nanite's memory features (activation ranking, chronological
recall, tag filters, confidence thresholds) work without an embedder.
