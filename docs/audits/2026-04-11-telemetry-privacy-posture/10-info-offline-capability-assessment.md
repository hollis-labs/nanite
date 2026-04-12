# [Info] Nanite functions fully offline with Ollama; graceful degradation documented

**Scope:** Local-first guarantees
**Topic:** Offline capability
**Date:** 2026-04-11

## Problem

No issue. This is an assessment of offline capability.

## Evidence

### What works offline (with local Ollama)

- **Chat:** Ollama provider connects to `localhost:11434`. No internet required. Full streaming, tool calling (if model supports it).
- **Store:** SQLite database. Fully local.
- **Memory:** Conduit memory store is SQLite-based. Fully local.
- **Embedding:** Ollama embedding (`nomic-embed-text`) works locally. Similarity ranking functional.
- **MCP tools:** Stdio-based MCP servers (subprocesses) work offline. Built-in dev tools (read, write, grep, glob, edit) are fully local.
- **Plugins:** Built-in plugins are compiled in. External plugins run as Go code in-process.
- **Frontend:** Embedded SPA served from the Go binary. No CDN dependencies.

### What breaks offline

| Feature | Dependency | Behavior when offline |
|---------|-----------|----------------------|
| Remote LLM providers | Anthropic, OpenAI, Gemini, etc. APIs | API calls fail. Chat with those providers unavailable. |
| Remote embedding (OpenAI) | `api.openai.com` | Embedding fails. Falls back to no-similarity ranking if Ollama also unavailable. |
| PTY CLI adapters | Depend on their own CLIs (Claude CLI, Codex, etc.) which need internet | CLI adapter calls fail. |
| Plugin catalog | Remote catalog URLs | Falls back to local disk cache (`catalog-*.yaml` files). Graceful degradation. |
| oEmbed previews | External oEmbed endpoints | Fetch fails. No preview generated. Non-blocking. |
| Giphy | `api.giphy.com` | Falls back to static demo GIF URLs (which would fail to load in browser). Non-blocking. |
| OTel export | OTLP collector endpoint | Export fails silently. Non-blocking. |
| Activity emitter | Engine server | Disabled by default. When enabled, fails silently. Non-blocking. |
| CrossApp UI commands | Engine server | Fails with logged warning. Non-blocking for nanite core. |
| MCP HTTP transports | Remote MCP servers | Connection fails. Tools from those servers unavailable. |

### Graceful degradation patterns

All outbound calls in nanite follow a consistent pattern: failure is logged, not fatal. No outbound call failure crashes the application or blocks the user's chat flow.

- Provider calls: fail with error event sent to SSE stream.
- Embedding: probe at startup; if unavailable, similarity ranking is simply disabled.
- Activity emitter: `disabled` flag set at startup when URL is empty.
- Catalog fetcher: falls back to local disk cache.
- OTel: init failure is logged as warning, not fatal.

## Impact

Nanite is fully functional offline when paired with a local LLM (Ollama). The architecture is genuinely local-first: all persistence is SQLite, the frontend is embedded, and every remote dependency degrades gracefully.

## Recommendation

1. Document the offline capability matrix (this finding's table is a good starting point for user-facing docs).
2. Consider a `nanite status` command that shows which outbound services are reachable and which features are available/degraded.

## References

- `internal/service/container.go:L245-270` — embedder selection with Ollama fallback
- `internal/plugin/catalog.go:L140-171` — catalog disk cache fallback
- `cmd/nanite/main.go:L88-95` — OTel non-fatal init
- `internal/chat/activity.go:L54-67` — activity emitter disabled by default
