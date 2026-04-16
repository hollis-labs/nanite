# Memory Embeddings

Nanite stores long-term memory via an embedded Vanta Conduit instance. Memory
revisions are persisted regardless of configuration; the embedder only affects
whether **similarity-based recall** is available during context assembly.

## Ranking modes

`memory.RecallOpts.Ranking` accepts:

| Value | Conduit behavior |
|---|---|
| `"activation"` | Recency × access-count weighted score. Good for "what's been hot lately." No query needed. |
| `"chronological"` | Newest-first by `written_at`. No query needed. |
| `"similarity"` | Cosine against the query embedding. Requires `Query` and a configured embedder. Degenerate without both. |
| `"relevance"` | **Hybrid** (Vanta v0.4.0+): BM25 (FTS5) ∪ cosine fused via Reciprocal Rank Fusion, multiplied by status / origin / confidence / recency / activation modifiers. Works with just BM25 if no embedder is configured — fresh memories surface immediately instead of waiting on the async embedder. Requires `Query`. |
| `""` (empty) | Smart default — Conduit resolves to `relevance` when `Query` is non-empty, else `activation`. |

The **context-broker memory source** (`internal/contextbroker/source_memory.go`) uses `relevance` — the primary beneficiary of hybrid recall during auto-assembled context. The **MCP `nanite_memory_recall` tool** passes empty Ranking with the agent-supplied query; Conduit's smart default picks `relevance`. HTTP `/api/memories?tags=...` and `nanite_memory_list`-style queries still use `activation` since they're browses, not queries.

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
