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

## Pre-Existing Issues (logged for follow-up)

- **Broken connector imports in `plugin.go`:** `github.com/hollis-labs/fragments-engine/connectors/gmail` and `connectors/webhook` — modules don't exist. Compiles today because the file is likely behind a build tag or not reached, but will fail if those paths are resolved. Spotted 2026-03-27.
- **mcp.TestSelfToolsTransport_ListTools:** expects 12 tools, gets 20. Test count is stale after tools were added. Spotted 2026-03-27.
- **server.TestAuthMiddlewareEnabled:** auth middleware not enforcing in test. Returns 200 instead of 401. Spotted 2026-03-27.
- **Seed/migration overlap:** migrations 010/011 and Seed() both insert PTY provider rows. Fixed with INSERT OR IGNORE in seed.go but the duplication pattern is fragile.

---

## Beta Release TODO

### 1. Provider Expansion

#### 1a. More HTTP API providers (backend)
Currently: Anthropic, OpenAI, Ollama. Each implements the `Provider` interface in `internal/provider/`.

- [ ] **Google Gemini API** — HTTP provider (not CLI). Follow `anthropic.go` pattern. Gemini has streaming SSE similar to Anthropic. Register with `GOOGLE_API_KEY` env var.
- [ ] **Mistral API** — OpenAI-compatible API. Could subclass `openai.go` with a different base URL and model mapping, or create a thin `mistral.go`.
- [ ] **Cohere API** — Different streaming format (NDJSON). Needs its own adapter.
- [ ] **Azure OpenAI** — Same protocol as OpenAI but different auth (API key + deployment). Could be a config variant of `openai.go` with `base_url` + `api_version` params.
- [ ] **AWS Bedrock** — SDK-based, not HTTP. Would need the AWS Go SDK. Consider whether this is beta scope.
- [ ] Seed new provider/model rows in `seed.go` and add pricing to `usage.go:modelPricing`.
- [ ] Add models to the dynamic model picker (frontend reads from `/api/models`).

**Reference:** `internal/provider/anthropic.go` (771 lines) is the gold standard. `openai.go` is simpler. New providers should follow the same `StreamChat`/`Complete`/`Capabilities` pattern.

#### 1b. More CLI adapters (backend)
Currently: Claude, Codex, Gemini via PTY + subprocess bridges.

- [ ] **GitHub Copilot CLI** — If it supports a prompt mode with structured output, add a `pty_copilot.go` adapter. Check if `gh copilot` has a non-interactive mode.
- [ ] **Aider** — Popular coding CLI. Has `--message` mode. Would need a parser for its output format.
- [ ] Register new adapters in the `cliAdapters` slice in `main.go:153-158`.

**Reference:** `pty_claude.go` (ClaudeAdapter) is the most complete. `pty_codex.go` and `pty_gemini.go` are simpler. Each adapter implements `CLIAdapter` (defined in `cli_adapter.go`): `Name()`, `BuildArgs()`, `ParseLine()`, `Detect()`.

### 2. Plugin System — Beta Readiness

The plugin SDK (`libs/plugin/`) defines Plugin, Host, EventHook, CRUDHandler, UIComponent interfaces. 14 event types exist. 9 built-in plugins exist. Key gaps for beta:

#### 2a. Plugin config & settings (backend + frontend)
- [ ] **Plugin config registration API** — Plugins should register their config schema via `Host.RegisterConfig(schema)`. Store plugin configs in a `plugin_settings` table (plugin_id → JSON). Provide `Host.GetConfig(key)` / `Host.SetConfig(key, value)` backed by the DB.
- [ ] **Config primitives** — Define a set of config field types (string, bool, int, select, secret) that the frontend renders automatically. Plugins provide field definitions + data; frontend provides the UI.
- [ ] **Config override** — Allow plugins to override primitive rendering with custom components. Gate behind a `developer_mode` flag on user_settings. Add a `recover_mode` flag that disables all plugin overrides and uses default primitives.
- [ ] **API endpoints** — `GET/PUT /api/plugins/{id}/config` for per-plugin settings.

#### 2b. Connector & adapter registration (backend)
- [ ] **Plugin-registered connectors** — Extend the Host interface: `Host.RegisterConnector(name, connector)`. Connectors should implement a standard interface (e.g., `Send(ctx, payload) error`). Currently only webhook and gmail connectors exist in `libs/connectors/`.
- [ ] **Plugin-registered providers** — Allow plugins to register LLM providers at runtime via `Host.RegisterProvider(name, provider)`. This is the preferred way for third-party providers to be added without modifying core code.
- [ ] **Plugin-registered adapters** — Similarly, `Host.RegisterCLIAdapter(name, adapter)` for CLI tool integrations.

#### 2c. Hooks & events completeness (backend)
- [ ] Audit the 14 event types against real plugin needs. Missing candidates: `config.changed`, `plugin.installed`, `plugin.uninstalled`, `session.archived`, `provider.error`, `provider.fallback`.
- [ ] **Pre-hooks** — Some events need pre-hooks (before the action) not just post-hooks. E.g., `message.sending` (can modify/block) vs `message.sent` (notification only). Check if `EventHook.Handle` return value can signal cancellation.
- [ ] **Widget registration** — `UIComponentTypeWidget` exists but verify the frontend actually renders plugin-registered widgets. Check `ui/src/` for widget mount points.

#### 2d. Plugin docs, example, generator (backend + frontend)
- [ ] **Plugin example** — Create a well-documented example plugin that demonstrates: config registration, event hooks, UI component (envelope + widget), connector usage, CRUD handler. The `support-ticket` plugin is closest but needs cleanup.
- [ ] **Plugin generator** — CLI command or script: `conduit plugin init <name>` → scaffolds a plugin directory with boilerplate (plugin.go, config schema, test file, README).
- [ ] **Plugin guide** — Document the full lifecycle: discovery → loading → config → events → UI → uninstall. Cover the Host API, event types, component types, connector pattern.

### 3. Slash Commands & UI/UX from Fragments v1

Currently 11 commands in `internal/chat/commands.go`. Categories: agent, session, tools, help.

#### 3a. Backend slash commands
- [ ] Review Fragments v1 slash commands and port missing ones. Likely candidates: `/agent <name>` (switch agent), `/model <name>` (switch model), `/export` (export session), `/import`, `/search` (search messages).
- [ ] `/status` — Show session info (agent, model, provider, adapter, message count, token usage).
- [ ] `/providers` — List registered providers and their status (available/unavailable).
- [ ] Ensure the command registry pattern in `commands.go` is extensible — plugins should be able to register custom slash commands via `Host.RegisterCommand(name, handler)`.

#### 3b. Frontend UI/UX (frontend)
- [ ] Port relevant UI patterns from Fragments v1 (the user will specify which ones).

### 4. Multi-Session Presence

Presence broadcasts exist (`stream_start`, `stream_end`, `tool_pending`, `tool_resolved`) via SSE at `/api/presence`. Issues:

#### 4a. Multi-live-session presence (backend)
- [ ] Verify that multiple simultaneous streaming sessions broadcast correctly. The `activePresence` sync.Map should handle this, but test with 3+ concurrent sessions.
- [ ] Add presence event for session archive/close so the UI can update immediately.

#### 4b. PTY session presence (backend)
- [ ] PTY sessions DO emit presence via the same engine path — `stream_start` fires when `generateResponse` begins and `stream_end` when it completes. **But**: long-running PTY processes that are active (producing output) between messages don't emit presence. Consider adding a `cli_active` presence event driven by the `ActivityCallback` (Touch) — would show the PTY process is alive and producing output even between formal message boundaries.
- [ ] Tool-level presence for PTY: CLIs manage their own tools internally, so `tool_pending`/`tool_resolved` won't fire for PTY tool calls. Document this as a known limitation or parse tool events from CLI output and forward them as presence events.

### 5. Artifacts

Storage exists (`internal/store/artifacts.go`, `internal/api/artifacts.go`). Upload/download works. Gaps:

#### 5a. Backend
- [ ] **Auto-detect artifacts from responses** — When an assistant response creates/writes a file (detected via tool calls), automatically create an artifact record. Currently artifacts are only created via explicit upload.
- [ ] **Artifact metadata** — Extend metadata to track origin (tool call ID, message ID, agent that created it).

#### 5b. Frontend
- [ ] **Artifacts drawer** — Show session artifacts in a drawer/panel. List with name, type, size, created time. Click to preview (images, code, text) or download.
- [ ] **Inline artifact references** — When a message mentions a created file, link it to the artifact for one-click access.

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
- [ ] **Decision needed:** How standalone should Conduit be? Options:
  1. **Import agentrc configs** — Read `~/.agentrc/config.yaml` and `.agentrc/config.yaml` at startup, create/update AgentProfile records from them. Conduit owns the runtime, agentrc provides definitions.
  2. **Full integration** — Conduit's config loader (`internal/config/`) already merges user+project agentrc YAML. Extend this to populate agent profiles from the merged config.
  3. **Adapter layer** — Define an `AgentSource` interface. One implementation reads from DB, another reads from agentrc YAML. Engine queries the source at runtime. Allows switching or layering.
- [ ] **Agent framework adapters (future)** — Consider adapters for other agent definition formats (e.g., CrewAI, AutoGen, LangGraph agent configs). These would implement the same `AgentSource` interface.

### 7. Small Backend Items (from evolution doc)

- [ ] Wire utility provider/model from database settings (currently reads env vars; frontend UI already writes to DB via user_settings table)
- [ ] Accept `agent_id` in session creation API (currently hardcodes `mentat-001`)

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
