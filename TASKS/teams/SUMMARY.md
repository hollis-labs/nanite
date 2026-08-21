# Summary — Teams batch

Implements `docs/engineering/architecture/15-teams.md`, the design produced by a dedicated architecture-alignment session (2026-08-20, operator-signed-off, no code changed) that reviewed an external proposal against Nanite's real code before proposing anything — generalizing today's hardcoded three-role dispatch pattern into a configurable, reusable N-slot organizational shape (Agent Workflow = execution, Team = organization/slots/authority/routing, TeamRun = a compiled WorkflowRun) rather than building a second orchestration engine. 11 task files, not part of `docs/engineering/TASKS.md`'s Phase 0-9 sequence, a sibling to `TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`, and `TASKS/scheduling/`.

## What Teams now lets Nanite do

A Team is a saved, named, reusable organizational shape — a set of roles ("Team Slots" like architect/orchestrator/engineer/reviewer), who's allowed to spawn or message whom, how a phase sequence of work moves forward (fluid collaboration phases and human-approval gates, in any order, including a Team with no gates at all), and how incoming messages get routed to the right role. Launching a Team (`POST /api/teams/{id}/launch`) resolves each role to a real agent — either waking an existing durable identity or constructing a fresh one through the normal agent-composition path — compiles the phase sequence into an ordinary workflow definition, and runs it through the exact same engine every other Nanite workflow already runs through. Nothing new was built to execute a Team; it reuses the existing workflow engine, messaging system, and agent-dispatch machinery throughout, exactly as the design's own "reuse, don't reinvent" framing required.

Concretely, as of this batch: a Team definition can be created/read/updated/deleted via a REST API; launching one produces a real, trackable run with real resolved team members; two previously-open runtime edge cases (what happens when a collaboration phase ends while someone's still working, and what happens when a message is addressed to a role whose member has failed or hasn't been created yet) both have concrete, tested answers rather than being left as design gaps.

**What Teams does not yet do:** nothing in the live chat/agent runtime can actually launch a Team on its own — no self-tool, no UI button, no automatic trigger calls this new API. That's deliberately the next layer of work, not part of this batch.

## What shipped, by phase

**Phase 1 — schema and storage (tasks 01-05).** Five new/changed database pieces, four of them independently parallelizable: a `teams` table for saved Team definitions; a `team_run_members` table recording which real agent/session was resolved to which role for a given run (the design's own stated "one genuinely new table"); a schema change allowing a new kind of workflow step ("flex" — a fluid collaboration phase, as opposed to a hard approval gate); a new table for authority grants (who may spawn/message/exclude whom); and a new optional scoping column letting an agent's automated routing behavior be limited to just one specific run instead of firing globally. All five passed a full independent review with two minor, non-blocking findings (a missing rollback-safety marker on one migration, inherited from an existing pattern; a documented sharp edge in the authority-check function, flagged forward for the task that would first actually call it).

**Phase 2 — the runtime engine (tasks 06-08).** The actual execution machinery: code that lets a workflow pause at a "flex" phase and resume once a defined exit condition fires (with a chosen, tested answer for what happens if other participants are still working when that happens — everyone in the phase is stood down cleanly, nothing is left hanging); a compiler that turns a Team's stored role/phase configuration into the same kind of workflow definition every other Nanite feature already runs; and a launcher that resolves each role to a real agent (correcting a real mistake in the original design's assumption about which existing database flag meant "wake an existing durable agent" — the actual flag means something unrelated), checks that the resolving identity is actually authorized to do so, and starts the run. All three passed review; task 08 (the launcher) surfaced two real, non-blocking findings that were carried forward as required work for later tasks rather than silently left open (see "Deferred" below).

**Phase 3 — routing (task 09).** The layer that decides where a message goes: explicit addressing (name a role directly), rule-based routing (route based on what's being said), and a fallback (if nothing else matches, go to the coordinator role) — plus the second of the two open runtime-edge-case answers: if a message is addressed to a role whose current member has failed or doesn't exist yet, the send fails loudly with a clear error rather than silently rerouting somewhere else. This phase had the batch's one real bug — see below.

**Phase 4 — the API surface (tasks 10-11).** A standard create/read/update/delete API for Team definitions, and a launch endpoint that accepts per-call overrides (e.g., "use 3 engineers this time instead of the default 1") without requiring a new saved Team definition per call. Task 11 also had to solve a real, found-during-implementation problem: every Team launch was permanently adding an entry to the same public list of "skills" Nanite advertises externally, which would have polluted that public listing with a growing number of one-off internal launch artifacts. Fixed before anything called this API in real traffic.

## Batch stats

11 tasks, 4 phases. 9 of 11 tasks passed their first review clean with no fix needed. One required a real fix-then-re-review cycle: task 09 (routing) had the batch's one real, reproduced bug, described below. Two tasks (10 and 11) had a minor process/bookkeeping gap caught during their own reviews and corrected in the same pass, not a code fix.

## The one real bug found and fixed

Task 09 built the rule that decides which routing rule wins when more than one could apply to the same message — normally, a Team's own custom rules (e.g., "route architecture questions to the architect") should win over the generic fallback rule ("otherwise, go to the coordinator"). The fix that was supposed to guarantee the fallback rule always loses only worked when a Team author left the priority unset and let the system pick a default — if a Team author explicitly set their own rule's priority to a low number (which the system's own documentation invited them to do), that safety guarantee silently didn't apply, and the fallback rule could permanently win instead, even on an exact match. This was caught by the reviewer reproducing it directly (not just reading the code), fixed by making the safety guarantee apply unconditionally regardless of how the priority was set, verified both ways (confirmed broken on the old code, confirmed fixed on the new code), and independently re-reviewed by a second, fresh reviewer who reproduced both the break and the fix themselves. Closed clean. Full detail: `TASKS/ESCALATIONS.md`'s "Teams task 09 review" entry (2026-08-20) and `TASKS/teams/09-team-routing.md`'s Work Log "Fix addendum" section.

## Process incidents worth knowing about

None of these affected any shipped code — all were caught and self-disclosed by the person/agent who caused them, per this project's standing discipline of logging even harmless process slips. Full detail in `TASKS/ESCALATIONS.md`'s 2026-08-20 entries:

- **Two separate `git stash`-class incidents.** Task 06's worker ran a plain `git stash` once (immediately reversed, no collision, since nothing else was running concurrently at that moment). Task 08's reviewer ran the more serious `git stash -u` + `git checkout <ref> -- .` (checking the whole working tree out to an older commit) while investigating something, caught it immediately, and restored the tree cleanly with no work lost. Both closed with zero impact, but the second incident prompted the standing recommendation to be repeated explicitly in every subsequent dispatch for the rest of this batch.
- **A brief working-directory mixup by the Orchestrator itself** while merging task 08 — a `git add` briefly landed in the wrong directory's index. Caught before any commit, corrected, no data lost.
- **Task 07's worker edited `TASKS/INDEX.md` directly**, which is supposed to be the Orchestrator's own job. The edit only ever lived in that worker's own isolated workspace and was never merged — the Orchestrator's own equivalent update superseded it with no consequence.
- **Task 10's `TASKS/INDEX.md` row went stale** (`not-started`) for a period after the actual code had already merged and the task file's own status said `implemented` — caught by task 10's reviewer, corrected in the same pass.

None of these represent a defect in shipped code — they're logged as a matter of this project's standing "log every real finding, even self-inflicted and non-blocking" discipline, and the operator does not need to take any action on them.

## What's explicitly out of scope / deferred, and why

Per `TASKS/INDEX.md`'s own "Deliberately out of this batch's scope" line, plus two real findings surfaced mid-batch and carried forward rather than silently absorbed:

- **Mid-run elastic slot growth** (adding, say, a second engineer to a Team after the run has already started) — deferred for a concrete, current technical reason, not just caution: the existing tool an agent would use to spawn a new participant is currently blocked from being called by exactly the kind of role (a Team's own orchestrator) that would need to trigger this. This batch resolves the number of participants per role once, at launch (or via a per-call override), and does not build growing that number mid-run.
- **The final set of authority verbs** — only three exist today (may-spawn, may-message, may-not-review); the design's own suggested additional verbs (may-delegate, may-approve, may-signal) are not built. The schema is built so adding one later is a small, safe database change, not a rebuild.
- **A final, locked default for what happens when a role resolves to more than one person** (e.g., "the engineer role" when there are three engineers) — this batch picked a working default (send to everyone currently active in that role) and explicitly flagged it as provisional, matching the design's own instruction not to lock this prematurely.
- **The eventual split between "Team" (who/what roles exist) and "Workflow" (what order things happen in) as two independently combinable things** — the phase sequence is already stored as its own separable piece specifically so this split is possible later without a rewrite, but the actual split was not built this batch.
- **Two things found only after this batch started, both resolved responsibly rather than silently absorbed or dropped:** the public "what can this system do" discovery listing was at risk of being permanently polluted by every Team launch (found during task 08's review, required as blocking work for task 11, and fixed there); and the real correctness bug in how routing priorities interact with the coordinator fallback (found during task 09's own review, fixed and re-verified in the same phase, described above).

## Still flagged for the operator/next session

- **`authority_json` (a column on the `teams` table) has no real typed validation and appears to be effectively unused.** The actual authority-grant mechanism ended up being a separate, dedicated database table built later in the batch, and nobody ever gave the original placeholder column a real shape to validate against. It accepts any well-formed JSON array with no further checking. Not a live bug (nothing currently depends on this column's contents being well-formed beyond "it's a JSON array"), but worth a decision before it's built on further: give it a real shape, or retire it.
- **A slow, unbounded memory-growth pattern for Team runs that are launched but never resumed** (e.g., stuck at a still-open collaboration phase or approval gate forever). The public-discovery-listing pollution problem above is fully fixed, but the underlying "every launch adds one more entry to an in-memory list that's never cleaned up" issue is only partly addressed (only launches that finish, fail, or get cancelled immediately are cleaned up; ones that are still waiting are not, correctly, since a still-open run genuinely needs to stay findable in case it's later resumed). Not urgent for normal usage, worth knowing if this system is ever used to launch a large volume of Teams that don't reliably resolve.
## Current `TASKS/INDEX.md` state for this batch

Per `TASKS/INDEX.md`'s "Teams" section (lines 394-436):

| Task | Phase | Status |
|---|---|---|
| `01-team-definition-schema` | 1 | reviewed (migration 128) |
| `02-team-run-members-table` | 1 | reviewed (migration 129) |
| `03-stepkindflex-schema` | 1 | reviewed (migration 130) |
| `04-team-authority-schema` | 1 | reviewed (migration 132) |
| `05-agent-reflexes-run-scoping` | 1 | reviewed (migration 131) |
| `06-stepkindflex-executor` | 2 | reviewed (migration 133) |
| `07-team-compiler` | 2 | reviewed (no migration) |
| `08-team-run-launcher` | 2 | reviewed (no migration) |
| `09-team-routing` | 3 | reviewed (no migration; fixed + re-reviewed) |
| `10-team-crud-api` | 4 | reviewed (no migration) |
| `11-team-run-launch-api` | 4 | reviewed |

All 11 tasks `reviewed` and closed — none in-progress, none blocked, none parked. Per `TASKS/INDEX.md`'s own closing line for this section: "All 11 tasks in the Teams batch (`01`-`11`) are now `reviewed`. The batch is complete."

Doc-writer handoff (this document and `TASKS/teams/HANDOFF.md`) is the last step for this batch — no further dispatch is queued.
