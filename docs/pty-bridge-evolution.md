# PTY Bridge — Canonical Context Document

> This is the single source of truth for PTY bridge architecture, implementation status, and planned evolution. Supersedes the original task prompt at `agent-workspaces/execution/pty-bridge-task-prompt.md`.

---

## Current State (2026-03-27)

The PTY bridge is a new provider (`internal/provider/pty.go`) that spawns Claude CLI in a pseudo-terminal, reads `--output-format stream-json` output, and maps events to Conduit's `StreamEvent` types. It sits alongside the Anthropic, OpenAI, and Ollama providers.

### Implemented
- **Provider interface:** `StreamChat`, `StreamChatWithTools`, `Complete`, `Capabilities`
- **Claude stream-json parser** (`pty_claude.go`): maps `assistant`, `result`, `error` events to `delta`, `tool_use`, `usage`, `done` StreamEvents
- **Dynamic provider routing:** `session.Provider` field routes to correct provider; `inferProvider()` fallback for legacy sessions
- **Utility provider/model:** `UtilityProvider` + `UtilityModel` on Engine, configurable via `CONDUIT_UTILITY_PROVIDER` / `CONDUIT_UTILITY_MODEL` env vars. Decouples autoTitle/autoTags from session provider.
- **Registration:** Conditional in `main.go` — only registers if `claude` binary found in PATH
- **Database:** Provider and model seeded; migration `010_add_pty_provider.sql` for existing DBs
- **Frontend:** PTY in provider icons, Claude CLI in model list, model picker sets both `model` + `provider` on session
- **Unit tests:** 11 parser tests, 4 provider tests

### How it works today
1. User selects "Claude CLI" model in UI → session gets `provider: "pty"`, `model: "claude-cli"`
2. User sends message → engine loads session, resolves provider to PTY bridge
3. PTY bridge extracts last user message, spawns: `claude -p "<message>" --output-format stream-json --verbose --system-prompt "<agent prompt>"`
4. Goroutine reads PTY output line by line, parses JSON, emits StreamEvents on channel
5. Engine forwards events to frontend via SSE (same pipeline as API providers)
6. Process exits after response; no state persists between turns

### Current limitations
- **Single-turn only** — each message spawns a fresh CLI process (`-p` flag)
- **Last message only** — conversation history is not sent; CLI has no context of prior turns
- **Conduit's working directory** — CLI runs in Conduit's project dir, not a per-session sandbox
- **CLI's own MCP servers** — not Conduit's MCP connections
- **No agent profile injection** — CLI is unaware of Conduit agents, skills, tool broker rules
- **No envelope format validation** — malformed envelopes are silently dropped
- **Unix only** — `creack/pty` requires darwin/linux (`//go:build !windows`)

### What was explicitly deferred (first pass)
- Multi-turn session persistence
- Naked PTY passthrough mode (raw terminal in browser)
- Non-Claude CLI adapters (Codex, Gemini CLI, etc.)
- Output backpressure / ACK
- Orphan process tracking
- Process stats (CPU/memory polling)
- Agent state detection (planning/idle/active)

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
- Interactive chat sessions (the primary Conduit use case)
- Multi-step debugging / refactoring loops
- Any task where turn N depends on context from turn N-1

**Desired pattern:** All Conduit chat sessions backed by PTY should use `--resume`. All utility/one-shot calls should use `-p`. The provider can decide based on whether a CLI session ID exists for the Conduit session.

---

## Provider Interface Reference

Every Conduit provider (Anthropic, OpenAI, Ollama, PTY) implements:

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
- Default sandbox directory (e.g. `~/.conduit/sandboxes/<session-id>/`)
- Override hierarchy: global config → workspace config → session-level override
- Pre-populate sandbox with:
  - `CLAUDE.md` — agent behavior, envelope format instructions, compaction-safe context
  - `.mcp.json` — MCP server connections for this session
  - Symlinks or mounts to relevant project files as needed

### Why CLAUDE.md is the durable injection point
Claude CLI reads `CLAUDE.md` from its working directory at boot and re-reads after every context compaction. This makes it the most reliable way to persist instructions across long-running PTY sessions. Agent profiles, envelope format rules, behavioral constraints — anything that must survive compaction belongs here.

---

## Phase 2: Conduit as MCP Server

**Goal:** PTY-spawned CLIs connect back to Conduit for skills, tools, and control.

### Architecture

```
┌──────────────────────────────┐
│         Conduit Engine        │
│  ┌────────┐  ┌────────────┐  │
│  │ Agent  │  │ Tool Broker │  │
│  │Profiles│  │   Rules     │  │
│  └───┬────┘  └──────┬─────┘  │
│      │              │         │
│  ┌───┴──────────────┴─────┐  │
│  │   Conduit MCP Server   │  │
│  │  (exposes skills,      │  │
│  │   tools, envelopes)    │  │
│  └───────────┬────────────┘  │
└──────────────┼───────────────┘
               │ MCP protocol
    ┌──────────┴──────────┐
    │   Claude CLI (PTY)  │
    │  reads .mcp.json    │
    │  connects to Conduit│
    └─────────────────────┘
```

### How it works
1. Before spawning the CLI, Conduit writes a `.mcp.json` in the sandbox directory pointing to its own MCP endpoint
2. CLI discovers the Conduit MCP server at boot alongside any other configured servers
3. Conduit's MCP server exposes:
   - **Skills** as callable MCP tools (same skills the API-backed agents use)
   - **Tool broker rules** — permission checks before execution
   - **Envelope helpers** — tools that return pre-formatted envelope blocks
   - **Agent context** — tools to read agent profile, workspace info, etc.
4. CLI calls these tools like any other MCP tool — no special integration needed

### Benefits
- Single source of truth for skills and tools (Conduit's database)
- Tool broker enforces the same permission rules regardless of provider path
- Skills can be updated centrally without restarting PTY sessions (CLI re-discovers on next tool list)
- No CLI-specific configuration management — everything flows through MCP

---

## Phase 3: Multi-Turn PTY Sessions

**Goal:** Persistent CLI sessions that maintain conversation context across messages.

### Approach
- Use Claude CLI's `--resume <session-id>` flag for subsequent messages
- Map Conduit session IDs to CLI session IDs (store in session metadata or a lookup table)
- First message: `claude -p "..." --output-format stream-json --verbose`
- Subsequent messages: `claude --resume <cli-session-id> -p "..." --output-format stream-json --verbose`

### Considerations
- CLI manages its own context window and compaction — Conduit cannot control this
- `CLAUDE.md` survives CLI compaction, so critical instructions persist
- Conduit's message history and CLI's internal history will diverge — Conduit is the source of truth for the UI, CLI history is internal
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

Currently: `CONDUIT_UTILITY_PROVIDER` and `CONDUIT_UTILITY_MODEL` env vars, defaults to Anthropic Sonnet.

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

2. **MCP is the bridge** — Don't try to make the CLI understand Conduit concepts. Expose them as MCP tools. The CLI stays generic; Conduit controls the environment.

3. **Sandbox directory is the control surface** — Working directory, CLAUDE.md, .mcp.json — these three files define the CLI's entire operating context. Conduit assembles them before spawning.

4. **Conduit is source of truth** — The CLI's internal state (conversation history, compaction) is a black box. Conduit tracks messages, usage, and session state independently.

5. **`-p` for tasks, `--resume` for chats** — Single-turn execution for stateless work (utilities, one-shots, fan-out). Multi-turn sessions for conversational use cases.

6. **Lock adapter, flex model** — Provider adapter (http/pty/subprocess) is set at session creation and doesn't change. Model and agent can be switched within the same adapter.

---

## Testing Checklist

- [x] Unit test: mock PTY output → verify StreamEvent mapping
- [x] Unit test: parser handles all Claude stream-json event types
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
| `internal/provider/pty.go` | PTY bridge provider (unix only) |
| `internal/provider/pty_claude.go` | Claude stream-json parser |
| `internal/provider/pty_claude_test.go` | Parser unit tests |
| `internal/provider/pty_test.go` | Provider unit tests |
| `internal/provider/provider.go` | Provider interface (StreamChat, StreamChatWithTools, Complete, Capabilities) |
| `internal/chat/engine.go` | Dynamic provider routing, UtilityProvider/UtilityModel, inferProvider() |
| `internal/api/sessions.go` | Session update API (accepts provider field) |
| `internal/store/migrations/010_add_pty_provider.sql` | Migration for existing databases |
| `internal/store/seed.go` | PTY provider + claude-cli model seed |
| `cmd/conduit/main.go` | PTY registration, UtilityProvider env var |
| `ui/src/components/chat/ComposerToolbar.tsx` | Provider icons, model picker sets provider+model |
| `ui/src/lib/types.ts` | AVAILABLE_MODELS includes claude-cli |

---

## TODO

### Backend
- [ ] Phase 1: Per-session sandbox directories with CLAUDE.md + .mcp.json generation
- [ ] Phase 3: Multi-turn sessions via `--resume`
- [ ] Phase 5: CLI adapter abstraction for non-Claude tools (Codex, Gemini CLI)
- [ ] Subprocess adapter (pipe-based fallback for Windows)
- [ ] Provider fallback chain: session → agent preference → user priority list → system default
- [ ] Move utility provider/model config from env vars to database settings
- [ ] Orphan process tracking and cleanup
- [ ] Process health checks (detect hung CLI processes)
- [ ] Concurrency limits (max simultaneous PTY processes per user/workspace)
- [ ] Execution observability: extend RecordUsage to capture full execution snapshot (duration, context size, memory, adapter, cost)
- [ ] Utility call comparison log (provider, latency, quality per background call)

### Frontend
- [ ] Settings UI: default adapter (http/pty/subprocess), provider, model, agent
- [ ] Settings UI: utility provider + model (for autoTitle, autoTags — support Ollama, etc.)
- [ ] Settings UI: provider fallback chain (ordered list, drag to reorder)
- [ ] Settings UI: tool call display mode (inline | drawer | drawer-auto) — global default
- [ ] Per-session override for tool call display mode
- [ ] Adapter badge on session (read-only after creation)
- [ ] "Clone session with different adapter" action
- [ ] Tool call drawer: compact (10 rows), expandable (full chat area), closeable
- [ ] Tool call drawer: real-time updates, bookmarks, click-to-copy
- [ ] Tool call drawer: drag-to-resize between compact and expanded
- [ ] Observability dashboard: execution stats, utility call log (power user opt-in)
- [ ] Review agent/model/PTY selection UX — how session creation flows with all new options

### Phase 2 (Conduit as MCP Server)
- [ ] Design MCP server interface (which tools/skills to expose)
- [ ] Implement Conduit MCP server endpoint
- [ ] Generate .mcp.json for sandbox directories
- [ ] Phase 4: Envelope validation + retry logic

### Testing
- [ ] Integration test: full PTY event flow with real CLI
- [ ] Envelope test: PTY path envelope rendering
- [ ] Process cleanup test: context cancellation kills CLI
- [ ] Multi-provider routing test
- [ ] Utility provider test: autoTitle via Ollama
- [ ] Fallback chain test: provider unavailable → next in chain
