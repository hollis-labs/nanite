# [Medium] EmitPreCompact / EmitPostCompact event hooks are dead code

**Scope:** context-management
**Topic:** Antipatterns / Dead code
**Date:** 2026-04-11

## Problem

The `EventEmitter` interface defines `EmitPreCompact` and `EmitPostCompact` methods. `CompositeEmitter` implements both, including plugin pre-hook dispatch (`context.pre_compact`) and activity logging. The post-compaction memory extraction hook is registered in `container.go:436-440`. None of these methods are ever called from any code path.

## Evidence

Interface definitions:

```go
// internal/service/events.go:28-29
EmitPreCompact(ctx context.Context, sessionID string, messageCount int, reason string)
EmitPostCompact(ctx context.Context, sessionID string, tokensSaved int, stagesApplied []string)
```

Call site search:

```
$ rg "\.EmitPreCompact\(|\.EmitPostCompact\(" --type go
internal/service/events_composite.go:117:  go c.activity.EmitPreCompact(...)
```

Only one hit: `CompositeEmitter.EmitPreCompact` calls `activity.EmitPreCompact` internally. But `CompositeEmitter.EmitPreCompact` itself is never called from any code path. No call to `EmitPostCompact` exists anywhere.

The post-compact memory extraction hook is registered:

```go
// internal/service/container.go:436-440
postCompactHook := extractor.PostCompactHook()
if err := cfg.Plugins.RegisterEventHook(postCompactHook.EventTypes(), postCompactHook); err != nil {
    log.Printf("service container: failed to register post-compact memory hook: %v", err)
}
```

This hook listens for `context.compacted` events that are never emitted.

## Impact

Plugins that register for `context.pre_compact` or `context.compacted` events will never receive them. The memory extraction system's post-compaction hook (which presumably extracts memories from compacted content before it's lost) is wired but inert. This means compaction — even the MVP handler — does not trigger memory extraction.

## Recommendation

When findings 01/02/04 are addressed and the `CompactionPipeline` is wired in, add `EmitPreCompact` before and `EmitPostCompact` after the pipeline run. Until then, these are harmless dead code but should be annotated with a `// TODO: wire when CompactionPipeline is integrated` comment to prevent confusion.

## References

- `internal/service/events.go:L28-L29` — interface definitions
- `internal/service/events_composite.go:L115-L134` — implementations (never called)
- `internal/service/container.go:L436-L440` — post-compact hook registration (hook never fires)
- `internal/chat/activity.go:L212-L224` — activity emitter (never called)
