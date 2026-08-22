# Consider a sync test for the two intentionally-independent `dispatch_to_agent` reflex evaluators

**Phase:** Wave 6 — Semantic duplication / migration drift
**Status:** not-started
**Depends on:** none
**Touches:** `internal/service/chat_reflex_dispatch.go` (upstream evaluator), `internal/selftools/self_tools_dispatch.go` (inside `task_execute`, the second evaluator).

`requires_architect_decision: false` — low priority, informational finding; the two evaluation points themselves are confirmed intentional and are not being merged.

## Context

### Findings addressed
- `GO-MCPTOOL-010` — severity **informational**, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.7; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-MCPTOOL-010`.

### Root cause / why this is classification (5), not a bug to fix

`internal/selftools/self_tools_dispatch.go` (inside `task_execute` itself) and `internal/service/chat_reflex_dispatch.go` (upstream, before `task_execute` is even reached) each independently evaluate the same `dispatch_to_agent` reflex rows. This is **self-documented and deliberately independent** — the code's own comment argues this is intentional, because the two evaluation points answer two genuinely different questions:

1. **Upstream (`chat_reflex_dispatch.go`)**: should the LLM even reach for `task_execute` in the first place?
2. **Inside `task_execute` (`self_tools_dispatch.go`)**: once inside `task_execute`, which agent should it actually target?

The audit accepts this reasoning and classifies the finding as **(5) intentionally independent** — this task does **not** attempt to merge the two evaluation points, and doing so would be actively wrong given the documented rationale. The one real residual risk the audit identifies is narrower and more specific: **nothing tests that the two evaluators can never diverge** on the underlying reflex rules they both read from. If a future change to how reflex rows are interpreted lands in one evaluator and not the other, the two decision points could start disagreeing about what a given reflex row means — not because the *decision points* diverged (they're supposed to answer different questions) but because their shared *interpretation of the underlying rule data* silently drifted apart.

### Current behavior

The audit's evidence array for this finding is empty; locate both evaluators via `grep -n` in `internal/service/chat_reflex_dispatch.go` and `internal/selftools/self_tools_dispatch.go` before starting, and read the existing in-code comment that documents the intentional-independence rationale (quote it, don't paraphrase, when recording work).

### Desired invariant

The two evaluation points remain independently reachable and answer their two distinct questions (as today), but their shared interpretation of the underlying `dispatch_to_agent` reflex rule data — e.g. how a given reflex row's match conditions are parsed and evaluated — cannot silently diverge without a test catching it.

## What to do

### Scope
- `internal/service/chat_reflex_dispatch.go` — the upstream evaluator's reflex-row interpretation logic.
- `internal/selftools/self_tools_dispatch.go` — `task_execute`'s internal evaluator's reflex-row interpretation logic.
- Whatever shared data structure or parsing logic both evaluators read from (the reflex rows themselves) — this is the actual surface a sync test needs to cover, not the two decision points' higher-level "should I dispatch" logic, which is intentionally different.

### Proposed direction

Per the audit's own recommendation: "Record for architect awareness; consider a test asserting the two evaluators stay in sync." Concretely: write a table-driven test that constructs a representative set of `dispatch_to_agent` reflex rows and feeds them through **both** evaluators' rule-interpretation logic (not their full decision pipelines, which are legitimately different — specifically the row-parsing/matching sub-logic each one shares), asserting both evaluators agree on what each row *means* even though they may legitimately reach different final dispatch decisions for different reasons. If the two evaluators' rule-interpretation logic is not currently factored into an isolable sub-function in either file, this task should extract it minimally (without changing either evaluator's external behavior) so it can be tested this way — but keep this extraction narrowly scoped to "make the shared interpretation testable," not a broader refactor of either evaluator.

### Non-goals
- Not merging the two evaluation points — the audit explicitly endorses their separation.
- Not changing either evaluator's actual dispatch decision logic — only adding a test (and, if needed, a minimal extraction to make that test possible) covering the shared rule-interpretation surface.

## Tests required

- A new table-driven test asserting both evaluators' interpretation of a representative set of `dispatch_to_agent` reflex rows agrees, even where their final dispatch decisions may legitimately differ for the documented reasons above.

## Prevention

This is itself the prevention mechanism the finding asks for — a low-cost regression test that would catch the one real residual risk the audit identified (silent divergence in rule interpretation) without constraining the two evaluators' intentionally-different decision logic.

## Verification

```bash
go build ./internal/service/... ./internal/selftools/...
go vet ./internal/service/... ./internal/selftools/...
go test ./internal/service/... ./internal/selftools/... -run 'ReflexDispatch|DispatchToAgent' -v
```

Observable behavior required for PASS: the new sync test passes today (both evaluators currently agree, per the audit's confirmation that no divergence exists yet) and would fail if a future change caused the two evaluators' rule interpretation to disagree.

## Risk / rollback

Very low risk — this task adds a test (and, at most, a narrowly-scoped extraction to make that test possible) without changing either evaluator's observable dispatch behavior. Rollback is a revert of the added test/extraction.

## Done means

- [ ] Both evaluators' documented intentional-independence rationale re-confirmed by reading the current in-code comment.
- [ ] A sync test added covering both evaluators' shared reflex-row interpretation logic.
- [ ] No change to either evaluator's actual dispatch-decision behavior.
- [ ] `go build`, `go vet`, `go test` pass for both packages.

## Work log

<!-- Worker fills this in. -->

## Review notes

<!-- Reviewer fills this in. -->
