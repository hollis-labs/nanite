# [Medium] Activity emitter sends session metadata to Engine when configured

**Scope:** Chat activity
**Topic:** Outbound network — activity events
**Date:** 2026-04-11

## Problem

The `ActivityEmitter` in `internal/chat/activity.go` sends structured event payloads to an Engine activity feed server when `ENGINE_ACTIVITY_URL` is set. The payloads include session IDs, agent IDs, model names, tool names, token counts, error details, and mode names. This is a localhost-to-localhost call in the typical setup, but if `ENGINE_ACTIVITY_URL` is pointed at a remote host, this metadata leaves the machine.

## Evidence

`internal/chat/activity.go:L54-67`:
```go
func NewActivityEmitter(url string) *ActivityEmitter {
    if url == "" {
        url = os.Getenv("ENGINE_ACTIVITY_URL")
    }
    disabled := url == ""
    if disabled {
        log.Println("activity: no ENGINE_ACTIVITY_URL set — activity emitter disabled")
    }
    // ...
}
```

**Default: disabled.** No `ENGINE_ACTIVITY_URL` = no-op. This is opt-in.

When enabled, the following data is sent per event (`activity.go:L41-49`):
```go
type activityEvent struct {
    ProjectID   string `json:"project_id"`
    EventType   string `json:"event_type"`
    EntityType  string `json:"entity_type"`
    EntityID    string `json:"entity_id"`
    EntityTitle string `json:"entity_title,omitempty"`
    Actor       string `json:"actor,omitempty"`
    Payload     string `json:"payload,omitempty"`
}
```

Example payloads sent:
- `EmitResponseComplete`: `{"model":"...", "input_tokens":N, "output_tokens":N}`
- `EmitToolCall`: `{"tool":"...", "status":"success", "result_len":N}`
- `EmitError`: `{"error_type":"...", "detail":"..."}`

No conversation content is included. The data is operational metadata: session IDs, agent IDs, model names, tool names, token counts.

## Impact

- **Default (disabled):** No data leaves. No privacy concern.
- **With `ENGINE_ACTIVITY_URL` set to localhost:** Metadata goes to a local Engine instance. Normal development setup.
- **With `ENGINE_ACTIVITY_URL` set to remote:** Session metadata leaves the machine. No user content, but operational patterns are visible (when sessions happen, what models/tools are used, token volumes, error types).

This is opt-in by env var, which is the right default. The concern is that the env var is not documented as having privacy implications, and a user might set it without realizing it creates an outbound data flow.

## Recommendation

1. Document `ENGINE_ACTIVITY_URL` as an opt-in data flow that sends operational metadata to the configured endpoint.
2. Consider adding a log line at startup when the emitter is active: "Activity events will be sent to {url}".
3. The error detail in `EmitError` could potentially contain user-context information — audit what `errorType` and `detail` values are passed and ensure no conversation content leaks through error messages.

## References

- `internal/chat/activity.go:L1-250`
