# Low-risk error-handling gaps: silently discarded/unlogged errors across 5 unrelated files

**Phase:** Wave 8 — Mechanical cleanup (audit-remediation batch, sequenced 2026-08-21 — see the sequencing block below)
**Status:** not-started
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

<!-- Worker fills this in as it goes, including the GO-INFRA-003 intent-confirmation finding and the GO-SEC4-010 logging-vs-signature choice. -->

## Review notes

<!-- Reviewer fills this in: pass/fail per row, what was independently re-verified. -->
