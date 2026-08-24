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

The original plan placed this folder after Wave 8, with `13/03` (the repo-wide
`gofmt` sweep) still the final commit of the entire batch per AD-22. The
operator subsequently approved tasks `01` and `02` for inclusion in Wave 8;
task `02` is implemented there and `13/03` must still land after it.

Neither task here is gated on an open architect decision. Both are gated on
their wave's predecessors only.

## Tasks

| Task | Source | Why it had no owner |
|---|---|---|
| `01-remove-default-seeded-catalog-source.md` | **AD-05** | Reviewed in the operator-approved Wave 8 window with guarded migration `147` |
| `02-error-handling-backlog-paydown.md` | **AD-21** | Implemented in Wave 8: paid the measured 281/48/24 correctness backlog to zero and activated Stage 2 |
| `03-test-fixture-migration-cost.md` | promoted candidate 4 | Completed and independently reviewed ahead of Wave 6; full-repo race gate now passes |

## Candidate register

`TASKS/ESCALATIONS.md` records **thirteen** follow-up candidates as of 2026-08-24;
ten remain open and three are now closed.
Listed here so they are visible in one place rather than only in a chronological
log. Open candidates remain operator-controlled because each was deliberately
judged out of scope by the review that found it.

1. **Leftover downloaded archive in every plugin install directory** (Wave 1,
   `01/01` re-review). `HTTPDownloader` writes the archive inside `targetDir`
   and nothing deletes it post-extraction. A regression introduced by AD-04's
   convergence — the pre-convergence handler downloaded to OS temp entirely
   outside the target. Correctness/hygiene, not security.
2. **`callGrep` first-line context panic** (Wave 3, `08/02` review). A match on
   the first input line panics before confinement is exercised — which meant
   the original symlink regression could pass for the wrong reason. Fixture
   corrected; the underlying `ringLen`-before-modulo bug is not.
3. ~~**`08/08`'s deferred full race gate**~~ — **CLOSED BY TASK `14/03`**.
   The full repository race suite completed in 263.45s wall; `08/08` and
   GO-SEC-001/GO-SEC-002 are now `reviewed`.
4. ~~**Test-fixture migration cost inflating every race run**~~ —
   **CLOSED BY TASK `14/03` on 2026-08-23**. Shared template-copy fixtures
   preserve per-test database files while eliminating repeated fresh migration
   runs; both formerly blocked aggregates and `go test -race ./...` pass.
5. **246 `TODO(ctx-sweep)` markers** (`06/03`). A greppable map of every call
   site with no context plumbing at all. Newly visible work, never scoped.
6. ~~**Test isolation: `Container`-constructing tests must redirect *all*
   independently resolved stores.**~~ — **CLOSED BY `08/10`, CONFIRMED BY
   `14/03`**. Service and API package TestMain setup redirects every HOME/XDG/
   Tesseract root, and dedicated tests inspect SQLite's actually opened `main`
   path under disposable roots.
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

Closed candidates 4 and 6 were the two with leverage beyond their own line
items: one unblocked a deferred verification gate and a standing performance
complaint, while the other closed the isolation class behind the only incident
in this batch that touched operator data.

9. **`lint-goroutines` is advisory and absent from CI** (Wave 7). `12/03`
   widened its coverage to 13 packages, but the target still cannot fail
   (leading `@-`) and `.github/workflows/full-repo-quality.yml` does not run
   it. It is the only mechanism watching `internal/safego` adoption —
   `forbidigo` cannot match the `go` keyword — so nothing detects a regression
   in the lifecycle-ownership class AD-26 covered. Add it to the workflow as a
   reporting step first, per AD-21's baseline-then-ratchet posture.

10. **Skills UI fork affordances call a removed backend route** (`13/01`).
    `SkillDetailView.tsx` and `SkillsBrowser.tsx` still gate and show the
    source-based fork action, but `api.forkSkillToUser` posts to the removed
    `/api/skills/{id}/fork-to-user` route. Decide in the post-freeze UI/UX
    workstream whether to remove the stranded action/client or restore a
    supported backend workflow.
11. **Durable one-shot expiry can still be suppressed silently** (`13/04`).
    `BumpAgentScheduleFireCount` failure remains unlogged and prevents the
    expiry write from running. The audited expiry-write failure itself now
    logs; this adjacent failure is a separate control-flow/observability gap.
12. **`driveBootSession` can panic sending to a concurrently closed channel**
    (pre-existing; recurred during `13/05`). An asynchronous SendInput failure
    path checks `closed` and later sends without synchronizing the close; the
    resulting `send on closed channel` panic cannot always be attributed to a
    named test. The original 2026-08-22 escalation remains the owning defect;
    `git blame` dates it to `7a0e37936` (2026-05-19), long before this batch.
    **Its consequence changed on 2026-08-24 and this is now the highest-value
    candidate here.** `13/03`'s race gate reproduced it as a real
    `WARNING: DATA RACE` (`chansend1` at `chat_boot_drive.go:297` racing
    `sessionRouter.closeOnce`). With `12/01`'s gate live, `14/03` making the
    aggregate race suite runnable in 260s, and `14/02` activating stage 2, this
    flake will now **intermittently red CI** — which was not true when it was
    accepted as a follow-up. It is intermittent, so it will fail confusingly
    and irreproducibly rather than consistently.
13. **`internal/worker.TestShutdown` has a startup/status timing flake**
    (`13/05` review). One full race run observed `failed` rather than
    `cancelled`; the exact-tree retry passed and isolated race `-count=50`
    passed. Replace its fixed startup sleep with a deterministic barrier and
    pin the intended terminal-state transition.

## Post-remediation backlog — deliberately *after* this batch

Distinct from the candidate register above. Those are items that could still be
promoted into this batch; these are decisions that explicitly scheduled work for
**after audit remediation completes**. Neither is urgent; both are schedulable
whenever resources allow.

They live here rather than in a new file so follow-ups have one home — creating
a fourth tracking location for work-to-be-done would repeat the duplicated-data
failure this batch documented in `docs/engineering/tracking-integrity.md`.

### P1 — Split `internal/chat`'s vocabulary from its wiring (AD-31, `GO-CHAT-008`)

`internal/chat` mixes broadly-consumed wire-vocabulary types
(`Envelope`, `ResponseV1`) with subsystem-specific response-handler wiring, so
any caller wanting only the vocabulary transitively depends on an 11-package
graph spanning subagent, elicitation, mcp and plugin.

Move the subsystem-specific handlers into their own thin wiring files or a
subpackage, leaving a pure-vocabulary core. **Not attempted in Wave 8** because
it is a Wave-5-sized architecture refactor and belongs in a wave scoped for
that, with characterization tests — not inside mechanical cleanup.

Worth pairing with whatever architecture pass comes next rather than doing
alone.

### P2 — Converge the context pipeline's measurement contracts (AD-32, `GO-MEM-005` + the rest of `GO-MEM-004`)

Wave 8 fixes the *relevance* half: a shared 0–1 normalization contract across
`contextbroker`'s three sources, because merging differently-scaled values onto
one ranking is arbitrary by construction.

This follow-up finishes the job:

- **Converge the two token estimators.** `internal/context/tokens.go` uses
  `len/4` (floors); `internal/contextbroker/broker.go` uses `(len+3)/4`
  (rounds up). Both measure the same content at different stages of one budget
  pipeline. Converging needs a **new shared lower-level package**, because
  `contextbroker` deliberately depends one-way away from `internal/context` —
  that structure was disproportionate to a rounding difference inside the
  batch, but is the right end state.
- **Revisit the relevance contract** once that package exists, so normalization
  and estimation live together rather than as two disconnected edits.

The reason to do these as one pass: they are the same underlying problem — one
measurement, several uncoordinated implementations, no shared contract — and
fixing them separately means touching the same call sites twice.

### P3 — Finish `UnloadPlugin`'s teardown extraction (AD-35, `GO-PLUGIN-004`)

Wave 8 fixes the TOCTOU. This finishes the structural half: ~10 of
`UnloadPlugin`'s 18 lock-disciplined teardown categories are still hand-inlined
map-iterate-delete logic while ~8 are already extracted.

**Not attempted in Wave 8** because it is a 346-line lock-disciplined function
and restructuring it needs characterization tests — the Wave 5 method, not
cleanup-wave effort. The audit itself judged its complexity essential-dominated,
so this is consistency work rather than a defect fix.

Natural to pair with P1 (the `internal/chat` split) in whatever architecture
pass comes next.

### P4 — Badger vlog rotation for the coordination store

`internal/coordination/badger.go`'s `NewBadgerStore` uses Badger's 1 GB default
value-log size. Heartbeat and lock churn can balloon a single vlog past 2 GB
before it rotates, and **the active vlog is never GC-eligible** — so `gcLoop`
runs with nothing to reclaim.

A written fix exists and is preserved at
`~/dev/hollis-labs/nanite-worktree-archive-20260824/patches/repo.patch`:
`WithValueLogFileSize(64 << 20)` plus `WithValueThreshold(1 << 10)`, the latter
inlining small coordination payloads into the LSM so they skip the vlog
entirely.

Found during the worktree-archive audit; it originates outside audit
remediation and was never reviewed. **Review whether 64 MB / 1 KB suit this
workload before landing it** — the rationale is sound but the constants are
someone's judgment, not a measurement.

## Out of scope

**The UI/UX review is not this wave's work** and must not be folded in.
Task `01` leaves the plugin catalog with no configured source out of the
box, which is a deliberate consequence of AD-05 and needs an empty-state screen
explaining *why* rather than a blank list or an error. That belongs to the
separate UI/UX review workstream the operator has queued for after the freeze
lifts — this batch's job is to make the backend behaviour correct and to state
the UI consequence clearly enough that the review picks it up.

## Status

**In progress.** Task `03` was completed and independently reviewed ahead of
Wave 6 under its hard sequencing rule. Tasks `01` and `02` are reviewed in the
operator-approved Wave 8 execution, and Stage 2 is active.
