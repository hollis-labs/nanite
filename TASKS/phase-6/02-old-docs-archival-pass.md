# Old-docs archival pass

**Phase:** 6
**Status:** not-started

## ⚠️ POLICY NOT YET CONFIRMED — DO NOT RESOLVE DURING PLANNING OR EXECUTION WITHOUT OPERATOR CONFIRMATION

**TASKS.md itself flags its proposed policy as "needs confirmation before executing." This task file drafts the scope and inventory fully, but the actual archive-vs-delete-vs-banner policy decision must come from the operator before any file in this task's scope is moved, banner-stamped, or deleted. Do not default to any single treatment (including the standing aggressive-dead-code-removal policy) as a substitute for that confirmation — this item is explicitly carved out from that default by TASKS.md's own wording.**

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

1. **Get explicit operator confirmation on the archive-vs-delete-vs-banner policy before touching any file** — present the three-way split (archive location, delete-per-dead-code-policy, superseded-banner) and get a real decision, not an inferred default. Also get an explicit in/out-of-scope call on the third `adr/` ADR sequence.
2. Resolve the two (or three, if in scope) colliding `ADR-001` files per whatever the operator decides — likely: keep both/all as historical record with a banner noting the collision and pointing to `docs/engineering/decisions/` as canonical going forward, rather than renumbering retroactively (per `docs/engineering/decisions/README.md`'s own stated approach).
3. For `docs/architecture/` and `docs/decisions/` (smaller, target-architecture-shaped corpora): do the real file-by-file superseded-vs-real-historical-value triage the research pass above only sampled, and apply the confirmed policy.
4. For `docs/audits/`: apply whatever unit-of-treatment the operator confirms (whole-tree archival vs. per-file triage) — do not default to per-file triage given the ~29K-line scale unless the operator explicitly wants that granularity.
5. For top-level `docs/*.md`: apply the confirmed policy to the ~56-58 non-excluded files; explicitly leave the 6-7 review-artifact files listed above untouched, un-bannered, and in place.
6. Fix `docs/audits/README.md`'s stale "where ADRs go" pointer regardless of the broader policy outcome.
7. If "archive location" is the chosen policy for any bucket, confirm the location doesn't collide with anything else in the repo and is clearly marked as historical (not just moved to a same-looking directory).

## Done means

- Explicit operator confirmation of the policy is recorded in this file's Work Log before any file was moved, banner-stamped, or deleted.
- The `ADR-001` collision (and the `adr/` third-sequence question) is resolved per that confirmed policy, not silently.
- Every file identified as superseded either carries an explicit "superseded by `docs/engineering/...`" banner, has been moved to a clearly-marked archive location, or was deleted — per the confirmed policy, applied consistently, not on an ad hoc per-file basis.
- The 6-7 live review-artifact files are confirmed untouched.
- `docs/audits/README.md`'s stale ADR-location pointer is fixed.
- A final accounting (file counts moved/deleted/bannered/left-as-is) is recorded in this file's Work Log.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated. Record the operator's policy confirmation explicitly here before any archival action.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
