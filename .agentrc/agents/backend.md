# Backend Context — Nanite

> Project-specific backend conventions. Loaded by the backend agent role when working in this project.
> Lives at `nanite/.agentrc/agents/backend.md`.

## Stack

- **Go version:** 1.26.1
- **Module path:** `github.com/hollis-labs/nanite`
- **Router:** `net/http` stdlib (`http.ServeMux` with Go 1.22+ method routing: `"GET /api/..."`)
- **Database:** SQLite via `modernc.org/sqlite v1.46.1` (WAL mode, foreign keys, busy_timeout=5000). PostgreSQL via `github.com/lib/pq v1.12.0` for Nexus A2A messaging only.
- **CLI framework:** None (manual `os.Args` switch in `cmd/nanite/main.go`)
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
└── nanite/
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
├── mcpserver/               # Nanite's own MCP server (exposed via `nanite mcp`)
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
| mcpserver | `internal/mcpserver/` | Nanite's own MCP server (JSON-RPC over stdio). Exposes nanite tools to external MCP clients (e.g., Claude CLI). |
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
- Provider API keys from environment variables: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`. *File: `cmd/nanite/main.go:126-134`*
- Auth from env: `NANITE_AUTH_USER`, `NANITE_AUTH_PASSWORD` (no-op when unset). *File: `internal/server/auth.go:14-16`*

### Database Access
- Raw SQL queries with `database/sql`. No ORM or query builder. *File: `internal/store/sessions.go:52-64`*
- COALESCE for nullable columns in SELECT statements. *File: `internal/store/sessions.go:53-55`*
- Embedded migrations via `//go:embed migrations/*.sql`. Migration files listed explicitly in order. *File: `internal/store/store.go:8`, `:67-77`*
- Idempotent migrations: `CREATE TABLE IF NOT EXISTS` and duplicate-column errors silently skipped. *File: `internal/store/store.go:93-95`*
- UUID generation for IDs: `uuid.NewString()`. *File: `internal/store/sessions.go` via `github.com/google/uuid`*

### Provider Interface
- All LLM providers implement `Provider` interface: `StreamChat`, `StreamChatWithTools`, `Complete`, `Capabilities`. *File: `internal/provider/provider.go:84-91`*
- Streaming via channels: `<-chan StreamEvent`. Events: `delta`, `tool_use`, `usage`, `error`, `done`, `session_id`. *File: `internal/provider/provider.go:56-63`*
- Provider registration: `registry.Register("name", provider)`. *File: `cmd/nanite/main.go:128-151`*

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

- **Hardcoded MCP server paths** -- `setupMCPServers()` in `main.go` hardcodes absolute paths like `home + "/go/bin/engine"` and `home + "/Projects-apps/hadron/bin/hadrond"`. These are developer-machine-specific and will break for other contributors. *File: `cmd/nanite/main.go:377-419`*

- **Hardcoded Cortex MCP token** -- A hex token is hardcoded as a fallback in `setupMCPServers()`. *File: `cmd/nanite/main.go:407-409`*

- **Hardcoded default model** -- `"claude-sonnet-4-20250514"` appears as a hardcoded default in `handleDelegateAndAggregate` and `Engine.UtilityModel`. Should be a constant or config value. *File: `internal/api/messages.go:128`, `internal/chat/engine.go:156`*

- **Version string drift** -- `server.go:handleHealth` returns `"0.2.0"`. No central version constant. *File: `internal/server/server.go:88`*

- **Store.Seed() is a 428-line monolith** -- Seeds default agents, templates, skills, modes, and prompt templates all in one file with inline SQL. No structured seed data files. *File: `internal/store/seed.go`*

- **Envelope sync fragility** -- Backend envelope types in `chat/envelope.go` and frontend registry in `ui/src/generated/plugin-envelopes.ts` must be manually kept in sync. Adding a backend type without a frontend entry causes silent data loss. CLAUDE.md explicitly warns about this. *File: `CLAUDE.md:42-57`*

- **Anonymous struct request bodies** -- Every handler defines its own inline `var req struct {...}` for request parsing. No shared request/response types. This makes API documentation and type reuse impossible. *File: `internal/api/sessions.go:27-31`, `internal/api/messages.go:12-15`, etc.*

- **Fat `main.go` wiring** -- `cmdServe()` in `main.go` is 307 lines of manual dependency wiring. No dependency injection container or wire framework. Every new subsystem requires editing main.go. *File: `cmd/nanite/main.go:62-369`*

- **Inconsistent nil checks for optional deps** -- API handlers check `if a.ToolClient == nil` inline. Some handlers (e.g., `handleListTools`) return empty arrays, others return errors. No consistent pattern for optional dependency availability. *File: `internal/api/tools.go:10-12` vs `tools.go:49`*

## Reference Implementations

| Pattern | Reference File | Why it's good |
|---------|---------------|---------------|
| HTTP handler + store interaction | `internal/api/sessions.go` + `internal/store/sessions.go` | Clean handler pattern: decode request, validate, call store, format response. Store methods use raw SQL with COALESCE, return typed structs. Good example for any new CRUD resource. |
| Provider interface + adapter | `internal/provider/provider.go` + `internal/provider/anthropic.go` | Clean interface definition with streaming channels. Anthropic adapter shows full streaming implementation with content blocks, tool use, and usage tracking. |
| Context broker + sources | `internal/contextbroker/broker.go` + `source_pcc.go` | Well-structured plugin architecture: `ContextSource` interface, budget allocation, relevance ranking, timeout handling. Good example of how to add new context sources. |

## Pre-Existing Issues (logged for follow-up)

- **Broken connector imports in `plugin.go`:** `github.com/hollis-labs/fragments-engine/connectors/gmail` and `connectors/webhook` — modules don't exist. Compiles today because the file is likely behind a build tag or not reached, but will fail if those paths are resolved. Spotted 2026-03-27.
- **mcp.TestSelfToolsTransport_ListTools:** expects 12 tools, gets 20. Test count is stale after tools were added. Spotted 2026-03-27.
- **server.TestAuthMiddlewareEnabled:** auth middleware not enforcing in test. Returns 200 instead of 401. Spotted 2026-03-27.
- **Seed/migration overlap:** migrations 010/011 and Seed() both insert PTY provider rows. Fixed with INSERT OR IGNORE in seed.go but the duplication pattern is fragile.

---

## Beta Release TODO

### 1. Provider Expansion ✅

#### 1a. More HTTP API providers — DONE
Providers: Anthropic, OpenAI, Ollama, Gemini, Mistral, Azure OpenAI, OpenRouter, OpenZen.
All seeded in `seed.go` with model rows and pricing in `usage.go`.

- [x] **Google Gemini API** — `internal/provider/gemini.go`
- [x] **Mistral API** — `internal/provider/mistral.go`
- [x] **Azure OpenAI** — `internal/provider/azure_openai.go`
- [x] **OpenRouter** — `internal/provider/openrouter.go` (model gateway)
- [x] **OpenZen** — `internal/provider/openzen.go`
- [ ] **Cohere API** — Deferred (not beta scope)
- [ ] **AWS Bedrock** — Deferred (not beta scope)
- [x] Seed new provider/model rows in `seed.go` and add pricing to `usage.go:modelPricing`.
- [x] Add models to the dynamic model picker (frontend reads from `/api/models`).

#### 1b. More CLI adapters — DONE
8 adapters: Claude, Codex, Gemini, Copilot, Aider, Junie, Kiro, Qwen.
All use `lookPathExpanded()` (`cli_detect.go`) for robust binary detection.
All registered in `main.go` cliAdapters slice and `handleDetectCLI` API endpoint.

- [x] **GitHub Copilot CLI** — `pty_copilot.go` (standalone + gh extension modes)
- [x] **Aider** — `pty_aider.go`
- [x] **Junie** — `pty_junie.go` (JetBrains, `--output-format json`, `--session-id` resume)
- [x] **Kiro** — `pty_kiro.go` (AWS, `kiro-cli chat --no-interactive`)
- [x] **Qwen** — `pty_qwen.go` (stream-json format, Claude-compatible parsing)
- [x] Register new adapters in `main.go` and `provider_manage.go:handleDetectCLI`.

### 2. Plugin System — Beta Readiness ✅

#### 2a. Plugin config & settings — DONE
- [x] **Plugin config registration API** — `Host.RegisterConfigSchema()`, `plugin_settings` table, `PluginConfig.Get()`/`Set()`
- [x] **Config primitives** — Field types in plugin.yaml schema, frontend renders automatically
- [x] **Config override** — `developer_mode` / `recover_mode` flags in `user_settings` (migration 016)
- [x] **API endpoints** — `GET/PUT /api/plugins/{id}/config`

#### 2b. Connector & adapter registration — DONE
- [x] **Plugin-registered connectors** — `Host.RegisterConnector(name, connector)`
- [x] **Plugin-registered providers** — `Host.RegisterProvider(name, provider)`
- [x] **Plugin-registered adapters** — `Host.RegisterCLIAdapter(name, adapter)`

#### 2c. Hooks & events completeness — DONE
- [x] Event audit — expanded event catalog in `internal/plugin/events.go` (session, agent, message, mode, tool, UI, workflow events + `session.archived`)
- [x] **Pre-hooks** — `EventMessageSending`, `EventToolExecuting` with `EmitPreHook()` cancellation support
- [x] **Widget registration** — `UIComponentTypeWidget` wired, mount points verified

#### 2d. Plugin docs, example, generator — DONE
- [x] **Plugin example** — support-ticket plugin cleaned up
- [x] **Plugin generator** — `nanite plugin new <name>` with `--with-agent`, `--with-envelope`, `--with-crud`
- [x] **Plugin guide** — `docs/plugin-install-guide.md`

### 3. Slash Commands & UI/UX from Fragments v1

Currently 11 commands in `internal/chat/commands.go`. Categories: agent, session, tools, help.

#### 3a. Backend slash commands
- [ ] Review Fragments v1 slash commands and port missing ones. Likely candidates: `/agent <name>` (switch agent), `/model <name>` (switch model), `/export` (export session), `/import`, `/search` (search messages).
- [ ] `/status` — Show session info (agent, model, provider, adapter, message count, token usage).
- [ ] `/providers` — List registered providers and their status (available/unavailable).
- [ ] Ensure the command registry pattern in `commands.go` is extensible — plugins should be able to register custom slash commands via `Host.RegisterCommand(name, handler)`.

#### 3b. Frontend UI/UX (frontend)
- [ ] Port relevant UI patterns from Fragments v1 (the user will specify which ones).

### 4. Multi-Session Presence ✅

#### 4a. Multi-live-session presence — DONE
- [x] Multiple simultaneous streaming sessions broadcast correctly via `activePresence` sync.Map.
- [x] `session_archived` presence event via `BroadcastSessionArchived()` + plugin event `session.archived`.

#### 4b. PTY session presence — DONE
- [x] `cli_active` presence event via `throttledCLIActivePresence()`, driven by ActivityCallback. Configurable throttle via `CLIActiveThrottleSeconds`.
- [ ] Tool-level presence for PTY — documented as known limitation. CLIs manage their own tools internally; `tool_pending`/`tool_resolved` won't fire for PTY tool calls.

### 5. Artifacts ✅

#### 5a. Backend — DONE
- [x] **Auto-detect artifacts from responses** — `attemptAutoArtifact()` in `engine.go` detects file writes via tool calls and creates artifact records with `ArtifactOriginAuto`.
- [x] **Artifact metadata** — Tracks origin (tool call ID, message ID, agent that created it).

#### 5b. Frontend — DONE
- [x] **Artifacts drawer** — `ArtifactsDrawer.tsx` in `drawers/`. Lists artifacts with preview (images, code, markdown), download, back navigation. Eye icon for previewable types.
- [x] **Inline artifact references** — `ArtifactChip.tsx`. `[name](artifact:name)` markdown links render as clickable chips that open the drawer.
- [x] **Artifact upload** — Paperclip button + drag-and-drop in ChatComposer. Keyboard shortcut Cmd+.

### 6. Agent Model — agentrc Compatibility

Currently agents are DB records (AgentProfile in `internal/store/agents.go`). The agentrc system (`~/.agentrc/`) uses YAML composition (roles + skills + context). Key alignment:

#### 6a. Agent schema (backend)
- [ ] **Extend AgentProfile** to support the agentrc-style fields:
  - `unique_id` — stable identifier across versions (currently just `id` which is a UUID)
  - `version` — agent definition version
  - `hash` — content hash, constant through versions (for identity tracking)
  - `tools` — JSON array of allowed/configured tools (currently in `tool_permissions` but as permission rules, not tool lists)
  - `skills` — JSON array of skill IDs (currently managed via `agent_skills` join table)
  - `permissions` — structured permission object (currently `tool_permissions` JSON blob)
  - `directories` — JSON array of directories this agent can access (for sandbox scoping)
- [ ] **Per-project/session overrides** — tools, skills, permissions, and directories should be overridable at the project level (`projects.settings` JSON) and session level (`sessions.metadata` JSON). Define merge semantics: session overrides project overrides agent defaults.

#### 6b. agentrc integration (backend — needs decision)
- [ ] **Decision needed:** How standalone should Nanite be? Options:
  1. **Import agentrc configs** — Read `~/.agentrc/config.yaml` and `.agentrc/config.yaml` at startup, create/update AgentProfile records from them. Nanite owns the runtime, agentrc provides definitions.
  2. **Full integration** — Nanite's config loader (`internal/config/`) already merges user+project agentrc YAML. Extend this to populate agent profiles from the merged config.
  3. **Adapter layer** — Define an `AgentSource` interface. One implementation reads from DB, another reads from agentrc YAML. Engine queries the source at runtime. Allows switching or layering.
- [ ] **Agent framework adapters (future)** — Consider adapters for other agent definition formats (e.g., CrewAI, AutoGen, LangGraph agent configs). These would implement the same `AgentSource` interface.

### 7. Small Backend Items ✅

- [x] Wire utility provider/model from database settings — `UtilityModel` field in `user_settings` table
- [x] Accept `agent_id` in session creation API — `handleCreateSession()` resolves agent from request → settings → fallback

## Build & Run

- **Build:** `make build` (builds UI first, then Go binary) or `go build -o nanite ./cmd/nanite`
- **Install:** `make install` (builds UI, then `go install ./cmd/nanite` to `~/go/bin/`)
- **Test:** `make test` or `go test ./...`
- **Lint:** `golangci-lint run --new --timeout 30s` (via lefthook pre-commit)
- **Vet:** `go vet ./...` (via lefthook pre-commit)
- **Format:** `gofmt` + `goimports` (via lefthook pre-commit)
- **Run:** `./nanite serve --port 8090 --db ./nanite.db` or `make run`
- **Run (dev):** `./nanite serve --port 8090 --dev` (skips embedded SPA, use Vite dev server separately)
- **Run MCP server:** `./nanite mcp --db ./nanite.db [--session ID]` (stdio JSON-RPC)
- **Docker:** `docker-compose up` (multi-stage: Node build -> Go build -> Alpine runtime, port 8090)

## Notes

- **Deploying changes:** Use Cerberus (`cerberus_rebuild nanite-api --reason "..."`). Direct `go build` outputs to `./nanite` in the project root, but the running service uses `~/go/bin/nanite` installed by Cerberus. These are separate binaries.
- **Pre-commit hooks via lefthook:** `gofmt`, `goimports`, `golangci-lint --new`, `go vet` (parallel). Frontend: `biome check`. Pre-push: `go test ./...`.
- **SPA embedding:** Go binary embeds the built UI from `internal/server/ui_dist/` via `//go:embed`. The `-dev` flag skips this for local development with Vite HMR.
- **Auth:** Optional basic auth via `NANITE_AUTH_USER` / `NANITE_AUTH_PASSWORD` env vars. Disabled when unset (local dev). `/api/health` is always exempt.
- **A2A messaging:** Prefers Postgres (via `ENGINE_POSTGRES_DSN` or `VOLON_POSTGRES_DSN` env var) for Nexus-backed messaging. Falls back to SQLite when Postgres is unavailable.
- **Provider registration:** Anthropic/OpenAI require API keys; Ollama is always registered (local); PTY adapters auto-detect installed CLI binaries (Claude, Codex, Gemini).
- **Tool broker:** Manages tool permissions and intent-based selection. Progressive discovery kicks in above 5 tools (sends summaries to LLM, LLM requests full schemas via `request_tools` meta-tool).
- **Output filters:** Configurable via `NANITE_OUTPUT_FILTERS` env var (comma-separated). Default: `no_emoji`.
- **Local replace directives:** Four sibling libraries (`../libs/otel`, `../libs/toolbroker`, `../libs/plugin`, `../../nexus`) are referenced via `replace` in `go.mod`. These must be present locally for builds to work.
