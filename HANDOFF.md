# Where we are (2026-08-19, end of day)

Picking this up after compaction: read this whole file first, then the
pointers it names. This replaces the 2026-08-18 version of this file, which
described the pre-Phase-0 state — everything below is current.

## Headline status

- **Phase 0** (removal-heavy cleanup): done. Only real gap was `10-seed-
  builtin-agent-profiles` — its claimed fix never actually merged; redone as
  `TASKS/phase-2/05`.
- **Phase 1** (Agent Construction): done and merged to `main` (`c94f2f9d`).
  The `phase-1-execution` worktree is preserved on purpose (local-only, no
  remote) — don't delete it.
- **Phases 2-9 resequencing**: the original Phases 1-6 plan was reviewed and
  reorganized into a new Phase 2-9 layout (see "Resequencing" below).
  Committed at `628c5647` (the reorg) and `5d152068` (Phase 9's docs-archival
  policy, resolved by direct operator decision).
- **Phases 2-5**: executed and **`reviewed`** (closed) as of today. Real bugs
  were found and fixed live during dogfeed validation, not just build/test —
  see "Loose ends" below for what came out of that.
- **Phases 6-9**: not started. Phase 8 `01` (CLI-vs-API Curator experiment)
  has a standing live-sign-off gate — never dispatch it as part of a routine
  batch, it needs your explicit in-the-moment go-ahead every time. Phase 5
  `06` (HTTP middleware plugin-extensibility) is skipped, same reason —
  unresolved design question, see `TASKS/ESCALATIONS.md`.

## Loose ends from Phases 2-5 — needs your review before anything else moves

Three real deliverables came out of Phase 2-5 execution that need your call,
not mechanical dispatch:

1. **`TASKS/phase-2/07-audit-agent-roster.md`** — not-started, a planning
   deliverable. Explicitly held for your review before any implementation
   dispatch.
2. **`TASKS/adhoc/01-eliminate-file-based-agent-runtime.md`**
3. **`TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md`**

Also still open, not new: **Phase 5 `06`** (`make-http-middleware-plugin-
extensible`) needs an operator design decision on where plugin middleware
may sit relative to the existing security-ordered chain — see
`TASKS/ESCALATIONS.md`.

For the full blow-by-blow of what Phase 4/5 review found (real bugs: a
`tool_permissions`-vs-`agent_tools` "two systems of record" gap, hot-reload
never actually applying manifest registrations, plugin unload keyed by the
wrong identifier — all fixed as new fix-tasks mid-phase), read
`TASKS/INDEX.md`'s Phase 4 and Phase 5 sections directly — the Work Log
entries there are detailed and worth it before you make any related call.

## Resequencing reference (Phase 2-9 layout)

- Phase 2 — Clean-up (7 tasks, incl. new `07-audit-agent-roster` above)
- Phase 3 — Compaction & Recovery Events (3 tasks)
- Phase 4 — Steering & Reflex Migration (9 tasks, incl. one fix-task found
  during review)
- Phase 5 — Plugins & Registers (8 tasks, incl. three fix-tasks found during
  review; `06` skipped)
- Phase 6 — Envelopes & Cards (6 tasks)
- Phase 7 — PTY Rename (1 task)
- Phase 8 — Test, Review, Verify (5 tasks; `01` needs live sign-off at
  dispatch)
- Phase 9 — Final Clean-up (1 task; docs-archival policy already resolved)

`TASKS/INDEX.md` is the live tracker for all of this — always check it fresh,
don't trust anything cached from a prior session. `PHASE-0-1-AUDIT-FOLLOWUPS.md`
has the full reconciliation history if you need the "why" behind the
resequencing. `docs/engineering/EXECUTION-PROCESS.md` is the standing
operating procedure (Orchestrator/Worker/Reviewer roles, escalation rules,
worktree-safety requirement).

## Torque task audit & tagging (separate track, same session)

Nanite's Torque project is `PRJ-20260417-0002`. Ran a full audit + cleanup +
tagging pass across every `todo`/`backlog` task (209 total).

**Important, found the hard way**: `torque_task_list`'s `status` filter is
unreliable at scale — it silently returned 19 rows when 174 real `todo` rows
existed. **For anything that needs to be trustworthy, query the live SQLite
DB directly**: `~/.local/share/torque/workspaces/default/main.db`. Example:

```sql
SELECT t.id, t.status, t.title FROM tasks t
WHERE t.project_id = 'PRJ-20260417-0002' AND t.status IN ('todo','backlog');
```

**Closed as obsolete/superseded** (comment + `torque_task_transition
force=true` to `abandoned` — direct FSM transitions to `abandoned` don't
work, force is required and is the documented, sanctioned escape hatch for
user-initiated cleanup): the giphy/oembed plugin cluster (3 tasks, both
plugins fully cut in Phase 0), the SS1/2/3 skill-slash-command cluster (file-
based skill discovery confirmed a hardcoded no-op), a stale `MaxTurns`
caller-override task (the field it would extend doesn't exist), the boot-
profile-catalog registry-warning task (that catalog is fully retired), and a
`store.splitSQL` bug report (the function no longer exists post-goose).

**One audit closed with a real finding**: `CW-20260817-0004` (migration-
story audit) — substantially resolved by Phase 0 `#09`'s goose adoption;
filed one small follow-up, `CW-20260819-0001` (two doc updates, no code).

**Tagging taxonomy**: documented at
`/Users/chrispian/dev/hollis-labs/apps/torque/docs/task-tagging-conventions.md`
(also linked from that repo's `docs/SUMMARY.md`) — four dimensions (Kind,
Decision-state, Subsystem, Layer), designed to reuse already-established
organic tags rather than invent parallel ones. Portfolio-wide convention, not
Nanite-specific. **Applied across all 209 tasks** — 196 carry a Kind tag, the
other 13 legitimately carry only `discuss-first` (genuine open questions).

**Two things flagged for your own review, not resolved**:
- **`CW-20260430-0021`** (SS11, an ADR proposal) — tagged `needs-review`.
  Its content is likely redundant with `docs/engineering/architecture/01-
  agent-construction.md`, which already states the same boundary more
  authoritatively. Possibly closeable as superseded.
- **`CW-20260501-0009`** (SS15, deprecate `~/.nanite/reflexes/*.yaml`) —
  tagged `cleanup` + `needs-review` + `steering`. This is a **real, still-
  open gap**: reflexes are the one file-based config source our "no file
  storage except builtin seed" work never touched. Worth considering folding
  into Phase 4's reflex-migration work rather than treating as separate
  backlog.
- Six more tasks the tagging pass flagged as genuinely ambiguous kind calls
  (not guessed) — see the fork's own report in this session's transcript, or
  just re-derive from `discuss-first`/kind-tag gaps if that transcript isn't
  available: `CW-20260504-0004`, `CW-20260513-0014`, `CW-20260814-0019`,
  `CW-20260503-0022`, `CW-20260420-0040`, `CW-20260816-0089`.
- `nanite-arch-audit-202608` tag marks 19 tasks flagged during the manual
  triage pass as needing review against the current architecture (not yet
  individually resolved — todo/backlog cross-referencing, mostly the rest of
  the SS-sprint cluster and a few subagent-execution/reflex-scoping backlog
  items).
- Noticed, not acted on: the `abandoned` bucket has several exact-title
  duplicates of tasks that were also open elsewhere (different IDs) — looks
  like leftover noise from a 2026-05-18 bulk migration/dedup pass. Not
  investigated further.

**Not yet done**: the ~155 remaining `done`-adjacent/lower-priority backlog
items past what this pass covered, and the user's own planned manual sweep
of the `todo` list (in progress, separate from this).

## How we've been working — process notes worth carrying forward

- **"This code is real, working, and was carefully designed" is never
  grounds to keep something a locked decision already covers cutting** —
  hit this mistake directly once this session (the nanite-native adapter
  investigation), corrected by the operator, saved as a feedback memory in
  Vanta (`user/chrispian/memory/feedback`). Check `TASKS.md`/architecture
  docs for an already-decided cut before recommending "keep this."
- **Worktree isolation is mandatory for any parallel dispatch** — a real
  collision happened this session (a `git stash` from one concurrent agent
  swept up another's uncommitted work; recovered, but avoidable). Never
  `git stash`/`git clean` on the shared main working tree.
- **Prefer `torque_task_transition` → `abandoned` over hard delete** for
  closing obsolete Torque tasks — audit-preserving, and the tool's own
  guidance recommends it. Always post a `torque_comment_add` explaining why
  before closing, so the reasoning survives in Torque's own history, not
  just this conversation.
- **Verify claims against real code, not against a task file's own Work
  Log** — this session found multiple task files whose Work Log claimed a
  fix landed when the actual commit never merged (paperwork-reconciliation-
  without-the-real-merge pattern). Grep/read the actual current code before
  trusting a "done" claim that matters.
- Real, hard-to-reverse or judgment-heavy decisions went through
  `AskUserQuestion` rather than being guessed (docs-archival policy, tagging
  taxonomy naming, close-method for obsolete tasks). Routine mechanical work
  (file moves, comment-and-abandon on confirmed-obsolete tasks) didn't wait
  for per-item confirmation once the pattern was established.
- Large mechanical/repetitive passes (the Phase 2-9 file reorg, the 209-task
  tagging pass, read-only research audits) were forked out rather than run
  inline, specifically to keep bulk tool-call noise out of the main
  conversation — worth continuing that pattern for similarly-shaped work.

## Key pointers

- `TASKS/INDEX.md` — live status tracker, always re-read fresh.
- `TASKS/ESCALATIONS.md` — escalation log, check before re-raising something.
- `TASKS/phase-2/` through `TASKS/phase-9/` — current task files.
- `TASKS/adhoc/` — the two loose-end tasks above.
- `PHASE-0-1-AUDIT-FOLLOWUPS.md` — historical reconciliation record.
- `docs/engineering/EXECUTION-PROCESS.md` — the operating procedure.
- `docs/engineering/architecture/*.md` — target architecture, numbered to
  match the Kind/Subsystem tagging taxonomy's `agent-construction`/`agent-
  launching`/`steering`/`harness` split.
- Torque: project `PRJ-20260417-0002`; tagging conventions at
  `/Users/chrispian/dev/hollis-labs/apps/torque/docs/task-tagging-conventions.md`;
  live DB at `~/.local/share/torque/workspaces/default/main.db` for direct
  queries when the MCP layer looks untrustworthy.

## What's next

Operator has a few things ready to plan and turn into tasks — pick up from
there once this doc's been read. Likely touches the three loose-end
deliverables above at minimum.
