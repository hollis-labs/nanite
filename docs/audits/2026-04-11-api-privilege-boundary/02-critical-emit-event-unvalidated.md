# [Critical] POST /api/plugins/events accepts arbitrary event types and data with no validation

**Scope:** Plugin event API
**Topic:** Security
**Date:** 2026-04-11

## Problem

`POST /api/plugins/events` accepts an arbitrary `event_type`, `source`, `data`, and `session_id` and emits them into the plugin event bus with no validation, no allowlisting, and no authentication beyond basic auth (which is optional). Any caller can impersonate any event source and inject any event type.

## Evidence

`internal/server/server.go:L189-222`

```go
func (s *Server) handleEmitEvent(w http.ResponseWriter, r *http.Request) {
    // ...
    var req struct {
        EventType string                 `json:"event_type"`
        Source    string                 `json:"source"`
        Data      map[string]interface{} `json:"data"`
        SessionID string                 `json:"session_id"`
    }

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "Invalid JSON", http.StatusBadRequest)
        return
    }
    defer r.Body.Close()

    event := naniteplugin.NewEvent(req.EventType, req.Source, naniteplugin.EventData{
        SessionID: req.SessionID,
    })

    // Override data with the provided data
    event.Data = req.Data
    // ...
    s.pluginHost.EmitEvent(event)
    // ...
}
```

Issues:

1. **No event type validation.** `req.EventType` is passed directly — an attacker can emit internal events like `tool.executing`, `session.start`, `config.changed`, or any custom event that plugins listen for.
2. **No source validation.** `req.Source` is arbitrary — an attacker can impersonate `"system"`, `"engine"`, or any plugin.
3. **No request body size limit.** `json.NewDecoder(r.Body)` reads unbounded input.
4. **`defer r.Body.Close()` after Decode.** The `Decode` call may not consume the full body, so the deferred close is correct for cleanup but the body was not limited before Decode.
5. **Data map is user-controlled.** `event.Data = req.Data` directly sets the event data from user input with no sanitization.

## Impact

- **Plugin behavior manipulation.** If any plugin has a hook on `tool.executing` that modifies tool arguments or applies security policies, a crafted event can bypass or confuse those hooks.
- **Trigger rule activation.** Connector trigger rules (`internal/api/triggers.go`) fire on event types. An attacker can trigger external connectors (webhooks, notifications) by emitting the matching event type.
- **Event stream pollution.** The SSE event stream at `/api/plugins/events/stream` forwards all events — injected events reach every connected client.

## Recommendation

1. Add an allowlist of event types that the HTTP API is permitted to emit. Internal engine events should not be emittable from the API.
2. Set `Source` to a fixed value like `"api"` — never let the caller specify it.
3. Add a `http.MaxBytesReader` wrapper on `r.Body`.
4. Consider whether this endpoint should exist at all. If it's for frontend-originated UI events, the allowlist should be narrow (e.g., `ui.*` prefix only).

## References

- Plugin event catalog: `internal/plugin/events.go` (30+ event types)
- Trigger dispatch: `internal/plugin/triggers.go` — fires connectors on matching events
- Cross-ref: `01-critical-cors-origin-reflection.md` — combined with CORS, a remote site can emit arbitrary plugin events.
