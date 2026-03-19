# Session Stats Plugin - End-to-End Demo

This document demonstrates how the session-stats plugin proves the universal contract works end-to-end in Conduit.

## Plugin Structure

```
plugins/session-stats/
├── plugin.yaml                    # Plugin specification
├── handlers.go                    # Backend CRUD operations
├── hooks.js                       # Event hook handlers
└── components/
    ├── SessionStatsCard.tsx        # Envelope renderer component
    ├── SessionSummaryCard.tsx      # Summary envelope renderer
    ├── SessionStatsWidget.tsx      # Right-rail widget
    └── SessionStatsQuickActions.tsx # Quick action buttons
```

## Universal Contract Proof Points

### 1. Plugin Registration (plugin.yaml)

✅ **CRUD Operations**: Registers API endpoints for session stats CRUD
- `GET /api/plugins/session-stats/stats/{session_id}` - Read stats
- `POST /api/plugins/session-stats/stats/{session_id}` - Update stats
- `GET /api/plugins/session-stats/export/{session_id}` - Export stats

✅ **Event Hooks**: Registers handlers for system events
- `session.start` → Initialize session stats
- `session.end` → Finalize session stats
- `message.received` → Increment message count, track tokens
- `tool.called` → Increment tool call count

✅ **UI Extensions**: Registers UI components and actions
- Envelope renderers: `SessionStatsCard`, `SessionSummaryCard`
- Widget: `SessionStatsWidget` (right-rail slot)
- Quick actions: "Session Stats", "Export Stats" (session context)

### 2. Integration Points

**Backend Integration** (handlers.go):
- SQLite schema creation and management
- RESTful API endpoints for stats CRUD operations
- CSV export functionality
- Database operations with proper error handling

**Frontend Integration** (React components):
- Envelope system integration for structured data display
- Widget system integration for persistent UI elements
- Quick action system integration for contextual buttons
- Real-time updates and loading states

**Event System Integration** (hooks.js):
- Event listener registration with plugin registry
- Automatic stat tracking on session lifecycle events
- Token usage tracking from message metadata
- Tool usage tracking from tool call events

### 3. Demonstration Workflow

**Step 1: Plugin Loading**
```javascript
// Plugin loader discovers and validates plugin.yaml
const loader = new PluginLoader()
await loader.loadPluginsFromDir('./plugins')

// Registry contains all plugin registrations
const registry = loader.getRegistry()
console.log(registry.listEnvelopeTypes()) // ['session-stats', 'session-summary']
console.log(registry.getQuickActions('session')) // [show-session-stats, export-session-stats]
```

**Step 2: Session Lifecycle**
```javascript
// Session start triggers plugin hook
eventBus.emit('session.start', {
  session_id: 'sess_123',
  agent_id: 'mentat',
  mode: 'architect'
})
// → Creates session_stats record with start_time

// Message received triggers plugin hook
eventBus.emit('message.received', {
  session_id: 'sess_123',
  role: 'user',
  content: 'Hello',
  usage: { input_tokens: 50, output_tokens: 100 }
})
// → Increments message_count, adds to token counts

// Session end triggers plugin hook
eventBus.emit('session.end', { session_id: 'sess_123' })
// → Sets end_time, emits session-stats envelope
```

**Step 3: UI Rendering**
```typescript
// Agent emits envelope in response
const envelope = {
  kind: 'conduit-envelope',
  version: 1,
  type: 'session-stats',
  data: {
    session_id: 'sess_123',
    message_count: 15,
    total_tokens: 2500,
    tool_calls: 3
  }
}

// Conduit's EnvelopeRenderer uses plugin registry
const renderer = registry.getEnvelopeRenderer('session-stats') // 'SessionStatsCard'
// → Renders SessionStatsCard component with stats data
```

**Step 4: Quick Actions**
```typescript
// Right-click or action menu shows contextual quick actions
const actions = registry.getQuickActions('session')
// → [{ id: 'show-session-stats', label: 'Session Stats', ... }]

// User clicks "Session Stats" action
await pluginHandlers.showSessionStats('sess_123')
// → Fetches stats, emits envelope, displays in chat
```

**Step 5: Widget Integration**
```typescript
// Right rail shows plugin widgets
const widgets = registry.getWidgets('right-rail')
// → [{ id: 'session-stats-widget', component: 'SessionStatsWidget', ... }]

// Widget auto-refreshes every 30 seconds
<SessionStatsWidget sessionId="sess_123" />
// → Live updating stats in sidebar
```

## Contract Verification

### ✅ **Universal Plugin Spec**
- Standard `plugin.yaml` format with all required fields
- Declarative registration of capabilities (no code coupling)
- Version management and dependency declaration

### ✅ **Cross-Service Integration**
- Backend API endpoints registered in plugin spec
- Frontend components registered by name
- Event hooks registered by event type
- Database schema managed by plugin

### ✅ **Event-Driven Architecture**
- Plugins react to system events without tight coupling
- Events carry structured payload data
- Plugins can emit events for other plugins to consume

### ✅ **UI Extension Points**
- Envelope renderers extend message display
- Widgets extend application layout
- Quick actions extend context menus
- All extensions are declaratively registered

### ✅ **Data Persistence**
- Plugins manage their own database schemas
- CRUD operations through standard API patterns
- Data export capabilities built-in

## Expected Outputs

1. **Plugin loads successfully** - No validation errors, all registrations active
2. **Database schema created** - `session_stats` table exists with proper indexes
3. **API endpoints respond** - GET/POST to stats endpoints return valid JSON
4. **Event hooks trigger** - Session events update database records correctly
5. **UI components render** - Envelope, widget, and action components display properly
6. **Export works** - CSV download contains session data in expected format

## Testing Commands

```bash
# Test plugin loading
curl http://localhost:8090/api/plugins

# Test stats API
curl http://localhost:8090/api/plugins/session-stats/stats/sess_123

# Test export
curl http://localhost:8090/api/plugins/session-stats/export/sess_123

# Test event emission
curl -X POST http://localhost:8090/api/plugins/emit-event \
  -H "Content-Type: application/json" \
  -d '{"event_type": "session.start", "payload": {"session_id": "sess_123"}}'
```

This implementation proves that the universal plugin contract enables:
- **Declarative capability registration** without code coupling
- **Event-driven integration** across frontend and backend
- **UI extension points** for custom user experiences
- **Data persistence** with plugin-owned schemas
- **Standard API patterns** for interoperability

The session-stats plugin demonstrates all five contract layers working together to extend Conduit's functionality without modifying core application code.