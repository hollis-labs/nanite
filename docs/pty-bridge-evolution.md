# PTY Bridge — Canonical Context Document

> This is the single source of truth for PTY bridge architecture, implementation status, and planned evolution. Supersedes the original task prompt at `agent-workspaces/execution/pty-bridge-task-prompt.md`.

---

## Current State (2026-03-27)

The PTY bridge is a multi-CLI provider system (`internal/provider/pty.go`) that spawns CLI tools (Claude, Codex, Gemini) in pseudo-terminals, reads structured output, and maps events to Nanite's `StreamEvent` types. It supports multi-turn sessions, per-session sandboxing, MCP tool access, and envelope validation with retry.

### Implemented
- **Provider interface:** `StreamChat`, `StreamChatWithTools`, `Complete`, `Capabilities`
- **CLIAdapter abstraction** (`pty_adapter.go`): pluggable interface for CLI-specific args, parsing, detection
- **Three adapters:** Claude (`pty_claude.go`), Codex (`pty_codex.go`), Gemini (`pty_gemini.go`)
- **Dynamic provider routing:** `session.Provider` routes to correct provider; `inferProvider()` handles `claude-cli`, `codex-cli`, `gemini-cli`; `isPTYProvider()` matches all PTY variants
- **Multi-turn sessions** (Phase 3): CLI session ID captured from system init event, persisted in session metadata, `--resume` used on subsequent messages
- **Per-session sandbox** (Phase 1): `~/.nanite/sandboxes/<session-id>/` with CLAUDE.md (compact rules + pointers) and `.sandbox/` subdir (envelope-schema.md, agent-context.md)
- **Nanite MCP server** (Phase 2): `nanite mcp` subcommand via `mark3labs/mcp-go`, exposes all self-service tools (skills, agents, workflows, envelope helpers) over stdio JSON-RPC. Sandbox `.mcp.json` points CLI to this server.
- **Envelope validation + retry** (Phase 4): `ParseEnvelopes()` returns validation errors; PTY sessions get one correction prompt via `--resume` for fatal errors
- **Utility provider/model:** `UtilityProvider` + `UtilityModel` on Engine, configurable via env vars
- **Registration:** Auto-detection of all available CLIs at startup; backwards-compat `"pty"` alias for Claude
- **Database:** Providers and models seeded for Claude, Codex, Gemini; migrations 010 + 011
- **Frontend:** PTY in provider icons, model picker sets both `model` + `provider` on session
- **Unit tests:** 30+ parser tests (Claude, Codex, Gemini), adapter tests, envelope validation tests, sandbox tests, MCP server tests

### How it works today
1. User selects a CLI model in UI → session gets `provider: "pty-claude"`, `model: "claude-cli"` (or codex/gemini variants)
2. User sends message → engine loads session, resolves provider to PTY bridge
3. Engine creates sandbox dir, writes CLAUDE.md + .sandbox/ + .mcp.json
4. PTY bridge delegates to adapter: `adapter.BuildArgs(prompt, systemPrompt, cliSessionID)` → spawns CLI in sandbox dir
5. CLI discovers Nanite MCP server via `.mcp.json`, can call skills/envelope tools
6. Goroutine reads PTY output, delegates to `adapter.ParseLine()`, emits StreamEvents
7. Engine captures CLI session ID from system init event, persists for `--resume` on next turn
8. Engine parses envelopes from response; on validation errors, sends correction prompt (max 1 retry)
9. Engine forwards events to frontend via SSE (same pipeline as API providers)

### Current limitations
- **Unix only** — `creack/pty` requires darwin/linux (`//go:build !windows`)
- **Codex resume** — Codex CLI resume is interactive-only; PTY adapter uses single-turn `exec`
- **No subprocess fallback** — Windows requires pipe-based adapter (future)

### What was explicitly deferred
- Naked PTY passthrough mode (raw terminal in browser)
- Output backpressure / ACK
- Orphan process tracking
- Process stats (CPU/memory polling)
- Agent state detection (planning/idle/active)
- Subprocess adapter (pipe-based fallback for Windows)

---

## Execution Patterns

### `-p` (single-turn) — for stateless tasks
Spawn CLI, send prompt, get response, process exits. No session state.

**Use for:**
- Utility calls (autoTitle, autoTags, summarization)
- One-off tool calls ("read this file", "run these tests")
- Parallel fan-out (spawn N independent tasks simultaneously)
- Disposable agents — fire-and-forget with no cleanup overhead

**Provider methods:** `Complete()` uses this for non-streaming. `StreamChat`/`StreamChatWithTools` use this for streaming single turns.

### `--resume` (multi-turn) — for conversations
First message uses `-p`. Subsequent messages use `--resume <cli-session-id> -p "<next message>"`. CLI maintains its own conversation history and compaction.

**Use for:**
- Interactive chat sessions (the primary Nanite use case)
- Multi-step debugging / refactoring loops
- Any task where turn N depends on context from turn N-1

**Desired pattern:** All Nanite chat sessions backed by PTY should use `--resume`. All utility/one-shot calls should use `-p`. The provider can decide based on whether a CLI session ID exists for the Nanite session.

---

## Provider Interface Reference

Every Nanite provider (Anthropic, OpenAI, Ollama, PTY) implements:

| Method | Purpose | PTY implementation |
|--------|---------|-------------------|
| `StreamChat(ctx, systemPrompt, messages, model)` | Streaming response, no tools | Spawns CLI with `-p`, streams events |
| `StreamChatWithTools(ctx, systemPrompt, messages, model, tools)` | Streaming with tool definitions | Same as StreamChat — CLI manages its own tools, `tools` param ignored |
| `Complete(ctx, systemPrompt, messages, model)` | Single request → string response | Spawns CLI with `-p`, collects all deltas into string |
| `Capabilities()` | Declare provider features | Returns supported feature flags |

---

## Phase 1: Working Directory & Sandboxing

**Goal:** Each PTY session runs in an isolated directory tailored to its context.

### Design
- Default sandbox directory (e.g. `~/.nanite/sandboxes/<session-id>/`)
- Override hierarchy: global config → workspace config → session-level override
- Pre-populate sandbox with:
  - `CLAUDE.md` — agent behavior, envelope format instructions, compaction-safe context
  - `.mcp.json` — MCP server connections for this session
  - Symlinks or mounts to relevant project files as needed

### Why CLAUDE.md is the durable injection point
Claude CLI reads `CLAUDE.md` from its working directory at boot and re-reads after every context compaction. This makes it the most reliable way to persist instructions across long-running PTY sessions. Agent profiles, envelope format rules, behavioral constraints — anything that must survive compaction belongs here.

---

## Phase 2: Nanite as MCP Server

**Goal:** PTY-spawned CLIs connect back to Nanite for skills, tools, and control.

### Architecture

```
┌──────────────────────────────┐
│         Nanite Engine        │
│  ┌────────┐  ┌────────────┐  │
│  │ Agent  │  │ Tool Broker │  │
│  │Profiles│  │   Rules     │  │
│  └───┬────┘  └──────┬─────┘  │
│      │              │         │
│  ┌───┴──────────────┴─────┐  │
│  │   Nanite MCP Server   │  │
│  │  (exposes skills,      │  │
│  │   tools, envelopes)    │  │
│  └───────────┬────────────┘  │
└──────────────┼───────────────┘
               │ MCP protocol
    ┌──────────┴──────────┐
    │   Claude CLI (PTY)  │
    │  reads .mcp.json    │
    │  connects to Nanite│
    └─────────────────────┘
```

### How it works
1. Before spawning the CLI, Nanite writes a `.mcp.json` in the sandbox directory pointing to its own MCP endpoint
2. CLI discovers the Nanite MCP server at boot alongside any other configured servers
3. Nanite's MCP server exposes:
   - **Skills** as callable MCP tools (same skills the API-backed agents use)
   - **Tool broker rules** — permission checks before execution
   - **Envelope helpers** — tools that return pre-formatted envelope blocks
   - **Agent context** — tools to read agent profile, workspace info, etc.
4. CLI calls these tools like any other MCP tool — no special integration needed

### Benefits
- Single source of truth for skills and tools (Nanite's database)
- Tool broker enforces the same permission rules regardless of provider path
- Skills can be updated centrally without restarting PTY sessions (CLI re-discovers on next tool list)
- No CLI-specific configuration management — everything flows through MCP

---

## Phase 3: Multi-Turn PTY Sessions

**Goal:** Persistent CLI sessions that maintain conversation context across messages.

### Approach
- Use Claude CLI's `--resume <session-id>` flag for subsequent messages
- Map Nanite session IDs to CLI session IDs (store in session metadata or a lookup table)
- First message: `claude -p "..." --output-format stream-json --verbose`
- Subsequent messages: `claude --resume <cli-session-id> -p "..." --output-format stream-json --verbose`

### Considerations
- CLI manages its own context window and compaction — Nanite cannot control this
- `CLAUDE.md` survives CLI compaction, so critical instructions persist
- Nanite's message history and CLI's internal history will diverge — Nanite is the source of truth for the UI, CLI history is internal
- Session cleanup: need to manage CLI session files in the sandbox directory

---

## Phase 4: Envelope Validation & Retry

**Goal:** Detect malformed envelopes and request corrections.

### Design
- Post-processing step in the engine between PTY output and SSE emission
- When an envelope was expected (e.g. after a tool call that should produce structured output) but the response lacks valid envelope JSON:
  1. Log the malformed output
  2. Send a follow-up prompt with the malformed output + correction instructions
  3. In single-turn mode: spawn a new CLI process with context
  4. In multi-turn mode: send a follow-up message in the same session
- Rate-limit retries (max 1 retry per response) to avoid loops

---

## Phase 5: Non-Claude CLI Adapters

**Goal:** Support additional CLI tools via the same PTY bridge pattern.

### Candidates
- **Codex CLI** — OpenAI's CLI tool
- **Gemini CLI** — Google's CLI tool
- **Local tools** — any CLI that accepts a prompt and outputs structured text

### Design
The `CLIConfig` struct is already in the architecture:
```go
type CLIConfig struct {
    Command    string   // binary name or path
    Args       []string // default args
    OutputMode string   // "stream-json", "json-rpc", "text"
    Env        []string // additional env vars
}
```
Each CLI gets its own parser (like `pty_claude.go`) that maps its native output format to StreamEvents. The PTY bridge dispatches to the correct parser based on config.

---

## Frontend Requirements

### Settings UI — Provider & Adapter Defaults

The settings menu needs to expose configuration for the provider/adapter system:

**Global defaults (user-level):**
- **Default adapter:** `http` | `pty` | `subprocess` (subprocess as fallback for Windows)
- **Default provider:** which registered provider to use for new sessions
- **Default model:** which model within that provider
- **Default agent:** which agent profile to assign to new sessions
- **Utility provider + model:** which provider/model handles autoTitle, autoTags, and other background utility calls. Users should be able to experiment (e.g. local Llama for titles).

**Fallback chain:**
- If a user has multiple providers configured, they should be able to set a priority order
- Example: try `pty` first, fall back to `anthropic` API if CLI not available, then `ollama` if API key missing
- The engine uses the first available provider in the chain

**Per-session overrides:**
- Model picker already sets provider + model per session
- Agent picker already works per session
- Adapter (http/pty) should also be settable per session

### Provider/Adapter Locking — Should it change mid-session?

**Question:** Once a session starts with a provider+adapter, should the user be able to switch mid-session?

**Considerations:**

| | Allow switching | Lock at session start |
|---|---|---|
| **Pro** | Flexible — user can try PTY then switch to API if it's slow | Simple mental model — session is consistent |
| **Pro** | Can fall back if a provider goes down mid-conversation | No context confusion (PTY has no history of prior API turns) |
| **Con** | Context discontinuity — PTY has no memory of API turns and vice versa | User must create a new session to try a different provider |
| **Con** | UI state may be inconsistent (tool calls rendered differently) | Less flexible |
| **Con** | Complex edge cases — what happens to in-flight tool loops? | |

**Recommendation:** Lock adapter (http/pty) at session creation. Allow model switching within the same adapter (e.g. switch from Sonnet to Opus on Anthropic API). Display adapter as a read-only badge on the session. Provide an easy "clone session with different adapter" action if the user wants to switch.

**Rationale:** Switching adapters mid-session creates context discontinuity that's confusing and hard to recover from. A PTY session has no knowledge of prior API turns, and vice versa. Better to keep it clean and make it easy to start fresh.

### Session Creation Flow

When creating a new session, the UI should:
1. Use global defaults for adapter + provider + model + agent
2. Allow override at creation time (expandable "Advanced" section or quick-pick)
3. Show the adapter as a visual indicator on the session (e.g. `PTY` badge vs `API` badge)
4. Lock adapter after creation; model/agent remain changeable

### Future: Terminal & Long-Running Tasks

When terminals and long-running tasks are added:
- Terminals are inherently PTY — they are the raw passthrough mode (deferred from first pass)
- Long-running tasks could use either adapter depending on the task type
- The adapter selection UX should account for these additional session types

---

## Backend Requirements

### Provider Fallback Chain

Currently the engine does: `session.Provider` → `inferProvider(model)` → `"anthropic"`. This should evolve to:

1. `session.Provider` (explicit per-session)
2. Agent's preferred provider (agent profile config)
3. User's fallback chain (ordered list from settings)
4. System default (`"anthropic"`)

### Utility Provider Configuration

Currently: `NANITE_UTILITY_PROVIDER` and `NANITE_UTILITY_MODEL` env vars, defaults to Anthropic Sonnet.

Target: stored in user settings (database), editable from settings UI. Support any registered provider — including Ollama for local experimentation with titles/tags.

### Process Lifecycle (future)

As PTY usage scales:
- **Orphan tracking:** detect and kill CLI processes whose parent session is closed/archived
- **Process stats:** CPU/memory monitoring for long-running PTY sessions
- **Health checks:** detect hung CLI processes (no output for N seconds)
- **Concurrency limits:** max simultaneous PTY processes per user/workspace

### Subprocess Adapter (Windows fallback)

For environments without PTY support (Windows), a subprocess adapter that uses `exec.Command` with pipes instead of `creack/pty`. Same provider interface, same parser, different I/O mechanism. Lower fidelity (CLI may detect non-TTY and change behavior) but functional.

---

## Observability

**Principle:** Capture every measurable signal at execution time. This data is critical during development and optionally available for power users in production. Hidden by default for novice users — revealed when they seek it out.

### Data to capture per execution
- Token counts (input, output, cache creation, cache read)
- Cost (USD, computed from model pricing)
- Wall-clock duration (total, API/CLI response time, tool execution time)
- Context size at time of call (messages, tokens, breakdown)
- Model, provider, adapter, agent, mode
- Memory usage (for PTY processes)
- Process stats (for long-running PTY sessions)

### Storage
Record alongside existing usage tracking (`store.RecordUsage`). Extend to capture the full execution snapshot — not just token counts but the complete context at invocation time.

### Utility call comparison
When experimenting with different providers for utility calls (e.g. local Llama vs Anthropic for autoTitle), expose a utility call log or dashboard showing: which provider handled each call, latency, quality (title length, tag relevance), cost. Makes the experimentation loop tight.

### Configuration
- Dev mode: always on, full verbosity
- Production: opt-in via settings, "power user" tier. Not heavy enough to gate behind a flag if the overhead is negligible — just hide the UI surface until the user enables it.

---

## Tool Call Drawer — UI/UX

**Concept:** Separate tool call visibility from the main chat flow. Chat stays clean and linear; tool details live in a dedicated drawer.

### Chat view (default)
- Messages render linearly as they do now
- Tool calls appear inline but **condensed** (icon + tool name + status, same as current minimal mode)
- Clicking a condensed tool call opens the **tool drawer**

### Tool drawer
- Slides down from below the chat header
- Default height: ~10 rows of text
- Updates in real-time during streaming (same as chat)
- Each tool call shown condensed; click to expand and see the full call stack (input, output, timing)
- Supports bookmarks and click-to-copy (same affordances as main chat)

### Drawer states
1. **Closed** — tool calls shown inline only
2. **Compact** (~10 rows) — drawer visible below header, chat visible below it. User can drag to resize or click expand.
3. **Expanded** — drawer fills the entire chat area (between header and input). Click tab to shrink back to compact. Close button to dismiss entirely.

### Configuration
- Global default in settings: `inline` | `drawer` | `drawer-auto` (opens drawer automatically on tool calls)
- Per-session override: toggle via session toolbar
- When set to `inline`, tool calls render fully in the chat stream (current behavior) — useful for debugging or tasks where tool output IS the content

---

## Key Principles

1. **CLAUDE.md is the contract** — Anything the CLI must know goes in the sandbox's CLAUDE.md. It's the only injection point that survives compaction.

2. **MCP is the bridge** — Don't try to make the CLI understand Nanite concepts. Expose them as MCP tools. The CLI stays generic; Nanite controls the environment.

3. **Sandbox directory is the control surface** — Working directory, CLAUDE.md, .mcp.json — these three files define the CLI's entire operating context. Nanite assembles them before spawning.

4. **Nanite is source of truth** — The CLI's internal state (conversation history, compaction) is a black box. Nanite tracks messages, usage, and session state independently.

5. **`-p` for tasks, `--resume` for chats** — Single-turn execution for stateless work (utilities, one-shots, fan-out). Multi-turn sessions for conversational use cases.

6. **Lock adapter, flex model** — Provider adapter (http/pty/subprocess) is set at session creation and doesn't change. Model and agent can be switched within the same adapter.

---

## Testing Checklist

- [x] Unit test: mock PTY output → verify StreamEvent mapping
- [x] Unit test: parser handles all Claude stream-json event types
- [x] Unit test: Codex JSONL parser (message, turn.completed, error, thread.started)
- [x] Unit test: Gemini stream-json parser (init, message, tool_use, result)
- [x] Unit test: CLI session ID context round-trip
- [x] Unit test: sandbox dir context round-trip
- [x] Unit test: sandbox Dir creates directory + .sandbox/ subdir
- [x] Unit test: sandbox Populate writes CLAUDE.md, .sandbox/, .mcp.json
- [x] Unit test: MCP server envelope marker conversion
- [x] Unit test: envelope validation (invalid_json, missing_kind, missing_version, unregistered_type)
- [x] Manual test: create PTY session, send message, verify response streams
- [ ] Integration test: spawn real `claude` with a simple prompt, verify full event flow
- [ ] Envelope test: verify agent-generated envelopes render correctly via PTY path
- [ ] Process cleanup test: cancel session mid-stream, verify CLI process is killed
- [ ] Multi-provider test: switch between PTY and API sessions, verify routing
- [ ] Utility provider test: set autoTitle to use Ollama, verify titles generate
- [ ] Fallback test: remove `claude` from PATH, verify PTY provider not registered, sessions fall back

---

## Files

| File | Purpose |
|------|---------|
| `internal/provider/pty.go` | Generic PTY bridge provider (unix only), delegates to CLIAdapter |
| `internal/provider/cli_adapter.go` | CLIAdapter interface + CLIConfig struct (all platforms) |
| `internal/provider/subprocess.go` | Subprocess bridge provider (all platforms, pipe-based fallback) |
| `internal/provider/subprocess_test.go` | Subprocess bridge tests (mock CLI, sandbox dir, cancellation) |
| `internal/provider/pty_claude.go` | ClaudeAdapter + Claude stream-json parser |
| `internal/provider/pty_codex.go` | CodexAdapter + Codex JSONL parser |
| `internal/provider/pty_gemini.go` | GeminiAdapter + Gemini stream-json parser |
| `internal/provider/pty_claude_test.go` | Claude parser + system event tests |
| `internal/provider/pty_codex_test.go` | Codex parser + adapter tests |
| `internal/provider/pty_gemini_test.go` | Gemini parser + adapter tests |
| `internal/provider/pty_test.go` | Provider tests, context helpers |
| `internal/provider/provider.go` | Provider interface, StreamEvent (with SessionID), context keys |
| `internal/sandbox/sandbox.go` | Per-session sandbox dirs, CLAUDE.md, .sandbox/, .mcp.json |
| `internal/sandbox/sandbox_test.go` | Sandbox unit tests |
| `internal/mcpserver/server.go` | Nanite MCP server (mark3labs/mcp-go, stdio) |
| `internal/mcpserver/handlers.go` | Tool handlers, envelope marker conversion |
| `internal/mcpserver/server_test.go` | MCP server unit tests |
| `internal/chat/engine.go` | Provider routing, sandbox setup, session ID capture, envelope retry |
| `internal/chat/envelope.go` | Envelope types, ParseEnvelopes (with validation), ValidateEnvelope |
| `internal/chat/envelope_test.go` | Envelope validation tests |
| `internal/api/sessions.go` | Session update API (accepts provider field) |
| `internal/store/store.go` | Store with DBPath() |
| `internal/store/sessions.go` | UpdateSessionMetadata() |
| `internal/store/seed.go` | Seeds for Claude, Codex, Gemini providers/models |
| `internal/store/migrations/010_add_pty_provider.sql` | Migration: Claude PTY provider |
| `internal/store/migrations/011_add_codex_gemini_providers.sql` | Migration: Codex + Gemini providers |
| `internal/store/migrations/012_add_user_settings.sql` | Migration: user_settings table + agent default_provider |
| `internal/store/user_settings.go` | UserSettings CRUD (fallback chain, defaults) |
| `internal/api/settings.go` | GET/PUT /api/settings endpoints |
| `cmd/nanite/main.go` | Multi-adapter registration, `nanite mcp` subcommand |
| `ui/src/components/chat/ComposerToolbar.tsx` | Provider icons, model picker sets provider+model |
| `ui/src/lib/types.ts` | AVAILABLE_MODELS includes CLI models |

---

## TODO

### Backend — Completed
- [x] Phase 1: Per-session sandbox directories with CLAUDE.md + .sandbox/ + .mcp.json generation
- [x] Phase 2: Nanite as MCP server (`nanite mcp` subcommand, mark3labs/mcp-go)
- [x] Phase 3: Multi-turn sessions via `--resume`
- [x] Phase 4: Envelope validation + retry logic
- [x] Phase 5: CLI adapter abstraction for non-Claude tools (Codex, Gemini CLI)

### Backend — Completed (this session)
- [x] Subprocess adapter (pipe-based fallback for Windows)
- [x] Provider fallback chain: session → agent preference → user priority list → system default
- [x] Orphan process tracking and cleanup
- [x] Process health checks (detect hung CLI processes)
- [x] Concurrency limits (max simultaneous PTY processes per user/workspace)
- [x] Execution observability: full execution snapshot (duration, context size, adapter, cost)
- [x] Utility call comparison log (provider, latency, quality per background call)

### Backend — Remaining
- [x] Wire utility provider/model from database settings (DB → env var → default fallback chain + live refresh)
- [x] Accept agent_id in session creation API (request → settings.DefaultAgent → mentat-001 fallback)

### Frontend — Completed
- [x] Settings UI: default adapter (http/pty/subprocess), provider, model, agent
- [x] Settings UI: utility provider + model (for autoTitle, autoTags — support Ollama, etc.)
- [x] Settings UI: provider fallback chain (ordered list, drag to reorder)
- [x] Settings UI: tool call display mode — global default
- [x] Settings UI: keyboard shortcuts (view + edit bindings, wired into useKeyboardShortcuts)
- [x] Per-session override for tool call display mode
- [x] Adapter badge on session (read-only after creation)
- [x] "Clone session with different adapter" action
- [x] Tool call drawer: compact, expandable (full chat area), closeable
- [x] Tool call drawer: real-time updates, click-to-copy
- [x] Tool call drawer: drag-to-resize between compact and expanded
- [x] Unified event stream: shared ContentActions + ToolCallItem across inline + drawer
- [x] Dynamic model picker (removed hardcoded AVAILABLE_MODELS)

### Frontend — Completed (prior session)
- [x] Observability dashboard: execution stats widget + utility call comparison (Chart.js + shadcn)
- [x] Observability right-rail widget with at-a-glance stats
- [x] Session creation UX: creation-time overrides (adapter, provider, model, agent) — inline form in sidebar
- [x] Session creation UX: wire default_agent to backend (removed localStorage hack)
- [x] Session creation UX: improve clone to carry title + agent
- [x] Fork session: clone with full message history (`POST /api/sessions/{id}/fork`)
- [x] Error persistence: tool errors + chat errors survive page refresh (localStorage)
- [x] NavRail "New Chat" now passes defaults from userSettings

### Frontend — Completed (2026-03-27 session)
- [x] Preferences panel performance: removed loading gate, optimistic updates, keepPreviousData
- [x] Route conflict fix: `/api/plugins/{id}/config` → `/api/plugin-config/{id}`
- [x] Multi-session presence: `cli_active` (cyan dot), `session_archived` (sidebar invalidation)
- [x] Provider management UI: compact flex-wrap cards, enable/disable, API keys, CLI paths, base URLs
- [x] OS keychain storage for API keys (macOS Keychain / Windows Credential Manager / Linux Secret Service)
- [x] Provider startup: keychain → env var → skip (removes .env dependency)
- [x] All 11 providers seeded on every boot via SeedProviders()
- [x] CLI auto-detection via adapter.Detect() with manual path override
- [x] Model picker filters disabled providers, icons for all providers
- [x] Themed horizontal scrollbars for compact provider card fields

### Frontend — Remaining

### Testing
- [ ] Integration test: full PTY event flow with real CLI
- [ ] Envelope test: PTY path envelope rendering
- [ ] Process cleanup test: context cancellation kills CLI
- [ ] Multi-provider routing test
- [ ] Utility provider test: autoTitle via Ollama
- [ ] Fallback chain test: provider unavailable → next in chain
