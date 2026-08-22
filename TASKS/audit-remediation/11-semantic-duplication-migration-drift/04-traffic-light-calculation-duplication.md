# Export `inspector.TrafficLight` and remove `internal/service`'s duplicate reimplementation

**Phase:** Wave 6 — Semantic duplication / migration drift
**Status:** not-started
**Depends on:** none
**Touches:** `internal/inspector/service.go` (`trafficLight`, lines 266-276), `internal/service/inspector_producers.go` (`trafficLightFor`, lines 56-63).

`requires_architect_decision: false` — this is a clear, small fix with no design ambiguity: export an already-correct unexported function and delete its byte-for-byte duplicate.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 6 — semantic duplication and migration drift · **Dispatch unit:** `W6b`
> - **Depends on:** `10/01`
> - **Blocks:** none
> - **Parallel-safe with:** `11/03`, `11/12`, `11/14`, `11/16`
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

### Findings addressed
- `GO-INFRA-002` — severity low, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.2; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-INFRA-002`.

### Root cause

`inspector.trafficLight` (`internal/inspector/service.go:266-276`) is **unreachable in production** — only its own test calls it, confirmed by the audit's dead-code check. `internal/service/inspector_producers.go`'s `trafficLightFor` (`internal/service/inspector_producers.go:56-63`) is a **byte-for-byte reimplementation** of the same 3-line business rule, created specifically **because** `trafficLight` is unexported and `internal/service` had no way to call it directly. This is classification **(2) same semantics/stable** — both copies currently agree, but they are two copies of a business rule (some traffic-light threshold calculation) that has to be kept in sync by hand on every future edit, with nothing enforcing that they stay identical.

### Current behavior

```go
// internal/inspector/service.go:266-276 (trafficLight, unexported, only called by its own test)
```

```go
// internal/service/inspector_producers.go:56-63 (trafficLightFor, byte-for-byte reimplementation)
```

Confirm both line ranges against current source before editing — verify via `grep -n 'func trafficLight' internal/inspector/service.go` and `grep -n 'func trafficLightFor' internal/service/inspector_producers.go`, since unrelated changes may have shifted the exact ranges since the audit's commit `8feeee5c`.

### Desired invariant

There is exactly one implementation of the traffic-light calculation rule; `internal/service` calls the exported `inspector.TrafficLight` rather than maintaining its own copy.

## What to do

1. In `internal/inspector/service.go`, rename `trafficLight` to `TrafficLight` (export it), updating its own test call site (`internal/inspector/service_test.go` or wherever the existing test lives) to use the new exported name.
2. In `internal/service/inspector_producers.go`, delete `trafficLightFor` entirely and replace its call sites with calls to `inspector.TrafficLight`, adding the `internal/inspector` import if not already present (check first — `internal/service` may already import `internal/inspector` for other reasons).
3. Confirm the two functions' signatures actually match closely enough for a direct swap — read both bodies before assuming; if `trafficLightFor`'s call sites pass slightly different argument types or ordering than `TrafficLight` expects, adapt the call sites, not the exported function (the exported function is the one being kept as canonical).

## Tests required

- Existing tests for both `inspector.trafficLight`/`TrafficLight` (renamed, not rewritten) and any test exercising `internal/service/inspector_producers.go`'s traffic-light-dependent behavior must pass unchanged after the swap.
- If `trafficLightFor` had no dedicated test of its own (confirm this), no new test is strictly required beyond the existing `inspector.TrafficLight` coverage — the fix removes a code path rather than adding one.

## Prevention

Removing the duplicate is itself the fix — a single-source-of-truth function has no divergence risk by construction. No new lint rule or test is required beyond confirming the duplicate is actually gone (`grep -rn 'func trafficLightFor' internal/service/` should return nothing after this task).

## Verification

```bash
go build ./internal/inspector/... ./internal/service/...
go vet ./internal/inspector/... ./internal/service/...
go test ./internal/inspector/... ./internal/service/... -run 'TrafficLight' -v
grep -rn 'func trafficLightFor' internal/service/
```

Observable behavior required for PASS: `go build`/`go vet`/`go test` all pass; the `grep` for `trafficLightFor` returns nothing; `inspector.TrafficLight` is the only implementation of the rule anywhere in the tree.

## Risk / rollback

Very low risk — a pure export-and-delegate change with no behavior change (both implementations were already byte-for-byte identical). Rollback is a single-commit revert.

## Done means

- [ ] `inspector.trafficLight` renamed to `inspector.TrafficLight` (exported).
- [ ] `internal/service/inspector_producers.go`'s `trafficLightFor` deleted; call sites use `inspector.TrafficLight` directly.
- [ ] No remaining reference to `trafficLightFor` anywhere in the tree.
- [ ] `go build`, `go vet`, `go test` all pass for both packages.

## Work log

<!-- Worker fills this in. -->

## Review notes

<!-- Reviewer fills this in. -->
