# [Low] HadronBlueprintGate is defined but not wired into the broker

**Scope:** contextbroker
**Topic:** Antipatterns / Dead Code
**Date:** 2026-04-11

## Problem

`gate_hadron_blueprints.go` defines a `ContextGate` interface and a `HadronBlueprintGate` implementation (with tests), but neither the `ContextGate` interface nor the `HadronBlueprintGate` type is referenced from the broker wiring in `container.go`. The broker does not call `SessionStart` or use gates at all. The `ContextGate` interface is defined but never consumed by `Broker.Fetch`.

## Evidence

```go
// internal/contextbroker/gate_hadron_blueprints.go:L17-L28
type ContextGate interface {
    ContextSource

    SessionStart(ctx context.Context, intent Intent) (bool, error)
    IsCached() bool
    ClearCache()
}
```

Grep for `ContextGate` outside the contextbroker package returns no results. Grep for `HadronBlueprintGate` outside the package returns no results. The gate is never added as a source in `container.go`.

## Impact

Dead code. No runtime impact. The tests pass because they test the gate in isolation.

## Recommendation

Either wire the gate into the broker (add it as a source in `container.go` and add gate lifecycle to `Broker.Fetch`), or remove the file and its tests to reduce maintenance surface. If the intent is to wire it later, add a TODO with a tracking reference.

## References

- `internal/contextbroker/gate_hadron_blueprints.go` — full file
- `internal/contextbroker/gate_hadron_blueprints_test.go` — tests for unwired code
- `internal/service/container.go:L308-L344` — broker wiring (no gate)
