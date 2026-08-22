# Workflow-branded and dispatch/dispatcher naming collisions — awareness only, no action required

**Phase:** Wave 6 — Semantic duplication / migration drift
**Status:** not-started
**Depends on:** none
**Touches:** nothing — no code change is proposed by this task.

`requires_architect_decision: false` — no action required beyond continued awareness.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 6 — semantic duplication and migration drift · **Dispatch unit:** `W6b`
> - **Depends on:** Wave 6a complete
> - **Blocks:** none
> - **Parallel-safe with:** anything — no code change proposed
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** false

## Context

### Findings addressed
- `GO-EXEC-003` — severity informational, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.10; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-EXEC-003`.
- `GO-EXEC-004` — severity low, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.10; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-EXEC-004`.

### Why this file exists

Both findings are classification **(5) intentionally independent**, and both are already well-documented in-repo. This file exists purely so these two naming observations don't disappear from the finding record, per the guide's "no finding should disappear merely because it was grouped" rule — not because either requires implementation work.

**GO-EXEC-003:** three "workflow"-branded packages — `internal/workflow`, `internal/workflowrunner`, `internal/agentworkflow` — implement three different execution models. The maintainers were already aware of the naming collision risk: `internal/agentworkflow/doc.go` explicitly documents why it wasn't nested as a `workflow` subpackage, specifically to avoid this exact collision. Deliberate, not an oversight — still a real grep/mental-model cost for anyone unfamiliar with the codebase, which is why the audit records it, but not a defect.

**GO-EXEC-004:** `internal/dispatch` vs. `internal/dispatcher` — similar names, genuinely different, well-documented concepts. The audit's own recommendation: "No rename recommended without architect review; flagged for awareness only."

## What to do

Nothing. No rename, split, or documentation change is proposed by this task. If a future planner or architect decides either naming collision has become a real problem (e.g. a new engineer repeatedly confuses the packages, or a 4th "workflow"-branded package is about to be added), that would be a new decision made with fresh information, not something this task should pre-empt.

## Done means

- [ ] Both findings recorded in this folder's `README.md` classification table (see that file) so they remain visible to future planning passes.
- [ ] No code change made.

## Work log

N/A — no implementation work associated with this task.

## Review notes

N/A — nothing to review beyond confirming this file accurately reflects the audit's own "no action required" framing for both findings.
