# [High] Panic in any event hook crashes the whole host — plan doesn't fix it

**Scope:** plugin system — event dispatch
**Topic:** plan-completeness / error handling
**Date:** 2026-04-10

## Problem

`Host.EmitEvent` spawns one goroutine per registered event hook with no `recover()` — a single panic in a plugin's `EventHook.Handle` implementation (or in the proxy `subprocessEventHook.Handle`, or in the trigger dispatcher) takes down the entire nanite process. The reviewer-context file flags this explicitly. The plan mentions `EventHook` in B.6 only to change its interface (adding `PluginID()`) for cleanup purposes, and never adds a recover boundary.

## Evidence

`internal/plugin/host.go:L1080-1106`:

```go
func (h *Host) EmitEvent(event plugin.Event) {
	// 1. Dispatch to registered event hooks.
	h.mu.RLock()
	hooks := h.eventHooks[event.Type]
	h.mu.RUnlock()

	if len(hooks) > 0 {
		var wg sync.WaitGroup
		for _, hook := range hooks {
			wg.Add(1)
			go func(hook plugin.EventHook) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
				defer cancel()

				if err := hook.Handle(ctx, event); err != nil {
					h.logger.Error("event hook failed", "eventType", event.Type, "error", err)
				}
			}(hook)        // <-- no recover()
		}
		wg.Wait()
	}

	// 2. Dispatch to trigger rules (event → connector bindings).
	if h.triggers != nil {
		go h.triggers.Dispatch(event)   // <-- also no recover()
	}

	// 3. Broadcast to SSE event stream subscribers.
	h.broadcastEvent(event)
}
```

Same pattern at `internal/plugin/triggers.go:L44-82`:

```go
func (td *TriggerDispatcher) Dispatch(event pluginsdk.Event) {
    // ... synchronous rule matching ...
    for _, rule := range rules {
        // ...
        go td.sendWithRetry(connector, payload, rule.ID)   // <-- no recover in sendWithRetry either
    }
}
```

And `sendWithRetry` at `triggers.go:L150-185` calls `connector.Send(ctx, payload)` without a recover wrapper. Connectors are plugin code; any panic propagates to the goroutine top and crashes the process.

The reviewer-context file specifically calls this out:

> `internal/plugin/events.go` — 35+ typed Emit helpers; any panic in an event hook currently propagates. Check recover() placement.

The plan's B.6 entry on event hooks:

> **Sharp edge: event hook unregistration.** Current `EventHook` interface has no PluginID accessor ... Add `PluginID() string` to the interface ... Then `UnloadPlugin` can iterate `h.eventHooks` and remove entries matching the plugin ID.

That's an unrelated concern. The panic-propagation issue is never mentioned, and B.6 is the natural place to put the fix (rewriting event dispatch to add plugin ID accessors means touching the dispatch loop anyway).

## Impact

Every plugin is one bad dereference away from crashing the host. This is the same severity of "normal input triggers a process crash" that the reviewer rubric classes as Critical for core code and High for plugin code (since plugin code is less directly exercised). For a beta release, it's very easy to imagine:

- A plugin event hook assumes `event.Data["session_id"]` is a string and type-asserts without the `, ok` form → panic
- A plugin calls `json.Unmarshal` on a nil payload → panic on nil deref
- A connector's `Send` panics because a user pastes an emoji into a webhook URL field

The host dies and every chat session, worker, sandbox subprocess, and MCP connection dies with it. For developer friends, that's a reboot-your-tooling event.

Note also that Track H migrates oembed, which uses the event hook path (per plan §H.1: "oembed uses the event hook path (`EventHandler`) with URL detection in `message.sent` events"). If oembed panics on a weird URL, the host goes down.

## Recommendation

Add a short subsection to Track B (new §B.14 or append to B.6):

**B.X — Panic recovery at event dispatch boundaries.**

1. Wrap the event hook invocation in EmitEvent with a recover block:

```go
go func(hook plugin.EventHook) {
    defer wg.Done()
    defer func() {
        if r := recover(); r != nil {
            h.logger.Error("event hook panic", "eventType", event.Type,
                "hookType", fmt.Sprintf("%T", hook), "panic", r,
                "stack", string(debug.Stack()))
        }
    }()
    ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
    defer cancel()
    if err := hook.Handle(ctx, event); err != nil {
        h.logger.Error("event hook failed", "eventType", event.Type, "error", err)
    }
}(hook)
```

2. Same pattern in `TriggerDispatcher.sendWithRetry` around the `connector.Send` call.

3. Same pattern in `TriggerDispatcher.Dispatch` around the `matchFilter` / `renderPayload` calls — those run synchronously in the caller's goroutine so a panic there also propagates.

4. Add a gate to Track B.13: "Panic in a test event hook produces a log line and does not crash the process."

This is a cheap fix (maybe 30 lines) with enormous blast-radius reduction. It must land in beta.

## Alternative: panic-to-unload escalation

A repeated panic in the same plugin should probably unload that plugin to prevent a crash loop. The SDK can track panic count per plugin and auto-disable on N consecutive panics in a short window. Nice-to-have, not required for beta.

## References

- `internal/plugin/host.go:L1080-1106` — EmitEvent without recover
- `internal/plugin/triggers.go:L44-82, L150-185` — Dispatch and sendWithRetry without recover
- Reviewer-context `reviewer-backend.md` §1 — "any panic in an event hook currently propagates. Check recover() placement."
- Related: subprocess event hook proxy at `plugin.go:L270-298` — its Call/Notify invocations can also panic in the JSON marshaling path; the fix at the host-side dispatcher covers this because `subprocessEventHook.Handle` is invoked from inside the goroutine being recovered.
