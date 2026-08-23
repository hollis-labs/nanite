# 10 — Architectural concentration

"Architectural concentration" is this grouping's name for the audit's god-object
and extreme-complexity findings — cases where one type or one function
accumulated far more fields, methods, or branching than the remediation
guide's own inspection thresholds, concentrating a large share of a package's
real behavior (or, in one deliberately-included counter-example, its
*apparent* but not *actual* behavior) into a single unit. This is distinct
from the mechanical complexity/duplication findings tracked elsewhere in this
batch: every finding here was individually read and judged by a human
reviewer during the audit's deep-dive phase, not just flagged by a metric
threshold — `chatServiceImpl`/`generateResponse` (`internal/service`) and
`SelfToolsTransport` (`internal/selftools`) were confirmed, by direct reading,
to fail the guide's "wiring vs. embedded behavior" test decisively, while
`Container` (`internal/service`), `internal/store`, and `Host`
(`internal/plugin`) were confirmed, by the same direct reading, to *pass* it —
high field/method counts that turned out, on inspection, not to indicate a
real problem. All five findings' evidence and dispositions are in
`docs/audits/2026-08-21-go-quality/REPORT.md` §8.1/§8.3/§8.4/§8.6/§8.7 and
`docs/audits/2026-08-21-go-quality/findings.json`.

Per the remediation guide's proposed wave structure (§4), this is **Wave 5 —
architectural concentration**, sequenced after security, correctness, and
production-island decisions and before semantic-duplication cleanup and
quality-ratchet work, because — per the guide's own dependency-ordering
default flow — architecture refactors should follow a trustworthy race/test
baseline and island decisions, not precede them. Two of this folder's three
files (`01-chatserviceimpl-generateresponse-decomposition.md`,
`02-selftoolstransport-decomposition.md`) are real decomposition-**planning**
work — a responsibility map, a phase-boundary or capability-domain map, and
(for file 01) a characterization test suite — deliberately stopping short of
any actual code extraction, which the guide's own §9 decision queue requires
an architect to approve first. The third file
(`03-container-and-store-review-note.md`) is the opposite: a **deliberate
non-task**, documenting a decision to **not** refactor `Container`,
`internal/store`, or `Host` despite their high field/method counts, because
the audit's own evidence and the remediation guide's own explicit
instructions both say a refactor would be the wrong call. It is included as
its own file — rather than being silently dropped from this batch — precisely
so those five findings (`GO-DEP-001`, `GO-DEP-002`, `GO-STORE-001`,
`GO-STORE-002`, `GO-PLUGIN-006`) don't disappear from tracking the way the
guide's own disposition-tracking requirement (§7 output C: "no finding should
disappear merely because it was grouped") is written to prevent.

**Wave reopened 2026-08-23.** The operator expressly approved both evidence
artifacts and directed implementation here rather than adding more deferred
follow-up work. AD-12 authorizes the reviewed six-phase action pipeline while
keeping `generateResponse` responsible for orchestration. AD-13 authorizes the
four recommended selective `SelfToolsTransport` delegations. Implementation is
tracked separately in `04-chatserviceimpl-generateresponse-action-pipeline.md`
and `05-selftoolstransport-selective-delegation.md`; the original `01` and `02`
files remain the evidence and characterization records those tasks build on.
`03` remains a deliberate no-refactor disposition.

**Implementation beat closed.** Tasks `10/04` and `10/05` are reviewed. The
result keeps both adapters/orchestrators recognizable while moving the approved
behavior into explicit owners; no additional count-driven decomposition was
added.
