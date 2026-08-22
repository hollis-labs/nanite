# Fix Container constructor partial-failure cleanup (reaper goroutines leak on NewContainer error paths)

**Phase:** Wave 2 — Correctness, lifecycle, concurrency (audit-remediation batch, sequenced 2026-08-21 — see the sequencing block below)
**Status:** not-started
**Depends on:** none — self-contained within `internal/service/container.go`'s `NewContainer`.
**Touches:** `internal/service/container.go` (`NewContainer` only; no other symbol).
**Requires architect decision:** false. Note: `findings.json`'s raw entry for `GO-LIFE-001` carries `requires_architect_decision: true`, but its own `recommendation` text is a concrete mechanical direction ("match the existing `stopCatalog()` cleanup pattern already present at both flagged sites"), not an open design question — this task file sets the flag to `false` per this batch's own stated convention (README.md: "flags `requires_architect_decision: true` wherever the underlying finding's recommendation was 'architect decision' in the audit"). Flagging this explicitly in case the `true` value in `findings.json` is a data-entry inconsistency rather than deliberate signal; a planner should treat `false` as this task's working assumption but can override.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 2 — correctness, lifecycle, concurrency · **Dispatch unit:** `W2a`
> - **Depends on:** `00/01`
> - **Blocks:** `07/04`, `09/01`, `09/02` — all three edit `internal/service/container.go` after this
> - **Parallel-safe with:** `04/02`, `04/04`, `04/05`, `05/01`
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

This task implements **`GO-LIFE-001`** (medium severity, high confidence — audit report `REPORT.md` §3, `docs/audits/2026-08-21-go-quality/REPORT.md:160-212`).

### Findings addressed
- `GO-LIFE-001` — Subagent/runtime reaper goroutines leak on container construction failure.

### Root cause
`NewContainer` starts two long-lived background goroutines (the subagent reaper and the `agent_runtime` reaper) well before it finishes assembling the `*Container` struct that would let a caller stop them. Between goroutine-start and struct-assembly there are error-return paths that bail out with `(nil, err)` — on those paths, nothing in the process retains a handle to the two `context.CancelFunc`s that would stop the goroutines, because those funcs are only written into the struct at the very end of the constructor. This is a general "resource started before its owner exists" lifecycle bug, not specific to these two reapers — the fix should establish the general invariant, not just patch the two currently-flagged call sites.

### Current behavior (verified against current `HEAD`, not just the audited commit)
- `reaperCtx, stopReaper := context.WithCancel(context.Background())` at `internal/service/container.go:1162` immediately starts `subagentReaper.Start(reaperCtx)` — a background goroutine polling `subagent_runs`.
- `runtimeReaperCtx, stopRuntimeReaper := context.WithCancel(context.Background())` at `internal/service/container.go:1182` immediately starts `runtimeReaper.Start(runtimeReaperCtx)` — a background goroutine polling `agent_runtime`.
- Both cancel funcs are captured into the `Container` struct only near the end of the constructor, at `internal/service/container.go:1398` (`stopSubagentReaper: stopReaper`) and `internal/service/container.go:1400` (`stopRuntimeReaper: stopRuntimeReaper`).
- Two confirmed early-return paths execute between reaper start and struct assembly without calling either cancel func (verified against current source, line numbers match the audit's citations exactly):
  - `internal/service/container.go:1241` — `if err != nil { stopCatalog(); return nil, fmt.Errorf("service container: durable agent recipes: %w", err) }`, guarding `NewDurableAgentRecipeService(...)` at line 1239.
  - `internal/service/container.go:1245` — `if err := SyncManagedDurableAgentConfigs(cfg.Store, managedConfigRoot); err != nil { stopCatalog(); return nil, fmt.Errorf("service container: sync managed durable agents: %w", err) }`.
  - Both branches already call `stopCatalog()` to unwind an earlier-started resource (the model catalog) — they simply don't extend that same cleanup discipline to the two reapers started afterward.
- `go vet ./...` independently flags both cancel funcs: `internal/service/container.go:1162:2: the stopReaper function is not used on all paths (possible context leak)` and `internal/service/container.go:1182:2: the stopRuntimeReaper function is not used on all paths (possible context leak)`.
- On either error path, `NewContainer` returns `(nil, err)`. The caller (only one exists in production — see below) never receives a `*Container`, so it has no reference to call `Shutdown`. The two reaper goroutines keep running and keep querying SQLite indefinitely — nothing in the process can stop them short of process exit. If container construction is ever retried in a loop, each failed attempt leaks one more pair of goroutines and DB polling loops.

## What to do

### Desired invariant
Every resource `NewContainer` starts has an owner from the moment it starts, on every code path — including every error-return path between that resource's start and the point where the constructor commits to returning a live `*Container`. No error return from `NewContainer` may leave a started goroutine, ticker, or other background resource without a call that stops it.

### Scope
`internal/service/container.go`, `NewContainer` only (lines ~1160-1410 in current `HEAD`; verify exact bounds before editing — this file has had unrelated churn since the audit's `8feeee5c` baseline, confirm current line numbers via `grep -n` rather than trusting this task file's citations blindly). No other function or file should need to change.

### All production callers
Only one production caller of `NewContainer` exists: `cmd/nanite/main.go:372` (confirmed current, `container, err := service.NewContainer(service.ContainerConfig{...})`). `cmd/nanite/main.go` treats a `NewContainer` error as fatal at startup — it does not retry construction in a loop today, so in production this leak is currently latent (the process exits shortly after a construction failure) rather than actively accumulating. Do not use this as a reason to skip the fix — the audit's own false-positive-considerations note treats the leak as "real but inconsequential in practice" only under that specific caller behavior, and the fix is cheap and mechanical regardless. `internal/api`'s test suites also call `NewContainer` directly (see sibling task `02-fix-api-test-container-shutdown-leak.md` in this folder) — those call sites are a different problem (Container is constructed successfully and never torn down) and are explicitly out of scope here; this task only concerns error paths *inside* the constructor.

### Proposed direction
Match the existing `stopCatalog()` cleanup pattern already present at both flagged sites. Two viable mechanical approaches — pick whichever reads cleaner in context, this is not an architect-level decision:
1. **Explicit cleanup on each error branch** (matches current style exactly): add `stopReaper()` / `stopRuntimeReaper()` calls alongside the existing `stopCatalog()` call at each of the two flagged error returns (and any other error return between line 1182 and line 1398/1400 — re-scan for any the audit didn't enumerate, since this constructor may have grown/shrunk since the audited commit).
2. **`defer`-with-committed-flag**: `defer` both cancel funcs immediately after starting each reaper, guarded by a `committed bool` that's only set `true` on the final successful `return`, so every error path (including any future one added later) is covered without needing another explicit call. This is more robust against a *future* error path being added without remembering to add matching cleanup — worth strongly considering given this constructor is long and has grown before.

### Non-goals
- Do not refactor `NewContainer`'s overall structure, extract helper functions, or change any other constructor behavior beyond adding the missing cleanup.
- Do not touch `stopCatalog()` itself, the reaper implementations (`subagent.NewReaper`, `orphansweep.NewRuntimeReaper`), or any other resource lifecycle in this constructor not named above.
- Do not attempt to fix `internal/api`'s test-shutdown gap (GO-TEST-001) or `internal/service`'s own `-race` timeout (GO-SVCCORE-006) here — those are separate task files in this folder for a reason (see this folder's `README.md`).

### Dependencies
None on other tasks in this batch.

### Tests required
- A unit/integration test that forces `NewContainer` to fail *after* the reaper-start point (line ~1182) — e.g. by injecting a failing `NewDurableAgentRecipeService` (via a bad `cfg.DurableAgentRecipeCatalogPaths` value, or a test seam if one needs to be added) — and asserts no reaper goroutine/DB-polling activity survives the failed call. Concrete assertion options: a goroutine-count diff (`runtime.NumGoroutine()` before/after, allowing for GC settling), or a hook into the reaper's tick interval to detect any post-return activity within a bounded wait window.
- Cover both flagged error paths (durable agent recipes failure at line 1241, sync managed durable agents failure at line 1245) if the chosen approach (option 1 above) doesn't naturally guarantee both from one test à la the `defer`-with-flag approach.
- Re-run `go vet ./internal/service` and confirm the two `possible context leak` warnings are gone.

### Prevention
`go vet`'s "possible context leak" check already exists and already caught this — the gap was in enforcement, not detection. Consider adding `go vet ./...` (or at least `./internal/service`) as a required, blocking pre-merge check if it isn't already one (verify current CI/pre-commit config before assuming this needs adding — it may already run and simply wasn't gating merges when this constructor grew past it). This maps to the remediation guide's "Lifecycle Ownership" standard (§4/Wave 7): "Every goroutine/background worker/resource has an explicit owner and shutdown path; partial construction cleans up already-started resources."

### Verification
- `go vet ./internal/service` — zero "possible context leak" findings.
- `go build ./...` — clean.
- `go test ./internal/service/...` — the new regression test passes, and the full package suite still passes.
- The regression test itself is the primary PASS signal: it must fail against the current (unfixed) code and pass after the fix, to prove it actually exercises the leak.

### Risk / rollback
Low risk — the change is additive cleanup code on already-identified error paths, touching no successful-path behavior. Rollback is a straightforward revert of the diff to `NewContainer`; no schema, API, or persisted-state impact.

### Done means
- [ ] Every error-return path in `NewContainer` between reaper-start and struct-assembly stops both the subagent reaper and the runtime reaper before returning.
- [ ] `go vet ./internal/service` reports zero "possible context leak" warnings for `stopReaper`/`stopRuntimeReaper`.
- [ ] A new regression test forces a post-reaper-start constructor failure and asserts no reaper activity survives; the test fails against the pre-fix code (verified by temporarily reverting the fix) and passes after it.
- [ ] `go build ./...` and `go test ./internal/service/...` remain clean.

## Work log

<!-- Worker fills this in as it goes. -->

## Review notes

<!-- Reviewer fills this in. -->
