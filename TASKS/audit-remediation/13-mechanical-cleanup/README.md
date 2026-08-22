# 13 — Mechanical cleanup

This group covers the remediation guide's Wave 8 ("Mechanical cleanup") —
the guide's own list of what belongs here is explicit: misspell findings,
staticcheck quickfixes, confirmed dead code, stale history/task comments,
stale package docs, the gofmt backlog, trivial naming issues, obsolete
agent artifacts, deprecated APIs, and accepted/noisy linter exclusions. It
deliberately batches 34 individually small findings into 5 task files
grouped **by type** — dead code, comments, naming/formatting,
error-handling, and a genuine grab-bag of hygiene items — rather than
producing one task per finding. This follows the remediation guide's own
explicit instruction ("Do not create one remediation task per raw analyzer
occurrence. Collapse occurrences into underlying causes where evidence
supports it") and its Wave-8 framing ("Batch mechanical changes separately
from semantic changes"). Several findings in this folder don't actually
share a root cause with their batch-mates — the task files say so plainly
where that's true (most notably `05-low-risk-hygiene-and-lock-scope-batch.md`,
which is an explicit grab-bag) rather than forcing a fake shared narrative
just to justify the grouping. The grouping principle here is closer to
"same disposition-decision shape" (all confirmed-dead, all comment-only,
all naming/formatting, all silent-error, or all genuinely too-small-to-split)
than "same underlying defect."

**Scheduling note, restated from the guide's own dependency ordering
(`docs/engineering/...` guide §5's default flow ends `... → quality
enforcement → mechanical hygiene`) and the audit-remediation README's wave
table:** this folder should run **last**, after every semantic/architectural
task in folders `01` through `12` has landed. Nearly every task file in this
folder touches files that earlier-wave tasks (security fixes, lifecycle
fixes, duplication consolidation, island wire/defer/retire decisions) also
touch — running mechanical cleanup first risks trivial merge conflicts with
substantive work, and running it concurrently with semantic changes in the
same files makes review harder for no benefit, since none of this work is
release-blocking. Two task files (`01`, `05`) contain a small number of
`requires_architect_decision: true` items that are genuine exceptions to
"purely mechanical" — those specific rows should still wait for the
Wave-8 scheduling slot even though they need a decision, since none of them
is urgent enough to jump the queue.

## Task files

- **`01-confirmed-dead-code-removal.md`** — 8 findings (`GO-STORE-008`,
  `GO-AGENT-003`, `GO-MEM-003`, `GO-MEM-007`, `GO-MEM-008`, `GO-SVCCORE-007`,
  `GO-MCPTOOL-005`, `GO-CHAT-005`), all `deadcode`-tool-confirmed. Split into
  three buckets: clean removal candidates, documented-as-intentional
  scaffolding needing an explicit keep-or-remove decision, and one
  genuinely no-action item recorded so a future pass doesn't re-flag it.
- **`02-stale-comments-and-docs-cleanup.md`** — 6 findings (`GO-STORE-009`,
  `GO-SVCCORE-009`, `GO-SVCEXEC-007`, `GO-RUNTIME-006`, `GO-CHAT-006`,
  `GO-SVCCORE-003`). A scoped trim pass for bare task-ID/path citations, not
  a blanket removal — most of this cluster's comments are explicitly
  load-bearing invariant documentation per the audit's own repeated caution.
  Two findings (`GO-SVCCORE-003`, `GO-CHAT-006`) are comments making a
  factually false claim about what the code does, not just long comments —
  those need a real fix-the-code-or-fix-the-comment decision.
- **`03-naming-and-formatting-fixes.md`** — 3 findings (`GO-SVCCORE-008`,
  `GO-AGENT-004`, `GO-CHAT-007`), all trivial and unambiguous: a builtin-type
  shadow, a stale test filename, and the repo-wide gofmt backlog (with the
  option to clear all 122 failing files in one step, not just this
  cluster's 12).
- **`04-low-risk-error-handling-batch.md`** — 5 findings (`GO-INFRA-003`,
  `GO-API-010`, `GO-SEC4-010`, `GO-SVCEXEC-006`, `GO-MCPTOOL-013`) sharing a
  "silently discarded or unlogged error" pattern across unrelated files.
  `GO-SVCEXEC-006` gets priority within the batch since its silent failure
  undermines a documented double-fire-race guarantee, not just observability.
- **`05-low-risk-hygiene-and-lock-scope-batch.md`** — 12 findings
  (`GO-MCPTOOL-009`, `GO-PLUGIN-005`, `GO-AGENT-005`, `GO-MEM-004`,
  `GO-MEM-005`, `GO-MEM-006`, `GO-MEM-009`, `GO-CHAT-003`, `GO-CHAT-008`,
  `GO-CHAT-009`, `GO-PLUGIN-004`, `GO-RUNTIME-008`), the true grab-bag: no
  shared root cause, explicitly stated as such. Two items are heavier than
  the rest — `GO-PLUGIN-004` carries a real, traced TOCTOU concurrency bug
  (with an option flagged for a planner to split that portion into its own
  task later) and `GO-RUNTIME-008` is purely an administrative
  add-to-the-tracking-list item requiring zero code change.
