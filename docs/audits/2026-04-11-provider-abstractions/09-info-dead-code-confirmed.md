# [Info] Dead code confirmation: scope_guard.go and event_pipeline.go

**Scope:** Dead code confirmation
**Topic:** Antipatterns
**Date:** 2026-04-11

## Problem

`scope_guard.go` and `event_pipeline.go` are confirmed dead code — no non-test callers exist. This was previously flagged by the `2026-04-10-chat-engine` audit and the `2026-04-11-whole-repo-tooling-and-tests-sweep`.

## Evidence

Grep for non-test callers:

```
rg 'ScopeGuard|NewScopeGuard' pkg/provider/ -g '!*_test.go'
# Only hits within scope_guard.go itself and event_pipeline.go (which uses ScopeGuard internally)

rg 'EventReactionPipeline|NewEventReactionPipeline' pkg/provider/ -g '!*_test.go'
# Only hits within event_pipeline.go itself

rg 'ScopeGuard|NewScopeGuard|EventReactionPipeline|NewEventReactionPipeline' --type go -g '!pkg/provider/' ~/Projects-apps/nanite/
# No matches outside the provider package
```

Both files have test coverage (`event_pipeline_test.go` tests `EventReactionPipeline`, `ScopeGuard`), but no production code path instantiates or uses them.

Additionally, the `ProgressTracker` (`progress_tracker.go`) and `CostMonitor` (`cost_monitor.go`) are only used by `EventReactionPipeline`. If the pipeline is dead, these are also dead.

The `panic-recovery-sweep` noted: "pkg/provider/event_pipeline.go:145 and :239 goroutines are in dead code per chat-engine audit." This review confirms no regression — the pipeline has not been wired since the prior audit.

## Impact

None (dead code). ~700 lines across 4 files (`scope_guard.go`, `event_pipeline.go`, `progress_tracker.go`, `cost_monitor.go`) that compile but never execute.

## Recommendation

No immediate action needed. If these are intended for future use, they should be behind a build tag or in a separate module. If they're abandoned, they should be removed to reduce maintenance surface and avoid misleading future reviewers into thinking they provide active protection.

## References

- `2026-04-10-chat-engine` audit — original dead code finding.
- `2026-04-11-panic-recovery-sweep` — confirmed dead in goroutine map.
- `2026-04-11-whole-repo-tooling-and-tests-sweep` — flagged unused functions.
