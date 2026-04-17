# [Critical] Shutdown deadlock in shared Registry

**Scope:** `framework/libs/go-plugin/registry.go`
**Topic:** Registry correctness
**Date:** 2026-04-11

## Problem

`Registry.Shutdown()` acquires `r.mu.Lock()` and then calls `r.UnloadPlugin(id)` in a loop. `UnloadPlugin()` also calls `r.mu.Lock()`, causing an immediate deadlock on the non-reentrant `sync.Mutex`.

## Evidence

`framework/libs/go-plugin/registry.go:L287-306`:

```go
func (r *Registry) Shutdown() error {
	r.mu.Lock()          // <-- acquires lock
	defer r.mu.Unlock()

	var unloadErrors []error
	for id := range r.plugins {
		if err := r.UnloadPlugin(id); err != nil {  // <-- calls Lock() again
			unloadErrors = append(unloadErrors, err)
		}
	}
	// ...
}
```

`framework/libs/go-plugin/registry.go:L93-95`:

```go
func (r *Registry) UnloadPlugin(id string) error {
	r.mu.Lock()          // <-- deadlock: already held by Shutdown()
	defer r.mu.Unlock()
```

## Impact

Any code path that calls `Registry.Shutdown()` will deadlock permanently. This includes:

- Test code that creates a `Registry` via `NewRegistry()` and calls `Shutdown()` in cleanup.
- Any lightweight host implementation that uses the shared `Registry` as its plugin container.

The nanite `Host` has its own shutdown implementation in `internal/plugin/host.go` and does NOT delegate to this `Registry`, so nanite production code is not affected. However, if any test or tooling uses the SDK's `Registry.Shutdown()`, it hangs silently.

Additionally, `Shutdown()` iterates over `r.plugins` while modifying it (via `delete` in `UnloadPlugin`), which would cause undefined map iteration behavior in Go even if the lock were not deadlocking.

## Recommendation

Extract an `unloadPluginLocked()` helper that assumes the lock is already held, and call it from both `UnloadPlugin()` and `Shutdown()`:

```go
func (r *Registry) unloadPluginLocked(id string) error {
	plugin, exists := r.plugins[id]
	if !exists {
		return fmt.Errorf("plugin with ID %s is not loaded", id)
	}
	// dependency check ...
	if err := plugin.Unload(); err != nil { ... }
	delete(r.plugins, id)
	return nil
}

func (r *Registry) Shutdown() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Collect IDs first to avoid map mutation during iteration.
	ids := make([]string, 0, len(r.plugins))
	for id := range r.plugins {
		ids = append(ids, id)
	}
	var errs []error
	for _, id := range ids {
		if err := r.unloadPluginLocked(id); err != nil {
			errs = append(errs, err)
		}
	}
	r.cancel()
	// ...
}
```

## References

- Go spec: mutexes are not reentrant. `sync.Mutex.Lock()` on an already-held mutex blocks forever.
- Related: nanite's `Host.Shutdown()` deadlock risk is tracked in `reviewer-backend.md` (separate code path, same pattern class).
- `framework/libs/go-plugin/registry.go:L92-121` (UnloadPlugin) and `L287-306` (Shutdown).
