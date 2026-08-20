# Summary — Scheduling batch

Implements `docs/engineering/architecture/12-scheduling.md`, the design produced by a dedicated architecture design session (2026-08-20, operator-signed-off, no code changed) that reviewed the `go-scheduler` library, Hadron's live production adoption of it, Torque's own (unrelated) scheduling engine, and Nanite's existing half-built scheduling substrate before proposing anything. This batch is that design's implementation — 9 task files, not part of `docs/engineering/TASKS.md`'s Phase 0-9 sequence, a sibling to `TASKS/reflex-taxonomy/` and `TASKS/harness-reactive-self-tools/`.

Resolves Torque tasks `CW-20260819-0004` ("build a Nanite-native scheduler — needs a library-vs-build decision and a DB-backed failure/retry-strategy design") and `CW-20260819-0006` ("verify whether the `add_schedule` reflex action already covers some or all of `0004`'s ground before building from scratch"). Unblocks `CW-20260819-0005` (two periodic durable audit agents, previously deferred pending a trigger mechanism that didn't exist) — this batch supplies that trigger mechanism but does not build the two agents themselves.

## What shipped

**A single, real scheduling engine replaces the old one — not a second system running alongside it.** Nanite previously polled for due schedules every 2 minutes and recomputed "is this due" from scratch on every poll using a 15-minute lookback window — a fragile, ad hoc mechanism with no real claim/lock semantics. That mechanism is now fully deleted, not just superseded. In its place: `go-scheduler` (an in-house library already running in production for a sibling app, Hadron), ticking once per second and using a real compare-and-set database update to claim each due schedule — safe against two processes racing the same row because Nanite's database connection is deliberately single-writer by design.

**Schedules can now do four different kinds of things, not just one.** Previously the schema had five theoretical "kinds" of schedule but only one — waking a durable agent with a prompt — ever actually worked in production. That's collapsed to two real trigger types (cron-based, one-time) crossed with four real job types: waking a durable agent (the one that already worked, unchanged in effect), running a workflow, running a named command/tool, or firing a reflex on a timer instead of only on chat activity. The three new job types are real and dispatchable, though the audit-agent work that would actually exercise "run a named command on a schedule" (`CW-20260819-0005`) is intentionally not part of this batch.

**Real retry and failure policy, built where the library doesn't provide it.** The underlying scheduling library only knows how to retry a failed firing once a second, forever, with no memory of how many times it's already tried. This batch adds a proper policy on top: a schedule can specify how many retries to attempt, with an increasing wait between each attempt (capped, so it doesn't spiral), and what should happen once retries run out — disable itself, or just log and keep going for its next natural occurrence.

**Four ways a schedule gets created, all wired and independently verified working:**
1. An operator-authored YAML config file, synced at every boot (this already existed for the one real production schedule Nanite runs today — a nightly Loom Curator export job — and continues to work, after a bug found mid-batch was fixed, see below).
2. An agent's own automated "reflex" behavior can now create a schedule — this was designed months ago, seeded into the database, and never actually connected to anything. It's connected now.
3. An agent can schedule its own follow-up wake-up directly, via a new tool it can call mid-conversation, scoped so it can only ever schedule something for itself, never for another agent.
4. A new operator-facing API (`/api/schedules`) for direct create/read/update/delete/status control, mirroring the same kind of surface Hadron already exposes.

**Visibility into what fired, when, and why.** Every real schedule firing — successful, retried-then-succeeded, or eventually given up on — now writes a record to the same event log the agent-behavior ("reflex") system already uses, under its own clearly labeled category, so an operator looking at recent activity can see schedule fires alongside everything else without a separate tool.

## What got escalated, and how it resolved

All entries below are in `TASKS/ESCALATIONS.md`, dated 2026-08-20, under the "Scheduling" heading:

- **A migration-numbering collision with the sibling `harness-reactive-self-tools` batch, resolved cleanly.** Both batches' first task independently claimed the same next-available database migration number, since they were planned against the same starting point. The sibling batch happened to land first; this batch's own task file had already anticipated this exact scenario and instructed itself to renumber — which it did, with no rework needed.
- **A real correctness finding, caught and fixed before it could reach production: the library's own internal "run ID" turned out not to be a stable way to track retry attempts of the same firing.** The task building the retry-policy layer assumed (per the earlier design and schema work) that each firing keeps the same internal ID across retry attempts — the library's actual behavior is to generate a fresh one on every one-second tick. Taking the original assumption at face value would have silently broken the retry-limit feature this whole piece of work exists to build (every retry attempt would have looked like a brand-new firing with a fresh budget, meaning schedules that keep failing would retry forever instead of stopping after the configured limit). Found by reading the actual library source rather than trusting the design document's paraphrase, fixed with the correct tracking approach, and independently re-confirmed by a second, fresh reviewer with no involvement in the original fix.
- **A real, previously-undetected bug found while wiring the new engine in: the existing boot-time schedule sync was silently erasing the new retry-policy settings on every restart.** The one schedule Nanite genuinely runs in production today (a nightly automated export job) gets re-synced from its config file on every server restart, by design — that's expected. What wasn't expected: the re-sync code hadn't been updated to carry forward the new "when should this next fire" timestamp and retry settings this batch added, so every restart after the very first one would have silently un-scheduled that job permanently. Found and fixed in the same work session, before merge, by tracing the actual boot sequence rather than assuming the fix built earlier in the batch was sufficient on its own.
- **A real, reproduced bug found during structured review, fixed via this project's standard "fix it as new work, then get it re-reviewed fresh" process.** The reviewer checking the four producer mechanisms found that the "agent behavior creates a schedule" path had no check for a malformed schedule expression — and traced through the actual library code to show this wasn't a minor cosmetic gap: a malformed schedule would silently and permanently never fire, with almost no visible signal that anything was wrong. This was fixed with the same validation the other two schedule-creating paths already had, and the fix itself was independently re-checked by a second reviewer with no involvement in either the original work or the first review — confirmed correctly closed.

## Anything still flagged for later

Four small, non-blocking cleanup items are logged in `TASKS/ESCALATIONS.md` (all 2026-08-20) and recommended to be bundled into a single small future task rather than handled individually: a field-naming inconsistency worth a rename for clarity, one small piece of duplicated date-math logic that should be consolidated, one remaining spot (an operator/YAML-authored config path, not an agent-authored one, so lower real-world risk) that still has the same missing-validation gap the reviewer's finding above fixed elsewhere, and one hardcoded default value that duplicates a setting defined elsewhere. None of these represent live bugs today — see `TASKS/scheduling/HANDOFF.md` for the full technical detail on each, intended for whoever picks up that cleanup work.

Separately, a lower-priority observability gap was flagged during review (not part of the bundle above): if the system's own internal record-keeping about a retry attempt ever fails to write, that failure is currently only logged, not otherwise surfaced — a narrow edge case, already partially mitigated by this batch's own telemetry work, worth a closer look whenever someone next has capacity in this area.

`CW-20260819-0005`'s two audit agents (code-quality, security) remain entirely unscoped and unbuilt — this batch supplies the trigger mechanism they were blocked on, nothing more.

## Current `TASKS/INDEX.md` state for this batch

Per `TASKS/INDEX.md`'s "Scheduling" section (lines 353-377):

| Task | Phase | Status |
|---|---|---|
| `01-schema-schedule-kind-collapse-and-retry-columns` | 1 | reviewed |
| `02-store-adapter` | 1 | reviewed |
| `03-runner-adapter-and-job-taxonomy` | 1 | reviewed |
| `04-retry-backoff-on-fail-policy` | 1 | reviewed |
| `05-engine-wiring-and-full-replace` | 1 | reviewed |
| `06-schedule-fire-telemetry` | 2 | reviewed |
| `07-wire-add-schedule-reflex` | 2 | reviewed (bug found in review, fixed, re-reviewed clean) |
| `08-agent-self-tool` | 2 | reviewed |
| `09-operator-http-api` | 2 | reviewed |

All 9 tasks `reviewed` and closed — none in-progress, none blocked, none parked. Confirmed by two independent fresh-reviewer section passes (Phase 1: `01`-`05`; Phase 2: `06`-`09`), each with no shared context with the implementing workers, plus the Orchestrator's own build/vet/test verification and multiple live dogfeeds against real running instances at each checkpoint — including a restart-mid-cycle safety claim that was independently re-verified twice (once by the Orchestrator, once separately by the fresh reviewer building its own test harness against a harsher, non-graceful process kill). Per `TASKS/INDEX.md`'s own summary line for this section: "Batch complete, all 9 tasks reviewed clean (2026-08-20)."

Doc-writer handoff (this document and `TASKS/scheduling/HANDOFF.md`) is the last step for this batch — no further dispatch is queued.
