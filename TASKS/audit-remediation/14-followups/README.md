# 14 — Follow-ups (final wave)

The batch's closing wave. It holds work that is **real, decided, and owned by
nobody** — items that emerged during Waves 0–3 as decisions or review findings
without a task attached, plus a register of candidates the operator may promote
into tasks.

This folder exists because two things kept happening: an architect decision
would resolve into work with no task file, and a reviewer would find a genuine
defect that was correctly out of its wave's scope and correctly logged to
`TASKS/ESCALATIONS.md` — where it then sat, visible but unowned. A batch that
closes with seven live follow-up candidates in its escalation log and no
landing place for them has not really closed.

## Sequencing

**Runs last, after Wave 8** — with one hard exception: `13/03` (the repo-wide
`gofmt` sweep) is the final commit of the entire batch and must stay that way
per AD-22. So this wave lands **after Wave 7, before `13/03`**, or the sweep
gets rerun.

Neither task here is gated on an open architect decision. Both are gated on
their wave's predecessors only.

## Tasks

| Task | Source | Why it had no owner |
|---|---|---|
| `01-remove-default-seeded-catalog-source.md` | **AD-05** | Decided 2026-08-22; was filed as a `01/01` follow-up and never became a task |
| `02-error-handling-backlog-paydown.md` | **AD-21** | Stage 2's 365-finding prerequisite. `13/04` touches five files and nothing else covers it |

## Candidates — logged, not yet tasks

`TASKS/ESCALATIONS.md` carries **eight** follow-up candidates as of 2026-08-23.
Listed here so they are visible in one place rather than only in a chronological
log. Promoting any of these into a task is the operator's call; none is
promoted by default, because each was deliberately judged out of scope by the
review that found it.

1. **Leftover downloaded archive in every plugin install directory** (Wave 1,
   `01/01` re-review). `HTTPDownloader` writes the archive inside `targetDir`
   and nothing deletes it post-extraction. A regression introduced by AD-04's
   convergence — the pre-convergence handler downloaded to OS temp entirely
   outside the target. Correctness/hygiene, not security.
2. **`callGrep` first-line context panic** (Wave 3, `08/02` review). A match on
   the first input line panics before confinement is exercised — which meant
   the original symlink regression could pass for the wrong reason. Fixture
   corrected; the underlying `ringLen`-before-modulo bug is not.
3. **`08/08`'s deferred full race gate** (Wave 3). The task is `implemented`,
   not `reviewed`, by operator decision.
4. **Test-fixture migration cost inflating every race run** (Waves 2–3). The
   attributed cause of `08/08`'s deferral — a focused `internal/selftools` race
   run took 1,446.675s. This is the higher-value fix: it closes candidate 3 and
   the `internal/service` race-suite performance follow-up together.
5. **246 `TODO(ctx-sweep)` markers** (`06/03`). A greppable map of every call
   site with no context plumbing at all. Newly visible work, never scoped.
6. **Test isolation: `Container`-constructing tests must redirect *all*
   independently resolved stores.** From the `08/10` incident that wrote
   synthetic memories into the operator's real Tesseract database. The incident
   is closed and cleaned; the *class* is not.
7. **Two test-validity gaps.** `TestNetnsBridge_HostArbitraryPortStillBlocked`
   can pass for the wrong reason in an unprivileged container (`02/01`
   review), and `TestDurableAgentStopRuntimeErrorMarksFailed` is an observed
   non-reproducing event-order flake (Wave 2).

8. **`SendToSlot`/`ResolveLazySlot` have no production caller** (Wave 4). A
   seventh production island, found while wiring AD-08 and correctly scoped out
   of it — installed routing reflexes dispatch through
   `chat_reflex_dispatch`/`task_execute` and never invoke the explicit
   Team-Slot messaging path. Not one of the audit's six, so no finding, AD, or
   task covers it. Decide it the way the other six were decided; the open
   question is whether explicit `@Team Slot` messaging is intended product
   direction, which belongs with whoever owns Teams.

Candidates 4 and 6 are the two with leverage beyond their own line items —
one unblocks a deferred verification gate and a standing performance
complaint, the other prevents a repeat of the only incident in this batch that
touched operator data.

## Out of scope

**The UI/UX review is not this wave's work** and must not be folded in.
Task `01` will leave the plugin catalog with no configured source out of the
box, which is a deliberate consequence of AD-05 and needs an empty-state screen
explaining *why* rather than a blank list or an error. That belongs to the
separate UI/UX review workstream the operator has queued for after the freeze
lifts — this batch's job is to make the backend behaviour correct and to state
the UI consequence clearly enough that the review picks it up.

## Status

**Planned 2026-08-23. Not dispatched.** Created during the Wave 4 decision
pass, once AD-05 and AD-21 resolved into work with no home.
