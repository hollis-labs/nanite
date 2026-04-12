# [Medium] RegisterEventHook error ignored in RegisterAutoTriggerHandler

**Scope:** internal/plugin/auto_triggers.go
**Topic:** Error Handling
**Date:** 2026-04-11

## Problem

`RegisterAutoTriggerHandler` calls `host.RegisterEventHook(eventTypes, handler)` on line 39 but discards the returned error. If registration fails, the auto-trigger handler silently does not fire for any events.

## Evidence

```go
// internal/plugin/auto_triggers.go:39
host.RegisterEventHook(eventTypes, handler)
```

`Host.RegisterEventHook` returns `error` (see `internal/plugin/host.go:250`):

```go
func (h *Host) RegisterEventHook(eventTypes []string, hook plugin.EventHook) error {
```

The return value is never checked.

This was also flagged independently by the `whole-repo-tooling-and-tests-sweep` audit's golangci-lint findings (`docs/audits/2026-04-11-whole-repo-tooling-and-tests-sweep/05-golangci-lint-findings.md:117`).

## Impact

If `RegisterEventHook` ever fails (e.g., due to a future validation check or host state error), all custom action auto-triggers silently stop working. No log entry, no error propagated. The system appears healthy but auto-triggers never fire.

In practice, the current `RegisterEventHook` implementation always returns `nil`, so this is not exploitable today. But the contract says it can fail, and ignoring the error violates the project's established error-handling convention.

## Recommendation

Check the error and log or return it:

```go
if err := host.RegisterEventHook(eventTypes, handler); err != nil {
    host.logger.Error("failed to register auto-trigger event hook", "error", err)
    return // or return the error if the function signature is changed
}
```

Consider changing `RegisterAutoTriggerHandler` to return `error` so callers in `main.go:253` can handle it.

## References

- `internal/plugin/host.go:250` — `RegisterEventHook` signature
- `cmd/nanite/main.go:253` — call site
- `docs/audits/2026-04-11-whole-repo-tooling-and-tests-sweep/05-golangci-lint-findings.md:117` — same finding from lint sweep
