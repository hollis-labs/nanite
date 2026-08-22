# Revalidate all 113 audit findings against frozen HEAD and set real dispositions

**Phase:** Audit remediation — Wave 0 (revalidate the baseline)
**Status:** not-started
**Depends on:** none — but **hard-gated on the dev freeze** (see Context). Do not start while other batches are still landing code.
**Blocks:** every task in `01/` through `13/`. This is the batch's dispatch gate. Also blocks **AD-01 through AD-04**, which the operator decides against this task's interim critical/high report — see "Interim report required" under What to do.
**Parallelizable with:** `00/02` (different tooling; see that task's Non-goals for the `findings.json` merge protocol).
**Touches:** `TASKS/audit-remediation/findings.json` (the working tracking view — the *only* file this task writes structurally), and the `## Context` sections of any task file under `01/`–`13/` found to carry a stale pointer or superseded premise. **No production code changes whatsoever.**
**requires_architect_decision:** false — this task *populates* the architect-decision queue with real evidence, it does not resolve any decision in it.
**requires_security_review:** false
**requires_regression_test:** false — this task adds no behavior.

## Context

### Findings addressed

All 113. Not by fixing them — by deciding, for each, whether it still needs
fixing. This task produces the remediation guide's §7 output-format **C
(finding disposition table)** in its `findings.json` form.

### Root cause

The audit ran against commit `8feeee5c`. The task-creation pass that produced
`TASKS/audit-remediation/` deliberately did **not** revalidate against current
source — it says so explicitly in its own README ("Audited commit vs. current
state — Wave 0, not yet done"). So every one of the 113 findings carries
`disposition: remediate` as a *placeholder*, and every task file's `file:line`
citations are as of `8feeee5c`, not as of HEAD.

Measured drift at planning time (2026-08-21):

```
$ git diff --stat 8feeee5c..HEAD
 156 files changed, 24891 insertions(+), 539 deletions(-)
$ git log --oneline 8feeee5c..HEAD | wc -l
      40
```

That drift is concentrated in audited packages — `internal/store/skills.go`
alone gained 275 lines, and migrations `136`/`137` landed. `internal/service/`
(17 findings), `internal/api/` (11 findings), and `internal/store/` (10
findings) are the three most-audited packages *and* three of the most-churned.

### Current behavior

`TASKS/audit-remediation/findings.json` — `catalog_kind:
"working-remediation-view"`, `finding_count: 113`, `commit: "8feeee5c"`. Every
entry has the three tracking fields the task-creation pass added:

```json
"task_file": "TASKS/audit-remediation/04-container-reaper-lifecycle/01-....md",
"disposition": "remediate",
"task_status": "not-started"
```

Verified at planning time: **113 of 113** entries have
`disposition: "remediate"`. Severity distribution: 3 critical, 8 high,
33 medium, 45 low, 24 informational. **44 of 113** carry
`requires_architect_decision: true`.

### Desired invariant

**Every one of the 113 findings carries a disposition that a human actually
decided, backed by a look at current source — and no task in this batch is
dispatched against a finding that is already fixed.**

Concretely, after this task:

1. Zero findings remain on the placeholder value *by default*. `remediate` is
   still a legitimate outcome — it just has to be a decision, evidenced in the
   entry's `revalidation_note`, not a leftover.
2. The 3 critical and 8 high findings each carry an explicit, individually
   evidenced re-confirmation that they are still open (or a specific commit
   citation showing they were fixed). These are what justify the dev freeze;
   they get the most scrutiny.
3. Any task file whose premise evaporated is marked so at the top of its own
   file, not silently left looking dispatchable.

### Scope

- Read current source for each finding's cited `files`/`symbols`.
- Write `disposition` + a new `revalidation_note` field per finding in
  `TASKS/audit-remediation/findings.json`.
- Amend task-file `## Context` sections where citations are stale.
- Append the `## Outcome (<DATE>)` section to
  `00-revalidate-baseline/README.md`.

### Out of scope — and why

- **Re-auditing.** The guide is explicit: *"Do this quickly; do not re-audit
  the whole repository."* If you notice a new defect that the audit missed,
  record it in `TASKS/ESCALATIONS.md` as a new finding — do **not** add it to
  `findings.json`, which is a frozen-schema catalog of *this* audit's output
  (guide §10: *"record new findings separately rather than rewriting
  baseline"*).
- **Fixing anything.** Even a one-line fix you're certain of. It belongs to
  its wave's task, with that task's regression test.
- **Resolving architect decisions.** See `ARCHITECT-DECISIONS.md`.

## What to do

### 0. Confirm the freeze is real, first

Do not start otherwise — a moving baseline invalidates this entire task's
output.

```bash
git log --oneline -5
git status --short
git worktree list
```

Record the exact HEAD SHA you revalidate against in the `## Outcome` section
and in a new top-level `revalidated_at_commit` field in `findings.json`. If
HEAD moves mid-task, note it; findings revalidated before the move may need a
spot re-check.

### 1. Work finding-by-finding, hardest first

Process in this order — it front-loads the decisions that justify the freeze:

1. **3 critical** — `GO-PLUGIN-001`, `GO-PLUGIN-002`, `GO-SEC4-001`
2. **8 high** — `GO-AGENT-001`, `GO-AGENT-002`, `GO-PLUGIN-003`,
   `GO-RUNTIME-002`, `GO-SEC4-002`, `GO-STORE-003`, `GO-SVCEXEC-001`,
   `GO-SVCEXEC-002`
3. **33 medium**
4. **45 low + 24 informational** — these can go faster; many are
   comment/naming findings where "does the string still exist" is the whole
   check. Task `00/02` refreshes the count-bearing ones mechanically — don't
   hand-count what that task is measuring.

> **Interim report required — do not batch this to the end.** AD-01 through
> AD-04 (the Linux sandbox fail-open pair, the macOS seatbelt disclosure, and
> the plugin-install convergence shape) are **Wave 0 decisions** as of the
> operator's 2026-08-21 direction, and the operator decides them against *this
> task's* revalidated evidence rather than audit-era evidence that is 40
> commits stale. The findings behind them — `GO-PLUGIN-001/002/003` and
> `GO-SEC4-001/002/005/006` — are all in the critical/high tranche processed
> first, precisely so this can happen.
>
> **Report the critical and high results the moment they exist**, as a short
> written summary (disposition + evidence per finding), and do not wait for
> the remaining 100+ findings. Wave 1 is blocked on those four decisions, and
> those four decisions are blocked on this interim report. Note that
> `GO-SEC4-005` (AD-03) is a *low*-severity finding and therefore falls outside
> the critical/high tranche — pull it forward and revalidate it alongside them
> anyway, out of severity order, because AD-03 needs it.

For each finding, read the cited file(s) at the cited symbol(s) and answer:

- Does the code the finding describes still exist, unchanged in the relevant
  respect?
- If it changed — was it *fixed* (disposition `already-resolved`, cite the
  commit), *restructured but still defective* (`remediate`, correct the
  pointer), or *deleted/replaced* (`superseded`, say by what)?
- Is the finding's premise actually right? The audit's own
  `false_positive_considerations` field is a starting hypothesis, not a
  verdict — several findings ship with one. Where you conclude
  `false-positive`, the note must engage with the audit's specific evidence
  strings, not just assert disagreement.

### 2. Write the disposition

Set `disposition` to one of `allowed_dispositions` (already declared at the
top of `findings.json`):

`remediate | already-resolved | accepted-risk | false-positive | superseded |
defer | retire-feature | needs-architect-decision | needs-more-evidence`

Add a **new** field alongside it:

```json
"revalidation_note": "<one or two sentences: what you read, what you concluded, and the file:line or commit that supports it>"
```

Guidance on the values that are easy to misuse:

- **`needs-architect-decision`** — use it when the *disposition itself*
  depends on a pending call in `ARCHITECT-DECISIONS.md` (most of `09/`'s
  islands: whether `GO-MEM-001` is `retire-feature` or `remediate` literally
  is the architect's decision). Do not use it merely because the *fix*
  needs architect input while the finding is plainly still open — that's
  `remediate` with the task's own `requires_architect_decision` flag.
- **`already-resolved`** — requires a commit SHA or a current-source citation
  proving it. "Looks fine now" is not a disposition.
- **`accepted-risk`** — is an operator/architect call, not a worker's. If you
  believe a finding should be accepted, set `needs-architect-decision` and add
  a row to `ARCHITECT-DECISIONS.md`.
- **`needs-more-evidence`** — legitimate and expected for a handful. Say
  precisely what evidence would settle it.

### 3. Correct the task files that revalidation invalidates

Three cases, three different treatments:

- **Stale line numbers only** — amend the citation in the task's `## Context`
  and move on. Expected to be common and is not itself notable.
- **Premise partially evaporated** (2 of 3 findings fixed, 1 open) — narrow
  the task's scope in place, and note in its Context that Wave 0 narrowed it
  and why.
- **Premise fully evaporated** — add a banner immediately under the
  `**Status:**` line:

  ```markdown
  > **CLOSED BY WAVE 0 REVALIDATION (<DATE>).** <Which findings, resolved how,
  > citing commit/source.> This task is not dispatchable. Left in place rather
  > than deleted so `findings.json`'s `task_file` mapping stays intact.
  ```

  Set its `**Status:**` to `done` and the corresponding findings'
  `task_status` to `done` with `disposition: already-resolved`. **Do not
  delete the file** — `findings.json` and `FINDING-INDEX.md` both point at it
  by path.

### 4. Keep the two trackers consistent

`FINDING-INDEX.md` maps finding → task file. If (and only if) you split,
merge, or rename a task file, update `FINDING-INDEX.md` and `findings.json`'s
`task_file` in the same edit. They are cross-validated; drift between them is
the failure mode this batch's README explicitly warns about.

### 5. Write the outcome summary

Append `## Outcome (<DATE>)` to `00-revalidate-baseline/README.md`: the HEAD
SHA revalidated against, a disposition-count table, the explicit
critical/high re-confirmation, a list of every task file closed or narrowed,
and any finding you had to leave at `needs-more-evidence` with what would
settle it.

## Done means

- `findings.json` has `revalidated_at_commit` set to a real SHA, and all 113
  entries carry a `revalidation_note` plus a `disposition` that is a decision
  rather than the inherited placeholder.
- A programmatic check passes: no entry is missing `revalidation_note`; every
  `disposition` is in `allowed_dispositions`; every `task_file` path resolves
  to a file that exists on disk.

  ```bash
  python3 - <<'EOF'
  import json, os, collections
  d = json.load(open('TASKS/audit-remediation/findings.json'))
  f = d['findings']
  assert d.get('revalidated_at_commit'), 'revalidated_at_commit not set'
  missing = [x['id'] for x in f if not x.get('revalidation_note')]
  bad = [x['id'] for x in f if x['disposition'] not in d['allowed_dispositions']]
  gone = [x['task_file'] for x in f if not os.path.exists(x['task_file'])]
  print('no note:', missing); print('bad disposition:', bad); print('missing task file:', sorted(set(gone)))
  print(collections.Counter(x['disposition'] for x in f))
  assert not (missing or bad or gone)
  EOF
  ```

- Each of the 3 critical and 8 high findings has a `revalidation_note` citing
  a specific current-source location or a specific fixing commit — not a
  generic "still present."
- **The interim critical/high report was delivered to the operator before the
  full sweep finished**, and included `GO-SEC4-005` pulled forward out of
  severity order. AD-01 through AD-04 cannot be decided without it, and Wave 1
  cannot dispatch without those decisions.
- Every task file closed or narrowed by this pass says so in its own text; no
  task file remains that looks dispatchable but isn't.
- `00-revalidate-baseline/README.md` has a real `## Outcome (<DATE>)` section.
- `docs/audits/2026-08-21-go-quality/findings.json` is **unchanged** — verify
  with `git status` before finishing.

## Work log

## Review notes
