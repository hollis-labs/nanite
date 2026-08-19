# Old-docs archival pass

**Phase:** 9
**Status:** not-started

## ✅ Policy confirmed 2026-08-19 — operator decision, see below

**Resolved.** The operator confirmed the archive-vs-delete-vs-banner policy directly (2026-08-19), including the third `adr/` sequence question. Recorded here so a worker doesn't need to re-ask:

- **Genuinely superseded docs with no remaining historical value** (the bulk of `docs/architecture/` and the ~56-58 supersedable top-level `docs/*.md`): **delete per the standing dead-code policy.** Do not archive these — git history is sufficient if ever needed.
- **ADRs — all three colliding sequences** (`docs/decisions/`'s 3 files, `docs/architecture/ADR-001-tool-scoping-and-resilience.md`, and the newly-found `adr/` directory's 23 files, including its own `ADR-001-tech-stack.md`): **keep, do not delete** — add a banner on each noting the collision and pointing to `docs/engineering/decisions/` as the one canonical sequence going forward. ADRs are an immutable record of a real decision-with-alternatives; that's real historical value even when superseded, unlike a plain design doc that's just wrong now. The `adr/` directory is explicitly **in scope** for this pass — do not silently fold it in without a banner, and do not defer it.
- **`docs/audits/`** (325 files, ~29K lines, the April tree): treat as **one historical-snapshot unit**, not per-file triage — archive/banner the whole tree together rather than triaging 325 individual files. Scale alone (not urgency) is why: per-file triage here would be a materially different, much larger task.
- The 6-7 live review-artifact top-level files (see list below) stay untouched regardless — not part of any bucket above.

**Depends on:** none functionally, but should run late in the overall effort (per its own phase placement) since docs across `docs/engineering/*` may keep evolving until then, and a docs sweep run too early would need re-verification.
**Touches:** `docs/architecture/` (42 files, 8,786 lines), `docs/decisions/` (3 files, 403 lines), `docs/audits/` (325 files, 29,307 lines — the April tree, distinct from `docs/system-audit/2026-08-17/`, which is EXCLUDED from this task's scope), top-level `docs/*.md` (65 files, 12,995 lines).

## Context

TASKS.md Phase 6 item 2: *"`docs/architecture/`, `docs/decisions/`, `docs/audits/` (the April tree, distinct from `docs/system-audit/2026-08-17/`), and most top-level `docs/*.md` predate `docs/engineering/` and are now superseded where they overlap. Resolve the two colliding `ADR-001` files specifically. Proposed policy (needs confirmation before executing): move genuinely superseded, no-remaining-value docs to a clearly-marked archive location or delete them per the standing dead-code policy; anything with real historical value that isn't duplicated here gets an explicit 'superseded by `docs/engineering/...`' banner rather than being silently left to look current."*

### Scale

~435 files, ~51,491 lines total across the four locations, dominated by `docs/audits/` (~57% of line volume, 325 files across 27 dated audit folders following the `deep-review` skill's `index.md`/`NN-severity-topic.md` convention).

### The `ADR-001` collision — and a third sequence TASKS.md doesn't mention

Two files collide exactly as TASKS.md names:
- `docs/architecture/ADR-001-tool-scoping-and-resilience.md` (2026-03-09) — ToolBroker intent-based selection + 429 retry logic.
- `docs/decisions/ADR-001-models-catalog-sync.md` (2026-04-25) — `models.dev` overlay caching for pricing/context-window metadata.

`docs/engineering/decisions/README.md` (written 2026-08-18, part of this same review's own output) already names and tracks this exact collision as a known follow-up: *"There are currently two separate, colliding ADR sequences elsewhere in this repo... Both predate this folder and are not being renumbered retroactively... tracked as a follow-up, not done automatically."* That README establishes `docs/engineering/decisions/` as the new canonical ADR sequence going forward (currently empty — README only).

**A third, undiscussed `ADR-001` exists**: `adr/ADR-001-tech-stack.md` (2026-03-07, "Technology Stack Selection," written for "Mentat Chat" — a pre-Nanite product name). The `adr/` directory holds ~25 ADR files total (dated April 3-27), a full third sequence never mentioned by TASKS.md or `docs/engineering/decisions/README.md`'s "two colliding sequences" framing, and outside the four locations TASKS.md names (`docs/architecture/`, `docs/decisions/`, `docs/audits/`, top-level `docs/*.md` — none of which cover a repo-root `adr/` directory). **Flag this explicitly to the operator as an in/out-of-scope question** — don't silently fold it in or silently ignore it.

### Superseded-ness — representative findings, not exhaustive triage

- `docs/architecture/chat-system/` (13 files, self-dated "current as of 2026-05-07") — describes `classify.ClassifyMode` mode-suggestion and strategy-planner soft `MaxTurns` as live steps; both cut in full per this review. Clearly superseded — strong "superseded by `docs/engineering/architecture/04-harness.md`" banner candidate (cross-reference 01-03/07 too for the construction/launching/steering/routing slices it also covers).
- `docs/architecture/ARCHITECTURE.md` (v2.0, 2026-03-15) — directly superseded by `docs/engineering/architecture/00-overview.md`.
- `docs/architecture/tool-broker-design.md` and `docs/decisions/ADR-003-reasoning-augmented-broker.md` — describe the formal tool-broker abstraction Phase 0 #22 retires entirely. The design doc is straightforwardly superseded/dead. The ADR is a harder call — ADRs are meant as an immutable record of a real decision-with-alternatives (this one genuinely has one) — better candidate for "keep + superseded banner" than deletion, consistent with TASKS.md's own stated policy split.
- `docs/decisions/ADR-002-mcp-internalization-wrap-layer.md` (2026-04-26) — a real decision (uniform MCP tool-name registry) not obviously superseded by anything in `docs/engineering/` — likely still current; verify directly rather than assuming stale-by-age.
- `docs/architecture/plugin-system.md` (updated 2026-03-19, self-status "Implemented (core), Evolving") — likely overlaps `docs/engineering/architecture/09-plugin-system.md`; needs a direct side-by-side comparison, not yet done by this research pass.
- `docs/audits/` is categorically different from the other two directories — dated point-in-time bug/gap findings, not target-architecture docs, so "superseded by `docs/engineering/*`" isn't the right test; the real per-finding question is "still live in current code, or already fixed." The corpus already does *some* internal self-correction (e.g. `docs/audits/2026-04-26-foundation/02-a2-compaction-fires.md:10`: "The 2026-04-11 finding... is stale") but not systematically. Given the volume (324 real files, ~29K lines), file-by-file triage is likely out of scope for a single task — treating the whole dated tree as one historical-snapshot archival unit (rather than per-file banners) is the more realistic approach, but this is itself a policy question for the operator, not this task's call.
- `docs/audits/README.md` — a process doc (how the `deep-review` skill organizes output) still describing an active convention, but its own text ("Design docs or ADRs. Those go in `docs/architecture/` or an `adr/` directory.") now points at superseded locations rather than `docs/engineering/decisions/`. A small, concrete fix needed regardless of which broader policy the operator picks.
- Top-level `docs/*.md` — the bulk (56-58 of 65) look like pre-review, single-topic docs (`plugin-*-guide.md`, `promptrouter-*.md`, `tool-broker.md`, `messaging*.md`, etc.), reasonable "most... now superseded" targets. **6-7 files must be explicitly excluded from any sweep** — they are the direct inputs/outputs of the 2026-08-17/18 review itself and remain authoritative: `docs/architecture-decision-log-2026-08-17.md` (cited as authoritative by `EXECUTION-PROCESS.md` and every task file in this effort), `docs/architecture-agents-2026-08-18.md`, `docs/architecture-agents-tasks-2026-08-18.md`, `docs/alignment-review-2026-08-17.md`, `docs/alignment-review-full-context.md`, `docs/alignment-review-synthesis.md`, `docs/fresh-eyes-alignment-review-prompt.md`. TASKS.md's "most... predate `docs/engineering/`" phrasing implies this exception already; this task should carve it out explicitly in code/process rather than rely on a date-based sweep to happen to skip them correctly.

## What to do

1. For `docs/architecture/` and `docs/decisions/` (smaller, target-architecture-shaped corpora): do the real file-by-file superseded-vs-real-historical-value triage the research pass above only sampled. Delete confirmed-superseded, no-remaining-value files (mostly `docs/architecture/`); keep and banner the ADRs (`docs/decisions/`'s 3 files, plus `docs/architecture/ADR-001-tool-scoping-and-resilience.md`) per the confirmed policy above.
2. Resolve all three colliding `ADR-001` files (`docs/decisions/ADR-001-models-catalog-sync.md`, `docs/architecture/ADR-001-tool-scoping-and-resilience.md`, `adr/ADR-001-tech-stack.md`) the same way: keep all three as historical record, each with a banner noting the collision and pointing to `docs/engineering/decisions/` as canonical going forward — not renumbered retroactively (per `docs/engineering/decisions/README.md`'s own stated approach).
3. Apply the same keep-and-banner treatment to the rest of the `adr/` directory's 23 files — in scope for this pass, not deferred.
4. For `docs/audits/`: archive/banner the whole tree as one historical-snapshot unit — do not do per-file triage, per the confirmed policy.
5. For top-level `docs/*.md`: delete the ~56-58 confirmed-superseded, non-excluded files; explicitly leave the 6-7 review-artifact files listed above untouched, un-bannered, and in place.
6. Fix `docs/audits/README.md`'s stale "where ADRs go" pointer.
7. For whichever bucket ends up archived rather than deleted (the audits tree, at minimum), confirm the archive location doesn't collide with anything else in the repo and is clearly marked as historical (not just moved to a same-looking directory).

## Done means

- The `ADR-001` collision across all three sequences (including `adr/`) is resolved per the confirmed policy: kept, bannered, not deleted.
- Every file identified as genuinely superseded with no remaining historical value (mainly `docs/architecture/` and top-level `docs/*.md`) is deleted per the standing dead-code policy.
- `docs/audits/` is archived/bannered as one unit, not per-file.
- The 6-7 live review-artifact files are confirmed untouched.
- `docs/audits/README.md`'s stale ADR-location pointer is fixed.
- A final accounting (file counts deleted/bannered/left-as-is) is recorded in this file's Work Log.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated. Record the operator's policy confirmation explicitly here before any archival action.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
