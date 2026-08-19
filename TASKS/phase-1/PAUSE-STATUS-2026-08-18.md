# Phase 1 pause — status and issues, 2026-08-18

Written at the operator's request: *"document where we are and all the issues we've run into, commit everything and update tasks/docs in our working branch. We'll pause here for tonight until I've had a chance to review how far we off the mark before we continue."* This is not the standard end-of-phase `HANDOFF-TO-NEXT.md`/`PHASE-SUMMARY.md` pair (Phase 1 isn't finished — Wave 3 remains) — it's an interim checkpoint for the operator's own review before deciding how to proceed.

**Branch:** `phase-1-execution`, worktree at `.claude/worktrees/phase-1-execution`, branched off Phase 0's `f2d2114b`. 40 commits ahead of that point as of this pause. Nothing pushed anywhere, nothing merged to `main` or the shared Phase-0 working directory.

## Where things stand

| Task | Status |
|---|---|
| `01` roles table + cascade resolver | reviewed |
| `02` agents composition columns (`role_id`/`model_id`/`runtime_kind`, `activation_mode` 3-value enum) | implemented |
| `03` consumers table | reviewed |
| `04` `known_tools`/`agent_tools` FK | implemented |
| `05` `agent_skills`/`agent_projects` FK | reviewed |
| `06` models table sync target | reviewed |
| `07` reflex opt-out field | reviewed |
| `08` kill file-reingest-on-boot | implemented (Round 2 — see "The real issue" below) |
| `09` assignment API | not-started, rescoped to backend-only tonight |
| `10` data-migrate legacy `.nanite/agents/*.md` | out-of-scope (operator decision) |
| `11` wire `SelectForAgent` to `agent_tools` | not-started |
| `12` fix `AgentService.Get` dropping DB-only columns | implemented |
| `13` `nanite-native` adapter config-conflation | **deferred, awaiting your review** — do not dispatch |

**Remaining before Phase 1 is actually done:** Wave 3 (`09`, `11`), plus whatever you decide on `13`. Validation/fresh-review/handoff docs haven't happened for anything past Wave 2 yet.

Every merged task passed: `go build`/`go vet`/`go test ./...` clean (one pre-existing, unrelated `container.go` vet finding throughout, confirmed present before Phase 1 started), a real-backed-up-DB migration test, and a live dogfeed against a running scratch instance — not just the test suite. Wave 1 got a fresh independent Reviewer dispatch (passed, two minor non-blocking follow-ups logged in `INDEX.md`); Waves 2+ have not been reviewed yet.

## Issues encountered, in order — the honest account

### 1. Real bugs found and fixed cleanly (process working as intended)
- **Task `05`'s own escalation**: correctly stopped before adding an FK when it found two live write paths (`handleAssignAgentSkill`, `handleAddAgentProject`) that could create the exact orphaned reference the FK was meant to prevent. Scope expanded, fixed, re-verified. No process failure here — this is the system catching a real gap before it shipped.
- **Migration numbering**: five parallel/near-parallel workers independently picked the same next migration number from the same base commit (106, four times; 110, twice). Caught and renumbered at every merge, including one real functional bug the renumbering exposed (a `goose DownTo` test target that would have cascade-reverted unrelated migrations if left at its pre-renumber value). Mechanical, expected in a parallel-dispatch model, all resolved.
- **Phase 0 item `#10`'s status not matching the code**: found while investigating task `08`, independently confirmed via `git log` that the described fix never actually landed despite being marked `implemented` in Phase 0's own tracker. Logged in the shared `ESCALATIONS.md` for Phase 0's owning session to correct — not something this branch can fix, Phase 0's table isn't ours to edit.
- **`AgentService.Get` silently dropping `role_id`/`model_id`/`runtime_kind`/`consumer_id`** for file-discovered agents (task `12`): found by live-dogfeeding Wave 2 instead of trusting the test suite, exactly the failure mode `standards/testing.md` warns about. Real, fixed, verified.

### 2. The real issue — task `08` shipped once, wrong, and I didn't catch it

This is the one that matters. Task `08` ("kill the file-reingest-on-boot pattern") was implemented, tested, live-verified, and merged — a real, correct fix for the problem *as that task file described it*. The task file's own framing (written by an earlier planning session, before this Orchestrator existed) said the goal was "ingest once, then leave DB-authoritative," explicitly preserving automatic first-ingest of new `.nanite/agents/*.md` files as a permanent mechanism.

That framing doesn't match the architecture doc's own plain text: *"Files as agent storage, [cut] except builtin/seed content"* — no carve-out for project/user-source files — and the decision log's settled summary: *"DB-authoritative with no re-ingest-on-boot,"* not "no destructive re-ingest." I dispatched and accepted this task without cross-checking its framing against that primary text closely enough to catch the gap. It only surfaced because you asked directly — "weren't file-discovered agents eliminated in phase-0?" — and then pushed back a second time when I didn't find it on the first pass ("I'm positive we decided... please look again"). On the second, more careful read, the text was there and unambiguous.

**What this means concretely:** a task file I trusted as accurately scoped wasn't, I didn't independently verify it against the architecture doc before dispatching, and it took two rounds of you catching it — not the process — to fix. Task `08` was reopened, redone (Round 2 — file discovery for project/user/plugin sources actually removed now, not just frozen), re-verified live by both the worker and independently by me, and merged. But the underlying pattern — trusting an inherited task file's stated scope rather than re-deriving it from primary sources every time — is the thing worth you actually reviewing, not just this one instance of it.

### 3. A second instance of the same pattern, caught before shipping — task `13`, deferred

While confirming task `08`'s "leave the `nanite-native` adapter alone" carve-out (itself inherited from Phase 0 item `16`'s reasoning) at your request, found that `internal/plugin/builtin/adapter-nanite-native/plugin.go` treats `.nanite/config.yaml` — the dev-boot-persona convention `GLOSSARY.md` explicitly says does *not* participate in Nanite's runtime agent system — as a real runtime-agent source anyway, unconditionally overwriting real `agent_profiles` rows on every boot via a path neither round of `08` touched. Confirmed live in two separate dogfeed logs. You asked to defer this rather than fix it tonight, since you suspect (and I have no way to confirm or deny from the committed docs alone) that this was already resolved somewhere and the resolution didn't make it into what actually shipped. Filed as `TASKS/phase-1/13-review-nanite-native-adapter-config-conflation.md` with full findings, explicitly not dispatched.

### 4. My own process incident, caught late — a live-dogfeed write landed on a real tracked file

While independently re-verifying `02`'s `activation_mode` change during Wave 2's live-dogfeed checkpoint, I `PUT`'d a real change against `agent-builder` (a file-discovered agent) on a scratch server run from this worktree's own directory. `writeManaged`'s relative `source_ref` write resolved against the process's real CWD, not the scratch DB path — the exact same footgun task `08`'s Round 1 worker had already hit and documented in `ESCALATIONS.md`, with a standing recommendation I didn't apply to my own, differently-shaped verification (a live server, not a `go test` run). It sat as an uncommitted, uncaught working-tree change through several subsequent commits until a routine `git status --short` just now caught it, while preparing this pause. Reverted cleanly, nothing was ever committed, no data lost — but it should have been caught immediately, not several commits later. Logged as its own `ESCALATIONS.md` entry with a concrete follow-up: the standing advice needs to actually move into `EXECUTION-PROCESS.md` itself, not just sit in this log where it's easy to miss a second time.

### 5. A standing rule that should have been established from the start — no frontend work

You corrected task `09`'s scope tonight: no frontend work in any phase, backend/API only. I don't know whether this was already decided earlier and lost the same way the file-storage decision was, or whether it's a new instruction — I found nothing in the architecture docs or decision log one way or the other. Rescoped `09` to API-only (full rationale in the task file), and flagged the same concern in Phase 5's Cards section (real React component work, per this repo's own `CLAUDE.md`) as a heads-up for whoever picks that up — not corrected there, just flagged, since that section isn't Phase 1's to edit and I haven't reviewed it in detail.

## The pattern worth your review

Three separate times tonight, something already written into a task file, an architecture doc's carve-out, or an earlier phase's stated decision turned out not to match what you actually intended, and in two of the three cases I initially took the written version at face value instead of catching the mismatch myself. You've said you suspect this traces back further than tonight — to wherever the original two-day architecture review's conclusions got captured (or didn't) into `docs/engineering/architecture/*.md` and `docs/architecture-decision-log-2026-08-17.md`. I can't resolve that question from inside this branch; it needs your own pass through the source material.

## What I'd flag for your review, concretely

- **Everything task `08`/`09`/`13` touch or reference** — the file-storage decision and the frontend-scope decision, specifically, since both turned out to be mis-captured somewhere upstream of this session.
- **Phase 0's remaining tail** (its own `INDEX.md` section, not this branch's) — worth a similar trust-but-verify pass given the `#10` status mismatch found here; I only checked the one item that happened to intersect with my own work.
- **Whether the pattern extends to Phases 2-6's already-written task files** — none of them have been touched or re-verified against primary sources this session; they're exactly as the original planning session left them.

## Resuming from here

Nothing is blocking a resume whenever you're ready — `08`, `12` are merged and verified; `09` is rescoped and ready to dispatch once you're satisfied with the scope; `11` has no unmet dependency; `13` waits on your direction. If your review changes anything else already merged (`01`-`07` in particular, all landed before tonight's corrections and not re-audited against them), say so explicitly rather than assuming I'll catch it on my own — that's exactly the gap tonight surfaced.
