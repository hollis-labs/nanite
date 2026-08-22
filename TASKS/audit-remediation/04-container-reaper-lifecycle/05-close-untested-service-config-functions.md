# Add unit tests for buildRepairConfig and discoverManagedDurableAgentConfigs (both 0.0% covered)

**Phase:** Wave 2 — Correctness, lifecycle, concurrency (audit-remediation batch, sequenced 2026-08-21 — see the sequencing block below)
**Status:** not-started
**Depends on:** none — independently landable, pure test-debt closure.
**Touches:** New/expanded test files for `internal/service/tool_cache_wiring.go` (`buildRepairConfig`) and `internal/service/managed_durable_configs.go` (`discoverManagedDurableAgentConfigs`). No production code changes expected.
**Requires architect decision:** false — pure test-debt closure, no design ambiguity.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 2 — correctness, lifecycle, concurrency · **Dispatch unit:** `W2a`
> - **Depends on:** `00/01`
> - **Blocks:** none
> - **Parallel-safe with:** all of W2a — test-only
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

This task implements **`GO-SVCEXEC-005`** (medium severity, high confidence, category testing — `docs/audits/2026-08-21-go-quality/REPORT.md:813`, §8.4).

### Findings addressed
- `GO-SVCEXEC-005` — `buildRepairConfig` and `discoverManagedDurableAgentConfigs` are both 0.0% test-covered.

### Root cause
Not a defect in behavior — a testing gap. Both functions are pure/deterministic and standing out as untested against a package whose coverage is otherwise unevenly-but-not-uniformly thin: sibling functions in the same files sit at 77-95% coverage (per `raw/coverage-func.log`, cited by the audit), making these two genuine outliers rather than part of a broader package-wide coverage problem.

### Current behavior (verified against current `HEAD`)
- `buildRepairConfig` — confirmed current at `internal/service/tool_cache_wiring.go:113`: `func buildRepairConfig(reg *provider.Registry, s *store.Store, utilityProvider string) *RepairConfig`. Resolves the C2 auto-repair pipeline's provider/model/timeout configuration via a 4-level fallback chain (the audit's complexity-outlier verdict for this function, `REPORT.md:819`, judges it "essential" complexity — a coercion/fallback table, not accidental complexity worth refactoring — so this task is purely additive test coverage, not a refactor).
- `discoverManagedDurableAgentConfigs` — confirmed current at `internal/service/managed_durable_configs.go:311`: `func discoverManagedDurableAgentConfigs(configRoot string) ([]ManagedDurableAgentConfig, error)`. A boot-time config-directory loader. The audit's complexity-outlier verdict (`REPORT.md:819`) calls this "mild accidental — nesting-driven, low real complexity, untested" — meaning a *light* simplification pass could be worth considering while adding tests, but the primary deliverable is coverage, not a rewrite (see Non-goals).

## What to do

### Desired invariant
Both functions have meaningful unit-test coverage exercising their real branches — not just a single happy-path smoke test — bringing them in line with their sibling functions' 77-95% coverage band in the same files.

### Scope
- `internal/service/tool_cache_wiring.go` — read `buildRepairConfig`'s full body before writing tests; trace the "4-level fallback chain" the audit describes (verify what the 4 levels actually are by reading the function directly — this task file does not enumerate them since they weren't independently re-traced during authoring; the worker should read the function first and design test cases around its actual branch structure, not around this file's paraphrase).
- `internal/service/managed_durable_configs.go` — read `discoverManagedDurableAgentConfigs`'s full body, including how it walks `configRoot`, what file patterns/extensions it accepts or skips, and its error paths (malformed config file, missing directory, permission errors, etc.) before designing test cases.
- No other functions in either file are in scope.

### All production callers
Not applicable in the security/correctness-migration sense — this is a testing-only task adding coverage to existing, unchanged logic. Understanding callers is still useful context for realistic test fixtures: `buildRepairConfig` is called from wherever the C2 auto-repair pipeline resolves its provider/model/timeout (trace this before writing tests, to build fixtures resembling real `*provider.Registry`/`*store.Store` states rather than arbitrary ones); `discoverManagedDurableAgentConfigs` is called at boot time (trace its caller in `internal/service` to understand what `configRoot` values and directory layouts are realistic).

### Proposed direction
- For `buildRepairConfig`: table-driven tests covering each level of the fallback chain independently (e.g. explicit config present at level 1 short-circuits; level 1 absent falls to level 2; all levels absent falls to whatever the final default is) plus at least one test confirming the function is side-effect-free (pure) given identical inputs.
- For `discoverManagedDurableAgentConfigs`: tests using a temp directory (`t.TempDir()`) fixture covering: empty directory, one valid config, multiple valid configs, a malformed/invalid config file (confirm the error path — does it fail the whole discovery or skip-and-continue? read the code to find out, don't assume), and a missing/nonexistent `configRoot` (confirm whether this is treated as "no configs found" or an error).
- Both functions are "pure/deterministic and cheap to cover" per the audit's own assessment — no heavy fixtures, mocking frameworks, or database setup should be needed for either.

### Non-goals
- Do not refactor `buildRepairConfig` or `discoverManagedDurableAgentConfigs`'s implementation. Even though the audit's complexity verdict calls `discoverManagedDurableAgentConfigs` "mild accidental" complexity, simplifying it is explicitly out of scope for this task — this task closes a testing gap, it does not also take a discretionary refactor pass. If, while writing tests, the worker finds the function's structure makes correct testing meaningfully harder than it should be, note that as a follow-up candidate rather than refactoring inline.
- Do not expand scope to other 0.0%-covered functions elsewhere in `internal/service` — this task's scope is exactly these two functions, per the finding.

### Dependencies
None.

### Tests required
- New test functions (or an expansion of an existing `_test.go` file if one already exists for `tool_cache_wiring.go`/`managed_durable_configs.go` — check first rather than assuming a new file is needed) covering the branch structures described above.
- Coverage should be verified numerically, not just "tests exist": run `go test -coverprofile=... ./internal/service/...` and confirm both functions no longer show 0.0% in `go tool cover -func=...` output.

### Prevention
Consider whether a coverage-floor check (per-function or per-file, not necessarily package-wide) belongs in this batch's Wave 7 quality-ratchet work (`12-quality-ratchet-and-standards/` — out of scope for this task itself, but worth a cross-reference note) so a newly-added 0%-covered pure function doesn't silently reappear later.

### Verification
- `go test ./internal/service/...` passes, including the new tests.
- `go test -coverprofile=cover.out ./internal/service/... && go tool cover -func=cover.out | grep -E "buildRepairConfig|discoverManagedDurableAgentConfigs"` shows non-zero coverage for both, materially closer to the 77-95% band their siblings sit at (not just a token 5% smoke test).
- `go build ./...` remains clean (no production code should have changed).

### Risk / rollback
Essentially zero risk — additive test-only changes. Rollback is a trivial revert if anything unexpectedly breaks (e.g. a test fixture accidentally depends on filesystem state outside `t.TempDir()`).

### Done means
- [ ] `buildRepairConfig` has table-driven tests covering each level of its fallback chain, verified against the function's actual current implementation (not an assumed structure).
- [ ] `discoverManagedDurableAgentConfigs` has tests covering empty/valid/multiple/malformed/missing-directory cases, verified against its actual current error-handling behavior.
- [ ] `go tool cover -func=...` confirms both functions moved off 0.0%, materially into (or near) their sibling functions' 77-95% coverage band.
- [ ] No production code in either file was modified.

## Work log

<!-- Worker fills this in as it goes. -->

## Review notes

<!-- Reviewer fills this in. -->
