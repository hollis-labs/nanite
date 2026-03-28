# Backend Context — Conduit

> Project-specific backend conventions. Loaded by the backend agent role when working in this project.
> Lives at `conduit/.agentrc/agents/backend.md`.

## Stack

- **Go version:** 1.26.1
- **Module path:** `github.com/hollis-labs/conduit`
- **Router:** `net/http` stdlib (`http.ServeMux` with Go 1.22+ method routing: `"GET /api/..."`)
- **Database:** SQLite via `modernc.org/sqlite v1.46.1` (WAL mode, foreign keys, busy_timeout=5000). PostgreSQL via `github.com/lib/pq v1.12.0` for Nexus A2A messaging only.
- **CLI framework:** None (manual `os.Args` switch in `cmd/conduit/main.go`)
- **Config format:** YAML (`gopkg.in/yaml.v3`) for agentrc config; `.env` via `github.com/joho/godotenv`
- **Tracing:** OpenTelemetry (`go.opentelemetry.io/otel v1.41.0`) via `github.com/hollis-labs/otel` wrapper
- **Notable dependencies:**
  - `github.com/hollis-labs/nexus` — A2A messaging (Postgres-backed, local replace)
  - `github.com/hollis-labs/tool-broker` — Tool permission/selection engine (local replace)
  - `github.com/hollis-labs/fragments-engine/plugin` — Plugin SDK interface (local replace)
  - `github.com/hollis-labs/otel` — OTel init wrapper (local replace)
  - `github.com/mark3labs/mcp-go v0.44.1` — MCP protocol client (indirect, used by tool-broker)
  - `github.com/google/uuid v1.6.0` — UUID generation
  - All four local deps use `replace` directives pointing to sibling directories (`../libs/otel`, `../libs/toolbroker`, `../libs/plugin`, `../../nexus`)

## Project Structure

```
cmd/
└── conduit/
    ├── main.go              # Entrypoint: serve, plugin, mcp subcommands (494 lines)
    └── plugin_cmd.go        # Plugin CLI subcommand
internal/
├── agentvalidation/         # Agent config validation rules
├── api/                     # HTTP handlers (22 files, one per resource)
│   ├── api.go               # API struct + RegisterRoutes (100+ routes) + helpers
│   ├── sessions.go          # Session CRUD handlers
│   ├── messages.go          # Message send + SSE streaming handlers
│   ├── tools.go             # Tool broker API
│   ├── agents.go            # Agent profile CRUD
│   ├── a2a.go               # Agent-to-agent messaging
│   ├── workflows.go         # Workflow execution
│   └── ...                  # skills, modes, providers, bookmarks, etc.
├── builders/                # Fluent builders for agents, skills, prompt templates
├── chat/                    # Chat engine (orchestration, streaming, context, delegation)
│   ├── engine.go            # Engine struct + generateResponse loop (1760 lines)
│   ├── context.go           # System prompt assembly
│   ├── orchestrator.go      # Multi-agent task decomposition
│   ├── delegate.go          # Task delegation to child sessions
│   ├── envelope.go          # Structured response envelope creation
│   ├── commands.go          # Slash command handling
│   └── activity.go          # Volon GUI activity events
├── config/                  # agentrc.yaml config loading (user + project merge)
├── contextbroker/           # Universal context retrieval (Cortex, PCC, Engine, Session)
├── crossapp/                # Cross-app engine client
├── filter/                  # Output filter chain (e.g., strip emoji)
├── mcp/                     # MCP client: Manager, transports, built-in tools
│   ├── manager.go           # MCP server lifecycle (449 lines)
│   ├── dev_tools.go         # Built-in dev tools (grep, read, write, glob, edit)
│   ├── general_tools.go     # Built-in general tools (web_fetch, web_search)
│   ├── self_tools.go        # Self-service tools (skill/agent/workflow CRUD)
│   ├── self_tools_transport.go  # Self-service MCP transport (762 lines)
│   ├── stdio_transport.go   # Stdio subprocess transport
│   └── http_transport.go    # HTTP/SSE transport
├── mcpserver/               # Conduit's own MCP server (exposed via `conduit mcp`)
├── plugin/                  # Plugin host, discovery, lifecycle, events
├── provider/                # LLM provider adapters
│   ├── provider.go          # Provider interface + core types
│   ├── anthropic.go         # Anthropic adapter (771 lines)
│   ├── openai.go            # OpenAI adapter
│   ├── ollama.go            # Ollama adapter
│   ├── pty.go               # PTY bridge base (spawns CLI agents as subprocesses)
│   ├── pty_claude.go        # Claude CLI adapter
│   ├── pty_codex.go         # Codex CLI adapter
│   ├── pty_gemini.go        # Gemini CLI adapter
│   ├── circuit.go           # Circuit breaker (per-provider)
│   ├── ratelimit.go         # Rate limiter
│   ├── retry.go             # Retry with backoff
│   ├── cache.go             # Response cache
│   ├── event_pipeline.go    # Streaming event pipeline (transforms + cost monitor)
│   ├── scope_guard.go       # Scope-based safety guardrails
│   └── registry.go          # Provider registry
├── sandbox/                 # Sandboxed execution for agent tools
├── server/                  # HTTP server, middleware, SPA handler, auth
├── store/                   # SQLite persistence layer (embedded migrations)
│   ├── store.go             # DB open, WAL config, migration runner
│   ├── migrations/          # 11 SQL migration files (001-011)
│   ├── seed.go              # Seed data (agents, templates, skills, modes)
│   ├── sessions.go          # Session/Message CRUD
│   ├── agents.go            # Agent profile CRUD
│   ├── skills.go            # Skill CRUD + agent-skill bindings
│   ├── modes.go             # Mode CRUD + agent-mode assignments
│   └── ...                  # usage, templates, a2a, etc.
├── toolclient/              # Tool broker client (selection, permissions, knowledge)
│   ├── broker.go            # ToolClient struct + tool selection (308 lines)
│   ├── tool_knowledge.go    # Tool knowledge base (495 lines)
│   ├── permissions.go       # Permission checking
│   ├── intent.go            # Intent-based tool discovery
│   └── meta_tools.go        # request_tools meta-tool definition
├── truncate/                # Tool output truncation + cleanup
└── workflow/                # YAML-driven workflow engine
    ├── engine.go            # Workflow execution engine
    └── loader.go            # Workflow YAML loader
config/agents/               # Agent profile YAML definitions
plugins/                     # Plugin directory (runtime discovery)
ui/                          # React SPA (see frontend.md)
```

## Package Inventory

| Package | Location | Responsibility |
|---------|----------|----------------|
| api | `internal/api/` | HTTP handlers for all REST endpoints (100+ routes). One file per resource. Uses `http.ServeMux` method routing. |
| chat | `internal/chat/` | Core chat engine: message handling, LLM streaming, tool-use loop, context assembly, session compaction, delegation, orchestration, slash commands, activity events. |
| config | `internal/config/` | Loads and merges agentrc YAML from user-level (`~/.agentrc/agentrc.yaml`) and project-level (`./agentrc.yaml`). |
| contextbroker | `internal/contextbroker/` | Universal context retrieval. Queries multiple sources (Cortex, PCC, Engine, Session) with token budget allocation and relevance ranking. |
| filter | `internal/filter/` | Composable output filter chain applied to LLM responses (e.g., `no_emoji`). |
| mcp | `internal/mcp/` | MCP server manager: lifecycle management for stdio/HTTP transports, built-in dev/general/self-service tools, auto-discovery, tool broker integration. |
| mcpserver | `internal/mcpserver/` | Conduit's own MCP server (JSON-RPC over stdio). Exposes conduit tools to external MCP clients (e.g., Claude CLI). |
| plugin | `internal/plugin/` | Plugin host: discovery, loading, lifecycle, event bus, UI component registry. Supports both built-in and external plugins. |
| provider | `internal/provider/` | LLM provider adapters implementing the `Provider` interface: Anthropic, OpenAI, Ollama, PTY bridge (Claude/Codex/Gemini CLIs). Includes circuit breaker, rate limiter, retry, cache, event pipeline, scope guard. |
| sandbox | `internal/sandbox/` | Sandboxed execution environment for agent tool calls. |
| server | `internal/server/` | HTTP server with middleware chain: recover -> logging -> basicAuth -> CORS. Serves API routes + embedded SPA. |
| store | `internal/store/` | SQLite persistence: embedded migrations, CRUD for sessions, messages, agents, skills, modes, templates, usage, bookmarks, artifacts, a2a messages, MCP servers, workflows. |
| toolclient | `internal/toolclient/` | Tool broker client: intent-based tool selection, permission checking, built-in tool registration, progressive discovery, tool knowledge base. |
| truncate | `internal/truncate/` | Truncates large tool outputs for LLM context windows. Periodic cleanup of saved outputs. |
| workflow | `internal/workflow/` | YAML-driven workflow engine: loads workflow definitions from files and database, executes multi-step workflows with LLM providers. |
| builders | `internal/builders/` | Fluent builder pattern for creating agents, skills, and prompt templates programmatically. |
| agentvalidation | `internal/agentvalidation/` | Validation rules for agent configuration. |
| crossapp | `internal/crossapp/` | Cross-application engine client for inter-service communication. |

## Patterns to Follow

### Error Handling
- Errors are wrapped with `fmt.Errorf("context: %w", err)` consistently across store, config, and engine packages. *File: `internal/store/store.go:38`, `internal/config/config.go:76`*
- API handlers use `a.errorResp(w, status, msg)` for all error responses, which formats `{"error": "message"}`. *File: `internal/api/api.go:196`*
- `a.decode(r, &v)` handles request body parsing with defer close. *File: `internal/api/api.go:200`*
- `a.jsonResp(w, status, data)` handles all JSON responses. *File: `internal/api/api.go:188`*

### HTTP Handlers
- Handler signature: `func (a *API) handleVerb(w http.ResponseWriter, r *http.Request)`. *File: `internal/api/sessions.go`*
- Path parameters via `r.PathValue("id")` (Go 1.22+ `http.ServeMux`). *File: `internal/api/sessions.go:67`*
- Query parameters via `r.URL.Query().Get("param")`. *File: `internal/api/sessions.go:11`*
- Request body decoded into anonymous structs with JSON tags. *File: `internal/api/sessions.go:27-31`*
- All routes registered centrally in `api.go:RegisterRoutes()` with method+path pattern: `"GET /api/resource"`. *File: `internal/api/api.go:32-185`*

### Configuration
- Config loaded once at startup via `config.Load()`. Merges user-level (`~/.agentrc/agentrc.yaml`) with project-level (`./agentrc.yaml`). Project values override user values. *File: `internal/config/config.go:58-68`*
- Provider API keys from environment variables: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`. *File: `cmd/conduit/main.go:126-134`*
- Auth from env: `CONDUIT_AUTH_USER`, `CONDUIT_AUTH_PASSWORD` (no-op when unset). *File: `internal/server/auth.go:14-16`*

### Database Access
- Raw SQL queries with `database/sql`. No ORM or query builder. *File: `internal/store/sessions.go:52-64`*
- COALESCE for nullable columns in SELECT statements. *File: `internal/store/sessions.go:53-55`*
- Embedded migrations via `//go:embed migrations/*.sql`. Migration files listed explicitly in order. *File: `internal/store/store.go:8`, `:67-77`*
- Idempotent migrations: `CREATE TABLE IF NOT EXISTS` and duplicate-column errors silently skipped. *File: `internal/store/store.go:93-95`*
- UUID generation for IDs: `uuid.NewString()`. *File: `internal/store/sessions.go` via `github.com/google/uuid`*

### Provider Interface
- All LLM providers implement `Provider` interface: `StreamChat`, `StreamChatWithTools`, `Complete`, `Capabilities`. *File: `internal/provider/provider.go:84-91`*
- Streaming via channels: `<-chan StreamEvent`. Events: `delta`, `tool_use`, `usage`, `error`, `done`, `session_id`. *File: `internal/provider/provider.go:56-63`*
- Provider registration: `registry.Register("name", provider)`. *File: `cmd/conduit/main.go:128-151`*

### Middleware Chain
- Order: `recoverMiddleware(loggingMiddleware(basicAuthMiddleware(corsMiddleware(mux))))`. *File: `internal/server/server.go:57`*
- CORS allows any localhost origin in dev. *File: `internal/server/server.go:93-109`*
- Basic auth skips `/api/health` and non-API routes. *File: `internal/server/auth.go:24-33`*

### Testing
- Test files co-located with source in the same package (e.g., `store/sessions_test.go`). 43 test files across 15 packages.
- Store tests use `t.TempDir()` for isolated SQLite databases. *File: `internal/store/store_test.go`*
- Provider tests mock HTTP responses via `httptest.NewServer`. *File: `internal/provider/anthropic_test.go`*
- Table-driven tests used in `config_test.go`, `filter_test.go`, `ratelimit_test.go`, `retry_test.go`.
- No mocking frameworks -- all test doubles are inline or use stdlib `httptest`.
- Test coverage is good for: store, provider, toolclient, chat, filter, truncate, builders, contextbroker, sandbox, mcpserver.

### Streaming (SSE)
- Message send returns `message_id` + `stream_url`. Client connects to `/api/stream/{messageID}` for SSE. *File: `internal/api/messages.go:28-33`*
- Events: `delta`, `tool_call`, `tool_result`, `tool_warning`, `status`, `circuit_open`, `session_takeover`, `stream_end`, `error`. *File: `internal/chat/engine.go:92`*
- Session-level SSE deduplication: one active SSE connection per session. New connections send `session_takeover` to the old one. *File: `internal/api/messages.go:181-186`*
- Presence SSE at `/api/presence` broadcasts stream_start/end and tool_pending/resolved. *File: `internal/chat/engine.go:116-121`*

## Anti-Patterns to Avoid

- **God object: `Engine` struct** (1760 lines) -- `internal/chat/engine.go` contains the chat engine, streaming, tool-use loop, context assembly, session management, presence tracking, and utility helpers all in one file. 12 fields on the struct, 6 `sync.Map` fields for concurrent state. Should be split into focused subsystems. *File: `internal/chat/engine.go:130-148`*

- **Manual migration ordering** -- Migration files are listed explicitly as a string slice in `store.go:67-77` rather than using directory listing or a migration framework. Adding a migration requires editing both the file AND the Go source. The embedded FS has 11 files but the code only lists 9. *File: `internal/store/store.go:67-77` vs `internal/store/migrations/` (11 files)*

- **Hardcoded MCP server paths** -- `setupMCPServers()` in `main.go` hardcodes absolute paths like `home + "/go/bin/engine"` and `home + "/Projects-apps/hadron/bin/hadrond"`. These are developer-machine-specific and will break for other contributors. *File: `cmd/conduit/main.go:377-419`*

- **Hardcoded Cortex MCP token** -- A hex token is hardcoded as a fallback in `setupMCPServers()`. *File: `cmd/conduit/main.go:407-409`*

- **Hardcoded default model** -- `"claude-sonnet-4-20250514"` appears as a hardcoded default in `handleDelegateAndAggregate` and `Engine.UtilityModel`. Should be a constant or config value. *File: `internal/api/messages.go:128`, `internal/chat/engine.go:156`*

- **Version string drift** -- `server.go:handleHealth` returns `"0.2.0"`. No central version constant. *File: `internal/server/server.go:88`*

- **Store.Seed() is a 428-line monolith** -- Seeds default agents, templates, skills, modes, and prompt templates all in one file with inline SQL. No structured seed data files. *File: `internal/store/seed.go`*

- **Envelope sync fragility** -- Backend envelope types in `chat/envelope.go` and frontend registry in `ui/src/generated/plugin-envelopes.ts` must be manually kept in sync. Adding a backend type without a frontend entry causes silent data loss. CLAUDE.md explicitly warns about this. *File: `CLAUDE.md:42-57`*

- **Anonymous struct request bodies** -- Every handler defines its own inline `var req struct {...}` for request parsing. No shared request/response types. This makes API documentation and type reuse impossible. *File: `internal/api/sessions.go:27-31`, `internal/api/messages.go:12-15`, etc.*

- **Fat `main.go` wiring** -- `cmdServe()` in `main.go` is 307 lines of manual dependency wiring. No dependency injection container or wire framework. Every new subsystem requires editing main.go. *File: `cmd/conduit/main.go:62-369`*

- **Inconsistent nil checks for optional deps** -- API handlers check `if a.ToolClient == nil` inline. Some handlers (e.g., `handleListTools`) return empty arrays, others return errors. No consistent pattern for optional dependency availability. *File: `internal/api/tools.go:10-12` vs `tools.go:49`*

## Reference Implementations

| Pattern | Reference File | Why it's good |
|---------|---------------|---------------|
| HTTP handler + store interaction | `internal/api/sessions.go` + `internal/store/sessions.go` | Clean handler pattern: decode request, validate, call store, format response. Store methods use raw SQL with COALESCE, return typed structs. Good example for any new CRUD resource. |
| Provider interface + adapter | `internal/provider/provider.go` + `internal/provider/anthropic.go` | Clean interface definition with streaming channels. Anthropic adapter shows full streaming implementation with content blocks, tool use, and usage tracking. |
| Context broker + sources | `internal/contextbroker/broker.go` + `source_pcc.go` | Well-structured plugin architecture: `ContextSource` interface, budget allocation, relevance ranking, timeout handling. Good example of how to add new context sources. |

## Build & Run

- **Build:** `make build` (builds UI first, then Go binary) or `go build -o conduit ./cmd/conduit`
- **Install:** `make install` (builds UI, then `go install ./cmd/conduit` to `~/go/bin/`)
- **Test:** `make test` or `go test ./...`
- **Lint:** `golangci-lint run --new --timeout 30s` (via lefthook pre-commit)
- **Vet:** `go vet ./...` (via lefthook pre-commit)
- **Format:** `gofmt` + `goimports` (via lefthook pre-commit)
- **Run:** `./conduit serve --port 8090 --db ./conduit.db` or `make run`
- **Run (dev):** `./conduit serve --port 8090 --dev` (skips embedded SPA, use Vite dev server separately)
- **Run MCP server:** `./conduit mcp --db ./conduit.db [--session ID]` (stdio JSON-RPC)
- **Docker:** `docker-compose up` (multi-stage: Node build -> Go build -> Alpine runtime, port 8090)

## Notes

- **Deploying changes:** Use Cerberus (`cerberus_rebuild conduit-api --reason "..."`). Direct `go build` outputs to `./conduit` in the project root, but the running service uses `~/go/bin/conduit` installed by Cerberus. These are separate binaries.
- **Pre-commit hooks via lefthook:** `gofmt`, `goimports`, `golangci-lint --new`, `go vet` (parallel). Frontend: `biome check`. Pre-push: `go test ./...`.
- **SPA embedding:** Go binary embeds the built UI from `internal/server/ui_dist/` via `//go:embed`. The `-dev` flag skips this for local development with Vite HMR.
- **Auth:** Optional basic auth via `CONDUIT_AUTH_USER` / `CONDUIT_AUTH_PASSWORD` env vars. Disabled when unset (local dev). `/api/health` is always exempt.
- **A2A messaging:** Prefers Postgres (via `ENGINE_POSTGRES_DSN` or `VOLON_POSTGRES_DSN` env var) for Nexus-backed messaging. Falls back to SQLite when Postgres is unavailable.
- **Provider registration:** Anthropic/OpenAI require API keys; Ollama is always registered (local); PTY adapters auto-detect installed CLI binaries (Claude, Codex, Gemini).
- **Tool broker:** Manages tool permissions and intent-based selection. Progressive discovery kicks in above 5 tools (sends summaries to LLM, LLM requests full schemas via `request_tools` meta-tool).
- **Output filters:** Configurable via `MENTAT_OUTPUT_FILTERS` env var (comma-separated). Default: `no_emoji`.
- **Local replace directives:** Four sibling libraries (`../libs/otel`, `../libs/toolbroker`, `../libs/plugin`, `../../nexus`) are referenced via `replace` in `go.mod`. These must be present locally for builds to work.
