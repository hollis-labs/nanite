# Backend Context — Nanite

> Project-specific backend conventions. Loaded by the backend agent role when working in this project.
> Lives at `nanite/.agentrc/agents/backend.md`.

## Stack

- **Go version:** 1.26.1
- **Module path:** `github.com/hollis-labs/nanite`
- **Router:** `net/http` stdlib (`http.ServeMux` with Go 1.22+ method routing: `"GET /api/..."`)
- **Database:** SQLite via `modernc.org/sqlite v1.46.1` (WAL mode, foreign keys, busy_timeout=5000). All messaging is SQLite-native (Nexus/PostgreSQL removed).
- **CLI framework:** None (manual `os.Args` switch in `cmd/nanite/main.go`)
- **Config format:** YAML (`gopkg.in/yaml.v3`) for agentrc config; `.env` via `github.com/joho/godotenv`
- **Tracing:** OpenTelemetry (`go.opentelemetry.io/otel v1.41.0`) via `github.com/hollis-labs/otel` wrapper
- **Notable dependencies:**
  - `github.com/hollis-labs/tool-broker` — Tool permission/selection engine (local replace)
  - `github.com/hollis-labs/go-plugin` — Plugin SDK interface (local replace)
  - `github.com/hollis-labs/otel` — OTel init wrapper (local replace)
  - `github.com/hollis-labs/go-providers` — Shared LLM provider library (local replace)
  - `github.com/mark3labs/mcp-go v0.44.1` — MCP protocol client (indirect, used by tool-broker)
  - `github.com/google/uuid v1.6.0` — UUID generation
  - Local deps use `replace` directives pointing to sibling directories

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
├── config/                  # nanite.yaml config loading (user + project merge)
├── contextbroker/           # Universal context retrieval (Conduit, PCC, Engine, Session)
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
├── plugin/                  # Plugin host, discovery, lifecycle, events, filters
│   ├── events.go            # Event catalog (30+ events), Emit* methods, EmitPreHook
│   ├── filter.go            # FilterRegistry: priority-ordered sync chains, 6 named filter points
│   ├── host.go              # Host: RegisterFilter/ApplyFilter, RegisterEventHook, plugin lifecycle
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
├── sandbox/                 # Sandbox dir lifecycle + delegates to adapter plugins
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
| config | `internal/config/` | Loads and merges nanite YAML from user-level (`~/.nanite/nanite.yaml`) and project-level (`./nanite.yaml`). |
| contextbroker | `internal/contextbroker/` | Universal context retrieval. Queries multiple sources (Conduit, PCC, Engine, Session) with token budget allocation and relevance ranking. |
| filter | `internal/filter/` | Composable output filter chain applied to LLM responses (e.g., `no_emoji`). |
| mcp | `internal/mcp/` | MCP server manager: lifecycle management for stdio/HTTP transports, built-in dev/general/self-service tools, auto-discovery, tool broker integration. |
| mcpserver | `internal/mcpserver/` | Nanite's own MCP server (JSON-RPC over stdio). Exposes nanite tools to external MCP clients (e.g., Claude CLI). |
| plugin | `internal/plugin/` | Plugin host: discovery, loading, lifecycle, event bus, UI component registry. Supports both built-in and external plugins. |
| provider | `internal/provider/` | LLM provider adapters implementing the `Provider` interface: Anthropic, OpenAI, Ollama, PTY bridge (Claude/Codex/Gemini CLIs). Includes circuit breaker, rate limiter, retry, cache, event pipeline, scope guard. |
| sandbox | `internal/sandbox/` | Sandbox directory lifecycle. Delegates content writing to CLIAgentAdapter plugins via AdapterRegistry. |
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
- Config loaded once at startup via `config.Load()`. Merges user-level (`~/.nanite/nanite.yaml`) with project-level (`./nanite.yaml`). Project values override user values. *File: `internal/config/config.go:58-68`*
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

### Resolved (2026-04-08 audit)
- ~~**God object engine.go**~~ — RESOLVED: Decomposed from 1760→197 lines. Logic moved to orchestrator.go, delegate.go, envelope.go, commands.go.
- ~~**Manual migration ordering**~~ — RESOLVED: Auto-discovered via `fs.ReadDir()`. Schema squashed to `001_schema.sql`. Rule: migrations = DDL only, seed.go = data only.
- ~~**Hardcoded MCP server paths**~~ — RESOLVED: MCP servers loaded from database via `loadPersistedMCPServers()`.
- ~~**Hardcoded Conduit MCP token**~~ — RESOLVED: Removed, uses DB-persisted configs.
- ~~**Version string drift**~~ — RESOLVED: Centralized in `internal/version/version.go` (`0.3.0-beta`).
- ~~**Store.Seed() monolith**~~ — RESOLVED: Down to 359 lines, structured by subsystem.
- ~~**Inconsistent nil checks**~~ — RESOLVED: Deliberate pattern now — reads return empty/safe defaults, writes return errors.

### Still Present
- (none — all tech debt items resolved 2026-04-08)

### Resolved (2026-04-08)
- ~~**Hardcoded default model**~~ — Uses `a.Services.UtilityModel` now.
- ~~**Anonymous struct request bodies**~~ — 51 named types extracted to `internal/api/types.go`.
- ~~**Fat `main.go` wiring**~~ — `cmdServe()` down to ~215 lines. Extracted `initProviders`, `initMCP`, `startBackgroundWorkers`, `discoverAndLoadPlugins`.
- ~~**Envelope sync fragility**~~ — A+ approach: `config/envelopes.yaml` manifest + `config/envelopes.schema.json`. Go reads at startup via `chat.InitCoreTypes()`. Codegen generates both core and plugin entries. CI check: `node scripts/generate-plugin-imports.mjs --check`.

## Reference Implementations

| Pattern | Reference File | Why it's good |
|---------|---------------|---------------|
| HTTP handler + store interaction | `internal/api/sessions.go` + `internal/store/sessions.go` | Clean handler pattern: decode request, validate, call store, format response. Store methods use raw SQL with COALESCE, return typed structs. Good example for any new CRUD resource. |
| Provider interface + adapter | `internal/provider/provider.go` + `internal/provider/anthropic.go` | Clean interface definition with streaming channels. Anthropic adapter shows full streaming implementation with content blocks, tool use, and usage tracking. |
| Context broker + sources | `internal/contextbroker/broker.go` + `source_pcc.go` | Well-structured plugin architecture: `ContextSource` interface, budget allocation, relevance ranking, timeout handling. Good example of how to add new context sources. |

## Pre-Existing Issues (logged for follow-up)

### Resolved
- ~~**server.TestAuthMiddlewareEnabled**~~ — Fixed. Test correctly returns 401 on missing/wrong credentials. Spotted 2026-03-27, resolved by 2026-04-08.
- ~~Broken connector imports~~ — cleaned up during rebrand, no broken imports exist.
- ~~mcp.TestSelfToolsTransport_ListTools~~ — test uses `>=` comparison, passes with all 16 tools.
- ~~Seed/migration overlap~~ — migrations squashed to DDL-only `001_schema.sql`, all seed data consolidated in `seed.go`.

---

## Beta Known Issues (canonical list)

**Primary tracking:** [`docs/beta-known-issues.md`](../../docs/beta-known-issues.md). Check this document before starting backend work — it contains the P0 issues currently being fixed (worker field sync, trigger dispatch race) and any P1 issues promoted from investigation.

**Post-beta items filed to Engine backlog:** query `engine_backlog_list --project-id nanite` for deferred items. As of 2026-04-10:
- BLG-20260410-001 — PTY tool-level presence (P3, deferred)
- BLG-20260410-002 — Install service `--dry-run` mode (P3, deferred)
- BLG-20260410-003 — Provider `prompt_too_long` recovery (P2, deferred)
- BLG-20260410-004 — Plugin host per-plugin event hook cleanup (P3, deferred)

---

## Plugin Framework Limitations

Source: [`.nanite/agents/plugin-dev.md`](plugin-dev.md) §Known Limitations. These are framework design notes, not bugs — the backend agent should be aware of them when working in `internal/plugin/`. Some entries predate the 2026-03-28 widget system work and may be stale — verify against current code before treating as load-bearing constraints.

1. Frontend component loading — historically hardcoded. **Likely resolved** by `ui/src/generated/plugin-widgets.ts` + `plugin-envelopes.ts` lazy registries (Task 5, 2026-03-28). Verify before referencing.
2. `plugin.yaml` was documentation-only. **Likely resolved** per `plugin-dev-tasks.md:14` ("plugin.yaml parsing" now resolved). Verify.
3. Plugin auto-discovery was manual via `main.go`. **Likely resolved** per `plugin-dev-tasks.md:14` ("auto-discovery" now resolved). Verify.
4. **No plugin isolation** — plugins run in the same process as the host. This is a design choice, not a bug. Relevant for sandboxing/security posture.
5. No widget/slot system — **RESOLVED** by `WidgetRenderer.tsx` + `plugin-widgets.ts` registry + Widget Admin panel (Task 5, 2026-03-28). Can be struck from the source list.
6. **No quick-action framework** — `action` is a listed `UIComponent` type but no action framework exists. Low priority; no current plugins request it.
7. **CRUD error mapping uses fragile string matching** — in the auto-wired CRUD handler layer. Still true. Candidate for a typed error taxonomy.
8. **`http.ServeMux` doesn't support route removal on unload** — stdlib constraint. Affects hot-unload of plugins with routes; plugins currently remain mounted until a process restart. Unload is rare at runtime, but worth a design note if we care about dynamic plugin lifecycle.

**Action for backend agent:** when next working in `internal/plugin/`, reconcile `plugin-dev.md` §Known Limitations against current state — strike the resolved ones and keep only the real constraints.

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

### 3. Slash Commands & UI/UX from Fragments v1 — Partially Done

- [x] Plugin-registered commands via `Host.RegisterCommand()` — wired in `host.go:718`, `main.go:370`
- Remaining slash commands and UI/UX porting will be addressed as needed.

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

### 6. Agent Adapter Architecture ✅ (PR #11, 2026-04-08)

Universal agent harness with plugin-per-adapter model. Spec: `docs/superpowers/specs/2026-04-08-agent-adapter-architecture-design.md`

#### 6a. Core interfaces — DONE
- [x] `CLIAgentAdapter` interface — Discover, PopulateSandbox, SyncProjectRoot, Priority
- [x] `AgentComposer` extension — ComposePrompt, ListRoles, ListSkills (for role-based composition)
- [x] `AdapterRegistry` — priority-sorted, thread-safe, copy-before-iterate for I/O safety
- [x] Config override cascade — Agent Base → Project → Session, per-field merge (scalars/lists/maps)
- [x] `session_agent_overrides` table + store methods
- [x] Managed section protocol — `<!-- nanite:start -->` blocks in CLI-specific files

#### 6b. Adapter plugins — DONE
- [x] `adapter-nanite-native` — replaces agentrc-sync, CLIAgentAdapter + AgentComposer, .nanite/.agentrc fallback
- [x] `adapter-claude` — reads .claude/agents/, writes CLAUDE.md + .mcp.json (absorbed sandbox.go)
- [x] `adapter-codex` — reads/writes AGENTS.md (Codex/Copilot)
- [x] `adapter-gemini` — reads/writes GEMINI.md
- [x] `adapter-opencode` — reads/writes OPENCODE.md

#### 6c. Infrastructure — DONE
- [x] Nexus dependency removed — A2A messaging SQLite-only
- [x] Discovery tiers 5-6 replaced by AdapterRegistry.DiscoverAll()
- [x] Sandbox.Populate() delegates to AdapterRegistry.PopulateAllSandboxes()
- [x] Go code paths renamed .agentrc → .nanite (nanite-native retains fallback)

#### 6d. Future adapters — DEFERRED
- [ ] CrewAI adapter (first priority — most config-driven)
- [ ] AutoGen adapter (declarative configs only)
- [ ] LangGraph adapter (declarative configs only)

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

### ⚠ Two binaries, only one is live — read this before debugging "my fix isn't taking effect"

Nanite has **two separate binary locations** and running the wrong command will silently leave the Cerberus-managed service running stale code. This is the single most common source of "my fix isn't taking effect" confusion.

| Command | Writes to | Is it the live service? |
|---|---|---|
| `go build ./cmd/nanite` *or* `go build -o nanite ./cmd/nanite` *or* `make build` | `./nanite` (project root) | **NO** — this binary is for local `./nanite serve` runs only. Cerberus does not know about it. |
| `go install ./cmd/nanite` *or* `make install` | `~/go/bin/nanite` | **YES, iff you then restart the service.** This is what Cerberus launches. |
| `cerberus_rebuild nanite-api --reason "..."` | `~/go/bin/nanite` *and* restarts the service | **YES — this is what you always want for a deploy.** |

**The rule:** when you want your change to hit the live `nanite-api` (port 8090), use `cerberus_rebuild nanite-api` — nothing else. `go build` is only for compile-checking or for running a one-off `./nanite serve` outside Cerberus.

Verifying which binary is actually running:

```bash
cerberus_status nanite-api                 # get the PID
lsof -p <PID> | awk '$4=="txt"{print $NF}' # first line is the executable path
```

It should print `/Users/<you>/go/bin/nanite`. If it prints the project-root `./nanite`, you are not running under Cerberus — you launched it manually at some point.

The `-dev` flag is **unrelated** to this. It only changes how the SPA is served (placeholder HTML instead of the `go:embed`'d UI, so Vite can run HMR on a different port — see the `if s.dev` branch at `internal/server/spa.go:19`). Dev mode does not change which binary is running or where it lives.

## Notes

- **Deploying changes:** Use Cerberus (`cerberus_rebuild nanite-api --reason "..."`). See §Build & Run "Two binaries, only one is live" above for the full footgun explanation.
- **Pre-commit hooks via lefthook:** `gofmt`, `goimports`, `golangci-lint --new`, `go vet` (parallel). Frontend: `biome check`. Pre-push: `go test ./...`.
- **SPA embedding:** Go binary embeds the built UI from `internal/server/ui_dist/` via `//go:embed`. The `-dev` flag skips this for local development with Vite HMR.
- **Auth:** Optional basic auth via `NANITE_AUTH_USER` / `NANITE_AUTH_PASSWORD` env vars. Disabled when unset (local dev). `/api/health` is always exempt.
- **A2A messaging:** SQLite-native via `internal/store/a2a.go`. Nexus/PostgreSQL dependency removed (PR #11).
- **Provider registration:** Anthropic/OpenAI require API keys; Ollama is always registered (local); PTY adapters auto-detect installed CLI binaries (Claude, Codex, Gemini).
- **Tool broker:** Manages tool permissions and intent-based selection. Progressive discovery kicks in above 5 tools (sends summaries to LLM, LLM requests full schemas via `request_tools` meta-tool).
- **Output filters:** Configurable via `NANITE_OUTPUT_FILTERS` env var (comma-separated). Default: `no_emoji`.
- **Local replace directives:** Sibling libraries (`../framework/libs/go-otel`, `go-toolbroker`, `go-plugin`, `go-providers`, `go-queue`, `go-mcp`, `../vanta-conduit`) are referenced via `replace` in `go.mod`. These must be present locally for builds to work.
