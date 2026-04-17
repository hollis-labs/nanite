# Audit: Telemetry & Privacy Posture

**Date:** 2026-04-11
**Reviewer:** nanite-reviewer-backend (deep-review)
**Branch:** audit-campaign-2026-04-11

## Scope

**Scope string:** `telemetry-privacy-posture`

**Interpretation:** Enumerate every outbound network call the nanite binary makes, classify what data each sends, whether it is opt-in or default, and whether the user can disable it. Assess local-first guarantees, data at rest, and offline capability. Distinct from observability (internal metrics/logging discipline) — this audit focuses on what leaves the user's machine.

**Packages read in full:**
- `cmd/nanite/main.go` — startup, OTel init, provider registration
- `internal/brand/brand.go` — UserAgent definition
- `internal/chat/activity.go` — activity event emitter
- `internal/crossapp/engine_client.go` — Engine UI command client
- `internal/mcp/general_tools.go` — web_fetch tool
- `internal/mcp/http_transport.go` — MCP HTTP transport
- `internal/memory/extraction.go` — memory extraction hooks
- `internal/plugin/catalog.go` — catalog fetcher
- `internal/plugin/builtin/oembed/fetch.go` — oEmbed fetcher
- `internal/plugin/builtin/giphy/giphy.go` — Giphy plugin
- `internal/secrets/keyring.go` — keychain storage
- `internal/service/container.go` — embedder selection, service wiring
- `internal/store/store.go` — database initialization
- `internal/sandbox/proxy.go` — sandbox HTTP proxy
- `pkg/provider/anthropic.go` — Anthropic adapter (headers)
- `pkg/provider/openai.go` — OpenAI adapter (headers + embedding)
- `pkg/provider/ollama.go` — Ollama adapter (headers + embedding)
- `pkg/provider/provider.go` — Embedder interface
- All other provider adapters (headers only): gemini, mistral, azure_openai, openrouter, openzen

**External libraries read:**
- `../framework/libs/go-otel/feotel.go` — OTel initialization
- `../vanta-conduit/embed.go` — Conduit embedding
- `../vanta-conduit/internal/memory/embed.go` — memory revision embedding

**Frontend scanned:**
- `ui/src/lib/api.ts` — frontend API client
- `ui/src/index.css` — checked for external resources
- Grep across `ui/src/` for analytics/error-reporting SDKs

**Skipped:** Test files (not relevant to outbound network calls). Plugin scaffold templates.

## Methodology

**Categories applied:**
- Outbound network call enumeration (HTTP, HTTPS, TCP)
- Data classification per outbound call (content vs. metadata vs. operational)
- Opt-in vs. default-on assessment
- Disable/control mechanism check
- Data at rest inventory
- Offline capability assessment

**Categories skipped with reason:**
- Security (covered by separate audits: sandbox-hardening, dev-tools-input-validation)
- Concurrency (not relevant to privacy posture)
- Test quality (not relevant to privacy posture)
- Standards/tooling (not relevant to privacy posture)

**Tooling deferred:** `govulncheck`, `golangci-lint`, `go test -race` — not relevant to the privacy posture scope. These are covered by the `whole-repo-tooling-and-tests-sweep` audit.

**Cross-audit grounding:** Reviewed prior audits `2026-04-10-dev-tools-input-validation/` and `2026-04-10-sandbox-hardening/` for overlap with SSRF/outbound concerns. The `web_fetch` SSRF risk noted in finding 04 was previously flagged; this audit adds the User-Agent/privacy dimension.

## Privacy posture summary

**Here is everything that leaves the user's machine:**

| Outbound call | Destination | Data sent | Default state | User can disable? |
|---|---|---|---|---|
| LLM provider API | Provider endpoint (Anthropic, OpenAI, etc.) | Conversation messages, system prompt, tool definitions, model name | Active when provider is configured | Yes — don't configure the provider |
| Embedding API (OpenAI) | `api.openai.com` | Memory summaries, context record text, search queries | Auto-enabled when `OPENAI_API_KEY` is set | No separate toggle |
| Embedding API (Ollama) | `localhost:11434` | Same content as above | Auto-enabled when Ollama is available | No separate toggle (but stays local) |
| OpenTelemetry traces | `OTEL_EXPORTER_OTLP_ENDPOINT` (default: `localhost:4318`) | Span names, durations, model names, tool names, status codes | Always initialized; exports to localhost by default | No toggle (fails silently if no collector) |
| Activity events | `ENGINE_ACTIVITY_URL` | Session IDs, agent IDs, model names, tool names, token counts | **Disabled** by default | Yes — don't set the env var |
| CrossApp commands | `ENGINE_API_URL` (default: `localhost:8085`) | UI navigation commands (page names, filter params) | Targets localhost | Yes — don't set the env var |
| Plugin catalog fetch | User-configured catalog source URLs | Plain HTTP GET (no nanite metadata) | No default sources configured | Yes — don't add catalog sources |
| oEmbed fetch | oEmbed provider endpoints (YouTube, Spotify, etc.) | The URL being previewed | Active when oEmbed plugin is loaded (built-in) | No per-plugin disable |
| Giphy search | `api.giphy.com` | Search query text | Only with API key configured | Yes — don't configure Giphy API key |
| web_fetch tool | Any URL (LLM-directed) | HTTP GET to user/LLM-specified URL | Active (built-in tool) | No per-tool disable |
| MCP HTTP transport | User-configured MCP server URLs | JSON-RPC tool calls and arguments | Only when user adds HTTP MCP servers | Yes — don't add HTTP MCP servers |
| Sandbox proxy | Allowlisted domains only | Proxied requests from sandboxed processes | Active during sandbox execution | Domain allowlist controls scope |

**Here is what does NOT leave the user's machine:**
- Conversation history (stored in local SQLite)
- Extracted memories (stored in local Conduit SQLite)
- Session metadata (local DB)
- Usage/cost data (local DB)
- Plugin settings (local DB)
- User settings (local DB)
- Artifacts (local DB + filesystem)
- API keys (OS keychain)

**Here is what the user can control:**
- Provider selection (determines which LLM API receives conversations)
- `ENGINE_ACTIVITY_URL` (opt-in activity events)
- `ENGINE_API_URL` (opt-in crossapp commands)
- `OTEL_EXPORTER_OTLP_ENDPOINT` (controls where traces go, but no disable toggle)
- Catalog source configuration (opt-in)
- MCP HTTP server configuration (opt-in)
- Giphy API key (opt-in)
- **Cannot control:** Embedding provider selection independently from chat provider; OTel export on/off; oEmbed plugin on/off; web_fetch tool availability

## Findings

### By severity

**Critical (0)**
- _none_

**High (2)**
- [01 — OpenTelemetry exporter always initializes with no opt-out](01-high-otel-always-exports.md)
- [02 — Embedding API calls send raw content to external providers](02-high-embedding-sends-raw-content.md)

**Medium (3)**
- [03 — Activity emitter sends session metadata to Engine](03-medium-activity-emitter-sends-session-metadata.md)
- [04 — web_fetch tool leaks default Go User-Agent](04-medium-web-fetch-no-user-agent-control.md)
- [05 — SQLite database and Conduit store have no encryption at rest](05-medium-no-data-at-rest-encryption.md)

**Low (2)**
- [06 — Plugin catalog fetcher makes outbound HTTP](06-low-catalog-fetcher-outbound.md)
- [07 — oEmbed and Giphy plugins make outbound HTTP](07-low-oembed-and-giphy-outbound.md)

**Info (4)**
- [08 — Provider API calls send only required data](08-info-provider-api-calls-clean.md)
- [09 — No analytics SDKs, no crash reporting, no phone-home](09-info-no-analytics-no-crash-reporting.md)
- [10 — Nanite functions fully offline with Ollama](10-info-offline-capability-assessment.md)
- [11 — CrossApp engine client sends UI commands to local Engine](11-info-crossapp-engine-client.md)

### By topic

**Outbound network — LLM providers**
- [08 — Provider API calls send only required data](08-info-provider-api-calls-clean.md)

**Outbound network — embedding**
- [02 — Embedding API calls send raw content to external providers](02-high-embedding-sends-raw-content.md)

**Outbound network — OTel**
- [01 — OpenTelemetry exporter always initializes with no opt-out](01-high-otel-always-exports.md)

**Outbound network — activity/crossapp**
- [03 — Activity emitter sends session metadata to Engine](03-medium-activity-emitter-sends-session-metadata.md)
- [11 — CrossApp engine client sends UI commands to local Engine](11-info-crossapp-engine-client.md)

**Outbound network — built-in tools**
- [04 — web_fetch tool leaks default Go User-Agent](04-medium-web-fetch-no-user-agent-control.md)

**Outbound network — plugins**
- [06 — Plugin catalog fetcher makes outbound HTTP](06-low-catalog-fetcher-outbound.md)
- [07 — oEmbed and Giphy plugins make outbound HTTP](07-low-oembed-and-giphy-outbound.md)

**Data at rest**
- [05 — SQLite database and Conduit store have no encryption at rest](05-medium-no-data-at-rest-encryption.md)

**Analytics / error reporting**
- [09 — No analytics SDKs, no crash reporting, no phone-home](09-info-no-analytics-no-crash-reporting.md)

**Offline / local-first**
- [10 — Nanite functions fully offline with Ollama](10-info-offline-capability-assessment.md)

## Recommended next steps

1. **Add `NANITE_OTEL_ENABLED` toggle** (finding 01). Make OTel export opt-in. This is the highest-priority privacy improvement because it is the only outbound data flow that is both always-on and has no user control.
2. **Decouple embedding provider from chat provider** (finding 02). Add `NANITE_EMBEDDING_PROVIDER` or `NANITE_EMBEDDING_ENABLED` config so users can choose Ollama for embedding even when using OpenAI for chat.
3. **Wire `brand.UserAgent`** on all outbound HTTP clients, or make a deliberate decision not to (finding 04).
4. **Add `nanite data purge` command** (finding 05). Let users delete their data with one command.
5. **Document the privacy posture**. The summary table in this index is a good starting point for a user-facing privacy page.
6. **Add per-plugin enable/disable** (finding 07). Let users turn off oEmbed and Giphy individually.

## Known issues skipped

- SSRF risk in `web_fetch` — covered by prior audit `2026-04-10-dev-tools-input-validation/`. This audit notes the User-Agent dimension but does not re-flag the SSRF.
- Sandbox proxy domain allowlist gaps — covered by prior audit `2026-04-10-sandbox-hardening/`.

## Noticed but out of scope

- **MCP stdio transport subprocess lifecycle** (`internal/mcp/stdio_transport.go`): Spawned MCP server subprocesses may make their own outbound network calls. Nanite cannot control what an external MCP server does. This is architecturally correct (MCP is a plugin boundary), but users should be aware that adding MCP servers extends the outbound surface. Suggested follow-up scope: `mcp-server-trust-model`.
- **PTY CLI adapters** (`pkg/provider/pty_*.go`): PTY-bridged CLIs (Claude, Codex, Gemini, etc.) make their own API calls. Nanite spawns them but does not proxy or inspect their traffic. The sandbox proxy is available but PTY bridges may bypass it depending on configuration. Suggested follow-up scope: `pty-bridge-network-isolation`.
- **Provider API key rotation/revocation**: API keys stored in OS keychain have no expiry or rotation mechanism. Suggested follow-up scope: `credential-lifecycle`.
- **Log output content**: `log.Printf` calls throughout the codebase may include session IDs, error messages, and operational details in stdout/stderr. If logs are captured by a remote log aggregator, this metadata leaves the machine. Suggested follow-up scope: `observability-logging-discipline` (INDEX.md item 41).
