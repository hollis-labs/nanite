# Where we are (2026-08-20, mid-day)

Picking this up after compaction: read this whole file first, then the
pointers it names. This replaces the 2026-08-19 version — everything below
is current as of a fresh check just before this doc was written.

## Headline: further along than it might look from the last message in chat

While you were mid-conversation saying "Phase 1 is under review," the
reflex-taxonomy Orchestrator kept working. By the time this doc was
written, **the entire batch — both of its internal sub-phases, plus one
review-driven fix task — is done, reviewed, and committed.** Only the one
task that was always meant to stay parked (`07`) is still untouched. See
"Reflex Action Taxonomy" below for the full picture, and note the one
real actionable item: **the work is committed but sitting in an open,
unmerged PR (#263) on branch `reflex-action-taxonomy`, not on `main`.**

## Headline status — Phase 0-9 (the original architecture-review sequence)

- **Phase 0, Phase 1**: done, merged to `main`.
- **Phases 2-5**: executed and `reviewed` (closed). Real bugs were found
  and fixed live during dogfeed validation, not just build/test.
- **Two of the three former "loose ends" from Phases 2-5 are now also
  closed**: `TASKS/adhoc/01-eliminate-file-based-agent-runtime.md` and
  `TASKS/adhoc/02-remove-tool-permissions-collapse-to-agent-tools.md` both
  landed and were live-reverified (see git log: `603e3287`, `a1c7c632`,
  `e346e926`, `45ff539f`). **Still open, still need your review:**
  - `TASKS/phase-2/07-audit-agent-roster.md` — `not-started`, a planning
    deliverable held for your review before any implementation dispatch.
  - `TASKS/phase-5/06-make-http-middleware-plugin-extensible.md` —
    `not-started`, a real unresolved design decision (where plugin
    middleware may sit relative to the security-ordered chain), see
    `TASKS/ESCALATIONS.md`.
- **The CLI-vs-API question is resolved, decision changed the task
  itself**: a separate review found the two paths much closer than
  originally assumed. Decision: keep both, app default is **CLI**,
  overridable system-wide (new `UserSettings.DefaultRuntimeKind`) and
  per-agent (existing, already-nullable `agent_profiles.runtime_kind`).
  Old task (`phase-8/01-cli-vs-api-experiment-durable-agents.md`, which
  required live production sign-off) was renamed and rewritten to
  `TASKS/phase-8/01-set-default-runtime-kind.md` — no sign-off gate
  anymore, it's an ordinary settings feature now. Architecture docs
  `00-overview.md`/`02-agent-launching.md` updated to match.
- **Phases 6, 7, 9**: not started, unchanged.
- **Phase 8**: `01` re-scoped as above; `02`-`05` not started; `06`
  (new, this session) and `07` (new, this session) added — see below.

## Reflex Action Taxonomy (`TASKS/reflex-taxonomy/`) — DONE, not yet on `main`

Outside the Phase 0-9 sequence (same treatment as `TASKS/adhoc/`).
Implements `docs/engineering/architecture/10-reflex-action-taxonomy.md`,
the design from a dedicated 2026-08-19 architecture-review session with
you directly (`TASKS/phase-4/10-reflex-architecture-review.md`, design
complete, no code changed at design time).

**Current real state, verified against git/INDEX.md/the DB just before
writing this doc:**
- All 7 real tasks (`01`-`06`, plus `08` — a review-driven fix task, see
  below) are `reviewed` and closed. `07` (harness-reactive self-tools
  design session) remains correctly parked/untouched — that was always
  the plan, not a gap.
- **Committed**: `fe3923b0`, "Reflex Action Taxonomy: taxonomy schema,
  shared decision engine, halt fix, telemetry" — 44 files, both new
  migrations (`124`, `125`) applied and verified live against the real
  production DB (not just a test fixture).
- **Validated twice**: once by the Orchestrator's own live dogfeed
  against the deployed `nanite-api-service`, once by a fresh Reviewer
  with no shared context with the implementing workers, across two
  review rounds (Phase 1: `01`-`04`, one real finding fixed as `08`;
  Phase 2: `05`/`06`/`08`, zero blocking findings).
- **One real finding, fixed same-day**: `08-fix-resolve-fail-open-visibility.md`
  — the shared decision engine's per-kind lookup silently fell back to
  `all_applicable` with zero logging on failure, which could have
  silently regressed `halt_session` back to "doesn't actually preempt"
  (the exact bug this whole batch exists to fix) with no operator-visible
  signal. Fixed: fallback behavior unchanged, logging added.
- **One non-blocking follow-up, not fixed, captured so it isn't lost**:
  `ApprovePendingReflex` bypasses the new provenance-tier gate (writes
  directly to `agent_reflexes`, skips the write-time check). Harmless
  today — every action kind currently allows `operator` tier, so it
  always lands on an already-permitted combination — but latent if the
  allow-list is ever tightened. Logged in `TASKS/ESCALATIONS.md`'s final
  entry and Vanta memory
  (`user/chrispian/memory/followups/reflex_taxonomy_approve_pending_reflex_provenance_gate_bypass`).
  Needs a real task whenever `pending_reflexes` next gets attention.

**What this actually fixed**: two `force_tool_choice` reflexes naming
different tools can no longer both fire with contradictory directives;
`halt_session` now actually stops the current turn synchronously, before
it reaches the LLM, instead of only stamping a DB flag a later request
checks.

**The one thing that still needs doing**: this is all on branch
`reflex-action-taxonomy`, one commit ahead of `main`, with **PR #263
open, not merged**. `TASKS/reflex-taxonomy/HANDOFF-TO-NEXT.md` and
`PHASE-SUMMARY.md` (written by the batch's own doc-writer) both warn
"code not yet committed" — that warning is now **stale**, the commit
landed after those docs were written. Don't be confused by it; trust
this file and a fresh `git log`/`gh pr view 263` instead.

**Full detail** (verification steps, gotchas, the deliberate behavior
narrowing on `first_applicable` fallback): `TASKS/reflex-taxonomy/HANDOFF-TO-NEXT.md`.
**Operator-facing summary**: `TASKS/reflex-taxonomy/PHASE-SUMMARY.md`.

## Orchestrator boot process — a real bug found and fixed this session

First attempt at booting the reflex-taxonomy Orchestrator produced a
**nested Orchestrator** — the booted session spawned another `orchestrator`
subagent to do the actual work, instead of dispatching `worker`/`reviewer`/
`research-auditor`/`doc-writer` directly itself.

**Root cause, confirmed with you directly**: your workflow boots these as
a **plain session with only the kickoff-prompt text pasted in** — no
`.claude/agents/orchestrator.md` system-prompt attachment, no custom
agent-type selection. The kickoff prompt previously assumed that file's
roster/guardrails were already loaded as system config; they weren't, so
the fresh session had no real roster and improvised.

**Fixed in both files** (`docs/engineering/orchestrator-kickoffs/reflex-taxonomy.md`
and the master `docs/engineering/ORCHESTRATOR-KICKOFF-TEMPLATE.md`, so
future Phase 6-9 kickoffs inherit the fix too): the prompt now tells the
session to read `.claude/agents/orchestrator.md` itself as its literal
first action, inlines the four-item roster (`worker`/`reviewer`/
`research-auditor`/`doc-writer`) directly in the text so it isn't solely
dependent on that read, explicitly forbids spawning another `orchestrator`
or any "coordinate the tasks" general-purpose agent, and tells it to stop
and report to you rather than improvise if the roster isn't actually
available. Also flagged: the repo's separate "Boot `<agent>`" text
convention (`.nanite/config.yaml`) doesn't reach `orchestrator` at all —
don't use it for this role.

The retry (documented above) worked correctly with the fixed prompt —
this pattern should now hold for future kickoffs too, but worth watching
the first Phase 6-9 boot to confirm.

## New Torque tasks this session (project `PRJ-20260417-0002`)

All created 2026-08-19/20, all real `todo`/manual (won't auto-dispatch
until promoted):

- **`CW-20260819-0002`** — audit the test suite for false-confidence
  coverage. Grounded in three real examples already found: a Torque task
  (`CW-20260512-0055`) marked `done` naming tests that don't exist
  anywhere in the codebase (verified via grep + `git log -S`, zero
  hits); the `dev_grep` divide-by-zero panic that live dogfeed caught but
  `go test ./...` never did; Phase 0 `#10`'s false "implemented" status.
- **`CW-20260819-0003`** — audit local dev tooling (lint/idioms/static
  analysis/security scanning). Already has real findings banked in its
  own description: `make lint` runs a real pipeline (`go vet` +
  uncapped `golangci-lint` + `staticcheck` + `errcheck` + `govulncheck`),
  `gosec` is already enabled, `modernize`/`gocritic`/`nilaway` are
  disabled-with-stated-reasons and worth revisiting, and **nothing found
  actually enforces any of this** — no `.github/` CI, no local pre-push
  hook despite a Makefile comment implying one should exist.
- **`CW-20260819-0004`** — build a Nanite-native scheduler. Real
  motivation, not speculative: Nanite runs durable agents/workflows but
  has zero in-process recurring/cron capability, and you explicitly don't
  want a hard dependency on Cerberus/launchd for something this core.
  Needs a library-vs-build decision (candidates: `robfig/cron`,
  `go-co-op/gocron`, `hibiken/asynq`, `reugn/go-quartz` — evaluate against
  the single-binary/SQLite-only constraint) and a DB-backed
  failure/retry-strategy design.
- **`CW-20260819-0005`** — build two periodic durable audit agents
  (code-quality, security — kept separate per your instruction). Depends
  on `0003` (rubric source) and `0004` (trigger mechanism). Findings file
  as `needs-review`-tagged Torque tasks with required dedup logic, not
  straight to `todo`. **Real infra note found while scoping this**:
  creating a real Nanite durable agent is a documented 9-phase build
  (`.nanite/agents/agent-builder.md`) ending in a real Cerberus redeploy
  — not a lightweight file write. Deferred actually building these;
  this task just captures the scope.
- **`CW-20260819-0006`** — verify whether "FU-30"/"FU-43" (referenced in
  `.nanite/agents/torque-task-writer.md`'s boot procedure as expected
  future scheduled-wake capabilities) already cover some or all of
  `0004`'s ground, before building from scratch. Couldn't locate either
  ID from this machine — you said you're pretty sure there's a real
  reason for the reference. Check before `0004` starts real design work.

Also fixed two things while doing the above: corrected `CW-20260512-0090`
(a real, already-existing backpressure task) with an accurate line
reference and removed a false claim I'd made about a second cap
(`CW-20260512-0055`'s `MaxConcurrentLongLived`) having decided semantics
— that feature doesn't exist in the codebase despite the task being
marked `done`. Flagged that discrepancy directly on `CW-20260512-0055`
itself rather than silently fixing my own note and moving on.

## Resequencing reference (Phase 2-9 layout, unchanged from before)

- Phase 2 — Clean-up (7 tasks) · Phase 3 — Compaction & Recovery (3) ·
  Phase 4 — Steering & Reflex Migration (10, incl. new `10-reflex-architecture-review`) ·
  Phase 5 — Plugins & Registers (8, `06` skipped) · Phase 6 — Envelopes &
  Cards (6) · Phase 7 — PTY Rename (1) · Phase 8 — Test, Review, Verify
  (7, incl. new `06-build-test-harness-adversarial-suite` and
  `07-telemetry-buildout`) · Phase 9 — Final Clean-up (1).

`TASKS/INDEX.md` is the live tracker — always re-read fresh, don't trust
anything cached from a prior session, including this doc.

## How we've been working — process notes worth carrying forward

- **"This code is real, working, and was carefully designed" is never
  grounds to keep something a locked decision already covers cutting** —
  hit this mistake once, corrected by you, saved as a Vanta feedback
  memory (`user/chrispian/memory/feedback`).
- **The flip side, equally real this session**: a locked *design* doc
  (like the reflex taxonomy) is also not something a worker should
  reopen just because reality doesn't quite match its stated facts —
  correct the record and proceed with the design's actual decision.
  Genuine surprise (reality vastly different in a way that changes
  what's safe to build) is still real grounds to stop and escalate.
  This distinction got written explicitly into the reflex-taxonomy
  kickoff prompt as "the one rule that matters more than usual."
- **Verify claims against real code, not against a task's own Work
  Log or Torque description** — this session found two separate
  instances of "marked done, doesn't actually exist in code"
  (`CW-20260512-0055`, and historically Phase 0 `#10`). Grep/read the
  actual current code before trusting a "done" claim that matters.
- **Boot-prompt-only sessions need to be fully self-contained** — the
  nested-orchestrator bug this session is the concrete lesson: never
  assume a referenced file's guardrails are "already loaded" unless
  you've confirmed how the session actually gets its configuration.
- **Prefer `torque_task_transition` → `abandoned` over hard delete**,
  always with a `torque_comment_add` explaining why first.
- Real, hard-to-reverse or judgment-heavy decisions went through
  `AskUserQuestion` (CLI-vs-API framing, periodic-agent trigger
  mechanism, agent-build scope) rather than being guessed. Routine
  mechanical work didn't wait for per-item confirmation.
- Large mechanical/repetitive passes and read-only research were forked
  or dispatched as subagents to keep bulk tool-call noise out of the
  main conversation — including a dedicated `research-auditor` telemetry
  audit this session, whose findings directly grounded `phase-8/07`.

## Key pointers

- `TASKS/INDEX.md` — live status tracker, always re-read fresh.
- `TASKS/ESCALATIONS.md` — escalation log, check before re-raising
  something; final entry is the `ApprovePendingReflex` finding above.
- `TASKS/reflex-taxonomy/` — the new batch; `HANDOFF-TO-NEXT.md` and
  `PHASE-SUMMARY.md` have the full detail (note the stale "uncommitted"
  warning in both, corrected above).
- `docs/engineering/orchestrator-kickoffs/reflex-taxonomy.md` and
  `../ORCHESTRATOR-KICKOFF-TEMPLATE.md` — both fixed this session for
  the nested-orchestrator bug.
- `docs/engineering/architecture/10-reflex-action-taxonomy.md` — the
  reflex taxonomy design doc, now the canonical reference for how
  reflexes resolve/preempt.
- Branch `reflex-action-taxonomy`, PR #263 (open, unmerged) — the actual
  code this batch produced.
- Torque: project `PRJ-20260417-0002`; six new tasks this session,
  `CW-20260819-0002` through `-0006`; tagging conventions at
  `/Users/chrispian/dev/hollis-labs/apps/torque/docs/task-tagging-conventions.md`.

## What's next

Most concrete open item: **merge or otherwise resolve PR #263** so the
reflex-taxonomy work is actually on `main`, not stranded on its branch.
Beyond that: your own call on the two remaining Phase 2-5 loose ends
(`phase-2/07`, `phase-5/06`), whether to book `reflex-taxonomy/07`'s
design session, and whatever you were preparing to bring to this
conversation before compaction.
