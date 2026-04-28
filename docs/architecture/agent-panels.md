# Agent-Controlled Panels (J8 v1)

> **Status:** v1 shipped 2026-04-27 per CW-20260426-0006 Decision Log.
> Composition primitives, larger panel catalog, plugin-authored composition,
> and agent-ephemeral panels are explicitly v2 and out of scope here.

The agent can open / close a small set of UI drawers and signal a workspace
mode that opens a preset of drawers. Visibility-only — agents never mutate
workspace data through this surface; the user always has the final say on
what's visible.

## v1 catalog (3 drawers)

| Panel ID             | Surface                | Notes |
|----------------------|------------------------|-------|
| `bottom_chat_drawer` | Bottom of chat (separate from right-rail) | Long-form reference content (documents, scratchpads). |
| `work`               | Right-rail tab         | Todos / plans / sprint cards. |
| `workflows`          | Right-rail tab         | Guided-interaction templates. |

Plugin-shipped panel IDs (declared via `plugin.yaml` `registers.panels[]`)
are also addressable, subject to H1 trust.

## Three entry points

### 1. Explicit tool calls (B3 chat surface)

Three self-tools are added to the Chat agent's static surface
(`internal/dispatch/role.go` `ChatToolSurface`):

- `nanite_panel_open(panel_id)` — visibility-only, dismiss-gated.
- `nanite_panel_close(panel_id)` — closes the panel; refused on
  user-opened panels (user-overrides-agent rule).
- `nanite_signal_mode(mode)` — broadcasts a mode signal; the FE resolves
  the mode against the preset map.

Each tool emits a `panel_signal` SSE event onto the originating session's
chat stream. The FE handler in `ui/src/hooks/useChat.ts` (`SSE.PANEL_SIGNAL`)
routes the payload through `applyPanelSignal` in `ui/src/lib/panel-signal.ts`.

Tool result shape:

```json
// nanite_panel_open
{"opened": true, "panel_id": "work"}
{"opened": false, "panel_id": "ghost", "reason": "unknown_panel"}
{"opened": false, "panel_id": "plugin-x", "reason": "untrusted"}

// nanite_panel_close
{"closed": true, "panel_id": "work"}

// nanite_signal_mode
{"signaled": true, "mode": "planning"}
```

### 2. Envelope-borne `target` and `mode` fields

Any envelope can carry optional `target` and `mode` fields
(`internal/chat/envelope.go:Envelope`). The FE handler for `plugin_envelope`
SSE events calls `applyEnvelopePanelEffects` which applies both fields with
`source='agent'`. Independent — both can be set on the same envelope.

```json
{
  "kind": "envelope",
  "version": 1,
  "type": "document-viewer",
  "target": "bottom_chat_drawer",
  "mode": "planning",
  "data": {"...": "..."}
}
```

When `target` is omitted the envelope renders inline in chat (current default
behavior). `mode` works exactly like `nanite_signal_mode(mode)` — FE preset
map lookup, unknown modes silently skipped.

### 3. User-driven open/close (UI)

The right-rail tab strip (RightRailV2) routes user tab switches through
`setPanelOpen(id, 'user')`. Manual closes mark the panel `user_dismissed` via
`markPanelDismissed`. The bottom drawer surface uses
`setBottomDrawerOpen(open, 'user')` for user-driven toggles.

## 4-state dismiss machine

Encoded purely in the layout store (`ui/src/stores/useLayoutStore.ts`)
without a dedicated state field — derived from two existing maps:

| State           | Derivation |
|-----------------|------------|
| `closed`        | No entry in `panelOpenSource[id]` and no entry in `dismissedByUser[id]`. |
| `agent_opened`  | `panelOpenSource[id] === 'agent'` (and not dismissed). |
| `user_opened`   | `panelOpenSource[id] === 'user'` (and not dismissed). |
| `user_dismissed`| `dismissedByUser[id] === true`. Source attribution cleared. |

### Transitions

| From            | Trigger                | To              | Notes |
|-----------------|------------------------|-----------------|-------|
| `closed`        | `setPanelOpen(id, 'agent')` | `agent_opened`  | |
| `closed`        | `setPanelOpen(id, 'user')`  | `user_opened`   | |
| `agent_opened`  | `setPanelOpen(id, 'agent')` | `agent_opened`  | Idempotent. |
| `agent_opened`  | `setPanelOpen(id, 'user')`  | `user_opened`   | User overrides. |
| `agent_opened`  | `markPanelDismissed(id)`    | `user_dismissed`| |
| `user_opened`   | `setPanelOpen(id, 'agent')` | `user_opened`   | Idempotent (user attribution preserved). |
| `user_opened`   | `markPanelDismissed(id)`    | `user_dismissed`| |
| `user_dismissed`| `setPanelOpen(id, 'agent')` | `user_dismissed`| **NO-OP** — agent loses fight. |
| `user_dismissed`| `setPanelOpen(id, 'user')`  | `user_opened`   | Manual re-open clears the dismiss flag. |
| `user_dismissed`| `clearAllPanelDismissed()`  | `closed`        | Reset trigger (see below). |

### Dismiss-reset trigger

The v1 simplification: **any new user-message turn resets dismiss state for
ALL panels**. Wired in `useChat.sendMessage` (calls
`useLayoutStore.getState().clearAllPanelDismissed()` at the top of the user
turn). After reset, the agent can re-open dismissed panels in its response
to the new user turn.

The smarter classified-trigger version ("only reset Work when the new turn
classifies as a todo creation") is captured as
`followups_j8_classified_dismiss_reset` and explicitly out of scope for v1.

### Bottom-drawer close semantics

The bottom drawer surface follows the same machine via
`setBottomDrawerOpen(open, source)`:

- `setBottomDrawerOpen(false, 'user')` → user dismiss; sets the dismiss flag.
- `setBottomDrawerOpen(false, 'agent')` → agent close; **NO-OP** when the
  current source is `'user'` (user-overrides-agent). Otherwise drops the
  source entry without touching dismiss state.

## Mode preset map

`ui/src/lib/panel-modes.ts` maps mode names to panel ID arrays. v1 ships a
single mode:

```ts
export const PANEL_MODE_PRESETS: Record<string, readonly string[]> = {
  planning: ["work", "workflows"],
}
```

Adding a new mode is a one-line change here — no other code needs to update.
The FE applies the preset by calling `setPanelOpen(id, 'agent')` for each
ID in array order; the dismiss machine handles per-panel gating.

## Plugin trust gate

For plugin-shipped panel IDs (anything outside the v1 built-in catalog),
`callPanelOpen` / `callPanelClose` resolve the caller's H1 trust via the
shared `dispatch.TrustResolver` (the store's `ResolveTrust`). Only callers
resolving to `dispatch.TrustTrusted` can address plugin-shipped panel IDs;
others receive `{opened: false, reason: "untrusted"}`.

Built-in panel IDs (`bottom_chat_drawer`, `work`, `workflows`) skip the trust
gate because they're visibility-only and low risk.

The check uses the caller-profile context stamped by the service layer
(`mcp.WithCallerProfile`) — same convention as the H1 subagent gate
(CW-20260421-0014).

## Wire format

### `panel_signal` SSE event

Riding on the existing chat session SSE stream. Payload sits inside the
existing `envelope` field of `chat.StreamEvent` (no new wire field):

```json
{
  "type": "panel_signal",
  "envelope": "{\"action\":\"open\",\"panel_id\":\"work\",\"source\":\"agent\"}"
}
```

`PanelSignal` payload (`internal/mcp/self_tools_panels.go`):

```go
type PanelSignal struct {
    Action  string `json:"action"`            // "open" | "close" | "mode"
    PanelID string `json:"panel_id,omitempty"`// open/close
    Mode    string `json:"mode,omitempty"`    // mode
    Source  string `json:"source"`            // always "agent" for tool-driven
}
```

## Out of scope (v2)

Per J8 Decision Log:

- **Composition primitives** — dynamic panel building from form fields,
  lists, buttons, layout primitives.
- **Larger panel catalog** — Scratchpad, Documents, Context modal, Broker
  decisions, Inbox, Artifacts, Subagent/workers/reminders widgets.
- **Plugin-authored composition** — plugins shipping dynamic panels with
  their own primitives.
- **Agent-ephemeral panels** — runtime-registered, per-turn UI surfaces.
- **Agent runtime `panel_register` tool** — plugins ship via manifest only
  in v1.
