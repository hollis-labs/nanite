# `TASKS/INDEX.md` section template — one per batch

`docs/engineering/EXECUTION-PROCESS.md`'s "Index format" section defines the
minimal per-task row shape. In practice, every sibling batch adds a full
section (not just rows) to `TASKS/INDEX.md`, shaped like this. This is the
Orchestrator's own running record — it starts as a copy of the batch README's
task table and gets updated in place as work lands, growing a narrative
underneath the table as sections complete.

```markdown
## <Batch Name> (`TASKS/<batch-name>/`, outside the Phase 0-9 sequence)

<One paragraph: what this implements, which design doc, when planned, sibling
batch list. Usually near-identical to the batch README's own opening
paragraph — keep them in sync if the README changes.>

| Task | Phase | Status | Depends on |
|---|---|---|---|
| `01-slug` | 1 | not-started | none |
| ... | | | |

<Any real corrections restated with citations — same content as the README's
own corrections section, kept in sync.>

<Any real scope-fence items worth restating.>

**Planned <DATE>, not yet dispatched.** Per `EXECUTION-PROCESS.md`'s Phase A
discipline, this is the planning checkpoint — present to the operator for
review before any worker is dispatched.
```

## As work actually lands, this section grows a narrative — the real, load-bearing part

Every phase/section's completion gets its own paragraph, appended in order,
never overwriting the earlier ones — this is what lets a later reader (a
kickoff-prompt author checking a "previous phase" is real, an Orchestrator
picking the batch back up after a gap) reconstruct exactly what happened
without reading raw git log. The strongest real examples of this narrative
include:

- **What was validated, and how** — not just "tests pass," but what was
  actually exercised: a real dogfeed against a booted instance, a direct
  `sqlite3` inspection confirming row counts/values, a real subprocess plugin
  installed and reloaded live. Passive claims ("should work") don't belong
  here; only things actually done and observed.
- **What a fresh reviewer independently re-verified**, not just what the
  worker claimed — "reviewer traced X by hand," "reviewer independently
  re-grepped for Y," "reviewer ran its own live dogfeed including a kill-9,"
  are the load-bearing phrases. A review note that only restates the worker's
  own Work Log hasn't actually reviewed anything.
- **Every real bug found and fixed**, with the fix-as-new-worker-task
  discipline explicit — a reviewer finds something, it becomes a new numbered
  task (not an inline patch), gets independently re-reviewed, and *that*
  closes the loop. This project's task numbering has real gaps and
  letter-suffixed tasks (`05a`, `08`, `09`) exactly because of this pattern —
  that's expected, not a numbering error to "clean up."
- **The final closing line for the batch**: "All N tasks in the <Batch> batch
  (`01`-`NN`) are now `reviewed`. The batch is complete," or the honest
  partial-completion equivalent if that's the real state (see
  `filesystem-snapshots` for a real example of a batch that stalled partway
  through with its own status accurately reflecting that — 1 of 3 tasks
  `implemented`, 2 `not-started`, and the index section says so plainly rather
  than reading as more complete than it is).

## A section that goes stale mid-batch

If a batch pauses (a real cross-batch collision risk, an operator decision
still pending, a dependency not yet landed), the section should say so
explicitly with the real reason and date, and — this has happened — a later
"Update (<DATE>)" paragraph should be appended when the situation changes,
rather than editing the original note to read as if it were always current.
The goal is a section a reader can trust as an honest timeline, not a single
mutable snapshot that silently loses history.
