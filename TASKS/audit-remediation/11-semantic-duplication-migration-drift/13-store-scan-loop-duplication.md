# Optional: a generic `scanRows[T any]` helper to collapse `internal/store`'s query→scan-loop→append duplication

**Phase:** Wave 6 — Semantic duplication / migration drift
**Status:** not-started
**Depends on:** none
**Touches:** `internal/store/*.go` — ~20+ list methods across the package, no single file is canonical.

`requires_architect_decision: false` — explicitly optional; do not treat this as must-fix.

## Context

### Findings addressed
- `GO-STORE-007` — severity low, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.1; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-STORE-007`.

### Root cause

Mechanical "query → scan-loop → append" duplication exists across ~20+ list methods in `internal/store` — 41 `dupl`-tool hits confirmed inside the package (`raw/golangci-audit-complexity.log`), with 2 pairs manually sampled and confirmed as genuine structural duplication of "scan N rows into a typed slice." No shared generic `scanRows[T any](...)` helper exists, even though Go generics are available in this codebase (confirm current Go version supports the generics feature set assumed here before implementing — check `go.mod`'s `go` directive).

### Why this is explicitly optional — quote the audit directly

Classification **(1) textual-only boilerplate** — and unlike several of `internal/store`'s other findings (e.g. `GO-STORE-003`, `GO-STORE-004`), **every site is already individually correct** (proper `Close`/`Err` handling per row, per the audit's own package-level review in §8.1). This is purely a DRY/maintainability question, not a defect. The audit's own `false_positive_considerations` field states this explicitly and should be quoted verbatim in any decision to skip or defer this task: **"per guide §27, do not treat correct, readable, repeated boilerplate as must-fix without identified pain."** No bug was found riding on this duplication — contrast this directly with `GO-STORE-004` (`internal/store/plugin_settings.go`), where the audit found a real behavioral inconsistency (an unchecked `json.Unmarshal` in one method vs. a checked one in its sibling) riding on a similar-looking duplication; no such inconsistency was found here.

### Current behavior

The audit sampled 2 pairs of the 41 `dupl`-confirmed hits and manually verified genuine structural duplication of the scan-loop shape; it did not read all 20+ occurrences individually (explicitly out of budget — see §8.1's "Not covered" note). **Before implementing, re-run the `dupl` tool (or read `raw/golangci-audit-complexity.log` if it is still present in the repo) to get the current, authoritative list of the ~20+ affected methods** — do not assume the audit's file-count estimate is exhaustive or still accurate, since `internal/store` may have grown new list methods since the audited commit.

### Desired invariant

If this task is undertaken: one generic `scanRows[T any](...)` helper implements the "scan N rows into a typed slice, with correct `Close`/`Err` handling" shape once; individual list methods call it with their existing per-type `scanX` helper (which the audit confirms are already consistently used and correct) rather than hand-rolling the outer loop.

## What to do

### Scope
- Identify the current, authoritative set of list methods exhibiting this duplication (re-run `dupl` or equivalent — see above).
- Design a `scanRows[T any](rows *sql.Rows, scan func(*sql.Rows) (T, error)) ([]T, error)` (or similar signature, adapt to match the package's existing `scanX` helper conventions) generic helper, placed wherever `internal/store`'s existing shared helpers already live (check for a `helpers.go` or similar — do not introduce a new file for one function if an obvious existing home exists).
- Migrate list methods to use it **incrementally and verifiably** — this is optional, low-priority cleanup, not a single big-bang rewrite; a worker picking this up should feel free to do a representative subset first (e.g. the 2 pairs the audit itself sampled) and confirm the pattern works cleanly before deciding whether to extend it to all ~20+ sites, per the guide's own preference for incremental, reviewable changes over broad rewrites.

### Non-goals
- **Not required.** This entire task may be legitimately skipped or deferred by a planner without further justification beyond citing the audit's own false-positive framing above — this is the one item in this folder the audit itself pre-emptively argues against treating as urgent.
- Not a repository-wide Store abstraction rewrite — the remediation guide's own Wave 2 "Store correctness" section explicitly warns against this for `internal/store` generally, and it applies with equal force here: this task is a narrow, optional DRY helper, not license to restructure how `internal/store` accesses the database.
- Not touching the per-type `scanX` helpers themselves — the audit confirms these are already correct and consistently used; only the outer loop shape is in scope.

## Tests required

- If undertaken: existing tests for every migrated list method must pass unchanged — this is a pure refactor with no intended behavior change, so the pre-existing test suite (already noted by the audit as covering transaction safety and correctness well) is the regression guard.
- No new test is strictly required for the generic helper's own correctness beyond what the migrated call sites' existing tests already exercise, though a small dedicated unit test for `scanRows` itself (empty result set, single row, multiple rows, a scan error mid-iteration) is good practice given it becomes a shared primitive.

## Prevention

Not applicable in the usual sense — this is a maintainability improvement, not a defect-prevention task. If undertaken, its value is reducing the amount of boilerplate a future engineer has to write correctly by hand every time a new list method is added (correct `Close`/`Err` handling is a place bugs *could* be introduced in a future hand-rolled copy, even though none currently exist).

## Verification

```bash
go build ./internal/store/...
go vet ./internal/store/...
go test ./internal/store/... -v
```

Observable behavior required for PASS: if undertaken, all migrated list methods' existing tests pass unchanged; `go vet`/`staticcheck` clean; a rerun of `dupl` (or equivalent) shows a measurable reduction in the flagged hit count for `internal/store`.

## Risk / rollback

Low risk if undertaken carefully and incrementally (per the guide's own preference) — each migrated call site is independently testable against its existing test coverage, since the audit confirms every site is already individually correct (nothing to accidentally "fix" that wasn't broken). The main risk is scope creep — attempting to migrate all ~20+ sites in one pass rather than incrementally, increasing review surface for a change explicitly marked optional and low-priority. Rollback is a per-file revert; the incremental approach recommended above makes partial rollback easy if only some migrated sites cause issues.

## Done means

- [ ] Explicit decision recorded (undertaken vs. deferred/skipped) — either is an acceptable outcome for this task per its own optional framing.
- [ ] If undertaken: `scanRows[T any]` helper implemented; at least the audit's 2 sampled pairs migrated as a proof of pattern.
- [ ] If undertaken further: remaining list methods migrated incrementally, each with passing existing tests.
- [ ] `go build`, `go vet`, `go test ./internal/store/...` all pass regardless of how far the migration goes.

## Work log

<!-- Worker fills this in: what was actually done, or the explicit decision to defer and why. -->

## Review notes

<!-- Reviewer fills this in. -->
