# Audit Remediation — task inventory

This is **not** a sequenced batch yet. Per the operator's explicit instruction,
this folder is the output of a **task-creation pass only** — a concrete,
well-specified inventory of remediation work derived from the completed Go
code-quality/architecture audit, organized into sub-folders by grouping so a
future planner/architect session can sequence, parallelize, and dispatch it
without having to rediscover the audit first. **No production code has been
touched.** No task below has been sequenced, prioritized against the others,
or assigned a dependency graph beyond what's obviously implied within its own
group — that work is deliberately deferred to a planner pass.

## Source material — read these first

1. **`docs/audits/2026-08-21-go-quality/REPORT.md`** — the full audit (13
   package-cluster reviews, ~150 pages of evidence). Every task file below
   cites the specific `§8.N` section(s) it draws from — read those sections,
   not the whole report, unless you need the cross-cluster synthesis in `§9`.
2. **`docs/audits/2026-08-21-go-quality/findings.json`** — the machine-readable
   catalog (113 findings, guide §26 schema) the audit produced.
3. **`~/dev/chrispian/inbox/nanite-audit-triage-remediation-planning-guide.md`**
   — the advisor's remediation-planning guide this task inventory follows.
   Its "Proposed remediation waves" (§4) is the direct source for this folder's
   grouping scheme; its §6 task template is the direct source for each task
   file's content sections (`Root cause`, `Desired invariant`,
   `All production callers`, `Non-goals`, `Prevention`, etc., layered onto
   this project's own `docs/engineering/templates/03-task-file-template.md`
   skeleton).
4. **`FINDING-INDEX.md`** (this folder) — the authoritative finding→task
   mapping, generated and cross-validated programmatically against
   `findings.json`: every one of the 113 findings maps to exactly one task
   file below, and every task file traces back to at least one real finding
   (two exceptions in `12-quality-ratchet-and-standards/`, explicitly
   guide-derived rather than finding-derived — marked as such in the index).
   **No finding was dropped, silently absorbed, or double-counted.**

## Audited commit vs. current state — Wave 0, not yet done

The audit was run against commit `8feeee5c`. **Per the remediation guide's own
Wave 0 ("Revalidate the baseline"), nobody has yet compared that commit
against current `HEAD` to check whether any of these findings have already
been fixed, superseded, or made moot by development that continued after the
audit.** This task-creation pass did not do that revalidation — it would have
required re-reading current source for all 113 findings, which is squarely
the planner's first job, not this pass's. Treat every task below as
**unconfirmed-still-open** until a planner pass does that comparison. Several
task files note this explicitly where the underlying code area is one this
audit already knew was under active development.

## How this folder is organized

13 sub-folders, one per root-cause/theme grouping, following the remediation
guide's own "Proposed remediation waves" structure (§4) — the grouping is
real signal (the guide put real analysis into it), but the **numbering is not
a mandated sequence**. A planner may reorder, split, merge, or re-prioritize
freely; the grouping exists to make related findings easy to hand to one
implementer/reviewer pair, not to lock in an execution order.

| Folder | Theme | Task count | Guide wave |
|---|---|---:|---|
| `01-plugin-install-convergence/` | Unify the GUI/API and CLI plugin-install security pipelines | 2 | Wave 1 |
| `02-linux-sandbox-fail-open/` | Sandbox must not report isolation success when isolation is absent | 2 | Wave 1 |
| `03-agent-slug-traversal/` | Canonical validation for identifiers that become filesystem paths | 1 | Wave 1 |
| `04-container-reaper-lifecycle/` | Container/reaper goroutine lifecycle, partial-construction cleanup, and the two distinct `-race` timeouts | 5 | Wave 2 |
| `05-subagent-execution-ordering/` | Approval bypasses the spawn concurrency cap; queued cancellation doesn't stop a run | 1 | Wave 2 |
| `06-store-correctness/` | `DeleteAgentByID` error-swallowing and related `internal/store` gaps | 2 | Wave 2 |
| `07-runtime-correctness-lifecycle/` | Observable runtime/lifecycle defects (orphan branches, unbounded job registries, silent decode errors) | 5 | Wave 2 |
| `08-remaining-security-hardening/` | Everything else security-flavored: SSRF, TOCTOU, permission-mode gaps, secret heuristics, auth/bind posture, dependency bumps, API validation gaps | 10 | Wave 3 |
| `09-production-islands/` | 6 fully-built, production-unreachable features needing an explicit wire/defer/retire decision | 6 | Wave 4 |
| `10-architectural-concentration/` | `chatServiceImpl`/`generateResponse`, `SelfToolsTransport`, and an explicit do-not-refactor note for `Container`/`internal/store` | 3 | Wave 5 |
| `11-semantic-duplication-migration-drift/` | 16 instances of "the same concept implemented twice" — naming collisions, migration drift, mechanical/semantic duplication | 16 | Wave 6 |
| `12-quality-ratchet-and-standards/` | Enforce the uncapped lint gate; codify the guide's 6 named engineering standards | 3 | Wave 7 |
| `13-mechanical-cleanup/` | Dead code, stale comments, naming/formatting, low-risk error-handling and hygiene batches | 5 | Wave 8 |

**Total: 61 task files addressing all 113 audit findings.**

## What "creating the task" means here — and doesn't

Each task file follows this project's `docs/engineering/templates/03-task-file-template.md`
skeleton (title/Phase/Status/Depends-on/Touches, Context, What to do, Done
means, Work log, Review notes — the last two left empty for whoever executes),
with the **Context** and **What to do** sections structured around the
remediation guide's own richer template (§6): findings addressed, root cause,
current behavior with real file:line pointers, desired invariant, scope, all
production callers (mandatory for shared-primitive/security fixes),
proposed direction, non-goals, tests required, prevention mechanism, and a
verification checklist.

Every task file:

- **Status: not-started.** None of this has been executed.
- Cites real `file:line` pointers pulled from `REPORT.md`'s evidence, not
  invented ones.
- States its own findings' severity/confidence from `findings.json` directly.
- Flags `requires_architect_decision: true` wherever the underlying finding's
  recommendation was "architect decision" in the audit (most of Waves 1, 3-6
  qualify; see each task's frontmatter).
- Does **not** attempt to sequence itself against sibling tasks beyond an
  obvious same-folder dependency — cross-folder dependency ordering (per the
  guide's §5 `depends_on`/`blocks`/`can_parallelize_with` scheme) is explicit
  planner work, not done here.

## What this pass deliberately did NOT do (planner's job)

- **Sequencing/wave assignment as a commitment** — the folder numbering
  mirrors the guide's suggested order but is not binding.
- **The finding disposition table** (guide §4 output C) — deciding
  `already-resolved` / `accepted-risk` / `false-positive` / `superseded` /
  `defer` / `needs-more-evidence` per finding requires the Wave-0
  HEAD-vs-audit-commit revalidation this pass didn't do.
- **The dependency ordering YAML** (guide §5) per task — `depends_on: /
  blocks: / can_parallelize_with:` needs cross-folder analysis.
- **`TASKS/INDEX.md` entry** — not added yet; a planner pass should add one
  per `docs/engineering/templates/04-index-section-template.md` once
  sequencing is decided (this batch doesn't fit the sibling-batch flow's
  single-`README.md`-with-task-sequence shape cleanly, since it's explicitly
  pre-planning inventory, not a planned batch yet).
- **Architect decision resolution** — `09-production-islands/`'s 6
  wire/defer/retire calls and the other ~9 items the remediation guide's §9
  names as required architect decisions are queued, not made.

## Recommended next step

Boot a Planner session (or the operator, directly) against this folder plus
the remediation guide, to: (1) do the Wave-0 HEAD-vs-`8feeee5c` revalidation,
(2) produce the finding disposition table, (3) sequence these 61 tasks into
an actual batch `README.md` with dependency ordering and a parallelization
plan, and (4) resolve the architect-decision queue before any task is
dispatched to a worker.
