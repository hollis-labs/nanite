# IT Support Plugin Architecture

> Created: 2026-03-19 | Status: Demo-ready | Related: plugin-system.md, ADR-029

## 1. Overview

The IT Support plugin is a proof-of-concept that demonstrates Conduit's plugin system end-to-end. It provides employee self-service IT support through a dedicated agent that searches a knowledge base, displays articles as rich cards, and creates support tickets with auto-routing -- all integrated into the Conduit chat UI.

**Demo use case:** An employee asks "My VPN keeps disconnecting." The IT Support agent searches the KB, displays matching articles as interactive cards, and if the KB doesn't help, presents a ticket creation form. The created ticket includes auto-routing (e.g., "Network Operations -- VPN/Remote Access") and can be downloaded as a formatted HTML page. In production, the ticket creation step would be replaced by an API call to BMC Helix ITSM.

**Plugin ID:** `support`
**Version:** `0.1.0`
**Dependencies:** None

## 2. Architecture

```
User types message
        |
        v
+-------------------+     +------------------------+
| IT Support Agent  |     | System Prompt          |
| (claude-sonnet-4) |<----| "search once, intro    |
|                   |     |  briefly, offer ticket" |
+-------------------+     +------------------------+
        |
        | calls mcp__support-kb__search_kb
        v
+-------------------+     +------------------------+
| KBTransport       |---->| kb_demo Postgres DB    |
| (kb.go)           |     | kb_smart_search()      |
+-------------------+     +------------------------+
        |
        | returns compact JSON + hidden ENVELOPE_DATA
        v
+-------------------+
| Chat Engine       |
| (engine.go)       |
|  - extracts       |
|    ENVELOPE_DATA  |
|  - builds KB      |
|    envelope       |
|  - injects after  |
|    LLM response   |
+-------------------+
        |
        v
+-------------------+     +------------------------+
| EnvelopeRenderer  |---->| KBResultCard.tsx        |
| (.tsx)            |     | (expandable articles)   |
+-------------------+     +------------------------+

--- Ticket Flow ---

Agent emits ticket-form envelope
        |
        v
+-------------------+
| TicketFormCard.tsx |
| (form UI)         |
+-------------------+
        |
        | POST /api/plugins/tickets
        v
+-------------------+     +------------------------+
| TicketHandler     |---->| TicketStore (in-memory) |
| (tickets.go)      |     | map[string]*Ticket      |
+-------------------+     +------------------------+
        |
        | returns created ticket with routing
        v
+-------------------+     +------------------------+
| Success state in  |     | Download button         |
| TicketFormCard    |---->| /api/plugins/ui/        |
+-------------------+     |  support-ticket-download |
                          +------------------------+
```

## 3. Files

### Backend (Go)

| File | Purpose | Key Functions/Types |
|------|---------|-------------------|
| `conduit/plugins/support/plugin.go` | Plugin entry point | `SupportPlugin` struct, `New()`, `Load()`, `Unload()` |
| `conduit/plugins/support/tickets.go` | Ticket CRUD handler + in-memory store | `TicketStore`, `TicketHandler`, `Ticket` struct, `routingForCategory()` |
| `conduit/plugins/support/download.go` | HTML ticket download page | `DownloadHandler.ServeHTTP()`, `ticketTemplate` |
| `conduit/plugins/support/kb.go` | KB search MCP transport | `KBTransport`, `ListTools()`, `CallTool()`, `searchKB()`, `getKBArticle()` |
| `conduit/plugins/support/seed.go` | Agent profile seeding | `seedAgent()`, `itSupportSystemPrompt` const |
| `conduit/plugins/support/plugin.yaml` | Declarative spec (not parsed) | Envelope types, hooks, API endpoints |

### Frontend (React/TypeScript)

| File | Purpose | Key Components |
|------|---------|---------------|
| `conduit/ui/src/components/chat/envelopes/KBResultCard.tsx` | KB search result display | `KBResultCard`, `ArticleCard` |
| `conduit/ui/src/components/chat/envelopes/TicketFormCard.tsx` | Ticket creation form | `TicketFormCard` |
| `conduit/ui/src/components/chat/envelopes/TicketConfirmationCard.tsx` | Ticket confirmation display | `TicketConfirmationCard` |
| `conduit/ui/src/components/chat/envelopes/ResolutionCaptureCard.tsx` | Resolution capture form | `ResolutionCaptureCard` |
| `conduit/ui/src/components/chat/envelopes/EnvelopeRenderer.tsx` | Dispatch to correct card | Type-checks `envelope.type` and renders |

### Integration Points

| File | Lines | What It Does |
|------|-------|-------------|
| `conduit/cmd/conduit/main.go` | 259-288 | Creates plugin host, registers services, loads support plugin, re-runs MCP discovery |
| `conduit/internal/chat/engine.go` | 550-561 | Extracts `ENVELOPE_DATA` from search_kb results into `pendingEnvelopes` |
| `conduit/internal/chat/engine.go` | 682-688 | Injects pending envelopes after LLM response |
| `conduit/internal/chat/engine.go` | 977-1000 | Falls back to direct MCP discovery for plugin-registered servers |
| `conduit/internal/chat/envelope.go` | 42-84 | `buildKBEnvelope()` -- sanitizes bodies, wraps as envelope JSON |
| `conduit/internal/server/server.go` | 59-63 | Registers plugin management routes |

## 4. Data Flow: KB Search

Step-by-step flow when a user asks an IT question:

### Step 1: User sends message

User types: "My VPN keeps disconnecting when I switch networks"

### Step 2: Agent receives tools

`getToolsForAgent()` runs. The IT Support agent's `MCPServers` field is `["cortex","support-kb"]`. The tool broker may not know about `support-kb`, so the **direct MCP discovery fallback** kicks in (`engine.go` lines 977-1000):

```go
// For each server in agent.MCPServers, discover tools
srvTools, err := e.MCPManager.DiscoverServerTools(ctx, "support-kb")
// Adds: mcp__support-kb__search_kb, mcp__support-kb__get_kb_article
```

### Step 3: LLM calls search_kb

The LLM, guided by the system prompt ("call search_kb ONCE"), calls `mcp__support-kb__search_kb` with:
```json
{"query": "VPN keeps disconnecting when I switch networks"}
```

### Step 4: KB search executes

`KBTransport.searchKB()` (`kb.go` lines 121-201):

1. Calls `kb_smart_search($1, $2, $3, $4)` Postgres function with the query
2. Joins `kb_articles` table to get full body text
3. Classifies confidence (high/medium/low) based on rank and match method
4. Builds **compact** JSON (no body) for the LLM
5. Builds **full** JSON (with body) for the envelope
6. Returns combined response:

```
{compact JSON}

[SYSTEM: KB articles found and will be displayed to the user automatically. Write ONE brief sentence introducing the results. Do NOT call any more tools.]

<!--ENVELOPE_DATA:{full JSON with bodies}:ENVELOPE_DATA-->
```

### Step 5: Engine captures envelope data

The chat engine (`engine.go` lines 550-561) detects the tool name ends with `__search_kb`:

```go
if strings.HasSuffix(tu.Name, "__search_kb") {
    if eStart := strings.Index(result, "<!--ENVELOPE_DATA:"); eStart >= 0 {
        tail := result[eStart+len("<!--ENVELOPE_DATA:"):]
        if eEnd := strings.Index(tail, ":ENVELOPE_DATA-->"); eEnd >= 0 {
            if env := buildKBEnvelope(tail[:eEnd]); env != "" {
                pendingEnvelopes = append(pendingEnvelopes, env)
            }
        }
    }
}
```

### Step 6: LLM writes brief intro

The LLM sees the compact results and the `[SYSTEM: ...]` instruction. It writes something like:

> "I found some articles that should help with your VPN disconnection issue."

### Step 7: Engine injects envelopes

After the LLM finishes, `pendingEnvelopes` are injected (`engine.go` lines 682-688):

```go
for _, env := range pendingEnvelopes {
    envelopeBlock := "\n\n```conduit-envelope\n" + env + "\n```"
    responseContent += envelopeBlock
    ch <- StreamEvent{Type: "delta", Content: envelopeBlock}
}
```

### Step 8: Frontend renders KBResultCard

`ParseEnvelopes()` extracts the envelope JSON. The frontend's `EnvelopeRenderer` sees `type: "kb-result"` and renders `KBResultCard`.

The card shows:
- Article count header ("Knowledge Base -- 3 articles found")
- Each article as an expandable card with ID badge, title, category pill, severity dot, confidence label
- First article expanded by default
- Article body rendered as markdown via `MessageContent`

## 5. Ticket Flow

### Step 1: User says KB didn't help

User: "Those articles didn't help, can you create a ticket?"

### Step 2: LLM emits ticket-form envelope

The system prompt instructs the LLM to emit:

````
```conduit-envelope
{"kind":"envelope","version":1,"type":"ticket-form","data":{"categories":["network","access","vpn",...],"prefilled":{"title":"VPN disconnecting on network switch","description":"...","category":"vpn"}}}
```
````

### Step 3: TicketFormCard renders

`EnvelopeRenderer` dispatches to `TicketFormCard`. The form displays:
- **Issue Summary** (text input, pre-filled from conversation)
- **Category** (select dropdown with 13 categories)
- **Priority** (radio buttons: low/medium/high/critical)
- **Description** (textarea, pre-filled)
- **Steps Already Tried** (textarea, optional)
- **Create Ticket** button

### Step 4: User submits form

`TicketFormCard` POSTs to `/api/plugins/tickets`:

```json
{
  "title": "VPN disconnecting on network switch",
  "category": "vpn",
  "priority": "medium",
  "description": "My VPN keeps disconnecting...",
  "steps_tried": "Tried restarting GlobalProtect..."
}
```

### Step 5: TicketHandler creates ticket

`TicketHandler.Create()` (`tickets.go` lines 67-101):

1. Generates ID: `TKT-{unix_millis}-{random_4digit}` (e.g., `TKT-1742392845123-0042`)
2. Sets defaults: status=`open`, priority=`medium`
3. Calls `routingForCategory("vpn")` -> `"Network Operations -- VPN/Remote Access"`
4. Stores in in-memory `TicketStore`
5. Returns the full ticket JSON

### Step 6: TicketFormCard shows success state

The card transitions to a success view showing:
- Green header with ticket ID and title
- Category, priority, routing in a 3-column grid
- **Download Ticket** button (opens new tab)
- Production note: "In production, this ticket would be automatically created in BMC Helix ITSM"

### Step 7: onSendMessage callback

The card also sends a message back to the chat:
```
Ticket created: TKT-1742392845123-0042 -- VPN disconnecting on network switch [Category: vpn, Priority: medium, Routing: Network Operations -- VPN/Remote Access]
```

This keeps the conversation context aware of the ticket creation.

### Step 8: Download (optional)

Clicking "Download Ticket" opens `/api/plugins/ui/support-ticket-download?ticket_id=TKT-xxx`. The `DownloadHandler` (`download.go`) renders a styled HTML page with:
- Ticket title, ID, status/priority badges
- Category, requester, description
- Steps tried, resolution (if any)
- Related KB articles
- Routing recommendation in a blue highlighted box
- DEMO disclaimer banner
- Print-optimized CSS

## 6. KB Database

### Connection

`KBTransport` connects to PostgreSQL: `host=localhost port=5432 dbname=kb_demo sslmode=disable` (hardcoded in `kb.go` line 22).

### Search Function

The search uses a Postgres function `kb_smart_search(query, category_filter, severity_filter, max_results)` that returns:

| Column | Type | Description |
|--------|------|-------------|
| `id` | text | Article ID (e.g., "KB-042") |
| `title` | text | Article title |
| `category` | text | Category (e.g., "VPN / GlobalProtect") |
| `severity` | text | low/medium/high/critical |
| `tags` | text[] | Search tags |
| `related` | text[] | Related article IDs |
| `rank` | float | Search relevance rank |
| `headline` | text | Highlighted match snippet |
| `match_method` | text | How it matched: fts_exact, fts_prefix, fuzzy |

The function is joined with `kb_articles` to get the full `body` text.

### Confidence Classification

`classifyConfidence()` (`kb.go` lines 240-249):

| Condition | Confidence |
|-----------|-----------|
| rank >= 0.3 AND method is fts_exact or fts_prefix | high |
| rank < 0.2 AND method is fuzzy | low |
| Everything else | medium |

### Default Results

The search defaults to 3 results (`kb.go` line 134) -- enough for relevance, small enough to avoid context window bloat.

## 7. Agent Profile

### Configuration

| Field | Value |
|-------|-------|
| ID | `it-support-001` |
| Slug | `it-support` |
| Name | `IT Support` |
| Model | `claude-sonnet-4-20250514` |
| Mode | `default` |
| CanExecute | `false` |
| MCP Servers | `["cortex","support-kb"]` |
| Tool Permissions | `{"allow":["mcp__cortex__*","mcp__support-kb__*"]}` |

### System Prompt Design

The system prompt (`seed.go` line 58) is structured around three principles:

**1. Search once, present briefly.**
The agent calls `search_kb` exactly once per user message with the user's raw text as the query. It does NOT rephrase or add keywords -- the KB search engine handles natural language. After results come back, the agent writes 1-2 sentences of introduction. The system injects the KB result cards automatically.

**2. Never fabricate.**
The agent is explicitly forbidden from inventing KB article IDs, titles, or resolution steps. It presents only what the tool returns.

**3. Escalate via envelope.**
When the KB doesn't help, the agent emits a `ticket-form` envelope with the exact JSON structure. The system prompt includes the literal envelope syntax with `conduit-envelope` fence.

### Why "search once" matters

Without this constraint, the LLM might:
- Search multiple times, creating duplicate KB cards
- Call `get_kb_article` after `search_kb`, wasting context
- Rephrase the query and search again with different terms

The `[SYSTEM: ...]` instruction in the search results reinforces this: "Do NOT call any more tools."

## 8. Envelope Components

### KBResultCard

**File:** `conduit/ui/src/components/chat/envelopes/KBResultCard.tsx`

**Props:** `{ data: { results?: KBArticle[], articles?: KBArticle[], query: string } }`

Supports two data shapes for backward compatibility: `results[]` (from search) and `articles[]` (legacy).

**KBArticle shape:**
```typescript
interface KBArticle {
  id: string        // "KB-042"
  title: string
  category: string  // "VPN / GlobalProtect"
  severity: string  // "low" | "medium" | "high" | "critical"
  tags?: string[]
  related?: string[]
  body?: string     // Full article markdown
  rank?: number
  confidence?: string  // "high" | "medium" | "low"
}
```

**Rendering:**
- Header: BookOpen icon + "Knowledge Base -- N articles found"
- Each article: expandable card with `ArticleCard` subcomponent
- First article expanded by default (`defaultExpanded={i === 0}`)
- ID badge (blue), category pill (gray), severity dot (green/amber/red), confidence label (color-coded)
- Expanded body rendered through `MessageContent` (markdown renderer)

### TicketFormCard

**File:** `conduit/ui/src/components/chat/envelopes/TicketFormCard.tsx`

**Props:** `{ data: { prefilled?: {...}, categories: string[] }, onSendMessage?: (content: string) => void }`

**States:** `idle` -> `submitting` -> `success` | `error`

- In `idle`: renders form with pre-filled values from conversation
- In `submitting`: shows spinner on button
- In `success`: transforms into a ticket confirmation view with download button
- In `error`: shows error banner
- On success, calls `onSendMessage()` with a formatted ticket summary

### TicketConfirmationCard

**File:** `conduit/ui/src/components/chat/envelopes/TicketConfirmationCard.tsx`

**Props:** `{ data: { ticket: { id, title, description, category, priority, status, requester, routing, created_at } } }`

Read-only card showing created ticket details. Includes download button and BMC Helix production note. Priority badges use color-coded styles (green/amber/red).

### ResolutionCaptureCard

**File:** `conduit/ui/src/components/chat/envelopes/ResolutionCaptureCard.tsx`

**Props:** `{ data: { ticket_id?: string, issue_summary?: string, categories: string[] }, onSendMessage?: (content: string) => void }`

**States:** `idle` -> `submitted` | `skipped`

Captures what resolved an issue for KB improvement:
- What Fixed It (required textarea)
- Issue Category (optional select)
- Time Spent (optional select: <15 min, 15-30, 30-60, 1-2h, 2h+)
- Related KB Articles (optional text)
- "Should this become a new KB article?" (checkbox, default true)
- Submit Resolution / Skip buttons

On submit, sends structured resolution data back via `onSendMessage()`.

## 9. What's Working

- KB search via Postgres full-text search with confidence scoring
- Deterministic envelope injection (search results always render as cards)
- Ticket CRUD with auto-routing across 14 categories
- Ticket download as formatted HTML with print CSS
- Agent profile auto-seeding (idempotent)
- Event hook registration (message.sent -- currently logging only)
- MCP server registration (support-kb) with post-plugin discovery
- All four frontend envelope components render correctly
- Direct MCP discovery fallback ensures plugin tools are available even when the broker doesn't know about them

## 10. What Needs Work

### Backend

- **Hardcoded DB connection** -- `kb.go` line 22 hardcodes `host=localhost port=5432 dbname=kb_demo`. Needs configuration.
- **In-memory ticket store** -- Tickets are lost on restart. Needs persistence (SQLite or Postgres).
- **Event hook is a stub** -- The `supportEventHook` only logs. Could be used for analytics, KB article view tracking, or resolution pattern detection.
- **No `get_kb_article` envelope injection** -- Only `search_kb` triggers deterministic injection. If the LLM calls `get_kb_article`, results come back as plain text.
- **No resolution storage** -- `ResolutionCaptureCard` sends data back as a chat message. The resolution data should be stored and optionally used to update the ticket or create a KB article.
- **KB article seeding** -- No code to populate the `kb_demo` database. The `seed.go` only seeds the agent profile, not KB articles.
- **No ticket status transitions** -- The API supports updating status via PUT, but there's no workflow for moving tickets through open -> in_progress -> resolved -> closed.

### Frontend

- **TicketFormCard duplicates confirmation UI** -- Both `TicketFormCard` (success state) and `TicketConfirmationCard` render similar ticket confirmation views. Should use one component.
- **No error recovery on ticket creation** -- If the POST fails, the user can retry, but there's no detailed error handling (e.g., validation errors).
- **Resolution capture not connected** -- The `ResolutionCaptureCard` exists but there's no flow that triggers it. The agent doesn't emit `resolution-capture` envelopes yet.
- **No ticket list view** -- `GET /api/plugins/tickets` exists but there's no UI to browse tickets.

### Integration

- **EnvelopeRenderer is hardcoded** -- Adding new envelope types requires editing `EnvelopeRenderer.tsx`. This blocks the plugin from being truly standalone.
- **Agent profile not updated on re-load** -- If the system prompt changes in code, the seeded agent profile isn't updated (idempotent create, no update).

## 11. Extracting to Standalone Plugin

This section describes what would need to change to make the IT Support plugin (and plugins in general) truly installable and self-contained.

### What Would Need to Change in the Plugin Itself

**Configuration system:**
The plugin needs a way to declare and receive configuration. The `plugin.yaml` should support a `config` section:

```yaml
config:
  kb_database_url:
    type: string
    required: true
    description: "PostgreSQL connection string for KB database"
    default: "host=localhost port=5432 dbname=kb_demo sslmode=disable"
  helix_api_url:
    type: string
    required: false
    description: "BMC Helix ITSM API endpoint (production only)"
```

The `Load()` method would receive config values via the Host:
```go
func (p *SupportPlugin) Load(host plugin.Host) error {
    dbURL, _ := host.GetConfig("kb_database_url")
    // ...
}
```

**Ticket persistence:**
Replace `TicketStore` (in-memory map) with a configurable store. Options:
1. Use the Host's `"store"` service to create a plugin-specific SQLite table
2. Accept a separate database URL in config
3. Let the Host provide a `PluginStore` interface with key-value or table operations

**Self-contained agent profile:**
Move the agent profile to `plugin.yaml` or a separate `agent.yaml` so it can be loaded without the `store` package dependency:

```yaml
agents:
  - slug: it-support
    name: IT Support
    model: claude-sonnet-4-20250514
    mcp_servers: [support-kb]
    system_prompt_file: prompts/it-support.md
```

### What Would Need to Change in Conduit's Plugin Loader

**YAML-driven registration:**
The loader should parse `plugin.yaml` and call the appropriate Host methods automatically:
1. Read `registers.envelopes` -> register UI components
2. Read `registers.hooks` -> register event hooks
3. Read `api_endpoints` -> validate that the plugin registers matching CRUD handlers
4. Read `requires.mcp_servers` -> validate servers are available or defer loading
5. Read `config` -> load config from environment or config file, pass to `Load()`

**Dependency resolution:**
Implement topological sort for plugin load order based on `requires` and `Dependencies()`.

**Error recovery:**
If a plugin fails to load, log the error and continue. Currently a WARNING is logged (`main.go` line 276) but this should be formalized with a health check API.

### Auto-Discovery from a plugins/ Directory

Replace the manual plugin instantiation in `main.go` with directory scanning:

```
conduit/plugins/
  support/
    plugin.yaml          # Parsed by loader
    plugin.go            # Compiled into conduit binary (for now)
    ...
  session-stats/
    plugin.yaml
    handlers.go
    ...
```

**Phase 1 (compiled):** All plugins in `conduit/plugins/` are compiled into the binary. The loader reads `plugin.yaml` from each directory, validates, and calls the Go plugin's `New()` and `Load()` in dependency order. A plugin registry maps YAML name to Go constructor.

**Phase 2 (dynamic):** Use Go's `plugin` package or HashiCorp's `go-plugin` for dynamic loading. Each plugin compiles to a `.so` file. The loader scans a directory (e.g., `~/.conduit/plugins/`), loads `.so` files, and calls the exported `New()` function.

### Frontend Component Loading

This is the hardest part. Currently, envelope components are compiled into the React bundle.

**Option A: Dynamic imports with a build step.**
At build time, scan plugin directories for React components. Generate a dynamic import map:

```typescript
// Auto-generated by build step
const PLUGIN_ENVELOPES: Record<string, () => Promise<{ default: React.ComponentType }>> = {
  'kb-result': () => import('./plugins/support/KBResultCard'),
  'ticket-form': () => import('./plugins/support/TicketFormCard'),
  // ...
}
```

`EnvelopeRenderer` becomes:
```tsx
const Component = PLUGIN_ENVELOPES[envelope.type]
if (Component) {
    const Loaded = React.lazy(Component)
    return <Suspense><Loaded data={envelope.data} /></Suspense>
}
```

**Option B: Plugin serves its own bundle.**
Each plugin builds its React components into a JS bundle (e.g., `support-ui.js`). The Host serves it at `/api/plugins/ui/support/bundle.js`. The frontend dynamically loads and evaluates it. This is more complex but allows fully independent plugin development.

**Option C: Server-rendered HTML.**
Plugins render HTML server-side (like the ticket download page does today) and the frontend embeds it in an iframe or uses `dangerouslySetInnerHTML`. Simpler but limited interactivity.

**Recommended:** Option A for near-term, Option B for long-term.

### Plugin Packaging Format

A standalone plugin should be a single directory with this structure:

```
my-plugin/
  plugin.yaml              # Required: declarative spec
  plugin.go                # Required: Go Plugin implementation
  go.mod / go.sum          # Go dependencies
  agents/                  # Optional: agent profiles
    my-agent.yaml
  prompts/                 # Optional: system prompts
    my-agent.md
  ui/                      # Optional: React components
    src/
      MyEnvelope.tsx
      MyWidget.tsx
    package.json
  migrations/              # Optional: database migrations
    001_create_tables.sql
  config.example.yaml      # Optional: example configuration
```

### Plugin Configuration

Configuration should flow from multiple sources (in priority order):
1. Environment variables: `CONDUIT_PLUGIN_SUPPORT_KB_DATABASE_URL`
2. Config file: `~/.conduit/plugins/support/config.yaml`
3. Plugin defaults from `plugin.yaml`

The Host should provide a `GetConfig(key string) (string, error)` method that resolves through these layers.

### Plugin Marketplace/Registry Concept

**Near-term:** A curated list of plugins in a GitHub repository. Each entry points to a Git repo + tag. Users clone into `~/.conduit/plugins/` and rebuild.

**Mid-term:** A simple registry API that returns plugin metadata (name, version, description, compatibility, download URL). A CLI command `conduit plugin install support@0.1.0` fetches and installs.

**Long-term:** A web UI for browsing, installing, configuring, and updating plugins. Version compatibility checks against the running Conduit version.

### Versioning and Dependency Management

**Plugin versioning:**
- Plugins declare their version in `plugin.yaml` (semver)
- Plugins declare the minimum Conduit version they require:
  ```yaml
  requires:
    conduit: ">=0.2.0"
  ```

**API stability:**
- The `plugin.Plugin` interface is the stability contract
- Breaking changes to the interface require a major version bump
- Host services (`"store"`, `"mcp"`, etc.) should be versioned -- plugins declare which service versions they need

**Dependency between plugins:**
- `Dependencies()` already supports declaring required plugins
- Could be extended with version constraints: `["session-stats@>=1.0.0"]`
- The loader should detect circular dependencies and fail with a clear error

**Migration support:**
- Plugins with database tables should include migration files
- The Host runs pending migrations during `Load()` using a plugin-specific migration table
- Migrations are idempotent and ordered by filename
