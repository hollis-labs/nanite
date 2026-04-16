# build-conduit-plugin

Build a complete Conduit plugin from scratch. Covers manifest, Go backend, envelope components, CRUD handlers, event hooks, and agent seeding. The canonical reference plugin is `support-ticket`.

## When to Use

- When building a new Conduit plugin
- When extending an existing plugin with new capabilities
- When scaffolding plugin structure for a new feature

## Quick Start

```bash
# Scaffold a new plugin (creates directory + boilerplate)
conduit plugin new <name>

# Or manually create the directory structure:
plugins/<name>/
  plugin.yaml           # Manifest (required)
  plugin.go             # Go entry point (required)
  agents/               # Agent profiles to seed (optional)
    my-agent.yaml
  ui/                   # Envelope components (optional)
    MyCard.tsx
```

After creating files, run `npm run generate:plugins` in the `ui/` directory to register envelope components.

## Plugin Manifest (plugin.yaml)

The manifest declares everything the plugin provides. All fields:

```yaml
name: my-plugin                    # Unique plugin ID (kebab-case)
version: 0.1.0                     # Semver
description: What this plugin does # Human-readable

# Configuration schema — each key becomes available via host.GetConfig("key")
# Resolution order: env var -> config file -> default
config:
  database_url:
    type: string                   # string | int | bool
    required: true                 # If true and missing, Load() will get an error
    env_var: MY_PLUGIN_DB_URL      # Environment variable name
    default: "localhost:5432"      # Fallback value
    description: "PostgreSQL connection string"

# External dependencies
requires:
  mcp_servers: []                  # MCP servers this plugin needs available

# Agent profiles to seed into the database
agents:
  - agents/my-agent.yaml           # Path relative to plugin directory

# What the plugin registers with the host
registers:
  # Envelope components — rich UI cards rendered in chat
  envelopes:
    - type: my-result              # Envelope type string (used in JSON)
      component: ui/MyResultCard   # Path relative to ui/src/ (no .tsx)
      export: MyResultCard         # Named export from the TSX file

  # Quick actions — buttons in the chat UI
  quick_actions:
    - id: do-thing
      label: "Do Thing"
      icon: zap                    # Lucide icon name
      handler: doThing             # Go handler function name
      context: [session]           # Where the action appears

  # Event hooks — react to system events
  hooks:
    - event: message.sent          # Event type to listen for
      handler: onMessageSent       # Go handler function name

# Optional: explicit API endpoints (CRUD handlers are auto-wired)
api_endpoints:
  - path: /api/plugins/my-resource
    method: GET
    handler: listResources
  - path: /api/plugins/my-resource
    method: POST
    handler: createResource
  - path: /api/plugins/my-resource/{id}
    method: GET
    handler: getResource
```

### Full Example (support-ticket plugin)

```yaml
name: support
version: 0.1.0
description: IT employee self-service support — KB search + ticket creation

config:
  database_url:
    type: string
    required: true
    env_var: SUPPORT_DATABASE_URL
    default: "host=localhost port=5432 dbname=kb_demo sslmode=disable"
    description: "PostgreSQL connection string for KB database"

requires:
  mcp_servers: []

agents:
  - agents/it-support.yaml

registers:
  envelopes:
    - type: kb-result
      component: ui/KBResultCard
      export: KBResultCard
    - type: ticket-form
      component: ui/TicketFormCard
      export: TicketFormCard
    - type: ticket-confirmation
      component: ui/TicketConfirmationCard
      export: TicketConfirmationCard
    - type: resolution-capture
      component: ui/ResolutionCaptureCard
      export: ResolutionCaptureCard

  quick_actions:
    - id: create-ticket
      label: "Create Ticket"
      icon: ticket
      handler: createTicket
      context: [session]

  hooks:
    - event: message.sent
      handler: onMessageSent

api_endpoints:
  - path: /api/plugins/tickets
    method: GET
    handler: listTickets
  - path: /api/plugins/tickets
    method: POST
    handler: createTicket
  - path: /api/plugins/tickets/{id}
    method: GET
    handler: getTicket
```

## Go Plugin Implementation

### Entry Point (plugin.go)

Every plugin needs an `init()` function that registers its constructor with the host:

```go
package myplugin

import (
    "fmt"
    "time"

    hostplugin "github.com/hollis-labs/conduit/internal/plugin"
    "github.com/hollis-labs/plugin-sdk"
)

func init() {
    // The ID here MUST match the "name" field in plugin.yaml
    hostplugin.RegisterPlugin("my-plugin", func() plugin.Plugin { return New() })
}

type MyPlugin struct {
    host   plugin.Host
    status plugin.PluginStatus
}

func New() *MyPlugin {
    return &MyPlugin{}
}

// --- Required interface methods ---

func (p *MyPlugin) ID() string          { return "my-plugin" }
func (p *MyPlugin) Name() string        { return "My Plugin" }
func (p *MyPlugin) Version() string     { return "0.1.0" }
func (p *MyPlugin) Description() string { return "What it does" }
func (p *MyPlugin) Dependencies() []string { return nil } // Or: []string{"other-plugin-id"}

func (p *MyPlugin) Load(host plugin.Host) error {
    p.host = host
    logger := host.Logger()

    // 1. Register CRUD handler (auto-wires /api/plugins/myresource/*)
    handler := NewMyHandler()
    if err := host.RegisterCRUDHandler("myresource", handler); err != nil {
        return fmt.Errorf("failed to register CRUD handler: %w", err)
    }

    // 2. Register event hooks
    hook := &myEventHook{logger: logger}
    if err := host.RegisterEventHook([]string{"message.sent"}, hook); err != nil {
        return fmt.Errorf("failed to register event hook: %w", err)
    }

    // 3. Seed agent profile
    if err := seedAgent(host); err != nil {
        logger.Warn("failed to seed agent", "error", fmt.Sprintf("%v", err))
        // Non-fatal — plugin still works without dedicated agent
    }

    // 4. Read config
    dbURL, err := host.GetConfig("database_url")
    if err != nil {
        dbURL = "fallback-default"
        logger.Warn("config unavailable, using default", "error", fmt.Sprintf("%v", err))
    }
    _ = dbURL

    // 5. Access core services
    if svc, err := host.GetService("store"); err == nil {
        // Use the store service...
        _ = svc
    }

    p.status = plugin.PluginStatus{
        Loaded:  true,
        Enabled: true,
        LoadedAt: time.Now(),
    }
    logger.Info("my-plugin loaded", "version", p.Version())
    return nil
}

func (p *MyPlugin) Unload() error {
    p.status.Loaded = false
    p.status.Enabled = false
    if p.host != nil {
        p.host.Logger().Info("my-plugin unloaded")
    }
    return nil
}

func (p *MyPlugin) Status() plugin.PluginStatus {
    return p.status
}
```

### Plugin Interface (from libs/plugin/plugin.go)

The `plugin.Plugin` interface requires these methods:

| Method | Signature | Purpose |
|--------|-----------|---------|
| ID | `ID() string` | Unique identifier, matches plugin.yaml `name` |
| Name | `Name() string` | Human-readable display name |
| Version | `Version() string` | Semver string |
| Description | `Description() string` | Brief description |
| Dependencies | `Dependencies() []string` | Plugin IDs this depends on |
| Load | `Load(host Host) error` | Called when plugin loads — register everything here |
| Unload | `Unload() error` | Called when plugin unloads |
| Status | `Status() PluginStatus` | Current runtime status |

### Optional Interfaces

```go
// Implement Uninstallable for cleanup during uninstall
type Uninstallable interface {
    Uninstall(host Host) error
}

// Implement Installable for first-time setup
type Installable interface {
    Install(host Host) error
}
```

### Host API (available in Load())

| Method | Purpose |
|--------|---------|
| `host.RegisterCRUDHandler(resourceType, handler)` | Wire CRUD routes at `/api/plugins/{resourceType}/*` |
| `host.RegisterEventHook(eventTypes, hook)` | Listen for system events |
| `host.RegisterUIComponent(component)` | Register server-side UI handlers |
| `host.GetService(name)` | Access core services: `"store"`, `"mcp"` |
| `host.GetConfig(key)` | Read config value (env var -> config file -> default) |
| `host.GetPlugin(id)` | Access another loaded plugin |
| `host.Logger()` | Get a structured logger |
| `host.Context()` | Get the plugin execution context |

## CRUD Handlers

Implement the `plugin.CRUDHandler` interface for automatic HTTP route wiring:

```go
type MyHandler struct {
    // your state (database, cache, etc.)
}

func NewMyHandler() *MyHandler {
    return &MyHandler{}
}

// Create — POST /api/plugins/{resourceType}
func (h *MyHandler) Create(ctx context.Context, resource interface{}) (interface{}, error) {
    // resource is the parsed JSON body
    raw, err := json.Marshal(resource)
    if err != nil {
        return nil, fmt.Errorf("invalid data: %w", err)
    }
    var item MyItem
    if err := json.Unmarshal(raw, &item); err != nil {
        return nil, fmt.Errorf("invalid data: %w", err)
    }

    item.ID = generateID()
    item.CreatedAt = time.Now()
    // ... save to store ...

    return &item, nil
}

// Read — GET /api/plugins/{resourceType}/{id}
func (h *MyHandler) Read(ctx context.Context, id string) (interface{}, error) {
    // ... fetch by ID ...
    return item, nil
}

// Update — PUT /api/plugins/{resourceType}/{id}
func (h *MyHandler) Update(ctx context.Context, id string, resource interface{}) (interface{}, error) {
    // resource is the patch data — marshal/unmarshal to merge fields
    // ... fetch existing, merge fields, save ...
    return updated, nil
}

// Delete — DELETE /api/plugins/{resourceType}/{id}
func (h *MyHandler) Delete(ctx context.Context, id string) error {
    // ... remove by ID ...
    return nil
}

// List — GET /api/plugins/{resourceType}?status=open&category=network
func (h *MyHandler) List(ctx context.Context, filters map[string]interface{}) ([]interface{}, error) {
    // Query params are passed as filters
    // ... filter and return ...
    return results, nil
}
```

The host auto-wires these routes when you call `host.RegisterCRUDHandler("tickets", handler)`:
- `GET /api/plugins/tickets` -> List
- `POST /api/plugins/tickets` -> Create
- `GET /api/plugins/tickets/{id}` -> Read
- `PUT /api/plugins/tickets/{id}` -> Update
- `DELETE /api/plugins/tickets/{id}` -> Delete

## Event Hooks

### EventHook Interface

```go
type myEventHook struct {
    logger plugin.Logger
}

func (h *myEventHook) Handle(ctx context.Context, event plugin.Event) error {
    // event.Type    — the event type string
    // event.Source  — "conduit"
    // event.Data    — map[string]interface{} with event-specific fields
    // event.SessionID — the session that triggered the event

    h.logger.Debug("received event", "type", event.Type, "session", event.SessionID)

    // Example: detect !command in messages
    if content, ok := event.Data["content"].(string); ok {
        if strings.HasPrefix(content, "!mycommand ") {
            query := strings.TrimPrefix(content, "!mycommand ")
            // Handle the command...
            _ = query
        }
    }

    return nil
}

func (h *myEventHook) EventTypes() []string {
    return []string{"message.sent"}
}
```

### Available Event Types

| Event | Fired When | Key Data Fields |
|-------|-----------|-----------------|
| `session.start` | New chat session created | session_id, agent_id, mode |
| `session.end` | Chat session ends | session_id |
| `agent.switched` | User switches agent | session_id, agent_id, previous_agent_id |
| `agent.loaded` | Agent profile loaded | agent_name, agent_version |
| `message.sent` | User sends a message | session_id, message_id, content, role |
| `message.received` | Agent responds | session_id, message_id, content, response_time_ms |
| `message.deleted` | Message deleted | session_id, message_id |
| `mode.changed` | Agent mode changes | previous_mode, new_mode |
| `tool.called` | MCP tool invoked | tool_name, tool_args, tool_result |
| `tool.failed` | Tool call failed | tool_name, tool_args, error |
| `tool.complete` | Tool call completed | tool_name, tool_result |
| `envelope.rendered` | Envelope card rendered | envelope_type, envelope_data |
| `action.triggered` | Quick action clicked | action_id, action_data |
| `workflow.started` | Workflow begins | workflow_name, workflow_data |
| `workflow.complete` | Workflow finishes | workflow_name, workflow_data |
| `workflow.failed` | Workflow fails | workflow_name, error |

### Common Pattern: !command Detection

The giphy plugin uses `message.sent` to detect `!giphy <query>` commands. Pattern:

```go
func (h *myHook) Handle(ctx context.Context, event plugin.Event) error {
    content, ok := event.Data["content"].(string)
    if !ok || event.Data["role"] != "user" {
        return nil
    }
    if strings.HasPrefix(content, "!mycommand ") {
        query := strings.TrimPrefix(content, "!mycommand ")
        // Process the command, emit envelope, etc.
        _ = query
    }
    return nil
}
```

## Agent Profile Seeding

Plugins can seed agent profiles into the database during Load(). The pattern:

### Agent YAML (agents/my-agent.yaml)

```yaml
id: my-agent-001
name: My Agent
slug: my-agent
description: >-
  What this agent does
default_model: claude-sonnet-4-20250514
default_mode: default
can_execute: false

mcp_servers:
  - cortex
  - my-custom-server

tool_permissions:
  allow:
    - "mcp__vanta__*"
    - "mcp__my-custom-server__*"

system_prompt: |
  You are a specialized agent. Your job is to...

  ## Emitting Envelopes

  When you need to show a rich UI card, emit a fenced code block:
  ```conduit-envelope
  {"kind":"envelope","version":1,"type":"my-result","data":{...}}
  ```
```

### Seed Function (seed.go)

```go
package myplugin

import (
    "database/sql"
    "errors"
    "fmt"

    "github.com/hollis-labs/conduit/internal/store"
    "github.com/hollis-labs/plugin-sdk"
)

func seedAgent(host plugin.Host) error {
    svc, err := host.GetService("store")
    if err != nil {
        return fmt.Errorf("get store service: %w", err)
    }

    db, ok := svc.(*store.Store)
    if !ok {
        return fmt.Errorf("store service is %T, expected *store.Store", svc)
    }

    // Idempotent — skip if agent already exists
    _, err = db.GetAgentBySlug("my-agent")
    if err == nil {
        return nil // Already exists
    }
    if !errors.Is(err, sql.ErrNoRows) {
        return fmt.Errorf("check existing agent: %w", err)
    }

    agent := &store.AgentProfile{
        ID:              "my-agent-001",
        Name:            "My Agent",
        Slug:            "my-agent",
        Description:     "What this agent does",
        DefaultModel:    "claude-sonnet-4-20250514",
        DefaultMode:     "default",
        CanExecute:      false,
        MCPServers:      `["cortex","my-custom-server"]`,
        ToolPermissions: `{"allow":["mcp__vanta__*","mcp__my-custom-server__*"]}`,
        Modes:           `[]`,
        Settings:        `{}`,
        SystemPrompt:    myAgentSystemPrompt,
    }

    if err := db.CreateAgent(agent); err != nil {
        return fmt.Errorf("create agent: %w", err)
    }

    host.Logger().Info("seeded agent profile", "slug", "my-agent")
    return nil
}

const myAgentSystemPrompt = `You are a specialized agent...`
```

## Envelope Components (TSX)

See the dedicated `/build-envelope-component` skill for full envelope component details. Summary:

### JSON Format (emitted by agent in chat)

    ```conduit-envelope
    {"kind":"envelope","version":1,"type":"my-result","data":{"field":"value"}}
    ```

### React Component Pattern

```typescript
interface MyResultData {
  field: string
}

interface MyResultCardProps {
  data: MyResultData
  onSendMessage?: (content: string) => void
}

export function MyResultCard({ data, onSendMessage }: MyResultCardProps) {
  return (
    <div className="rounded-lg border border-zinc-700 bg-zinc-900/50 p-4">
      {/* Your component */}
    </div>
  )
}
```

### Registration

1. Add to `plugin.yaml` under `registers.envelopes`
2. Place TSX file at the path referenced in `component` (relative to `ui/src/`)
3. Run `npm run generate:plugins` to update `ui/src/generated/plugin-envelopes.ts`

The codegen script (`scripts/generate-plugin-imports.mjs`) reads all `plugins/*/plugin.yaml` files, finds envelope entries, verifies the TSX files exist, and generates lazy imports in the `PLUGIN_ENTRIES` section of `plugin-envelopes.ts`. Core envelopes in `CORE_ENVELOPE_REGISTRY` are never touched.

## Step-by-Step: New Plugin from Scratch

1. **Create directory**: `mkdir -p plugins/my-plugin/ui plugins/my-plugin/agents`
2. **Write plugin.yaml** — declare name, config, registers
3. **Write plugin.go** — `init()` + Plugin struct + Load/Unload
4. **Write CRUD handler** (if needed) — implement CRUDHandler interface
5. **Write event hook** (if needed) — implement EventHook interface
6. **Write agent YAML + seed.go** (if needed) — agent profile for the plugin
7. **Write envelope TSX components** (if needed) — React cards for chat
8. **Run codegen**: `cd ui && npm run generate:plugins`
9. **Build**: The plugin compiles into the Conduit binary (Go build tags or blank import)
10. **Test**: Start Conduit, verify plugin loads in logs, test API endpoints

## Key Source Paths (Conduit repo)

| What | Path |
|------|------|
| Plugin interface | `libs/plugin/plugin.go` |
| Host implementation | `internal/plugin/host.go` |
| Plugin registry | `internal/plugin/registry.go` |
| Event catalog | `internal/plugin/events.go` |
| CRUD wiring | `internal/plugin/host.go` (RegisterCRUDHandler) |
| Config resolution | `internal/plugin/config.go` |
| Plugin management | `internal/plugin/manage.go` |
| Envelope codegen | `scripts/generate-plugin-imports.mjs` |
| Envelope registry | `ui/src/generated/plugin-envelopes.ts` |
| Envelope renderer | `ui/src/components/chat/envelopes/EnvelopeRenderer.tsx` |
| Reference plugin | `plugins/support-ticket/` |
