# Low-risk error-handling gaps: silently discarded/unlogged errors across 5 unrelated files

**Phase:** Wave 8 — Mechanical cleanup (audit-remediation batch, sequenced 2026-08-21 — see the sequencing block below)
**Status:** reviewed
**Depends on:** none within this batch.
**Touches:** `internal/task/snapshot.go`, `internal/api/bookmarks.go`, `internal/secrets/keyring.go`, `internal/service/durable_wake.go`, `internal/mcpconfig/mcpconfig.go`.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 8 — mechanical cleanup · **Dispatch unit:** `W8`
> - **Depends on:** Wave 7 complete
> - **Blocks:** none
> - **Parallel-safe with:** `13/01`, `13/05`
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

The 5 findings in this task don't share a file or a package, but they share a pattern: an error is either silently discarded or handled without logging, in a way that would hide a real failure from an operator if it ever fired. None of these requires a design decision — each is either "add a log line" or "use `errors.Is` instead of a direct comparison," with the fix's shape fully determined by the surrounding code. `requires_architect_decision: false` for all five. Root cause / all-production-callers analysis is n/a for most of this batch (mechanical, single-site fixes); `GO-SVCEXEC-006` is the one exception — see its row below.

One item in this batch deserves priority over the other four: `GO-SVCEXEC-006`'s silently-swallowed write isn't just an observability gap like the rest — the file's own doc comment frames that specific write as the mechanism preventing a documented double-fire race with an external scheduler. If it fails silently, the race-prevention guarantee the comment claims is undermined, not just unlogged. Treat it as the first item worked in this batch, and don't downgrade it to "same as the others" during implementation.

## What to do

| Finding | File(s) | What |
|---|---|---|
| `GO-SVCEXEC-006` (low, **priority within this batch**) | `internal/service/durable_wake.go:232-233` (`RunDue`) | A best-effort store write marking a one-shot schedule `expired` is silently swallowed on failure. This write is the actual mechanism the file's own doc comment says prevents a documented double-fire race with the external scheduler's tick — silently discarding its failure defeats that guarantee without any signal. Add a log line (at minimum `slog.Warn`, consistent with sibling failure paths in the same file) at this specific call site. Do not change the surrounding control flow — this is a "log, don't propagate" fix, matching how nearby best-effort writes in the same file are already handled (confirm this pattern before implementing, since some sibling writes in this file may already log and this one should match, not diverge).
| `GO-INFRA-003` (low) | `internal/task/snapshot.go:94-121` (`scanTask`) | `json.Unmarshal`/`time.Parse` errors on `metadata`/`created_at`/`updated_at` are silently discarded during durable-recovery `Restore` — a malformed row silently produces an empty `Metadata`/zero-value timestamp with zero observability. **Confirm intent before treating as a pure gap**: the audit found sibling `LocalBackend.List`/`Snapshot` already treat similar failures as skip-and-continue without logging, which may be a deliberate "best-effort restore" convention rather than an oversight. If the sibling convention is confirmed deliberate, match it exactly but add a log line at `slog.Warn` (or equivalent) with the task ID on parse failure — logging doesn't change the best-effort behavior, it just makes the silent case visible.
| `GO-API-010` (low) | `internal/api/bookmarks.go:90` | Uses `err != sql.ErrNoRows` instead of `errors.Is(err, sql.ErrNoRows)`. Would silently misclassify a legitimate not-found as an internal error if the underlying call is ever wrapped with `%w`. Straightforward swap to `errors.Is`.
| `GO-SEC4-010` (informational) | `internal/secrets/keyring.go` (`Get`) | Collapses "key not found" and "keychain access error" into the identical silent empty-string return, with no logging — unlike its sibling `Delete`, which does log on failure. Add logging on the error path (at debug or warn level, matching `Delete`'s existing convention), or — if callers genuinely need to distinguish the two cases — consider returning `(string, error)` instead. Prefer the logging-only fix unless a caller is already found to need the distinction; don't widen the function signature speculatively.
| `GO-MCPTOOL-013` (low) | `internal/mcpconfig/mcpconfig.go:145,150` (`Export`) | 2 `json.Unmarshal` errors on stored `Args`/`Env` are silently dropped — already part of the base report's tracked errcheck cluster, cited here with precise locations. Add `slog.Warn` (or equivalent) logging at both call sites on unmarshal failure.

## Done means

- [ ] `GO-SVCEXEC-006`: the silently-swallowed `expired`-marking write now logs on failure, consistent with sibling error handling in `durable_wake.go`.
- [ ] `GO-INFRA-003`: intent confirmed against `LocalBackend.List`/`Snapshot`'s sibling convention (recorded in Work Log); logging added at minimum regardless of which convention is confirmed.
- [ ] `GO-API-010`: `bookmarks.go:90` uses `errors.Is(err, sql.ErrNoRows)`.
- [ ] `GO-SEC4-010`: `keyring.Get`'s error path logs on failure (or, if chosen, returns `(string, error)` — recorded in Work Log which was chosen and why).
- [ ] `GO-MCPTOOL-013`: both `Export` unmarshal error paths log on failure.
- [ ] `go build ./...`, `go vet ./...`, and `go test ./internal/task/... ./internal/api/... ./internal/secrets/... ./internal/service/... ./internal/mcpconfig/...` pass.

## Work log

- 2026-08-24: Implemented the three findings still open on the reviewed
  `14/02` base. `GO-SVCEXEC-006` now warns when the post-wake one-shot expiry
  write fails, with `schedule_id`, `instance_id`, and the underlying error.
  The warning does not alter `RunDue` control flow: the successful wake result
  and fire-count update remain successful, while expiry stays best-effort. An
  injected store regression captures the warning and proves those postconditions.
- Confirmed the `GO-INFRA-003` intent against `LocalBackend.List` and
  `LocalBackend.Snapshot`: both deliberately skip malformed coordination-store
  JSON and continue, establishing best-effort recovery behavior. `scanTask`
  therefore still returns the row with the same initialized-empty metadata and
  zero-time defaults, but now warns separately for malformed `metadata`,
  `created_at`, `updated_at`, and `completed_at`, including task ID, field, and
  parse error. The regression injects all four malformed values and proves both
  the four warnings and preserved row/default behavior.
- Kept `secrets.Get`'s `func(string) string` signature because its callers use
  the documented empty-string absence contract and none needs a new error
  channel. A keyring not-found error now logs at debug; other keychain-access
  errors log at warn with the underlying error. Neither path logs the retrieved
  value. Mock-keyring tests cover not-found, an injected access failure, the
  unchanged empty-string results, and successful secret retrieval without
  secret-value logging.
- `GO-API-010` and `GO-MCPTOOL-013` required no new edits: the reviewed `14/02`
  work already changed bookmarks to `errors.Is` and added both MCP-config JSON
  warnings in `9147bf84`; the formal review-fix commit was `2d532314`. Those
  existing fixes were re-exercised by this task's focused API/MCP-config suites.
- Inspected the adjacent `BumpAgentScheduleFireCount` call. Its error remains
  silently discarded by the `err == nil` condition, which also suppresses the
  one-shot expiry attempt. That is a durable escalation candidate for a later
  scoped task, but it is outside `GO-SVCEXEC-006`'s exact expiry-status finding
  and was deliberately not changed or added to the shared escalation tracker.
- Regression mutation check: with only the three production fixes temporarily
  reverted, the new durable-wake, malformed-snapshot, and two keyring-error log
  tests all failed on absent diagnostics; restoring the fixes made the same
  command pass. Final focused ordinary tests passed for `internal/task`
  (0.624s), `internal/api` (9.913s), `internal/secrets` (0.154s),
  `internal/service` (23.336s), `internal/service/install` (1.301s), and
  `internal/mcpconfig` (0.999s). Focused race tests passed for the same package
  set (`internal/api` 108.408s, `internal/service` 55.226s).
- Full verification passed: `go build ./...`; `go vet ./...`;
  audit-config correctness lint (`errcheck`, `errorlint`, `nilerr`) with
  `0 issues`;
  `go test -count=1 ./...` (`internal/service` 29.681s,
  `internal/store` 16.397s); and `go test -race -count=1 ./...`
  (`internal/api` 286.281s, `internal/service` 124.701s,
  `internal/store` 276.905s). No review or approval is claimed; status is
  implemented pending fresh review.

## Review notes

- **PASS — fresh review of `f14db17d` against base `8b1e61bf` (2026-08-24).** The implementation diff is limited to the task record, three production error paths, and their regressions; no shared tracker or unrelated application file changed.
- `GO-SVCEXEC-006`: traced `RunDue` through successful `Wake`, fire-count persistence, and one-shot expiry. The only production control-flow change is inspecting the existing best-effort expiry write's error and emitting a warning with `schedule_id`, `instance_id`, and `err`; the error is not returned and does not alter the successful result. The injected-store regression independently proves the wake result remains successful, fire count reaches 1, status remains active after the injected expiry failure, and every required diagnostic attribute is present.
- `GO-INFRA-003`: confirmed `LocalBackend.List` and `LocalBackend.Snapshot` both implement skip-and-continue handling for malformed coordination-store JSON. `scanTask` retains its prior best-effort row/default semantics while warning independently for malformed `metadata`, `created_at`, `updated_at`, and `completed_at`, always with task ID, field, and parse error. The regression proves the row survives, metadata remains an initialized empty map, the required timestamps remain zero, and all four field diagnostics fire.
- `GO-SEC4-010`: confirmed `Get` retains its `func(string) string` signature and all production callers retain the empty-string absence contract. `errors.Is(err, keyring.ErrNotFound)` logs at debug; every other access failure logs at warn; success returns the value without logging it. Mock-keyring regressions cover both error classes and verify the stored secret value is absent from captured logs.
- `GO-API-010` and `GO-MCPTOOL-013`: commits `9147bf84` and review-fix `2d532314` are ancestors of the reviewed base, and this branch has no diff in `internal/api/bookmarks.go` or `internal/mcpconfig/mcpconfig.go`. Live source still uses `errors.Is` for bookmark not-found and logs both malformed MCP args/env decodes with server identity and underlying error. Focused API and MCP-config suites passed.
- The adjacent unchecked `BumpAgentScheduleFireCount` error is accurately recorded in the Work Log: it can suppress the expiry attempt, but is outside this finding's exact expiry-write scope. The implementation did not silently broaden into that follow-up.
- Independent verification passed: `git diff --check 8b1e61bf..f14db17d`; the three new focused ordinary regressions; focused API/MCP-config suites; focused race runs for the new service/task/secrets regressions; audit-config `errcheck,errorlint,nilerr` (`0 issues`); `go build ./...`; `go vet ./...`; and uncached `go test -count=1 ./...` (`internal/api` 10.888s, `internal/service` 24.394s, `internal/store` 11.181s).
