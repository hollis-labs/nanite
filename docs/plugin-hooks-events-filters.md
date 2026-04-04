# Plugin Hooks, Events & Filters Plan

> Decision date: 2026-04-04
> Companion to `plugin-extraction-plan.md` — that doc covers *what* moves into plugins;
> this doc covers *how* plugins interact with the host.

## Principles

1. **Events are fire-and-forget.** Plugins observe but cannot block or modify.
2. **Pre-hooks can cancel.** Return `true` to abort the operation. Cannot modify data.
3. **Filters transform data.** Synchronous pipeline — each handler receives the output of the previous one, ordered by priority (lower = earlier). Async filters deferred to a future version.
4. **UI slots are named mount points.** Plugins register components into slots; frontend renders them.
5. **Wire what's defined before adding new things.** 21 events are defined but never emitted.

---

## Gap 1: Wire Dead Events

21 event constants exist in `internal/plugin/events.go` but are never emitted.
Add `Emit*()` calls at the correct points in the codebase.

### Session Events

| Event | Where to Emit | File |
|-------|--------------|------|
| `session.end` | When session is explicitly ended/closed | `internal/chat/engine.go` |
| `session.archived` | When session is archived | `internal/api/sessions.go` (archive handler) |

### Agent Events

| Event | Where to Emit | File |
|-------|--------------|------|
| `agent.switched` | When agent changes mid-session | `internal/chat/engine.go` (agent assignment) |
| `agent.loaded` | When agent profile is loaded at session start | `internal/chat/engine.go` (session init) |

### Message Events

| Event | Where to Emit | File |
|-------|--------------|------|
| `message.sent` | After user message is persisted | `internal/api/messages.go` (send handler) |
| `message.deleted` | After message deletion | `internal/api/messages.go` (delete handler) |

### Mode Events

| Event | Where to Emit | File |
|-------|--------------|------|
| `mode.changed` | When session mode changes | `internal/api/modes.go` or `internal/chat/engine.go` |
| `scope.changed` | When scope guard changes | `internal/provider/scope_guard.go` |

### Tool Events

Already wired: `tool.called`, `tool.failed`. No gaps.

### UI Events

| Event | Where to Emit | File |
|-------|--------------|------|
| `envelope.rendered` | After envelope is built and attached to message | `internal/chat/envelope.go` |
| `widget.loaded` | After UI component is registered | `internal/plugin/host.go` |
| `action.triggered` | When a plugin action is invoked via API | `internal/api/api.go` (action endpoint) |

### Workflow Events

| Event | Where to Emit | File |
|-------|--------------|------|
| `workflow.started` | When workflow execution begins | `internal/workflow/engine.go` |
| `workflow.complete` | When workflow finishes successfully | `internal/workflow/engine.go` |
| `workflow.failed` | When workflow errors | `internal/workflow/engine.go` |

### Config Events

| Event | Where to Emit | File |
|-------|--------------|------|
| `config.changed` | After plugin or user config is updated | `internal/plugin/host.go` (SetConfig) |

### Plugin Lifecycle Events

| Event | Where to Emit | File |
|-------|--------------|------|
| `plugin.installed` | After plugin is loaded | `internal/plugin/host.go` (LoadPlugin) |
| `plugin.uninstalled` | After plugin is unloaded | `internal/plugin/host.go` (UnloadPlugin) |

### Provider Events

| Event | Where to Emit | File |
|-------|--------------|------|
| `provider.error` | On LLM provider error | `internal/chat/engine.go` (error handling) |
| `provider.fallback` | When circuit breaker triggers fallback | `internal/provider/circuit.go` |

---

## Gap 2: Wire Pre-Hooks

Two pre-hooks are defined with cancellation support but never called.

### `message.sending`
- **Where:** `internal/chat/engine.go`, before the LLM API call in `generateResponse()`
- **Data:** `{session_id, agent_id, model, messages (count), system_prompt (length)}`
- **Cancel effect:** Abort the LLM call, return cancellation message to user
- **Use case:** Content policy plugin blocks certain prompts, rate-limit plugin

### `tool.executing`
- **Where:** `internal/chat/engine.go`, before tool execution in the tool-use loop
- **Data:** `{session_id, tool_name, tool_input}`
- **Cancel effect:** Skip tool execution, return "tool blocked" result to LLM
- **Use case:** Permission plugin blocks dangerous tools, audit plugin logs before execution

---

## Gap 3: Filter System (NEW)

### Architecture

```go
// internal/plugin/filter.go

type FilterFunc func(data interface{}, ctx FilterContext) (interface{}, error)

type FilterContext struct {
    SessionID string
    AgentID   string
    Metadata  map[string]interface{}
}

type filterEntry struct {
    PluginID string
    Priority int // lower = earlier in chain
    Fn       FilterFunc
}

// On Host:
func (h *Host) RegisterFilter(name string, priority int, fn FilterFunc) error
func (h *Host) ApplyFilter(name string, data interface{}, ctx FilterContext) (interface{}, error)
```

`ApplyFilter` iterates registered handlers for `name` in priority order.
Each handler receives the output of the previous one. If any handler returns an error, the chain stops and the error propagates.

### Filter Points

| Filter Name | Data Type | Where Applied | Use Case |
|-------------|-----------|--------------|----------|
| `system_prompt` | `string` | `internal/chat/context.go` — after prompt assembly, before LLM call | Inject persona rules, compliance disclaimers, dynamic instructions |
| `user_message` | `string` | `internal/chat/engine.go` — after user message received, before LLM sees it | PII redaction, input sanitization, expansion |
| `tool_result` | `string` | `internal/chat/engine.go` — after tool execution, before result enters context | Summarize large outputs, redact secrets, reformat |
| `assistant_response` | `string` | `internal/chat/engine.go` — after LLM response, before persist/render | Tone filter, brand voice, compliance scrubbing |
| `context_window` | `[]Message` | `internal/chat/context.go` — full assembled context before LLM call | Plugin prunes or injects context blocks |
| `envelope_data` | `map[string]interface{}` | `internal/chat/envelope.go` — envelope payload before frontend render | Enrich card data, add links, transform fields |
| `shell_command` | `string` | `internal/shell/` (upcoming) — command before execution | Alias expansion, safety rewriting, logging |
| `shell_output` | `string` | `internal/shell/` (upcoming) — output before it enters conversation | Redact secrets from output, truncation |

---

## Gap 4: New UI Slots

### Frontend Slots to Add

| Slot Name | Location | Purpose | Priority |
|-----------|----------|---------|----------|
| `message-actions` | Per-message footer area | Plugin buttons per message (translate, bookmark, copy, etc.) | High — most requested |
| `message-header` | Per-message header/badge area | Plugin badges/tags on messages (sentiment, cost, etc.) | Medium |
| `composer-above` | Above composer input | Info drawers (shell info drawer is this), suggestions | High — needed for shell feature |
| `composer-below` | Below composer input | Quick actions, suggestions, hints | Low |
| `session-sidebar` | Session list item decoration | Plugin badges on session entries | Low |
| `modal` | Overlay/dialog | Plugin-triggered modals (giphy already does this ad-hoc, should use slot) | Medium |

### Backend Changes
- Add slot name constants to `internal/plugin/` types
- No host.go changes needed — `RegisterSlot()` already accepts any slot name string

### Frontend Changes
- Add `usePluginSlots("slot-name")` calls at each mount location
- `message-actions`: in message component, after message content
- `message-header`: in message component, before message content
- `composer-above`: in composer component, above input area
- `composer-below`: in composer component, below input area
- `session-sidebar`: in session list item component
- `modal`: global modal container, triggered by plugin actions

---

## Gap 5: Wire Unconsumed Defined Slots

Three slots are defined in backend types but have no frontend rendering.

| Slot | Frontend File | What to Add |
|------|--------------|-------------|
| `context-menu:message` | Message component | Right-click/long-press context menu with plugin items |
| `context-menu:session` | Session list item | Right-click context menu with plugin items |
| `command-palette` | Global component | Cmd+K palette includes plugin-registered commands |

---

## Gap 6: Missing Event Categories

Events that don't exist yet and should be added to `events.go`.

### Shell Events (for upcoming `!` feature)

| Event | When |
|-------|------|
| `shell.exec` | After `!` command is executed |
| `shell.error` | When shell command fails |
| `shell.blocked` | When denylist blocks a command |

### Context Events

| Event | When |
|-------|------|
| `context.compacted` | After context window compaction |
| `context.assembled` | After system prompt + context assembly |

### Artifact Events

| Event | When |
|-------|------|
| `artifact.created` | After artifact is persisted |
| `artifact.deleted` | After artifact is deleted |

### API Events

| Event | When |
|-------|------|
| `api.request` | On incoming API request (middleware level) |
| `api.response` | On outgoing API response (middleware level) |

---

## Implementation Order

### Task 1: Wire Dead Events + Pre-Hooks
- Add `Emit*()` calls for all 21 dead events at the locations listed above
- Wire `EmitPreHook("message.sending", ...)` in engine.go before LLM call
- Wire `EmitPreHook("tool.executing", ...)` in engine.go before tool exec
- Add new event constants for shell, context, artifact, API categories
- **Estimated scope:** ~15 files touched, no architecture changes

### Task 2: Filter System
- Create `internal/plugin/filter.go` with `FilterFunc`, `FilterContext`, registration, `ApplyFilter`
- Add `RegisterFilter()` and `ApplyFilter()` to Host
- Wire filter points at the 6 locations listed (shell filters deferred to shell feature)
- Add filter registration to plugin SDK interface
- Tests: filter chain ordering, error propagation, empty chain passthrough
- **Estimated scope:** 1 new file, ~6 files touched for wiring

### Task 3: New UI Slots (Frontend)
- Add slot constants to backend types
- Wire `composer-above` first (needed for shell feature)
- Wire `message-actions` second (highest value)
- Wire remaining slots: `message-header`, `composer-below`, `session-sidebar`, `modal`
- **Estimated scope:** ~6 frontend files

### Task 4: Wire Unconsumed Slots (Frontend)
- Add context menu rendering for `context-menu:message` and `context-menu:session`
- Add command palette integration for `command-palette` slot
- **Estimated scope:** ~3 frontend files, may need shadcn ContextMenu + CommandDialog

---

## Open Questions

1. **Filter error policy:** When a filter handler errors, should the chain abort (current plan) or skip that handler and continue? Abort is safer but one bad plugin breaks everything.
2. **API events granularity:** `api.request`/`api.response` at middleware level could be noisy. Consider opt-in per-route or only for specific prefixes.
3. **Modal slot lifecycle:** Who manages modal open/close state — the plugin or the host? Need a `openModal(slotId)`/`closeModal()` API on the frontend plugin bridge.
