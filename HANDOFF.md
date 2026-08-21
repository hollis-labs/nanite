# Where we are (2026-08-21)

Picking this up after compaction: read this whole file first, then the
pointers it names. This replaces the 2026-08-20 version — everything below
is current as of a fresh check just before this doc was written.

## Headline: four sibling batches now done, three of them since the last handoff

All four "outside the Phase 0-9 sequence" batches that have been run so far
via the Orchestrator kickoff-prompt pattern are **complete, reviewed, and on
`main`**:

| Batch | Design doc | Landed | How |
|---|---|---|---|
| Reflex Action Taxonomy | `10-reflex-action-taxonomy.md` | ✅ | PR #263, merged |
| Harness-Reactive Self-Tools | `11-harness-reactive-self-tools.md` | ✅ | PR #264, merged |
| Scheduling | `12-scheduling.md` | ✅ | direct commits to `main`, no PR |
| Teams | `15-teams.md` | ✅ (just now) | direct commits to `main`, no PR |

The kickoff-prompt pattern (`docs/engineering/orchestrator-kickoffs/*.md`,
fixed for the nested-orchestrator bug back on the reflex-taxonomy batch) has
now held clean across three more runs in a row — self-tools, scheduling,
and Teams all went through worktree-isolated parallel dispatch, fresh
review, and end-of-batch doc-writer output with no repeat of that bug.

**A new "next project" is already being scoped**: two new, uncommitted
architecture docs exist in the working tree — `docs/engineering/architecture/16-agent-host.md`
and `17-acp.md` (Agent Host boundary / adopting `go-agent-wrapper`; Agent
Client Protocol as a way to drive underlying CLI agents). `00-overview.md`
has a small uncommitted edit linking both in. **These are explicitly
evidence-gathering reviews, not locked designs yet** — the doc's own words:
"Both are evidence-gathering reviews, not decisions; planning inherits an
open call, not a foregone one." No `TASKS/` folder exists for either yet.
Don't treat these as kickoff-ready the way Teams/Scheduling were — they need
a design/decision pass first, same as `15-teams.md` got before `TASKS/teams/`
existed.

## Batch detail

### Teams (`TASKS/teams/`) — DONE, landed 2026-08-21

Implements `docs/engineering/architecture/15-teams.md` — generalizes the
hardcoded three-role dispatch model into configurable N-slot organizational
shapes (routing, authority, gates) that compile to ordinary `agentworkflow`
runs rather than a new orchestration engine. All 11 tasks across 4 phases
`reviewed` and closed (`TASKS/INDEX.md`'s "Teams" section has the full
per-task detail, including two real bugs found and fixed during review —
a routing priority-floor clamp bug in task `09`, and an agent-card
registry-pollution risk in task `08`/`11` — both re-reviewed clean after
fixing). New migrations `128`-`133`. End-of-batch docs:
`TASKS/teams/HANDOFF.md`, `TASKS/teams/SUMMARY.md`.

The Teams kickoff prompt (`docs/engineering/orchestrator-kickoffs/teams.md`)
flagged five real corrections the planning session found against live code
before dispatch even started (advisory-only dispatch allowlist, an
unrelated `durable` flag, a hardcoded step-kind CHECK, the real
`message_send` tool name, a naming collision with `internal/context/slot.go`'s
existing "Slot" meaning) — all held up; no new ones surfaced during
execution beyond the two review findings above.

### Scheduling (`TASKS/scheduling/`) — DONE

Implements `docs/engineering/architecture/12-scheduling.md` — adopted
`/Users/chrispian/dev/hollis-labs/libs/go-scheduler` wholesale (full
replace of the old ad hoc ticker, not a dual-run), following Hadron's live
adapter as a template. All 9 tasks landed. Resolves Torque
`CW-20260819-0004`/`-0006`; unblocks `CW-20260819-0005` (the two periodic
audit agents — still not built, still blocked on that task's own
dependency chain).

### Harness-Reactive Self-Tools (`TASKS/harness-reactive-self-tools/`) — DONE

Implements `docs/engineering/architecture/11-harness-reactive-self-tools.md`
— moved every self-tool from `internal/mcp` to `internal/selftools`, added
the reactive layer (`selftool_reaction_kinds`/`selftool_reactions`) letting
a self-tool call trigger a configured reaction (`render_card` built;
`internal_api_call` minimally built; `external_api_call`/`callback` seeded
`implemented=false`, no execution code). All 7 tasks landed, PR #264.

### Reflex Action Taxonomy (`TASKS/reflex-taxonomy/`) — DONE, merged

Implements `docs/engineering/architecture/10-reflex-action-taxonomy.md`.
All 7 real tasks (`01`-`06`, `08`) `reviewed`; `07` (harness-reactive
self-tools design session) correctly stayed parked — it later became its
own batch above. PR #263 merged (the previous handoff's "stale, not yet on
main" warning no longer applies). One still-open, non-blocking follow-up:
`ApprovePendingReflex` bypasses the provenance-tier gate — harmless today,
latent if the allow-list is ever tightened. Logged in `TASKS/ESCALATIONS.md`
and Vanta memory; still no dedicated fix task filed.

## Headline status — Phase 0-9 (the original architecture-review sequence)

Unchanged since the last handoff — not re-verified this session, carried
forward as-is:

- **Phase 0, Phase 1**: done, merged to `main`.
- **Phases 2-5**: executed and `reviewed` (closed).
- **Still open, still need your review**:
  - `TASKS/phase-2/07-audit-agent-roster.md` — `not-started`.
  - `TASKS/phase-5/06-make-http-middleware-plugin-extensible.md` —
    `not-started`, real unresolved design decision, see `ESCALATIONS.md`.
- **CLI-vs-API**: resolved — keep both, app default CLI, overridable
  system-wide and per-agent. `TASKS/phase-8/01-set-default-runtime-kind.md`.
- **Phases 6, 7, 9**: not started, unchanged.
- **Phase 8**: `01` re-scoped; `02`-`05` not started; `06`
  (test-harness/adversarial-suite) and `07` (telemetry buildout) planned,
  not started.

## Memory & Knowledge Tools follow-ups (Torque, filed 2026-08-20)

`docs/engineering/architecture/13-memory-and-knowledge-tools.md`'s research
pass produced 8 follow-ups, all filed in Torque (`CW-20260820-0001` through
`-0008`, project `PRJ-20260417-0002`), all still `todo`/`manual`, none
promoted. Full list cross-referenced inline in that doc. Nothing further
done on these since filing.

## Key pointers

- `TASKS/INDEX.md` — live status tracker, always re-read fresh; has full
  per-task detail for all four completed sibling batches.
- `TASKS/ESCALATIONS.md` — escalation log; latest real entries are the
  Teams Phase 1/task-09/task-11 review findings (all resolved).
- `docs/engineering/orchestrator-kickoffs/` — reflex-taxonomy, scheduling,
  harness-reactive-self-tools, teams — all four used and confirmed working
  as a pattern; `ORCHESTRATOR-KICKOFF-TEMPLATE.md` is the Phase 0-9-specific
  variant (not yet exercised for Phase 6-9, still worth watching on first
  real use).
- `docs/engineering/architecture/16-agent-host.md`, `17-acp.md` — the next
  candidate batch, evidence-gathering stage only, no `TASKS/` folder yet.
  Uncommitted, along with a small `00-overview.md` edit linking them in.
- Torque: project `PRJ-20260417-0002` — `CW-20260819-0002` through `-0006`
  (test/tooling audits, scheduler [now superseded by the landed batch
  above — worth a status pass], periodic audit agents, FU-30/-43
  verification) and `CW-20260820-0001` through `-0008` (memory/knowledge
  follow-ups) all still `todo`.

## What's next

Most concrete open items, roughly in likely order:
1. **Decide whether/how to move Agent Host / ACP from review to a locked
   design** — the natural next step before any `TASKS/` folder or kickoff
   prompt can exist for it, mirroring how `15-teams.md` preceded
   `TASKS/teams/`.
2. `CW-20260819-0004` (build a scheduler) is now stale/superseded by the
   landed Scheduling batch — worth a status pass in Torque so it doesn't
   read as still-open work.
3. Your own call on the two remaining Phase 2-5 loose ends (`phase-2/07`,
   `phase-5/06`).
4. Whatever's next in the Memory & Knowledge Tools follow-up list
   (`CW-20260820-000X`) whenever you're ready to pick one up.
