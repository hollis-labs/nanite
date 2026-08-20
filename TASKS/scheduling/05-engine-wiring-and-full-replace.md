# Engine wiring — full replace of the 2-minute ticker and `wakeScheduleDue`

**Phase:** 1 — Core engine (`TASKS/scheduling`)
**Status:** not-started
**Depends on:** `02-store-adapter.md`, `04-retry-backoff-on-fail-policy.md` (the Engine needs a complete Store + retry-wrapped Runner to start for real).
**Touches:** `cmd/nanite/main.go` (removes the `"durable-agent-wake-tick"` goroutine, `main.go:1065-1094`; adds `Engine.Start()`/`Stop()` lifecycle wiring, following `cmd/hadrond/main.go`'s pattern of starting/stopping the engine alongside the rest of the daemon), `internal/service/durable_wake.go` (`wakeScheduleDue`, `internal/service/durable_wake.go:435`, and the 15-minute-lookback logic at `:311-314,451-461` — removed, not deprecated-in-place).

## Context

`docs/engineering/architecture/12-scheduling.md`'s "Full replace, not dual-run" section is the design, and its framing is explicit: `go-scheduler`'s `Engine` **replaces** the existing mechanism in full — it does not run alongside it. Running both would mean two independent pollers racing the same `agent_schedules` rows with two different correctness models (one CAS-based, one lookback-window-based); there's no scenario where that's safer than one engine with one claim mechanism. This is the single highest-blast-radius task in this batch — it deletes the one live, production-exercised scheduling path Nanite has today and replaces it with a new one in the same change. Treat it accordingly: real live-verification before considering it done, not just green tests.

Confirmed directly against this checkout (not assumed) before writing this task:
- The ticker: `cmd/nanite/main.go:1070-1094`, `lc.Go("durable-agent-wake-tick", ...)` — a 2-minute `time.Ticker` calling `container.DurableWake.RunDue(ctx, service.DurableAgentWakeRunRequest{Now: time.Now()})`.
- `wakeScheduleDue` (`internal/service/durable_wake.go:435`) is the per-schedule due-check `RunDue` calls; the 15-minute lookback window (`:311-314,451-461`) is the fragility this replacement retires as a side effect, not a separately-patched bug.

## What to do

1. Construct the `go-scheduler.Engine` in `cmd/nanite/main.go`, wired to `02`'s Store adapter and `04`'s retry-wrapped Runner, following `cmd/hadrond/main.go`'s exact lifecycle pattern (construct once at boot, `Start()` alongside the rest of the daemon's background goroutines, `Stop()` on shutdown — check `cmd/hadrond/main.go` for where in its own boot sequence this happens and mirror the equivalent point in `main.go`).
2. Remove the `"durable-agent-wake-tick"` goroutine (`main.go:1065-1094`) entirely — not commented out, not feature-flagged, deleted. If any other code depends on `DurableAgentWakeService.RunDue` being called on a timer specifically (as opposed to the underlying wake-prompt dispatch logic `03`'s Runner now calls directly per-schedule), find it and update it — grep for other callers of `RunDue` before assuming this is the only one.
3. Remove `wakeScheduleDue` and the 15-minute lookback logic (`internal/service/durable_wake.go:311-314,451-461`) — confirm via grep that nothing else calls `wakeScheduleDue` directly before deleting it (it's described as `RunDue`'s own internal due-check, but verify).
4. If `RunDue` itself becomes dead code once its only caller (the deleted ticker) is gone and its due-check logic (`wakeScheduleDue`) is removed, remove it too — but confirm first whether anything else (an HTTP endpoint, a test harness, an operator manual-trigger path) still calls it; the design doc's own review mentions "an external caller hitting POST /api/durable-agent-wake/run-due" in a comment near the ticker (`main.go:1065`) — check whether that endpoint is real and still needed as a manual-trigger surface even after the ticker is gone, or whether it should be removed/repointed at the new Engine too.
5. Confirm `Engine.Status()` (`libs/go-scheduler/engine.go:25-30` — `Running`, `LastTickAt`, `Dispatches`, `WorkerErrors`) is reachable from wherever `09-operator-http-api.md` will need it, even though this task doesn't build the API endpoint itself.

## Done means

- The old ticker and `wakeScheduleDue`/lookback logic are gone from the codebase, not dormant.
- The new `Engine` starts and stops cleanly with the rest of the daemon's lifecycle.
- **Live dogfeed, not just green tests** (per `EXECUTION-PROCESS.md`'s validation-checkpoint convention, and per this task's own elevated risk): boot a scratch instance with a real `cron`-kind `agent_schedules` row due in the near future, confirm the new Engine actually fires it (a real wake prompt delivered, or whatever the job type's real effect is) within a reasonable window of its `next_run` — not just that the Store/Runner adapters pass unit tests in isolation. Also confirm a `one_shot` schedule fires exactly once and is disabled afterward (per `go-scheduler`'s own `oneTimeHorizon`/`DisableSchedule`-after-dispatch behavior, `libs/go-scheduler/engine.go:174-176`).
- Confirm via the same live dogfeed that restarting the scratch instance mid-cycle doesn't double-fire a schedule that was already claimed (the CAS mechanism `02` built is what should prevent this — this task is where that guarantee gets its first real, live exercise, not just a unit test of the claim SQL in isolation).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass, including every existing test that exercised the old ticker/`wakeScheduleDue` path (either updated to exercise the new path instead, or removed if it was testing now-deleted code — document which per removed test).

## Work log

Not started.

## Review notes

<!-- Reviewer fills in. -->
