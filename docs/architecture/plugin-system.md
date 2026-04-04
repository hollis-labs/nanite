# Nanite Plugin System Architecture

> Created: 2026-03-13 | Updated: 2026-03-19 | Status: Implemented (core), Evolving | Related: ADR-013, ADR-029

## 1. Overview

The Nanite plugin system allows extensions to add CRUD endpoints, event hooks, UI envelope components, and MCP tool servers to Nanite without modifying core application code. Plugins are Go structs that implement the `plugin.Plugin` interface, are loaded at startup by the plugin Host, and declaratively describe their capabilities in a `plugin.yaml` file.

**Current state:** The core plugin infrastructure (Host, CRUD auto-wiring, event system, UI component registration, MCP server injection) is implemented and working. Two plugins exist: the **IT Support** plugin (full-featured demo) and the **session-stats** plugin (reference/skeleton). Frontend envelope rendering is hardcoded in `EnvelopeRenderer.tsx` rather than dynamically loaded. The `plugin.yaml` files are declarative documentation -- they are not parsed by the loader today.

**Why it exists:** Nanite is an agent-agnostic chat harness. Plugins let domain-specific functionality (IT support, sprint planning, analytics) live outside core, following ADR-029's component autonomy principle. The IT Support plugin was the first real proof that the architecture works end-to-end.

## 2. Architecture

```
+---------------------------+
|        plugin.yaml        |  Declarative spec (not parsed yet)
+---------------------------+
             |
+---------------------------+
|     plugin.Plugin (Go)    |  Interface: ID, Name, Load, Unload, ...
+---------------------------+
             |
    Load(host plugin.Host)
             |
             v
+---------------------------+     +-----------------------+
|     Nanite Host           |---->| HTTP Router (ServeMux)|
| (internal/plugin/host.go) |     +-----------------------+
|                           |        |
|  .RegisterCRUDHandler()   |------->| /api/plugins/{type}/*
|  .RegisterUIComponent()   |------->| /api/plugins/ui/{id}
|  .RegisterEventHook()     |        |
|  .GetService()            |     +--+--------------------+
|    - "store"   (*Store)   |     |  Plugin CRUD Handlers |
|    - "engine"  (*Engine)  |     |  (crud.go)            |
|    - "mcp"     (*Manager) |     +--+--------------------+
|    - "toolclient" (*TB)   |
+---------------------------+
             |
     .EmitEvent()
             |
             v
+---------------------------+     +-----------------------+
|    Event Hooks            |     | MCP Server Registry   |
|    (events.go)            |     | mgr.AddServer(name,t) |
+---------------------------+     +-----------------------+
                                           |
                                           v
                                  +-----------------------+
                                  | Tool Discovery        |
                                  | (chat/engine.go       |
                                  |  getToolsForAgent)    |
                                  +-----------------------+
                                           |
                                           v
                                  +-----------------------+
                                  | Frontend Envelopes    |
                                  | EnvelopeRenderer.tsx  |
                                  | KBResultCard.tsx      |
                                  | TicketFormCard.tsx     |
                                  | etc.                  |
                                  +-----------------------+
```

## 3. Plugin Interface

Defined in `libs/plugin/plugin.go`:

```go
type Plugin interface {
    ID() string           // Unique identifier, e.g. "support"
    Name() string         // Human-readable name, e.g. "IT Support"
    Version() string      // Semver, e.g. "0.1.0"
    Description() string  // Brief purpose description
    Dependencies() []string // IDs of plugins that must load first
    Load(host Host) error // Called when plugin loads; registers everything
    Unload() error        // Called on shutdown; cleanup
    Status() PluginStatus // Current loaded/enabled state
}
```

| Method | When Called | Purpose |
|--------|-----------|---------|
| `ID()` | Registration, dependency checks | Must be unique across all plugins |
| `Name()` | Logging, API responses | Display name |
| `Version()` | Logging, API responses | Semver string |
| `Description()` | API responses | Human-readable purpose |
| `Dependencies()` | Before `Load()` | Ensures load order; returns other plugin IDs |
| `Load(host)` | Startup, after dependency check | Registers CRUD handlers, event hooks, UI components, MCP servers |
| `Unload()` | Shutdown, explicit unload | Cleanup resources, close DB connections |
| `Status()` | Any time | Returns `PluginStatus{Loaded, Enabled, LoadedAt, LastError}` |

## 4. Host Services

The Host interface (`libs/plugin/plugin.go` line 46) provides the runtime environment:

```go
type Host interface {
    GetPlugin(id string) (Plugin, bool)
    RegisterCRUDHandler(resourceType string, handler CRUDHandler) error
    RegisterEventHook(eventTypes []string, hook EventHook) error
    RegisterUIComponent(component UIComponent) error
    GetService(name string) (interface{}, error)
    Logger() Logger
    Context() context.Context
}
```

### Available Services

Registered in `nanite/cmd/nanite/main.go` (lines 263-267):

| Service Name | Type | What It Provides |
|-------------|------|-----------------|
| `"store"` | `*store.Store` | SQLite database access -- agents, sessions, messages |
| `"engine"` | `*chat.Engine` | Chat engine -- streaming, tool execution |
| `"mcp"` | `*mcp.Manager` | MCP server manager -- register servers, discover tools |
| `"toolclient"` | `*toolbroker.Client` | Tool broker -- permission-checked tool execution |

To access a service from within a plugin's `Load()`:

```go
func (p *MyPlugin) Load(host plugin.Host) error {
    svc, err := host.GetService("store")
    if err != nil {
        return err
    }
    db := svc.(*store.Store)
    // ...
}
```

### Logger

The Host provides a structured logger implementing `plugin.Logger`:

```go
type Logger interface {
    Debug(msg string, keysAndValues ...interface{})
    Info(msg string, keysAndValues ...interface{})
    Warn(msg string, keysAndValues ...interface{})
    Error(msg string, keysAndValues ...interface{})
    With(keysAndValues ...interface{}) Logger
}
```

Nanite's implementation (`nanite/internal/plugin/logger.go`) wraps Go's `log` package with level prefixes and key-value formatting.

## 5. CRUD Handlers

### Interface

Defined in `libs/plugin/plugin.go` (line 70):

```go
type CRUDHandler interface {
    Create(ctx context.Context, resource interface{}) (interface{}, error)
    Read(ctx context.Context, id string) (interface{}, error)
    Update(ctx context.Context, id string, resource interface{}) (interface{}, error)
    Delete(ctx context.Context, id string) error
    List(ctx context.Context, filters map[string]interface{}) ([]interface{}, error)
}
```

### Auto-Wiring to HTTP Routes

When a plugin calls `host.RegisterCRUDHandler("tickets", handler)`, the Host (`nanite/internal/plugin/host.go` lines 61-101) automatically creates five HTTP endpoints:

| HTTP Method | Path | Handler Method |
|------------|------|---------------|
| `GET` | `/api/plugins/tickets` | `List()` -- query params become filter map |
| `POST` | `/api/plugins/tickets` | `Create()` -- JSON body decoded to `interface{}` |
| `GET` | `/api/plugins/tickets/{id}` | `Read()` -- path param extracted |
| `PUT` | `/api/plugins/tickets/{id}` | `Update()` -- path param + JSON body |
| `DELETE` | `/api/plugins/tickets/{id}` | `Delete()` -- path param extracted |

### Error Mapping

The CRUD handler (`nanite/internal/plugin/crud.go`) maps errors to HTTP status codes:

- Error message contains `"not found"` -> `404 Not Found`
- JSON decode failure -> `400 Bad Request`
- Missing ID path param -> `400 Bad Request`
- All other errors -> `500 Internal Server Error`

All responses are JSON. List responses wrap results in `{"items": [...], "count": N}`.

### Query Parameter Parsing

For `List()`, query parameters are auto-parsed with type inference (lines 14-30 of `crud.go`):
- Integers: `?limit=10` -> `filters["limit"] = 10`
- Floats: `?score=3.5` -> `filters["score"] = 3.5`
- Booleans: `?active=true` -> `filters["active"] = true`
- Strings: everything else stays as string

## 6. Event System

### Event Catalog

Defined in `nanite/internal/plugin/events.go`:

| Category | Event Constant | String Value | When Emitted |
|----------|---------------|-------------|-------------|
| Session | `EventSessionStart` | `session.start` | New session created |
| Session | `EventSessionEnd` | `session.end` | Session closed |
| Agent | `EventAgentSwitched` | `agent.switched` | User switches agent |
| Agent | `EventAgentLoaded` | `agent.loaded` | Agent profile loaded |
| Message | `EventMessageSent` | `message.sent` | User sends message |
| Message | `EventMessageReceived` | `message.received` | Assistant responds |
| Message | `EventMessageDeleted` | `message.deleted` | Message deleted |
| Mode | `EventModeChanged` | `mode.changed` | Chat mode changes |
| Mode | `EventScopeChanged` | `scope.changed` | Scope changes |
| Tool | `EventToolCalled` | `tool.called` | Tool execution starts |
| Tool | `EventToolFailed` | `tool.failed` | Tool execution fails |
| Tool | `EventToolComplete` | `tool.complete` | Tool execution succeeds |
| UI | `EventEnvelopeRendered` | `envelope.rendered` | Envelope renders |
| UI | `EventWidgetLoaded` | `widget.loaded` | Widget loads |
| UI | `EventActionTriggered` | `action.triggered` | User triggers action |
| Workflow | `EventWorkflowStarted` | `workflow.started` | Workflow begins |
| Workflow | `EventWorkflowComplete` | `workflow.complete` | Workflow completes |
| Workflow | `EventWorkflowFailed` | `workflow.failed` | Workflow fails |

### Event Structure

```go
// libs/plugin/plugin.go
type Event struct {
    Type      string                 `json:"type"`
    Source    string                 `json:"source"`
    Timestamp time.Time              `json:"timestamp"`
    Data      map[string]interface{} `json:"data"`
    SessionID string                 `json:"session_id,omitempty"`
}
```

### EventData Helper

`nanite/internal/plugin/events.go` defines `EventData` -- a structured type with fields for all event categories. The `NewEvent()` function converts it to the generic `map[string]interface{}` used by `plugin.Event`.

### Hook Interface

```go
type EventHook interface {
    Handle(ctx context.Context, event Event) error
    EventTypes() []string
}
```

### Hook Execution

Hooks are processed **concurrently** with a 5-second timeout per hook (`host.go` lines 247-261). If a hook fails, the error is logged but other hooks continue. This prevents a misbehaving plugin from blocking the system.

### Convenience Emitters

The Host provides typed emit methods for common events:
- `EmitSessionStart(sessionID, agentID, mode)`
- `EmitSessionEnd(sessionID)`
- `EmitAgentSwitched(sessionID, previousAgentID, newAgentID)`
- `EmitMessageSent(sessionID, messageID, content, role, tokensUsed)`
- `EmitMessageReceived(sessionID, messageID, content, responseTime)`
- `EmitModeChanged(sessionID, previousMode, newMode)`
- `EmitToolCalled(sessionID, toolName, args, result)`
- `EmitToolFailed(sessionID, toolName, args, errString)`
- `EmitEnvelopeRendered(sessionID, envelopeType, data)`
- `EmitActionTriggered(sessionID, actionID, actionData)`

### External Event API

Events can also be emitted via HTTP:

```
POST /api/plugins/events
{
  "event_type": "action.triggered",
  "source": "frontend",
  "session_id": "sess-123",
  "data": { "action_id": "create-ticket" }
}
```

Handled by `server.go` line 165.

## 7. UI Components

### Registration

Plugins register UI components via `host.RegisterUIComponent()`:

```go
type UIComponent struct {
    ID          string            `json:"id"`
    Type        UIComponentType   `json:"type"`      // widget, envelope, action, workflow, view
    Name        string            `json:"name"`
    Description string            `json:"description"`
    Props       map[string]interface{} `json:"props,omitempty"`
    Handler     http.Handler      `json:"-"`          // Server-side handler (optional)
}
```

### Component Types

| Type | Constant | Purpose |
|------|----------|---------|
| `widget` | `UIComponentTypeWidget` | Dashboard/sidebar widgets |
| `envelope` | `UIComponentTypeEnvelope` | Chat message rich cards |
| `action` | `UIComponentTypeAction` | Quick action buttons |
| `workflow` | `UIComponentTypeWorkflow` | Multi-step workflows |
| `view` | `UIComponentTypeView` | Full-page views |

### Server-Side Handlers

If a `UIComponent` has a non-nil `Handler`, the Host registers it at `/api/plugins/ui/{component.ID}`. The IT Support plugin uses this for the ticket download page: `GET /api/plugins/ui/support-ticket-download?ticket_id=TKT-xxx`.

### Frontend Rendering Pipeline

Currently, frontend envelope rendering is **hardcoded** in `nanite/ui/src/components/chat/envelopes/EnvelopeRenderer.tsx`:

```tsx
if (envelope.type === 'kb-result' && envelope.data) {
    return <KBResultCard data={...} />
}
if (envelope.type === 'ticket-form' && envelope.data) {
    return <TicketFormCard data={...} />
}
// ... etc
```

The `EnvelopeRenderer` checks `envelope.type` and dispatches to the appropriate React component. This is a known limitation -- see section 13.

### UI Component Discovery API

```
GET /api/plugins/ui-components
```

Returns all registered UI components as JSON. This could be used by a future dynamic loader.

## 8. MCP Tool Integration

### How Plugins Register MCP Servers

During `Load()`, a plugin can obtain the MCP Manager and add a server:

```go
// From nanite/plugins/support/plugin.go lines 74-79
mcpSvc, mcpErr := host.GetService("mcp")
if mcpErr == nil {
    if mgr, ok := mcpSvc.(*mcp.Manager); ok {
        mgr.AddServer("support-kb", kbTransport)
    }
}
```

The transport must implement the `mcp.MCPTransport` interface with `ListTools()` and `CallTool()` methods.

### Tool Discovery Flow

1. Plugin registers MCP server during `Load()` (e.g., `support-kb`)
2. After all plugins load, `main.go` runs `mcpManager.AutoDiscover()` again (lines 282-288) to pick up newly registered servers
3. When a chat message arrives, `getToolsForAgent()` (`engine.go` line 953) determines which tools the agent can use:
   a. First tries the Tool Broker (`ToolClient.SelectToolsAsProvider`) based on intent extraction
   b. If no MCP tools were selected, falls back to **direct MCP discovery** from the agent's configured servers (`engine.go` lines 977-1000)
4. The fallback reads `agent.MCPServers` (JSON array like `["cortex","support-kb"]`), calls `MCPManager.DiscoverServerTools()` for each, and adds them with namespaced names (`mcp__support-kb__search_kb`)

### Agent Permissions

Agent profiles store tool permissions as JSON:

```json
{"allow": ["mcp__cortex__*", "mcp__support-kb__*"]}
```

The Tool Broker checks these permissions. The direct MCP discovery fallback (`engine.go` lines 977-1000) bypasses the broker -- it runs when the broker returned zero MCP tools, ensuring plugin-registered servers are always available to agents that declare them.

## 9. Agent Profiles

Plugins can seed agent profiles into the database during `Load()`. The IT Support plugin does this in `nanite/plugins/support/seed.go`:

```go
agent := &store.AgentProfile{
    ID:              "it-support-001",
    Slug:            "it-support",
    Name:            "IT Support",
    DefaultModel:    "claude-sonnet-4-20250514",
    MCPServers:      `["cortex","support-kb"]`,
    ToolPermissions: `{"allow":["mcp__cortex__*","mcp__support-kb__*"]}`,
    SystemPrompt:    itSupportSystemPrompt,
}
db.CreateAgent(agent)
```

The seeding is idempotent -- it checks `GetAgentBySlug("it-support")` first and does nothing if the agent already exists.

### System Prompt Structure

The IT Support agent's system prompt (`seed.go` line 58) follows a specific pattern:

1. **Role statement** -- "You are an IT Support specialist"
2. **Workflow steps** -- Numbered sequence: search KB, present results, offer ticket creation
3. **Envelope syntax** -- Exact JSON format for the `ticket-form` envelope in a `nanite-envelope` code fence
4. **Rules** -- Constraints like "search once per message", "never fabricate KB articles"

The "search once" pattern is critical: it prevents the LLM from making multiple KB search calls per user message, which would create duplicate envelope injections.

## 10. Deterministic Envelope Injection

This is the most intricate part of the plugin system. It ensures KB search results always render as rich cards, regardless of whether the LLM decides to emit an envelope.

### The Problem

When the LLM calls `search_kb`, it receives search results as text. We want the results displayed as interactive `KBResultCard` components. But the LLM might not emit the correct envelope JSON, or might mangle it.

### The Solution: Three-Layer Pipeline

**Layer 1: KB tool embeds metadata** (`nanite/plugins/support/kb.go` lines 196-201)

The `search_kb` tool returns a response with three parts:
1. Compact JSON summary (no article body -- small enough for the LLM context)
2. A `[SYSTEM: ...]` instruction telling the LLM to write a brief intro and stop
3. Hidden full data in an HTML comment: `<!--ENVELOPE_DATA:{full JSON with bodies}:ENVELOPE_DATA-->`

```
{compact results JSON}

[SYSTEM: KB articles found and will be displayed to the user automatically. ...]

<!--ENVELOPE_DATA:{"results":[...with bodies...],"query":"..."}:ENVELOPE_DATA-->
```

**Layer 2: Chat engine extracts and collects** (`nanite/internal/chat/engine.go` lines 550-561)

During tool result processing, if a tool name ends with `__search_kb`, the engine:
1. Looks for `<!--ENVELOPE_DATA:` markers
2. Extracts the full JSON between the markers
3. Calls `buildKBEnvelope()` to wrap it as a `nanite-envelope`
4. Appends it to `pendingEnvelopes`

**Layer 3: Post-response injection** (`engine.go` lines 682-688)

After the LLM finishes responding, all pending envelopes are:
1. Wrapped in `` ```nanite-envelope `` fences
2. Appended to the response content
3. Streamed to the client as delta events
4. Parsed by `ParseEnvelopes()` and stored with the message

### Backtick Sanitization

`buildKBEnvelope()` (`nanite/internal/chat/envelope.go` lines 45-84) replaces triple backticks in KB article bodies with `~~~` to prevent them from breaking the `nanite-envelope` code fence delimiter.

### Compact vs Full Results

The LLM sees **compact** results (no body, just ID/title/category/severity/confidence/rank). The frontend gets **full** results (including body text) via the envelope data. This keeps the LLM context small while giving users complete articles.

## 11. Plugin Lifecycle

### Load Order

The load sequence in `nanite/cmd/nanite/main.go` (lines 259-288):

1. **Create plugin host** with `nil` router -- `NewHost(nil, logger)` (line 261)
2. **Register services** -- store, engine, mcp, toolclient (lines 264-267)
3. **Create HTTP server** -- this calls `pluginHost.SetRouter(mux)` (line 271, via `server.New()`)
4. **Load plugins** -- `pluginHost.LoadPlugin(supportPlugin)` (line 275)
5. **Re-discover tools** -- `mcpManager.AutoDiscover()` picks up plugin-registered MCP servers (line 283)
6. **Start listening** -- `srv.ListenAndServe()` (line 290)

### The Deadlock Fix

The Host's `LoadPlugin()` method (`host.go` lines 173-203) has a specific pattern to avoid deadlocks:

```go
func (h *Host) LoadPlugin(p plugin.Plugin) error {
    // Pre-checks under lock
    h.mu.Lock()
    // ... check exists, check deps ...
    h.mu.Unlock()

    // Load WITHOUT holding the lock -- Load calls RegisterCRUDHandler etc. which acquire h.mu
    if err := p.Load(h); err != nil {
        return err
    }

    // Store the loaded plugin
    h.mu.Lock()
    h.plugins[id] = p
    h.mu.Unlock()
}
```

The lock is released before calling `p.Load(h)` because `Load()` calls back into `RegisterCRUDHandler()`, `RegisterUIComponent()`, etc., which all acquire `h.mu`. Holding the lock during `Load()` would deadlock.

### Dependency Validation

Before loading, the host checks that all declared dependencies are already loaded. If plugin B depends on plugin A, A must load first. Circular dependencies are not handled -- they would deadlock.

### Unload

`UnloadPlugin()` checks reverse dependencies -- you cannot unload a plugin if another loaded plugin depends on it. `Shutdown()` unloads all plugins and cancels the host context.

### Graceful Shutdown

`Host.Shutdown()` (`host.go` lines 300-318) iterates all plugins, calls `Unload()` on each, collects errors, and cancels the host context. Errors during unload are collected but don't prevent other plugins from unloading.

## 12. plugin.yaml

### Format

```yaml
name: support                    # Must match Plugin.ID()
version: 0.1.0                  # Must match Plugin.Version()
description: "..."               # Human-readable

requires:
  mcp_servers: []                # MCP servers this plugin needs (for validation)

registers:
  envelopes:                     # Envelope types this plugin provides
    - type: kb-result
      renderer: KBResultCard     # React component name (aspirational)
    - type: ticket-form
      renderer: TicketFormCard

  quick_actions:                 # Action buttons (aspirational)
    - id: create-ticket
      label: "Create Ticket"
      icon: ticket
      handler: createTicket
      context: [session]

  hooks:                         # Event hooks
    - event: message.sent
      handler: onMessageSent

api_endpoints:                   # HTTP endpoints the plugin creates
  - path: /api/plugins/tickets
    method: GET
    handler: listTickets
```

### What's Parsed vs Aspirational

**Currently used:** The `plugin.yaml` is **not parsed** by the Go loader. It serves as declarative documentation. All registration happens imperatively in the plugin's `Load()` method.

**Aspirational fields:**
- `registers.envelopes[].renderer` -- intended for dynamic component loading
- `registers.quick_actions` -- action button framework not yet built
- `registers.widgets` -- widget slot system not yet built
- `requires.mcp_servers` -- not validated at load time
- `database.tables` -- schema auto-creation not implemented

The session-stats plugin's `plugin.yaml` (`nanite/plugins/session-stats/plugin.yaml`) shows the full aspirational spec including `widgets`, `database.tables`, and multi-event hooks.

## 13. Known Limitations & Gaps

### Frontend Component Loading is Hardcoded

`EnvelopeRenderer.tsx` has a static `if/else` chain for each envelope type. Adding a new envelope type requires editing this file. No dynamic component loading exists yet.

### plugin.yaml is Not Parsed

The YAML file is documentation only. The Go code in `Load()` is the actual registration. There is no validation that the YAML matches the runtime behavior.

### No Plugin Auto-Discovery

Plugins are manually instantiated and loaded in `main.go`. There is no directory scanning or dynamic loading. Each new plugin requires a code change in `main.go`.

### No Plugin Isolation

Plugins run in the same process with full access to all services. A misbehaving plugin can crash Nanite. No sandboxing or resource limits exist.

### No Widget/Slot System

The `registers.widgets` YAML field references a slot system (`right-rail`, etc.) that doesn't exist in the frontend.

### No Quick Action Framework

The `registers.quick_actions` YAML field references an action system that isn't built yet.

### CRUD Error Mapping is Fragile

Error-to-HTTP-status mapping uses `strings.Contains(err.Error(), "not found")` (crud.go line 74). A structured error type would be more robust.

### Event Data Conversion is Verbose

The `NewEvent()` function manually copies each `EventData` field to a map. This should use reflection or code generation.

### No Plugin Configuration

Plugins cannot declare configuration (API keys, database URLs, etc.). The KB transport hardcodes its Postgres connection string (`host=localhost port=5432 dbname=kb_demo`).

### Library vs Host Duplication

Both `libs/plugin/registry.go` and `nanite/internal/plugin/host.go` implement similar functionality. The library registry is a generic implementation; the Host is Nanite-specific. The library registry is not currently used by Nanite -- it uses the Host directly.

### Unload Does Not Clean Up Routes

When a plugin unloads, its HTTP routes remain registered in the `ServeMux`. Go's `http.ServeMux` does not support route removal.
