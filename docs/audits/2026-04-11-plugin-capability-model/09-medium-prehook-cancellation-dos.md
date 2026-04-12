# [Medium] Pre-hook cancellation allows any plugin to block messages and tool execution

**Scope:** Plugin capability model
**Topic:** Security — denial of service via pre-hook
**Date:** 2026-04-11

## Problem

`EmitPreHook` runs hooks synchronously and returns `true` (cancel the action) if any hook returns `plugin.ErrCancelled` or sets `event.Data["cancel"] = true`. A plugin registering a hook for `message.sending` or `tool.executing` can block ALL message sends or tool executions by always returning `ErrCancelled`.

## Evidence

`internal/plugin/events.go:L552-592`:

```go
func (h *Host) EmitPreHook(eventType, sessionID string, data map[string]interface{}) bool {
    // ...
    for _, hook := range hooks {
        ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
        err := hook.Handle(ctx, event)
        cancel()
        if err != nil {
            if errors.Is(err, plugin.ErrCancelled) {
                h.logger.Info("pre-hook cancelled action", "eventType", eventType)
                return true
            }
        }
    }
    // Legacy: check map-based cancel flag
    if cancelled, ok := event.Data["cancel"].(bool); ok && cancelled {
        return true
    }
    return false
}
```

Two cancellation mechanisms exist:
1. Return `plugin.ErrCancelled` from the handler.
2. Mutate `event.Data["cancel"] = true` (legacy path — event data is shared by reference).

## Impact

- A malicious or buggy plugin can completely prevent any message from being sent to the LLM (`message.sending` hook) or any tool from executing (`tool.executing` hook).
- The 5-second timeout per hook means a malicious plugin can also slow down every message/tool operation by 5 seconds before the timeout fires.
- The map-mutation path (`event.Data["cancel"]`) is particularly fragile — one hook's mutation affects subsequent hooks' view of the event data.

## Recommendation

1. Log which plugin cancelled an action (currently the log just says "pre-hook cancelled action" with no plugin ID).
2. Add a manifest flag (`can_cancel: true`) required for plugins that use pre-hook cancellation. Default to non-cancellation.
3. Consider rate-limiting or circuit-breaking repeated cancellations from the same plugin.
4. Remove the legacy map-mutation cancellation path — it's a shared-mutable-state pattern that makes hook ordering semantics unpredictable.

## References

- `internal/plugin/events.go:L550-592` — EmitPreHook
- `internal/plugin/prehook_test.go` — test coverage for both cancellation paths
- SDK: `framework/libs/go-plugin/plugin.go:L263` — `ErrCancelled` sentinel
