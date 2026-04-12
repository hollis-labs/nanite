# [Medium] action.triggered event has no consumer

**Scope:** internal/plugin/auto_triggers.go, internal/plugin/events.go, internal/chat/, internal/api/, ui/src/
**Topic:** Antipatterns
**Date:** 2026-04-11

## Problem

`AutoTriggerHandler.Handle` emits `action.triggered` events via `h.host.EmitActionTriggered()`, but no code in the chat engine, API layer, or frontend SSE handling consumes or acts on this event. The emitted event is fire-and-forget into a void.

## Evidence

The emit call:

```go
// internal/plugin/auto_triggers.go:75
h.host.EmitActionTriggered(sessionID, action.ID, map[string]interface{}{
    "action_name": action.Name,
    "command":     action.Command,
    "trigger":     triggerName,
    "auto":        true,
})
```

Consumer search results (all negative):
- `grep -r "action.triggered\|ActionTriggered" internal/chat/` — no matches
- `grep -r "action.triggered\|ActionTriggered" internal/api/` — no matches
- `grep -r "action[._]triggered" ui/src/` — no matches

The `docs/hardening-phase-plan.md:38` explicitly calls this out:

> `action.triggered` — **delete** (frontend concern)

This means the hardening plan already identified `action.triggered` as a dead event candidate.

## Impact

The auto-trigger mechanism does real work (queries the database, iterates actions, parses JSON, emits events) but produces no observable effect. Custom actions with auto-triggers configured via the UI settings panel (`ActionsPanel.tsx`) appear to be wired but do nothing when the trigger event fires. This is a user-facing correctness gap: the UI lets users configure auto-triggers, but the backend machinery to act on them is incomplete.

The `Command` field in the action data payload (intended to be "injected into the session") has no injection mechanism.

## Recommendation

Two paths:

1. **Wire the consumer.** Add a hook in the chat engine or API layer that listens for `action.triggered` events and injects the action's `command` into the session. This completes the feature.
2. **Remove the dead path.** If auto-triggers are not currently needed, delete `auto_triggers.go`, remove the `RegisterAutoTriggerHandler` call from `main.go:253`, and document the removal. The store layer (`custom_actions.go`) and UI panel can remain since they serve the manual keybinding/slash-command use case.

Recommended: option 2 now (remove dead code), option 1 when the feature is scoped and a consumer is designed.

## References

- `internal/plugin/events.go:44` — `EventActionTriggered` constant definition
- `internal/plugin/events.go:354-362` — `EmitActionTriggered` implementation
- `cmd/nanite/main.go:253` — registration call site
- `docs/hardening-phase-plan.md:38` — hardening plan flags `action.triggered` for deletion
- `docs/architecture/plugin-evolution-plan.md:300` — original design intent
- `ui/src/components/settings/ActionsPanel.tsx` — UI that configures auto-triggers with no backend effect
