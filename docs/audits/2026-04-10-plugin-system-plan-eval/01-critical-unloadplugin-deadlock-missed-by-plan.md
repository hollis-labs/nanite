# [Critical] Plan fixes `Host.Shutdown()` deadlock but leaves identical deadlock in `Host.UnloadPlugin()`

**Scope:** plugin system — host lifecycle
**Topic:** plan-completeness / concurrency
**Date:** 2026-04-10

## Problem

The plan's Track A.2 fixes the `Host.Shutdown()` mutex-across-`p.Unload()` deadlock. The same pattern exists in `Host.UnloadPlugin()` and the plan never addresses it. Track G (hot install/uninstall) executes uninstall via `UnloadPlugin`, so the entire "install/uninstall flow" the plan is built around runs straight into this deadlock the first time a subprocess plugin's Unload re-enters the host.

## Evidence

`internal/plugin/host.go:L1010-1076`:

```go
func (h *Host) UnloadPlugin(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	p, exists := h.plugins[id]
	// ...
	if err := p.Unload(); err != nil {               // <-- holds h.mu across plugin call
		return fmt.Errorf("failed to unload plugin %q: %w", id, err)
	}

	delete(h.plugins, id)

	// Clean up plugin-owned registrations: filters, event hooks, UI components, ...
	if removed := h.filters.RemoveByPlugin(id); removed > 0 { ... }
	for eventType, hooks := range h.eventHooks { ... }  // still under h.mu.Lock
	// ...
}
```

Compare to the plan's Track A.2 fix for `Shutdown()`:

> **Fix `Host.Shutdown()` deadlock trap.** `internal/plugin/host.go:1170` holds `h.mu` across the entire `p.Unload()` loop. Release the lock across `Unload()` calls the same way `LoadPlugin` does at `host.go:896`.

`LoadPlugin` (the "correct" pattern per the plan) deliberately drops the lock across `p.Load(h)`:

```go
// internal/plugin/host.go:918
// Load the plugin WITHOUT holding the lock — Load calls back into
// RegisterCRUDHandler / RegisterUIComponent / etc. which acquire h.mu.
if err := p.Load(h); err != nil {
    return fmt.Errorf("failed to load plugin %q: %w", id, err)
}
```

`UnloadPlugin` does the opposite: it holds `h.mu.Lock()` (write lock) for the entire function body including `p.Unload()` and post-unload cleanup. Any plugin whose `Unload()` re-enters `RegisterCRUDHandler`, `RegisterEventHook`, `RegisterUIComponent`, `GetPlugin`, or any other host method deadlocks. Subprocess plugins' `Unload()` calls `sp.mgr.Stop()`, which sends `plugin/unload` RPC and waits on `waitCh` — that is unlikely to reenter the host in the current codebase, but Track H's real plugins (support-ticket with CRUD handlers, oembed with event hooks, core plugins with HTTP routes) all have Unload paths that want to clean up registrations.

Worse, when Track B.6's `Unregister*` paths land (as the plan mandates), they will need to acquire `h.mu`. If `Unload()` on the plugin side calls them, deadlock is guaranteed — not a corner case.

## Impact

Triggered on the first real hot-uninstall of a plugin that either:
- Calls any `host.*` method from its `Unload()` implementation, OR
- Calls any of the new `Unregister*` methods the plan mandates in B.6 (which all take `h.mu`).

Symptom: `nanite plugin uninstall <id>` hangs forever. Ctrl-C won't help because the stuck goroutine holds `h.mu`; all other plugin RPCs stall behind it. User sees a frozen CLI and has to `kill -9` the nanite process — which is exactly the "hot install/uninstall without process restart" capability the plan is built around.

This is a plan-completeness gap that undermines Track G's stated goal. It is also a plan-accuracy gap: the plan's diagnosis of the Shutdown deadlock is correct, but it treats it as a single-site fix ("release the lock across Unload calls the same way LoadPlugin does") when the same pattern exists and must be fixed at a second site.

## Recommendation

Track A.2 should be amended to fix **both** `Shutdown()` and `UnloadPlugin()` with the same pattern: perform pre-checks under lock, snapshot the plugin reference, drop the lock, call `p.Unload()`, re-acquire the lock for the bookkeeping cleanup. Sketch:

```go
func (h *Host) UnloadPlugin(id string) error {
    h.mu.Lock()
    p, exists := h.plugins[id]
    if !exists {
        h.mu.Unlock()
        return fmt.Errorf("plugin %q not found", id)
    }
    // dependency check under lock
    for _, other := range h.plugins {
        if other.ID() == id { continue }
        for _, dep := range other.Dependencies() {
            if dep == id {
                h.mu.Unlock()
                return fmt.Errorf("cannot unload plugin %q: %q depends on it", id, other.ID())
            }
        }
    }
    h.mu.Unlock()

    // Call Unload WITHOUT holding the lock — mirrors LoadPlugin.
    if err := p.Unload(); err != nil {
        return fmt.Errorf("failed to unload plugin %q: %w", id, err)
    }

    // Re-acquire for bookkeeping.
    h.mu.Lock()
    delete(h.plugins, id)
    // ... filters/hooks/UI/keybinding cleanup ...
    h.mu.Unlock()

    h.logger.Info("unloaded plugin", "id", id)
    go h.EmitPluginUninstalled(id)
    return nil
}
```

Also add a gate item to Track A.5: "UnloadPlugin no longer holds h.mu across p.Unload()."

## References

- `internal/plugin/host.go:L1010-1076` — current deadlocked `UnloadPlugin`
- `internal/plugin/host.go:L918-922` — correct LoadPlugin pattern
- `internal/plugin/host.go:L1170-1189` — the `Shutdown` case the plan already fixes
- Plan Track A.2 (`docs/architecture/plugin-execution-plan-2026-04-10.md:156-158`)
- Plan Track B.6 (Unregister paths that will trigger this at runtime)
- Reviewer-context `reviewer-backend.md` — "Flag as Critical if this lands on a real re-entry path." Track G makes it a real re-entry path.
