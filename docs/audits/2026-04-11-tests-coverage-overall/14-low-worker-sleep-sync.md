# [Low] Worker manager tests use time.Sleep for synchronization

**Scope:** internal/worker/
**Topic:** Test Quality — flaky test patterns
**Date:** 2026-04-11

## Problem

`internal/worker/manager_test.go` uses `time.Sleep` for goroutine synchronization in 4 test functions. While the tests pass consistently today (0 failures in the tooling sweep), sleep-based synchronization is inherently racy under CI load.

## Evidence

Sleep-based synchronization in `internal/worker/manager_test.go`:

- `TestCancelWorker:L168` — `time.Sleep(50 * time.Millisecond)` to wait for worker to appear in List()
- `TestListAndActiveCount:L228` — `time.Sleep(50 * time.Millisecond)` to wait for workers to start
- `TestShutdown:L253` — `time.Sleep(50 * time.Millisecond)` to wait for worker to start
- `TestListConcurrentFieldAccess:L275` — uses `stubDelegator{delay: 20 * time.Millisecond}` as the synchronization mechanism (this one is acceptable — the delay is in the mock, not a bare sleep)

The deep-review skill rubric states: "Deterministic tests — no time.Sleep(...) as synchronization; use fakes/mocks for time."

## Impact

Under heavy CI load or slow machines, 50ms sleeps may not be sufficient for a goroutine to register in the sync.Map. The probability is low (no flakes observed), but the pattern is a maintenance risk. If more tests are added following this pattern, the cumulative flake risk increases.

## Recommendation

Replace `time.Sleep(50 * time.Millisecond)` with polling loops:
```go
// Wait for at least 1 worker to appear (up to 2s).
deadline := time.Now().Add(2 * time.Second)
for time.Now().Before(deadline) {
    if len(mgr.List()) > 0 {
        break
    }
    time.Sleep(5 * time.Millisecond)
}
```

Or add a `Manager.WaitForWorker()` method that blocks on a channel until a worker is registered, then use it in tests.

## References

- `internal/worker/manager_test.go:L168,228,253` — sleep-based sync
- Deep-review skill: "Deterministic tests — no time.Sleep(...) as synchronization"
