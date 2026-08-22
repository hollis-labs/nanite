# Architecture doc `## Status` block

Every `docs/engineering/architecture/NN-name.md` that's meant to become a
`TASKS/<batch-name>/` batch needs this section — usually the last section in
the doc. It is the single fact a kickoff-prompt author checks before writing
anything, and its absence is a real, load-bearing gap, not a formality.

## Why this exists

A kickoff prompt's job includes confirming the batch is actually authorized
before dispatching workers. Without a machine/agent-checkable sign-off line,
that confirmation has nothing to check — the kickoff-prompt author is left
inferring approval from "the design doc exists and looks thorough," which is
not the same thing and has already produced a real gap once (a batch whose
design doc had no `## Status` section and no sign-off language anywhere,
discovered only when a kickoff prompt was being written against it — the fix
was a mandatory pre-flight paragraph telling the Orchestrator to confirm
approval with the operator directly before proceeding, rather than silently
treating thoroughness as authorization).

## Template

```markdown
## Status

<One of the two shapes below, depending on how the design was reached.>

**Shape A — a locked decision with named forks resolved:**
Design discussed and aligned with the operator <DATE>, including explicit
resolution of the <N> load-bearing forks (<name each one briefly>). No code or
schema changed. Implementation is deferred to follow-up work<, not yet filed |
— see TASKS/<batch-name>/>.

**Shape B — approved-for-implementation after a corrected framing:**
**Approved for implementation, <DATE>** — <one sentence naming what changed
between the original framing and what got approved, if anything did>. <What
this unlocks> is real, scoped, sequenced work, tracked under
`TASKS/<batch-name>/` (see that folder's `README.md` for the task breakdown).
```

## What a kickoff-prompt author actually checks

1. Does a `## Status` section (or equivalent — some docs fold this into their
   opening paragraph instead, which is acceptable as long as the content is
   real) exist at all? `grep -n "^## Status\|signed-off\|sign-off\|approved for
   implementation\|operator" docs/engineering/architecture/NN-name.md` is
   usually enough to find it or confirm its absence.
2. Does it name a real date and, ideally, what specifically was resolved (not
   just "approved" with no substance — a doc that says "approved" but never
   says what forks or open questions got closed is weaker evidence than one
   that names them)?
3. If it's missing entirely: write a mandatory verification paragraph into the
   kickoff prompt (see `06-orchestrator-kickoff-template-sibling-batch.md`'s
   own "Verify before dispatching" section for the exact pattern) rather than
   silently assuming approval or silently blocking. The Orchestrator should be
   the one to confirm with the operator, not the kickoff-prompt author
   guessing on their behalf.
4. If real work has already landed against this design doc (a task marked
   `implemented` in `TASKS/INDEX.md`, real commits in `git log`), that's
   concrete evidence the batch *was* approved and dispatched at least once —
   treat resuming an in-progress batch differently from authorizing a fresh
   one. Don't re-gate something that's already demonstrably underway.
